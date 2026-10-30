# Plan: conch as a desktop app

Status: plan only, nothing built (2026-10-29). Written from a pass over the
code on that date; every file and line named below was read then, so check
them again before relying on one.

## Goal

Run conch as a window: a native app on macOS and Linux that shows the same
fleet the TUI does — machines → Workspace → projects → branches → agents and
terminals — with tabs and splits, the changes and sessions views, and the
orchestration moves (start a task, hand off, review, merge), in a GUI that a
person who has never used tmux can pick up.

It is a **second client of the same server**, never a second conch. Agents
keep running when the window closes, the TUI and the window can be open at
once on the same panes, and a remote machine needs nothing new to be shown
in the window.

### Shape

conch already has the hard half of a desktop client: a server that owns the
PTYs and renders screens (`proto.Frame`, `internal/proto/proto.go:2160`),
and a web client of it (`internal/phone`). So the window is a thin native
shell around a web UI, with a Go host behind it that talks to the server.
The window paints the screens the server renders and sends input back; it
runs no terminal emulator of its own. A conversation view of an agent may
come later as one more way to look at it (phase 8).

### Non-goals (first version)

- Windows. conch has no Windows server; the window would have nothing to
  attach to locally.
- A second terminal emulator in the client. The server's `charmbracelet/x/vt`
  is the only one; the window paints its frames.
- Replacing the TUI or moving its logic out wholesale. The TUI stays the
  first client; the window gets what it needs, lifted into shared packages
  only where both use it.
- Raw PTY byte streaming. See "Terminal rendering" for why frames first.
- Editing files.

## Decisions to make before phase 1

These change what gets built. **Decided 2026-10-29:** D1 Wails, D2 no build
step, D4 terminal first (the server owns the terminals, the window paints
its screens and sends input back; the conversation view stays phase 8). D5 is still open.

| # | Question | Recommendation | Alternatives |
|---|---|---|---|
| D1 | Native shell | **Wails v2** (Go, the system webview: WKWebView on macOS, WebKitGTK on Linux). One language, ~10 MB app, menus and dialogs from Go | Electron (Chromium everywhere, ~120 MB, a Node toolchain); Tauri (Rust); native Go toolkits (Gio/Fyne — every view rebuilt by hand, and a text renderer of our own); no shell at all (browser tab, phase 1 alone) |
| D2 | Frontend build | **No build step**, plain ES modules embedded as they are, as `internal/phone/ui` does; logic in `lib.mjs` tested with node | Vite + TypeScript + a framework (React/Svelte). Worth it if the UI grows past what hand-written modules carry; deciding later costs a rewrite of the views, not of the Go side |
| D3 | How the UI reaches conch | **A generic proxy**: the webview sends `{machine, method, params}` and gets events back; Go owns connections, frames and native effects | A curated API like the phone's (`internal/phone/api.go`) — safer for an untrusted device, but the desktop is the person, and a curated API would have to grow by one route per TUI feature |
| D4 | v1 scope | **Terminal-first parity** with the TUI's daily path (phases 1–5), conversation view later | Conversation-first (chat over transcripts, terminal secondary) |
| D5 | Distribution | Signed, notarized `.app`/`.dmg` (needs an Apple Developer ID), `.deb`/`.rpm`/tarball on Linux, a Homebrew cask | Unsigned builds only (Gatekeeper prompt), source builds |

The rest of this plan assumes the recommendations; where another answer
changes a phase, the phase says how.

## Architecture

```
┌──────────────── conch.app ────────────────┐
│  webview (ui/: HTML, CSS, ES modules)     │
│        │ WebSocket + HTTP, 127.0.0.1,     │
│        │ per-launch token                 │
│  desktop host (Go, internal/desktop)      │
│    • fleet: one client per machine        │──ssh bridge──▶ remote conch server
│    • call proxy, event fan-out            │
│    • frame subscriptions                  │
│    • native: menus, notifications,        │
│      dialogs, clipboard, badge            │
│    • layout + settings (desktop.json)     │
│    • brain (as the TUI runs it)           │
└──────────┬────────────────────────────────┘
           │ unix socket, NDJSON (unchanged)
     local conch server ── PTYs, agents, projects
```

