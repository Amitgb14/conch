# Integrations: research notes

Gathered 2026-11-15 for [marketplace.md](marketplace.md). Points marked
(3rd-party) come from secondary sources; check them before relying on them.

## 1. Extension marketplaces in developer tools

- **VS Code Marketplace / Open VSX.** Hosted. Extensions run with VS Code's
  own permissions, unsandboxed. Mitigations: signed at publish and checked at
  install; a trust prompt for each new publisher (1.97); a block list with
  automatic uninstall; enterprise allow-lists.
  https://code.visualstudio.com/docs/configure/extensions/extension-runtime-security
  - The verified badge needs a domain plus 6 months in good standing.
  - Malware scanning: 110 of the 136 extensions reviewed in H1 2025 were
    removed.
    https://developer.microsoft.com/blog/security-and-trust-in-visual-studio-marketplace/
- **JetBrains Marketplace.** Every plugin and every update is approved by a
  person (about 2 business days). Signed twice, by the author and again by
  the Marketplace with a key held in KMS.
  https://plugins.jetbrains.com/docs/marketplace/jetbrains-marketplace-approval-guidelines.html ·
  https://plugins.jetbrains.com/docs/intellij/plugin-signing.html
- **Zed.**
  - Format: a git repository with `extension.toml`, published by a pull
    request adding it as a submodule to `zed-industries/extensions`, plus an
    `extensions.toml` entry. One extension per pull request; versions pinned
    to a commit.
    https://zed.dev/docs/extensions/publishing/publishing-guide
  - What is declarative: themes, languages and snippets.
  - What is code: language servers, MCP servers and debuggers, as Rust
    compiled to WASM.
  - Permissions: capability grants, such as `process:exec` scoped by
    command, `download_file` scoped by host and path, and `npm:install`.
    https://zed.dev/docs/extensions/capabilities
- **Raycast Store.** Publishing opens a pull request to `raycast/extensions`;
  after review it is published on merge. Secrets live in `password`
  preferences. OAuth uses PKCE through a proxy Raycast runs, so no client
  secret ships in an extension.
  https://developers.raycast.com/basics/publish-an-extension ·
  https://developers.raycast.com/api-reference/oauth
- **Terraform Registry.** Releases are GitHub Releases with a GPG-signed
  `SHA256SUMS` and a manifest. A released version is never replaced. Tiers:
  Official, Partner, Community, Archived.
  https://developer.hashicorp.com/terraform/registry/providers/publishing
- **Grafana.** Plugins are signed at private, community or commercial level.
  An unsigned plugin is not loaded unless it is allow-listed.
  https://grafana.com/docs/grafana/latest/administration/plugin-management/plugin-sign/
- **Homebrew taps.** A tap is `user/homebrew-repo`. Since 6.0 (June 2026)
  a third-party tap needs `brew trust`, because a tap is unsandboxed Ruby.
  https://docs.brew.sh/Tap-Trust
- **Backstage.** Plugins are npm packages listed in a directory. Community
  plugins live in a monorepo.
  https://github.com/backstage/community-plugins
- **n8n.** Declarative nodes (`routing`) are the default; triggers and
  non-REST APIs need code. A verified community node may have no runtime
  dependencies and, from 2026-05-01, must be published from GitHub Actions
  with provenance.
  https://docs.n8n.io/integrations/creating-nodes/plan/choose-node-method
- **Zapier.** Two ways to build: a no-code UI and a CLI, and moving from the
  UI to the CLI is one-way. Auth schemes: API key, OAuth v2, session, basic.
  https://docs.zapier.com/platform/build/auth
- **Pipedream.** Components are JavaScript. Credentials live in Connected
  Accounts, never in the component.
  https://pipedream.com/docs/apps/connected-accounts
- **Warp.** No extensions; YAML workflows only.
- **Orca.** No marketplace; GitHub, Linear and Jira are built in
  (3rd-party).

## 2. Declarative formats for API connectors

- **Airbyte low-code.**
  - Pieces: a requester with an authenticator (API key, bearer, basic,
    OAuth), a paginator (offset, page or cursor) and a record selector,
    validated by a JSON Schema.
  - Manifest-only connectors run in a generic image; the escape hatch is
    Python. It builds sources only, not destinations.
  - This is the most complete precedent.
    https://docs.airbyte.com/platform/connector-development/config-based/understanding-the-yaml-file/yaml-overview
- **Nango.** Auth is data (`providers.yaml`: `auth_mode`, scopes); syncs and
  actions are TypeScript.
  https://docs.nango.dev/reference/api-configuration
