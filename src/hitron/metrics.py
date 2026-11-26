"""Normalise every supported Hitron device into one flat metric vocabulary.

The exporter, the Telegraf/Influx renderer, the OTLP renderer, the Grafana
dashboards, and the static site all read the same :class:`Sample` objects, so a
metric added here appears everywhere without a second mapping table.

Naming rules
------------
* CODA6X / CODA-57 use the historical ``modem_*`` names so existing Prometheus
  scrape configs and dashboards keep working.
* HT-EM5 / MoCA uses ``hitron_*``.
* Counters end in ``_total`` and are emitted as integers where the firmware
  gives integers.
* Every series carries a ``source`` (CODA) or ``host`` (HT-EM5) label. The
  extra labels are device-level identifiers (channel, radio, band, host), never
  unbounded strings such as an event-log message.
"""

from __future__ import annotations

import math
import re
from dataclasses import dataclass, field
from typing import Any, Iterable

__all__ = [
    "Sample",
    "SpecRange",
    "coda60_samples",
    "coda57_samples",
    "ht_em5_samples",
    "parse_uptime",
    "parse_counter",
]


@dataclass(frozen=True)
class Sample:
    """One metric point."""

    name: str
    value: float
    labels: dict[str, str] = field(default_factory=dict)
    help: str = ""
    kind: str = "gauge"  # "gauge" | "counter"
    unit: str = ""

    def signature(self) -> tuple[str, tuple[tuple[str, str], ...]]:
        return self.name, tuple(sorted(self.labels.items()))


@dataclass(frozen=True)
class SpecRange:
    """Carrier power envelope, in dBmV. Defaults are Midco's published range."""

    ds_min: float = -6.0
    ds_max: float = 15.0
    us_min: float = 35.0
    us_max: float = 48.0


# ---------------------------------------------------------------------------
# scalar helpers
# ---------------------------------------------------------------------------

_NUM_RE = re.compile(r"[-+]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][-+]?\d+)?")


def _f(value: Any) -> float | None:
    """Best-effort float. Returns ``None`` for blanks, sentinels, and junk.

    The firmware quotes numbers, appends units, and emits the literal string
    ``-inf`` for a channel with no reading. ``-inf`` is mapped to ``None`` so a
    dead channel does not become a huge negative spike on a dashboard.
    """
    if value is None or isinstance(value, bool):
        return None
    if isinstance(value, (int, float)):
        v = float(value)
    else:
        text = str(value).strip().strip('"')
        if not text:
            return None
        if text.lower() in {"-inf", "inf", "+inf", "nan", "n/a", "na", "--"}:
            return None
        m = _NUM_RE.search(text)
        if not m:
            return None
        v = float(m.group(0))
    if math.isnan(v) or math.isinf(v):
        return None
    return v


def parse_counter(value: Any) -> int | None:
    """Parse the firmware's counter encodings.

    SC-QAM octets arrive as ``"34 * 2e32   1203911172"``; codeword counters
    arrive as plain integers. Take the last integer-looking group.
    """
    if value is None:
        return None
    if isinstance(value, bool):
        return None
    if isinstance(value, int):
        return value
    if isinstance(value, float):
        return int(value) if math.isfinite(value) else None
    text = str(value).strip().strip('"')
    if not text:
        return None
    matches = re.findall(r"\d+", text.replace(",", ""))
    if not matches:
        return None
    try:
        return int(matches[-1])
    except ValueError:
        return None


def parse_uptime(value: Any) -> float | None:
    """Parse ``"134h:54m:33s"``, ``"2 days, 03:04:05"``, or plain seconds."""
    if value is None:
        return None
    text = str(value).strip()
    if not text:
        return None
    if re.fullmatch(r"\d+(?:\.\d+)?", text):
        return float(text)
    days = 0.0
    m = re.search(r"(\d+)\s*(?:day|d)\b", text, re.I)
    if m:
        days = float(m.group(1))
    h = re.search(r"(\d+)\s*h", text, re.I)
    mnt = re.search(r"(\d+)\s*m", text, re.I)
    sec = re.search(r"(\d+)\s*s", text, re.I)
    if h or mnt or sec:
        return days * 86400 + (float(h.group(1)) if h else 0) * 3600 + (float(mnt.group(1)) if mnt else 0) * 60 + (float(sec.group(1)) if sec else 0)
    m = re.search(r"(?:(\d+)\s+days?,\s*)?(\d{1,2}):(\d{2}):(\d{2})", text)
    if m:
        return float(m.group(1) or 0) * 86400 + float(m.group(2)) * 3600 + float(m.group(3)) * 60 + float(m.group(4))
    return _f(text)


