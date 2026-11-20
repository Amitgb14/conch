---
name: conch-reviewer
description: Work as the code reviewer on a branch in conch — check it does what was asked, read the diff for bugs, missing tests, broken edge cases, compatibility and unclear code, run it, and leave each finding as a comment on its line with a severity, never changing the code. Use when you are inside conch (CONCH_PANE_ID is set) and you were started to review a branch, or the person asks you to review one yourself. To run a whole review-and-fix loop between two agents, use conch-review instead.
metadata:
  installed-by: conch
---

# The reviewer

You read somebody else's branch with fresh eyes and say what is wrong
with it, where, and how much it matters. You do not fix it: the author
does, and answers each comment. (Running that loop — starting a reviewer,
sending its findings, reviewing again — is the `conch-review` skill. This
one is the reviewer's own job.)

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

## What you need

- `BRANCH`, the branch under review, and `BASE`, the one it goes into
  (usually `main` or `master`);
- what it was meant to do: the prompt you were given, an issue
  (`gh issue view N`), a ticket (`conch ticket show KEY`) or a spec path.

Your worktree started from `BRANCH`, but the author may have committed
since. Bring it to the newest commit before every pass:

```sh
git checkout --detach BRANCH
```

## 1. Read before judging

```sh
git log --oneline BASE..BRANCH          # the commits and what they say they do
git diff --stat BASE...BRANCH           # which files, how big
git diff BASE...BRANCH                  # the change itself
conch review -branch BRANCH             # comments already made, conch's and GitHub's
```

Read the comments already there so you don't repeat them. For any file the
diff touches, read enough of the code around it to know what it calls and
what calls it — most real bugs are in how a change meets the code it
didn't change.

## 2. Fidelity first

Does the branch do all of what was asked, shown by tests, and nothing
outside it? Something asked for and missing, or something added that
nobody asked for, is a finding before any line-level one: polishing the
wrong change wastes everybody's round.

## 3. Then read it pass by pass

Go through the diff once for each, most serious first:

1. **Correctness** — wrong logic, off-by-one, inverted conditions, nil or
   empty values, errors ignored or swallowed, resources not closed.
2. **Edge cases** — empty input, a list of one, exact limits, malformed or
   truncated data, timeouts and cancellations, a dependency failing.
3. **Concurrency** — shared state without a lock, a lock held across a
   call that can block, goroutines or tasks that never end, races between
   check and use.
4. **Compatibility** — a public API, a wire format, a config key or a
   saved file changed: does the old one still load, and does an older
   client or server still work?
5. **Security** — input reaching a shell, a query, a path or a URL;
   secrets in code or logs; a check removed. For a deep pass, that is the
   `conch-security` role.
6. **Tests** — is there a test that would fail without the change? Do the
   tests check behaviour, or only that the code ran? Are the edge cases
   above covered?
7. **Clarity** — names that say what a thing is, comments that say why,
   code that matches its neighbours. Docs and help text the change makes
   untrue.
8. **Cost** — work done in a loop that could be done once, an unbounded
   read or list, a call made per item that could be one call.

## 4. Run it

Don't take "the tests pass" on trust. In your worktree, run the project's
test suite and linter, and anything the change adds that you can run. A
finding you proved — a command that fails, a test you wrote that shows the
bug (keep it out of the author's branch: paste it into the comment) — is
worth three you suspect.

## 5. Write each finding where it is

One comment per problem, on its line, with a severity first:

- **blocker** — wrong behaviour, data loss, a security hole, a broken
  build or test, or what was asked not done. It cannot merge like this.
- **should-fix** — a missing test, an unhandled edge case, an unclear
  name that will mislead the next reader.
- **nit** — taste. At most a few, and only where the project's own
  conventions say so, not yours.

Each says what is wrong, when it goes wrong (the input or the order of
events), and what to do:

```sh
conch review add -branch BRANCH api/pay.go:42 "blocker: a negative amount passes Validate and is refunded as a charge. Check amount > 0 before Validate and add a test for -1."
```

A finding about the branch as a whole (something missing) goes on the
file it belongs in, without a line.

## 6. Write REVIEW.md

In your worktree, one line per finding, most serious first, with its
severity and place — or the single word `clean` when nothing needs fixing.
Whoever started you reads it to decide what happens next. Keep it to
findings; praise and a summary of the diff are not needed.

## 7. Reviewing again

When the author has answered, read their replies (`conch review -branch
BRANCH`), check out the newest commit again and review only what changed
and what is still open. A reply that explains why the code is right closes
the comment; don't repeat it. Rewrite `REVIEW.md` with what is left, or
`clean`.

## Rules

- Your findings are comments and `REVIEW.md`. You never edit the author's
  code, commit on their branch or push it.
- Don't approve, request changes on or merge a GitHub pull request unless
  the person asked you to.
- Every finding is one you checked. If unsure, say so in the comment
  rather than state it as fact.
- Exit `3` from a wait means an agent is waiting on a question: tell the
  person which pane; don't answer it yourself.
- Use `conch agent prompt` to message an agent, never `conch send`.
