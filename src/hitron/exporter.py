"""Unified exporter for every Hitron device this project knows about.

One process can poll any number of HT-EM5 MoCA bridges, CODA6X gateways, and
CODA-57 legacy gateways, then expose the result as:

* Prometheus text on ``/metrics`` (default),
* Influx line protocol for Telegraf (``--once --format telegraf``),
* OTLP/HTTP JSON pushed to an OpenTelemetry Collector
  (``--otlp-endpoint http://collector:4318/v1/metrics``),
* plain JSON for debugging (``--once --format json``).

It also serves ``/healthz``, ``/api/v1/samples``, and ``/api/v1/logs`` so a
static dashboard or a human can see what the last scrape did without a
Prometheus round-trip.

Usage::

    hitron-exporter --port 9877 \\
        --ht-em5 192.168.0.212 --ht-em5 192.168.0.241 \\
        --coda60 192.168.100.1 \\
        --coda57 192.168.100.2

    hitron-exporter --coda60 192.168.100.1 --once --format telegraf

Targets accept ``name=host`` when the label should not be the raw address::

    --coda60 edge=192.168.100.1
"""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
import os
import threading
import time
from collections import deque
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

import aiohttp

from .coda57 import Coda57Client
from .coda60 import Coda60Client
from .formats import (
    render_influx,
    render_json,
    render_otlp_json,
    render_prometheus,
    render_telegraf,
)
from .grafana import write_bundle
from .ht_em5 import HitronMocaClient
from .metrics import Sample, SpecRange, coda57_samples, coda60_samples, dedupe, ht_em5_samples

log = logging.getLogger("hitron.exporter")
VERSION = "0.2.0"


# ---------------------------------------------------------------------------
# recent-log ring, exposed over /api/v1/logs
# ---------------------------------------------------------------------------


class RingHandler(logging.Handler):
    def __init__(self, ring: deque[dict[str, Any]], capacity: int = 500):
        super().__init__()
        self.ring = ring
        self.setFormatter(logging.Formatter("%(message)s"))

    def emit(self, record: logging.LogRecord) -> None:
        try:
            self.ring.append(
                {
                    "ts": record.created,
                    "time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(record.created)),
                    "level": record.levelname,
                    "logger": record.name,
                    "message": self.format(record),
                }
            )
        except Exception:  # noqa: BLE001 - logging must never break polling
            pass


# ---------------------------------------------------------------------------
# collection
# ---------------------------------------------------------------------------


@dataclass
class Target:
    name: str
    host: str


def parse_target(value: str) -> Target:
    """Accept ``host`` or ``name=host``."""
    if "=" in value:
        name, host = value.split("=", 1)
        if name and host:
            return Target(name=name.strip(), host=host.strip())
    return Target(name=value.strip(), host=value.strip())


@dataclass
class Config:
    ht_em5: list[Target] = field(default_factory=list)
    coda60: list[Target] = field(default_factory=list)
    coda57: list[Target] = field(default_factory=list)
    interval: float = 30.0
    phy_rates: bool = False
    spec: SpecRange = field(default_factory=SpecRange)


@dataclass
class Snapshot:
    samples: list[Sample] = field(default_factory=list)
    errors: list[dict[str, str]] = field(default_factory=list)
    sources: dict[str, dict[str, Any]] = field(default_factory=dict)
    generated_at: float = 0.0
    duration: float = 0.0


