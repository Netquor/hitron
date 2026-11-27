#!/usr/bin/env python3
"""Generate specs/openapi-coda60.yaml and specs/openapi-coda4x8x.yaml.

The registry in ``src/hitron/adapters.py`` is the single source of truth for
paths, methods, capability requirements and per-firmware verification state.
This script renders it into OpenAPI 3.0.3 plus an ``x-hitron`` block, so the
spec can never describe a path the client does not call or omit one it does.

Run from the repo root:

    python tools/gen_spec.py           # rewrite both specs and their site copies
    python tools/gen_spec.py --check   # fail if any generated file is out of date

The experimental CODA 4x8x spec is derived from the same registry: it selects
the read routes that a third-party client lists for that family, strips the
``/1/Device`` prefix, and adds the routes the registry has no entry for. It is a
hypothesis, not a measurement; see docs/KB.md section 4.
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
CODA4X8X_SPEC_PATH = ROOT / "specs" / "openapi-coda4x8x.yaml"
CODA4X8X_SITE_SPEC_PATH = ROOT / "site" / "openapi-coda4x8x.json"
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
    if (not endpoint.read and not endpoint.anonymous) or endpoint.state == "session":
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


# --------------------------------------------------------------------------
# Experimental CODA 4x8x spec (CODA-4582, CODA-4680 and relatives)
# --------------------------------------------------------------------------

EXPERIMENTAL = "experimental, not verified on live hardware"

# The 31 read routes that hairyhenderson/hitron_coda (MIT) lists for this
# family. Only facts are borrowed (route names, auth flow), not code. Paths are
# relative to the site root, with no /1/Device prefix.
CODA4X8X_ROUTES: tuple[str, ...] = (
    "/CM/DocsisProvision", "/CM/DsInfo", "/CM/DsOfdm", "/CM/Log", "/CM/SysInfo",
    "/CM/UsInfo", "/CM/UsOfdm", "/CM/Version",
    "/Router/Capability", "/Router/DMZ", "/Router/Location",
    "/Router/PortForward/Status", "/Router/PortForward/all",
    "/Router/PortTrigger/Status", "/Router/PortTrigger/all",
    "/Router/SysInfo", "/Router/TR069",
    "/DDNS", "/DNS", "/Hosts", "/Time",
    "/WiFi/AccessControl", "/WiFi/AccessControl/Status", "/WiFi/Client",
    "/WiFi/GuestSSID", "/WiFi/Radios", "/WiFi/Radios/Advanced",
    "/WiFi/Radios/Survey", "/WiFi/SSIDs", "/WiFi/WPS",
    "/Users/CSRF",
)

# Routes the CODA6X registry has no read entry for. Summaries only; nothing
# about their payloads is known here.
_CODA4X8X_EXTRA_SUMMARY = {
    "/Router/Capability": "Router capability flags",
    "/Router/DMZ": "DMZ host setting",
    "/Router/Location": "Device location setting",
    "/Router/PortForward/all": "All port-forward rules",
    "/Router/PortTrigger/all": "All port-trigger rules",
    "/Router/TR069": "TR-069 client configuration",
    "/Users/CSRF": "CSRF token for state-changing requests (unconfirmed; this client never sends one)",
}

_CODA4X8X_SCHEMAS = (
    "DocsisDs", "DocsisUs", "DocsisDsOfdm", "DocsisUsOfdm", "DocsisLog", "RouterSysInfo",
)

_CODA4X8X_SCHEMA_NOTE = {
    "RouterSysInfo": "Hardware/software version, serial, RF MAC and uptime as the CODA6X reports them.",
}

_CODA4X8X_TAG = {"CM": "CM", "Router": "Router", "WiFi": "WiFi", "Users": "Users"}


def _coda4x8x_info() -> dict[str, object]:
    return {
        "title": "Hitron CODA 4x8x console REST API (experimental, reverse engineered)",
        "version": "experimental",
        "description": f"""\
STATUS: {EXPERIMENTAL}. Nothing in this document has been captured from a
CODA-4582, CODA-4680 or any other 4x8x unit by this project.

