# CODA6X read-surface verification

Live, GET-only measurement against a customer-owned CODA60 running firmware
`8.7.1.0.1b4`, 2026-11-26. No write, reboot or reset endpoint was called.

The device self-identifies as **CODA60** via `GET /deviceinfo` (`hw_model`);
TR-181 `modelName` is null on this build. Capability detection therefore resolved
the `coda60` profile: DOCSIS 4.0, no WiFi, no voice, no MoCA, no USB, no router mode.

## Summary

| Result | Count |
| --- | ---: |
| ok | 30 |
| php8: missing key | 7 |
| rejected (400) | 3 |
| php8: type error | 1 |
| php8: missing function | 1 |
| ok (non-json) | 1 |

Plan size: 43 endpoints, including known-broken ones because the verifier re-measures rather than trusts the registry.

Registry states across all models: `ok` 50, `php8` 53, `session` 5, `untested` 3, `verb` 1.

## Per-endpoint results (CODA60 plan)

### diagnostics

| id | path | HTTP | result | bytes |
| --- | --- | ---: | --- | ---: |
| `diagnostics.root` | `/1/Device/Diagnostics` | 500 | php8: missing key — Undefined array key "info" | 388 |
| `diag.input` | `/1/Device/Diagnostics/Input` | 200 | ok | 87 |
| `diag.ping` | `/1/Device/Diagnostics/Ping` | 200 | ok | 245 |
| `diag.traceroute` | `/1/Device/Diagnostics/TraceRoute` | 200 | ok | 201 |

### docsis

| id | path | HTTP | result | bytes |
| --- | --- | ---: | --- | ---: |
| `cm.provision` | `/1/Device/CM/DocsisProvision` | 200 | ok | 253 |
| `cm.ds` | `/1/Device/CM/DsInfo` | 200 | ok | 1390 |
| `cm.us` | `/1/Device/CM/UsInfo` | 200 | ok | 324 |
| `cm.ds.ofdm` | `/1/Device/CM/DsOfdm` | 200 | ok | 1091 |
| `cm.us.ofdm` | `/1/Device/CM/UsOfdm` | 200 | ok | 1362 |
| `cm.log` | `/1/Device/CM/Log` | 200 | ok | 4984 |
| `cm.ready` | `/1/Device/CM/Ready` | 400 | rejected (400) — {"errCode":"001","errMsg":"Invalid Paramters."} | 48 |

### identity

| id | path | HTTP | result | bytes |
| --- | --- | ---: | --- | ---: |
| `deviceinfo` | `/deviceinfo` | 200 | ok | 273 |
| `cm.version` | `/1/Device/CM/Version` | 200 | ok | 160 |
| `cm.sysinfo` | `/1/Device/CM/SysInfo` | 200 | ok | 212 |
| `router.sysinfo` | `/1/Device/Router/SysInfo` | 200 | ok | 259 |

### lan

| id | path | HTTP | result | bytes |
| --- | --- | ---: | --- | ---: |
| `ether.vlan` | `/1/Device/Ether/VLAN` | 200 | ok | 113 |
| `advanced.switch` | `/1/Device/Advanced/AdvancedSwitch` | 200 | ok | 136 |

### shell

| id | path | HTTP | result | bytes |
| --- | --- | ---: | --- | ---: |
| `menu.main` | `/1/Device/Menu/Main` | 200 | ok | 169 |
| `menu.sub` | `/1/Device/Menu/Sub` | 200 | ok | 1049 |
| `users.list` | `/1/Device/Users` | 500 | php8: missing key — Undefined array key "action" | 375 |

### spectrum

| id | path | HTTP | result | bytes |
| --- | --- | ---: | --- | ---: |
| `spectrum` | `/1/Device/Spectrum` | 200 | ok | 64 |
| `spectrum.freqlist` | `/1/Device/Spectrum/FreqList` | 200 | ok | 211 |
| `spectrum.rtdata` | `/1/Device/Spectrum/RTData` | 200 | ok | 297 |
| `spectrum.constellation` | `/1/Device/Spectrum/Constellation` | 200 | ok | 49 |
| `spectrum.freqpower` | `/1/Device/Spectrum/FreqPower` | 200 | ok | 6811 |

### system

| id | path | HTTP | result | bytes |
| --- | --- | ---: | --- | ---: |
| `time` | `/1/Device/Time` | 500 | php8: missing key — Undefined array key "info" | 367 |
| `advanced.root` | `/1/Device/Advanced` | 500 | php8: missing key — Undefined array key "info" | 379 |
| `usb.root` | `/1/Device/USB` | 500 | php8: missing key — Undefined array key "info" | 364 |
| `adminremote.root` | `/1/Device/AdminRemoteManagement` | 500 | php8: missing key — Undefined array key "info" | 418 |
| `adminremote.status` | `/1/Device/AdminRemoteManagement/Status` | 200 | ok | 51 |
| `adminremote.aclist` | `/1/Device/AdminRemoteManagement/AcList` | 500 | php8: type error — count(): Argument #1 ($value) must be of type Countable\|array, RemoteAc given [ | 78765 |
| `adminremote.rlist` | `/1/Device/AdminRemoteManagement/RList` | 500 | php8: missing function — Call to undefined function getInstanceIDs() [/www/hitron/app/controllers/AdminRe | 78786 |
| `health.root` | `/1/Device/Health` | 500 | php8: missing key — Undefined array key "info" | 382 |
| `health.settings` | `/1/Device/Health/Settings` | 400 | rejected (400) — {"errCode":"001","errMsg":"Invalid Paramters."} | 48 |
| `tempaccess.status` | `/1/Device/TempAccess/Status` | 400 | rejected (400) — {"errCode":"005","errMsg":"GET request is not supported for this model"} | 73 |
| `addons.assia` | `/1/Device/Addons/AssiaAgent` | 200 | ok | 45 |
| `manage.root` | `/1/Device/Manage` | 200 | ok (non-json) | 14 |

### telemetry

| id | path | HTTP | result | bytes |
| --- | --- | ---: | --- | ---: |
| `telemetry.rfmetrics` | `/rfmetrics` | 200 | ok | 12880 |
| `telemetry.pnm` | `/pnm` | 200 | ok | 121117 |
| `telemetry.pnm.rxmer` | `/pnm/rxmer` | 200 | ok | 19128 |
| `telemetry.pnm.ch_est_coef` | `/pnm/ch_est_coef` | 200 | ok | 75427 |
| `telemetry.pnm.us_pre_eq` | `/pnm/us_pre_eq` | 200 | ok | 17122 |
| `telemetry.pnm.mod_prof` | `/pnm/mod_prof` | 200 | ok | 9726 |

## What the failures are

- **php8 / 500** — a fatal in the shipped controller, reproduced identically in a
  browser: `Undefined array key "info"` at the top of most `get()` handlers,
  `getInstanceIds()` where the platform exports `getInstanceIDs()`,
  `Optional parameter $radio declared before required parameter $f3`,
  `count()` on a non-Countable, dynamic-property deprecations promoted to errors.
- **rejected (400)** — `errCode 001` for session-gated reads (`CM/Ready`,
  `Health/Settings`) and `errCode 005` for `TempAccess/Status` ("GET request is not
  supported for this model").
- **ok (non-json)** — `/1/Device/Manage` returns the literal text `cusadmin,false`:
  the admin username, unauthenticated.

## Reproduce

```bash
hitron coda60-verify 192.168.100.1 --out verify.json
hitron coda60-models coda60
```
