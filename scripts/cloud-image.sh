#!/bin/sh
# Makes an Ubuntu machine (or the image it is about to become) ready to be
# a Conch Cloud machine: git, the user conch, conch in /usr/local/bin, and
# the conch-cloud-agent unit enabled. Run it once, as root, while building
# the image; the control plane does the rest at each boot, by writing
# /run/conch-cloud/boot.json and restarting the unit.
#
#   curl -fsSL https://raw.githubusercontent.com/Amitgb14/conch/master/scripts/cloud-image.sh | sudo sh
#   sudo sh scripts/cloud-image.sh 0.1.12          # a given release
#   sudo CONCH_BINARY=./bin/conch-linux sh scripts/cloud-image.sh   # a build of your own
#
# Environment:
#   CONCH_VERSION   release to install (default: latest)
#   CONCH_BINARY    install this conch instead of a release
#   CONCH_USER      the machine's user (default: conch)
set -eu

fail() { echo "conch cloud image: $*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || fail "run as root"

user=${CONCH_USER:-conch}
version=${1:-${CONCH_VERSION:-latest}}

if command -v apt-get >/dev/null 2>&1; then
	export DEBIAN_FRONTEND=noninteractive
	apt-get update -q
	apt-get install -y -q git curl ca-certificates
fi
command -v git >/dev/null 2>&1 || fail "needs git"

if ! id "$user" >/dev/null 2>&1; then
	useradd --create-home --shell /bin/bash "$user"
fi

if [ -n "${CONCH_BINARY:-}" ]; then
	install -m 0755 "$CONCH_BINARY" /usr/local/bin/conch
else
	tmp=$(mktemp)
	curl -fsSL https://raw.githubusercontent.com/Amitgb14/conch/master/install.sh -o "$tmp"
	CONCH_INSTALL_DIR=/usr/local/bin CONCH_APP=no sh "$tmp" "$version"
	rm -f "$tmp"
fi

/usr/local/bin/conch cloud install -user "$user"
/usr/local/bin/conch version
