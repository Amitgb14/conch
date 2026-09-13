# conch

A terminal orchestrator for AI coding agents. A background server owns the
terminals your agents run in, so they keep working when you close the UI. The
TUI organises everything as a tree — machines, projects, git branches, agents
and terminals — beside a live view of whatever you select.

Status: local and remote machines (over SSH) with projects, branches,
worktrees, pull requests and Claude Code state tracking. The Claude "brain"
comes next.

## Build

```sh
go build -o bin/conch ./cmd/conch
```

## The sidebar

```
MACHINES
▾ ● local                      ⠋1 ⚑1
  ▾ ◆ api                      ⠋1 ⚑1
    ▾ Branches                     7
        ● main                    +1        checked out in the repo, uncommitted lines
        ◇ conch/add-health-check  ⠋ ↑3      own worktree, agent working, 3 ahead of main
        · feat/login          #12✓  ↑2      not checked out, pull request with passing checks
        … 18 more
    ▾ Agents                       2
        ⠋ Add health check   conch/add-health-check
        ! Fix flaky tests    fix/flaky
    ▾ Terminals                    1
        › dev-server         main
```

- **Projects** appear when you open a pane inside a git repository, or add
  them with `a`, which opens a folder browser on the selected machine (local
  or remote): `enter` opens a folder, `←` goes up, `a` adds the selected
  folder, `.` adds the folder you're in, `/` filters, `g` jumps to a typed
  path, `n` creates a folder and `p` creates a new project (a folder with a
  git repository and an initial commit). Git repositories show `◆ git`,
  existing projects `◆ project`. From the CLI: `conch project add PATH` or
  `conch project create [-no-git] PATH`. Linked worktrees belong to their
  main repository. The list is kept in `~/.config/conch/projects.json`.
- **Branches** list checked-out branches, the base branch, branches with
  panes and recent ones; the rest fold into `… N more`. Each shows uncommitted
  changes (`+42 −7`, `⚠` conflicts), commits ahead/behind the base (`↑2 ↓1`),
  and the state of agents working on it. Git state refreshes when git
  metadata changes, after an agent uses a tool, and every few seconds.
- **Pull requests** come from the `gh` CLI (your existing GitHub login):
  `#12✓` checks passing, `#13✗` failing, `#14●` running, purple when merged.
  `o` opens the branch's PR. They refresh every minute while agents work in
  the project, every five minutes otherwise, and on `R`.
- **Agents** are named after their task (Claude's title) until you rename
  them with `r`.
- **Fold state** and sidebar width are remembered in `~/.config/conch/ui.json`.

Selecting a branch shows its **changes**: its pull request (checks, review),
uncommitted files (or files changed since the base when it isn't checked
out), commits ahead, and diffs.

## Remote machines

Each machine runs its own conch server, which owns that machine's panes and
agents, so work continues there when your laptop sleeps or the network drops.
The TUI connects to all of them at once over your normal `ssh`:

```sh
conch machine add gpu-box              # an alias from ~/.ssh/config, user@host or ssh://user@host:port
conch machine ls
conch -m gpu-box status                # any command against a machine
conch -m gpu-box task -cwd ~/src/api "Fix the flaky tests"
```

Or press `M` in the TUI. Adding a machine:

1. **Probes** it over ssh (OS, CPU, an installed conch).
2. **Installs** conch to `~/.local/bin/conch` there if it is missing or older
   than your client — `machine add` asks first. For another platform (say a
   Linux server from a Mac) conch cross-compiles itself from its source tree
   (found next to the binary, or `CONCH_SOURCE`) with your Go toolchain, and
   caches the result in `~/.config/conch/binaries/<os>-<arch>/` until you
   rebuild conch. Without source or Go, put a binary there or point
   `CONCH_REMOTE_BINARY` at one.
3. **Connects** through `conch bridge`, which starts the server there if needed.

conch runs the system `ssh` with a generated config that includes yours first
(so your settings win) and adds keepalives and connection sharing. It stores
no secrets; `machines.json` holds only an ID, label and ssh target. Background
connections never prompt: if ssh needs a password or host-key answer, run
`conch machine add` in a terminal once, or `ssh-add` your key.

In the sidebar each machine shows its state: `●` online, `⠋` connecting,
`○ offline` (its last-seen panes stay visible, dimmed, while conch retries
with backoff), `! setup` (conch needs installing or upgrading — `m` → Install),
`outdated` (the server there predates your client — `m` → Restart server,
which stops its panes). `R` on a machine reconnects now; `x` removes it.

### Claude Code on a machine

conch checks whether `claude` is installed on each machine (the machine view
shows its version). If it isn't, `c` offers to install it, or press `C` (also
in the machine menu, or `conch -m NAME agent install claude`). conch runs
Anthropic's official installer (`curl -fsSL https://claude.ai/install.sh | bash`)
in a pane you can watch; it installs `~/.local/bin/claude` for your user, no
root. Then `c` starts Claude there: on first run it asks you to log in — open
the link it shows on any computer and paste the code back. conch never copies
credentials between machines; for unattended machines Claude also accepts
`ANTHROPIC_API_KEY` or a `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`.
Alpine needs `apk add bash curl libgcc libstdc++ ripgrep` (as root) first.

## Scrollback and copying

The wheel over a pane scrolls back through its history (10,000 lines); the
view stays on the same text while output continues, and typing returns to the
live screen. `ctrl+b [` (or `ctrl+b pgup`) scrolls from the keyboard: `↑↓`,
`pgup`/`pgdn`, `g` for the oldest line, any other key for live.

Drag across a pane to select and copy; double-click copies a word. Copies use
OSC 52 (works over SSH) plus `pbcopy`/`wl-copy`/`xclip` locally. `y` copies a
branch name or directory in the tree, a file path or the whole diff in the
changes view. Programs that use the mouse themselves (vim, htop) still get the
mouse; full-screen programs get arrow keys from the wheel.

