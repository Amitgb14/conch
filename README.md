# conch

A terminal orchestrator for AI coding agents. A background server owns the
terminals your agents run in, so they keep working when you close the UI. The
TUI organises everything as a tree — machines, projects, git branches, agents
and terminals — beside a live view of whatever you select.

Status: local and remote machines (over SSH) with projects, branches,
worktrees, pull requests and Claude Code state tracking. The Claude "brain"
comes next.

## Install

macOS and Linux (amd64 and arm64):

```sh
curl -fsSL https://raw.githubusercontent.com/Amitgb14/conch/master/install.sh | sh
```

This downloads the latest release, checks it against the release checksums
and installs `~/.local/bin/conch`. `CONCH_VERSION=0.2.0` picks a release and
`CONCH_INSTALL_DIR` another directory. Later, `conch update` replaces the
binary with the newest release (restart the server afterwards with
`conch server stop` — that closes its panes).

With Go 1.25 or newer:

```sh
go install github.com/Amitgb14/conch/cmd/conch@latest
```

### From source

```sh
make build        # bin/conch
make test         # go test -race ./...
make release VERSION=0.2.0   # dist/: archives for every platform + checksums.txt
```

Pushing a `v*` tag runs the release workflow, which builds the same archives
and publishes them as a GitHub release.

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

## Settings

Press `,` or click `⚙` at the right end of the status bar.

- **Theme** — colour schemes: Conch, Dracula, Catppuccin Mocha, Nord,
  Gruvbox Dark, Tokyo Night (applied instantly). Below them, an
  **Oh My Zsh prompt theme** for new zsh terminals, picked from the themes
  installed on this computer. conch starts zsh with a wrapper that loads your
  own `.zshenv`/`.zprofile`/`.zshrc`/`.zlogin` first and then switches the
  theme for that terminal only (`omz theme use`), so your dotfiles are never
  edited; "Keep my .zshrc theme" turns it off.
- **Notifications** — master switch; desktop notification, system sound,
  terminal beep; notify when an agent is waiting and/or when one finishes;
  send a test.
- **Agents** — every machine with each supported agent, installed (with
  version) or not; `enter` on a missing one installs it there.

Changes are saved to `~/.config/conch/config.toml`.

## Splits and tabs

The main area has tabs (click them, `+` adds one, `×` closes), and each tab
can be split into several views: panes, branch changes, projects or
machines side by side. The focused split has the highlighted border; the
tree picks what it shows (a single-view tab follows the tree as you move;
with splits, click or `enter` a row).

| Keys | |
|---|---|
| `ctrl+b v` / `ctrl+b -` | split right / down (the new split shows the same thing until you pick another) |
| `ctrl+b ←→↑↓` or `h j k l`, `ctrl+b o` | move focus between splits |
| `ctrl+b x` | close the split (the pane keeps running) |
| `ctrl+b c`, `n`, `p`, `1-9`, `&`, `,` | new, next, previous, go to, close, rename tab |
| `ctrl+b =` | equalize splits |
| `v` / `s` / `O` in the tree | open the selected item in a split right / below / a new tab |

Drag a border between splits to resize. Tabs and splits are remembered in
`ui.json`.

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
   rebuild conch. A released conch without source or Go downloads the
   matching release binary instead (checksum-verified). Otherwise put a
   binary there or point `CONCH_REMOTE_BINARY` at one.
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
live screen. `ctrl+b [` (or `ctrl+b pgup`) enters scroll mode with a cursor:
`↑↓←→`/`hjkl` move it (scrolling at the edges), `0`/`$` line start/end,
`pgup`/`pgdn` page, `g` oldest line, `v` starts a selection and `y` (or
enter) copies it; any other key returns to live.

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

### Local files in worktrees

A fresh worktree has only what git tracks, so an agent there would miss your
`.env` or personal agent settings. When conch creates a worktree it copies
the main checkout's **gitignored** files that match the project's patterns —
by default `.env`, `.env.*`, `.envrc`, `.claude/settings.local.json`,
`CLAUDE.local.md`, `.mcp.json`, `AGENTS.override.md` and `.gemini/.env`.
Untracked files git doesn't ignore are never copied, so an agent can't commit
them on the task's branch by accident; the setup view lists them instead.

`F` on a project (or `conch project files ID PATTERN...`, `-reset`, `-none`)
changes the patterns, which are git globs. Existing files in a worktree are
never overwritten.

## Agent setup

`i` on a project, branch or pane shows what each agent loads there, read
from the agents' own configuration (conch only reads it):

- **Instructions** — `CLAUDE.md`, `AGENTS.md`, `GEMINI.md` and friends, from
  your home and every folder the agent reads them from
- **Skills** — `~/.claude/skills`, `.agents/skills` (Codex, Gemini, OpenCode),
  `.gemini/skills`, `.opencode/skills`, plugin skills
- **Commands and subagents**, **MCP servers** (names and transport only —
  never commands, URLs or secrets), **plugins and extensions**
- **Added by conch** — the hooks or plugin conch injects for state tracking
- Warnings, e.g. a folder Claude or Codex hasn't trusted yet

In a worktree it also compares with the main checkout: local files that are
missing (`c` copies them) and setup that only the main checkout has, such as
Claude MCP servers registered for that folder or an uncommitted `.mcp.json`.
`conch agent setup [-agent NAME] [-copy] [-json] [DIR]` prints the same.

## Keys

