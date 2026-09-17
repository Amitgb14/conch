# Plan: drop local files into remote panes

Status: built (2026-09-16), phases 1–5. Tested on busybox in run R10 of
[end-to-end.md](../testing/end-to-end.md), with drops simulated as bracketed
pastes; a drag in a real terminal (9.16) and a password-auth machine (9.14)
are still open.

Where the build differs from the draft below:

- **Limits.** The server doesn't read the TUI's settings, so it enforces the
  size the first chunk announces (`Size`) and a fixed 256 MiB cap; the
  25 MiB default (`upload_max_mb`) is checked in the client.
- **Which pastes are drops.** Besides every word being an existing local
  file, the files must be under the home folder, a temporary folder or
  `/Volumes`. `/etc/hosts` or `/bin/sh` exist on both machines and were more
  likely meant for the remote one.
- **Splitting** treats only ASCII whitespace as separators: macOS names
  screenshots `… at 10.02.11\u202fAM.png` and terminals don't escape U+202F.
- **A trailing space** in the drop (Terminal.app adds one) is kept after the
  pasted paths.
- **Synchronized tabs** (`S`) don't copy a drop to the other panes.
- **Settings** live under Settings → Agents → Remote machines.
- **CLI** is `conch -m MACHINE upload FILE...`, matching the other commands.

## Goal

Drag a screenshot from the Mac (Finder, the desktop, or the floating
screenshot thumbnail) onto conch while a pane on a remote machine (e.g.
`studio`) has focus. The agent running there should get the image, the same
way it would if it ran on the Mac.

Today the drop does nothing useful. The terminal pastes the **local** path
(`/Users/amit/Desktop/Screenshot\ 2026-09-16\ at\ 10.02.11.png`). conch
forwards that text to the remote pane, and the agent there can't open it
because the file isn't on that machine.

### Non-goals (for the first version)

