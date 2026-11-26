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
hitron coda60 192.168.100.1
hitron coda57 192.168.100.1 --spec-check
```

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
export HITRON_EXPORTER_PORT=9877
hitron-exporter
```

CODA6X/CODA-57 series use `modem_*` names matching the metric names an
earlier standalone exporter already used, so an existing Prometheus
`scrape_config` pointed at port 9877 needs no changes. New HT-EM5/MoCA series
use a `hitron_` prefix.

## Library

```python
import asyncio
from hitron import HitronMocaClient, Coda60Client, Coda57Client

async def main():
    async with HitronMocaClient("192.168.0.212") as bridge:
        print(await bridge.get_all())

    print(await Coda60Client("192.168.100.1").get_all())
    print(await Coda57Client("192.168.100.1").get_all())

asyncio.run(main())
```

## Specs

`specs/openapi-ht-em5.yaml`, `specs/openapi-coda60.yaml`,
`specs/openapi-coda57-legacy.yaml` — OpenAPI 3.0, reverse engineered from live
devices and (for CODA6X) a static read of a real signed firmware image.
Not official Hitron documentation.

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
