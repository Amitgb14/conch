#!/bin/sh
# Builds Conch.app: the Swift window (release), conch itself inside it so
# each pane's `conch attach` is this same build, and conch's icon. The
# panes are Ghostty's when macos/ghostty.sh has built its library, and
# SwiftTerm's otherwise.
#
#   macos/bundle.sh            # → macos/build/Conch.app
#   macos/bundle.sh /Applications
#
# With CONCH_VERSION set (macos/release.sh does) it is a build for others:
# conch inside stamped with that version, as scripts/release.sh stamps
# the archives, so it installs released binaries on machines rather than
# building them; and no ConchSource, a path on this Mac nobody else has.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(dirname "$here")
out=${1:-$here/build}
app="$out/Conch.app"

# With Ghostty's library built (ghostty.sh) the panes are Ghostty's, and
# linking it takes Xcode's toolchain rather than the Command Line Tools.
if [ -d "$here/vendor/GhosttyKit.xcframework" ] && [ -z "${DEVELOPER_DIR:-}" ] && [ -d /Applications/Xcode.app ]; then
	export DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer
fi
swift build -c release --package-path "$here"
bin=$(swift build -c release --package-path "$here" --show-bin-path)

rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$bin/Conch" "$app/Contents/MacOS/Conch"
if [ -n "${CONCH_VERSION:-}" ]; then
	(cd "$root" && go build -trimpath -ldflags "-s -w -X github.com/Amitgb14/conch/internal/proto.Version=${CONCH_VERSION#v}" \
		-o "$app/Contents/Resources/conch" ./cmd/conch)
else
	(cd "$root" && go build -o "$app/Contents/Resources/conch" ./cmd/conch)
fi

# The icon from conch's own 512px PNG, as `conch app install` makes it;
# without sips or iconutil the bundle still works, with the generic icon.
png="$root/internal/phone/ui/icon-512.png"
if command -v sips >/dev/null && command -v iconutil >/dev/null && [ -f "$png" ]; then
	set_dir=$(mktemp -d)/conch.iconset
	mkdir -p "$set_dir"
	for s in 16 32 128 256 512; do
		sips -z $s $s "$png" --out "$set_dir/icon_${s}x${s}.png" >/dev/null
		d=$((s * 2))
		[ $d -le 512 ] && sips -z $d $d "$png" --out "$set_dir/icon_${s}x${s}@2x.png" >/dev/null
	done
	cp "$png" "$set_dir/icon_512x512@2x.png"
	iconutil -c icns "$set_dir" -o "$app/Contents/Resources/Conch.icns"
	rm -rf "$(dirname "$set_dir")"
fi

# The version the window shows is conch's own (`conch version`, as the
# status bar says it), the build the git commit: About read a git
# describe — v0.1.8-254-g09f463c — that matched nothing else on screen.
version=$("$app/Contents/Resources/conch" version 2>/dev/null | awk '{print $2}')
[ -n "$version" ] || version=dev
build=$(cd "$root" && git rev-parse --short HEAD 2>/dev/null || echo "$version")
source_key="<key>ConchSource</key><string>$root</string>"
[ -z "${CONCH_VERSION:-}" ] || source_key=""
cat >"$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>Conch (Beta)</string>
	<key>CFBundleDisplayName</key><string>Conch (Beta)</string>
	<key>CFBundleIdentifier</key><string>dev.conch.mac</string>
	<key>CFBundleExecutable</key><string>Conch</string>
	<key>CFBundleIconFile</key><string>Conch</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>$version</string>
	<key>CFBundleVersion</key><string>$build</string>
	<key>LSMinimumSystemVersion</key><string>14.0</string>
	<key>NSHighResolutionCapable</key><true/>
	$source_key
	<key>NSSupportsAutomaticTermination</key><false/>
	<!-- A browser tab shows what a dev server speaks, which is plain http. -->
	<key>NSAppTransportSecurity</key><dict>
		<key>NSAllowsLocalNetworking</key><true/>
		<key>NSAllowsArbitraryLoadsInWebContent</key><true/>
	</dict>
</dict>
</plist>
EOF
echo "$app"
