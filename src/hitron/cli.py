"""Unified CLI: `hitron <device> <host> [options]`. Read subcommands print JSON to stdout.

Write/action subcommands (coda60-reboot, coda60-factory-reset, coda60-hard-reset,
coda60-set-password, coda60-set-username) are separate, explicitly-named subcommands -
never a flag on the read command - so a write is never one typo away from a read.
Each requires --username/--password to log in first, and a destructive one
(everything except set-password/set-username, which still need the old password)
additionally requires --yes-really-do-this.
"""

from __future__ import annotations

import argparse
import asyncio
import json
import sys

from .coda57 import Coda57Client
from .coda60 import Coda60Client
from .ht_em5 import HitronMocaClient


def _run_site(args: argparse.Namespace) -> None:
    from .site import main as site_main

    argv = ["--port", str(args.port), "--listen", args.listen]
    if args.device:
        argv += ["--device", args.device]
    if args.dir:
        argv += ["--dir", args.dir]
    if args.read_only:
        argv += ["--read-only"]
    if args.no_open:
        argv += ["--no-open"]
    site_main(argv)


def _run_grafana(args: argparse.Namespace) -> None:
    from .grafana import write_bundle

    written = write_bundle(args.dir)
    for name, path in written.items():
        print(f"{name}: {path}")


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


async def _coda60_action(args: argparse.Namespace) -> dict:
    async with Coda60Client(args.host) as client:
        if not await client.login(args.username, args.password):
            return {"error": "login failed"}
        if args.action == "reboot":
            return await client.reboot()
        if args.action == "factory-reset":
            return await client.factory_reset()
        if args.action == "hard-reset":
            return await client.hard_reset_and_reboot()
        if args.action == "set-password":
            return await client.set_admin_password(args.old_password, args.new_password)
        if args.action == "set-username":
            return await client.set_admin_username(args.new_username)
        raise AssertionError(args.action)  # argparse choices should prevent this


def _add_destructive_action(sub, name: str, action: str, extra_help: str) -> None:
    p = sub.add_parser(name, help=f"CODA6X: {extra_help} - a real, immediate device action")
    p.add_argument("host")
    p.add_argument("--username", required=True)
    p.add_argument("--password", required=True)
    p.add_argument("--yes-really-do-this", action="store_true", dest="confirmed", help="required - this is not a drill")
    p.set_defaults(run=_coda60_action, action=action)


def main() -> None:
    ap = argparse.ArgumentParser(prog="hitron", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="device", required=True)

    p = sub.add_parser("ht-em5", help="HT-EM5 MoCA bridge (register API) - read")
    p.add_argument("host")
    p.add_argument("--username", default="admin")
    p.add_argument("--password", default="admin")
    p.add_argument("--phy-rates", action="store_true", help="also include the PHY rate matrix")
    p.set_defaults(run=_ht_em5)

    p = sub.add_parser("coda60", help="CODA6X gateway (REST API) - read")
    p.add_argument("host", default="192.168.100.1", nargs="?")
    p.set_defaults(run=_coda60)

    p = sub.add_parser("coda57", help="CODA-57 gateway (legacy .asp API) - read")
    p.add_argument("host", default="192.168.100.1", nargs="?")
    p.add_argument("--spec-check", action="store_true", help="include the Midco in-spec power-range check")
    p.set_defaults(run=_coda57)

    _add_destructive_action(sub, "coda60-reboot", "reboot", "flag-based reboot request")
    _add_destructive_action(sub, "coda60-factory-reset", "factory-reset", "flag-based factory reset - destroys all configuration")
    _add_destructive_action(sub, "coda60-hard-reset", "hard-reset", "IMMEDIATE reboot, no TR-181 indirection - the point of no return")

    p = sub.add_parser("coda60-set-password", help="CODA6X: change the admin password")
    p.add_argument("host")
    p.add_argument("--username", required=True, help="current login username")
    p.add_argument("--password", required=True, help="current login password")
    p.add_argument("--old-password", required=True)
    p.add_argument("--new-password", required=True)
    p.add_argument("--yes-really-do-this", action="store_true", dest="confirmed")
    p.set_defaults(run=_coda60_action, action="set-password")

    p = sub.add_parser("coda60-set-username", help="CODA6X: change the admin username")
    p.add_argument("host")
    p.add_argument("--username", required=True, help="current login username")
    p.add_argument("--password", required=True, help="current login password")
    p.add_argument("--new-username", required=True)
    p.add_argument("--yes-really-do-this", action="store_true", dest="confirmed")
    p.set_defaults(run=_coda60_action, action="set-username")

    p = sub.add_parser("site", help="serve the static docs site and proxy a gateway")
    p.add_argument("--port", type=int, default=8000)
    p.add_argument("--listen", default="127.0.0.1")
    p.add_argument("--device", default=None, help="gateway used by the /proxy endpoint")
    p.add_argument("--dir", default=None, help="site directory (defaults to ./site)")
    p.add_argument(
        "--read-only",
        action="store_true",
        help="proxy GET/HEAD only; writes (reboot, reset) return 403",
    )
    p.add_argument(
        "--no-open",
        action="store_true",
        help="do not open the default browser (it opens automatically otherwise)",
    )
    p.set_defaults(sync=_run_site)

    p = sub.add_parser("grafana", help="write the Grafana dashboards and provisioning bundle")
    p.add_argument("--dir", default="deploy/grafana", help="output directory")
    p.set_defaults(sync=_run_grafana)

    args = ap.parse_args()
    if getattr(args, "sync", None) is not None:
        args.sync(args)
        return
    if getattr(args, "run", None) is _coda60_action and not getattr(args, "confirmed", False):
        ap.error(f"this is a real action against {args.host} - pass --yes-really-do-this to proceed")

    try:
        result = asyncio.run(args.run(args))
    except KeyboardInterrupt:
        sys.exit(130)
    print(json.dumps(result, indent=2, default=str))


if __name__ == "__main__":
    main()
