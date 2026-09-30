# Handoff: `conch web` — the phone gateway (phase 1), app (phase 2) and terminal (phase 3)

Branch `conch/build-conch-web-phone-gateway`, three commits on top of `0d25b65`:
the gateway, the app, the terminal. Not pushed, not merged. Phases 2 and 3
are at the end.

## Contract used

| | |
| --- | --- |
| Plan | `docs/plans/phone.md` in the `conch-phone-plan` worktree (gitignored there) |
| API | `docs/plans/phone-api.md` beside it — the task called it `web/api.md`; no file of that name exists |
| Version | `api_version` 1. The file carries no other version, so: as read 2026-09-30 00:36, sha256 `1a4e80c5…c8f3e8` |
| Status | compatible; nothing in the contract was changed |

Neither file is copied into this branch: both are in that worktree's
`.gitignore` as confidential. `internal/phone/api.go` mirrors the API file,
and `TestRoutesAreTheContract` pins the routes, socket messages, permissions
and error statuses, so drift from the contract fails a test.

## What was built

- `cmd/conch/web.go` — `conch web [-listen ADDR] [-port N] [-cert F -key F]`,
  `conch web pair [-permission view|reply|full]`, `conch web devices`,
  `conch web revoke ID`. Wired into `main.go` and its usage.
- `internal/phone` — the gateway, a client of the local server through
  `client.Dial`:
  - `api.go` the contract's types; `gateway.go` HTTP routes, auth, CSRF,
    pairing and its rate limit; `socket.go` the WebSocket; `store.go`
    `phone.json` in `CONCH_HOME`; `question.go` choices read off a screen;
    `agents.go` the Agent object; `listen.go` the Tailscale address;
    `ui/` the embedded stub page.
- Docs: `web/src/app/docs/phone/page.mdx` (in the sidebar), a `web` section
  in the CLI page, `phone.json` in the configuration page, the repository
  map in `AGENTS.md`, rows 9.70–9.74 in `docs/testing/end-to-end.md`.
- One new dependency: `github.com/coder/websocket v1.8.15` (no dependencies
  of its own).

## Decisions the contract left open

1. **What an answer sends** (confirmed by Amit on 2026-09-30: keep arrow
   keys and Enter). phone.md's v1 says "that option's number and
   Enter", under a heading that calls it an open question. I send **arrow
   keys from the cursor's row to the chosen row, then Enter**. Claude takes
   a number as the answer by itself, so the Enter after it would land on
   whatever Claude asks next — the very thing `question_id` exists to
   prevent. The cost: a menu with no visible cursor (`❯`, `›` or `>`) gives
   no choices (`no_choices`), and only `full` can answer it, by keys. This
   is proven on a fake agent only; row 9.71 is the check on real Claude and
   Codex screens, and is the first thing phase 3 should run.
2. **`question.id`** hashes the pane, `since` and text as the contract
   says, and also the choices and which one the cursor is on. Stricter, and
   invisible to a client, which treats the ID as opaque.
3. **Question text without a hook message** is the paragraph above the
   menu (or the last paragraph on screen), not "the lines the blocked rule
   matched": the gateway doesn't load the detection manifests.
4. **One server connection per socket.** Each open app gets its own
   `client.Dial`, so its frame subscriptions are its own and end with it.
   HTTP routes share one, redialled when the server reloads.
5. **Revocation across processes.** `conch web revoke` is another process,
   so the gateway stats `phone.json` every 250 ms (and on every request,
   socket message and event) and closes that device's sockets with
   `bye {reason: "revoked"}`.
6. **Pairing limits.** 5 attempts a minute per address, 20 overall; 5 wrong
   codes cancel the code outstanding; a new code replaces the old one.
7. **TLS.** The gateway serves plain HTTP on the Tailscale address and
   expects `tailscale serve` in front, as the plan says; `-cert`/`-key`
   serve HTTPS directly. The cookie is always `Secure`.
8. **`frame.open` and `keys` reach any pane**, not only agents' — the
   contract doesn't restrict them. A `view` device can therefore watch a
   plain terminal pane.
9. **`conch web pair` and `revoke` are refused from a scoped agent's
   pane** (asks the server `pane.caller`). Not in the plan; added because a
   paired phone acts as the person, outside the agent's scope.
10. Errors the table has no row for: a wrong method is 405 with
    `bad_request`; a server error that isn't one of the shared codes is
    `server_unavailable`; a missing or wrong CSRF token is `forbidden`.

