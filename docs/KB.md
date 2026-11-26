# Hitron KB: HT-EM5 MoCA bridges + CODA6X (CODA-60) + CODA-57 (legacy .asp API)

Everything we've reverse engineered about these Hitron product/API families. Read
this first; the three OpenAPI specs in `../specs/` (`openapi-ht-em5.yaml`,
`openapi-coda60.yaml`, `openapi-coda57-legacy.yaml`) are the wire-level reference.
This file is the aggregate — if you only read one thing, read this one.

**Scope note on consent:** the three HT-EM5 bridges are lab devices that can be
tested destructively (default creds, not production). The CODA-60/CODA-57 is a
**real, in-service cable modem** on a real ISP account (Midco) — treat its data as
production. Never write/reboot/factory-reset it without being asked each time, and
never commit real account credentials/tokens (the kind that lived in
`netquor-sdn/aggregator/rebootmodem.txt`: a real name, email, account number, bearer
tokens) into this or any repo. This KB and all three specs describe the *API shape*
only.

## Why this repo exists

Before this repo, "talk to these Hitron devices" existed in three disconnected
places: `netquor-sdn/aggregator/moca_client.py` (a complete HT-EM5 client with no
consumer), `netquor-sdn/server/src/features/modem/service.ts` (a consumer with no
client — it expects a sidecar to feed it `modem_metrics.json`), and
`~/Documents/hitron-client/` (that sidecar, but a separate, unversioned project next
to this product). This repo is the golden source that replaces all three: one
package, `hitron`, with a client per device family, a unified Prometheus exporter,
a CLI, and these specs — meant to be imported by `netquor-sdn` (or anything else)
instead of re-derived or duplicated again.

See `../README.md` for how to install and run it.

## 1. HT-EM5 (MoCA bridge)

Full spec: `../specs/openapi-ht-em5.yaml`. Client: `../src/hitron/ht_em5.py`.

- Tiny RPC-over-HTTP API: `POST /ms/{type}/{reg}[/GET|/PUT]`, body `{"data":[...]}`,
  values as 32-bit hex words. HTTP Basic auth (default `admin`/`admin`), `csrf_token`
  cookie echoed as `X-CSRF-TOKEN`, single connection at a time (InterNiche embedded
  stack — serialize every call; the client's `max_concurrency` defaults to 1).
- Per-band register families (MoCA password, scan mask/offset, primary-channel masks)
  are `BASE + bandIdx` (0=D-Ext, 1=D-Low, 2=D-High), computed client-side — this isn't
  three different endpoints, it's arithmetic on one.
- `0xb00` reboot and `0xb01` factory-reset (string-table confirmed, factory-reset not
  yet exercised live) have no server-side confirmation.
- Admin password (`0x20e`) and MoCA password (`0x1049`-family) are both returned in
  plaintext to any authenticated reader — the client never reads `0x20e`.

### Hidden OIDs, including a dormant TR-069/CWMP client config

A read-only sweep of ~1,400 additional OIDs (type 0: `0x0`-`0xff`, `0x1000`-`0x11ff`;
type 1: `0x100`-`0x4ff`; skipping `0xb00`-`0xbff` and the known password OIDs) against
one HT-EM5 unit found 412 that answer, none referenced anywhere in the device's own
HTML/JS — genuinely undocumented by the UI. The most significant block:

| OID | Decoded value | What it is |
| --- | --- | --- |
| `0x267` | `http://10.1.1.150:8080/tr069` | **A TR-069/CWMP ACS URL baked into the bridge.** |
| `0x268` | `admin` | ACS connection-request username. |
| `0x269` | `xaps` (partial — alignment not fully resolved) | Likely the start of a ConnectionRequestPassword field; decode the full word run before trusting it. |
| `0x275` | `10.1.1.150` | Same host as `0x267`, stored separately (likely a distinct TR-069 param). |
| `0x277` / `0x278` | `stun_test` / `stun_pass` | STUN credentials for TR-069 NAT traversal — these read as **placeholder/test values**, not a real carrier's secrets. |
| `0x304` | `ECA7ML3` | A sub-component/chip string. |
| `0x306` | `1.0.0` | A sub-component firmware version. |
| `0x350` | `111-999` | Looks like a placeholder serial/model pattern. |
| `0x360` | `MaxLinear` | Confirms the chip vendor directly (expected — MXL371x). |

The other ~390 hits are mostly all-zero or small-integer config slots in the
`0x1000`-`0x117f` MoCA-config block whose purpose isn't decoded (e.g. `0x116b`-`0x1173`
all read `0x00000005`; `0x10a9`-`0x10c0` look like a table of small tx-power-ish
values, possibly per-channel TX power — worth correlating against a channel-plan
change).

