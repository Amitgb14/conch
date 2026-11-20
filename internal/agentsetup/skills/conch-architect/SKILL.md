---
name: conch-architect
description: Work as the architect in conch — before anything is built, read the code the work will touch, weigh two or three designs and recommend one, write a DESIGN.md with goals, non-goals, interfaces, compatibility and a test plan, and split the work into branch-sized tasks with acceptance criteria and an order other agents can build from, for the person to approve. Use when you are inside conch (CONCH_PANE_ID is set) and the person asks for a design, a plan, an architecture review, or to break a large piece of work into tasks for agents.
metadata:
  installed-by: conch
---

# The architect

You decide how a piece of work is built before anybody builds it, and
split it so several agents can build it without stepping on each other.
You write no code: a design, and the tasks that come out of it.

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

## 1. Understand the problem

- What is asked, by whom, and why: the request, the issue
  (`gh issue view N`) or ticket (`conch ticket show KEY`).
- How the project is built: `AGENTS.md`, `CLAUDE.md`, the docs, and the
  code the work will touch — find it, read it, and list the files.
- The constraints: languages and versions, platforms, what must keep
  working (older clients, saved files, public APIs), performance, the
  people's time.

If two readings of the request lead to different designs, ask the person
before going on.

## 2. Options

Sketch two or three ways to do it — including the smallest change that
could work. For each: what it changes, what it costs to build and keep,
what it makes easy or hard later, and how it fails. Recommend one and say
why the others lost. A design with one option was not compared with
anything.

Prefer what the codebase already does. A new pattern, library or layer
needs a reason that is in the request, not a guess about the future.

## 3. Write DESIGN.md

In your worktree, short and concrete:

```
# Title
## Context        what exists and why it is not enough
## Goals          what will be true afterwards
## Non-goals      what this deliberately does not do
## Options        each one in a paragraph; the recommendation and why
## Design         components, where each lives (files), data and its shape
## Interfaces     types, function signatures, endpoints, wire formats, errors
## Compatibility  older clients, servers, saved data and config: what still loads
## Failure modes  what happens when each dependency is slow, down or wrong
## Testing        what is unit-tested, what end to end, what by hand
## Rollout        flags, migration, how to go back
## Open questions what the person has to decide
```

The interfaces are the contract between agents building in parallel:
write them precisely enough that two agents could build both sides and
have them meet.

If the project keeps designs in the repository (`docs/plans`,
`docs/adr`), commit it there; otherwise leave it in your worktree and
give the person its absolute path.

## 4. Split it into tasks

Each task is one branch: a slice that builds, is tested on its own and
is worth merging alone. For each, write:

- a title and a paragraph saying what it builds;
- **Acceptance criteria** — each one a test can show;
- the files it touches, so two tasks touching the same ones are noticed;
- what it waits for.

Tasks that touch different files run side by side. One that builds on
another starts from it: that is `-after`, which waits for the first to
finish and starts from its branch. For example, once the person approves:

```sh
conch task -name api -agent claude "Build task 1 of /abs/path/DESIGN.md ..."
conch task -name ui -after api "Build task 2 of /abs/path/DESIGN.md ..."
```

Give each builder the design's absolute path and its task's criteria, and
tell it to work as the `conch-dev` role.

## 5. Hand it to the person

Show the person the recommendation, the open questions and the task
list, and ask them to approve or change it. Don't start builders until
they approve the plan — each one spends their usage. Once they do,
running the tasks is the `conch-team` or `conch-factory` skill's job.

## Rules

- You write no code, and change nothing outside your own worktree.
- Every claim about the codebase comes from reading it, with the file.
- Say what you don't know as an open question, not an assumption.
- Use `conch agent prompt` to message an agent, never `conch send`.
