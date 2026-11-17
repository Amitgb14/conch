# The software factory in conch

Status: proposal, 2026-11-17. Research: [software-factory-research.md](software-factory-research.md).
Mocks: https://claude.ai/artifact/TZ1b6Bas1wkXurw6CSbram (floor, run, inbox, line).

## The problem

conch already runs most of a software factory: issues come in by label, an
agent triages them, a builder works on its own branch, the check runs, CI
failures go back to the agent, conflicts are rebased, and green pull
requests merge behind gates. But the pieces don't share a record, and
nothing shows the line:

- A task is a pane plus a branch whose name carries the issue number. When
  the pane closes, what happened to the ticket can only be pieced together
  from `autopilot.json` maps, the activity feed and the branch name.
- The reviewer runs only in the TUI, so the server ships a branch after its
  check without waiting for a review, and a review never blocks a merge.
- The spec is a file in `$TMPDIR` written by the `conch-factory` skill's
  foreman agent. The server never sees it and never records that the person
  approved it.
- Nothing validates the work against tests the builder couldn't see, which
  the research says is where factories fail: agents pass the tests they can
  read.
- Merges are one at a time as each goes green, with no queue that rebases
  and re-checks the next one.
- Cost is recorded per pane and limited per plan; nobody can say what a
  ticket cost or stop one that runs away.
- In Conch.app there is no picture of it: the Tickets window is a list, the
  autopilot is a feed beside the commit graph, and Settings has a row of
  toggles.

## The idea

A **run** is one ticket's journey down a **line**. The server owns both.

- A **line** is the project's stages, in order, each with who does it (an
  agent, a command, conch itself, or a person) and what lets the run move on
  (a gate). It is written in config, so the TUI, CLI, phone and app see the
  same line.
- A **run** is stored (`factory.json`, beside `autopilot.json`): the ticket,
  the line it is on, its stage, each stage's attempts with the panes and
  commits they made, the gates it passed and who passed them, the spec, the
  validation results, and the tokens and cost spent. It outlives its panes.
- The existing machinery does the work: intake and triage start runs,
  `task.create` builds, `[verify] commands` checks, `auto_fix` and
  `auto_resolve` repair, `ship`/`autopilotMerge` land. The factory is the
  record and the gates around them, not a second autopilot.

This keeps the layering conch already has: the server works with no window
open; clients only draw it and answer its gates.

### The line

```toml
# .conch/factory.toml, or [factory] in config.toml per project
autonomy = "L4"          # L3: you approve specs and merges · L4: specs only · L5: no gates
wip = 3                  # runs building at once (was intake_wip)
budget_usd = 15          # per run; over it the run stops and asks
merge_queue = true
holdout = "~/.config/conch/holdout/conch"   # outside every checkout the builder sees

# the default stages
stages = [
  { name = "triage",   by = "agent:claude" },                  # triage.go
  { name = "spec",     by = "agent:claude", gate = "person" }, # SPEC.md kept in the run
  { name = "build",    by = "agent:claude" },
  { name = "check",    by = "command",      fixes = 3 },        # [verify] commands, auto_fix
  { name = "review",   by = "agent:codex",  rounds = 3 },
  { name = "validate", by = "agent:claude", attempts = 2 },
  { name = "merge",    by = "conch",        gate = "ci" },     # PR, CI, auto_fix, queue
]
```

`autonomy` only moves the `person` gates: L3 adds one before merge, L5
removes the spec one. Anything else stays explicit, so what runs unwatched
is readable in one file.

### Gates and the inbox

