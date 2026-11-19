# Re-review: the builder's answers to REVIEW.md (f5b4223f, fc57a683, 6f65da05)

The history was rewritten; the branch is now a08b0ffc..6f65da05. I read the
commits from the builder's repository and ran the tests at
`git checkout --detach 6f65da05` in this worktree, with
`env -u CONCH_SOCKET -u CONCH_PANE_ID`:

- `go test -race -count=1 ./cmd/conch-sandbox ./internal/config ./internal/sandbox ./internal/sshkey ./internal/remote`: all ok
- `go vet` on those packages and `gofmt -l cmd internal`: clean

M4's own findings (REVIEW-M4.md) are not part of this round and are not
re-judged here.

## Finding by finding

| # | REVIEW.md finding | Verdict |
| --- | --- | --- |
| 1 | Committed binary | **Fixed.** No commit in `20db9964..6f65da05` touches `cmd/conch-sandbox/conch-sandbox` (`git log -- <path>` is empty), and no commit's tree contains it (checked with `ls-tree` on every commit). `.gitignore` now has `/cmd/conch-sandbox/conch-sandbox`. |
| 2, 3 | create/start on a provider with no ssh | **The builder is right; my finding was wrong.** `ProviderFile.problems()` (`internal/sandbox/custom.go`, line 369 at 20db9964 already) refuses a file without `[ssh] host` and `user`. Such a file is never `Known` and never opens, so `sshKind(p) == ""` cannot happen for a provider that loads. `TestNoSSHWayInIsRefusedFirst` shows create and start are refused before any API call, with `~/.ssh/config` untouched. `openProvider` now says what is wrong with the file rather than "unknown provider", which is better. The `sshKind == ""` branches in `access` and `findOrphans` are now dead defensive code, which is harmless. |
| 4 | `max_secs` reaching conch | **Fixed.** `SSHAccess` caps the life at `shortLife` (3600), or `max_secs` if that is lower. Only a context from `sandbox.LongLived` gets the full `max_secs`, and conch-sandbox's single `access()` is the only caller that sets it, so create, start, ssh-alias and Orca all go through it. conch's `remote.TransportFor` passes a plain context, so it is capped. The test covers both. |
| 5 | `key set` rewriting config.toml | **Fixed, and the targeted edit is sound.** See "SetSandboxKey" below. The help and a message after saving both say a running conch must be restarted. |
| 6 | `%` / `${` expansion | **Refusing is the right call, and the builder's claim holds.** In OpenSSH before token expansion reached `User`, `%%` is sent as two characters, so an escape would break on one ssh version or another. `sshLiteral` refuses `%`, `${`, quotes and control characters for host, user, key path and known_hosts path, and the Include's own path is checked before anything is written. A user value is never echoed in the error (`safeShow`). |
| 7 | Hashed known_hosts | **Fixed.** Every block now says `HashKnownHosts no`. `hostsName` also recognises `\|1\|salt\|hash` lines. I checked the HMAC-SHA1 against a line `ssh-keygen -H` wrote for `conch-p-i`, and it matches (the test computes its own HMAC, so this was worth confirming). Hashing the alias with no port is right for `HostKeyAlias`. |
| 8 | Include under `Host`/`Match` | **Fixed.** Only Includes before any `Host`/`Match`, or under exactly `Host *` / `Match all`, count. Comment lines fall through. A conservative miss (for example `Host * !x`) just asks again, which is harmless. |
| 9 | Nits | Relative `CONCH_HOME` is made absolute (`sshPath`). `TestLinksNoServer` now fails under `CI`. The alias collision is guarded in `setAlias`, but see finding 1 below. `PollFaster` is left as it was, which is acceptable. |

## SetSandboxKey (`internal/config/sandboxkey.go`): no bug found

The edit is checked by reading it back:

1. It decodes the old file into `Default()`.
2. It sets the key in that decoded config, giving `want`.
3. It line-edits the text.
4. It decodes the edited text, giving `got`.
5. It writes the edited text only if `reflect.DeepEqual(got, want)`, and otherwise falls back to `Save(want)`.

I worked through the shapes that could fool a line edit. Each one either
round-trips or falls back:

- **Inline table** (`[sandbox]` with `daytona = {…}`) or **dotted key**
  (`sandbox.daytona.api_key`): appending `[sandbox.daytona]` redefines the
  table. Decoding fails, so it falls back to `Save`.
- **Spaced or quoted headers** (`[ sandbox . daytona ]`,
  `[sandbox."daytona"]`): the header isn't matched, the appended table is a
  duplicate, and it falls back.
- **An `api_key = …` line inside a multi-line string** in that table: it
  gets replaced, the string's value changes, `DeepEqual` fails, and it
  falls back.
- **A key containing `"` or `\`:** `strconv.Quote` produces a valid TOML
  basic string.
- **A key with a control character:** Go writes `\x01`, which TOML
  rejects, so it falls back. (Whitespace is already refused before this
  point.)
- **CRLF files:** the header regex's `\s*$` takes the `\r`.
- **A subtable** `[sandbox.daytona.env]`: the scan stops at the next
  header.
- **Keys this build doesn't know** (a newer conch's): the line edit keeps
  them. Only the fallback drops them, as `Load`+`Save` did before.

Two differences from before, neither of them a bug:

- Refusing now happens on a TOML decode error rather than on `Load`'s
  error.
- The fallback saves a plain decode rather than `Load`'s result.

The file mode is 0600 and the folder is created as before.

## 1. The collision guard stops at `setAlias`; `removeAlias` still removes by name — should fix

6f65da05 makes `setAlias` refuse to overwrite `conch-a-b-c` when its meta
line says it belongs to provider `a`'s sandbox `b-c` and the caller is
provider `a-b`'s sandbox `c`. `CheckName` allows `-` in provider names, so
the collision is possible. However, every removal still goes by name
alone:

- `abandon` (`verbs.go:440`) calls `removeAlias(aliasName(p.Name(), id))`.
- `remove` (`rm`) and `ssh-alias --remove --provider P ID` do the same.

That makes the new refusal destructive. `create` for provider `a-b`
reaches `setAlias`, which correctly refuses. `makeSandbox` then calls
`abandon`, which deletes the new box (right) and then removes **provider
`a`'s alias block, and its known_hosts lines** (wrong). The sandbox the
guard protected loses its alias, and herdr or Orca can no longer reach it.
Similarly, `rm --provider a-b c` takes `a`'s alias with it. No test covers
removal under a collision.

**Fix:**
- Give `removeAlias` the owner (provider, id).
- Remove the block, and forget its host keys, only when `blockOwner(name)`
  matches that owner or the block has no meta line (a block written before
  M4).
- Have `--machine`/`--from-herdr` removals pass the owner from
  `aliasOwner`.
- Add a test: an alias block for provider `a`'s sandbox `b-c` exists; a
  create for provider `a-b` (its fake answering with id `c`) is refused;
  `a`'s block and its known_hosts line are still there afterwards. Add the
  same check for `rm --provider a-b c`.

VERDICT: FIX
