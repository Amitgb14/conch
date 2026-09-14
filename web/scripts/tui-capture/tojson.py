#!/usr/bin/env python3
"""Convert captured conch frames (ANSI lines) into compact styled runs for
the website: each line is a list of [text, style] where style is a short
string key into a shared style table."""
import json, os, re, sys

T = os.path.dirname(os.path.abspath(__file__))
BASIC = ["#484f58", "#ff7b72", "#3fb950", "#d29922", "#58a6ff", "#bc8cff", "#39c5cf", "#b1bac4",
         "#6e7681", "#ffa198", "#56d364", "#e3b341", "#79c0ff", "#d2a8ff", "#56d4dd", "#ffffff"]
SGR = re.compile(r"\x1b\[([0-9;:]*)m")


def xterm256(n):
    if n < 16:
        return BASIC[n]
    if n < 232:
        n -= 16
        steps = [0, 95, 135, 175, 215, 255]
        return "#%02x%02x%02x" % (steps[n // 36], steps[n // 6 % 6], steps[n % 6])
    v = 8 + (n - 232) * 10
    return "#%02x%02x%02x" % (v, v, v)


def apply(st, params):
    ps = [int(p) if p else 0 for p in params.replace(":", ";").split(";")] if params else [0]
    i = 0
    while i < len(ps):
        p = ps[i]
        if p == 0:
            st.clear()
        elif p == 1: st["b"] = 1
        elif p == 2: st["d"] = 1
        elif p == 3: st["i"] = 1
        elif p == 4: st["u"] = 1
        elif p == 7: st["r"] = 1
        elif p == 22: st.pop("b", None); st.pop("d", None)
        elif p == 23: st.pop("i", None)
        elif p == 24: st.pop("u", None)
        elif p == 27: st.pop("r", None)
        elif 30 <= p <= 37: st["fg"] = BASIC[p - 30]
        elif 90 <= p <= 97: st["fg"] = BASIC[p - 90 + 8]
        elif 40 <= p <= 47: st["bg"] = BASIC[p - 40]
        elif 100 <= p <= 107: st["bg"] = BASIC[p - 100 + 8]
        elif p == 39: st.pop("fg", None)
        elif p == 49: st.pop("bg", None)
        elif p in (38, 48):
            key = "fg" if p == 38 else "bg"
            if ps[i + 1] == 2:
                st[key] = "#%02x%02x%02x" % tuple(ps[i + 2:i + 5]); i += 4
            elif ps[i + 1] == 5:
                st[key] = xterm256(ps[i + 2]); i += 2
        i += 1


def convert(frame, styles):
    out = []
    for line in frame["lines"]:
        st, runs, pos = {}, [], 0
        for m in list(SGR.finditer(line)) + [None]:
            text = line[pos:m.start()] if m else line[pos:]
            if text:
                s = dict(st)
                if s.pop("r", None):
                    s["fg"], s["bg"] = st.get("bg", "#0d1117"), st.get("fg", "#e6edf3")
                key = json.dumps(s, sort_keys=True)
                if key not in styles:
                    styles[key] = len(styles)
                idx = styles[key]
                if runs and runs[-1][1] == idx:
                    runs[-1][0] += text
                else:
                    runs.append([text, idx])
            if m:
                apply(st, m.group(1))
                pos = m.end()
        out.append(runs)
    return out


names = sys.argv[1:]
styles = {json.dumps({}): 0}
frames = {n: convert(json.load(open(f"{T}/frames/{n}.json")), styles) for n in names}
table = [None] * len(styles)
for k, v in styles.items():
    table[v] = json.loads(k)
json.dump({"cols": 140, "styles": table, "frames": frames}, sys.stdout, ensure_ascii=False, separators=(",", ":"))