**Implication:** this firmware ships with a dormant TR-069 management client pointing
at what looks like a lab/dev ACS (`10.1.1.150`), not a live carrier endpoint. Treat
"ever point it at a real ACS" as its own, separate decision — it's a carrier-grade
remote-management channel, a different risk class from the `/ms/` register API.

**Confirmed identical across all three physical bridges** (`.212`, `.241`, `.60`):
the same 412 OIDs answer on every unit, with byte-for-byte identical values for the
entire TR-069 block (same ACS URL, same `admin` username, same `stun_test`/
`stun_pass` placeholders) — this is a shared default baked into the firmware image,
not per-device config. The only values that differ between units are exactly the
ones that should: each bridge's own MAC (`0x103`), IP (`0x20b`), a per-device counter
(`0x104`), and two config flags (`0x1002` preferred-NC, `0x1009` band mask) that
simply happen to differ because these particular units were configured differently,
not because the firmware build differs.

## 2. CODA6X / CODA-60

Full spec: `../specs/openapi-coda60.yaml`. Client: `../src/hitron/coda60.py`.

- Real architecture: `lighttpd`+`php-fpm` serving a Fat-Free-Framework PHP app
  (`www/hitron/app/`), whose controllers are thin wrappers over `extension=cosa.so` —
  RDK-B's native bridge into the standard `Device.*` TR-181 object tree. The endpoint
  names are Hitron's; the underlying data model is the same one CableLabs/RDK-B
  publish for every DOCSIS gateway, not Hitron-proprietary.
- Base path `/1/Device/...`, ~50 routes covering DOCSIS status (`CM/DsInfo`,
  `CM/UsInfo`, `CM/DsOfdm`, `CM/UsOfdm`, `CM/Log`, `CM/DocsisProvision`), MoCA
  (`/1/Device/MoCA`, full field list confirmed from the firmware's own `MoCA.php`
  model), WiFi radios/SSIDs/ATF, the on-board Ethernet switch (routed, misleadingly,
  under `/Advanced/AdvancedSwitch`), firewall rules, DHCP/DDNS/WAN, voice (MTA) status,
  and a **full spectrum analyzer** (see below).
- Two auth schemes: a cookie session (`SESSION.uid`, via `/1/Device/Users/Login`) for
  the browser UI, and a separate RS256-JWT vendor-app registration flow
  (`/1/Device/API/Register`) issuing a bearer token sent as a custom `ht-app-header`.
- Confirmed against the independently-derived `CODA_60.py` field names — they match,
  strong corroboration this spec's shape is right.
- One firmware image is shared across the whole CODA family. `app/routes.ini` has 87
  route patterns; the controllers' `@info` / `@ifname` branches expand them to 104
  concrete read targets. Which of those a given SKU should be asked for is a
  capability question, not a routing one — see the next two subsections.

### Model identity: `/deviceinfo`, not TR-181

On the live 8.7.1.0.1b4 unit both `CM/Version` and `Router/SysInfo` report
`modelName: null`, so there is nothing in `/1/Device/*` to key a per-model adapter on.
The root telemetry route does report it:

    GET /deviceinfo
    {"errCode":"000","device_id":"...","hw_model":"CODA60","hw_manufacturer":"Hitron Technologies",
     "hardware_version":"1A","software_version":"8.7.1.0.1b4","serial_number":"..."}

