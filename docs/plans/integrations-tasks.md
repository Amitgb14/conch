# conch integration — task list (phase 1, CI)

Design: [marketplace.md](marketplace.md). Already on
`conch/conch-markertplace-feature-i-think` (every task starts from it):

- `docs/plans/marketplace*.{md,html}` — design, research, mocks
- `internal/integration` — the CI file format (`integration.go`), its
  check (`File.Problems`), detection (`ParseRemote`, `File.Uses`, `For`),
  `All()` (shipped files replaced by `$CONCH_HOME/integrations/NAME.toml`),
  the guide (`format.md`, as `integration.Guide`) and two shipped files
  (`files/circleci.toml`, `files/gitlab-ci.toml`), with tests

What is left is four tasks. Each owns its files; none edits another's.

| Task | Owns | Needs | Size |
| --- | --- | --- | --- |
| A. Engine | `internal/integration/run.go`, `run_test.go`, `testdata/` | — | M |
| B. Server fallback | `internal/server/cilog.go` (+test), `internal/proto` (field, capability) | A | S–M |
| C. CLI | `cmd/conch/integration.go` (+test), `internal/integration/source.go`, `main.go` usage, help/completion | — | S–M |
| D. Docs | `web/src/app/docs/integrations/`, nav, CI pages, `AGENTS.md`, `docs/testing/end-to-end.md` | names from A–C (fixed below) | S |

Order: A, C and D can run side by side; B starts from A's branch.

The contract between them, fixed here so they can be built apart:

```go
// internal/integration (task A)
type Target struct {
    Root   string // the checkout, for nothing yet but kept for detection
    Remote Remote
    Branch string
    Head   string // the pull request's head commit; "" when none
}
type Run struct {
    Source   string // the integration's label: "CircleCI"
    Workflow string
    URL      string
    Log      string // the whole text; the server keeps the end
}
var ErrNoRun = errors.New("no failed run")         // a pick found nothing
func (f *File) FailedLog(ctx context.Context, key string, t Target) (Run, error)
```

Every task's prompt ends with the same rules:

> Follow AGENTS.md. Write the test that fails without the change first.
> Tests never reach the network, a real CI service, the Keychain or the
> developer's conch: `httptest`, `t.TempDir()`, `t.Setenv("CONCH_HOME", …)`,
> `CONCH_SECURITY` for the Keychain, `CONCH_SOCKET=""`. Run
> `go test -race -count=1 ./internal/integration/ ./internal/server/ ./cmd/conch/`
> for what you touched, then `make lint`. Commit in small commits saying
> what and why. Don't push, rebase or merge. Touch only the files listed
> as yours; if you need something from another task's files, say so in
> your summary instead.

---

## A. Engine — run a file's `[failed_log]` against the service

**Prompt:**

Build the engine for conch's CI integration files in
`internal/integration/run.go`. Read `internal/integration/format.md` (the
format, and the rules the engine must keep), `integration.go`, and the two
shipped files in `files/`. Add exactly this API:
`Target`, `Run`, `ErrNoRun` and `func (f *File) FailedLog(ctx, key string,
t Target) (Run, error)` as in `docs/plans/integrations-tasks.md`.

What it does:

1. Placeholders: `{project}` from `[project] id` filled with the remote's
   `{host} {owner} {repo} {path} {path_escaped}`, put in raw; `{branch}`
   `{head}`; `{NAME.PATH}` for an earlier step's value; `{item}` /
   `{item.PATH}` inside `each`. Escape every value but `{project}` where
   it lands: `url.PathEscape` in the path, query values through
   `url.Values`, and a query parameter left empty is dropped (`sha={head}`
   with no head asks for nothing). A placeholder whose value is missing is
   an error naming it, not an empty string, except in a query or a
   selector.
2. `pick` paths, exactly as `format.md` says: dotted keys and indexes,
   `[k=v]` the first match, `[*k=v]` every match, `[*]` every item (and
   alone at the root), a selector key reaching in with `/`, a value filled
   from placeholders, an empty value matching every item; after `*`, the
   rest read in each item and flattened, nothing kept for an item that
   lacks it. Values compare as strings (a number 12 reads "12").
3. A `call` goes to `api` + path with the key in `auth_header` /
   `auth_value` (defaults `Authorization` / `Bearer {key}`), `Accept:
   application/json`, and a `User-Agent` of `conch`. A `fetch` URL must be
   https (or http on a loopback host, for tests) on a host in `follow`,
   exactly or as a subdomain of one, and is **never** sent the key, nor
   followed through a redirect to any host outside `follow` (set
   `CheckRedirect`; a call's redirects must stay on api's host, too).
