"""
Unified Prometheus exporter for every Hitron device this project knows about:
any number of HT-EM5 MoCA bridges, a CODA6X gateway (REST API), and/or a
CODA-57 (legacy .asp API). One process, one `/metrics` port.

Metric names for the CODA6X/CODA-57 downstream/upstream series intentionally
match `~/Documents/hitron-client/CODA_60_rpt.py`'s existing `modem_*` names
(the ones `aggregator/prometheus.yml`'s `hitron_modem` job - port 9877 -
already scrapes), so swapping this exporter in for that script needs no
Prometheus config change. New series (MoCA, per-device labeling) use a
`hitron_` prefix.

Usage:
    hitron-exporter --port 9877 \\
        --ht-em5 192.168.0.212 --ht-em5 192.168.0.241 --ht-em5 192.168.0.60 \\
        --coda60 192.168.100.1

Or purely env-driven (handy under systemd/Docker) - see README.md.
"""

from __future__ import annotations

import argparse
import asyncio
import logging
import os
import re

from prometheus_client import Gauge, start_http_server

from .coda57 import Coda57Client
from .coda60 import Coda60Client
from .ht_em5 import HitronMocaClient
from .models import ModelProfile, get_profile

log = logging.getLogger("hitron.exporter")

# Per-host capability profile, resolved once and reused for every scrape.
_CODA60_PROFILES: dict[str, ModelProfile] = {}

# ---------------------------------------------------------------------------
# CODA6X / CODA-57 gauges - names match CODA_60_rpt.py exactly where that
# script already defines the series, so existing dashboards keep working.
# ---------------------------------------------------------------------------

modem_uptime_seconds = Gauge("modem_system_uptime_seconds", "Modem system uptime", ["source"])
ds_signal = Gauge("modem_downstream_signal_strength_dbmv", "Downstream signal strength", ["source", "channel_id"])
ds_snr = Gauge("modem_downstream_snr_db", "Downstream SNR", ["source", "channel_id"])
ds_corrected = Gauge("modem_downstream_corrected_total", "Downstream corrected codewords", ["source", "channel_id"])
ds_uncorrectable = Gauge("modem_downstream_uncorrectable_total", "Downstream uncorrectable codewords", ["source", "channel_id"])
us_signal = Gauge("modem_upstream_signal_strength_dbmv", "Upstream signal strength", ["source", "channel_id"])
ds_ofdm_snr = Gauge("modem_downstream_ofdm_snr_db", "OFDM downstream SNR", ["source", "index"])
ds_ofdm_plc_locked = Gauge("modem_downstream_ofdm_plc_locked", "OFDM PLC lock status (1=locked)", ["source", "index"])
ds_ofdm_plc_power = Gauge("modem_downstream_ofdm_plc_power_dbmv", "OFDM PLC power", ["source", "index"])
ds_ofdm_corrected = Gauge("modem_downstream_ofdm_corrected_total", "OFDM corrected codewords", ["source", "index"])
ds_ofdm_uncorrectable = Gauge("modem_downstream_ofdm_uncorrectable_total", "OFDM uncorrectable codewords", ["source", "index"])
us_ofdm_power = Gauge("modem_upstream_ofdm_reported_power_dbmv", "OFDMA reported power", ["source", "uschindex"])
us_ofdm_power_1_6 = Gauge("modem_upstream_ofdm_reported_power_1_6_dbmv", "OFDMA reported power, 1.6MHz-equivalent", ["source", "uschindex"])
us_ofdm_state = Gauge("modem_upstream_ofdm_operating", "OFDMA channel operating (1=OPERATE)", ["source", "uschindex"])
provisioning_step = Gauge("modem_docsis_provisioning_step_success", "1 if provisioning step succeeded", ["source", "step"])
spec_in_range = Gauge("modem_power_in_spec", "1 if live DS/US power is inside the carrier's stated spec (CODA-57 legacy source only)", ["source", "direction"])

# ---------------------------------------------------------------------------
# HT-EM5 / MoCA gauges - new series, hitron_ prefix.
# ---------------------------------------------------------------------------

moca_link_up = Gauge("hitron_moca_link_up", "1 if this bridge reports its MoCA link up", ["host"])
moca_node_count = Gauge("hitron_moca_node_count", "Number of MoCA nodes this bridge currently sees on the coax", ["host"])
moca_eth_frames = Gauge("hitron_moca_ethernet_frames_total", "Ethernet frame counters on the bridge's LAN side", ["host", "direction", "outcome"])
moca_tx_power_index = Gauge("hitron_moca_tx_power_index", "Configured TX power index", ["host"])
moca_beacon_power_index = Gauge("hitron_moca_beacon_power_index", "Configured beacon power level index", ["host"])
moca_node_present = Gauge("hitron_moca_node_present", "1 for each node id this bridge currently sees (is_self/is_nc as labels)", ["host", "node_id", "mac_address", "is_self", "is_network_coordinator"])


