# hitron

Golden source for talking to Hitron **HT-EM5** MoCA bridges, the **CODA6X**
(e.g. CODA-60) DOCSIS gateway REST API, and the **CODA-57** legacy `.asp`
status API. One Python package, one set of OpenAPI specs, one metrics
exporter — meant to replace the three separate, unwired implementations that
otherwise accumulate (a standalone client with no consumer, a consumer with
no client, and an unversioned sidecar script next to the product that needs
it). See `docs/KB.md` for the full reverse-engineering writeup: every
endpoint, every auth scheme, firmware quirks, and open questions.

**Devices this covers:**

| Package | Device | API shape | Auth |
| --- | --- | --- | --- |
| `hitron.ht_em5` | HT-EM5 (MoCA bridge) | `POST /ms/{type}/{reg}` register RPC | HTTP Basic |
| `hitron.coda60` | CODA6X (e.g. CODA-60) | `/1/Device/...` PHP+REST (TR-181 via `cosa.so`) | Session cookie or JWT app token |
| `hitron.coda57` | CODA-57 | `GET /data/{endpoint}.asp` legacy status API | Unconfirmed (session-primed via `/login.html`) |

## Install

```bash
python3 -m venv .venv && source .venv/bin/activate
pip install -e ".[dev]"
```

## CLI

Read (prints a JSON snapshot to stdout):

```bash
hitron ht-em5 192.168.0.212 --phy-rates
hitron coda60 192.168.100.1                # detect model, poll only what it supports
hitron coda60 192.168.100.1 --no-detect    # poll every known endpoint (legacy shape)
hitron coda60 192.168.100.1 --model coda60 # force a profile if detection is wrong
hitron coda57 192.168.100.1 --spec-check
```

Model catalog and read-only verification:

```bash
hitron coda60-models                # every known profile + capability set
hitron coda60-models coda6011uv     # one profile
hitron coda60-verify 192.168.100.1 --out verify.json
hitron coda60-verify 192.168.100.1 --model coda60 --group docsis
```

`coda60-verify` is GET-only and re-measures every endpoint in the resolved
profile's plan, including ones this firmware is known to break, so the report is
a current measurement rather than a restatement. The latest run against a
customer-owned CODA60 on `8.7.1.0.1b4` is in `docs/VERIFICATION.md`; the machine
readable copy is `specs/verification-coda60.json`.

### Model detection and capability gating

The CODA family shares one firmware image, so the app exposes WiFi, voice, MoCA
and router endpoints whether or not the SKU has the hardware. `hitron coda60`
resolves a profile first (explicit `--model`, then `hw_model` from the root
`GET /deviceinfo`, then `Device.DeviceInfo.ModelName`, then a probe-refined
generic profile) and only polls the endpoints that profile can answer. A CODA60
— DOCSIS 4.0, one Ethernet port, no WiFi/voice/MoCA/USB — is never asked for
`/WiFi/*`, `/MTA/*`, `/MoCA` or the firewall. `specs/models.json` is the
catalog; `src/hitron/adapters.py` is the endpoint registry that both the client
and the verifier consume, and `specs/openapi-coda60.yaml` is generated from it
(`python tools/gen_spec.py`).

Two caveats that matter on real hardware: `/WiFi/WPS` and `/MTA/StatusInfo`
return `200` with empty hardware fields on a unit that has neither, so detection
keys on payload evidence rather than status codes; and an empty USB list is an
empty port, not an absent one.

Write/action — each is its own named subcommand (never a flag on the read
command), needs a login, and the destructive ones need `--yes-really-do-this`:

```bash
hitron coda60-reboot        192.168.100.1 --username admin --password admin --yes-really-do-this
hitron coda60-factory-reset 192.168.100.1 --username admin --password admin --yes-really-do-this
hitron coda60-hard-reset    192.168.100.1 --username admin --password admin --yes-really-do-this
hitron coda60-set-password  192.168.100.1 --username admin --password admin --old-password admin --new-password '...' --yes-really-do-this
hitron coda60-set-username  192.168.100.1 --username admin --password admin --new-username newadmin --yes-really-do-this
```

These act on a real device the instant they run — see "Writes" below.

## Metrics exporter

One process, one `/metrics` port, any combination of devices:

```bash
hitron-exporter --port 9877 \
  --ht-em5 192.168.0.212 --ht-em5 192.168.0.241 --ht-em5 192.168.0.60 \
  --coda60 192.168.100.1
```

