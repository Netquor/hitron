"""Serve the static documentation site and reverse-proxy a gateway.

The site under ``site/`` is plain HTML/CSS/JS so GitHub Pages can host it with
no build step. Its API console needs to call the gateway, and a browser will
not let ``https://netquor.github.io`` call ``http://192.168.100.1`` (mixed
content and no CORS headers). Running this command locally solves both:
static files are served from the same origin as ``/proxy``, which is forwarded
to the device.

    hitron site --device 192.168.100.1 --port 8000

Then open http://127.0.0.1:8000 and leave the API base URL at ``/proxy``.
"""

from __future__ import annotations

import argparse
import json
import os
import posixpath
import urllib.error
import urllib.request
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any
from urllib.parse import unquote, urlparse

__all__ = ["main", "find_site_dir"]

PROXY_PREFIX = "/proxy"
HOP_HEADERS = {
    "connection",
    "keep-alive",
    "proxy-authenticate",
    "proxy-authorization",
    "te",
    "trailers",
    "transfer-encoding",
    "upgrade",
    "host",
    "content-length",
}


def find_site_dir(explicit: str | None = None) -> Path:
    candidates: list[Path] = []
    if explicit:
        candidates.append(Path(explicit))
    if os.environ.get("HITRON_SITE_DIR"):
        candidates.append(Path(os.environ["HITRON_SITE_DIR"]))
    candidates.append(Path.cwd() / "site")
    # Editable install: <repo>/src/hitron/site.py -> <repo>/site
    candidates.append(Path(__file__).resolve().parents[2] / "site")
    for candidate in candidates:
        if (candidate / "index.html").is_file():
            return candidate.resolve()
    raise SystemExit(
        "could not find the site directory; pass --dir, set HITRON_SITE_DIR, "
        "or run from the repository root"
    )


def _handler(site_dir: Path, device: str | None):
    class Handler(SimpleHTTPRequestHandler):
        server_version = "hitron-site/0.2"

        def __init__(self, *args: Any, **kwargs: Any):
            super().__init__(*args, directory=str(site_dir), **kwargs)

        def log_message(self, fmt: str, *args: Any) -> None:  # noqa: A003
            if os.environ.get("HITRON_SITE_VERBOSE"):
                super().log_message(fmt, *args)

        # -- proxy ---------------------------------------------------------
        def _proxy(self) -> bool:
            parsed = urlparse(self.path)
            if not parsed.path.startswith(PROXY_PREFIX):
                return False
            if not device:
                self._json_error(502, "no --device configured for the proxy")
                return True

            upstream_path = parsed.path[len(PROXY_PREFIX):] or "/"
            if not upstream_path.startswith("/"):
                upstream_path = "/" + upstream_path
            upstream_path = posixpath.normpath(upstream_path)
            if parsed.query:
                upstream_path += "?" + parsed.query
            url = f"http://{device}{upstream_path}"

            length = int(self.headers.get("Content-Length") or 0)
            body = self.rfile.read(length) if length else None
            headers = {
                key: value
                for key, value in self.headers.items()
                if key.lower() not in HOP_HEADERS
            }
            headers.setdefault("Accept", "application/json")
            request = urllib.request.Request(url, data=body, headers=headers, method=self.command)

            try:
                with urllib.request.urlopen(request, timeout=20) as response:
                    payload = response.read()
                    status = response.status
                    content_type = response.headers.get("Content-Type", "application/json")
            except urllib.error.HTTPError as exc:
                payload = exc.read()
                status = exc.code
                content_type = exc.headers.get("Content-Type", "application/json") if exc.headers else "application/json"
            except Exception as exc:  # noqa: BLE001 - surface the failure in the console
                self._json_error(502, f"proxy error: {exc}")
                return True

            self.send_response(status)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(payload)))
            self.send_header("Cache-Control", "no-store")
            self.send_header("Access-Control-Allow-Origin", "*")
            self.end_headers()
            self.wfile.write(payload)
            return True

        def _json_error(self, status: int, message: str) -> None:
            payload = json.dumps({"error": message}).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.send_header("Access-Control-Allow-Origin", "*")
            self.end_headers()
            self.wfile.write(payload)

        # -- verbs ---------------------------------------------------------
        def do_GET(self) -> None:  # noqa: N802
            if self._proxy():
                return
            if self.path.startswith("/healthz"):
                self._json_error(200, "ok")
                return
            super().do_GET()

        def do_HEAD(self) -> None:  # noqa: N802
            if self._proxy():
                return
            super().do_HEAD()

        def do_POST(self) -> None:  # noqa: N802
            if not self._proxy():
                self._json_error(405, "static files are read-only")

        def do_PUT(self) -> None:  # noqa: N802
            if not self._proxy():
                self._json_error(405, "static files are read-only")

        def do_DELETE(self) -> None:  # noqa: N802
            if not self._proxy():
                self._json_error(405, "static files are read-only")

        def do_OPTIONS(self) -> None:  # noqa: N802
            self.send_response(204)
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
            self.send_header("Access-Control-Allow-Headers", "*")
            self.end_headers()

        def end_headers(self) -> None:
            if not self.path.startswith(PROXY_PREFIX):
                self.send_header("Access-Control-Allow-Origin", "*")
            super().end_headers()

        def translate_path(self, path: str) -> str:
            # Keep query strings and fragments out of the filesystem path.
            path = unquote(path.split("?", 1)[0].split("#", 1)[0])
            return super().translate_path(path)

    return Handler


def main(argv: list[str] | None = None) -> None:
    ap = argparse.ArgumentParser(description="Serve the hitron static site plus a gateway proxy")
    ap.add_argument("--port", type=int, default=int(os.environ.get("HITRON_SITE_PORT", "8000")))
    ap.add_argument("--listen", default=os.environ.get("HITRON_SITE_LISTEN", "127.0.0.1"))
    ap.add_argument("--device", default=os.environ.get("HITRON_SITE_DEVICE") or None, help="gateway address used by /proxy")
    ap.add_argument("--dir", default=None, help="site directory (defaults to ./site)")
    args = ap.parse_args(argv)

    site_dir = find_site_dir(args.dir)
    handler = _handler(site_dir, args.device)
    server = ThreadingHTTPServer((args.listen, args.port), handler)
    print(f"hitron site: http://{args.listen}:{args.port}  (site={site_dir}, device={args.device or 'none'})")
    print("API console base URL: /proxy" if args.device else "start with --device to enable the API console proxy")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
