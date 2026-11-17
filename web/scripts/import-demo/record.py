#!/usr/bin/env python3
"""Record the Conductor import demo: each command typed at a prompt and run
in a terminal of its own, its output timed as it arrives.

  record.py WORK CAST TEXTDIR PNGDIR

WORK is what capture.sh staged: WORK/home is the scratch HOME (a made-up
Conductor database, a made-up shop's repository) and WORK/bin holds the
conch built from this checkout and a stand-in `claude`. Every command runs
with an environment made here from nothing — never this process's own —
so no CONCH_SOCKET or CONCH_PANE_ID of the developer's session reaches it.

It writes CAST, an asciicast v2 recording (`asciinema play CAST`; written
here rather than by asciinema, so it needs nothing installed), and each
step's final screen as text in TEXTDIR and as a PNG in PNGDIR, drawn by
tui-capture's topng.py so the pictures match the site's other terminals.
The scratch HOME is shown as ~ in all of them: it is a temporary folder,
and its path says nothing a reader needs.
"""
import json
import os
import pty
import re
import select
import subprocess
import sys
import time
import fcntl
import struct
import termios

T = os.path.dirname(os.path.abspath(__file__))
sys.dont_write_bytecode = True  # no __pycache__ left in tui-capture
sys.path.insert(0, os.path.join(T, "..", "tui-capture"))
import topng  # noqa: E402  (the site's terminal window, drawn the same way)
import tojson  # noqa: E402  (ANSI to the styled runs topng draws)

COLS, ROWS = 140, 36
SOCKET = "/tmp/conch-import-demo.sock"  # short: macOS caps socket paths

# The conversations the demo chooses: one whose worktree is there, one in
# another workspace, and the marketing site's, whose repository is gone.
COUPONS = "0199f1c2-3a10-7b4e-9c21-4f8a2d6e1b01"
BADGE = "0199f4a8-0b17-7c3d-b5e9-2a4c6e8f0d03"
PRICING = "0199f7b0-5a2c-7e81-a3d7-0c2e4a6c8e06"
COUPONS_FILE = "~/.config/conch/imports/conductor/storefront/add-coupon-codes-to-checkout-0199f1c2.md"
# The one the import's closing command names: the newest it can carry on.
BADGE_FILE = "~/.config/conch/imports/conductor/storefront/cart-badge-keeps-the-old-count-0199f4a8.md"
KYOTO = "~/conductor/workspaces/storefront/kyoto"

# name, title, commands; a command is (text, None), (text, an answer to its
# [y/N] question), or (text, "until:MARKER"): run unrecorded until its
# output holds MARKER, then recorded.
STEPS = [
    ("import-find", "Find the Conductor import", [
        ("conch integration search conductor", None),
        ("conch integration info conductor", None),
    ]),
    ("import-install", "Install it", [
        ("conch integration install conductor", "y"),
    ]),
    ("import-list", "See what Conductor has", [
        ("conch import conductor -n", None),
    ]),
    ("import-choose", "Import the ones you want", [
        ("conch import conductor -ids %s,%s,%s" % (COUPONS, BADGE, PRICING), None),
    ]),
    ("import-handoff", "What an agent is given", [
        ("cat " + COUPONS_FILE, None),
    ]),
    ("import-carry-on", "Carry it on in conch", [
        ("conch project add ~/src/storefront", None),
        ("conch new -agent claude -cwd %s -- 'Read %s and carry on from where it leaves off.'" % (KYOTO, BADGE_FILE), None),
        # Read once the stand-in has drawn its line, not before.
        ("conch read p1", "until:stand-in for claude"),
    ]),
]


def env(work):
    home = os.path.join(work, "home")
    return {
        "HOME": home, "CONCH_HOME": os.path.join(home, ".config", "conch"), "CONCH_SOCKET": SOCKET,
        "PATH": os.path.join(work, "bin") + ":/usr/bin:/bin:/usr/sbin:/sbin",
        "TERM": "xterm-256color", "LANG": "en_US.UTF-8", "SHELL": "/bin/bash", "USER": "ada",
        "COLUMNS": str(COLS), "LINES": str(ROWS),
        "TZ": "UTC",  # the handoff's times, the same wherever it is recorded
    }


# A terminal is asked its background colour (OSC 11) and where its cursor is
# (DSR 6) by the styling library conch uses. These answer as a terminal
# would — the colour conch's own theme has — and the questions are left out
# of the recording, where a player or a picture shows them as junk.
QUERIES = [
    (re.compile(r"\x1b\]11;\?(\x07|\x1b\\)"), "\x1b]11;rgb:1515/1919/1e1e\x1b\\"),
    (re.compile(r"\x1b\[6n"), "\x1b[1;1R"),
]