class Collector:
    def __init__(self, config: Config):
        self.config = config

    async def collect(self) -> Snapshot:
        started = time.monotonic()
        tasks: list[asyncio.Task] = []
        keys: list[tuple[str, str]] = []

        for target in self.config.coda60:
            tasks.append(asyncio.create_task(self._coda60(target)))
            keys.append(("coda60", target.name))
        for target in self.config.coda57:
            tasks.append(asyncio.create_task(self._coda57(target)))
            keys.append(("coda57", target.name))
        for target in self.config.ht_em5:
            tasks.append(asyncio.create_task(self._ht_em5(target)))
            keys.append(("ht-em5", target.name))

        results = await asyncio.gather(*tasks, return_exceptions=True)
        samples: list[Sample] = []
        errors: list[dict[str, str]] = []
        sources: dict[str, dict[str, Any]] = {}

        for (kind, name), result in zip(keys, results):
            if isinstance(result, BaseException):
                errors.append({"source": name, "kind": kind, "error": str(result)})
                sources[name] = {"kind": kind, "up": False, "samples": 0, "error": str(result)}
                log.warning("poll of %s (%s) failed: %s", name, kind, result)
                continue
            device_samples, error = result
            if error:
                errors.append({"source": name, "kind": kind, "error": error})
                sources[name] = {"kind": kind, "up": False, "samples": 0, "error": error}
                log.warning("poll of %s (%s) failed: %s", name, kind, error)
                continue
            samples.extend(device_samples)
            sources[name] = {"kind": kind, "up": True, "samples": len(device_samples), "error": ""}

        duration = time.monotonic() - started
        generated = time.time()
        samples = dedupe(samples)
        samples.extend(self._health_samples(sources, duration, generated))
        return Snapshot(samples=samples, errors=errors, sources=sources, generated_at=generated, duration=duration)

    def _health_samples(self, sources: dict[str, dict[str, Any]], duration: float, generated: float) -> list[Sample]:
        out = [Sample("hitron_exporter_info", 1.0, {"version": VERSION}, "Exporter build information")]
        out.append(Sample("hitron_exporter_last_scrape_duration_seconds", duration, {}, "Duration of the whole collection cycle", unit="s"))
        out.append(Sample("hitron_exporter_last_scrape_timestamp_seconds", generated, {}, "Unix time of the last completed collection", unit="s"))
        for name, info in sources.items():
            labels = {"source": name, "kind": info["kind"]}
            out.append(Sample("hitron_exporter_up", 1.0 if info["up"] else 0.0, labels, "1 when the source was polled successfully"))
            out.append(Sample("hitron_exporter_scrape_duration_seconds", duration, labels, "Collection duration for this source", unit="s"))
            out.append(Sample("hitron_exporter_samples", float(info["samples"]), labels, "Samples produced by this source"))
            out.append(Sample("hitron_exporter_last_scrape_timestamp_seconds", generated, labels, "Unix time of the last successful poll", unit="s"))
            if info.get("error"):
                out.append(Sample("hitron_exporter_scrape_error_info", 1.0, {**labels, "error": info["error"][:200]}, "Present only while a source is failing"))
        return out

    async def _coda60(self, target: Target) -> tuple[list[Sample], str]:
        try:
            snapshot = await Coda60Client(target.host).get_all()
            return coda60_samples(snapshot, source=target.name), ""
        except Exception as exc:  # noqa: BLE001 - one bad device must not stop the rest
            return [], str(exc)

    async def _coda57(self, target: Target) -> tuple[list[Sample], str]:
        try:
            data = await Coda57Client(target.host).get_all()
            return coda57_samples(data, source=target.name, spec=self.config.spec), ""
        except Exception as exc:  # noqa: BLE001
            return [], str(exc)

    async def _ht_em5(self, target: Target) -> tuple[list[Sample], str]:
        try:
            client = HitronMocaClient(target.host)
            async with client:
                snapshot = await client.get_all()
                phy = await client.get_phy_rates() if self.config.phy_rates else None
            return ht_em5_samples(snapshot, host=target.name, phy_rates=phy), ""
        except Exception as exc:  # noqa: BLE001
            return [], str(exc)


# ---------------------------------------------------------------------------
# HTTP surface
# ---------------------------------------------------------------------------


class ExporterState:
    def __init__(self) -> None:
        self.lock = threading.Lock()
        self.snapshot = Snapshot()
        self.logs: deque[dict[str, Any]] = deque(maxlen=500)

    def set(self, snapshot: Snapshot) -> None:
        with self.lock:
            self.snapshot = snapshot

    def get(self) -> Snapshot:
        with self.lock:
            return self.snapshot

    def log_items(self, limit: int = 200) -> list[dict[str, Any]]:
        with self.lock:
            return list(self.logs)[-limit:]


