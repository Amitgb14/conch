# Review: conch-sandbox M1–M3 (a08b0ffc … c4b0b80d, against 20db9964)

Checked against `conch-sandbox-build.md`, `sandbox-bridges.md` and AGENTS.md.
No code was changed.

Run with `env -u CONCH_SOCKET -u CONCH_PANE_ID`:

- `go test -race -count=1 ./cmd/conch-sandbox ./internal/sshkey ./internal/sandbox ./internal/remote ./internal/config`: all ok
- `go test -race -count=1 -run 'Sandbox|Deleted' ./cmd/conch`: ok
- `go vet` on the touched packages and `gofmt -l cmd internal`: clean
- `ssh -G -F` against scratch files only (no network) to check how ssh reads the alias file. Details are in findings 6 and 9 and under "Checked and fine".

I did not run the full suite or `make gate`; conch runs those before shipping.

---

## 1. A 9.6 MB macOS binary is committed — must fix

`cmd/conch-sandbox/conch-sandbox` (added in c4b0b80d) is a Mach-O arm64
executable, probably left by running `go build` inside `cmd/conch-sandbox`.
`.gitignore` covers `/bin/` and `/conch`, but not this path. Once this is
pushed it stays in the history for good, and it makes the PR diff 9.6 MB of
binary.

**Fix:** `git rm --cached cmd/conch-sandbox/conch-sandbox`. Add
`/cmd/conch-sandbox/conch-sandbox` to `.gitignore`, the same way `/conch` is
ignored. Since c4b0b80d is the last, unpushed commit, fold the removal into
it (`git commit --amend`) rather than adding a commit on top, so the blob
never reaches the remote.

## 2. `create` pays for a sandbox it already knows it cannot reach — must fix

`cmd/conch-sandbox/verbs.go` `create`: `ready` → `ensureInclude` →
`p.Create` → `access`. `access` returns "…'s file says no ssh way in"
whenever `sshKind(p) == ""`, but only *after* the box exists. So
`create --provider X`, for any provider whose file has no `[ssh]` (15 of 23,
per the design):

- adds the Include line to `~/.ssh/config`,
- creates a billed sandbox and waits up to 10 minutes for it to start,
- then deletes it and fails.

`providers` already lists these with `"ssh": ""`, so a tool can pick one.

**Fix:** right after `ready(*provider)`, refuse when `sshKind(p) == ""`,
before `ensureInclude` and before `p.Create`. Add a test using a fake
provider file without `[ssh]`. It should assert that the fake saw no create
call and that `~/.ssh/config` was not written.

## 3. `start` exits 1 for a provider without ssh, after it has started the box — must fix

`verbs.go` `start`: `p.Start` succeeds, then `access(ctx, p, id)` fails with
"no ssh way in". The command prints an error and exits 1 even though the
sandbox is now running and billing. A tool would report a failed wake while
the box runs.

**Fix:** when `sshKind(p) == ""`, skip `access` and print the result with no
`ssh` field, the way `get` does. Alternatively, refuse before `p.Start` with
the same message `create` gets (after fixing finding 2). Add a test for
whichever you choose.

## 4. `[ssh] max_secs` also changes conch, not only conch-sandbox — should fix or justify

`internal/sandbox/custom.go:1294` fills `{expires_secs}`/`{expires_mins}`
with `max_secs` inside `Custom.SSHAccess`. That is the same call conch's
`remote.TransportFor` makes before every connection.

Once a provider file adds `max_secs` for the aliases (as the brief asks for
Daytona and InstaBox), conch itself starts asking for maximum-life tokens on
every reconnect. Those are tokens it uses for one connection and puts on
ssh's command line or in its generated config. The commit message and
`providerfile.md` say the setting is for conch-sandbox's aliases.

That breaks the brief's "conch must behave exactly as before". It also makes
every transient token conch mints live as long as the provider allows.

**Fix:** make the long life the caller's choice. For example, add an
`sandbox.AccessFor(ctx, id, life time.Duration)` (or a context value that
conch-sandbox sets) and keep `SSHAccess` filling a short default, such as
`min(max_secs, 3600)`. Then `max_secs` reaches the API only from
conch-sandbox. Add a test that conch's path (`SSHAccess`) does not send the
maximum.

## 5. `key set` can be undone by a running conch TUI — should fix or document

`cmd/conch-sandbox/key.go` writes the key with `config.Load` + `config.Save`,
which rewrites the whole of `config.toml`. Two consequences:

- AGENTS.md (`internal/configweb` row) notes that the TUI holds the
  configuration in memory and writes its older copy back on its next save.
  A person running conch who then runs `conch-sandbox key set daytona` loses
  the key the next time they change any setting in the TUI.
- Comments and anything hand-written in `config.toml` are dropped. The
  file's own header says the settings screen does this, but a CLI that sets
  one key is not expected to.

