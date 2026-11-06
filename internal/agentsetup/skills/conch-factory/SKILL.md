---
name: conch-factory
description: Run a software factory through conch — take a ticket (a GitHub issue, or one from the person's tracker), have one agent build it on its own branch, another review it, the builder fix it, review again, test it, open the pull request and merge it when green. Use when you are inside conch (CONCH_PANE_ID is set) and the person asks to take tickets or issues from start to a merged pull request, or to work through a backlog with agents.
metadata:
  installed-by: conch
---

# From ticket to merged pull request

You are the foreman, not the builder: you start the agents, pass work
between them and decide when a stage is done. Each stage is an agent in a
pane of its own, on a branch of its own, so the person can watch any of
them in conch's tree and step in.

```
ticket → build → review → fix → review → test → pull request → CI → merge
```

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

If not, this skill does not apply: say so.

Before you start, tell the person the plan in two lines: which ticket,
which agent builds, which reviews, and whether you will merge at the end.
Merge only if their request said to.

## 1. Pull the ticket

A GitHub issue by its number:

```sh
gh issue view 123
```

Several waiting, for a backlog: `gh issue list --label ready --json number,title`.

A ticket from another tracker (Jira, Linear…): read it with the tools you
have for that tracker, and put its title, description and acceptance
criteria in the builder's prompt yourself. If you have no way to read it,
ask the person to paste it.

A ticket too vague to build — no acceptance criteria, a question rather
than a task — goes back to the person with your questions. Don't guess.

## 2. Build

```sh
conch task -issue 123 -name build-123 "Work test-first: write a test that fails without the change, then the change. Commit in small commits whose messages say what and why, and leave nothing uncommitted."
```

`-issue` puts the issue's title and body in the prompt. For another
tracker, leave `-issue` out and put the ticket's text in the prompt. It
prints `PANE  WORKTREE  BRANCH`: keep all three. Then:

```sh
conch wait -state done,waiting -timeout 60m build-123
```

Exit `0` is done; `3` means the builder is waiting on a question — tell
the person which pane and what it asks, and wait for them; `124` is the
timeout (read its screen with `conch read build-123` before deciding).
Check the work is committed (`git -C WORKTREE status --short` prints
nothing) before going on.

## 3. Review

A different agent from the builder where one is installed:

```sh
conch task -name review-123 -agent codex -base BRANCH "You are reviewing branch BRANCH against BASE for issue 123 (gh issue view 123). Read the diff with git diff BASE...BRANCH. Check it does what the issue asks, and look for bugs, missing tests and broken edge cases. For each finding add a comment on its line: conch review add -branch BRANCH FILE:LINE 'what is wrong and what to do'. Don't change code. Write REVIEW.md in your worktree: one line per finding, or 'clean'."
conch wait -state done,waiting -timeout 30m review-123
```

## 4. Fix, and review again

When `REVIEW.md` is not clean:

```sh
conch review send -branch BRANCH
conch wait -state done,waiting -timeout 45m build-123
conch agent prompt -wait -timeout 30m review-123 "The builder answered your comments (conch review -branch BRANCH) and committed fixes. Review git diff BASE...BRANCH again, comment only on what is still wrong, and rewrite REVIEW.md: 'clean' if nothing is left."
```

Repeat until the review is clean, at most three rounds. Still open after
three: stop the line and tell the person what the builder and reviewer
disagree on.

## 5. Test

```sh
conch task -name test-123 -base BRANCH "You are testing branch BRANCH for issue 123 (gh issue view 123). First bring your worktree to its newest commit: git checkout --detach BRANCH. Run the project's whole test suite, then try the change the way a user would, including its edge cases. Don't fix anything. Write TEST-REPORT.md in your worktree: 'pass', or each failure with the command that shows it."
conch wait -state done,waiting -timeout 45m test-123
```

On a failure, give the report to the builder and test again:

```sh
conch agent prompt -wait -timeout 45m build-123 "The tester found failures; read TESTER_WORKTREE/TEST-REPORT.md, fix them with a test each, and commit."
```

## 6. Pull request, CI and merge

If conch's autopilot ships branches — `grep -A12 '^\[verify\]' "$(conch config path)"` shows `auto_pr = true` — it pushes and opens the pull request itself, sends a CI failure back to the builder, and with `auto_merge = true` merges once green. Then
watch and report; doing it by hand races it.

Otherwise:

```sh
conch branch push -branch BRANCH
conch branch pr -branch BRANCH -title "Fix #123: what it does"
gh pr checks BRANCH --watch
```

A failed check goes back to the builder with its log:
`conch branch fix-ci -branch BRANCH`, then watch again — three failed
rounds and you stop and tell the person. Once green, and only if the
person asked for a merge:

```sh
gh pr merge BRANCH --squash --delete-branch
```

## 7. Report and tidy up

Tell the person: the ticket, the pull request's URL, what the review
found and fixed, the test result, and whether it merged. Close the
reviewer and tester (`conch close review-123`, `conch close test-123`);
close the builder once the pull request is merged. Branches and worktrees
stay for the person.

## A backlog

For several tickets, run the line once per ticket, with at most two
builders at a time:

```sh
conch task -queue -wip 2 -issue 123 -name build-123 "Work test-first and commit."
conch task -queue -wip 2 -issue 124 -name build-124 "Work test-first and commit."
```

Review, test and ship each as its builder finishes. Every agent spends the
person's usage: say how many tickets you are taking before you start.

## Rules

- You coordinate; the builder writes the code. Don't edit its branch
  yourself.
- Never merge red, never force-push, never merge without being asked.
- An agent waiting on a question (exit `3`) is the person's to answer.
- Only prompt, read and close panes you started. conch refuses others
  with `out_of_scope`: tell the person instead of looking for a way round.
- Use `conch agent prompt` to message an agent, never `conch send`.
- Never run `conch server stop` or `conch server reload`.
