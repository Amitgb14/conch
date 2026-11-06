#!/bin/sh
# Builds Conch.app for others to download — the desktop beta — into dist/:
#   Conch_<version>_macos_arm64.zip and its .sha256
# Usage: macos/release.sh VERSION   (e.g. 0.1.9; a leading v is dropped)
#
# Apple Silicon only: Ghostty's library (macos/ghostty.sh) is built for
# arm64, and a beta without Ghostty's panes would not be the app people
# are trying. Signed ad hoc, not notarized — there is no Developer ID
# here — so a downloaded copy is let through once in System Settings →
# Privacy & Security → Open Anyway (the docs say so). Upload the zip to the release by hand:
#   gh release upload vVERSION dist/Conch_VERSION_macos_arm64.zip*
set -eu

version=${1:?usage: macos/release.sh VERSION}
version=${version#v}
here=$(cd "$(dirname "$0")" && pwd)
root=$(dirname "$here")
dist="$root/dist"
name="Conch_${version}_macos_arm64"

[ "$(uname -s)" = Darwin ] || { echo "release.sh: builds on macOS only" >&2; exit 1; }
[ "$(uname -m)" = arm64 ] || { echo "release.sh: builds on Apple Silicon only (Ghostty's library is arm64)" >&2; exit 1; }
[ -d "$here/vendor/GhosttyKit.xcframework" ] || { echo "release.sh: no Ghostty library — run macos/ghostty.sh first" >&2; exit 1; }

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
CONCH_VERSION=$version "$here/bundle.sh" "$stage" >/dev/null
app="$stage/Conch.app"

# What was built has to be what is being released.
got=$("$app/Contents/Resources/conch" version | awk '{print $2}')
[ "$got" = "$version" ] || { echo "release.sh: conch inside says $got, not $version" >&2; exit 1; }
if /usr/libexec/PlistBuddy -c "Print :ConchSource" "$app/Contents/Info.plist" >/dev/null 2>&1; then
	echo "release.sh: the bundle still names this Mac's source folder" >&2; exit 1
fi

codesign --force --deep --sign - "$app"
codesign --verify --deep --strict "$app"

mkdir -p "$dist"
rm -f "$dist/$name.zip" "$dist/$name.zip.sha256"
ditto -c -k --sequesterRsrc --keepParent "$app" "$dist/$name.zip"
(cd "$dist" && shasum -a 256 "$name.zip" >"$name.zip.sha256")
cat "$dist/$name.zip.sha256"
