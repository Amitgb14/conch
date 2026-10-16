# What conch needs next to be the orchestrator people want

A review of conch against the other parallel-agent orchestrators as of October
2026, and of what people running several agents actually ask for, turned into
a plan. Other tools are described by kind, not by name. Signal strength is
marked **S**trong, **M**edium or **W**eak.

## Status (2026-10-14)

Built on the branch `conch/how-make-better-ai-agents`, each with tests, docs
and an end-to-end row (9.117–9.127). What was built differs from the plan
below in places, and those places are noted.

| # | Item | State | Notes |
| --- | --- | --- | --- |
| 0.1 | Live e2e rows | partly | R54–R56: setup, ports, queue and schedule with stand-in agents; permission rules with a real Claude Code (found and fixed a false "waiting"). The older rows (7.4, 3.1–3.6, 8.1, 9.28…) and the TUI paths still need a person at a real terminal |
| 0.2 | Brain against a real model | done | R56 with the `claude` provider: plan, answer, run. Found and fixed: no action for a terminal, so it started an agent instead. Anthropic and OpenAI providers (1.3, 1.4) and summaries (1.5) not run |
| 0.3 | State soak | done | `TestStateSoak`: OSC 7501 cycles under -race; 2000 cycles, no wrong state, slowest 19 ms (`CONCH_SOAK_CYCLES`) |
| 1.1 | Worktrees that run | done | `project.set_setup`, `CONCH_PORT` blocks (ports.json), `L` preview (local, ssh forward, sandbox link) |
| 1.2 | Review comments | done | `a` on a hunk, `S` sends. **Kept in the TUI, not the server**, so the phone does not see them yet |
| 1.3 | Verified done | done | `f` sends a failed check or CI run (`branch.ci_log`); `[verify] auto_fix` |
| 1.4 | Reviewer agent | done | `[verify] reviewer`; `agent.reply` reads its report; `R` hands it over |
| 1.5 | While you were away | done | The question and the work size on each queue row |
| 2.1 | Backlog, WIP limit | done | `conch task -queue [-wip N]`, task dialog *When*. `-after REF` (2026-10-15, `schedule.after.v1`): waits for a branch, pane or scheduled task to finish, then stacks on its branch; a discarded branch, uncommitted work or a loop leave it waiting and saying why. E2E row 9.129 |
| 2.2 | Issues as tasks | done | GitHub through gh: `task -issue N`, project menu |
| 2.3 | Resume after a limit | done | Server-side schedule (`schedule.*`); `C` in the queue |
| 2.4 | Overlap warning | done | Overlaps and merge-tree conflicts on each worktree, shown in the queue and changes |
| 3.1 | Permission rules | done | `[[permissions]]`, answered through Claude Code's PermissionRequest hook; dry-run; `agent explain` lists answers. Claude only |
| 3.2 | Sandboxed tasks | done | `conch task -sandbox PROVIDER`. MicroVM plan still deferred |
| 3.3 | Scheduled tasks | done | `conch task -at / -every`, on the same server schedule |
| 3.4 | Notify command | done | `[notify] command`, also in Settings |

## The short version

The field has converged. These are now **table stakes**:

- a worktree per agent;
- several vendors' agents;
- a state and an alert per agent;
- a diff to review;
- sessions that outlive the UI;
- ssh;
- a phone app.

conch has all of them, and several things most of the field lacks.

What people say they are short of is not more agents in parallel. **The person
is the bottleneck.** They have to:

- review what came back;
- know which agent needs them;
- trust that "done" means done;
- get a new worktree to *run*.

Most tools isolate files and stop there.

So the plan is not to copy the popular desktop apps. It is to close the loop
between an agent finishing and the person landing its work, which nobody does
well:

1. **A worktree that runs** as well as it builds: setup, ports, preview.
2. **Review that goes back to the agent:** comments on the diff.
3. **"Done" that means verified:** checks and CI fed back, with a cap.
4. **A second opinion by default:** a reviewer agent from another vendor.
5. **Work that waits for you:** a backlog with a work-in-progress (WIP) limit
   sized to your review capacity, and resuming after a plan limit.