### The desktop host (`internal/desktop`, new)

The Go half of the app. It is to the window what `internal/tui` is to the
terminal: a client of every machine's server, acting as the person.

- **Fleet.** The phone gateway already keeps one client per machine, reached
  in the background so nothing waits on ssh, with panes addressed
  `machine:pane` (`internal/phone/machines.go`: `ensureLocked`:222,
  `composePaneID`:380, `splitPaneID`:385, `lacks`:352). Lift that into
  `internal/fleet` with the phone as its first user and the desktop as its
  second, rather than writing a third copy beside the TUI's. The phone's
  per-device reach (`Device.Machines`) stays in the phone; the desktop
  reaches every machine.
- **Call proxy.** One WebSocket message type `call {id, machine, method,
  params}` → `result {id, result|error}`. The host forwards to that
  machine's client with a timeout and returns what the server said. The
  UI checks capabilities from each machine's `hello` before offering a
  feature, the same rule the TUI keeps (`MissingCapabilities`).
  A short allowlist refuses what the window must not do casually —
  `server.stop`, `server.reload` go through a confirm in the host, not the
  page.
- **Events.** Every machine's events (`pane.*`, `project.*`,
  `worktree.changed`, `review.changed`, `autopilot.note`, `view.show`, …,
  `internal/proto/proto.go:354`–382) are forwarded with the machine named.
- **Frames.** `pane.subscribe` per visible pane, coalesced: two splits
  showing the same pane share one subscription, and a pane not on screen is
  unsubscribed. Frames go to the page as they come — the server already
  throttles to one per `frameInterval` (`internal/server/server.go:1456`).
  The socket is not compressed: it is loopback, where the AGENTS.md note on
  compression applies (a frame moves in microseconds; deflating costs more).
- **Native effects** sit behind variables, as `internal/tui` keeps
  `putClipboard`, `openInBrowser` and the rest, and `TestMain` replaces them.
- **State.** Layout (tabs, splits, workspaces, sidebar width, window
  geometry) in `$CONCH_HOME/desktop.json`. Saved ssh hosts and folders are
  read from `ui.json` (`internal/tui/ssh.go`, `sshhosts.go`, `folders.go`)
  through a small shared reader, so a host saved in the TUI shows in the
  window and the other way round. `config.toml` is shared as is.
- **Transport to the page.** HTTP + WebSocket on 127.0.0.1, a random port,
  a 256-bit token generated at launch and handed to the webview in its URL
  fragment; every request checks the token and `Origin`. Anything running as
  the same user can already reach the unix socket, so this adds no reach —
  but it must add none either: no `0.0.0.0`, no token in logs.
  Why a socket and not Wails' own bindings: the same host then serves
  `conch desktop --browser` for development and tests, runs under `httptest`
  in CI with no webview, and keeps the UI free of Wails.

### The app shell (`desktop/`, its own Go module)

Wails needs cgo and, on Linux, WebKitGTK headers. The root module's
`go vet ./...` and `go test -race ./...` must keep running on CI without
them, and `conch` itself must stay cross-buildable without cgo
(`scripts/release.sh:23`–31). So the shell is a **separate module** in
`desktop/` with a `replace` back to the root; it holds `main.go`, the Wails
window, menus and the build scripts, and imports `internal/desktop` for
everything else. (A Go module may import another's `internal/` only from
inside that tree — `desktop/` sits inside the repository root, so it can.)

### Which binary is the server — the part that bites

