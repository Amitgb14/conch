#!/bin/sh
# conch-sandbox.sh runs conch-sandbox at the version pinned below, for the
# herdr plugin, the herdr app's mod and Orca's recipes, none of which can
# count on it being installed:
#   1. conch-sandbox on PATH, if it is that version or newer;
#   2. else the copy kept in ~/.local/share/conch-sandbox/VERSION/;
#   3. else that version for this OS and CPU from conch's release, run only
#      if its sha256 is the one pinned here.
# A copy whose hash is not the pinned one never runs. --fetch only fetches
# and checks it, and prints where it is.
#
# The release (scripts/sandbox-release.sh) writes the version and hashes
# in; a plugin pins the launcher it was given. CONCH_SANDBOX_URL is where
# the builds are, for a mirror or a test.
set -eu

want=0.2.1-dev
sha_darwin_arm64=
sha_darwin_amd64=
sha_linux_arm64=
sha_linux_amd64=

say() { echo "conch-sandbox.sh: $*" >&2; }

# at_least HAVE WANT: HAVE is WANT or newer. x.y.z, and a pre-release
# (x.y.z-dev) is older than its release and the same only as itself.
at_least() {
	h=${1#v} w=${2#v} hp= wp=
	case $h in *-*) hp=${h#*-} h=${h%%-*} ;; esac
	case $w in *-*) wp=${w#*-} w=${w%%-*} ;; esac
	case $h.$w in *[!0-9.]* | *..* | .* | *.) return 1 ;; esac
	old=$IFS IFS=.
	set -- $h; h1=${1:-0} h2=${2:-0} h3=${3:-0}
	set -- $w; w1=${1:-0} w2=${2:-0} w3=${3:-0}
	IFS=$old
	for p in "$h1 $w1" "$h2 $w2" "$h3 $w3"; do
		set -- $p
		[ "$1" -gt "$2" ] && return 0
		[ "$1" -lt "$2" ] && return 1
	done
	[ -z "$hp" ] || [ "$hp" = "$wp" ]
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}

fetch=
if [ "${1:-}" = --fetch ]; then fetch=1; shift; fi

if [ -z "$fetch" ] && have=$(conch-sandbox version 2>/dev/null) && at_least "$have" "$want"; then
	exec conch-sandbox "$@"
fi

case $(uname -s) in Darwin) os=darwin ;; Linux) os=linux ;; *) say "no conch-sandbox build for $(uname -s)"; exit 1 ;; esac
case $(uname -m) in x86_64 | amd64) arch=amd64 ;; arm64 | aarch64) arch=arm64 ;; *) say "no conch-sandbox build for $(uname -m)"; exit 1 ;; esac
eval "pin=\${sha_${os}_${arch}}"
if [ -z "$pin" ]; then
	say "this launcher pins no build of conch-sandbox $want for $os-$arch"
	exit 1
fi

dir=$HOME/.local/share/conch-sandbox/$want
bin=$dir/conch-sandbox
if [ ! -f "$bin" ] || [ "$(sha256 "$bin")" != "$pin" ]; then
	name=conch-sandbox-$want-$os-$arch
	url=${CONCH_SANDBOX_URL:-https://github.com/Amitgb14/conch/releases/download/v$want}/$name
	mkdir -p "$dir"
	tmp=$(mktemp "$dir/.download.XXXXXX")
	trap 'rm -f "$tmp"' EXIT
	say "fetching $name"
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL -o "$tmp" "$url" || { say "could not fetch $url"; exit 1; }
	else
		wget -q -O "$tmp" "$url" || { say "could not fetch $url"; exit 1; }
	fi
	got=$(sha256 "$tmp")
	if [ "$got" != "$pin" ]; then
		say "$url is not the build this launcher pins (sha256 $got, want $pin): not run"
		exit 1
	fi
	chmod 755 "$tmp"
	mv -f "$tmp" "$bin"
	trap - EXIT
fi

if [ -n "$fetch" ]; then
	echo "$bin"
	exit 0
fi
exec "$bin" "$@"
