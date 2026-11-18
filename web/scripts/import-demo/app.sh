#!/bin/bash
# Open a demo copy of Conch.app on the import demo's made-up Conductor, for
# recording the move from Conductor in the app (⌘⇧5 records the screen).
#
# The copy is built from this checkout into a scratch folder with a bundle
# id of its own (dev.conch.importdemo), so its settings are not your
# Conch.app's. It talks only to a scratch server on a socket of its own,
# with a scratch HOME holding the made-up Conductor database and the
# made-up shop's repository (stage.sh). Your Conch.app, your server and
# your Conductor are never touched. It starts behind your other windows
# (-background YES, which also leaves the global scratch key to your own
# Conch.app); click it in the Dock to bring it forward.
#
#   web/scripts/import-demo/app.sh            # build, stage, open
#   SLOW_SCAN=2 web/scripts/import-demo/app.sh  # hold "Reading…" 2 s, to film it
#
# In the app: Window → Integrations, Import in the sidebar, Import… on the
# Conductor card. Quit the copy (⌘Q), or ctrl-c here, and this script
# stops it and its server.
set -euo pipefail
T=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$T/../../.." && pwd)
WORK=${TMPDIR:-/tmp}/conch-import-demo-app
ID=dev.conch.importdemo
SOCK=/tmp/conch-import-demo-app.sock # short: macOS caps socket paths
rm -rf "$WORK"
mkdir -p "$WORK/bin"

(cd "$ROOT" && go build -o "$WORK/bin/conch-real" ./cmd/conch)
(cd "$ROOT" && go build -o "$WORK/bin/claude" ./internal/tools/standin)
# What the app runs as conch (CONCH_BIN). With SLOW_SCAN it waits before
# reading Conductor, so the sheet's "Reading…" stays long enough to film;
# everything else goes straight through.
cat > "$WORK/bin/conch" <<EOF
#!/bin/sh
if [ "\$1" = import ] && [ -n "${SLOW_SCAN:-}" ]; then
  case " \$* " in *" -n "*) sleep ${SLOW_SCAN:-0} ;; esac
fi
exec "$WORK/bin/conch-real" "\$@"
EOF
chmod +x "$WORK/bin/conch"

APP=$WORK/Conch.app
"$ROOT/macos/bundle.sh" "$WORK" >/dev/null
/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier $ID" "$APP/Contents/Info.plist"
defaults delete "$ID" >/dev/null 2>&1 || true # a fresh start each time

unset CONCH_SOCKET CONCH_PANE_ID CONCH_HOME
H=$WORK/home
IE=(env -i HOME=$H CONCH_HOME=$H/.config/conch CONCH_SOCKET=$SOCK CONCH_BIN=$WORK/bin/conch
  PATH=$WORK/bin:/usr/bin:/bin:/usr/sbin:/sbin TERM=xterm-256color LANG=en_US.UTF-8 SHELL=/bin/bash USER=ada)
APP_PID=
cleanup() {
  # The app asks before quitting, so a scripted quit can be cancelled: stop
  # it outright if it is still there, then its server.
  [ -z "$APP_PID" ] || kill "$APP_PID" 2>/dev/null || true
  "${IE[@]}" "$WORK/bin/conch" server stop >/dev/null 2>&1 || true
}
trap cleanup EXIT
"${IE[@]}" "$WORK/bin/conch" server stop >/dev/null 2>&1 || true

bash "$T/stage.sh" "$H" >/dev/null
# The server running, with no project yet: the import adds storefront, which
# is what the recording shows.
"${IE[@]}" "$WORK/bin/conch" project ls >/dev/null

echo "Opening the demo copy of Conch.app (behind your windows: click it in the Dock)."
echo "Window → Integrations → Import → Import… on the Conductor card. ⌘Q when done."
"${IE[@]}" "$APP/Contents/MacOS/Conch" -background YES > "$WORK/app.log" 2>&1 &
APP_PID=$!
wait "$APP_PID" || true
