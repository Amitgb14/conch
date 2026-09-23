#!/bin/sh
# Shows the live diff working, without a real agent or an API key.
#
# Makes a throwaway checkout, starts a fake agent that edits it the way a
# real one does — rewriting a line in place, adding and deleting lines,
# creating files, writing into an ignored folder, then committing — and opens
# a conch of its own on it. Everything is isolated: its own CONCH_HOME, its
# own server and socket, a temporary repository. Your own conch, its panes
# and its projects are never touched.
#
# Usage:
#   scripts/live-diff-demo.sh              set it up and open the TUI
#   scripts/live-diff-demo.sh --repo-only  just make the repo and edit it,
#                                          to add to a conch of your choosing
#   scripts/live-diff-demo.sh --pace 1     seconds between edits (default 3)
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
pace=3
tui=1
while [ $# -gt 0 ]; do
	case $1 in
	--repo-only) tui=0 ;;
	--pace) pace=${2:?usage: --pace SECONDS}; shift ;;
	-h | --help) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) echo "live-diff-demo: unknown option $1" >&2; exit 2 ;;
	esac
	shift
done

work=$(mktemp -d)
repo="$work/demo"
home="$work/home"
mkdir -p "$home"

# macOS caps a unix socket path at about 104 bytes.
if [ ${#home} -gt 80 ]; then
	echo "live-diff-demo: $home is too long for a socket; set TMPDIR to something shorter" >&2
	exit 1
fi

agent_pid=
cleaned=
cleanup() {
	[ -n "$cleaned" ] && return 0
	cleaned=1
	if [ -n "$agent_pid" ]; then
		kill "$agent_pid" 2>/dev/null || true
		# Its sleep is orphaned rather than killed; it ends on its own and
		# the loop is gone with the shell that ran it.
		wait "$agent_pid" 2>/dev/null || true
	fi
	# Only ever this demo's own server: CONCH_HOME is the temporary one.
	env -u CONCH_SOCKET -u CONCH_PANE_ID CONCH_HOME="$home" \
		"$root/bin/conch" server stop >/dev/null 2>&1 || true
	rm -rf "$work"
	echo
	echo "cleaned up $work"
}
# The EXIT trap does the work; the signal traps only exit, so that a Ctrl-C
# during `wait` cleans up exactly as a normal exit does.
trap cleanup EXIT
trap 'cleanup; exit 130' INT
trap 'cleanup; exit 143' TERM

echo "building conch…"
make -C "$root" build >/dev/null

# A checkout with something to change, and a folder git ignores so you can
# see that writing there moves nothing.
mkdir -p "$repo/src" "$repo/web" "$repo/node_modules/pkg"
cd "$repo"
git init -q -b main
git config user.name "Live Diff Demo"
git config user.email demo@example.invalid
git config commit.gpgsign false
printf 'node_modules/\n*.log\n' >.gitignore
cat >src/server.go <<'GO'
package main

import "fmt"

func main() {
	fmt.Println("listening on :8080")
	fmt.Println("ready")
}
GO
cat >src/store.go <<'GO'
package main

// store keeps what the server serves.
type store struct {
	items int
	dirty bool
}
GO
cat >web/index.html <<'HTML'
<!doctype html>
<title>demo</title>
<h1>demo</h1>
<p>status: starting</p>
HTML
printf 'module demo\n\ngo 1.25\n' >go.mod
printf 'x\n' >node_modules/pkg/index.js
git add -A
git commit -q -m "the starting point"
git checkout -q -b feat

# The fake agent. No model, no network: a shell loop writing files, in the
# shapes that matter for a diff you are watching.
cat >"$work/agent.sh" <<AGENT
#!/bin/sh
set -eu
cd "$repo"
pace=$pace
owner=$$
n=0
while :; do
	# If the demo was killed outright rather than asked to stop, go with it
	# instead of editing a temporary folder forever.
	kill -0 \$owner 2>/dev/null || exit 0
	n=\$((n + 1))

	# 1. Rewrite a line in place. The file's +/- counts do not move, so
	#    nothing but the watcher can notice this one.
	sed -i.bak "s/listening on :[0-9]*/listening on :\$((8080 + n))/" src/server.go
	rm -f src/server.go.bak
	sleep \$pace

	# 1b. Move to another file, then a third: the list's ▌ follows the work
	#     from one to the next, the way an agent does.
	sed -i.bak "s/items int.*/items int \/\/ round \$n/" src/store.go
	rm -f src/store.go.bak
	sleep \$pace
	sed -i.bak "s|<p>status:.*|<p>status: round \$n</p>|" web/index.html
	rm -f web/index.html.bak
	sleep \$pace

	# 2. Add a line.
	printf '\tfmt.Println("tick %d")\n' "\$n" >>src/server.go
	sleep \$pace

	# 3. Write into a folder git ignores: the diff must not stir.
	printf 'noise %s\n' "\$n" >>node_modules/pkg/index.js
	printf 'noise %s\n' "\$n" >>build.log
	sleep \$pace

	# 4. A new file in a new folder, which has to start being watched.
	mkdir -p "src/gen\$n"
	printf 'package gen%s\n\n// generated\n' "\$n" >"src/gen\$n/doc.go"
	sleep \$pace

	# 5. Delete a line again.
	sed -i.bak '/tick /d' src/server.go
	rm -f src/server.go.bak
	sleep \$pace

	# 6. Every fourth round, commit: an open diff empties and says so.
	if [ \$((n % 4)) -eq 0 ]; then
		git add -A
		git commit -q -m "round \$n"
		sleep \$pace
	fi
done
AGENT
chmod +x "$work/agent.sh"
"$work/agent.sh" >"$work/agent.log" 2>&1 &
agent_pid=$!

echo
echo "repository : $repo"
echo "branch     : feat"
echo "fake agent : pid $agent_pid, editing every ${pace}s"

if [ "$tui" -eq 0 ]; then
	echo
	echo "Add it to a conch of your choosing:"
	echo "  conch project add $repo"
	echo
	echo "Ctrl-C here stops the edits and removes everything."
	# Not `wait`: a signal arriving during it is not acted on until the
	# child ends, and this child never does.
	while kill -0 "$agent_pid" 2>/dev/null; do
		sleep 1
	done
	exit 0
fi

conch() {
	env -u CONCH_SOCKET -u CONCH_PANE_ID CONCH_HOME="$home" "$root/bin/conch" "$@"
}
conch project add "$repo" >/dev/null

# Say which of the two paths this machine is on, so a diff that only keeps
# up every couple of seconds is not mistaken for a broken watcher.
if conch version --json | grep -q '"worktree.watch.v1"'; then
	echo "live       : file watching is on — edits show in about a tenth of a second"
else
	echo "live       : this machine gives no file watches; falling back to a 2s poll"
	echo "             (the diff still keeps up, just not as promptly)"
fi

cat <<TXT

What to do once the TUI opens:

  1. Open  demo › feat  in the tree and press enter — that is the changes view.
  2. Press enter on  src/server.go  to open its diff.
  3. Watch. The port line is rewritten in place every few seconds: the file
     stays at the same +/- counts, so only the watcher sees it. Changed lines
     are marked  ▌  and counted in the header, and fade after a few seconds.
  4. Scroll with ↑↓ and the header turns from "following" to "F follow";
     press F or g to follow again.
  5. Nothing stirs while the agent writes node_modules/ or build.log — git
     ignores those, so they are never watched.
  6. Every fourth round it commits, and the open diff says
     "no changes in this file" instead of going blank.
  7. ctrl+b % splits: put feat's changes beside it and watch both halves.

  q quits the TUI. Everything is removed when you leave.

TXT
printf 'press enter to open the TUI… '
read -r _
conch
