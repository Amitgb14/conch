# End-to-end test plan

The automated suite (`go test -race ./...`, 91% coverage) runs everything
against fakes: fake servers and clients, fake `ssh`, `gh`, `sqlite3` and
agent scripts, `httptest` release and model servers. These paths have never
been run for real. Work through this list before a release, after changing
one of these areas, and whenever a fake might have drifted from the real
tool.

Status legend: ☐ not run · ✅ passed (date, build) · ❌ failed (issue link)

## Ground rules

- **Use a separate conch.** Build into a scratch path and run it with its own
  home, so your real server and sessions are never touched:

  ```sh
  make build && cp bin/conch /tmp/conch-e2e
  export CONCH_HOME=/tmp/conch-e2e-home        # short path: unix socket limit
  env -u CONCH_SOCKET -u CONCH_PANE_ID /tmp/conch-e2e
  ```

  Inside a conch pane, always `env -u CONCH_SOCKET -u CONCH_PANE_ID`, or
  commands reach the outer (real) server.
- **Cost.** Items marked 💳 spend model or plan usage; keep prompts tiny
  ("reply with OK"). Items marked 🌐 need the network. Items marked 🖥 need a
  second machine over SSH.
- **Trust prompts.** A first launch in a new folder asks the agent's own
  trust question. Answer it yourself; never script it.
- Record the build (`conch version`), OS and date with each result.

## 1. Brain (model providers)

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 1.1 💳 | Claude CLI provider | Settings → Brain → provider `claude`. In the tree press `:` and ask "what is waiting for me?" | A plan appears (or "nothing needs you"); nothing runs before confirming; no error flash | ☐ |
| 1.2 💳 | Plan execution | `:` "open a terminal in <project>" → confirm | A shell pane opens in that project; the plan shows each step's result | ☐ |
| 1.3 💳 | Anthropic API provider | `export ANTHROPIC_API_KEY=…`, provider `anthropic`, repeat 1.1 | Same as 1.1; a bad key shows a clear auth error | ☐ |
| 1.4 💳 | OpenAI-compatible provider | Provider `openai` pointed at OpenAI or a local Ollama/LM Studio URL, repeat 1.1 | Same as 1.1; an unreachable URL shows a clear error | ☐ |
| 1.5 💳 | Summaries | Select a running agent, press `S`; also enable automatic summaries | A `✦` summary appears in the pane title within a few seconds | ☐ |
| 1.6 💳 | `conch ask` CLI | `conch ask -n "list my agents"`, then `-y` with a harmless request | `-n` prints the plan only; `-y` runs it | ☐ |

## 2. Agents launched for real

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 2.1 💳 | Claude Code state hooks | `c` → Claude; send a prompt needing a permission | Tree glyph goes working `⠋` → waiting `!` → done `✓`; a desktop notification fires when not viewing | ☐ |
| 2.2 💳 | Claude plan limits | After 2.1, look at the status bar and machine page | `Claude 5h N% · 7d N%` appears and matches `/usage` in Claude; your own statusLine still renders | ☐ |
| 2.3 💳 | Codex | `c` → Codex, one prompt, `/exit` | State tracked; `Codex` limits appear from the rollout; the tab closes on exit | ☐ |
| 2.4 💳 | Gemini CLI | `c` → Gemini, one prompt, exit | State tracked; token usage shown | ☐ |
| 2.5 💳 | OpenCode | `c` → OpenCode, one prompt, exit | State tracked; usage read from `opencode.db` | ☐ |
| 2.6 🌐 | Agent installers | On a machine without an agent, `c` → pick it → confirm install | Official installer runs in a pane; flash says installed; `c` then starts it | ☐ |
| 2.7 | Agent setup view | `i` on a project with CLAUDE.md, skills and MCP servers | Lists instructions, skills, MCP servers (approved/pending) matching what the agent loads | ☐ |

## 3. Sessions

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 3.1 | Claude sessions | Project → Sessions after 2.1 | The session is listed with its AI title and branch; `enter` resumes it (`--resume`) | ☐ |
| 3.2 | Codex sessions | After 2.3 | Listed; resume runs `codex resume <id>` and continues the conversation | ☐ |
| 3.3 | Gemini sessions | After 2.4 | Listed (from `~/.gemini/tmp/<project>/chats`); resume works | ☐ |
| 3.4 | OpenCode sessions (sqlite) | After 2.5, with `sqlite3` installed | Listed from `opencode.db`; resume works; `d` deletes through `opencode session delete` | ☐ |
| 3.5 | OpenCode sessions (old file store) | A machine with `storage/session/*/ses_*.json` and no sqlite3 | Listed; `d` moves the file to the Trash | ☐ |
| 3.6 | Delete to Trash | `d` on a Claude session | Confirm; the `.jsonl` and its sibling folder land in `~/.Trash` (macOS) or `$CONCH_HOME/trash` | ☐ |
| 3.7 | Interrupted runs | Start an agent, `conch server stop` (in the e2e home), reopen | Sessions shows `⚠` interrupted; `I` resumes them all | ☐ |

