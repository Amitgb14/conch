---
name: conch
description: Hand work to another coding agent — a reviewer, a tester, a second opinion — and read what it did, when you are running inside conch (CONCH_PANE_ID is set). Use it to start a helper agent on its own branch, prompt it, wait for it and read its answer, instead of asking the person to do the hand-off.
metadata:
  installed-by: conch
---

# Working with other agents through conch

conch is the terminal you are running in. It can start other coding agents
(Claude Code, Codex, Gemini CLI, OpenCode, Devin) in panes of their own, each on
its own git branch and worktree, and lets you prompt them and read their
answers. The person sees every pane you start in conch's tree and can step
in at any time.

## First, check you are in conch

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

If either is missing, you are not in a conch pane: don't use this skill.

## Start a helper on its own branch

```sh
conch task -name reviewer -agent codex "Review the changes on this branch against main. Write your findings to REVIEW.md, most serious first."
```

- It prints `PANE  WORKTREE  BRANCH`. The helper works in that worktree, on
  that new branch, starting from the project's base branch.
- To have it start from **your** work, commit first and pass your branch:
  `-base "$(git branch --show-current)"`. It sees only what you committed.
- `-name` lets you address it as `reviewer` from then on. Pick a name no
  other running pane has.
- `-agent` is `claude`, `codex`, `gemini`, `opencode` or `devin`; leave it
  out for the person's default.
- Start one helper per job, and only as many as the task needs: each one
  is an agent spending the person's usage.

## Prompt it and wait for the answer

```sh
conch agent prompt -wait -timeout 30m reviewer "Check finding 3 again after my fix in 4e1a2c0"
```

It exits:

- `0` — the helper finished the work your message started;
- `3` — the helper is **waiting for an answer** (a permission prompt, a
  question, a menu). Nothing was typed. Read what it is asking (below). Do
  not prompt it again and do not answer it yourself: tell the person which
  pane is waiting and why, and let them decide;
- `124` — the timeout ran out; the helper may still be working;
- anything else — a real error; read the message.

To wait without prompting: `conch wait -state done,waiting -timeout 30m reviewer`.

## Read what it did

- Prefer files: ask the helper to write its result to a file in its
  worktree (the path `conch task` printed), then read that file.
- `conch read reviewer` prints its visible screen — enough for a short
  answer or to see what it is asking.
- `conch status` lists every pane with its agent and state.

## Finish

Close the helpers you started once you have what you need:
`conch close reviewer`. Their branches and worktrees stay for the person
to review, merge or discard.

## Rules

- Use `conch agent prompt` to message an agent, never `conch send`:
  `send` types anywhere, including onto a question the agent is asking.
- Only change panes you started, or panes in your own project. conch
  refuses the rest with `out_of_scope`; don't look for another way
  round — tell the person.
- Never run `conch server stop` or `conch server reload`, and don't close,
  rename or type into panes you didn't start.
- Don't use `-m MACHINE` (another machine) unless the person asked for it.
- Don't merge, push or discard a helper's branch yourself unless the
  person asked; leave that to them.
