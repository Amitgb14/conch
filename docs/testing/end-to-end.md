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
| 2.8 🌐 | Devin for Terminal | On a Mac without `devin`, `c` → Devin → install; then `devin auth login`; `c` → Devin in a trusted project; `t` task with Devin; resume from Sessions or `-r` | The official installer runs in a pane and puts `devin` in `~/.local/bin`; `c` starts it; a task passes its prompt after `--`; the pane shows as Devin; its sessions appear under the project's Sessions (via `devin list`), `enter` resumes one with `-r`, `d` deletes it. Then read its real screens and process name to add state rules, and check whether `--config` merges with the user's config before conch passes hooks | ◐ R15/R16 working and the trust prompt read live; sessions listed, resumed and deleted; installing it on a machine without it is all that is left |
| 2.9 💳 | `conch wait` on a real agent | `conch new -agent claude`, send it a prompt, then `conch wait -state waiting,done PANE` from another shell; repeat with `-timeout 5s` while it works | The wait returns as the agent's state changes (prints `PANE claude done`), not on a timer; `-timeout` exits 124; closing the pane ends the wait with an error | ☐ |

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
| 4.1 | Add a machine | `conch machine add user@host` (key auth) | Probes, installs `~/.local/bin/conch`, connects; machine appears in the tree | ✅ R13 busybox, key auth |
| 4.2 | Password auth | Same against a host that needs a password; and `M` in the TUI with the Password field and Key login ticked | Terminal: ssh asks during probe and install. TUI: added without a terminal, key authorized and verified, reconnects need no password | ◐ R3 TUI path against a real OpenSSH server in Docker; terminal path not run |
| 4.3 | Other architecture | Add a Linux host of a different arch than this computer | conch cross-builds from source (or downloads a release) and installs it | ✅ R13 linux/amd64 cross-built on an arm64 Mac, 31 s |
| 4.4 | Panes over SSH | `n` and `c` on the remote machine; type, resize, scroll, copy | Works like local; copy reaches the local clipboard via OSC 52 | ◐ R4 `conch -m` new, send, read and close on a Linux x86_64 devbox; typing, resize, scroll and copy in the TUI not run |
| 4.5 | Remote hot reload | Rebuild, then machine menu → Reload server | Remote panes keep running; the TUI reconnects; version popup shows it up to date | ✅ R13 via `conch machine upgrade`: same pid, pane and scrollback kept |
| 4.6 | Auto-update of remotes | Rebuild locally, open the version box, untick one of two remotes, press `u` | Local server reloads, TUI restarts, then only the ticked remote gets the new build and reloads; the unticked one keeps its build and still shows in the box | ◐ R13 one remote: ticked updates and reloads it, unticked is skipped; two remotes still untried |
| 4.7 | Outdated remote server | Connect a TUI to a remote running an older build | "outdated" warning; `conch machine upgrade` reloads (or offers a restart) | ◐ R4 `conch machine upgrade` reloaded the devbox onto a different build with the same PID, uptime and panes |
| 4.8 | Connection loss | Drop the network or kill ssh | Machine shows connecting/offline, retries, recovers with panes intact | ✅ R4 killing the TUI's ssh bridge: offline at once, back online within 12 s by itself, remote panes intact |
| 4.9 | `-m` commands | `conch -m host status`, `new`, `server stop` | Operate on the remote; `status` on an unreachable host prints the reason | ◐ R4 `-m` status by label, `new`, `close`; `server stop` and an unreachable host not run |
| 4.10 | Saved SSH hosts | `H` to a real host; answer `enter` once and `y` once (two hosts); quit and reopen conch; `exit` the saved session; click the saved row; `x` it | `enter` connects unsaved and the host is gone after reopening; the `y` host is listed `○ … saved` under CLI → SSH after reopening, is hidden while its session runs, reconnects on a click without asking, and `x` forgets it | ☐ |
| 4.11 | SSH login after the host stops answering | Open an SSH session, then make the host stop answering without closing (sleep it, drop the network, or `kill -STOP` its `sshd-session`); open a new session to it | The new login starts straight away (or fails straight away if the host is really down), never a blank terminal for a minute | ✅ R26 on busybox: with a paused shared master, `ssh -F <conch config> -o ControlPath=none` logged in in 0 s; without it, 60 s blank, then `mux_client_request_session: read from master failed: Broken pipe` |

## 5. Releases and updates 🌐

These need a published GitHub release; use a throwaway pre-release tag.