def run(cmd, e, answer, emit, limit=90):
    """Runs cmd in a terminal of COLS×ROWS, calling emit(text) with its
    output as it comes; answers its [y/N] question once it is asked. A
    command still running after limit seconds is killed, and the recording
    fails rather than waiting for ever."""
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
    p = subprocess.Popen(["/bin/bash", "-c", cmd], env=e, cwd=e["HOME"], stdin=slave, stdout=slave, stderr=slave,
                         start_new_session=True)
    os.close(slave)
    seen, deadline = "", time.time() + limit
    while True:
        if time.time() > deadline:
            p.kill()
            sys.exit("record.py: %r still running after %ds:\n%s" % (cmd, limit, seen[-500:]))
        r, _, _ = select.select([master], [], [], 0.2)
        if r:
            try:
                data = os.read(master, 4096)
            except OSError:
                data = b""
            if not data:
                break
            text = data.decode("utf-8", "replace")
            for q, reply in QUERIES:
                if q.search(text):
                    os.write(master, reply.encode())
                    text = q.sub("", text)
            seen += text
            if text:
                emit(text)
            if answer and seen.rstrip().endswith("[y/N]"):
                time.sleep(0.8)  # a person reading the question
                os.write(master, (answer + "\n").encode())
                answer = None
        elif p.poll() is not None:
            break
    p.wait()
    os.close(master)
    if p.returncode != 0:
        sys.exit("record.py: %r failed (%d):\n%s" % (cmd, p.returncode, seen[-500:]))


def settle(cmd, e, marker, limit=20):
    """Waits, unrecorded, until cmd's output holds marker."""
    deadline = time.time() + limit
    while time.time() < deadline:
        got = subprocess.run(["/bin/bash", "-c", cmd], env=e, cwd=e["HOME"], capture_output=True, text=True)
        if marker in got.stdout:
            return
        time.sleep(0.3)
    sys.exit("record.py: %r never said %r" % (cmd, marker))


def main(work, cast, textdir, pngdir):
    e = env(work)
    home = e["HOME"]
    # As given, as conch prints it (TMPDIR ends in / on a Mac, so the path
    # has a // that conch cleans) and with its links resolved.
    homes = sorted({home, os.path.normpath(home), os.path.realpath(home)}, key=len, reverse=True)

    def tilde(s):
        for h in homes:
            s = s.replace(h, "~")
        return s

    events, t0 = [], time.time()
    clock = [0.0]  # the recording's own time: typing is paced, waiting is real

    def out(text, pause=0.0):
        clock[0] += pause
        events.append([round(clock[0], 3), "o", tilde(text)])

    os.makedirs(textdir, exist_ok=True)
    os.makedirs(pngdir, exist_ok=True)
    for name, title, cmds in STEPS:
        screen = []
        for cmd, answer in cmds:
            if answer and answer.startswith("until:"):
                settle(cmd, e, answer[len("until:"):])
                answer = None
            out("\x1b[32m$\x1b[0m ", 0.6)
            for ch in cmd:
                out(ch, 0.035 if len(cmd) < 70 else 0.012)
            out("\r\n", 0.3)
            shown = ["\x1b[32m$\x1b[0m \x1b[1m" + cmd + "\x1b[0m"]
            got = []
            start = time.time()

            def emit(text):
                got.append(text)
                out(text, 0)
                events[-1][0] = round(clock[0] + (time.time() - start), 3)

            run(cmd.replace("~/", home + "/"), e, answer, emit)
            clock[0] += time.time() - start
            body = tilde("".join(got)).replace("\r\n", "\n").replace("\r", "")
            shown += body.rstrip("\n").split("\n") if body.strip() else []
            screen += shown + [""]
        out("", 1.5)  # a moment on the finished screen
        lines = [wrap for line in screen[:-1] for wrap in fit(line)]
        with open(os.path.join(textdir, name + ".txt"), "w") as f:
            f.write("\n".join(strip(line) for line in lines) + "\n")
        draw(lines, "conch — " + title, os.path.join(pngdir, name + ".png"))

    header = {"version": 2, "width": COLS, "height": ROWS, "timestamp": int(t0),
              "title": "Moving from Conductor to conch", "env": {"SHELL": "/bin/bash", "TERM": "xterm-256color"}}
    with open(cast, "w") as f:
        f.write(json.dumps(header) + "\n")
        for ev in events:
            f.write(json.dumps(ev, ensure_ascii=False) + "\n")
    print("recorded %d steps, %.0f s" % (len(STEPS), clock[0]))


SGR = re.compile(r"\x1b\[[0-9;:]*[A-Za-z]")


def strip(s):
    return SGR.sub("", s)


def fit(line):
    """A line as the terminal shows it: cut into COLS-wide rows. A styled
    line that needs cutting loses its styles rather than half of an escape."""
    if len(strip(line)) <= COLS:
        return [line]
    plain = strip(line)
    return [plain[i:i + COLS] for i in range(0, len(plain), COLS)]


def draw(lines, title, png):
    styles = {json.dumps({}): 0}
    rows = tojson.convert({"lines": lines}, styles)
    table = [None] * len(styles)
    for k, v in styles.items():
        table[v] = json.loads(k)
    width = round(COLS * topng.CELL_W) + 26
    height = round(len(rows) * topng.FONT_PX * topng.LINE) + 60
    topng.shot(topng.page(rows, table, COLS, title), png, width, height)


if __name__ == "__main__":
    if len(sys.argv) != 5:
        sys.exit(__doc__)
    main(*sys.argv[1:])
