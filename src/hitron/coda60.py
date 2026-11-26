"""
Async client for the Hitron CODA6X (e.g. CODA-60) console REST API
(`/1/Device/...`, PHP + the RDK-B `cosa.so` TR-181 bridge, Fat-Free Framework).

Ported from the standalone `CODA_60.py` script in `~/Documents/hitron-client`
(which was already proven against a real unit) and cleaned up into a reusable
module. See ../../specs/openapi-coda60.yaml and ../../docs/KB.md for the full
reverse-engineering writeup: route table, auth model (session cookie or
RS256-JWT vendor-app token), the spectrum analyzer, and known firmware quirks.

This module is READ-ONLY by design: it only calls GET endpoints. Anything that
writes to a real, in-service gateway (admin password, reboot, factory reset,
firewall/WiFi config) is deliberately not implemented here - those are
destructive-by-nature on a production device and belong in a tool a human
runs deliberately, not a metrics poller.
"""

from __future__ import annotations

import asyncio
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

import aiohttp

# Each entry: the route under /1/Device/, and the fields to keep from its
# response (a dict for scalar responses, or {"ListKey": {field: label}} for
# the Freq_List/Log_List-shaped endpoints). Labels are documentation, not
# used at runtime - kept so this table doubles as a field reference.
ENDPOINTS: list[dict[str, Any]] = [
    {
        "endpoint": "Router/SysInfo",
        "gathers": {
            "hwVersion": "Hardware Version",
            "swVersion": "Firmware Version",
            "serialNumber": "Serial Number",
            "systemTime": "System Time",
            "rfMac": "HFC MAC Address",
            "systemUptime": "System Uptime",
        },
    },
    {
        "endpoint": "CM/DocsisProvision",
        "gathers": {
            "hwInit": "Hardware Initialization",
            "findDownstream": "Downstream Lock",
            "ranging": "Ranged With CMTS",
            "dhcp": "Obtained IP Address",
            "timeOfday": "Time of Day",
            "downloadCfg": "Download Configuration File",
            "registration": "Registered with CMTS",
            "bpiStatus": "BPI Security",
        },
    },
    {
        "endpoint": "CM/DsInfo",
        "gathers": {
            "Freq_List": {
                "portId": "Port ID",
                "frequency": "Frequency (Hz)",
                "modulation": "Modulation",
                "signalStrength": "Signal Strength (dBmV)",
                "snr": "Signal to Noise Ratio (dB)",
                "channelId": "Channel ID",
                "dsoctets": "Downstream Octets",
                "correcteds": "Corrected Codewords",
                "uncorrect": "Uncorrectable Codewords",
            }
        },
    },
    {
        "endpoint": "CM/SysInfo",
        "gathers": {
            "ntAccess": "Access Type",
            "ip": "IP Address",
            "subMask": "Subnet Mask",
            "gw": "Default Gateway",
            "lease": "Lease Time",
            "Configname": "Configuration File Name",
        },
    },
    {
        "endpoint": "CM/UsInfo",
        "gathers": {
            "Freq_List": {
                "portId": "Port ID",
                "frequency": "Frequency (Hz)",
                "modtype": "Modulation QAM Type",
                "signalStrength": "Signal Strength (dBmV)",
                "bandwidth": "Bandwidth",
                "channelId": "Channel ID",
                "scdmaMode": "SCDMA Mode",
            }
        },
    },
    {
        "endpoint": "CM/DsOfdm",
        "gathers": {
            "Freq_List": {
                "receive": "Receive Channel",
                "ffttype": "FFT Type",
                "Subcarr0freqFreq": "Subcarrier 0 Frequency (Hz)",
                "plclock": "PLC Lock",
                "ncplock": "NCP Lock",
                "mdc1lock": "MDC1 Lock",
                "plcpower": "PLC Power (dBmV)",
                "SNR": "Signal to Noise Ratio (dB)",
                "dsoctets": "Downstream Octets",
                "correcteds": "Corrected Codewords",
                "uncorrect": "Uncorrectable Codewords",
                "index": "Channel Index",
            }
        },
    },
    {
        "endpoint": "CM/UsOfdm",
        "gathers": {
            "Freq_List": {
                "uschindex": "Channel Index",
                "state": "State",
                "frequency": "Frequency (Hz)",
                "digAtten": "Digital Attenuation",
                "channelBw": "Channel Bandwidth",
                "repPower": "Reported Power (dBmV)",
                "repPower1_6": "Reported Power, DOCSIS 3.0-equivalent (dBmV)",
                "fftVal": "FFT Value",
            }
        },
    },
    {
        "endpoint": "CM/Log",
        "gathers": {
            "Log_List": {
                "index": "Log Index",
                "time": "Log Time",
                "type": "Log Type",
                "priority": "Log Priority",
                "event": "Log Event",
            }
        },
    },
    {
        "endpoint": "Advanced/AdvancedSwitch",
        "gathers": {
            "advSwitch_List": {
                "portId": "Port ID",
                "power": "Power Status",
                "neg": "Negotiation Status",
                "speed": "Speed",
                "duplex": "Duplex Mode",
                "linked": "Linked Status",
            }
        },
    },
    {
        # Full field list per the firmware's own models/MoCA.php - see
        # ../../docs/KB.md §2. Password is intentionally not requested here;
        # treat it as a secret if you add it back for a specific need.
        "endpoint": "MoCA",
        "gathers": {
            "moca_status": "MoCA Enabled",
            "fs_mode": "Channel Plan",
            "fs_channel": "Operating Channel",
            "txpower": "TX Power",
            "beaconpwr": "Beacon Power",
            "nctype": "Network Coordinator Type",
            "mocaNTSecurity": "Privacy Enabled",
            "mocaMac": "MoCA MAC",
            "coaxTx": "Coax TX",
            "coaxRx": "Coax RX",
            "mocaLink": "Link Status",
        },
    },
]


