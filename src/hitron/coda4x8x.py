"""
EXPERIMENTAL, READ-ONLY async client for the Hitron "CODA 4x8x" family
(CODA-4582, CODA-4680 and relatives).

Status: experimental: not verified on live hardware.

Hypothesis, not fact: the 4x8x serves the same REST data model our CODA6X
client already parses (/CM/DsInfo, /CM/UsInfo, /CM/DsOfdm, /CM/UsOfdm, /CM/Log,
/CM/SysInfo, /CM/Version, /Router/SysInfo, /Hosts, /WiFi/..., ...), but from the
site ROOT, i.e. without the `/1/Device` prefix that `coda60.py` uses. The
evidence is identical route names and an identical login shape. The counter-
evidence is that the field names behind that were reconstructed from a third-
party client rather than captured from a unit, so nothing here is confirmed on
hardware. Expect to adjust it the first time it meets a real 4x8x.

Facts (route list, login flow) are borrowed from the MIT-licensed project
hairyhenderson/hitron_coda (Go; written against a "CODA-4680 fw 7.1.1.2.2b9").
No code is taken from it. Credit goes to that project.

get_all() polls only the 13 routes that overlap the CODA6X field tables; the
other 18 of the 31 READ_ROUTES are reachable through get_json() as raw reads.
get_json() refuses any route outside READ_ROUTES.

Auth, also unconfirmed: `POST /Users/Login` with a form body `model=<json
{"username","password"}>` should set a session cookie; `GET /Users/CSRF` should
return the token in `Csrf_token` or `token`. A redirect (302) on a data request
is read as "not logged in". `login()` and `csrf()` exist only so authenticated
READS are possible.

DELIBERATELY NO WRITE METHODS HERE, and none are inherited. `coda60.py` has
reboot/factory-reset/password methods that were measured on a CODA6X; nothing
comparable is known for this family, and guessing a write URL against a real,
in-service modem is not acceptable. Asking this client for reboot(),
factory_reset(), set_admin_password() and friends raises an AttributeError that
says so. The CSRF token is returned to the caller but never sent by this
client, because it only ever issues GET requests (plus the one login POST).
"""

from __future__ import annotations

import asyncio
import json
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

import aiohttp

from .coda60 import ENDPOINTS as _CODA60_ENDPOINTS, clean_value

__all__ = [
    "Coda4x8xClient",
    "Coda4x8xError",
    "Coda4x8xNotLoggedIn",
    "READ_ROUTES",
    "STATUS",
    "detect_api_root",
]

STATUS = "experimental: not verified on live hardware"

# The 31 GET routes named by the source client, relative to the site root.
READ_ROUTES: tuple[str, ...] = (
    # cm
    "CM/DocsisProvision",
    "CM/DsInfo",
    "CM/DsOfdm",
    "CM/Log",
    "CM/SysInfo",
    "CM/UsInfo",
    "CM/UsOfdm",
    "CM/Version",
    # router
    "Router/Capability",
    "Router/DMZ",
    "Router/Location",
    "Router/PortForward/Status",
    "Router/PortForward/all",
    "Router/PortTrigger/Status",
    "Router/PortTrigger/all",
    "Router/SysInfo",
    "Router/TR069",
    # net
    "DDNS",
    "DNS",
    "Hosts",
    "Time",
    # wifi
    "WiFi/AccessControl",
    "WiFi/AccessControl/Status",
    "WiFi/Client",
    "WiFi/GuestSSID",
    "WiFi/Radios",
    "WiFi/Radios/Advanced",
    "WiFi/Radios/Survey",
    "WiFi/SSIDs",
    "WiFi/WPS",
    # users
    "Users/CSRF",
)

# What get_all() polls: the 13 routes where the CODA6X field tables overlap the
# 31 READ_ROUTES, so the snapshot keys match the existing metric mappers. The
# other 18 routes (Router/Capability, DMZ, DNS, Users/CSRF, ...) are raw reads
# through get_json() only. Using the CODA6X tables also bakes in the "same data
# model" hypothesis from the module docstring.
ENDPOINTS: list[dict[str, Any]] = [e for e in _CODA60_ENDPOINTS if e["endpoint"] in READ_ROUTES]

_CM_VERSION_FIELDS = ("modelName", "vendorName", "hwVersion", "swVersion", "serialNumber")
_CODA57_SYSINFO_FIELDS = ("hwVersion", "swVersion", "serialNumber", "systemUptime", "rfMac", "modelName", "vendorName")

_REDIRECTS = {301, 302, 303, 307, 308}

# Names a user could reasonably expect from a CODA6X client. They are refused
# loudly instead of with a bare "no attribute".
_ACTION_NAMES = frozenset(
    {
        "reboot",
        "factory_reset",
        "hard_reset_and_reboot",
        "set_admin_password",
        "set_admin_username",
        "set_wifi_radio",
        "reset_fec",
        "update_radio",
        "post",
        "put",
        "delete",
        "write",
    }
)