**Fix:** at minimum, say both in `key set --help` and in the message after
saving ("a running conch keeps its own copy; restart it, or set the key in
its settings"). Better: write only `[sandbox.P] api_key` with a targeted
TOML edit. Add a test that a comment in `config.toml` survives `key set`,
or one that states the documented behaviour.

## 6. ssh expands `%` (and `${…}`) in alias values — low

`cmd/conch-sandbox/alias.go` `configValue` refuses quotes and control
characters, which prevents injecting new lines. It does not account for
ssh's own token expansion. With OpenSSH 10.0, `ssh -G` on a block
containing `User a%hb` gives `user aconch-x-1b`. `HostName`, `IdentityFile`
and `UserKnownHostsFile` also expand `%` tokens and `~`. So a token or path
containing `%` or `${` is silently changed into a different credential or
path. That is not an injection, but the login fails in a confusing way.
Today's tokens are alphanumeric, and `CONCH_HOME` rarely contains `%`.

**Fix:** in `configValue`, write `%` as `%%` and refuse `${`. Add a case to
`TestAliasRefusesWhatWouldInjectConfig`.

## 7. Hashed known_hosts lines are never forgotten — low

`alias.go` `forgetHostKeys`/`hostsName` match the alias by plain text. A
person with `HashKnownHosts yes` in effect (it is in Debian's and Ubuntu's
default `/etc/ssh/ssh_config`) gets `|1|…` lines in
`sandboxes_known_hosts`. `rm` then leaves those lines behind, so a later
sandbox given the same alias fails `accept-new` with "REMOTE HOST
IDENTIFICATION HAS CHANGED".

**Fix (simplest):** add `HashKnownHosts no` to every block. The file is
conch-sandbox's own and holds only aliases. Alternatively, run
`ssh-keygen -R ALIAS -f FILE`, which handles hashed lines. Add a test with a
hashed line.

## 8. An `Include` inside a `Host`/`Match` block counts as present — low

`cmd/conch-sandbox/include.go` `includes` accepts an `Include` line anywhere
in the file. If a person has it indented under `Host work-*`, ssh reads the
alias file only for those hosts. `ensureInclude` then reports that all is
well, and no alias works.

**Fix:** count only Includes before the first `Host`/`Match` line, or
inside `Host *` or `Match all`. Add a test for it.

## 9. Smaller points (nits; fix when convenient)

- `aliasName` (`alias.go`): `conch-a-b-c` could be provider `a-b` with id
  `c`, or provider `a` with id `b-c`. One sandbox's alias would then
  overwrite the other's. The brief puts the remedy in M4 (a
  `# conch-sandbox provider=… id=…` comment). Make sure it lands there, and
  have `setAlias` refuse to overwrite a block whose recorded provider/id
  differs.
- `includeLine`/`aliasFile`: if `CONCH_HOME` is a relative path, the
  Include line is relative too, and ssh resolves it against `~/.ssh`.
  Wrap the path in `filepath.Abs`.
- `main_test.go` `TestLinksNoServer` skips when `go` is not on `PATH`. Under
  `CI=true` it should fail instead, as `internal/nodetest` does for node, or
  the guard can quietly stop guarding.
- `internal/sandbox/common.go` `PollFaster` is a test hook exported from
  production code. It is acceptable, but an `export_test.go`-style helper is
  not possible across packages. Keep it and leave the comment saying it is
  for tests only, which it already does.
- The registry's InstaBox file still sends `ttlSeconds = 3600`, and neither
  Daytona's nor InstaBox's file sets `max_secs`. Until those files change
  (in the conch-integrations repo), token aliases last an hour, or
  whatever Daytona's default is. The brief's "longest life" therefore needs
  that follow-up; note it for M4–M6.

## Checked and fine

- **The alias file is `ssh/sandboxes`, not `ssh/config` (the brief's
  location).** The reasoning holds.
  - `internal/remote/ssh.go:115` writes `Include ~/.ssh/config` into conch's
    generated `ssh/config`, and conch rewrites that file whole.
  - If `~/.ssh/config` pointed back at it, the two files would include each
    other: ssh fails on recursive includes, and conch's next start would
    erase the aliases.
  - The divergence is justified and is recorded in AGENTS.md and the usage
    text.
  - Separately, I confirmed with `ssh -G` that an `Include` at the top of
    `~/.ssh/config`, pointing at a file of `Host` blocks, does not leak
    `Host` scope into the global options that follow it.
- **M1 is a pure move.**
  - `sshkey.Authorize` uses `sandbox.As[...]` where the old code used bare
    type assertions. For a file provider, `offers(*KeyAuthorizer)` is
    `key == "authorize"`, which matches the old `authorizesKey` check, so
    behaviour is unchanged.
  - `resetConfig` is still called when conch's key is made.
  - `sandboxes-deleted.json` keeps its path and format.
- **Test isolation.** `TestMain` and `isolate`/`bare` set `HOME` and
  `CONCH_HOME`, clear the session variables and provider keys, and point
  `CONCH_SSH_KEYGEN` at `/bin/false`. The sshkey tests isolate every case.
  Nothing reads the real `~/.ssh` or `~/.config/conch`, and the providers
  are httptest fakes.
- **File modes.** The alias file, known_hosts and `config.toml` are written
  0600 through `WriteFileAtomic`, and the folder is created 0700. An
  existing `~/.ssh/config` keeps its mode and is written through a symlink.
- **Tokens never printed.** They are struck from `--json` and from plain
  output whenever `PlainUser` is false, and are only on disk in the 0600
  alias file.
- **Cleanup on failure.** A failed or cancelled create deletes the box on a
  fresh context (`abandon`), and `Custom.wait` returns the ID on error, so
  the delete has something to act on.
- **Binary size.** It is 6.6 MB stripped, against 23 MB for conch.

VERDICT: FIX
