# Review: 1610533c (answers to REVIEW-M4.md) and e195e90f (M5: launcher, release builds)

I read both commits from the builder's repository and tested at
`git checkout --detach e195e90f` in this worktree, with
`env -u CONCH_SOCKET -u CONCH_PANE_ID`:

- `go test -race -count=1 ./cmd/conch-sandbox`: ok
- `go test -run 'Script|Capture' ./cmd/conch`: ok
- `go vet` and `gofmt -l cmd internal`: clean
- `sh -n` / `dash -n` on both scripts: ok

I also ran the launcher by hand in a scratch folder, with `env -i`,
`HOME` and the release both in scratch, and `CONCH_SANDBOX_URL=file://…`.
There was no network, and the "build" was a stand-in shell script whose
hash I pinned. Combinations tried:

- dash and macOS `/bin/sh` (bash 3.2), from a file
- `sh -s --` and `dash -s --` from stdin, including Orca's
  `base64 | base64 -d | sh -s --`
- a tampered cache
- a wrong-hash release
- neither curl nor wget installed
- `umask 0`
- a `conch-sandbox` on PATH that reads stdin, and the launcher itself on
  PATH as `conch-sandbox`

## 1610533c against REVIEW-M4.md

| # | Finding | Verdict |
| --- | --- | --- |
| 1 | Orphans across tools | **Fixed.** The meta line carries `tool=` (`herdr`, `herdr-app`, `orca`, `cli`). `pick` sets herdr; `--from-orca`/`--format orca` set orca (any other `--tool` beside them is refused). `create --tool` sets the rest, defaulting to `cli`. A rewrite keeps the tool. A block from before `tool=` existed reads as `cli`. `findOrphans` requires `own.Tool == tool`. Tests cover a sandbox of another tool not being offered. *But see note A.* |
| 2 | Unpushed branch | **Fixed.** With `--ref`, the script clones the whole repository, then checks `cat-file -e REF^{commit}` ("push it first"), then runs `checkout -B BRANCH REF`, or `--detach` with no branch. A branch alone is checked with `ls-remote --exit-code`, where 2 means "branch … is not on …". Tests run the generated script with sh against a real bare repository. |
| 3 | Orca's `git@` address | **Fixed.** `httpsOf(ORCA_REPO_URL)` is used, falling back to the address as given (which `check` then refuses with its message). |
| 4 | `--from-herdr` label match | **Fixed.** Only machine fields are read (`target`, `machine`, `machine.host`, `machine_id`, `machine.id`/`profile_id`), and ids are matched against herdr's machine ids only, never labels. The field names are a guess at herdr's, and the commit message says end-to-end.md checks them. |
| 5 | Credential helper | **Fixed.** `-c credential.helper=` comes first, so the sandbox's own helpers are cleared before the GH_TOKEN one, and `ls-remote` gets the same. |
| 6 | herdr app `project_root` | **Fixed for `--tool herdr-app`.** See note A. |
| 7 | Nits | **Fixed.** The ssh retry gives up after a one-minute grace on `Permission denied`, host key or name failures. `$HC_BIN_PATH` is honoured. `--ref` needs at least 7 characters. |

**Note A: the herdr app mod must pass `--tool herdr-app`.** The herdr app
call shape this review was given (`create --provider P --name N --json
--yes …`) has no `--tool`. Without it, the sandbox is recorded as `cli`, so:

- `orphans --tool herdr-app` never lists it, and the app's clean-up
  silently finds nothing;
- `project_root` is empty when nothing is cloned, so `space.new` gets no
  folder.

Nothing here needs to change, but the mod (in conch-herdr) and the docs
in M6 must say `--tool herdr-app`. A row in end-to-end.md should check
that orphans then lists its sandboxes.

**Still open from REVIEW-FIXES.md:** `removeAlias` still removes by name
(`alias.go:339`; called from `verbs.go:459` `abandon`, `:578` and `:602`).
A `setAlias` refused for a colliding alias is still followed by `abandon`
deleting the other sandbox's block. This round didn't claim to fix it; it
is listed here so it isn't lost.

---

## 1. The launcher calls itself without end when it is the `conch-sandbox` on PATH — must fix

`scripts/conch-sandbox.sh:51` probes `conch-sandbox version`. If the
launcher is itself on PATH under that name, the probe runs the launcher,
which probes again, and so on. That happens when someone copies or
symlinks it there, a package installs the launcher instead of the binary,
or a plugin directory on PATH holds it under that name.

Each level is a new `sh` that never returns, so this is a fork bomb.
Running it with the stand-in, in scratch, gave **1,549 processes within
5 seconds** that were still growing (all cleaned up afterwards). There is
no exit or error, so a tool calling it just hangs while the machine fills
with processes.

**Fix:**
- Mark the probe so a nested launcher skips it: `export
  CONCH_SANDBOX_LAUNCHER=$$` before the probe, and at the top skip the
  probe whenever `CONCH_SANDBOX_LAUNCHER` is set.
- Also run the probe as `conch-sandbox version </dev/null`, which matters
  for finding 2 as well.
- Add a test that puts the launcher itself on PATH as `conch-sandbox` and
  expects it to fetch and run the pinned build, under a timeout.

