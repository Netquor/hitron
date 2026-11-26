"""Lock in the exact write-request shapes confirmed from the CODA6X firmware's own
PHP source (ModelInfo.php::doRebootAction, AdminPassword.php). No network access -
patches aiohttp's session with a fake that just records what was sent."""

import json

import pytest

from hitron.coda60 import Coda60Client, WriteNotAuthenticated


class _FakeResponse:
    def __init__(self, body=None, status=200):
        self._body = body if body is not None else {"success": True}
        self.status = status

    async def json(self, content_type=None):
        return self._body

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc):
        return False


class _FakeSession:
    def __init__(self):
        self.calls = []  # list of (method, url, kwargs)
        self.closed = False

    def post(self, url, **kwargs):
        self.calls.append(("POST", url, kwargs))
        return _FakeResponse()

    def put(self, url, **kwargs):
        self.calls.append(("PUT", url, kwargs))
        return _FakeResponse()

    async def close(self):
        self.closed = True


@pytest.mark.asyncio
async def test_reboot_requires_login_first():
    client = Coda60Client("192.168.100.1")
    with pytest.raises(WriteNotAuthenticated):
        await client.reboot()


@pytest.mark.asyncio
async def test_reboot_payload_shape():
    client = Coda60Client("192.168.100.1")
    client._session = _FakeSession()  # skip real login for this test
    await client.reboot()
    method, url, kwargs = client._session.calls[0]
    assert method == "POST"
    assert url.endswith("/1/Device/CM/Reboot")
    assert json.loads(kwargs["data"]["model"]) == {"reboot": 1}


@pytest.mark.asyncio
async def test_factory_reset_payload_shape():
    client = Coda60Client("192.168.100.1")
    client._session = _FakeSession()
    await client.factory_reset()
    _, url, kwargs = client._session.calls[0]
    assert url.endswith("/1/Device/CM/Reboot")
    assert json.loads(kwargs["data"]["model"]) == {"factoryReset": 1}


@pytest.mark.asyncio
async def test_hard_reset_payload_shape():
    client = Coda60Client("192.168.100.1")
    client._session = _FakeSession()
    await client.hard_reset_and_reboot()
    _, url, kwargs = client._session.calls[0]
    assert json.loads(kwargs["data"]["model"]) == {"restoreReset": 1}


@pytest.mark.asyncio
async def test_set_admin_password_payload_shape():
    client = Coda60Client("192.168.100.1")
    client._session = _FakeSession()
    await client.set_admin_password("oldpw", "newpw")
    method, url, kwargs = client._session.calls[0]
    assert method == "POST"
    assert url.endswith("/1/Device/Manage/Password")
    body = json.loads(kwargs["data"]["model"])
    assert body["oldPassword"] == "oldpw"
    assert body["newPassword"] == "newpw"
    assert body["newPasswordConfirm"] == "newpw"  # defaults to newPassword when omitted


@pytest.mark.asyncio
async def test_set_admin_username_payload_shape():
    client = Coda60Client("192.168.100.1")
    client._session = _FakeSession()
    await client.set_admin_username("newadmin")
    _, url, kwargs = client._session.calls[0]
    assert url.endswith("/1/Device/Manage/Username")
    assert json.loads(kwargs["data"]["model"]) == {"newUsername": "newadmin"}


@pytest.mark.asyncio
async def test_login_payload_shape():
    client = Coda60Client("192.168.100.1")
    client._session = _FakeSession()
    await client.login("admin", "sekrit")
    method, url, kwargs = client._session.calls[0]
    assert method == "POST"
    assert url.endswith("/1/Device/Users/Login")
    assert json.loads(kwargs["data"]["model"]) == {"username": "admin", "password": "sekrit"}


@pytest.mark.asyncio
async def test_set_wifi_radio_requires_login_first():
    client = Coda60Client("192.168.100.1")
    with pytest.raises(WriteNotAuthenticated):
        await client.set_wifi_radio(1, {"channel": 6})