def _flag(value: Any) -> int | None:
    if value is None:
        return None
    text = str(value).strip().strip('"').lower()
    if text in {"1", "on", "yes", "true", "success", "up", "operate", "operating", "locked", "allowed", "enabled", "enable"}:
        return 1
    if text in {"0", "off", "no", "false", "fail", "failed", "down", "none", "disabled", "disable"}:
        return 0
    return None


def _clean(value: Any) -> str:
    return str(value).strip().strip('"') if value is not None else ""


def _speed_mbps(value: Any) -> float | None:
    """Parse switch port speed: ``1Gbps`` -> 1000, ``100Mbps`` -> 100."""
    if value is None:
        return None
    text = str(value).strip().lower()
    match = re.search(r"([\d.]+)\s*(g|m)?bps", text)
    if match:
        number = float(match.group(1))
        return number * 1000 if match.group(2) == "g" else number
    return _f(value)


def _samples_for_channels(
    channels: Iterable[dict[str, Any]],
    *,
    source: str,
    kind: str,
    id_key: str,
    id_label: str,
    frequency_key: str = "frequency",
    power_key: str = "signalStrength",
    snr_key: str = "snr",
    prefix: str = "modem",
) -> list[Sample]:
    out: list[Sample] = []
    for ch in channels or []:
        cid = _clean(ch.get(id_key))
        if not cid:
            continue
        labels = {"source": source, id_label: cid}
        port = _clean(ch.get("portId"))
        if port:
            labels["port_id"] = port
        if kind == "ds":
            out.append(Sample(f"{prefix}_downstream_channel_frequency_hz", _f(ch.get(frequency_key)) or 0.0, labels, "Downstream channel centre frequency", unit="Hz"))
            if (v := _f(ch.get(power_key))) is not None:
                out.append(Sample(f"{prefix}_downstream_channel_power_dbmv", v, labels, "Downstream channel power", unit="dBmV"))
            if (v := _f(ch.get(snr_key))) is not None:
                out.append(Sample(f"{prefix}_downstream_channel_snr_db", v, labels, "Downstream channel SNR", unit="dB"))
            for key, name, desc in (
                ("dsoctets", f"{prefix}_downstream_channel_octets_total", "Downstream octets"),
                ("correcteds", f"{prefix}_downstream_channel_corrected_total", "Corrected codewords"),
                ("uncorrect", f"{prefix}_downstream_channel_uncorrectable_total", "Uncorrectable codewords"),
            ):
                if (v := parse_counter(ch.get(key))) is not None:
                    out.append(Sample(name, float(v), labels, desc, kind="counter"))
            mod = _clean(ch.get("modulation"))
            if mod:
                out.append(Sample(f"{prefix}_downstream_channel_info", 1.0, {**labels, "modulation": mod}, "Downstream channel labels"))
        else:
            out.append(Sample(f"{prefix}_upstream_channel_frequency_hz", _f(ch.get(frequency_key)) or 0.0, labels, "Upstream channel centre frequency", unit="Hz"))
            if (v := _f(ch.get(power_key))) is not None:
                out.append(Sample(f"{prefix}_upstream_channel_power_dbmv", v, labels, "Upstream channel power", unit="dBmV"))
            if (v := _f(ch.get("bandwidth"))) is not None:
                out.append(Sample(f"{prefix}_upstream_channel_bandwidth_hz", v, labels, "Upstream channel bandwidth", unit="Hz"))
            mod = _clean(ch.get("modtype")) or _clean(ch.get("scdmaMode"))
            if mod:
                out.append(Sample(f"{prefix}_upstream_channel_info", 1.0, {**labels, "modulation": mod}, "Upstream channel labels"))
    return out


