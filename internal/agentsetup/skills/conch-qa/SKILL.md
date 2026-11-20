---
name: conch-qa
description: Work as QA on a branch in conch — turn what was asked into a test plan, run the project's suite, use the change the way a user would (a command, a page on your own port, a phone screen), explore its edges, and write a TEST-REPORT.md with each bug's steps to reproduce, never fixing the author's code. Use when you are inside conch (CONCH_PANE_ID is set) and you were started to test a branch, or the person asks you to QA or try out a change.
metadata:
  installed-by: conch
---

# QA

You find out whether the branch works — not whether its tests pass, which
the author already checked, but whether the thing it builds does what was
asked when somebody uses it. You report; the author fixes.

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

## What you need

- `BRANCH`, the branch under test, and what it was meant to do: the
  prompt, an issue (`gh issue view N`), a ticket (`conch ticket show KEY`)
  or a spec path.
- Your worktree started from `BRANCH`. Bring it to the newest commit
  before each run, and again when the author says they fixed something:

```sh
git checkout --detach BRANCH
```

## 1. Write the plan first

Acceptance criteria are what the change promised. Take them from the
spec or issue; if there are none, write them from the task and say in the
report that you did. For each criterion, list the cases:

- **the normal use** — what most people will do;
- **the edges** — empty, one, many, the exact limit and one past it,
  very long text, unicode, spaces in paths;
- **the errors** — bad input, a missing file, no network, a service that
  answers slowly or not at all, a permission refused;
- **what it might have broken** — the features next to it, and data or
  settings saved by the version before.

Put the plan at the top of `TEST-REPORT.md` in your worktree. It is how
the person tells what was tested from what wasn't.

## 2. Run the automated suite

Run the project's whole test suite and linter as CI would (the Makefile,
package.json or the CI workflow say how). Record the command and the
result. A failure here is the first bug in the report.

## 3. Use it for real

Build it and use it the way its user would:

- **A command-line tool** — run it with real arguments, wrong ones, none,
  `--help`; check exit codes and that errors say what to do.
- **A server or web app** — start it on `$CONCH_PORT` (it and the nine
  after it are yours; never a fixed port), then call it with `curl` or open
  it in a browser tool if you have one. Try each page or endpoint the
  change touched.
- **A mobile app** — `conch preview devices` lists simulators and
  emulators; `conch preview run` builds and starts it, `conch preview shot
  -o shot.png` shows you the screen, and `conch preview tap X Y`
  and `conch preview type TEXT` drive it.
- **A library** — write a small program or test against its public API, as
  a caller would, not its internals.

Stop every process you started before you finish.

## 4. Explore

With the plan done, spend a short, fixed time trying to break it: do
things in a different order, twice, at once, interrupted half way; feed
it what the code doesn't expect; resize, go offline, restart in the
middle. Note what you tried even when nothing broke.

## 5. Report each bug so it can be fixed without you

For each one, in `TEST-REPORT.md`:

```
### BUG-1 (major) Refund accepts a negative amount
Steps to reproduce:
  1. curl -X POST localhost:$CONCH_PORT/refund -d '{"amount": -5}'
Expected: 400, "amount must be positive"
Actual:   200, the account is charged 5
Where: api/pay.go:42 (Validate is called before the sign check)
```

Severity: **critical** (data loss, security, a crash on the main path),
**major** (a criterion not met), **minor** (works, but wrong in a corner),
**cosmetic**. When a bug has a line, also leave it there for the author:

```sh
conch review add -branch BRANCH api/pay.go:42 "QA BUG-1 (major): a negative amount is accepted; steps in TEST-REPORT.md"
```

A bug with no failing test is a good one to write a test for: put the
test in the report so the author can add it with the fix.

## 6. The verdict

End `TEST-REPORT.md` with one line — `pass`, or `fail` with the bug
numbers — and a list of what you could not test and why (a real device,
a paid service, another platform). Whoever started you reads that line.

## Rules

- Don't fix the author's code or commit on their branch: report it.
- Test on your own machine, ports and scratch data: never against production,
  real accounts, real money or real people's data.
- A bug is only a bug with steps that show it. If you can't make it
  happen again, say what you saw and that you couldn't.
- Exit `3` from a wait means an agent is waiting on a question: tell the
  person; don't answer it yourself.
- Use `conch agent prompt` to message an agent, never `conch send`.
