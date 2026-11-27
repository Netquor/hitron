"""
Async client for the Hitron CODA6X (e.g. CODA-60) console REST API
(`/1/Device/...`, PHP + the RDK-B `cosa.so` TR-181 bridge, Fat-Free Framework).

Ported from the standalone `CODA_60.py` script in `~/Documents/hitron-client`
(which was already proven against a real unit) and cleaned up into a reusable
module. See ../../specs/openapi-coda60.yaml and ../../docs/KB.md for the full
reverse-engineering writeup: route table, auth model (session cookie or
RS256-JWT vendor-app token), the spectrum analyzer, and known firmware quirks.

This module is read-only by default: `get_all()` and the exporters only call
GET endpoints. A few write methods exist (`reboot`, `factory_reset`,
`hard_reset_and_reboot`, `set_admin_password`, `set_admin_username`,
`reset_fec`) because they were needed to measure what the firmware actually
does; they are never called by the pollers, and the reboot family needs an
explicit `login()` first. See `docs/KB.md` for what each one does to the unit.
"""

from __future__ import annotations

import asyncio
import json
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

import aiohttp

from .adapters import get_endpoint
from .metrics import parse_counter
from .models import (
    CapabilityProbes,
    ModelProfile,
    ProfileMatch,
    detect_profile as _detect_profile,
)

__all__ = ["Coda60Client", "WriteNotAuthenticated", "select_endpoints"]