| # | Path | Steps | Expected | Status |
| --- | --- | --- | --- | --- |
| 5.1 | `install.sh` | `curl -fsSL …/install.sh \| sh` on macOS and Linux (amd64, arm64) | Installs the right asset; checksum verified | ✅ R5 the published v0.1.0 one-liner (latest lookup) on macOS arm64 and Linux arm64 and amd64 |
| 5.2 | `conch update` | From an older release | Downloads, verifies, replaces the binary; `conch version` shows the new one | ✅ R11 v0.1.0 → 0.1.1 on macOS arm64 |
| 5.3 | Release check in the TUI | A release build older than the latest | Status bar shows `⬆`; version popup offers the update | ✅ R12 a real v0.1.0 TUI against the published 0.1.1, R24 a real v0.1.3 one against 0.1.4 · ✅ R32 2026-09-27 for v0.1.5: a real v0.1.4 TUI showed `⬆ v0.1.4` within seconds, and the popup read "⬆ Release   0.1.5 available (running v0.1.4)" |
| 5.4 | Update from the TUI | `u` in the version popup | Installs, reloads the server keeping panes, restarts the TUI, updates remotes | ✅ R12 local, R13 the remote half, R24 again on 0.1.3 → 0.1.4 · ✅ R32 2026-09-27 for v0.1.5: `u` installed it, reloaded the server **in place, same pid 74788** (0.1.4 → 0.1.5) and restarted the TUI onto build 98cb5a903ad7 with the arrow gone. Remotes not covered: the harness had none |
| 5.5 | `conch update list` | On a release build, with two or more releases published | Lists them newest first, marks the running one with `*`, the latest, and any kept locally | ✅ R24 on a real 0.1.4 after updating from 0.1.3 · ✅ R32 for v0.1.5 |
| 5.6 | Moving back | After a real `conch update`, run `conch update rollback` | Installs the kept copy with no download, reloads the server keeping panes, `conch version` shows the older release; a second `rollback` comes forward again | ☐ |
| 5.7 | Moving back to a version never kept | `conch update VERSION` for an older release on a fresh `CONCH_HOME` | Downloads and verifies that release, replaces the binary, reloads the server | ☐ |
| 5.8 | `[update] auto = true` | A release build older than the latest, `auto = true`, TUI open | The daily check installs the release on its own: flash, server reload, TUI restart, remotes | ☐ |
| 5.9 | install.sh with a version | `curl … \| sh -s -- OLDER_VERSION` over an installed newer conch | Installs that release into `~/.local/bin` (no `conch update` involved) | ☐ |

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
| 7.11 | Resource monitor | Click `▁▃▆` with a busy agent and an idle shell open; compare with `top` or Activity Monitor; run the TUI inside a conch pane; close with a key, a click outside and the icon | Memory and CPU match `top` for the TUI, the server and each pane's processes; the total counts nothing twice; the icon lights only while open; sampling stops when closed | ☐ fakes plus the real `ps` format (Linux); not yet compared with `top` on macOS or Linux |

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
| 9.26 💳 | A command in every attempt | In the compare view of 9.25, `t` with the project's real test command; watch the rows; `o` on a failing one | A terminal per attempt in its own worktree; rows go running… → passed / failed (exit N) matching what the terminal shows; `o` opens it; `t` again reruns | ☐ |
| 9.25 💳 | Compare attempts | After 9.24, let the agents work, then `A` on one attempt's changes: read the rows, `enter` into one, come back, `x` to keep it | Each row's files, lines and commits match its worktree; the running agent's state and cost show; `x` asks before discarding each other attempt and never touches one with an agent running | ☐ |
| 9.24 💳 | Best-of-N attempts | `conch task -agent claude,codex -n 3 "…"` in a real project, then `t` in the TUI with Agent `claude,codex` and Attempts 3 | Three worktrees and branches `…/claude`, `…/codex`, `…/claude-2`, each with its agent working on the same prompt; the dialog lists the branches before starting and warns when a plan window is nearly used | ☐ |
| 9.23 💳 | Brain handoff and broadcast | With Claude and Codex running in a project: `:` "hand the auth thread to Codex", confirm; then `:` "tell both agents to run the tests", confirm; then `/ !` in the tree | Codex starts on the handoff file and summarises the earlier work; both agents receive the message once; the filter lists only agents waiting, across machines | ☐ |
| 9.22 💳 | Cost of saved sessions | After real Claude, Codex and OpenCode runs, open Sessions for the project | Claude and OpenCode rows show a cost matching each agent's own report, Codex rows show tokens, Gemini rows show neither; a long Claude conversation (>256 KB) still shows its cost | ☐ |
| 9.21 💳 | Cost on the tree | Run Claude and Codex in one project until both have used tokens; watch the tree, the project page and the machine page; then `t` with Claude past 80% of its 5-hour window | Claude rows show `$…` matching `/cost`, Codex rows show tokens; the project and machine totals add up; the task dialog warns for Claude, not for Codex, and still starts the task | ☐ |
| 9.20 | Harvest from the CLI | In a task worktree: `conch branch commit -m "…"`, `conch branch push`, `conch branch pr`, then `conch worktree ls` and `conch worktree clean -y` | Each prints what it did and the branch really moves; `clean` removes only finished worktrees and exits non-zero when it keeps one | ☐ |
| 9.19 | Commit hunks | A file an agent changed in two places: open its diff, `n`/`space` to mark one hunk, `c`; then edit the file in an editor and press `c` again with the stale mark | Only the marked hunk is committed and the file keeps the rest; the stale one is refused with "changed since the diff was read" and nothing is committed | ☐ |
| 9.18 | Clean up worktrees | In a real project with a few old task worktrees: one merged on GitHub, one with uncommitted work, one whose folder was deleted in Finder, one with an agent running; `W` | The merged and deleted ones come ticked; the running one can't be ticked; ticking the uncommitted one needs `y` and names the files; afterwards `git worktree list` and `git branch` agree with what the flash says | ☐ |
| 9.14 🖥 | Drop on a password-auth machine | A machine added with a password; drop a file on its pane | Uploaded over the existing connection; no password prompt | ☐ |
| 9.15 🖥 | Drop on an outdated remote | A remote on a build without `fs.upload.v1` | Warning to update it; the local path is pasted unchanged | ✅ R10 (warning reworded so its cut-off form still reads) |
| 9.16 | Which terminals bracket drops | Drop a file on a local pane running `cat -v` in each terminal you use (Terminal.app, iTerm2, Ghostty, WezTerm, kitty) | Record whether the path arrives as a bracketed paste (`^[[200~`); ones that type it can't be uploaded yet | ☐ |
| 9.27 | Review queue on a real fleet | Several agents on two machines, some waiting, some done, plus a dirty worktree and an unpushed branch; press `Q` | The rows are the things actually needing a decision, waiting first and longest-wait first; `enter` lands on the pane for a waiting agent and on the changes for the rest; rows leave the queue as each is dealt with | ◐ R17/R18 branch rows on the real projects, grouped by project, opened and dismissed in use; agents waiting or done not yet seen in it |
| 9.29 💳 | Live diff as an agent writes (`make demo` sets this up with a fake agent; do it once with a real one too) | Watch the branch's file list while a real agent works across several files, then open one file's diff and leave it open while the agent rewrites lines in place, adds some, and finally commits | The list marks the file being written `▌` and counts them in the heading, moving from file to file as the agent does, and the marks fade a few seconds after it stops; the diff re-reads itself as the agent writes, in well under a second and without being asked; lines it just brought are marked `▌` and counted in the header for a few seconds, then fade; the view scrolls to the newest change until you scroll yourself (`F` and `g` start it again); a marked hunk the agent rewrote loses its mark; after the commit the diff says "no changes in this file" rather than going blank | ☐ |
| 9.30 | Watching a big repository | Add a project with a large ignored tree (`node_modules`, `target`, a `.venv`) and let an agent work in it; on Linux check `find /proc/*/fd -lname anon_inode:inotify` or the server's log, and run a build that writes thousands of ignored files | The watched directories are the unignored ones only — nothing under an ignored folder or `.git`; the build storms nothing into the TUI; no "directories is the limit" line in the log for an ordinary repository, and if it appears the changes view still refreshes by polling | ☐ |
| 9.32 | Live diff beside its agent | With an agent's pane open in a tab, move the tree cursor onto its branch and press `v`, open a file's diff in the new half, and let the agent work; then split a second branch in beside it, and put a third on its own tab | The diff half follows the agent's edits while the pane half keeps drawing; the two branch halves each follow their own worktree and neither moves for the other's edits; the tab switched back to is up to date, not showing what it had when you left | ☐ |
| 9.31 🖥 | Live diff from a remote | Open a diff on a branch on a remote machine while an agent edits it there; then point a current TUI at a remote server built before this feature | Edits on the remote reach the diff as fast as local ones; against the older server nothing breaks — the view re-reads every two seconds instead, with no error | ☐ |
| 9.28 | Checks on a real project | Set a check (`go test ./...`) on a real repository, let an agent finish on a branch, and watch the queue | The check runs in that branch's worktree only, in its own terminal; the row says checked or check failed with the exit code; a failure sorts to the top; a slow check shows `checking…` meanwhile | ☐ |
| 9.33 | Sessions when an agent's program is slow | With Devin installed, open a project's Sessions view while `devin list` is slow or signed out (pull the network, or `devin logout`); then sign back in | The list appears in about a second and a half with the other agents' sessions rather than waiting out Devin's own timeout; Devin's sessions appear a moment later on their own, without pressing `R`; nothing flickers or empties in between | ☐ |
| 9.34 | Reload a server that has been up for hours | On a machine where conch has run for hours with agents in panes, several watched projects and a TUI attached, `conch server reload` onto a fresh build; watch `conch status` from another terminal | The server comes back within a second or two with its panes; it never sits in a state where the socket is there but nothing answers. If it does: `conch status` says "not answering", running `conch` again starts a fresh server, and the old process's stacks (`sample PID`) go in the bug report | ☐ cause found: darwin's runtime_BeforeExec waits for async preemption signals that a thread-heavy server never takes (Go #41702); `go test -race -count=2 ./internal/server/` reproduces it and passes under GODEBUG=asyncpreemptoff=1 |
| 9.35 | Finishing with a branch | In a real project, start a task, then `x` on its branch row to remove the worktree; press `x` again on the branch that is left | The first removes the worktree and says the branch is kept; the second offers to delete the branch, saying what that loses, and the row leaves the tree once it is gone. On the base branch `x` says the base stays |  ☐ |
| 9.36 | Task branches carry the project's name | Start a task in two different projects (say OneNutri and conch) | Each branch is under its own project's name (`onenutri/…`, `conch/…`); the task dialog's Branch placeholder shows the same before you start |  ☐ |
| 9.37 | New tabs are terminals | With the tree selected (not a pane), press `ctrl+b c` three times, then `n` on a project row | Each `ctrl+b c` opens a tab with a shell in it — no tab says "empty" — and the terminal from `n` does not bring a row of empty tabs on screen with it; the `+` menu's **Empty tab** still makes one |  ☐ |
| 9.39 | Copying part of a long agent answer | With a real agent that has said more than a screenful, press `Y` on it in the tree; scroll the conversation, drag over a few paragraphs including past the bottom edge, release | The conversation opens with what the agent actually said; dragging past the edge keeps scrolling and the highlight follows; releasing puts exactly the highlighted text on the clipboard; `a` copies the whole thread; nothing is sent to the agent |  ☐ |
| 9.40 | The queue's filter, dismissals and stale checks | With rows from two projects: press `/` and type a project name, then a word from a reason ("waiting", "failed"); dismiss a row with `x`, quit the TUI and start it again; with a check set, let it pass on a branch, then edit a file there and wait | The filter narrows as you type and the count says how many rows are hidden; `esc` clears it before leaving the queue. The dismissed row is still gone after the restart, and returns when what it says changes. The passing check reads "check out of date" after the edit and stops sorting as a failure; about half a minute after the branch goes quiet it runs again on its own |  ☐ |
| 9.41 | Scrollback in an agent's pane | With a real agent that has printed more than a screenful, press `ctrl+b [` in its pane and scroll back; select some of it and copy; then do the same in `vim` and in `less` on a long file | The agent's earlier output is there to read and select, in order and without repeated screens; copying gives that text. `less` scrolls back the same way. `vim`, which repaints rather than scrolls, keeps nothing — the pane still behaves normally and nothing is lost from the live screen |  ☐ |
| 9.42 | Searching a pane's history | In a terminal, `seq 1 20000`, then `ctrl+b [`, `/1999` `enter`, `n` a few times, `N`, `?`; then in a real agent's pane search for a word it said a few screens ago; on a remote machine with an older conch, press `/` | The view jumps to each match with the cursor on it and every match on screen is highlighted; `n` goes on up, `N` back down; past the oldest line it says it went on from the bottom; the agent's earlier words are found in its kept scrollback; `v`/`y` from the match copies it; the older machine says to update it rather than failing |  ☐ |
| 9.43 | Watching a terminal for quiet and output | In a terminal run `sleep 5; make test` (or any command that prints for a while), press `ctrl+b M`, switch to another tab; separately `ctrl+b A` on an idle terminal, switch away, `echo hi` into it via `conch send`; detach and come back while one is raised; reload the server with one watched | The first shows `~` in the tree and tab bar and a desktop notification about 30 s after the last output; the second shows `#` and notifies; opening either clears the mark; the marks are still there after detaching; the watching (menu ticks) survives the reload; nothing alerts while the pane is on screen, and an idle shell being watched does not alert on its own |  ☐ |
| 9.44 | File explorer icons in real fonts | Settings → Theme → File icons → Nerd Font glyphs, in a terminal with a Nerd Font and in one without, on macOS and Linux and over SSH; open Files on a project with `.go`, `.ts`, `.tsx`, `.md`, `Dockerfile` | With a Nerd Font the `.ts` file shows the TypeScript logo and `.tsx` the React atom, each in its own theme colour, and every row stays aligned (no glyph drawn two cells wide pushing the status letter out); without one the glyphs are boxes but nothing misaligns; Letters reads everywhere | ☐ |
| 9.45 | File explorer on a real checkout | `f` on a branch an agent is working on; expand `internal/`, select a file the agent is writing; `a`; `d`; `e`; `.` and `i` in a repository with `node_modules`; `w`; the same on a remote machine 🖥 | The branch's worktree is listed with the agent's files marked `M`/`?`; the preview follows the agent's writes without `r`; `a` puts the path in the agent's prompt (relative to its folder); `d` opens the diff; `e` opens `$EDITOR` in a new tab; `node_modules` only shows with `i`; on the remote every path is the remote's, and an older remote server says to update | ◐ R23 2026-09-24, a scratch repository with a fake agent: `f` listed it with folders first and type icons, `M` on a modified file, `?` on an untracked one and `A` carried up to the folder holding a staged one, header "3 changed". Folders opened a level at a time with the breadcrumb following; the preview showed "README.md · 2 lines · modified" with numbered lines and described a PNG as "binary · image/png · 2.0 KB" rather than printing it; `/` narrowed to matches and said "nothing loaded matches app" before the folder holding it was opened; `y` copied the path; `a` said "no agent is working in this checkout" with none running, and with one running put `internal/tui/app.go` in its prompt. Not covered here: a real agent's writes appearing without `r`, `d`, `e`, `node_modules` and `i`, `w`, and the whole of it against a remote machine |
| 9.5 | Search large histories | A project with 100+ long Claude sessions | Conversation matches arrive within a few seconds; typing stays responsive | ✅ R1 35 MB of Claude history: 163 ms first search, ~10 ms after |
| 9.53 💳🌐 | Share a session with a fresh sandbox | A sandbox with nothing cloned in it: `s` on a local session → `o` → the sandbox (offered as **home folder**) → Start Claude there | The sandbox is offered although it has no project; the conversation lands in `~/.conch/handoff/<agent>-<id>.md` there, the agent starts in the home folder, reads it and says what was done and what remains | ☐ |
| 9.38 💳🖥 | Share a session on another machine | Local Claude session; `s` → `o` → busybox · a project with the same branch checked out → Start Codex | Codex starts on busybox in that branch's worktree, reads `.conch/handoff/claude-<id>.md`, and its prompt says the session was on the local machine; nothing is written locally; an older server on either side says to reload it | ✅ R19 real Claude session handed from this Mac to busybox (linux/amd64): the file landed in the remote checkout, Claude read it, summarised what was done and what remained, and noticed by itself that the code it referred to was not in that checkout; nothing written locally |
| 9.44 💳🖥 | Move a worktree to another machine | A task worktree on the Mac with unpushed commits, staged and unstaged edits, an untracked file and a `.env`, a Claude agent working in it; `T` → busybox → the same project there (then again with busybox lacking the project: **Clone … there**) | The worktree appears on busybox on the same commit with the same staged/unstaged split and files; Claude starts there, reads the handoff and summarises; the Mac's Claude pane closes and its worktree stays; a branch already checked out on busybox is refused with nothing changed; clone over ssh without access fails with git's reason rather than hanging | ◐ R21 every path with a fake Claude (below): clone, move, handover, refusal, unreachable origin. A real Claude reading the handoff not run · R22 2026-09-24, the move for real from this Mac to busybox: `T` → machine → project (the picker offered **Clone aghadge/move-test there…** as well), a confirm naming "1 staged, 1 changed, 2 untracked" and the agent it would continue. On busybox the branch arrived on the same commit with `M  README.md` staged, ` M src/main.go` unstaged, `NOTES.txt` untracked and `.env` with its contents; the agent started there and the Mac's pane closed, its worktree kept with every change. The agent moved had no saved conversation, so the handover leg took the "starts afresh" path; the clone-there option and a branch already checked out on busybox are still only covered by fakes. |
| 9.54 💳🌐 | A sandbox that is not answering yet | Make a sandbox and watch what conch says while it sets itself up; then make several in a row, so that one of them meets a gateway that is not ready or drops the 16 MB copy | It says *waiting for X to answer*, then probes and copies; a dropped connection is tried again (three attempts, two seconds apart) and says which attempt it is on, instead of deleting the sandbox at the first failure; if it really cannot connect, the reason reads *the ssh connection failed (255)* with ssh's own words when it had any | ◐ R31 2026-09-27: the waiting step and a clean create against real Daytona; the retry itself only against a fake ssh that drops two connections, since the drop is not one you can ask for |
| 9.46 💳🌐 | A Daytona sandbox as a machine | With `DAYTONA_API_KEY` set: `conch sandbox create -label dt`; `conch -m dt status`; open the TUI and start a terminal and a fake or real agent in `dt`; close the TUI for longer than 15 minutes and come back; `conch sandbox stop dt`, `conch -m dt status`, `conch sandbox start dt`; `conch sandbox rm dt` with a branch there that isn't pushed | `create` makes a sandbox with auto-stop off, installs conch through Daytona's ssh gateway (a non-interactive `ssh token@ssh.app.daytona.io 'sh -c …'` with piped input and output, which Daytona's docs don't promise) and saves it; the TUI shows it as a machine; the agent is still running after the TUI was away; a stopped sandbox says to run `conch sandbox start` instead of retrying; `rm` lists the unpushed branch before asking; no ssh token is printed anywhere | ◐ R25 2026-09-26: create, install through the gateway (the non-interactive ssh works, 17 MB copied over stdin), `-m`, a pane, a git project, stop/`-m` refused/start with files kept, `rm` listing uncommitted work, all against real Daytona; no token printed. Not run: leaving it more than 15 minutes with the TUI closed, and an unpushed branch (only an uncommitted file) |
| 9.47 💳🌐 | Sandboxes from the TUI | With `DAYTONA_API_KEY` set: `M` → **New Daytona sandbox…** with a label and one `Pass in` variable; start an agent there; `m` → **Stop sandbox…**, then **Start sandbox**; `m` → **Delete sandbox…** with a branch there that isn't pushed; then the dialog again without the key | The new machine appears once conch is set up there, with the flash saying it runs until stopped; Stop asks (naming working agents), the row then says `stopped` and the page offers Start; Start reconnects and the agent's files are there; Delete lists the unpushed branch before asking and the machine leaves the tree; without a key the dialog says so and nothing is created | ◐ R25 2026-09-26: the menus, Stop (spinner, then `stopped` and **Start** on the page), Start (reconnected), **New Daytona sandbox…** (created `dt2` and added it) and Delete (gone from the tree and Daytona) on real sandboxes. Not run: `Pass in`, an agent working when stopping, the dialog without a key, and the "creating…" flash, which did not show (no flash did at the time, `R` included: a status bar bug at about 84 columns, since fixed — see R25) |
| 9.48 💳🌐 | A boat.dev sandbox as a machine | With `BOAT_API_KEY` set: `conch sandbox -provider boat create -label hull`; `conch -m hull status`; a terminal and an agent there from the TUI; `conch sandbox url hull 3000` with a server listening on `0.0.0.0:3000`; `conch sandbox usage hull`; `conch sandbox stop hull` then `start hull`; `conch sandbox snapshot -name hull-base hull`, `snapshots`, `snapshots -rm hull-base`; `conch sandbox rm hull`; and, on a paid plan, `auto_stop` longer than the plan allows, plus what boat does to a sandbox whose two hours run out | `create` authorizes this computer's public key in the sandbox, installs conch over ssh as `user` to the machine's own address (or its `sshEndpoint` when it has no public IPv4), and saves the machine as `boat:bx_…`; a size in `-cpu`/`-memory`/`-disk` is refused with the named sizes instead; the preview link answers at the sandbox's own subdomain and still does the next time; `usage` shows the seconds and dollars boat reports; a resumed sandbox lands on another machine and conch reaches it without being told; the stopped sandbox costs nothing; the named snapshot appears, deploys a new sandbox with `-snapshot hull-base`, and is forgotten again; a life longer than the plan allows is asked for again at the length the refusal named rather than failing; what boat does when the life runs out is written down here once it is seen; nothing but the key line is sent to boat | ◐ R27 2026-09-26: create, the key, install over ssh to `sshEndpoint`, `-m hull2 status`, `ls`, `rm`; sizes and life. Not run: stop/resume, the preview link, usage, named snapshots, a plan that allows less, and what boat does when a life runs out |
| 9.49 💳 | One setup, every agent | A real checkout with Claude Code set up in it (CLAUDE.md, `.claude/skills/*`, `.mcp.json` with a real server): `i` → `s`, read the question, answer yes; then start Codex, Gemini and OpenCode there and ask each what instructions, skills and MCP servers it has; `i` → `u`; the same over `-m` to a remote checkout 🖥; `conch agent sync -apply` on a repository whose AGENTS.md somebody wrote | The question lists each write with its path; after yes, Codex and OpenCode read the copied AGENTS.md, Gemini follows `@CLAUDE.md`, all three list the skill from `.agents/skills`, and each agent really lists the synced MCP server when asked (the formats are the part fakes cannot prove); a server whose env holds a token is left out and said so; `u` puts the checkout back exactly, `git status` clean; the hand-written AGENTS.md is reported skipped and unchanged | ◐ R28 2026-09-27: every MCP format against each agent's own CLI, Codex reading the copied AGENTS.md for real, Gemini reading the linked skill from `.agents/skills`, undo leaving the checkout as it was. Not run: Gemini's `@CLAUDE.md` import and OpenCode's AGENTS.md with a model (neither is logged in non-interactively here), a hand-written AGENTS.md, and the whole of it over `-m` |
| 9.55 💳 | Your own setup, given to the others | With Claude Code set up in your home (CLAUDE.md, ~/.claude/skills, servers in ~/.claude.json): Settings → Agents → **Give the others Claude Code's setup…**, read the question, answer yes; then ask Codex, Gemini and OpenCode what they load, anywhere; then **Put the last one back…**; and again with one of the files symlinked into a dotfiles repository | The question names every path in full and warns there is no git status; afterwards each agent's own CLI lists the synced server and skill wherever you are, not only in one checkout; Claude's ~/.claude.json is untouched; putting it back leaves the home as it was, and the record is under ~/.config/conch/agent-sync; a symlinked file is skipped with "a dotfiles repository?" and the repository is unchanged | ◐ R33 2026-09-27: the plan, apply and undo in a scratch home, `codex mcp list` showing the server from ~/.codex/config.toml and `gemini skills list` the skill from ~/.agents/skills, and the symlink guard by test. Not run: a real home of somebody's own, and OpenCode's CLI (its login is expired here) |
| 9.50 | A login URL an agent printed | An agent in a sandbox (or over ssh) running its login: `claude /login`, `codex login`, `gemini` — whatever prints a URL and asks you to open it; then `ctrl+b u`, and `m` → **Open a link it printed**; also with the URL scrolled off (`ctrl+b [` first), and in a narrow pane where the box wraps it twice | The menu lists the URL whole — no border, no line break, nothing missing — opening it signs the agent in and the clipboard holds the same text; a pane with no links says so; prose under a link is not glued to it (or, when it is, it is plain in the menu before anything opens) | ☐ |
| 9.51 | What a long job is doing | `M` → **New sandbox…** on a provider that works, and `M` → **Over ssh** to a machine with no conch on it yet: watch the status bar for the whole minute or two | A line with a spinner says what it is doing and keeps saying it — asking the provider, the sandbox is up, probing, building, copying N MB, connecting — until the machine appears or a reason does; it is not a flash that fades after four seconds, and nothing else takes the line while it runs | ◐ R29 2026-09-27: the flow and the refusal (boat's plan had lapsed, so the create was refused in a second: the notice read "boat.dev wants a plan or a payment method…" once, not twice); a fast add showed only its "added" flash. Not run: watching the line through a slow install, which is the point of it |
| 9.52 | Conch's own folder deleted under it | On a machine or sandbox with conch running: `rm -rf ~/.config/conch`, then start an agent (Claude, Gemini, OpenCode) from the TUI; then `conch -m ID status`; then a second server check (`ps` for `conch server`) | Starting the agent writes the files it is launched with again, so Claude does not fail with "Settings file not found"; the next connection starts a fresh server, which recreates the folder; the old server is left orphaned holding its panes, so its pid has to be killed by hand — nothing pretends those panes are still reachable | ◐ R29 2026-09-27: seen on a real Daytona sandbox — the binary in ~/.local/bin survived, `status` started a fresh server and the folder came back, and two servers were running (the orphan holding one shell). The launch-time rewrite is covered by tests; not yet tried on a real agent after the folder went |
| 9.56 | A prompt theme as it really looks | On a computer with Oh My Zsh: Settings → Theme → **Shell prompt**, in a wide window and a narrow one; pick one and open a new zsh terminal; and against a local server built before this change | Each theme shows the prompt it draws, in its colours — agnoster's powerline segments, a two-line theme on one line, a long one cut with `…`; the terminal that opens draws the same prompt; a theme zsh could not expand shows nothing rather than `$(git_prompt_info)`; an older server simply shows the names, as before (the list is the local computer's either way) | ◐ R34 2026-09-28: the expansion itself against this Mac's 143 real themes (agnoster, bira, fino, gnzh, cloud, af-magic, minimal, robbyrussell) — see R34. Not run: the rows in the TUI at either width, opening a terminal to compare, and an older server |

## Driving a TUI under test

Keys go in with `conch send -keys ID KEY` (the flag before the id). Clicks
need `internal/tools/clicker`, which sends `pane.send_mouse` through the
protocol — `conch send` types text, and a raw mouse escape sequence is
encoded as text rather than passed through:

```sh
go run ./internal/tools/clicker "$CONCH_SOCKET" p1 109 39   # 0-based cells
```

Read the screen back with `conch read ID`. Always with a scratch
`CONCH_HOME` and socket, and `CONCH_PANE_ID` unset.

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

### R11 — 2026-09-18, v0.1.1 release, macOS arm64

The tag on 1718026 built and published the four archives and `checksums.txt` through `release.yml` (2m13s). Everything below ran with its own `HOME` and `CONCH_HOME`, and never touched the real server.

- **Dry run:** `scripts/release.sh 0.1.1` built darwin and linux, amd64 and arm64; the darwin/arm64 binary reported `conch 0.1.1`.
- **install.sh (5.1):** the documented one-liner from master installed 0.1.1 for darwin/arm64, finding the latest release by itself; the downloaded archive's SHA-256 matched the published `checksums.txt`.
- **go install:** `go install …/cmd/conch@v0.1.1` reported `conch 0.1.1`, so the release-tag version fix from R5 still holds.
- **Update from an older release (5.2):** a real v0.1.0 binary ran `conch update` and replaced itself: `0.1.0 → 0.1.1`, and then reported the same build hash as the install.sh copy.
- **Not run:** the TUI's own release check and update (5.3, 5.4) still need a TUI running an older release build.

### R12 — 2026-09-18, v0.1.0 updating to v0.1.1 from the TUI, macOS arm64

The first run possible with two releases published. A real v0.1.0 binary, downloaded from its release, ran its TUI in a pane of a throwaway harness server; both had their own `CONCH_HOME` and `HOME`, and the pane's command unset `CONCH_SOCKET` and `CONCH_PANE_ID` so the TUI started its own 0.1.0 server instead of joining the harness. The real server was never touched. The pane was widened to 200×50, since the status bar drops the version on a narrow screen. Clicks were sent as the SGR bytes a terminal sends.

- **Release check (5.3):** the status bar showed `⬆ v0.1.0`, and clicking it opened the version popup: `Version 0.1.0`, `⬆ Release 0.1.1 available (running v0.1.0)`, `u update everything (agents and shells keep running)`.
- **Update (5.4):** `u` replaced the binary within two seconds (`conch 0.1.1`), and the 0.1.0 server hot-reloaded in place — **same pid**, its `/bin/sh` pane still running with its scrollback (a marker printed before the update). The TUI came back on the new build two seconds later, with the `⬆` gone and `v0.1.1` in the status bar; `?` still opened the help and the tree still listed the kept pane. `conch update` afterwards said it is the latest release.
- **Not covered:** the remote half of 5.4 (updating machines from the same popup) still needs a machine in the catalog, as 4.6 says.

### R13 — 2026-09-18, a real remote machine over SSH, macOS arm64 → busybox (Linux x86_64)

The remote rows, at last, with key login to busybox. Isolated on both sides: its own local `CONCH_HOME`, `HOME` and short private `TMPDIR`, a `CONCH_SSH_CONFIG` pinning the key with `IdentityAgent none` and `ControlPath none` so no ssh connection was shared with the real conch, and a `CONCH_SSH` wrapper running every remote command under a scratch `HOME` and `CONCH_HOME` on busybox. busybox's own conch (server pid 5222) and its `~/.config/conch` were never touched, and the catalog used was the harness's own.

- **Found while setting up:** a wrapper that merely prefixes the remote command with `env HOME=… CONCH_HOME=… ` isolates almost nothing — the prefix reaches only the first command before a `;`, and `$HOME` in the probe script is expanded by the remote login shell regardless. The first add duly found busybox's real conch. Wrapping the whole script (`env … sh -c '<script>'`) is what isolates it; worth knowing for the next run.
- **Add (4.1) and other architecture (4.3):** `conch machine add` probed linux/amd64, found no conch in the scratch home, cross-built one from source for linux/amd64 on this arm64 Mac, copied 14 MB over ssh stdin and installed it; a remote `/bin/sh` pane then echoed its marker and `x86_64`. 31 s in all.
- **Remote hot reload (4.5):** `conch machine upgrade` with a binary built at a different version installed it and reloaded the remote server **in place — same pid 6232**, its pane still running with its scrollback.
- **Remote updates from the version popup (4.6, and the remote half of 5.4):** a TUI on the harness showed `⬆`, and the popup listed `[x] busybox   runs build 5a3f7aa19f3e · installs and reloads`. `space` toggled it to `[ ]` and back. Ticked, `u` installed and reloaded busybox (same pid, pane kept) and the `⬆` cleared; unticked, `u` left the remote on its old build. One remote only — 4.6's two-remote case still wants a second machine.
- **Not covered:** password auth (4.2; busybox has key login now), and the TUI's own machine menu → Reload server, which `machine upgrade` stands in for here.

### R26 — 2026-09-25, SSH logins after a host stops answering, macOS arm64 → busybox

A saved SSH host "logged in once, then not again". Run with plain `ssh` and a copy of conch's generated config, in a scratch `HOME` with its own known_hosts, `IdentityAgent none`, and a private ControlPath directory, so the real TUI's master connection was never shared. Only the test logins' own `sshd-session` processes on busybox were touched, and all of them were ended afterwards.

- **Host closes the connection:** killing the shared master's `sshd-session` let the next login connect at once. The master saw the close and went away.
- **Host stops answering (4.11):** pausing it with `kill -STOP` instead left the master alive but dead. A login through conch's config then sat blank for **60 s**, printed `mux_client_request_session: read from master failed: Broken pipe`, and only then connected. That is the reported "can't connect". It isn't about passwords, since busybox uses key login.
- **Fixed:** SSH sessions now pass `-o ControlPath=none` and make their own connection. With the same paused master, that login connected in **0 s**.
- **Found and fixed:** a login that failed at once (exit 255) stays on screen exited, and it hid the saved host's row, so there was nothing left to click to try again. Only a running session stands in for the host now.
- **Not run:** a host that only takes a password (busybox has none), and the TUI itself. The change is to the command the TUI runs, which the tests check.

### R24 — 2026-09-25, the v0.1.4 release, macOS arm64

The release rows again, on the release that added moving a worktree to another
machine, the file explorer, reading a pane's past on the alternate screen, the
finished review queue and terminal monitoring. The tag built on GitHub — tests,
four platforms, checksums, 2m37s — and the website deployed from it in 53s.

- **The archives (5.x):** all four are there with `checksums.txt`; the
  darwin/arm64 archive's sha256 matches it, and the binary inside says
  `conch 0.1.4 (build 71b54981694a, darwin/arm64)`.
- **install.sh (5.1):** with a scratch `HOME` it downloaded 0.1.4 for
  darwin/arm64, installed it to `~/.local/bin/conch` and said to add it to
  `PATH`; the installed binary reports the same build.
- **conch update (5.2):** a real v0.1.3 binary, downloaded from its own
  release, updated itself — "updated …/conch: 0.1.3 → 0.1.4" — and then
  reported 0.1.4 on the same build hash as the installed one.
- **The website:** https://amitgb14.github.io/conch/docs/installation/ serves
  0.1.4, resolved from the tag.

- **The update in the TUI (5.3, 5.4):** a real v0.1.3 TUI, in its own `HOME`
  and `CONCH_HOME` inside a harness pane, showed `⬆ v0.1.3` within seconds of
  starting; clicking the version cell opened
  "⬆ Release   0.1.4 available (running v0.1.3)". `u` downloaded it, reloaded
  its server **in place — same pid 48973** and restarted the TUI onto
  `v0.1.4` (build 71b54981694a) with the arrow gone, the memory icon of this
  release in the corner, and the popup then saying "Server up to date".
- **conch update list (5.5):** the updated binary listed
  `* 0.1.4 (latest, what conch update installs · running)`, `0.1.3 (kept, no
  download needed)`, then 0.1.2 to 0.1.0, and said rollback goes back to 0.1.3.

Not covered here: the linux archives, and updating a remote machine to 0.1.4.

### R20 — 2026-09-24, the v0.1.3 release, macOS arm64

The release rows again, on the release that added the review queue with its
checks, the live diff, staying in step with upstream, session handoff between
machines, and the scrolling and copying work. The tag built on GitHub — tests,
four platforms, checksums — and the website deployed from the tag as well.

- **The archives (5.x):** all four are there with `checksums.txt`; the
  darwin/arm64 archive's sha256 matches it, and the binary inside says
  `conch 0.1.3 (build 95261ca45915, darwin/arm64)`.
- **install.sh (5.1):** the one-liner from master, with a scratch `HOME`,
  downloaded 0.1.3 for darwin/arm64 and installed it.
- **conch update (5.2):** a real v0.1.2 binary, downloaded from its own
  release, updated itself — "updated …/conch: 0.1.2 → 0.1.3" — and then
  reported 0.1.3.

Not covered here: the linux archives, and updating a remote machine to 0.1.3.

### R14 — 2026-09-19, the v0.1.2 release, macOS arm64

The release rows again, on the release that added `conch wait`. `scripts/release.sh 0.1.2` built the four archives; the tag published them through `release.yml` in 2m.

- **install.sh (5.1):** the one-liner from master installed 0.1.2 for darwin/arm64, and the archive matched the published `checksums.txt`.
- **go install:** `…/cmd/conch@v0.1.2` reported 0.1.2.
- **Update from the previous release (5.2):** a real v0.1.1 binary ran `conch update` and replaced itself — `0.1.1 → 0.1.2`, same build hash as the install.sh copy.
- **Pages on a release tag:** the tag's website deploy **succeeded**, where v0.1.1's was rejected by the environment's protection rules. The deployment branch policy added afterwards (a `v*` tag policy) is what fixed it, and this is the first real release to prove it; the site shows 0.1.2.
- **Not run:** 5.3 and 5.4 again — R12 covered them, and nothing in this release touched the update path.

### R15 — 2026-09-19, reading Devin's real screens, macOS arm64

Devin v3000.10.31 (free plan) resumed in a pane of the live server, prompted from the CLI while its screen was sampled every second.

- **Working:** the status line reads `⠸⠀ Thinking · 0s (esc twice to interrupt)`, the verb changing (`Typing`, and a token counter appears after a second), and the input placeholder becomes `❭ Guide Devin while it works`. Both are now rules in `devin.toml`, so the tree shows the spinner.
- **Idle:** the placeholder reads `❭ Ask Devin to build features, fix bugs, or work on your code` and the bar shows `Context: 17k / 200k tokens (8%)`.
- **Waiting:** asked to delete a file, Devin printed the command and `This will permanently delete the file. Should I proceed?` **in prose, with the idle placeholder and status bar unchanged**. Nothing on screen separates waiting from idle, and a trailing question mark would catch its ordinary answers (its greeting ends "What would you like to work on?"), so no rule was added: a Devin pane needing an answer reads as idle and `!` will not jump to it. Hooks are the fix, once `--config` is known to merge.
- **Title:** `devin: hi` — the session's first prompt, not a state, so nothing to match there.
- **Trust prompt:** starting Devin in a directory it hasn't seen shows `✱ Do you trust the authors of this directory?` with `1 Yes, trust / 2 No, exit`. That one *is* a screen worth matching, so it is now a `blocked` rule — the only waiting state Devin exposes.
- **Sessions (3.x):** the running session appeared in the list as `devin · quark-schooner · "hi"`, with its updated time tracking the conversation and its pane linked. A throwaway session in a scratch repo (`magical-prepared`) was then deleted through `session.delete`, which runs `devin rm --force`: it disappeared from the list and the real session was untouched.
- **Found while testing:** typing into a Devin pane on a timer answered its trust prompt by accident (the keystrokes went to the dialog, not to Devin). Probes must read the screen before sending anything — the reason conch now detects that prompt at all.
- **Not run:** installing Devin on a machine without it, and resuming a session with `enter` from the Sessions view.

### R16 — 2026-09-20, resuming a Devin session, macOS arm64

The last part of 2.8 that automated tests can't reach: that `enter` in the Sessions view brings a Devin conversation back, not just a fresh CLI. Run against a harness server with its own `CONCH_HOME` and the real home, since Devin's login lives there; the real server and its sessions were not touched.

- Devin was started in a directory it already trusts (the conch checkout — a new directory would have asked about trust, and a probe must never answer that), and told to remember the word "pomegranate". It replied `ok`.
- The session appeared in `session.list` as `devin · polydactyl-skateboard`, with its title from the prompt and its pane linked.
- Its pane was closed, then `session.resume` — the call `enter` makes — started `devin -r polydactyl-skateboard`. The earlier exchange was on screen, and asking what word it had been told to remember answered **pomegranate**, so the conversation itself came back rather than the scrollback.
- `session.delete` then removed that session (`devin rm --force`); it left the list and the real session stayed.

### R17 — 2026-09-20, the review queue on real projects, macOS arm64

The queue rendered against the real projects (their `projects.json` copied into a harness `CONCH_HOME`, own server, the real one untouched), then confirmed in the actual TUI after a restart.

- **Rows:** three — `OneNutri · changelog-1.8` (22 files uncommitted), `onenutri-web · redesign/interactive-landing` (4 commits not merged) and `OneNutri · sandbox-recover/…` (1 commit not pushed) — ordered unshipped-then-dirty, oldest first within each. `feature/dietary-preferences`, 39 days old and not checked out, is filtered.
- **Found:** at 50–70 columns every row truncated to the same stub (`OneNutri ·…  1 com…`), because the project and branch shared the space equally. Fixed: the context drops from the left so the branch survives; rows now read `sandbox-reco…`, `redesign/int…`, `changelog-1.8`.
- **Found:** a restart of the *server* doesn't restart the TUI, and a TUI left running all day silently keeps the build it started with — which is what "the queue doesn't show properly" turned out to be. The TUI does offer a restart; it has to be taken.
- **Not yet seen:** the waiting and done bands on a real fleet, since no agent was in either state during the run.

### R18 — 2026-09-21, the queue in daily use, macOS arm64

Three bugs found by using it, all from the screenshots rather than the tests, and all fixed and confirmed in the real TUI afterwards.

- **A mangled glyph doubled rows.** The selected row was built by slicing two bytes off the drawn line to drop the band's glyph; `↑`, `✓` and `·` are multi-byte, so the row lost half a rune, measured wrong, wrapped, and left the row above drawn twice. That is what the duplicated `master` in the tree and the doubled status bar were — the data was never wrong.
- **Opening a row piled up tabs.** The queue shared the browsing tab, which `syncView` reassigns to the tree cursor, so opening a branch replaced the queue *and* opened the branch's own tab: four visits, four tabs of the same branch. The queue owns its tab now.
- **`Q` was unreachable from a view**, so after opening a row you could only return by clicking the status-bar count.
- **Grouping:** a repository with two loose ends read as itself listed twice. Rows now sit under a heading per project.
- **Still not seen on a real fleet:** the waiting and done bands, since no agent has been in either state while the queue was open.

### R21 — 2026-09-24, moving a worktree to busybox, macOS arm64 → Linux amd64

This Mac to busybox (`aghadge@10.0.0.115`), isolated on both sides. Here: its own `CONCH_HOME`, `HOME` and `TMPDIR`, with the TUI under test in a pane of a separate harness server. There: a `CONCH_SSH` wrapper running every remote command as `env HOME=/tmp/cmv-r/home CONCH_HOME=/tmp/cmv-r/conch PATH=… SHELL=/bin/sh sh -c '<script>'` with `ControlPath none`, so conch found this build in the scratch home and started its own server there. busybox's own server (pid 4161) was never reached and was still the same process afterwards. Agents were a fake `claude` on both machines that saves a transcript for its folder, then prints its host and arguments. The origin was a bare repository at the same path on both machines.

- **Clone, then move:** `feat` had 2 unpushed commits, a staged edit with an unstaged one on top, an untracked `notes/todo.md`, `.env` and an ignored `debug.log`. `T` → busybox offered only **Clone cmv-o/origin there…** (it had no project), defaulting to `~/api`. The clone appeared under busybox, and the confirmation listed 1 staged, 1 changed, 1 untracked, `.env` and the agent, but not the terminal. On busybox the worktree was on the same commit, with README staged and its last line unstaged, `notes/todo.md` and `.env` present and `debug.log` absent. Claude started there with "an earlier Claude Code session on local", and its handoff held the conversation, with `.conch/` excluded from git. On the Mac the Claude pane closed, while the terminal and the worktree stayed as they were.
- **Into an existing project:** `feat2` (one commit, clean) listed busybox's clone first as `api ~/api · same origin`, moved just that commit, and its agent continued there.
- **Refusals:** moving `feat` again was refused because it was already checked out there. `T` on `main` was refused as the base branch. Cloning an origin busybox couldn't reach failed within a second with git's own reason ("Host key verification failed…"), with no prompt and no hang.
- **Reload:** both test servers reloaded onto a newer build mid-run with their panes kept.
- **Found and fixed:** the status bar cut the refusal to "moving feat failed: rebuild t…". Failures now open a notice with the whole reason, and the status bar keeps the short form. The server's messages said "here", which read as the Mac inside a notice that also said "nothing changed here". They no longer name a place, and the notice names both machines ("moving feat to busybox failed … Nothing changed on local.").
- **Found and fixed:** a menu whose title was its widest line lost the end of it ("Move feat to which machin…"), in every menu: the frame's spaces weren't counted.
- **Not run:** a real Claude reading the handoff after a move (the handoff itself was checked in R19), and a pushed branch sending no commits (covered by the server tests).

### R34 — 2026-09-28, how each Oh My Zsh theme's prompt is expanded, macOS arm64

The script `themeSamples` runs, by hand against the real `~/.oh-my-zsh`
(143 themes), in a temporary `HOME` that is no repository.

- `print -rP -- "$PROMPT"` — the obvious way — left the substitutions a
  theme defers: `robbyrussell` ended in a literal `$(git_prompt_info)`,
  `af-magic` in `${(l.$(afmagic_dashes)..-.)}` and `agnoster` was nothing
  but `$(build_prompt)`. Nobody would recognise their prompt in that.
- `${(%%e)PROMPT}` ran them but left the escapes the helpers printed
  (`%{%K{black}%}` all through agnoster).
- `${(%%)${(e)PROMPT}}` — substitute first, then expand the prompt escapes
  that came back — drew them as the shell does: agnoster with its
  powerline segments and background colours, `bira` and `fino` over two
  lines joined into one, `cloud`, `gnzh`, `minimal` each in their colours.
  That is what shipped.
- The temporary `HOME` shows in the samples as `~`, and a git-aware theme
  shows its plain form because the folder is no repository — which is the
  point: a sample is the theme, not this checkout's branch.

### R33 — 2026-09-27, your own setup given to the other agents, macOS arm64

A scratch home — `~/.claude/CLAUDE.md`, a `tide-check` skill, and a server
in `~/.claude.json` beside a project history that had to survive — synced
with `conch agent sync -user` through a server of its own.

- The plan named every path in full (`~/.codex/AGENTS.md`,
  `~/.agents/skills/tide-check`, `~/.config/opencode/opencode.json`) and
  wrote nothing. Applying it wrote them; `~/.claude.json` was untouched,
  project history and all; the record went to `~/.config/conch/agent-sync`,
  and no `.conch` appeared in the home.
- **The agents' own CLIs, in that home:** `codex mcp list` showed
  `tidewatch` from the `~/.codex/config.toml` conch wrote, its variable
  masked; `gemini skills list` showed `tide-check` from the
  `~/.agents/skills` conch linked into.
- Undoing put the home back: every file conch wrote gone, the sources kept.
- **Found while doing it:** the trust notes were a checkout's. In your home
  Codex's project-trust note does not apply, and Gemini's is worse than the
  checkout one — it suppresses *your own* servers and skills in a folder
  you have not trusted — so the note now says which it is.
- The dotfiles guard (a file that is a symlink is skipped, the repository
  left alone) is covered by tests rather than by a real dotfiles setup.

### R32 — 2026-09-27, v0.1.5 arriving in a real TUI, macOS arm64

The last part of the release path, done after the re-cut tag: a genuine
**v0.1.4** binary (downloaded from its own release) with its own `HOME`,
`CONCH_HOME` and socket, its TUI running in a pane of that isolated server,
driven with `conch send` and — for the status bar — clicks through
`pane.send_mouse`.

- **5.3:** the 0.1.4 TUI showed `⬆ v0.1.4` in the status bar within seconds
  of starting. Clicking the version cell opened the popup: *"Server up to
  date · pid 74788 · running 3m"*, then *"Updates: ⬆ Release   0.1.5
  available (running v0.1.4)"*. Its Settings had four tabs, no Sandboxes —
  the old build, as intended.
- **5.4:** `u` downloaded and applied it. The server **reloaded in place —
  same pid 74788**, version 0.1.4 → 0.1.5 — the binary at
  `~/.local/bin/conch` became `conch 0.1.5 (build 98cb5a903ad7)`, the one
  install.sh serves, and the TUI restarted onto it: the machine row shows
  that build and the status bar reads `v0.1.5` with the arrow gone.
- **5.5:** `conch update list` then showed `* 0.1.5 (latest, what conch
  update installs · running)` above `0.1.4 (kept, no download needed)` and
  the rest.
- **A tool came out of it:** `internal/tools/clicker` sends a click to a
  pane through the protocol. `conch send` types text, and a raw mouse escape
  sequence is encoded as text rather than passed through, so there was no
  other way to press a status-bar cell in a TUI under test.

Not covered: updating a remote machine from the popup (the harness had no
remotes), and `[update] auto = true`.

### R31 — 2026-09-27, a Daytona create that failed, macOS arm64, build 0.1.5

From a real failure in the TUI: *setting up sandbox fe31f988… failed, so it
was deleted: install conch: exit status 255*.

- **What it was not.** The key, the quota, the region (`us`) and the
  snapshots were all fine: a raw API create worked, and `conch sandbox
  create` succeeded three times in a row. Nor was it the day's ssh-key
  change — `ssh -v` through Daytona's gateway shows *"Authenticated to
  ssh.app.daytona.io using "none""*: the token in the username is the whole
  login and no key is ever offered, with the old config or the new one. The
  session change in #18 touches interactive logins only.
- **What it was.** 255 is ssh's own code for a connection that failed
  rather than a command that ran, and it came with nothing on stderr, so
  conch showed the number. Daytona reports a sandbox `started` **0.2 s**
  after it is asked for one — timed here — and conch went straight at it;
  and the 16 MB copy is the longest thing it asks of that connection.
- **Fixed three ways:** conch waits for the machine to answer before asking
  anything of it, tries a dropped connection again (three attempts, two
  seconds apart, saying which one it is on), and says *the ssh connection
  failed (255), with nothing said: the machine may not be answering yet*
  rather than an exit code. A command that ran and failed is still passed
  straight back — trying that again would only fail the same way.
- Seen afterwards on a real create: *waiting for dtfix to answer…* then the
  probe, the build, the copy and the machine. The retry itself is covered by
  a fake ssh that drops its first two connections.

### R30 — 2026-09-27, the 0.1.5 release, rehearsed before the tag, macOS arm64

The release rows done against a local mirror rather than GitHub, so the
archives, the install script and `conch update` were checked before anything
was tagged (`CONCH_RELEASE_URL` pointed at a `python3 -m http.server` serving
`dist/`).

- **The archives (5.x):** `scripts/release.sh 0.1.5` built all four —
  darwin/amd64 (no cgo, so kqueue), darwin/arm64 (cgo, FSEvents), linux/amd64,
  linux/arm64 — with `checksums.txt`; `shasum -c` says OK for all four, and the
  binary inside the arm64 archive reports `conch 0.1.5 (build 5589d5a0d815,
  darwin/arm64)`.
- **install.sh (5.1):** with a scratch `HOME` and `CONCH_VERSION=0.1.5` it
  downloaded, checked the sha256, installed to `~/.local/bin/conch` and said to
  add it to `PATH`; the installed binary is the same build as the archive's,
  byte for byte. Resolving *latest* could not be rehearsed: it follows GitHub's
  redirect to the tag, which only exists after tagging.
- **conch update (5.2):** a real **v0.1.4** binary, downloaded from its own
  release, updated itself — *"updated …/conch: 0.1.4 → 0.1.5"* — and then
  reported 0.1.5 on that same build.
- **conch update rollback:** it went back to the 0.1.4 it had kept under
  `versions/`, reporting *"moved back … 0.1.5 → 0.1.4"*.
- **The linux archive really runs:** the linux/amd64 binary, copied to busybox
  (Linux x86_64), reports `conch 0.1.5 (build 31aaa0c0e26d, linux/amd64)` — a
  different hash from the Mac build, as a cross-build is.
- **Tests:** `go vet`, `gofmt`, `go test -race -count=1 ./...` all clean, and a
  `CGO_ENABLED=0` build, which is what the release script uses for three of the
  four platforms. Coverage 93.0%.

**Re-cut the same evening:** after R31's three fixes, the tag and its
release were deleted and remade from `37e8104`. GitHub built it again
(success), the assets are the four archives and `checksums.txt`, the notes
were restored with the new line, and a fresh `install.sh` run installed
`conch 0.1.5 (build 98cb5a903ad7)` — which carries the fix. Only this
machine had installed the first build.

**After the tag, the same day:** GitHub built `v0.1.5` (tests, four
platforms, checksums, gh release create) and the website deployed from it in
47 s, serving 0.1.5 on
https://amitgb14.github.io/conch/docs/installation/. `install.sh` with no
version resolved *latest* through GitHub's redirect and installed the
CI-built binary, `conch 0.1.5 (build 22673fb1944e)` — a different hash from
the local build, as another machine's is. A real v0.1.4 binary then ran
`conch update` with no argument, resolved 0.1.5 by itself and installed the
copy it had kept ("kept, no download needed"), and `conch update list`
showed `* 0.1.5 (latest, what conch update installs · running · kept…)`
above 0.1.4 down to 0.1.1.

Still not covered: the update arriving in a real TUI (5.3, 5.4), and the
linux archives installed by `install.sh` rather than copied by hand.

### R29 — 2026-09-27, a sandbox create from the TUI, macOS arm64, build 0.1.5-dev

Driven through an isolated conch (its own `CONCH_HOME` and socket, the TUI in a pane of that server, keys sent with `conch send`), because the point was what the status bar says while a job of minutes runs.

- **Found and fixed — a job of minutes said nothing.** The create set a flash, which fades after four seconds, and threw the install's progress away (`setUpSandboxFn(ctx, m, func(string) {})`): a minute or two of silence, then a machine or an error. Both the sandbox create and adding a machine over ssh now report each step to a line that stays until the job ends, with a spinner — the same steps the CLI prints.
- **Found and fixed — the reason read twice.** "create sandbox: create sandbox: boat: …": the provider's Create says it and the TUI said it again.
- **boat.dev's trial had lapsed**, which is why nothing could be created: `402 Start the $20/month Boat plan to create sandboxes.` The new advice showed as intended — *boat.dev wants a plan or a payment method on the account before it will make sandboxes* — in a notice, whole, rather than truncated in the bar.
- The `M` → **New sandbox…** → boat.dev dialog showed the life it gives (`boat.dev gives it 2h 00m from now…`) and no vCPUs, memory or disk fields, as a provider with named sizes should.
- **Found afterwards, from the same sandbox:** deleting `~/.config/conch` left Claude failing with *Settings file not found: …/conch/claude-settings.json*. The integration files were written once, when the server started; they are written again before every launch now, and the folder is made if it has gone. The binary lives in `~/.local/bin/conch`, so nothing needs reinstalling — but the old server keeps running with a deleted socket, unreachable and still holding its panes, and has to be killed by hand.
- **Not covered:** the line through a slow install. The account could make no sandbox, and the machine it added already had conch on it, so both finished in under two seconds; the rendering is covered by tests instead.

### R28 — 2026-09-27, one setup every agent, macOS arm64, build 0.1.5-dev

A scratch git checkout set up as Claude Code — `CLAUDE.md` naming a harbour master nobody could guess, a `tide-check` skill, and two MCP servers, one with a `${VAR}` and one holding a literal secret — synced to Codex, Gemini CLI and OpenCode with `conch agent sync` through a conch server of its own (`CONCH_SOCKET=/tmp/conch-sync.sock`, its own `CONCH_HOME`). The agents' real installations were used, read-only; no login was touched.

- **The formats are right, checked with each agent's own CLI rather than conch's reader.** `codex mcp list` and `codex mcp get tidewatch` showed the server from the `.codex/config.toml` conch appended to, with its `${TIDE_TOKEN}` masked — once the folder was trusted (through a scratch `CODEX_HOME`, so the real one was left alone). `gemini mcp list` showed it from `.gemini/settings.json`, and once the folder was trusted it *launched* it. `opencode mcp list` launched it too from `opencode.json`'s `command` list. The server whose environment held `hk-live-secret-do-not-copy` appeared nowhere: it was skipped, with the reason.
- **Codex read the instructions conch wrote.** `codex exec "Who is the harbour master?"` answered *Mirabel Quist* — a name that existed only in `CLAUDE.md` before the sync copied it into `AGENTS.md`. 11.7k tokens.
- **Gemini read the skill conch linked**, from `.agents/skills/tide-check` — the shared folder, not a copy: `gemini skills list` printed that path.
- **Found and fixed — trust hides more than it says.** Until the folder was trusted, `gemini skills list` left the project skill out **without a word** (project MCP servers at least warn). The sync now says so before you wonder: "Gemini leaves a project's MCP servers and skills out until you have trusted the folder, and says nothing about the skills", beside the note Codex already had.
- **Found and fixed — undoing.** It called everything `instructions` (the record kept no kind) and left `.agents`, `.codex` and `.gemini` behind empty. The record now keeps each entry's kind, older records still load as "file", and undoing takes away the folders the sync made, including `.conch` when that is all it held. A folder with anything else in it stays.
- **Not covered:** Gemini's `@CLAUDE.md` import and OpenCode's `AGENTS.md` with a real model — Gemini has no auth method set for `-p`, and OpenCode's xAI refresh token is expired, and neither is conch's business. A hand-written `AGENTS.md` being skipped, and the whole thing over `-m` to another machine, are still only covered by fakes.

### R27 — 2026-09-26, boat.dev sandboxes, macOS arm64, build 0.1.5-dev

A real boat.dev trial account, from this checkout's `./bin/conch` with `CONCH_PANE_ID` unset; the key came from `config.toml`. Two sandboxes were made and one deleted; `hull2` was left running as a machine.

- **Found and fixed — the create failed at the ssh login.** boat made the sandbox and conch authorized a key, but the install died with `user@217.182.194.157: Permission denied (publickey)`. `publicKey` authorizes the *user's* `~/.ssh/id_ed25519.pub` when there is one, while conch's generated ssh config named only its own `~/.config/conch/ssh/id_ed25519` — and naming any key stops ssh trying the defaults, so the key that had just been authorized was never offered. `ssh -i ~/.ssh/id_ed25519` into the same sandbox worked, which proved it. Now the config names every key there is (conch's own first, then the user's defaults), and a sandbox login offers exactly the key conch authorized (`-i`, `IdentitiesOnly=yes`). The same mismatch was failing key-login verification for ordinary machines whenever `ssh-agent` held nothing — the agent here held no identities.
- **After the fix:** `sandbox -provider boat create -label hull2` made `bx_sybjjee8`, probed it, cross-built for linux/amd64, copied 16 MB and connected: *added hull2 (hull2): boat sandbox bx_sybjjee8, server pid 16221 on box-node-e05ba44d3390bc69*. `-m hull2 status` reached that server. The machine has no public IPv4, so it was reached through `sshEndpoint` (`217.182.194.157:19037`, and later its own) — the path that only exists on boat.
- **Sizes and life:** boat reports `vcpu`/`memoryGB` and no disk, so `ls` now prints `4 vCPU, 8 GiB` rather than inventing `0 GiB disk`. The trial's two-hour ceiling is what conch asks for; `auto_stop` raises it, and boat's docs put the refusal behind the code `trial_auto_stop_required`, which the retry now recognises.
- **Also fixed from boat's own docs:** create takes `small | default | large` only (`xlarge` needs an allocation), so conch no longer offers it; a sandbox is `ready` a moment before it has an address, so `SSHAccess` waits for one; and refusals now carry boat's code, with a line saying what would mend the common ones.
- **Left behind:** `bx_w9e9q634` (the failed create, kept on purpose to probe, then deleted) and `hull2`, still running — it costs until it is deleted.

### R25 — 2026-09-26, Daytona sandboxes, Linux amd64, build 22815eadf83a

A real Daytona account, driven by an isolated conch: its own `CONCH_HOME` in a scratch folder, `CONCH_SOCKET=/tmp/conch-dt.sock`, `CONCH_PANE_ID` unset, and the TUI under test in a pane of that isolated server. The key was passed in the environment only. Two sandboxes were made (default snapshot: 1 vCPU, 1 GiB, 3 GiB disk) and both deleted at the end.

- **Create:** `conch sandbox create -label dt -yes` made the sandbox, then probed, copied this build (17 MB) and connected through `ssh.app.daytona.io` with a fresh token. This settles the open question: Daytona's gateway runs a non-interactive command with piped stdin and stdout, which its docs don't promise. No token appeared in any output.
- **The machine:** `sandbox ls` showed it `started`; `-m dt status` reached its server; a pane ran as `daytona` on x86_64 in `/home/daytona`, with Node 25.9 and Claude Code 2.1.19 already there. `-m dt project create` made a git repository, so git is on the image.
- **Stop and start:** `sandbox stop -y dt` took 3 s; `-m dt status` then failed with "sandbox dt is stopped; run: conch sandbox start dt". `sandbox start dt` took 1 s, the next connection started conch's server again by itself, the project was still listed and a file written before the stop was still there. Panes did not survive, as expected.
- **Found and fixed:** `sandbox rm` asked without listing an untracked file in that new repository. A repository with no commits has no branches, and uncommitted work was only looked for through branches. Worktrees are now checked on their own too; after the fix it listed "demo main: 1 file uncommitted".
- **TUI:** `dt` showed in the tree with its project and terminal. `m` offered Stop, Delete and "Remove machine (the sandbox keeps running)"; Stop asked, showed `stopping` with a spinner, then `■ dt stopped` with **m → Start sandbox** on the page; Start reconnected. `M` → **New Daytona sandbox…** with the label `dt2` created it and added it to the tree about a minute later; Delete on `dt2` asked and removed it from the tree and from Daytona within a second.
- **Found and fixed:** the connection dropping mid-stop found the sandbox `stopping` and treated it as settled: the page said "sandbox stopping" and, had the stop come from outside the TUI, would never have retried. States on their way somewhere now retry like a lost connection.
- **Found and fixed:** straight after a delete, Daytona still reports the sandbox `started` for a few seconds, so `sandbox ls` showed it as a stray. Sandboxes whose desired state is `destroyed` are now left out.
- **Found and fixed afterwards:** no status bar message showed at that point in the TUI, "creating a Daytona sandbox…" and `R`'s "connecting to…" alike, though "stopping dt…" had shown earlier. The TUI under test had shrunk itself to about 84 columns, by showing its own pane in the preview (a quirk of running it inside the server it watches). At that width the bar dropped the message though it fit: it counted the separators between its first four hints twice, so it thought it needed 6 more columns than it did. Not specific to sandboxes; any terminal around that width lost its messages.