- Pasting a clipboard image (`ctrl+v` in Claude Code reads the *remote*
  clipboard). See [Later](#later).
- Uploading to SSH panes under `CLI → SSH`. Those are local ssh processes,
  and the host has no conch server to receive the file.
- Syncing folders, or copying files back from the remote.
- Local panes. The path already works there, so nothing changes.

## How a drop reaches conch

macOS terminals (Terminal.app, iTerm2, Ghostty, WezTerm, kitty) don't send a
drop event. They type the dropped file's path, escaped for the shell, and
separate several files with spaces. When the app has bracketed paste on
(Bubble Tea turns it on), most terminals send the path as a paste. The TUI
gets a `tea.KeyMsg` with `Paste: true` and forwards it in `forwardKey`
(`internal/tui/input.go`).

What this means for conch:

- The drop goes to the **focused** pane, not the pane under the pointer.
  Since af00a0e, the focused pane is the tab you last clicked.
- The paste is the only signal. conch sees a paste whose text is made up
  entirely of paths to files that exist locally.
- Screenshots dragged from the floating thumbnail live in a temporary folder
  (`/var/folders/…/NSIRD_screencaptureui_*/`). macOS deletes it a few seconds
  later, so conch reads the bytes as soon as the paste arrives.
- Path formats vary by terminal: backslash escapes, single or double quotes,
  and sometimes `file://` URLs. Several files arrive as one paste.

## Design

```
Mac TUI                                   studio conch server
────────                                  ───────────────────
paste "/Users/…/Shot\ 1.png"
 ├─ remote pane? all tokens local files?
 ├─ read bytes (right away)
 ├─ fs.upload chunks ───── ssh bridge ───▶ write uploads/…/Shot 1.png.part
 │                                         rename → Shot 1.png
 │◀──────────────────── remote path ──────
 └─ pane.send_text (paste) "/home/amit/.config/conch/uploads/…/Shot\ 1.png"
```

### 1. Detect (TUI)

In the paste branch of `forwardKey`, or just before calling it, where the
machine is known:

- The pane's machine is remote (`machine.target != ""`), and upload is enabled
  in settings.
- `parseDroppedPaths(text)` splits the paste like a shell would (backslash
  escapes, `'…'`, `"…"`, `file://` URLs with percent-decoding) and returns the
  paths only if **every** token is an absolute path to an existing regular
  file. Otherwise it returns nil.
- nil means the paste goes through unchanged, as it does today. Code, prose,
  or a remote path the user meant literally never triggers an upload. The one
  ambiguous case is a path that exists on both machines. Across Mac and Linux
  this is rare; the setting turns uploads off if it matters.

### 2. Upload (protocol and server)

A new method, `fs.upload`, with capability `fs.upload.v1`:

```go
// FSUploadParams sends one chunk of a file for the server to store under its
// uploads folder. The first chunk has no Upload ID; the server returns one.
type FSUploadParams struct {
    Upload string `json:"upload,omitempty"` // ID from the first chunk's result
    Name   string `json:"name,omitempty"`   // base name, first chunk only
    Data   []byte `json:"data"`             // base64 in JSON
    Final  bool   `json:"final,omitempty"`
}
type FSUploadResult struct {
    Upload string `json:"upload"`
    Path   string `json:"path,omitempty"` // absolute path, set once Final
}
```

- **Chunks of 512 KiB.** The protocol's line limit is 16 MiB
  (`proto.NewConn`), and small chunks keep pane output flowing over the same
  ssh bridge during an upload. Chunks are sent one after another, each
  waiting for its reply.
- **The server picks the place:**
  `$CONCH_HOME/uploads/<yyyy-mm-dd>/<8 random hex>/<name>`. The directories
  are 0700 and the file is 0600. The random directory keeps the original file
  name, which agents show to the user, and still avoids collisions.
- **Name sanitising:** `filepath.Base`, control characters and `/` removed,
  and an empty name, `.` or `..` becomes `file`. The length is capped at 200
  bytes, keeping the extension. The client never sends a directory, so there
  is nothing to traverse.
- **Atomic:** the server writes to `<name>.part` and renames it on `Final`. An
  unknown upload ID, or a chunk after `Final`, is an error.
- **Limits** are checked on the server as well as in the client: 25 MiB per
  file by default. Past the limit, the server deletes the `.part` file and
  returns an error.
- **Abandoned uploads:** the server forgets an upload that hasn't received a
  chunk for 2 minutes and deletes its `.part` file. A closed connection drops
  its uploads.
- **Pruning:** on start-up and once a day, the server deletes upload folders
  older than 7 days.
- **Hot reload:** uploads in progress aren't carried over. The client's next
  chunk fails, and the TUI reports the error. A leftover `.part` file is
  deleted by pruning, so nothing goes into the reload-state file.

Why not `scp` or a second ssh command: machines added with a password
(`internal/remote/password.go`) would ask again, and it would be a separate
code path to fake in tests. The bridge is already connected and authenticated.

### 3. Paste the remote path (TUI)

- The upload runs as a `tea.Cmd` in the background. The status bar shows
  `Uploading Shot 1.png to studio… 40%`, then `Uploaded 2 files to studio`.
- When every file is done, the TUI sends one `pane.send_text` with
  `Paste: true` to the **original pane ID**, even if focus has moved. The
  remote paths are escaped the way the drop was (backslash style by default)
  and separated by spaces.
- Claude Code attaches a pasted image path as `[Image #1]`. Codex, Gemini CLI
  and OpenCode take file paths in the prompt.
- **Failures:** if one file fails, nothing is pasted, and the status bar names
  the file and the reason. If the pane has closed, the TUI says so and pastes
  nothing. A file over the limit is refused before reading, with
  `Shot.png is 40 MB; the limit is 25 MB (settings)`.
- **Old remote server** (missing `fs.upload.v1`): the paste goes through
  unchanged, with a warning: `studio's conch is older — update it to drop
  files`. This uses the existing `MissingCapabilities` pattern.

### 4. Settings

`config.toml`, shown in the settings view:

```toml
[remote]
upload_drops = true      # upload local files pasted into remote panes
upload_max_mb = 25
```

Before adding the table, check `internal/config` for the naming used by
existing sections.

### 5. CLI (optional, small)

`conch upload <machine> <file>…` prints the remote paths. It uses the same
client code. It's useful from scripts, and it lets the end-to-end check run
without a drag.

## Phases

1. **Protocol and server:** `fs.upload`, sanitising, limits, abandonment,
   pruning, and the capability.
2. **Path parsing:** `parseDroppedPaths` with a table of real drops captured
   from each terminal.
3. **TUI flow:** detect, upload, progress, paste, failures, old servers, and
   settings.
4. **Docs:** a help overlay line, the settings page, `web/src/app/docs`
   (`interface/page.mdx`, the remote machines page), CLI usage if phase 5
   ships, and end-to-end rows.

## Tests

Following `AGENTS.md`: no real ssh, only `t.TempDir()` homes, and unset
`CONCH_SOCKET` and `CONCH_PANE_ID`.

**Server (`internal/server`, `startServer`):**
- One-chunk and multi-chunk uploads produce identical bytes, and the file is
  0600 inside a 0700 directory.
- Empty files (a zero-length final chunk).
- The exact size limit is accepted, and one byte over is rejected with the
  `.part` file removed.
- Names: `../../etc/passwd`, `a/b.png`, `..`, empty, control characters,
  a 300-byte name with an extension, and unicode (`Screenshot … at 10.02.png`
  with U+202F).
- An unknown upload ID, a chunk after `Final`, and two uploads of the same
  name running at once.
- A connection closed mid-upload removes the `.part` file. Abandonment runs
  on an injectable clock, with no sleeps.
- Pruning deletes old folders, keeps today's, and ignores a missing
  `uploads/`.
- `fs.upload.v1` is listed in `proto.Capabilities`.

**Parsing (`internal/tui`):**
- Terminal.app, iTerm2 and Ghostty drop formats, and several files.
- Spaces, quotes and backslashes in names, and `file://` with `%20`.
- A mix of one existing and one missing path returns nil. Relative paths,
  directories, plain text, empty input, and a trailing newline are handled.

**TUI flow (`a1FakeClient`, a fake remote machine):**
- A paste into a remote pane uploads and then pastes the remote path, with
  `Paste: true` sent to the original pane after focus has moved.
- The same paste into a local pane is forwarded unchanged.
- Non-path pastes are unchanged. `upload_drops = false` leaves pastes
  unchanged.
- An old server gets the unchanged paste plus a warning.
- An upload error mid-way pastes nothing and shows the file and the reason.
- The pane closes during the upload.
- A file deleted between the paste and the read (the thumbnail case) shows a
  clear error.
- Status-bar progress text fits at 20×5 and in narrow widths.

**Race:** an upload runs while pane output streams over the same client
(`-race`).

## End-to-end rows (fakes can't prove these)

Add to [docs/testing/end-to-end.md](../testing/end-to-end.md):

- Drag a PNG from Finder into a Claude Code pane on a real remote Linux
  machine, in Terminal.app, iTerm2 and Ghostty. Claude shows `[Image #1]`
  and describes the picture.
- Drag straight from the floating screenshot thumbnail. The upload succeeds
  before macOS deletes the temporary file.
- Drop 3 files at once.
- Drop a 20 MB file while an agent streams output. The UI stays responsive.
- Drop into a remote pane on a password-auth machine. There's no second
  password prompt.
- Drop onto an outdated remote server. A warning appears, and the original
  paste goes through.

## Risks and open questions

- **Terminals that don't bracket drops** (some send the path as typed text).
  They arrive as `KeyRunes` bursts, not pastes. The first version only
  handles pastes; the end-to-end check records which terminals need more.
- **Agents outside the checkout:** Claude Code may ask permission to read a
  file under `~/.config/conch/uploads`. If that is noisy, an option could put
  uploads in `<pane cwd>/.conch-uploads/` (ignored in git), or conch's
  existing agent setup could add the uploads folder to allowed directories.
  Decide after the end-to-end check.
- **Base64 over ssh costs about 33% overhead.** That's fine for screenshots;
  larger transfers would want a binary side channel, which is out of scope.

## Later

- **Clipboard images:** a conch key (e.g. `ctrl+b v`) reads the Mac
  clipboard image (via `osascript`/`NSPasteboard`), uploads it with the same
  method, and pastes the path. This covers `cmd+shift+ctrl+4` screenshots
  that never touch disk.
- **SSH panes:** upload through the pane's own ssh connection (for example
  with an `scp` sidecar) for hosts without conch.