Hypothesis: the CODA 4x8x serves the same REST data model that the CODA6X spec
(openapi-coda60.yaml) describes, but from the site root, without the
/1/Device prefix. Evidence for: the route names are identical (CM/DsInfo,
CM/UsInfo, CM/DsOfdm, CM/UsOfdm, CM/Log, CM/SysInfo, Router/SysInfo, Hosts,
WiFi/...), and the login shape matches (a form field `model` holding JSON).
Evidence against: the field names here are reconstructed from a third-party
client, not confirmed on hardware, and the source tested the 7.x firmware
line while the CODA6X analyzed here is an 8.x PHP + RDK-B build, so the two may
be different firmware generations. What the 4x8x runs is unknown.

Source of the route list and auth flow: hairyhenderson/hitron_coda (Go, MIT),
which targets a CODA-4680 on firmware 7.1.1.2.2b9. Only facts (endpoint names,
the login and CSRF flow) are used; no code is copied. Credit goes to that
project.

Auth, unconfirmed: POST /Users/Login with a form body `model=<json
{{"username","password"}}>` sets a session cookie. GET /Users/CSRF returns a
token in `Csrf_token` or `token`; a state-changing request would send it as the
`X-Csrf-Token` header. A 302 redirect on a data request means the session is
missing or expired.

Read-only policy: this document describes GET routes plus the login needed to
reach them. It deliberately lists no reboot, factory-reset, password or WiFi
write route. Those would act on in-service modems and nothing about them is
confirmed. The hitron package exposes no write path for this family.

