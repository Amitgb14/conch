# Deploy previews in Conch.app (and the TUI)

This builds the deploy mock's three screens (Integrations, Connect, and
a branch's previews and release) on the `deploy` kind this branch could
already read and run. It covers steps 3 and 4 of DEPLOY-NEXT.md, built
to the corrected mock (the ten corrections are listed under "Differs
from the mock"). DEPLOY-STATUS.md has the server and protocol side in
detail.

## What was built

**Server and protocol** (`branch.previews.v1`, `internal/server/deploy.go`)
- **`branch.previews`** lists every deploy integration the project uses
  (its `[detect]` files or remote) or whose check its pull request has.
  - With a pull request, it reads only the deploys at the PR's head
    commit (the files' `sha={head}`), and says that head. Without one,
    it reads the branch's history.
  - Each deploy has its state, preview link, inspect page, error and
    commit. It also has the commit's subject, read from the checkout's
    git by sha with one `git log`, and whether it may be released now
    (or why not).
  - Each integration carries its release words, whether releasing
    rebuilds, and whether its file reads a real build log.
  - When nothing on the service builds from the repository, it says so
    in `missing`.
  - Answers are kept for 15 s per branch. An answer that could not ask
    the service (no key) is not kept.
- **`ErrUnlinked`.** In the runner, a named lookup step before the last
  that picks nothing (Vercel's project by repository, Netlify's site) is
  now `ErrUnlinked` for a deploy file. This tells "nothing builds from
  here" apart from "no deploy yet" (which is still an empty list, not an
  error).
- **Other methods:** `branch.deploy_log` (a build's log as a
  `BranchCILog`, so the agent's prompt is `CIFailurePrompt` unchanged;
  an empty log falls back to the deploy's error), `branch.redeploy` and
  `branch.promote`.
- **Failed builds to the agent.** `branch.ci_log` falls back to the
  newest failed build at the head when no CI failed. The autopilot's
  auto_fix, `conch branch fix-ci` and the review queue therefore send a
  red Vercel or Netlify check to the agent. `fix-ci -source S -deploy ID`
  sends one build.
- **Promote is never an agent's.** It has a new scope kind,
  `scopePerson`, refused to every agent even in its own project.
  `conch integration promote` asks the local server who is running and
  refuses an agent's pane before calling anything. The autopilot never
  promotes or redeploys.
- **Release rules and words** are written once, in Go
  (`internal/integration/release.go`): `ReleaseOf`, `CanRelease`,
  `Unlinked` and `HasBuildLog`. A `failed_log` that is a single call
  reading previews' own error field is not a build log, which is
  Netlify's case.
- **Recognised with nothing installed.** Vercel and Netlify checks and
  files are named, and their checks belong to the deploy file after any
  CI file that claims them.

**Screen 1: Integrations window.** `IntegrationKind.deploy` adds a
"Deploy previews" section. A deploy card shows:
- what its file can do, as chips
- "Your key goes only to api.vercel.com"
- "Found vercel.json in this repo"
- **Install and Connect**

The icons are the registry's own `deploy/vercel/icon.svg` and
`deploy/netlify/icon.svg`. `conch integration icons` downloads them and
checks their hashes, as for every other card; nothing is bundled. The
banner says a project "deploys to Vercel", and the project sheet has a
Deploy previews row.

A deploy integration's page has a **Where it shows** box. It lists each
project here that deploys there, with why ("found vercel.json") and
**Open Its Previews**, which opens the project's base branch in a git
tab. With none, it says so and what conch looks for. The page's setup
text is the corrected Vercel key help. (Added after the first install
showed the page with nowhere to go: no project of the person's used
Vercel.)

**Screen 2: Connect sheet.** The existing install flow:
1. Agree to the one host.
2. Give the token. It is kept in the Keychain, or read from
   `$VERCEL_TOKEN` / `$NETLIFY_AUTH_TOKEN`.
3. One read-only check against a project that uses it.

For a deploy file the sheet adds:
- **Key help.** Vercel's asks for a token for your personal account and
  says team projects aren't supported yet.
- **"This file can"**, from the integration's page.
- **Step 3's wording:** it reads the Vercel project (Netlify site) that
  builds from the repository, "found by the repository it builds from:
  there is nothing to choose".
- **Nothing found:** the check answers "the key works, but No Vercel
  project in your account is linked to github.com/OWNER/REPO. Projects
  owned by a Vercel team aren't supported yet. The key is kept…", shown
  amber. For Netlify: "…builds from OWNER/REPO. Only your first 100
  sites are read."

**Screen 3: the branch's git tab.**
- **Header:** a chip with the newest deploy's state, which opens the
  preview, or a failed build's page.
- **Pull request card:** the newest ready preview's link.
- **Overview's Deploy previews card**, one table per integration:
  - **Scope line:** "Deploys at the pull request's head, 3f2a91c, newest
    first. Earlier commits' deploys aren't listed." Without a PR: "No
    pull request: the branch's deploys, newest first."
  - **Columns:** State · Commit (its environment beside it, and its git
    subject) · Preview · Built · Actions.
  - **All four states:** Ready (link), Failed (error in red), Building
    ("Building…") and Canceled ("Canceled before it was ready").
  - **A failed deploy:** Vercel offers **Build Log**, which expands with
    the log and **Send to the Branch's Agent** (`fix-ci -source -deploy`).
    Netlify offers **Error Message**, the error with "Netlify's API gives
    no build log: this is the deploy's error message. The full log is on
    Netlify's deploy page.", and Send ("the error message, with the
    commit"). Both offer **Redeploy** and a link to the service's page.
  - **A ready deploy:** **Promote to Production** / **Publish to
    production**, behind a sheet with the service's title, words and
    note.
  - **After the sheet:** Vercel's promote adds a **Building** row with a
    *production* label for the new build, kept until Vercel lists that
    build itself. Netlify's publish says "Netlify now serves e18b03f: it
    published the deploy as it was built."
  - **Netlify deploy previews:** the row says "A deploy preview can't be
    published". Under the table: "A deploy preview can't be published.
    Publish a production-branch deploy — when auto publishing is locked
    a ready one waits for this — or an earlier one to roll back."
  - **Nothing found:** the service's words in place of the table.
  - **Footer:** "Commit messages come from this repository's git."
- **Older server:** nothing is asked of a server without
  `branch.previews.v1`, and nothing is shown.

**TUI**
- **Settings → Integrations:** a Deploy previews section.
- **The project sheet:** a Deploy previews row.
- **The branch view:** a line per deploy integration under the checks,
  showing the newest deploy's state, commit, and preview host or error.
  Instead, it shows why the service could not be asked, or the
  nothing-found words. `v` opens the preview or the failed build's page,
  and is in the help overlay and the keys page.

## How to try it in the app

You need a Vercel token for your personal account (not a team's
project) or a Netlify token, and a repository that project or site
builds from. The registry's files are on Amitgb14/conch-integrations
PR #15. Until it merges, copy that branch's `deploy/vercel/integration.toml`
to `~/.config/conch/integrations/vercel.toml` (or Netlify's likewise).

1. `make build`, then `macos/bundle.sh`, and open Conch.app. Reload the
   server so it has `branch.previews.v1` (`conch server reload`).
2. **Window → Integrations → Deploy previews → Vercel → Install and
   connect.** Agree to `api.vercel.com`, paste the token, then Install.
   It says "checked: read N deploys of BRANCH on Vercel". In a
   repository nothing builds from, it says so in amber and keeps the
   key.
3. Open a branch with a pull request in a git tab. The chip, the PR
   card's preview line and the Overview's card show the deploys at its
   head. A branch with no pull request shows its history.
4. Break the build and push. The row turns Failed. On Vercel, open
   **Build Log**; on Netlify, **Error Message**. Then **Send to the
   branch's agent**, and **Redeploy**.
5. On a ready Vercel preview, **Promote to Production**. Cancel first;
   nothing happens. Then Promote: a Building production row appears. On
   Netlify, a production-branch deploy's **Publish**.

Without keys, `go test ./internal/server -run 'BranchPreviews|DeployLog|Redeploy'`
drives a fake Vercel through the registry's own file.

## Differs from the mock

**The ten corrections, as built** (the mock is being updated to match):
1. **Vercel key help.** It asks for a token for your personal account
   and says projects owned by a Vercel team aren't supported yet. The
   app says this even though the registry file's `key_help` says "Scope
   it to the account that owns the project". That file's text should
   change too.
2. **No setting to change.** There is no "change it in config.toml's
   [deploy.NAME]": the project or site is found only by the repository
   it builds from, "there is nothing to choose".
3. **Netlify has no build log.** A failed Netlify deploy shows its error
   message and says Netlify's API gives no build log. The log view is
   Vercel's only, decided by `HasBuildLog` from the file rather than by
   name. An empty log falls back to the deploy's error.
4. **Head-only lists.** With a pull request, only the deploys at its
   head commit are listed (`sha={head}`), with a line saying so. Without
   one, the branch's history is listed.
5. **No Production row.** No operation reads production's current
   deploy, domain or commit. The release sheet's box therefore reads
   "Vercel production → 3f2a91c · preview-host".
6. **Vercel's promote is a new build.** It shows as a Building
   production row until Vercel lists that build, while Netlify's publish
   is immediate. Which is which comes from `Release.Rebuilds`.
7. **All four states are drawn.** Canceled says "Canceled before it was
   ready", where the mock says "Superseded by a newer build". A canceled
   deploy is not always superseded: Vercel's DELETED and Netlify's
   rejected also map to canceled.
8. **Nothing found.** The text is the server's, naming the remote's host
   and path ("…linked to github.com/OWNER/REPO" / "…builds from
   OWNER/REPO").
9. **Commit messages come only from local git, by sha.** A deploy's name
   (Vercel's project name) is no longer shown in their place.
10. **Netlify's note.** Its wording is the corrected one. The row shows
    its first sentence; the whole note is under the table, once.

**Other differences:**
- **Layout.** The previews are a card on the git tab's Overview, beside
  the pull request card, rather than a page of their own. The mock's
  stepper, sidebar and fonts are framing; the app's theme is kept. The
  "Mock shows" branch toggle is the mock's own device: the app shows
  whichever branch the tab is on.
- **The "community" chip.** Cards keep the window's standing chip ("not
  checked live"). "Community" is the tier word, used by the Community
  filter.
- **The connect sheet does not name the linked project** ("web-app,
  linked to github.com/acme/web-app"). The check reads the project but
  reports a count of deploys, not the project's name.
- **The log.** It is the end that the server reads (64 KB, as for CI),
  not the whole output.

## Anything left

- **Not run against the real Vercel or Netlify APIs.** No keys were
  available, and tests must not reach the network. Row 9.205 in
  docs/testing/end-to-end.md is the live run. DEPLOY-STATUS.md lists the
  endpoints to confirm.
- **Not opened and clicked in a running Conch.app.** The views compile,
  and the logic is tested in ConchKit (`DeployTests`,
  `IntegrationsTests`, `ProjectIntegrationsTests`). The git tab was not
  compared with the mock in a running app or a headless render.
- **A pending production build may linger.** It is dropped once Vercel
  lists a production deploy of that commit at the branch's head. If
  Vercel lists it under another branch, the Building row stays until the
  tab is closed.
- **Not built yet:** Vercel team projects (`teamId`), a preview chip on
  the branch's row in the TUI's tree, and releasing from the TUI (it has
  `conch integration promote`, which asks).
- **A pre-existing test was fixed while here.**
  `TestA4SandboxCreateRefusesBeforeSpending` failed in every full
  `cmd/conch` run, on master too. A Keychain lookup set by
  `TestA4MainDispatch`'s in-process `main()`, and a key left in
  `keychainCache`, reached it. Both are now reset after their tests.
