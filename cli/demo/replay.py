#!/usr/bin/env python3
"""Replay a recorded CODA6X so the TUI can be run, screenshotted and filmed
without a modem. Read-only: GETs are answered from modem-sample.json, and a
reboot POST is acknowledged and ignored.

The sample is a real capture with the serial number, MAC addresses, CMTS MAC
and IP addresses replaced. The live signal endpoints step through 80
one-second polls so the numbers move the way a real modem's do; the analyser
(spectrum, constellation) answers with real captures.

    python3 replay.py --port 8080
"""
import argparse
import json
import re
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

LOCK = threading.Lock()


class State:
    def __init__(self, path):
        with open(path) as handle:
            data = json.load(handle)
        self.static = data["static"]
        self.series = data["series"]
        self.spectrum = data["spectrum"]
        self.index = 0
        self.polls = 0

    def advance(self):
        with LOCK:
            self.index = self.polls % len(self.series)
            self.polls += 1

    def uptime(self):
        base = self.static["Router/SysInfo"]["systemUptime"]
        hours, minutes, seconds = (int(x) for x in re.findall(r"\d+", base))
        total = hours * 3600 + minutes * 60 + seconds + self.polls
        return "%02dh:%02dm:%02ds" % (total // 3600, total % 3600 // 60, total % 60)


def nearest_sweep(spectrum, center):
    best, gap = None, 1e9
    for key, value in spectrum["FreqPower"].items():
        if value.get("spectrum") and abs(float(key) - center) < gap:
            best, gap = value, abs(float(key) - center)
    return best if best is not None and gap <= 1.0 else {"errCode": "000", "errMsg": "", "spectrum": []}


class Handler(BaseHTTPRequestHandler):
    state = None

    def log_message(self, *args):
        pass

    def reply(self, payload, status=200):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        url = urlparse(self.path)
        if not url.path.startswith("/1/Device/"):
            return self.reply({"errCode": "404"}, 404)
        name = url.path[len("/1/Device/"):]
        query = parse_qs(url.query)
        state = self.state
        if name == "CM/DsInfo":
            state.advance()
        if name in ("CM/DsInfo", "CM/UsInfo", "CM/DsOfdm", "CM/UsOfdm"):
            return self.reply(state.series[state.index][name])
        if name == "Router/SysInfo":
            payload = dict(state.static[name])
            payload["systemUptime"] = state.uptime()
            return self.reply(payload)
        if name == "Spectrum/FreqPower":
            center = float(query.get("centFreq", ["645"])[0])
            return self.reply(nearest_sweep(state.spectrum, center))
        if name == "Spectrum/RTData":
            return self.reply(state.spectrum["RTData"])
        if name == "Spectrum/Constellation":
            port = query.get("portId", ["24"])[0]
            return self.reply(state.spectrum["Constellation"].get(
                port, {"errCode": "000", "errMsg": "", "constellation": []}))
        if name in state.static:
            return self.reply(state.static[name])
        return self.reply({"errCode": "000", "errMsg": ""})

    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length") or 0))
        self.reply({"errCode": "000", "errMsg": ""})


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("--sample", default="modem-sample.json")
    parser.add_argument("--bind", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8080)
    args = parser.parse_args()
    Handler.state = State(args.sample)
    ThreadingHTTPServer((args.bind, args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