4. `each` runs the step once per item of the named step's list, in order;
   texts joined with a newline. `text` keeps the reply's body; `lines`
   reads a JSON list and joins each item's PATH with "\n".
5. A `pick` that finds nothing → `ErrNoRun` (wrapped, naming the step).
   A non-2xx reply → an error with the status and the first 200 bytes of
   the body, and for 401/403 the hint `check $KEY_ENV`. No key → an error
   naming `key_env`, before any request. Reply bodies are read up to 16 MB
   (more is an error); the log is returned whole.
6. `Workflow` and `URL` are the operation's templates filled from the
   steps; `Source` is `f.DisplayName()`.
7. The HTTP client is a package variable (timeout one minute), so the
   server's tests can point it elsewhere; the context bounds everything.

Tests (`run_test.go`, `httptest` only):
- the path grammar alone, table-driven, every form above plus misses,
  a list of one, an empty list, nested `*`, a number compared as text;
- CircleCI's shipped file end to end against an httptest server serving
  recorded reply shapes in `testdata/circleci/` (pipeline, workflow, job,
  v1.1 steps with two failed actions, output JSON) — copy the file and
  point `api` and `follow` at the test server; the log holds both
  actions' messages in order, URL and Workflow are filled;
- GitLab's shipped file the same way (`testdata/gitlab/`, a nested-group
  project so `{path_escaped}` is `a%2Fb%2Fc` on the wire), two failed
  jobs' traces joined, `sha=` dropped when Head is "";
- the key: sent to api only, never to a follow host (assert the header is
  absent there), refused with no key, the 401 hint;
- a fetch to a host not in follow, an http fetch to a non-loopback host,
  a redirect off-host from both a call and a fetch: all refused;
- no failed pipeline → `errors.Is(err, ErrNoRun)`; a non-JSON reply where
  JSON is wanted; a 500; a body over the cap; a cancelled context.

Yours: `internal/integration/run.go`, `run_test.go`, `testdata/`. You may
fix a shipped file in `files/` if a test shows it wrong — say so.

## B. Server fallback — `branch.ci_log` reads the failure from an integration

**Starts from A's branch** (`conch task -after A`).

**Prompt:**

When a branch's CI fails on a service other than GitHub Actions,
`branch.ci_log` (`internal/server/cilog.go`) answers "no failed GitHub
Actions run" and the autopilot's `auto_fix` and `conch branch fix-ci` have
nothing to give the agent. Make it read the failure through the CI
integrations the project uses (`internal/integration`: `For`,
`File.FailedLog`, `ParseRemote`; read `format.md` and
`docs/plans/integrations-tasks.md`).

1. `proto.BranchCILog` gains `Source string \`json:"source,omitempty"\``
   ("GitHub Actions" is left empty, so old clients see nothing new).
   Add `CapCIIntegrations = "ci.integrations.v1"` to `proto.Capabilities`
   with a comment, as the neighbours have.
2. In `ciLog`: try `ghx.FailedLog` first, unchanged. On
   `ghx.ErrNoFailedRun` only, take the project's remote
   (`gitx.RemoteURL`) and the integrations it uses (`integration.For(
   KindCI, p.root, remote)`), in name order. For each: the key from
   `s.trackerSecret(f.KeyEnv)` (environment, then Keychain — reach the
   server through `pm.s`); no key → remember "CircleCI: set $CIRCLECI_TOKEN
   where conch's server runs, or in the Keychain as conch-mcp
   CIRCLECI_TOKEN" and go on; `FailedLog` with the branch and the PR's head;
   the first with a log wins, capped to the end as Actions logs are, with
   `Source` set. `ErrNoRun` from one → go on. Another error → remember it,
   go on.
3. Nothing answered: `ErrNotFound`, and the message says what was tried —
   "no failed GitHub Actions run on B" plus, per integration, its
   outcome (no failed run / no key / the error). A project that uses none
   keeps today's message word for word (`TestBranchCILogRefusals` stays).
4. The autopilot's prompt (`proto.CIFailurePrompt`) names the source when
   set ("CircleCI failed on this branch…") — test it in
   `internal/proto/autopilot_test.go`.
5. `conch branch fix-ci` prints the source when there is one (one line in
   `cmd/conch/branch.go`, nothing else there).