def _handler_factory(state: ExporterState):
    class Handler(BaseHTTPRequestHandler):
        server_version = f"hitron-exporter/{VERSION}"

        def log_message(self, fmt: str, *args: Any) -> None:  # noqa: A003 - stdlib signature
            log.debug("%s - %s", self.address_string(), fmt % args)

        def _send(self, code: int, body: bytes, content_type: str) -> None:
            self.send_response(code)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "no-store")
            self.send_header("Access-Control-Allow-Origin", "*")
            self.end_headers()
            self.wfile.write(body)

        def do_OPTIONS(self) -> None:  # noqa: N802
            self.send_response(204)
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Access-Control-Allow-Methods", "GET, OPTIONS")
            self.send_header("Access-Control-Allow-Headers", "*")
            self.end_headers()

        def do_GET(self) -> None:  # noqa: N802
            snapshot = state.get()
            path = self.path.split("?", 1)[0]
            if path == "/metrics":
                body = render_prometheus(snapshot.samples, int(snapshot.generated_at * 1000) if snapshot.generated_at else None)
                self._send(200, body.encode(), "text/plain; version=0.0.4; charset=utf-8")
            elif path == "/healthz":
                payload = {
                    "status": "ok" if snapshot.generated_at and all(s.get("up") for s in snapshot.sources.values()) else "degraded",
                    "version": VERSION,
                    "generated_at": snapshot.generated_at,
                    "duration_seconds": snapshot.duration,
                    "samples": len(snapshot.samples),
                    "sources": snapshot.sources,
                    "errors": snapshot.errors,
                }
                self._send(200, json.dumps(payload, indent=2).encode(), "application/json")
            elif path == "/api/v1/samples":
                self._send(200, render_json(snapshot.samples).encode(), "application/json")
            elif path == "/api/v1/logs":
                self._send(200, json.dumps(state.log_items(), indent=2).encode(), "application/json")
            elif path in ("/", "/index.html"):
                body = (
                    "<!doctype html><meta charset=utf-8><title>hitron-exporter</title>"
                    "<style>body{font:16px system-ui;max-width:44rem;margin:3rem auto;padding:0 1rem}"
                    "code{background:#eee;padding:.1rem .3rem;border-radius:3px}</style>"
                    f"<h1>hitron-exporter {VERSION}</h1>"
                    "<p>Endpoints: <code>/metrics</code>, <code>/healthz</code>, "
                    "<code>/api/v1/samples</code>, <code>/api/v1/logs</code>.</p>"
                    f"<p>{len(snapshot.samples)} samples from {len(snapshot.sources)} source(s), "
                    f"last completed {time.strftime('%Y-%m-%d %H:%M:%SZ', time.gmtime(snapshot.generated_at)) if snapshot.generated_at else 'never'}.</p>"
                )
                self._send(200, body.encode(), "text/html; charset=utf-8")
            else:
                self._send(404, b'{"error":"not found"}\n', "application/json")

    return Handler


def start_server(state: ExporterState, listen: str, port: int) -> ThreadingHTTPServer:
    server = ThreadingHTTPServer((listen, port), _handler_factory(state))
    thread = threading.Thread(target=server.serve_forever, name="hitron-http", daemon=True)
    thread.start()
    return server


# ---------------------------------------------------------------------------
# OTLP push
# ---------------------------------------------------------------------------


def _parse_headers(values: list[str]) -> dict[str, str]:
    headers = {"Content-Type": "application/json"}
    for value in values:
        if "=" in value:
            key, val = value.split("=", 1)
            headers[key.strip()] = val.strip()
    return headers


async def push_otlp(endpoint: str, samples: list[Sample], headers: dict[str, str]) -> None:
    payload = render_otlp_json(samples, service_version=VERSION)
    try:
        async with aiohttp.ClientSession() as session:
            async with session.post(endpoint, data=payload, headers=headers, timeout=aiohttp.ClientTimeout(total=15)) as resp:
                if resp.status >= 300:
                    body = (await resp.text())[:300]
                    log.warning("OTLP push to %s returned %s: %s", endpoint, resp.status, body)
                else:
                    log.debug("OTLP push to %s accepted %d samples", endpoint, len(samples))
    except Exception as exc:  # noqa: BLE001 - telemetry export failure must not stop Prometheus
        log.warning("OTLP push to %s failed: %s", endpoint, exc)


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def _split_env(name: str) -> list[str]:
    return [item for item in os.environ.get(name, "").split(",") if item.strip()]


def build_config(args: argparse.Namespace) -> Config:
    ht = args.ht_em5 or _split_env("HITRON_HT_EM5_HOSTS")
    c60 = args.coda60 or _split_env("HITRON_CODA60_HOSTS") or _split_env("HITRON_CODA60_HOST")
    c57 = args.coda57 or _split_env("HITRON_CODA57_HOSTS") or _split_env("HITRON_CODA57_HOST")
    return Config(
        ht_em5=[parse_target(x) for x in ht],
        coda60=[parse_target(x) for x in c60],
        coda57=[parse_target(x) for x in c57],
        interval=args.interval,
        phy_rates=args.phy_rates,
        spec=SpecRange(args.ds_spec_min, args.ds_spec_max, args.us_spec_min, args.us_spec_max),
    )


