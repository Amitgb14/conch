#!/bin/sh
# Builds release archives for every supported platform into dist/:
#   conch_<version>_<os>_<arch>.tar.gz and checksums.txt
# Usage: scripts/release.sh VERSION   (e.g. 0.2.0; a leading v is dropped)
set -eu

version=${1:?usage: scripts/release.sh VERSION}
version=${version#v}
root=$(cd "$(dirname "$0")/.." && pwd)
dist="$root/dist"
module=github.com/Amitgb14/conch

rm -rf "$dist"
mkdir -p "$dist"
cd "$root"

for platform in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
	os=${platform%/*}
	arch=${platform#*/}
	name="conch_${version}_${os}_${arch}"
	stage="$dist/$name"
	mkdir -p "$stage"
	echo "building $platform"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
		-ldflags "-s -w -X $module/internal/proto.Version=$version" \
		-o "$stage/conch" ./cmd/conch
	cp LICENSE README.md "$stage/"
	tar -C "$dist" -czf "$dist/$name.tar.gz" "$name"
	rm -rf "$stage"
done

cd "$dist"
if command -v sha256sum >/dev/null 2>&1; then
	sha256sum ./*.tar.gz | sed 's# \./# #' >checksums.txt
else
	shasum -a 256 ./*.tar.gz | sed 's# \./# #' >checksums.txt
fi
cat checksums.txt
