# Software factory: what the field means by it (research, 2026-11-17)

The sources for [software-factory.md](software-factory.md). Each claim names
where it came from; anything taken only from a second-hand summary is marked
*unverified*.

## The word, then and now

- **Microsoft, 2004.** Greenfield and Short's *Software Factories* meant
  product-line engineering: DSLs, templates and frameworks so applications are
  assembled rather than written. https://softwarefactories.com/TheBook.html
- **US Air Force, 2017 on.** Kessel Run and the ~17 factories on Platform One
  are in-house DevSecOps units. No AI in that meaning.
  https://kesselrun.af.mil/mission/
- **2025–26.** A system where agents turn intent (tickets, specs, signals)
  into validated, merged changes, and people build and govern the factory
  instead of writing the code.

| Who | What they mean | Source |
| --- | --- | --- |
| Dan Shapiro, *The Five Levels* (Jan 2026) | A ladder modelled on driving automation. L3: a person reviews what the AI wrote. L4: a person writes specs and skills, leaves it running and checks the tests. L5 "dark factory": a black box from specs to software | https://www.danshapiro.com/blog/2026/01/the-five-levels-from-spicy-autocomplete-to-the-software-factory/ |
| StrongDM Software Factory (Feb 2026) | "Code must not be written by humans", "…must not be reviewed by humans". Specs plus **scenarios held out of the codebase**, a probabilistic *satisfaction* score, a Digital Twin Universe of their dependencies; ≥ $1,000 of tokens per engineer per day | https://factory.strongdm.ai/ |
| Simon Willison | The open question is how you know it works when agents write the code *and* the tests | https://simonwillison.net/2026/Feb/7/software-factory/ |
| Factory (Droids) | "This system is the software factory": signals → triage → build → test → review → secure → ship → monitor → signals. Autonomy in steps up to *Missions* lasting hours or days | https://factory.com/news/software-factory |
| 8090 Software Factory | Requirements → Blueprints → Work Orders (run by Claude Code, Cursor…) → Feedback, with a skills library and knowledge base over MCP; speaks of a floor plan, assembly lines, line operators | https://www.8090.ai/software-factory |
| Steve Yegge, Gas Town | Mayor (coordinator), Polecats (workers), **Refinery** (a merge queue that batches, gates and bisects), Witness (finds stuck agents); work items as git-backed "beads" grouped into convoys; `gt feed` TUI and a web dashboard | https://github.com/steveyegge/gastown |
| Ethan Mollick, *Twilight Factory* (Aug 2026) | Against the dark factory: a facilitator agent brings people in on four triggers — approval, expertise, variance, interest | https://www.oneusefulthing.org/p/agency-and-agents |
| OpenAI, *Harness engineering* (Feb 2026) | A product built with no hand-written code; the repository arranged for agents to read. Doesn't use the word | https://openai.com/index/harness-engineering/ |
| GitHub Agent HQ / Mission Control | One place to assign, steer and track agents (Copilot and others); Plan Mode, self-review, metrics, budgets. Doesn't use the word | https://github.blog/news-insights/product-news/welcome-home-agents/ |
| Spec Kit, Kiro, BMAD | Spec-driven inputs to a factory rather than factories: constitution → specify → plan → tasks → implement, each gated | https://github.github.com/spec-kit/index.html, https://martinfowler.com/articles/exploring-gen-ai/sdd-3-tools.html |

Checked and dropped: I found no primary source where Cognition/Devin, Amp,
Sourcegraph or Cursor call what they sell a software factory.

## The line everyone draws

Across the sources the stages are the same, give or take a name:

1. **Intake**: issues, labels, Slack or Linear mentions, monitoring signals.
2. **Triage**: is it ready, how big is it, what does "done" mean.
3. **Spec / plan**: acceptance criteria, approved before code (GitHub Plan
   Mode, Spec Kit gates, Factory Missions).
4. **Decompose**: work orders, beads, story files.
5. **Build**: an isolated worktree, container or VM.
6. **Review**: by another agent first (GitHub self-review, `@codex review`).
7. **Validate**: tests plus evidence; Factory's `/qa` posts a pass/fail
   table to the PR; StrongDM runs hidden scenarios.
