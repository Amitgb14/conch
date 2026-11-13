#!/usr/bin/env python3
"""Record the demo videos: draw each frame of stage.html in headless Chrome
and hand the frames to ffmpeg.

Run by capture.sh. The repository is served on loopback (an ES module will
not load from file://), Chrome gets a profile of its own, and every frame
is drawn from the time alone, so nothing here depends on how fast this
computer is.

  record.py OUT_DIR [NAME ...]     # no names: every video in videos.json
"""
import asyncio
import base64
import functools
import http.server
import json
import os
import shutil
import subprocess
import sys
import tempfile
import threading
import time

import websockets

CHROME = os.environ.get(
    "CHROME", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
)
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))


class Quiet(http.server.SimpleHTTPRequestHandler):
    # Chrome runs no module served under another type, and not every
    # python knows .mjs.
    extensions_map = {**http.server.SimpleHTTPRequestHandler.extensions_map, ".mjs": "text/javascript"}

    def log_message(self, *a):
        pass


def serve():
    """The repository on a loopback port of its own, in the background."""
    handler = functools.partial(Quiet, directory=ROOT)
    httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    return httpd, f"http://127.0.0.1:{httpd.server_address[1]}"


class Page:
    """One browser page, spoken to over the DevTools protocol."""

    def __init__(self, ws, session):
        self.ws, self.session, self.n = ws, session, 0

    async def call(self, method, **params):
        self.n += 1
        msg = {"id": self.n, "method": method, "params": params}
        if self.session:
            msg["sessionId"] = self.session
        await self.ws.send(json.dumps(msg))
        while True:
            m = json.loads(await self.ws.recv())
            if m.get("id") == self.n:
                if "error" in m:
                    raise RuntimeError(f"{method}: {m['error']}")
                return m.get("result", {})

    async def js(self, expr):
        r = await self.call(
            "Runtime.evaluate", expression=expr, awaitPromise=True, returnByValue=True
        )
        if r.get("exceptionDetails"):
            d = r["exceptionDetails"]
            raise RuntimeError(d.get("exception", {}).get("description") or d.get("text"))
        return r["result"].get("value")

    async def until(self, expr, what, timeout=30):
        end = time.time() + timeout
        while time.time() < end:
            if await self.js(expr):
                return
            await asyncio.sleep(0.2)
        raise SystemExit(f"timed out waiting for {what}")


async def open_chrome(profile):
    proc = subprocess.Popen(
        [
            CHROME,
            "--headless",
            "--hide-scrollbars",
            "--force-color-profile=srgb",
            "--no-first-run",
            "--no-default-browser-check",
            "--remote-debugging-port=0",
            f"--user-data-dir={profile}",
            "about:blank",
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    port_file = os.path.join(profile, "DevToolsActivePort")
    for _ in range(80):
        if os.path.exists(port_file):
            lines = open(port_file).read().split("\n")
            if len(lines) > 1:
                return proc, f"ws://127.0.0.1:{lines[0]}{lines[1]}"
        await asyncio.sleep(0.25)
    proc.kill()
    raise SystemExit("chrome never opened a debugging port")


def encoder(path, fps):
    # H.264 in yuv420p with the index up front: what X, LinkedIn, Slack and
    # a GitHub attachment all play.
    return subprocess.Popen(
        [
            "ffmpeg", "-loglevel", "error", "-y",
            "-f", "image2pipe", "-framerate", str(fps), "-i", "-",
            "-c:v", "libx264", "-preset", "slow", "-crf", "18", "-tune", "stillimage",
            "-pix_fmt", "yuv420p", "-movflags", "+faststart", path,
        ],
        stdin=subprocess.PIPE,
    )


async def record(page, base, video, fps, out):
    name = video["name"]
    await page.call("Page.navigate", url=f"{base}/web/scripts/demo-video/stage.html?video={name}")
    try:
        await page.until("window.ready === true", f"{name} to load", timeout=20)
    except SystemExit:
        # Say why: a picture the storyboard names that is not there, most often.
        raise SystemExit(f"{name} did not load: {await page.js('window.loadError || null')}")
    total = await page.js("window.duration")
    n = round(total * fps)
    path = os.path.join(out, f"{name}.mp4")
    ff = encoder(path, fps)
    try:
        for k in range(n):
            # Every picture decoded before the frame is taken.
            await page.js(
                f"draw({k / fps}); Promise.all(Array.from(document.images, i => i.decode().catch(() => 0))).then(() => 1)"
            )
            r = await page.call("Page.captureScreenshot", format="png")
            ff.stdin.write(base64.b64decode(r["data"]))
            if k % fps == 0:
                print(f"\r{name}: {k // fps}s of {total:.0f}s", end="", flush=True)
        # A still for a poster or a thumbnail, from the middle of the video.
        await page.js(f"draw({total / 2})")
        r = await page.call("Page.captureScreenshot", format="png")
        with open(os.path.join(out, f"{name}.png"), "wb") as f:
            f.write(base64.b64decode(r["data"]))
    finally:
        ff.stdin.close()
        if ff.wait() != 0:
            raise SystemExit(f"ffmpeg failed on {name}")
    print(f"\rwrote {path} ({total:.1f}s, {n} frames)")


async def run(out, names):
    sb = json.load(open(os.path.join(HERE, "videos.json")))
    videos = [v for v in sb["videos"] if not names or v["name"] in names]
    missing = set(names) - {v["name"] for v in videos}
    if missing:
        raise SystemExit(f"no such video: {', '.join(sorted(missing))}")
    os.makedirs(out, exist_ok=True)
    httpd, base = serve()
    # Not TemporaryDirectory: Chrome writes to its profile while it is
    # being killed, and the cleanup error would bury the real one.
    profile = tempfile.mkdtemp(prefix="conch-demo-video")
    proc, endpoint = await open_chrome(profile)
    try:
        async with websockets.connect(endpoint, max_size=64 << 20) as ws:
            root = Page(ws, None)
            target = await root.call("Target.createTarget", url="about:blank")
            attached = await root.call("Target.attachToTarget", targetId=target["targetId"], flatten=True)
            page = Page(ws, attached["sessionId"])
            await page.call("Page.enable")
            await page.call("Runtime.enable")
            await page.call(
                "Emulation.setDeviceMetricsOverride",
                width=sb["width"], height=sb["height"], deviceScaleFactor=1, mobile=False,
            )
            for v in videos:
                await record(page, base, v, sb["fps"], out)
    finally:
        proc.terminate()
        proc.wait(timeout=10)
        shutil.rmtree(profile, ignore_errors=True)
        httpd.shutdown()


def main(argv):
    if not argv:
        sys.exit(__doc__)
    asyncio.run(run(argv[0], argv[1:]))


if __name__ == "__main__":
    main(sys.argv[1:])
