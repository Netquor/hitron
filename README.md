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

```bash
hitron ht-em5 192.168.0.212 --phy-rates
hitron coda60 192.168.100.1
hitron coda57 192.168.100.1 --spec-check
```

Each prints a JSON snapshot to stdout.

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

## A note on the devices this was built against

The HT-EM5 units used to write this were lab/test bridges with default
credentials, not production equipment. The CODA6X/CODA-57 covered here is a
real, in-service cable modem on a real ISP account — this package is
deliberately **read-only for the CODA6X/CODA-57** (no reboot, no password
change, no firewall/WiFi writes implemented) even though the register-level
HT-EM5 client supports writes, since those are lab devices. If you extend
this to write to a CODA6X/CODA-57, treat that as a separate, deliberate
decision each time, not something a metrics poller should ever do.
