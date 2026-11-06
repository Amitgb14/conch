---
name: conch-team
description: Run several coding agents at once through conch — the same task tried by two or three agents to keep the best, or one large task split into parts that run side by side. Use when you are inside conch (CONCH_PANE_ID is set) and the person asks for several agents, a race, attempts to compare, or work done in parallel.
metadata:
  installed-by: conch
---

# Several agents at once

conch runs each agent in a pane of its own, on a branch and worktree of its
own, so agents working side by side never touch each other's files. You
start them, wait for them and read what they did; the person watches every
one of them in conch's tree and can step in.

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

If not, this skill does not apply: say so and do the work yourself.

## Choose the shape

- **A race** — one task, several attempts, keep the best. Good when the
  approach is unclear (a hard bug, a design with several answers).
- **A split** — one task cut into parts that don't share files, one agent
  each. Good when the parts are really independent (a backend change and
  its client, three separate modules).
- **A chain** — parts that depend on each other. Each starts from the
  branch before it.

Every agent spends the person's usage. Start two or three, not ten, and say
how many you are starting before you start them.

## A race

```sh
conch task -n 3 -name parser-fix "Fix the parser crash on empty input. Add a test that fails without the fix."
conch task -agent claude,codex -name parser-fix "Fix the parser crash on empty input. Add a test that fails without the fix."
```

`-n 3` makes three attempts with the person's default agent; `-agent
claude,codex` makes one attempt per agent named. Each prints `PANE
WORKTREE BRANCH` — keep those lines. Then wait for every attempt:

```sh
conch wait -state done,waiting -timeout 45m PANE
```

Exit `0` is done, `3` is waiting on a question (tell the person which pane
and why — don't answer it yourself), `124` is the timeout.

Compare the attempts by what they did, not what they said:

```sh
git diff BASE...BRANCH --stat
git diff BASE...BRANCH
```

Run each attempt's tests in its own worktree. Recommend one with your
reasons — passing tests, a smaller diff, a test that really fails without
the fix — and let the person choose. Put the finalists in front of them:

```sh
conch show PANE1 PANE2
```

## A split

Start one task per part, each with a name and a prompt that says which
files are its and which are not:

```sh
conch task -name api "Add the /export endpoint in server/. Don't touch web/."
conch task -name web "Add the Export button in web/, calling POST /export. Don't touch server/."
```

Then wait for each and read its result. If a part needs another part's
work, it is a chain, not a split.

## A chain

```sh
conch task -name schema "Add the invoices table and its migration."
conch task -after schema -name api "Add the invoices API on top of the new table."
```

`-after` waits for that pane or branch to finish and starts from its
branch, so the second agent sees the first one's commits. Ask each agent to
commit its work, or the next one will not see it.

## Holding the number down

To run a list of tasks with at most two agents at a time:

```sh
conch task -queue -wip 2 "First task"
conch task -queue -wip 2 "Second task"
conch task -queue -wip 2 "Third task"
```

`conch schedule` lists what is queued.

## Keep an eye on them

```sh
conch status          # every pane, its agent and state
conch read api        # what an agent's screen shows
conch view            # what the person is looking at now
```

Prompt one that needs more direction with `conch agent prompt -wait
-timeout 30m api "..."` — never `conch send`, which types even onto a
question the agent is asking.

## Finish

Report each agent's branch, what it did and whether its tests pass. Close
the panes you started once you have what you need (`conch close api`);
their branches and worktrees stay for the person. Don't merge, push or
discard a branch unless the person asked — that is `conch-ship`'s job.

## Rules

- Only start, prompt, read and close panes you started. conch refuses
  others with `out_of_scope`: tell the person rather than look for a way
  round.
- Never run `conch server stop` or `conch server reload`.
- Don't use `-m MACHINE` unless the person asked for another machine.
