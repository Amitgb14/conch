#!/bin/bash
# Regenerate the real TUI screens on the landing page
# (src/components/landing/tui-frames.json).
#
# It builds conch and a fake `claude`, stages a demo project in a scratch
# HOME with its own conch server, runs the TUI inside a second scratch
# server, walks the tree and saves the rendered frames. It never touches
# your own conch server, home directory or agents.
#
#   web/scripts/tui-capture/capture.sh
set -euo pipefail
T=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$T/../../.." && pwd)
WORK=${TMPDIR:-/tmp}/conch-tui-capture
mkdir -p "$WORK/frames"
cp "$T/stage.sh" "$T/run.sh" "$T/drive.py" "$T/tojson.py" "$WORK/"

(cd "$ROOT" && go build -o "$WORK/conch" ./cmd/conch)
(cd "$T/_fakeclaude" && GO111MODULE=off go build -o "$WORK/claude-bin" main.go)

unset CONCH_SOCKET CONCH_PANE_ID CONCH_HOME
bash "$WORK/run.sh" 140 28 >/dev/null
sleep 6 # let git and pull request state settle so the tree order is final

P() { python3 "$WORK/drive.py" "$@"; }
shot() { P select "$1"; sleep "${3:-2.5}"; P frame "$WORK/frames/$2.json"; }

shot "Add health check" working
shot "Fix flaky login" waiting
shot "◆ api" project
shot "feat/login" branch 3
shot "dev-server" terminal

python3 "$WORK/tojson.py" working waiting project branch terminal > "$T/../../src/components/landing/tui-frames.json"

env -i CONCH_SOCKET=/tmp/cwd-outer.sock "$WORK/conch" server stop >/dev/null 2>&1 || true
env -i CONCH_SOCKET=/tmp/cwd-inner.sock "$WORK/conch" server stop >/dev/null 2>&1 || true
echo "wrote src/components/landing/tui-frames.json — check the screens with pnpm dev"
