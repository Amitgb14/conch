# End-to-end test plan

The automated suite (`go test -race ./...`, 91% coverage) runs everything
against fakes: fake servers and clients, fake `ssh`, `gh`, `sqlite3` and
agent scripts, `httptest` release and model servers. These paths have never
been run for real. Work through this list before a release, after changing
one of these areas, and whenever a fake might have drifted from the real
tool.

Status legend: ☐ not run · ◐ partly run (see note) · ✅ passed · ❌ failed. Details of each run are in [Runs](#runs) at the end.

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
| 2.2 💳 | Claude plan limits | After 2.1, look at the status bar and machine page | `Claude 5h N% · 7d N%` appears and matches `/usage` in Claude; your own statusLine still renders | ✅ R2 5h and weekly windows reported through the status line after one request |
| 2.3 💳 | Codex | `c` → Codex, one prompt, `/exit` | State tracked; `Codex` limits appear from the rollout; the tab closes on exit | ◐ R2 state tracked through a request; limits and /exit not checked |
| 2.4 💳 | Gemini CLI | `c` → Gemini, one prompt, exit | State tracked; token usage shown | ☐ |
| 2.5 💳 | OpenCode | `c` → OpenCode, one prompt, exit | State tracked; usage read from `opencode.db` | ◐ R2 message submitted; OpenCode's own xAI login had expired, so no answer — shown as done, **now `✗ failed`** |
| 2.6 🌐 | Agent installers | On a machine without an agent, `c` → pick it → confirm install | Official installer runs in a pane; flash says installed; `c` then starts it | ☐ |
| 2.7 | Agent setup view | `i` on a project with CLAUDE.md, skills and MCP servers | Lists instructions, skills, MCP servers (approved/pending) matching what the agent loads | ✅ R1 (`@` imports in CLAUDE.md / GEMINI.md now listed, since the run) |

## 3. Sessions

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 3.1 | Claude sessions | Project → Sessions after 2.1 | The session is listed with its AI title and branch; `enter` resumes it (`--resume`) | ◐ R1 listed with titles and branches; resume not run |
| 3.2 | Codex sessions | After 2.3 | Listed; resume runs `codex resume <id>` and continues the conversation | ◐ R1 32 of 32 rollouts listed; resume not run |
| 3.3 | Gemini sessions | After 2.4 | Listed (from `~/.gemini/tmp/<project>/chats`); resume works | ☐ |
| 3.4 | OpenCode sessions (sqlite) | After 2.5, with `sqlite3` installed | Listed from `opencode.db`; resume works; `d` deletes through `opencode session delete` | ◐ R1 listed and searched; resume and delete not run |
| 3.5 | OpenCode sessions (old file store) | A machine with `storage/session/*/ses_*.json` and no sqlite3 | Listed; `d` moves the file to the Trash | ☐ |
| 3.6 | Delete to Trash | `d` on a Claude session | Confirm; the `.jsonl` and its sibling folder land in `~/.Trash` (macOS) or `$CONCH_HOME/trash` | ☐ |
| 3.7 | Interrupted runs | Start an agent, `conch server stop` (in the e2e home), reopen | Sessions shows `⚠` interrupted; `I` resumes them all | ✅ R1 listing by project; **bug found and fixed**: a directory given through a symlink found nothing |

## 4. Remote machines 🖥

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 4.1 | Add a machine | `conch machine add user@host` (key auth) | Probes, installs `~/.local/bin/conch`, connects; machine appears in the tree | ☐ |
| 4.2 | Password auth | Same against a host that needs a password; and `M` in the TUI with the Password field and Key login ticked | Terminal: ssh asks during probe and install. TUI: added without a terminal, key authorized and verified, reconnects need no password | ◐ R3 TUI path against a real OpenSSH server in Docker; terminal path not run |
| 4.3 | Other architecture | Add a Linux host of a different arch than this computer | conch cross-builds from source (or downloads a release) and installs it | ☐ |
| 4.4 | Panes over SSH | `n` and `c` on the remote machine; type, resize, scroll, copy | Works like local; copy reaches the local clipboard via OSC 52 | ◐ R4 `conch -m` new, send, read and close on a Linux x86_64 devbox; typing, resize, scroll and copy in the TUI not run |
| 4.5 | Remote hot reload | Rebuild, then machine menu → Reload server | Remote panes keep running; the TUI reconnects; version popup shows it up to date | ☐ |
| 4.6 | Auto-update of remotes | Rebuild locally, open the version box, untick one of two remotes, press `u` | Local server reloads, TUI restarts, then only the ticked remote gets the new build and reloads; the unticked one keeps its build and still shows in the box | ☐ R4 not run: a TUI left open on an older build undid the remote upgrade (fixed, see R4); rerun once every TUI has the fix |
| 4.7 | Outdated remote server | Connect a TUI to a remote running an older build | "outdated" warning; `conch machine upgrade` reloads (or offers a restart) | ◐ R4 `conch machine upgrade` reloaded the devbox onto a different build with the same PID, uptime and panes |
| 4.8 | Connection loss | Drop the network or kill ssh | Machine shows connecting/offline, retries, recovers with panes intact | ✅ R4 killing the TUI's ssh bridge: offline at once, back online within 12 s by itself, remote panes intact |
| 4.9 | `-m` commands | `conch -m host status`, `new`, `server stop` | Operate on the remote; `status` on an unreachable host prints the reason | ◐ R4 `-m` status by label, `new`, `close`; `server stop` and an unreachable host not run |

## 5. Releases and updates 🌐

These need a published GitHub release; use a throwaway pre-release tag.

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 5.1 | `install.sh` | `curl -fsSL …/install.sh \| sh` on macOS and Linux (amd64, arm64) | Installs the right asset; checksum verified | ✅ R5 the published v0.1.0 one-liner (latest lookup) on macOS arm64 and Linux arm64 and amd64 |
| 5.2 | `conch update` | From an older release | Downloads, verifies, replaces the binary; `conch version` shows the new one | ☐ |
| 5.3 | Release check in the TUI | A release build older than the latest | Status bar shows `⬆`; version popup offers the update | ☐ |
| 5.4 | Update from the TUI | `u` in the version popup | Installs, reloads the server keeping panes, restarts the TUI, updates remotes | ☐ |

## 6. Server hot reload and TUI updates

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 6.1 💳 | Reload with live agents | Two agents working, a split and a scrolled pane; rebuild; `r` in the version popup | Agents keep working; screens, scrollback, titles and state survive; no stale text | ◐ R1 with shells and scrollback, no agents |
| 6.2 | Failed reload | Reload into a binary that can't start (`conch server reload -binary /bin/false`) | Server stays up on the old build; panes keep running; error shown | ✅ R1 |
| 6.3 | Stale TUI | Rebuild while a TUI is open | TUI notices within ~5s and offers to restart onto the new build | ✅ R1 |

## 7. Terminal UI in real terminals

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 7.1 | Terminals | iTerm2, Terminal.app, Ghostty, kitty, WezTerm, tmux, over SSH | Rendering, colours, mouse, `ctrl+b` keys, alt/ctrl+arrows | ☐ |
| 7.2 | Mouse in agents | Click in Claude's UI; drag to select; double-click | Clicks reach the agent; drags copy text; shift-drag uses the terminal's selection | ☐ |
| 7.3 | Clipboard | Copy in scroll mode, `y` in the tree, over SSH | Text arrives in the system clipboard (pbcopy/wl-copy/xclip, or OSC 52) | ☐ |
| 7.4 | Notifications | Settings → test notification; sound; bell; quiet hours; snooze | Desktop banner (osascript/notify-send), sound plays, silenced when quiet/snoozed | ☐ |
| 7.5 | Tabs and splits | Tab scoping, CLI group, sync typing, resize repeat, `w` picker, `ctrl+b q` numbers, `space` layouts, `< > .` tab moves, detach and reattach | Behaves as documented in the Keys page; layout restored after reattach | ☐ |
| 7.6 | Tiny and huge windows | Shrink to 1 row / 20 columns, grow to 300×100 | No crash; nothing wider than the window | ✅ R1 1×1 to 300×100 with dialogs open |

## 8. Git, GitHub and worktrees

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 8.1 🌐 | Pull request status | A branch with an open PR and `gh` logged in | `#N✓/✗` badge; `o` opens the PR | ☐ |
| 8.2 💳 | Task | `t` → branch name + prompt | Worktree under `<repo>.worktrees/<slug>`, local files copied, agent starts with the prompt | ☐ |
| 8.3 | Local files | `F` patterns like `.env`, `config/*.local.json` | Matching ignored/untracked files are copied into new worktrees | ✅ R1 |
| 8.4 | Changes view | Edit, add, rename, delete and binary files | Counts, diffs and renames correct; refreshes within ~2s | ✅ R1 |

## 9. Planned features (add rows as they land)

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 9.1 💳 | Plan-limit alerts | Use Claude until a window passes 80% | One alert per threshold per window; none repeated after restarting the TUI; resets with the window | ☐ |
| 9.2 | Session search | `/` in Sessions; words from a title and from inside a conversation | Title matches instantly; conversation matches with a snippet | ✅ R1 |
| 9.3 💳 | Share a session with a new agent | `s` on a Claude session → Start Codex | Codex starts in the session's folder, reads `.conch/handoff/claude-<id>.md` and summarises the earlier work; `git status` doesn't show `.conch/` | ☐ |
| 9.4 💳 | Share into a running agent | `s` on a Codex session → Send to a running Claude | The prompt is pasted and submitted (not left as an unsent newline) in Claude, Codex, Gemini CLI and OpenCode | ☐ |
| 9.6 💳 | Broadcast | Three agents (Claude, Codex, OpenCode) in a project, one waiting on a permission; `B` on the project, message "reply OK" | The waiting one starts unticked, the project's terminal unticked; after confirming, each agent receives and submits the message once | ✅ R2 Claude, Codex and OpenCode each submitted once (idle → working → done in ≤2.5 s); Codex's update menu showed as waiting, as intended |
| 9.8 | Broadcast to terminals | Two zsh terminals in different worktrees; `B` on Terminals, `git status` | Both run the command once; a terminal busy in `vim` gets the text typed into vim (expected) | ◐ R1 two zsh worktrees ran a pipeline once each; the vim case not run |
| 9.7 💳🖥 | Broadcast across machines | Agents locally and on a remote; `B`, `e` every machine | Both machines' agents receive it; a remote on an older build is named as skipped | ◐ R4 terminals on this Mac and a Linux devbox, `e` every machine: each ran the command once, the unticked devbox shell got nothing. Found the status counting one machine (fixed). Agents and an older remote not run |
| 9.9 🖥 | SSH sessions | `H` → a host from `~/.ssh/config`; then `H` → Enter a host… → `user@host` needing a password; then a host that doesn't exist | The first logs in with the user's key and config (aliases, jump hosts) and is listed under CLI → SSH; the password and host-key prompts appear in the pane; the bad host's error stays readable; `exit` closes the tab; the session survives a server reload | ☐ |
| 9.5 | Search large histories | A project with 100+ long Claude sessions | Conversation matches arrive within a few seconds; typing stays responsive | ✅ R1 35 MB of Claude history: 163 ms first search, ~10 ms after |

## Runs

### R1 — 2026-09-14, build e9a9d32 (+ symlink fix), macOS arm64

An isolated server (`CONCH_HOME=/tmp/ce`) with the real home, so agent session stores were read but not changed; nothing sent to a real agent, no remote machine, no network.

- **Sessions (3.x):** real stores listed correctly. Claude files skipped were right to be: one had no messages, one belonged to a worktree folder. Codex 32/32, OpenCode (sqlite) listed.
- **Search and handoff (9.2, 9.5):** conversation search on 35 MB of history, snippets present. Transcripts of real Claude (21 MB → 171 turns, 219 KB handoff, no system-reminder or tool noise), Codex and OpenCode sessions parse; no Gemini history to test.
- **Broadcast to terminals (9.8):** without `shells` the server refused both zsh panes; with it each ran `git status --short | wc -l | sed …` once, with the right per-worktree result, under Oh My Zsh.
- **Reload (6.1–6.3):** `/bin/false` (missing on macOS) and `/bin/ls` refused with clear errors, server and panes unaffected; a real new build reloaded with the same PID and panes still taking input; a TUI noticed a rebuilt binary within 7 s (`⬆`). Uptime in `conch status` restarted at a reload — since fixed: uptime now counts from the first server, and reloads are detected by a separate `loaded_at`.
- **Worktrees (8.3, 8.4):** `.env` and `config/*.local.json` copied, `node_modules` and tracked files not; untracked-but-not-ignored matches are not copied, as documented. Changes: modify, delete, rename, binary and a nested untracked folder, with a correct diff.
- **Agent setup (2.7):** real Claude, Codex, Gemini and OpenCode configs read.
- **Window sizes (7.6):** the TUI survived resizes from 1×1 to 300×100 with the broadcast dialog and help open.
- **Interrupted runs (3.7):** a fake agent's run was offered after `server stop`. Bug: `session.list` with `dir` `/tmp/…` found nothing because runs and agents record `/private/tmp/…`; fixed by resolving symlinks in every session method, with a regression test.

### R2 — 2026-09-15, build f7de043, macOS arm64 (paid)

Real Claude Code 2.1.271 (Opus 5), Codex 0.144.1 (gpt-5.6-sol) and OpenCode (Grok 4.3 via xAI) in `~/project/sandbox-cli`, a folder Claude and Codex already trusted; an isolated server (`CONCH_HOME=/tmp/cp`) with the real home for logins. One prompt each: "Reply with just the word OK and nothing else. Do not use any tools."

- **Broadcast submission (9.6):** `agent.broadcast` to all three; each went idle → working → done within 2.5 s, the prompt appeared once, and the input boxes were left empty. Claude and Codex replied "OK".
- **A menu is not a prompt:** Codex opened an "Update available" menu on start; conch showed it as `blocked`. Typing a broadcast into it would have pressed Enter on "Update now" — why waiting agents start unticked. "Skip" was chosen by hand (not a trust prompt).
- **Plan limits (2.2):** Claude's 5-hour (6%) and weekly (21%) windows reached conch with reset times.
- **Token usage:** Codex totals right away; Claude's caught up within ~20 s (transcript polling).
- **Found:** OpenCode's request failed (expired xAI login) but conch showed `done` and would have notified "finished". Fixed: Claude's `StopFailure` and OpenCode's `session.error` now mark the agent `✗ failed`, and the notification says it stopped.
- **Not run:** sharing a session into a running agent (9.4) and a new agent reading a handoff file (9.3) — the paste-and-submit path is the same one proven here; reading `.conch/handoff/…` inside each agent's sandbox is still unverified.

### R3 — 2026-09-15, uncommitted build with the password field, macOS arm64

A real OpenSSH server (`linuxserver/openssh-server`, Linux arm64) in Docker on 127.0.0.1:2222 with password login; an isolated TUI (`CONCH_HOME=/tmp/cs`, temporary `HOME`, its own known_hosts file) so the real `~/.ssh` was untouched.

- **Add with a password (4.2):** in the `M` dialog, target `ssh://dev@127.0.0.1:2222`, a password with a space and `!`, Key login ticked. The password field showed `•`; six seconds after enter the machine was online. conch was installed in the container and its server running.
- **Key login:** with no user key in the temporary home, conch created `CONCH_HOME/ssh/id_ed25519`, added it to the container's `authorized_keys`, and named it in the generated ssh config. Logging in with `BatchMode=yes`, `PasswordAuthentication=no` and no shared connection succeeded.
- **Secrets:** the password was in no file under the conch or home directories afterwards, and no askpass directory was left.
- **Reconnect:** after closing every ssh connection and restarting the TUI, the machine came back online with no password; `conch -m container status` worked.

### R4 — 2026-09-15, build 0ea04ff (+ fixes below), macOS arm64 and a Linux x86_64 devbox

A real devbox over SSH (key login through conch's own key), reached from an isolated local server (`CONCH_HOME=/tmp/cd`) with its TUI in a second isolated server. The devbox's own server — shared with the user's real TUI — was upgraded and put back; its user shell was never selected or typed into. Test panes were closed afterwards.

- **Upgrade (4.7):** a build from different flags (`-trimpath`) replaced the devbox server with the same PID, uptime and panes.
- **Found, update war (4.6):** a TUI restarted by an update auto-updates remotes on *every* reconnect, so a TUI still on the old build put the old build back on the devbox shortly after the upgrade. Fixed: only a machine's first connection after the update is checked; later reconnects leave it alone.
- **Panes over SSH (4.4, 4.9):** `conch -m busybox new`, `send`, `read` (printed `x86_64`) and `close`.
- **Broadcast across machines (9.7):** `B`, a command, `tab`, `e`; the dialog listed `local › CLI` and `busybox › CLI` with the devbox's user shell unticked. After ticking one test shell per machine the confirmation named just those two; each ran the command once and printed its own host and arch.
- **Found:** the status bar said "broadcast sent to 1 terminal" — each machine's answer replaced the last. Fixed: the machines are called together and the status adds them up ("sent to 2 terminals", confirmed on the rebuilt TUI).
- **Connection loss (4.8):** killing the TUI's ssh bridge showed the machine offline at once; it reconnected by itself within 12 s with a new bridge and both remote panes.
- **Harness note:** a command without `env -u CONCH_SOCKET` reached the real server running this session and typed into it. Every harness call must clear `CONCH_SOCKET` and `CONCH_PANE_ID` itself; shell state isn't carried between calls.

### R5 — 2026-09-15, v0.1.0 release dry run, macOS arm64 and Linux in Docker

`scripts/release.sh 0.1.0` built the four archives and `checksums.txt` (15 s). They were served from a local folder laid out like a GitHub release.

- **install.sh (5.1):** `CONCH_VERSION=0.1.0` installed the darwin/arm64 archive on this Mac and the linux/arm64 and linux/amd64 archives in Debian containers, each verified against the checksums. Every binary reported `conch 0.1.0`, started its own server, ran a pane and read its output back.
- **Found:** `go install …@v0.1.0` skips the release `-ldflags`, so such a binary would have called itself `0.1.0-dev` and been treated as a development build by updates. Fixed: a build from a release tag takes its version from the module.
- **Published:** the tag built and published the release through `release.yml`. Then, from GitHub: the documented `curl … install.sh | sh` (finding the latest release) installed 0.1.0 on macOS arm64 and in linux/amd64 and linux/arm64 containers; `go install …@v0.1.0` reported 0.1.0; `conch update` on 0.1.0 said it is the latest release.
- **Found:** the website build cloned without tags, so its header showed the source's development version instead of the release. Fixed: Pages fetches tags and rebuilds on a release tag.
- **Not yet possible:** updating from an older release (5.2–5.4) needs a second release.

