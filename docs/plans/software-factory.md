# The AI software factory in conch

Status: proposal, 2026-11-17. Research: [software-factory-research.md](software-factory-research.md).
Mocks: https://claude.ai/artifact/TZ1b6Bas1wkXurw6CSbram (floor, run, inbox, line).
Report: https://claude.ai/artifact/BV8dzLXpZtMNtSN1rhkn9V.

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

## A real example: #230 down the line

Issue [#230](https://github.com/Amitgb14/conch/issues/230), *An agent in a
checkout mid-rebase shows under no branch*, went from issue to merge
through conch as it is today. The table shows what really happened, then
what the line would have done and recorded. Times are PST.

| Stage | What happened (real) | What the line adds |
| --- | --- | --- |
| Intake | Opened Sat 14 Nov 12:24, after seeing pane p569 in `~/ceph` listed with no branch | Labelled `conch`: intake starts run R-230 and stores it in `factory.json` |
| Triage | None | A triage agent reads it in a clean checkout and says *ready, M*. The issue already names the cause (`worktreeFor` reads `git worktree list`; `gitx.RebasingBranch` exists but only `branchgit.go` uses it) |
| Spec | The issue body **is** a spec: *What done means* (six points, plus five test cases) and *Out of scope* | At L4 the run waits for one click: *Approve and build*. The spec is copied into the run, so the reviewer and validator read the same text |
| Build | An agent on `conch/rebasing-branch`, one commit (`11907ae1`), Sun 15 Nov 13:15: 16 files, +466 −26, six test files | Same. The run records the pane, the commit and the agent's cost. Today the cost is lost when the pane closes |
| Check | Not recorded: whatever check ran before the push left nothing on the PR | `[verify] commands` on the exact commit, with up to 3 fixes, kept in the run |
| Review | **None.** PR #245 merged with 0 reviews | A codex round is a gate. It is given the spec and the diff. Something real for it to judge: the commit also changed `worktree.stale` and `branch.prune`, which the spec never asked for. The run marks that *beyond the spec* so a person or reviewer looks at it on purpose |
| Validate | Only the issue's own test list, which the builder read and then wrote the tests for | Holdout scenarios kept out of the builder's checkout, e.g.: a rebase whose `head-name` names a branch deleted since; a branch with a `/` in its name; an older Conch.app against this server (`worktree.operation.v1` missing) shows no error; the tree row `main · rebasing · 3 conflicts` at 40 columns; `git rebase --abort` returns the pane to `main` on the next refresh |
| Merge | PR opened 13:15, CI green (Linux, macOS, swift, settings-ui, build), merged 13:49, 34 min later. Issue closed a second after the merge | The merge queue: rebase on master, check again, merge. Overlaps with other runs are shown while it builds |
| Learn | Not done | The reviewer's *beyond the spec* finding, if it recurs, becomes a line in AGENTS.md: "a fix that changes another command's behaviour says so in the PR" |

Numbers the run page would show: **25 h 25 m** issue to merge, of which
**34 m** PR to merge; 0 review rounds (the gap this design closes); and a
cost per stage that conch does not record today.

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

## Changes in conch

What each phase changes, file by file. New files are marked **new**;
everything else is an existing file that grows. Sizes are rough: S is a
day or less, M a few days, L a week or more.

### Phase 0 — runs from what exists (M–L, no behaviour change)

The record, and the screens that read it. Nothing waits on it yet, so a
bug here can show a wrong stage but cannot stop or merge anything.

| Where | Change |
| --- | --- |
| `internal/proto/factory.go` **new** | `Run` (id, project, ticket ref, title, line, stage, state, `Attempts`, `Gates`, `Spec`, `Validation`, `Cost`, `Timeline`), `Line`, `Stage`, `Gate`; params and results for `factory.runs`, `factory.run`, `factory.line.get`; event `factory.changed`. Every field `omitempty` |
| `internal/proto/proto.go` | `CapFactory = "factory.v1"` in `Capabilities`; the method and event names beside the others |
| `internal/config/factory.go` **new** | Reads `.conch/factory.toml` from a project's checkout. With no file, it builds the default line from `[verify]`: `intake_wip` becomes `wip`, `reviewer` the review stage, `auto_fix` the check's fixes, and the autonomy is the one today's settings amount to |
| `internal/server/factory.go` **new** | The store: `factory.json` in the config directory, loaded and saved the way `autopilot.json` is, so a reload keeps it with no reload-state change. `startRun`, `advance(run, stage, note)`, `attachPane`, and the three read methods |
| `internal/server/intake.go` | Where an issue is queued (`scheduleAdd`), start a run too |
| `internal/server/triage.go` | `triagePost` records the verdict on the issue's run |
| `internal/server/server.go` | `createTask` with an `Issue` or `Ticket` starts a run, or attaches to the one intake started; the method switch serves the new methods |
| `internal/server/autopilot_ship.go` | `autopilotFinished`, `autopilotStartCheck` and `ship` advance the run: build done → check → PR |
| `internal/server/autopilot.go` | `autopilotMerge` and `mergeHold` advance it to merged, or say why it is held. `ci` notes go on its timeline |
| `internal/server/activity.go` | Each `AutopilotNote` with a branch that belongs to a run is also put on that run's timeline |
| `internal/server/agents.go`, `usage.go` | When a pane of a run closes, copy its `Tokens` (cost, tokens) into its attempt. Today that is lost with the pane, as #230 showed |
| `internal/server/scope.go` | `factory.runs` and `factory.run` are reads: an agent sees its own project's runs |
| `cmd/conch/factory.go` **new**, `main.go` | `conch factory ls [-stage S] [-json]`, `conch factory show RUN`, `conch factory line`; usage text |
| `cmd/conch/mcptools.go` | A `factory` read tool, so an agent can see the line it works on |
| `macos/Sources/ConchKit/Factory.swift` **new**, `Protocol.swift` | The types, and the board's grouping by stage (what is tested with `swift test`) |
| `macos/Sources/ConchApp/FactoryView.swift`, `RunPage.swift` **new** | The floor and the run page; a **Factory** row under each project in the tree (`Views.swift`), and a toolbar item |
| `web/src/app/docs/factory/page.mdx` **new**, `AGENTS.md` | What a run is and where it shows. A row in the repository map |

Tests: a run is made for an issue taken by intake, an issue run by `task
-issue`, and a ticket run from the Tickets window. It moves through check →
PR → merged with fake `gh`. An older `factory.json`, a truncated one or
none at all loads. A run survives a reload. A client without `factory.v1`
gets the Tickets window as today. In the TUI, `-race` with notes arriving
mid-advance.

### Phase 1 — review as a server gate (M)

| Where | Change |
| --- | --- |
| `internal/proto/autopilot.go` | `AutopilotPolicy` gains `Reviewer` and `ReviewRounds` (`omitempty`). The config already has `[verify] reviewer`; the server has never been told it |
| `internal/server/reviewgate.go` **new** | After the check passes on a branch, start the reviewer (`createTask` with `base` = the branch, as the `conch-review` skill does), wait for it, read its report, put its findings through `review.*`, send them to the builder, and go again, up to `ReviewRounds`. Each round goes on the run |
| `internal/server/autopilot_ship.go` | `ship` waits for a clean review when a reviewer is set |
| `internal/server/autopilot.go` | `mergeHold` holds while the review is not clean or has run out of rounds. Out of rounds becomes a gate |
| `internal/server/reviewers.go` | `closeDoneReviewers` also closes the gate's reviewer panes |
| `internal/tui/reviewer.go` | Runs only against a server without `autopilot.review.v1`, so two reviewers never start |
| `proto.Capabilities` | `autopilot.review.v1` |

Tests: a clean review ships; findings go back to the builder and round 2 is
clean; three rounds with findings make a gate and no merge; the reviewer
fails to start; the builder pane is gone mid-loop; a TUI and a server
against each other start one reviewer, not two.

### Phase 2 — spec, gates, inbox, autonomy (L)

| Where | Change |
| --- | --- |
| `internal/server/spec.go` **new** | The spec stage. If the issue already has *Done means* (as #230 did), the issue body is the spec. Otherwise a spec agent writes `.conch-spec.md` in a detached checkout of the base, reusing triage's (`triageDir`). The text is kept in the run |
| `internal/server/factory.go` | Gates: open, decide (`approve`, `changes` with a note, `another_round`, `raise_budget`, `take_over`, `stop`), and the `factory.gate` and `factory.line.set` methods. Autonomy decides which stages get a person gate |
| `internal/server/intake.go`, `schedule.go` | The build is queued only after the spec gate is passed. Until then the run waits in Spec, not in the queue |
| `internal/server/server.go` | The builder's prompt carries the approved spec. The reviewer's (phase 1) is given it too |
| `internal/server/scope.go` | `factory.gate` and `factory.line.set` are refused from agent panes, as `autopilot.set` is: an agent must not approve its own spec |
| `internal/config/config.go` | A new `[notify] gate_after` (default 30 min): a gate open that long runs the notify command |
| `internal/phone/api.go`, `ui/` | An inbox route and screen; `phoneNeeds` adds `factory.v1`. Approve a spec from the phone |
| `internal/tui/factory.go` **new**, `overlay.go`, `internal/keymap` | The floor as a list by stage and the inbox, an action for each, help text |
| `macos/Sources/ConchApp/InboxView.swift`, `LineSheet.swift` **new** | The inbox and the line editor. The Settings line of toggles (`SettingsUI.swift` `autopilotSteps`) opens the line sheet for a project that has a line |

Tests: an issue with *Done means* skips the spec agent; one without gets a
spec; approve, ask for changes (the spec agent gets the note), stop; L3,
L4 and L5 put the gates where the line says; an agent pane's
`factory.gate` is refused; the phone without the capability says which
machine lacks it.

### Phase 3 — validation with holdout scenarios (L)

| Where | Change |
| --- | --- |
| `internal/server/validate.go` **new** | A detached checkout of the branch under the config directory, like triage's, with the scenarios copied in only there. A validator agent runs them and writes `.conch-validation.json` (`[{scenario, pass, evidence}]`). Failures go to the builder as *what was done, what happened, what was expected*, never the scenario text. Attempts are limited |
| holdout folder | `$CONCH_HOME/holdout/<project>/` by default, or the line's `holdout`. Never inside a checkout conch gives a builder |
| `internal/gitx` | `TestsWeakened(base, head)`: test files deleted, or skips added (`t.Skip`, `it.skip`, `@pytest.mark.skip`). Fails the stage |
| `internal/server/autopilot_ship.go` | The PR body (`ghx.Create`) carries the validation table |
| `macos/…/RunPage.swift` | The table; scenarios edited from the run or the line sheet |

Honest limit: a builder agent can still read any file on the machine. The
holdout keeps the scenarios out of its checkout, its prompt and its
messages, which is what stops it tuning to them by accident. It is not a
sandbox. A sandboxed task (`-sandbox`) is the strong form.

Tests: pass, fail then fixed on attempt 2, out of attempts gives a gate, a
missing holdout folder, a validator that writes no file or bad JSON, a
diff that deletes a test, a skip added in Go and in JS.

### Phase 4 — merge queue and budgets (M)

| Where | Change |
| --- | --- |
| `internal/server/autopilot.go` | `autopilotMerges` takes green PRs per base in order (a `queue` kept in `autopilot.json`), one in flight. Before merging, rebase on the base with `autopilot_resolve.go`'s machinery. `mergeGates` already requires the check on the exact head, so the rebased head is checked again before it merges. A red one leaves the queue and goes back to its builder |
| `internal/server/factory.go` | Each tick, a run's cost is the sum of its panes' `Tokens.CostUSD`, live ones included. Over `budget_usd`, its next stage does not start and a gate opens. A working agent is not killed |
| `macos/…/FactoryView.swift`, `AutopilotPanel.swift` | Cost bars on cards; the queue in order |

Tests: two green PRs merge one after the other, the second rebased and
checked again; the second fails after the rebase; the base moves
mid-queue; a reload mid-queue; a run crosses its budget mid-build; an
agent that reports no cost (counted as unknown, not zero).

### Phase 5 — numbers and learn (S–M)

| Where | Change |
| --- | --- |
| `internal/server/factory.go` | `factory.stats`: runs started and merged, time in each stage, review rounds, waiting on people, cost; escapes (a revert of a run's merge, or a run marked a follow-up of another) |
| `internal/agentsetup` | *Add to instructions*: a reviewer finding seen in several runs becomes a proposed line for the project's instructions, written with the same code that syncs them between agents, after the person sees the diff |

### What does not change

The autopilot keeps doing the work: `auto_fix`, `auto_resolve`, `ship`,
`mergeGates`. A project with no line behaves as today. The skills keep
working against any server, and `conch-factory` gains one line: on a
server with `factory.v1`, start the run with `conch task -issue` and watch
it with `conch factory show` rather than driving each stage itself.

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
