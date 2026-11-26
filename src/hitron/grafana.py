"""Grafana bundle generator.

``write_bundle(dir)`` produces a self-contained provisioning tree:

    <dir>/dashboards/hitron-coda6x.json
    <dir>/dashboards/hitron-ht-em5.json
    <dir>/provisioning/datasources/hitron-prometheus.yml
    <dir>/provisioning/dashboards/hitron.yml
    <dir>/prometheus/rules/hitron.rules.yml

The dashboards are also committed under ``dashboards/`` so they can be imported
straight from GitHub without running anything.
"""

from __future__ import annotations

import copy
import json
from pathlib import Path
from typing import Any

__all__ = ["coda6x_dashboard", "ht_em5_dashboard", "write_bundle", "DASHBOARD_UID", "MOCA_DASHBOARD_UID"]

DASHBOARD_UID = "hitron-coda6x"
MOCA_DASHBOARD_UID = "hitron-ht-em5"


def _ds() -> dict[str, str]:
    return {"type": "prometheus", "uid": "${DS}"}


def _target(expr: str, legend: str = "", ref: str = "A", instant: bool = False) -> dict[str, Any]:
    return {
        "datasource": _ds(),
        "expr": expr,
        "legendFormat": legend,
        "refId": ref,
        "instant": instant,
        "range": not instant,
    }


def _panel(
    *,
    title: str,
    type_: str,
    grid: tuple[int, int, int, int],
    targets: list[dict[str, Any]] | None = None,
    unit: str = "short",
    description: str = "",
    options: dict[str, Any] | None = None,
    field_config: dict[str, Any] | None = None,
) -> dict[str, Any]:
    x, y, w, h = grid
    panel: dict[str, Any] = {
        "title": title,
        "type": type_,
        "datasource": _ds(),
        "gridPos": {"x": x, "y": y, "w": w, "h": h},
        "targets": targets or [],
        "options": options or {},
        "fieldConfig": field_config
        or {
            "defaults": {
                "unit": unit,
                "custom": {"lineWidth": 2, "fillOpacity": 8, "showPoints": "never"},
            },
            "overrides": [],
        },
    }
    if description:
        panel["description"] = description
    return panel


