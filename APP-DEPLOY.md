# Deploy previews in Conch.app (and the TUI)

The deploy mock's three screens (Integrations → Connect → a branch's
previews and release) built on the `deploy` kind this branch already
read and ran. Steps 3 and 4 of DEPLOY-NEXT.md; DEPLOY-STATUS.md has the
server and protocol side in detail.

## What was built

**Server and protocol** (`branch.previews.v1`, `internal/server/deploy.go`)
- `branch.previews`: every deploy integration the project uses (its
  `[detect]` files or remote) or whose check its pull request has, each
  with the branch's deploys, newest first. Each deploy has its state,
  preview link, inspect page, error, commit, the commit's subject (read
  from the checkout with one `git log`), whether it built the PR's head,
  and whether it may be released now (or why not). Each integration
  carries its release words. Kept 15 s per branch. An answer that could
  not ask the service (no key) is not kept.
- `branch.deploy_log` (a build's log as a `BranchCILog`, so the agent's
  prompt is `CIFailurePrompt` unchanged), `branch.redeploy`,
  `branch.promote`.
- `branch.ci_log` falls back to the newest failed build at the head when
  no CI failed. The autopilot's auto_fix, `conch branch fix-ci` and the
  review queue therefore send a red Vercel or Netlify check to the agent
  with no client change.
- Promote is a new scope kind, `scopePerson`: refused to every agent,
  even in its own project. `conch integration promote` asks the local
  server who is running and refuses an agent's pane before calling the
  service. The autopilot calls neither promote nor redeploy.
- The release rules and words are in Go once
  (`internal/integration/release.go`). Only a ready deploy may be
  released, and Netlify won't publish a deploy preview or a branch
  deploy. Vercel: *Promote*, "built again for production". Netlify:
  *Publish*, "nothing is built again".
- Vercel and Netlify checks and files are recognised with nothing
  installed. Their checks belong to their deploy file, after any CI file
  that claims the same check.

