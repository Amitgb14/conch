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
| 7.7 | Agent titles stay out of input boxes | Run Claude Code for a while so it sets titles like `✳ Terminals SSH support`; watch its input box across title changes | The box holds only what Claude draws (its placeholder or your text), never the tail of a title; `ctrl+b r` clears anything left by an older build | ◐ R9 reproduced on real panes (a Claude box showed `commit the plan` over `screenshots plan`, the end of its title) and fixed; the fixed server needs watching over a working session |
| 7.8 | Pages and clicks | Click Workspace, a project, Branches, a branch, Agents, Terminals and their rows; with agent tabs already open; reopen a branch | Each row shows its own page; clicking a branch or pane opens it in its own tab, and reopening focuses that tab; the bar lists the group's tabs beside the page | ◐ R9 in a test TUI against a real server with open tabs; not yet over SSH or on a remote machine |
| 7.9 | Typing into a clicked tab | Select Workspace, click an agent's tab, type | Keys reach that agent at once; the tree stays on Workspace | ◐ R9 with a shell on an isolated server (typing into real agents was avoided) |
| 7.10 | Redraw | `ctrl+b r` on an agent whose box shows stale text, on a shell, and over SSH | The agent redraws with its input kept; a shell shows its prompt again on the next key | ◐ R9 a repainting test program redrew at the same size; not yet a real Claude box or SSH |

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
| 9.9 🖥 | SSH sessions | `H` → a host from `~/.ssh/config`; then `H` → Enter a host… → `user@host` needing a password; then a host that doesn't exist | The first logs in with the user's key and config (aliases, jump hosts) and is listed under CLI → SSH; the password and host-key prompts appear in the pane; the bad host's error stays readable; `exit` closes the tab; the session survives a server reload | ✅ R6 alias with key login, `ssh://dev@localhost:2299` with a password, and an unresolvable host; host-key and password prompts in the pane; the failed login stayed with ssh's error; `exit` closed the tab; typing worked after a reload. Found typing lost in the first seconds after a reload (all panes; fixed, see R6). R7 on busybox: key login, URL target, broadcast, reload with typing held. R8 on busybox with the fixes committed: all passed |
| 9.10 🖥💳 | Drop a screenshot on a remote Claude | In Terminal.app, iTerm2 and Ghostty: drag a PNG from Finder onto a Claude Code pane on a Linux machine | Status bar shows the upload; Claude shows `[Image #1]` and describes the picture; the file is in `~/.config/conch/uploads/<date>/…` there | ◐ R10 on busybox with the drop's bytes sent to the TUI: `[Image #1]`, described correctly, no permission prompt. A drag in a real terminal not run |
| 9.11 🖥 | Drop from the screenshot thumbnail | `cmd+shift+4`, then drag the floating thumbnail straight onto a remote pane | Uploaded before macOS deletes the temporary file; the path pasted there | ◐ R10 a file under `/var/folders/…/NSIRD_screencaptureui_…` deleted 0.3 s after the drop was uploaded; the real thumbnail not dragged |
| 9.12 🖥 | Drop several files | Select 3 files in Finder (one with spaces and a `'` in its name) and drop them on a remote shell; `ls -l` the pasted paths | One paste with three escaped paths that all exist there | ✅ R10 (as Terminal.app escapes them) — U+202F, `'`, `"`, `()`, `&`, `$`; identical bytes |
| 9.13 🖥 | Large drop while streaming | Drop a 20 MB file on a remote pane while an agent there streams output | Progress climbs; other panes keep updating; typing stays responsive | ✅ R10 3–4.5 s over the LAN beside a split printing 20 lines a second, which kept printing. **Found:** progress cut off; fixed |
| 9.15 🌐 | Finish a task through a pull request | A task branch with `gh` logged in against a throwaway GitHub repo: `space` two files, `c`, then `p` with a title; merge the PR on GitHub (squash); `R`, then `D` | Only the marked files are committed; the branch is pushed and the PR opens with that title; after the squash merge, `D` says nothing is lost and removes the worktree and branch | ☐ |
| 9.16 🖥 | Merge and discard on a remote machine | A task on a remote machine with an uncommitted file: `M`, then `c`, `M` with Squash; then a second branch that conflicts with the base: `M`; `D` on a branch with unpushed commits | The first `M` asks to commit first; the squash lands on the remote's base checkout; the conflict leaves the base checkout untouched and names the file; `D` lists the unpushed commits and only `y` discards | ☐ |
| 9.17 | Commit hooks and signing | A repository with a `pre-commit` hook that fails and `commit.gpgsign` on; `c` and `M` | The hook's message is shown and nothing is committed or half-merged; a signing prompt (pinentry) either works or fails with a readable error rather than hanging | ☐ |
| 9.18 | Clean up worktrees | In a real project with a few old task worktrees: one merged on GitHub, one with uncommitted work, one whose folder was deleted in Finder, one with an agent running; `W` | The merged and deleted ones come ticked; the running one can't be ticked; ticking the uncommitted one needs `y` and names the files; afterwards `git worktree list` and `git branch` agree with what the flash says | ☐ |
| 9.14 🖥 | Drop on a password-auth machine | A machine added with a password; drop a file on its pane | Uploaded over the existing connection; no password prompt | ☐ |
| 9.15 🖥 | Drop on an outdated remote | A remote on a build without `fs.upload.v1` | Warning to update it; the local path is pasted unchanged | ✅ R10 (warning reworded so its cut-off form still reads) |
| 9.16 | Which terminals bracket drops | Drop a file on a local pane running `cat -v` in each terminal you use (Terminal.app, iTerm2, Ghostty, WezTerm, kitty) | Record whether the path arrives as a bracketed paste (`^[[200~`); ones that type it can't be uploaded yet | ☐ |
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