- **Singer.** A fixed message model (`SCHEMA`/`RECORD`/`STATE`) that
  programs emit; it does not describe how to call an API.
- **Steampipe.** Go gRPC plugins.
- **Overall.** Auth and pagination describe well as data. Mapping replies,
  triggers and non-REST APIs are where every system falls back to code.

## 3. CI provider APIs

- **CircleCI.**
  - Auth: the `Circle-Token` header.
  - Runs: v2 `GET /project/{slug}/pipeline?branch=`, then
    `/pipeline/{id}/workflow`, then `/workflow/{id}/job`.
  - Re-run: `POST /workflow/{id}/rerun`.
  - Logs: not in v2. Read the v1.1 build at
    `/api/v1.1/project/:vcs/:org/:proj/:build_num`, then fetch each step's
    presigned `output_url`, which CircleCI calls unsupported.
    https://support.circleci.com/en/articles/15090750-retrieving-job-step-output-via-the-api
  - Reports to GitHub as per-job commit statuses (`ci/circleci: job`), or as
    per-workflow Checks through its GitHub App.
    https://circleci.com/docs/guides/integration/enable-checks/
- **GitLab CI.**
  - Auth: `PRIVATE-TOKEN`.
  - Runs: `GET /projects/:id/pipelines?ref=&sha=`; failed jobs with
    `GET .../pipelines/:pid/jobs?scope[]=failed`.
  - Log: `GET .../jobs/:id/trace`.
  - Re-run: `POST .../pipelines/:pid/retry`.
  - Another CI posts into GitLab with `POST /projects/:id/statuses/:sha`.
    https://docs.gitlab.com/api/jobs/
- **Buildkite.**
  - Auth: a bearer token with scopes `read_builds`, `read_build_logs`,
    `write_builds`.
  - Runs: `GET .../builds?branch=&commit=` (the full SHA).
  - Log: `GET .../jobs/{id}/log` as text.
  - Re-run: `PUT .../builds/{n}/rebuild`, or retry one job.
  - Reports to GitHub as commit statuses through its GitHub App.
    https://buildkite.com/docs/apis/rest-api/builds
- **Jenkins.**
  - Auth: Basic, a user and an API token.
  - Every object answers at `.../api/json`; a log is at
    `/job/NAME/N/consoleText`.
  - Re-running depends on plugins (Rebuild, Replay), and each site has its
    own base URL.
  - Reports to GitHub as checks through the GitHub Checks plugin.
    https://www.jenkins.io/doc/book/using/remote-access-api/
- **On GitHub.** Only a GitHub App can create check runs; any token can post
  a commit status. So "checks" means merging both, which `gh pr checks`
  already does.

## 4. Code hosts

- **GitLab.**
  - CLI: `glab` (`mr create/view/merge`, `ci view/trace/retry`).
  - Merge request API: `POST/PUT /projects/:id/merge_requests[/:iid/merge]`
    with `auto_merge=true` (`merge_when_pipeline_succeeds` is deprecated
    since 17.11); discussions and notes for review.
  - https://docs.gitlab.com/api/merge_requests/
- **Bitbucket Cloud.**
  - No official `gh`-like CLI; the closest is Atlassian's Teamwork Graph
    CLI, `twg bitbucket pull-requests`.
  - REST API: pull requests, and build statuses keyed by `key`.
  - No public API for merge checks.
  - App passwords: none can be created since 2025-09-09, and they stop
    working entirely on 2026-07-28; use scoped API tokens instead.
    https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests

## 5. Security lessons

- **VS Code and Open VSX.** 550+ secrets leaked in extensions, publisher
  tokens among them. GlassWorm, a self-spreading worm, used leaked Open VSX
  tokens.
  https://www.wiz.io/blog/supply-chain-risk-in-vscode-extension-marketplaces ·
  https://blogs.eclipse.org/post/mikaël-barbero/open-vsx-security-update-october-2025
  - Established Open VSX extensions were hijacked again in January 2026.
  - A lookalike of Material Icon Theme was still listed in November 2025.
- **npm, Shai-Hulud (September 2025).** In response, GitHub moved to FIDO
  2FA, publish tokens limited to 7 days, and trusted publishing over OIDC
  with provenance.
  https://github.blog/security/supply-chain-security/our-plan-for-a-more-secure-npm-supply-chain/
- **The pattern.**
  - Sign, and check the signature on install.
  - Publish from CI with provenance.
  - Scan at publish time.
  - Keep a block list and uninstall what is on it.
  - Trust each source explicitly.
  - Scope capabilities by host.
  - The root problem everywhere is code that runs unsandboxed. Data-only
    providers avoid most of it.
