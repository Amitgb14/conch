# Handoff: `conch web` — your agents from your phone

Branch `conch/build-conch-web-phone-gateway`, 19 commits on `0d25b65`
(master when the branch was cut). **Not pushed, not merged.**

All five phases of `docs/plans/phone.md` are built. Further work followed
from Amit using the app on his iPhone: a redesign, a direct terminal,
scrollback, settings in the TUI. It runs on his Mac behind `tailscale serve`.
What remains is checking on real devices; the open rows are listed at the end.

## Running it

What works on Amit's Mac today:

1. **Tailscale.** Run `tailscale serve --bg http://127.0.0.1:8722` once. The
   phone needs Tailscale on and connected.
2. **Address and gateway.** In the TUI, open ⚙ Settings → Web. Set the
   address to `https://laptop.tail1234.ts.net`, then choose
   **Start conch web**. Or, from a terminal pane:
   `conch web -url https://…ts.net`.
3. **Pairing.** Click 🌐 in the status bar, or press `P` in the tree, and scan
   the QR code with the phone's camera. Pairing gives `full` by default.
4. **Devices.** ⚙ Settings → Web → Devices… lists paired devices. There,
   `v` / `r` / `f` change a device's permission and `x` twice revokes it.

## The contract

| | |
| --- | --- |
| Plan | `docs/plans/phone.md`, in the `conch-phone-plan` worktree (gitignored there as confidential; not copied here) |
| API | `docs/plans/phone-api.md` beside it. The original task said `web/api.md`, which never existed |
| Version | `api_version` 1 throughout. As built against at the end: sha256 `a66bcd69…` |
| Changes | Additive only. The file's own **"Additions — 2026-09-30"** section has a changelog. It added: the Pane object; `GET`/`POST /api/panes`, `/api/close` and `/api/rename`; and on the socket `panes.watch` (answered by `panes`, `pane.changed`, `pane.gone`), `text`, `scroll` and `wheel`, with `mouse` on Frame |

`internal/phone/api.go` mirrors the API file one to one.
`TestRoutesAreTheContract` pins every route, socket message, permission and
error status, so drift from the file fails a test.

**Where the build departs from phone.md, and why:**

- **Pairing defaults to `full`, not `reply`.** This was Amit's decision,
  after two phones paired as `reply` couldn't type. `-permission` and
  `r` / `v` still give less.
- **An answer sends arrow keys and Enter**, not the option's number and
  Enter. Amit confirmed this. Claude takes a number as the answer by
  itself, so a following Enter would land on its next question. The cost is
  that a menu with no visible cursor (`❯` `›` `>`) offers no choices.
- **Answering came in phase 2, not 3.** A question shown without its buttons
  was the wrong half to ship.
- **The gateway listens on loopback when `-url` is set**, not on the
  Tailscale address. The macOS Tailscale app can't proxy to the machine's
  own tailnet address (`tailscale nc 100.101.102.103 8722` → connection refused).

## What was built

**CLI** (`cmd/conch/web.go`, usage in `main.go`):
- `conch web [-listen ADDR] [-port N] [-url URL] [-cert F -key F]`.
  Without flags it uses `[web] url` / `port` from config.toml.
- `conch web pair [-permission …]` prints a QR code when it writes to a terminal.
- `devices`, `revoke ID`, `permission ID view|reply|full`, and `stop`.
- `pair`, `revoke` and `permission` are refused from a scoped agent's pane.

**Gateway** (`internal/phone`), a client of the local server like the TUI:

| File | What it does |
| --- | --- |
| `gateway.go` | Routes, auth, CSRF, same-origin checks (plus `AllowOrigin` for the `-url` address), pairing with its rate limit, refusing to pair from a plain-http page |
| `socket.go` | The WebSocket: one server connection per open app; permission checked on every message; a revoked device is closed within 250 ms |
| `store.go` | `phone.json`: hashed tokens, codes, push subscriptions, the VAPID key, the gateway's own record (pid and start flags, so the TUI can start it again) |
| `question.go` | Choices read off a waiting agent's screen; the question id; the keys for an answer |
| `agents.go` | The Agent and Pane objects |
| `push.go`, `webpush.go` | Web Push (RFC 8291/8292, standard library only), on the change into waiting or done, sent only to the known push services |
| `qr.go` | The QR code, drawn in half blocks |
| `listen.go` | The Tailscale address |

