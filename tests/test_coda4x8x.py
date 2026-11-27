"""CODA 4x8x client: experimental, read-only, not verified on live hardware.

Everything here runs against a SYNTHETIC in-process server. The fixture bodies
are invented to match the field names the CODA6X mapper reads; they were not
captured from a CODA-4582 or CODA-4680.
"""

import argparse
import json
import re
from urllib.parse import parse_qs

import pytest
from aiohttp import web

from hitron import Coda4x8xClient, coda4x8x_samples, detect_api_root
from hitron import exporter
from hitron.coda4x8x import READ_ROUTES, Coda4x8xError, Coda4x8xNotLoggedIn

# SYNTHETIC fixtures (not captured from hardware).
SYNTHETIC = {
    "CM/Version": {"modelName": "SYNTH-4x8x", "vendorName": "Synthetic", "hwVersion": "1A", "swVersion": "0.0.0-synthetic", "serialNumber": "SYN0001"},
    "Router/SysInfo": {"hwVersion": "1A", "swVersion": "0.0.0-synthetic", "serialNumber": "SYN0001", "systemUptime": "1 days,02h:03m:04s", "rfMac": "00:11:22:33:44:55"},
    "CM/DsInfo": {"Freq_List": [{"portId": "1", "frequency": "267000000", "modulation": "256QAM", "signalStrength": "3.5", "snr": "40.1", "channelId": "7", "dsoctets": "1000", "correcteds": "5", "uncorrect": "0"}]},
    "CM/UsInfo": {"Freq_List": [{"portId": "1", "frequency": "30000000", "modtype": "64QAM", "signalStrength": "40.0", "bandwidth": "6400000", "channelId": "2", "scdmaMode": "ATDMA"}]},
}


def _fresh_routes():
    return {k: dict(v) for k, v in SYNTHETIC.items()}


class _Server:
    def __init__(self, require_login=False, csrf_body=None, login_body=None):
        self.requests = []  # (method, path, form)
        self.require_login = require_login
        self.csrf_body = csrf_body if csrf_body is not None else {"Csrf_token": "tok-1"}
        self.login_body = login_body if login_body is not None else {"errCode": "000", "errMsg": ""}
        self.runner = None
        self.host = ""

    async def _handle(self, request: web.Request):
        form = parse_qs((await request.read()).decode()) if request.method == "POST" else {}
        self.requests.append((request.method, request.path, form, request.headers.get("Cookie", "")))
        path = request.path.lstrip("/")
        if request.method == "POST" and path == "Users/Login":
            resp = web.json_response(self.login_body)
            resp.set_cookie("SESSION", "abc")
            return resp
        if self.require_login and "SESSION=abc" not in request.headers.get("Cookie", ""):
            return web.Response(status=302, headers={"Location": "/login.html"})
        if path == "Users/CSRF":
            return web.json_response(self.csrf_body)
        if path in SYNTHETIC:
            return web.json_response(SYNTHETIC[path])
        return web.Response(status=404, text="nope")

    async def start(self):
        app = web.Application()
        app.router.add_route("*", "/{tail:.*}", self._handle)
        self.runner = web.AppRunner(app)
        await self.runner.setup()
        site = web.TCPSite(self.runner, "127.0.0.1", 0)
        await site.start()
        port = self.runner.addresses[0][1]
        self.host = f"127.0.0.1:{port}"
        return self

    async def stop(self):
        await self.runner.cleanup()


@pytest.fixture
async def server():
    s = await _Server().start()
    yield s
    await s.stop()


def test_route_count_and_no_prefix():
    assert len(READ_ROUTES) == 31
    assert not any(r.startswith("1/Device") for r in READ_ROUTES)
    assert Coda4x8xClient("192.168.0.1").base_url == "http://192.168.0.1"


async def test_get_all_uses_root_paths(server):
    snapshot = await Coda4x8xClient(server.host).get_all()
    paths = {p for _, p, _, _ in server.requests}
    assert "/CM/DsInfo" in paths and "/Router/SysInfo" in paths
    assert not any(p.startswith("/1/Device") for p in paths)
    assert all(method == "GET" for method, *_ in server.requests)
    assert snapshot["metadata"]["family"] == "coda4x8x"
    assert "not verified on live hardware" in snapshot["metadata"]["status"]
    assert snapshot["metrics"]["cm_dsinfo"]["Freq_List"][0]["channelId"] == "7"
    # routes the synthetic server 404s are recorded per endpoint, not fatal
    assert snapshot["metrics"]["cm_log"] == {"error": "GET CM/Log: status 404"}


async def test_snapshot_to_samples_keeps_metric_names(server):
    snapshot = await Coda4x8xClient(server.host).get_all()
    samples = coda4x8x_samples(snapshot, source="lab4")
    names = {s.name for s in samples}
    assert "modem_downstream_channel_power_dbmv" in names
    assert "modem_info" in names
    power = next(s for s in samples if s.name == "modem_downstream_channel_power_dbmv")
    assert power.value == pytest.approx(3.5)
    assert power.labels["source"] == "lab4"
    assert coda4x8x_samples(snapshot)[0].labels["source"] == "coda4x8x"


