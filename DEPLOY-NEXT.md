# Deploy previews: what step 3 needs

Steps 1–2 are done: `internal/integration` reads and runs `kind = "deploy"`
files, and `conch integration previews | deploy-log | redeploy | promote`
uses them by hand. Step 3 — a branch's preview in the branch and pull
request views, and a failed build sent to the branch's agent — is a
protocol change across server, TUI, autopilot and Conch.app, so it was
left for its own change. This is what it needs, following how CI is wired.

## 1. Protocol (`internal/proto`)

- A capability, e.g. `CapBranchPreviews = "branch.previews.v1"`, in
  `proto.Capabilities`; clients check `MissingCapabilities` and show
  nothing (not an error) against an older server.
- `MethodBranchPreviews = "branch.previews"` with
  `BranchCILogParams`-shaped params (`project_id`, `branch`) and a reply
  of the project's deploy integrations, each with its newest deploys:
  `{source, service, deploys: [{id, state, url, environment, commit,
  error, inspect, started, finished}], why}` — `why` when it has no key
  or the project has no remote, as `ciSource.why` does. All fields
  `omitempty`.
- `MethodBranchDeployLog` (or a `Source`/`Kind` on `branch.ci_log`): the
  failed build's log as a `BranchCILog`, so `proto.CIFailurePrompt` and
  `NoteFromLog` can be reused unchanged. A separate method is cleaner:
  `branch.ci_log`'s error text is matched word for word by older clients.
- Add both methods to the `scoped` table in `internal/server/scope.go`
  (read) — `promote` must **not** be reachable from an agent's pane
  without the person: leave it out of the protocol for now, or add it
  only with the TUI's own confirmation and refuse it from a scoped caller.

## 2. Server (`internal/server`)

- `deployFor(ctx, p, branch, pr)` beside `ciFor` in `cilog.go`: every
  `integration.Cached()` file with `Kind == KindDeploy` and
  `f.Uses(p.root, rem)`, **plus** any whose `[detect] checks` matches a
  check on the pull request — Vercel and Netlify report to GitHub as
  checks (`Vercel*`, `netlify/*`). Connect with `s.connectFile`.
- `integration.CheckSource` skips every file that is not `KindCI`
  (`internal/integration/source.go:68`), so today a failing Vercel or
  Netlify check is "a service conch only recognises", unreadable. Either
  let it return deploy files too (and have the caller tell the kinds
  apart) or add a `DeploySource`. Its test must keep a ci file winning
  when both match.
- `Target.Head` is `pr.Head`, as for CI: Netlify's file picks the
  deploys at the head and returns none for a branch whose head was not
  built yet — that is "building soon", not an error.
- Cache previews per branch for a short while (they are asked at every
  redraw of a branch row); the project ticker already refreshes PRs.

## 3. Autopilot

- `autopilotFixCI` reads `ciLog`; a red Vercel/Netlify check reaches it
  as a failed check today and ends in "no failed GitHub Actions run".
  After `ciLog`'s CI integrations, try the deploy integrations: the
  newest `failed` deploy at the head, then `FailedLog` → `BranchCILog`
  (`Workflow` = deploy name or id, `URL` = `Inspect`, `Source`/`Service`
  the file's, `Log` = `tail(text, maxCILog)`; when the log is empty —
  Netlify with no error message — `ErrNothing` comes back: use the
  deploy's `Error` field instead).
- Never promote from the autopilot. Redeploy only if a policy says so;
  none does today.

## 4. TUI and Conch.app

- Branch row / pull request view: the newest deploy's state as a chip
  and its preview `url` as a link (`openInBrowser`, behind the variable
  tests replace), with `inspect` for a failed one. Width-test the row at
  20×5 and narrow widths.
- The review queue's `f` and `conch branch fix-ci` already send
  `branch.ci_log`'s answer; with the server change above they pick up a
  failed build with no TUI change.
- Settings → Integrations (`internal/tui/integrations.go`) lists rows per
  kind; add a "Deploy previews" header with `fileRows(m, files,
  integration.KindDeploy)`, and a row in `projectKinds`
  (`internal/tui/projectintegrations.go`).
- Conch.app: the git tab's branch header (`GitTab.swift`) and the PR
  panel, guarded by the capability.
- `Does(KindDeploy, …)` (`internal/integration/does.go`) says "by hand
  for now"; update its `Where`s when this lands.

## 5. Docs and end-to-end

- Help overlay, `web/src/app/docs/integrations` ("Deploy previews",
  "Not yet") and `interface/page.mdx` for the chip.
- A row in `docs/testing/end-to-end.md`: a real Vercel and Netlify
  project, a failing build sent to a stand-in agent, a promote done by
  hand. Fakes cannot prove the registry files' endpoints (see
  DEPLOY-STATUS.md).
