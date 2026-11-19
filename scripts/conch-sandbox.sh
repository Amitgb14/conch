#!/bin/sh
# conch-sandbox.sh runs conch-sandbox at the version pinned below, for the
# herdr plugin, the herdr app's mod and Orca's recipes, none of which can
# count on it being installed:
#   1. conch-sandbox on PATH, if it says it is that version or newer — run
#      as it is: the pin does not cover a copy somebody installed;
#   2. else the copy kept in ~/.local/share/conch-sandbox/VERSION/;
#   3. else that version for this OS and CPU from conch's release, run only
#      if its sha256 is the one pinned here.
# A kept or fetched copy whose hash is not the pinned one never runs. --fetch
# only fetches and checks it, and prints where it is.
#
# It may be run from a file or piped into sh -s (Orca's recipes): nothing it
# runs reads stdin, which then holds the rest of the script, and the
# program gets the stdin the launcher was given — under sh -s that is the
# pipe's end, so key set and pick want the launcher run from a file.
#
# The release (scripts/sandbox-release.sh) writes the version and hashes
# in; a plugin pins the launcher it was given, and a version cut again has
# new hashes, so its plugins need the new launcher. CONCH_SANDBOX_URL is
# where the builds are, for a mirror or a test.
set -eu
umask 077 # the kept copy's folder is this user's alone

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
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" </dev/null; else shasum -a 256 "$1" </dev/null; fi | cut -d' ' -f1
}

# main has the launcher's stdin on fd 3 as well, for the program; every
# other command it runs is given /dev/null. (Not main "$@" </dev/null:
# macOS's bash 3.2 aborts, SIGABRT, when fd 0 is moved while it is still
# reading the script from it under sh -s.)
main() {
	fetch=
	if [ "${1:-}" = --fetch ]; then fetch=1; shift; fi

	# A launcher that is itself the conch-sandbox on PATH would ask itself
	# its version for ever: one started under another asks nothing.
	if [ -z "$fetch" ] && [ -z "${CONCH_SANDBOX_LAUNCHER:-}" ]; then
		CONCH_SANDBOX_LAUNCHER=$$
		export CONCH_SANDBOX_LAUNCHER
		if have=$(conch-sandbox version </dev/null 2>/dev/null) && at_least "$have" "$want"; then
			exec conch-sandbox "$@" <&3 3<&-
		fi
	fi

	case $(uname -s </dev/null) in Darwin) os=darwin ;; Linux) os=linux ;; *) say "no conch-sandbox build for $(uname -s </dev/null)"; exit 1 ;; esac
	case $(uname -m </dev/null) in x86_64 | amd64) arch=amd64 ;; arm64 | aarch64) arch=arm64 ;; *) say "no conch-sandbox build for $(uname -m </dev/null)"; exit 1 ;; esac
	eval "pin=\${sha_${os}_${arch}}"
	if [ -z "$pin" ]; then
		say "this launcher pins no build of conch-sandbox $want for $os-$arch"
		exit 1
	fi

	if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
		say "needs sha256sum or shasum to check conch-sandbox, and has neither"
		exit 1
	fi

	dir=$HOME/.local/share/conch-sandbox/$want
	bin=$dir/conch-sandbox
	mkdir -p "$dir" </dev/null
	if [ ! -O "$dir" ]; then
		say "$dir is not yours: not run"
		exit 1
	fi
	chmod 700 "$dir" </dev/null
	if [ ! -f "$bin" ] || [ "$(sha256 "$bin")" != "$pin" ]; then
		name=conch-sandbox-$want-$os-$arch
		url=${CONCH_SANDBOX_URL:-https://github.com/Amitgb14/conch/releases/download/v$want}/$name
		tmp=$(mktemp "$dir/.download.XXXXXX" </dev/null)
		trap 'rm -f "$tmp"' EXIT
		trap 'rm -f "$tmp"; exit 130' INT TERM
		say "fetching $name"
		if command -v curl >/dev/null 2>&1; then
			curl -fsSL -o "$tmp" "$url" </dev/null || { say "could not fetch $url"; exit 1; }
		elif command -v wget >/dev/null 2>&1; then
			wget -q -O "$tmp" "$url" </dev/null || { say "could not fetch $url"; exit 1; }
		else
			say "needs curl or wget to fetch $name, and has neither"
			exit 1
		fi
		got=$(sha256 "$tmp")
		if [ "$got" != "$pin" ]; then
			say "$url is not the build this launcher pins (sha256 $got, want $pin): not run"
			exit 1
		fi
		chmod 755 "$tmp" </dev/null
		mv -f "$tmp" "$bin" </dev/null
		trap - EXIT INT TERM
	fi

	if [ -n "$fetch" ]; then
		echo "$bin"
		exit 0
	fi
	exec "$bin" "$@" <&3 3<&-
}

# The whole script has been read by the time this line runs, and nothing
# main starts reads stdin, so none of it can take the rest of a piped
# sh -s.
main "$@" 3<&0
