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
# Each task's worktree, found by its branch: tasks name both after the
# project and the task (api/add-health-check), and a path written here
# went stale when that naming changed, leaving the demo branches empty.
wt() { git -C "$R" worktree list --porcelain | awk -v b="refs/heads/$1" '/^worktree /{w=$2} $0=="branch " b{print w}'; }
W=$(wt api/add-health-check)
( cd "$W" && cat > internal/http/health.go <<'GO'
package http

import (
	"encoding/json"
	"net/http"
)

// health answers /healthz: 200 when the database answers, 503 when not.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	status := map[string]string{"db": "ok"}
	if err := s.db.PingContext(r.Context()); err != nil {
		status["db"] = err.Error()
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(status)
}
GO
  git add -A && git commit -qm "Add /healthz handler" &&
  printf '\nfunc (s *Server) routes() {\n\ts.mux.HandleFunc("GET /healthz", s.health)\n\ts.mux.HandleFunc("GET /readyz", s.ready)\n}\n' >> internal/http/routes.go &&
  git commit -qam "Route /healthz and /readyz" &&
  printf '\n// ready answers /readyz once migrations have run.\nfunc (s *Server) ready(w http.ResponseWriter, r *http.Request) {\n\tif !s.migrated.Load() {\n\t\thttp.Error(w, "migrating", http.StatusServiceUnavailable)\n\t\treturn\n\t}\n\tw.WriteHeader(http.StatusOK)\n}\n' >> internal/http/health.go )
W2=$(wt api/fix-flaky-login-test)
( cd "$W2" && printf 'package auth\n\n// wait for sweeper\n' >> auth/session.go )

echo '{"expanded":{},"show_all":{},"sidebar_width":44}' > "$D/.config/conch/ui.json"
nohup "${OE[@]}" "$T/conch" server > "$T/outer.log" 2>&1 &
sleep 1.5
python3 "$T/drive.py" start "${1:-150}" "${2:-40}"
sleep 4
"${IE[@]}" conch status
