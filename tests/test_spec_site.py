"""Guard rails for the spec and the generated artifacts.

These catch the failures that are easy to miss by eye: a dangling $ref, a path
template without its parameter, a duplicated operationId, drift between the YAML
spec and the JSON copy the site loads, and dashboards that stopped being valid
JSON.
"""

import json
import os
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[1]
SPEC_PATH = ROOT / "specs" / "openapi-coda60.yaml"
SITE_SPEC_PATH = ROOT / "site" / "openapi.json"


@pytest.fixture(scope="module")
def spec():
    return yaml.safe_load(SPEC_PATH.read_text())


def iter_operations(spec):
    methods = {"get", "post", "put", "delete", "patch", "head", "options"}
    for path, item in spec["paths"].items():
        for method, op in item.items():
            if method in methods and isinstance(op, dict) and op.get("operationId"):
                yield path, method, op, item


def deref(spec, ref):
    node = spec
    for part in ref.lstrip("#/").split("/"):
        node = node[part]
    return node


def test_spec_is_valid_openapi(spec):
    openapi_spec_validator = pytest.importorskip("openapi_spec_validator")
    validate = getattr(openapi_spec_validator, "validate", None) or openapi_spec_validator.validate_spec
    validate(spec)


def test_every_ref_resolves(spec):
    refs = []

    def walk(node):
        if isinstance(node, dict):
            for key, value in node.items():
                if key == "$ref" and isinstance(value, str):
                    refs.append(value)
                walk(value)
        elif isinstance(node, list):
            for value in node:
                walk(value)

    walk(spec)
    assert refs
    for ref in set(refs):
        assert ref.startswith("#/"), f"external ref not allowed: {ref}"
        assert deref(spec, ref) is not None, f"dangling ref: {ref}"


def test_operation_ids_are_unique(spec):
    ids = [op["operationId"] for _, _, op, _ in iter_operations(spec)]
    duplicates = {i for i in ids if ids.count(i) > 1}
    assert not duplicates, duplicates


def test_path_parameters_are_declared(spec):
    for path, method, op, item in iter_operations(spec):
        names = set()
        for param in (item.get("parameters") or []) + (op.get("parameters") or []):
            param = deref(spec, param["$ref"]) if "$ref" in param else param
            names.add(param["name"])
        for placeholder in path.strip("/").split("/"):
            if placeholder.startswith("{") and placeholder.endswith("}"):
                assert placeholder[1:-1] in names, f"{method.upper()} {path} missing {placeholder}"


def test_coverage_is_what_we_claim(spec):
    operations = list(iter_operations(spec))
    assert len(spec["paths"]) >= 110
    assert len(operations) >= 165
    tags = {op["tags"][0] for _, _, op, _ in operations}
    for required in ("CM", "WiFi", "Telemetry", "Firewall", "Spectrum", "Diagnostics"):
        assert required in tags


def test_advanced_signal_fields_exist(spec):
    schemas = spec["components"]["schemas"]
    ofdm = schemas["DsOfdmChannel"]["properties"]
    for field in ("plcpower", "SNR", "correcteds", "uncorrect", "Subcarr0freqFreq", "plclock", "ncplock", "mdc1lock"):
        assert field in ofdm
    ofdma = schemas["UsOfdmChannel"]["properties"]
    for field in ("repPower", "repPower1_6", "digAtten", "channelBw", "state", "fftVal"):
        assert field in ofdma
    pnm_properties = {}
    for part in schemas["Pnm"]["allOf"]:
        pnm_properties.update(part.get("properties", {}))
    for field in ("rxmer", "ch_est_coef", "us_pre_eq", "mod_prof"):
        assert field in pnm_properties


def test_destructive_routes_are_marked(spec):
    reboot = spec["paths"]["/1/Device/CM/Reboot"]["post"]
    assert reboot["x-hitron-destructive"] is True
    assert "/1/Device/Upload" in spec["paths"]
    assert "/1/Device/AdminRemoteManagement/Restore" in spec["paths"]


def test_site_spec_matches_yaml(spec):
    site_spec = json.loads(SITE_SPEC_PATH.read_text())
    assert site_spec == spec, "run the YAML -> site/openapi.json conversion after editing the spec"


def test_dashboards_are_valid_json():
    for name in ("hitron-coda6x.json", "hitron-ht-em5.json"):
        dashboard = json.loads((ROOT / "dashboards" / name).read_text())
        assert dashboard["uid"]
        assert dashboard["panels"]
        assert any(panel.get("type") == "row" for panel in dashboard["panels"])
        for panel in dashboard["panels"]:
            for target in panel.get("targets", []):
                assert target["expr"]
                # The optional Loki panel has its own datasource; Prometheus
                # panels all use the ${DS} dashboard variable.
                if "datasource" in target:
                    assert target["datasource"]["uid"] in ("${DS}", "${LOKI}")


