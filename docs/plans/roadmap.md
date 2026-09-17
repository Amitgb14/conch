# Roadmap

Order of upcoming work (updated 2026-09-16). Finished items move to the git
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

## Next

8. **Harvest** — finish a task from the TUI instead of only starting one:
   - commit in the worktree (optionally selected hunks), push, and
     `gh pr create`
   - squash-merge or cherry-pick into the base, refusing on conflicts
   - discard a worktree and its branch, after a confirmation that names
     uncommitted files and commits not pushed or merged (removal forces only
     here)
   - a cleanup list of worktrees with no pane or whose branch is merged or
     gone, plus `git worktree prune`

   All server methods behind a capability, so remote machines work too.
   Hunk adoption across branches and a side-by-side diff are out of scope.
9. **Cost on the tree** — per-pane tokens on tree rows and a total per
   project, labelled as usage conch has seen (plan windows are per account).
   Starting a task while a plan window is past its alert threshold warns in
   the task dialog and can be overridden; missing or stale limits never
   block.
10. **Brain actions for handoff and broadcast** — `share` and `broadcast`
    join the planner's actions, still confirmed like every other action. A
    tree filter shows agents waiting for input across machines.
11. **Best-of-N tasks** — `conch task -n N` and `-agent claude,codex,...` start
    the same prompt in separate worktrees with suffixed branch names, and a
    compare view shows each attempt's diffstat, state and an optional test
    command's result, harvested through item 8. Needs item 9's warning.
12. **MicroVM sandboxes** — see [microvm-sandbox.md](microvm-sandbox.md).
    Bring-back (phase 4) lands commits where harvest can finish them.
13. **`conch wait`** — `conch wait PANE -state done` so people can script
    chains of tasks themselves before conch automates any.

## Last

14. **Auto-approve rules** — per-project rules that let agents run safe
    commands without waiting for the user.
15. **Task graph** — server-side rules such as "when A is done, start a
    review agent on its worktree", only once auto-approve rules exist, since
    they run actions nobody confirmed. Notifies rather than moving focus.

## Decisions

- New worktrees keep copying `.env`, `.env.*` and `.envrc` by default
  (`internal/server/localfiles.go`), so agents in a task have the same local
  setup as the main checkout.

Every item ships with tests covering its edge cases (see `AGENTS.md`) and a
row in the [end-to-end plan](../testing/end-to-end.md) for what fakes can't
prove.
