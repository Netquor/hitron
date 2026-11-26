"""
Async client for the Hitron MoCA adapter's embedded web UI (HT-EM5 and similar,
"InterNiche Technologies WebServer" firmware).

--------------------------------------------------------------------------
How the device's web UI actually works (reverse engineered from a HAR
capture of index.html / devSetup.html / moca.html / phyRates.html):

  * There is no real REST API - the pages POST to a tiny RPC endpoint:

        POST /ms/<type>/<reg>[/GET|/PUT]
        Content-Type: application/x-www-form-urlencoded   (body is actually JSON!)
        X-CSRF-TOKEN: <token from the csrf_token cookie>

        body:  {"data": []}            # plain read
               {"data": [nodeId]}      # read that takes a parameter (e.g. per-node info)
               {"data": [value]}       # write (PUT)

    Response:  {"data": ["0xXXXXXXXX", ...]}   - always a list of 32-bit values
               encoded as lower-case 0x-prefixed hex strings.

  * "<type>" is a small namespace id (0 = MoCA/PHY MIB objects, 1 = system /
    network config objects). "<reg>" is the object id, in hex or decimal -
    the firmware doesn't care which, e.g. 0x1063 and 4195 are the same
    register.

  * Some of the low, "classic" MoCA MIB registers (0x14, 0x15, 0x16, 0x24,
    0x7f, 0x1D) are called with no trailing verb at all; everything else
    (system config, 0x1000+ MoCA config objects) uses an explicit /GET or
    /PUT suffix. Both forms are reproduced faithfully below rather than
    guessed.

  * Multi-value registers are just flat arrays of hex words that main.js /
    moca.js / index.html's inline scripts hand-decode with bit shifts,
    ASCII unpacking (byte2ascii/hex2ascii) and per-field magic offsets.
    This file re-implements those decodes in Python.

  * Authentication: the page JS reads a `csrf_token` cookie and echoes it
    back as `X-CSRF-TOKEN`. The captured HAR didn't include a Set-Cookie
    (likely stripped by the capture tool), so this client fetches
    index.html once at connect time and pulls whatever cookie the server
    sets out of the cookie jar, the same way the browser does. If the
    device doesn't actually enforce the token, requests still succeed
    with an empty token.

  * There's no login page/form in the capture because there isn't one -
    the device gates the whole web UI behind HTTP Basic Auth (the
    browser's native credential prompt, sent as a plain `Authorization:
    Basic ...` header on every request), which HAR capture tools
    typically don't record. Default credentials on these units are
    admin/admin, which is what this client uses unless you override
    `username`/`password`.
--------------------------------------------------------------------------

Register map
------------
type 0 (MoCA / PHY MIB objects):
    0x15   LOCAL_INFO          local node info block (29 words)
    0x16   NET_INFO            per-node info, param: [nodeId]        (26 words)
    0x14   FRAME_INFO          ethernet tx/rx frame counters
    0x24   MISC_PHY_INFO       misc PHY info (beacon channel/LOF temp)
    0x7f   MISC_M25_PHY_INFO   MXL371x-only extra PHY info (first/num channel)
    0x1D   FMR_INFO            FMR (PHY rate) payload, params: [nodeMask, mocaVer]
    0x1001 NW_SEARCH           network search enabled (bool)
    0x1002 PREFERRED_NC        preferred network coordinator (bool)
    0x1003 LOF                 LOF / operating frequency (MHz)
    0x1009 BAND_MASK           enabled band scan mask (D-Ext/D-Low/D-High)
    0x100e BEACON_PWR_LEVEL    beacon power level index
    0x1049 MOCA_PASSWORD_BASE  +0/1/2 = per-band MoCA privacy password (write-only use)
    0x1059 SECURITY_MODE_MASK  per-band "privacy enabled" bitmask
    0x1063 SCAN_MASK_BASE      +0/1/2 per-band scan mask (64-bit, 2 words)
    0x106b SCAN_OFFSET_BASE    +0/1/2 per-band scan offset
    0x1073 PRI_CH_ABOVE_MASK_BASE +0/1/2
    0x107b PRI_CH_BELOW_MASK_BASE +0/1/2
    0x10a8 TX_POWER            tx power index
    0x10f5 HW_CFG_WORD         hardware band capability bitmask
    lastOFrq                   last operating frequency (named endpoint, no type/reg)

type 1 (system / network config objects):
    0x102  HW_VERSION          hardware version (ASCII, packed in hex words)
    0x20b  IP_ADDR             device IP address
    0x20e  ADMIN_PASSWORD      admin password hash (write-only; not read by this client)
    0x210  NET_MASK            device netmask
    0x211  GATEWAY             default gateway
    0x212  ADAPTER_NAME        "room name" index (myRoom array in moca.js)
    0x216  TELNET_ENABLE       telnet enabled (bool)
    0x217  DHCP_ENABLE         DHCP client enabled (bool)
    0x218  AUTOIP_ENABLE       auto-IP (APIPA) enabled (bool)
    0x302  SW_VERSION          firmware version
    0x303  CHIP_ID             chipset id (-> chipArray lookup)
    0xb00  REBOOT              (write-only, triggers a reboot - NOT exposed by default)
"""

