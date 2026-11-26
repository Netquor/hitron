# TypeScript client (experimental)

A zero-dependency TypeScript client intended for Node/TS backends that need to
talk to Hitron gear directly, rather than through the Python exporter.

**Status: experimental, not yet reconciled with the repo's specs.**

`hitron-coda.ts` targets the **CODA 4x8x generation** (`POST /Users/Login` with
a `model=<json>` body; `GET /CM/SysInfo`, `/Router/SysInfo`, `/WiFi/...`), which
is the API surface reverse-engineered from `hairyhenderson/hitron_coda`
(CODA-4680). Note this is a **different firmware generation** from the families
already covered here:

| This repo (Python) | API shape | Matches `hitron-coda.ts`? |
| --- | --- | --- |
| `hitron.coda60` (CODA6X / CODA-60) | `/1/Device/...` PHP+REST, cookie/JWT | No |
| `hitron.coda57` (CODA-57) | `GET /data/{endpoint}.asp` | No (that's the legacy `.asp` API) |
| `hitron.ht_em5` (HT-EM5) | `POST /ms/{type}/{reg}` Basic auth | No |

**Open questions before this is consumer-ready:**

- Reconcile against `specs/openapi-coda60.yaml` — decide whether this client
  targets a real device family we own, or should be re-pointed at the CODA6X
  `/1/Device/` surface to actually share the specs.
- Response field names on the typed interfaces are reconstructed, not confirmed
  against live firmware. Dump a real `cmSysInfo()` / `downstream()` and pin them.
- CSRF header name (`X-Csrf-Token`) and `/Users/CSRF` field name are unconfirmed.

See `docs/KB.md` for the authoritative reverse-engineering notes.
