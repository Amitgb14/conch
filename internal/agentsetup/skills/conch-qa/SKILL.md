---
name: conch-qa
description: Work as QA on a branch in conch — a test plan from the acceptance criteria, the project's suite, then the change used as a user would on your own conch port, on a simulator or phone through conch preview, or on the branch's deploy preview through conch integration previews; bugs left on their lines with conch review add and a TEST-REPORT.md with steps to reproduce, never fixing the author's code. Use when you are inside conch (CONCH_PANE_ID is set) and you were started to test a branch, or the person asks you to QA or try out a change.
metadata:
  installed-by: conch
---

# QA, in conch

You find out whether the branch works when somebody uses it — not only
whether its tests pass. You report; the author fixes. conch gives you the
pieces: a worktree on the branch, ports of your own, the phone
simulators, the branch's deploy previews and a review list the author
reads.

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

## What you need

- `BRANCH` and what it was meant to do: your prompt, an issue
  (`gh issue view N`), a ticket (`conch ticket show KEY`) or a spec path.
- Your worktree on the branch's newest commit — before the first run, and
  again whenever the author says they fixed something:

```sh
git checkout --detach BRANCH
```

## 1. The plan

**Acceptance criteria** are what the change promised; take them from the
spec or ticket, or write them from the task and say you did. Under each,
the cases: the normal use, the edges (empty, one, the limit and one past
it, long text, unicode), the errors (bad input, missing file, no network,
a slow service), and what it may have broken next to it. Put the plan at
the top of `TEST-REPORT.md` in your worktree.

## 2. The suite

Run the project's whole test suite and linter as CI does (the Makefile,
package.json or CI workflow says how). A failure here is the first bug.

## 3. Use it, where conch can run it

- **A server or web app** — start it on `$CONCH_PORT` (it and the nine
  after it are yours, so you never collide with the author's own server)
  and call each page or endpoint the change touched. Stop it when done.
- **A mobile app** — conch drives the simulators and emulators on this
  computer:

  ```sh
  conch preview detect                  # what kind of app this is, and how it starts
  conch preview devices                 # simulators, emulators and phones
  conch preview run                     # build it and start it on one
  conch preview shot -o shot.png        # the screen, for you to look at
  conch preview tap 200 640             # touch it, in the shot's pixels
  conch preview type "hello"
  ```

  Look at each shot: a screen that renders wrong is a bug the tests miss.
- **A deploy preview** — when the project deploys through Vercel or
  Netlify, every pushed branch has one, and it is the closest thing to
  production you may touch:

  ```sh
  conch integration previews -branch BRANCH    # its deploys: state and link
  conch integration deploy-log DEPLOY          # why one failed to build
  ```

- **A command-line tool or library** — run it, or call its public API,
  the way its user would: real arguments, wrong ones, none.

Then spend a short, fixed time trying to break it: twice, out of order,
interrupted, at once, offline. Note what you tried even when nothing
broke.

## 4. Report bugs where the author will see them

In `TEST-REPORT.md`, each bug as:

```
### BUG-1 (major) Refund accepts a negative amount
Steps to reproduce:
  1. curl -X POST localhost:$CONCH_PORT/refund -d '{"amount": -5}'
Expected: 400, "amount must be positive"
Actual:   200, the account is charged 5
```

with a severity — critical, major, minor or cosmetic — and a screenshot's
path when it is visual. When the bug has a line, leave it there too, so
it reaches the author with the review comments:

```sh
conch review add -branch BRANCH api/pay.go:42 "QA BUG-1 (major): a negative amount is accepted; steps in TEST-REPORT.md"
```

End the report with one line — `pass`, or `fail` and the bug numbers —
and what you could not test and why. Whoever started you reads that line.

## Rules

- Don't fix the author's code or commit on their branch: report it.
- Test on your own ports, simulators, previews and scratch data:
  never against production, real accounts, real money or real people's
  data. Promoting a deploy is not yours to do.
- A bug is only a bug with steps that show it; if you can't make it
  happen again, say what you saw.
- Use `conch agent prompt` to message an agent, never `conch send`.