Before any of that comes **proving what is already built**:

- `docs/testing/end-to-end.md` has about 100 rows never run live.
- The brain has never met a real model.
- Users say a false "waiting" alert is what makes them stop trusting a tool.

## Where conch stands

conch has more features than most of the field, but almost nobody knows it
exists yet:

- The repository is public, about a month old, with a handful of stars.
- The two most popular open-source orchestrators have tens of thousands of
  stars each.

The gap is **adoption, not capability.**

**Engineering:** around the top 3–5.

**Popularity:** effectively unknown.

### Features compared with the field

The columns stand for the tools conch is most often measured against:

- **A:** the most-starred cross-platform desktop app.
- **B:** a terminal UI with a background server.
- **C:** a commercial Mac app.
- **D:** a Go terminal UI with a web view.
- **E:** an early tmux-based terminal UI.

| Area | conch | A | B | C | D | E |
|---|---|---|---|---|---|---|
| Sessions survive closing the UI | ✅ server, and survives its own upgrade | ? | ✅ | cloud | tmux | tmux |
| Worktree per agent | ✅ | ✅ | ❌ | ✅ | ✅ | ✅ |
| Several vendors' agents | ✅ 5 supported, any by manifest | ✅ any | ✅ 15+ | 3 | ~12 | 6 |
| Instructions, skills and MCP shared across agents | ✅ **unique** | ❌ | ❌ | ❌ | MCP only | ❌ |
| Remote machines | ✅ installs itself, moves a worktree between machines | ssh | ssh | ? | ssh | ❌ |
| Phone, on your own machines | ✅ over Tailscale | app | ❌ | paid | web/chat bots | ❌ |
| Agents driving agents | ✅ scoped (**unique**) | task graph | socket | API | supervisor session | ❌ |
| Agents report their own state | ✅ OSC 7501 | screen | screen | ? | screen | ❌ |
| Best-of-N and compare | ✅ | ✅ | ❌ | ? | ❌ | ❌ |
| Plan-limit and cost tracking | ✅ limits and cost | limit resets | ❌ | ? | ✅ with budgets | ❌ |
| Hosted sandboxes | ✅ 3 providers | ❌ | ❌ | ✅ | Docker | ❌ |
| Diff comments sent back to the agent | ❌ | ✅ | ❌ | ✅ | ? | ❌ |
| Worktree setup, ports, preview | ❌ (copies files only) | partial | ❌ | ✅ | hooks | ❌ |
| Issues or backlog as tasks | ❌ | ✅ | ❌ | ? | ❌ | ❌ |
| Slack or webhook alerts | ❌ | ❌ | ❌ | ? | ✅ | ❌ |
| Windows | ❌ | ✅ | ? | ❌ | ? | ❌ |

### Ahead: architecture

No other tool has this combination:

- Sessions outlive the UI and survive the server's own upgrade.
- Remote machines are first-class.
- The phone reaches your own machines over Tailscale. People ask for this and
  hack it together today.
- Agents drive other agents, scoped by peer pid.
- Agents report their own state through OSC 7501.
- One agent's setup is shared with the others. This answers the biggest single
  request in the field: one instructions file for every agent, at over 5k 👍.

It is also an Apache-2.0 Go binary with no login and no telemetry. That avoids
the two most-cited complaints about other tools: Electron bloat and Mac-only
apps.

### Behind: the daily workflow

None of these exist yet:

- Diff comments that go back to the agent.
- A worktree that runs its own setup and gets its own ports.
- A failed check that goes back to the agent.

These are what users ask for most, and the leading desktop apps already have
the first two.

### Weakest: proof and reach

**Proof:**

- About 100 end-to-end rows have never run live.
- The brain has never met a real model.
- Tests cover about 91%, but only against fakes.

**Learning curve:** many features mean a steep start. The most popular tools
win on being obvious in the first five minutes.