## 2. Under `sh -s` (how Orca runs it), anything that reads stdin eats the script, and the launcher exits 0 having done nothing — should fix

When the script arrives on stdin, bash reads it a byte at a time, and
every command it runs shares that stdin. A `conch-sandbox` on PATH that
reads stdin when asked `version` swallows the rest of the script. I
reproduced this with `cat launcher.sh | sh -s -- get`: **rc=0, no output,
nothing run.**

Orca would take that as a successful create or resume with an empty
result. The real conch-sandbox doesn't read stdin, nor do `uname`,
`curl`, `wget` or `mktemp`, so today only a foreign program on PATH
triggers it. But nothing guards against it, and no test runs the launcher
from stdin at all. Every launcher test runs `/bin/sh script args`, which
is not the shape Orca uses.

Related: the binary that is finally exec'd gets the leftover pipe as its
stdin, which is EOF. `key set` (key on stdin) and `pick` (questions)
therefore can't work through the stdin form. That is fine for Orca's
verbs, but worth a line in the launcher's header.

**Fix:**
- Add `</dev/null` to the probe and to `uname`, `curl`/`wget` and
  `mktemp`, or wrap the whole body in `main "$@" </dev/null` and exec the
  binary with the original stdin saved first (`exec 3<&0` … `exec "$bin"
  "$@" <&3`).
- Add a test that pipes the launcher to `sh -s -- get --json` (and to
  `dash -s` where dash exists), including a PATH `conch-sandbox` that
  reads stdin.

## 3. The cache folder takes the caller's umask, so the binary can be swapped between check and exec — low

With `umask 0`, `mkdir -p` made `~/.local/share/conch-sandbox/9.9.9`
mode `drwxrwxrwx` (observed). On a shared machine, another user can then
replace `conch-sandbox` after line 65 hashes it and before line 91 execs
it, or at any time afterwards. With a private folder, only the same user
could, and that user can do anything anyway.

The download path itself is sound:
- it writes to a `mktemp` file in the same folder,
- verifies the hash,
- `chmod`s, then
- renames with `mv -f`.

So a partial or wrong download never becomes `$bin`. I checked that a
wrong-hash release leaves nothing behind and exits 1. Two launchers
racing each rename a verified file over the other, which is atomic.

**Fix:** `umask 077` at the top (the `chmod 755` still makes the binary
runnable by its owner), or `chmod 700 "$dir"` after the `mkdir -p`.
Optionally refuse a `$dir` not owned by the user (`[ -O "$dir" ]`).

## 4. Smaller points (nits)

- **No curl and no wget:** the run prints `wget: not found`, then "could
  not fetch URL", which reads as a network failure. Say "needs curl or
  wget" when `command -v wget` also fails.
- **No `sha256sum` and no `shasum`:** the hash comes out empty and the
  message says the URL "is not the build this launcher pins". That fails
  safe, but is misleading. Say which tool is missing.
- **An interrupted download** can leave a `.download.XXXXXX` behind,
  because dash doesn't run an EXIT trap on SIGINT. It is never run or
  used, so this is harmless. `trap 'rm -f "$tmp"; exit 130' INT TERM`
  tidies it.
- **A copy of conch-sandbox on PATH isn't hash-checked.** The brief asks
  for exactly that (step 1), so this is accepted, but it means any
  program named `conch-sandbox` that prints a high enough version is run.
  Say so in the header, so nobody later assumes the pin covers it.
- **Re-cutting a tag:** `release.yml` replaces the builds with
  `--clobber`. A launcher already pinned to the first cut's hashes then
  refuses the new builds. That fails safe, but if a version is re-cut
  the plugins must take the new launcher.

## Checked and fine

- **Never runs an unpinned file.**
  - The cached copy is hashed on every run and re-fetched on mismatch.
    I tampered with it, and it was fetched again and the pinned build ran.
  - A download runs only after its hash matches. With a wrong hash
    nothing is run and nothing is kept.
  - The pin for the platform is required, and an empty one is refused.
  - `eval "pin=\${sha_${os}_${arch}}"` takes `os` and `arch` only from
    fixed lists.
- **POSIX sh.**
  - `local`, arrays and `[[` are not used.
  - `set --` inside `at_least` changes only the function's own arguments.
  - IFS is restored after use.
  - It runs under dash and macOS sh, from a file and from stdin with
    arguments (including ones with spaces). `${1:-}` keeps `set -u`
    happy with no arguments.
- **Version comparison:** the PATH-copy test covers the table (`1.10.0`
  above `1.2.3`; `1.2.3-dev` below it; `dev`, empty, `1..3`, `x1.2.3`
  refused). A pinned pre-release is met by itself or by its release.
- **sandbox-release.sh:**
  - the version is checked against `[0-9A-Za-z.-]` before going into sed;
  - the builds have no cgo;
  - `SHA256SUMS` is written;
  - it refuses to produce a launcher missing any hash;
  - it runs after `release.sh` empties `dist/`;
  - `dist/conch-sandbox-*` does not match `conch-sandbox.sh`.
- **release.yml:** the only trigger is `push: tags: ["v*"]`. No
  `pull_request` or `workflow_dispatch` path reaches the publish step, and
  `ci.yml` doesn't run `sandbox-release`. A pull request cannot publish
  anything.

VERDICT: FIX
