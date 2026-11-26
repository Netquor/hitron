# Run the docs site and gateway proxy locally

The published site at <https://hitron.netquor.dev> is static. It can show the
API reference, dashboards, and `llms.txt`, but it **cannot call your modem**.
A browser will not let a page served over `https://` fetch `http://192.168.100.1`
(mixed content), and the CODA6X firmware sends no CORS headers either. The API
console therefore calls `/proxy/...` on its own origin, and that path only
exists when someone runs the small proxy in this repo.

That is the whole reason for this guide. It takes about a minute.

## The fast path (one line)

```bash
curl -fsSL https://hitron.netquor.dev/install.sh | bash -s -- --run --device 192.168.100.1
```

This installs into `~/.local/share/hitron` (an isolated virtualenv, no sudo,
no system packages), links `~/.local/bin/hitron`, starts the site on
<http://127.0.0.1:8000>, and points the proxy at your gateway. Open the URL and
leave the API console's base URL at `/proxy`.

Stop it with Ctrl-C. Start it again later with:

```bash
~/.local/bin/hitron site --device 192.168.100.1
```

Prefer to browse without any chance of a write reaching the modem? Add
`--read-only`:

```bash
curl -fsSL https://hitron.netquor.dev/install.sh | bash -s -- \
  --run --device 192.168.100.1 --read-only
```

## Is it pip3?

Not from PyPI — there is no published `hitron` package there, and installing
this repo's Python package into the system interpreter is the least reliable
path (macOS Homebrew Python refuses with `externally-managed-environment`, and
the repo needs Python 3.11+). Use one of these instead:

| You have | Command |
|---|---|
| Nothing yet | the `curl … \| bash` line above |
| `pipx` | `pipx install "git+https://github.com/Netquor/hitron"` then `hitron site --device 192.168.100.1` |
| `uv` | `uvx --from "git+https://github.com/Netquor/hitron" hitron site --device 192.168.100.1` |
| A checkout | `python3 -m venv .venv && . .venv/bin/activate && pip install -e .` |

`pip install hitron` alone will not do what you want: that name is unrelated on
PyPI. The installer never uses `pip3 install` globally.

## Manual install, step by step

1. **Check Python.** 3.11 or newer:

   ```bash
   python3 --version
   ```

   macOS: `brew install python@3.12`. Debian/Ubuntu: `sudo apt install python3 python3-venv`.

2. **Get the code.**

   ```bash
   git clone https://github.com/Netquor/hitron.git
   cd hitron
   ```

3. **Make a virtualenv and install.**

   ```bash
   python3 -m venv .venv
   . .venv/bin/activate
   pip install -e .
   ```

4. **Confirm the device answers.** This is a plain HTTP read and needs no
   login on the firmware analyzed here:

   ```bash
   hitron coda60 192.168.100.1 | head -20
   curl -s http://192.168.100.1/1/Device/CM/Version
   ```

   If that fails, the proxy cannot work either — fix reachability first (see
   Troubleshooting).

5. **Start the site and proxy.**

   ```bash
   hitron site --device 192.168.100.1 --port 8000
   ```

   ```
   hitron site: http://127.0.0.1:8000  (site=…/site, device=192.168.100.1, read/write)
   ```

6. **Open <http://127.0.0.1:8000>.** The API console's base URL is `/proxy`.
   Pick `GET /CM/Version`, press **Send request**, and expect `200` plus the
   firmware/serial JSON.

7. **Prove the proxy from the shell.**

   ```bash
   curl -s http://127.0.0.1:8000/proxy/1/Device/CM/Version
   curl -s http://127.0.0.1:8000/proxy/1/Device/CM/DsInfo | head -c 300
   ```

## What the proxy does

`hitron site` serves `site/` from the repository and forwards `/proxy/<path>`
to `http://<device>/<path>`, preserving method, query string, request body, and
response status. The page and the API share one origin, so the browser is happy
and cookies work.

Useful flags:

| Flag | Meaning |
|---|---|
| `--device HOST[:PORT]` | gateway the proxy targets (required for API calls) |
| `--port N` | listen port, default `8000` |
| `--listen ADDR` | default `127.0.0.1`; use `0.0.0.0` only on a trusted LAN |
| `--read-only` | `GET`/`HEAD` only; `POST`/`PUT`/`DELETE` return 403 |
| `--dir PATH` | serve a different copy of the site |

The proxy forwards writes, including `POST /1/Device/CM/Reboot`. Use
`--read-only` whenever you are exploring rather than administering.

## Using a mock instead of hardware

The repo ships an offline CODA6X UI in `mock/` (PHP and the firmware tree). It
answers the same routes, so nothing above changes except the target:

```bash
./mock/run.sh            # terminal 1, serves 127.0.0.1:8080
hitron site --device 127.0.0.1:8080   # terminal 2
```

## Troubleshooting

### "fetch failed" on https://hitron.netquor.dev

Expected: the static site has no `/proxy`, so the request is a 404 from GitHub
Pages. Start the local proxy and use <http://127.0.0.1:8000> instead. The
console now says so explicitly when it detects a static host.

### "fetch failed" locally

Check the proxy's terminal. Then:

```bash
curl -i http://127.0.0.1:8000/proxy/1/Device/CM/Version
```

- `502 … proxy error: <urlopen error timed out>` — the device is unreachable
  from this machine. See the next item.
- `502 … Connection refused` — wrong `--device` port, or the modem's UI is off.
- `404` — you are on the published site, not the local one, or `--dir` points
  somewhere without `site/index.html`.

### The modem is not reachable

`192.168.100.1` only answers from the modem's own LAN. Check:

```bash
ipconfig getifaddr en0          # macOS: is this 192.168.100.x?
ping -c1 192.168.100.1
curl -m 4 http://192.168.100.1/1/Device/CM/Version
```

If your Mac is on a different subnet, join the CODA's Wi-Fi or plug into a LAN
port. There is no route through the internet.

### Port 8000 is already in use

```bash
hitron site --device 192.168.100.1 --port 8080
```

Then open <http://127.0.0.1:8080>. Do not pick 8080 if the mock is already
using it.

### Login is required

Reads are unauthenticated on the firmware analyzed here. If yours is locked
down, the console's **Login via proxy** button posts to
`/proxy/1/Device/Users/Login` and keeps the session cookie. The CLI's read
command is anonymous; write commands require credentials:

```bash
hitron coda60-reboot 192.168.100.1 --username admin --password '…'
```

### Updating and removing

```bash
curl -fsSL https://hitron.netquor.dev/install.sh | bash        # update
curl -fsSL https://hitron.netquor.dev/install.sh | bash -s -- --uninstall
```

The installer removes only its own virtualenv and symlinks.
