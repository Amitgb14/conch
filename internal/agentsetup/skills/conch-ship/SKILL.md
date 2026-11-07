---
name: conch-ship
description: Ship a branch through conch — run its tests, push it, open its pull request, watch CI, send a failure back to the branch's agent, and merge once it is green when the person asked for a merge. Use when you are inside conch (CONCH_PANE_ID is set) and the person asks to open a PR, get CI green, or merge a branch.
metadata:
  installed-by: conch
---

# Test, open the pull request, merge

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

You need the branch (`BRANCH`) and a directory in its project. Commands
below act on the branch checked out where you run them; `-branch BRANCH`
names another.

## Does conch ship it already?

When the person has turned on conch's autopilot, conch pushes finished
branches, opens their pull requests, sends a failed CI run back to the
branch's agent and merges green ones by itself. Look before doing any of
that by hand, or you race it:

```sh
grep -A12 '^\[verify\]' "$(conch config path)"
```

`auto_pr = true` means conch opens the pull request; `auto_merge = true`
means it merges once CI passes. Then your job is only to wait and report:
`gh pr view BRANCH --json state,statusCheckRollup,url`.

## 1. Tests first

Run the project's own test command in the branch's worktree (the
Makefile, package.json or README says which). Don't open a pull request
on red: send the failure to the branch's agent, or fix it if the branch is
yours, and run them again.

## 2. Commit

Everything that belongs to the change must be committed. If it is your
branch, commit with a message that says what changed and why:

```sh
conch branch commit -m "Explain what changed and why"
```

If another agent owns the branch, ask it to commit — don't commit its work
for it.

## 3. Push and open the pull request

```sh
conch branch push -branch BRANCH
conch branch pr -branch BRANCH -title "A title that says what it does"
```

`conch branch pr` prints the pull request's URL. Give it to the person.

## 4. Watch CI

```sh
gh pr checks BRANCH --watch
```

When a check fails, send the failure to the branch's agent — conch reads
the failed run's log and puts it in the prompt — and wait for the fix:

```sh
conch branch fix-ci -branch BRANCH
```

Then watch the checks again. After three failed rounds, stop and tell the
person what keeps failing.

## 5. Merge — only when asked

Merge only when the person's request said to merge. Once every check is
green:

```sh
gh pr merge BRANCH --squash --delete-branch
```

With no pull request — a branch that only goes into the local base —
`conch branch merge -branch BRANCH` squashes it into the base instead.

Once it is merged, tidy up: close the agent that worked on the branch if
you started it (`conch close NAME`), and run `conch worktree clean -y`,
which removes finished worktrees only — merged, nothing uncommitted, no
pane in them. Never add `-force` there.

## Rules

- Never force-push, and never merge red.
- A conflict with the base: ask the branch's agent to rebase or merge the
  base and keep both sides; stop and tell the person when they really
  contradict. Never delete a file or a test to get past one.
- Use `conch agent prompt` to message an agent, never `conch send`.
