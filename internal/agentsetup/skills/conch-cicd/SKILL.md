---
name: conch-cicd
description: Work as the CI/CD engineer in conch — see what conch's autopilot already does with CI, read a failed run and tell a real failure from a flake or an infrastructure fault, send it to the branch's agent with conch branch fix-ci or rerun it with conch branch rerun-ci, connect the project's CI and deploy services through conch integration, follow a branch's deploy previews, and promote to production only when the person asks. Use when you are inside conch (CONCH_PANE_ID is set) and the person asks why CI is red, to fix or speed up a pipeline, or about a deploy or preview.
metadata:
  installed-by: conch
---

# CI/CD, in conch

conch already knows a lot about a branch's CI and deploys: it reads
failed runs, sends them to the branch's agent, reruns them, and shows the
branch's previews. This skill is how to use that, and how to keep the
pipeline itself honest. Shipping one branch — push, pull request, merge —
is the `conch-ship` skill.

Check first that you are in conch:

```sh
test -n "$CONCH_PANE_ID" && command -v conch
```

## 0. What conch already does here

```sh
grep -A12 '^\[verify\]' "$(conch config path)"   # the autopilot's policy
conch integration ls                             # CI and deploy services this project uses
conch status                                     # the agents, and the branch each one is on
```

With the autopilot on, conch sends a failed CI run back to the branch's
agent and, with `auto_merge = true`, merges green pull requests by
itself. Don't do those by hand too, or you race it: your job is then to
look at what keeps failing. Changing that policy is the person's, through
the `conch-autopilot` skill.

GitHub Actions needs nothing set up. A project on CircleCI or GitLab CI,
or deploying through Vercel or Netlify, needs an integration:
`conch integration search ci` finds one, and installing it
(`conch integration install NAME`) asks for its key — that is the
person's to type, so tell them the command rather than run it.

## 1. A red run

```sh
gh pr checks BRANCH                        # which checks failed
gh run list --branch BRANCH --limit 5      # the runs, newest first
gh run view RUN_ID --log-failed            # only the failed steps' logs
```

Read the first error, not the last. Then say which kind it is:

| Kind | Signs | In conch |
| --- | --- | --- |
| The code | fails the same way every time, and locally too | `conch branch fix-ci -branch BRANCH` sends the log to the branch's agent (or starts one with `-agent`) |
| A flake | passes on a rerun; timing, order, load | `conch branch rerun-ci` once to confirm — then it is a bug to report or fix |
| The infrastructure | runner lost, registry down, quota, disk | `conch branch rerun-ci`; if it repeats, tell the person what the service said |
| The pipeline | a YAML error, a missing secret, an action that moved | fix the workflow on a branch (below) |

A flaky test is a bug: a rerun proves it flaky and nothing more. Never
rerun until green to get a merge through. After three rounds of the same
failure, stop and tell the person.

To reproduce a failure, run the failed step's command in a worktree with
what CI sets (`CI=true`, the same versions and flags). Start a task for it
rather than working in somebody else's worktree:

```sh
conch task -name ci-repro -base BRANCH "Run exactly: make test with CI=true. Report the first failure and its cause in CI.md. Don't fix anything."
```

## 2. Changing the pipeline

Work on a branch of your own (`conch task`, or the one you were started
on), never on the default branch, and watch the run there. What a change
to a workflow keeps true: `permissions:` as narrow as the job needs;
third-party actions pin to a commit SHA, not a tag; caches keyed on the
lock file; `timeout-minutes` on every job; older runs of a pull request
cancelled by `concurrency:`; a slow suite split into shards; secrets only
through `secrets.`, never echoed. Say in the commit why it changed and
how long a run takes before and after.

## 3. Deploys and previews

```sh
conch integration previews -branch BRANCH    # the branch's deploys: state, link, why one failed
conch integration deploy-log DEPLOY          # the end of a deploy's build log
```

After an infrastructure fault, `redeploy DEPLOY` in place of `deploy-log`
builds it again. A deploy whose build failed goes to the branch's agent
the way a CI run does: `conch branch fix-ci` takes `-source vercel` for it.

Releasing to production is the person's decision. Run
`conch integration promote DEPLOY` only when the person asked for that
deploy to go out, and let it ask them first — no `-y` on their behalf.
Before it goes, know which deploy it replaces, so it can be put back.

## Rules

- Never disable a check, skip a test or mark a job `continue-on-error` to
  get green. Red is information.
- Never push to the default branch, change branch protection, touch
  secrets or cut a release unless the person asked.
- Never print a secret, even partly.
- Use `conch agent prompt` to message an agent, never `conch send`.
