# AGENTS.md

Guidance for AI coding agents (and people) working on conch. Read it before
changing code; it is the contract for how work here is done.

## What conch is

conch is a terminal orchestrator for AI coding agents — Claude Code, Codex,
Gemini CLI and OpenCode — written in Go.

- A **server** daemon owns the pseudo-terminals the agents and shells run in,
  so they keep running when the UI closes. It speaks newline-delimited JSON
  over a unix socket (`~/.config/conch/conch.sock`, or `$CONCH_HOME`).
- A **Bubble Tea TUI** shows a tree of machines → projects → branches, agents
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
| `cmd/conch` | The `conch` CLI: TUI launch, `server`, `new/send/read/close`, `project`, `task`, `machine`, `ask`, `update`, hook reports, status line |
| `internal/server` | The daemon: panes, projects/worktrees, sessions, agent hooks and usage, hot reload, local files, agent setup |
| `internal/pane` | A program on a PTY with an emulated screen (charmbracelet/x/vt), key/mouse encoding, detach/adopt for reload |
| `internal/proto` | Wire protocol: messages, methods, events, payload types, capability list, `Version` |
| `internal/client` | Protocol client (calls, notifications, events, handshake) and starting a local server |
| `internal/tui` | The TUI: tree, tabs/splits/scoping, keys, mouse, views (changes, sessions, setup), dialogs, settings, brain bar, updates |
| `internal/remote` | SSH config and commands, remote install, bridging, machine catalog, release downloads, cross builds |
| `internal/adapter` | How to launch each agent: commands, settings/hooks, resume and prompt arguments |
| `internal/detect` | Which agent runs in a pane and its state (working, waiting, done, idle) from hooks, titles and screens |
| `internal/brain` | Model providers (Claude CLI, Anthropic, OpenAI-compatible), planner, action execution, summaries |
| `internal/sessions` | Reading and deleting each agent's saved conversations |
| `internal/usage` | Token usage and plan limits from transcripts and rollouts |
| `internal/agentsetup` | What an agent loads in a checkout: instructions, skills, MCP servers, trust |
| `internal/gitx` / `internal/ghx` | git status, branches, worktrees, untracked files / pull requests via the `gh` CLI |
| `internal/update` | Version comparison, same-build checks, release installs, reloading servers |
| `internal/config` | Paths (`CONCH_HOME`), `config.toml` loading and saving |
| `internal/buildinfo` | Build identity (executable hash) used to detect stale servers and TUIs |
| `web/` | The documentation website (Next.js, MDX under `web/src/app/docs`) |
| `docs/plans` | Design plans for deferred work, and the [roadmap](docs/plans/roadmap.md) |
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
  returned; don't run it.
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
- **Panes on macOS.** `poll` doesn't work on ttys and read deadlines aren't
  supported on ptys; the read loop uses `select`. Shared pane fields are
  guarded by `p.mu`/`p.emuMu` — check with `-race`.
- **Tabs are tmux-like.** A pane is shown in exactly one place; splits and new
  tabs start new shells instead of mirroring; closing a split or tab ends its
  panes after confirming; panes that exit close their split or tab (except
  failures in the first seconds). The tab bar lists the tabs of the group
  selected in the tree (`internal/tui/scope.go`); the machine row lists none
  and shows a preview.
- **Machine-level panes** (under a machine's `CLI` group) are created with
  `NoProject` and start in the home directory.
- **Build identity.** `buildinfo.Build()` hashes the executable at start-up;
  stale-server and stale-TUI detection depend on it.

## Before you finish

- [ ] Tests added or updated for the change, covering edge cases
- [ ] `gofmt -l cmd internal` prints nothing; `go vet ./...` is clean
- [ ] `go test -race -count=1 ./...` passes
- [ ] Help text, CLI usage and web docs updated for user-visible changes
- [ ] Summary says what was tested, what wasn't, and any skipped bug tests
- [ ] Anything fakes can't prove has a row in
      [docs/testing/end-to-end.md](docs/testing/end-to-end.md)
