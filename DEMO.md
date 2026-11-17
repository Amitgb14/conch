# Demo: moving from Conductor to conch with the Conductor import

A recorded run of the `import` integration (PR #272, issue #271): someone who
used Conductor installs the Conductor import, lists their Conductor
conversations, picks some, and imports them as Markdown handoffs that an
agent in conch carries on from.

## What was recorded

Everything ran against a **made-up** Conductor, never a real one. The
database is written by `web/scripts/import-demo/fakeconductor.py` into a
scratch HOME. It holds a small shop called *storefront* with conversations in
two Conductor workspaces (`lisbon` on `ada/coupon-codes`, `kyoto` on
`ada/cart-badge`, both real git worktrees of a made-up repository), a
conversation where nothing was said (which the import leaves out), and a
*marketing-site* conversation whose worktree and repository are gone.

| Step | Commands | Screen |
| --- | --- | --- |
| Find the import | `conch integration search conductor`, `conch integration info conductor` | `import-find` |
| Install it | `conch integration install conductor` (answering `y`) | `import-install` |
| See what Conductor has | `conch import conductor -n` | `import-list` |
| Import a chosen few | `conch import conductor -ids …` (3 of 5) | `import-choose` |
| The handoff written | `cat ~/.config/conch/imports/conductor/storefront/add-coupon-codes-to-checkout-0199f1c2.md` | `import-handoff` |
| Carry it on | `conch project add ~/src/storefront`, the `conch new -agent claude …` the import printed, `conch read p1` | `import-carry-on` |

The agent in the last step is `internal/tools/standin` built as `claude`, so
no real agent ran and no model was called. Its pane says so.
`conch integration install` read the real registry (GitHub Pages); nothing
was sent.

## Where the files are

- `web/public/demos/import-conductor.cast` is the whole run as an asciicast v2
  recording (`asciinema play web/public/demos/import-conductor.cast`), about
  70 s. asciinema is not installed here, so `record.py` writes the cast
  itself: each command typed at a prompt, run in a pty of 140×36, its output
  timed as it arrived.
- `web/public/demos/import-conductor/*.txt` holds each step's final screen as
  plain text.
- `web/src/images/import-*.png` holds the same screens as PNGs, drawn by
  `web/scripts/tui-capture/topng.py` so they match the site's other
  terminals. They are registered in `web/src/components/doc-shot.tsx`.
- `web/src/app/docs/from-conductor/page.mdx` has a new section, **Your
  conversations, step by step**, that walks through the PNGs and links the
  cast.

The scratch HOME is shown as `~` in all of them. The handoff's times are in
UTC (`TZ=UTC`), so a re-run gives the same text anywhere.

## How to re-run it

```sh
web/scripts/import-demo/capture.sh
```

It needs go, python3, sqlite3 and Google Chrome (for the PNGs), and takes
about two minutes. It builds conch and the stand-in into
`$TMPDIR/conch-import-demo`, stages the scratch HOME (`stage.sh`), runs every
command in an environment it builds from nothing (`HOME`, `CONCH_HOME` and
`CONCH_SOCKET=/tmp/conch-import-demo.sock`, all scratch), and stops that
scratch server on exit. `TestCaptureScriptsParse` checks that the scripts
parse and keep those guards.

## What changed in conch along the way

The recording exposed three small things in the CLI. Each was fixed with a
test, in its own commit:

- `conch import conductor -n` printed titles and files, but not the ids that
  `-ids` takes (only `-json` had them). It now prints each id, marks
  conversations whose worktree or repository is gone or that were imported
  already, and ends by naming `-ids`.
- `conch import` ended with a command for the *last* conversation written.
  When that conversation's folders were gone, the command was the
  placeholder `-cwd ~/src/REPO`. It now names the newest conversation that
  can still be carried on.
- `conch integration info` printed an empty `What it can do:` line for an
  import, which has no operations.

## Recording it in Conch.app

To show the move in the desktop app instead of the terminal, record it yourself:

```sh
web/scripts/import-demo/app.sh               # or SLOW_SCAN=2 … to keep "Reading…" on screen long enough to film
```

The script does four things:

1. It builds a demo copy of Conch.app from this checkout into
   `$TMPDIR/conch-import-demo-app`, with its own bundle id
   (`dev.conch.importdemo`). That copy keeps its own settings, separate
   from your Conch.app's.
2. It stages the same made-up Conductor data and repository in a scratch
   HOME.
3. It starts a scratch server on `/tmp/conch-import-demo-app.sock` and adds
   *storefront* as a project.
4. It opens the copy behind your windows with `-background YES`, so the
   copy doesn't take your global scratch key.

Then:

1. Click the copy in the Dock.
2. Start ⌘⇧5.
3. Open **Window → Integrations**, choose **Import** in the sidebar, and
   press **Import…** on the Conductor card.
4. In the sheet, the conversations are listed by workspace, and the
   marketing-site one says its worktree and repository are gone. Untick
   some and press **Import**.
5. On the done screen, use **Copy Prompt** and **Show**.

Quit the copy with ⌘Q, or press ctrl-c in the terminal: the script stops
the copy and its server.

Checked on 2026-11-17: the copy launched, its only socket connected to the
scratch server, and nothing connected to `~/.config/conch/conch.sock`.

## What could not be captured

**Conch.app's Integrations → Import card and Import sheet were not
captured.** The safe route was ready: a separately built copy of the app with
its own bundle id, run with `-background YES` and pointed at the scratch
server through `CONCH_SOCKET`, `CONCH_HOME`, `HOME` and `CONCH_BIN`. Two
things blocked it:

- A background app takes no posted keys or clicks, so it has to be driven
  through the Accessibility API. This session is not allowed to use it
  (`AXIsProcessTrusted()` is false).
- Taking a window's picture needs Screen Recording permission, which this
  session also lacks (`CGPreflightScreenCaptureAccess()` is false).

Granting either permission is for you to decide, so I did not ask for it
mid-run. The other route was adding a snapshot mode to the shipping app,
where it draws its own windows to PNGs. That is app code written only for
screenshots, so I left it out. Instead, `app.sh` above sets the app up
for you to record by hand.

Also not shown: a real agent reading the handoff. A real Claude Code would
have called a model, so the demo stops at the agent being started in the
right worktree with the prompt.