async def run(config: Config, args: argparse.Namespace, state: ExporterState) -> None:
    collector = Collector(config)
    otlp_headers = _parse_headers(args.otlp_header)
    while True:
        started = time.monotonic()
        snapshot = await collector.collect()
        state.set(snapshot)
        log.info(
            "collected %d samples from %d source(s) in %.2fs (%d error(s))",
            len(snapshot.samples), len(snapshot.sources), snapshot.duration, len(snapshot.errors),
        )
        if args.otlp_endpoint:
            await push_otlp(args.otlp_endpoint, snapshot.samples, otlp_headers)
        await asyncio.sleep(max(0.5, config.interval - (time.monotonic() - started)))


def _render_once(snapshot: Snapshot, fmt: str) -> str:
    if fmt in ("telegraf", "influx"):
        return render_telegraf(snapshot.samples)
    if fmt == "json":
        return render_json(snapshot.samples)
    if fmt == "otlp":
        return render_otlp_json(snapshot.samples, service_version=VERSION)
    return render_prometheus(snapshot.samples, int(snapshot.generated_at * 1000))


def main(argv: list[str] | None = None) -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--port", type=int, default=int(os.environ.get("HITRON_EXPORTER_PORT", "9877")))
    ap.add_argument("--listen", default=os.environ.get("HITRON_EXPORTER_LISTEN", "0.0.0.0"))
    ap.add_argument("--interval", type=float, default=float(os.environ.get("HITRON_EXPORTER_INTERVAL", "30")))
    ap.add_argument("--ht-em5", action="append", default=[], metavar="[NAME=]HOST", help="HT-EM5 bridge; repeatable")
    ap.add_argument("--coda60", action="append", default=[], metavar="[NAME=]HOST", help="CODA6X gateway; repeatable")
    ap.add_argument("--coda57", action="append", default=[], metavar="[NAME=]HOST", help="CODA-57 legacy gateway; repeatable")
    ap.add_argument("--phy-rates", action="store_true", help="also collect the HT-EM5 PHY-rate matrix (slow)")
    ap.add_argument("--ds-spec-min", type=float, default=float(os.environ.get("HITRON_DS_SPEC_MIN", "-6")))
    ap.add_argument("--ds-spec-max", type=float, default=float(os.environ.get("HITRON_DS_SPEC_MAX", "15")))
    ap.add_argument("--us-spec-min", type=float, default=float(os.environ.get("HITRON_US_SPEC_MIN", "35")))
    ap.add_argument("--us-spec-max", type=float, default=float(os.environ.get("HITRON_US_SPEC_MAX", "48")))
    ap.add_argument("--otlp-endpoint", default=os.environ.get("HITRON_OTLP_ENDPOINT") or None, help="OTLP/HTTP metrics URL, e.g. http://collector:4318/v1/metrics")
    ap.add_argument("--otlp-header", action="append", default=_split_env("HITRON_OTLP_HEADERS"), metavar="KEY=VALUE", help="extra OTLP header; repeatable")
    ap.add_argument("--grafana-dir", default=os.environ.get("HITRON_GRAFANA_DIR") or None, help="write the Grafana/Prometheus bundle here and continue")
    ap.add_argument("--once", action="store_true", help="collect once, print to stdout, exit")
    ap.add_argument("--format", choices=["prometheus", "telegraf", "influx", "otlp", "json"], default="prometheus", help="output format for --once")
    args = ap.parse_args(argv)

    config = build_config(args)
    if not (config.ht_em5 or config.coda60 or config.coda57):
        ap.error("give at least one of --ht-em5 / --coda60 / --coda57 (or the matching env var)")

    if args.grafana_dir:
        written = write_bundle(args.grafana_dir)
        log.info("wrote %d Grafana/Prometheus files under %s", len(written), args.grafana_dir)

    state = ExporterState()
    handler = RingHandler(state.logs, capacity=500)
    handler.setLevel(logging.INFO)
    logging.getLogger().addHandler(handler)

    if args.once:
        snapshot = asyncio.run(Collector(config).collect())
        print(_render_once(snapshot, args.format), end="")
        if snapshot.errors:
            for error in snapshot.errors:
                log.warning("source %s: %s", error["source"], error["error"])
            raise SystemExit(1)
        return

    log.info("serving /metrics on %s:%d, polling every %ss", args.listen, args.port, config.interval)
    log.info(
        "HT-EM5: %s | CODA6X: %s | CODA-57: %s | OTLP: %s",
        [t.name for t in config.ht_em5] or "(none)",
        [t.name for t in config.coda60] or "(none)",
        [t.name for t in config.coda57] or "(none)",
        args.otlp_endpoint or "(disabled)",
    )

    server = start_server(state, args.listen, args.port)
    try:
        asyncio.run(run(config, args, state))
    except KeyboardInterrupt:
        pass
    finally:
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    main()
