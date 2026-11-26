"""Live, read-only capability verifier for a CODA6X gateway.

Fetches the identity, probes the capabilities that matter for model selection,
resolves a profile (explicit override, device model string, or probe-refined
fallback), then GETs exactly the endpoints that profile should support.

It never selects a write endpoint: ``adapters.plan()`` is read-only by default,
and the verifier additionally asserts that nothing in its plan is in
``DESTRUCTIVE_IDS``.

Typical use::

    hitron coda60-verify 192.168.100.1 --model coda6011uv --out verify.json
"""

from __future__ import annotations

import asyncio
import html
import json
import re
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Iterable

import aiohttp

from .adapters import DESTRUCTIVE_IDS, Endpoint, plan
from .models import (
    CapabilityProbes,
    ModelProfile,
    ProfileMatch,
    closest_profiles,
    detect_profile,
    family_capabilities,
)

__all__ = ["ProbeResult", "verify", "classify", "probe_capabilities"]

# Paths fetched first because they decide the profile. Everything else is
# fetched after the plan is known.
PROBE_PATHS = (
    "/deviceinfo",
    "/1/Device/CM/Version",
    "/1/Device/Router/SysInfo",
    "/1/Device/WiFi/WPS",
    "/1/Device/WiFi/GuestSSID",
    "/1/Device/MTA/StatusInfo",
    "/1/Device/MoCA",
    "/1/Device/USB/DeviceList",
    "/1/Device/Spectrum",
    "/1/Device/CM/DsOfdm",
    "/1/Device/CM/UsOfdm",
)


@dataclass
class ProbeResult:
    path: str
    status: int
    klass: str
    keys: list[str] | None = None
    count: int | None = None
    bytes: int = 0
    detail: str = ""
    endpoint_id: str | None = None

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.endpoint_id,
            "path": self.path,
            "status": self.status,
            "class": self.klass,
            "keys": self.keys,
            "count": self.count,
            "bytes": self.bytes,
            "detail": self.detail,
        }


def classify(status: int, body: str) -> tuple[str, list[str] | None, str]:
    """Separate a real success from a PHP-8 build failure or an auth gate."""
    if status == 200:
        try:
            parsed = json.loads(body)
        except Exception:
            return "ok (non-json)", None, body[:100].strip()
        if isinstance(parsed, dict):
            if str(parsed.get("errCode")) == "001":
                return "rejected (session/param)", list(parsed)[:6], str(parsed.get("errMsg", ""))[:80]
            if str(parsed.get("errCode")) == "005":
                return "rejected (verb)", list(parsed)[:6], str(parsed.get("errMsg", ""))[:80]
            return "ok", list(parsed)[:12], ""
        if isinstance(parsed, list):
            return "ok (list)", None, f"{len(parsed)} items"
        return "ok (scalar)", None, ""

    m = re.search(r"<p>(.*?)</p>", body, re.S)
    msg = html.unescape(re.sub(r"\s+", " ", (m.group(1) if m else body[:160])).strip())
    low = msg.lower()
    if "undefined array key" in low or "undefined index" in low:
        return "php8: missing key", None, msg[:100]
    if "undefined function" in low:
        return "php8: missing function", None, msg[:90]
    if "undefined variable" in low:
        return "php8: undefined var", None, msg[:90]
    if "count()" in low or "must be of type" in low:
        return "php8: type error", None, msg[:90]
    if "optional parameter" in low:
        return "php8: signature", None, msg[:90]
    if "dynamic property" in low:
        return "php8: dynamic prop", None, msg[:90]
    if "unsupported operand" in low:
        return "php8: operand", None, msg[:90]
    if "failed to open stream" in low or "readfile" in low:
        return "build: file/helper", None, msg[:90]
    if status == 404:
        return "no route", None, msg[:80]
    if status == 302:
        return "redirect", None, ""
    if status == 400:
        return "rejected (400)", None, msg[:80]
    if status == 405:
        return "verb not allowed", None, ""
    return "500 other", None, msg[:90]


async def _fetch(
    session: aiohttp.ClientSession,
    endpoint_id: str | None,
    path: str,
    query: dict[str, Any] | None,
    timeout: float,
) -> ProbeResult:
    try:
        async with session.get(
            path,
            params=query or None,
            timeout=aiohttp.ClientTimeout(total=timeout),
            allow_redirects=False,
        ) as resp:
            raw = await resp.read()
            body = raw.decode("utf-8", "replace")
            klass, keys, detail = classify(resp.status, body)
            count = None
            if keys and "Freq_List" in keys:
                try:
                    count = len(json.loads(body).get("Freq_List") or [])
                except Exception:
                    count = None
            return ProbeResult(
                path=path,
                status=resp.status,
                klass=klass,
                keys=keys,
                count=count,
                bytes=len(raw),
                detail=detail,
                endpoint_id=endpoint_id,
            )
    except Exception as exc:  # transport is a first-class outcome, not a crash
        return ProbeResult(path=path, status=0, klass="transport", detail=str(exc)[:120], endpoint_id=endpoint_id)


