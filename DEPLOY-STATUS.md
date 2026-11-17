# Deploy previews: status

Branch `conch/add-new-integration-kind-deploy`, not pushed. Steps 1 and 2
are done; step 3 is written up in [DEPLOY-NEXT.md](DEPLOY-NEXT.md).

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

## What is left

Step 3 — DEPLOY-NEXT.md. Also: deploy files are not shipped with conch
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