### R6 — 2026-09-16, build 20f01ab, macOS arm64 and Linux in Docker

A real OpenSSH server (`linuxserver/openssh-server`) in Docker on 127.0.0.1:2299, with key and password login for `dev`. The TUI under test ran with `CONCH_HOME=/tmp/c9t` and a temporary `HOME` whose `~/.ssh/config` named the alias `e2e-box`, its own key and its own known_hosts, and turned off the ssh agent, so the real `~/.ssh` was never used. That TUI ran inside a pane of a second isolated server, which was used to send clicks and keys and read the screen.

- **Mouse path (9.9):** clicking `+` opened New (Empty tab, Terminal, Agent, SSH to a host…). SSH to a host… listed `e2e-box` (the `Host *` block was left out). Clicking it opened `ssh e2e-box` in its own tab under CLI → SSH, with the host-key prompt in the pane. After `yes`, key login worked (`dev@<container>`). Only the temporary known_hosts was written.
- **Keyboard path and password:** `H` → `e` → `ssh://dev@localhost:2299`. The different host name meant no shared connection, so the host-key prompt and then the password prompt appeared in the pane. The password wasn't echoed, logged in, and was in no file afterwards.
- **Refused and failed hosts:** `-oProxyCommand=touch …` was refused in the status bar ("an ssh host can't start with -"); no pane started and no file was created. `nosuchhost-e2e.invalid` stayed open, marked `exit 255`, showing ssh's resolve error.
- **SSH page:** clicking the SSH row showed "SSH 3 in CLI" with all three sessions, the bar listed only their tabs, and the status bar showed `H ssh`. Clicking a session opened its tab.
- **Reload:** `conch server reload` onto a `-trimpath` build and back kept the session (same PID, `ssh -F … -- e2e-box`, still under SSH), and commands typed afterwards ran on the host.
- **Found:** keys typed about 1 s after a reload never reached the pane; from about 3 s they did. On losing the connection the TUI moved focus to the tree, so those keys ran as tree commands, and it waited 2 s before reconnecting. Fixed: focus stays on the pane, keys are held and sent once the same pane is listed again, and the first retry comes after 250 ms.
- **Exit:** `exit` in `ssh e2e-box` closed its tab and pane; the other tabs stayed.
- **Cosmetic:** a URL target gave the name `ssh ssh://dev@localhost:2299`. Fixed: it is now `ssh dev@localhost:2299`.

### R7 — 2026-09-16, build with the R6 fixes (uncommitted), macOS arm64 and the busybox devbox (Linux x86_64)

