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


class _DisconnectingSession(_FakeSession):
    """The firmware reboots before it finishes answering, so the socket dies."""

    def post(self, url, **kwargs):
        self.calls.append(("POST", url, kwargs))
        raise ConnectionResetError("connection reset by peer")


class _FECSession(_FakeSession):
    """Emulates the measured clear paths: PUT DsInfo zeroes every 3.0 counter at
    once, PUT DsOfdm zeroes one channel per call, and `stubborn` never clears."""

    def __init__(self, stubborn=None, order=("1", "4", "2", "3")):
        super().__init__()
        self.ds = {"1": [14, 644], "2": [0, 0]}
        self.ofdm = {"1": 428, "2": 9, "3": 1657, "4": 148}
        self.stubborn = stubborn
        self.order = list(order)
        self.next = 0

    def put(self, url, **kwargs):
        self.calls.append(("PUT", url, kwargs))
        if url.endswith("CM/DsInfo"):
            for counters in self.ds.values():
                counters[:] = [0, 0]
        elif url.endswith("CM/DsOfdm"):
            index = self.order[self.next % len(self.order)]
            self.next += 1
            if index != self.stubborn:
                self.ofdm[index] = 0
        else:  # pragma: no cover - a wrong URL is the test failing
            raise AssertionError(url)
        return _FakeResponse({"errCode": "000", "errMsg": ""})

    def get(self, url, **kwargs):
        self.calls.append(("GET", url, kwargs))
        if url.endswith("CM/DsInfo"):
            body = {
                "Freq_List": [
                    {
                        "channelId": channel,
                        "frequency": "267000000",
                        "correcteds": str(counters[0]),
                        "uncorrect": str(counters[1]),
                    }
                    for channel, counters in self.ds.items()
                ]
            }
        elif url.endswith("CM/DsOfdm"):
            body = {
                "Freq_List": [
                    {
                        "index": int(index),
                        "Subcarr0freqFreq": "296600000",
                        "correcteds": str(value),
                        "uncorrect": "0",
                    }
                    for index, value in self.ofdm.items()
                ]
                + [
                    {
                        "index": 5,
                        "Subcarr0freqFreq": "NA",
                        "correcteds": "NA",
                        "uncorrect": "NA",
                    }
                ]
            }
        else:  # pragma: no cover
            raise AssertionError(url)
        return _FakeResponse(body)


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
    # doRebootAction() reads all three flags before it picks one, and a missing
    # key is a PHP fatal on this build, so every body carries all three.
    assert json.loads(kwargs["data"]["model"]) == {"reboot": 1, "factoryReset": 0, "restoreReset": 0}


@pytest.mark.asyncio
async def test_factory_reset_payload_shape():
    client = Coda60Client("192.168.100.1")
    client._session = _FakeSession()
    await client.factory_reset()
    _, url, kwargs = client._session.calls[0]
    assert url.endswith("/1/Device/CM/Reboot")
    assert json.loads(kwargs["data"]["model"]) == {"reboot": 0, "factoryReset": 1, "restoreReset": 0}


@pytest.mark.asyncio
async def test_hard_reset_payload_shape():
    client = Coda60Client("192.168.100.1")
    client._session = _FakeSession()
    await client.hard_reset_and_reboot()
    _, url, kwargs = client._session.calls[0]
    assert json.loads(kwargs["data"]["model"]) == {"reboot": 0, "factoryReset": 0, "restoreReset": 1}


@pytest.mark.asyncio
async def test_hard_reset_treats_a_dropped_connection_as_success():
    client = Coda60Client("192.168.100.1")
    client._session = _DisconnectingSession()
    result = await client.hard_reset_and_reboot()
    assert "closed the connection" in result["result"]


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


class _ErroringReadSession(_FECSession):
    """Every read answers 500, the way the firmware does while it is busy."""

    def get(self, url, **kwargs):
        self.calls.append(("GET", url, kwargs))
        return _FakeResponse({"errCode": "001", "errMsg": "Invalid Paramters."}, status=500)


@pytest.mark.asyncio
async def test_reset_fecputs_both_endpoints_in_order():
    client = Coda60Client("192.168.100.1")
    session = _FECSession()
    client._session = session

    result = await client.reset_fec()

    puts = [url for method, url, _ in session.calls if method == "PUT"]
    assert puts[0].endswith("/1/Device/CM/DsInfo")
    assert all(url.endswith("/1/Device/CM/DsOfdm") for url in puts[1:])
    assert puts.count(puts[0]) == 1, "the 3.0 clear is deterministic, one call is enough"
    assert result["docsis_busy"] == 1, "an already-clean channel must not count as cleared"
    assert result["docsis_cleared"] == 1
    assert result["ofdm_busy"] == 4
    assert result["ofdm_cleared"] == 4
    assert result["ofdm_not_zero"] == []


@pytest.mark.asyncio
async def test_reset_fec_reports_a_channel_that_never_clears():
    client = Coda60Client("192.168.100.1")
    session = _FECSession(stubborn="3")
    client._session = session

    result = await client.reset_fec()

    assert result["ofdm_not_zero"] == ["3"]
    assert result["ofdm_cleared"] == 3
    puts = [url for method, url, _ in session.calls if method == "PUT"]
    assert len(puts) <= 9, "the OFDM retry loop must stay bounded"


@pytest.mark.asyncio
async def test_reset_fec_does_not_need_a_login():
    client = Coda60Client("192.168.100.1")
    client._session = _FECSession()
    await client.reset_fec()  # no WriteNotAuthenticated


@pytest.mark.asyncio
async def test_reset_fec_reports_a_failed_read_instead_of_a_clean_sweep():
    client = Coda60Client("192.168.100.1")
    client._session = _ErroringReadSession()

    # An unreadable counter route must not be mistaken for "everything cleared".
    with pytest.raises(RuntimeError):
        await client.reset_fec()
