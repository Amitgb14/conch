# Roadmap

Order of upcoming work (updated 2026-09-26). Finished items move to the git
history; details for big items live in their own plan files.

## Done

1. **Plan-limit alerts** — notify once when Claude's or Codex's 5-hour,
   weekly or spend window passes 80% and 95% (configurable), per machine and
   window, remembered across TUI restarts until the window resets.
2. **Session search** — `/` in the Sessions view filters by title, agent,
   branch and ID as you type, and searches inside conversations on the
   server, showing a matching snippet.
3. **Share a session with another agent** — hand a saved conversation to a
   different agent (or a running one): conch writes the conversation to a
   handoff file in the checkout and starts the chosen agent there with a
   prompt to read it and continue.
4. **Broadcast to a group** — send one prompt to every agent in a project or
   group from the tree, and one command to its terminals. Waiting agents and
   terminals in mixed groups start unticked; the confirmation lists recipients.
5. **tmux extras** — `ctrl+b < > .` move tabs, `ctrl+b q` split numbers,
   `ctrl+b space` and `alt+1`–`alt+5` layouts, `w` picker names with each
   split's branch or folder and agent state.
6. **Website deploy** — `web/` published to GitHub Pages on every push.
7. **First release** — v0.1.0: archives for macOS and Linux (amd64, arm64)
   with checksums, installed by `install.sh` and `conch update`.
8. **Harvest** — finishing a task from the TUI: commit (marked files or
   single hunks), push, `gh pr create`, merge into the base (undone if it
   conflicts), discard a branch and its worktree after naming what that
   loses, and a cleanup list of leftover worktrees. Server methods behind
   `branch.harvest.v1`, `branch.hunks.v1` and `worktree.cleanup.v1`, so
   remote machines work too, and `conch branch commit|push|pr|merge|discard`
   and `conch worktree ls|clean` do the same from a shell. Hunk adoption
   across branches and a side-by-side diff stayed out of scope; marking
   hunks is a TUI step.
9. **Cost on the tree** — each agent's cost on its tree row, totals on the
   Agents section, the project and the machine, and what each saved session
   used in the Sessions list, all labelled as the usage conch has seen.
   Agents that report no cost show tokens; conch ships no price table. The
   task dialog warns when the chosen agent's plan window is past an alert
   threshold, and never refuses.
