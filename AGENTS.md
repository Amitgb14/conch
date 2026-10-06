# AGENTS.md

Guidance for AI coding agents (and people) working on conch. Read it before
changing code; it is the contract for how work here is done.

## What conch is

conch is a terminal orchestrator for AI coding agents — Claude Code, Codex,
Gemini CLI, OpenCode and Devin — written in Go.

- A **server** daemon owns the pseudo-terminals the agents and shells run in,
  so they keep running when the UI closes. It speaks newline-delimited JSON
  over a unix socket (`~/.config/conch/conch.sock`, or `$CONCH_HOME`).
- A **Bubble Tea TUI** shows a tree of machines → Workspace (projects) and CLI → branches, agents
  and terminals, beside tabs and splits that behave like tmux windows.
- **Remote machines** are reached over SSH: conch installs itself there and
  bridges the remote server's socket.
- The **brain** turns plain-language requests into proposed actions using a
  pluggable model provider; nothing runs until the user confirms.
- The server **hot-reloads** onto a new build by exec'ing itself, keeping
  PTYs open; the TUI detects new builds and updates itself.

Module: `github.com/Amitgb14/conch` · Go version: see `go.mod` · License:
Apache-2.0 · Default branch: `master`.

## Repository map

| Path | What lives there |
| --- | --- |
| `cmd/conch` | The `conch` CLI: TUI launch, `server`, `new/send/read/close`, `project`, `task`, `branch`, `worktree`, `machine`, `ask`, `web`, `mcp` (an MCP server over stdio in front of the same socket — `mcp.go` is the transport, `mcptools.go` the tools, and `await.go` the wait both it and the commands share), `update`, hook reports, status line |
| `internal/server` | The daemon: panes, projects/worktrees, sessions, agent hooks and usage, hot reload, local files, agent setup, worktree watching |
| `internal/pane` | A program on a PTY with an emulated screen (charmbracelet/x/vt), key/mouse encoding, detach/adopt for reload |
| `internal/proto` | Wire protocol: messages, methods, events, payload types, capability list, `Version` |
| `internal/client` | Protocol client (calls, notifications, events, handshake) and starting a local server |
| `internal/tui` | The TUI: tree, tabs/splits/scoping, keys, mouse, views (changes, sessions, setup), dialogs, settings, brain bar, updates |
| `internal/remote` | How a machine is reached (`Transport`: ssh, a sandbox through its provider's ssh gateway with a fresh token, or a local command), ssh config and commands, remote install, bridging, machine catalog, release downloads, cross builds |
| `internal/sandbox` | Hosted sandbox providers (Daytona, boat.dev): create, start, stop, delete, list conch's own, ssh access — a fresh token, or a key of your own authorized in the sandbox |
| `internal/phone` | The gateway `conch web` runs for a phone: HTTP and a WebSocket in front of every machine's server (a client of each, as the TUI is — `machines.go` keeps one per machine, reached in the background so a phone never waits on ssh, and a pane is addressed `machine:pane`), pairing codes, per-device tokens kept hashed in `phone.json`, `view`/`reply`/`full` checked on every route and socket message — and *where*, since the gateway reaches every machine with the person's credentials: a device reaches this computer and the machines named for it (`Device.Machines`, `Reaches`), which is why `machinesFor`, `machineFor`, `paneOn`, `everyAgent`, `everyPane` and the socket's own `paneOn` all take the device, a machine it was not given is left out of every list rather than merely refused, pushes about one are not sent, and an older `phone.json` loads as this computer alone, reading a waiting agent's choices off its screen, and the phone's app (`ui/`: plain HTML, CSS and ES modules embedded as they are, no build step; its logic in `ui/lib.mjs`, tested with node from `uitest/` through `TestUILogic`, which skips without node). Its shapes are a contract (`api.go`) a test pins |
| `internal/adapter` | How to launch each agent: commands, settings/hooks, resume and prompt arguments |
| `internal/detect` | Which agent runs in a pane and its state (working, waiting, done, idle) from hooks, titles and screens |
| `internal/brain` | Model providers (Claude CLI, Anthropic, OpenAI-compatible), planner, action execution, summaries |
| `internal/sessions` | Reading and deleting each agent's saved conversations |
| `internal/usage` | Token usage and plan limits from transcripts and rollouts |
| `internal/agentsetup` | What an agent loads in a checkout: instructions, skills, MCP servers, trust — and, the other way, writing one agent's instructions, skills and servers where the others look for them (`sync.go`), with a record to undo it; the library of servers and skills the agents follow (`library.go`); each agent's way of naming a variable (`vars.go`); conch's own skill for agents driving agents (`skill/SKILL.md`, installed by `skill.go`) — its commands are checked against the CLI's usage by a test |
| `internal/gitx` / `internal/ghx` | git status, branches, worktrees, untracked files / pull requests via the `gh` CLI |
| `internal/update` | Version comparison, same-build checks, release installs, reloading servers |
| `internal/config` | Paths (`CONCH_HOME`), `config.toml` loading and saving |
| `internal/buildinfo` | Build identity (executable hash) used to detect stale servers and TUIs |
| `internal/report` | What `conch bug` and the TUI's *Report a problem…* hand over: versions, counts, sizes, a server's missing capabilities and conch's own last error, rendered for a GitHub issue. **Facts, never contents** — no path, project, branch, pane title or screen — which is what makes one keystroke safe; it sends nothing itself |
| `internal/tools` | Programs for driving conch under test: `clicker` clicks and drags a pane through the protocol, `standin` is a pane conch detects as an agent (by the name it is built under) that starts panes as an agent does |
| `web/` | The documentation website (Next.js, MDX under `web/src/app/docs`) |
| `docs/plans` | Design plans for deferred work. The roadmap and the gap plan are kept out of the repository (see `.gitignore`); ask for them rather than looking for them here |
| `docs/testing` | The [end-to-end plan](docs/testing/end-to-end.md): paths the automated tests only cover with fakes |

## Build, run, test

```sh
make build                     # bin/conch
make test                      # go test -race ./...
make vet                       # go vet ./...
gofmt -l cmd internal          # must print nothing
go test -race -count=1 ./...   # what to run before every commit
go test -coverprofile=/tmp/c.out ./... && go tool cover -func=/tmp/c.out | tail -1
```

CI (`.github/workflows/ci.yml`) runs `go vet` and `go test -race` on **Linux
and macOS**. Code and tests must work on both.

## Working rules

1. **Every change comes with tests — including small ones.** A bug fix gets a
   test that fails without the fix. A new feature gets tests for its normal
   use *and* its edge cases. A refactor keeps or improves coverage. Do not
   commit a change whose tests you haven't run.
2. **Cover edge cases on purpose.** For each change, think through and test:
   - empty, nil and zero values; missing files and directories; malformed or
     truncated input (bad JSON lines, overlong lines, partial writes)
   - boundaries: first/last item, lists of one, exact limits, off-by-one
   - errors from every dependency: failed calls, timeouts, cancelled contexts,
     closed connections, an old server lacking a capability
   - concurrency: run with `-race`; think about events arriving mid-operation
   - terminal UI sizes: tiny (1×1, 20×5), narrow (< 40 columns) and large;
     rendered lines must never exceed the width
   - platform differences between macOS and Linux (paths, tools, pty
     behaviour)
   - state that persists: saved files from older versions must still load
3. **Keep the full suite green.** Run `go test -race -count=1 ./...` (not just
   the package you touched), `go vet ./...` and `gofmt`. A flaky test is a bug:
   fix it, don't retry it.
4. **Found a bug you're not fixing now?** Write the test for the correct
   behaviour and mark it `t.Skip("bug: <what and where>")` so it stays
   visible; say so in your summary.
5. **Match the surrounding code.** Short, plain comments that explain *why*;
   names and idioms like the neighbouring code; no speculative abstractions.
6. **User-visible changes update their docs** in the same change: the help
   overlay (`internal/tui/overlay.go` `helpText`), status bar hints, CLI usage
   in `cmd/conch/main.go`, and the pages under `web/src/app/docs` (notably
   `interface/page.mdx` and `keys/page.mdx`).
7. **Commit only when asked**, with a message that says what changed and why.
8. **Nothing is pushed red.** Every new change — a feature, a fix, a doc
   tweak that touches code — carries its own tests, and before any push the
   whole regression suite must be green on the final state of the branch:
   `go test -race -count=1 ./...`, `go vet ./...`, and `gofmt -l cmd internal`
   printing nothing. Re-run it after the *last* edit, not just after the
   big one: a one-line follow-up fix checked only in its own package is not
   verified. If something fails, fix it before pushing; never push with a
   failing, skipped-to-pass or retried-until-green test.

## Test isolation — required

Tests must never touch the developer's real conch, agents, machines or
network. The person running the tests may be *inside* a live conch session.

- **Config and home:** use `t.TempDir()` and
  `t.Setenv("HOME", …)`, `t.Setenv("CONCH_HOME", …)`. Never read or write
  `~/.config/conch`, `~/.claude`, `~/.codex`, `~/.gemini` or
  `~/.local/share/opencode`. TUI models in tests keep `statePath` empty so
  `ui.json` is never written.
- **Inherited session variables:** `t.Setenv("CONCH_SOCKET", "")` and
  `t.Setenv("CONCH_PANE_ID", "")` in anything that could connect. When running
  a conch binary by hand for testing, use
  `env -u CONCH_SOCKET -u CONCH_PANE_ID` and a separate `CONCH_HOME` — an
  inherited socket once sent test keystrokes into a real session.
- **Never stop, reload or send to the user's running server.**
- **No real agents or model calls.** Don't run `claude`, `codex`, `gemini` or
  `opencode`, and don't spend API usage. Use fake scripts on a temporary
  `PATH`, a fake `SHELL`, or `/bin/sh` commands; model providers go through
  `httptest` servers.
- **No network or SSH.** Use the hooks: `CONCH_SSH` (fake ssh script),
  `CONCH_SSH_CONFIG`, `CONCH_RELEASE_URL` (httptest), `CONCH_REMOTE_BINARY`,
  `CONCH_SOURCE`, `CONCH_GH` (fake gh).
- **Unix sockets:** macOS limits socket paths to ~104 bytes; make socket dirs
  with `os.MkdirTemp("", "x")`, not deep `t.TempDir()` paths.
- **Waiting on terminals:** poll with a timeout for a marker that only the
  program's *output* contains — never text that also appears in the typed
  command line (that produced a flaky test). Avoid fixed sleeps.
- **Clipboard, browser, notifications, sounds:** assert that a command is
  returned; don't run it. In `internal/tui` the reaching-out itself is
  behind variables — `putClipboard`, `openInBrowser`, `runOutward`,
  `startOutward`, `ringBell`, `setPointerShape` — and `TestMain` replaces
  all of them, because
  a test that ran a copy once put its own fixture on the developer's
  clipboard: the OSC 52 escape conch prints was honoured by the terminal
  running `go test`. Keep new outward effects behind a variable too, and
  set it in `TestMain` rather than trusting every test to remember.
- **git:** only in temporary repositories, with an explicit identity.

Reuse the existing helpers before writing new ones — e.g. `startServer`
(server tests), `startShell`/`waitScreen` (pane tests), `splitModel`,
`prefixed`, `a1Fixture`, `a1FakeClient` (TUI), the fake-server and fake-ssh
helpers in `cmd/conch` and `internal/remote`.

## Architecture notes that bite

- **Protocol compatibility.** Clients and servers of different builds talk to
  each other (a TUI may reach an older remote server). A new method or
  behaviour gets a capability string in `proto.Capabilities`; clients check
  `MissingCapabilities` before relying on it and degrade with a clear message.
  New payload fields are `omitempty` and optional.
- **Hot reload.** The server `syscall.Exec`s itself; PTY and listener fds
  survive via `KeepOnExec`, and pane state passes through a reload-state file
  (`CONCH_RELOAD_STATE`). Anything added to a pane or server entry that must
  survive a reload has to be serialized there, and older state files must
  still load.
  **`syscall.Exec` can hang on macOS.** `runtime_BeforeExec` waits there for
  every pending async preemption signal to be taken, and in a process with
  many threads — a pane each, clients, a CoreFoundation thread per FSEvents
  stream — one is not, so the exec never happens: the server stops serving
  (its listener is already handed over) but never comes back, panes still
  running. Seen once on a real server, and reproducible with
  `go test -race -count=2 ./internal/server/`, which hangs in
  `TestA5ReloadExecFailureCarriesOn` and passes under
  `GODEBUG=asyncpreemptoff=1` (Go issue #41702). So `conch server` starts
  itself again once on macOS with that set (`withoutAsyncPreemption` in
  `cmd/conch/main.go`), while it still has nothing to preempt — the one exec
  that is safe to make. A client that meets a wedged server clears the socket
  and starts a fresh one (`client.EnsureServer`), so conch always starts
  again, but that server's panes are lost.
- **Scrollback on the alternate screen.** A program that takes the whole
  screen — an agent's interface, vim, less — leaves no scrollback: the
  screen is repainted, nothing scrolls off, and the emulator keeps none.
  `internal/pane/altscroll.go` watches the screen between writes and keeps
  the rows that moved off the top, so `ctrl+b [` and selection reach what an
  agent said a page ago. It is recognition, not recording: output goes into
  the emulator half a screen at a time so a burst cannot scroll a screenful
  past unseen, and a program that repaints rather than scrolls leaves
  nothing behind, which is right — none of it scrolled away. An agent's
  interface scrolls only the conversation, above a prompt and status line
  that stay put, so each finished frame — where a synchronized update
  (mode 2026) ends, or at the end of a write — is also compared with the
  last for a scroll in part of the screen (`scrolledRegion`); lines the
  agent brings back by scrolling its own view are not kept twice. The lines are
  kept as text, capped at `altHistoryMax`, dropped when the program leaves
  the alternate screen, and carried through a reload in
  `Snapshot.AltHistory`: text read off the screen, which no replay could
  rebuild. A pane reloaded on the alternate screen also replays the main
  screen's history first, or it would be lost — both once were, at every
  reload, for every agent with a full-screen interface.
- **History is text, not cells.** The emulator stores every character as a
  cell of over a hundred bytes, so ten thousand lines of scrollback cost
  ~145 MB a pane. After each piece of output, `internal/pane/history.go`
  renders what scrolled off the main screen to text (colours and links as
  escape codes) into `p.hist` and clears the emulator's own scrollback;
  frames, search and the reload replay read `p.hist`. The emulator also
  keeps an unreadable scrollback for the alternate screen, which an agent's
  full-screen interface filled just as fast; `dropAltScrollback` turns it
  off by reflection, since the emulator gives no way in — after upgrading
  `charmbracelet/x/vt`, `TestAltScreenKeepsNoCells` and `TestHistoryMemory`
  say whether that still holds.
- **One owner per pty.** `Detach` hands the *same* `*os.File` to the caller,
  so for a moment two panes hold one descriptor: the one that gave it away
  and the one that adopted it, each with its own `mu`. The pane that gave it
  away marks itself `handedOver` and no longer closes the terminal when its
  program ends (`Resume` takes it back) — otherwise it closes a descriptor
  the adopted pane is using, which the race detector saw intermittently as
  `wait`'s `Close` against `Adopt`'s `Fd`. Anything new that writes to
  `p.ptmx` outside the read loop has to answer the same question: whose is
  it now?
- **Compression belongs to a connection, not to a session on it.** Asking
  for it per command does nothing here: conch shares connections
  (`ControlMaster auto`), so a bridge run with `ssh -C` multiplexes onto
  whatever master a probe or an install opened first and ssh ignores the
  flag — `-v` says `auto-mux: Trying existing master` and never names a
  compression method. That shipped once and did nothing. So it is asked
  for where the master is made, in the config conch generates
  (`Compression yes`, `sshConfigs` in `internal/remote/ssh.go`), which
  means it is per machine and settled when that machine's first
  connection is, and `Compress()` is read while the config is written —
  a setting changed now is for the next conch, not this one. And not even
  reliably then: a master outlives a reload, because a bridge holds a
  session on it for as long as the machine is up so `ControlPersist`
  never retires it, and the restarted TUI's bridge simply joins the old
  connection with the old setting. Seen on a real machine right after the
  fix went in (R49). Only closing it (`ssh -O exit`) makes conch open a
  new one; `R` Reconnect on its own usually rejoins the same master. Worth it
  because those connections carry frames: a screenful of styled text,
  12 KB deflating to under 1 KB, 4.3 MB of real traffic crossing a LAN
  as 54 KB and arriving sooner. The local socket never gets it — there
  is no connection to settle it on, and it moves a frame in ~12µs where
  compressing would cost ~26µs, which is why this is an ssh matter and
  not something in `internal/proto`. `[remote] no_compression` turns it
  off for a link as fast as the processor.
- **The pointer's shape is the terminal's to draw.** Hovering a link
  underlines it, which conch does by drawing; the arrow becoming a hand
  is asked for with `OSC 22` (kitty's pointer shape, which Ghostty takes
  too) and nothing else can do it — the mouse protocols carry no such
  thing, and iTerm2 and Terminal.app have no escape for it, so there the
  underline is the whole feature. `internal/tui/pointer.go` remembers the
  shape it asked for, because hover reports every cell the pointer
  crosses and a write per cell for a shape already set is waste; the zero
  value is an arrow, since that is what a terminal shows before conch
  asks. Every way out goes through `quitting` or `releasePointer`: a hand
  asked for by conch outlives conch, over whatever the shell draws next.
- **The mouse goes to the pane under the pointer, not the focused one.**
  A click or a wheel over another split asks for that split's focus, and
  the asking is a command that has not run yet — so for that event the
  focused pane is still the old one. `paneMouse` therefore takes the
  machine and pane it is acting on and reads *that* pane's frame and
  client (`internal/tui/mouse.go`). Deciding by the focused frame sent SGR
  mouse reports to a pane that had never asked for the mouse, which
  printed them into an agent's prompt as `<65;106;43M`, and would have
  sent another machine's pane id down this computer's connection.
  Scrolling conch's own history stays the focused pane's business, since
  its offset and selection are what move.
- **A conch inside a pane never shows that pane.** conch refuses to open a
  TUI in one and says how to do it anyway (`CONCH_PANE_ID= conch`), which
  leaves the case: the TUI resizes every pane it draws to the space it has,
  so showing its own pane shrinks the terminal it is drawing in, which
  shrinks the space, which shrinks the pane — 1×1, with each redraw
  producing the next. Which pane that is comes from the server
  (`pane.caller`, as scoping does), never from `CONCH_PANE_ID`: clearing
  that variable is how somebody got here. `show` refuses it and `syncView`
  skips it (`internal/tui/tabs.go`, `ownPane`).
- **Panes on macOS.** `poll` doesn't work on ttys and read deadlines aren't
  supported on ptys; the read loop uses `select`. Shared pane fields are
  guarded by `p.mu`/`p.emuMu` — check with `-race`.
- **Tabs are tmux-like.** A pane is shown in exactly one place; splits and new
  tabs start new shells instead of mirroring; closing a split or tab ends its
  panes after confirming; panes that exit close their split or tab (except
  failures in the first seconds). The tab bar lists the tabs of the group
  selected in the tree (`internal/tui/scope.go`); the machine row lists none
  and shows a preview.
- **Scoping agents that drive conch.** `internal/server/scope.go` keeps an
  agent calling from inside its pane to its own work: panes it started (a
  lineage, carried through reloads), panes in its project, and that
  project's branches and worktrees. The caller is found from the socket's
  peer pid (`peer_darwin.go`, `peer_linux.go`) walked up its parents to a
  pane's program — never from `CONCH_PANE_ID` — and only a pane with an
  agent detected in it is scoped. A new method that changes a pane or a
  project belongs in `scoped` with a verb, or an agent can reach past its
  scope through it. Tests put a real caller inside a pane by running the
  test binary there (`TestScopeHelper`).
  Across machines the remote server can't see the caller, so `conch -m`
  asks the local one (`pane.caller`) and, for a scoped agent, declares it
  there (`scope.act_for`, `cmd/conch/scope.go`); a connection that acts
  for an agent elsewhere reaches only what that agent started. Declaring
  only narrows, so it is taken at its word; a remote without
  `scope.remote.v1` isn't driven from an agent's pane.
- **Saved ssh hosts** live in `ui.json`, not `machines.json`
  (`internal/tui/ssh.go`, `sshhosts.go`). `saved_ssh` stays the plain list
  of hosts older builds read; a host's name and extra ssh options sit
  beside it in `ssh_hosts` (by host). A host's folder is a `{"host": …}`
  member of an SSH-section folder (`folders.go`), so its sessions follow
  it; the tree settles hosts before pane members. Options are checked by
  `remote.NormalizeLoginArgs` and go before the `--`, so `sshTarget` still
  finds the host as the last word.
- **Machine-level panes** (under a machine's `CLI` group) are created with
  `NoProject` and start in the home directory.
- **Build identity.** `buildinfo.Build()` hashes the executable at start-up;
  stale-server and stale-TUI detection depend on it.
- **Worktree watching.** `internal/server/watch.go` watches every project's
  worktrees with fsnotify and broadcasts `worktree.changed`, so a diff on
  screen follows an agent's edits: git's own metadata does not move when a
  file is written, and rewriting a line leaves its `+`/`−` counts alone.
  Directories git ignores are never watched (one `git ls-files --directory`
  per walk keeps `node_modules` free), `.git` is left to the project ticker,
  and the number of watched directories is capped. A machine that gives no
  watches leaves `s.watcher` nil — every method is nil-safe — and the server
  then *drops* `worktree.watch.v1` from its capabilities, so clients keep
  polling. Never announce a capability the running server cannot honour.
- **Watch backends differ by platform.** `watchBackend` (watch.go) hides
  them: `watch_fsevents.go` (`darwin && cgo`) takes a whole tree per stream,
  `watch_fsnotify.go` (everywhere else) is told about each directory.
  fsnotify's kqueue backend opens a descriptor per watched path — the
  directory *and* every file in it — while Linux's inotify keeps one for the
  lot. Descriptors go out lowest first and `internal/pane.readable` waits on
  a pty with `select`, whose `fd_set` holds 1024: a kqueue watcher holding
  thousands once left a new pane's pty past the set and panicked the whole
  server. So a backend that charges per path is `budgeted()`, keeps to
  `watchMaxFDs` (far under 1024), and a worktree is watched whole or not at
  all. `Changes.Watched` tells the client which it got, so an unwatched
  worktree keeps the fast poll instead of silently going stale.
  **FSEvents needs cgo**, which `scripts/release.sh` and the remote
  cross-builder cannot use across platforms: a macOS binary built without it
  still works, it just watches by kqueue and so mostly polls.
  **FSEvents hands its batches over from a CoreFoundation callback**, so
  whatever reads `EventStream.Events` must keep reading until the stream
  closes it. A callback whose send has no receiver blocks inside cgo on a
  thread locked to it and stays blocked, leaking that thread for the life of
  the process — so the pump drains its stream for as long as the stream
  lives, forwarding only while it is wanted.

## Adding an agent

An agent is supported only when it works everywhere the others do. Adding one
means all of these, each with tests (a fake binary on a scratch `PATH` or
`HOME`, never the real agent):

- **Adapter** (`internal/adapter`): in the `Registry`, with its binary, the
  directory its installer uses, how a first message and a resume are passed,
  and an `InstallScript` so `c` / `conch agent install NAME` can install it.
- **Detection** (`internal/detect/manifests/NAME.toml`): its process names,
  and screen or title rules taken from the real agent — never copied from
  another agent's strings. Leave the state unknown rather than guess.
- **Sessions** (`internal/sessions`): its saved conversations **must** show in
  the project's Sessions view, resume with its own option, and delete with
  `d`. Read its session files when their format is known; when it keeps them
  somewhere undocumented (Devin's database), ask its CLI (`devin list
  --format json`). Find the binary through the `Env` a store is given, not
  this process's `PATH`. A store that runs another program is listed through
  `slowList` (`internal/sessions/slow.go`), so a program that hangs holds up
  the list for a grace and no longer; the list then says it is incomplete and
  the TUI asks again. Say in the docs if search or sharing can't read it.
- **Setup** (`internal/agentsetup`): an inspector for the `i` view, where it
  keeps instructions, skills and MCP servers in a checkout (`writable`) and
  in your home (`userFiles`), how it refers to a variable (`vars.go`), and
  which other agents' files it reads by itself, so sync and the library
  don't give it a second copy.
- **Labels**: `agentLabels` in `internal/tui/model.go` and the handoff labels
  in `internal/sessions/handoff.go`.
- **Docs and plans**: the Supported agents table (`web/src/app/docs/agents`),
  the Sessions page's resume table, the agent lists here and on the home page,
  and a row in `docs/testing/end-to-end.md` for installing, starting, state and
  sessions with the real agent.

Its hooks or plugins are added only once it's confirmed they don't replace the
user's own configuration.

## Before you finish

- [ ] Tests added or updated for the change, covering edge cases
- [ ] `gofmt -l cmd internal` prints nothing; `go vet ./...` is clean
- [ ] `go test -race -count=1 ./...` passes — run again after the last edit, before any push
- [ ] Help text, CLI usage and web docs updated for user-visible changes
- [ ] Summary says what was tested, what wasn't, and any skipped bug tests
- [ ] A new agent's sessions show, resume and delete in the Sessions view
- [ ] Anything fakes can't prove has a row in
      [docs/testing/end-to-end.md](docs/testing/end-to-end.md)