## Tested

`gofmt -l cmd internal` prints nothing, `go vet ./...` is clean (also with
`GOOS=linux` for the two packages touched), and `go test -race -count=1
./...` passes. The new tests passed three runs in a row.

- Every HTTP route, from the route table itself: nobody, an unknown token,
  a revoked device (401), and each of view/reply/full (403 below what the
  route needs). Every socket message the same way.
- `answer` against a fake agent pane (the test binary, run in a real pane
  of a real server) that prints each byte it receives: choice 3 from a
  cursor on 1 arrives as exactly down, down, Enter; choice 1 from a cursor
  on 3 as up, up, Enter, with a sentinel key right after proving nothing
  else was sent. Fourteen refusals type nothing, including a late answer
  after the question changed.
- `reply` to a waiting agent comes back `agent_blocked` and types nothing.
- Pairing: single use, expiry at the boundary, replaced codes, the rate
  limit with `Retry-After`, hashed storage, file mode 0600, cookie flags.
- Revoking from outside the gateway closes both of a device's open sockets
  and leaves another device's alone; a message racing the revocation types
  nothing.
- CSRF and cross-site requests, including a cross-site socket; the socket
  round (list, updates, frames, keys, `agent.gone`, limits, bad input);
  shutdown and server-gone `bye`s; no server (503); task creation in a
  temporary git repository; push subscriptions; store files that are
  missing, empty, older, truncated, or written concurrently; the listen
  address; and `conch web` as a real process, stopped by SIGTERM.
- Logs hold kinds and sizes only — a test looks for the reply text and the
  token in them.

## Not tested, not built

- **Nothing was run against Tailscale, a real phone, or a real agent.**
  This machine has no Tailscale address (`conch web` refuses, correctly).
  Rows 9.70–9.74 cover it. One thing to watch in 9.70: the socket's
  same-origin check assumes `tailscale serve` passes `Host` through.
- **Push is stored, not sent** (phase 4). `/api/push/key` makes and keeps a
  VAPID key; subscriptions are saved per device.
- The UI is a stub that pairs and lists agents (phase 2). No QR code and no
  TUI key (phase 5), so the help overlay and status bar are unchanged.
- The docs site was not built (`next build`); the page follows the other
  MDX pages.
- No skipped bug tests.

## For the security reviewer

- The pairing code is six digits. What protects it is the 5 minutes, the
  single use and the 5-wrong-guesses rule, not its hash in `phone.json`.
- Behind `tailscale serve` every phone has the proxy's address, so the
  per-address limit collapses into the overall one (20 a minute).
- Any process running as the user can write `phone.json` and pair itself.
  The refusal in an agent's pane (decision 9) is a guard for an agent that
  asks, like the server's scoping — not a lock.
- A `view` device sees every pane's screen (decision 8).

## Conflicts to expect

The `conch-conch-remote-connection-via-webapp` worktree has an uncommitted,
different gateway with its own `cmd/conch/web.go`, `internal/web` and
`web/src/app/docs/phone/page.mdx`. It was not read or reused. Merging both
will conflict on those two paths, `main.go`, `go.mod`, `docs.ts` and the CLI
docs page.

---

# Phase 2: the phone app

Contract: the same `phone-api.md`, `api_version` 1, unchanged. The app uses
only routes and messages that were already there; the gateway gained no
route.

## What was built

`internal/phone/ui/`, embedded as it is — no build step, no dependency:

- `index.html`, `app.css`, `app.mjs` — the app: pairing (a `#code=` link
  fills the code in), the agents list grouped by project and kept live over
  the socket, an agent's page (state, question with its choices as buttons,
  the screen drawn from frames, reply), a new-task form for `full` devices.
- `lib.mjs` — the logic with no page in it: ANSI rows to styled runs,
  ordering, grouping, routes, backoff. `uitest/lib.test.mjs` tests it.
- `sw.js`, `manifest.webmanifest`, icons — installable, and it opens while
  the laptop is away: network first, the cached copy when that fails,
  nothing from `/api` or `/pair` ever cached.
- `ui.go` now serves each file with its own type at its exact address, and
  the page at `/agent/pN` and `/new` — the push payload's `url` is
  `/agent/p3`, so views are paths, not fragments. The CSP names the
  socket's address, since not every browser takes `'self'` to cover `wss:`.

## Decisions