**App** (`internal/phone/ui`): plain HTML, CSS and ES modules, embedded
with no build step. It's about 84 KB of code; the icons are separate.
- **Look:** after the Claude app, with a warm palette, a serif for headings,
  and conch's queen-conch logo.
- **Inbox:** waiting questions are answered in place.
- **Drawer:** every agent and terminal, by project.
- **Agent page:** a conversation with a composer; send turns into stop (Esc)
  while the agent works.
- **Terminal:** typed into directly. A hidden field holds a sentinel, and a
  key row with sticky ctrl and alt sits above the keyboard. The terminal is
  sized to the visible area.
- **Scrollback:** history is stitched above the live screen. For a program
  that took the mouse (Claude Code), swiping past the box's edge becomes
  wheel steps instead.
- **Menus:** ⋯ switches between chat and terminal, renames, closes. ＋ starts
  a task, an agent or a terminal.
- **Settings:** notifications.
- **Offline:** while the laptop is away it shows the last list, without
  questions.
- **Code layout:** `lib.mjs` holds the logic that has no page in it and is
  tested with node.

**TUI**:
- `pair.go`: the pairing dialog, opened by 🌐 in the status bar or `P`. It
  shows the QR code and code; a click copies the code or the link; `s`
  starts conch web.
- `devices.go`: the Devices panel.
- Settings → Web (`settings.go`): address, port, Start/Stop, Pair, Devices…,
  and copyable commands.
- The help overlay is updated.

**Docs:**
- `web/src/app/docs/phone/page.mdx`, plus the CLI, keys and configuration pages.
- The repository map in `AGENTS.md`.
- `docs/testing/end-to-end.md`: rows 9.70–9.81 and runs R38–R44.

**Dependencies:** `github.com/coder/websocket`, `rsc.io/qr`, and
`charmbracelet/x/term`, which was already in the module graph and is now
direct.

## Decisions worth knowing

- **Permissions.**
  - `view` sees everything and can scroll conch's own history.
  - `reply` adds replying, answering and the wheel.
  - `full` adds typing, stopping, and starting, renaming and closing panes.
- **Watching and typing reach any pane**, not only agents'. A `view`
  device sees plain terminals too.
- **Pushes:**
  - They go only to Google, Mozilla, Apple and Microsoft push hosts, over
    https, with no redirects followed. Otherwise a paired device could aim
    the laptop at any address.
  - The VAPID subject is the project's URL.
  - Questions are never in a push.
  - A change that happens while the watcher reconnects isn't pushed.
- **The QR code** has a two-module margin. It shows only when all of it
  fits; 80×24 is enough.
- **Offline**, the phone keeps the list in `localStorage` but never the
  questions.
- **The pairing code travels in the URL fragment**, so it appears in no log.
  Logs record request kinds and sizes only.

## For a security reviewer

- **This is remote shell access.** A `full` device can type into your shells.
- **Code strength.** The pairing code is six digits. It's protected by its
  5 minutes, single use, 5 wrong guesses per code, and 5 attempts a minute
  per address (20 overall). Behind `tailscale serve` every phone has the
  proxy's address, so the overall limit is what counts.
- **Agent guard.** Any process running as the user can write `phone.json`.
  The refusal from an agent's pane is a guard, like the server's scoping,
  not a lock.
- **Loopback.** With `-url`, the plain-http port is on loopback, so other
  local accounts on the Mac can reach it. They still need a pairing code.

## Tested