8. **Merge**: a queue with gates and bisection (Gas Town's Refinery).
9. **Release, monitor**: which feeds intake again.
10. **Learn**: a repeated mistake becomes an instruction, a skill or a test.

## How products show it

- **Mission control:** a list of agents by status, filterable (GitHub, Warp,
  Antigravity, Factory). Most desktop tools stop here — Conductor, Superset,
  Orca, Claude Squad and Sculptor are lists of workspaces.
- **Kanban:** To do → In progress → Review → Done, a card per worktree
  (Vibe Kanban, Nimbalyst).
- **A floor:** only Gas Town (convoys, queues, problems), Factory and 8090
  draw anything like a factory floor.
- **A session timeline:** tools, tokens and time per run (GitHub).
- **Approval gates:** plan before code, a person before merge.
- **Numbers:** StrongDM's satisfaction; Factory's time to make a patch
  against time to repair and review it, since counting finished sessions
  hides the validation bottleneck; 8090's yield and throughput.

## What went wrong for people, in public

- **Verification is the bottleneck**, not generation (Willison; Factory:
  "treat an unexercised path as unverified").
- **Agents game the tests they can see.** StrongDM moved scenarios out of the
  repository as a holdout set after agents made narrow tests pass.
- **Dependencies get twins.** StrongDM cloned Okta, Jira and Slack from their
  API docs, to test at volume, free and against failures the real ones won't
  show on demand.
- **Review fatigue.** Willison calls four agents at once exhausting. Studies
  report more PRs merged more slowly, and many agent PRs never reviewed
  (*unverified*: secondary summaries, e.g.
  https://letsdatascience.com/news/harvard-study-finds-ai-coding-agents-raise-code-volume-not-o-ea78729b).
- **Parallelism is limited by attention and conflicts, not compute.**
  Practitioners settle on 3–5 agents a repository; conflicts cluster in
  registry and config files; semantic conflicts (each side sensible, together
  wrong) outweigh textual ones. Worktrees don't prevent either.
- **Cost** is real and per engineer (StrongDM's $1k/day).
- **Spec quality decides the result.** A vague ticket ("fix password reset")
  yields a symptom fix; the same spec gives different implementations.
- **The field churns.** Terragon closed (Jan 2026), Vibe Kanban's company
  closed (Apr 2026, the project lives on), Crystal was folded into Nimbalyst,
  Conductor is joining Meta, Jules is winding down. Vendors build their own
  mission control for their own agent. What lasts is any agent, on your own
  machines, with state in plain files.

## What conch already has (2026-11-17)

Almost every stage, in three places that don't share a record:

- **Server:** intake by label (`intake.go`), triage by an agent with a
  verdict (`triage.go`), the project check, CI failures sent back
  (`auto_fix`), conflicts rebased (`auto_resolve`), PRs opened, undrafted and
  merged when green behind gates (`autopilot*.go`, `mergeHold`), group tasks
  merged in order (`groupmerge.go`), a queue with WIP and `-after`
  (`schedule.go`), an activity feed (`autopilot.activity`).
- **TUI only:** the reviewer agent (`internal/tui/reviewer.go`); `reviewer`
  is not in `proto.AutopilotPolicy`, so the server ships after the check
  without waiting for a review.
- **A skill only:** `conch-factory`, the whole line (ticket → spec → build →
  review ×3 → test → PR → CI → merge → retro) as instructions to a foreman
  agent; the spec is a file in `$TMPDIR`.
- **Conch.app:** the Tickets window (a list), the Autopilot and CI panels
  beside the commit graph, the group task page, Settings' six-step line of
  toggles. No board, no run.

Missing: a **run** that is stored (today a task is a pane plus a branch whose
name carries the issue); a spec stage the server knows; review as a server
gate with a round limit; a validator and holdout scenarios; a merge queue;
cost per run and budgets; explicit approval gates and an inbox for them;
numbers per stage; turning findings into rules.
