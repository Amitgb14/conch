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
conch task -name reviewer -agent codex "Review the changes on this branch against main. Run any commands in the foreground, not in the background. Write your findings to REVIEW.md, most serious first."
```

- It prints `PANE  WORKTREE  BRANCH`. The helper works in that worktree, on
  that new branch, starting from the project's base branch.
- It sees only what is committed on the branch it starts from. If your
  work is committed, pass your branch: `-base "$(git branch --show-current)"`.
  If it isn't, don't commit just to hand it off — the person decides what
  is committed. Put the diff (`git diff`) in the prompt instead, or give the
  helper your worktree's path to read.
- Files it needs that aren't committed — a spec, a plan, notes kept out of
  git — don't reach its worktree on their own. Files git ignores are copied
  in from the main checkout when they match the project's local files
  (`conch project files`; `.env` does by default): if the helper needs one
  that doesn't match, ask the person to add the pattern — don't change the
  project's patterns yourself. Otherwise give the helper the file's
  absolute path to read. Never commit a private file, or copy it into the
  helper's worktree, to hand it over.
- Each checkout has ports of its own: start servers on `$CONCH_PORT` (it
  and the nine after it are yours), never a fixed port another worktree's
  agent may be using.
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

- `0` — the helper's turn ended. That is usually the work done, but not
  always: a helper that put a long command in the background ends its turn
  and picks the work up later, and one whose request failed (an API error)
  ends it too. So check the result — the file you asked for — before you
  rely on it; if it isn't there yet, `conch read` the helper and wait again;
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
- `conch status` lists every pane with its agent and state, and the host of a terminal on ssh (`conch status -json` for the same as JSON).

## Leave review comments

When you are the reviewer, comments on the branch can say what a findings
file says, where the person reviews anyway:

```sh
conch review add -branch feature/login api/login.py:42 "This swallows the timeout; let it raise."
conch review -branch feature/login        # what is waiting, with IDs
conch review send -branch feature/login   # hand them to the agent on that branch
```

They show in the person's changes view and on their phone, marked as
yours. Leave the sending to the person unless you were asked to send them.

`conch review -branch …` also lists what reviewers said on the branch's
pull request, marked `on GitHub`, with their replies: read those before
reviewing, so you don't repeat them.

When comments are sent *to* you, each of conch's own has an id. Once you
have dealt with one, answer it in a sentence — what you changed, or why
you left it — and the person reads it under the line it is about:

```sh
conch review reply c1a2b3c4 "Moved the check above Validate; a negative amount never reaches it."
```


## Clean up after yourself

Every helper is an agent left running — its own processes, its own MCP
servers — and every task is a branch and a worktree on disk. Leave only
what somebody still needs:

1. **Close each pane you started** once you have what you need from it:
   `conch close reviewer`. A helper that finished is still running until
   it is closed.
2. **Discard a read-only helper's branch** — a reviewer's or a tester's,
   which wrote notes (`REVIEW.md`) and changed no code. It is the branch
   `conch task` printed (the third column; `conch status` lists them
   too). Close its pane first, then look before you delete:

   ```sh
   conch branch discard -n -branch HELPER_BRANCH       # what would go
   conch branch discard -force -branch HELPER_BRANCH   # only if that is just its notes
   ```

   `-n` names the worktree and anything it would lose. Go ahead only when
   that is the helper's own notes and commits that are already on the
   branch it read. Anything else — files you don't recognise, commits of
   its own — stop and tell the person instead.
3. **A helper that wrote code** keeps its branch: that work is the
   person's to review, merge or discard.
4. **After a branch is merged**, its worktree is done with:

   ```sh
   conch worktree ls        # leftovers, and why each is or isn't finished
   conch worktree clean -y  # removes only finished ones: merged, nothing uncommitted, no pane in it
   ```

   Never add `-force` there: it removes worktrees that still hold work.

Say what you closed and removed when you report back.

## The same work as tools, if your client has them

If your client lists conch's MCP tools — `list`, `read`, `start`, `prompt`,
`wait`, `task`, `rename` — use those instead of the commands above. They are
the same work: one server, the same scope, the same refusals, but a typed
result rather than text to parse. If they are not listed, the commands above
are the way, and the person can add the tools with
`conch agent library mcp` and `conch agent library apply`. Mixing them is
fine; don't ask the person to set them up mid-task.

## Showing the person what you found

`conch view` is what is on their screen: the tabs, what each holds, and
which pane has the keyboard. `conch show p3 p4` asks for panes to be put
in front of them — one is brought up, several go side by side in one tab.

```sh
conch view                       # what am I looking at?
conch show reviewer tester       # put those two side by side
```

Use it when you have found what somebody asked about: "the two agents on
the parser" is a list you can name, and showing them is better than
describing them. It is a request about panes you could already type into,
so it is refused for anybody else's — and it moves them, since a pane is
shown in exactly one place.


## When conch ships your branch

If your first message ended with "How this branch is shipped", conch
pushes the branch and opens its pull request when you finish, and comes
back to you when the project's check or CI fails, when the base moves under
it, or when it wants a test. Work the way that needs:

- Write a test that fails without the change, then the change.
- Commit in small commits whose messages say what changed and why, and
  leave nothing that belongs to the work uncommitted.
- Don't push, force-push or merge: conch does, after checking the work.
- Asked to resolve a conflict, keep both sides — every change the base
  made and every change the branch made. Where they really contradict,
  stop and say so; never delete a file or a test to get past one.

## Rules

- Use `conch agent prompt` to message an agent, never `conch send`:
  `send` types anywhere, including onto a question the agent is asking.
- Only change panes you started, or panes in your own project. conch
  refuses the rest with `out_of_scope`; don't look for another way
  round — tell the person.
- Never run `conch server stop` or `conch server reload`, and don't close,
  rename or type into panes you didn't start.
- Don't use `-m MACHINE` (another machine) unless the person asked for it.
- Don't merge or push a helper's branch yourself unless the person
  asked, and discard only a read-only helper's, as *Clean up after
  yourself* says; leave the rest to them.