async def test_login_encodes_model_form_body():
    s = await _Server().start()
    try:
        async with Coda4x8xClient(s.host) as client:
            assert await client.login("admin", "p@ss word&=") is True
    finally:
        await s.stop()
    method, path, form, _ = s.requests[0]
    assert (method, path) == ("POST", "/Users/Login")
    assert json.loads(form["model"][0]) == {"username": "admin", "password": "p@ss word&="}


async def test_login_then_reads_send_the_session_cookie():
    s = await _Server(require_login=True).start()
    try:
        async with Coda4x8xClient(s.host) as client:
            await client.login("admin", "pw")
            body = await client.get_json("CM/Version")
    finally:
        await s.stop()
    assert body["modelName"] == "SYNTH-4x8x"
    assert "SESSION=abc" in s.requests[-1][3]


async def test_redirect_is_not_logged_in():
    s = await _Server(require_login=True).start()
    try:
        client = Coda4x8xClient(s.host)
        with pytest.raises(Coda4x8xNotLoggedIn):
            await client.get_json("CM/DsInfo")
        with pytest.raises(Coda4x8xNotLoggedIn):
            await client.get_all()
    finally:
        await s.stop()


async def test_login_rejected_raises():
    s = await _Server(login_body={"errCode": "001", "errMsg": "bad credentials"}).start()
    try:
        async with Coda4x8xClient(s.host) as client:
            with pytest.raises(Coda4x8xError, match="bad credentials"):
                await client.login("admin", "wrong")
    finally:
        await s.stop()


@pytest.mark.parametrize(
    "body,token",
    [({"Csrf_token": "a"}, "a"), ({"token": "b"}, "b"), ({"Csrf_token": "c", "token": "d"}, "c")],
)
async def test_csrf_token_field_variants(body, token):
    s = await _Server(csrf_body=body).start()
    try:
        assert await Coda4x8xClient(s.host).csrf() == token
    finally:
        await s.stop()


async def test_csrf_missing_token_raises():
    s = await _Server(csrf_body={"other": 1}).start()
    try:
        with pytest.raises(Coda4x8xError, match="CSRF"):
            await Coda4x8xClient(s.host).csrf()
    finally:
        await s.stop()


@pytest.mark.parametrize(
    "name",
    ["reboot", "factory_reset", "hard_reset_and_reboot", "set_admin_password", "set_admin_username", "set_wifi_radio", "reset_fec", "update_radio"],
)
def test_no_write_methods(name):
    client = Coda4x8xClient()
    assert not hasattr(client, name)
    assert not hasattr(Coda4x8xClient, name)
    with pytest.raises(AttributeError, match="read-only"):
        getattr(client, name)


def test_unknown_attribute_message_is_plain():
    with pytest.raises(AttributeError, match="no attribute 'bogus'"):
        Coda4x8xClient().bogus


async def test_exporter_collects_with_kind_label(server):
    config = exporter.Config(coda4x8x=[exporter.Target(name="edge", host=server.host)])
    snap = await exporter.Collector(config).collect()
    assert snap.sources["edge"]["kind"] == "coda4x8x" and snap.sources["edge"]["up"]
    up = next(s for s in snap.samples if s.name == "hitron_exporter_up")
    assert up.labels == {"source": "edge", "kind": "coda4x8x"}


async def test_exporter_reports_login_required_as_down():
    s = await _Server(require_login=True).start()
    try:
        config = exporter.Config(coda4x8x=[exporter.Target(name="edge", host=s.host)])
        snap = await exporter.Collector(config).collect()
    finally:
        await s.stop()
    assert snap.sources["edge"]["up"] is False
    assert "not logged in" in snap.errors[0]["error"]


def _parse(argv):
    ap = argparse.ArgumentParser()
    for flag in ("ht_em5", "coda60", "coda57", "coda4x8x"):
        ap.add_argument("--" + flag.replace("_", "-"), action="append", default=[])
    ap.add_argument("--coda60-model", default=None)
    ap.set_defaults(interval=30.0, phy_rates=False, ds_spec_min=-6.0, ds_spec_max=15.0, us_spec_min=35.0, us_spec_max=48.0)
    return ap.parse_args(argv)


def test_exporter_arg_and_env_wiring(monkeypatch):
    for var in ("HITRON_CODA4X8X_HOSTS", "HITRON_CODA4X8X_HOST"):
        monkeypatch.delenv(var, raising=False)
    cfg = exporter.build_config(_parse(["--coda4x8x", "edge=10.0.0.1", "--coda4x8x", "10.0.0.2"]))
    assert [(t.name, t.host) for t in cfg.coda4x8x] == [("edge", "10.0.0.1"), ("10.0.0.2", "10.0.0.2")]
    monkeypatch.setenv("HITRON_CODA4X8X_HOST", "a=10.0.0.3")
    assert [t.host for t in exporter.build_config(_parse([])).coda4x8x] == ["10.0.0.3"]
    monkeypatch.setenv("HITRON_CODA4X8X_HOSTS", "10.0.0.4,10.0.0.5")
    assert [t.host for t in exporter.build_config(_parse([])).coda4x8x] == ["10.0.0.4", "10.0.0.5"]