# ---------------------------------------------------------------------------
# CODA6X (CODA-60 and siblings)
# ---------------------------------------------------------------------------


def coda60_samples(snapshot: dict[str, Any], source: str = "coda60") -> list[Sample]:
    """Turn a ``Coda60Client.get_all()`` snapshot into samples."""
    data = snapshot.get("metrics", snapshot)
    out: list[Sample] = []

    version = data.get("cm_version", {}) or {}
    router = data.get("router_sysinfo", {}) or {}
    if version or router:
        model = _clean(version.get("modelName") or router.get("modelName"))
        labels = {
            "source": source,
            "model": model,
            "hardware": _clean(version.get("hwVersion") or router.get("hwVersion")),
            "firmware": _clean(version.get("swVersion") or router.get("swVersion")),
            "serial": _clean(version.get("serialNumber") or router.get("serialNumber")),
        }
        out.append(Sample("modem_info", 1.0, labels, "Device identity labels"))

    if (uptime := parse_uptime(router.get("systemUptime"))) is not None:
        out.append(Sample("modem_system_uptime_seconds", uptime, {"source": source}, "System uptime", unit="s"))
    if (rf_mac := _clean(router.get("rfMac"))) :
        out.append(Sample("modem_rf_mac_info", 1.0, {"source": source, "mac": rf_mac}, "HFC MAC address"))

    prov = data.get("cm_docsisprovision", {}) or {}
    prov_lower = {str(k).lower(): v for k, v in prov.items()}
    for step in ("hwInit", "findDownstream", "ranging", "dhcp", "timeOfday", "downloadCfg", "registration"):
        # The firmware spells this one both "timeOfday" and "timeofday"
        # depending on build, so look the key up case-insensitively.
        if step.lower() in prov_lower:
            ok = _flag(prov_lower[step.lower()])
            out.append(Sample("modem_docsis_provisioning_step_success", 1.0 if ok else 0.0, {"source": source, "step": step}, "1 when the provisioning step reports success"))

    sysinfo = data.get("cm_sysinfo", {}) or {}
    if sysinfo:
        labels = {
            "source": source,
            "ip": _clean(sysinfo.get("ip")),
            "gateway": _clean(sysinfo.get("gw")),
            "subnet": _clean(sysinfo.get("subMask")),
            "config_file": _clean(sysinfo.get("Configname")),
            "access": _clean(sysinfo.get("ntAccess")),
        }
        out.append(Sample("modem_cm_wan_info", 1.0, labels, "CM WAN and provisioning labels"))

    out += _samples_for_channels(
        (data.get("cm_dsinfo", {}) or {}).get("Freq_List", []),
        source=source, kind="ds", id_key="channelId", id_label="channel_id",
    )
    out += _samples_for_channels(
        (data.get("cm_usinfo", {}) or {}).get("Freq_List", []),
        source=source, kind="us", id_key="channelId", id_label="channel_id",
    )

    for ch in (data.get("cm_dsofdm", {}) or {}).get("Freq_List", []) or []:
        idx = _clean(ch.get("index"))
        if not idx:
            continue
        labels = {"source": source, "index": idx}
        if (v := _f(ch.get("Subcarr0freqFreq"))) is not None:
            out.append(Sample("modem_downstream_ofdm_channel_frequency_hz", v, labels, "OFDM subcarrier-0 frequency", unit="Hz"))
        for key, name, desc, unit in (
            ("SNR", "modem_downstream_ofdm_channel_snr_db", "OFDM SNR", "dB"),
            ("plcpower", "modem_downstream_ofdm_channel_plc_power_dbmv", "OFDM PLC power", "dBmV"),
        ):
            if (v := _f(ch.get(key))) is not None:
                out.append(Sample(name, v, labels, desc, unit=unit))
        for key, name, desc in (
            ("plclock", "modem_downstream_ofdm_channel_plc_locked", "OFDM PLC lock"),
            ("ncplock", "modem_downstream_ofdm_channel_ncp_locked", "OFDM NCP lock"),
            ("mdc1lock", "modem_downstream_ofdm_channel_mdc1_locked", "OFDM MDC1 lock"),
        ):
            if (v := _flag(ch.get(key))) is not None:
                out.append(Sample(name, float(v), labels, desc))
        for key, name, desc in (
            ("dsoctets", "modem_downstream_ofdm_channel_octets_total", "OFDM downstream octets"),
            ("correcteds", "modem_downstream_ofdm_channel_corrected_total", "OFDM corrected codewords"),
            ("uncorrect", "modem_downstream_ofdm_channel_uncorrectable_total", "OFDM uncorrectable codewords"),
        ):
            if (v := parse_counter(ch.get(key))) is not None:
                out.append(Sample(name, float(v), labels, desc, kind="counter"))
        if (fft := _clean(ch.get("ffttype"))):
            out.append(Sample("modem_downstream_ofdm_channel_info", 1.0, {**labels, "fft_type": fft}, "OFDM channel labels"))

    for ch in (data.get("cm_usofdm", {}) or {}).get("Freq_List", []) or []:
        idx = _clean(ch.get("uschindex"))
        if not idx:
            continue
        labels = {"source": source, "index": idx}
        state = _clean(ch.get("state"))
        # A disabled OFDMA channel reports sentinel values (-99 dBmV, 0 Hz).
        # Emitting those would look like a real outage, so only the state flag
        # survives for a disabled channel.
        disabled = state.upper().startswith("DISABLED")
        if not disabled:
            if (v := _f(ch.get("frequency"))) is not None:
                out.append(Sample("modem_upstream_ofdm_channel_frequency_hz", v, labels, "OFDMA centre frequency", unit="Hz"))
            if (v := _f(ch.get("channelBw"))) is not None:
                out.append(Sample("modem_upstream_ofdm_channel_bandwidth_mhz", v, labels, "OFDMA channel bandwidth", unit="MHz"))
            for key, name, desc, unit in (
                ("repPower", "modem_upstream_ofdm_channel_power_dbmv", "OFDMA reported power", "dBmV"),
                ("repPower1_6", "modem_upstream_ofdm_channel_power_1_6_dbmv", "OFDMA DOCSIS-3.0-equivalent power", "dBmV"),
                ("digAtten", "modem_upstream_ofdm_channel_digital_attenuation_db", "OFDMA digital attenuation", "dB"),
            ):
                if (v := _f(ch.get(key))) is not None:
                    out.append(Sample(name, v, labels, desc, unit=unit))
        if state:
            out.append(Sample(
                "modem_upstream_ofdm_channel_operating",
                0.0 if disabled else 1.0,
                {**labels, "state": state},
                "OFDMA channel operating",
            ))
        if (fft := _clean(ch.get("fftVal"))):
            out.append(Sample("modem_upstream_ofdm_channel_info", 1.0, {**labels, "fft": fft}, "OFDMA channel labels"))

    moca = data.get("moca", {}) or {}
    if moca:
        if (v := _flag(moca.get("moca_status"))) is not None:
            out.append(Sample("modem_moca_enabled", float(v), {"source": source}, "MoCA enabled"))
        if (v := _flag(moca.get("mocaLink"))) is not None:
            out.append(Sample("modem_moca_link_up", float(v), {"source": source}, "MoCA link up"))
        for key, name, desc in (
            ("txpower", "modem_moca_tx_power", "MoCA TX power"),
            ("beaconpwr", "modem_moca_beacon_power", "MoCA beacon power"),
            ("coaxTx", "modem_moca_coax_tx_db", "MoCA coax TX"),
            ("coaxRx", "modem_moca_coax_rx_db", "MoCA coax RX"),
        ):
            if (v := _f(moca.get(key))) is not None:
                out.append(Sample(name, v, {"source": source}, desc))
        if (mac := _clean(moca.get("mocaMac"))):
            out.append(Sample("modem_moca_info", 1.0, {"source": source, "mac": mac, "channel": _clean(moca.get("fs_channel")), "mode": _clean(moca.get("fs_mode")), "nctype": _clean(moca.get("nctype"))}, "MoCA labels"))

    radios = (data.get("wifi_radios", {}) or {}).get("Raidos_List", []) or []
    for i, radio in enumerate(radios, start=1):
        band = _clean(radio.get("band")) or str(i)
        labels = {"source": source, "radio_index": str(i), "band": band}
        if (v := _flag(radio.get("wlsOnOff"))) is not None:
            out.append(Sample("modem_wifi_radio_enabled", float(v), labels, "Radio enabled"))
        if (v := _f(radio.get("wlsCurrentChannel"))) is not None:
            out.append(Sample("modem_wifi_radio_channel", v, labels, "Current radio channel"))
        if (v := _f(radio.get("wlsChannel"))) is not None:
            out.append(Sample("modem_wifi_radio_configured_channel", v, labels, "Configured radio channel"))

    clients = ((data.get("wifi_client", {}) or {}).get("Client_List")
               or (data.get("wifi_client", {}) or {}).get("Clients_List")
               or [])
    per_band: dict[str, int] = {}
    for client in clients:
        band = _clean(client.get("band")) or "unknown"
        per_band[band] = per_band.get(band, 0) + 1
        labels = {
            "source": source,
            "band": band,
            "hostname": _clean(client.get("hostname")),
            "mac": _clean(client.get("mac")),
            "ssid": _clean(client.get("ssid")),
        }
        if (v := _f(client.get("rssi"))) is not None:
            out.append(Sample("modem_wifi_client_rssi_dbm", v, labels, "Wireless client RSSI", unit="dBm"))
        out.append(Sample("modem_wifi_client_info", 1.0, labels, "Wireless client labels"))
    for band, count in per_band.items():
        out.append(Sample("modem_wifi_client_count", float(count), {"source": source, "band": band}, "Wireless clients per band"))

    hosts = (data.get("hosts", {}) or {}).get("Hosts_List", []) or []
    if hosts:
        out.append(Sample("modem_host_count", float(len(hosts)), {"source": source}, "LAN hosts seen"))
    for host in hosts:
        labels = {
            "source": source,
            "host_id": _clean(host.get("hostID")),
            "hostname": _clean(host.get("hostName")),
            "ip": _clean(host.get("ip")),
            "mac": _clean(host.get("macAddr")),
            "connect_type": _clean(host.get("connectType")),
            "device_type": _clean(host.get("deviceType")),
        }
        out.append(Sample("modem_host_info", 1.0, labels, "LAN host labels"))
        if (v := _f(host.get("rssi"))) is not None:
            out.append(Sample("modem_host_rssi_dbm", v, labels, "Host RSSI", unit="dBm"))

    for port in (data.get("advanced_advancedswitch", {}) or {}).get("advSwitch_List", []) or []:
        pid = _clean(port.get("portId"))
        if not pid:
            continue
        labels = {"source": source, "port_id": pid}
        if (v := _flag(port.get("linked"))) is not None:
            out.append(Sample("modem_ethernet_port_link_up", float(v), labels, "Ethernet port link up"))
        if (v := _speed_mbps(port.get("speed"))) is not None:
            out.append(Sample("modem_ethernet_port_speed_mbps", v, labels, "Ethernet port speed", unit="Mbps"))
        out.append(Sample("modem_ethernet_port_info", 1.0, {**labels, "duplex": _clean(port.get("duplex")), "negotiation": _clean(port.get("neg"))}, "Ethernet port labels"))

    mta = data.get("mta_status", {}) or {}
    for line in (1, 2, 3, 4):
        reg = mta.get(f"Line{line}RegState")
        if reg is None:
            continue
        label = {"source": source, "line": str(line)}
        out.append(Sample("modem_mta_line_registered", 1.0 if "regist" in _clean(reg).lower() else 0.0, {**label, "state": _clean(reg)}, "Voice line registered"))
        call = mta.get(f"line{line}CallState")
        if call is not None:
            out.append(Sample("modem_mta_line_call_state", 1.0, {**label, "state": _clean(call)}, "Voice line call state"))

    lte = data.get("cm_ltebackup", {}) or {}
    if lte:
        if (v := _f(lte.get("LTE_SignalStrength"))) is not None:
            out.append(Sample("modem_lte_signal_strength", v, {"source": source}, "LTE backup signal strength"))
        out.append(Sample("modem_lte_info", 1.0, {
            "source": source,
            "connection": _clean(lte.get("LTE_Connection")),
            "isp": _clean(lte.get("LTE_ISP")),
            "wan_ip": _clean(lte.get("LTE_WANIP")),
            "apn": _clean(lte.get("LTE_APN")),
        }, "LTE backup labels"))

    return out