`/deviceinfo` is not under `/1/Device/` — it maps to the root `@info` route handled by
`TelemetryAssist`, alongside `/rfmetrics` and `/pnm` (plus `/pnm/{rxmer,ch_est_coef,
us_pre_eq,mod_prof}`). Those are unauthenticated and were missed by the earlier
`/1/Device`-only survey. `src/hitron/models.py` prefers `hw_model` over
`Device.DeviceInfo.ModelName`, then falls back to a probe-refined generic profile.
If a build reports a family label rather than a SKU, pass `--model` explicitly; probe
evidence can still add WiFi/voice back, but it cannot detect router mode.

### Capability gating, and why a 200 is not proof

`specs/models.json` is an 18-profile catalog (vendor portfolio rows plus the
firmware-internal strings in `MenuCreater.php`: `CODA-4589-CGO`, `CODA-4589-RES`,
`CHITA-MGC`, `CHITA-RES`, `CODA-4582-RES`). Each profile carries DOCSIS generation,
WiFi bands, voice lines, MoCA, USB, battery and router-mode flags;
`src/hitron/adapters.py` tags every endpoint with the capability it needs, and
`plan(profile)` selects the intersection.

The trap this design exists to avoid: the shared app answers `/1/Device/WiFi/WPS`
and `/1/Device/MTA/StatusInfo` with **200 on a wired-only CODA60**, with empty strings
and null hardware fields (`NumberOfSLIC: null`, all `LineNRegState: null`). A
status-code-only prober concludes "WiFi + voice" and then alarms on hardware that does
not exist. Probes are therefore evidence-based: a populated BSSID/SSID means a radio,
a non-null SLIC count or line state means an EMTA. An empty USB list means an empty
port, not an absent one, so it never removes the capability.

### Live verification: 8.7.1.0.1b4 (customer-owned CODA60, 2026-11-26)

GET-only, no writes, no reboot/reset. Full table in `VERIFICATION.md`; the machine
readable report is `../specs/verification-coda60.json`. Headline:

- 104 branch targets probed: **43 ok, 7 rejected, 54 PHP-8 500s**.
- The 500s are shipped-controller bugs, not request shaping: 37
  `Undefined array key "info"`, 7 `getInstanceIds()` (platform defines
  `getInstanceIDs`), 5 `Optional parameter $radio declared before required`, plus
  dynamic-property/type errors. They fail identically in the browser.
- Every `/WiFi/Radios/*` and `/WiFi/SSIDs*` read is dead on this build: `RadioInfo`
  fatals at class load, and `Utils::checkValidIfName()` does a `string + string`.
- Corrected shapes vs. the first pass: `/Menu/Main` and `/Menu/Sub` are
  case-sensitive (lowercase `main` is rejected); SSID reads need
  `/WiFi/SSIDs/{ifname}/version`, not a numeric index; the WiFi survey shape is
  `/WiFi/Radios/Advanced`; telemetry lives at the root, not under `/1/Device`.
- `/1/Device/Manage` returns `cusadmin,false` in plaintext to an unauthenticated
  GET — the admin username, disclosed before login.
- `/1/Device/AdminRemoteManagement/Backup` is exposed but 500s: `confPhp get` never
  creates `/tmp/basic.cfg`.
- `/1/Device/Upload` accepts a `.cfg` upload into `/var/tmp` without auth; the
  restore handler builds `exec('confPhp restore '.$target)` from an unvalidated
  filename. Neither was exercised.

### Radio/channel deep dive

- **DOCSIS SC-QAM + OFDM, both directions** are fully exposed with per-channel
  frequency, modulation, signal strength, SNR, corrected/uncorrectable codewords
  (`CM/DsInfo`, `CM/UsInfo`), plus OFDM-specific PLC/NCP/MDC1 lock status and PLC power
  (`CM/DsOfdm`), and OFDMA digital attenuation / reported power / FFT value
  (`CM/UsOfdm`). Everything needed for a real signal-quality dashboard or alerting.