class WriteNotAuthenticated(RuntimeError):
    """Raised by any write/action method when called before login(). There is
    deliberately no auto-login on these - a write against a real, in-service
    modem should always be a traceable, explicit act: call login() yourself
    first, with credentials you provide, right before you mean to write."""

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
            "timeofday": "Time of Day (older builds spell the key in lowercase)",
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
        "endpoint": "CM/Version",
        "gathers": {
            "modelName": "Model Name",
            "vendorName": "Vendor",
            "hwVersion": "Hardware Version",
            "swVersion": "Firmware Version",
            "serialNumber": "Serial Number",
            "deploymentName": "Deployment",
        },
    },
    {
        "endpoint": "CM/LTEBackup",
        "gathers": {
            "LTE_Backup_Enable": "LTE backup enabled",
            "LTE_Backup_Mode": "LTE backup mode",
            "LTE_Connection": "LTE connection state",
            "LTE_ISP": "LTE ISP",
            "LTE_IMSI": "LTE IMSI",
            "LTE_APN": "LTE APN",
            "LTE_APN_Username": "LTE APN username",
            "LTE_APN_Password": "LTE APN password",
            "LTE_APN_Authentication": "LTE APN authentication",
            "LTE_APN_Auto": "LTE APN auto",
            "LTE_APN_ProfileName": "LTE APN profile name",
            "LTE_SignalStrength": "LTE signal strength",
            "LTE_WANIP": "LTE WAN IP",
            "Dongle_Vendor": "Dongle vendor",
            "Dongle_IMEI": "Dongle IMEI",
            "Dongle_HardwareVersion": "Dongle hardware version",
            "Dongle_SoftwareVersion": "Dongle software version",
            "Dongle_RX_Monthly": "Dongle monthly RX",
            "Dongle_TX_Monthly": "Dongle monthly TX",
        },
    },
    {
        "endpoint": "MTA/StatusInfo",
        "gathers": {
            "NumberOfSLIC": "Number of SLIC lines",
            "voiceProtocol": "Voice protocol",
            "telDhcp": "Telephony DHCP state",
            "telReg": "Telephony registration state",
            "telSec": "Telephony security state",
            "telTftp": "Telephony TFTP state",
            "Line1IMPU": "Line 1 IMPU",
            "Line1RegState": "Line 1 registration",
            "line1CallState": "Line 1 call state",
            "Line2IMPU": "Line 2 IMPU",
            "Line2RegState": "Line 2 registration",
            "line2CallState": "Line 2 call state",
            "Line3RegState": "Line 3 registration",
            "line3CallState": "Line 3 call state",
            "Line4RegState": "Line 4 registration",
            "line4CallState": "Line 4 call state",
        },
    },
    {
        "endpoint": "WiFi/Radios",
        "gathers": {
            "RadioNumberOfEntries": "Radio count",
            "Raidos_List": {
                "Radio_URI": "Radio URI",
                "band": "Band",
                "wlsOnOff": "Radio on/off",
                "wlsMode": "Wireless mode",
                "n_bandwidth": "Bandwidth",
                "wlsChannel": "Configured channel",
                "wlsCurrentChannel": "Current channel",
                "autoChannel": "Auto channel",
                "wlsDfsOnOff": "DFS",
                "wlsWpsOnOff": "WPS",
                "hasGuest": "Guest network",
                "bandsteer": "Band steering",
                "countryRegion": "Country/region",
                "igmpSnoop": "IGMP snooping",
            },
        },
    },
    {
        "endpoint": "WiFi/Client",
        "gathers": {
            "ClientNumberOfEntries": "Client count",
            "Client_List": {
                "index": "Index",
                "band": "Band",
                "ssid": "SSID",
                "mac": "MAC address",
                "hostname": "Hostname",
                "aid": "Association id",
                "rssi": "RSSI",
                "br": "Downlink rate",
                "bw": "Bandwidth",
                "pm": "Operating standard",
                "ch": "Channel",
            },
        },
    },
    {
        "endpoint": "Hosts",
        "gathers": {
            "HostNumberOfEntries": "Host count",
            "Hosts_List": {
                "hostID": "Host id",
                "hostName": "Hostname",
                "ip": "IPv4 address",
                "ipv6": "IPv6 address",
                "macAddr": "MAC address",
                "connectType": "Connection type",
                "deviceType": "Device type",
                "status": "Status",
                "rssi": "RSSI",
                "rx": "RX bytes",
                "tx": "TX bytes",
                "wlsMode": "Wireless mode",
                "Ssid": "SSID",
                "fwLevel": "Firewall level",
                "addressSource": "Address source",
                "leaseTimeRemaining": "DHCP lease remaining",
            },
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
    {
        # Only present on SKUs with WiFi. select_endpoints() drops this for a
        # CODA60/60U/60V, so a wireless-less gateway is never asked for it.
        "endpoint": "WiFi/WPS",
        "gathers": {
            "wlsWpsOnOff": "WPS Enabled",
            "wlsWpsStatus": "WPS Status",
            "wlsWpsMethod": "WPS Method",
        },
    },
    {
        # Only present on voice SKUs (CODA60V / 6011UV / 57 / EN2251-HSP).
        "endpoint": "MTA/StatusInfo",
        "gathers": {
            "telDhcp": "Telephony DHCP",
            "telSec": "Telephony Security",
            "telReg": "Telephony Registration",
            "voiceProtocol": "Voice Protocol",
            "line1CallState": "Line 1 Call State",
        },
    },
    {
        "endpoint": "USB/DeviceList",
        "gathers": {
            "ListNumberOfEntries": "USB Device Count",
        },
    },
]


def select_endpoints(profile: ModelProfile | None) -> list[dict[str, Any]]:
    """The subset of ENDPOINTS that ``profile`` can actually answer.

    ``profile=None`` returns everything (the historical behavior). A profile
    with no WiFi drops ``WiFi/WPS``; no voice drops ``MTA/StatusInfo``; no MoCA
    drops ``MoCA``; no battery/LTE drops those too. The capability requirement
    itself lives once, in ``adapters.ENDPOINTS``.
    """
    if profile is None:
        return list(ENDPOINTS)
    selected: list[dict[str, Any]] = []
    for entry in ENDPOINTS:
        endpoint = get_endpoint(f"/1/Device/{entry['endpoint']}")
        if endpoint is None or (endpoint.applies_to(profile) and endpoint.working):
            selected.append(entry)
    return selected


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
    """
    Read methods (get_all, etc.) are stateless: each opens its own short-lived
    session, no login needed, since the firmware exempts those routes from
    auth. Write/action methods (reboot, factory_reset, set_admin_password,
    ...) need a logged-in session cookie - call `await client.login(user,
    password)` first, which this client then reuses for every write call
    until you call `await client.close()` or the client is garbage collected.

    WRITE METHODS ARE REAL. This client, by design, does not gate them behind
    anything beyond "you called the method" - that decision belongs to
    whoever is calling this code, every time, not to a flag baked into the
    library. `reboot()`, `factory_reset()`, and `hard_reset_and_reboot()`
    act on a real device the instant they're awaited. See docs/KB.md §2 for
    exactly what each one does on the device (confirmed from the firmware's
    own PHP source, ModelInfo.php::doRebootAction and AdminPassword.php).
    """

    host: str = "192.168.100.1"
    timeout: float = 10.0
    # The firmware's PHP-FPM worker pool is small; CODA_60.py's own comment
    # notes going above 2 concurrent requests risks overwhelming the CM's CPU.
    max_concurrency: int = 2

    _session: aiohttp.ClientSession | None = field(default=None, init=False, repr=False)

    @property
    def base_url(self) -> str:
        return f"http://{self.host}/1/Device"

    # ---- write/action session lifecycle ---------------------------------

    async def login(self, username: str, password: str) -> bool:
        """POST /1/Device/Users/Login - the same call the browser's own login form makes.
        On success, keeps the resulting SESSION.uid cookie for every write call this client makes afterwards.
        Returns True on success. The device's own login is LAN-aware: it checks a different
        (UCI-configured) credential pair depending on whether the request's source IP is the
        gateway's own LAN address, so a login that works from one network path may not from another."""
        if self._session is None:
            self._session = aiohttp.ClientSession()
        payload = {"model": json.dumps({"username": username, "password": password})}
        async with self._session.post(
            f"{self.base_url}/Users/Login", data=payload, timeout=aiohttp.ClientTimeout(total=self.timeout)
        ) as resp:
            if resp.status != 200:
                return False
            body = await resp.json(content_type=None)
            return not (isinstance(body, dict) and body.get("errCode"))

    async def close(self) -> None:
        if self._session is not None:
            await self._session.close()
            self._session = None

    async def __aenter__(self) -> "Coda60Client":
        return self

    async def __aexit__(self, *exc) -> None:
        await self.close()

    # ---- capability detection --------------------------------------------

    async def _probe_json(self, session: aiohttp.ClientSession, path: str) -> Any:
        url = f"http://{self.host}{path}" if path.startswith("/") else f"{self.base_url}/{path}"
        try:
            async with session.get(
                url, timeout=aiohttp.ClientTimeout(total=self.timeout)
            ) as resp:
                if resp.status != 200:
                    return None
                return await resp.json(content_type=None)
        except Exception:  # noqa: BLE001 - a probe that fails is "unknown", not fatal
            return None

    async def get_identity(self) -> dict[str, Any]:
        """Merge /deviceinfo, CM/Version and Router/SysInfo into one identity dict."""
        async with aiohttp.ClientSession() as session:
            info = await self._probe_json(session, "/deviceinfo")
            version = await self._probe_json(session, "CM/Version")
            router = await self._probe_json(session, "Router/SysInfo")
        identity: dict[str, Any] = {}
        for body in (info, version, router):
            if isinstance(body, dict):
                identity.update({k: v for k, v in body.items() if k not in ("errCode", "errMsg") and v is not None})
        return identity

    async def detect_profile(self, model: str | None = None) -> ProfileMatch:
        """Read-only: probe capabilities, then resolve a model profile.

        ``model`` forces a profile id/alias (e.g. ``coda6011uv``); otherwise the
        device's ``hw_model`` (from ``/deviceinfo``) is used, falling back to a
        probe-refined generic profile.

        Probes are evidence-based, not status-based: the shared CODA6X app
        answers ``/WiFi/WPS`` and ``/MTA/StatusInfo`` on a wired-only CODA60,
        so a 200 only counts when the payload shows a real radio/SLIC.
        """
        async with aiohttp.ClientSession() as session:
            info = await self._probe_json(session, "/deviceinfo")
            version = await self._probe_json(session, "CM/Version")
            router = await self._probe_json(session, "Router/SysInfo")
            wifi_wps = await self._probe_json(session, "WiFi/WPS")
            wifi_guest = await self._probe_json(session, "WiFi/GuestSSID")
            mta = await self._probe_json(session, "MTA/StatusInfo")
            moca = await self._probe_json(session, "MoCA")
            usb = await self._probe_json(session, "USB/DeviceList")
            spectrum = await self._probe_json(session, "Spectrum")
            ds_ofdm = await self._probe_json(session, "CM/DsOfdm")
            us_ofdm = await self._probe_json(session, "CM/UsOfdm")

        identity: dict[str, Any] = {}
        for body in (info, version, router):
            if isinstance(body, dict):
                identity.update({k: v for k, v in body.items() if k not in ("errCode", "errMsg") and v is not None})

        wifi_body = {**(wifi_wps or {}), **(wifi_guest or {})}
        wifi: bool | None = None
        if wifi_body:
            wifi = any(
                isinstance(wifi_body.get(k), str) and wifi_body[k].strip()
                for k in ("bssid", "bssid5G", "ssidName", "ssidName5G", "wlsWpsState24g", "wlsWpsState5g")
            )

        voice: bool | None = None
        if isinstance(mta, dict):
            slic = mta.get("NumberOfSLIC")
            line_states = [mta.get(f"Line{i}RegState") for i in range(1, 5)]
            if (isinstance(slic, int) and slic > 0) or any(v is not None for v in line_states):
                voice = True
            elif slic is None and all(v is None for v in line_states):
                voice = False

        probes = CapabilityProbes(
            wifi=wifi,
            voice=voice,
            moca=("moca_status" in moca) if isinstance(moca, dict) else None,
            usb=(True if usb.get("ListNumberOfEntries", 0) > 0 else None) if isinstance(usb, dict) else None,            spectrum=isinstance(spectrum, (dict, list)),
            ofdm_downstream=(len(ds_ofdm.get("Freq_List") or []) > 0) if isinstance(ds_ofdm, dict) else None,
            ofdm_upstream=(len(us_ofdm.get("Freq_List") or []) > 0) if isinstance(us_ofdm, dict) else None,
        )
        return _detect_profile(identity, probes, override=model)


    async def _write(self, path: str, model: dict[str, Any]) -> dict[str, Any]:
        if self._session is None:
            raise WriteNotAuthenticated("call `await client.login(username, password)` before any write/action method")
        async with self._session.post(
            f"{self.base_url}/{path}", data={"model": json.dumps(model)}, timeout=aiohttp.ClientTimeout(total=self.timeout)
        ) as resp:
            try:
                return await resp.json(content_type=None) or {}
            except Exception:  # noqa: BLE001 - some of these actions return no body / a bare success marker
                return {"http_status": resp.status}

    # ---- write/action methods ---------------------------------------------
    #
    # All three reboot-family actions below hit the SAME endpoint,
    # POST /1/Device/CM/Reboot. ModelInfo.php::doRebootAction() reads each flag
    # in turn before it checks which one is set, and on this build a missing
    # key is a PHP fatal (`Undefined array key "reboot"`): a body carrying only
    # the flag you want never reaches its branch. `_reboot_body` always fills
    # in all three.
    #
    #   "reboot"        -> sets Device.DeviceInfo.X_HITRON_COM_MGMT.Reboot=true.
    #                      Measured inert on 8.7.1.0.1b4: errCode 000, and the
    #                      unit keeps running. Nothing consumes the flag.
    #   "factoryReset"  -> sets the matching FactoryReset flag the same way.
    #                      Same mechanism, so expect the same inert result;
    #                      not measured.
    #   "restoreReset"  -> immediate: the handler returns, sleeps 2 s, then runs
    #                      `exec('sync; ncpu_exec -e ht_reset_cmd.sh reboot')`
    #                      and `exec('reboot')`. Measured working: the socket
    #                      closes with no reply and the unit is back ~2 min
    #                      later with fresh counters. Treat it as the point of
    #                      no return - it clears the local configuration first.

    @staticmethod
    def _reboot_body(set_flag: str) -> dict[str, int]:
        body = {"reboot": 0, "factoryReset": 0, "restoreReset": 0}
        body[set_flag] = 1
        return body

    async def reboot(self) -> dict[str, Any]:
        """Flag-based reboot request. Accepted with errCode 000 but inert on 8.7.1.0.1b4."""
        return await self._write("CM/Reboot", self._reboot_body("reboot"))

    async def factory_reset(self) -> dict[str, Any]:
        """Flag-based factory-reset request. Same inert flag mechanism as `reboot`; not measured."""
        return await self._write("CM/Reboot", self._reboot_body("factoryReset"))

    async def hard_reset_and_reboot(self) -> dict[str, Any]:
        """{"restoreReset": 1} - the PHP handler execs `reboot` directly, with no TR-181
        indirection. The only reboot measured working on 8.7.1.0.1b4, and it clears the
        local configuration first. A dropped connection is the success signal."""
        try:
            return await self._write("CM/Reboot", self._reboot_body("restoreReset"))
        except (aiohttp.ClientConnectionError, ConnectionResetError):
            return {"result": "reboot requested; the device closed the connection"}

    # ---- counter maintenance ---------------------------------------------

    async def reset_fec(self) -> dict[str, Any]:
        """Clear the DOCSIS 3.0 and OFDM downstream FEC counters together.

        Both stops are bare PUTs with no body, and measured unauthenticated on
        8.7.1.0.1b4. PUT CM/DsInfo zeroes all eight 3.0 channels in one call;
        PUT CM/DsOfdm is unreliable - one call zeroes zero to two of the four
        live channels, which ones vary, and the handler ignores the body and
        query string, so there is no per-channel targeting. The OFDM stop is
        repeated (bounded) and the counters re-read, so the result reports how
        many of the channels that were counting before now read zero instead of
        claiming a clean sweep.
        """
        timeout = aiohttp.ClientTimeout(total=self.timeout)
        session = self._session or aiohttp.ClientSession(timeout=timeout)
        owns_session = self._session is None
        try:
            before_ds = self._busy_channels(await self._get_json(session, "CM/DsInfo"), "channelId")
            before_ofdm = self._busy_channels(await self._get_json(session, "CM/DsOfdm"), "index")

            await self._put_clear(session, "CM/DsInfo")
            after_ds = self._busy_channels(await self._get_json(session, "CM/DsInfo"), "channelId")
            docsis_cleared = len(before_ds - after_ds)

            best_cleared = -1
            ofdm_cleared = 0
            ofdm_not_zero: list[str] = []
            for _ in range(8):
                await self._put_clear(session, "CM/DsOfdm")
                after_ofdm = self._busy_channels(await self._get_json(session, "CM/DsOfdm"), "index")
                still = sorted(before_ofdm & after_ofdm)
                cleared = len(before_ofdm) - len(still)
                if cleared > best_cleared:
                    best_cleared = cleared
                    ofdm_cleared, ofdm_not_zero = cleared, still
                if not still:
                    break
        finally:
            if owns_session:
                await session.close()
        return {
            "docsis_busy": len(before_ds),
            "docsis_cleared": docsis_cleared,
            "ofdm_busy": len(before_ofdm),
            "ofdm_cleared": ofdm_cleared,
            "ofdm_not_zero": ofdm_not_zero,
        }

    async def _get_json(self, session: aiohttp.ClientSession, path: str) -> dict[str, Any]:
        """Read a counter route. Unlike _probe_json, a failure is an error here:
        treating a failed read as an empty payload would make a failed clear look
        like a complete one. Retries cover the firmware's occasional 500
        while it applies a clear."""
        last: Exception | None = None
        for attempt in range(3):
            if attempt:
                await asyncio.sleep(0.15)
            try:
                async with session.get(
                    f"{self.base_url}/{path}", timeout=aiohttp.ClientTimeout(total=self.timeout)
                ) as resp:
                    if resp.status != 200:
                        raise RuntimeError(f"GET {path}: HTTP {resp.status}")
                    body = await resp.json(content_type=None)
                if isinstance(body, dict) and body.get("errCode") not in (None, "", "000"):
                    raise RuntimeError(f"GET {path}: device error {body.get('errCode')} {body.get('errMsg')}")
                return body if isinstance(body, dict) else {}
            except (aiohttp.ClientError, asyncio.TimeoutError, RuntimeError) as exc:
                last = exc
        raise RuntimeError(f"GET {path} failed: {last}")

    @staticmethod
    def _busy_channels(body: Any, id_key: str) -> set[str]:
        """Channels with a non-zero counter. "NA" rows are inactive receivers,
        not clean ones, so they are skipped instead of counted either way."""
        busy: set[str] = set()
        for channel in (body or {}).get("Freq_List") or []:
            corrected = parse_counter(channel.get("correcteds"))
            uncorrect = parse_counter(channel.get("uncorrect"))
            if corrected is None and uncorrect is None:
                continue
            if corrected or uncorrect:
                busy.add(str(channel.get(id_key)))
        return busy

    async def _put_clear(self, session: aiohttp.ClientSession, path: str) -> None:
        async with session.put(
            f"{self.base_url}/{path}", timeout=aiohttp.ClientTimeout(total=self.timeout)
        ) as resp:
            if resp.status != 200:
                raise RuntimeError(f"PUT {path}: HTTP {resp.status}")
            try:
                body = await resp.json(content_type=None)
            except Exception:  # noqa: BLE001 - an empty body is still a success answer
                return
            if isinstance(body, dict) and body.get("errCode") not in (None, "", "000"):
                raise RuntimeError(f"PUT {path}: device error {body.get('errCode')} {body.get('errMsg')}")

    async def set_admin_password(self, old_password: str, new_password: str, new_password_confirm: str | None = None, new_username: str | None = None) -> dict[str, Any]:
        """POST /1/Device/Manage/Password (AdminPassword.php::handlePasswordChange).
        new_password_confirm defaults to new_password if omitted. A wrong old_password
        is the device's own job to reject - this client does not pre-validate it."""
        return await self._write(
            "Manage/Password",
            {
                "oldPassword": old_password,
                "newPassword": new_password,
                "newPasswordConfirm": new_password_confirm or new_password,
                "newUsername": new_username or "",
            },
        )

    async def set_admin_username(self, new_username: str) -> dict[str, Any]:
        """POST /1/Device/Manage/Username (AdminPassword.php::handleUsernameChange)."""
        return await self._write("Manage/Username", {"newUsername": new_username})

    async def set_wifi_radio(self, radio_index: int, settings: dict[str, Any]) -> dict[str, Any]:
        """PUT /1/Device/WiFi/Radios/{index} (RadioInfo.php::put, server-validated via
        Utils::checkRadioValidate). `radio_index` is the 1-based index from get_all()'s
        WiFi radio list, not an interface name. `settings` is whatever subset of fields
        that endpoint accepts - not independently enumerated here; read a radio's current
        shape with get_all() first and send back a modified copy of it."""
        if self._session is None:
            raise WriteNotAuthenticated("call `await client.login(username, password)` before any write/action method")
        async with self._session.put(
            f"{self.base_url}/WiFi/Radios/{radio_index}",
            data={"model": json.dumps(settings)},
            timeout=aiohttp.ClientTimeout(total=self.timeout),
        ) as resp:
            try:
                return await resp.json(content_type=None) or {}
            except Exception:  # noqa: BLE001
                return {"http_status": resp.status}

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

    async def get_all(
        self,
        endpoints: list[dict] | None = None,
        profile: ModelProfile | None = None,
    ) -> dict[str, Any]:
        """Fetch every applicable endpoint concurrently, bounded by max_concurrency.

        Pass ``profile`` (from ``detect_profile()``) to skip endpoints this SKU
        cannot answer — no WiFi/MoCA/voice/LTE/USB probes on a model without
        them. With no profile, every known endpoint is fetched (legacy shape).
        """
        if endpoints is None:
            endpoints = select_endpoints(profile)
        sem = asyncio.Semaphore(self.max_concurrency)
        async with aiohttp.ClientSession() as session:
            tasks = [self._fetch_one(session, ed, sem) for ed in endpoints]
            results = await asyncio.gather(*tasks)
        return {
            "metadata": {
                "timestamp": datetime.now(timezone.utc).isoformat(),
                "device_ip": self.host,
                "model_profile": profile.id if profile else None,
                "capabilities": sorted(profile.capabilities) if profile else None,
            },
            "metrics": dict(results),
        }
