---
name: conch-cicd
description: Work as the CI/CD engineer in conch — read a failed CI run and tell a real failure from a flake, an infrastructure fault or a broken workflow, send it to the branch's agent or fix it, change pipelines safely (least permissions, pinned actions, caches, timeouts, shards), and follow a branch's deploys and previews. Use when you are inside conch (CONCH_PANE_ID is set) and the person asks why CI is red, to fix or speed up a pipeline, or about a deploy or preview.
metadata:
  installed-by: conch
---

# CI/CD

You keep the path from a commit to a running build honest: red means
something is wrong, green means it isn't, and both arrive quickly. Shipping
a single branch — push, pull request, merge — is the `conch-ship` skill;
this one is the pipeline itself.

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

## A red run

### 1. Find the failure

```sh
gh pr checks BRANCH                        # which checks failed
gh run list --branch BRANCH --limit 5      # the runs, newest first
gh run view RUN_ID --log-failed            # only the failed steps' logs
```

Read the first error, not the last: later failures are usually the first
one falling over. Note the job, the step, the command it ran and the
runner (OS, versions).

### 2. Say what kind of failure it is

| Kind | Signs | What to do |
| --- | --- | --- |
| The code | a test or build fails the same way every time, and locally too | send it to the branch's agent (below), or fix it if the branch is yours |
| A flake | passes on a rerun, fails on timing, order or load | rerun once to confirm, then treat it as a bug |
| The infrastructure | runner lost, network or registry down, quota, disk full | rerun; if it repeats, tell the person what the service said |
| The pipeline | YAML error, a missing secret, an action that moved, a cache gone bad | fix the workflow (below) |
| The deploy | the preview or production build failed | read the deploy's log (below) |

### 3. Reproduce it locally

Run the exact command the failed step ran, in your worktree, with what
CI sets: `CI=true`, the same tool versions (from the workflow, `go.mod`,
`.nvmrc`, `.tool-versions`), the same flags (race detectors, shards). A
failure you can reproduce is half fixed.

### 4. Send it, or rerun it

```sh
conch branch fix-ci -branch BRANCH      # the failed run's log, to the branch's agent
conch branch rerun-ci                   # rerun only what failed (a flake or the infrastructure)
```

A flaky test is a bug: rerunning proves it is flaky, and that is all. Find
why it fails — shared state, order, a clock, a fixed port, a sleep — and
fix it or report it with what you found. Never rerun until green to get a
merge through. After three rounds of the same failure, stop and tell the
person.

## Changing a pipeline

Read the workflows (`.github/workflows/*.yml`, or the project's CI
config) and what they call (Makefile, scripts) before changing anything.
What a good pipeline does:

- **Fast feedback first** — lint and build before the long tests; cancel
  a pull request's older runs when it gets a new commit (`concurrency:`
  with `cancel-in-progress: true`).
- **Least privilege** — `permissions:` at the top, `contents: read` by
  default, more only on the job that needs it. Never run a fork's code
  with secrets (`pull_request_target` checking out the head).
- **Pinned** — pin third-party actions to a full commit SHA, with the
  version in a comment; pin tool versions too. A tag can be moved under
  you.
- **Cached** — dependencies cached on a key from the lock file, so a
  changed lock file is a fresh cache.
- **Bounded** — `timeout-minutes` on every job; a hung job otherwise
  holds a runner for hours.
- **Split when slow** — shard a long suite across jobs; a matrix only for
  platforms the project really supports.
- **Secrets stay secret** — only through `secrets.`, never echoed, never
  in a cache, an artifact or a log line.
- **Logs kept** — upload test output as an artifact when a job fails.

Check the change before it runs: `actionlint` if installed, then push to
your own branch and watch the run — never test a workflow on the default
branch. Say in the commit why the pipeline changed and how long it now
takes compared with before.

## Deploys and previews

When the project deploys through an integration (Vercel, Netlify):

```sh
conch integration previews -branch BRANCH    # its deploys: state, link, why one failed
conch integration deploy-log DEPLOY          # the end of a deploy's build log
```

After an infrastructure fault, `redeploy DEPLOY` in place of `deploy-log`
builds it again. A deploy whose build failed goes to the branch's agent the way a CI run
does: `conch branch fix-ci` takes `-source vercel` for it.

Releasing to production is the person's decision. Run
`conch integration promote DEPLOY` only when the person asked for that
deploy to go out, and let it ask them first — no `-y` on their behalf.
Before it goes, know how to go back: the deploy it replaces, and the
command that puts it back.

## Rules

- Never disable a check, skip a test, lower coverage or mark a job
  `continue-on-error` to get green. Red is information.
- Never push to the default branch, change branch protection, rotate or
  create secrets, or cut a release tag unless the person asked.
- Never print a secret, even partly, even to show it is set.
- Production deploys and releases only when the person asked, never on
  your own judgement.
- Use `conch agent prompt` to message an agent, never `conch send`.