from __future__ import annotations

import asyncio
import json
import time
from dataclasses import dataclass, field
from typing import Any, Optional

import aiohttp

# --------------------------------------------------------------------------
# Register map
# --------------------------------------------------------------------------

TYPE_MOCA = 0
TYPE_SYS = 1

# Registers that are called with NO /GET suffix (matches captured traffic).
# Everything else uses an explicit /GET (or /PUT for writes) suffix.
_NO_SUFFIX_REGS = {(TYPE_MOCA, 0x14), (TYPE_MOCA, 0x15), (TYPE_MOCA, 0x16),
                    (TYPE_MOCA, 0x24), (TYPE_MOCA, 0x7F), (TYPE_MOCA, 0x1D)}

# Registers whose HTML hidden <input name="data"> defaults to a literal
# value (e.g. value="0") that the page JS never overrides before firing
# the call - the browser always sends that literal, not an empty array.
# Sending [] instead of [0] here gets rejected with a 400 (confirmed against
# a real device). NET_INFO also defaults to "0" but is always given an
# explicit node id by callers, so it's handled via the `node=` param instead.
_DEFAULT_DATA = {
    "FRAME_INFO": [0],
    "CHIP_ID": [0],
}

REG = {
    # type 0 - MoCA / PHY
    "LOCAL_INFO": (TYPE_MOCA, 0x15),
    "NET_INFO": (TYPE_MOCA, 0x16),
    "FRAME_INFO": (TYPE_MOCA, 0x14),
    "MISC_PHY_INFO": (TYPE_MOCA, 0x24),
    "MISC_M25_PHY_INFO": (TYPE_MOCA, 0x7F),
    "FMR_INFO": (TYPE_MOCA, 0x1D),
    "NW_SEARCH": (TYPE_MOCA, 0x1001),
    "PREFERRED_NC": (TYPE_MOCA, 0x1002),
    "LOF": (TYPE_MOCA, 0x1003),
    "BAND_MASK": (TYPE_MOCA, 0x1009),
    "BEACON_PWR_LEVEL": (TYPE_MOCA, 0x100E),
    "TX_POWER": (TYPE_MOCA, 0x10A8),
    "HW_CFG_WORD": (TYPE_MOCA, 0x10F5),
    "SECURITY_MODE_MASK": (TYPE_MOCA, 0x1059),
    "MOCA_PASSWORD_BASE": (TYPE_MOCA, 0x1049),
    "SCAN_MASK_BASE": (TYPE_MOCA, 0x1063),
    "SCAN_OFFSET_BASE": (TYPE_MOCA, 0x106B),
    "PRI_CH_ABOVE_MASK_BASE": (TYPE_MOCA, 0x1073),
    "PRI_CH_BELOW_MASK_BASE": (TYPE_MOCA, 0x107B),
    # type 1 - system / network
    "HW_VERSION": (TYPE_SYS, 0x102),
    "IP_ADDR": (TYPE_SYS, 0x20B),
    "NET_MASK": (TYPE_SYS, 0x210),
    "GATEWAY": (TYPE_SYS, 0x211),
    "ADAPTER_NAME": (TYPE_SYS, 0x212),
    "TELNET_ENABLE": (TYPE_SYS, 0x216),
    "DHCP_ENABLE": (TYPE_SYS, 0x217),
    "AUTOIP_ENABLE": (TYPE_SYS, 0x218),
    "SW_VERSION": (TYPE_SYS, 0x302),
    "CHIP_ID": (TYPE_SYS, 0x303),
    # write-only / dangerous - not called by any getter in this client
    "ADMIN_PASSWORD": (TYPE_SYS, 0x20E),
    "REBOOT": (TYPE_SYS, 0xB00),
}