def test_provisioning_bundle_exists():
    required = [
        ROOT / "deploy" / "compose.yaml",
        ROOT / "deploy" / "prometheus" / "prometheus.yml",
        ROOT / "deploy" / "telegraf" / "telegraf.conf",
        ROOT / "deploy" / "otel" / "collector.yaml",
        ROOT / "deploy" / "grafana" / "prometheus" / "rules" / "hitron.rules.yml",
        ROOT / "deploy" / "grafana" / "provisioning" / "dashboards" / "hitron.yml",
        ROOT / "deploy" / "grafana" / "provisioning" / "datasources" / "hitron-prometheus.yml",
    ]
    for path in required:
        assert path.is_file(), path


def test_site_assets_and_llms_present():
    for name in ("index.html", "openapi.json", "llms.txt", "assets/app.js", "assets/style.css"):
        assert (ROOT / "site" / name).is_file(), name
    # The root copy is what LLM crawlers fetch from the repo.
    assert (ROOT / "llms.txt").read_text().startswith("# hitron")
    assert (ROOT / "site" / "llms.txt").read_text() == (ROOT / "llms.txt").read_text()


def test_exporter_cli_declares_every_format():
    from hitron.exporter import main  # importable without a device

    assert callable(main)


def test_installer_is_shipped_with_the_site():
    script = ROOT / "install.sh"
    assert script.is_file()
    assert (ROOT / "site" / "install.sh").read_text() == script.read_text()
    text = script.read_text()
    # The one-liner documented everywhere must be the one the script supports.
    assert "--run" in text
    assert "--read-only" in text
    assert "https://github.com/$REPO/archive/$RESOLVED.tar.gz" in text
    assert "git ls-remote" in text
    # pip skips an unchanged direct-URL version, so an update must reinstall.
    assert "--force-reinstall" in text


def test_local_proxy_guide_is_linked():
    guide = ROOT / "docs" / "LOCAL-PROXY.md"
    assert guide.is_file()
    body = guide.read_text()
    assert "install.sh" in body
    assert "--read-only" in body
    assert "fetch failed" in body
    assert "LOCAL-PROXY.md" in (ROOT / "README.md").read_text()


def test_read_only_proxy_refuses_writes():
    import http.client
    import threading
    from http.server import ThreadingHTTPServer

    from hitron.site import _handler

    server = ThreadingHTTPServer(
        ("127.0.0.1", 0),
        _handler(ROOT / "site", "127.0.0.1:9", read_only=True),
    )
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        conn = http.client.HTTPConnection("127.0.0.1", server.server_port, timeout=5)
        conn.request("POST", "/proxy/1/Device/CM/Reboot", body='model={"reboot":1}')
        response = conn.getresponse()
        payload = response.read().decode()
        assert response.status == 403
        assert "read-only" in payload
        conn.close()
    finally:
        server.shutdown()
        server.server_close()


def test_cli_site_forwards_read_only():
    """The `hitron site` wrapper must expose every flag site.py supports."""
    import subprocess
    import sys

    result = subprocess.run(
        [sys.executable, "-m", "hitron.cli", "site", "--help"],
        capture_output=True,
        text=True,
        check=True,
        env={**os.environ, "PYTHONPATH": str(ROOT / "src")},
    )
    assert "--read-only" in result.stdout
    wrapper = (ROOT / "src" / "hitron" / "cli.py").read_text()
    assert 'argv += ["--read-only"]' in wrapper


def test_wheel_ships_the_site():
    """`hitron site` runs outside a checkout, so the site must be package data."""
    pyproject = (ROOT / "pyproject.toml").read_text()
    assert '[tool.hatch.build.targets.wheel.force-include]' in pyproject
    assert '"site" = "hitron/_site"' in pyproject
    assert (ROOT / "site" / "index.html").is_file()


def test_find_site_dir_falls_back_to_package_data(tmp_path, monkeypatch):
    from hitron import site as site_module

    package = tmp_path / "pkg"
    packaged = package / "_site"
    packaged.mkdir(parents=True)
    (packaged / "index.html").write_text("<!doctype html>")
    (tmp_path / "elsewhere").mkdir()

    monkeypatch.chdir(tmp_path / "elsewhere")
    monkeypatch.delenv("HITRON_SITE_DIR", raising=False)
    monkeypatch.setattr(site_module, "__file__", str(package / "site.py"))

    # <repo>/site would resolve to tmp_path/pkg/../../site, which does not exist.
    assert site_module.find_site_dir() == packaged.resolve()