def clean_value(val: Any) -> Any:
    """Strip whitespace/quoting the firmware's JSON encoder leaves in, and
    normalize the literal string "-inf" (seen on some signal fields when a
    channel has no reading) to a real float for downstream math."""
    if not isinstance(val, str):
        return val
    val = val.strip().strip('"').strip()
    if val == "-inf":
        return -99.0
    return val


@dataclass
class Coda60Client:
    host: str = "192.168.100.1"
    timeout: float = 10.0
    # The firmware's PHP-FPM worker pool is small; CODA_60.py's own comment
    # notes going above 2 concurrent requests risks overwhelming the CM's CPU.
    max_concurrency: int = 2

    @property
    def base_url(self) -> str:
        return f"http://{self.host}/1/Device"

    async def _fetch_one(self, session: aiohttp.ClientSession, endpoint_def: dict, sem: asyncio.Semaphore) -> tuple[str, dict]:
        url = f"{self.base_url}/{endpoint_def['endpoint']}"
        gathers = endpoint_def["gathers"]
        safe_key = endpoint_def["endpoint"].replace("/", "_").lower()
        async with sem:
            try:
                async with session.get(url, timeout=aiohttp.ClientTimeout(total=self.timeout)) as response:
                    if response.status != 200:
                        return safe_key, {"error": f"status {response.status}"}
                    raw = await response.json(content_type=None)
                    if not raw:
                        return safe_key, {"error": "empty response"}
                    filtered: dict[str, Any] = {}
                    for key, target in gathers.items():
                        if key not in raw:
                            continue
                        if isinstance(raw[key], list) and isinstance(target, dict):
                            wanted = target.keys()
                            filtered[key] = [{k: clean_value(item.get(k)) for k in wanted} for item in raw[key]]
                        else:
                            filtered[key] = clean_value(raw[key])
                    return safe_key, filtered
            except Exception as e:  # noqa: BLE001 - this is a best-effort poller, one bad endpoint shouldn't kill the rest
                return safe_key, {"error": str(e)}

    async def get_all(self, endpoints: list[dict] | None = None) -> dict[str, Any]:
        """Fetch every endpoint in ENDPOINTS (or a caller-supplied subset) concurrently, bounded by max_concurrency."""
        sem = asyncio.Semaphore(self.max_concurrency)
        async with aiohttp.ClientSession() as session:
            tasks = [self._fetch_one(session, ed, sem) for ed in (endpoints or ENDPOINTS)]
            results = await asyncio.gather(*tasks)
        return {
            "metadata": {"timestamp": datetime.now(timezone.utc).isoformat(), "device_ip": self.host},
            "metrics": dict(results),
        }