**Distribution:** none confirmed yet. No launch post, no package-manager
install, no demo.

### Getting known

Once Phase 0 and Phase 1 below are done, launch with the story nobody else can
tell: **your own machines and phone, any agent, no account, and agents that
survive closing the UI.** That means:

- a Show HN post;
- a 60-second demo of a phone answering an agent on a remote box;
- a one-line install with `brew` or `curl | sh`.

### What the field is short of

| Need | Signal | conch today | The field |
| --- | --- | --- | --- |
| A worktree that runs: setup script, ports, preview URL | S | Copies local files (`project.set_files`); no setup, ports or preview | Several apps have setup scripts and a port variable per workspace |
| Diff comments go back to the agent | S | Absent | Most desktop apps |
| A check or CI failure goes back to the agent | S | The check runs when an agent finishes; the result stays on the queue row | Nobody does this well |
| A reviewer agent when work finishes | M | Only if an agent chooses to (skill) | Cloud agents pre-review; users chain review bots |
| Backlog, WIP limit, issue intake | M | `Q` is an attention list, not a backlog | Kanban boards; GitHub and Linear intake |
| Resume after a plan limit resets | S | Alerts only | Requested, not shipped anywhere |
| Overlap between parallel branches | M–S | Detected only when a merge conflicts | Nobody |
| Permission policy between YOLO and click fatigue | S | Brain plans need confirming; no auto-approve rules (roadmap) | Supervisor sessions that auto-answer; Docker modes |
| Scheduled or recurring work | M | Absent | Two apps have automations |
| Slack, ntfy or webhook alerts | M | Desktop and phone push | One terminal UI has chat bridges |
| jj; tasks across several repos | M | Absent | Nobody; top requests on one leading app (46 👍, 42 👍) |
| Windows | M | Absent | The desktop apps |

## Things to avoid

What users reject, so conch keeps not doing it:

- **Ceremony and role-play.** One orchestrator with mayors-and-workers roles
  drew hundreds of "lost the plot" comments. A task graph should stay a list
  with dependencies, not an org chart.
- **Throughput maximalism.** The realistic ceiling is 3–8 agents per person.
  Design for *finishing* work, not for starting 20 jobs.
- **Push alerts that reorder things.** Experienced users turn notifications
  off, and users of one app complained when its tabs reordered. A stable board
  to pull from beats being pushed. Keep the tree order stable, and make the
  queue the place to look.
- **Requiring a cloud, a login, telemetry or broad OAuth scopes.** These drew
  the angriest launch threads.
- **UI churn and abandonment.** The top issues on two once-popular tools are
  "is this maintained?" and "bring back the old UI". Ship small and keep keys
  stable.

---

## Plan

Each item says:

- what the need is;
- what to build;
- where it goes;
- the protocol and test notes from AGENTS.md that apply.

Sizes: **S** is days, **M** is a week or two, **L** is more.

### Phase 0: prove what exists (before new features)

**0.1 Run the end-to-end rows that matter most.** *Size M.*

Run these live, in this order, because they are what users touch every hour:

1. State and notifications: 2.1 Claude hooks, 7.4 notifications, 9.111 OSC 7501.
2. Resume for each agent: 3.1–3.6.
3. Checks: 9.28.
4. PR status: 8.1.
5. Finishing through a PR: 9.86.
6. Plan-limit alerts: 9.1.
7. Attempts and compare: 9.24–9.26.

Fix what fails before Phase 1 builds on it.

**0.2 Run the brain against a real model, once.** *Size S.*

Rows 1.1–1.6 have never run. Either prove the brain or stop advertising it.
An unproven planner is a liability in a tool whose pitch is "nothing runs
until you confirm".

**0.3 Measure state accuracy.** *Size S.*

A false "waiting" alert is what users name as destroying trust in a tool. Add
a soak test:

- a standin agent cycles through states for an hour, under `-race`;
- every transition is logged against what it reported.

### Phase 1: close the loop from finished to landed