SSH sessions against `busybox` (`aghadge@10.0.0.115`, key login with conch's key). The TUI under test had its own `CONCH_HOME` and a temporary `HOME`. Its `.ssh/config` named busybox with that key and its own known_hosts, with `IdentityAgent none`. A private `TMPDIR` meant its ssh connection didn't share the real TUI's to busybox. It ran inside a pane of a second isolated server. The machine catalog wasn't copied, so busybox's conch server was never reached.

- **Mouse path (9.9):** `+` → SSH to a host… listed `busybox`. The click showed the host-key prompt in the pane. Its fingerprint matched the busybox entry in the real known_hosts before `yes` (which wrote only the temporary file). Key login worked (`aghadge@… x86_64`), and its control socket was in the private directory.
- **Login prompt on the host:** busybox's oh-my-zsh asks "update? [Y/n]" at login, and it took the first key typed. It was declined each time; nothing on busybox changed.
- **Reload with typing:** `conch server reload` onto a `-trimpath` build, with keys sent at once and after 0.5 s. The status bar said typing was held, and every line reached busybox in order. The session kept running.
- **Keyboard path and name:** `H` → `e` → `ssh://aghadge@10.0.0.115:22` opened `ssh aghadge@10.0.0.115:22`, logging in over the test's own shared connection.
- **Refused and failed hosts:** `-oProxyCommand=…` was refused with nothing run. `aghadge@busybox-nosuch.invalid` stayed open, marked `exit 255`, with ssh's error.
- **SSH page and broadcast:** the SSH row's page listed all three sessions, and clicking one opened its tab. `B` on SSH listed only the two running sessions under "SSH"; `echo broadcast-$((3*11))` printed `broadcast-33` once in each.
- **Exit:** `exit` in `ssh busybox` closed its tab. The URL session, which shared that connection, kept working, and its own `exit` closed its tab too.
- **Found:** after clicking a session's tab while the tree's cursor was on the SSH row, the status bar said `CHANGES`, not `PANE`. It took its mode from the tree's cursor, while typing follows the focused tab (since af00a0e). Typing went to the right pane. Fixed: the status bar (and its plan limits) use the same row as typing. Checked by hand: `n`, click Terminals, click the tab gives `PANE`, and `ctrl+b [` gives `SCROLL`.
- **Harness note:** when a pane exits and its tab closes, focus returns to the tree, so text sent next runs as tree keys. Click back into a tab before typing.

### R8 — 2026-09-16, build 3b4cf22, macOS arm64 and the busybox devbox (Linux x86_64)

The R7 checks repeated on the committed fixes, with the same isolation (own `CONCH_HOME`, temporary `HOME` and known_hosts, `IdentityAgent none`, private `TMPDIR`, no machine catalog). All passed; nothing new found.

- **Mouse path:** `+` → SSH to a host… → `busybox`. The host-key prompt's fingerprint matched the real known_hosts entry. Key login worked (`aghadge@… x86_64`), using the test's own control socket.
- **Reload with typing:** keys sent the moment the reload finished and 0.5 s later all reached busybox in order. Meanwhile the status bar showed `PANE` and "reconnecting to local · typing is held"; the session kept running.
- **URL target:** `H` → `e` → `ssh://aghadge@10.0.0.115:22` opened `ssh aghadge@10.0.0.115:22` and ran commands.
- **Refused and failed hosts:** `-oProxyCommand=…` was refused with nothing run. `aghadge@busybox-nosuch.invalid` stayed open, marked `exit 255`, with ssh's error.
- **Status bar after clicking a tab:** with the tree's cursor on the SSH row, clicking `ssh busybox`'s tab showed `PANE` (R7 showed `CHANGES`).
- **Broadcast:** `B` on SSH listed the two running sessions; `broadcast-33` printed once in each.
- **Exit:** `exit` closed `ssh busybox`'s tab. The URL session sharing its connection still ran commands, and its own `exit` closed its tab.
- **busybox:** oh-my-zsh didn't ask to update this time, and nothing on busybox was changed.

### R9 — 2026-09-16, builds af00a0e to b421b66, macOS arm64

Test TUIs attached to the real server (read-only navigation, clicks sent as mouse events), isolated servers for anything that typed.

- **Title leak (7.7):** reading pane p43's styled screen showed its input box as a dim placeholder over normal text ending in `screenshots plan`, the tail of its title `✳ Drag-and-drop screenshots plan`. Reproduced in an isolated pane: ASCII titles were consumed, but every title starting with `✳` printed its rest at the cursor. **Found:** the emulator's parser (charmbracelet/x/ansi, still in v0.11.8) ends an escape string at byte 0x9C, which is also inside UTF-8 characters — `✳` is E2 9C B3. Fixed by replacing UTF-8 characters inside escape strings before the emulator sees them; titles, read from the raw output, keep their text.
- **Pages (7.8):** Branches, Agents and Terminals each showed their own page; a click on a branch or pane opened its tab, and clicking an open branch again focused its tab. **Found three:** clicking a branch dropped the request for its changes, so it loaded forever; clicking a branch on the Branches page (itself a preview) opened a second tab; and with an agent's tab open, selecting Agents jumped to that tab instead of listing them. All fixed with tests.
- **Typing (7.9):** with Workspace selected, a click on a shell's tab followed by typing reached the shell. **Found:** keys had followed the tree's cursor, so they were dropped; they follow the focused split now, and clicking a running pane's tab focuses it.
- **Reply stalls:** a client that left 3000 events unread no longer holds up replies (a regression test fails with the old delivery).

### R10 — 2026-09-16, the uncommitted drag-and-drop build, macOS arm64 and the busybox devbox (Linux x86_64)

Test TUIs ran in panes of an isolated harness server, with their own `CONCH_HOME`, a temporary `HOME` and known_hosts, `IdentityAgent` off, a private `TMPDIR` and no shared ssh connection. A wrapper around ssh (`CONCH_SSH`) sent conch's remote commands to a separate conch on busybox, under `/tmp/cdrop-new` (this build) or `/tmp/cdrop-old`. busybox's own server (pid 4955), which the real TUI uses, and its `~/.config/conch` were never touched. Both test servers and their folders were removed afterwards.

A drop was simulated by sending the TUI what a terminal sends: a bracketed paste of the escaped path. No terminal was dragged into (9.16 still open). The test files were a generated 320×200 PNG named `Screenshot 2026-09-16 at 10.02.11\u202fAM.png`, a file named `it's a "quoted" (1).png`, one named `notes & $stuff.txt`, and 20 MB of random bytes.

- **Several files (9.12):** `ls -l ` followed by a drop of three files, as Terminal.app escapes them (U+202F left unescaped, trailing space). The status bar said `uploaded 3 files to busybox`; the pasted busybox paths ran; SHA-256 matched; files 0600 in 0700 folders.
- **Claude (9.10):** Claude Code 2.1.274 in a trusted folder on busybox. The drop turned into `❯ [Image #1]` with no permission prompt. One prompt: "A checkerboard with a red-to-yellow-to-cyan gradient." (correct).
- **Large while streaming (9.13):** 20 MB with a split beside it printing a counter: uploaded in 4.5 s, the counter kept going (318 → 401), bytes identical, no `.part` left. **Found:** in a pane at 120 columns the status bar keeps 30 cells of message, so `uploading big 20MB.bin to bus…` never showed a percentage. Fixed: `uploading to busybox 40% · big 20MB.bin`, with a regression test; rerun showed 28%, 47%, … 89%, then done in 3.2 s.
- **Thumbnail (9.11):** a copy under `$DARWIN_USER_TEMP_DIR/NSIRD_screencaptureui_…`, deleted 0.3 s after the drop, was uploaded and its path pasted.
- **Left alone:** `/etc/hosts` and `hello <existing file>` pasted into the busybox shell, and the screenshot dropped on a local shell, all came through unchanged with nothing uploaded. A pasted name with an ASCII space where the file has U+202F (a mistake while testing) wasn't a file, so it too was pasted unchanged.
- **Outdated (9.15):** an old-build server running while the new binary sat beside it: busybox showed `outdated`, a drop pasted the local path with the warning. **Found:** cut to 30 cells the warning read `busybox's conch is older · up…`; now `busybox needs a conch update …`.
- **CLI:** `conch -m busybox upload` of two files printed both paths there.
- **Not run:** a drag in a real terminal (9.16, and the real-terminal part of 9.10–9.12), a password-auth machine (9.14; busybox has no password login), and the file-size limit and settings toggles in the real TUI (unit tests only).
