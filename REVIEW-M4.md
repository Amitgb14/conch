# Review: conch-sandbox M4 (79850e3e, 4ca20e20)

Checked against the brief's M4 section, `sandbox-bridges.md` (Orca, herdr,
herdr app) and AGENTS.md. The M1–M3 findings in REVIEW.md are not repeated
here. In particular, the committed `cmd/conch-sandbox/conch-sandbox` binary
is still there and was rewritten again in 4ca20e20.

What I ran, at `git checkout --detach 4ca20e20` in this worktree, with
`env -u CONCH_SOCKET -u CONCH_PANE_ID`:

- `go test -race -count=1 ./cmd/conch-sandbox`: ok
- `go test -race -count=3 -run 'TestSafelyTakesAPanic|TestKeep' ./internal/server`: ok
- `go vet ./cmd/conch-sandbox` and `gofmt -l cmd internal`: clean

**79850e3e (server test) is right.** The test now waits for `safely` itself
to return, not for the panicking function's defer. That closes the window
in which `repanic` was restored before `recovered` read it. `goSafe` is
still covered as `go safely(...)`.

---

## 1. `orphans` and `pick` treat every other tool's sandbox as herdr's orphan, and offer to delete it — must fix

`cmd/conch-sandbox/tools.go` `findOrphans` counts any sandbox that has an
alias block and no machine in the tool being asked. All three tools write
to the one alias file:

- herdr's `pick`
- the herdr app's `create --json --yes`
- Orca's `create --format orca` and `start --format orca`, which also call
  `setAlias`

So a sandbox made for the herdr app or Orca, which is alive and in use
there, is an "orphan" of herdr:

- `orphans --tool herdr --notify`, which runs on herdr's
  `workspace.closed` event, reports it as forgotten and still billing.
- `pick --tool herdr` asks "Delete conch-daytona-…? y/N" for it. A person
  clearing out what they think herdr left behind deletes the sandbox an
  Orca workspace is running in.

The reverse happens with `--tool herdr-app`. `orphans` has no test with a
sandbox that belongs to another tool.

**Fix:**
- Record the owning tool in the meta line, for example
  `tool=herdr|herdr-app|orca|cli`.
- Set it in each path: `pick` sets herdr; `--from-orca` and
  `--format orca` set orca; give `create` a `--for TOOL` flag (or
  `--tool`) for the herdr app's mod; the default is `cli`.
- Have `findOrphans(tool)` count only blocks with that tool.
- Add tests in which an Orca and a herdr-app sandbox are not orphans of
  herdr, and the other way round.

## 2. A branch that isn't pushed yet makes the clone fail and the sandbox get deleted — must fix