**1.1 Worktrees that run** (S signal; the most-cited missing piece). *Size M.*

- **Setup and teardown:**
  - `[project] setup = "…"` and `teardown = "…"`, run in the new worktree's
    own pane after local files are copied.
  - `CONCH_*` variables are set, as for `[[actions]]`.
  - A failed setup is shown on the row; the agent still starts.
- **Ports:**
  - Each worktree gets a stable `CONCH_PORT`: a base plus an index kept with
    the project and carried through reload.
  - Agents and setup scripts read it.
  - Two worktrees never share one.
- **Preview:**
  - `o` on a worktree row opens `localhost:$CONCH_PORT`.
  - On a remote machine, conch forwards that port over the existing ssh master
    (`ssh -O forward`). This is the "see the running app on the box" request.
- **Where:**
  - `internal/config`: project config.
  - `internal/server`: worktree creation in the task path, and port allocation
    persisted like other project state.
  - `internal/remote`: the forward.
  - The TUI row, help text, and `web/.../interface`.
- **Protocol:** a new `worktree.setup.v1` capability. Against an old server,
  setup is left to the user and the TUI says so.
- **Tests:**
  - a setup that fails, and one that hangs (timeout);
  - no setup configured;
  - a port reused after its worktree is cleaned;
  - a reload keeping allocations;
  - an older project state file without ports.

**1.2 Review comments back to the agent** (S). *Size M.*

- **Comments:**
  - In the Changes view, `c` on a line or hunk adds a comment.
  - Comments collect per branch.
- **Sending:**
  - `R` sends them to the branch's agent as one prompt: the file, the line,
    the quoted hunk and the comment.
  - It goes through the same path as `agent prompt`, which is refused while
    the agent waits on a question, as now.
  - If no agent is running, conch offers to resume its last session with the
    review as the first message.
- **Storage:**
  - Comments live on the server per branch, so the phone and the TUI see the
    same set.
  - A new `branch.review.v1` capability.
  - The phone's diff view gains the same action later.
- **Tests:**
  - a comment on a deleted line;
  - a comment on a file that has since been renamed;
  - a comment on a hunk the agent has since changed (sent with the quoted
    hunk, so it still makes sense);
  - an empty set of comments;
  - a narrow terminal rendering the comment marker.

**1.3 "Done" means verified** (S). *Size M.*

Today a project check runs when an agent finishes. This closes the loop.

- **On a failed check:**
  - The queue row offers `f` (fix), which sends the tail of the failing
    output, capped, to the agent.
  - Opt-in `[project] check_fix = N` does this automatically up to N times,
    then stops and asks.
  - The cap is the difference between a fix loop and an unattended loop
    burning a plan window overnight.
- **On a failed CI run:**
  - When a PR's `statusCheckRollup` fails, fetch the failed job's log tail
    with `gh run view --log-failed`.
  - Offer the same `f`.
- **Showing why a row is done:**
  - `done ✓`: the check passed.
  - `done ✗`: the check failed.
  - `done ·`: there is no check.
- **Where:**
  - `internal/tui/verify.go` and `queue.go`.
  - `internal/ghx`: the failed log.
  - `internal/server`: the auto-fix counter per branch, which survives reload.
- **Tests:**
  - a check that never ends;
  - a check that prints megabytes (capped);
  - the cap reached;
  - an agent busy when the check fails (queued, not dropped);
  - `gh` missing or not logged in (`CONCH_GH` fake).

**1.4 A second opinion when an agent finishes** (M). *Size S–M*, since the
skill, task and await already do the hard parts.

- **Starting the reviewer:**
  - Configured with `[project] review = "codex"`, or a list.
  - When a branch's agent finishes and its check passes, conch starts the
    reviewer in the same worktree.
  - The reviewer is given the diff against the base and told to report, not
    edit (read-only by prompt).
- **Its answer:**
  - It is attached to the queue row.
  - `R` sends it to the writer, as in 1.2.
