#!/usr/bin/env python3
"""Generate specs/openapi-coda60.yaml from the endpoint registry.

The registry in ``src/hitron/adapters.py`` is the single source of truth for
paths, methods, capability requirements and per-firmware verification state.
This script renders it into OpenAPI 3.0.3 plus an ``x-hitron`` block, so the
spec can never describe a path the client does not call or omit one it does.

Run from the repo root:

    python tools/gen_spec.py           # rewrite specs/openapi-coda60.yaml
    python tools/gen_spec.py --check   # fail if the file is out of date
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from datetime import date
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src"))

from hitron.adapters import ENDPOINTS, FIRMWARE_VERIFIED, state_counts  # noqa: E402
from hitron.models import all_profiles  # noqa: E402

SPEC_PATH = ROOT / "specs" / "openapi-coda60.yaml"
SITE_SPEC_PATH = ROOT / "site" / "openapi.json"
GENERATED = date(2026, 11, 26).isoformat()


# --------------------------------------------------------------------------
# Minimal, deterministic YAML emitter (no PyYAML dependency)
# --------------------------------------------------------------------------

_PLAIN = re.compile(r"^[A-Za-z_][A-Za-z0-9_./\-]*$")
_RESERVED = {"true", "false", "null", "yes", "no", "on", "off", "~"}


def _scalar(value: object) -> str:
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, (int, float)):
        return str(value)
    text = str(value)
    if _PLAIN.match(text) and text.lower() not in _RESERVED:
        return text
    return json.dumps(text, ensure_ascii=False)


def _emit(obj: object, indent: int, out: list[str]) -> None:
    pad = "  " * indent
    if isinstance(obj, dict):
        if not obj:
            out.append(f"{pad}{{}}")
            return
        for key, value in obj.items():
            name = _scalar(str(key))
            if isinstance(value, str) and "\n" in value:
                out.append(f"{pad}{name}: |")
                for line in value.rstrip("\n").splitlines():
                    out.append(f"{pad}  {line}" if line else "")
            elif isinstance(value, (dict, list)):
                if not value:
                    out.append(f"{pad}{name}: {'{}' if isinstance(value, dict) else '[]'}")
                else:
                    out.append(f"{pad}{name}:")
                    _emit(value, indent + 1, out)
            else:
                out.append(f"{pad}{name}: {_scalar(value)}")
    elif isinstance(obj, list):
        for item in obj:
            if isinstance(item, str) and "\n" in item:
                out.append(f"{pad}- |")
                for line in item.rstrip("\n").splitlines():
                    out.append(f"{pad}  {line}" if line else "")
            elif isinstance(item, (dict, list)):
                sub: list[str] = []
                _emit(item, indent + 1, sub)
                first = sub[0].lstrip()
                out.append(f"{pad}- {first}")
                out.extend(sub[1:])
            else:
                out.append(f"{pad}- {_scalar(item)}")
    else:
        out.append(f"{pad}{_scalar(obj)}")


def to_yaml(obj: object) -> str:
    out: list[str] = []
    _emit(obj, 0, out)
    return "\n".join(out) + "\n"


# --------------------------------------------------------------------------
# Static parts of the document
# --------------------------------------------------------------------------

SCHEMAS: dict[str, object] = {
    "Envelope": {
        "type": "object",
        "description": "Every response wraps its payload in this envelope. errCode 000 is success; 001 is a rejected parameter or missing session; 005 is a verb the model does not support.",
        "properties": {
            "errCode": {"type": "string", "example": "000"},
            "errMsg": {"type": "string"},
        },
    },
    "DeviceInfo": {
        "type": "object",
        "description": "GET /deviceinfo (root telemetry route). The only place hw_model is populated on 8.7.1.0.1b4; this is what model detection keys on.",
        "properties": {
            "device_id": {"type": "string", "description": "RF/device MAC"},
            "mso": {"type": "string"},
            "hw_model": {"type": "string", "example": "CODA60"},
            "hw_manufacturer": {"type": "string"},
            "hardware_version": {"type": "string", "example": "1A"},
            "software_version": {"type": "string", "example": "8.7.1.0.1b4"},
            "serial_number": {"type": "string"},
            "parent": {"type": "string"},
            "connect_type": {"type": "string"},
        },
    },
    "CmVersion": {
        "type": "object",
        "description": "GET /1/Device/CM/Version. Unauthenticated on this build.",
    },
    "RouterSysInfo": {
        "type": "object",
        "description": "GET /1/Device/Router/SysInfo. modelName/vendorName/deploymentName are null even when the device reports hw_model elsewhere.",
        "properties": {
            "hwVersion": {"type": "string"},
            "swVersion": {"type": "string"},
            "serialNumber": {"type": "string"},
            "systemTime": {"type": "string"},
            "rfMac": {"type": "string"},
            "systemUptime": {"type": "string"},
        },
    },
    "DocsisDs": {
        "type": "object",
        "description": "One locked downstream SC-QAM channel.",
        "properties": {
            "portId": {"type": "integer"},
            "frequency": {"type": "string", "description": "Hz"},
            "modulation": {"type": "string"},
            "signalStrength": {"type": "string", "description": "dBmV"},
            "snr": {"type": "string", "description": "dB"},
            "channelId": {"type": "string"},
            "dsoctets": {"type": "string"},
            "correcteds": {"type": "string"},
            "uncorrect": {"type": "string"},
        },
    },
    "DocsisUs": {
        "type": "object",
        "properties": {
            "portId": {"type": "integer"},
            "frequency": {"type": "string"},
            "modtype": {"type": "string"},
            "signalStrength": {"type": "string"},
            "bandwidth": {"type": "string"},
            "channelId": {"type": "string"},
            "scdmaMode": {"type": "string"},
        },
    },
    "DocsisDsOfdm": {
        "type": "object",
        "properties": {
            "receive": {"type": "string"},
            "ffttype": {"type": "string"},
            "Subcarr0freqFreq": {"type": "string"},
            "plclock": {"type": "string"},
            "ncplock": {"type": "string"},
            "mdc1lock": {"type": "string"},
            "plcpower": {"type": "string"},
            "SNR": {"type": "string"},
            "dsoctets": {"type": "string"},
            "correcteds": {"type": "string"},
            "uncorrect": {"type": "string"},
            "index": {"type": "string"},
        },
    },
    "DocsisUsOfdm": {
        "type": "object",
        "properties": {
            "uschindex": {"type": "string"},
            "state": {"type": "string"},
            "frequency": {"type": "string"},
            "digAtten": {"type": "string"},
            "channelBw": {"type": "string"},
            "repPower": {"type": "string", "description": "dBmV"},
            "repPower1_6": {"type": "string", "description": "DOCSIS 3.0-equivalent reported power"},
            "fftVal": {"type": "string"},
        },
    },
    "DocsisLog": {
        "type": "object",
        "properties": {
            "index": {"type": "string"},
            "time": {"type": "string"},
            "type": {"type": "string"},
            "priority": {"type": "string"},
            "event": {"type": "string"},
        },
    },
    "WifiWps": {
        "type": "object",
        "description": "GET /1/Device/WiFi/WPS. Answers 200 on units with no radios; all state fields are empty strings. Treat a populated wlsWpsState24g/5g as the evidence a radio exists.",
    },
    "WifiGuestSsid": {
        "type": "object",
        "description": "GET /1/Device/WiFi/GuestSSID. bssid/ssidName are the radio-presence evidence; empty strings mean a stub response.",
    },
    "MtaStatus": {
        "type": "object",
        "description": "GET /1/Device/MTA/StatusInfo. NumberOfSLIC and the LineNRegState fields are null on a unit with no EMTA hardware even though the route returns 200.",
        "properties": {
            "telDhcp": {"type": "string"},
            "telSec": {"type": "string"},
            "telTftp": {"type": "string"},
            "telReg": {"type": "string"},
            "Line1RegState": {"type": "string", "nullable": True},
            "Line2RegState": {"type": "string", "nullable": True},
            "Line3RegState": {"type": "string", "nullable": True},
            "Line4RegState": {"type": "string", "nullable": True},
            "NumberOfSLIC": {"type": "integer", "nullable": True},
        },
    },
    "MoCA": {
        "type": "object",
        "description": "Field set from models/MoCA.php. password is plaintext on the device; it is deliberately not requested here.",
        "properties": {
            "moca_status": {"type": "string", "enum": ["ON", "OFF"]},
            "fs_mode": {"type": "string"},
            "fs_channel": {"type": "integer"},
            "txpower": {"type": "string"},
            "beaconpwr": {"type": "string"},
            "nctype": {"type": "string", "enum": ["Preferred", "Auto Negotiated"]},
            "mocaNTSecurity": {"type": "string", "enum": ["Enable", "Disable"]},
            "fwVersion": {"type": "string"},
            "mocaMac": {"type": "string"},
            "coaxTx": {"type": "string"},
            "coaxRx": {"type": "string"},
            "mocaLink": {"type": "string"},
        },
    },
    "AdvancedSwitchPort": {
        "type": "object",
        "description": "On-board Ethernet switch port. The live CODA60 reports exactly one (1Gbps, linked).",
        "properties": {
            "portId": {"type": "integer"},
            "power": {"type": "string", "enum": ["true", "false"]},
            "neg": {"type": "string"},
            "speed": {"type": "string", "example": "1Gbps"},
            "duplex": {"type": "string"},
            "linked": {"type": "string"},
        },
    },
    "PnmBundle": {
        "type": "object",
        "description": "Proactive Network Maintenance bundle from the root telemetry route. Arrays carry base64-encoded, vendor-encoded payloads (rxmer, us_pre_eq, mod_prof) keyed by channel_id.",
        "properties": {
            "mac": {"type": "string"},
            "timestamp": {"type": "string", "description": "Unix seconds, device clock"},
            "rxmer": {"type": "string", "description": "base64 vendor-encoded per-subcarrier MER"},
            "ch_est_coef": {"type": "string", "description": "base64 channel-estimation coefficients"},
            "us_pre_eq": {"type": "string", "description": "base64 upstream pre-equalization"},
            "mod_prof": {"type": "string", "description": "base64 modulation profile"},
        },
        "additionalProperties": True,
    },
    "RegisterRequest": {
        "type": "object",
        "required": ["appid", "seckey"],
        "properties": {
            "appid": {"type": "string"},
            "seckey": {"type": "string", "description": "RS256 JWT with jti, iss=Hitron, vendor (from Device.X_HITRON_COM_App.VendorList), model."},
        },
    },
}

_PATH_PARAMS = {
    "ifname": {"name": "ifname", "in": "path", "required": True, "schema": {"type": "string", "example": "ath0"}},
    "index": {"name": "index", "in": "path", "required": True, "schema": {"type": "integer", "minimum": 1}},
    "info": {"name": "info", "in": "path", "required": True, "schema": {"type": "string"}},
}

_QUERY_PARAMS = {
    "portId": {"name": "portId", "in": "query", "required": True,
               "schema": {"type": "integer", "minimum": 0, "maximum": 31, "default": 24}},
    "centFreq": {"name": "centFreq", "in": "query", "required": True,
                 "schema": {"type": "number", "minimum": 49, "maximum": 1788, "default": 645}},
    "fromFreq": {"name": "fromFreq", "in": "query", "required": True,
                 "schema": {"type": "number", "minimum": 43, "maximum": 1794, "default": 637.5}},
    "toFreq": {"name": "toFreq", "in": "query", "required": True,
               "schema": {"type": "number", "minimum": 43, "maximum": 1794, "default": 643.5}},
}

_RESPONSE_SCHEMA = {
    "cm.ds": "DocsisDs",
    "cm.us": "DocsisUs",
    "cm.ds.ofdm": "DocsisDsOfdm",
    "cm.us.ofdm": "DocsisUsOfdm",
    "cm.log": "DocsisLog",
    "deviceinfo": "DeviceInfo",
    "telemetry.deviceinfo": "DeviceInfo",
    "telemetry.pnm": "PnmBundle",
    "telemetry.pnm.rxmer": "PnmBundle",
    "telemetry.pnm.ch_est_coef": "PnmBundle",
    "telemetry.pnm.us_pre_eq": "PnmBundle",
    "telemetry.pnm.mod_prof": "PnmBundle",
    "moca": "MoCA",
    "mta.status": "MtaStatus",
    "advanced.switch": "AdvancedSwitchPort",
    "api.register": "RegisterRequest",
}


def _info() -> dict[str, object]:
    counts = state_counts()
    models = len(all_profiles())
    return {
        "title": "Hitron CODA6X console REST API (reverse engineered)",
        "version": FIRMWARE_VERIFIED,
        "description": f"""\