class Coda4x8xError(RuntimeError):
    """A request to a CODA 4x8x failed or answered something unexpected."""


class Coda4x8xNotLoggedIn(Coda4x8xError):
    """The device redirected (302) instead of answering: no valid session."""


def _looks_like_cm_version(body: Any) -> bool:
    """True only for a JSON object carrying at least one real CM version field
    (an errCode on its own is not enough: a generic error envelope has one).
    An HTML page, a list or a bare scalar never matches."""
    if not isinstance(body, dict):
        return False
    return any(k in body for k in _CM_VERSION_FIELDS)


def _looks_like_coda57_sysinfo(body: Any) -> bool:
    """True only for a dict with at least one known getSysInfo field, so a
    catch-all {"error": "not found"} answered with HTTP 200 does not match."""
    return isinstance(body, dict) and any(k in body for k in _CODA57_SYSINFO_FIELDS)


async def _probe(session: aiohttp.ClientSession, url: str, timeout: float) -> Any:
    """GET ``url`` and return the parsed JSON body, or None for anything else
    (redirect, non-200, non-JSON, connection error)."""
    try:
        async with session.get(url, timeout=aiohttp.ClientTimeout(total=timeout), allow_redirects=False) as resp:
            if resp.status != 200:
                return None
            return json.loads(await resp.text())
    except Exception:  # noqa: BLE001 - a probe that fails is "no match", not fatal
        return None


async def detect_api_root(host: str, timeout: float = 5.0) -> str | None:
    """Work out which API family answers at ``host``. Read-only GETs, in order:

    1. ``/1/Device/CM/Version``  -> "coda6x"
    2. ``/CM/Version``           -> "coda4x8x" (experimental)
    3. ``/data/getSysInfo.asp``  -> "coda57"
    4. otherwise None (an HT-EM5 is not distinguished by this helper).

    Each match needs a JSON body of the right shape; an HTML page or a 404
    never matches.
    """
    base = f"http://{host}"
    async with aiohttp.ClientSession() as session:
        if _looks_like_cm_version(await _probe(session, f"{base}/1/Device/CM/Version", timeout)):
            return "coda6x"
        if _looks_like_cm_version(await _probe(session, f"{base}/CM/Version", timeout)):
            return "coda4x8x"
        sysinfo = await _probe(session, f"{base}/data/getSysInfo.asp", timeout)
        if isinstance(sysinfo, list):
            sysinfo = sysinfo[0] if sysinfo else None
        if _looks_like_coda57_sysinfo(sysinfo):
            return "coda57"
    return None


