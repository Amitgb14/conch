# Code hosts beyond GitHub (T13)

Status: plan, 2026-11-15, for review before any code. Part of
[marketplace.md](marketplace.md); task T13 of
[marketplace-tasks.md](marketplace-tasks.md).

## What conch assumes today

Every pull request, check, merge, review and issue goes through
`internal/ghx`, which runs `gh`. These are its callers (outside tests):

| Caller | What it uses |
| --- | --- |
| `server/projects.go`, `github.go` | `List`, `ByBranch`, `PR`, `Check` — each branch's pull request and checks, at every refresh |
| `server/harvest.go`, `autopilot_ship.go` | `Create`, `Ready`, `Merge` (with auto-merge) — `conch branch pr/merge`, the autopilot |
| `server/gitview.go` | `Reviews`, `PRReviews`, `LineComment`, `Review` — review threads, submitting a review |
| `server/issues.go`, `intake.go`, `triage.go`, `ticketcheck.go`, `ticketoverview.go` | `Issue*`, `CommentIssue`, `EditLabels`, `EnsureLabel`, `IssueCount` — issues, intake, triage |
| `server/cilog.go` | `FailedLog`, `RerunFailed` — already one source among several (T3) |
| `cmd/conch/doctor.go`, `registry.go` | `Binary`, `Whoami`, `API` — checks, publishing to the registry |

So a project on GitLab or Bitbucket has no pull requests, checks, merges
or reviews in conch. Its CI can already be read (T3/T4: GitLab CI).

## Decision proposed: code, not files, for this kind

Integration files fit CI: a run, a job, a log, a re-run — a small model
whose answers conch maps. A code host's model is large (merge requests and
their approvals, discussions anchored to diff lines, draft state, merge
methods, auto-merge, protected branches) and differs in semantics, not
only in paths: a GitLab discussion is not a GitHub review thread with
other field names. A file that tried would be a programming language in
TOML, which is what the design refuses.

So each host is **Go code in conch** behind one interface, as GitHub is
now, and `kind = "host"` files are not written. What stays data: the
host's address (`[integrations.base]` for self-managed GitLab), and its
key, read as integrations read theirs (environment, then Keychain).

## The interface

A new package, `internal/forge`, with `ghx` becoming its GitHub side
unchanged underneath:

```go
type Host interface {
    Name() string                                   // "github", "gitlab", "bitbucket"
    Can() Caps                                      // what it supports, said, never guessed
    List(ctx, repo) ([]PR, error)                   // open and recent, with checks
    Create(ctx, repo, base, head, title, body string, draft bool) (string, error)
    Ready(ctx, repo, number int) error
    Merge(ctx, repo, number int, head string, auto bool, method string) (string, error)
    Threads(ctx, repo, number int) ([]Thread, error)   // review threads, resolved or not
    Comment(ctx, repo, number int, at LineRef, body string) error
    SubmitReview(ctx, repo, number int, verdict, body string) error
    Issues(ctx, repo, q IssueQuery) ([]Issue, error)    // intake, triage, tickets
    CommentIssue / Label(ctx, …) error
    Whoami(ctx) (string, error)
}
```

`Caps` is what the clients read to degrade per host, as they degrade per
server capability: `AutoMerge`, `Drafts`, `ReviewThreads`,
`ResolveThreads`, `MergeChecks`, `Issues`. A project's host comes from its
remote (`github.com`, `gitlab.com` or a configured self-managed address,
`bitbucket.org`); the protocol gains `ProjectInfo.host` and a capability
`forge.v1`, and every PR type stays the one clients have now.

## GitLab

- Through `glab` where it covers the call (`mr list/create/merge/view`,
  `glab api` for the rest), as GitHub goes through `gh`: `glab` holds its
  own login, including self-managed instances, and is the vendor's own.
  This is a **built-in** adapter: no registry file may run a program.
- Merge with `auto_merge=true` (`merge_when_pipeline_succeeds` is
  deprecated since GitLab 17.11).
- Review threads are discussions (`/merge_requests/:iid/discussions`),
  anchored by `position` (base, start and head SHAs and a line), which a
  `LineRef` must carry; resolving is per discussion.
- Issues: `/projects/:id/issues`, labels as GitHub's.
- Test: a fake `glab` on `CONCH_GLAB`, as `CONCH_GH` today; the live rows
  in end-to-end.md on gitlab.com and on a self-managed instance.

## Bitbucket Cloud

- No vendor CLI conch can lean on, so its REST API directly
  (`/2.0/repositories/{workspace}/{repo}/pullrequests…`), with an
  **API token with scopes** — app passwords cannot be created since
  2025-09-09 and stopped working on 2026-07-28.
- Checks are build statuses on a commit (`/commit/{sha}/statuses`).
- What conch can't show, said in `Caps` and in the UI rather than
  pretended: there is no public API for merge checks, so "ready to merge"
  is the statuses and approvals only; auto-merge must be verified against
  the API before it is offered (it is not in the research yet).
- Comments: inline comments on a pull request (`inline.path`, `inline.to`);
  no resolvable-thread model to match GitHub's, so `ResolveThreads` is
  false.
- Issues: Bitbucket's issue tracker is off in most workspaces and teams
  use Jira — so `Issues` is false and intake reads Jira (built in).

## Order

1. `internal/forge` with GitHub only: the interface, `ghx` behind it, every
   caller moved to it. No behaviour changes; the whole suite proves it.
   This is the big refactor and lands alone.
2. Host detection on the project and `ProjectInfo.host`, `forge.v1`;
   clients show "GitLab merge request" where they said "pull request".
3. GitLab, the most asked for.
4. Bitbucket Cloud.
5. Conch.app and the TUI degrade per `Caps` (a Merge button with no
   auto-merge, no Resolve on Bitbucket threads).

Each step ships alone, with its own end-to-end rows.

## Questions for the owner

1. **Code, not files, for hosts** — agreed? (It changes the earlier plan's
   "an MR file for what glab lacks".)
2. **`glab` as a dependency** for GitLab, as `gh` is for GitHub — or the
   REST API directly, so nothing else needs installing?
3. **Bitbucket at all** this release, or GitLab first and Bitbucket on
   demand?
4. **Words in the UI**: "merge request" on GitLab projects, or one word
   ("pull request") everywhere?