CHIP_NAMES = ["MXL370x", "MXL371x", "UNKNOWN"]
BAND_NAMES = ["D-Ext", "D-Low", "D-High"]
ROOM_NAMES = ["Master", "LivingRoom", "TVRoom", "GameRoom", "Office", "Den",
              "Room1", "Room2", "Room3", "Room4", "Room5", "Kids1", "Kids2",
              "Bedroom1", "Bedroom2", "Office1", "Office2"]

MAX_NODES = 16
LDPC_LEN_100MHZ = 3900
LDPC_LEN_50MHZ = 1200
FFT_LEN_100MHZ = 512
FFT_LEN_50MHZ = 256


# --------------------------------------------------------------------------
# Small binary/ASCII helpers, ported 1:1 from main.js / index.html / moca.js
# --------------------------------------------------------------------------

def h2i(hexstr: str) -> int:
    """hex string ("0x1234abcd") -> unsigned 32-bit int."""
    return int(hexstr, 16) & 0xFFFFFFFF


def moca_version(word: int) -> str:
    """Decode a packed MoCA version nibble pair, e.g. 0x25 -> '2.5'."""
    return f"{(word >> 4) & 0xF}.{word & 0xF}"


def byte2ascii(hi_hex_or_int) -> str:
    """Unpack a 32-bit word into up to 4 ASCII bytes, stopping at the first
    non-printable byte (mirrors main.js byte2ascii)."""
    val = hi_hex_or_int if isinstance(hi_hex_or_int, int) else int(hi_hex_or_int, 16)
    out = []
    for i in range(4):
        b = (val >> ((3 - i) * 8)) & 0xFF
        if not (0 < b < 0x80):
            break
        out.append(chr(b))
    return "".join(out)


def decode_soc_version(local_info: list[str], start: int = 21) -> str:
    """LOCAL_INFO words from `start` on hold a NUL-terminated ASCII string,
    4 bytes per word (index.html's refreshPage do/while loop)."""
    out = ""
    i = start
    while i < len(local_info):
        chunk = byte2ascii(local_info[i])
        if not chunk:
            break
        out += chunk
        i += 1
    return out


def hex2mac(hi: int, lo: int) -> str:
    return ":".join(f"{b:02x}" for b in [
        (hi >> 24) & 0xFF, (hi >> 16) & 0xFF, (hi >> 8) & 0xFF, hi & 0xFF,
        (lo >> 24) & 0xFF, (lo >> 16) & 0xFF,
    ])


def int2ip(val: int) -> str:
    return f"{(val >> 24) & 0xFF}.{(val >> 16) & 0xFF}.{(val >> 8) & 0xFF}.{val & 0xFF}"


def parse_sw_version(word: int) -> str:
    return f"{(word >> 24) & 0xFF}.{(word >> 16) & 0xFF}.{(word >> 8) & 0xFF}"


# --------------------------------------------------------------------------
# Client
# --------------------------------------------------------------------------

