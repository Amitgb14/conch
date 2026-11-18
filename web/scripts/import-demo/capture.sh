#!/bin/bash
# Record the demo of moving from Conductor to conch: the Conductor import
# found, installed and run, a few conversations chosen, the handoff they
# became, and an agent started on one.
#
# It builds conch and a stand-in `claude` (internal/tools/standin: not an
# agent, nothing is sent to any model), stages a scratch HOME with a
# made-up Conductor database and a made-up shop's repository, and records
# the commands against it. It never reads your own Conductor database —
# the demo is published — and never touches your conch server, home
# directory or agents: HOME, CONCH_HOME and the socket are scratch paths.
# `conch integration install` reads the public registry; nothing is sent.
#
#   web/scripts/import-demo/capture.sh
#
# Writes web/public/demos/import-conductor.cast (asciinema play it), each
# step's final screen as text beside it, and as web/src/images/import-*.png.
set -euo pipefail
T=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$T/../../.." && pwd)
WORK=${TMPDIR:-/tmp}/conch-import-demo
CAST=$ROOT/web/public/demos/import-conductor.cast
TEXT=$ROOT/web/public/demos/import-conductor
PNG=$ROOT/web/src/images
rm -rf "$WORK"
mkdir -p "$WORK/bin" "$(dirname "$CAST")"

# Stamped with the newest release, as the archives are, so what it prints
# is what visitors would install rather than a development version.
VERSION=$(cd "$ROOT" && git describe --tags --abbrev=0 --match 'v*' 2>/dev/null | sed 's/^v//')
(cd "$ROOT" && go build -ldflags "-X github.com/Amitgb14/conch/internal/proto.Version=${VERSION:-0.0.0-dev}" -o "$WORK/bin/conch" ./cmd/conch)
(cd "$ROOT" && go build -o "$WORK/bin/claude" ./internal/tools/standin)

unset CONCH_SOCKET CONCH_PANE_ID CONCH_HOME
H=$WORK/home
IE=(env -i HOME=$H CONCH_HOME=$H/.config/conch CONCH_SOCKET=/tmp/conch-import-demo.sock
  PATH=$WORK/bin:/usr/bin:/bin:/usr/sbin:/sbin TERM=xterm-256color LANG=en_US.UTF-8 SHELL=/bin/bash USER=ada)
cleanup() { "${IE[@]}" "$WORK/bin/conch" server stop >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup # a server left by an earlier run would hold the old HOME

bash "$T/stage.sh" "$H" >/dev/null
rm -rf "$TEXT"
python3 "$T/record.py" "$WORK" "$CAST" "$TEXT" "$PNG"
echo "wrote $CAST, its screens in $TEXT and web/src/images/import-*.png"