## Tasks

`t` on a project (or `conch task "PROMPT"`) creates a branch named from the
prompt, checks it out into `<repo>.worktrees/<branch>`, and starts Claude with
the prompt — every task isolated from the others. `c`/`n` on a branch that
isn't checked out creates its worktree first. `x` on a branch removes its
worktree (never with uncommitted changes, never the main one).

## Keys

| Where | Keys |
|-------|------|
| Tree  | `↑↓` `jk` move · `←→` `hl` fold · `space` toggle · `enter` open pane / view changes · `/` filter · `!` next agent waiting for you · `o` open PR · `y` copy · `m` menu · `?` all keys · `q` detach |
| Create | `t` task · `c` Claude here · `C` install Claude Code · `n` terminal here · `a` add project · `M` add machine · `r` rename · `x` close / remove · `R` refresh git and PRs, or reconnect a machine |
| Pane  | everything goes to the program · `ctrl+b [` scroll history · `ctrl+b` then any key → tree · `ctrl+b z` zoom · `ctrl+b !` next waiting |
| Changes | `↑↓` file · `enter` diff · `o` open PR · `y` copy path / diff · `esc` back |

**Mouse:** click to select, click a selected folder (or its arrow) to fold,
double-click to open, right-click for a context menu, wheel to scroll, drag
the sidebar's edge to resize, click `⚑ N waiting` to jump. Clicks inside a
pane go to programs that use the mouse. Hold shift (option in iTerm2) to
select text with your terminal, or set `[ui] mouse = false`.

## Agent states

| State | Meaning |
|-------|---------|
| `⠋` working | the agent is busy |
| `!` waiting | blocked on you: a permission, trust or other question |
| `✓` done    | finished while you were looking elsewhere |
| `○` idle    | waiting for a prompt |

A desktop notification fires when an agent you aren't viewing needs you.
How the state is decided, most reliable first:

1. **Hooks.** Claude panes started by conch get
   `--settings ~/.config/conch/claude-settings.json`, adding hooks that call
   `conch report claude-hook`. Your own settings files are untouched and their
   hooks still run.
2. **Screen rules.** Regexes over the bottom of the screen (tolerant of line
   wrapping), e.g. "Do you want to proceed?" → waiting. Also covers `claude`
   started by hand. Override `internal/detect/manifests/claude.toml` by copying
   it to `~/.config/conch/agents/claude.toml`.
3. **Foreground process.** A pane is only an agent while the agent is the
   terminal's foreground process.

`conch agent explain p1` shows the evidence behind a state.

## Scripting

```sh
conch task -cwd ~/src/api "Add a health check endpoint"   # pane, worktree, branch
conch new -agent claude -cwd ~/src/api                   # Claude in the repo
conch send p1 "fix the failing tests" && conch send -keys p1 enter
conch read p1                          # visible screen as text
conch status                           # panes with agent state
conch project ls                       # projects, base, worktrees
conch close p1
conch server stop                      # stops the server and all panes
```

## Layout

```
cmd/conch          CLI entry point and subcommands
internal/proto     NDJSON protocol: requests, responses, events, handshake
internal/server    daemon: panes, projects and git refresh, unix socket
internal/gitx      git plumbing: worktrees, branches, status, changes, diffs
internal/ghx       pull requests via the gh CLI
internal/remote    ssh machines: probe, install, bridge, machine catalog
internal/buildinfo build identity for comparing binaries across machines
internal/pane      PTY + VT emulator per pane, keys, mouse, title scanning
internal/detect    agent detection: foreground process, screen manifests, state tracker
internal/adapter   agent launchers (Claude Code with hooks), title cleanup
internal/client    socket client, server auto-start
internal/tui       Bubble Tea UI: tree, changes view, overlays, mouse
internal/config    paths and config.toml
```

## Files

Everything lives in `~/.config/conch` (override with `CONCH_HOME`):
`conch.sock` (override with `CONCH_SOCKET`), `server.log`, `projects.json`,
`claude-settings.json`, `ui.json` (fold state), `machines.json`, `ssh/config`
(generated), `binaries/<os>-<arch>/conch` (for other platforms), `config.toml`:

```toml
[keys]
prefix = "ctrl+b"

[pane]
default_command = ""   # empty = $SHELL

[notify]
desktop = true   # macOS notification / notify-send
bell = false

[ui]
mouse = true
accent = "teal"  # teal, blue, green, orange, pink, red, gray, purple or "#rrggbb"
```

## Protocol

One JSON object per line. Requests `{"id","method","params"}` get
`{"id","result"}` or `{"id","error":{"code","message"}}`; requests without an
`id` are notifications. The server pushes `{"event","data"}`: `pane.created`,
`pane.exited`, `pane.closed`, `pane.updated`, `pane.frame` (subscribed panes),
`project.updated`, `project.removed`.

Over SSH the same stream runs through `conch bridge`. `hello` returns the
server's version, build, platform, hostname, home and capabilities; clients use
capabilities to detect servers from older builds.

Methods: `hello`, `ping`, `server.stop`; `pane.list`, `pane.create`,
`pane.close`, `pane.resize`, `pane.send_text`, `pane.send_keys`,
`pane.send_mouse`, `pane.scroll`, `pane.read`, `pane.rename`, `pane.subscribe`,
`pane.unsubscribe`, `pane.mark_seen`; `agent.report`, `agent.explain`;
`project.list`, `project.add`, `project.remove`, `project.refresh`,
`project.changes`, `project.diff`, `project.create`; `fs.list`, `fs.mkdir`; `agent.status`, `agent.install`; `worktree.add`, `worktree.remove`;
`task.create`.