- **Suite:** `go test -race -count=1 ./...`, `go vet ./...` and `gofmt -l`
  are clean. `TestUILogic` runs node (34 tests over `lib.mjs` and `sw.js`),
  and skips where node is missing; GitHub's CI runners have it.
- **Gateway:**
  - Every route and socket message against every permission, a revoked
    device and no device.
  - `answer` against a stand-in agent that prints every byte it gets.
  - Revocation closing open sockets.
  - Pairing limits and expiry.
  - Push against a loopback TLS push service that decrypts as a browser
    does, and RFC 8291's own worked example.
- **CLI:** `conch web` as a real process (listen address, recording, `stop`,
  config, `-url`). `conch web pair` checked byte for byte against the QR it
  should draw.
- **TUI:**
  - The dialog, panel and settings at every size.
  - Clicks and keys.
  - Starting and stopping, with the process launch stubbed.
- **Browser** (headless Chrome at 390×844, light and dark, against an
  isolated conch with a stand-in agent):
  - R38–R39: the first app and terminal.
  - R41: the rebuilt app, typing into a real `/bin/sh`.
  - R42: the keyboard taking the bottom of the screen.
  - R43: 300 lines of scrollback.
  - R44: an agent's own view scrolled by the wheel.
- **Real Claude Code pane:** the data behind R44 came from reading this
  session's own Claude Code pane, without changing it. It runs full screen,
  takes the mouse, and conch kept only 20 lines of its history.

## Not tested, open

- **On real devices.** Amit has paired his iPhone, used the app over
  Tailscale and typed into terminals. Nothing beyond that has been
  confirmed on a device. Open rows in `docs/testing/end-to-end.md`:
  - **9.71:** answering real Claude and Codex questions. Gemini, OpenCode
    and Devin have no recorded question screens; they get no choices and
    are answered in the terminal.
  - **9.75–9.77, 9.81:** the Home Screen app, offline, the terminal and the
    keyboard on iOS and Android.
  - **9.78–9.79:** a real push. In headless Chrome a push sent through the
    debugging port never reached the worker; the handler is covered only by
    running `sw.js` in node.
  - **9.80:** a camera scanning the QR code.
  - **R44:** scrolling the real Claude Code from the phone. The stand-in's
    3-lines-per-step may not match Claude's.
- **Untested paths:**
  - Starting a task or agent, renaming, and closing from the app were not
    exercised in a browser; the gateway routes are tested.
  - Turning notifications on in a browser needs the network.
- **The docs site** was not built (`next build`).
- **No skipped bug tests.**

## Found in use and fixed

Each of these has a test now.

- **Pairing over plain http** made devices that could never get in, because
  the `Secure` cookie was dropped. That page is now refused with the reason.
- **`tailscale serve` to the tailnet address** hung and gave 502. The
  gateway now listens on loopback.
- **Pairing defaulted to `reply`,** so the phone couldn't type. It now
  defaults to `full`, and `permission` and the Devices panel can raise an
  existing device.
- **Typing on an iPhone sent backspaces.** iOS left the cursor ahead of
  the hidden field's sentinel; letters were read as deletions. Now read
  from either side of it, the cursor put back after it (R45).
- **The keyboard covered the terminal.** The terminal is now sized to what
  the keyboard leaves.
- **The chat couldn't scroll back.** Fixed with scrollback, then the wheel
  for full-screen agents.
- **Nothing in the dialog or settings could be copied.** A click or a key
  now copies.
- **App rendering slips:** `null` and `[object …]` printed in the drawer;
  `[hidden]` overridden by CSS; frames closed by the next view.

## Conflicts to expect

The `conch-conch-remote-connection-via-webapp` worktree has an uncommitted
gateway of its own. It was not read or reused. Merging both will conflict on:
- `cmd/conch/web.go`
- `web/src/app/docs/phone/page.mdx`
- `main.go`
- `go.mod`
- `docs.ts`
- the CLI docs page

The plan and API files stay in the `conch-phone-plan` worktree, outside git.