Tests (`internal/server/cilog_test.go`, the existing fake gh and
`setupRepo`): a project with `.circleci/config.yml` and a github remote,
an httptest CircleCI (install a copy of the shipped file into
`$CONCH_HOME/integrations/circleci.toml` with `api`/`follow` pointed at
it, or set A's client variable), key from the environment → the log,
`Source` "CircleCI", capped; the key from a fake Keychain (`CONCH_SECURITY`
script, `keychainHas`); no key anywhere → the message names the variable;
an integration with no failed run and one that errors → both said; gh
finding an Actions failure still wins and never calls the integration
(the test server fails the test if hit); the capability is announced.

Yours: `internal/server/cilog.go`, `cilog_test.go`, `internal/proto`
(the field, the capability, `CIFailurePrompt` and their tests), the one
line in `cmd/conch/branch.go`.

## C. CLI — `conch integration`

**Prompt:**

Add `conch integration` to the CLI (`cmd/conch/integration.go`,
dispatched from `main.go` as `integration` / `integrations`), on top of
`internal/integration` (read `integration.go`, `format.md`,
`docs/plans/integrations-tasks.md`). Model it on `conch sandbox provider`
(`cmd/conch/sandbox*.go`, `registry.go`) — its flags, `-json`, wording.
It runs locally and needs no server.

- `conch integration ls [-json] [-cwd DIR]`: every integration from
  `integration.All()` — name, label, kind, `shipped` or `installed`,
  whether its key is set (environment only; say "key: $NAME not set"),
  and `uses` when the project at DIR (default the current directory: its
  git root and `gitx.RemoteURL`) uses it. Files `All` refused are listed
  after, with why, and the exit status is still 0.
- `conch integration show NAME`: the file's text — installed, else
  shipped. Add `internal/integration/source.go` with
  `func Source(name string) ([]byte, bool /*shipped*/, error)` for it
  (CheckName first; the path is made of the name).
- `conch integration check [-json] NAME|FILE`: what is wrong with the
  file, one line each, exit 1 when anything is; "NAME is fine" otherwise.
  A FILE is anything with a `/` or ending `.toml`.
- `conch integration guide`: prints `integration.Guide`, for an agent
  writing a file.
- `conch integration` with no subcommand or `-h`: its usage.

Add the commands to `usage` in `main.go`, to `conch help integration`,
and to completion (`completion.go`), and make sure the tests that check
usage against the docs pass (`help_test.go`, `docs_test.go`,
`makedocs_test.go` — read what they demand; D writes the web page).

Tests (`cmd/conch/integration_test.go`, `t.Setenv("CONCH_HOME", …)`,
`HOME`, `CONCH_SOCKET=""`): ls with shipped files only, with an installed
one replacing a shipped one, with a broken file, `-json` shape, `uses`
for a checkout with `.gitlab-ci.yml` and for a gitlab.com remote (a git
repo in a temp dir with an explicit identity), key set/unset; show for
shipped, installed, unknown and `../x`; check clean, broken, unknown,
an unreadable file; guide; usage.

Yours: `cmd/conch/integration.go`, `integration_test.go`,
`internal/integration/source.go` (+ test), and the usage, help and
completion lines for this command.

## D. Docs

**Prompt:**

Write the user docs for conch's CI integrations. Read
`docs/plans/integrations-tasks.md` (what is being built and the exact
command names), `docs/plans/marketplace.md`, `internal/integration/format.md`
and the two files in `internal/integration/files/`.

- A page `web/src/app/docs/integrations/page.mdx`, in the style of its
  neighbours (look at the sandbox providers' page): what an integration
  is and why it is a file and not a plugin (two sentences, not the
  research); which ship (CircleCI, GitLab CI), the key each needs and
  where it comes from (environment, or the Keychain on a Mac); that
  `conch branch fix-ci` and the autopilot's auto_fix now read their
  failures; `conch integration ls | show | check | guide`; writing your
  own (`$CONCH_HOME/integrations/NAME.toml`, the format by example);
  what it cannot do yet (re-run, stats, a registry, other kinds — say
  "later", promise nothing).
- Add it to the docs navigation; link it from the pages about CI fixes
  and the autopilot (find them: `grep -rl "fix-ci\|auto_fix" web/src/app/docs`).
- `AGENTS.md`: a row for `internal/integration` in the repository map,
  in the voice of the others (what lives there, and the one rule that
  bites: a file's key goes only to its api, a follow host never gets it).
- `docs/testing/end-to-end.md`: rows for a real CircleCI failure and a
  real GitLab CI failure read through `conch branch fix-ci` and fixed by
  the autopilot, numbered after the last row there.

Build the site's check if one runs in tests (`go test ./cmd/conch/ -run
Docs`), and `cd web && pnpm build` if node and pnpm are installed (say if
not). Yours: the files above only.
