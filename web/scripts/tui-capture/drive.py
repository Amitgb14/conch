#!/usr/bin/env python3
"""Drive the staged demo: start the inner TUI in a pane of the outer scratch
server, send it keys, and save rendered frames. Only ever talks to the
/tmp/cwd-*.sock scratch sockets."""
import json, os, socket, sys, time

OSOCK = "/tmp/cwd-outer.sock"
T = os.path.dirname(os.path.abspath(__file__))
D = os.path.join(T, "demo")
assert "cwd-outer" in OSOCK


class Conn:
    def __init__(self, path):
        self.s = socket.socket(socket.AF_UNIX)
        self.s.connect(path)
        self.buf = b""
        self.n = 0
        self.frames = {}
        self.call("hello", {"client": "demo-capture", "version": "0", "protocol": 1, "capabilities": []})

    def send(self, obj):
        self.s.sendall((json.dumps(obj) + "\n").encode())

    def read(self):
        while b"\n" not in self.buf:
            chunk = self.s.recv(1 << 20)
            if not chunk:
                raise EOFError
            self.buf += chunk
        line, self.buf = self.buf.split(b"\n", 1)
        m = json.loads(line)
        if m.get("event") == "pane.frame":
            d = m["data"]
            self.frames[d["id"]] = d
        return m

    def call(self, method, params):
        self.n += 1
        mid = str(self.n)
        self.send({"id": mid, "method": method, "params": params})
        while True:
            m = self.read()
            if m.get("id") == mid:
                if m.get("error"):
                    raise RuntimeError(m["error"])
                return m.get("result")

    def pump(self, seconds):
        self.s.settimeout(0.2)
        end = time.time() + seconds
        while time.time() < end:
            try:
                self.read()
            except (socket.timeout, TimeoutError):
                pass
        self.s.settimeout(None)


def inner_env():
    return [
        f"HOME={D}", f"CONCH_HOME={D}/.config/conch", "CONCH_SOCKET=/tmp/cwd-inner.sock",
        f"CONCH_GH={D}/.local/bin/gh", f"PATH={D}/.local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
        "TERM=xterm-256color", "COLORTERM=truecolor", "LANG=en_US.UTF-8", "SHELL=/bin/bash",
        "USER=ada", "SSH_TTY=", "CONCH_PANE_ID=",
    ]


def main():
    cmd = sys.argv[1]
    c = Conn(OSOCK)
    state = os.path.join(T, "tui-pane")
    if cmd == "start":
        cols, rows = int(sys.argv[2]), int(sys.argv[3])
        env = " ".join(f"'{e}'" for e in inner_env())
        info = c.call("pane.create", {
            "name": "tui", "cwd": f"{D}/src/api", "cols": cols, "rows": rows,
            "command": ["/bin/sh", "-c", f"exec /usr/bin/env -i {env} {D}/.local/bin/conch"],
        })
        open(state, "w").write(info["id"])
        print("pane", info["id"])
        return
    pid = open(state).read().strip()
    if cmd == "keys":  # drive.py keys down down enter
        for k in sys.argv[2:]:
            if k.startswith("text:"):
                c.call("pane.send_text", {"id": pid, "text": k[5:]})
            else:
                c.call("pane.send_keys", {"id": pid, "keys": [k]})
            time.sleep(0.15)
    elif cmd == "select":  # drive.py select "feat/login": walk the tree to it
        import re
        target = sys.argv[2]
        c.send({"method": "pane.subscribe", "params": {"id": pid}})

        def selected():
            c.pump(0.6)
            for line in c.frames[pid]["lines"][2:]:
                # the tree's selected row is drawn on the accent background
                if "48;2;15;123;138" in line and target in re.sub(r"\x1b\[[0-9;:]*m", "", line)[:45]:
                    return True
            return False

        for _ in range(25):
            c.call("pane.send_keys", {"id": pid, "keys": ["up"]})
        for _ in range(30):
            if selected():
                print("selected", target)
                return
            c.call("pane.send_keys", {"id": pid, "keys": ["down"]})
        sys.exit(f"could not select {target}")
    elif cmd == "resize":
        c.call("pane.resize", {"id": pid, "cols": int(sys.argv[2]), "rows": int(sys.argv[3])})
    elif cmd in ("frame", "text"):
        c.send({"method": "pane.subscribe", "params": {"id": pid}})
        c.pump(float(sys.argv[3]) if len(sys.argv) > 3 else 1.5)
        f = c.frames.get(pid)
        if not f:
            sys.exit("no frame")
        if cmd == "frame":
            json.dump(f, open(sys.argv[2], "w"), ensure_ascii=False)
            print("saved", sys.argv[2], f["cols"], "x", f["rows"])
        else:
            import re
            for l in f["lines"]:
                print(re.sub(r"\x1b\[[0-9;:]*[A-Za-z]", "", l))


main()
