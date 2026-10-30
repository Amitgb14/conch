#!/bin/sh
# Builds the desktop app for this machine into desktop/dist. Phase 0 spike.
#
#   desktop/build.sh                 # unsigned (ad-hoc signed on macOS)
#   CONCH_SIGN_ID="Developer ID Application: …" desktop/build.sh
#
# The bundle carries the conch CLI of the same source beside the window, so
# the window starts its server through that binary and never itself — a
# server started from the window's own executable would have another build
# hash, and a TUI would call it stale (docs/plans/desktop-gui.md).
set -eu
cd "$(dirname "$0")"
root=..
dist=dist
rm -rf "$dist"
mkdir -p "$dist"
version=$(git -C "$root" describe --tags --always --dirty 2>/dev/null || echo dev)

case "$(uname -s)" in
Darwin)
	app="$dist/conch.app"
	mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
	# Wails' macOS frontend links UniformTypeIdentifiers, which go build
	# alone does not ask for.
	CGO_ENABLED=1 CGO_LDFLAGS="-framework UniformTypeIdentifiers" \
		go build -trimpath -tags desktop,production -o "$app/Contents/MacOS/conch-desktop" .
	# The CLI keeps FSEvents (cgo) when built here, as release.sh does.
	(cd "$root" && CGO_ENABLED=1 go build -trimpath -o "desktop/$app/Contents/Resources/conch" ./cmd/conch)
	cat > "$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>conch</string>
	<key>CFBundleDisplayName</key><string>conch</string>
	<key>CFBundleIdentifier</key><string>dev.conch.desktop</string>
	<key>CFBundleVersion</key><string>$version</string>
	<key>CFBundleShortVersionString</key><string>$version</string>
	<key>CFBundleExecutable</key><string>conch-desktop</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>LSMinimumSystemVersion</key><string>11.0</string>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
EOF
	# Sign the CLI first: a bundle's signature covers what is inside it.
	id="${CONCH_SIGN_ID:--}"
	opts="--force --timestamp=none"
	[ "$id" != "-" ] && opts="--force --timestamp --options runtime"
	codesign $opts --sign "$id" "$app/Contents/Resources/conch"
	codesign $opts --sign "$id" "$app"
	codesign --verify --strict "$app"
	echo "built $app ($version, signed by ${CONCH_SIGN_ID:-ad hoc})"
	;;
Linux)
	# WebKitGTK 4.1 is what current distributions ship; 4.0 is going.
	CGO_ENABLED=1 go build -trimpath -tags desktop,production,webkit2_41 -o "$dist/conch-desktop" .
	(cd "$root" && CGO_ENABLED=0 go build -trimpath -o "desktop/$dist/conch" ./cmd/conch)
	tar -C "$dist" -czf "$dist/conch-desktop-linux-$(uname -m).tar.gz" conch-desktop conch
	echo "built $dist/conch-desktop-linux-$(uname -m).tar.gz ($version)"
	;;
*)
	echo "build.sh: no desktop build for $(uname -s)" >&2
	exit 1
	;;
esac
