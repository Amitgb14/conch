#!/bin/bash
# Make the demo videos: short clips of conch for a launch post, the website
# or a README, drawn from the storyboard in videos.json.
#
# Each scene is one of conch's own screens — Conch.app's stills
# (web/src/images/desktop-*.png), the phone's, the TUI's — with a camera
# that moves onto what the scene is about, a ring around it, a pointer
# where you would click, and a caption. Frames are drawn by stage.html in
# headless Chrome and encoded by ffmpeg as 1920x1080 H.264, with a still
# from the middle of each beside it. Needs Chrome, ffmpeg and python3 with
# websockets. Touches no conch server.
#
#   web/scripts/demo-video/capture.sh [OUT_DIR] [NAME ...]
#
# OUT_DIR defaults to $TMPDIR/conch-demo-videos; the names are those in
# videos.json, and none means every video.
set -euo pipefail
T=$(cd "$(dirname "$0")" && pwd)
OUT=${1:-${TMPDIR:-/tmp}/conch-demo-videos}
[ $# -gt 0 ] && shift

command -v ffmpeg >/dev/null || { echo "needs ffmpeg (brew install ffmpeg)" >&2; exit 1; }
python3 -c 'import websockets' 2>/dev/null || { echo "needs python3 websockets (pip3 install websockets)" >&2; exit 1; }
# A storyboard mistake is cheaper to find before minutes of drawing.
node "$T/timeline.test.mjs" >/dev/null

python3 "$T/record.py" "$OUT" "$@"
echo "videos in $OUT"