| Where | Keys |
|-------|------|
| Tree  | `↑↓` `jk` move · `←→` `hl` fold · `space` toggle · `enter` open pane / view changes · `/` filter · `!` next agent waiting for you · `o` open PR · `y` copy · `m` menu · `?` all keys · `q` detach |
| Create | `t` task · `c` default agent here · `A` start or install any agent · `n` terminal here · `a` add project · `M` add machine · `r` rename · `x` close / remove · `R` refresh git and PRs, or reconnect a machine |
| Pane  | everything goes to the program · `ctrl+b [` scroll history · `ctrl+b` then any key → tree · `ctrl+b z` zoom · `ctrl+b !` next waiting |
| Changes | `↑↓` file · `enter` diff · `o` open PR · `y` copy path / diff · `esc` back |

**Mouse:** click to select, click a selected folder (or its arrow) to fold,
double-click to open, right-click for a context menu, wheel to scroll, drag
the sidebar's edge to resize, click `⚑ N waiting` to jump. Clicks inside a
pane go to programs that use the mouse. Hold shift (option in iTerm2) to
select text with your terminal, or set `[ui] mouse = false`.

## Agents

| Agent | Launch | How conch knows its state | Install (no root) |
|---|---|---|---|
| **Claude Code** | `claude --settings <conch hooks>` | hooks, screen, token usage from the transcript | `curl -fsSL https://claude.ai/install.sh \| bash` |
| **Codex** | `codex` | terminal title (`[ ! ] Action Required`, spinner) and screen ("Would you like to run the following command?") | `curl -fsSL https://chatgpt.com/codex/install.sh \| sh` |
| **Gemini CLI** | `gemini` with conch's hooks in a system-defaults settings file (merged with yours; Gemini runs hooks only in trusted folders) | hooks when trusted, title (`✋ Action Required`, `✦ Working…`, `◇ Ready`) and screen | `npm install -g --prefix ~/.local @google/gemini-cli` (Node 20+) |
| **OpenCode** | `opencode` with conch's plugin via `OPENCODE_CONFIG_CONTENT` (merged with your config) | plugin events (busy, idle, permission, question) and screen | `curl -fsSL https://opencode.ai/install \| bash` |

`c` (or clicking **c agent** in the status bar, or **Start an agent…** in the
right-click menu) asks which agent to start at the selected place — a
machine, project, branch or pane: every agent installed there is listed, the
default (Settings → Agents) pre-selected, so `c` `enter` starts it. Missing
agents can be installed from the same list. `t` tasks use the default agent
unless the task dialog's Agent field (or `conch task -agent NAME`) names
another. conch never edits an agent's own configuration or answers its
trust prompts for you.

## Brain

Press `:` (or click **✦ Ask**) and say what you want in plain words:

- *"start 3 agents on api: fix the flaky login test, add rate limiting, update the README"*
  — split into tasks, each on its own branch and worktree with a self-contained prompt
- *"tell the agent on fix-login to also cover the logout path"* — a message typed into that agent
- *"what is waiting for me?"* — an answer, no actions

The brain sees every connected machine, its projects, branches and agents
(with their states and summaries) and proposes a plan. Each action is listed
with a checkbox; invalid ones (an unknown project, an agent that isn't
installed, an offline machine) are marked and skipped. **Nothing runs until
you press enter.** `conch ask [-y | -n] "…"` does the same from a shell.

**Summaries.** `S` on an agent asks for a one-line summary — what it is doing
and what it needs from you — shown in its title bar and on the project page,
and given to the planner. Settings → Brain → *Summarise agents* does this on
its own whenever an agent finishes or starts waiting (a small model request
each; the agent's visible screen is sent to the provider).

**Providers** (Settings → Brain, or `[brain]` in config.toml):

| Provider | Uses | Default models |
|---|---|---|
| `claude` *(default)* | the Claude Code CLI headless (`claude -p`, no tools, MCP servers, settings or session history) with your existing Claude login | sonnet · haiku for summaries |
| `anthropic` | the API with `$ANTHROPIC_API_KEY` | claude-sonnet-5 · claude-haiku-4-5 |
| `openai` | any OpenAI-compatible endpoint: `base_url = "http://localhost:11434/v1"` for Ollama | set `model` |

```toml
[brain]
provider = "claude"
model = ""            # provider default
summary_model = ""    # a small, fast model by default
summaries = false     # automatic summaries
```

New providers implement `brain.Provider` (`Complete` with an optional JSON
schema for structured output).

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

Claude panes also show token usage in their title — `ctx 45k · out 12k`
(the latest request's context and output so far) — read incrementally from
the session transcript the hooks point at.

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
enabled = true
desktop = true   # macOS notification / notify-send
sound = false    # system sound
bell = false     # terminal beep
waiting = true   # when an agent needs an answer
done = true      # when an agent finishes

[ui]
mouse = true
theme = "conch"  # conch, dracula, catppuccin, nord, gruvbox, tokyo-night
accent = ""      # override: teal, blue, green, orange, pink, red, gray, purple or "#rrggbb"

[shell]
omz_theme = ""   # Oh My Zsh theme for new zsh terminals; "" keeps .zshrc's

[agents]
default = "claude"  # what c starts: claude, codex, gemini, opencode
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
`project.changes`, `project.diff`, `project.create`; `fs.list`, `fs.mkdir`;
`shell.themes`; `agent.status`, `agent.install`; `worktree.add`, `worktree.remove`;
`task.create`.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
