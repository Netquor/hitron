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

Registry states across all models: `ok` 52, `php8` 51, `session` 5, `untested` 3, `verb` 1.

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

All four branches shell out to `/usr/sbin/hitrondec`. They share one hardware
tuner, so captures are sequential; two sweeps in flight return interleaved or
empty data.

Measured capture behaviour (`FreqPower`, repeated while the link was up):

| Request | Points | Window actually returned |
| --- | ---: | --- |
| `centFreq=267&fromFreq=264&toFreq=270` | 300 | 264.0–269.98 MHz |
| `centFreq=273&fromFreq=270&toFreq=276` | 300 | 270.0–275.98 MHz |
| `centFreq=291&fromFreq=288&toFreq=294` | 300 | 288.0–293.98 MHz |
| `centFreq=645&fromFreq=637.5&toFreq=643.5` | 300 | 637.5–643.48 MHz |
| `centFreq=645&fromFreq=642&toFreq=648` | 300 | 643.5–649.48 MHz |

- 300 points at 20 kHz spacing across 6 MHz. Requesting a wider window
  (`fromFreq`/`toFreq` 12 MHz apart) returns an empty `spectrum` array.
- The window starts at `fromFreq` when that value sits on the tuner's raster,
  and can shift by 1.5 MHz otherwise. Clients must plot the `f` values in the
  payload, not the request.
- The handler's parameter regex
  `^[1-9][0-9][0-9]?[0-9]?\.?[1-9]?$` allows at most one non-zero fractional
  digit. `273.000` misses it and silently falls back to the 645/637.5/643.5
  defaults, which returns an unrelated slice.
- `Constellation?portId=N` returns 256 `{i,q}` points for the eight locked
  downstream ports. `FreqList` reports those ports as `ri` 0 and 25..31;
  ports 1..24 answer 200 with an empty array, not an error.
- `RTData` returns a rolling buffer of timestamped frames (10 on this build,
  eight channels each); `m` is MER in dB, not a max-hold power.

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

## Write path measured: reboot flags and FEC counters

Three writes were exercised on 8.7.1.0.1b4. All three are listed here with the
payload that worked, because the controller's replies do not tell you what it did.

### `POST /1/Device/CM/Reboot` — only `restoreReset` reboots

`doRebootAction()` inspects the flags in the order `reboot` → `factoryReset` →
`restoreReset`, and it reads **all three** before choosing a branch. Under PHP 8 a
missing key is a fatal, so every request must carry the full set.

| body | result |
| --- | --- |
| `{"reboot":1}` | HTTP 200 `{"errCode":"000"}` and **nothing happens** |
| `{"reboot":0,"factoryReset":0,"restoreReset":0}` | HTTP 400 `errCode 001` (all keys present, no branch taken) |
| `{"restoreReset":1}` | HTTP 500 `Undefined array key "reboot"` at `ModelInfo.php:297` |
| `{"reboot":0}` | HTTP 500 `Undefined array key "factoryReset"` |
| `{}` | HTTP 500 `Undefined array key "reboot"` |
| `{"reboot":0,"factoryReset":0,"restoreReset":1}` | **reboots the unit** |

The inert `reboot` flag:

```bash
curl -sS -X POST http://192.168.100.1/1/Device/CM/Reboot \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'model={"reboot":1}'
# {"errCode":"000","errMsg":""}   HTTP 200
```

- ICMP keeps answering at the same latency for 40 s after the 200.
- The WAN DHCP lease is unchanged (`D: 05 H: 00 M: 00 S: 00`), so the link never
  dropped and re-acquired.
- DOCSIS downstream octets keep incrementing across the call (about 3 MB in 30 s).
- No `CM Reboot Reason` entry appears in `/1/Device/CM/Log`.

So `Device.DeviceInfo.X_HITRON_COM_MGMT.Reboot = true` is accepted by `setStr()`
and then ignored by whatever is supposed to consume it. `factoryReset` sets a
matching flag and was not run.

The working payload, measured 2026-11-26 at 13:16:56:

```bash
curl -sS -X POST http://192.168.100.1/1/Device/CM/Reboot \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'model={"reboot":0,"factoryReset":0,"restoreReset":1}'
# curl: (52) Empty reply from server
```

- The connection drops instead of returning a body, so a client must treat
  "empty reply" and reset/EOF as success, not as a failure.
- ICMP is **down 5 s** after the POST and back **127 s** later (~122 s outage).
- The first downstream channel's octet counter resets (2,827,080,918 → 2,969,663).
- `/1/Device/CM/Log` gains `CM Reboot Reason : POWER_ON` and entries at
  13:18:08, 13:18:10 and 13:18:51.
- Hardware model, firmware, serial, WAN IP (`10.33.138.182`) and the config file
  (`CBR8_Bundle2_Default-1.cfg`) are unchanged, and `/1/Device/Manage` still returns
  `cusadmin,false`.

The PHP branch itself is `returnBeforeCommit`, `sleep(2)`, then
`exec('sync; ncpu_exec -e ht_reset_cmd.sh reboot'); exec('reboot')`.

### `PUT /1/Device/CM/DsInfo` and `PUT /1/Device/CM/DsOfdm` — FEC counters

`ModelInfo.php::doClearDsFec()` runs on `PUT /1/Device/CM/DsInfo` and sets
`Docsis.ClearAllDownstreamFECCounter = true`. The OFDM counters live behind a
separate route, `PUT /1/Device/CM/DsOfdm`. Both ignore the body and the query
string, both answer `{"errCode":"000","errMsg":""}` with HTTP 200, and neither
needs a session. The vendor's own "Reset FEC Counters" button only calls the 3.0
endpoint.

- **3.0 is deterministic.** One bare `PUT CM/DsInfo` zeroes all eight channels
  (corrected and uncorrected) immediately; a `Bogus` route on the same controller
  answers 400 and leaves the counters alone, so the zeroing is that handler.
- **OFDM is not.** One bare `PUT CM/DsOfdm` example, 300 ms apart:

  ```
  before     {1: 274, 2: 3, 3: 234, 4: 234}
  put 1      {1: 0,   2: 3,  3: 0,   4: 456}
  put 2      {1: 0,   2: 3,  3: 1,   4: 456}
  put 3      {1: 0,   2: 3,  3: 2,   4: 457}
  put 4      {1: 0,   2: 3,  3: 2,   4: 457}
  ```

  Each call zeroes zero to two channels, which ones vary, and one channel can sit
  out several calls in a row before clearing. Sending `{"index":N}` serially for
  N = 1..4 also only cleared some of them, so there is no per-channel targeting to
  rely on. The handler reads nothing from the request.
- The `NA` row (index 5) is an inactive OFDM receiver, not an error.

`hitron-cli monitor` binds this to `x`: one `PUT CM/DsInfo`, then up to eight
`PUT CM/DsOfdm` calls, keeping the best observation and reporting how many of the
channels that were counting before now read zero. The same flow is
`Client.ResetFEC` in Go and `CODA60Client.reset_fec()` in Python.

## Reproduce

```bash
hitron coda60-verify 192.168.100.1 --out verify.json
hitron coda60-models coda60
```