def _step_ok(value: str | None) -> int:
    return 1 if (value or "").strip().lower() == "success" else 0


async def poll_coda60(client: Coda60Client, source: str, model: str | None = None) -> None:
    # Model detection costs ~10 GETs, so do it once per host, not per scrape.
    # A wrong capability profile only changes which endpoints are polled, so
    # caching it for the process lifetime is safe; a restart re-detects.
    cached = _CODA60_PROFILES.get(client.host)
    if cached is None:
        match = await client.detect_profile(model)
        _CODA60_PROFILES[client.host] = match.profile
        log.info("coda60 %s: model profile %s (matched by %s)", client.host, match.profile.id, match.matched_by)
        profile = match.profile
    else:
        profile = cached
    data = (await client.get_all(profile=profile))["metrics"]

    sysinfo = data.get("router_sysinfo", {})
    # parse "123h:45m:06s"-shaped uptime if present, else leave unset
    m = re.match(r"(\d+)h:(\d+)m:(\d+)s", sysinfo.get("systemUptime") or "")
    if m:
        h, mnt, sec = (int(x) for x in m.groups())
        modem_uptime_seconds.labels(source=source).set(h * 3600 + mnt * 60 + sec)

    prov = data.get("cm_docsisprovision", {})
    for step in ("hwInit", "findDownstream", "ranging", "dhcp", "timeOfday", "downloadCfg", "registration"):
        if step in prov:
            provisioning_step.labels(source=source, step=step).set(_step_ok(prov.get(step)))

    for ch in data.get("cm_dsinfo", {}).get("Freq_List", []):
        cid = ch.get("channelId")
        if cid is None:
            continue
        if (v := _f(ch.get("signalStrength"))) is not None:
            ds_signal.labels(source=source, channel_id=cid).set(v)
        if (v := _f(ch.get("snr"))) is not None:
            ds_snr.labels(source=source, channel_id=cid).set(v)
        if (v := _f(ch.get("correcteds"))) is not None:
            ds_corrected.labels(source=source, channel_id=cid).set(v)
        if (v := _f(ch.get("uncorrect"))) is not None:
            ds_uncorrectable.labels(source=source, channel_id=cid).set(v)

    for ch in data.get("cm_usinfo", {}).get("Freq_List", []):
        cid = ch.get("channelId")
        if cid is not None and (v := _f(ch.get("signalStrength"))) is not None:
            us_signal.labels(source=source, channel_id=cid).set(v)

    for ch in data.get("cm_dsofdm", {}).get("Freq_List", []):
        idx = ch.get("index")
        if idx is None:
            continue
        if (v := _f(ch.get("SNR"))) is not None:
            ds_ofdm_snr.labels(source=source, index=idx).set(v)
        if (v := _f(ch.get("plcpower"))) is not None:
            ds_ofdm_plc_power.labels(source=source, index=idx).set(v)
        ds_ofdm_plc_locked.labels(source=source, index=idx).set(1 if ch.get("plclock") == "YES" else 0)
        if (v := _f(ch.get("correcteds"))) is not None:
            ds_ofdm_corrected.labels(source=source, index=idx).set(v)
        if (v := _f(ch.get("uncorrect"))) is not None:
            ds_ofdm_uncorrectable.labels(source=source, index=idx).set(v)

    for ch in data.get("cm_usofdm", {}).get("Freq_List", []):
        idx = ch.get("uschindex")
        if idx is None:
            continue
        if (v := _f(ch.get("repPower"))) is not None:
            us_ofdm_power.labels(source=source, uschindex=idx).set(v)
        if (v := _f(ch.get("repPower1_6"))) is not None:
            us_ofdm_power_1_6.labels(source=source, uschindex=idx).set(v)
        us_ofdm_state.labels(source=source, uschindex=idx).set(1 if ch.get("state") == "OPERATE" else 0)


async def poll_coda57(client: Coda57Client, source: str) -> None:
    data = await client.get_all()

    for ch in data.get("dsinfo", []) or []:
        cid = ch.get("channelId")
        if cid is None:
            continue
        if (v := _f(ch.get("signalStrength"))) is not None:
            ds_signal.labels(source=source, channel_id=cid).set(v)
        if (v := _f(ch.get("snr"))) is not None:
            ds_snr.labels(source=source, channel_id=cid).set(v)

    for ch in data.get("usinfo", []) or []:
        cid = ch.get("channelId")
        if cid is not None and (v := _f(ch.get("signalStrength"))) is not None:
            us_signal.labels(source=source, channel_id=cid).set(v)

    for ch in data.get("dsofdminfo", []) or []:
        idx = ch.get("index") or "0"
        if (v := _f(ch.get("SNR"))) is not None:
            ds_ofdm_snr.labels(source=source, index=idx).set(v)
        if (v := _f(ch.get("plcpower"))) is not None:
            ds_ofdm_plc_power.labels(source=source, index=idx).set(v)

    for ch in data.get("usofdminfo", []) or []:
        idx = ch.get("uschindex")
        if idx is None:
            continue
        if (v := _f(ch.get("repPower1_6"))) is not None:
            us_ofdm_power_1_6.labels(source=source, uschindex=idx).set(v)

    spec = Coda57Client.check_midco_spec(data)
    spec_in_range.labels(source=source, direction="downstream").set(1 if spec["ds_in_spec"] else 0)
    spec_in_range.labels(source=source, direction="upstream").set(1 if spec["us_in_spec"] else 0)


