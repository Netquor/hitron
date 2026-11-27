# Getting started, end to end

This is the long path: from a checkout to a dashboard that shows downstream
power, SNR, OFDM locks, and uncorrectable codewords. Every step is copy-paste.
If you only want the API reference, open the [static site](../site/index.html)
or read [`specs/openapi-coda60.yaml`](../specs/openapi-coda60.yaml).

> **Scope.** Read routes work on the analyzed CODA6X firmware without a login.
> Write routes include reboot, factory reset, credential changes, and config
> upload. The exporter never calls them. Do not run the `*-reboot`,
> `*-factory-reset`, or `*-hard-reset` subcommands on a connection you are not
> prepared to lose.

---

## 0. What you need

| Thing | Why |
| --- | --- |
| Python 3.11+ | clients, exporter, docs server |
| Network access to the gateway | usually `192.168.100.1` on the LAN side |
| Docker (optional) | the one-command Prometheus + Grafana stack in `deploy/` |
| A device you are allowed to poll | CODA6X, CODA-57, HT-EM5, or (experimental) CODA 4x8x |

You do **not** need Node, a bundler, or a build step. The site is plain HTML.

---

## 1. Install

```bash
git clone https://github.com/Netquor/hitron.git
cd hitron
python3 -m venv .venv
source .venv/bin/activate
pip install -e ".[dev]"
```

Sanity check:

```bash
hitron --help
hitron-exporter --help
```

`hitron` has read subcommands (`coda60`, `coda57`, `ht-em5`, and the experimental
`coda4x8x`) and guarded write subcommands (`coda60-reboot`,
`coda60-set-password`, …). The `coda4x8x` family has no write subcommands.
`hitron-exporter` is the long-running process.

---

## 2. Prove the device answers

```bash
hitron coda60 192.168.100.1
```

You should see a JSON snapshot with `cm_version`, `cm_dsinfo`, `cm_usinfo`,
`cm_dsofdm`, and friends. If the JSON is empty or every field is `null`, the
device is not a CODA6X or the address is wrong.

Useful spot checks:

```bash
hitron coda60 192.168.100.1 | jq '.cm_dsinfo.Freq_List[0]'
hitron coda60 192.168.100.1 | jq '.cm_dsofdm.Freq_List[0].SNR'
hitron ht-em5 192.168.0.212
hitron coda57 192.168.100.2 --spec-check
```

### Experimental: CODA 4x8x (CODA-4582, CODA-4680)

```bash
hitron coda4x8x 192.168.0.1                       # unauthenticated reads
hitron coda4x8x 192.168.0.1 --username USER --password PASS   # log in first
```

