# assets

Images and short animations shown in [README.md](../README.md). Anything here
is served straight from the repository:

```
https://raw.githubusercontent.com/Amitgb14/conch/master/assets/<file>
```

That URL names a branch, so a file renamed or moved breaks every link already
published — in the README of an older tag, in a release, in anything anybody
posted. Add a new name instead of changing one.

## What renders in a README, and what doesn't

| In markdown | GitHub shows |
| --- | --- |
| `![alt](…/assets/demo.webp)` — animated WebP | plays, loops, no controls |
| `![alt](…/assets/demo.gif)` | plays, loops, no controls |
| `![alt](…/assets/demo.mp4)` | **nothing** — image syntax is not a player |
| `<video src="…">` | unreliable; markdown HTML is sanitized |

So a demo that belongs in the README is an **animated WebP** (or a GIF), kept
in here. Real video gets a player only as a GitHub *attachment*: drag the file
into an issue, pull request or release description on github.com, and paste
the `…/user-attachments/assets/…` URL it hands back on a line of its own. That
file lives in GitHub's storage rather than the repository, which is the trade:
a player and sound, but nothing `git clone` brings with it.

A still goes in as a PNG, as the two in the README do.

## Making one

A terminal recording (macOS: ⇧⌘5, or any screen capture) into an animated
WebP. `ffmpeg` here has no WebP encoder, so frames go through `img2webp`:

```sh
mkdir -p /tmp/frames
ffmpeg -i demo.mov -vf "fps=12,scale=1200:-1:flags=lanczos" /tmp/frames/f%04d.png
img2webp -loop 0 -d 83 -q 70 /tmp/frames/f*.png -o assets/demo.webp   # -d 83ms ≈ 12 fps
```

A GIF instead, when something has to read it that WebP defeats:

```sh
ffmpeg -i demo.mov -vf "fps=12,scale=1200:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=128[p];[b][p]paletteuse=dither=bayer" assets/demo.gif
gifsicle -O3 --lossy=40 assets/demo.gif -o assets/demo.gif
```

Keep it **under a few MB** — a README that takes seconds to paint is worse
than a still. 12 fps, 1200 px wide and under 30 seconds is usually enough for
a terminal; the frames are mostly text and compress well. Check the size
before committing, and crop to the part of the window that is the point.

Describe it in the alt text as the README's stills do: what the picture shows,
not "demo".
