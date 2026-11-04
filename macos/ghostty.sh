#!/bin/sh
# Builds Ghostty's embeddable library (GhosttyKit.xcframework) from a pinned
# upstream release — no fork — into macos/vendor, where Package.swift finds
# it and draws each pane with Ghostty instead of SwiftTerm.
#
# Needs Xcode 26 (for Metal: `xcodebuild -downloadComponent MetalToolchain`
# once) and the Zig the release names (0.17.0 for v1.4.0: `brew install zig`).
#
#   macos/ghostty.sh
set -eu

tag=v1.4.0
here=$(cd "$(dirname "$0")" && pwd)
vendor="$here/vendor"
src="$vendor/ghostty"

# Xcode, not the Command Line Tools: the shaders need its Metal compiler.
if [ -z "${DEVELOPER_DIR:-}" ] && [ -d /Applications/Xcode.app ]; then
	export DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer
fi
if ! xcrun -sdk macosx metal --version >/dev/null 2>&1; then
	echo "Ghostty needs Xcode's Metal compiler: install Xcode, then run" >&2
	echo "  xcodebuild -downloadComponent MetalToolchain" >&2
	exit 1
fi
command -v zig >/dev/null || { echo "Ghostty needs Zig: brew install zig" >&2; exit 1; }

mkdir -p "$vendor"
if [ ! -d "$src/.git" ]; then
	git clone -q --depth 1 --branch "$tag" https://github.com/ghostty-org/ghostty.git "$src"
fi
have=$(git -C "$src" describe --tags --exact-match 2>/dev/null || echo "")
if [ "$have" != "$tag" ]; then
	echo "macos/vendor/ghostty is at ${have:-an untagged commit}, not $tag; remove it and run this again" >&2
	exit 1
fi

# This Mac's architecture only, a release build, and none of Ghostty's own
# app, executable or docs.
(cd "$src" && zig build -Demit-xcframework=true -Dxcframework-target=native \
	-Demit-macos-app=false -Demit-exe=false -Demit-docs=false -Doptimize=ReleaseFast)

rm -rf "$vendor/GhosttyKit.xcframework"
cp -R "$src/macos/GhosttyKit.xcframework" "$vendor/GhosttyKit.xcframework"
echo "$vendor/GhosttyKit.xcframework (Ghostty $tag)"
