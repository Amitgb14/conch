# Plan: a file explorer for a project's checkout

Status: built (2026-09-24), all five phases. Written from a design pass over
the current code; every file and line referenced below was read on that date,
so check them again before relying on one. Where the build differs from the
design, it says so below under "As built".

## Goal

Browse the files of a project — or of one branch's worktree — from inside
conch, with a tree that reads well: a per-filetype icon (`.tsx` shows a React
icon, `.go` a gopher, `.md` a document), git status beside the name, and a
preview of the selected file. Local and remote machines alike.

The point isn't to be an editor. It is that conch already knows which agent
is working in which worktree, and the answer to "what did it just write?"
currently means leaving conch or reading a diff. An explorer closes that, and
carries one orchestration-specific move no editor has: **press a key on a
file and its path lands in the focused agent's prompt**, on the right machine.

### Non-goals (first version)

- Editing, renaming, deleting, creating files.
- Syntax highlighting. `chroma` is a heavy dependency for a preview pane;
  plain text with line numbers first, and see if it is missed.
- Searching file *contents* (session search exists; a grep view is its own
  feature).
- Image previews, drag-and-drop out of the explorer.

## Where it lives

`Sessions` is the model to copy: a section row under each project
(`kindSessions`, `internal/tui/tree.go:23`) that owns a full view in the
split (`internal/tui/sessions.go`, dispatched from `internal/tui/view.go`
around lines 497–502 and 594–601).

- A **`Files` row** under each project, at the same depth as `Sessions`.
- **`f`** opens it for the current scope from anywhere in the tree. Checked
  against `internal/tui/keys.go`: `f` is unbound. `F` is *taken* (local-files
  patterns), so it can't be that.
- A new `kindFiles` in the `nodeKind` list, its `rowScope` mapping in
  `internal/tui/scope.go`, a `filesView` on the leaf next to `changes`,
  `sessions` and `queue`, and a `m.filesView` pointer on the model the way
  `queueView` is held (`internal/tui/model.go:126`).

**Not** a file tree inside the sidebar. The sidebar is the fleet map —
machines → Workspace → branches → panes — and one `node_modules` would bury
it. The main area has the width to be worth looking at.

### It is bound to a checkout, not a project

A project has N checkouts: the main one plus a worktree per task branch.
So the view's identity is (machine, project, checkout path):

- opened from the project row → the main checkout;
- opened while a **branch** row is selected → that branch's worktree, so
  "browse what the agent on `fix-flaky` actually wrote" is one keypress;
- `w` cycles the project's checkouts, named in the header.

A branch with no worktree falls back to the main checkout and says so.

## What it looks like

```
 Files  conch · fix-flaky-tests          enter open · a → agent · y copy · / find
 ~/.conch/wt/conch-fix-flaky-tests · 3 changed
 ──────────────────────────────────────────┬─────────────────────────────────────
   ▾  cmd/                                 │   queue.go · 472 lines · modified
      ▾  conch/                            │    1  package tui
           main.go                         │    2
   ▾  internal/                            │    3  import (
      ▾  tui/                              │    4    "fmt"
             model.go               M      │    5    "sort"
         ▸   queue.go               M      │    6    "time"
             theme.go                      │    7
             app.tsx                ?      │    8    tea "github.com/charm…
   ▸  web/                                 │    9  )
        AGENTS.md                          │   10
        go.mod                             │   11  // The review queue answers
        Dockerfile                         │   12  // "what needs me now?"…
```

- Header line, then the checkout path (tildified, `m.tildify`), then the body.
- **Lazily expanded indented tree** on the left — the sidebar's own idiom, not
  Miller columns.
- **Preview on the right when the split is ≥ ~100 columns**; below that the
  preview takes the whole body and `enter`/`esc` toggle between them. The
  status bar already steps down by width (`rightNoExtras`,
  `internal/tui/statusbar.go`); this is the same idea.
- Tree column clamped to ~28–45 columns so the preview keeps something.
- Git status is a right-aligned letter in the alphabet the changes view
  already uses (`M`, `A`, `?`) and tints the **name**; the icon keeps its
  language colour, so type and status are both readable.
- Every line through `fit`/`ansi.Truncate`, like the queue's rows.

## The icons

