# Deploy previews: status

Steps 1 and 2 were done on `conch/add-new-integration-kind-deploy`; steps
3 and 4 of [DEPLOY-NEXT.md](DEPLOY-NEXT.md) (protocol, server, autopilot,
TUI and Conch.app) were done on `conch/build-deploy-previews-flow-conch`
— see "Step 3 and 4: what now works" below and
[APP-DEPLOY.md](APP-DEPLOY.md) for the app.

## What works

- **Reading** (`internal/integration/format.go`): `kind = "deploy"`,
  format 3. Operations `previews` (required), `failed_log`, `redeploy`,
  `promote`; `{deploy}` is the id placeholder (not `{run}`). `[previews]`
  takes a run's fields plus `environment`, `error`, `inspect`. `[states]`
  must map to `ready | building | failed | canceled`, and something must
  map to `ready` and to `failed`. `[detect]` is required, as for ci. A
  deploy file without `previews`, with a ci state, with `{run}`, or with
  a ci operation is refused; a ci file with `[promote]` or a deploy field
  is refused too.
- **Running** (`internal/integration/deploy.go`, `run.go`):
  `Conn.Previews`, `FailedLog` (shared with ci), `Redeploy`, `Promote`.
  Single calls and steps (`as`, `pick`, `each`, `follow`, `text`) work as
  for ci; `runs` and `previews` now share one item reader.
- **CLI**: `conch integration previews [-json] [-cwd] [-branch] [-head]
  [NAME]`, `deploy-log [-lines N] [NAME] DEPLOY`, `redeploy [NAME]
  DEPLOY`, `promote [-y] [NAME] DEPLOY`. With no name, the deploy
  integration the project uses. `promote` asks `[y/N]`; anything but yes
  releases nothing and exits non-zero. `conch integration key/install`
  checks a deploy key by reading previews. `adapt -kind deploy` works;
  `Does` and the default kinds list "Deploy previews".
- **Docs**: `format.md` (the guide; its example is parsed by a test),
  `web/src/app/docs/integrations` and `cli`, `cmd/conch/main.go` usage.
- `conch integration check` on the registry's real files (built binary,
  isolated `CONCH_HOME`): `vercel is fine`, `netlify is fine`; a copy
  with `BUILDING = "pending"` is refused with exit 1.

## Changes that also touch ci/other kinds

- `checkCall` now looks for `://` only in the path, not the query —
  Vercel's `repoUrl=https://{host}/{path}` was refused as "a call to
  another host". The path is still checked.
- `FailedLog` returns `ErrNothing` when the log reads as empty text (for
  every kind), instead of an empty log for an agent. The server's
  `ciLog` already handles `ErrNothing`.
