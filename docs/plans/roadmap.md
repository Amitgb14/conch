# Roadmap

Order of upcoming work (updated 2026-09-19). Finished items move to the git
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

## Next

13. **Review queue** — one view of what is waiting for a decision, across
    every machine: branches whose agent has finished, what each changed,
    what it cost and how long it has been sitting, ordered by what needs
    answering first. The tree is a map of the fleet; this is the list of
    what to do about it, and it ends in the actions harvest already has
    (open the diff, commit, PR, merge, discard). Attention is the scarce
    thing when ten agents are running, and nothing else here spends it
    for you. Starts read-only over the existing branch, agent and cost
    data; no new server state until the view earns it.

## Not now

14. **MicroVM sandboxes** — see [microvm-sandbox.md](microvm-sandbox.md).
    Deferred: a VM added as an ordinary machine already gives the isolation,
    so what is left to build is lifecycle convenience, not safety. Revisit
    when agents are meant to act unattended — 15 and 16 below need a
    boundary first — or when the code being worked on isn't trusted.

## Last

15. **Auto-approve rules** — per-project rules that let agents run safe
    commands without waiting for the user. Wants 14 first: rules that skip
    the confirmation are only sane inside a sandbox.
16. **Task graph** — server-side rules such as "when A is done, start a
    review agent on its worktree", only once auto-approve rules exist, since
    they run actions nobody confirmed. Notifies rather than moving focus.

## Decisions

- New worktrees keep copying `.env`, `.env.*` and `.envrc` by default
  (`internal/server/localfiles.go`), so agents in a task have the same local
  setup as the main checkout.

Every item ships with tests covering its edge cases (see `AGENTS.md`) and a
row in the [end-to-end plan](../testing/end-to-end.md) for what fakes can't
prove.
