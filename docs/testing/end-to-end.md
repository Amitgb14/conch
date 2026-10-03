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
  second machine over SSH. Items marked 📱 need a phone on your tailnet.
- **Trust prompts.** A first launch in a new folder asks the agent's own
  trust question. Answer it yourself; never script it.
- **Numbers are labels, not an order.** A new row takes the next unused
  number in its section, whatever its position in the table, and keeps it
  for good: a run below and the plans refer to rows by number. Two branches
  adding rows at once both reached for the same next number six times, so
  check the highest in use before adding one — the row that had it first
  keeps it (9.86-9.91 are the colliders, renumbered 2026-10-02).
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
| 2.10 💳 | `conch agent prompt` on real agents | Claude and Codex in panes. `conch agent prompt -wait PANE "say OK"` on each while idle, again while working, and again after it finished unseen (`done`); then send one a prompt needing a permission and, while it asks, `conch agent prompt PANE "go on"`; also with Codex showing its update menu | Each wait returns only after the new answer (`PANE claude done`), never at once on the old `done`/`idle`; the one sent while working returns when the message it queued is answered; the blocked one exits 3, says what it is asking, and nothing appears in its input; the update menu is refused the same way. Not provable with fakes: that each agent's working state is seen after the message, so the turn moves — an agent read only from its screen that answers between two samples would leave the wait running to its timeout | ◐ R36 Claude: refused on its trust prompt (exit 3, cursor unmoved); idle, done and working all waited for the answer, the log showing done → idle at the keystroke that a state-only wait would have taken. Codex: its update menu and trust prompt refused; answers not seen (its API key was rejected, 401) |
| 2.11 💳 | Scoping a real agent | Two projects, a terminal pane in each. In project A start Claude and ask it to run `conch close` on B's terminal, `conch send` to it, `conch server stop`, and then `conch task -name helper "say hi"` and `conch close helper`; repeat with Codex and OpenCode. Then from A's terminal pane close B's terminal by hand | Each command the agent runs through its own shell tool is refused with `out_of_scope` naming the agent and the pane, nothing happens to B or the server; the task it starts has its pane as `created_by` in the pane list, and the agent may close it. The terminal pane is not scoped. Not provable with fakes: that each agent's tool processes descend from its pane (a tool that daemonises or double-forks would escape the walk) | ◐ R36 Claude: its tools descend from its pane — send and close to B's terminal and `server stop` refused with the agent and pane named; A's terminal reached; `task -name helper` started and closed. Codex and OpenCode not run |
| 2.12 💳🌐 | Scoping a real agent on another machine | A machine (busybox) with a pane of your own there. From Claude in a local project ask it to run `conch -m busybox close` on that pane, then `conch -m busybox task -cwd /abs/repo -name helper "say hi"` and `conch -m busybox close helper`; then repeat against a machine running an older conch | The close of your pane is refused `out_of_scope` naming "p4 on <this host>"; the task starts there with `created_by` that agent, and closing it works; against the older remote every `-m` command from the agent is refused with the upgrade hint, while the same commands from a terminal pane work | ◐ R36 Claude against busybox (a scratch server there): `close mine` refused as "p1 on Amits-MacBook-Pro-2.local", `server stop` refused; the task it started there closed. The older-remote half not run, to leave the shared server alone |
| 2.13 💳 | The conch skill in real agents | `conch agent skill -apply`, then in a project start Claude, Codex, Gemini and OpenCode in turn and ask each "get a second opinion on this branch from another agent"; answer a permission prompt in the helper only when asked to | Each agent lists or loads the `conch` skill; it checks it is in conch, commits, starts a helper with `conch task -name … -base <its branch>`, prompts it with `conch agent prompt -wait`, and reads the answer from the helper's file; when the helper asks a question it stops and says which pane is waiting rather than answering or prompting again; it closes the helper at the end. `conch agent skill -remove -apply` takes it away and the agents no longer list it | ◐ R36 Claude: loaded the skill, started a reviewer with the uncommitted diff in its prompt, read its REVIEW.md, closed it. Found the stale-hook bug (fixed). A helper asking a question not seen: auto mode asked nothing. Other agents not run |

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
| 8.5 | Git panel in a real terminal | Click a branch in the tree; try Fetch and Pull against a real remote, Stash / Pop, `log -5` typed, a rebase on the base that conflicts, then Continue and Abort; on a narrow (< 40 columns) and a large terminal | The panel opens beside the row, output shows from its first line, the conflict brings Continue / Abort / Ask to resolve, the tree's counts follow; a fetch needing credentials fails at once instead of hanging | ☐ |
| 8.6 💳 | Git panel to a real agent | With Claude on a branch, `ctrl+t` in the panel, type an instruction; then leave a rebase conflicted and press Ask to resolve | The message arrives as Claude's next prompt; it resolves and continues the rebase; a Claude waiting on a permission gets nothing and the panel says why | ☐ |

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
| 9.86 🌐 | Finish a task through a pull request | A task branch with `gh` logged in against a throwaway GitHub repo: `space` two files, `c`, then `p` with a title; merge the PR on GitHub (squash); `R`, then `D` | Only the marked files are committed; the branch is pushed and the PR opens with that title; after the squash merge, `D` says nothing is lost and removes the worktree and branch | ☐ |
| 9.87 🖥 | Merge and discard on a remote machine | A task on a remote machine with an uncommitted file: `M`, then `c`, `M` with Squash; then a second branch that conflicts with the base: `M`; `D` on a branch with unpushed commits | The first `M` asks to commit first; the squash lands on the remote's base checkout; the conflict leaves the base checkout untouched and names the file; `D` lists the unpushed commits and only `y` discards | ☐ |
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
| 9.44 | File explorer icons in real fonts | Settings → Theme → File icons → Nerd Font glyphs, in a terminal with a Nerd Font and in one without, on macOS and Linux and over SSH; open Files on a project with `.go`, `.ts`, `.tsx`, `.md`, `Dockerfile`, and on one holding the newer kinds (`.ipynb`, `.xlsx`, `.pem`, `.zst`, `.heic`, `Jenkinsfile`, `.prettierrc`) | With a Nerd Font the `.ts` file shows the TypeScript logo and `.tsx` the React atom, each in its own theme colour, and every row stays aligned (no glyph drawn two cells wide pushing the status letter out); no glyph in the table is a box in a Nerd Font v2 or v3 (they are all in the Private Use Area of the BMP, which is why the Material range is avoided); without a Nerd Font the glyphs are boxes but nothing misaligns; Letters reads everywhere | ◐ R35 2026-09-28: both halves on this Mac in iTerm2 — blank icon column with the terminal's own font, every icon drawn once a Nerd Font's Mono variant was the profile's font (JetBrains first, then MesloLGS, which is the one kept), and Letters reading throughout in between. That was the build before this change, so it proves the modes and the column, not the newer kinds; Linux, ssh and a font that draws boxes rather than blanks are still not run |
| 9.45 | File explorer on a real checkout | `f` on a branch an agent is working on; expand `internal/`, select a file the agent is writing; `a`; `d`; `e`; `.` and `i` in a repository with `node_modules`; `w`; the same on a remote machine 🖥 | The branch's worktree is listed with the agent's files marked `M`/`?`; the preview follows the agent's writes without `r`; `a` puts the path in the agent's prompt (relative to its folder); `d` opens the diff; `e` opens `$EDITOR` in a new tab; `node_modules` only shows with `i`; on the remote every path is the remote's, and an older remote server says to update | ◐ R23 2026-09-24, a scratch repository with a fake agent: `f` listed it with folders first and type icons, `M` on a modified file, `?` on an untracked one and `A` carried up to the folder holding a staged one, header "3 changed". Folders opened a level at a time with the breadcrumb following; the preview showed "README.md · 2 lines · modified" with numbered lines and described a PNG as "binary · image/png · 2.0 KB" rather than printing it; `/` narrowed to matches and said "nothing loaded matches app" before the folder holding it was opened; `y` copied the path; `a` said "no agent is working in this checkout" with none running, and with one running put `internal/tui/app.go` in its prompt. Not covered here: a real agent's writes appearing without `r`, `d`, `e`, `node_modules` and `i`, `w`, and the whole of it against a remote machine |
| 9.5 | Search large histories | A project with 100+ long Claude sessions | Conversation matches arrive within a few seconds; typing stays responsive | ✅ R1 35 MB of Claude history: 163 ms first search, ~10 ms after |
| 9.53 💳🌐 | Share a session with a fresh sandbox | A sandbox with nothing cloned in it: `s` on a local session → `o` → the sandbox (offered as **home folder**) → Start Claude there | The sandbox is offered although it has no project; the conversation lands in `~/.conch/handoff/<agent>-<id>.md` there, the agent starts in the home folder, reads it and says what was done and what remains | ☐ |
| 9.38 💳🖥 | Share a session on another machine | Local Claude session; `s` → `o` → busybox · a project with the same branch checked out → Start Codex | Codex starts on busybox in that branch's worktree, reads `.conch/handoff/claude-<id>.md`, and its prompt says the session was on the local machine; nothing is written locally; an older server on either side says to reload it | ✅ R19 real Claude session handed from this Mac to busybox (linux/amd64): the file landed in the remote checkout, Claude read it, summarised what was done and what remained, and noticed by itself that the code it referred to was not in that checkout; nothing written locally |
| 9.88 💳🖥 | Move a worktree to another machine | A task worktree on the Mac with unpushed commits, staged and unstaged edits, an untracked file and a `.env`, a Claude agent working in it; `T` → busybox → the same project there (then again with busybox lacking the project: **Clone … there**) | The worktree appears on busybox on the same commit with the same staged/unstaged split and files; Claude starts there, reads the handoff and summarises; the Mac's Claude pane closes and its worktree stays; a branch already checked out on busybox is refused with nothing changed; clone over ssh without access fails with git's reason rather than hanging | ◐ R21 every path with a fake Claude (below): clone, move, handover, refusal, unreachable origin. A real Claude reading the handoff not run · R22 2026-09-24, the move for real from this Mac to busybox: `T` → machine → project (the picker offered **Clone aghadge/move-test there…** as well), a confirm naming "1 staged, 1 changed, 2 untracked" and the agent it would continue. On busybox the branch arrived on the same commit with `M  README.md` staged, ` M src/main.go` unstaged, `NOTES.txt` untracked and `.env` with its contents; the agent started there and the Mac's pane closed, its worktree kept with every change. The agent moved had no saved conversation, so the handover leg took the "starts afresh" path; the clone-there option and a branch already checked out on busybox are still only covered by fakes. |
| 9.54 💳🌐 | A sandbox that is not answering yet | Make a sandbox and watch what conch says while it sets itself up; then make several in a row, so that one of them meets a gateway that is not ready or drops the 16 MB copy | It says *waiting for X to answer*, then probes and copies; a dropped connection is tried again (three attempts, two seconds apart) and says which attempt it is on, instead of deleting the sandbox at the first failure; if it really cannot connect, the reason reads *the ssh connection failed (255)* with ssh's own words when it had any | ◐ R31 2026-09-27: the waiting step and a clean create against real Daytona; the retry itself only against a fake ssh that drops two connections, since the drop is not one you can ask for |
| 9.59 💳🌐 | A shell in a sandbox | With a Daytona sandbox `dt` and a boat.dev sandbox `hull` running: `conch sandbox -provider daytona ssh dt`, type a few commands and `exit`; `conch sandbox -provider daytona ssh dt uname -a; echo $?`; `conch sandbox -provider daytona ssh dt false; echo $?`; `conch sandbox -provider daytona ssh -t dt top`; the same three on `hull` with `-provider boat`; `conch sandbox -provider daytona ssh` on a stopped one | The shell is a real terminal (prompt, line editing, `ctrl+c` reaches the sandbox, resizing the window reflows it); a command prints its output and `$?` is its own status (0, then 1); `-t top` draws and `q` ends it; on boat.dev it logs in with this computer's key as `user`; the stopped one says to run `conch sandbox start`; no token is printed | ✓ R37 2026-09-29: on Daytona and boat.dev — a command with its output and status (0, 1, 7), a shell driven through a pty (commands ran, `/dev/pts/N`, `exit` closed it), `-t` got a pty and no `-t` got none, `hull` logged in as `user` with this computer's key, the stopped one said `run: conch sandbox -provider boat start hull`, a wrong provider was refused naming the right one, no token printed. Not run: resizing the window mid-session |
| 9.60 💳🌐 | A task in a sandbox's home | With a sandbox `dt` holding no repository: `conch -m dt task -agent claude "Plan a CLI for tracking expenses"`; then `conch -m dt task -cwd /tmp "…"`; then `t` on `dt`'s row in the TUI, with a prompt and Agent `claude,codex` | The agent starts in the sandbox's home with the prompt already sent and shows under the machine's CLI group, not as a project; the `/tmp` one runs in `/tmp`; nothing asks for a git repository; `-branch` is refused saying the folder is not one; `t` opens a dialog with Prompt, Agent and Attempts only, warns that two attempts share the folder, and starts both in the home | ✓ R37 2026-09-29: `-m dt task` started Claude in `/home/daytona` with the prompt (server log: `exec claude --settings … 'Plan a CLI for tracking expenses'`), under the machine's CLI group with no project; `-cwd /tmp` started Codex there; `-branch` refused; `-m hull task` the same on boat.dev in `/home/user`; `t` on `dt`'s row in the real TUI showed the three-field dialog, warned that two attempts share the folder, and started Claude and Codex in the home. Neither agent was logged in, so no model usage |
| 9.61 💳🌐 | Every provider at once | With a Daytona sandbox and a boat.dev sandbox (one running, one stopped), and a sandbox made in Daytona's own dashboard: `conch sandbox stats`; then unset `BOAT_API_KEY` and run it again; then `conch sandbox stop dt` with no `-provider`, and `conch sandbox -provider boat stop dt` | One table lists both providers' sandboxes with the right state and size, the dashboard one with `-` for ID and label, and a line per provider counts them by state and sums the running vCPUs and memory; without the key boat.dev's machine shows `?`, the line says why, and the command exits non-zero; the stop without a provider is refused asking for one, and the one under boat is refused naming Daytona | ◐ R37 2026-09-29: both providers in one table with the right states and sizes, the counts and running vCPU/memory per provider, a stopped boat sandbox out of the running total, one whose machine was removed shown with `-`, boat with no key and no machine *not set up* (exit 0), with a machine `?` and exit 1, a bad Daytona key's 401 named, `-provider daytona stats` narrowed it; `stop` without a provider and under the wrong one refused. Not run: a sandbox made in Daytona's own dashboard |
| 9.46 💳🌐 | A Daytona sandbox as a machine | With `DAYTONA_API_KEY` set: `conch sandbox -provider daytona create -label dt`; `conch -m dt status`; open the TUI and start a terminal and a fake or real agent in `dt`; close the TUI for longer than 15 minutes and come back; `conch sandbox -provider daytona stop dt`, `conch -m dt status`, `conch sandbox -provider daytona start dt`; `conch sandbox -provider daytona rm dt` with a branch there that isn't pushed | `create` makes a sandbox with auto-stop off, installs conch through Daytona's ssh gateway (a non-interactive `ssh token@ssh.app.daytona.io 'sh -c …'` with piped input and output, which Daytona's docs don't promise) and saves it; the TUI shows it as a machine; the agent is still running after the TUI was away; a stopped sandbox says to run `conch sandbox start` instead of retrying; `rm` lists the unpushed branch before asking; no ssh token is printed anywhere | ◐ R25 2026-09-26: create, install through the gateway (the non-interactive ssh works, 17 MB copied over stdin), `-m`, a pane, a git project, stop/`-m` refused/start with files kept, `rm` listing uncommitted work, all against real Daytona; no token printed. Not run: leaving it more than 15 minutes with the TUI closed, and an unpushed branch (only an uncommitted file) |
| 9.47 💳🌐 | Sandboxes from the TUI | With `DAYTONA_API_KEY` set: `M` → **New Daytona sandbox…** with a label and one `Pass in` variable; start an agent there; `m` → **Stop sandbox…**, then **Start sandbox**; `m` → **Delete sandbox…** with a branch there that isn't pushed; then the dialog again without the key | The new machine appears once conch is set up there, with the flash saying it runs until stopped; Stop asks (naming working agents), the row then says `stopped` and the page offers Start; Start reconnects and the agent's files are there; Delete lists the unpushed branch before asking and the machine leaves the tree; without a key the dialog says so and nothing is created | ◐ R25 2026-09-26: the menus, Stop (spinner, then `stopped` and **Start** on the page), Start (reconnected), **New Daytona sandbox…** (created `dt2` and added it) and Delete (gone from the tree and Daytona) on real sandboxes. Not run: `Pass in`, an agent working when stopping, the dialog without a key, and the "creating…" flash, which did not show (no flash did at the time, `R` included: a status bar bug at about 84 columns, since fixed — see R25) |
| 9.48 💳🌐 | A boat.dev sandbox as a machine | With `BOAT_API_KEY` set: `conch sandbox -provider boat create -label hull`; `conch -m hull status`; a terminal and an agent there from the TUI; `conch sandbox -provider boat url hull 3000` with a server listening on `0.0.0.0:3000`; `conch sandbox -provider boat usage hull`; `conch sandbox -provider boat stop hull` then `start hull`; `conch sandbox -provider boat snapshot -name hull-base hull`, `snapshots`, `snapshots -rm hull-base`; `conch sandbox -provider boat rm hull`; and, on a paid plan, `auto_stop` longer than the plan allows, plus what boat does to a sandbox whose two hours run out | `create` authorizes this computer's public key in the sandbox, installs conch over ssh as `user` to the machine's own address (or its `sshEndpoint` when it has no public IPv4), and saves the machine as `boat:bx_…`; a size in `-cpu`/`-memory`/`-disk` is refused with the named sizes instead; the preview link answers at the sandbox's own subdomain and still does the next time; `usage` shows the seconds and dollars boat reports; a resumed sandbox lands on another machine and conch reaches it without being told; the stopped sandbox costs nothing; the named snapshot appears, deploys a new sandbox with `-snapshot hull-base`, and is forgotten again; a life longer than the plan allows is asked for again at the length the refusal named rather than failing; what boat does when the life runs out is written down here once it is seen; nothing but the key line is sent to boat | ◐ R27 2026-09-26: create, the key, install over ssh to `sshEndpoint`, `-m hull2 status`, `ls`, `rm`; sizes and life. Not run: stop/resume, the preview link, usage, named snapshots, a plan that allows less, and what boat does when a life runs out |
| 9.62 🌐 | A sandbox-cli microVM as a machine | With sandbox-cli's sandboxd running on this Mac (macOS 26, `container system start`), Settings → Sandboxes → sandbox-cli → Network `open`: `conch sandbox -provider sandbox-cli create -label vm1 -env CLAUDE_CODE_OAUTH_TOKEN`; `conch -m vm1 status`; a terminal and Claude there from the TUI, with a long session (well past 8 MB of screen output); `conch sandbox -provider sandbox-cli ssh vm1`, and `ssh vm1 id -un`; close the TUI for an hour and come back; `stop` (refused on a Mac), `rm`; `create -dir` on a project with sandboxd's `allow_bind`; then the same against busybox's sandboxd (Firecracker) through a context, with stop/start; and restart sandboxd with a sandbox up | conch is copied in through an attached process and `conch bridge -listen` is reached through the tunnel; the connection survives a long session (the 8 MB attach cap does not apply to it); the server outlives the process that started it and the sandbox is not idled away with the TUI closed; `id -un` says `sandbox`; the Mac refuses to stop, once, and the idle watch leaves it; on Linux stop suspends and start resumes with the agent where it was; `-dir`'s folder is `/workspace`; after a sandboxd restart the machine shows as gone | ◐ 2026-10-03 against a real sandboxd on this Mac (`--backend macos --allow-bind`, sandbox-cli's context `mac`): `create` made a VM, built linux/arm64 conch, copied 18 MB through an attached process and connected through the tunnel; `-m vm1 status`; `ssh` ran as `sandbox` on Linux aarch64 with exit statuses (0, 7) passed back; the server outlived the process that started it; one connection carried 27 MB of replies (past the 8 MB attach cap) and kept answering; `stop` was refused (no suspend on a Mac); `-dir` mounted a folder both ways; `rm` deleted both. Then from the real TUI (sandboxd restarted with `ceiling: open`, Network `open` in Settings): `M` → New sandbox… → sandbox-cli… made `vm-claude` (2 vCPU, 4 GiB, network open), shown under Sandboxes → sandbox-cli; its page listed the image's agents; `c` → Claude Code started in `/sandbox/home`; its OAuth login completed from inside the VM; the trust question showed as blocked (`⚑ 1 waiting`); a prompt ran (`idle → working → idle` by Claude's own hooks, read from the VM's server log), the pane took Claude's title and the tree its token counts, and the file it wrote ran in the VM. Not run: the TUI closed for an hour, Linux/Firecracker (suspend and resume), a sandboxd restart |
| 9.49 💳 | One setup, every agent | A real checkout with Claude Code set up in it (CLAUDE.md, `.claude/skills/*`, `.mcp.json` with a real server): `i` → `s`, read the question, answer yes; then start Codex, Gemini and OpenCode there and ask each what instructions, skills and MCP servers it has; `i` → `u`; the same over `-m` to a remote checkout 🖥; `conch agent sync -apply` on a repository whose AGENTS.md somebody wrote | The question lists each write with its path; after yes, Codex and OpenCode read the copied AGENTS.md, Gemini follows `@CLAUDE.md`, all three list the skill from `.agents/skills`, and each agent really lists the synced MCP server when asked (the formats are the part fakes cannot prove); a server whose env holds a token is left out and said so; `u` puts the checkout back exactly, `git status` clean; the hand-written AGENTS.md is reported skipped and unchanged | ◐ R28 2026-09-27: every MCP format against each agent's own CLI, Codex reading the copied AGENTS.md for real, Gemini reading the linked skill from `.agents/skills`, undo leaving the checkout as it was. Not run: Gemini's `@CLAUDE.md` import and OpenCode's AGENTS.md with a model (neither is logged in non-interactively here), a hand-written AGENTS.md, and the whole of it over `-m` |
| 9.55 💳 | Your own setup, given to the others | With Claude Code set up in your home (CLAUDE.md, ~/.claude/skills, servers in ~/.claude.json): Settings → Agents → **Give the others Claude Code's setup…**, read the question, answer yes; then ask Codex, Gemini and OpenCode what they load, anywhere; then **Put the last one back…**; and again with one of the files symlinked into a dotfiles repository | The question names every path in full and warns there is no git status; afterwards each agent's own CLI lists the synced server and skill wherever you are, not only in one checkout; Claude's ~/.claude.json is untouched; putting it back leaves the home as it was, and the record is under ~/.config/conch/agent-sync; a symlinked file is skipped with "a dotfiles repository?" and the repository is unchanged | ◐ R33 2026-09-27: the plan, apply and undo in a scratch home, `codex mcp list` showing the server from ~/.codex/config.toml and `gemini skills list` the skill from ~/.agents/skills, and the symlink guard by test. Not run: a real home of somebody's own, and OpenCode's CLI (its login is expired here) |
| 9.56 💳 | Each agent's own way of naming a variable | A real server needing a token (e.g. GitHub's, `GITHUB_TOKEN`) and an HTTP one with `Authorization: Bearer ${TOKEN}`, declared for Claude Code; `conch agent sync -apply` to Codex, Gemini, OpenCode and Devin (with Devin's `read_config_from.claude` off, so it gets its own file); then in each agent list the servers and use one | Codex's config.toml has `env_vars = ["GITHUB_TOKEN"]` and `bearer_token_env_var`, no `${…}`, and the server authenticates; OpenCode's has `{env:GITHUB_TOKEN}`; Devin's `.devin/mcp_config.json` has `${env:GITHUB_TOKEN}` and `transport: "http"`, and `devin mcp list` shows both working — the docs only promise `${env:…}` in its OAuth fields, so this is the check that it works in `env` and `headers`; Gemini lists the HTTP server from `httpUrl`. A server whose variable is renamed (`GITHUB_PERSONAL_ACCESS_TOKEN=${GITHUB_TOKEN}`) is left out for Codex, with the reason | ☐ |
| 9.57 💳 | Devin reads the others' setup | In a checkout set up for Claude Code: `i` → Devin tab; start Devin there and ask what instructions, skills and MCP servers it has; then set `read_config_from.claude = false` in `~/.config/devin/config.json` and ask again | The tab lists CLAUDE.md, `.claude/skills` and `.mcp.json` as "Claude Code compatibility", matching what Devin says it loaded; with the setting off they leave the tab and Devin's answer alike; a sync to Devin writes nothing while it reads Claude's, and gives it its own files once it doesn't. Also check with `devin mcp list` whether a server declared both in `.mcp.json` and `.devin/mcp_config.json` shows twice | ☐ |
| 9.58 💳 | The library | Settings → Agents → **Shared MCP servers & skills…**: take Claude Code's into the library, add a server with a `${VAR}` and a skill folder, tick Codex, Gemini and Devin, **Apply…**; ask each agent what it loads; untick Codex and apply; remove the server and apply; **Put the last one back…**; the same with `conch agent library` | The page shows a column per agent and the question lists every write; Claude's server is added with `claude mcp add-json --scope user` and `claude mcp list` shows it, while `~/.claude.json` keeps its project history; Devin is not given a server Claude already has; unticking takes only conch's copy out, leaving the rest of `config.toml` and any hand-declared server as they were; a shared `~/.agents/skills` link stays while another agent wants it; undo puts it all back, Claude's through `claude mcp remove` | ☐ |
| 9.50 | A login URL an agent printed | An agent in a sandbox (or over ssh) running its login: `claude /login`, `codex login`, `gemini` — whatever prints a URL and asks you to open it; then `ctrl+b u`, and `m` → **Open a link it printed**; also with the URL scrolled off (`ctrl+b [` first), and in a narrow pane where the box wraps it twice | The menu lists the URL whole — no border, no line break, nothing missing — opening it signs the agent in and the clipboard holds the same text; a pane with no links says so; prose under a link is not glued to it (or, when it is, it is plain in the menu before anything opens) | ☐ |
| 9.51 | What a long job is doing | `M` → **New sandbox…** on a provider that works, and `M` → **Over ssh** to a machine with no conch on it yet: watch the status bar for the whole minute or two | A line with a spinner says what it is doing and keeps saying it — asking the provider, the sandbox is up, probing, building, copying N MB, connecting — until the machine appears or a reason does; it is not a flash that fades after four seconds, and nothing else takes the line while it runs | ◐ R29 2026-09-27: the flow and the refusal (boat's plan had lapsed, so the create was refused in a second: the notice read "boat.dev wants a plan or a payment method…" once, not twice); a fast add showed only its "added" flash. Not run: watching the line through a slow install, which is the point of it |
| 9.52 | Conch's own folder deleted under it | On a machine or sandbox with conch running: `rm -rf ~/.config/conch`, then start an agent (Claude, Gemini, OpenCode) from the TUI; then `conch -m ID status`; then a second server check (`ps` for `conch server`) | Starting the agent writes the files it is launched with again, so Claude does not fail with "Settings file not found"; the next connection starts a fresh server, which recreates the folder; the old server is left orphaned holding its panes, so its pid has to be killed by hand — nothing pretends those panes are still reachable | ◐ R29 2026-09-27: seen on a real Daytona sandbox — the binary in ~/.local/bin survived, `status` started a fresh server and the folder came back, and two servers were running (the orphan holding one shell). The launch-time rewrite is covered by tests; not yet tried on a real agent after the folder went |
| 9.89 | A prompt theme as it really looks | On a computer with Oh My Zsh: Settings → Theme → **Shell prompt**, in a wide window and a narrow one; pick one and open a new zsh terminal; and against a local server built before this change | Each theme shows the prompt it draws, in its colours — agnoster's powerline segments, a two-line theme on one line, a long one cut with `…`; the terminal that opens draws the same prompt; a theme zsh could not expand shows nothing rather than `$(git_prompt_info)`; an older server simply shows the names, as before (the list is the local computer's either way) | ◐ R34 2026-09-28: the expansion itself against this Mac's 143 real themes (agnoster, bira, fino, gnzh, cloud, af-magic, minimal, robbyrussell) — see R34. Not run: the rows in the TUI at either width, opening a terminal to compare, and an older server |
| 9.90 | Dragging a selection past a pane's edge | In iTerm2, Terminal.app and Ghostty: in a shell with a long history, and in a real agent's pane after more than a screenful, drag from mid-pane up over the tab bar and hold; then down over the status bar; then turn the wheel with the button held; release. In a diff and on the Branches page, drag over some lines and release; click a row without dragging | The terminal keeps reporting the held drag outside the pane: the history scrolls while the pointer stays above or below, the wheel scrolls mid-drag, and the clipboard has every line passed over, not only the ones on screen. On a page the dragged text is copied and a plain click still opens its row |  ☐ |
| 9.91 | History of an agent's full-screen interface | With Claude Code in its full-screen mode (prompt box fixed at the bottom), have it print a long answer; then wheel up and down over it a few times; then drag a selection from its pane up over the tab bar and hold | conch has kept the conversation as it scrolled above the prompt: the drag scrolls back through it, in order, with no line repeated by the agent's own scrolling, and the copy has every line passed over |  ☐ |
| 9.62 🖥 | The git window on a branch | On a project with a remote and an agent working on a branch: open it every way — click the branch row, `b` and `$` on it, `$` in its changes, **Git panel** from `m` — then type `status -sb`, `log -5 --stat`, `diff -- '*.go'`, `switch -c spike`, `commit -m 'fix: the "quoted" bit'`, and the refusals: `-C /tmp status`, `-c user.name=x log`, `commit` with no `-m`, a `fetch` against a host needing credentials. `tab` and run Status, Log, Diff stat, Fetch, Pull, Push, Amend, Stash, Pop stash, Stashes, Branches, Rebase on, Merge in, Undo last commit and Discard changes…. Make a real conflict (rebase on a base that touched the same line) and use **Continue**, **Abort** and **Ask it to resolve**. `ctrl+t` and send the agent a message, then again while it waits on a question. Click another branch with the window open. At 120, 90 and 40 columns, and on a 12-row window. Then the same over `-m` to a remote 🖥, and against a server built before `branch.git.v1` | Every command runs in that branch's worktree and nowhere else; a leading `git` is optional; quotes group words but nothing is expanded, so `*.go` reaches git as typed; options before the subcommand are refused rather than reaching another repository; nothing hangs waiting for an editor or a password — `commit` with no `-m` stops, the fetch fails with git's own words; the window holds only what the commands printed, 500 lines of it, and clicking another branch moves it there; `tab` swaps output for the actions and back, amend, undo and discard ask first and discard takes only `y`; a stopped rebase or merge says so above the output and the actions start with Continue and Abort; `ctrl+t` sends to the agent instead, an agent waiting on a question is sent nothing and the window says why; at 40 columns it takes the whole width and no line overflows; an older server still opens it with push, commit and messages for the agent, and says typed git commands need it reloaded | ☐ |
| 9.63 | Dragging a tab to reorder it | With four tabs open in one group, in a real terminal: press on the last tab and drag it slowly to the front, then to the end past the `+`, and let go there; drag one a single cell at a time across a much narrower neighbour; drag off the bar into a pane and back before letting go; let go outside the window; drag with only one tab in the group; drag a preview (a machine row's page); the same in iTerm2, Ghostty and over ssh 🖥 | The bar reorders under the pointer as it moves and the tab ends where it was let go; it never swaps back and forth while the pointer is held still over it; off the bar nothing moves and the drag is still live when the pointer returns; a release anywhere ends it and the order is saved (it survives closing the TUI); one tab and a preview cannot be dragged and nothing else happens; no terminal leaves a stuck drag after the mouse is released outside the window | ☐ |
| 9.64 🖥 | conch's skill from the TUI | `,` → **Agents** → **conch's own skill…** on a computer with the agents installed and a remote machine 🖥 in the tree: read both machines' rows; open each; install for every agent and read the question before answering it; ask an agent (`claude`, then `codex`) what skills it has; put your own skill named `conch` in one agent's folder and look again; **Take conch's copy away…**; then with a machine offline, and one whose server predates `agent.skill.v1`. Then `i` on a branch: read the **conch** line on each agent's tab, press `S` on one that hasn't got it, answer the question, and `S` again to take it away; `S` on an agent that reads no skills, and on the skill of your own | Each machine's row says how it stands without opening it; the question names every path it would write and nothing is written until it is answered; afterwards each agent lists the skill by its own command, and Claude Code has it mid-session; a skill of that name conch did not write is left alone and says so rather than counting as installed; removing takes only conch's copies; an offline machine and an older server each say why on their own row and the rest of the page still works | ☐ |
| 9.65 💳 | Prompting an agent from the tree | With a real agent working in a pane: `m` on its row → **Prompt…**, send it a small job and watch the status bar; do it again with that pane not on screen (another tab), and with notifications on; send while the agent is mid-work; send while it is waiting on a permission question; close the pane before it answers; the same on a remote machine 🖥, and on one whose server predates `agent.prompt.v1` | The message goes in as the agent's next one, exactly as typed; conch says *finished what you asked* when that work ends and notifies when the pane is not in view, once; a message sent mid-work is delivered when the current work ends and the report follows that turn, not the one already running; an agent waiting on a question is refused in the server's own words and nothing is typed over the dialog; a pane that ends first reads *ended before it answered*; an older server says so in the menu rather than failing | ☐ |
| 9.66 💳 | Who started what, in the tree | With a real agent that starts helpers (`conch agent prompt` from inside its pane, or its skill): watch the tree while a helper starts, works, waits and ends; `Q` and `!` while a helper waits; a notification with the pane off screen; then end the agent that started them and look again; the same on a remote machine 🖥 | The helper's row says ↳ for the agent that started it and the creator's row counts them (⑂2), turning amber with ! the moment one waits; the queue row, the jump and the notification all name the same agent; a helper that ends is no longer counted; when the creator is gone its helpers stop naming it rather than pointing at a pane that is not there | ☐ |
| 9.67 | Rearranging splits with the mouse | In a real terminal with three splits — an agent, a branch's changes scrolled part way, and a file explorer in a folder — drag each one's title onto another and let go; drag onto itself; let go over the tree, the status bar and outside the window; drag while an agent is writing to the pane; try it zoomed (`ctrl+b z`) and with one split only; then the same over ssh 🖥 | The two swap and nothing else moves: the splits keep their sizes, the diff its scroll, the explorer its folder, and the agent keeps running throughout; while the button is held the split being carried is marked and so is the one under the pointer, and after the release both stay marked for about a second and then go back by themselves; dropping on itself or anywhere that is not another split changes nothing and clears the mark; zoomed or alone, no drag starts; a resize on a border still resizes rather than swapping | ☐ |
| 9.68 | The scrollbar on a pane | In a real terminal: an agent with thousands of lines behind it — drag the thumb from the bottom to the top and back, click part way down the track, drag past the top and bottom edges, and drag while the agent is still writing; then in a two-column split (the shared border), in a tall pane and a 6-row one, zoomed, and over ssh 🖥 | The thumb's size says how much history there is and its position where the screen is; dragging moves through it smoothly and letting go leaves it there; the bottom is live and new output resumes following; a drag past either edge stops at that end; the shared border between two splits still resizes rather than scrolling; a pane too short for a thumb simply has none, and no line ever overflows its border | ☐ |
| 9.69 | Hover, and what it costs | Turn on Settings → Theme → **Light what the pointer is on**, quit and start conch again: move the pointer through the tree, across the tab bar and over panes; watch CPU (`top`) while moving it fast for a minute; do the same over ssh to a machine across the internet 🖥; with vim or an agent that takes the mouse in the focused pane, move over it and check the program does not react; then turn it off again | The row and tab under the pointer are tinted and nothing else changes; the text does not move; CPU stays where it was without hover, and over ssh the link is not visibly busier — if either is, that is the finding and hover stays off by default; a program that takes the mouse sees nothing until a button is held; turning it off stops the reports at the next start and the setting says so | ☐ |
| 9.70 💳 | The plan usage window | With Claude and Codex both used today, and a remote machine 🖥 that has run an agent: click the plan chip in the status bar; read it against `claude /usage` and Codex's own report; leave it open past a 5-hour reset; click the chip again and press `esc`; then with a window at 100%, one whose reset has passed, and on a machine where no agent has reported | The window opens at the bottom right beside the chip, its bars filling as it does, and names every agent that has reported on every machine with every window that agent named — including a weekly allowance for one model, which conch passes on without knowing the name — each with a bar, percentage, when it resets (both how long and at what clock time) and when the agent last said so; the numbers match what the agents themselves report; a window at 100% says *used up* and one past its reset says the window has turned over rather than showing a stale number as if it were live; what conch has seen the machine spend is shown apart from the plan windows, says it counts whole conversations rather than the run in progress, and breaks the tokens into prompt, cache read, cache write and output so it can be reconciled with `claude /usage` line by line — the cost there will be far smaller when that panel's session is minutes old, and that is not a fault; clicking the chip again and `esc` both close it; nothing overflows at 200, 100, 60 and 40 columns | ☐ |
| 9.71 📱 | Pair a phone over Tailscale | `conch web`, `tailscale serve --bg` in front of it, `conch web pair`; open the `https://…ts.net` address in iOS Safari and Android Chrome, enter the code | The page loads over HTTPS, the code pairs, the agents are listed; the cookie is sent (it is `Secure`) and a reload stays paired. `tailscale serve` passes `Host` and `Origin` through, so the socket's same-origin check lets the phone in | ☐ |
| 9.72 📱💳 | Answer a real permission prompt | A real Claude Code waiting on a Bash permission, and Codex on an approval; `POST /api/answer` with each choice in turn from a paired device | The choices read off the real screen match what the agent shows; the cursor lands on the chosen row and one Enter picks it; nothing reaches whatever the agent asks next. Agents whose menus take no arrow keys, or show no cursor, come back with no choices rather than a wrong key | ☐ |
| 9.73 📱 | Revoke a phone mid-session | The phone's page open on the agents list; `conch web revoke ID` on the laptop | The phone's connection closes within a second and its next request is refused; another paired phone is untouched | ☐ |
| 9.74 📱 | The laptop sleeps and wakes | Pair, close the lid for a minute, open it | The gateway is still listening on the Tailscale address, the phone is still paired and the list loads again without re-pairing | ☐ |
| 9.75 | `conch web` with no Tailscale | On a machine with Tailscale stopped: `conch web`, then `conch web -listen 127.0.0.1:8722` | The first refuses and says why; the second starts and prints the warning | ☐ |
| 9.76 📱 | The app on a real phone | After 9.71 on iOS Safari and Android Chrome: add to the home screen and open it from there; open an agent; rotate; reply; start a task with **＋** | It opens without browser bars, with the icon and name; the list follows agents changing on the laptop within a second; an agent's screen is readable and scrolls sideways when wide; the keyboard doesn't cover the reply box; the new task's agent opens | ◐ R38 in headless Chrome at 390×844 only: pairing from a `#code=` link, the list, an answer by button, a reply, the new-task form, light and dark. No real phone, no home screen, no task started |
| 9.77 📱 | The app while the laptop sleeps | With the app installed and paired: close the laptop's lid, open the app; open the lid | The app opens from the home screen, says the laptop can't be reached and when it last was, and shows the agents from then; once the laptop is back the banner goes and the list is live again, without re-pairing | ◐ R38 with Chrome's network switched off and on: opened from the service worker's copy, banner with the time, caught up by itself. A real sleep not run |
| 9.78 📱💳 | The terminal from a phone | On a real phone with a `full` device: open a Claude agent's **Terminal**; answer a question whose choices the agent page can't offer (Gemini's confirmation, a Codex approval) with the arrows and ⏎; type a line and ⏎ it; shift+tab through Claude's modes; ^C once, then twice | Every key does what it does at the laptop; typed text arrives as typed, accents included, with no Enter of its own; one ^C tap sends nothing and two stop the agent; the key bar stays above the phone's keyboard | ◐ R39 in headless Chrome with a stand-in agent: arrows and ⏎ picked choice 3, a typed line and ⏎ arrived, one ^C sent nothing (5 key messages at the gateway), two sent it (6). No real phone or agent |
| 9.79 📱🌐 | A real push, on iOS and Android | The app on the Home Screen (iOS 16.4+) and in Android Chrome: ⚙ → Turn on notifications, tick *Also when an agent finishes*; lock the phone; make an agent ask a question, then another finish while nobody watches it in the TUI | Each arrives within seconds on the lock screen, saying the agent and project and nothing of the question; tapping opens that agent's page, with the app already open and with it closed; an agent already waiting when `conch web` started sends nothing; Turn off stops them | ☐ Not run: pushes were tested against a loopback push service and the RFC's example only. In headless Chrome the settings page rendered, but a push delivered through the debugging port never reached the worker, so the notification itself is unseen |
| 9.80 📱🌐 | Pushes after the phone forgets | Turn notifications on, then remove the app from the Home Screen (or clear the site's data) and make an agent wait | The push service answers 404/410 and conch drops that subscription from `phone.json` (`conch web devices` still lists the device) | ☐ |
| 9.81 📱 | Pair by QR code | `conch web -url https://<name>.ts.net` behind `tailscale serve`; `P` in the TUI in an 80×24 terminal, in both a dark and a light theme; scan with the iPhone and Android camera; then `conch web pair` in a terminal and scan that | The camera offers the link, it opens conch with the code filled in, one tap pairs; the code reads in both themes (it is drawn dark on light whatever the theme); `f` in the dialog gives a code that pairs as full | ☐ The drawing is checked module for module against the encoder, but no camera has read it; Chrome's barcode detector isn't in headless Chrome |
| 9.82 📱 | Type into a terminal on a real phone | iPhone Safari (Home Screen) and Android Chrome: open a terminal, tap it, type a command with autocorrect-prone words, an accent, an emoji; paste two lines; use ctrl (once and locked), alt, the arrows, ^C; rotate | Each key arrives once and as typed (no autocorrect, no capital first letter), backspace deletes one character, the key row stays on top of the keyboard, paste arrives as a paste, ctrl locks and unlocks | ◐ R41, R45 in headless Chrome; 2026-10-01 Amit typed into terminals from his iPhone (Safari, over Tailscale), after the R45 fix. Android, paste, ctrl/alt and rotation not yet run |
| 9.83 | Copying a line an agent wrapped | In a real terminal, with an agent that printed a long shell command (longer than the pane is wide): drag across it and paste into a shell; triple-click it and paste; do the same for a command exactly as wide as the pane, for one with a wide character (CJK, an emoji) at the wrap, for output with trailing spaces, and for several separate short lines; then in a narrow split and over ssh 🖥 | The command pastes as one line and runs; the rows it wrapped over are joined without a newline between them, and genuinely separate lines keep theirs; a line exactly the width of the pane is joined with what follows it, which is the price of the emulator not recording continuations and is worth knowing about; a wide character at the edge may leave a wrap in — the rarer mistake, and the one that costs nothing but a rejoin by hand | ☐ |
| 9.84 | Clicking a link an agent printed | With a real agent mid-login (`claude /login`, `codex login`) in a pane: click the URL it printed; click the words beside it; alt+click it inside the agent's interface and plain-click it in a plain shell pane; click a link wrapped over several rows and one in a narrow split; then the same over ssh 🖥 and in iTerm2, Ghostty and Terminal.app | The link opens in the browser and is on the clipboard, whole, however many rows it wrapped over; a click beside it does nothing; inside an agent's interface a plain click still reaches the agent and only alt+click opens the link; every terminal passes alt through (if one does not, that is the finding, and ctrl+b u is the way there) | ☐ |
| 9.85 📱 | A pane that fits the window it is shown in | Open `conch web` in a desktop browser on a pane the laptop made narrow (120 columns or so): watch it widen; then open the same pane in the TUI and watch what happens to both; make the browser window small and large again; open it on a phone beside the desktop browser; and with a device paired `view` | The pane grows to about what the window could show and the text stays readable rather than enormous; the TUI, if it is showing that pane, takes the size back and nothing oscillates — each side asks once; shrinking the browser window does not shrink the pane; a phone opening the same pane never makes it smaller for the laptop; a `view` device is refused with that reason | ☐ |
| 9.92 | The explorer when the branch changes | With `f` open on a branch's worktree, a folder expanded, a file selected and the search narrowed: move the tree cursor to another branch of the same project and open its explorer, then back; do it with a branch whose worktree is somewhere else, one an agent is writing to, and one with no worktree yet; then the same on a remote machine 🖥 | Each switch lists the branch it was opened on: its own worktree's folders at the top level, the breadcrumb back to that root, paths that belong to that worktree and no expansion or search carried over from the last one; the preview shows the new branch's file or nothing rather than the old one's contents; going back is the same again; a branch without a worktree says so instead of listing the project's |  ☐ |
| 9.93 | Selecting inside one split, not its neighbours | In a real terminal with a file explorer in the left column and an agent or a preview in the right: drag across several lines of the right-hand pane, starting past its first column and ending past its last; drag from the left pane into the right and back; triple-click a line in each; open a file in the explorer and drag down several of its numbered lines, and again starting on the numbers themselves; then with three splits, with the panes swapped (9.67) and over ssh 🖥 | What is copied holds only the lines of the pane the drag started in — no column of the neighbour, no border and no row of the pane above or below — and a drag that leaves the pane stays bounded to it rather than taking what it crosses; triple-click takes that pane's line alone; a file's lines paste without the numbers the preview draws them with — a command copied out of a file runs — while a drag begun on the numbers keeps them; what lands on the clipboard matches what is on screen, as 9.83 asks of a wrapped one |  ☐ |
| 9.94 | Carrying a split to another tab | In a real terminal with three tabs in one group, one of them split in two: press a split's title and drag it along the tab bar, watching the tab under the pointer; let go on another tab, on the tab it came from, on the `+` (from a tab of two splits and from a tab of one), on the gap between tabs and off the bar entirely; then carry the only split of a tab away, and try it with one tab open and while zoomed (`ctrl+b z`); then the same over ssh 🖥 | The tab under the pointer is marked while the button is held and no split is; letting go on another tab moves the split there, shows that tab with the split focused, and closes the one it left if that was its last split — the agent in it keeps running throughout; letting go on the `+` gives the split a tab of its own beside the one it left, unless it was that tab's only split, when it stays; letting go on its own tab, the gap or off the bar moves nothing and leaves no tab behind; with one tab and nothing to carry to, no drag starts; zoomed, none starts either |  ☐ |
| 9.95 | The tree grouped by tab | On a project with agents and terminals, some open in tabs and some not: Settings → **Tree grouping** → **By tab**; rename a tab (`ctrl+b ,`), split one, close a tab, open a pane that was in none, and carry a split to another tab (9.94); then a tab holding panes from two projects, one holding a branch's changes rather than a pane, and the setting back to Agents and Terminals | Each tab is a section named as the bar names it — its number among that project's tabs, its name, `⊞` when it holds splits — holding the panes open in it; panes in no tab keep Agents and Terminals below; renaming, splitting, closing and moving are followed without a restart; a tab of two projects' panes is listed under neither and its panes keep their sections; folding is remembered; the setting back leaves the tree exactly as it was |  ☐ |
| 9.96 | Carrying a tree row into a tab | With the tree grouped by tab (9.95), in a real terminal: drag an agent's row onto another tab's section; drag a terminal that is in no tab onto one; drag onto a branch, a section that is not a tab, and off the tree entirely; drag the last pane of a tab onto another; press a row and let go without moving; then over ssh 🖥 and with the grouping off | The section under the pointer is marked while the button is held; letting go on it puts the pane in that tab and shows it, the pane still running; a pane that was in no tab is opened there and leaves its Agents or Terminals section; the tab a pane was the last split of closes; letting go anywhere else moves nothing; a press that does not move still selects and opens the row as it always did; with the grouping off no drag starts |  ☐ |
| 9.97 | Folders of your own in the tree | In a project with several terminals and agents: `N` on Terminals, name it `eng`, drag two rows in and one back out; `N` on Agents and on SSH; a second folder of the same name; rename a pane in a folder (`r`); give two terminals the same name and put one in a folder; close a pane in one; restart the server (`conch server stop`, then open conch again) and look; `x` on the folder; then the same on a remote machine 🖥 and with the tree grouped by tab | A folder is listed in its section with what is in it and the rest below; a row dragged in leaves the list and one dragged onto the section comes back; a folder of another section refuses the row; a second folder of the same name is refused by name; a renamed pane stays where it was put; a closed pane leaves no gap and the folder stays; two panes of one name are told apart by the folder while they run, the other staying where it is, and after a restart the folder keeps neither, having nothing to choose by; after a restart the folder has found its other panes again by name, and a pane that never had one is loose; `x` removes the folder and nothing closes; folders survive a conch restart |  ☐ |
| 9.98 💳 | A server's memory with busy panes | On a build from before this change, run three or four agents (Claude Code, and Codex or OpenCode for the alternate screen) and a terminal running `yes "$(printf '%0120d' 0)" \| head -200000`, at about 120 columns, until each has printed well past ten thousand lines; note the server's memory (`ps -o rss= -p $(pgrep -f 'conch server')`). Reload onto this build (6.1) and repeat; in each pane `ctrl+b [` back to the top, search (9.42) and select across colours and a link | The server holds a few MB a pane rather than 100 MB or more; scrolling back shows the same history as before, in colour, in order and to the same depth (ten thousand lines on the main screen), and the reload keeps it; ESC[3J (`clear` in most shells, `/clear` in Claude) empties it; a pane narrowed after printing shows its older lines cut to the width rather than wrapped |  ☐ |

## Driving a TUI under test

Keys go in with `conch send -keys ID KEY` (the flag before the id). Clicks
need `internal/tools/clicker`, which sends `pane.send_mouse` through the
protocol — `conch send` types text, and a raw mouse escape sequence is
encoded as text rather than passed through:

```sh
go run ./internal/tools/clicker "$CONCH_SOCKET" p1 109 39      # click
go run ./internal/tools/clicker "$CONCH_SOCKET" p1 10 3 60 0   # drag: press, move, let go
```

A drag is three events, so a press and a release alone are not one: the rows
for dragging a tab, a split or a scrollbar need the second pair of cells.

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

### R45 — 2026-10-01, typing on an iPhone, as iOS leaves the field, in headless Chrome, macOS arm64, build 0.1.6-dev

Amit, on his iPhone: typing into an idle agent's terminal did nothing. The gateway's log showed the keystrokes arriving — almost all as `keys` of one backspace each. iOS Safari puts the cursor in front of the zero-width sentinel the hidden field keeps, so each letter landed ahead of it, and the app took "the field doesn't start with the sentinel" for "the sentinel was deleted". Now what was typed is read from either side of the sentinel (`ttyInput`), and the cursor is put back after it each time. Re-run with the cursor forced to the start before every keystroke: "hello agent" reached an idle stand-in agent and `echo typed-on-ios` ran in a real shell — 31 `text` messages and 3 `keys` (two Enters, one deliberate backspace) at the gateway. **Confirmed on Amit's iPhone afterwards: typing into a terminal from the phone works.**

### R44 — 2026-09-30, scrolling an agent's own view, in headless Chrome and on this Mac's own pane, macOS arm64, build 0.1.6-dev

Amit: the agent window couldn't be scrolled up as on the laptop. A read-only look at a real Claude Code pane (this session's own) showed why: it runs on the alternate screen with mouse reports on, and conch had kept only 20 lines of it — Claude keeps its conversation itself, and the laptop scrolls it by passing the wheel on. Frames now say `mouse`, and the socket takes `wheel`. With a stand-in that behaves the same (alternate screen, SGR mouse, 200 lines, 3 per wheel step), at 390×844 with touch: four pulls down at the top of the box took its view from line 171 to 39, six wheel turns to line 1, wheel down at the bottom brought it forward to 34. Not yet: the real Claude Code on the iPhone, whose wheel step and screen layout differ from the stand-in's.

### R43 — 2026-09-30, scrolling back, in headless Chrome, macOS arm64, build 0.1.6-dev

Amit: the agent chat could not be scrolled up — it showed the last 24 rows of the screen and nothing else. With the socket's new `scroll` and the stitched history: a shell after `seq 1 300` and a stand-in agent after 60 typed messages, at 390×844. Scrolling the terminal to its top again and again loaded page after page until the first line; all 300 numbers were there once each and in order, the "Earlier output" line went, and a command typed afterwards still landed at the bottom, once. In the chat the first of the 60 messages, far above the screen, came into reach the same way, all 60 present. Pairing now defaults to `full` (`conch web pair`, `P`).  Not on a real phone yet: momentum scrolling on iOS while a page is being put in above.

### R42 — 2026-09-30, the terminal with the keyboard up, in headless Chrome, macOS arm64, build 0.1.6-dev

Amit, on his iPhone: "the terminal keyboard covers the screen and not able to type" — the phone was paired as `reply` (typing needs `full`), and the key row sat fixed over a screen that didn't shrink. After the fix, a shell with `seq 1 120` in it, at 390×844, then with the visible height cut to 504 as a keyboard does: the terminal ended exactly at the keyboard, the key row just above it, the last prompt in view, the page not scrolling; `echo still-here` typed with the keyboard up and ran. `conch web permission ID full` raised a paired device, taking effect on its open socket (tested). Not yet on the iPhone: the real keyboard, and iOS scrolling the page for a focused field.

### R41 — 2026-09-30, the app rebuilt after the Claude app, in headless Chrome, macOS arm64, build 0.1.6-dev

Amit asked for a UI like the Claude mobile app with direct terminal access and everything else. A research pass over the Claude app, Happy (its source), Blink and Termius gave the palette, fonts, the drawer, the composer, inline approval and the key row. Checked at 390×844, light and dark, against an isolated conch with the stand-in agents and a real `/bin/sh` terminal:

- The inbox led with "One agent needs you" and its question with three choice buttons in the card; the drawer listed the project's two agents and the terminal.
- In the conversation, choice 2 was answered (the stand-in printed *chose 2*), the question left, a reply typed in the composer arrived on the agent's screen.
- ⋯ offered Terminal, Rename and Close. ＋ → Terminal opened a login shell and the app went to its screen.
- **Typed directly into the shell:** `echo hi-from-$((20+22))` and Enter from the (emulated) phone keyboard gave `hi-from-42`. `echo oops`, then **ctrl** on the key row (lit once) and `u` cleared the line — ctrl went back off — and `echo clean` ran alone.
- Nothing wider than the phone on any page.
- **Found and fixed:** the drawer and the inbox's sections printed `null` and `[object HTMLHeadingElement]` — lists given straight to the DOM; a waiting row inside the inbox card stacked vertically, its state's class clashing with the container's.
- **Not covered:** a real iPhone or Android keyboard (autocorrect, dictation, the key row riding on the keyboard through `visualViewport`), stop (■) on a real working agent, rename and close from the menu, starting an agent or a task from the sheet.

### R40 — 2026-09-30, the app's redesign in headless Chrome, macOS arm64, build 0.1.6-dev

After Amit found the first design "very bad" on his phone. Checked at 390×844 in both schemes against an isolated conch with the stand-in agents: a header with a back arrow, the title and a live dot; agents as cards with a state pill and the question quoted; on an agent, its question first with each choice a large numbered button, then its latest output wrapped at a readable size, and the reply box pinned to the bottom; the terminal at 10px, scrolling sideways, with its empty rows dropped and a text-size chip (10, 12, 8, fit width); settings in grouped cards. No page wider than the phone. **Found and fixed:** a button's own display brought back what the page had hidden (Settings showed *Turn off* with notifications off). Not yet seen on the iPhone itself.

### R39 — 2026-09-30, the terminal view in headless Chrome, macOS arm64, build 0.1.6-dev

As R38: an isolated conch, `conch web -listen 127.0.0.1:18722`, the stand-in `claude` asking a three-choice question, Chrome headless at 390×844.

- From the agent's page, **Terminal** opened `/agent/p1/terminal` with all 40 rows of the screen and the ten keys; nothing wider than the phone.
- ↓ moved the stand-in's cursor to row 2, ↓ again to 3, ⏎ picked it: *chose 3*.
- One ^C tap turned the key red and read *again*; the gateway's log showed no key message for it, and after two seconds it went back. Two taps sent it.
- `git status -s` typed from the line arrived as typed, with no Enter until ⏎; the line cleared.
- Back to the agent's page, the screen went on following the pane: the frame was closed by the terminal and opened again by the page, in that order.
- **Found and fixed:** the Type button wrapped under the line; links showed the browser's visited purple.

### R38 — 2026-09-30, the phone app in headless Chrome, macOS arm64, build 0.1.6-dev

An isolated conch (its own `HOME`, `CONCH_HOME` and socket, `CONCH_PANE_ID` unset), `conch web -listen 127.0.0.1:18722`, and two panes running a stand-in program named `claude` — one asking a three-choice question. Chrome was driven headless over its debugging port at 390×844, mobile, in both colour schemes. No Tailscale, no phone, no real agent.

- **Pairing:** a link ending `#code=…` filled the code in; submitting paired (Chrome accepts the `Secure` cookie on loopback) and the code left the address bar.
- **List and agent:** both agents under their project, the waiting one first with its question; the agent's page showed the question, three buttons and the screen with its colours. No page was wider than the phone.
- **Answer and reply:** the third button moved the cursor two rows and pressed Enter — the program reported choice 3 — and the question left the page by itself. A reply then appeared on the agent's screen.
- **Offline:** with the network off, a reload opened the app from the service worker's copy, with the banner and the list from before; with it back on, the banner went without a reload. The cache held the app's files and nothing from `/api`.
- **Revoked:** `conch web revoke` from the laptop put the app back on the pairing form and cleared what it had kept.
- **Found and fixed:** the agent's heading ended in the word `null`; a terminal's forty rows were drawn though three had text.
- **Noted for 9.72:** the first stand-in read each burst of keys as one key and so missed the answer's — its own bug, but a real agent that drops keys arriving together would show the same way.
- **Not covered:** a real phone, the home screen, Safari, `tailscale serve`, starting a task from the form.

### R37 — 2026-09-29, providers named, stats, ssh and tasks without git, macOS arm64, build f3ffe4b1431c

Rows 9.59–9.61 against real Daytona (`us`, default snapshot) and boat.dev,
with a scratch `CONCH_HOME` holding only the `[sandbox]` keys, and the TUI
run inside a pane of a second scratch server. Both sandboxes were deleted
at the end and both accounts listed nothing.

- **Create:** Daytona `dt` in 8 s (install cross-built, 16 MB), boat.dev
  `hull` in 13 s, each with `-provider` given.
- **ssh:** the gateway runs a command, passes its exit status, and gives a
  pty with `-t`; a shell typed into through `script` ran what was typed.
  Getting fresh access first took about 1 s, once 12 s — typing sent before
  ssh has the terminal is thrown away when it goes raw, as with any ssh.
- **Tasks without git:** neither sandbox's home is a repository, and
  neither mattered: Claude, Codex and a two-agent `t` all started there
  with the prompt.
- **stats:** see row 9.61. Right after `rm`, Daytona still listed the
  deleted sandbox as `started` with no machine for a few seconds; the next
  stats had it gone.
- **Found and fixed:** the machine page hinted *"t new task in a project"*;
  `t` there now starts one in the machine's home, and the hint says so.
- **Noticed, not changed:** a bad Daytona key read from `config.toml` is
  reported as *"check the key in $DAYTONA_API_KEY"*.

### R36 — 2026-09-28, agents driving agents, macOS arm64 → Linux amd64, build 0.1.6-dev (8cef9ef)

Rows 2.10–2.13 with real agents, through a test server of its own (a
scratch `CONCH_HOME`, the person's real `HOME` so the agents were signed
in), and for 2.12 a scratch server on busybox reached through a
`CONCH_SSH` wrapper that ran every remote script under `/tmp/ce-r`. Claude
Code v2.1.284 (auto mode on), Codex 0.144.1. The build was installed at
`~/.local/bin/conch` first: every adapter puts that folder first on the
agent's `PATH`, so an older conch there is what the agents would have run.

- **The blocked guard on real screens.** Claude's first-run trust question
  opens with the cursor on *No, exit*, and Codex's update menu on *Update
  now* (a `curl … | sh`): an Enter typed onto either is the harm the guard
  exists for. `agent prompt` refused all three with exit 3, and the cursor
  hadn't moved.
- **The turn, not the state.** Prompted while done, Claude went done →
  idle the moment the message was typed — the log has it at 22:17:15 — and
  a wait on state would have returned there; the turn wait returned after
  working → done a second later. A message sent while Claude worked was
  folded into that turn (one UserPromptSubmit, one Stop), and the wait
  ended with it.
- **A helper that backgrounds its work ends its turn early.** Asked to run
  `sleep 20`, Claude ran it in the background and stopped, promising to
  answer when it finished; `-wait` returned then, correctly by its rule.
  Claude did take the work up again by itself when the job ended (a turn
  with no prompt, 25 s later). So the skill now has helpers asked to run
  commands in the foreground, and says to check the result, not the exit.
- **Scoping holds with a real agent's tools.** Commands Claude ran through
  its Bash tool were refused from the kernel's view of its pane, not from
  anything it could unset — the question the fakes could not answer. Over
  `-m` the refusal named it "p1 on Amits-MacBook-Pro-2.local".
- **Found and fixed:** Claude read as done while it worked. Its manifest
  knew work by "esc to interrupt", which v2.1.284 no longer shows; after
  8 s without a hook (`hook_working_stale`) a long think or a long command
  fell back to the screen and read as idle. Both panes in 2.13 did, and
  the driving agent noticed `conch` saying done while the reviewer's screen
  said working. A `spinner` rule now knows the line it shows instead —
  `✢ Effecting… (32s · ↓ 1.5k tokens)`, `✻ Galloping… (running Stop hook ·
  20s · …)` — and not the finished `✻ Crunched for 16s · done`.
- **Changed in the skill:** asked for a second opinion with the change
  uncommitted, Claude chose not to commit without being asked and put the
  diff in the helper's prompt, which is better than the skill's "commit
  first". The skill now says that.
- **Codex's failures look like done.** Its API key was rejected (401) on
  every request; with no hooks, conch reads Codex from its screen, which
  went working → done either way, so `-wait` exited 0. The skill now says
  a failed request ends a turn too.
- **Clean-up:** the skill removed again, the two trust entries the run
  wrote (`~/.claude.json`'s project, `~/.codex/config.toml`'s) taken out,
  both test servers stopped, busybox's shared server untouched.
- **Not run:** Codex's answers (its key is to be fixed before a rerun), OpenCode
  and Gemini, a helper stopping to ask a question (auto mode asked none),
  and `-m` against an older remote (the shared server was left alone).

### R35 — 2026-09-28, the icon column with and without a Nerd Font, macOS arm64

Reported as "file icon is not showing in Files", with `[ui] icons = "nerd"`
set. iTerm2, and `~/Library/Fonts` and `/Library/Fonts` held no Nerd Font.

- In `nerd` mode the column was **blank** — not a box. A terminal that
  draws nothing for a Private Use Area glyph makes a missing font look
  exactly like a missing feature, which is how this was first read as a
  bug in the explorer.
- Settings → Theme → File icons → **Letters** filled the same column with
  `go`, `ts`, `md`: the table and the column were fine all along. That is
  the check to ask for first, and the reason Letters is the default.
- `brew install --cask font-jetbrains-mono-nerd-font` and iTerm2 →
  Profiles → Text → **JetBrainsMono Nerd Font Mono** (the Mono variant,
  whose glyphs keep to one cell, since conch fixes the column at two by
  measuring). The icons then drew, with no conch restart: the mode is
  read on every render.
- **Which** Nerd Font is the user's own business, and the first one was
  sent back: JetBrains Mono replaced the text face as well as the icons,
  which is the whole profile changed to get an icon column. Either fix
  keeps the letters: a symbols-only Nerd Font as iTerm2's non-ASCII font
  beside the font they already had, or a Nerd Font of a face they want
  anyway — MesloLGS/LGM, which is Menlo's shape and what the powerline
  prompt themes are drawn against. Meslo is what stuck, and the icons
  draw with it.
- Not seen here: a terminal that draws a box instead of a blank, the
  alignment of a non-Mono variant, Linux, and the same over ssh.

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
