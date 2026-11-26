---
name: conch-status
description: Say what is going on across conch in one short report — which agents are done, which are waiting on the person and what they ask, which branches have open review comments or red CI, what is scheduled for later — and offer to clean up finished worktrees, merged branches and idle panes, doing only what the person says yes to. Use when you are inside conch (CONCH_PANE_ID is set) and the person asks what their agents are doing, what is going on, what needs them, or to tidy up.
metadata:
  installed-by: conch
---

# What is going on

The person has been away, or has too many panes to read. You read them
all and say, in a screenful, what is done, what needs them and what is
stuck — then offer to tidy up. Reading changes nothing; tidying waits for
their yes.

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

## 1. Read

```sh
conch status -json               # every pane: its agent, state, branch, what it left running
conch schedule ls                # what the server will do later
conch worktree ls                # leftover worktrees, and why each is or isn't finished
```

For each agent that is **waiting**, read what it asks:

```sh
conch agent question p7          # its question and choices, as JSON (null when not waiting)
```

For each branch an agent is on, what is open:

```sh
conch review -branch BRANCH      # review comments not yet answered
gh pr view BRANCH --json number,state,url,statusCheckRollup
```

You reach only your own project and the panes you started; conch leaves
the rest out. Say so in the report rather than guess at what you can't
see.

## 2. Report

Most urgent first, a line per item, nothing that needs no one:

```
Needs you (1)
  p7 build-150 waits: "Run rm -rf dist?" (yes / no)
Red (1)
  feature/export: CI failed in TestUpload — already sent to its agent at 08:10
Done (2)
  build-142: PR #318, checks green, ready to merge
  review-login: REVIEW.md says clean
Working (1)
  build-151, 25 min, on its third commit
Later
  02:00 nightly dependency update (task -every 1d)
Tidy up
  4 finished worktrees, 2 idle helper panes
```

- **Needs you** — every waiting agent, with its question and choices.
  Don't answer it: the choice is the person's. Tell them the pane, or
  that `conch agent answer p7 CHOICE` answers it without opening it.
- **Red** — a failed check or CI run, and whether it was already sent
  back to the agent (the autopilot does that when it is on). If it was
  not, offer `conch branch fix-ci -branch BRANCH`.
- **Done** — what each finished agent left: a pull request and its
  checks, a report file, a branch to review.
- **Working** — how long, and anything that looks stuck: the same screen
  for a long time, or a long command it left in the background
  (`BACKGROUND` in `conch status`).

## 3. Tidy up — only what they say yes to

Offer each as a line they can accept or not:

```sh
conch worktree clean -y          # finished worktrees only: merged, nothing uncommitted, no pane in them
conch branch prune -merged       # a dry run: the merged branches it would delete
conch close review-login         # a helper whose result was read
```

`conch branch prune -merged` without `-y` only lists; run it with `-y`
after they agree to that list. Close only panes you started or that
belong to the project, and only once their result is read. Never add
`-force` to a clean or a prune: it removes work that is not finished.

Say what you removed, and what you left and why.

## Rules

- Reading first, and changing nothing they didn't agree to.
- Never answer a waiting agent's question for the person.
- Never close a pane with unread work, or one you can't tell is finished.
- Use `conch agent prompt` to message an agent, never `conch send`.
