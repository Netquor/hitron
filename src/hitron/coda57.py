"""
Async client for the CODA-57's legacy ".asp" status API.

Ported from `~/Documents/hitron-client/main.py` + `test.py` and confirmed as
the CODA-57's own API (as distinct from the CODA6X's newer `/1/Device/...`
PHP+REST layer in `coda60.py`) - see ../../docs/KB.md §3 for the identification.

The endpoint and field names are identical to coda60.py's (signalStrength,
channelId, plcpower, Subcarr0freqFreq, repPower1_6, uschindex, ...): both
devices sit on the same underlying DOCSIS/RDK-B data model, just exposed
through different URL conventions and (per `test.py`) a different,
PHPSESSID-cookie-based auth flow (primed via `GET /login.html`) rather than
CODA6X's JSON login or the HT-EM5's HTTP Basic.

Not independently verified live in this session (the host was unreachable
from the development machine at the time) - this is a direct, careful port of
the already-working script, not a re-derivation. Re-confirm response shapes
against a real unit before relying on anything not already covered by
`main.py`'s own rendering code (dsinfo/usinfo/dsofdminfo/usofdminfo are
rendered and therefore field-confirmed; getCMInit/getLinkStatus/getCmDocsisWan
are fetched but not decoded anywhere, so their shape is unconfirmed).
"""

from __future__ import annotations

import asyncio
from dataclasses import dataclass
from typing import Any

import aiohttp

# Fetched by main.py's "all" command. Only the first four are also decoded/
# rendered there (see the field-confirmed note above).
ENDPOINTS: tuple[str, ...] = (
    "dsinfo",
    "dsofdminfo",
    "usinfo",
    "usofdminfo",
    "getCMInit",
    "getLinkStatus",
    "getCmDocsisWan",
    "getSysInfo",
)

# Midco's stated in-spec power ranges, lifted verbatim from main.py's
# check_specs(). Downstream SC-QAM uses signalStrength directly; downstream
# OFDM uses plcpower. Upstream SC-QAM/ATDMA uses signalStrength; upstream
# OFDMA uses repPower1_6 + 6.0 dB (a DOCSIS-3.0-equivalent adjustment).
MIDCO_DS_RANGE_DBMV = (-6.0, 15.0)
MIDCO_US_RANGE_DBMV = (35.0, 48.0)


@dataclass
class Coda57Client:
    host: str = "192.168.100.1"
    timeout: float = 10.0
    max_concurrency: int = 2

    @property
    def base_url(self) -> str:
        return f"http://{self.host}/data"

    async def prime_session(self, session: aiohttp.ClientSession) -> None:
        """GET /login.html once, per test.py, to pick up whatever session cookie the device sets. Unconfirmed whether the .asp endpoints actually require it."""
        try:
            await session.get(f"http://{self.host}/login.html", timeout=aiohttp.ClientTimeout(total=self.timeout))
        except Exception:  # noqa: BLE001 - priming is best-effort; endpoints may just work without it
            pass

    async def _fetch_one(self, session: aiohttp.ClientSession, endpoint: str, sem: asyncio.Semaphore) -> tuple[str, list[dict] | dict]:
        url = f"{self.base_url}/{endpoint}.asp"
        async with sem:
            try:
                async with session.get(url, timeout=aiohttp.ClientTimeout(total=self.timeout)) as response:
                    if response.status != 200:
                        return endpoint, []
                    data = await response.json(content_type=None)
                    return endpoint, data if isinstance(data, list) else [data]
            except Exception:  # noqa: BLE001 - main.py's own behavior: a bad endpoint returns [] rather than aborting the batch
                return endpoint, []

    async def get_all(self, prime: bool = True) -> dict[str, list[dict] | dict]:
        sem = asyncio.Semaphore(self.max_concurrency)
        async with aiohttp.ClientSession() as session:
            if prime:
                await self.prime_session(session)
            results = await asyncio.gather(*[self._fetch_one(session, ep, sem) for ep in ENDPOINTS])
        return dict(results)

    @staticmethod
    def check_midco_spec(data: dict[str, Any]) -> dict[str, Any]:
        """Port of main.py's check_specs(): are this modem's live DS/US power levels inside Midco's stated spec?"""

        def f(x: Any, default: float = 0.0) -> float:
            try:
                return float(x)
            except (TypeError, ValueError):
                return default

        ds_powers = [f(ch.get("signalStrength")) for ch in data.get("dsinfo", [])]
        ds_powers += [f(ch.get("plcpower")) for ch in data.get("dsofdminfo", []) or []]
        us_powers = [f(ch.get("signalStrength")) for ch in data.get("usinfo", [])]
        us_powers += [f(ch.get("repPower1_6")) + 6.0 for ch in data.get("usofdminfo", []) or []]

        ds_min, ds_max = (min(ds_powers), max(ds_powers)) if ds_powers else (0.0, 0.0)
        us_min, us_max = (min(us_powers), max(us_powers)) if us_powers else (0.0, 0.0)
        ds_ok = MIDCO_DS_RANGE_DBMV[0] <= ds_min and ds_max <= MIDCO_DS_RANGE_DBMV[1]
        us_ok = MIDCO_US_RANGE_DBMV[0] <= us_min and us_max <= MIDCO_US_RANGE_DBMV[1]
        return {
            "ds_min_dbmv": ds_min, "ds_max_dbmv": ds_max, "ds_in_spec": ds_ok,
            "us_min_dbmv": us_min, "us_max_dbmv": us_max, "us_in_spec": us_ok,
            "in_spec": ds_ok and us_ok,
        }