- **Full-band spectrum analyzer, genuinely exposed via REST:** `/1/Device/Spectrum/*`
  shells out to `/usr/sbin/hitrondec` (a stripped native ELF binary) with four modes:
  `-f centFreq fromFreq toFreq` (power sweep, 43-1794 MHz range enforced server-side),
  `-l` (available frequency list), `-m` (real-time data), `-c portId` (QAM
  **constellation diagram** points for one downstream port, 0-31). A carrier-grade
  diagnostic normally locked to field techs, sitting behind the same session cookie as
  everything else. Not yet wrapped in `coda60.py` (read-only, low risk to add).
- **WiFi radios** are addressed by 1-based index (bounded by `Utils::getRadioNum()`,
  itself driven by `SSID2GNum=4` / `SSID5GNum=4` in `app/config.ini`), support GET,
  PUT (full, server-validates via `Utils::checkRadioValidate`), and PATCH (partial,
  also accepts `athX`-style interface names remapped to the numeric index). Airtime
  Fairness (ATF) is a first-class sibling resource, both globally and per-SSID/station.
- **MoCA fields** (from the firmware's own model, the authoritative shape):
  `moca_status`, `fs_mode` (channel plan), `fs_channel`, `fs_scan_start/end`,
  `txpower`, `beaconpwr`, `nctype` (Preferred vs Auto Negotiated), `mocaNTSecurity`,
  `password` (plaintext — not requested by this client), `fwVersion`, `mocaMac`,
  `coaxTx`, `coaxRx`, `mocaLink`. Same underlying `Device.MoCA.Interface.1.*` TR-181
  object the HT-EM5's own register map conceptually mirrors — two different
  front-ends over the same kind of MoCA stack.

### Write/action endpoints (confirmed from the firmware's own PHP source, implemented in `coda60.py`)

- **Login:** `POST /1/Device/Users/Login`, body `model={"username":...,"password":...}`.
  `UserAction.php::checkLogin()` is LAN-aware: on the LAN-side IP it checks one UCI
  credential pair (`access-mgmt.cusadmin.*`), off-LAN it checks a different one
  (`access-mgmt.userrole.*`) with a `crypt()`-hashed password — the same login call
  can behave differently depending on which interface the request arrives on.
- **Reboot family — all three hit the same endpoint**, `POST /1/Device/CM/Reboot`,
  distinguished only by the body flag (`ModelInfo.php::doRebootAction`):
  - `{"reboot": 1}` → sets `Device.DeviceInfo.X_HITRON_COM_MGMT.Reboot = true` (a
    TR-181 flag; some other backend process is expected to act on it). **Measured
    inert on 8.7.1.0.1b4** — the call answers `errCode 000` and nothing happens
    (see `docs/VERIFICATION.md`).
  - `{"factoryReset": 1}` → sets the matching FactoryReset flag the same way.
  - `{"restoreReset": 1}` → **no flag indirection at all** — the PHP code itself runs
    `exec('sync; ncpu_exec -e ht_reset_cmd.sh reboot'); exec('reboot')`. This is the
    most immediate of the three.
- **Admin password/username:** `AdminPassword.php` exposes `POST /1/Device/Manage/Password`
  (body `{oldPassword, newPassword, newPasswordConfirm}`) and
  `POST /1/Device/Manage/Username` (body `{newUsername}`).
- **WiFi radio:** already documented above — `PUT /1/Device/WiFi/Radios/{index}`.

Only `{"reboot": 1}` has been executed against the real device; it was inert. Nothing
else here has — see the README's "Writes" section for the handling policy (explicit
login, no auto-write, CLI requires `--yes-really-do-this` for the destructive ones).

### Quirks found in the firmware source (informational, none exploited)
- Shipped with `DEBUG=5` (Fat-Free verbose-error mode) in `app/config.ini`.
- A debug line (`shell_exec("echo check_token > /tmp/app-debug.log")`) fires on every
  app-token auth check, world-readable.
- A user-creation/reset code path sets a fallback password hash of `md5("password")` —
  worth confirming whether that path is reachable without a prior factory reset.
- No CableLabs-signature bypass, no shell, no secondary backdoor credentials found in
  the extracted filesystem's strings or source — the device's management surface
  really is just this REST API plus whatever the native `cosa.so`/DOCSIS MAC stack
  exposes.

