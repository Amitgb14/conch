---
name: conch-review
description: Have another coding agent review a branch through conch, send its findings to the agent that wrote the code, and repeat until the review is clean — review, fix, review again. Use when you are inside conch (CONCH_PANE_ID is set) and the person asks for a review, a second opinion, or a review-and-fix loop on a branch.
metadata:
  installed-by: conch
---

# Review, fix, review again

A second agent reads the branch with fresh eyes and leaves comments on its
lines. The agent that wrote the code gets them, fixes or answers each one,
and the reviewer looks again. You run the loop; the person sees every
comment in conch's changes view.

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

## What is being reviewed

You need the branch (`BRANCH`), the base it goes into (`BASE`, usually
`main` or `master`) and, if there is one, the pane of the agent that wrote
it (`AUTHOR`). `conch status` lists panes with their branches.

The reviewer sees only what is committed. If the branch has uncommitted
work, ask its author to commit it first; don't commit somebody else's work
yourself.

## Start the reviewer

Use a different agent from the author where one is installed — a second
opinion from the same model finds less:

```sh
conch task -name review-BRANCH -agent codex -base BRANCH "You are reviewing branch BRANCH against BASE. Read the diff with: git diff BASE...BRANCH. If there is a spec or an issue for it, check fidelity first: does it do all of what was asked, shown by tests, and nothing outside it? Then look for bugs, missing tests, broken edge cases and unclear code, most serious first. For each finding add a comment on its line with: conch review add -branch BRANCH FILE:LINE 'what is wrong and what to do'. Don't change any code. When you are done, write REVIEW.md in your worktree: one line per finding, or 'clean' if there is nothing to fix."
conch wait -state done,waiting -timeout 30m review-BRANCH
```

`git diff BASE...BRANCH` always shows the branch's latest commits: every
worktree shares the repository's branches. Read `REVIEW.md` in the path
`conch task` printed.

## Send the findings to the author

```sh
conch review -branch BRANCH           # what is waiting, with IDs
conch review send -branch BRANCH      # hand them to the agent on the branch
conch wait -state done,waiting -timeout 45m AUTHOR
```

With no agent on the branch, `conch review send -branch BRANCH -agent claude` starts one in its worktree. The author answers each comment
with `conch review reply ID "..."` — what it changed, or why it left it.

## Review again

```sh
conch agent prompt -wait -timeout 30m review-BRANCH "The author has answered your comments and pushed new commits to BRANCH. Read conch review -branch BRANCH for their replies, review git diff BASE...BRANCH again, add comments only for what is still wrong, and rewrite REVIEW.md: 'clean' if nothing is left."
```

Repeat send → fix → review until `REVIEW.md` says clean. Stop after three
rounds even if it doesn't, and tell the person what is still open: a loop
that doesn't converge needs a person, not a fourth round.

## Finish

Tell the person what was found, what was fixed and what is left, then
clean up after the reviewer — it changed no code, so nothing of it needs
keeping:

```sh
conch close review-BRANCH
conch branch discard -n -branch REVIEW_BRANCH       # the branch conch task printed for it
conch branch discard -force -branch REVIEW_BRANCH   # only if -n lists just REVIEW.md
```

If `-n` lists anything besides `REVIEW.md` and the commits of BRANCH it
read, leave it and say so. BRANCH itself — the author's — stays. Don't
merge or push — if the person wants the branch shipped, that is
`conch-ship`.

## Rules

- The reviewer comments; it never edits the author's code.
- Exit `3` from a wait means an agent is waiting on a question: tell the
  person which pane and why; don't answer it yourself.
- Only prompt and close panes you started, or ones in your own project.
- Use `conch agent prompt` to message an agent, never `conch send`.
