#!/bin/bash
# Stage a demo conch for website screenshots. Nothing here touches the user's
# real conch server, home directory or agents: HOME, CONCH_HOME and both
# sockets point at scratch paths, and `claude` is a fake script.
set -euo pipefail
T=$(cd "$(dirname "$0")" && pwd)
D=$T/demo              # fake HOME for the inner (screenshotted) conch
OUT=$T/outer           # CONCH_HOME of the outer server hosting the TUI
ISOCK=/tmp/cwd-inner.sock
OSOCK=/tmp/cwd-outer.sock

rm -rf "$D" "$OUT"
mkdir -p "$D/.local/bin" "$D/.config/conch" "$D/src" "$OUT"
cp "$T/conch" "$D/.local/bin/conch"

cat > "$D/.config/conch/config.toml" <<'EOF'
[notify]
enabled = false
desktop = false
[update]
check_releases = false
EOF

# --- fake agents -------------------------------------------------------------
cp "$T/claude-bin" "$D/.local/bin/claude"
chmod +x "$D/.local/bin/claude"

cat > "$D/.local/bin/gh" <<'EOF'
#!/bin/sh
cat <<'JSON'
[{"number":12,"title":"Add OAuth login","state":"OPEN","isDraft":false,"url":"https://github.com/acme/api/pull/12",
  "headRefName":"feat/login","reviewDecision":"REVIEW_REQUIRED","updatedAt":"2026-09-13T10:00:00Z",
  "statusCheckRollup":[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"}]},
 {"number":9,"title":"Structured logging","state":"MERGED","isDraft":false,"url":"https://github.com/acme/api/pull/9",
  "headRefName":"chore/logging","reviewDecision":"APPROVED","updatedAt":"2026-09-10T10:00:00Z",
  "statusCheckRollup":[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"}]}]
JSON
EOF
chmod +x "$D/.local/bin/gh"

cat > "$D/.local/bin/devserver" <<'EOF'
#!/bin/bash
G=$'\e[32m'; Y=$'\e[33m'; D=$'\e[2m'; X=$'\e[0m'
echo "${D}\$${X} make dev"
echo "go run ./cmd/api -addr :8080"
echo "${D}2026/09/13 10:42:01${X} ${G}listening${X} on :8080"
paths=("GET /healthz 200 1.2ms" "GET /v1/users 200 8.4ms" "POST /v1/login 401 3.1ms" "GET /v1/users/42 200 4.9ms" "POST /v1/login 200 21.7ms")
for p in "${paths[@]}"; do echo "${D}2026/09/13 10:42:0$((RANDOM%9))${X} $p"; done
exec sleep 100000
EOF
chmod +x "$D/.local/bin/devserver"

# --- demo repository ---------------------------------------------------------
R=$D/src/api
mkdir -p "$R/cmd/api" "$R/internal/http" "$R/auth"
cd "$R"
git init -q -b main
git config user.name "Ada Lovelace"; git config user.email ada@example.com
echo "module github.com/acme/api" > go.mod
printf 'package main\n\nfunc main() {}\n' > cmd/api/main.go
printf 'package http\n\n// routes\n' > internal/http/routes.go
printf 'package auth\n' > auth/session.go
printf '# api\n' > README.md
printf '.env\n' > .gitignore
git add -A && git commit -qm "Initial API skeleton"
for m in "Add user listing endpoint" "Session store with expiry" "Structured logging" "Rate limit config" "CI workflow"; do
  echo "// $m" >> internal/http/routes.go; git commit -qam "$m"
done
git checkout -qb feat/login
printf 'package auth\n\n// OAuth callback\n' > auth/oauth.go; git add -A; git commit -qm "Add OAuth callback handler"
echo "// provider token" >> auth/session.go; git commit -qam "Store the provider token in the session"
git checkout -q main
for b in chore/deps fix/timeouts docs/api spike/grpc; do git branch "$b"; done
echo "Run \`make dev\` to start the server." >> README.md
echo "DATABASE_URL=postgres://localhost/api" > .env

echo staged "$D"