# ---------------------------------------------------------------------------
# CODA-57 legacy .asp API
# ---------------------------------------------------------------------------


def coda57_samples(data: dict[str, Any], source: str = "coda57", spec: SpecRange | None = None) -> list[Sample]:
    """Turn a ``Coda57Client.get_all()`` snapshot into samples."""
    out: list[Sample] = []
    out += _samples_for_channels(data.get("dsinfo", []) or [], source=source, kind="ds", id_key="channelId", id_label="channel_id")
    out += _samples_for_channels(data.get("usinfo", []) or [], source=source, kind="us", id_key="channelId", id_label="channel_id")

    for ch in data.get("dsofdminfo", []) or []:
        idx = _clean(ch.get("index")) or "0"
        labels = {"source": source, "index": idx}
        for key, name, desc, unit in (
            ("SNR", "modem_downstream_ofdm_channel_snr_db", "OFDM SNR", "dB"),
            ("plcpower", "modem_downstream_ofdm_channel_plc_power_dbmv", "OFDM PLC power", "dBmV"),
            ("Subcarr0freqFreq", "modem_downstream_ofdm_channel_frequency_hz", "OFDM subcarrier-0 frequency", "Hz"),
        ):
            if (v := _f(ch.get(key))) is not None:
                out.append(Sample(name, v, labels, desc, unit=unit))
        for key, name, desc in (
            ("plclock", "modem_downstream_ofdm_channel_plc_locked", "OFDM PLC lock"),
            ("ncplock", "modem_downstream_ofdm_channel_ncp_locked", "OFDM NCP lock"),
            ("mdc1lock", "modem_downstream_ofdm_channel_mdc1_locked", "OFDM MDC1 lock"),
        ):
            if (v := _flag(ch.get(key))) is not None:
                out.append(Sample(name, float(v), labels, desc))
        for key, name, desc in (
            ("dsoctets", "modem_downstream_ofdm_channel_octets_total", "OFDM downstream octets"),
            ("correcteds", "modem_downstream_ofdm_channel_corrected_total", "OFDM corrected codewords"),
            ("uncorrect", "modem_downstream_ofdm_channel_uncorrectable_total", "OFDM uncorrectable codewords"),
        ):
            if (v := parse_counter(ch.get(key))) is not None:
                out.append(Sample(name, float(v), labels, desc, kind="counter"))
        if (fft := _clean(ch.get("ffttype"))):
            out.append(Sample("modem_downstream_ofdm_channel_info", 1.0, {**labels, "fft_type": fft}, "OFDM channel labels"))

    for ch in data.get("usofdminfo", []) or []:
        idx = _clean(ch.get("uschindex"))
        if not idx:
            continue
        labels = {"source": source, "index": idx}
        state = _clean(ch.get("state"))
        disabled = state.upper().startswith("DISABLED")
        if not disabled:
            for key, name, desc, unit in (
                ("repPower", "modem_upstream_ofdm_channel_power_dbmv", "OFDMA reported power", "dBmV"),
                ("repPower1_6", "modem_upstream_ofdm_channel_power_1_6_dbmv", "OFDMA DOCSIS-3.0-equivalent power", "dBmV"),
                ("frequency", "modem_upstream_ofdm_channel_frequency_hz", "OFDMA centre frequency", "Hz"),
                ("channelBw", "modem_upstream_ofdm_channel_bandwidth_mhz", "OFDMA bandwidth", "MHz"),
                ("digAtten", "modem_upstream_ofdm_channel_digital_attenuation_db", "OFDMA digital attenuation", "dB"),
            ):
                if (v := _f(ch.get(key))) is not None:
                    out.append(Sample(name, v, labels, desc, unit=unit))
        if state:
            out.append(Sample(
                "modem_upstream_ofdm_channel_operating",
                0.0 if disabled else 1.0,
                {**labels, "state": state},
                "OFDMA channel operating",
            ))

    rng = spec or SpecRange()
    from .coda57 import Coda57Client  # local import keeps metrics importable in tests

    check = Coda57Client.check_midco_spec(data)
    out.append(Sample("modem_power_in_spec", 1.0 if check["ds_in_spec"] else 0.0, {"source": source, "direction": "downstream"}, "Power inside the configured carrier envelope"))
    out.append(Sample("modem_power_in_spec", 1.0 if check["us_in_spec"] else 0.0, {"source": source, "direction": "upstream"}, "Power inside the configured carrier envelope"))
    out.append(Sample("modem_downstream_power_min_dbmv", check["ds_min_dbmv"], {"source": source}, "Minimum downstream power"))
    out.append(Sample("modem_downstream_power_max_dbmv", check["ds_max_dbmv"], {"source": source}, "Maximum downstream power"))
    out.append(Sample("modem_upstream_power_min_dbmv", check["us_min_dbmv"], {"source": source}, "Minimum upstream power"))
    out.append(Sample("modem_upstream_power_max_dbmv", check["us_max_dbmv"], {"source": source}, "Maximum upstream power"))

    sysinfo = (data.get("getSysInfo") or [{}])
    sysinfo = sysinfo[0] if isinstance(sysinfo, list) and sysinfo else (sysinfo if isinstance(sysinfo, dict) else {})
    if isinstance(sysinfo, dict) and sysinfo:
        if (uptime := parse_uptime(sysinfo.get("systemUptime"))) is not None:
            out.append(Sample("modem_system_uptime_seconds", uptime, {"source": source}, "System uptime", unit="s"))
        if (mac := _clean(sysinfo.get("rfMac"))):
            out.append(Sample("modem_rf_mac_info", 1.0, {"source": source, "mac": mac}, "HFC MAC address"))
        out.append(Sample("modem_info", 1.0, {"source": source, "model": "CODA-57", "hardware": _clean(sysinfo.get("hwVersion")), "firmware": _clean(sysinfo.get("swVersion")), "serial": _clean(sysinfo.get("serialNumber"))}, "Device identity labels"))
    return out