- `FailedLog` refuses an empty id before any call.
- A pick that finds nothing during `redeploy`/`promote` is an error, not
  success (unlike ci's `rerun`, which swallows it): nothing was released.

## What was tested

- `go test -race ./internal/integration/...`, `go vet ./...`, gofmt —
  clean. `go test ./cmd/conch/` (integration tests) — clean; server and
  TUI packages: see the run at the end of this session.
- `internal/integration/deploy_test.go`: both registry files parse with
  no problems (they are kept as fixtures in `testdata/deploy/`, copied
  verbatim); the refusals above; the contract text.
- `internal/integration/deployrun_test.go`: httptest fakes shaped like
  Vercel's and Netlify's documented replies, run through the registry's
  files — project by `repoUrl`, site by `build_settings/repo_path`,
  deploys at the head and without one, none for an unknown repository,
  branch or head, an unknown state read as building, ms and RFC 3339
  times, build events as a log, Netlify's error message as the log and
  none when it is null, redeploy/promote bodies and query (a null
  `review_id` dropped), a 404 on promote sending no POST, empty ids, the
  wrong key named by its variable and never echoed, a file without the
  optional operations.
- `cmd/conch/integration_deploy_test.go`: every command against a fake
  Vercel, including promote's no / empty / EOF / yes / `-y` answers.

**Not tested**: anything against the real Vercel or Netlify APIs (no
keys, and tests must not reach the network). The endpoint shapes below
come from the files and my reading of the docs, not from a live call.

## Step 3 and 4: what now works

- **Recognised with nothing installed.** `CheckSource` reads a deploy
  file's checks after CI files' (a check both claim stays CI's); Vercel
  (`Vercel*`, vercel.com) and Netlify (`netlify/*`, netlify.com) are known
  services, and `vercel.json`, `.vercel/project.json`, `netlify.toml` are
  markers, so `integration.detect` names them with kind `deploy`.
- **Protocol** (`branch.previews.v1`): `branch.previews` (each deploy
  integration the project uses or whose check its PR has, with the
  deploys at the PR's head — or, with no PR, the branch's history —
  newest first: state, link, inspect, error, commit and its subject from
  the checkout's git, `promotable` / `not_promotable`; per integration
  `head`, `build_log`, `missing` and the release words with `rebuilds`),
  `branch.deploy_log`
  (a build's log as a `BranchCILog`), `branch.redeploy`, `branch.promote`.
  Kept 15 s per branch; an answer that could not ask (no key) is not kept.
- **Nothing found** (`ErrUnlinked`, `integration.Unlinked`): a named
  lookup step before the last that picks nothing (Vercel's project,
  Netlify's site) is told apart from "no deploys yet" and said in the
  service's terms; `conch integration key/install` says the key works
  and is kept, `previews` fails with it.
- **Build log** (`HasBuildLog`): Netlify's failed_log reads previews' own
  error field, so it is not a build log; the app shows the error.
- **Release rules** (`integration.ReleaseOf`, `CanRelease`): only a ready
  deploy; Netlify will not publish a deploy preview or branch deploy;
  Vercel rebuilds for production, Netlify publishes as built. The server
  checks them before calling promote.
- **Scope**: `branch.promote` is `scopePerson` — refused to every agent,
  even in its own project; `branch.redeploy` is project-scoped like a CI
  re-run. `conch integration promote` asks the local server who is
  running and refuses an agent's pane before calling anything.
- **Agent**: `branch.ci_log` falls back to the newest failed build at the
  head when no CI failed, so the autopilot's auto_fix, `fix-ci` and the
  review queue send it with `CIFailurePrompt` unchanged; a build with no
  log is sent its error. `conch branch fix-ci -source S [-deploy ID]`
  sends one build. Nothing in the autopilot promotes or redeploys.
- **TUI**: Settings → Integrations has a Deploy previews section; the
  project sheet a row; the branch view a line per deploy integration
  (newest deploy, commit, link or error), `v` to open it.
- **Conch.app**: see APP-DEPLOY.md.
- `Does(KindDeploy)` says where each shows now.

Tested with fakes only: `internal/integration/deploysource_test.go`,
`internal/server/deploy_test.go` (a fake Vercel through the registry's
file, a fake gh, a real git checkout), `TestScopeDeploys`,
`cmd/conch` `TestBranchFixCIDeploy` and
`TestIntegrationPromoteRefusedToAnAgent`, `internal/tui/previews_test.go`,
ConchKit `DeployTests` and `IntegrationsTests`. End-to-end row 9.205 is
not run.

## What is left

Live checks against Vercel and Netlify (row 9.205 in
docs/testing/end-to-end.md); a production row (what is live now: the
contract reads a branch's deploys only); Vercel teams (item 4 below).
Also: deploy files are not shipped with conch
(they come from the registry); installing one needs the registry's index
to stop marking the kind as needing a newer conch once this is released.

## Registry files: what to fix or verify

Nothing in either file was inexpressible once `checkCall` accepted a URL
in a query. Things to check live, or fix:

1. **README** says "`conch integration check` does not know these files"
   — true of released conch only; update when this lands, with the
   version.
2. **Vercel `previews`** (`GET /v7/deployments`): verify the version is
   current (I believe the documented list endpoint is `/v6/deployments`)
   and that list items carry `errorMessage` — it may only be on
   `GET /v13/deployments/{id}`, in which case `error` is always empty in
   `previews` (the CLI shows nothing for it; the log still works).
3. **Vercel `target`** is `null` for a preview, so `environment` reads
   empty, not "preview". A template can't default it; either accept
   empty or the file could use `{item.target}` with a fallback conch
   doesn't have.
4. **Vercel teams**: as the file's note says, a team-owned project needs
   `teamId` on every call, which a file cannot ask for. A conch-side
   setting (`[integrations.vars] vercel.team = …` filling `{team}`) would
   let the file say `&teamId={team}` (an empty one is dropped).
5. **Netlify `redeploy`** (`POST /sites/{site}/builds?branch=&commit_sha=&review_id=`):
   I could not confirm `createSiteBuild` takes these as query
   parameters; Netlify also documents `POST /deploys/{deploy_id}/retry`
   (retrySiteDeploy), which needs no first step. Verify live.
6. **Netlify `previews`** first step reads only the first 100 sites
   (`per_page=100`); an account with more may not find its site. Steps
   have no paging, so this is a reader limit as much as a file one.
7. **Netlify `failed_log`** is the error message only; on a deploy that
   failed with none, conch now says "left no log" rather than sending
   an empty one.
