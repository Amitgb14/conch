#!/bin/sh
# Builds conch-sandbox for every platform the launcher knows into dist/,
# beside conch's own archives (run it after scripts/release.sh, which
# empties dist/):
#   conch-sandbox-<version>-<os>-<arch>   the program itself, not archived:
#                                         the launcher fetches it as it is
#   SHA256SUMS                            their hashes
#   conch-sandbox.sh                      the launcher, with this version
#                                         and these hashes pinned in it
# Usage: scripts/sandbox-release.sh VERSION [DIST]   (a leading v is dropped)
# $GO is the go to build with (a test gives one that writes stand-ins).
set -eu

version=${1:?usage: scripts/sandbox-release.sh VERSION [DIST]}
version=${version#v}
root=$(cd "$(dirname "$0")/.." && pwd)
dist=${2:-$root/dist}
go=${GO:-go}
case $version in *[!0-9A-Za-z.-]* | "") echo "sandbox-release.sh: $version is not a version" >&2; exit 2 ;; esac

mkdir -p "$dist"
dist=$(cd "$dist" && pwd)
rm -f "$dist"/conch-sandbox-* "$dist/SHA256SUMS" "$dist/conch-sandbox.sh"
cd "$root"

names=
for platform in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64; do
	os=${platform%/*}
	arch=${platform#*/}
	name="conch-sandbox-$version-$os-$arch"
	echo "building $name"
	# No cgo: conch-sandbox needs none, and a cross-build cannot have it.
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch "$go" build -trimpath \
		-ldflags "-s -w -X main.version=$version" -o "$dist/$name" ./cmd/conch-sandbox
	names="$names $name"
done

cd "$dist"
if command -v sha256sum >/dev/null 2>&1; then
	sha256sum $names >SHA256SUMS
else
	shasum -a 256 $names >SHA256SUMS
fi

# The launcher, pinned: want= and each sha_OS_ARCH= line rewritten.
script="sed -e 's/^want=.*/want=$version/'"
while read -r sum name; do
	plat=${name#conch-sandbox-"$version"-}
	script="$script -e 's/^sha_${plat%-*}_${plat#*-}=.*/sha_${plat%-*}_${plat#*-}=$sum/'"
done <SHA256SUMS
eval "$script" <"$root/scripts/conch-sandbox.sh" >conch-sandbox.sh
chmod 755 conch-sandbox.sh
if grep -q '^sha_[a-z0-9]*_[a-z0-9]*=$' conch-sandbox.sh; then
	echo "sandbox-release.sh: the launcher still lacks a hash:" >&2
	grep '^sha_' conch-sandbox.sh >&2
	exit 1
fi
cat SHA256SUMS