# ---------------------------------------------------------------------------
# HT-EM5 MoCA bridge
# ---------------------------------------------------------------------------


def ht_em5_samples(snapshot: dict[str, Any], host: str | None = None, phy_rates: dict[str, Any] | None = None) -> list[Sample]:
    """Turn a ``HitronMocaClient.get_all()`` snapshot into samples."""
    host = host or snapshot.get("host") or "unknown"
    status = snapshot.get("status", {}) or {}
    net = snapshot.get("network", {}) or {}
    moca = snapshot.get("moca", {}) or {}
    nodes = snapshot.get("nodes", []) or []
    out: list[Sample] = []

    out.append(Sample("hitron_device_info", 1.0, {
        "host": host,
        "model": _clean(status.get("model_name")),
        "hardware": _clean(status.get("hardware_version")),
        "firmware": _clean(status.get("software_version")),
        "mac": _clean(status.get("mac_address")),
        "ip": _clean(status.get("ip_address")),
    }, "Device identity labels"))

    if (v := _flag(status.get("link_status"))) is not None:
        out.append(Sample("hitron_moca_link_up", float(v), {"host": host}, "MoCA link up"))
    for key, name, desc, unit in (
        ("beacon_channel_mhz", "hitron_moca_beacon_channel_mhz", "Beacon channel", "MHz"),
        ("primary_channel_mhz", "hitron_moca_primary_channel_mhz", "Primary channel", "MHz"),
        ("secondary_channel_mhz", "hitron_moca_secondary_channel_mhz", "Secondary channel", "MHz"),
        ("lof_mhz", "hitron_moca_lof_mhz", "Last operating frequency", "MHz"),
        ("num_channels", "hitron_moca_channel_count", "Number of MoCA channels", ""),
        ("my_node_id", "hitron_moca_self_node_id", "This bridge's node id", ""),
    ):
        if (v := _f(status.get(key))) is not None:
            out.append(Sample(name, v, {"host": host}, desc, unit=unit))
    if (v := _clean(status.get("network_moca_version"))):
        out.append(Sample("hitron_moca_network_version_info", 1.0, {"host": host, "version": v}, "MoCA network version"))

    for direction, counters in (("tx", status.get("ethernet_tx", {}) or {}), ("rx", status.get("ethernet_rx", {}) or {})):
        for outcome, value in counters.items():
            if (v := parse_counter(value)) is not None:
                out.append(Sample("hitron_moca_ethernet_frames_total", float(v), {"host": host, "direction": direction, "outcome": outcome}, "Ethernet frame counters", kind="counter"))

    if (v := _f(moca.get("tx_power_index"))) is not None:
        out.append(Sample("hitron_moca_tx_power_index", v, {"host": host}, "TX power index"))
    if (v := _f(moca.get("beacon_power_level_index"))) is not None:
        out.append(Sample("hitron_moca_beacon_power_index", v, {"host": host}, "Beacon power level index"))
    if (v := _f(moca.get("lof_mhz"))) is not None:
        out.append(Sample("hitron_moca_configured_lof_mhz", v, {"host": host}, "Configured LOF", unit="MHz"))
    for band in moca.get("bands", []) or []:
        name = _clean(band.get("band"))
        if not name:
            continue
        labels = {"host": host, "band": name}
        out.append(Sample("hitron_moca_band_enabled", 1.0 if band.get("enabled") else 0.0, labels, "MoCA band enabled"))
        out.append(Sample("hitron_moca_band_supported", 1.0 if band.get("supported") else 0.0, labels, "MoCA band supported"))
        out.append(Sample("hitron_moca_band_privacy_enabled", 1.0 if band.get("privacy_enabled") else 0.0, labels, "MoCA band privacy"))

    for key, name, desc in (("dhcp_enabled", "hitron_network_dhcp_enabled", "LAN DHCP enabled"), ("autoip_enabled", "hitron_network_autoip_enabled", "Auto-IP enabled"), ("telnet_enabled", "hitron_network_telnet_enabled", "Telnet enabled")):
        if key in net:
            out.append(Sample(name, 1.0 if net.get(key) else 0.0, {"host": host}, desc))

    out.append(Sample("hitron_moca_node_count", float(len(nodes)), {"host": host}, "MoCA nodes visible"))
    for node in nodes:
        labels = {
            "host": host,
            "node_id": str(node.get("node_id")),
            "mac": _clean(node.get("mac_address")),
            "is_self": str(bool(node.get("is_self"))).lower(),
            "is_network_coordinator": str(bool(node.get("is_network_coordinator"))).lower(),
        }
        out.append(Sample("hitron_moca_node_present", 1.0, labels, "MoCA node present"))
        if (v := _clean(node.get("moca_version"))):
            out.append(Sample("hitron_moca_node_version_info", 1.0, {**labels, "version": v}, "MoCA node version"))

    if phy_rates:
        for node_id, peers in (phy_rates.get("rate_mbps", {}) or {}).items():
            for peer_id, rate in (peers or {}).items():
                out.append(Sample("hitron_moca_phy_rate_mbps", float(rate or 0), {"host": host, "node_id": str(node_id), "peer_node_id": str(peer_id)}, "MoCA PHY rate", unit="Mbps"))
    return out


def dedupe(samples: Iterable[Sample]) -> list[Sample]:
    """Keep the last sample for a given name + label set.

    Multiple endpoints can describe the same channel (for example a legacy
    ``.asp`` response and a REST response); last write wins so a caller can
    layer corrections without duplicating series.
    """
    seen: dict[tuple[str, tuple[tuple[str, str], ...]], Sample] = {}
    for s in samples:
        seen[s.signature()] = s
    return list(seen.values())
