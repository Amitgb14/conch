#!/usr/bin/env python3
"""Photograph the phone app in headless Chrome, at a phone's size.

Run by capture.sh, which stages the demo and starts `conch web` in front
of it on loopback; this drives a browser through the DevTools protocol:
pair with the code it is given, then walk the app and save a picture of
each screen.

  shots.py http://127.0.0.1:8799 123-456 web/src/images
"""
import asyncio
import base64
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time

import websockets

CHROME = os.environ.get(
    "CHROME", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
)
WIDTH, HEIGHT, SCALE = 390, 844, 3  # an iPhone's own points, at its pixels

# Each screen: the file it is saved as, and what to tap to get there from
# the one before (the tab bar, or a card on the list).
SCREENS = [
    ("phone-agents", ""),
    ("phone-watch", "#tabs a[data-tab=watch]"),
    ("phone-agents-back", "#tabs a[data-tab=list]"),  # back to the list, not saved
    ("phone-agent", ".agents a.row.waiting, .agents a.row"),
]
KEEP = {"phone-agents", "phone-watch", "phone-agent"}


class Page:
    """One browser page, spoken to over the DevTools protocol."""

    def __init__(self, ws, session):
        self.ws, self.session, self.n = ws, session, 0

    async def call(self, method, **params):
        self.n += 1
        msg = {"id": self.n, "method": method, "params": params}
        if self.session:  # the browser itself takes no session id
            msg["sessionId"] = self.session
        await self.ws.send(json.dumps(msg))
        while True:
            m = json.loads(await self.ws.recv())
            if m.get("id") == self.n:
                if "error" in m:
                    raise RuntimeError(f"{method}: {m['error']}")
                return m.get("result", {})

    async def js(self, expr):
        r = await self.call("Runtime.evaluate", expression=expr, awaitPromise=True, returnByValue=True)
        if r.get("exceptionDetails"):
            raise RuntimeError(r["exceptionDetails"].get("text", "js failed"))
        return r["result"].get("value")

    async def until(self, expr, what, timeout=30):
        end = time.time() + timeout
        while time.time() < end:
            if await self.js(expr):
                return
            await asyncio.sleep(0.25)
        raise SystemExit(f"timed out waiting for {what}")

    async def tap(self, selector):
        await self.until(f"!!document.querySelector({json.dumps(selector)})", selector)
        await self.js(f"document.querySelector({json.dumps(selector)}).click()")

    async def shot(self, path):
        r = await self.call("Page.captureScreenshot", format="png", captureBeyondViewport=False)
        with open(path, "wb") as f:
            f.write(base64.b64decode(r["data"]))
        print(f"wrote {path}")


async def open_chrome(profile):
    proc = subprocess.Popen(
        [
            CHROME,
            "--headless",
            "--disable-gpu",
            "--hide-scrollbars",
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


async def run(base, code, out):
    # Not TemporaryDirectory: Chrome writes to its profile while it is
    # being killed, and the cleanup error would bury the real one.
    profile = tempfile.mkdtemp(prefix="conch-phone-shots")
    proc, endpoint = await open_chrome(profile)
    try:
        async with websockets.connect(endpoint, max_size=64 << 20) as ws:
            root = Page(ws, None)
            target = await root.call("Target.createTarget", url="about:blank")
            attached = await root.call(
                "Target.attachToTarget", targetId=target["targetId"], flatten=True
            )
            await walk(Page(ws, attached["sessionId"]), base, code, out)
    finally:
        proc.terminate()
        proc.wait(timeout=10)
        shutil.rmtree(profile, ignore_errors=True)


async def walk(page, base, code, out):
    await page.call("Page.enable")
    await page.call("Runtime.enable")
    await page.call(
        "Emulation.setDeviceMetricsOverride",
        width=WIDTH,
        height=HEIGHT,
        deviceScaleFactor=SCALE,
        mobile=True,
    )
    await page.call("Emulation.setTouchEmulationEnabled", enabled=True, maxTouchPoints=5)

    # A code in the link fills the pairing form in; a phone's owner taps
    # Pair, and so do we. (A code scanned at a relay submits itself, once
    # the words have been checked; this gateway has no relay.)
    await page.call("Page.navigate", url=f"{base}/#code={code}")
    await page.until("!!document.getElementById('pair-submit')", "the pairing form")
    await page.until("!document.getElementById('pair-submit').disabled", "a page that may pair")
    await page.tap("#pair-submit")
    await page.until("!document.getElementById('tabs').hidden", "the phone to pair")
    # The working agents' last lines come from their screens: wait for the
    # first frames, or the cards are drawn without them.
    await asyncio.sleep(4)

    for name, tap in SCREENS:
        if tap:
            await page.tap(tap)
            await asyncio.sleep(2.5)
        if name in KEEP:
            await page.shot(os.path.join(out, f"{name}.png"))


def main(argv):
    if len(argv) != 3:
        sys.exit(__doc__)
    asyncio.run(run(argv[0], argv[1], argv[2]))


if __name__ == "__main__":
    main(sys.argv[1:])
