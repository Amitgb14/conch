#!/usr/bin/env python3
"""Draw captured TUI frames as PNGs for the README, which cannot run the
website's renderer.

The frames come from the same tui-frames.json the landing page uses
(capture.sh writes it), so a README picture and the site's screens are the
same screen; they go in assets/, which is where README media lives
(assets/README.md: a file moved breaks every link already published, so add
a name rather than change one). The window around the screen and the
per-cell layout are copied from src/components/landing/tui-screen.tsx on
purpose, so the two keep looking alike. Chrome draws it, at twice the size,
because a terminal screenshot that is resampled stops being readable.

  web/scripts/tui-capture/topng.py assets project:tui-project diff:tui-diff

Each argument after the output directory is FRAME[:NAME].
"""
import html
import json
import os
import subprocess
import sys
import tempfile

T = os.path.dirname(os.path.abspath(__file__))
FRAMES = os.path.join(T, "..", "..", "src", "components", "landing", "tui-frames.json")
CHROME = os.environ.get(
    "CHROME", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
)

# Box-drawing verticals are stretched to meet the rows above and below, as
# the website does: fonts draw them shorter than the line height.
VERTICAL = set("│┃║╭╮╰╯├┤┌┐└┘")

CELL_W = 8.2  # px per column at 13.6px type, enough for the widest glyph
FONT_PX = 13.6
LINE = 1.22

# The conch theme's own background and text (internal/tui/theme.go — what
# `conch app` gives its terminal), and a band one step lighter, so a README
# picture is the colour conch really is. The website's window uses the same
# two, and a Go test keeps all three in step with the theme.
BG = "#15191E"
BAND = "#1B2026"
FG = "#C9D1D9"


def cells(text):
    """Every non-ASCII character gets exactly one cell, as on the website:
    fonts disagree about the width of ⠋, ⏺ and the box lines."""
    out, ascii_run = [], ""
    for ch in text:
        if ord(ch) < 0x7F:
            ascii_run += ch
            continue
        if ascii_run:
            out.append(html.escape(ascii_run))
            ascii_run = ""
        style = ' style="transform:scaleY(1.35)"' if ch in VERTICAL else ""
        out.append(f'<i class="c"{style}>{html.escape(ch)}</i>')
    if ascii_run:
        out.append(html.escape(ascii_run))
    return "".join(out)


def page(frame, styles, cols, title):
    lines = []
    for runs in frame:
        spans = []
        for text, s in runs:
            st = styles[s]
            css = []
            if st.get("fg"):
                css.append(f"color:{st['fg']}")
            if st.get("bg"):
                css.append(f"background:{st['bg']}")
            if st.get("b"):
                css.append("font-weight:700")
            if st.get("d"):
                css.append("opacity:.6")
            if st.get("i"):
                css.append("font-style:italic")
            if st.get("u"):
                css.append("text-decoration:underline")
            spans.append(f'<span style="{";".join(css)}">{cells(text)}</span>')
        lines.append(f'<div class="l">{"".join(spans) or "&nbsp;"}</div>')
    width = round(cols * CELL_W) + 24
    return f"""<!DOCTYPE html>
<meta charset="utf-8">
<style>
  * {{ margin: 0; padding: 0; box-sizing: border-box }}
  body {{ background: {BG}; width: {width + 2}px; font-family: "SF Mono", Menlo, monospace }}
  .w {{ border: 1px solid #30363d; border-radius: 10px; overflow: hidden; background: {BG} }}
  .bar {{ display: flex; align-items: center; gap: 7px; padding: 8px 12px;
          background: {BAND}; border-bottom: 1px solid rgba(255,255,255,.1) }}
  .bar i {{ width: 10px; height: 10px; border-radius: 50%; display: block }}
  .t {{ color: #8b949e; font-size: 11px; margin-left: 6px }}
  .s {{ padding: 10px 12px; color: {FG}; font-size: {FONT_PX}px; line-height: {LINE} }}
  .l {{ height: {LINE}em; white-space: pre }}
  .c {{ display: inline-block; width: 1ch; text-align: center; font-style: normal }}
</style>
<div class="w">
  <div class="bar">
    <i style="background:#ff5f57"></i><i style="background:#febc2e"></i><i style="background:#28c840"></i>
    <span class="t">{html.escape(title)}</span>
  </div>
  <div class="s">{"".join(lines)}</div>
</div>
"""


def shot(html_text, out, width, height):
    with tempfile.TemporaryDirectory() as tmp:
        src = os.path.join(tmp, "frame.html")
        with open(src, "w") as f:
            f.write(html_text)
        subprocess.run(
            [
                CHROME,
                "--headless",
                "--disable-gpu",
                "--hide-scrollbars",
                "--force-device-scale-factor=2",
                f"--window-size={width},{height}",
                f"--screenshot={out}",
                f"--user-data-dir={tmp}/chrome",
                f"file://{src}",
            ],
            check=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )


TITLES = {
    "project": "conch — api",
    "working": "conch — Add health check",
    "waiting": "conch — Fix flaky login test",
    "branch": "conch — feat/login",
    "terminal": "conch — dev-server",
    "diff": "conch — api/add-health-check · changes",
    "queue": "conch — review queue",
}


def main(argv):
    if len(argv) < 2:
        sys.exit(__doc__)
    outdir, wanted = argv[0], argv[1:]
    data = json.load(open(FRAMES))
    cols, styles = data["cols"], data["styles"]
    for spec in wanted:
        frame, _, name = spec.partition(":")
        name = name or frame
        rows = data["frames"][frame]
        width = round(cols * CELL_W) + 26
        height = round(len(rows) * FONT_PX * LINE) + 60
        out = os.path.join(outdir, f"{name}.png")
        shot(page(rows, styles, cols, TITLES.get(frame, "conch")), out, width, height)
        print(f"wrote {out} ({width}x{height} at 2x)")


if __name__ == "__main__":
    main(sys.argv[1:])