Status: **experimental, not verified on live hardware.** This family is read-only.
It is a hypothesis that the 4x8x serves the CODA6X data model from the site root
(no `/1/Device` prefix); the route list and login flow come from
[hairyhenderson/hitron_coda](https://github.com/hairyhenderson/hitron_coda) (MIT).
See [`KB.md`](KB.md) section 4 for the evidence, the unconfirmed auth flow, and the
checklist a real capture would need to answer. If you run it against a unit you own,
a sanitized capture helps. There is no reboot, reset, password or WiFi write for
this family.

If the CODA6X is on a different subnet, move your machine onto that subnet or
add a route. The console API has no cloud fallback.

### Firmware quirks you will meet

* `Raidos_List` (not `Radios_List`) is the radio array key.
* Numbers are strings, often with units or a multiplier suffix. The exporter
  normalizes them; raw JSON keeps the firmware's text.
* A dead downstream channel can report `-inf`; the exporter drops those points
  instead of plotting a spike.
* `POST` bodies are a form field named `model` containing JSON. `_method` inside
  that JSON can tunnel `PUT`/`DELETE`/`PATCH`.
* `errCode == "000"` means success; anything else is usually HTTP 400.

---

## 3. Run the exporter

```bash
hitron-exporter \
  --port 9877 \
  --interval 30 \
  --coda60 192.168.100.1 \
  --coda57 192.168.100.2 \
  --ht-em5 192.168.0.212
```

Add `--coda4x8x HOST` to poll an experimental CODA 4x8x the same way (read-only).
The exporter does not log in, so a unit that requires a login is reported down.
The Go `hitron-cli` has no `--coda4x8x` flag: it selects the family by detection
only (root `/CM/Version`), and detection never sends credentials.

Give a target a friendly label with `name=host`:

```bash
hitron-exporter --coda60 edge=192.168.100.1
```

That label becomes the `source` (or `host`) label on every series.

Endpoints served by the same process:

| Path | Use |
| --- | --- |
| `/metrics` | Prometheus text exposition |
| `/healthz` | JSON: per-source up state, sample counts, errors |
| `/api/v1/samples` | last snapshot as JSON |
| `/api/v1/logs` | recent exporter log lines |

```bash
curl -s http://127.0.0.1:9877/healthz | jq
curl -s http://127.0.0.1:9877/metrics | grep modem_downstream_channel_power
```

Environment variables work too: `HITRON_CODA60_HOSTS`,
`HITRON_CODA57_HOSTS`, `HITRON_CODA4X8X_HOSTS`, `HITRON_HT_EM5_HOSTS` (comma-separated), plus
`HITRON_EXPORTER_PORT`, `HITRON_EXPORTER_INTERVAL`, `HITRON_OTLP_ENDPOINT`.

### One-shot output in every format

```bash
# Prometheus text
hitron-exporter --coda60 192.168.100.1 --once

# Influx line protocol (what Telegraf eats)
hitron-exporter --coda60 192.168.100.1 --once --format telegraf

# OTLP/HTTP JSON payload, for inspection
hitron-exporter --coda60 192.168.100.1 --once --format otlp | jq '.resourceMetrics[0].scopeMetrics[0].metrics[0]'

# Debug JSON
hitron-exporter --coda60 192.168.100.1 --once --format json
```

---

## 4. Prometheus

Minimal scrape config:

```yaml
scrape_configs:
  - job_name: hitron
    scrape_interval: 30s
    scrape_timeout: 25s
    static_configs:
      - targets: ["192.168.1.10:9877"]
```

Reload Prometheus, then check **Status → Targets**. The `hitron` job should be
`UP`, and this query should return one series per downstream channel:

```promql
modem_downstream_channel_power_dbmv
```

Add the bundled alert rules:

```yaml
rule_files:
  - /etc/prometheus/rules/hitron.rules.yml
```

The rules live in `deploy/grafana/prometheus/rules/hitron.rules.yml` and cover
source down, provisioning failure, power outside −6…15 dBmV (downstream) or
35…48 dBmV (upstream), low SNR, uncorrectable errors, MoCA link down, and slow
scrapes. The power envelope is a starting point, not gospel — replace it with
your carrier's published range.

---

## 5. Grafana

Two dashboards ship in the repo, already generated from the same metric
vocabulary:

* `dashboards/hitron-coda6x.json` — RF, provisioning, OFDM/OFDMA, Wi-Fi,
  hosts, MoCA, exporter health, optional Loki log stream.
* `dashboards/hitron-ht-em5.json` — MoCA link, nodes, PHY rates, Ethernet
  counters.

### Option A: import by hand

1. Grafana → **Connections → Data sources → Prometheus**, URL
   `http://<prometheus>:9090`, Save & test.
2. **Dashboards → New → Import**, upload `dashboards/hitron-coda6x.json`.
3. When prompted for `DS`, pick the Prometheus datasource you just created.
4. Set the `source` variable to your exporter target name.

### Option B: provision everything

```bash
cd deploy
cp .env.example .env          # set CODA60_HOST, etc.
docker compose up -d
```

Open <http://localhost:3000> (admin / admin). The datasource and both
dashboards are provisioned into the **Hitron** folder. No import step.

The full walkthrough, including panel-by-panel notes and Loki, is in
[`GRAFANA.md`](GRAFANA.md).

---

## 6. Telegraf

Telegraf needs no plugin: the exporter writes Influx line protocol to stdout and
Telegraf parses it.

```toml
[[inputs.exec]]
  commands = ["/opt/hitron/bin/hitron-exporter --coda60 192.168.100.1 --once --format telegraf"]
  data_format = "influx"
  interval = "30s"
  timeout = "25s"

[[outputs.influxdb_v2]]
  urls = ["http://influxdb:8086"]
  token = "${INFLUX_TOKEN}"
  organization = "home"
  bucket = "hitron"
```

A complete example is in `deploy/telegraf/telegraf.conf`. Counters are emitted
as integers (`value=…i`), which InfluxDB stores as integers rather than floats.

---

## 7. OpenTelemetry

Point the exporter at any OTLP/HTTP metrics endpoint:

```bash
hitron-exporter --coda60 192.168.100.1 \
  --otlp-endpoint http://127.0.0.1:4318/v1/metrics \
  --otlp-header "authorization=Bearer $TOKEN"
```

It posts OTLP `ExportMetricsServiceRequest` JSON. A collector config that
receives it and re-exposes Prometheus on `:9464` is in
`deploy/otel/collector.yaml`:

```bash
otelcol --config deploy/otel/collector.yaml
```

Gauges become OTLP gauges; monotonic counters become OTLP sums with
`aggregationTemporality: 2`. Labels become datapoint attributes.

---

## 8. The static site

```bash
hitron site --device 192.168.100.1 --port 8000
```

Open <http://127.0.0.1:8000>. You get:

* a filterable API console generated from `site/openapi.json`,
* live RF charts fed by the responses you fetch (history in `localStorage`),
* a DOCSIS event-log viewer,
* a request log you can download as JSON,
* the Grafana tutorials and downloads.

The `--device` flag matters: the page calls `/proxy/...`, which the local server
forwards to the gateway. GitHub Pages cannot do that (mixed content + CORS), so
on Pages the console shows the error and points you back here.

To publish the site, enable GitHub Pages with **Source: GitHub Actions** or
**Deploy from a branch → /site**. Everything in `site/` is static.

---

## 9. Verify the whole chain

```bash
# 1. device answers
hitron coda60 192.168.100.1 >/dev/null && echo device-ok

# 2. exporter produces series
hitron-exporter --coda60 192.168.100.1 --once | grep -c '^modem_'

# 3. Prometheus sees the target
curl -s http://localhost:9090/api/v1/targets | jq -r '.data.activeTargets[].health'

# 4. the dashboard query returns data
curl -sG http://localhost:9090/api/v1/query \
  --data-urlencode 'query=count(modem_downstream_channel_info)' | jq '.data.result[0].value[1]'
```

If (1) works and (2) is zero, the device is a family the exporter does not
recognize. If (2) works and (3) fails, it is networking between Prometheus and
the exporter. If (3) works and (4) is empty, the dashboard variable `source`
does not match your target label.

---

## 10. Troubleshooting

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| `hitron coda60` returns empty | not a CODA6X, or wrong subnet | confirm model with `curl http://IP/1/Device/CM/Version` |
| Login returns 400 | firmware has auth disabled, or wrong body shape | read routes need no login; send `model={"username":…}` form-encoded |
| Exporter says `up 0` | timeout, wrong port, device rebooted | raise `--interval`, check `/healthz` error text |
| Prometheus target down | exporter not reachable from container | use `host.docker.internal` or the LAN IP, not `127.0.0.1` |
| Dashboard panels empty | variable `DS` points at the wrong datasource, or `source` filter mismatched | re-pick the datasource; clear `source` |
| Power looks like `-inf` | channel is down | expected; exporter drops the point |
| OTLP push 415 | endpoint expects protobuf | use a collector's HTTP receiver or a JSON-capable backend |
| Site console blocked | opened from GitHub Pages | run `hitron site --device …` |

Collect diagnostics for a bug report:

```bash
hitron coda60 192.168.100.1 > /tmp/device.json 2>/tmp/device.err
curl -s http://127.0.0.1:9877/healthz > /tmp/health.json
curl -s http://127.0.0.1:9877/api/v1/logs > /tmp/exporter-logs.json
```

Redact serial numbers, MAC addresses, and public IPs before attaching them.

---

## 11. Where things live

```
specs/openapi-coda60.yaml     full CODA6X API, 116 paths / 171 operations
specs/openapi-ht-em5.yaml     HT-EM5 MoCA API
specs/openapi-coda57-legacy.yaml  CODA-57 .asp API
specs/openapi-coda4x8x.yaml       CODA 4x8x API (experimental)
src/hitron/metrics.py         one normalized metric vocabulary
src/hitron/formats.py         Prometheus / Influx / OTLP / JSON encoders
src/hitron/exporter.py        poll loop, HTTP surface, OTLP push
src/hitron/grafana.py         dashboard + provisioning generator
src/hitron/site.py            static site server + gateway proxy
dashboards/                   importable Grafana JSON
deploy/                       compose, Prometheus, Telegraf, OTel, Loki
site/                         GitHub Pages build-free docs
docs/KB.md                    reverse-engineering notes
```

Next: [`GRAFANA.md`](GRAFANA.md) for the dashboard tour, or
[`KB.md`](KB.md) for how the API was reconstructed.
