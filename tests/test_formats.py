"""Output encoders: Prometheus, Influx/Telegraf, OTLP/HTTP JSON, and JSON."""

import json

import pytest

from hitron.formats import (
    otlp_payload,
    render_influx,
    render_json,
    render_otlp_json,
    render_prometheus,
    render_telegraf,
)
from hitron.metrics import Sample

SAMPLES = [
    Sample("modem_downstream_channel_power_dbmv", 8.098, {"source": "lab", "channel_id": "1"}, "Downstream power", unit="dBmV"),
    Sample("modem_downstream_channel_power_dbmv", -1.5, {"source": "lab", "channel_id": "2"}, "Downstream power", unit="dBmV"),
    Sample("modem_downstream_channel_uncorrectable_total", 1203911172, {"source": "lab", "channel_id": "1"}, "Uncorrectable", kind="counter"),
    Sample("hitron_moca_link_up", 1.0, {"host": "attic"}, "MoCA link"),
]


def test_prometheus_has_help_type_and_sorted_labels():
    text = render_prometheus(SAMPLES, timestamp_ms=1700000000000)
    assert "# TYPE modem_downstream_channel_power_dbmv gauge" in text
    assert "# TYPE modem_downstream_channel_uncorrectable_total counter" in text
    assert 'modem_downstream_channel_power_dbmv{channel_id="1",source="lab"} 8.098 1700000000000' in text
    # One HELP/TYPE per metric family, not per series.
    assert text.count("# HELP modem_downstream_channel_power_dbmv") == 1


def test_prometheus_escapes_label_values():
    text = render_prometheus([Sample("x", 1.0, {"error": 'bad "quote"\\path'})])
    assert 'error="bad \\"quote\\"\\\\path"' in text


def test_prometheus_drops_non_finite_values():
    text = render_prometheus([Sample("x", float("nan")), Sample("y", float("inf"))])
    assert text == ""


def test_influx_counter_is_integer_and_tags_escaped():
    text = render_influx([Sample("m", 5.0, {"a": "b c"}, kind="counter")], timestamp_ns=1)
    assert "m,a=b\\ c value=5i 1" in text


def test_influx_measurement_escapes_commas():
    text = render_influx([Sample("weird,name", 1.0, {})], timestamp_ns=1)
    assert text.startswith("weird\\,name value=1.0 1")


def test_telegraf_is_influx():
    assert render_telegraf(SAMPLES, timestamp_ns=1) == render_influx(SAMPLES, timestamp_ns=1)


def test_otlp_payload_shape():
    payload = otlp_payload(SAMPLES, service_name="hitron", time_ns=42)
    assert "resourceMetrics" in payload
    scope = payload["resourceMetrics"][0]["scopeMetrics"][0]
    metrics = {m["name"]: m for m in scope["metrics"]}
    assert "gauge" in metrics["modem_downstream_channel_power_dbmv"]
    assert metrics["modem_downstream_channel_uncorrectable_total"]["sum"]["isMonotonic"] is True
    assert metrics["modem_downstream_channel_uncorrectable_total"]["sum"]["aggregationTemporality"] == 2
    point = metrics["modem_downstream_channel_power_dbmv"]["gauge"]["dataPoints"][0]
    assert point["timeUnixNano"] == "42"
    attrs = {a["key"]: a["value"]["stringValue"] for a in point["attributes"]}
    assert attrs["source"] == "lab"
    resource = {a["key"]: a["value"]["stringValue"] for a in payload["resourceMetrics"][0]["resource"]["attributes"]}
    assert resource["service.name"] == "hitron"


def test_otlp_json_is_valid_json():
    payload = json.loads(render_otlp_json(SAMPLES, time_ns=42))
    assert payload["resourceMetrics"][0]["scopeMetrics"][0]["metrics"]


def test_json_renderer_round_trips():
    payload = json.loads(render_json(SAMPLES))
    assert len(payload) == len(SAMPLES)
    assert payload[0]["name"] == "modem_downstream_channel_power_dbmv"
    assert payload[0]["unit"] == "dBmV"


@pytest.mark.parametrize("renderer", [render_prometheus, render_influx, render_json])
def test_empty_input_never_crashes(renderer):
    assert renderer([]) in ("", "[]\n")