def _stat(title: str, expr: str, grid: tuple[int, int, int, int], unit: str = "short", description: str = "", thresholds: list[dict[str, Any]] | None = None) -> dict[str, Any]:
    config = {
        "defaults": {
            "unit": unit,
            "color": {"mode": "thresholds"},
            "thresholds": {"mode": "absolute", "steps": thresholds or [{"color": "red", "value": None}, {"color": "green", "value": 1}]},
        },
        "overrides": [],
    }
    return _panel(
        title=title,
        type_="stat",
        grid=grid,
        targets=[_target(expr, ref="A", instant=True)],
        unit=unit,
        description=description,
        options={"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False}, "colorMode": "background", "graphMode": "none"},
        field_config=config,
    )


def _row(title: str, y: int) -> dict[str, Any]:
    return {"title": title, "type": "row", "collapsed": False, "gridPos": {"x": 0, "y": y, "w": 24, "h": 1}, "panels": []}


def _dashboard(title: str, uid: str, panels: list[dict[str, Any]], description: str, tags: list[str]) -> dict[str, Any]:
    return {
        "uid": uid,
        "title": title,
        "description": description,
        "tags": ["hitron", *tags],
        "timezone": "browser",
        "schemaVersion": 39,
        "version": 1,
        "refresh": "30s",
        "time": {"from": "now-6h", "to": "now"},
        "editable": True,
        "graphTooltip": 1,
        "annotations": {"list": [{"builtIn": 1, "datasource": {"type": "grafana", "uid": "-- Grafana --"}, "enable": True, "hide": True, "type": "dashboard"}]},
        "templating": {
            "list": [
                {
                    "name": "DS",
                    "label": "Prometheus",
                    "type": "datasource",
                    "query": "prometheus",
                    "current": {},
                    "hide": 0,
                    "includeAll": False,
                    "multi": False,
                    "refresh": 1,
                }
            ]
        },
        "panels": panels,
    }


def coda6x_dashboard() -> dict[str, Any]:
    panels: list[dict[str, Any]] = []
    panels.append(_row("Overview", 0))
    panels.append(_stat("Uptime", 'modem_system_uptime_seconds{source=~"$source"}', (0, 1, 4, 4), unit="s", description="From Router/SysInfo."))
    panels.append(_stat("Provisioning", 'min(modem_docsis_provisioning_step_success{source=~"$source"})', (4, 1, 4, 4), description="1 only when every DOCSIS provisioning step succeeded."))
    panels.append(_stat("DS channels", 'count(modem_downstream_channel_info{source=~"$source"})', (8, 1, 4, 4)))
    panels.append(_stat("US channels", 'count(modem_upstream_channel_info{source=~"$source"})', (12, 1, 4, 4)))
    panels.append(_stat("Wi-Fi clients", 'sum(modem_wifi_client_count{source=~"$source"})', (16, 1, 4, 4)))
    panels.append(_stat("Power in spec", 'min(modem_power_in_spec{source=~"$source"})', (20, 1, 4, 4), description="Only emitted for the CODA-57 legacy source, where the carrier envelope is known."))

    panels.append(_row("Downstream RF", 5))
    panels.append(_panel(title="Downstream power per channel", type_="timeseries", grid=(0, 6, 12, 8), targets=[_target('modem_downstream_channel_power_dbmv{source=~"$source"}', "ch {{channel_id}}")], unit="dBmV"))
    panels.append(_panel(title="Downstream SNR per channel", type_="timeseries", grid=(12, 6, 12, 8), targets=[_target('modem_downstream_channel_snr_db{source=~"$source"}', "ch {{channel_id}}")], unit="dB"))
    panels.append(_panel(title="Downstream corrected codewords/s", type_="timeseries", grid=(0, 14, 12, 8), targets=[_target('rate(modem_downstream_channel_corrected_total{source=~"$source"}[5m])', "ch {{channel_id}}")], unit="ops"))
    panels.append(_panel(title="Downstream uncorrectable codewords/s", type_="timeseries", grid=(12, 14, 12, 8), targets=[_target('rate(modem_downstream_channel_uncorrectable_total{source=~"$source"}[5m])', "ch {{channel_id}}")], unit="ops"))

    panels.append(_row("Upstream RF", 22))
    panels.append(_panel(title="Upstream power per channel", type_="timeseries", grid=(0, 23, 12, 8), targets=[_target('modem_upstream_channel_power_dbmv{source=~"$source"}', "ch {{channel_id}}")], unit="dBmV"))
    panels.append(_panel(title="Upstream channel bandwidth", type_="timeseries", grid=(12, 23, 12, 8), targets=[_target('modem_upstream_channel_bandwidth_hz{source=~"$source"}', "ch {{channel_id}}")], unit="Hz"))

    panels.append(_row("OFDM / OFDMA", 31))
    panels.append(_panel(title="OFDM PLC power", type_="timeseries", grid=(0, 32, 8, 8), targets=[_target('modem_downstream_ofdm_channel_plc_power_dbmv{source=~"$source"}', "idx {{index}}")], unit="dBmV"))
    panels.append(_panel(title="OFDM SNR", type_="timeseries", grid=(8, 32, 8, 8), targets=[_target('modem_downstream_ofdm_channel_snr_db{source=~"$source"}', "idx {{index}}")], unit="dB"))
    panels.append(_panel(title="OFDMA power (DOCSIS 3.0 equivalent)", type_="timeseries", grid=(16, 32, 8, 8), targets=[_target('modem_upstream_ofdm_channel_power_1_6_dbmv{source=~"$source"}', "idx {{index}}")], unit="dBmV"))
    panels.append(_panel(title="OFDM lock state", type_="timeseries", grid=(0, 40, 12, 6), targets=[_target('modem_downstream_ofdm_channel_plc_locked{source=~"$source"}', "PLC {{index}}", "A"), _target('modem_downstream_ofdm_channel_ncp_locked{source=~"$source"}', "NCP {{index}}", "B")], unit="short"))
    panels.append(_panel(title="OFDMA operating", type_="timeseries", grid=(12, 40, 12, 6), targets=[_target('modem_upstream_ofdm_channel_operating{source=~"$source"}', "idx {{index}}")], unit="short"))

    panels.append(_row("Wireless, hosts, MoCA", 46))
    panels.append(_panel(title="Wi-Fi clients per band", type_="timeseries", grid=(0, 47, 8, 7), targets=[_target('modem_wifi_client_count{source=~"$source"}', "{{band}}")], unit="short"))
    panels.append(_panel(title="Client RSSI", type_="timeseries", grid=(8, 47, 8, 7), targets=[_target('modem_wifi_client_rssi_dbm{source=~"$source"}', "{{hostname}} ({{mac}})")], unit="dBm"))
    panels.append(_panel(title="LAN hosts", type_="timeseries", grid=(16, 47, 8, 7), targets=[_target('modem_host_count{source=~"$source"}', "hosts")], unit="short"))
    panels.append(_panel(title="Ethernet port link", type_="timeseries", grid=(0, 54, 12, 6), targets=[_target('modem_ethernet_port_link_up{source=~"$source"}', "port {{port_id}}")], unit="short"))
    panels.append(_panel(title="MoCA link", type_="timeseries", grid=(12, 54, 12, 6), targets=[_target('modem_moca_link_up{source=~"$source"}', "link")], unit="short"))

    panels.append(_row("Channels table", 60))
    panels.append(
        _panel(
            title="Downstream channel inventory",
            type_="table",
            grid=(0, 61, 12, 8),
            targets=[_target('modem_downstream_channel_info{source=~"$source"}', "", "A", instant=True)],
            options={"showHeader": True},
            field_config={"defaults": {"custom": {"align": "auto"}}, "overrides": []},
        )
    )
    panels.append(
        _panel(
            title="Upstream channel inventory",
            type_="table",
            grid=(12, 61, 12, 8),
            targets=[_target('modem_upstream_channel_info{source=~"$source"}', "", "A", instant=True)],
            options={"showHeader": True},
            field_config={"defaults": {"custom": {"align": "auto"}}, "overrides": []},
        )
    )

    panels.append(_row("Exporter health and logs", 69))
    panels.append(_stat("Sources up", 'sum(hitron_exporter_up)', (0, 70, 6, 4), description="1 per reachable device."))
    panels.append(_stat("Samples", 'sum(hitron_exporter_samples)', (6, 70, 6, 4)))
    panels.append(_panel(title="Scrape duration", type_="timeseries", grid=(12, 70, 12, 4), targets=[_target('hitron_exporter_scrape_duration_seconds', "{{source}}")], unit="s"))
    panels.append(
        _panel(
            title="Recent scrape errors",
            type_="table",
            grid=(0, 74, 24, 7),
            targets=[_target('hitron_exporter_scrape_error_info', "", "A", instant=True)],
            options={"showHeader": True, "sortBy": [{"displayName": "source", "desc": False}]},
            field_config={"defaults": {"custom": {"align": "auto"}}, "overrides": []},
            description="One row per failing source. Empty when every poll succeeds.",
        )
    )
    panels.append(
        {
            "title": "Exporter log stream (requires Loki)",
            "type": "logs",
            "datasource": {"type": "loki", "uid": "${LOKI}"},
            "gridPos": {"x": 0, "y": 81, "w": 24, "h": 8},
            "targets": [{"expr": '{job="hitron"}', "refId": "A"}],
            "options": {"showTime": True, "wrapLogMessage": True, "sortOrder": "Descending"},
            "description": "Optional. Scrape failures are also visible as the hitron_exporter_scrape_error_info metric without Loki.",
        }
    )

    dashboard = _dashboard(
        "Hitron CODA6X — DOCSIS, Wi-Fi, and MoCA",
        DASHBOARD_UID,
        panels,
        "RF, provisioning, wireless, host, and exporter health for any CODA6X gateway (CODA-60 class) scraped by hitron-exporter.",
        ["coda6x", "docsis", "prometheus"],
    )
    dashboard["__requires"] = [
        {"type": "grafana", "id": "grafana", "name": "Grafana", "version": "10.0.0"},
        {"type": "datasource", "id": "prometheus", "name": "Prometheus", "version": "1.0.0"},
    ]
    return dashboard


def ht_em5_dashboard() -> dict[str, Any]:
    panels: list[dict[str, Any]] = []
    panels.append(_row("Overview", 0))
    panels.append(_stat("MoCA link", "max(hitron_moca_link_up)", (0, 1, 6, 4)))
    panels.append(_stat("Nodes visible", "max(hitron_moca_node_count)", (6, 1, 6, 4)))
    panels.append(_stat("Beacon channel (MHz)", "max(hitron_moca_beacon_channel_mhz)", (12, 1, 6, 4)))
    panels.append(_stat("TX power index", "max(hitron_moca_tx_power_index)", (18, 1, 6, 4)))

    panels.append(_row("MoCA network", 5))
    panels.append(_panel(title="Ethernet frames/s", type_="timeseries", grid=(0, 6, 12, 8), targets=[_target('rate(hitron_moca_ethernet_frames_total[5m])', "{{direction}} {{outcome}}")], unit="ops"))
    panels.append(_panel(title="PHY rate by node pair", type_="timeseries", grid=(12, 6, 12, 8), targets=[_target('hitron_moca_phy_rate_mbps', "{{node_id}} → {{peer_node_id}}")], unit="Mbps", description="Only present when the exporter runs with --phy-rates."))
    panels.append(_panel(title="Configured bands", type_="timeseries", grid=(0, 14, 12, 7), targets=[_target('hitron_moca_band_enabled', "{{band}}")], unit="short"))
    panels.append(_panel(title="Node inventory", type_="table", grid=(12, 14, 12, 7), targets=[_target('hitron_moca_node_present', "", "A", instant=True)], options={"showHeader": True}, field_config={"defaults": {"custom": {"align": "auto"}}, "overrides": []}))

    panels.append(_row("Exporter health", 21))
    panels.append(_stat("Sources up", 'sum(hitron_exporter_up)', (0, 22, 6, 4)))
    panels.append(_panel(title="Scrape duration", type_="timeseries", grid=(6, 22, 18, 4), targets=[_target('hitron_exporter_scrape_duration_seconds', "{{source}}")], unit="s"))

    dashboard = _dashboard(
        "Hitron HT-EM5 — MoCA bridge",
        MOCA_DASHBOARD_UID,
        panels,
        "MoCA link, node inventory, PHY rates, and Ethernet counters for HT-EM5 bridges.",
        ["ht-em5", "moca", "prometheus"],
    )
    dashboard["__requires"] = [
        {"type": "grafana", "id": "grafana", "name": "Grafana", "version": "10.0.0"},
        {"type": "datasource", "id": "prometheus", "name": "Prometheus", "version": "1.0.0"},
    ]
    return dashboard


PROMETHEUS_RULES = """groups:
  - name: hitron
    interval: 30s
    rules:
      - alert: HitronSourceDown
        expr: hitron_exporter_up == 0
        for: 2m
        labels: {severity: critical}
        annotations:
          summary: "Hitron source {{ $labels.source }} is unreachable"
          description: "hitron-exporter has failed to poll {{ $labels.source }} for 2 minutes."
      - alert: HitronProvisioningFailed
        expr: min by (source) (modem_docsis_provisioning_step_success) == 0
        for: 10m
        labels: {severity: warning}
        annotations:
          summary: "DOCSIS provisioning incomplete on {{ $labels.source }}"
          description: "At least one provisioning step is not Success."
      - alert: HitronDownstreamPowerOutOfSpec
        expr: min by (source) (modem_downstream_channel_power_dbmv) < -6 or max by (source) (modem_downstream_channel_power_dbmv) > 15
        for: 10m
        labels: {severity: warning}
        annotations:
          summary: "Downstream power outside -6..15 dBmV on {{ $labels.source }}"
      - alert: HitronUpstreamPowerOutOfSpec
        expr: min by (source) (modem_upstream_channel_power_dbmv) < 35 or max by (source) (modem_upstream_channel_power_dbmv) > 48
        for: 10m
        labels: {severity: warning}
        annotations:
          summary: "Upstream power outside 35..48 dBmV on {{ $labels.source }}"
      - alert: HitronLowDownstreamSNR
        expr: min by (source) (modem_downstream_channel_snr_db) < 33
        for: 10m
        labels: {severity: warning}
        annotations:
          summary: "Downstream SNR below 33 dB on {{ $labels.source }}"
      - alert: HitronUncorrectableErrors
        expr: sum by (source) (rate(modem_downstream_channel_uncorrectable_total[5m])) > 1
        for: 10m
        labels: {severity: warning}
        annotations:
          summary: "Uncorrectable codewords on {{ $labels.source }}"
      - alert: HitronMocaLinkDown
        expr: hitron_moca_link_up == 0
        for: 5m
        labels: {severity: warning}
        annotations:
          summary: "MoCA link down on {{ $labels.host }}"
      - alert: HitronExporterScrapeSlow
        expr: hitron_exporter_scrape_duration_seconds > 20
        for: 5m
        labels: {severity: info}
        annotations:
          summary: "Polling {{ $labels.source }} is slow"
"""

DATASOURCE_YML = """apiVersion: 1
datasources:
  - name: Prometheus
    uid: prometheus
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
    editable: true
  - name: Loki
    uid: loki
    type: loki
    access: proxy
    url: http://loki:3100
    editable: true
"""

DASHBOARD_PROVIDER_YML = """apiVersion: 1
providers:
  - name: hitron
    orgId: 1
    folder: Hitron
    type: file
    disableDeletion: false
    updateIntervalSeconds: 30
    allowUiUpdates: true
    options:
      path: /var/lib/grafana/dashboards
"""


def write_bundle(directory: str | Path) -> dict[str, Path]:
    """Write the full Grafana/Prometheus provisioning bundle."""
    root = Path(directory)
    dash_dir = root / "dashboards"
    prov_ds = root / "provisioning" / "datasources"
    prov_dash = root / "provisioning" / "dashboards"
    rules_dir = root / "prometheus" / "rules"
    for d in (dash_dir, prov_ds, prov_dash, rules_dir):
        d.mkdir(parents=True, exist_ok=True)

    written: dict[str, Path] = {}
    for name, dashboard in (("hitron-coda6x.json", coda6x_dashboard()), ("hitron-ht-em5.json", ht_em5_dashboard())):
        path = dash_dir / name
        path.write_text(json.dumps(dashboard, indent=2) + "\n")
        written[name] = path

    (prov_ds / "hitron-prometheus.yml").write_text(DATASOURCE_YML)
    (prov_dash / "hitron.yml").write_text(DASHBOARD_PROVIDER_YML)
    (rules_dir / "hitron.rules.yml").write_text(PROMETHEUS_RULES)
    written["datasource"] = prov_ds / "hitron-prometheus.yml"
    written["provider"] = prov_dash / "hitron.yml"
    written["rules"] = rules_dir / "hitron.rules.yml"
    return written
