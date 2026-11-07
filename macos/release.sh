#!/bin/sh
# Builds Conch.app for others to download — the desktop beta — into dist/:
#   Conch_<version>_macos_arm64.zip and its .sha256
# Usage: macos/release.sh VERSION   (e.g. 0.1.9; a leading v is dropped)
#
# Apple Silicon only: Ghostty's library (macos/ghostty.sh) is built for
# arm64, and a beta without Ghostty's panes would not be the app people
# are trying.
#
# Signed with a Developer ID Application certificate when the keychain has
# one (CONCH_SIGN_IDENTITY picks another), with the hardened runtime and a
# secure timestamp, which notarization requires; otherwise ad hoc, and
# macOS then makes whoever downloads it let it through once by hand. With
# CONCH_NOTARY_PROFILE set — a profile made by `xcrun notarytool
# store-credentials` — the app is notarized, the ticket stapled to it, and
# Gatekeeper asked whether it now accepts it, so a download opens with no
# warning at all. Upload the zip to the release by hand:
#   gh release upload vVERSION dist/Conch_VERSION_macos_arm64.zip* --clobber
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

identity=${CONCH_SIGN_IDENTITY:-}
if [ -z "$identity" ]; then
	identity=$(security find-identity -v -p codesigning 2>/dev/null | sed -n 's/.*"\(Developer ID Application: [^"]*\)".*/\1/p' | head -1)
fi
profile=${CONCH_NOTARY_PROFILE:-}
if [ -n "$identity" ]; then
	echo "signing with $identity"
	# Inside out: the conch inside the bundle first, then the bundle.
	codesign --force --options runtime --timestamp --sign "$identity" "$app/Contents/Resources/conch"
	codesign --force --options runtime --timestamp --sign "$identity" "$app"
else
	[ -z "$profile" ] || { echo "release.sh: notarizing needs a Developer ID Application certificate, and the keychain has none" >&2; exit 1; }
	echo "  note: no Developer ID Application certificate: signed ad hoc, and macOS will warn whoever downloads it"
	codesign --force --deep --sign - "$app"
fi
codesign --verify --deep --strict "$app"

if [ -n "$profile" ]; then
	# Apple takes a zip; the ticket it issues is then stapled to the app
	# itself, so the app opens without asking Apple while offline.
	ditto -c -k --sequesterRsrc --keepParent "$app" "$stage/notarize.zip"
	out=$(xcrun notarytool submit "$stage/notarize.zip" --keychain-profile "$profile" --wait 2>&1) || true
	echo "$out"
	if ! echo "$out" | grep -q "status: Accepted"; then
		id=$(echo "$out" | sed -n 's/^ *id: //p' | head -1)
		echo "release.sh: Apple did not accept it${id:+ — xcrun notarytool log $id --keychain-profile $profile says why}" >&2
		exit 1
	fi
	xcrun stapler staple "$app"
	xcrun stapler validate "$app"
	spctl --assess --type execute --verbose=2 "$app" 2>&1 | tee "$stage/spctl.txt"
	grep -q "Notarized Developer ID" "$stage/spctl.txt" || { echo "release.sh: Gatekeeper does not take it as notarized" >&2; exit 1; }
elif [ -n "$identity" ]; then
	echo "  note: signed but not notarized (no CONCH_NOTARY_PROFILE): macOS will still warn whoever downloads it"
fi

mkdir -p "$dist"
rm -f "$dist/$name.zip" "$dist/$name.zip.sha256"
ditto -c -k --sequesterRsrc --keepParent "$app" "$dist/$name.zip"
(cd "$dist" && shasum -a 256 "$name.zip" >"$name.zip.sha256")
cat "$dist/$name.zip.sha256"