async def poll_ht_em5(client: HitronMocaClient) -> None:
    host = client.host
    async with client:
        snapshot = await client.get_all()

    status = snapshot.get("status", {})
    moca_link_up.labels(host=host).set(1 if status.get("link_status") == "Up" else 0)
    moca_tx_power_index.labels(host=host).set(_f(snapshot.get("moca", {}).get("tx_power_index")) or 0)
    moca_beacon_power_index.labels(host=host).set(_f(snapshot.get("moca", {}).get("beacon_power_level_index")) or 0)

    for direction, counters in (("tx", status.get("ethernet_tx", {})), ("rx", status.get("ethernet_rx", {}))):
        for outcome, value in counters.items():
            if (v := _f(value)) is not None:
                moca_eth_frames.labels(host=host, direction=direction, outcome=outcome).set(v)

    nodes = snapshot.get("nodes", [])
    moca_node_count.labels(host=host).set(len(nodes))
    for node in nodes:
        moca_node_present.labels(
            host=host,
            node_id=str(node["node_id"]),
            mac_address=node["mac_address"],
            is_self=str(node["is_self"]),
            is_network_coordinator=str(node["is_network_coordinator"]),
        ).set(1)


def _f(x) -> float | None:
    try:
        return float(x)
    except (TypeError, ValueError):
        return None


async def poll_once(
    ht_em5_hosts: list[str],
    coda60_host: str | None,
    coda57_host: str | None,
    coda60_model: str | None = None,
) -> None:
    jobs = [poll_ht_em5(HitronMocaClient(host)) for host in ht_em5_hosts]
    if coda60_host:
        jobs.append(poll_coda60(Coda60Client(coda60_host), source="coda60", model=coda60_model))
    if coda57_host:
        jobs.append(poll_coda57(Coda57Client(coda57_host), source="coda57"))
    results = await asyncio.gather(*jobs, return_exceptions=True)
    for host, result in zip(ht_em5_hosts + (["coda60"] if coda60_host else []) + (["coda57"] if coda57_host else []), results):
        if isinstance(result, Exception):
            log.warning("poll of %s failed: %s", host, result)


async def poll_loop(
    ht_em5_hosts: list[str],
    coda60_host: str | None,
    coda57_host: str | None,
    interval: float,
    coda60_model: str | None = None,
) -> None:
    while True:
        await poll_once(ht_em5_hosts, coda60_host, coda57_host, coda60_model)
        await asyncio.sleep(interval)


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--port", type=int, default=int(os.environ.get("HITRON_EXPORTER_PORT", "9877")))
    ap.add_argument("--interval", type=float, default=float(os.environ.get("HITRON_EXPORTER_INTERVAL", "30")))
    ap.add_argument("--ht-em5", action="append", default=[h for h in os.environ.get("HITRON_HT_EM5_HOSTS", "").split(",") if h], help="HT-EM5 bridge IP; repeatable")
    ap.add_argument("--coda60", default=os.environ.get("HITRON_CODA60_HOST") or None, help="CODA6X gateway IP")
    ap.add_argument("--coda60-model", default=os.environ.get("HITRON_CODA60_MODEL") or None,
                    help="force a model profile (e.g. coda6011uv) instead of detecting it")
    ap.add_argument("--coda57", default=os.environ.get("HITRON_CODA57_HOST") or None, help="CODA-57 gateway IP (legacy .asp API)")
    args = ap.parse_args()

    if not args.ht_em5 and not args.coda60 and not args.coda57:
        ap.error("give at least one of --ht-em5 / --coda60 / --coda57 (or the matching env var)")
    if args.coda60 and args.coda60_model:
        profile = get_profile(args.coda60_model)
        if profile is None:
            ap.error(f"unknown --coda60-model {args.coda60_model!r}")
        _CODA60_PROFILES[args.coda60] = profile

    log.info("serving /metrics on :%d, polling every %ss", args.port, args.interval)
    log.info("HT-EM5: %s | CODA6X: %s | CODA-57: %s", args.ht_em5 or "(none)", args.coda60 or "(none)", args.coda57 or "(none)")
    start_http_server(args.port)
    asyncio.run(poll_loop(args.ht_em5, args.coda60, args.coda57, args.interval, args.coda60_model))


if __name__ == "__main__":
    main()