`cmd/conch-sandbox/clone.go` `cloneScript` always runs
`git clone --branch B`, and only afterwards runs `git checkout -B B REF`.
`git clone --branch B` fails ("Remote branch B not found in upstream
origin") when B exists only locally. That is the normal case for:

- **Orca:** a new workspace is a new branch. `ORCA_REPO_BRANCH` is that
  branch and `ORCA_REPO_REF` its base commit, which is pushed.
- **`pick`:** it offers the folder's current branch.

The clone fails, `makeSandbox` abandons and deletes the sandbox, and the
whole create fails after the person has waited for it. The
`checkout -B B REF` line was written for exactly this case (the commit
message calls it "the branch, at the commit asked for"), but it is never
reached. The fake git in `clone_test.go` only logs its arguments, so no
test can catch this.

**Fix:**
- When `--ref` is given, run `git clone` without `--branch` (a full clone
  fetches every branch), then `git checkout -B B REF`.
- With `--branch` and no `--ref`, try `--branch B`. If B is not on the
  remote, say so clearly ("push B first, or give --ref"), not git's error.
- Add a test of the generated script against a real local bare
  repository (`git init --bare` in `t.TempDir()`, run with sh, explicit
  identity), covering an unpushed branch with `--ref`.

## 3. `--from-orca` refuses Orca's usual repo address (`git@…`) — should fix

`create --from-orca` takes `ORCA_REPO_URL` as it is. `repo.check` refuses
anything that is not `https://`, so a workspace whose origin is
`git@github.com:o/r.git`, the common case, fails with
"give the repository's https:// address". `pick` already converts these
addresses (`httpsOf`), but `--from-orca` does not.

**Fix:** in `create`'s `--from-orca` branch, apply `httpsOf` to
`ORCA_REPO_URL` when it is not already https. Only refuse when it gives
nothing. Add a test with `ORCA_REPO_URL=git@github.com:o/r.git`.

## 4. `--from-herdr` can pick another machine's sandbox by matching a label — should fix

`cmd/conch-sandbox/herdr.go` `herdrAlias` reads every string anywhere in
`HERDR_PLUGIN_CONTEXT_JSON`. If none of them is an alias, it matches
**any** of those strings against herdr's machine ids **and labels**.

The context also carries things like the workspace's label and cwd.
Suppose a workspace on this computer is labelled the same as a sandbox
machine, as in herdr's own example: `pick` labels machines "Daytona
feat-login", and a workspace is often named "feat-login". Then the context
can match exactly one sandbox that this workspace is not on, and the
action proceeds against it. `rm --from-herdr` would then delete it and
remove herdr's machine for it. Ambiguity is caught only when two or more
match.

**Fix:**
- Read the machine from the field herdr documents for the workspace's
  machine (the context's workspace → machine id or target), not from
  every string.
- Match machine ids only, never labels.
- If the action is destructive (`rm`, `stop`) and the source is not an
  exact field, refuse.
- Add a test with a context whose workspace label equals another sandbox
  machine's label.

## 5. A token can be saved by the sandbox's own git credential helper — low

`clone.go`: the clone passes
`-c 'credential.helper=!f() { … password=$GH_TOKEN; }; f'`.
`credential.helper` is a list, and `-c` adds to it. If the image already
configures a helper (for example `credential.helper store`, which some dev
images set), git asks both, and after a successful fetch calls `approve`
on every helper. That writes `GH_TOKEN` to `~/.git-credentials` in the
sandbox, where it outlives "this clone alone".

Otherwise the clone keeps the token out of every place it could leak:
- The script goes in on ssh's stdin (`sh -s`), so the token is never in
  process arguments here or there.
- The helper's text holds the literal `$GH_TOKEN`, not the token.
- `-c` is not written to `.git/config`.
- The remote URL is refused if it has credentials, so `.git/config`'s
  origin is clean.
- ssh's stderr has the token-user struck out before it reaches an error.

**Fix:** reset the list first:
`-c credential.helper= -c 'credential.helper=!f() …'`. An empty value
clears inherited helpers. A test can assert both `-c` values appear, in
that order, in the fake git's log.

## 6. The herdr app gets no `project_root` without `--repo` — low

The design's herdr app step 4 is
`run("space.new", { machine: alias, folder: project_root, … })`. However,
`create --json --yes` without `--repo` leaves `project_root` empty: only
`pick`, `--format orca` and `--from-orca` ask for the home folder
(`req.root`/`req.orca`). The mod would then pass an empty folder.

**Fix:** with `--json`, always fill `project_root` (the home, through
`homeScript`), or add `--root` and have the mod pass it. Add a test that
`create --json` without `--repo` has a `project_root`.

## 7. Smaller points (nits)

- **The ssh retry can wait out the whole deadline on an auth failure.**
  `remoteRoot` treats every exit 255 as "not answering yet". A permanent
  "Permission denied (publickey)", such as a passphrase-protected default
  key under `BatchMode=yes`, retries for the full 10 minutes before the
  box is deleted. Stop retrying when stderr says `Permission denied`,
  `Host key verification failed` or `Could not resolve hostname` after
  the first success-capable try, or cap auth retries at about a minute.
- **`hc` is looked up on `PATH` only** (`hcBin`). The herdr app's mod runs
  with a GUI app's `PATH`. Allow an override (`HC_BIN_PATH`, mirroring
  `HERDR_BIN_PATH`), or have the mod pass the path.
- **`--ref` accepts 4-character abbreviations** (`refChars`). An ambiguous
  short hash fails late, inside the sandbox. Require at least 7, or
  require a full 40/64.

## Call shapes checked

- **herdr app.** `create --provider P --name N --json --yes [--repo URL
  --branch B --ref SHA] [--size S]` parses. `--yes` accepts the Include
  with no terminal, and nothing is asked. `stop|start|rm --machine ALIAS`
  resolves through the meta line (`aliasOwner`). An alias without that
  line, or whose line names another alias, is refused. A `--provider`
  given beside `--machine` must agree.
- **Orca.** `create|start --provider P --from-orca --format orca` prints
  `schemaVersion 2`, `provisioned-root`,
  `connection {type ssh, projectRoot, target {label, host, port,
  username}}` and `userData {provider, id}`. It skips the Include, since
  Orca connects by address, and uses the alias as the host for a key
  sandbox when the Include exists.
- **Orca's ORCA_VM_INSTANCE_ID.** `stop|rm --provider P --from-orca` reads
  it as either the userData JSON or a bare id. A provider mismatch is
  refused, and the id is validated by `checkID`.
- **Token output.** The token is printed only under `--format orca`, as
  the design allows. `--json` still strikes it out.
- **Brief coverage.** `--repo/--branch/--ref`, `--from-orca`,
  `--format orca`, `--from-herdr`, `--machine` on get, start, stop, rm and
  `ssh-alias --remove`, provider and id recoverable without splitting at
  `-` (the meta line), `orphans --tool … [--json] [--notify]` and
  `pick --tool herdr` with fake terminal input are all present. Their
  tests use stand-ins (fake ssh, herdr, hc), with `PATH=/usr/bin:/bin`,
  `CONCH_SSH` and `HERDR_BIN_PATH` isolated. Nothing reaches the real
  `~/.ssh`, `~/.config/conch`, herdr or Orca.

VERDICT: FIX