- **Defaults:**
  - Use *another vendor* than the writer, the pattern people describe.
  - Never on by default, since it spends plan usage. The task dialog shows it.
- **Tests:**
  - the reviewer is not installed;
  - writer and reviewer are the same agent;
  - the reviewer edits files anyway (the watcher detects it; say so on the
    row);
  - the plan window is nearly used (skipped, and said).

**1.5 "While you were away"** (S). *Size S.*

- The queue gets a one-line summary per row of what changed since you last
  looked at it:
  - files and lines touched;
  - the check result;
  - the last thing the agent said.
- It is built from facts conch already has, not a model call. The brain
  summary is an optional extra when configured.
- This is the cure people cite for the cost of switching between agents.

### Phase 2: work that waits for you

**2.1 A backlog with a WIP limit** (M). *Size M.*

- **Queuing:**
  - `conch task -queue "…"`, or `a` in the TUI, adds a task that does not
    start yet.
  - Queued tasks start when the project has fewer unreviewed branches than
    `[project] wip` (default 3). That makes the review-capacity ceiling people
    describe into a setting.
- **Ordering:** optional `-after <task>` for stacked work. It is a dependency
  list, not a graph UI.
- **Storage and display:**
  - The backlog lives on the server, so it survives the TUI and reload.
  - A new `task.queue.v1` capability.
  - Queued tasks show as dimmed rows under the project.
- **Tests:**
  - WIP at 0;
  - a dependency that is discarded (the dependent asks rather than starts);
  - a cycle (refused);
  - a reload with queued tasks;
  - an older server (the TUI says queuing needs an upgrade).

**2.2 Issues as tasks** (M). *Size S* for GitHub, since conch already uses
`gh`.

- **Listing:** `conch task -issue 123`, and an *Issues* view on a project,
  list open issues through `gh issue list`.
- **Choosing one** makes a task with:
  - the issue's title and body as the prompt;
  - a branch named from it;
  - `Fixes #123` in the PR.
- **Other trackers:** Linear and Jira only if asked. They need tokens, and
  tokens are a trust surface.

**2.3 Resume after a plan limit** (S). *Size S.*

- **Detecting:**
  - A pane's agent stops on a plan limit, recognised by a detection rule plus
    the `agent.limits` window.
  - conch then knows when the window resets.
- **Resuming:**
  - conch offers "continue at 14:05".
  - At that time it sends a configurable prompt (default `continue`), once.
- **Queued tasks (2.1)** also wait while the window is above
  `[notify] limit_at`.
- **Tests:**
  - a reset time already in the past;
  - the pane closed before the reset;
  - a reload in between (the scheduled send is persisted);
  - clock changes.

**2.4 Overlap warning** (M–S). *Size M.*

- **Overlapping branches:**
  - The watcher already knows which files each worktree touches.
  - When two live branches of a project touch the same file, both rows are
    marked (`⚠ overlaps feat-x`).
- **Before a merge:**
  - `branch merge` first runs `git merge-tree --write-tree` against the
    target.
  - It says "will conflict in a.go, b.go", rather than finding out by undoing
    a merge.
  - It offers to hand the rebase to the agent, the path that already exists
    for a rebase that stops.
- **Tests:**
  - renames;
  - binary files;
  - a branch behind its base;
  - git older than 2.38, which has no `merge-tree --write-tree`: skip the
    pre-check and say so.

### Phase 3: autonomy people can trust

**3.1 Permission rules** (S). *Size L.*

This is the roadmap's auto-approve item, pulled forward because demand is
strong.

- **Rules:** per-project rules for answering an agent's permission prompt:
  allow, deny or ask, by tool and command pattern.
- **Source:** read where OSC 7501 or the hooks give a structured
  `blocked:kind=permission`. **Never** answer from screen scraping alone.
- **Visibility:** every automatic answer is logged on the pane and shown in
  `conch agent explain`.
- **Dry run:** it ships with a mode that only reports what it *would* have
  answered.

