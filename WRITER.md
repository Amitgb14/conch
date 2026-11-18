# Step 2 of the Conductor import: the Claude Code session writer

Branch `conch/build-step-2-conductor-import`, from `conch/docs-from-conductor`
(PR #261). Plan: `docs/plans/import.md` in conch-integrations ("Part 2 —
conch"). Not pushed.

## What was built

1. **Raw messages on `Imported`** (`internal/sessions/conductor.go`).
   `Imported.Messages []ImportedMessage{ID, Role, Content, Time}`: every row
   of `session_messages` for the conversation, in order, as stored (a
   user's prompt as typed, or one Agent SDK stream message as JSON). The
   row id is selected too, falling back to sqlite's `rowid` for a table
   without an `id` column. `Turns`, `Unread` and the handoff are unchanged.

2. **`SessionWriter` and `ClaudeWriter`** (`internal/sessions/claudewrite.go`).
   - The interface is the plan's: `Agent()`, `Write(ctx, im, dir) (agentSessionID, path, err)`.
     A second, optional `SessionAppender` adds new messages to a session an
     earlier import wrote.
   - One JSONL file at `$CLAUDE_CONFIG_DIR/projects/<dir>/<new uuid>.jsonl`
     (else `~/.claude/projects`), `dir` the workspace if it is still there,
     else the repository root (`Session.Dir` from the reader). Written to a
     temporary file beside it and renamed, mode 0600: a failed or refused
     write leaves nothing.
   - The plan's table: a `user` text row → `{"type":"user","message":{"role":"user","content":TEXT}}`;
     SDK `assistant` → its `message`, with `requestId` when the SDK gave
     one; SDK `user` (tool results) → its `message` plus `toolUseResult`;
     system, result, progress and unreadable rows → not written.
   - Every line: `uuid` (the SDK's; otherwise one derived from the
     conversation and row id, so it is the same every time), `parentUuid`,
     `sessionId`, `timestamp` (the row's, `…T15:19:01.000Z`), `cwd`,
     `gitBranch`, `isSidechain`, `userType: "external"`, `version`,
     `entrypoint: "conch-import"`; a final `{"type":"ai-title",…}` line.
   - Messages with `parent_tool_use_id` are `isSidechain: true`.
   - Tool calls and results are kept and checked to pair: a result for a
     call not made earlier in its chain is refused (→ Markdown).
   - The written file is read by conch's own `parseClaude` and
     `claudeTranscript`, and lists in `sessions.List` for its folder.

3. **`state.json` and importing again** (`internal/sessions/importstate.go`).
   `$CONCH_HOME/imports/conductor/state.json` is a map of Conductor's
   session id → `{agent, agentSessionID, path, lastMessageID, importedAt, size}`.
   `ImportResumable` applies the rules: new conversation → written; new
   messages → appended only while the file's size is what conch recorded;
   otherwise left alone and reported *carried on in conch*; nothing is
   deleted. `ImportPlan` makes the same decision without writing (for `-n`,
   and later `import.scan`'s `alreadyImported`).

4. **`conch import conductor`** (`cmd/conch/import.go`).
   `-resumable` (default true) writes Claude conversations as Claude Code
   sessions and prints `conch new -agent claude -cwd DIR -- --resume ID`
   for each. `-markdown` also writes today's handoff for every
   conversation; `-resumable=false` writes only the handoffs (today's
   behaviour). A conversation that cannot be written as a session — a
   Codex one, one with nowhere to resume, one whose stream does not pair —
   is written as Markdown and the output says why. `-n` writes nothing
   (and asks the writer whether it *would* write, via `ClaudeWriter.Check`);
   `-json` adds `status`, `why`, `agentName`, `session`, `sessionFile`,
   `added`. A `state.json` that cannot be read stops the import rather than
   being saved over (which would import everything twice).

Docs: CLI usage in `cmd/conch/main.go`, `web/src/app/docs/cli/page.mdx`
(`import`), `web/src/app/docs/from-conductor/page.mdx`, and row 9.204 in
`docs/testing/end-to-end.md`.

## The `claude --resume` proof (open question 2)

`TestClaudeResumesWrittenSession` (`internal/sessions/claudeproof_test.go`)
writes the fixture conversation (prompt, tool call and result, a subagent
Task with its own tool call, answer) into a scratch `CLAUDE_CONFIG_DIR`, then
runs `claude --resume <id> -p "say ok"` in its folder with a scratch `HOME`
and no credentials. It is skipped without `claude` on PATH.

**Result: Claude Code 2.1.326 accepted the file.** It found the session by
id, loaded it, and appended the new prompt to the same file with its
`parentUuid` chain leading back to `u10`, the last line conch wrote — which
is how Claude Code continues a conversation's history. It then stopped with
`Not logged in · Please run /login`, by design. A session Claude Code cannot
find or read gives `No conversation found with session ID: …` instead; I
checked the test fails that way when the file is removed before the run.
No field we lack was needed to load it.

What this does **not** yet prove: that the API accepts the resumed history
when it is sent (thinking blocks' signatures, the imported tool calls). The
test deliberately cannot sign in, so it never calls the model or spends
usage, and is safe in `make gate`. I tried the logged-in run by copying a
written session into the real `~/.claude/projects` (a folder for a scratch
directory, to be removed afterwards); the permission check refused writing
into `~/.claude`, so I did not. To run it yourself:

```sh
conch import conductor            # prints a resume command per conversation
cd <its -cwd> && claude --resume <id> -p "say ok, then: what did we last change?"
```

Row 9.204 in the end-to-end plan covers it with a real Conductor database.

Also found while proving: `claude --resume <id>` finds a session in **any**
project folder, so the folder name matters only for listing (conch's
Sessions view, Claude's own `/resume` picker).

## Deviations from the plan, and why

- **`parentUuid` is the line before *in its chain*, not in the file.** A
  subagent's lines (sidechain) chain among themselves from `null`, and the
  conversation's next line chains past them to its own last line. With one
  chain, Claude Code walking back from the leaf would pull the subagent's
  tool calls into the main conversation, where nothing answers them.
- **Calls that never returned get a result.** A `tool_use` with no
  `tool_result` (Conductor stopped mid-tool) is answered with
  `[Request interrupted by user for tool use]`, `is_error: true`, before
  the next prompt or at the end, as Claude Code answers a call it was
  stopped in; the API refuses a call with no result. When the real result
  turns up on a later import it is dropped rather than answering twice.
- **`version`** is the `claude_code_version` in the conversation's own
  `system`/`init` message, else 2.1.201. "The version found" could have
  meant running `claude --version`, but a writer that runs the agent would
  run the real agent in tests; the stream already records it.
- **`requestId`** is copied only when the SDK message has `request_id`;
  the plan lists it but Conductor's rows don't always carry one.
- **`ImportedMessage.Content` is a string**, not `json.RawMessage`: a
  user's row is plain text, not JSON.
- **`state.json` is the plan's map as-is**, no version wrapper. "State from
  an older conch loads" is tested as a file with fewer fields and an
  unknown one; a missing or empty file is an empty state; a broken one is
  an error.
- **A session removed since import** is reported as left alone (not
  rewritten), under "carried on", since the plan says nothing is ever
  deleted and a removal is the person's choice.
- **`-markdown` means "also"**, matching `import.run`'s `markdown` ("also
  save a readable copy"); `-resumable=false` is today's Markdown-only
  import.
- **Claude's project folder name** (`claudeDirName`, also used by the
  reader) counted bytes; Claude Code (JavaScript) replaces each UTF-16 unit,
  so `é` is one dash and an emoji two. Fixed for reader and writer alike —
  folders with such names never listed before. A name over 200 characters,
  which Claude cuts and suffixes with a hash conch cannot reproduce: the
  writer reuses Claude's existing folder for that path if there is one,
  else the cut name plus a hash of conch's; the reader now matches by the
  cut too.
- **The proof test runs without signing in** (see above): no model call,
  no usage, which keeps it within AGENTS.md's "no real agents or model
  calls" while still running Claude Code's own loader.

Not done (out of scope): the Codex writer, `import.scan`/`import.run`,
Conch.app screens. Appending after the workspace was removed in between
writes the new lines with the repository as `cwd`; Claude Code reads `cwd`
per line, so this is harmless but noted.

## Tests

Run on macOS arm64, claude 2.1.326 on PATH, sqlite3 present.

- `make lint` (gofmt, go vet, staticcheck): clean.
- `go test -race -count=1 -timeout 20m ./...`: **all 39 packages ok**, exit 0
  (`cmd/conch` 210.4s, `internal/sessions` 16.4s).
- The tests for this change, `go test -count=1 -v` (the import, writer,
  reader and Claude-reader tests in `internal/sessions`; the import tests
  in `cmd/conch`):

```
--- PASS: TestClaudeChatKeepsToolsAndResults (0.00s)
    claudeproof_test.go:105: claude 2.1.326 (Claude Code) resumed $TMPDIR/TestClaudeResumesWrittenSession506228970/001/claude-config/projects/-private-var-folders-fh-1fv3rys94q50ctrpv6ts8q580000gn-T-proof2784077757/45354af9-2ec7-4556-b64b-dc20f3f2038b.jsonl; it said: Not logged in · Please run /login
--- PASS: TestClaudeResumesWrittenSession (0.24s)
--- PASS: TestClaudeWriter (0.00s)
--- PASS: TestClaudeWriterReadsBack (0.23s)
--- PASS: TestClaudeWriterFromConductor (0.24s)
--- PASS: TestClaudeWriterInterrupted (0.00s)
--- PASS: TestClaudeWriterRefuses (0.00s)
--- PASS: TestClaudeWriterDefaultsAndOddRows (0.00s)
--- PASS: TestClaudeProjectDir (0.00s)
--- PASS: TestClaudeWriterLongPath (0.21s)
--- PASS: TestReadConductor (0.01s)
--- PASS: TestReadConductorOlderDatabase (0.01s)
--- PASS: TestReadConductorWAL (0.04s)
--- PASS: TestReadConductorErrors (0.01s)
--- PASS: TestConductorMessage (0.00s)
--- PASS: TestConductorDB (0.00s)
--- PASS: TestReadConductorMessagesWithoutIDs (0.02s)
--- PASS: TestImportAgain (0.00s)
--- PASS: TestImportAgainAfterAnOpenCall (0.00s)
--- PASS: TestImportResumableRefusals (0.00s)
--- PASS: TestImportStateFiles (0.00s)
--- PASS: TestImportPlan (0.00s)
--- PASS: TestA6ClaudeParsing (0.00s)
--- PASS: TestA6ClaudeOverlongAndTail (0.01s)
--- PASS: TestA6ClaudeDirName (0.00s)
--- PASS: TestClaudeCostInTheTail (0.01s)
--- PASS: TestClaudeTranscript (0.00s)
ok  	github.com/Amitgb14/conch/internal/sessions	1.295s
--- PASS: TestImportConductor (0.03s)
--- PASS: TestImportConductorNewMessages (0.02s)
--- PASS: TestImportConductorMarkdown (0.02s)
--- PASS: TestImportConductorDryRunAndJSON (0.02s)
--- PASS: TestImportConductorDefaultOut (0.01s)
--- PASS: TestImportConductorBrokenState (0.01s)
--- PASS: TestImportErrors (0.00s)
--- PASS: TestSlugify (0.00s)
--- PASS: TestShellWord (0.00s)
ok  	github.com/Amitgb14/conch/cmd/conch	0.474s
```

Every test sets `HOME`, `CONCH_HOME`, `CLAUDE_CONFIG_DIR` and `CODEX_HOME`
to a `t.TempDir()` (`importEnv`, `importFixture`). No test was skipped here;
in CI, without `claude`, `TestClaudeResumesWrittenSession` skips, and
without `sqlite3` the database tests do. No bug tests were left skipped.

Not tested: a real Conductor database (only fixtures shaped like the plan's
description); a logged-in resume that sends the imported history to the
model (row 9.204).