@dataclass
class HitronMocaClient:
    host: str
    # The web UI prompts for HTTP Basic Auth (browser's native login dialog,
    # not a form) - default credentials on these units are admin/admin.
    username: Optional[str] = "admin"
    password: Optional[str] = "admin"
    timeout: float = 5.0
    scheme: str = "http"
    # The device runs a very lightweight embedded TCP/IP stack
    # ("InterNiche Technologies WebServer" per its response headers) that
    # cannot reliably handle overlapping requests - hitting it with several
    # concurrent POSTs (as asyncio.gather naturally does) causes it to
    # start returning 400 Bad Request even for well-formed bodies. This
    # semaphore serializes actual on-the-wire requests to the device while
    # still letting the higher-level methods express "get all of this"
    # concurrently in code; raise it only if you've confirmed your unit
    # tolerates more than one in-flight request.
    max_concurrency: int = 1

    _session: Optional[aiohttp.ClientSession] = field(default=None, init=False, repr=False)
    _csrf_token: str = field(default="", init=False, repr=False)
    _sem: Optional[asyncio.Semaphore] = field(default=None, init=False, repr=False)

    @property
    def base_url(self) -> str:
        return f"{self.scheme}://{self.host}"

    async def __aenter__(self) -> "HitronMocaClient":
        await self.connect()
        return self

    async def __aexit__(self, *exc):
        await self.close()

    async def connect(self) -> None:
        auth = aiohttp.BasicAuth(self.username, self.password) if self.username else None
        self._sem = asyncio.Semaphore(self.max_concurrency)
        self._session = aiohttp.ClientSession(
            auth=auth,
            timeout=aiohttp.ClientTimeout(total=self.timeout),
            cookie_jar=aiohttp.CookieJar(unsafe=True),
        )
        # Load index.html once, the same way the browser does, so the
        # server can hand us a csrf_token cookie if it uses one.
        try:
            async with self._session.get(f"{self.base_url}/index.html") as resp:
                await resp.read()
        except aiohttp.ClientError:
            pass
        for cookie in self._session.cookie_jar:
            if cookie.key == "csrf_token":
                self._csrf_token = cookie.value
                break

    async def close(self) -> None:
        if self._session:
            await self._session.close()
            self._session = None

    # ---- low level -------------------------------------------------

    async def _rpc(self, type_: int, reg: int, method: str = "GET",
                   data: Optional[list[Any]] = None,
                   data2: Optional[list[Any]] = None) -> list[str]:
        assert self._session is not None, "call connect() / use `async with` first"

        path = f"/ms/{type_}/{reg if isinstance(reg, str) else hex(reg)}"
        if (type_, reg) not in _NO_SUFFIX_REGS:
            path += f"/{method}"

        body: dict[str, Any] = {"data": data or []}
        if data2 is not None:
            body["data2"] = data2

        # IMPORTANT: the device expects the exact compact JSON body the
        # browser sends (e.g. `{"data":[]}`, no whitespace) under a
        # x-www-form-urlencoded Content-Type - it is NOT a real form body
        # and it is NOT real application/json. Using aiohttp's `json=`
        # kwarg here forces `Content-Type: application/json; charset=...`
        # (regardless of an explicit header override) and pretty-printed
        # spacing, which this firmware rejects with a 400. So we build
        # the payload by hand and send it as raw `data=`.
        payload = json.dumps(body, separators=(",", ":"))

        headers = {
            "Content-Type": "application/x-www-form-urlencoded",
            "Accept": "text/html, */*",
            "Accept-Language": "en-US,en;q=0.9",
            "X-CSRF-TOKEN": self._csrf_token,
            # The device checks these on RPC calls (every captured request
            # had them - a bare XHR with no Origin/Referer gets a 400).
            # index.html is a safe generic value; the firmware doesn't
            # appear to need the specific page the field "belongs" to.
            "Origin": self.base_url,
            "Referer": f"{self.base_url}/index.html",
            "User-Agent": ("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
                           "AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"),
        }

        assert self._sem is not None
        async with self._sem:
            async with self._session.post(f"{self.base_url}{path}", data=payload, headers=headers) as resp:
                if resp.status == 400:
                    # Surface whatever the InterNiche server said, if anything -
                    # helps diagnose which specific check is failing.
                    text = await resp.text()
                    raise aiohttp.ClientResponseError(
                        resp.request_info, resp.history, status=resp.status,
                        message=f"Bad request calling {path} with body {payload!r}: {text!r}",
                        headers=resp.headers)
                resp.raise_for_status()
                payload_resp = await resp.json(content_type=None)
                return payload_resp.get("data", [])

    async def read(self, name: str, *, node: Optional[int] = None) -> list[str]:
        """Read a named register. `node` supplies the [nodeId] parameter for
        registers like NET_INFO that require one."""
        type_, reg = REG[name]
        if node is not None:
            data = [node]
        else:
            data = list(_DEFAULT_DATA.get(name, []))
        return await self._rpc(type_, reg, "GET", data=data)

    async def read_raw(self, type_: int, reg: int, *, node: Optional[int] = None) -> list[str]:
        data = [node] if node is not None else []
        return await self._rpc(type_, reg, "GET", data=data)

    async def write(self, name: str, value: int, *, node: Optional[int] = None) -> list[str]:
        """Write a named register (PUT). Not used by any of the read-only
        getters below; provided for completeness. Refuses to touch
        ADMIN_PASSWORD/REBOOT to avoid accidental misuse from a monitoring
        context - call `_rpc` directly if you really mean to."""
        if name in ("ADMIN_PASSWORD", "REBOOT"):
            raise ValueError(f"{name} is intentionally not writable via write(); use _rpc() directly")
        type_, reg = REG[name]
        data = ([node] if node is not None else []) + [value]
        return await self._rpc(type_, reg, "PUT", data=data)

    async def read_last_ofrq(self) -> int:
        data = await self._rpc(TYPE_MOCA, "lastOFrq", "GET")
        return h2i(data[0]) if data else 0

    # ---- high level: device status (index.html) ---------------------

    async def get_device_status(self) -> dict[str, Any]:
        """Everything shown on the Status page: model/version info, MoCA
        link status, channel plan, ethernet counters."""
        (local_info, misc_phy, hw_version, sw_version,
         chip_id, misc_m25, lof, ip_addr) = await asyncio.gather(
            self.read("LOCAL_INFO"),
            self.read("MISC_PHY_INFO"),
            self.read("HW_VERSION"),
            self.read("SW_VERSION"),
            self.read("CHIP_ID"),
            self.read("MISC_M25_PHY_INFO"),
            self.read("LOF"),
            self.read("IP_ADDR"),
        )

        my_node_id = h2i(local_info[0])
        link_status = "Up" if h2i(local_info[5]) != 0 else "Down"
        nw_moca_word = h2i(local_info[11]) & 0xFF
        network_moca_version = moca_version(nw_moca_word)

        # This node's own network info (needs node id as parameter)
        net_info = await self.read("NET_INFO", node=my_node_id)
        mac_address = hex2mac(h2i(net_info[0]), h2i(net_info[1]))
        node_moca_version = moca_version(h2i(net_info[4]) & 0xFF)

        frame_info = await self.read("FRAME_INFO")
        eth_tx = {
            "good": h2i(frame_info[6]),
            "bad": h2i(frame_info[15]),
            "dropped": h2i(frame_info[24]),
        }
        eth_rx = {
            "good": h2i(frame_info[33]),
            "bad": h2i(frame_info[42]),
            "dropped": h2i(frame_info[51]),
        }

        chip_id_val = h2i(chip_id[0])
        chip_idx = min(chip_id_val - 0x15, len(CHIP_NAMES) - 1)
        chip_idx = max(chip_idx, 0)
        chip_name = CHIP_NAMES[chip_idx]
        soc_version = f"{chip_name}.{decode_soc_version(local_info)}"

        beacon_channel = h2i(misc_phy[1])
        primary_lof_offset = h2i(local_info[18])
        secondary_lof_offset = h2i(local_info[19])
        primary_lof = {0: beacon_channel, 1: beacon_channel + 25, 2: beacon_channel - 25}.get(primary_lof_offset, 0)
        secondary_lof = {1: primary_lof - 125, 2: primary_lof + 125}.get(secondary_lof_offset, 0)

        if chip_name == "MXL370x":
            first_channel: Any = None
            num_channels: Any = None
        else:
            first_channel = h2i(misc_m25[2])
            num_channels = h2i(misc_m25[3])

        return {
            "model_name": "HT-EM5",
            "hardware_version": hw_version[0][8:].upper() if hw_version else None,
            "software_version": parse_sw_version(h2i(sw_version[0])),
            "soc_version": soc_version,
            "device_moca_version": node_moca_version,
            "network_moca_version": network_moca_version,
            "ip_address": int2ip(h2i(ip_addr[0])),
            "mac_address": mac_address,
            "link_status": link_status,
            "beacon_channel_mhz": beacon_channel,
            "primary_channel_mhz": primary_lof,
            "secondary_channel_mhz": secondary_lof,
            "first_channel": first_channel,
            "num_channels": num_channels,
            "lof_mhz": h2i(lof[0]),
            "my_node_id": my_node_id,
            "ethernet_tx": eth_tx,
            "ethernet_rx": eth_rx,
        }

    # ---- high level: network config (devSetup.html) ------------------

    async def get_network_config(self) -> dict[str, Any]:
        dhcp, autoip, telnet, ip_addr, netmask, gateway = await asyncio.gather(
            self.read("DHCP_ENABLE"),
            self.read("AUTOIP_ENABLE"),
            self.read("TELNET_ENABLE"),
            self.read("IP_ADDR"),
            self.read("NET_MASK"),
            self.read("GATEWAY"),
        )
        return {
            "dhcp_enabled": bool(h2i(dhcp[0])),
            "autoip_enabled": bool(h2i(autoip[0])),
            "telnet_enabled": bool(h2i(telnet[0])),
            "ip_address": int2ip(h2i(ip_addr[0])),
            "netmask": int2ip(h2i(netmask[0])),
            "gateway": int2ip(h2i(gateway[0])),
        }

    # ---- high level: MoCA config (moca.html, minus passwords) ---------

    async def get_moca_config(self) -> dict[str, Any]:
        (band_mask, tx_power, beacon_pwr, adapter_name, nw_search, lof,
         preferred_nc, hw_cfg_word, security_mask) = await asyncio.gather(
            self.read("BAND_MASK"),
            self.read("TX_POWER"),
            self.read("BEACON_PWR_LEVEL"),
            self.read("ADAPTER_NAME"),
            self.read("NW_SEARCH"),
            self.read("LOF"),
            self.read("PREFERRED_NC"),
            self.read("HW_CFG_WORD"),
            self.read("SECURITY_MODE_MASK"),
        )
        band_mask_val = h2i(band_mask[0])
        hw_cfg_val = h2i(hw_cfg_word[0])
        security_val = h2i(security_mask[0])

        bands = []
        for idx, name in enumerate(BAND_NAMES):
            bands.append({
                "band": name,
                "supported": bool((hw_cfg_val >> (31 - idx)) & 1),
                "enabled": bool((band_mask_val >> idx) & 1),
                "privacy_enabled": bool((security_val >> idx) & 1),
            })

        room_idx = h2i(adapter_name[0])
        room_name = ROOM_NAMES[room_idx] if room_idx < len(ROOM_NAMES) else str(room_idx)

        return {
            "adapter_name": room_name,
            "lof_mhz": h2i(lof[0]),
            "network_search_enabled": bool(h2i(nw_search[0]) & 1),
            "preferred_nc": bool(h2i(preferred_nc[0]) & 1),
            "tx_power_index": h2i(tx_power[0]),
            "beacon_power_level_index": h2i(beacon_pwr[0]),
            "bands": bands,
        }

    # ---- high level: node table ---------------------------------------

    async def get_nodes(self) -> list[dict[str, Any]]:
        """All nodes currently on the MoCA network (this device's view)."""
        local_info = await self.read("LOCAL_INFO")
        node_bitmask = h2i(local_info[12])
        nc_node_id = h2i(local_info[1]) & 0xFF
        my_node_id = h2i(local_info[0])

        node_ids = [i for i in range(MAX_NODES) if (node_bitmask >> i) & 1]
        infos = await asyncio.gather(*(self.read("NET_INFO", node=n) for n in node_ids))

        nodes = []
        for node_id, info in zip(node_ids, infos):
            mac = hex2mac(h2i(info[0]), h2i(info[1]))
            nodes.append({
                "node_id": node_id,
                "mac_address": mac,
                "moca_version": moca_version(h2i(info[4]) & 0xFF),
                "is_network_coordinator": node_id == nc_node_id,
                "is_self": node_id == my_node_id,
            })
        return nodes

    # ---- high level: PHY rate matrix (phyRates.html) -------------------
    #
    # This ports the bit-level FMR-payload decode from phyRates.html.
    # It's inherently fiddly (word alignment flips per node pair, and the
    # field layout differs between MoCA 1.x / 2.0 / 2.5), so treat unusual
    # values (0 where a real link exists) as a sign the on-device format
    # differs from this firmware version rather than a client bug.

    @staticmethod
    def _phy_rate_from_gap_ofdm(gap: int, ofdm_bits: int, hundred_mhz: bool) -> int:
        if gap == 0:
            return 0
        if hundred_mhz:
            return (LDPC_LEN_100MHZ * ofdm_bits) // ((FFT_LEN_100MHZ + (gap + 10) * 2) * 46)
        return (LDPC_LEN_50MHZ * ofdm_bits) // ((FFT_LEN_50MHZ + (gap * 2 + 10)) * 26)

    async def get_phy_rates(self) -> dict[str, Any]:
        local_info = await self.read("LOCAL_INFO")
        node_bitmask = h2i(local_info[12])
        net_moca_ver = h2i(local_info[11]) & 0xFF
        node_ids = [i for i in range(MAX_NODES) if (node_bitmask >> i) & 1]

        net_infos = dict(zip(node_ids, await asyncio.gather(
            *(self.read("NET_INFO", node=n) for n in node_ids))))
        node_moca_ver = {n: h2i(net_infos[n][4]) & 0xFF for n in node_ids}

        nc_node_id = h2i(local_info[1]) & 0xFF
        nc_moca_ver = node_moca_ver.get(nc_node_id, 0x20)

        fmr_type = 2 if min(nc_moca_ver, max(node_moca_ver.values(), default=0)) >= 0x20 else 1
        fmr_infos = {}
        fmr_results = await asyncio.gather(*(
            self._rpc(TYPE_MOCA, REG["FMR_INFO"][1], "GET",
                      data=[1 << n], data2=[fmr_type])
            for n in node_ids
        ))
        for n, words in zip(node_ids, fmr_results):
            fmr_infos[n] = [h2i(w) for w in words]

        rate_matrix: dict[int, dict[int, int]] = {n: {} for n in node_ids}
        rate_gcd: dict[int, int] = {}

        for node_id in node_ids:
            entry_ver = min(node_moca_ver[node_id], nc_moca_ver)
            read_idx = 10
            aligned = True
            words = fmr_infos[node_id]

            for jd in node_ids:
                if nc_moca_ver < 0x20:
                    fmr_ver = min(entry_ver, node_moca_ver[jd])
                else:
                    fmr_ver = node_moca_ver[node_id]

                if fmr_ver in (0x20, 0x25):
                    if aligned:
                        w0 = words[read_idx] if read_idx < len(words) else 0
                        w1 = words[read_idx + 1] if read_idx + 1 < len(words) else 0
                        gap_nper = (w0 >> 24) & 0xFF
                        ofdm_nper = w0 & 0xFFFF
                        ofdm_vlper = (w1 >> 16) & 0xFFFF
                        read_idx += 1
                    else:
                        w0 = words[read_idx] if read_idx < len(words) else 0
                        w1 = words[read_idx + 1] if read_idx + 1 < len(words) else 0
                        gap_nper = (w0 >> 8) & 0xFF
                        ofdm_nper = (w1 >> 16) & 0xFFFF
                        ofdm_vlper = w1 & 0xFFFF
                        read_idx += 2
                    gap_vlper = gap_nper  # NPER/VLPER share the gap field on 2.x
                else:
                    w0 = words[read_idx] if read_idx < len(words) else 0
                    if aligned:
                        gap_nper = (w0 & 0xF8000000) >> 27
                        ofdm_nper = (w0 & 0x07FF0000) >> 16
                    else:
                        gap_nper = (w0 & 0x0000F800) >> 11
                        ofdm_nper = w0 & 0x000007FF
                        read_idx += 1
                    gap_vlper = 0
                    ofdm_vlper = 0

                aligned = not aligned
                hundred = fmr_ver != 0x10  # anything but pure MoCA1.x uses 100MHz math above
                rate = self._phy_rate_from_gap_ofdm(gap_nper, ofdm_nper, hundred)
                rate_matrix[node_id][jd] = rate

                if node_id == jd:
                    rate_gcd[node_id] = rate

        return {
            "nodes": node_ids,
            "rate_mbps": rate_matrix,
            "gcd_rate_mbps": rate_gcd,
        }

    # ---- everything at once, fired concurrently ------------------------

    async def get_all(self) -> dict[str, Any]:
        t0 = time.monotonic()
        status, net_cfg, moca_cfg, nodes = await asyncio.gather(
            self.get_device_status(),
            self.get_network_config(),
            self.get_moca_config(),
            self.get_nodes(),
        )
        result = {
            "host": self.host,
            "collected_at": time.time(),
            "collection_seconds": None,  # filled in below
            "status": status,
            "network": net_cfg,
            "moca": moca_cfg,
            "nodes": nodes,
        }
        result["collection_seconds"] = round(time.monotonic() - t0, 3)
        return result