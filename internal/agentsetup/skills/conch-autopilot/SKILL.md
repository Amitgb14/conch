---
name: conch-autopilot
description: Set up an automated developer workflow for a project in conch — the check every finished branch must pass, a reviewer agent, failed checks and CI sent back to the agent, pull requests opened and merged when green, guardrails, and the ways to feed it work (issues, a queue, a schedule). Use when you are inside conch (CONCH_PANE_ID is set) and the person asks to automate their development workflow, set up the autopilot, or have agents work through tasks on their own.
metadata:
  installed-by: conch
---

# An automated developer workflow

conch's server can run the line a finished branch takes on its own, with
no window open:

```
agent finishes → check → review → pull request → fix CI → merge
```

You set it up with the person, step by step. **Propose, then apply only
what they agree to**: this is their configuration and their repository.
Nothing here writes code; it decides what happens to code agents write.

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

If not, this skill does not apply: say so.

## 1. Learn the project

- Its conch ID, from the project list (the row whose path is this
  repository):

  ```sh
  conch project ls
  ```

- Its check: the command that says a change is good — the Makefile's
  `lint` and `test`, `package.json`'s scripts, the README, CI's workflow
  file. Prefer what CI runs. Run it once in your worktree, and note
  whether it passes and how long it takes. A check that fails on the base
  branch already would send every branch back: tell the person and stop
  there.
- Which agents are installed, for the reviewer: a different agent from
  the one that usually builds finds more.

  ```sh
  conch agent status
  ```

- What a new worktree needs before an agent can work in it (`npm ci`,
  `go mod download`, a virtualenv) — or nothing.

## 2. Propose the policy

Show the person the `[verify]` section you suggest, with a line on what
each does, for example:

```toml
[verify]
commands = { r1a2b3c = "make lint && go test -race ./..." }  # the check, by project ID
reviewer = "codex"   # another agent reads every finished branch's diff
auto_fix = 2         # a failed check or CI run goes back to the agent, twice at most
auto_pr = true       # a passing branch is pushed and gets its pull request
auto_merge = false   # a green pull request merges itself — off until they trust it
auto_resolve = 1     # a pull request that conflicts with its base is rebased, once
require_tests = true # code changed with no test: the pull request is a draft
max_diff = 800       # past 800 changed lines: a draft, for a person to look at
```

Recommend starting with `auto_merge = false` and turning it on after a
week of pull requests they were happy with. Never turn it on unless they
ask for it: it writes to their base branch.

## 3. Apply what they agree to

The easy way for them: **Settings → Checks & autopilot**, in Conch.app or
in the TUI (`,` then the tab). It saves config.toml and hands the server
the new policy in one go.

If they ask you to make the change instead, back the file up, then edit
only the `[verify]` section of their config.toml — its path is printed by
`conch config path` — keeping every other line as it is, and show them the
diff:

```sh
conch config path
```

**Two steps are theirs, not yours.** conch refuses both from an agent's
pane (`out_of_scope`), on purpose: what merges on its own, and what runs
in every new worktree, are the person's setup, not an agent's. Don't look
for a way round; give them the commands to run in a terminal pane (or any
terminal outside conch):

- the new policy, handed to the server — until then it keeps the old one:
  `conch autopilot push` (or they open Settings → Checks & autopilot and
  save, which does the same);
- a new worktree's setup, when the project needs one:
  `conch project setup r1a2b3c "npm ci"`.

Tell them which one is waiting, and check afterwards that it took: the
policy is the one Settings shows.

## 4. Guardrails

Suggest, for them to decide:

- **Permission rules** (Settings → Permission rules, or `[[permissions]]`
  in config.toml): allow what is safe without asking — the check
  command, reading files — and deny what never should run, such as a
  force push. Everything else still asks them. Start with
  `permission_mode = "dry-run"` (under `[agents]`): the rules only record
  what they would have answered, for the person to check before turning
  them on.
- **Plan limits**: `[notify] limit_at = [80, 90, 95]`, so they hear
  before agents stop mid-task.
- `require_tests` and `max_diff`, above: drafts never merge on their own.

## 5. Feed it work

Show them the ways in, so they can pick:

```sh
conch task "Add retries to the uploader"
conch task -issue 123
conch task -queue -wip 2 -issue 124
conch task -ticket ENG-125
conch task -after api "Build the client on the new API"
conch task -every 1d -at 09:00 "Update dependencies and fix what breaks"
conch task -n 3 "Fix the flaky parser test"
conch schedule
```

`-issue` puts a GitHub issue in the prompt, and `-ticket` a Linear or
Jira one (set up in `[trackers]` in config.toml; `conch ticket ls` lists
them); `-queue -wip 2` starts it once fewer than two agents are busy
(working, waiting, or just started); `-every` repeats a task, each run on a branch of its
own; `-n 3` makes three attempts to keep the best. Asking an agent to
"take the ready issues to merged pull requests" is the `conch-factory`
skill, which runs the same line with agents in panes.

To have the backlog taken with nobody handing it in, offer intake: a label
per project in `[verify.intake]` (`r1a2b3c = "ready"`, and `intake_wip`
for the queue's limit). The server reads the open issues with that label
every few minutes, queues each one once as `-issue N`, and comments on the
issue. Which label means "take this" is the person's choice — ask, don't
pick one — and, as with the rest of `[verify]`, set it only once they agree:
every issue it takes spends their usage.

The autopilot does not touch a ticket by itself. If the person wants
theirs to follow the work — a comment when the pull request opens, moved
to In Review, then Done once it merges — that is something they ask for,
and it is done with `conch ticket comment`, `conch ticket link` and
`conch ticket status` as each step happens.

## 6. Try it once

Offer one small, real task to see the whole line work — every agent
spends their usage, so only if they want it. Watch it with
`conch wait -state done,waiting -timeout 30m PANE`, and show them where it
went: the branch, the check's result, the review, the pull request.

## 7. Report

Say what is now set — the check, the reviewer, what is automatic and what
still waits for them — and how to turn any of it off again (the same
Settings page).

## Rules

- Propose before you change anything; change only what they agreed to.
- Never turn on `auto_merge` unless asked.
- Don't run `conch autopilot push` or `conch project setup` yourself:
  conch refuses them from your pane. Hand them to the person.
- Never edit config.toml beyond `[verify]` and rules they approved, and
  never put a secret in it: a variable's name, `${TOKEN}`, not its value.
- Don't push, merge or discard branches yourself: that is what the
  autopilot, or the person, does.
- Use `conch agent prompt` to message an agent, never `conch send`.