Default LAN address on these units is often 192.168.0.1; many ISP builds use
192.168.100.1. Paths and shapes are generated from the endpoint registry in
src/hitron/adapters.py where it has a matching read entry, so they cannot drift
from what the CODA6X client calls.
""",
        "contact": {"name": "Netquor SDN - internal reverse-engineering notes"},
        "license": {"name": "Private - Netquor internal"},
    }


def _coda4x8x_operation(path: str, endpoint) -> dict[str, object]:
    first = path.strip("/").split("/")[0]
    tag = _CODA4X8X_TAG.get(first, "Network")
    op_id = "coda4x8x_" + re.sub(r"[^a-z0-9]+", "_", path.strip("/").lower()).strip("_")
    summary = endpoint.summary if endpoint else _CODA4X8X_EXTRA_SUMMARY[path]
    operation: dict[str, object] = {
        "operationId": op_id,
        "summary": summary,
        "tags": [tag],
        "x-hitron": {
            "status": EXPERIMENTAL,
            "family": "coda4x8x",
            "source": "hairyhenderson/hitron_coda endpoint list",
            "registryId": endpoint.id if endpoint else None,
            "destructive": False,
        },
        "x-hitron-destructive": False,
        "security": [{"cookieAuth": []}],
    }
    schema = _RESPONSE_SCHEMA.get(endpoint.id) if endpoint else None
    if schema not in _CODA4X8X_SCHEMAS:
        schema = "Envelope" if path == "/Users/CSRF" else None
    response: dict[str, object] = {
        "description": "JSON payload, shape unconfirmed. A 302 redirect means not logged in."
    }
    if schema:
        response["content"] = {"application/json": {"schema": {"$ref": f"#/components/schemas/{schema}"}}}
    operation["responses"] = {"200": response}
    return operation


def build_coda4x8x() -> dict[str, object]:
    reads: dict[str, object] = {}
    for route in CODA4X8X_ROUTES:
        match = [e for e in ENDPOINTS if e.path == "/1/Device" + route and e.read and "GET" in e.methods]
        if len(match) > 1:
            raise SystemExit(f"ambiguous registry match for {route}")
        reads[route] = {"get": _coda4x8x_operation(route, match[0] if match else None)}

    login: dict[str, object] = {
        "operationId": "coda4x8x_users_login",
        "summary": "Start a session (unconfirmed)",
        "description": "Authentication only. The form field `model` carries the JSON object. Cookie name and error shape are unconfirmed.",
        "tags": ["Users"],
        "x-hitron": {
            "status": EXPERIMENTAL,
            "family": "coda4x8x",
            "source": "hairyhenderson/hitron_coda login flow",
            "registryId": None,
            "destructive": False,
        },
        "x-hitron-destructive": False,
        "requestBody": {
            "required": True,
            "content": {
                "application/x-www-form-urlencoded": {
                    "schema": {
                        "type": "object",
                        "required": ["model"],
                        "properties": {
                            "model": {
                                "type": "string",
                                "description": 'JSON string {"username": "...", "password": "..."}',
                            }
                        },
                    }
                }
            },
        },
        "responses": {"200": {"description": "Session cookie set; body shape unconfirmed."}},
    }

    paths: dict[str, object] = {"/Users/Login": {"post": login}}
    paths.update(reads)

    schemas: dict[str, object] = {"Envelope": SCHEMAS["Envelope"]}
    for name in _CODA4X8X_SCHEMAS:
        schema = dict(SCHEMAS[name])
        base = _CODA4X8X_SCHEMA_NOTE.get(name, str(schema.get("description", "")))
        schema["description"] = (base + " " if base else "") + "Field set taken from the CODA6X client; unconfirmed for the 4x8x."
        schemas[name] = schema

    used_tags: list[str] = []
    for item in paths.values():
        for op in item.values():
            tag = op["tags"][0]
            if tag not in used_tags:
                used_tags.append(tag)

    return {
        "openapi": "3.0.3",
        "info": _coda4x8x_info(),
        "externalDocs": {
            "description": "Unified Hitron KB, section 4 (CODA 4x8x, experimental)",
            "url": "./KB.md",
        },
        "servers": [
            {
                "url": "http://{host}",
                "variables": {
                    "host": {"default": "192.168.100.1", "description": "The gateway's LAN IP. 192.168.0.1 on some builds."}
                },
            }
        ],
        "components": {
            "securitySchemes": {
                "cookieAuth": {
                    "type": "apiKey",
                    "in": "cookie",
                    "name": "SESSION",
                    "description": "Cookie name unconfirmed. A client should carry whatever Set-Cookie the login returns.",
                },
            },
            "schemas": schemas,
        },
        "x-hitron": {
            "generated": GENERATED,
            "generator": "tools/gen_spec.py",
            "status": EXPERIMENTAL,
            "family": "coda4x8x",
            "models": ["CODA-4582", "CODA-4680"],
            "sourceFirmware": "7.1.1.2.2b9 (per hairyhenderson/hitron_coda, unconfirmed)",
            "attribution": "hairyhenderson/hitron_coda (MIT): endpoint list and auth flow only",
            "readOnly": True,
            "routeCount": len(CODA4X8X_ROUTES),
            "modelCatalog": "./models.json",
            "note": "Derived from src/hitron/adapters.py where the registry has a matching read route. Do not edit by hand; run tools/gen_spec.py.",
        },
        "tags": [{"name": name} for name in used_tags],
        "paths": paths,
    }


def _targets() -> list[tuple[Path, str]]:
    """Every generated file paired with its expected content."""
    coda60 = build()
    coda4x8x = build_coda4x8x()
    return [
        (SPEC_PATH, to_yaml(coda60)),
        (SITE_SPEC_PATH, json.dumps(coda60, indent=2, ensure_ascii=False) + "\n"),
        (CODA4X8X_SPEC_PATH, to_yaml(coda4x8x)),
        (CODA4X8X_SITE_SPEC_PATH, json.dumps(coda4x8x, indent=2, ensure_ascii=False) + "\n"),
    ]


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--check", action="store_true", help="exit 1 if any generated file is stale")
    args = ap.parse_args()

    targets = _targets()
    if args.check:
        stale = [p.relative_to(ROOT) for p, text in targets if not p.exists() or p.read_text() != text]
        if stale:
            print(
                "out of date: " + ", ".join(map(str, stale)) + "; run tools/gen_spec.py",
                file=sys.stderr,
            )
            raise SystemExit(1)
        print(", ".join(str(p.relative_to(ROOT)) for p, _ in targets) + " are current")
        return
    for path, text in targets:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        print(f"wrote {path.relative_to(ROOT)} ({len(text.splitlines())} lines)")


if __name__ == "__main__":
    main()
