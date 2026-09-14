#!/bin/bash
# Start the staged demo: inner server with tasks, outer server hosting the TUI.
set -uo pipefail
T=$(cd "$(dirname "$0")" && pwd)
D=$T/demo
R=$D/src/api
IE=(env -i HOME=$D CONCH_HOME=$D/.config/conch CONCH_SOCKET=/tmp/cwd-inner.sock CONCH_GH=$D/.local/bin/gh
  PATH=$D/.local/bin:/usr/bin:/bin:/usr/sbin:/sbin TERM=xterm-256color LANG=en_US.UTF-8 SHELL=/bin/bash USER=ada)
OE=(env -i HOME=$T/outer CONCH_HOME=$T/outer CONCH_SOCKET=/tmp/cwd-outer.sock PATH=/usr/bin:/bin
  TERM=xterm-256color LANG=en_US.UTF-8 SHELL=/bin/bash)

"${OE[@]}" "$T/conch" server stop 2>/dev/null
"${IE[@]}" conch server stop 2>/dev/null
sleep 1
bash "$T/stage.sh"

cd "$R"
"${IE[@]}" conch project add "$R"
"${IE[@]}" conch task -cwd "$R" "Add health check"
"${IE[@]}" conch task -cwd "$R" "Fix flaky login test"
"${IE[@]}" conch new -cwd "$R" -name dev-server devserver

# Give the task branches some git state: commits ahead and uncommitted lines.
W=$R.worktrees/conch-add-health-check
( cd "$W" && printf 'package http\n\n// health\nfunc health() {}\n' > internal/http/health.go && git add -A &&
  git commit -qm "Add /healthz handler" && echo "// readyz" >> internal/http/routes.go && git commit -qam "Add /readyz" &&
  printf '\n// TODO test\n' >> internal/http/health.go )
W2=$R.worktrees/conch-fix-flaky-login-test
( cd "$W2" && printf 'package auth\n\n// wait for sweeper\n' >> auth/session.go )

echo '{"expanded":{},"show_all":{},"sidebar_width":44}' > "$D/.config/conch/ui.json"
nohup "${OE[@]}" "$T/conch" server > "$T/outer.log" 2>&1 &
sleep 1.5
python3 "$T/drive.py" start "${1:-150}" "${2:-40}"
sleep 4
"${IE[@]}" conch status
