#!/usr/bin/env bash
# hitron installer: creates an isolated virtualenv, installs the hitron package
# from GitHub, and links the `hitron` command into ~/.local/bin.
#
#   curl -fsSL https://hitron.netquor.dev/install.sh | bash
#
#   curl -fsSL https://hitron.netquor.dev/install.sh | bash -s -- \
#     --run --device 192.168.100.1
#
# Nothing is installed outside $HITRON_PREFIX (default
# ~/.local/share/hitron) and $HITRON_BINDIR (default ~/.local/bin).
# No sudo, no system Python packages, no shell rc edits.
set -euo pipefail

REPO="${HITRON_REPO:-Netquor/hitron}"
REF="${HITRON_REF:-main}"
PREFIX="${HITRON_PREFIX:-${XDG_DATA_HOME:-$HOME/.local/share}/hitron}"
BINDIR="${HITRON_BINDIR:-$HOME/.local/bin}"
PYTHON="${PYTHON:-python3}"
DEVICE="${HITRON_DEVICE:-192.168.100.1}"
PORT="${HITRON_PORT:-8000}"
LISTEN="${HITRON_LISTEN:-127.0.0.1}"
RUN=0
READ_ONLY=0
UNINSTALL=0

usage() {
  cat <<EOF
hitron installer

Usage:
  install.sh [options]

Options:
  --device HOST     gateway address used by --run (default: $DEVICE)
  --port PORT       port for --run (default: $PORT)
  --listen ADDR     bind address for --run (default: $LISTEN)
  --run             start the local docs site + gateway proxy after install
  --read-only       with --run, block write verbs at the proxy
  --ref REF         branch or tag to install (default: $REF)
  --prefix DIR      install venv here (default: $PREFIX)
  --bin-dir DIR     symlink the command here (default: $BINDIR)
  --uninstall       remove the install above and the symlink
  -h, --help        show this help

Environment: HITRON_REPO, HITRON_REF, HITRON_PREFIX, HITRON_BINDIR,
PYTHON, HITRON_DEVICE, HITRON_PORT, HITRON_LISTEN.

Examples:
  curl -fsSL https://hitron.netquor.dev/install.sh | bash
  curl -fsSL https://hitron.netquor.dev/install.sh | bash -s -- --run
  curl -fsSL https://hitron.netquor.dev/install.sh | bash -s -- \\
    --run --device 192.168.100.1 --read-only
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --device) DEVICE="${2:?--device needs a value}"; shift 2 ;;
    --port) PORT="${2:?--port needs a value}"; shift 2 ;;
    --listen) LISTEN="${2:?--listen needs a value}"; shift 2 ;;
    --run) RUN=1; shift ;;
    --read-only) READ_ONLY=1; shift ;;
    --ref) REF="${2:?--ref needs a value}"; shift 2 ;;
    --prefix) PREFIX="${2:?--prefix needs a value}"; shift 2 ;;
    --bin-dir) BINDIR="${2:?--bin-dir needs a value}"; shift 2 ;;
    --uninstall) UNINSTALL=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "install.sh: unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ "$UNINSTALL" = 1 ]; then
  rm -f "$BINDIR/hitron"
  rm -rf "$PREFIX"
  echo "removed $PREFIX and $BINDIR/hitron"
  exit 0
fi

if ! command -v "$PYTHON" >/dev/null 2>&1; then
  echo "install.sh: $PYTHON not found. Install Python 3.11+ first:" >&2
  echo "  macOS:  brew install python@3.12" >&2
  echo "  Debian: sudo apt install python3 python3-venv" >&2
  exit 1
fi

if ! "$PYTHON" - <<'PY'
import sys
if sys.version_info < (3, 11):
    raise SystemExit(
        "install.sh: Python 3.11+ required, found %s" % ".".join(map(str, sys.version_info[:3]))
    )
PY
then
  exit 1
fi

echo "hitron: installing into $PREFIX"
mkdir -p "$PREFIX" "$BINDIR"

if [ ! -x "$PREFIX/venv/bin/python" ]; then
  echo "hitron: creating virtualenv"
  "$PYTHON" -m venv "$PREFIX/venv"
fi

# Install straight from the GitHub archive: no git, no PyPI release needed.
# --upgrade makes re-running this script a working "update".
URL="https://github.com/$REPO/archive/$REF.tar.gz"
echo "hitron: downloading $URL"
"$PREFIX/venv/bin/python" -m pip install --quiet --disable-pip-version-check --upgrade "$URL"

ln -sf "$PREFIX/venv/bin/hitron" "$BINDIR/hitron"
ln -sf "$PREFIX/venv/bin/hitron-exporter" "$BINDIR/hitron-exporter" 2>/dev/null || true

case ":$PATH:" in
  *":$BINDIR:"*) ;;
  *)
    echo
    echo "Add this to your shell profile so 'hitron' is on PATH:"
    echo "  export PATH=\"$BINDIR:\$PATH\""
    ;;
esac

echo
echo "Installed. Verify with:"
echo "  $BINDIR/hitron --help"
echo

if [ "$RUN" = 1 ]; then
  ARGS=(site --listen "$LISTEN" --port "$PORT" --device "$DEVICE")
  if [ "$READ_ONLY" = 1 ]; then
    ARGS+=(--read-only)
  fi
  echo "Starting the local site + proxy on http://$LISTEN:$PORT (gateway $DEVICE)"
  echo "Press Ctrl-C to stop."
  exec "$PREFIX/venv/bin/hitron" "${ARGS[@]}"
else
  echo "Start the local site + proxy with:"
  echo "  $BINDIR/hitron site --device $DEVICE --port $PORT"
  echo
  echo "Then open http://127.0.0.1:$PORT and keep the API base URL at /proxy."
fi