`client.EnsureServer` starts a server by running **its own executable** with
`server` (`internal/client/client.go:380`–393), and `buildinfo.Build()` is
the hash of the running executable (`internal/buildinfo/buildinfo.go:33`).
A desktop binary that linked the server would therefore start *itself* as
the server with a different build hash from `conch` on `PATH`; the TUI would
then call that server stale and reload it onto `conch`, whose hash the
window calls stale, and so on.

So the app **bundles the `conch` CLI** of the same release
(`conch.app/Contents/Resources/conch`, `/usr/lib/conch/conch` on Linux) and:

- starts and compares the server through that binary, never itself
  (`EnsureServer` gains a way to name the executable — a parameter, not an
  environment variable, so a TUI never inherits it);
- offers *Install command-line tool* to link that binary
  onto `PATH`, so the TUI and the window are one build;
- when `conch` on `PATH` is a different build — a dev build, a Homebrew
  `conch` — says so in the window and does not fight: it attaches to
  whatever server is running, and lets the existing stale-server flow
  (`internal/update`) decide, the way a TUI of another build does.

The remote side is unchanged: machines are installed and upgraded by the
bundled `conch` through `internal/remote`, as the TUI does.

## Terminal rendering

Frames are already a rendered screen: one ANSI-styled string per row, the
cursor drawn in, plus `Offset`, `History`, `Mouse`, `AltScreen`. The phone
parses them (`internal/phone/ui/lib.mjs`: `parseLine`:76, `color256`:11).
The window does the same, onto a `<canvas>`:

- parse each row into runs (text, fg, bg, attributes, OSC 8 link); redraw
  only rows whose string changed since the last frame;
- a glyph cache per (font, size, style); wide characters by
  `displaywidth`-equivalent rules shared with the server's (one test feeds
  the same lines to both);
- selection, copy and search over the frame text and conch's history
  (`pane.scroll`, `pane.search`), not the page's DOM selection;
