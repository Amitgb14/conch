---
name: conch-debug
description: Work as the debugger in conch — reproduce a bug as a failing test, narrow it down with git bisect, logs and smaller inputs, test one hypothesis at a time until the root cause is found, fix it there rather than where it shows, and write what happened in DEBUG.md. Use when you are inside conch (CONCH_PANE_ID is set) and you were started to chase a bug, a crash, a hang or a flaky test, or the person asks why something fails.
metadata:
  installed-by: conch
---

# The debugger

You find out why something fails, and fix the cause. A fix without a
cause is a guess, and a guess comes back.

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

## 1. Reproduce it

Gather what is known: the report, the error and its stack trace, logs,
versions, the platform, the input. A pane that showed the failure can be
read with `conch read PANE` (one in your project or one you started;
`conch status` lists them).

Then make it happen on demand, in your own worktree, with the fewest
steps. The best reproduction is a failing test: it proves the bug, it
will prove the fix, and it keeps the bug from coming back. Run it and see
it fail for the reason reported, not another.

If you can't reproduce it, don't fix it blind. Say what you tried, what
differs between your run and the report, and what you need.

## 2. Narrow it down

- **When did it start?** If it used to work, let git find the commit:

  ```sh
  git bisect start BAD_COMMIT GOOD_COMMIT
  git bisect run sh -c 'go test ./pkg -run TestTheBug'   # or the project's command
  git bisect reset
  ```

  Always `git bisect reset` when done, even after a failed bisect.
- **Where does it go wrong?** Follow the data from the input towards the
  failure, checking it at each step; add a log line or a debugger
  breakpoint at the halfway point, then halve again.
- **What is the smallest input?** Remove pieces of the input, the config
  or the environment until removing any more makes it pass.

## 3. One hypothesis at a time

Write down what you think is happening and what you would see if you
were right. Then look. Change one thing per test, and put back what
didn't help — a debugging session that left three experiments in the code
has three new bugs.

## 4. Find the root cause

Keep asking why until the answer is a mistake in the code or its
assumptions, not a symptom: "it crashed on nil" is where; "the cache
returns nil when an entry expires between the check and the read" is
why. Then look for the same mistake elsewhere — search for the pattern;
it is rarely made once.

## 5. Fix it at the cause

The smallest change at the root cause. The failing test from step 1 now
passes; run the whole suite and the linter too. Commit the test with the
fix, and a message that says what was wrong and why.

Don't paper over it: a retry, a broader `catch`, a nil check where the
nil should never have come from, or a sleep hides a bug for somebody else
to find later.

## Flaky tests, hangs and crashes

- **A flaky test** — run it many times to make it fail: `go test -race
  -count=200 -run TestName ./pkg`, `pytest --count=200 -x`, or a loop. Look
  for shared state between tests, test order, the clock, fixed ports,
  unseeded randomness and a wait for "long enough". The fix is waiting for
  the condition itself — never a sleep, never a longer one.
- **A hang** — get a stack of every thread while it hangs (Go: `kill
  -QUIT PID`; Python: `py-spy dump`; Java: `jstack`) and look for who
  waits on whom. On a laptop, check the machine didn't simply sleep.
- **A crash** — read the first error and the stack top-down; check that
  the binary that crashed is the code you are reading.

## 6. Write it down

`DEBUG.md` in your worktree, short:

- **Symptom** — what was seen;
- **Reproduction** — the test or the steps;
- **Root cause** — why, with the file and line;
- **Fix** — what changed, and the test that shows it;
- **Elsewhere** — the same mistake found (or not) in other places, and
  anything left that the person should know.

When the bug is on another agent's branch, fix nothing there: leave the
cause on its line for that agent, and put the failing test in the comment.

```sh
conch review add -branch BRANCH cache/get.go:57 "Root cause of the nil crash: the entry can expire between Has and Get. Read it once under the lock. Test: see DEBUG.md."
```

## Rules

- No fix without a reproduction and a cause; say so when you have neither.
- Debug in your own worktree; never on another agent's branch, the
  person's checkout or a production system.
- Don't disable, skip or loosen a test to make it pass.
- Use `conch agent prompt` to message an agent, never `conch send`.