def test_exporter_requires_at_least_one_target(monkeypatch, capsys):
    for var in ("HITRON_CODA4X8X_HOSTS", "HITRON_CODA4X8X_HOST", "HITRON_HT_EM5_HOSTS", "HITRON_CODA60_HOSTS", "HITRON_CODA60_HOST", "HITRON_CODA57_HOSTS", "HITRON_CODA57_HOST"):
        monkeypatch.delenv(var, raising=False)
    with pytest.raises(SystemExit):
        exporter.main([])
    assert re.search(r"--coda57 / --coda4x8x", capsys.readouterr().err)


# ---- detect_api_root -------------------------------------------------------


class _Probe:
    """Serves a chosen set of paths: {path: (status, body, content_type)}."""

    def __init__(self, routes):
        self.routes = routes
        self.order = []

    async def start(self):
        async def handle(request):
            self.order.append(request.path)
            hit = self.routes.get(request.path)
            if hit is None:
                return web.Response(status=404, text="<html>not found</html>", content_type="text/html")
            status, body, ctype = hit
            return web.Response(status=status, text=body, content_type=ctype)

        app = web.Application()
        app.router.add_route("GET", "/{tail:.*}", handle)
        self.runner = web.AppRunner(app)
        await self.runner.setup()
        await web.TCPSite(self.runner, "127.0.0.1", 0).start()
        self.host = f"127.0.0.1:{self.runner.addresses[0][1]}"
        return self

    async def stop(self):
        await self.runner.cleanup()


VERSION_JSON = json.dumps({"modelName": "SYNTH", "swVersion": "x"})


@pytest.mark.parametrize(
    "routes,expected,probed",
    [
        ({"/1/Device/CM/Version": (200, VERSION_JSON, "application/json"), "/CM/Version": (200, VERSION_JSON, "application/json")}, "coda6x", ["/1/Device/CM/Version"]),
        ({"/CM/Version": (200, VERSION_JSON, "application/json")}, "coda4x8x", ["/1/Device/CM/Version", "/CM/Version"]),
        ({"/CM/Version": (200, json.dumps({"errCode": "000", "modelName": "SYNTH"}), "text/plain")}, "coda4x8x", None),
        ({"/CM/Version": (200, json.dumps({"errCode": "404"}), "application/json")}, None, None),
        ({"/data/getSysInfo.asp": (200, json.dumps({"error": "not found"}), "application/json")}, None, None),
        ({"/data/getSysInfo.asp": (200, json.dumps([{"hwVersion": "1"}]), "application/json")}, "coda57", ["/1/Device/CM/Version", "/CM/Version", "/data/getSysInfo.asp"]),
        ({}, None, None),
    ],
)
async def test_detect_api_root_order(routes, expected, probed):
    p = await _Probe(routes).start()
    try:
        assert await detect_api_root(p.host, timeout=2) == expected
    finally:
        await p.stop()
    if probed is not None:
        assert p.order == probed


@pytest.mark.parametrize(
    "body",
    ["<html><body>Login</body></html>", "[]", "42", json.dumps({"unrelated": 1}), "not json"],
)
async def test_detect_api_root_rejects_html_and_lookalikes(body):
    routes = {
        "/1/Device/CM/Version": (200, body, "text/html"),
        "/CM/Version": (200, body, "text/html"),
        "/data/getSysInfo.asp": (200, "<html>login</html>", "text/html"),
    }
    p = await _Probe(routes).start()
    try:
        assert await detect_api_root(p.host, timeout=2) is None
    finally:
        await p.stop()


async def test_detect_api_root_redirect_is_no_match():
    p = await _Probe({"/CM/Version": (302, "", "text/html")}).start()
    try:
        assert await detect_api_root(p.host, timeout=2) is None
    finally:
        await p.stop()


async def test_detect_api_root_unreachable_host():
    assert await detect_api_root("127.0.0.1:1", timeout=1) is None


@pytest.mark.parametrize(
    "route",
    ["../1/Device/CM/Version", "CM/Version?x=1", "CM/Version#f", "1/Device/CM/Version", "CM/Reboot", "", "CM/../CM/Version"],
)
async def test_get_json_rejects_routes_outside_read_routes(route):
    from hitron import coda4x8x

    with pytest.raises(coda4x8x.Coda4x8xError):
        await Coda4x8xClient("127.0.0.1:1").get_json(route)


async def test_failed_login_closes_its_session():
    s = await _Server(login_body={"errCode": "001", "errMsg": "bad"}).start()
    try:
        client = Coda4x8xClient(s.host)
        with pytest.raises(Exception):
            await client.login("u", "p")
        assert client._session is None
    finally:
        await s.stop()
