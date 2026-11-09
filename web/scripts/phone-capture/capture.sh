#!/bin/bash
# Regenerate the phone app's screenshots (web/src/images/phone-*.png).
#
# It reuses the TUI capture's staged demo — a scratch HOME with its own
# conch server, a fake `claude` and a fake `gh` — then runs `conch web` in
# front of it on loopback and photographs the app in headless Chrome at a
# phone's size. It never touches your own conch server, home directory or
# agents: HOME, CONCH_HOME and the socket are all scratch paths, and the
# gateway is bound to 127.0.0.1.
#
#   web/scripts/phone-capture/capture.sh [outdir]
#
# Pairing works over plain http here only because the browser is on the
# same computer: loopback is a secure context, which is what the gateway
# checks. A phone still needs HTTPS or a relay.
set -euo pipefail
T=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$T/../../.." && pwd)
TC=$ROOT/web/scripts/tui-capture
OUT=${1:-$ROOT/web/src/images}
PORT=${PORT:-8799}
WORK=${TMPDIR:-/tmp}/conch-phone-capture
rm -rf "$WORK"
mkdir -p "$WORK"
cp "$TC/stage.sh" "$TC/run.sh" "$WORK/"

# Stamped with the newest release, as the archives are, so the app's
# Settings says what visitors would install rather than a dev version.
VERSION=$(cd "$ROOT" && git describe --tags --abbrev=0 --match 'v*' 2>/dev/null | sed 's/^v//')
(cd "$ROOT" && go build -ldflags "-X github.com/Amitgb14/conch/internal/proto.Version=${VERSION:-0.0.0-dev}" -o "$WORK/conch" ./cmd/conch)
(cd "$TC/_fakeclaude" && GO111MODULE=off go build -o "$WORK/claude-bin" main.go)

unset CONCH_SOCKET CONCH_PANE_ID CONCH_HOME
D=$WORK/demo
IE=(env -i HOME=$D CONCH_HOME=$D/.config/conch CONCH_SOCKET=/tmp/cwd-inner.sock CONCH_GH=$D/.local/bin/gh
  PATH=$D/.local/bin:/usr/bin:/bin:/usr/sbin:/sbin TERM=xterm-256color LANG=en_US.UTF-8 SHELL=/bin/bash USER=ada)

CAPTURE_NO_TUI=1 bash "$WORK/run.sh" >/dev/null
sleep 4 # let git, the pull requests and the agents' states settle

cleanup() {
  "${IE[@]}" "$WORK/conch" web stop >/dev/null 2>&1 || true
  "${IE[@]}" "$WORK/conch" server stop >/dev/null 2>&1 || true
}
trap cleanup EXIT

"${IE[@]}" "$WORK/conch" web -listen 127.0.0.1 -port "$PORT" > "$WORK/web.log" 2>&1 &
for _ in $(seq 40); do
  curl -fsS -o /dev/null "http://127.0.0.1:$PORT/" && break
  sleep 0.25
done

CODE=$("${IE[@]}" "$WORK/conch" web pair -json | python3 -c 'import json,sys; print(json.load(sys.stdin)["code"])')
mkdir -p "$OUT"
python3 "$T/shots.py" "http://127.0.0.1:$PORT" "$CODE" "$OUT"
echo "wrote the phone screenshots to $OUT"