def probe_capabilities(by_path: dict[str, ProbeResult], bodies: dict[str, Any] | None = None) -> CapabilityProbes:
    """Turn observed responses into capability facts. Absence is ``None``.

    HTTP 200 is *not* proof of capability on this platform: the shared CODA6X
    app answers ``/WiFi/WPS`` and ``/MTA/StatusInfo`` on a wired-only CODA60
    with empty strings and null hardware fields. So WiFi and voice are decided
    from payload evidence (a BSSID, a SLIC count, a registration state), and
    USB only counts when a device is actually attached.
    """
    bodies = bodies or {}

    def body(path: str) -> dict[str, Any]:
        value = bodies.get(path)
        return value if isinstance(value, dict) else {}

    def ok(path: str) -> ProbeResult | None:
        r = by_path.get(path)
        return r if r and r.status == 200 and r.klass.startswith("ok") else None

    probes = CapabilityProbes()

    # WiFi: a populated BSSID/SSID or a live WPS state means a radio exists.
    wifi_body = {**body("/1/Device/WiFi/WPS"), **body("/1/Device/WiFi/GuestSSID")}
    if wifi_body:
        populated = any(
            isinstance(wifi_body.get(k), str) and wifi_body[k].strip()
            for k in ("bssid", "bssid5G", "ssidName", "ssidName5G", "wlsWpsState24g", "wlsWpsState5g")
        )
        probes.wifi = True if populated else False
    elif by_path.get("/1/Device/WiFi/WPS") and by_path["/1/Device/WiFi/WPS"].status == 404:
        probes.wifi = False

    # Voice: a SLIC count or a per-line registration state means an EMTA.
    mta_body = body("/1/Device/MTA/StatusInfo")
    if mta_body:
        slic = mta_body.get("NumberOfSLIC")
        line_states = [mta_body.get(f"Line{i}RegState") for i in range(1, 5)]
        if (isinstance(slic, int) and slic > 0) or any(v is not None for v in line_states):
            probes.voice = True
        elif slic is None and all(v is None for v in line_states):
            probes.voice = False
    elif by_path.get("/1/Device/MTA/StatusInfo") and by_path["/1/Device/MTA/StatusInfo"].status == 404:
        probes.voice = False

    moca_body = body("/1/Device/MoCA")
    if "moca_status" in moca_body:
        probes.moca = True

    usb_body = body("/1/Device/USB/DeviceList")
    if isinstance(usb_body.get("ListNumberOfEntries"), int):
        # Only a non-zero count proves a port exists; an empty list is an empty
        # port, not an absent one, so it stays None rather than becoming False.
        if usb_body["ListNumberOfEntries"] > 0:
            probes.usb = True

    if (r := ok("/1/Device/Spectrum")) is not None:
        probes.spectrum = True
    elif (r := by_path.get("/1/Device/Spectrum")) and r.status in (404, 500):
        probes.spectrum = False

    if (r := by_path.get("/1/Device/CM/DsOfdm")) and r.count is not None:
        probes.ofdm_downstream = r.count > 0
    if (r := by_path.get("/1/Device/CM/UsOfdm")) and r.count is not None:
        probes.ofdm_upstream = r.count > 0

    return probes


def _identity(by_path: dict[str, ProbeResult], bodies: dict[str, Any]) -> dict[str, Any]:
    """Merge /deviceinfo, CM/Version and Router/SysInfo.

    ``/deviceinfo`` is the root telemetry route; it carries ``hw_model`` and
    the full hw/sw/serial set, while Router/SysInfo adds rfMac and uptime.
    """
    identity: dict[str, Any] = {}
    for path in ("/deviceinfo", "/1/Device/CM/Version", "/1/Device/Router/SysInfo"):
        body = bodies.get(path)
        if isinstance(body, dict):
            identity.update({k: v for k, v in body.items() if k not in ("errCode", "errMsg") and v is not None})
    return identity


async def _fetch_body(session: aiohttp.ClientSession, path: str, timeout: float) -> Any:
    try:
        async with session.get(path, timeout=aiohttp.ClientTimeout(total=timeout)) as resp:
            if resp.status != 200:
                return None
            return await resp.json(content_type=None)
    except Exception:
        return None


def _assert_read_only(endpoints: Iterable[Endpoint]) -> None:
    bad = [e.id for e in endpoints if e.id in DESTRUCTIVE_IDS or not e.read]
    if bad:
        raise AssertionError(f"verifier plan contains non-read endpoints: {bad}")