A table in its own file (`internal/tui/fileicons.go`), looked up **by whole
filename first, then extension**: `Dockerfile`, `Makefile`, `go.mod`,
`go.sum`, `package.json`, `.gitignore`, `README.md`, `LICENSE` before `.md`
or `.json` get their turn. Roughly 60–80 entries covers what repositories
actually hold.

### Three modes, because nerd-font glyphs are tofu in a plain terminal

conch runs over ssh into whatever font the user has, so the pretty set can't
be the only set.

| `[ui] icons` | Shows | For |
| --- | --- | --- |
| `nerd` | real devicons: `` ts · `` tsx · `` go · `` rs · `` py · `` md · `` docker | Nerd Font terminals |
| `text` (default) | a coloured two-letter tag: `ts` `tx` `go` `rs` `py` `md` | everywhere, still colourful |
| `off` | nothing | purists, screen readers |

`.ts` and `.tsx` get **different** glyphs and colours (TypeScript blue vs the
React atom) — that per-extension distinction is the whole charm, so the table
is keyed by extension, not by language family.

There is no reliable way to detect a Nerd Font, so the default is `text` and
the switch lives in Settings → Theme, where one keypress shows the difference
live. New field on `UICfg` in `internal/config/config.go` (`icons`), which
means an older `config.toml` without it must still load — it does, empty
string meaning `text`.

### Colours come from the theme, never from a hex table

There are 15 themes (`internal/tui/theme.go`). A fixed devicon palette
clashes with most of them. So each table entry names a **role** — `web`,
`systems`, `script`, `docs`, `config`, `media`, `archive`, `binary` — and
each role maps to a colour the active theme already defines (`work`, `ok`,
`warn`, `err`, `merged`, `muted`, `accent`). Gruvbox then gets a file tree
that looks like Gruvbox.

### Width discipline is the real hazard

Nerd glyphs live in the Private Use Area: uniseg reports width 1, many fonts
draw them wider, and terminals disagree. So the icon lives in a **fixed
two-cell column**, padded by measuring with `ansi.StringWidth`, never by
assuming. A table test asserts every entry measures 1–2 cells and that no
rendered row exceeds `w`. This is the same class of bug as
`373b973 Stop the queue's selected row cutting a glyph in half` — build the
row from its parts, don't slice the drawn line.

## Protocol work

`fs.list` exists but was written for the project picker and **drops every
non-directory** (`internal/server/fs.go:66`), returning only name/git/project.

- `FSListParams.Files bool`; `FSEntry` gains `Dir`, `Size`, `ModTime`,
  `Symlink`, `Status` — all `omitempty`, all optional.
- **New capability `fs.files.v1`.** Without it an older remote server
  silently answers with directories only, and the explorer looks like a
  repository full of empty folders. Clients check `MissingCapabilities` and
  say "this machine's conch is too old to browse files — update it".
- **`fs.read`** for the preview: path, offset, byte cap (256 KiB), returning
  content, `truncated`, `binary`. Capability `fs.read.v1`. A binary file gets
  a card ("binary · 1.2 MB · image/png"), not mojibake.
- **Git status** folded into the listing from `gitx.StatusFiles`
  (`internal/gitx/harvest.go:26`) — one `git status --porcelain` per
  checkout, cached with the project's existing git refresh, not one per
  directory.
- **Path confinement server-side:** every path resolved and required to be
  under the project root or one of its worktrees, the way
  `projectManager.setLocalFiles` rejects absolute and `..` patterns
  (`internal/server/localfiles.go`). `resolvePath` today happily expands `~`
  and walks anywhere; the explorer must not.

### Freshness comes free

`internal/server/watch.go` already watches every worktree and broadcasts
`worktree.changed` (capability `worktree.watch.v1`). The explorer subscribes
to the same event and re-lists the **expanded** directories of the affected
checkout, so a file an agent writes appears without polling. Where the server
gives no watches, fall back to a refresh on `r` and on view focus — don't
poll a directory tree on a timer.

## Keys and mouse

Following the queue and sessions conventions (`esc q left h` back, `j k g G
pgup pgdn`, `enter` in):

| Key | Does |
| --- | --- |
| `enter` / `l` | expand a directory, or open the file's preview |
| `h` | collapse, or jump to the parent |
| `/` | fuzzy filter the loaded tree by name |
| `.` | show hidden files · `i` show gitignored |
| `y` | copy the path (returns a command, per the clipboard test rule) |
| `d` | jump to this file's diff in the changes view (reuses `project.diff`) |
| `e` | open `$EDITOR` on it in a new terminal pane in that checkout |
| `w` | next checkout of the project |
| `r` | refresh |
| **`a`** | **insert the path into the focused agent's pane** |

