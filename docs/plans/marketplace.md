# Integrations: one place to browse and install what conch talks to

Status: proposal, 2026-11-15. Mocks: [marketplace-mocks.html](marketplace-mocks.html)
(open in a browser).

## The problem

conch talks to services: GitHub through `gh` (pull requests, checks, Actions
logs, re-runs, issues), Linear and Jira over their APIs, and sandbox
providers through provider files. Everything else a team uses — CircleCI,
GitLab, Bitbucket, Buildkite, Jenkins, Shortcut — is either invisible or
half-seen:

- A CircleCI check turns a pull request red and conch sees that (CircleCI
  reports to GitHub), but `auto_fix`, `conch branch fix-ci` and Re-run
  failed jobs read logs from GitHub Actions only, so the agent is told
  nothing and the person reads "the failing check is another service's".
- A project on GitLab or Bitbucket has no pull requests, checks or merges
  in conch at all.
- Each new service so far has been code in conch, written by us, shipped
  in a release.

That doesn't scale to the long tail, and every team has a different tail.

## The idea

An **integration** is a *provider file*: a description of a service's HTTP
API — which call does what, where the answer is in the reply, how the key
is sent — for one **kind** of job conch already does (CI, code host,
tickets, sandboxes, notifications). conch's own code owns what is done with
the answers; the file only says where they are. Files come from a
**registry** (a GitHub repository with an index on Pages), are installed by
hash, and hold no secrets.

This is what conch already does for sandboxes
(`internal/sandbox/providerfile.md`, `conch-sandbox-providers`,
`conch sandbox adapt`). The proposal generalises it to every kind, and gives
it one window to browse.

Built-ins (GitHub through `gh`, GitHub Actions, GitHub Issues, Linear,
Jira, the sandbox providers in Go) are listed the same way, marked
*built in*, so there is one model and one place to look.

### Why data, not plugins

The research (below) is clear: every marketplace that runs extension code
— VS Code, Open VSX, Homebrew taps, npm — ends up bolting on signing,
publisher trust, scanning, block lists and forced uninstall, and still gets
GlassWorm and Shai-Hulud. Zed narrows it with WASM and capability grants;
that is a large runtime to own.

A data file can't run anything. Its one real risk is sending a key
somewhere it shouldn't, and conch already answers that: a file's hosts are
shown before install and asked again when an update changes them. So the
design keeps files declarative and grows the format (`format = N`, refused
by older conches) with **built-in operations** where APIs need more than
one call — never scripts.