## 3. CODA-57 — confirmed: the legacy `.asp` API

`~/Documents/hitron-client/main.py` + `test.py` is the CODA-57 client. It hits a
distinct URL family:

    GET /data/{endpoint}.asp          (dsinfo, usinfo, dsofdminfo, usofdminfo,
                                        getCMInit, getLinkStatus, getCmDocsisWan,
                                        getSysInfo)

Field names (`signalStrength`, `channelId`, `plcpower`, `Subcarr0freqFreq`,
`repPower1_6`, `uschindex`) are identical to the CODA6X REST layer's — both sit on the
same underlying DOCSIS/RDK-B data model; `.asp` is the CODA-57's own, older-generation
endpoint convention, predating the `app/`-based PHP+REST rewrite seen in the CODA6X
`8.5.0.0.x` firmware. Full spec: `../specs/openapi-coda57-legacy.yaml`. Client:
`../src/hitron/coda57.py`, including a direct port of `main.py`'s Midco in-spec power
check (DS -6..15 dBmV, US 35..48 dBmV, with a +6 dB DOCSIS-3.0-equivalent adjustment
on OFDMA upstream readings).

Auth differs again here: `test.py` primes a session via `GET /login.html` (a classic
HTML login page), implying a PHPSESSID-style cookie rather than HTTP Basic (HT-EM5) or
the JWT/`ht-app-header` flow (CODA6X REST). `main.py` itself doesn't authenticate at
all currently and silently returns `[]` on any failure, so whether these endpoints are
actually gated has not been confirmed live — the host was unreachable from the
development machine during this pass (different subnet). Re-verify before relying on
`coda57.py` unattended.

## 4. What's still open / next steps

1. **Live-verify `coda57.py`** against a real CODA-57/CODA-60 unit on a reachable
   network — confirm response shapes for `getCMInit`/`getLinkStatus`/`getCmDocsisWan`
   (fetched by the original script but never rendered/decoded, so their shape is
   unconfirmed) and whether `/login.html` actually gates anything.
2. **HT-EM5:** decode the remaining `0x10a9`-`0x1173` config block and the `0x269`
   ACS-password word alignment. Both are read-only investigation, no device risk.
3. ~~**HT-EM5:** sweep the other two bridges to confirm the register map and TR-069
   block are consistent across units.~~ **Done.** Confirmed identical on all three
   physical bridges — see §1.
4. ~~**CODA6X:** confirm the exact `@info` routing convention (path segment vs.
   querystring) against live traffic.~~ **Done.** Path segments; 104 concrete
   targets measured on 8.7.1.0.1b4 — see `VERIFICATION.md`.
5. **CODA6X:** never exercise `/1/Device/Manage` (admin password), reboot, or factory
   reset against a real unit without being asked each time.
6. **Decide on the TR-069 finding** — it's a firmware default shared across every
   unit, not per-device config, which makes it more plausible this is an
   intentionally-shipped management hook rather than a leftover. Leave it dormant, or
   treat "does this bridge ever phone home to a real ACS" as its own investigation.
7. **Wire this package into `netquor-sdn`** — replace `aggregator/moca_client.py` and
   the external `hitron-client` sidecar with `pip install`-ing this repo (or vendoring
   it) and pointing `server/src/features/modem` and the exporter at it. Not done as
   part of writing this repo — a deliberate choice for whoever does that integration.
8. ~~**Add the spectrum analyzer to `coda60.py`**~~ **Partly done.** Every Spectrum
   GET is in the adapter registry and the verifier, and `/Spectrum`, `/FreqList` and
   `/RTData` are measured working. `/Constellation` and `/FreqPower` 500 on this build
   (the handler reads `$args['query']`, which the route never supplies), so they stay
   out of the poller.
9. **Fix upstream, not around:** the PHP-8 fatals are a firmware bug. If Hitron ships a
   build with `getInstanceIDs()` and the `$radio`/`$f3` signature corrected, most of
   the 48 dead endpoints come back with no client change.