`a` is the one that earns the feature: browse, press `a`, the path is in
Claude's prompt. Remote-aware — it sends the path **on that machine**, which
is the same reasoning `internal/tui/drop.go` does for uploads.

Mouse throughout, since that is how conch is driven: click to select,
click-again/double-click to expand or open, wheel scroll, click a breadcrumb
segment to jump up. Model it on `queueView.mouse` and register it in
`internal/tui/mouse.go` beside `kindReviewQueue` (line ~155).

## Cost and limits

- Lazy listing per directory on expand, cached per path, invalidated by
  `worktree.changed`, `r`, or a project git refresh.
- The existing 2000-entry cap and `Truncated` flag carry over; a truncated
  directory says so on its last row.
- `.git` and `node_modules` hidden by default (the watcher's
  `git ls-files --directory` trick already knows what git ignores).
- Preview capped at 256 KiB; a bigger file shows its size and offers `e`.
- Remote: every expand is a round trip over the bridged socket, so show
  "reading…" and keep the previous content on screen, as the diff view does.

## Phases

1. **Server**: `fs.list` with files, `fs.read`, path confinement, git status
   in the listing, capabilities. Tests only in `internal/server`.
2. **Icons**: the table, the three modes, theme roles, the width test. Pure
   and testable on its own, no view needed.
3. **View**: tree, expansion, preview, header, responsive layout; the `Files`
   row, `f`, scope, leaf wiring.
4. **Keys and mouse**, including `a` → agent pane and `d` → diff.
5. **Watch integration**, settings row, docs.

## Tests (per AGENTS.md)

- Server: files and directories, symlinks (to a file, to a directory, broken),
  a file that vanishes between list and read, permission-denied, a directory
  over `maxListEntries`, an empty directory, `..` and absolute paths refused,
  binary and zero-byte files, a file over the cap.
- Capability degrade: a fake old server without `fs.files.v1` produces the
  clear message, not an empty tree.
- Icons: every table entry measures 1–2 cells in all three modes and has a
  role that resolves in all 15 themes; unknown extension falls back; a file
  with no extension; `.` and `..`-named files; a name with combining marks
  or an emoji.
- View: renders at 1×1, 20×5, 39, 80 and 200 columns; no rendered line
  exceeds `w`; the selected row survives a re-list; a deep path indents
  without overflowing; the preview/tree toggle below 100 columns.
- Mouse: click, click-again to expand, wheel, breadcrumb click — Amit drives
  this with the mouse, so these are not optional.
- Remote through the fake-ssh hooks; machine offline mid-browse.
- `ui.json` from an older version still loads with no explorer state.

## Docs to update

`helpText` in `internal/tui/overlay.go`, the status-bar hints,
`web/src/app/docs/interface/page.mdx` and `keys/page.mdx`, the settings page
for `[ui] icons`, and a row in `docs/testing/end-to-end.md` for the one thing
fakes can never prove: that `` actually renders as a TypeScript logo in a
real terminal with a real font, on macOS and Linux.

## As built

- **fs.list is confined by Root, not by a flag on the old call.** A listing
  with `Root` set is of a checkout: the root must be a project's folder or
  one of its worktrees, `Path` is relative to it, and every path — symlinks
  resolved — must stay inside. `Files` without `Root` is refused, so the
  project picker's unconfined listing is unchanged. The server code is in
  `internal/server/files.go`.
- **Git status** comes from one `git status` and one `git ls-files --ignored
  --directory` per checkout, kept for two seconds and dropped when the
  watcher announces an edit there, rather than hooked into the project's
  refresh. Deleted files don't appear: the listing is of what is on disk.
- **fs.read refuses anything but a regular file** before opening it (opening
  a fifo blocks), and calls text that is not UTF-8 binary.
- **Icons**: `text` tags are two letters; the nerd glyphs are from the Seti,
  Devicons and Font Awesome ranges. Folders have no tag in `text` mode — the
  `▸`/`▾` marker already says what they are.
- **`a`** picks the running agent whose folder is in the checkout, preferring
  one shown in a split of the current tab, else the one most recently busy,
  and types the path relative to that agent's folder.
- **Not built:** the explorer does not list deleted files, and a folder
  truncated at 2000 entries can't be paged.

