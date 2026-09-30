# Handoff: `conch web` — the phone gateway (phase 1), app (phase 2), terminal (phase 3), push (phase 4) and pairing by QR code (phase 5)

Branch `conch/build-conch-web-phone-gateway`, five commits on top of `0d25b65`:
the gateway, the app, the terminal, push, pairing by QR code. Not pushed, not
merged. Phases 2–5 are at the end.

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

---

# Phase 4: push notifications

Contract: `phone-api.md`, `api_version` 1, unchanged — the payload is the
contract's `{type, pane, name, project, url}`.

## What was built

- `webpush.go` — Web Push with the standard library only: the payload
  encrypted for the subscribing browser (RFC 8291, aes128gcm; a test
  checks the RFC's own worked example byte for byte), and a VAPID JWT
  signed with the key in `phone.json` (RFC 8292). Headers: `TTL` a day,
  `Urgency` high for waiting and normal for done, `Topic` per pane so an
  undelivered push is replaced by the next.
- `push.go` — a watcher with its own server connection. It learns every
  agent's state from `pane.list` when it connects (at start and after a
  server reload) and queues a push only on a change into waiting, or into
  done. A single sender goes through the subscriptions that asked for that
  kind; 404/410 drops the subscription.
- The app: ⚙ → settings, where notifications are turned on (permission,
  `pushManager.subscribe` with the gateway's key, `POST
  /api/push/subscribe`) and off, with "also when an agent finishes". The
  service worker shows the notification and, on a tap, tells an open app to
  go to the agent or opens a window there.

## Decisions

1. **Pushes go only to known push services** — `fcm.googleapis.com`,
   `updates.push.services.mozilla.com`, `web.push.apple.com`,
   `*.notify.windows.com` — checked when subscribing and again when
   sending, over https on the default port, with redirects not followed.
   Otherwise a `view` device could have the laptop POST anywhere it can
   reach. A browser with another push service can't be subscribed; say if
   that matters.
2. **The VAPID subject is the project's URL**, not a person's address:
   Apple requires one, and there is no one person behind every conch.
3. **`name` is the pane's display name** (the task's title when the agent
   set one), as the TUI labels it — the list uses the pane name.
4. **"Show the question in notifications"** from the contract has no
   field to switch it on, so the question is never sent.
5. **Missed while disconnected:** a change that happens while the watcher
   has no connection (a server reload) is not pushed; it learns the new
   state quietly.

## Tested

- `go test -race -count=1 ./...` passes; `gofmt`, `go vet` clean.
- RFC 8291's example; bad keys and oversized payloads refused; the VAPID
  JWT's claims and its signature verified with the gateway's key.
- Against a loopback TLS push service that decrypts as a browser would
  (`TestPushOnTransitions`): a push within 5 s of an agent starting to
  wait, to each subscription; none for an agent already waiting at start;
  none again while it keeps waiting; done only to the subscription that
  asked; none to a revoked device; a 410 drops that subscription; the
  payload has exactly the four fields; the log has no endpoint.
- Redirects not followed, non-2xx reported, the known-host check (look-alike
  hosts, ports, userinfo, http refused), a full queue dropping rather than
  blocking.
- node: the real `sw.js` run in a sandbox — the notice's words, tag and
  target for each kind of push, a push that tries to aim a tap elsewhere,
  a tap with the app open and closed. `keyBytes`, `appPath`.
- Headless Chrome: the settings page renders and reports "off".

## Not tested

- **No push has gone through a real push service or reached a real
  phone** — that needs the network and a device. Rows 9.78 and 9.79.
- In headless Chrome a push delivered through the debugging port
  (`ServiceWorker.deliverPushMessage`) never reached the worker, so the
  notification was not seen in a real browser either; the handler is only
  covered by the node run of `sw.js`.
- Turning notifications on in a browser (it subscribes with Google's
  service, over the network).

---

# Phase 5: the QR code and the TUI key

Contract: unchanged. The QR code holds `<url>/#code=NNN-NNN`, which the
app has read since phase 2.

## What was built

- `internal/phone/qr.go` — `PairLink` and `QRLines`: the code drawn with
  half blocks, two modules a cell, dark on light whatever the terminal's
  colours, with a two-module margin. Encoding is `rsc.io/qr` (new
  dependency, BSD, no dependencies of its own, from the module cache).
- `conch web pair` prints the QR code above the code, when stdout is a
  terminal (`charmbracelet/x/term`, already in the module graph, now
  direct).
- `conch web -url URL` — the address phones open, recorded for pairing,
  for the `https://…ts.net` name `tailscale serve` gives. Without it the
  listener's own address is recorded, as before.
- The TUI: **`P`** in the tree opens "Pair a phone with this computer" —
  a fresh `reply` code, its QR code, the address; `v` / `r` / `f` issue a
  new code with that permission, `y` copies the link, any other key or a
  click outside closes. It is in the help overlay, and `P phone` ends the
  local machine row's status hints (not a remote's: it pairs this
  computer's gateway) — last, so a narrow bar drops it before `? keys`,
  which a test caught it pushing off.
- Docs: the phone page, the keys page, the CLI page; e2e row 9.80.

## Decisions

1. **Margin of two modules**, not the standard's four: a terminal has few
   rows, and phone cameras read two. If 9.80 finds a phone that can't,
   `qrQuiet` is the one number to change.
2. **The QR code shows only when all of it fits**, box and words included;
   otherwise the code and address alone, and a line saying how big a
   terminal it needs. 80×24 is enough (tested).
3. **`P` issues a code as it opens**, as `conch web pair` does: the dialog
   is the pairing, not a preview of it.

## Tested

- `go test -race -count=1 ./...` passes; `gofmt`, `go vet` clean.
- The drawn code read back cell by cell equals the encoder's modules, for
  a short, a typical and a long link; too much text is refused.
- `conch web pair`: QR code only on a terminal, and it is the link to the
  code printed under it; `-url` checked and recorded (by a real
  `conch web` process in the test).
- TUI: `P` opens it with a code that pairs; `v`/`r`/`f` replace the code;
  `y` copies the link; other keys and clicks outside close it; ticks
  don't; sizes 1×1 to 200×60 stay on screen, with the QR code shown whole
  or not at all; no gateway on record, and an unreadable `phone.json`;
  the help and the hints.

## Not tested

- **No camera has scanned it** (row 9.80). There is no QR decoder here,
  and headless Chrome has no barcode detector.
- The whole phone path end to end — pair by QR over Tailscale, a push
  arriving, answering from the lock screen — is rows 9.70–9.80, all still
  to do on real devices.

---

# Fix after phase 5: pairing over plain http

Found by Amit: pairing at `http://100.101.102.103:8722` "worked" four times and
every request after it came back `unauthorized`. The device key is a
`Secure` cookie, which a browser drops on a plain-http page, so each
pairing made a device (four stray `Browser` devices in `phone.json`)
that could never get in.

- `/pair` from a page on plain http, other than loopback or `localhost`,
  is refused (`bad_request`, "pairing needs HTTPS…") before the code is
  spent, so no stray device is made.
- `conch web -url` now also tells the gateway that address is its own
  (`Gateway.AllowOrigin`): behind `tailscale serve` the page's origin is
  the `.ts.net` name while the Host the gateway sees may be its own
  address, which the same-origin checks on `/pair`, changes and the
  socket would otherwise refuse.
- Without `-url` or a certificate, `conch web` prints the two commands to
  run, with the tailnet name from `tailscale status --json`
  (`CONCH_TAILSCALE` names the program; tests use a fake).
- The app says so before a code is spent on an insecure page, and says
  when pairing succeeded but the browser still didn't keep the cookie.
- Tested: `TestPairingNeedsHTTPS`, `TestWebSaysHowToGetHTTPS`,
  `TestTailnetNameWithoutTailscale`; and in headless Chrome at
  `http://100.101.102.103:18723` (an isolated conch): the button is disabled
  with the reason, a direct POST gets 400, no device is made.

Then, on Amit's Mac: `tailscale serve --bg http://100.101.102.103:8722` answered
502 / hung. The Tailscale app can't dial the machine's own tailnet address
(`tailscale nc 100.101.102.103 8722`: "connection refused" while the gateway
listened there). So with `-url`, `conch web` now listens on
`127.0.0.1:<port>` unless `-listen` says otherwise, and prints the
`tailscale serve --bg http://127.0.0.1:<port>` to put in front; without
`-url` it still listens on the Tailscale address. Tested by
`TestWebWithURLListensOnLoopback`; not yet confirmed through the real
`tailscale serve`.

---

# The app, rebuilt after the Claude app

Asked for by Amit after the first redesign: "review other webui or mobile
app ui and build best ui … direct terminal access, all functionality, ui
similar like claude mobile app". A research pass (a subagent; the Claude
app, Happy's source, Blink and Termius docs) gave the palette (#FAF9F5 /
#262624, coral #C96442 / #D97757), fonts (a system serif for headings,
monospace for terminals), the drawer, the composer, inline approval, the
key row with sticky modifiers.