@dataclass
class Coda4x8xClient:
    """Read methods (get_all, get_json) are stateless unless login() was
    called, in which case they reuse that session cookie. Never sends anything
    but GETs after login. See the module docstring for status and policy."""

    host: str = "192.168.0.1"
    timeout: float = 10.0
    max_concurrency: int = 2

    _session: aiohttp.ClientSession | None = field(default=None, init=False, repr=False)

    @property
    def base_url(self) -> str:
        # The root, deliberately NOT /1/Device (that is the CODA6X layout).
        return f"http://{self.host}"

    def __getattr__(self, name: str) -> Any:
        # Only reached when normal lookup fails, so real attributes are unaffected.
        if name in _ACTION_NAMES:
            raise AttributeError(
                f"Coda4x8xClient has no {name}(): the CODA 4x8x family is read-only "
                f"({STATUS}); use Coda60Client only for devices where writes were measured"
            )
        raise AttributeError(f"{type(self).__name__!r} object has no attribute {name!r}")

    # ---- session helpers (for authenticated READS) -------------------------

    @staticmethod
    def _new_session() -> aiohttp.ClientSession:
        # unsafe=True: modems are addressed by IP, and the default jar drops cookies set by an IP host.
        return aiohttp.ClientSession(cookie_jar=aiohttp.CookieJar(unsafe=True))

    async def login(self, username: str, password: str) -> bool:
        """POST /Users/Login with ``model=<json>``. Unconfirmed on hardware.
        Keeps the session cookie for later reads. Returns True on success and
        raises Coda4x8xError (Coda4x8xNotLoggedIn on a redirect) otherwise."""
        created = self._session is None
        if created:
            self._session = self._new_session()
        try:
            payload = {"model": json.dumps({"username": username, "password": password})}
            async with self._session.post(
                f"{self.base_url}/Users/Login",
                data=payload,
                timeout=aiohttp.ClientTimeout(total=self.timeout),
                allow_redirects=False,
            ) as resp:
                if resp.status in _REDIRECTS:
                    raise Coda4x8xNotLoggedIn(f"login redirected (HTTP {resp.status}): not logged in")
                if resp.status != 200:
                    raise Coda4x8xError(f"login failed (HTTP {resp.status})")
                try:
                    body = json.loads(await resp.text() or "{}")
                except ValueError:
                    body = {}
            if isinstance(body, dict) and body.get("errCode") not in (None, "", "000", 0):
                raise Coda4x8xError(f"login rejected: {body.get('errCode')} {body.get('errMsg', '')}".strip())
        except BaseException:
            # Do not leak the session this call created when the login fails.
            if created:
                await self.close()
            raise
        # Success is not independently confirmed: the 200 may carry no cookie.
        # Callers that need certainty should follow with an authenticated read.
        return True

    async def csrf(self) -> str:
        """GET /Users/CSRF and return the token (field ``Csrf_token`` or
        ``token``). Unconfirmed on hardware. This client never sends it: it
        makes no state-changing requests."""
        body = await self.get_json("Users/CSRF")
        token = body.get("Csrf_token") or body.get("token") if isinstance(body, dict) else None
        if not token:
            raise Coda4x8xError("CSRF token not present in /Users/CSRF response")
        return str(token)

    async def close(self) -> None:
        if self._session is not None:
            await self._session.close()
            self._session = None

    async def __aenter__(self) -> "Coda4x8xClient":
        return self

    async def __aexit__(self, *exc) -> None:
        await self.close()

    # ---- reads --------------------------------------------------------------

    async def _get(self, session: aiohttp.ClientSession, route: str) -> Any:
        async with session.get(
            f"{self.base_url}/{route}",
            timeout=aiohttp.ClientTimeout(total=self.timeout),
            allow_redirects=False,
        ) as resp:
            if resp.status in _REDIRECTS:
                raise Coda4x8xNotLoggedIn(f"GET {route}: redirected (HTTP {resp.status}): not logged in")
            if resp.status != 200:
                raise Coda4x8xError(f"GET {route}: status {resp.status}")
            text = await resp.text()
        try:
            return json.loads(text)
        except ValueError as exc:
            raise Coda4x8xError(f"GET {route}: response is not JSON") from exc

    async def get_json(self, route: str) -> Any:
        """GET one route (relative to the root, e.g. ``CM/DsInfo``) and return
        the parsed JSON. Raises Coda4x8xNotLoggedIn on a redirect, and
        Coda4x8xError for any route that is not in READ_ROUTES (no traversal,
        no query strings, no other paths)."""
        route = route.lstrip("/")
        if route not in READ_ROUTES:
            raise Coda4x8xError(f"{route!r} is not a known CODA 4x8x read route (read-only client)")
        if self._session is not None:
            return await self._get(self._session, route)
        async with self._new_session() as session:
            return await self._get(session, route)

    async def _fetch_one(self, session: aiohttp.ClientSession, endpoint_def: dict, sem: asyncio.Semaphore) -> tuple[str, dict]:
        route = endpoint_def["endpoint"]
        safe_key = route.replace("/", "_").lower()
        async with sem:
            try:
                raw = await self._get(session, route)
            except Coda4x8xNotLoggedIn as exc:
                return safe_key, {"error": str(exc), "not_logged_in": True}
            except Exception as exc:  # noqa: BLE001 - best-effort poller: one bad endpoint must not kill the rest
                return safe_key, {"error": str(exc)}
        if not raw or not isinstance(raw, dict):
            return safe_key, {"error": "empty response"}
        filtered: dict[str, Any] = {}
        for key, target in endpoint_def["gathers"].items():
            if key not in raw:
                continue
            if isinstance(raw[key], list) and isinstance(target, dict):
                filtered[key] = [{k: clean_value(item.get(k)) for k in target} for item in raw[key]]
            else:
                filtered[key] = clean_value(raw[key])
        return safe_key, filtered

    async def get_all(self, endpoints: list[dict] | None = None) -> dict[str, Any]:
        """Fetch the cm/router/hosts/wifi endpoints our CODA6X snapshot covers,
        concurrently, bounded by max_concurrency. Same shape as
        ``Coda60Client.get_all()`` so the existing metric mappers work.

        Raises Coda4x8xNotLoggedIn when every endpoint was redirected, so a
        poller does not report a device that needs a login as healthy and empty.
        """
        endpoints = ENDPOINTS if endpoints is None else endpoints
        sem = asyncio.Semaphore(self.max_concurrency)
        if self._session is not None:
            results = await asyncio.gather(*[self._fetch_one(self._session, ed, sem) for ed in endpoints])
        else:
            async with self._new_session() as session:
                results = await asyncio.gather(*[self._fetch_one(session, ed, sem) for ed in endpoints])
        if results and all(body.get("not_logged_in") for _, body in results):
            raise Coda4x8xNotLoggedIn("every endpoint redirected (HTTP 302): not logged in; call login() first")
        return {
            "metadata": {
                "timestamp": datetime.now(timezone.utc).isoformat(),
                "device_ip": self.host,
                "family": "coda4x8x",
                "status": STATUS,
            },
            "metrics": dict(results),
        }