- keys to `pane.send_keys` (the phone's `keyFromEvent`:372 is the start),
  text and IME composition through a hidden `<textarea>` to
  `pane.send_text`, mouse to `pane.send_mouse` — routed to the pane under
  the pointer, not the focused one (AGENTS.md: "The mouse goes to the pane
  under the pointer") — and the wheel to conch's history on the main screen,
  to the program on the alternate screen, with alt/ctrl handing it over, the
  same rule the TUI keeps;
- resize: the canvas's cell grid decides `pane.resize`, compared against
  `PaneInfo.Cols/Rows` rather than what was last asked (the `syncView`
  lesson).

Two protocol additions, both optional fields behind a capability
(`pane.frame.cursor.v1`):

- `Frame.Cursor {Row, Col, Visible, Shape}` — the drawn-in cursor is enough
  to *see*, but an IME's candidate window and a blinking cursor need its
  position, which today has to be guessed from the styling.
- Nothing else until measured. If WebKitGTK cannot keep up with a dense
  160×50 pane at the frame interval, the fallback is per-row deltas on the
  wire (`pane.frame.delta.v1`), not a client-side emulator.

**Why not raw PTY bytes into xterm.js**: two emulators
disagree after every resize, reattach and reload; conch's alternate-screen
scrollback (`internal/pane/altscroll.go`) and text history
(`internal/pane/history.go`) exist only server-side; and a remote pane's
bytes would cross ssh uncompressed where a frame is compressed by the
master. Frames are what the TUI and the phone already paint.

## Two clients on one pane

The TUI resizes every pane it draws to its space, and so will the window.
With both open on one pane they would trade sizes on every redraw — the
phone already causes a milder form of this (AGENTS.md: "A pane has more than
one client"). Proposal, phase 3:

- the server keeps the size each client asked for, per pane, and applies the
  one from the client that **last sent input** to it (tmux's
  `window-size latest`); a capability `pane.size.latest.v1`;
- an older server keeps today's last-resize-wins, and the window says
  "also shown in another conch" on a pane whose reported size isn't its own.

## Layout and views

The window mirrors the TUI's model so habits carry over, with GUI
affordances where a terminal had keys:

- **Sidebar:** the tree (`internal/tui/tree.go`), with the agent marks from
  `agentDots`/`agentColors` (`internal/tui/view.go:1431`, 1461). The marks
  are emitted from Go into a generated `marks.json` the UI loads, so the
  rule "every agent gets a mark, no two share one" has one source; a test
  checks the generated file is current.
- **Tabs and splits:** tabs scoped to the group selected in the tree
  (`internal/tui/scope.go`), splits by drag or menu, a pane shown in exactly
  one place, closing a split ends its panes after confirming — the TUI's
  rules (`internal/tui/tabs.go`, `layout.go`). The layout is published with
  `view.state` and `view.show` is honoured (`internal/server/view.go`), so an
  agent asking for panes to be shown gets them in the window too.
- **Views in the main area:** Changes/diff with review comments, Sessions,
  the queue (`Q`), agent setup (`i`), Files — each a page in the UI over the
  same methods the TUI calls.
- **Command palette** (⌘K / ctrl+K) carrying every action a row menu has,
  including custom `[[actions]]` from `config.toml`; the TUI's keys stay
  available inside the palette's search, so `ctrl+b` muscle memory has a
  home.
- **Status:** agent states (working, waiting, done, idle) in the sidebar,
  tab, dock badge (count waiting) and native notifications.

## Phases

Each phase ends shippable behind the previous one, with its own tests.

### Phase 0 — spike (no merge)

Throwaway branch; answers the questions D1–D2 rest on.

1. A Wails window on macOS and Linux (WebKitGTK) loading a page from a
   loopback host.
2. The canvas renderer painting a recorded 160×50 dense frame stream (an
   agent's full-screen UI): frame-to-paint p95 under 16 ms on both, and
   input-to-echo under 30 ms locally.
3. A signed, notarized `.app` from CI; a `.deb` that installs on a clean
   Ubuntu LTS.

If 2 fails on WebKitGTK, D1 moves to Electron (same UI, Chromium) before
anything else is built. If 3 cannot be had (no Developer ID), ship unsigned
and say so in the docs.

#### As measured (2026-10-29)

The spike is in `desktop/`: `record/` captures frames from a real conch
pane running a dense, agent-like full-screen program (160×50, a scrolling
conversation, diff hunks, spinner, status line; 16/256/true colour, CJK,
emoji; ~17 KB a frame, 45 of 50 rows changing each frame); `host.go`
replays them over the loopback socket or shows one live `cat` pane of a
conch server; `ui/term.mjs` paints them; `build.sh` makes the bundle.

| | macOS 26.1, WKWebView (Apple silicon) | Rocky Linux 10.2, WebKitGTK 2.50 (headless mutter, software rendering) |
|---|---|---|
| Draw a frame, p50 / p95 / max | 6 / 8 / 34 ms | 6 / 7 / 47 ms |
| Arrival → next paint, p95 | 11 ms | 7 ms |
| 300 frames flooded | painted the newest 4, 56 ms | painted the newest 2, 38 ms |
| Keystroke → echo painted, p50 / p95 | 35 / 38 ms | 35 / 36 ms |
| Same, server sending on the leading edge | **2 / 5 ms** | not run |
| Binary | 13 MB (+ 21 MB bundled `conch`) | 16 MB |

1. **Window: passes** on both. Wails v2.17.0; macOS needs
   `CGO_LDFLAGS=-framework UniformTypeIdentifiers` with plain `go build`,
   Linux the `webkit2_41` tag (WebKitGTK 4.0 is absent on current
   distributions). The page in the webview's own scheme reaches the
   loopback WebSocket with no extra permission.
2. **Rendering: passes** with room, with no glyph cache and every changed
   row redrawn — even on WebKitGTK without a GPU. Max is the first frame
   (font load). WebKit's timer is coarsened to 1 ms, so these are whole
   milliseconds. Not yet measured at device pixel ratio 2, where filling
   costs four times the pixels; the obvious next saving if needed is to
   scroll rows with `drawImage` when a frame is the last one shifted.
   **Input-to-echo fails, and the window is not why:** the server's
   `subscribe` loop (`internal/server/server.go:1471`) waits for a change
   and then *always* sleeps `frameInterval` (33 ms) before sending, so a
   keystroke after a pause waits 33 ms on every client — the TUI too. A
   scratch build sleeping only what is left of the interval since the last
   frame brought echo to 5 ms p95 (reverted; it goes into phase 1 with
   tests, and the TUI gets it for free).
3. **Bundle: half.** `build.sh` makes `conch.app` with the CLI inside it
   at `Contents/Resources/conch`, ad-hoc signed, `codesign --verify
   --strict` clean, and the benchmark runs from inside it. Signing with a
   Developer ID and notarizing wait on D5. `.github/workflows/desktop.yml`
   builds and tests both platforms and uploads the bundle and tarball; it
   has not run yet.

Not covered by the spike: IME composition (a hidden textarea is in place,
untested with a real input method), a Linux desktop session with a GPU,
and the `.deb`/`.rpm`.

### Phase 1 — the host, in a browser

- `internal/fleet` lifted from `internal/phone/machines.go`; the phone's
  tests pass unchanged.
- `internal/desktop`: loopback HTTP/WS, token, Origin check, call proxy,
  event fan-out, coalesced frame subscriptions, `desktop.json`, shared
  `ui.json` reader for ssh hosts and folders.
- `EnsureServer` names its executable.
- Frames sent on the leading edge: `subscribe` sleeps only what is left of
  `frameInterval` since the last frame, so a change after a quiet spell
  goes out at once and a busy screen is still held to one frame per
  interval (phase 0 measured 38 → 5 ms echo). No capability: it is the
  same messages, sooner.
- `conch desktop --browser`: runs the host and opens the UI in the
  browser — no cgo, so it is in the main binary, and it is how the UI is
  developed and tested.

#### As built (2026-10-29)

- Leading-edge frames in `subscribe` (`internal/server/server.go`), with
  `frames_test.go`: the fastest of eight echoes after a quiet spell must
  beat 25 ms (the old loop measured 33.6 ms and fails), and a screen that
  never stops changing still gets at most ~30 frames a second.
- `client.EnsureServerWith(sock, log, exe)`; `EnsureServer` is it with
  `""` (this program).
- `internal/fleet`: the pool lifted out of `internal/phone/machines.go`.
  The phone keeps its hooks, wording and device reach and hands the pool
  functions, so its whole suite ran unchanged on top of it.
- `internal/desktop`: the host — token per start, the webview's origin or
  same-origin only, a generic call proxy that refuses `server.stop`,
  `server.reload` and direct (un)subscribes; one connection per machine
  for calls *and* events, so per-client state such as a scroll offset
  stays on the connection that subscribed; subscriptions shared between
  pages with the newest frame cached for a page that joins late (asking
  for `pane.redraw` would have made the program repaint); re-subscribing
  after a machine's connection drops; frames coalesced per pane for a
  slow page, and a page that stops reading closed at 1024 queued
  messages; `desktop.json` kept whole-or-not-at-all, a corrupt one set
  aside as `desktop.json.bad`.
- The page (`internal/desktop/ui`): machines and panes in a sidebar kept
  current by events, a chosen pane painted and typed into (keys by name,
  text and input-method composition as text). It paints at the pane's own
  size and does not resize it: sizing waits on `pane.size.latest.v1`
  (phase 3), or the window and a TUI would trade sizes.
- `conch desktop -browser [-listen ADDR] [-no-open]`, loopback only, the
  token in the URL's fragment; CLI usage, completion and the CLI docs page.
- **Moved to phase 6:** the shared `ui.json` reader (saved ssh hosts and
  folders) — nothing shows those before machines do.
- Still copied, not shared: `ansi.mjs` between the phone and the window,
  and the spike's own `desktop/ui`, which phase 5 replaces with
  `internal/desktop`'s page.

### Phase 2 — one pane, painted

- Sidebar tree (read-only), select a pane, paint it, type into it, mouse,
  wheel, scroll back, select and copy, resize.
- `pane.frame.cursor.v1`.

### Phase 3 — tabs, splits, workspaces

- Layout model, drag to split, tab bar scoped to the tree selection,
  `view.state`/`view.show`, new shell/agent in a split.
- `pane.size.latest.v1` and the "shown elsewhere" note.

### Phase 4 — the orchestration views

- New task/agent dialog (agent choice from `agent.status`, branch,
  prompt), handoff, close/rename.
- Changes and diff with review comments → `review.send`; commit, push, PR,
  merge, autopilot policy (`autopilot.set`).
- Sessions (list, search, resume, delete), queue, agent setup, files.

### Phase 5 — the app shell

- `desktop/` module, Wails window, the host in-process.
- Native menu with the palette's actions and their accelerators;
  notifications for an agent that waits or fails (the same aggregate the TUI
  sends up stdout, `internal/tui/hoststatus.go`); dock badge; open links
  in the browser; folder picker for *Add project*; drag a file onto a pane
  to type its path (local) or upload it (remote, `fs.upload`).
- Single instance; `conch://` deep links (`conch://pane/<machine>:<id>`)
  so a notification click opens that pane.
- Bundled `conch`, *Install command-line tool*.

### Phase 6 — machines

- Add/remove machines, ssh hosts with options, reconnect, `conch machine
  upgrade` from the window, sandboxes (`internal/sandbox`) — dimmed offline
  machines as the TUI shows them.

### Phase 7 — the rest of the TUI's surface

- Brain bar (runs in the host, as `internal/tui/brain.go` does), settings,
  limits and usage, custom actions, guide.
- App updates: the host downloads the release (`internal/update`), replaces
  the bundle, restarts the window, and reloads the server onto the bundled
  `conch` — the server's PTYs survive as they do today.

### Phase 7b — browser tabs beside terminals

A tab kind `web` in the layout, opened from the palette or by an agent
through `view.show` with a URL (an optional field, `view.web.v1`; a TUI
opens it in the system browser instead). It mostly helps with previews on
localhost, the "worktree that runs" gap in `orchestrator-gaps.md`. Many
public sites refuse to be framed, so those open in the system browser.
Notification sounds go into phase 5.

### Phase 8 — conversation view (optional)

An agent shown as a conversation rather than a screen: messages read from
its transcript (`internal/sessions` already reads Claude, Codex, Gemini,
OpenCode and Devin), tool calls folded, diffs inline, and a reply box that
sends through `agent.reply`. The terminal stays one click away, because the
transcript lags the screen and an agent's permission prompt lives only on
the screen. Needs a streaming transcript read (`session.watch.v1`); decide
after phase 5 whether it is wanted.

## Testing

Per AGENTS.md, nothing touches the developer's real conch.

- **Host (Go):** like the phone gateway's tests — a real server from the
  existing helpers on a short `os.MkdirTemp` socket, `t.Setenv("HOME")`,
  `CONCH_HOME`, `CONCH_SOCKET=""`, `CONCH_PANE_ID=""`; the WebSocket driven
  with `coder/websocket` under `httptest`. Cases: token missing/wrong/
  reused across launches, foreign `Origin`, call to an unknown machine, to a
  machine that is down, timeout, a server lacking a capability, events
  arriving mid-call, two pages subscribing the same pane, page closing
  mid-frame, a reload of the server underneath, `desktop.json` missing,
  truncated, from an older version.
- **Fleet lift:** the phone's whole suite unchanged, plus `-race`.
- **UI logic (JS):** `desktop/ui/lib.mjs` tested with node through a
  `TestDesktopUILogic` that skips without node, as `TestUILogic` does —
  ANSI parsing (shared with the phone), cell widths, key mapping, layout
  operations, palette filtering.
- **Renderer:** golden frames → expected runs; a frame wider than the
  canvas, 1×1, 20×5, a resize mid-frame, wide and combining characters,
  links.
- **Browser:** headless Chrome over CDP against an isolated conch and a
  stand-in agent (`internal/tools/standin`), as the phone was checked —
  local only, not CI.
- **Shell:** CI builds `desktop/` on macOS and Linux runners (WebKitGTK
  installed there only); its Go tests are few, since it is thin.
- **End to end:** rows in `docs/testing/end-to-end.md` for what fakes can't
  prove: WKWebView and WebKitGTK painting, IME, notifications, dock badge,
  notarization, an update replacing the bundle, TUI and window open on one
  pane, a remote machine over real ssh (busybox).

## Docs

A *Desktop app* page under `web/src/app/docs`, the keys page (palette and
accelerators), the install page (cask, dmg, deb), `conch desktop` in the CLI
usage (`cmd/conch/main.go`) and its completion, and the repository map in
AGENTS.md (`internal/fleet`, `internal/desktop`, `desktop/`).

## Risks

- **WebKitGTK** is the weakest webview: canvas speed, IME and font
  rendering vary by distribution. Phase 0 measures it; Electron is the
  fallback.
- **Two front ends** drift. Shared Go packages (`fleet`, the `ui.json`
  reader, marks) and capability checks limit it; a feature added to the TUI
  should name in its PR whether the window has it yet.
- **Size of the TUI.** `internal/tui` is ~79k lines with tests; parity is
  many phases. Phases 1–4 are the daily path and are where to stop and
  judge.
- **Signing** needs an Apple Developer ID and a CI secret; without it macOS
  users get a Gatekeeper prompt.


## Build log — the window to the design (from 2026-10-29)

The design is the canvas "conch desktop — clean" (session, rail, new task,
review, settings; dark and light). The window is being built to it in
passes; each pass ends green (`go test -race ./...`, the page's node tests)
and records here what is done and what is next.

How each pass is checked by eye: `scene.sh` in the session's scratchpad
builds a throwaway conch (own `CONCH_HOME`, `HOME`, socket) with a git
project, two stand-in agent tasks (`internal/tools/standin` built as
`claude` and `codex`) — one made to wait with a real numbered menu by
piping a permission notification through `conch report claude-hook` —
and a shell; `conch desktop -browser -no-open` serves the page, and
headless Chrome takes screenshots (`#view=home|settings`, `theme=dark`,
`rail=1` in the address choose what). Headless Chrome's virtual clock
stalls while the socket is open, so timers (the pane fit) do not run
there: check those in the real app window. Never `cp` over a running
binary on macOS — it is killed (exit 137); remove it first.

### Pass 1 — done

- Layout, colours and type of the design: sidebar (New task, Needs you
  with its count, Settings, projects → tasks with agent icon and state
  dot, Terminals, machines), the rail (⌘B, and in Settings), header,
  tabs of a task's branch, terminal panel with header, inspector
  (Changes, Commits from `project.changes`, refreshed on
  `worktree.changed`), reply box (`agent.prompt` for an agent, text and
  enter for a shell), New task (⌘N; `task.create`, agent picker from
  `agent.status`, recent cards), Settings (⌘,; theme dark/light/system,
  five accents, density, agent icons, rail; autopilot from
  `autopilot.get`/`set` behind `autopilot.v1`; machines).
- Agent icons as the TUI's marks (`ui/icons.mjs`), shared everywhere.
- The waiting card reads the agent's numbered menu off its screen
  (`readMenu`, as the phone does), shows the question above it, and
  answers with arrows and enter from the cursor (`choiceKeys`).
- Panes are fitted to their panel (`fitSize`): compared with the pane's
  real size, never below 20×5, debounced.
- The window opens on the task waiting longest on you.
- Preferences live in `desktop.json` (`prefs`); an address override is
  shown, never saved.

### Pass 2 — done

- Review (⇧⌘R, the inspector's Review, or a changed file): the branch's
  files, each file's diff (`project.diff`, `parseDiff`), comments pinned
  under their line or hunk (`review.list`, `commentsAt`), a comment
  written on a line (`review.set`), "Send N comments to <agent>"
  (`review.send`), commit all, push, open a pull request (`branch.*`).
  Checked against the scene: a comment written in the page is stored,
  listed and pinned under its line.
- Screenshots in real time over Chrome's DevTools protocol
  (`cdp/shot.mjs` in the scratchpad), which can also click and type.

### Pass 3 — done

- In the app (`desktop/native.go`): a notification for each agent that
  starts waiting while the window is not in front (clicking it opens
  that task), the sound once, and the Dock badge with the count
  (Objective-C in `native_darwin.go`); the page reports who waits
  through a binding. The menu bar: an Edit menu (without it ⌘C and ⌘V do
  nothing in the webview), Task and View with the shortcuts. One
  instance. Settings has Notify me and Sound in the app.
- Not seen yet: a notification, the sound and the badge on screen
  (needs a person at the Mac — end-to-end rows).

### Pass 4 — done

- Geist and Geist Mono embedded (`ui/fonts`, OFL), the terminal measured
  again once they load; the asset test follows `url()` too.
- A light palette for the sixteen terminal colours (`setPalette`), chosen
  with the theme.

### Pass 5 — done

- `pane.size.latest.v1` (`internal/server/size.go`): every client's size
  for a pane is kept and the pane takes that of the client last typed
  into (keys, text, a click or the wheel — not a pointer moving); until
  anyone types the last resize wins; a client that leaves gives its
  claim back. The TUI asks once per size it sees, so a resize that waits
  causes no flood of requests. AGENTS.md's note on a pane's many clients
  says so.

### Pass 6 — done

- Docs: a Desktop app page (`web/src/app/docs/desktop`), in the docs'
  navigation; the CLI page points at it. AGENTS.md's repository map has
  `internal/fleet`, `internal/desktop` and `desktop/`. End-to-end rows
  9.150–9.155 for what fakes cannot show.

### Pass 7 — done

- Machines in Settings (a machine in the sidebar opens them): each one's
  state and conch version, **Upgrade conch there**, and **Add a
  machine**. Both run the CLI's own flow (`conch machine add -- TARGET`,
  `conch machine upgrade ID`) in a pane the window then shows, since ssh
  may ask about a host key or a password; the host hands the page the
  `conch` to run (`Options.Conch`: the bundle's, or `conch desktop`'s
  own). The host looks at the machine list every five seconds and tells
  the pages only when it changed, so a machine added that way appears.
- Saved ssh hosts from `ui.json` are still the TUI's alone.

### Pass 8 — done

- The inspector's Checks tab (`branchChecks`): the branch's pull request
  and review, its CI from GitHub (passed of total), the project's own
  check and what the autopilot sent back or holds, from `project.list`
  and `autopilot.get`. Links open in the system browser.

### Pass 9 — done

- Every screen looked at again in real time, dark and light, rail and
  wide, with the fonts. Found and fixed: scrolling a settings section
  into view moved the whole window (the app is now pinned to it).

### Still open

- The app on screen by a person: notifications, sound, badge, input
  methods, copy and paste (rows 9.150–9.155).
- Signing and notarizing (D5), and `.deb`/`.rpm`.
- Phase 7's brain bar and app updates; phase 8's conversation view.
- Saved ssh hosts in the window.