- **Contract additions** (phone-api.md, "Additions — 2026-09-30", with a
  changelog; additive, `api_version` 1): Pane (any pane, with `kind` and
  `cwd`), `GET/POST /api/panes`, `/api/close`, `/api/rename`; socket
  `panes.watch` (`panes`, `pane.changed`, `pane.gone`) and `text`.
  Starting, renaming, closing and typing need `full`; a name shaped like
  a pane ID is refused (pane.create doesn't). Tested in `panes_test.go`
  and by the permission tests, which walk the route table.
- **The app** (`ui/`): an inbox that answers questions in place; a drawer
  of every agent and terminal by project; an agent as a conversation with
  a composer whose send turns into stop (Esc) while it works; its terminal
  typed into directly — a hidden field holds a zero-width sentinel so a
  backspace on an empty field still reports, text goes as `text`, special
  keys as `keys`, ctrl/alt sticky (once, locked), the key row placed on
  the keyboard with `visualViewport`; ⋯ for terminal/rename/close; ＋ for
  task, agent or terminal. `lib.mjs` gained `sortPanes`, `upsertPane`,
  `keyFromEvent`, `withMods`, `tapModifier`, `usedModifier`,
  `TERMINAL_KEYS`, `kids`, tested in node; the old key bar and
  `textKeys` went. Size budget raised from 64 to 96 KiB (78 KB now).
- Browser run R41; real-device rows 9.81 (typing) and the earlier ones.

After Amit's first runs on his iPhone:

- **Pairing defaults to `full`** (`conch web pair`, `P` in the TUI) — his
  decision, after two pairings as `reply` that couldn't type. phone.md
  says `reply`; `-permission reply|view` and `r` / `v` still give less.
  `conch web permission ID …` changes a paired device.
- **The terminal takes the visible area** (above the keyboard), key row
  under it, following the prompt.
- **Scrollback**: socket `scroll {pane, offset}` (view; phone-api.md
  changelog), `pane.scroll` on the socket's own connection.
  `lib.mjs` `scrollback`/`olderOffset` stitch pages above the live rows;
  `paneScreen` in `app.mjs` is the one screen both the chat and the
  terminal use. Runs R42 and R43.