Where data runs out (a non-REST API, a code host's whole review model),
the escape hatch is conch's own Go code for that kind of service, the way
GitHub is today, or a CLI the person already has (`glab`) behind a fixed,
built-in adapter.

## Research summary

Full report with sources: [marketplace-research.md](marketplace-research.md). Highlights:

| Precedent | What we take |
| --- | --- |
| **Zed** (`zed-industries/extensions`) | Registry repo; one PR per extension; versions pinned to a commit; host-scoped capabilities (`download_file` by host) — the model for "your key goes to these hosts only" |
| **Raycast** (`raycast/extensions`) | PR → review → published on merge; secrets as a secure preference, OAuth via PKCE with no secret in the extension |
| **Terraform Registry** | Immutable versions (never replace a released one); signed `SHA256SUMS`; trust tiers Official / Partner / Community |
| **Airbyte low-code** | The most complete declarative connector: requester + authenticator, paginator, record selector, validated by JSON Schema; manifest-only connectors run in a generic runtime. Where it runs out, it allows code — we won't |
| **n8n** | Declarative nodes are the default; triggers and non-REST need code. Verified nodes: no runtime deps, published from CI with provenance |
| **Nango** | Auth catalogue as data (`auth_mode`, scopes); mapping as code |
| **VS Code / Open VSX** | What goes wrong with code: 550 leaked publisher tokens, GlassWorm, lookalikes; a "verified" badge that meant only a DNS record until tightened |
| **Homebrew 6** | Third-party taps now need `brew trust` — explicit trust per source |

APIs a CI provider needs (runs for a branch/commit, a failed job's log,
re-run failed):

| CI | Runs | Failed log | Re-run | Auth |
| --- | --- | --- | --- | --- |
| CircleCI | `GET /api/v2/project/{slug}/pipeline?branch=` → workflows → jobs | **not in v2**: v1.1 build → each step's presigned `output_url` | `POST /workflow/{id}/rerun` `from_failed` | `Circle-Token` |
| GitLab CI | `GET /projects/:id/pipelines?ref=&sha=` | `GET /projects/:id/jobs/:id/trace` | `POST .../pipelines/:id/retry` | `PRIVATE-TOKEN` |
| Buildkite | `GET .../builds?branch=&commit=` (full SHA) | `GET .../jobs/{id}/log` (text) | `PUT .../builds/{n}/rebuild`, job retry | Bearer, scopes `read_builds read_build_logs write_builds` |
| Jenkins | `/job/NAME/api/json` | `/job/NAME/N/consoleText` | plugin-dependent (Rebuild, Replay) | Basic user + API token; base URL per site |

Code hosts: GitLab has `glab` and a full MR API (create, `auto_merge`,
pipelines, discussions). Bitbucket Cloud has no `gh`-like CLI, a REST API
for PRs and build statuses, no merge-check API, and app passwords gone
(fully removed 2026-07-28) in favour of scoped API tokens.

## Design

### Kinds and their contracts

A kind is a fixed set of **operations** conch's code calls, each optional
except where noted. A file says which it fills; the UI degrades per
operation, the way clients degrade per `proto.Capabilities`.

| Kind | Operations | Where conch uses them |
| --- | --- | --- |
| `ci` | `runs` (required), `failed_log`, `rerun`, `stats` | `ci.log` → autopilot `auto_fix`, `fix-ci`; `ci.rerun`; the CI panel |
| `host` | `prs`, `pr_create`, `checks`, `merge`, `comments`, `review` | Branch rows, `conch branch pr/merge`, autopilot merge, review threads |
| `tickets` | `list`, `get`, `comment`, `link`, `move` | Tickets view, `conch task -ticket`, intake |
| `sandbox` | as today (`create get list start stop delete ssh`) | Machines, `conch sandbox` |
| `notify` | `send` | Agent waiting / done / PR merged, to Slack, Discord, ntfy |

The shared model is small and fixed (a run, a job, a status, a log; a PR,
a check, a comment; a ticket). A file maps the provider's reply into it;
conch never sees the provider's own shape.

### The file

Same TOML as sandbox files, plus `kind`, `[detect]`, `[project]` and the
kind's operation tables. Mock 9 shows CircleCI. New in the format:

- **`kind`** — which contract.
- **`[detect]`** — files (`.circleci/config.yml`), remote hosts
  (`gitlab.com`), check names or link hosts (`ci/circleci*`,
  `app.circleci.com`). conch already reads each project's files, remote and
  checks; a match suggests the integration (mock 4).
- **`[project]`** — how the project's remote becomes the provider's id
  (`gh/{owner}/{repo}`, a GitLab project path, a Buildkite pipeline set by
  the person).
- **`steps`** — an operation as a short chain of calls, each `pick`ing an
  item (`items[status=failed]`, format 2's selectors) for the next.
- **`follow`** — fetch URLs found in a reply (CircleCI's presigned
  `output_url`). Followed hosts are listed in the file; the key is never
  sent to them.
- **`text`** — the reply is the answer as text (a log), optionally read
  from JSON lines.
- **`base_url`** asked of the person for self-hosted services (Jenkins,
  GitLab self-managed), and part of the host list they agree to.
- **`[auth]`** — `header`, `bearer` (with the scopes to ask for), `basic`
  (user + token); OAuth with PKCE later, through a conch-run redirect,
  never a secret in a file.

### Trust

| Rule | Why |
| --- | --- |
| A file runs nothing; a request goes only to the file's `api`/`base_url` with the key, or to a `follow` host without it | The whole attack surface is "where does the key go" |
| Hosts shown before install; an update that changes them asks again (exists for sandboxes) | Zed's host-scoped grants, without a runtime |
| Each version is immutable and pinned by sha256 in the index; the index is **signed** (minisign/age key in conch) | Terraform; a compromised Pages or repo can't swap a file |
| Published through the registry's CI with provenance; a PR per integration, reviewed | npm / n8n after Shai-Hulud |
| **Verified** means one concrete thing: a maintainer's key ran the file's operations against the real service with conch N on date D (`checked`, as today) | VS Code's badge meant too little |
| Tiers: *built in*, *verified*, *community* | Terraform's tiers, said plainly |
| The index can **revoke** a version; conch disables it at the next refresh and says why | VS Code's block list |
| Keys in the Keychain (exists for the library), or an environment variable; never in a provider file or `config.toml` | Raycast, Pipedream |
| Read and write operations listed separately (mock 2's Access); a write (`rerun`, `merge`) can be turned off per integration | The person decides what an integration may change |

### Where things are kept

- `$CONCH_HOME/integrations/NAME.toml` — installed files;
  `integrations/registry.json` — source, version, hash (as
  `providers/registry.json` today, which migrates into it).
- Which projects use what: the app/TUI's state by default; a team can
  commit `.conch/integrations.toml` (names only, never keys) so everyone's
  conch picks the same.
- Several registries: `conch-integrations` by default, others added by URL
  (a company's own), each with its own signing key, trusted explicitly
  (Homebrew 6's `brew trust`).

### Protocol

- `integration.list`, `integration.get`, `integration.install`,
  `integration.remove`, `integration.check`, `integration.detect`
  (capability `integration.v1`). The server installs and holds them, as it
  does sandbox files, so a remote machine's conch can have its own.
- CI operations go through the existing `ci.log`, `ci.rerun`, `ci.stats`
  with a `source` field; `BranchCILog` says which integration answered.
  An older server keeps answering for GitHub Actions only.
- `PRInfo.Checks` gains per-check `source` (from the check's name or link
  host), so a red check can say "CircleCI · install to read its log"
  (mock 4).

### Surfaces

- **Conch.app**: Settings → Integrations (mocks 1–3, 7); a project's
  Integrations sheet (mock 5); the branch's failing-check card (mock 4);
  ⌘K "Integrations".
- **TUI**: settings → Integrations (mock 8): list, page, install with a
  non-echoing key prompt, update, remove, adapt.
- **CLI**: `conch integration search|info|install|ls|update|remove|check|adapt|publish`
  — `conch sandbox provider …` stays as an alias.
- **Docs**: an Integrations page per kind, generated from the index.

## Plan

Each phase ships on its own and is useful alone.

**Phase 0 — spike (2–3 days).** Write `circleci.toml` and `gitlab-ci.toml`
by hand against the real APIs with the current sandbox engine extended
just enough (`steps`, `follow`, `text`). Learn what the format really
needs. Exit: a CircleCI failed log read end to end with a real token.

**Phase 1 — CI integrations (the ask).** `kind = "ci"`, the four
operations, `[detect]`, `ci.log/rerun` with `source`; CircleCI, GitLab CI,
Buildkite, Jenkins in the registry; failing-check card in the app and TUI.
Generalise the registry (rename to `conch-integrations`, keep the sandbox
index readable), signed index, revoke. Exit: the autopilot fixes a
CircleCI failure with a real agent (e2e row).

**Phase 2 — the window.** Settings → Integrations in Conch.app and the
TUI, project Integrations sheet, detection banner, `conch integration`
CLI, built-ins listed. Sandboxes move under it.

**Phase 3 — tickets and notifications as files.** Shortcut, Asana,
ClickUp, GitLab issues as `tickets` files; Linear and Jira stay built in
(they're done and tested) but are listed. `notify`: Slack (incoming
webhook — a URL, which is the key), Discord, ntfy.

**Phase 4 — code hosts.** The largest contract and the one data files fit
worst (review threads, approvals, merge rules). GitLab first, through a
built-in `glab` adapter plus an MR file for what `glab` lacks; Bitbucket
Cloud through its REST API with scoped tokens. This is where conch stops
assuming GitHub: `ghx` becomes one host among several behind an interface.
Big; plan separately once phase 1 has taught us the format.

**Phase 5 — adapt and publish for every kind.** `conch integration adapt`
(an agent drafts the file from API docs, in its own pane, without the key),
offline check, live check with the person's key, `publish` as a PR with the
check's result — what `conch sandbox adapt` and `registry publish` do now.

## Open questions

1. **Name**: "Integrations" (plain, used here) or "Marketplace"? Nothing is
   sold, and "marketplace" suggests paid listings; I'd say Integrations.
2. **Signing key custody** for the index: a key in conch's release
   pipeline, rotated with releases?
3. **Team config**: should `.conch/integrations.toml` be read without
   asking? (It only names integrations, but it makes a repository decide
   what your conch talks to.) Proposal: read it, ask once per repository.
4. **Code hosts via CLIs** (`glab`) vs files: a CLI holds its own login
   and is maintained by the vendor, but runs a program conch doesn't
   control. Proposal: allowed only for built-in adapters, never from a
   registry file.
5. **Remote machines**: install per machine (as sandboxes) or push the
   Mac's integrations to every machine? Keys would have to travel; proposal:
   per machine, with "install on busybox too" in the sheet.

## Out of scope

Paid listings, ratings and reviews, code plugins of any kind, integrations
that change conch's UI.