Or via environment variables (handy under systemd/Docker):

```bash
export HITRON_HT_EM5_HOSTS=192.168.0.212,192.168.0.241,192.168.0.60
export HITRON_CODA60_HOST=192.168.100.1
export HITRON_CODA60_MODEL=coda60      # optional: skip detection
export HITRON_EXPORTER_PORT=9877
hitron-exporter
```

CODA6X/CODA-57 series use `modem_*` names matching the metric names an
earlier standalone exporter already used, so an existing Prometheus
`scrape_config` pointed at port 9877 needs no changes. New HT-EM5/MoCA series
use a `hitron_` prefix.

The exporter detects the CODA6X model once per host (~10 GETs) and caches it,
so subsequent scrapes only hit the endpoints that SKU supports. Pass
`--coda60-model` / `HITRON_CODA60_MODEL` to skip detection entirely.

## Library

```python
import asyncio
from hitron import HitronMocaClient, Coda60Client, Coda57Client

async def main():
    async with HitronMocaClient("192.168.0.212") as bridge:
        print(await bridge.get_all())

    client = Coda60Client("192.168.100.1")
    match = await client.detect_profile()        # -> hw_model CODA60, capability-gated
    print(await client.get_all(profile=match.profile))
    print(await Coda57Client("192.168.100.1").get_all())

asyncio.run(main())
```

## Specs

`specs/openapi-ht-em5.yaml`, `specs/openapi-coda60.yaml`,
`specs/openapi-coda57-legacy.yaml` — OpenAPI 3.0, reverse engineered from live
devices and (for CODA6X) a static read of a real signed firmware image.
Not official Hitron documentation.

`openapi-coda60.yaml` is generated: paths, methods, capability tags and
per-firmware state come from `src/hitron/adapters.py` via `tools/gen_spec.py`.
`specs/models.json` is the model/capability catalog. Edit those, not the YAML.

## Tests

```bash
pytest
```

Only pure decode-function unit tests exist today (no device required). There
is no integration-test suite against real hardware yet — see `docs/KB.md` §4.

## Writes

`Coda60Client` has confirmed (not guessed) write/action methods, pulled
directly from the CODA6X firmware's own PHP source — not reverse engineered
from guesswork, and verified against `ModelInfo.php`/`AdminPassword.php`:

| Method | Device endpoint | What it does |
| --- | --- | --- |
| `login(user, password)` | `POST /1/Device/Users/Login` | Required before any other write; keeps the session cookie for the rest of this client's life. |
| `reboot()` | `POST /1/Device/CM/Reboot`, `{"reboot": 1}` | Sets a TR-181 reboot flag for a backend process to act on. |
| `factory_reset()` | same endpoint, `{"factoryReset": 1}` | Sets the factory-reset flag the same way. Destroys all configuration. |
| `hard_reset_and_reboot()` | same endpoint, `{"restoreReset": 1}` | The PHP handler `exec()`s `reboot` directly — immediate, no flag indirection. The point of no return. |
| `set_admin_password(...)` | `POST /1/Device/Manage/Password` | Changes the admin password. |
| `set_admin_username(...)` | `POST /1/Device/Manage/Username` | Changes the admin username. |
| `set_wifi_radio(index, settings)` | `PUT /1/Device/WiFi/Radios/{index}` | Server-validated radio config write. |

`Coda57Client` has **no write methods** — the one script this project was
built from (`hitron-client/main.py`) never implemented one, and no write
endpoint for the legacy `.asp` API has ever been observed. Use
`Coda60Client` for any action; both are believed to be the same underlying
hardware/software generation (see `docs/KB.md` §3).

None of these are gated by a config flag or an environment variable — the
library does exactly what you call. That's deliberate: a write against a
real, in-service modem should be a traceable act with your name on it, not
something that happens because a flag was left on. The CLI adds its own
layer on top (separate subcommands, `--yes-really-do-this` for the
destructive ones) so a write is never one typo away from a read — but the
library itself trusts the caller. **The metrics exporter never calls any of
these.**

## A note on the devices this was built against

The HT-EM5 units used to write this were lab/test bridges with default
credentials, not production equipment. The CODA6X/CODA-57 covered here is a
real, in-service cable modem on a real ISP account. Every write/action
method above was added, confirmed, and committed without ever being run
against that live device in this session — treat running one for the first
time as its own deliberate decision, the same way you'd treat it if you were
clicking the button on the device's own web UI.