**Screen 1: Integrations window.** `IntegrationKind.deploy` ("Deploy
previews"), so its section, filter and counts exist. A deploy card shows:
- what its file can do, as chips (`previews failed_log redeploy promote`)
- "Your key goes only to api.vercel.com"
- "Found vercel.json in this repo"
- **Install and connect**

The icons are the registry's own `deploy/vercel/icon.svg` and
`deploy/netlify/icon.svg`, downloaded and hash-checked by `conch
integration icons` like every other card's. Nothing is bundled. The
banner says a project "deploys to Vercel". The project sheet has a
Deploy previews row.

**Screen 2: Connect sheet.** The existing install flow:
1. agree to the one host
2. the token, kept in the Keychain or read from `$VERCEL_TOKEN` /
   `$NETLIFY_AUTH_TOKEN`
3. one read-only check against a project that uses it (`conch
   integration install -cwd DIR`, which reads a branch's deploys)

For a deploy file the sheet adds the key's help under the field and
"This file can" from its page. Step 3 says it reads "the Vercel project
(Netlify site) that builds from its repository".

**Screen 3: the branch's git tab.**
- **Header:** a chip with the newest deploy's state (*Vercel · Ready*,
  green / red / amber). Clicking it opens the preview, or a failed
  build's page on the service.
- **Pull request card:** "Preview on Vercel" with the newest ready link.
- **Overview, Deploy previews card:** a table per integration, as the
  mock has it: State · Commit (with subject and a *head* mark) · Preview
  (link, or the error in red) · Built · Actions.
  - A failed build has **Build log** (expands under the row with the log
    read through `branch.deploy_log`, and **Send to the branch's agent**,
    which runs `conch branch fix-ci -source S -deploy ID`, "as a failed
    CI run is"), **Redeploy**, and a link to its page.
  - A ready deploy has **Promote to production** / **Publish to
    production**. It opens a sheet with the service's own title, words
    and note, a box from "Vercel production" to the commit and its
    preview host, and Cancel / Promote.
  - A Netlify deploy preview says "A deploy preview can't be published:
    merge, then publish the production branch's deploy" where the button
    would be.
- **Older server:** nothing is asked of a server without
  `branch.previews.v1`, and nothing is shown.

**TUI**
- Settings → Integrations: a Deploy previews section. None ships, so it
  says Vercel and Netlify come from the registry until one is installed.
- The project sheet: a Deploy previews row.
- The branch view: a line per deploy integration under the checks
  (state, commit, preview host or error, or why it could not be asked).
  `v` opens the preview or the failed build's page. The help overlay and
  the docs' keys page list `v`.

## How to try it in the app

You need a Vercel or Netlify token and a repository linked to a project
or site. The registry's files are on Amitgb14/conch-integrations PR #15:
until it merges, copy that branch's `deploy/vercel/integration.toml` to
`~/.config/conch/integrations/vercel.toml` (or Netlify's likewise), then
use **Connect** to give it its key.

1. `make build`, then `macos/bundle.sh`, and open Conch.app. Reload the
   server so it has `branch.previews.v1` (`conch server reload`).
2. **Window → Integrations → Deploy previews → Vercel → Install and
   connect.** Agree to `api.vercel.com`, paste the token, then Install.
   It says "checked: read N deploys of BRANCH on Vercel" when a project
   here uses it.
3. Open a branch of that project in a git tab with a pushed commit and
   a pull request. The header chip, the PR card's preview line and the
   Overview's Deploy previews card show its deploys.
4. Break the build and push. The row turns Failed with the error.
   **Build log**, then **Send to the branch's agent**. **Redeploy**.
5. On a ready preview, **Promote to production**. Cancel first: nothing
   happens. Then Promote.

Without keys, the server's behaviour can be seen with `go test
./internal/server -run 'BranchPreviews|DeployLog|Redeploy'`, which
drives a fake Vercel through the registry's own file.

## What differs from the mock, and why

- **No "Production" row** (domain, live commit, "Released from this
  branch"). The deploy contract's `previews` reads a branch's deploys,
  and production is usually another branch's (main's). Showing it needs
  a new operation in the format and in both registry files. The release
  sheet's box therefore says "Vercel production → 3f2a91c ·
  preview-host" instead of "web-app.vercel.app e18b03f → 3f2a91c".
- **The connect sheet does not name the project** ("web-app, linked to
  github.com/acme/web-app") **or offer "change it in config.toml's
  [deploy.vercel]".** conch finds the project by the repository on every
  call and there is no such setting. The check's result line says what
  it read. Adding the name would need the check to report it.
- **"community" chip.** Cards keep the window's existing standing chip
  ("not checked live"), which is what the registry says of an unverified
  file. "Community" is the tier word, used by the Community filter.
- **Layout.** The previews are a card on the git tab's Overview, beside
  the pull request card, rather than a page of their own. The stepper,
  sidebar and fonts are the mock's framing, not the app's; the app's
  theme is kept.
- **"Released from this branch".** After a promote, the app says
  "Promoting … to production on Vercel" in its status line and reloads
  the previews. There is no production row to mark.
- **Netlify wording.** The mock says "publish main's deploy". The app
  says "the production branch's deploy", since the production branch
  need not be main.
- **Log.** The mock's log box is the whole build output. The app shows
  the end that the server reads (64 KB, as for CI). Netlify has no build
  log through its API, so its "log" is the error message.

## Anything left

- **Not run against the real Vercel or Netlify APIs.** Nothing here has
  been: no keys, and tests must not reach the network. Row 9.203 in
  docs/testing/end-to-end.md is the live run. DEPLOY-STATUS.md lists the
  endpoints to confirm (Vercel's `/v7/deployments` and whether list
  items carry `errorMessage`; Netlify's `builds?branch=…` query).
- **Not opened and clicked in a running Conch.app.** The views compile
  and ConchKit's logic is tested (`DeployTests`, `IntegrationsTests`,
  `ProjectIntegrationsTests`), but the git tab was not compared with the
  mock in a running app or a headless render.
- A production row (see above), Vercel team projects (`teamId`), a
  preview chip on the branch's row in the TUI's tree, and releasing from
  the TUI (it has `conch integration promote`, which asks).
- **Pre-existing test fixed while here.**
  `TestA4SandboxCreateRefusesBeforeSpending` failed in every full
  `cmd/conch` run, on master too. A Keychain lookup set by
  `TestA4MainDispatch`'s in-process `main()` and a key left in
  `keychainCache` reached it. Both are now reset after their tests.