Not an official Hitron specification. Reconstructed from a static read of a real
CODA6X firmware image (SquashFS rootfs mounted read-only; nothing executed from
it) and then verified GET-by-GET against a live, customer-owned CODA60 running
{FIRMWARE_VERIFIED} on 2026-11-26.

Architecture: lighttpd + php-fpm serve a Fat-Free-Framework PHP app at
www/hitron/. app/routes.ini maps URL patterns to app/controllers/*.php; those
controllers call the RDK-B COSA bridge (extension=cosa.so) which speaks TR-181
Device.*. Every path below is therefore a thin projection of a TR-181 subtree,
not an independent API.

Read surface, measured on {FIRMWARE_VERIFIED}: {counts.get('ok', 0)} working, {counts.get('php8', 0)} PHP-8 fatals, {counts.get('session', 0)} session-gated, {counts.get('verb', 0)} verb-rejected, {counts.get('untested', 0)} untested.
A PHP-8 fatal is a defect in the shipped controller (undefined array key,
getInstanceIds vs getInstanceIDs, a parameter declared before a required one),
not a request-shaping problem: it fails identically for the browser UI.

Two responses on this platform are stubs and must not be read as capability
evidence: /WiFi/* and /MTA/StatusInfo return 200 with empty strings or null
hardware fields on a unit that has no radios or SLIC. Model/capability detection
therefore keys on /deviceinfo's hw_model plus payload evidence, and each
endpoint carries its `requires` capability in x-hitron.

Every operation is tagged with x-hitron.state as measured against
{FIRMWARE_VERIFIED}. Five reads require the web UI session and one rejects GET;
everything else, including /1/Device/Manage, answers anonymously. Only
operations marked with `security` present a cookie. The attached model catalog
({models} profiles, specs/models.json) is what src/hitron/adapters.py uses to
decide which of these paths a given SKU should be asked for.
""",
        "contact": {"name": "Netquor SDN - internal reverse-engineering notes"},
        "license": {"name": "Private - Netquor internal"},
    }


_TAG_DISPLAY = {
    "shell": "Shell",
    "identity": "Identity",
    "docsis": "CM",
    "wifi": "WiFi",
    "lan": "LAN",
    "moca": "MoCA",
    "firewall": "Firewall",
    "voice": "Voice",
    "diagnostics": "Diagnostics",
    "spectrum": "Spectrum",
    "system": "System",
    "telemetry": "Telemetry",
    "write": "Write",
}


def _operation(endpoint, method: str, multi: bool) -> dict[str, object]:
    op_id = endpoint.id.replace(".", "_")
    if multi:
        op_id = f"{op_id}_{method.lower()}"
    tag = _TAG_DISPLAY.get(endpoint.group, endpoint.group.title())
    operation: dict[str, object] = {
        "operationId": op_id,
        "summary": endpoint.summary or endpoint.id,
        "tags": [tag],
        "x-hitron": {
            "requires": sorted(c for c in endpoint.requires if c != "always"),
            "state": endpoint.state,
            "httpStatus": endpoint.http_status,
            "verifiedOn": FIRMWARE_VERIFIED if endpoint.state != "untested" else None,
            "destructive": not endpoint.read,
            "models": list(endpoint.models),
        },
        "x-hitron-destructive": not endpoint.read,
    }
    if endpoint.note:
        operation["description"] = endpoint.note
    if endpoint.query:
        operation["parameters"] = [_QUERY_PARAMS[k] for k in endpoint.query if k in _QUERY_PARAMS]
    # Reads are anonymous on this build; only session-gated reads and writes
    # (or an app-token call) actually present credentials.
    if not endpoint.read or endpoint.state == "session":
        operation["security"] = [{"cookieAuth": []}]
    if endpoint.id == "api.register":
        operation["security"] = [{"appTokenAuth": []}]
        operation["requestBody"] = {
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/RegisterRequest"}}}
        }
    schema = _RESPONSE_SCHEMA.get(endpoint.id)
    response: dict[str, object] = {"description": "Envelope with errCode/errMsg plus the payload."}
    if schema:
        response["content"] = {"application/json": {"schema": {"$ref": f"#/components/schemas/{schema}"}}}
    responses: dict[str, object] = {"200": response}
    if endpoint.state == "php8":
        responses["500"] = {
            "description": f"PHP 8 fatal in the shipped controller on {FIRMWARE_VERIFIED}. {endpoint.note}".strip()
        }
    elif endpoint.state == "session":
        responses["400"] = {"description": "errCode 001 - requires the web UI SESSION.uid cookie."}
    elif endpoint.state == "verb":
        responses["400"] = {"description": "errCode 005 - GET is not supported for this model."}
    operation["responses"] = responses
    return operation


def build() -> dict[str, object]:
    paths: dict[str, object] = {}
    used_tags: list[str] = []
    for endpoint in ENDPOINTS:
        item = paths.setdefault(endpoint.path, {})
        params = []
        for name in re.findall(r"\{(\w+)\}", endpoint.path):
            if name in _PATH_PARAMS:
                params.append(_PATH_PARAMS[name])
        multi = len(endpoint.methods) > 1
        for method in endpoint.methods:
            key = method.lower()
            if key in item:
                raise SystemExit(
                    f"duplicate operation {method} {endpoint.path}: "
                    f"{item[key]['operationId']} and {endpoint.id}"
                )
            operation = _operation(endpoint, method, multi)
            if params:
                operation["parameters"] = params + operation.get("parameters", [])
            item[method.lower()] = operation
            tag = operation["tags"][0]
            if tag not in used_tags:
                used_tags.append(tag)

    return {
        "openapi": "3.0.3",
        "info": _info(),
        "externalDocs": {
            "description": "Unified Hitron KB (HT-EM5 + CODA6X + CODA-57)",
            "url": "./KB.md",
        },
        "servers": [
            {
                "url": "http://{host}",
                "variables": {
                    "host": {"default": "192.168.100.1", "description": "The gateway's LAN IP."}
                },
            }
        ],
        "components": {
            "securitySchemes": {
                "cookieAuth": {"type": "apiKey", "in": "cookie", "name": "SESSION.uid"},
                "appTokenAuth": {"type": "apiKey", "in": "header", "name": "ht-app-header"},
            },
            "schemas": SCHEMAS,
        },
        "x-hitron": {
            "generated": GENERATED,
            "generator": "tools/gen_spec.py",
            "firmware": FIRMWARE_VERIFIED,
            "modelCatalog": "./models.json",
            "stateCounts": state_counts(),
            "note": "Paths, methods, capability tags and states come from src/hitron/adapters.py. Do not edit the paths block by hand; run tools/gen_spec.py.",
        },
        "tags": [{"name": name} for name in used_tags],
        "paths": paths,
    }


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--check", action="store_true", help="exit 1 if the spec is stale")
    args = ap.parse_args()

    document = build()
    text = to_yaml(document)
    site_json = json.dumps(document, indent=2, ensure_ascii=False) + "\n"
    if args.check:
        stale = []
        if not SPEC_PATH.exists() or SPEC_PATH.read_text() != text:
            stale.append(SPEC_PATH.relative_to(ROOT))
        if not SITE_SPEC_PATH.exists() or SITE_SPEC_PATH.read_text() != site_json:
            stale.append(SITE_SPEC_PATH.relative_to(ROOT))
        if stale:
            print(
                "out of date: " + ", ".join(map(str, stale)) + "; run tools/gen_spec.py",
                file=sys.stderr,
            )
            raise SystemExit(1)
        print(f"{SPEC_PATH.relative_to(ROOT)} and {SITE_SPEC_PATH.relative_to(ROOT)} are current")
        return
    SPEC_PATH.write_text(text)
    SITE_SPEC_PATH.parent.mkdir(parents=True, exist_ok=True)
    SITE_SPEC_PATH.write_text(site_json)
    print(f"wrote {SPEC_PATH.relative_to(ROOT)} ({len(text.splitlines())} lines)")
    print(f"wrote {SITE_SPEC_PATH.relative_to(ROOT)}")


if __name__ == "__main__":
    main()
