"""Render :class:`hitron.metrics.Sample` objects in every wire format the
project promises: Prometheus text, Influx line protocol (Telegraf), OTLP/HTTP
JSON, and plain JSON.

No third-party dependency is required beyond the standard library, which keeps
the exporter usable on a router-side box where installing ``protobuf`` is
awkward. OTLP JSON is one of the two encodings the OpenTelemetry Collector
accepts on ``/v1/metrics``.
"""

from __future__ import annotations

import json
import math
import time
from collections import OrderedDict
from typing import Any, Iterable

from .metrics import Sample

__all__ = [
    "render_prometheus",
    "render_influx",
    "render_telegraf",
    "otlp_payload",
    "render_otlp_json",
    "render_json",
]

SERVICE_NAME = "hitron-exporter"
SCOPE_NAME = "github.com/Netquor/hitron"


def _fmt(value: float) -> str:
    if value == int(value) and abs(value) < 1e15:
        return str(int(value))
    return format(value, ".12g")


# ---------------------------------------------------------------------------
# Prometheus
# ---------------------------------------------------------------------------


def _prom_escape(value: str) -> str:
    return value.replace("\\", "\\\\").replace("\n", "\\n").replace('"', '\\"')


def render_prometheus(samples: Iterable[Sample], timestamp_ms: int | None = None) -> str:
    """Render the Prometheus text exposition format (version 0.0.4)."""
    grouped: "OrderedDict[str, list[Sample]]" = OrderedDict()
    for sample in samples:
        if not math.isfinite(sample.value):
            continue
        grouped.setdefault(sample.name, []).append(sample)

    lines: list[str] = []
    for name, group in grouped.items():
        first = group[0]
        help_text = next((s.help for s in group if s.help), name)
        lines.append(f"# HELP {name} {help_text.replace(chr(10), ' ')}")
        lines.append(f"# TYPE {name} {'counter' if first.kind == 'counter' else 'gauge'}")
        for sample in group:
            labels = ",".join(
                f'{key}="{_prom_escape(str(value))}"' for key, value in sorted(sample.labels.items())
            )
            suffix = f"{{{labels}}}" if labels else ""
            ts = f" {timestamp_ms}" if timestamp_ms is not None else ""
            lines.append(f"{name}{suffix} {_fmt(sample.value)}{ts}")
    return "\n".join(lines) + ("\n" if lines else "")


# ---------------------------------------------------------------------------
# Influx line protocol / Telegraf
# ---------------------------------------------------------------------------


def _influx_escape(value: str, *, tag: bool = False) -> str:
    value = str(value).replace("\\", "\\\\").replace("\n", "\\n")
    value = value.replace(",", "\\,").replace(" ", "\\ ")
    if tag:
        value = value.replace("=", "\\=")
    return value


def render_influx(samples: Iterable[Sample], timestamp_ns: int | None = None) -> str:
    """Render InfluxDB line protocol, which is what Telegraf's ``inputs.exec``
    consumes with ``data_format = "influx"``."""
    ts = timestamp_ns if timestamp_ns is not None else time.time_ns()
    lines: list[str] = []
    for sample in samples:
        if not math.isfinite(sample.value):
            continue
        measurement = _influx_escape(sample.name)
        tags = "".join(
            f",{_influx_escape(k, tag=True)}={_influx_escape(v, tag=True)}"
            for k, v in sorted(sample.labels.items())
        )
        if sample.kind == "counter":
            field = f"value={int(sample.value)}i"
        else:
            field = f"value={repr(float(sample.value))}"
        lines.append(f"{measurement}{tags} {field} {ts}")
    return "\n".join(lines) + ("\n" if lines else "")


def render_telegraf(samples: Iterable[Sample], timestamp_ns: int | None = None) -> str:
    """Alias for :func:`render_influx`, named for the consumer."""
    return render_influx(samples, timestamp_ns)


# ---------------------------------------------------------------------------
# OpenTelemetry OTLP/HTTP JSON
# ---------------------------------------------------------------------------


def _otlp_attr(key: str, value: str) -> dict[str, Any]:
    return {"key": key, "value": {"stringValue": str(value)}}


def _otlp_datapoint(sample: Sample, time_ns: int) -> dict[str, Any]:
    point: dict[str, Any] = {"timeUnixNano": str(time_ns)}
    if sample.labels:
        point["attributes"] = [_otlp_attr(k, v) for k, v in sorted(sample.labels.items())]
    if sample.kind == "counter":
        point["asInt"] = str(int(sample.value))
    else:
        point["asDouble"] = float(sample.value)
    return point


def otlp_payload(
    samples: Iterable[Sample],
    *,
    service_name: str = SERVICE_NAME,
    service_version: str = "0.2.0",
    extra_resource: dict[str, str] | None = None,
    time_ns: int | None = None,
) -> dict[str, Any]:
    """Build an OTLP ``ExportMetricsServiceRequest`` as JSON."""
    ts = time_ns if time_ns is not None else time.time_ns()
    grouped: "OrderedDict[str, list[Sample]]" = OrderedDict()
    for sample in samples:
        if math.isfinite(sample.value):
            grouped.setdefault(sample.name, []).append(sample)

    metrics: list[dict[str, Any]] = []
    for name, group in grouped.items():
        first = group[0]
        points = [_otlp_datapoint(s, ts) for s in group]
        metric: dict[str, Any] = {"name": name}
        if first.help:
            metric["description"] = first.help
        if first.unit:
            metric["unit"] = first.unit
        if first.kind == "counter":
            metric["sum"] = {"dataPoints": points, "aggregationTemporality": 2, "isMonotonic": True}
        else:
            metric["gauge"] = {"dataPoints": points}
        metrics.append(metric)

    resource_attrs = [
        _otlp_attr("service.name", service_name),
        _otlp_attr("service.version", service_version),
        _otlp_attr("telemetry.sdk.name", "hitron"),
        _otlp_attr("telemetry.sdk.language", "python"),
    ]
    for key, value in sorted((extra_resource or {}).items()):
        resource_attrs.append(_otlp_attr(key, value))

    return {
        "resourceMetrics": [
            {
                "resource": {"attributes": resource_attrs},
                "scopeMetrics": [
                    {
                        "scope": {"name": SCOPE_NAME, "version": service_version},
                        "metrics": metrics,
                    }
                ],
            }
        ]
    }


def render_otlp_json(samples: Iterable[Sample], **kwargs: Any) -> str:
    return json.dumps(otlp_payload(samples, **kwargs), separators=(",", ":"))


# ---------------------------------------------------------------------------
# Debug JSON
# ---------------------------------------------------------------------------


def render_json(samples: Iterable[Sample]) -> str:
    payload = [
        {
            "name": s.name,
            "value": s.value,
            "labels": s.labels,
            "type": s.kind,
            "unit": s.unit,
            "help": s.help,
        }
        for s in samples
        if math.isfinite(s.value)
    ]
    return json.dumps(payload, indent=2, sort_keys=True) + "\n"