A gate the server waits on becomes an **inbox item**: approve a spec, a
review that ran out of rounds, a run over budget, a validation that failed
twice, a merge held by a conflict the agent couldn't settle, and an agent
asking a question (today's "waiting"). Each says why, what it would do next
and what it cost so far. The inbox is the first thing the factory window
shows, on the phone too, because the research is clear that the person's
attention is the scarce part — Mollick's facilitator, Gas Town's Witness.

### Validation with holdout scenarios

Scenarios are acceptance checks written in plain language (or as scripts)
kept **outside the checkout the builder works in**: in conch's config
directory per project, or a branch the builder's worktree doesn't have. The
validator runs in a detached checkout of the branch, as triage does today,
with the scenarios copied in only there, and writes
`{scenario, pass, evidence}` rows. The run shows them as a table and the
PR body carries them. A builder told only "validation failed: scenario 4,
here is what happened" can't tune itself to the test text.

Also part of validation, cheap and mechanical: a diff that deletes or skips
tests, or touches paths the line protects, fails the stage.

### The merge queue

Green runs line up per base branch. Each in turn is rebased onto the base,
re-checked, and merged; a red one drops out back to its builder with the
reason. Branches touching the same files are flagged while they build
(conch already computes overlaps and merge-tree conflicts per worktree).

### Learn

When a reviewer flags the same kind of thing in several runs, the run page
offers to turn it into a line in the project's instructions or a check —
conch already writes instructions and skills for every agent
(`agentsetup/sync.go`).

## Conch.app

A **Factory** window per project (from the project's row menu and the
toolbar; key to be chosen — ⇧⌘I is Tickets):

1. **Floor** — columns for the line's stages, a card per run: ticket
   number and title, the agent's mark, how long in this stage, attempt
   (`review 2/3`), cost against budget, and a state chip. Above the
   columns: the inbox count, runs today, merged, the throughput and the
   time the line spends waiting on people. Columns with a person gate
   show it.
2. **Inbox** — the gates waiting on you, oldest first, each with its one
   decision (Approve spec, Give another round, Raise budget, Take over).
3. **Run** — a ticket's page: the stages as a timeline with every
   attempt, the spec (editable before approval), review rounds with their
   findings, the validation table, the PR and CI, cost per stage; open any
   pane the run used.
4. **Line** — the project's line drawn as stages, each with its worker and
   gate, the autonomy level, WIP, budget, holdout folder. It replaces
   Settings' row of toggles for a project that has a line; it writes the
   same config.

The TUI gets the floor as a list grouped by stage and the inbox; the phone
gets the inbox (approve a spec or a merge from a notification).

## Protocol

New capability `factory.v1`. Methods: `factory.line.get/set` (refused from
agent panes, as `autopilot.set` is), `factory.runs` (by project, stage,
state), `factory.run` (one, with its timeline), `factory.start` (a ticket or
a free-text brief), `factory.gate` (approve, reject with a note, another
round, raise budget, take over), `factory.stop`. Event `factory.changed`.
All payload fields `omitempty`; a client without the capability keeps
today's Tickets window and autopilot panel.

## Phases

0. **Runs from what exists.** Store a run whenever intake, triage or
   `task -issue/-ticket` starts one, and move it by what the autopilot
   already does. Floor and Run pages, read-only. No behaviour changes.
1. **Review as a server gate.** Move the reviewer into the server, with
   rounds and its findings sent through `review.*`; the merge waits for
   it.
2. **Spec and the inbox.** The spec stage, kept in the run; gates; the
   inbox in the app, TUI and phone; autonomy levels.
3. **Validation.** Holdout scenarios, the validator, the evidence table on
   the PR, the test-deletion check.
4. **Merge queue and budgets.** Queue per base; cost per run and stage;
   budget stops.
5. **Numbers and learn.** Time per stage, rounds, escapes (a revert or a
   follow-up fix of a merged run), suggestions into the instructions.

The `conch-factory` skill stays: a foreman agent is still how somebody runs
a line on a project with no line configured, and phase 0 makes its runs
visible too.

## Not doing

- A dark factory by default. L4 is the default; L5 is a setting somebody
  chooses per project.
- Digital twins of external services. A provider-file sandbox can host one,
  but writing them is the project's job.
- Decomposing one ticket into many by itself. A spec may list parts, and
  each becomes a run with `-after`; an agent splitting work unasked is the
  next step after this works.

## Open questions

- Where the line lives: `.conch/factory.toml` (shared with the team through
  git) or `config.toml` (the person's own). The marketplace put integrations
  in `.conch/`; a line is probably the same.
- Holdout scenarios in git at all? A separate private repo is the most
  honest holdout; the config directory is the easiest.
- Whether a run on a group task (several repositories) is one run or one
  per repository. `GroupTask` already has its own merge order.