10. **Brain actions for handoff and broadcast** — the planner can propose
    `share` (one agent's conversation continued by another) and `broadcast`
    (one message to several agents), still confirmed like every other
    action and never aimed at a terminal. `/ !` filters the tree to the
    agents waiting for an answer, across machines.
11. **Best-of-N tasks** — `conch task -n N` and a list of agents try one
    prompt several times, an attempt per branch under the task's own name.
    `A` compares them: what each changed, its agent's state and cost, and
    what a command (`t`) came to in each worktree. Keeping one and
    discarding the rest goes through harvest, which still says what it
    would lose.
12. **`conch wait`** — `conch wait PANE -state done` blocks until an agent
    is done (or waiting, working, idle) using the events the server already
    broadcasts, so a shell can chain tasks without polling. It exits 124
    when `-timeout` runs out, as `timeout(1)` does.
13. **Review queue** — `Q` lists what needs a decision across every
    machine: agents waiting, agents finished, commits not pushed or
    merged, worktrees left dirty — in that order, longest wait first.
    `enter` opens the pane when an agent is waiting and the branch's
    changes otherwise, so the actions stay where harvest put them. Read
    from what the TUI already holds, so it costs no server call and needs
    no capability. Branch rows follow the tree's own window — nothing
    stale enough for the tree to hide — and sit under a heading for their
    project, so a repository is named once however many of its branches
    need you. `x` dismisses a row until what it says changes, and the
    status bar counts what is waiting. A project can name a **check** —
    its own command — which conch runs in a branch's worktree when the
    agent there finishes: the queue then says `check passed` or `check failed
    (exit 1)`, and a failure sorts to the top. No default, nothing runs
    until a command is named, and `v` runs it on demand. Not yet:
    `/` filters the list by project, branch, agent, machine or reason —
    every word has to appear — and the count says how much is hidden.
    Dismissals are kept in `ui.json`, so they outlive the TUI, and are
    forgotten once the row they were about is gone. A verdict is about the
    branch as it stood: when the branch moves the check reads "out of
    date", stops counting as a failure, and is run again once the branch
    has been quiet for half a minute with no agent writing in it.

14. **Staying in step with upstream** — `conch update` installs the latest
    release, `conch update list` shows what is published (marking the
    running, latest and locally kept versions) and `conch update rollback`
    goes back to the version before the last update, from the copy every
    update keeps in `$CONCH_HOME/versions`; `conch update VERSION` goes to
    any release, older ones included, and `install.sh` takes a version as an
    argument for machines without conch yet. `[update] auto = true` installs
    a newer release as soon as the daily check finds one, off by default.
    Going back notes the version it leaves, so it also comes forward again.
    Not yet: choosing a version from the TUI, and moving a remote machine
    back (it follows this computer's build).

15. **File explorer** — `f` browses a project's checkout, or the worktree
    of the branch (or agent) selected, in a lazily expanded tree: a
    per-filetype icon, git status beside the name, a preview with line
    numbers that describes a binary rather than printing it, `/` to find
    among what is loaded, and `a` to put a file's path into the prompt of
    the agent working in that checkout, on whichever machine it is on. `d`
    opens the file's diff, `e` its `$EDITOR`, `y` copies the path. Reading
    only, and only inside the project, behind `fs.list` and `fs.read`. See
    [file-explorer.md](file-explorer.md).

16. **Move a worktree to another machine** — `T` on a branch sends the
    branch and everything uncommitted in its worktree to another machine,
    and the agents working there carry on: their conversations are handed
    over and their panes here close. Only the commits the far side lacks go
    across; staged arrives staged and unstaged unstaged; untracked files
    and the project's local files (`.env`) go with them, while what git
    ignores stays behind. The worktree here is kept, so a move that goes
    wrong costs nothing. The project picker offers to clone the repository
    there when the machine does not have it.

17. **Reading a pane's past** — a program on the alternate screen keeps no
    scrollback, so conch watches the screen and keeps the rows that scroll
    off it: `ctrl+b [` and selection reach what an agent said a page ago.
    `/` and `?` search that history in scroll mode, `n` and `N` repeat,
    and the server searches the whole of it so a match off screen is
    found. `Y` on an agent opens the conversation it saved, to scroll and
    copy part of — the alternate screen makes that impossible in the pane
    itself.

18. **Watching a terminal** — `ctrl+b M` alerts when a pane goes quiet
    after printing, `ctrl+b A` when it prints at all: a build or a test run
    says when it is done without being watched. An icon in the status bar
    opens what conch itself is using in memory and CPU.

19. **Saved ssh sessions** — a host reached with `H` stays in the tree
    rather than being typed again after it ends or conch restarts.

20. **Sandboxes** — a machine conch makes for you in the cloud, so a long
    task runs away from the laptop and keeps going once it is closed.
    `M` → **New Daytona sandbox…**, or `conch sandbox create|ls|start|stop|rm`:
    conch creates the sandbox, installs itself there and adds it as an
    ordinary remote machine, so the tree, `-m` and everything else work with
    it unchanged. The API key is read from the environment every time and
    never written down; a sandbox that fails to set itself up is deleted
    rather than left to cost; stopping and deleting say what they would end
    and what work has reached no remote. Daytona is the one provider so far,
    behind an interface that others can follow. The docs are the
    Sandboxes page under `web/src/app/docs/sandboxes`.

## Next

21. **E2B sandboxes** — a second provider, started: `internal/sandbox/e2b.go`
    makes, lists, pauses, resumes, ends and keeps alive an E2B sandbox
    through its platform API, with tests against a fake one. It is not in
    `sandbox.Providers` yet, so nothing offers to make a sandbox conch
    cannot reach — because reaching it is the part left.

    E2B has no ssh. Its agent inside the sandbox (envd) runs processes and
    terminals over ConnectRPC on plain HTTPS: `Process/Start` with stdin
    enabled, `Process/StreamInput` for what is typed, the start stream for
    what comes back, `Process/Update` to resize a terminal. A conch
    transport is "a local command whose stdin and stdout carry the
    stream", so what is missing is a command that speaks that: `conch
    sandbox exec`, which `remote.TransportFor` hands back as an ordinary
    exec.Cmd, leaving install, bridge and panes as they are. This is the
    "a sandbox's exec" transport the architecture notes already expect.

    Also needed: registering the provider, and pushing the sandbox's clock
    back while conch is connected — E2B ends a sandbox on a time to live
    rather than on idleness (an hour on the free plan, a day on Pro). They
    are made with auto-pause, so one whose clock runs out keeps its
    filesystem *and* its memory instead of being destroyed.

## Not now

22. **MicroVM sandboxes on your own hardware** — see
    [microvm-sandbox.md](microvm-sandbox.md). 20 covers the case that
    mattered: somewhere isolated to run an agent, made and thrown away from
    conch. What this plan adds is a sandbox on hardware you own — a
    Firecracker or Kata microVM on a Linux host, Apple `container` or Lima
    on a Mac — for code that can't leave the building, or when a cloud
    provider is not wanted. Still deferred: a VM added as an ordinary
    machine gives the same boundary today, so what is left is lifecycle
    convenience.

## Last

23. **Auto-approve rules** — per-project rules that let agents run safe
    commands without waiting for the user. A sandbox is the boundary these
    need, and 20 gives one: the rules would be allowed there and nowhere
    else, so a rule that skips a confirmation cannot reach the laptop.
24. **Task graph** — server-side rules such as "when A is done, start a
    review agent on its worktree", only once auto-approve rules exist, since
    they run actions nobody confirmed. Notifies rather than moving focus.

## Decisions

- New worktrees keep copying `.env`, `.env.*` and `.envrc` by default
  (`internal/server/localfiles.go`), so agents in a task have the same local
  setup as the main checkout.

Every item ships with tests covering its edge cases (see `AGENTS.md`) and a
row in the [end-to-end plan](../testing/end-to-end.md) for what fakes can't
prove.