1. **Answer buttons are in.** The plan puts answering in phase 3, but the
   route existed and an agent's page that shows a question you can't answer
   is the wrong half. Phase 3 is left with the terminal view and its key
   bar, and with checking choices against real agents (9.71).
2. **No framework, no xterm.js.** Frames are snapshots, so rows become
   spans. The whole app is under 64 KiB, which a test holds it to.
3. **The agent picker is a fixed list** (claude, codex, gemini, opencode,
   devin, or conch's default): the contract has no route for which are
   installed. Worth a `GET /api/agent-names` in a later contract version.
4. **Offline keeps the list, not the questions**, in `localStorage`; a
   question can quote a command. Revoking or a 401 clears it.
5. **An out-of-date page reloads itself once** when `hello`'s
   `api_version` isn't the one it was written for.

## Tested

- `go test -race -count=1 ./...` passes; `gofmt` and `go vet` clean.
- Go: every file served with its type, the view addresses, nothing served
  by walking out of `ui/`, the CSP, no inline script or style, no address
  outside the gateway, every route and socket message the app uses is one
  the gateway has, the size budget, the manifest.
- node (17 tests, run by `TestUILogic`; **skipped where node is missing** —
  GitHub's runners have it): styling, colours in every form, escapes
  dropped, markup kept as text, ordering, grouping, routes, permissions.
- A real browser: run R38 in `docs/testing/end-to-end.md` — headless
  Chrome at phone size against an isolated conch with a stand-in agent.
  Pair, list, answer by button, reply, offline and back, revoke.

## Not tested, not built

- **No real phone, no Safari, no home-screen install, no Tailscale.** Rows
  9.75 and 9.76. iOS is where this is most likely to differ: the keyboard
  over the reply box, the service worker's lifetime, `100vh`.
- `app.mjs` itself has no unit tests — it is the DOM and the network; only
  `lib.mjs` does. The browser run is what covers it, and it is not
  automated in the suite.
- Starting a task from the form was not submitted in the browser run (the
  route is covered by the gateway's test).
- Terminal view and key bar (phase 3), push (phase 4), QR and TUI key
  (phase 5). The docs site was not built.

---

# Phase 3: the terminal view and key bar

Contract: `phone-api.md`, `api_version` 1, unchanged. The terminal uses the
socket's `frame.open`/`frame.close` and `keys`, which were already there.
Answering (the `answer` route and the buttons) was built in phases 1–2;
Amit confirmed it sends arrow keys and Enter.

## What was built

- **`/agent/pN/terminal`**: the pane's whole screen, every frame. For a
  `full` device, a key bar — esc, tab, shift+tab, ↑ ↓ ← →, ⏎, backspace,
  ^C — and a line whose text is typed as keys, without Enter. Other
  devices see the screen and are told typing needs `full`.
- **^C takes two taps** within two seconds; the first only arms it.
- The agent page links to the terminal, and a question with no readable
  choices says "Answer it in the terminal" to a `full` device.
- `lib.mjs`: `KEYBAR`, `textKeys` (space, tab and new lines by name,
  control characters dropped so a paste can't carry an escape), `chunks`
  (64 keys a message, the gateway's limit), the terminal route.
- **Fixed on the way:** a view opened its frame before the one it replaced
  closed its own, so going from an agent to its terminal (the same pane)
  could close the frame just opened. Views now open frames in `show()`,
  after the old view has left.

## Tested

- `go test -race -count=1 ./...` passes; `gofmt`, `go vet` clean; 20 node
  tests pass, and `node --check` now parses `app.mjs` and `sw.js` too.
- Every key on the bar is a name `pane.ParseKey` accepts (a Go test reads
  them out of `lib.mjs`); the key names `textKeys` produces — `space`,
  `é`, `+` — are typed as they read into a real pane over the socket.
- The terminal address is served; near misses are not.
- Codex's update menu as the detection tests record it is read as two
  choices with the cursor on the first.
- Browser: run R39 in the end-to-end plan.

## Not tested, not built

- **Real agents' question screens.** Answering is proven on stand-ins and
  on the shapes the detection tests record, several of which are cut
  short. No recorded screen of Gemini's, OpenCode's or Devin's questions exists
  here; a menu whose cursor isn't `❯`, `›` or `>` comes with no choices
  and is answered in the terminal.
  Row 9.71 settles it per agent; 9.77 is the terminal on a real phone.
- Scrollback in the terminal (`pane.scroll` isn't in the contract).
- Push (phase 4), QR and TUI key (phase 5).
