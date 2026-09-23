#!/bin/sh
# Installs conch from a GitHub release.
#   curl -fsSL https://raw.githubusercontent.com/Amitgb14/conch/master/install.sh | sh
# A version — older than the installed one, to go back to it — as an
# argument or in CONCH_VERSION:
#   curl -fsSL .../install.sh | sh -s -- 0.2.0
# Once conch is installed, `conch update` does the same, `conch update list`
# shows the releases and `conch update rollback` goes back one.
# Environment:
#   CONCH_VERSION      release to install (default: latest), e.g. 0.2.0
#   CONCH_INSTALL_DIR  where to put the binary (default: ~/.local/bin)
#   CONCH_RELEASE_URL  release download base (default: GitHub releases)
set -eu

repo=Amitgb14/conch
dir=${CONCH_INSTALL_DIR:-$HOME/.local/bin}

fail() { echo "conch install: $*" >&2; exit 1; }

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "unsupported OS $(uname -s)" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "unsupported CPU $(uname -m)" ;;
esac

if command -v curl >/dev/null 2>&1; then
	get() { curl -fsSL "$1" -o "$2"; }
	final_url() { curl -fsSLI -o /dev/null -w '%{url_effective}' "$1"; }
elif command -v wget >/dev/null 2>&1; then
	get() { wget -qO "$2" "$1"; }
	final_url() { wget -S --spider "$1" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tail -1; }
else
	fail "needs curl or wget"
fi

version=${1:-${CONCH_VERSION:-}}
if [ "$version" = latest ]; then
	version=
fi
if [ -z "$version" ]; then
	# The latest-release page redirects to its tag, so no API token is
	# needed. A mirror in CONCH_RELEASE_URL is asked instead of GitHub.
	latest="https://github.com/$repo/releases/latest"
	if [ -n "${CONCH_RELEASE_URL:-}" ]; then
		latest="${CONCH_RELEASE_URL%/}/latest"
	fi
	version=$(final_url "$latest" | sed -n 's#.*/tag/v##p' | tr -d '\r')
	[ -n "$version" ] || fail "could not find the latest release"
fi
version=${version#v}

asset="conch_${version}_${os}_${arch}.tar.gz"
base="${CONCH_RELEASE_URL:-https://github.com/$repo/releases/download}/v$version"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "downloading conch $version for $os/$arch"
get "$base/$asset" "$tmp/$asset" || fail "download $base/$asset failed"
get "$base/checksums.txt" "$tmp/checksums.txt" || fail "download checksums failed"

want=$(awk -v a="$asset" '$2 == a || $2 == "*"a { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "$asset is not in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$asset" | awk '{ print $1 }')
else
	got=$(shasum -a 256 "$tmp/$asset" | awk '{ print $1 }')
fi
[ "$got" = "$want" ] || fail "checksum mismatch for $asset"

tar -C "$tmp" -xzf "$tmp/$asset"
mkdir -p "$dir"
cp "$tmp/conch_${version}_${os}_${arch}/conch" "$dir/conch.new"
chmod 755 "$dir/conch.new"
mv -f "$dir/conch.new" "$dir/conch"
echo "installed $dir/conch"

case ":$PATH:" in
*":$dir:"*) ;;
*) echo "add $dir to your PATH, e.g.: echo 'export PATH=\"$dir:\$PATH\"' >> ~/.profile" ;;
esac
