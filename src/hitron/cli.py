"""Unified CLI: `hitron <device> <host> [options]`. Each subcommand prints JSON to stdout."""

from __future__ import annotations

import argparse
import asyncio
import json
import sys

from .coda57 import Coda57Client
from .coda60 import Coda60Client
from .ht_em5 import HitronMocaClient


async def _ht_em5(args: argparse.Namespace) -> dict:
    async with HitronMocaClient(args.host, username=args.username, password=args.password) as client:
        data = await client.get_all()
        if args.phy_rates:
            data["phy_rates"] = await client.get_phy_rates()
        return data


async def _coda60(args: argparse.Namespace) -> dict:
    return await Coda60Client(args.host).get_all()


async def _coda57(args: argparse.Namespace) -> dict:
    data = await Coda57Client(args.host).get_all()
    if args.spec_check:
        data["_midco_spec_check"] = Coda57Client.check_midco_spec(data)
    return data


def main() -> None:
    ap = argparse.ArgumentParser(prog="hitron", description=__doc__)
    sub = ap.add_subparsers(dest="device", required=True)

    p = sub.add_parser("ht-em5", help="HT-EM5 MoCA bridge (register API)")
    p.add_argument("host")
    p.add_argument("--username", default="admin")
    p.add_argument("--password", default="admin")
    p.add_argument("--phy-rates", action="store_true", help="also include the PHY rate matrix")
    p.set_defaults(run=_ht_em5)

    p = sub.add_parser("coda60", help="CODA6X gateway (REST API)")
    p.add_argument("host", default="192.168.100.1", nargs="?")
    p.set_defaults(run=_coda60)

    p = sub.add_parser("coda57", help="CODA-57 gateway (legacy .asp API)")
    p.add_argument("host", default="192.168.100.1", nargs="?")
    p.add_argument("--spec-check", action="store_true", help="include the Midco in-spec power-range check")
    p.set_defaults(run=_coda57)

    args = ap.parse_args()
    try:
        result = asyncio.run(args.run(args))
    except KeyboardInterrupt:
        sys.exit(130)
    print(json.dumps(result, indent=2, default=str))


if __name__ == "__main__":
    main()
