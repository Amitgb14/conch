# Update 2026-11-26: shipped

This replaces what the handoff below says about pushing, PRs and testing.

- PR #338 merged as fc5a71fd, CI green on Linux and macOS at f49f0269.
  It also carries `/api/limits` and `/api/ask`.
- Website: https://amitgb14.github.io/conch/docs/iphone is live, linked from Phone.
- Conch.app's bundled `conch web` now includes #338: every new route
  answers 401 unauthenticated rather than 404.
- First artifacts listing over 2.1 GB of synthetic transcripts: 2.0 s, 3 ms cached.
- Still to do: end-to-end row 9.246 (a real iPhone), a remote machine,
  and opening a pull request from the phone. The routes reach other people
  with the next release (the docs say 0.2.3 or later).

---

# Handoff: phone gateway routes for the iPhone app

Branch `phone/gateway-routes`, 9 commits on `origin/master` (20db9964).
Shipped as #338 (see the update above).

## What was rebased

The 4 commits of `phone/project-detail` (on the old base edcb3dce, 422
behind) were rebased onto `origin/master` after `git fetch`:

| Old | New | Commit |
| --- | --- | --- |
| 6e58de76 | 140bb0dc | GET /api/project |
| cd0dcdda | 482f8251 | GET /api/resources (+ `internal/procs`, TUI resource monitor moved onto it) |
| a5f3263e | f5598a0f | POST /api/attach |
| c919b6a4 | f6978e0c | review an agent's branch (changes, diff, stage, note, commit, push, PR) |

The pre-rebase state is kept as the local branch
`backup/phone-gateway-routes-prerebase`. Delete it when you no longer need it.

After the rebase, the branch's diff against master is the same size as
before (19 files, +4190/−279).

### Conflicts and how they were resolved

All conflicts were in `AGENTS.md` (the repository map). No Go file
conflicted. None of them were real contradictions, so both sides were kept
every time:

1. **cd0dcdda (resources)**: master added the `internal/preview` row and
   the branch added the `internal/procs` row in the same place. Both rows
   were kept.
2. **a5f3263e (attach)**: master rewrote the `internal/sandbox` row
   (providers as files, format 3/4, conch-integrations). The branch
   appended its `POST /api/attach` sentence to the `internal/phone` row,
   next to it. Kept master's sandbox row and master's phone row, with the
   branch's sentence inserted before "Its shapes are a contract". A script
   checked that the branch's phone row differed from master's only by
   that insertion.
3. **c919b6a4 (git review)**: same shape as 2. Kept master's rows and
   inserted the branch's "Reviewing a pane's branch (`gitreview.go` …)"
   sentence.

`internal/tui/resources.go` and its test (which the resources commit
rewrites onto `internal/procs`) merged cleanly. Master has not touched
either file since edcb3dce, so the branch's change loses nothing of
master's.

## Artifacts work brought over

The old `phone/project-detail` worktree had
**both** staged and unstaged changes. I only read it: it was not changed,
committed in or cleaned, and its `git status` is the same as before.

- **Staged** (artifacts.go, its test, the route in gateway.go and
  gateway_test.go): committed as is in 51b073c8. This version lists only
  the pane's current session and answers 400 without a pane.
- **Unstaged** (listing with no pane: every transcript on the computer;
  with a pane: every transcript in the agent's directory; artifacts from
  several sessions merged; `dir` field): **also brought over**, as
  51de2335. The iPhone app needs it: it lists every artifact published
  on the Mac by calling `GET /api/artifacts` with no pane, which the
  staged version refuses.
  The test was added first and failed without the code (`readAll`/`Dir`
  undefined). I also added `TestArtifactsEndpoint`, which calls the
  route itself under a scratch HOME.

## Fixes of my own (each with a test that failed first)

- 80d89cd4: the transcript cache cleared itself past 1024 entries. Since
  the no-pane listing reads every transcript, a computer with more than
  1024 would re-read all of them on every request (this Mac has 210
  transcripts and 2 GB). The cache now holds 16384, drops deleted
  transcripts first, and only clears when it is full of live ones.
  Tests: `TestReadAllKeepsManyTranscripts` (failed before the fix) and
  `TestArtifactCacheFull`.
- a8548b58: `hello` now carries `features`
  (`artifacts, artifacts.all, attach, git.review, project, resources`).
  `featureRoutes` ties each one to its routes, and
  `TestHelloNamesItsFeatures` checks that every route it names exists. The
  field is `omitempty` and additive, so `APIVersion` stays 2. The web UI
  pins `API_VERSION = 2` and the app reads `api_version`, so a bump would
  have told current clients they were out of date. `artifacts.all` exists
  because a gateway with only the staged version answers the no-pane call
  with 400, not 404.
- cf51376e: the endpoint test now pins `not_found` and `bad_request`, and
  AGENTS.md describes artifacts.go and features.

## Routes the gateway answers now

hello, agents, machines, projects, **project**, **resources**, **artifacts**,
reply, answer, review/send, task, push/key, push/subscribe (POST, DELETE),
panes (GET, POST), close, rename, **attach**, **changes**, **diff**,
**stage**, **review/note**, **commit**, **push**, **pr**, and the socket.
`TestRoutesAreTheContract` pins this list.

I checked every `api/…` path the iPhone app calls against this list.

## The contract

The phone contract is a planning note kept out of the repository, so it
is not in this change; it was updated beside it with the artifacts route
and `features`. The shapes a client relies on are pinned in `api.go` by
`TestRoutesAreTheContract` and `TestHelloNamesItsFeatures`.

## Tests

- `make lint` (gofmt, go vet, staticcheck): clean.
- `go test -race -count=1 ./internal/phone/... ./internal/procs/... ./internal/tui/`:
  ok (phone 191 s, tui 115 s).
- Full suite: `make gate` (lint, then the whole race suite with
  `internal/server` split into 4 shards), run in the foreground: **exit 0**,
  44 packages/shards ok, none failed. I did not also run the unsharded
  `go test -race ./...`, because it runs longer than the 10-minute
  foreground limit. `make gate` runs the same tests.
- No tests were skipped to pass, and no new `t.Skip` bug tests were added.

## Left undone / not verified

- **Not tried live** against the iPhone app (row 9.246).
- Artifacts are read from `~/.claude/projects` only. A Claude Code started
  with another `CLAUDE_CONFIG_DIR` (conch's several-accounts support), and
  panes on remote machines, list none.
- The first no-pane listing reads every transcript on the computer; it
  was measured at 2.0 s over 2.1 GB, 3 ms once cached.
- The earlier `conch web` handoff is kept unchanged below this one.

---

*The earlier handoff in this file, from master, unchanged:*

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
- `docs/testing/end-to-end.md`: rows 9.71–9.82 and runs R38–R45.

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

- **On real devices.** Confirmed on Amit's iPhone over Tailscale:
  pairing, the app, scrolling, and typing into terminals (after the R45
  fix). Nothing beyond that has been confirmed on a device. Open rows in `docs/testing/end-to-end.md`:
  - **9.72:** answering real Claude and Codex questions. Gemini, OpenCode
    and Devin have no recorded question screens; they get no choices and
    are answered in the terminal.
  - **9.76–9.78, 9.82:** the Home Screen app, offline, the terminal and the
    keyboard on iOS and Android.
  - **9.79–9.80:** a real push. In headless Chrome a push sent through the
    debugging port never reached the worker; the handler is covered only by
    running `sw.js` in node.
  - **9.81:** a camera scanning the QR code.
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
  from either side of it, the cursor put back after it (R45). Confirmed
  on his iPhone.
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