## 4. Remote machines 🖥

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 4.1 | Add a machine | `conch machine add user@host` (key auth) | Probes, installs `~/.local/bin/conch`, connects; machine appears in the tree | ☐ |
| 4.2 | Password auth | Same against a host that needs a password | ssh asks for the password on the terminal during probe and install | ☐ |
| 4.3 | Other architecture | Add a Linux host of a different arch than this computer | conch cross-builds from source (or downloads a release) and installs it | ☐ |
| 4.4 | Panes over SSH | `n` and `c` on the remote machine; type, resize, scroll, copy | Works like local; copy reaches the local clipboard via OSC 52 | ☐ |
| 4.5 | Remote hot reload | Rebuild, then machine menu → Reload server | Remote panes keep running; the TUI reconnects; version popup shows it up to date | ☐ |
| 4.6 | Auto-update of remotes | Rebuild locally, press `u` | Local server reloads, TUI restarts, then each remote gets the new build and reloads | ☐ |
| 4.7 | Outdated remote server | Connect a TUI to a remote running an older build | "outdated" warning; `conch machine upgrade` reloads (or offers a restart) | ☐ |
| 4.8 | Connection loss | Drop the network or kill ssh | Machine shows connecting/offline, retries, recovers with panes intact | ☐ |
| 4.9 | `-m` commands | `conch -m host status`, `new`, `server stop` | Operate on the remote; `status` on an unreachable host prints the reason | ☐ |

## 5. Releases and updates 🌐

These need a published GitHub release; use a throwaway pre-release tag.

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 5.1 | `install.sh` | `curl -fsSL …/install.sh \| sh` on macOS and Linux (amd64, arm64) | Installs the right asset; checksum verified | ☐ |
| 5.2 | `conch update` | From an older release | Downloads, verifies, replaces the binary; `conch version` shows the new one | ☐ |
| 5.3 | Release check in the TUI | A release build older than the latest | Status bar shows `⬆`; version popup offers the update | ☐ |
| 5.4 | Update from the TUI | `u` in the version popup | Installs, reloads the server keeping panes, restarts the TUI, updates remotes | ☐ |

## 6. Server hot reload and TUI updates

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 6.1 💳 | Reload with live agents | Two agents working, a split and a scrolled pane; rebuild; `r` in the version popup | Agents keep working; screens, scrollback, titles and state survive; no stale text | ☐ |
| 6.2 | Failed reload | Reload into a binary that can't start (`conch server reload -binary /bin/false`) | Server stays up on the old build; panes keep running; error shown | ☐ |
| 6.3 | Stale TUI | Rebuild while a TUI is open | TUI notices within ~5s and offers to restart onto the new build | ☐ |

## 7. Terminal UI in real terminals

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 7.1 | Terminals | iTerm2, Terminal.app, Ghostty, kitty, WezTerm, tmux, over SSH | Rendering, colours, mouse, `ctrl+b` keys, alt/ctrl+arrows | ☐ |
| 7.2 | Mouse in agents | Click in Claude's UI; drag to select; double-click | Clicks reach the agent; drags copy text; shift-drag uses the terminal's selection | ☐ |
| 7.3 | Clipboard | Copy in scroll mode, `y` in the tree, over SSH | Text arrives in the system clipboard (pbcopy/wl-copy/xclip, or OSC 52) | ☐ |
| 7.4 | Notifications | Settings → test notification; sound; bell; quiet hours; snooze | Desktop banner (osascript/notify-send), sound plays, silenced when quiet/snoozed | ☐ |
| 7.5 | Tabs and splits | Tab scoping, CLI group, sync typing, resize repeat, `w` picker, detach and reattach | Behaves as documented in the Keys page; layout restored after reattach | ☐ |
| 7.6 | Tiny and huge windows | Shrink to 1 row / 20 columns, grow to 300×100 | No crash; nothing wider than the window | ☐ |

## 8. Git, GitHub and worktrees

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 8.1 🌐 | Pull request status | A branch with an open PR and `gh` logged in | `#N✓/✗` badge; `o` opens the PR | ☐ |
| 8.2 💳 | Task | `t` → branch name + prompt | Worktree under `<repo>.worktrees/<slug>`, local files copied, agent starts with the prompt | ☐ |
| 8.3 | Local files | `F` patterns like `.env`, `config/*.local.json` | Matching ignored/untracked files are copied into new worktrees | ☐ |
| 8.4 | Changes view | Edit, add, rename, delete and binary files | Counts, diffs and renames correct; refreshes within ~2s | ☐ |

## 9. Planned features (add rows as they land)

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 9.1 💳 | Plan-limit alerts | Use Claude until a window passes 80% | One alert per threshold per window; none repeated after restarting the TUI; resets with the window | ☐ |
| 9.2 | Session search | `/` in Sessions; words from a title and from inside a conversation | Title matches instantly; conversation matches with a snippet | ☐ |
| 9.3 💳 | Share a session with a new agent | `s` on a Claude session → Start Codex | Codex starts in the session's folder, reads `.conch/handoff/claude-<id>.md` and summarises the earlier work; `git status` doesn't show `.conch/` | ☐ |
| 9.4 💳 | Share into a running agent | `s` on a Codex session → Send to a running Claude | The prompt is pasted and submitted (not left as an unsent newline) in Claude, Codex, Gemini CLI and OpenCode | ☐ |
| 9.6 💳 | Broadcast | Three agents (Claude, Codex, OpenCode) in a project, one waiting on a permission; `B` on the project, message "reply OK" | The waiting one starts unticked, the project's terminal unticked; after confirming, each agent receives and submits the message once | ☐ |
| 9.8 | Broadcast to terminals | Two zsh terminals in different worktrees; `B` on Terminals, `git status` | Both run the command once; a terminal busy in `vim` gets the text typed into vim (expected) | ☐ |
| 9.7 💳🖥 | Broadcast across machines | Agents locally and on a remote; `B`, `e` every machine | Both machines' agents receive it; a remote on an older build is named as skipped | ☐ |
| 9.5 | Search large histories | A project with 100+ long Claude sessions | Conversation matches arrive within a few seconds; typing stays responsive | ☐ |