**3.2 Sandboxed tasks** (S signal; a plan already exists).

- Resume `docs/plans/microvm-sandbox.md` once 3.1 exists, as that plan
  already says.
- Meanwhile, `conch task -sandbox daytona` uses the providers already wired,
  so a task can run in a sandbox today with the same review loop.

**3.3 Scheduled and recurring tasks** (M). *Size S* on top of 2.1.

- `conch task -at 02:00`, and `-every 1d` for jobs like "dependency bumps
  every morning".
- A scheduled task enters the backlog at its time, so the WIP limit and the
  plan-limit hold still apply.
- The server is the scheduler, since it already outlives the TUI. Nothing is
  added to cron.

**3.4 Outbound notifications** (M). *Size S.*

- `[notify] command = "…"` runs a command with the event as JSON on stdin.
- That covers ntfy, Slack webhooks and Telegram in one line each, without
  conch holding any service's token.
- The recipes go in the docs, not the code.

### Later, or only when asked

- **jj workspaces** (M signal: intense but narrow). *Size L.*
  - Put it behind a VCS seam in `internal/gitx`, only if users ask.
  - The worktree code assumes git throughout, which is why it is large.
- **Tasks across several repos** (M).
  - A task naming several projects gets a branch in each and one compare view.
  - Wait for demand from conch's own users.
- **Windows** (M).
  - The cheap step first: test and document WSL2 running the Linux binary, and
    add a row in end-to-end.
  - Native support needs a pty layer (ConPTY) and peer lookup, both L.
- **Teams** (M).
  - The phone's per-device machines and permissions already answer "let a
    teammate watch".
  - Shared servers and accounts go against conch's no-login stance; don't
    build them.

## Order and rough cost

| # | Item | Size | Why this order |
| --- | --- | --- | --- |
| 0.1–0.3 | Prove what exists | M | Every later item stands on state accuracy, checks and PR status |
| 1.1 | Worktrees that run | M | The most-cited missing piece; 1.3's checks need it |
| 1.3 | Verified done | M | Needs nothing new; the biggest trust gain |
| 1.2 | Review comments to the agent | M | The review bottleneck, directly |
| 1.5 | While you were away | S | Cheap; makes `Q` the place to look |
| 1.4 | Reviewer agent | S–M | Reuses 1.2's send path |
| 2.3 | Resume after a limit | S | Strong signal, small |
| 2.1–2.2 | Backlog, WIP limit, issues | M | Turns the queue into a workflow |
| 2.4 | Overlap warning | M | |
| 3.4 | Notify command | S | Small; can go any time |
| 3.1–3.3 | Rules, sandboxed tasks, schedules | L | Autonomy only after verification exists |
| — | Launch | S | After Phase 0 and Phase 1: Show HN, demo, one-line install |

Every item follows AGENTS.md:

- a capability string, and a degraded message for an older server;
- reload-safe state wherever it persists;
- tests for empty, old-file and failure cases;
- help text, CLI usage and the web docs in the same change;
- a row in `docs/testing/end-to-end.md` for what fakes can't prove.

## Sources

The evidence for user needs comes from:

- practitioners' posts:
  - Simon Willison: https://simonwillison.net/2025/Oct/5/parallel-coding-agents/
  - Mitchell Hashimoto: https://mitchellh.com/writing/my-ai-adoption-journey
  - Anthropic's best practices: https://code.claude.com/docs/en/best-practices
- Hacker News threads, by item id:
  - 45489884, 44533004, 46368739
  - 47218318, 44594584, 47079718
  - 46682551, 48236770, 47575417
  - 44878650, 46991591, 46859054
  - 46458936, 46734302
- issues in the agents' own repositories:
  - claude-code: #6235, #16157, #18435, #30519
  - codex: #10450, #9224, #2109, #2847, #11023
- the most-upvoted issues in other orchestrators' trackers.

Upvote counts mostly reflect how many users a project has, so compare them
within a repository, not across. Pricing and licence claims from third-party
listings were not verified and are left out.