async def verify(
    host: str = "192.168.100.1",
    model: str | None = None,
    groups: Iterable[str] | None = None,
    concurrency: int = 2,
    timeout: float = 12.0,
) -> dict[str, Any]:
    """Read-only verification report for one device."""
    base = f"http://{host}"
    sem = asyncio.Semaphore(concurrency)
    by_path: dict[str, ProbeResult] = {}
    bodies: dict[str, Any] = {}

    async with aiohttp.ClientSession() as session:
        async def get(path: str, endpoint_id: str | None = None, query: dict[str, Any] | None = None) -> ProbeResult:
            async with sem:
                result = await _fetch(session, endpoint_id, base + path, query, timeout)
            by_path[path] = result
            return result

        # Phase 1: identity + capability probes (serial-ish, bounded).
        for path in PROBE_PATHS:
            if path in by_path:
                continue
            await get(path)
            if path in (
                "/deviceinfo",
                "/1/Device/CM/Version",
                "/1/Device/Router/SysInfo",
                "/1/Device/WiFi/WPS",
                "/1/Device/WiFi/GuestSSID",
                "/1/Device/MTA/StatusInfo",
                "/1/Device/MoCA",
                "/1/Device/USB/DeviceList",
            ):
                bodies[path] = await _fetch_body(session, base + path, timeout)

        identity = _identity(by_path, bodies)
        probes = probe_capabilities(by_path, bodies)
        match: ProfileMatch = detect_profile(identity, probes, override=model)
        profile = match.profile

        # Phase 2: everything the resolved profile should support, including
        # endpoints this firmware is known to break - the point is to record
        # the current state, not to agree with the last measurement.
        endpoints = plan(profile, only_groups=groups, include_unverified=True)
        _assert_read_only(endpoints)

        tasks = []
        for endpoint in endpoints:
            concrete = endpoint.concrete_path(ifname="ath0", index="1")
            if concrete in by_path:
                by_path[concrete].endpoint_id = endpoint.id
                continue
            tasks.append(get(concrete, endpoint.id, endpoint.query))
        if tasks:
            await asyncio.gather(*tasks)

    ordered: list[ProbeResult] = []
    seen: set[str] = set()
    for endpoint in plan(profile, only_groups=groups, include_unverified=True):
        concrete = endpoint.concrete_path(ifname="ath0", index="1")
        result = by_path.get(concrete)
        if result is None or concrete in seen:
            continue
        seen.add(concrete)
        result.endpoint_id = endpoint.id
        ordered.append(result)

    counts: dict[str, int] = {}
    for result in ordered:
        counts[result.klass] = counts.get(result.klass, 0) + 1

    skipped_groups = sorted(
        {e.group for e in plan(profile, include_writes=False, only_groups=None)} - {e.group for e in plan(profile, only_groups=groups)}
    ) if groups else []

    return {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "device": {
            "host": host,
            "model_name": identity.get("hw_model") or identity.get("modelName"),
            "vendor_name": identity.get("vendorName"),
            "hw_version": identity.get("hardware_version") or identity.get("hwVersion"),
            "sw_version": identity.get("software_version") or identity.get("swVersion"),
            "serial_number": identity.get("serial_number") or identity.get("serialNumber"),
            "rf_mac": identity.get("rfMac") or identity.get("device_id"),
            "system_uptime": identity.get("systemUptime"),
        },
        "detection": match.to_dict(),
        "capabilities": family_capabilities(profile),
        "probes": probes.as_dict(),
        "suggestions": closest_profiles(profile),
        "summary": {
            "endpoints_in_plan": len(ordered),
            "by_class": dict(sorted(counts.items(), key=lambda kv: -kv[1])),
            "skipped_groups": skipped_groups,
        },
        "results": [r.to_dict() for r in ordered],
    }


def main(argv: list[str] | None = None) -> None:  # pragma: no cover - thin CLI shim
    import argparse

    ap = argparse.ArgumentParser(description="Read-only CODA6X capability verification")
    ap.add_argument("host", nargs="?", default="192.168.100.1")
    ap.add_argument("--model", help="force a profile id (e.g. coda6011uv)")
    ap.add_argument("--group", action="append", dest="groups", help="limit to one endpoint group; repeatable")
    ap.add_argument("--out", help="write the JSON report here")
    args = ap.parse_args(argv)

    report = asyncio.run(verify(args.host, args.model, args.groups))
    text = json.dumps(report, indent=2, default=str)
    if args.out:
        with open(args.out, "w") as fh:
            fh.write(text + "\n")
        print(f"wrote {args.out}: {report['summary']['endpoints_in_plan']} endpoints, "
              f"{report['summary']['by_class']}")
    else:
        print(text)


if __name__ == "__main__":  # pragma: no cover
    main()
