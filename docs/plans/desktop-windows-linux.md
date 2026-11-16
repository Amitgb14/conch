# Plan: Conch desktop app on Linux and Windows

Status: plan and design (2026-11-15). Target: the next release.
Phases 0 and 1 are built (the bridge is `internal/desktop` and `conch
desktop`); the Linux and Windows apps are being started on branches of
their own. Decided: each OS gets its own native app; Electron and Tauri
stay out. Open decisions are listed at the end.

## Goal

What Conch.app is on the Mac — a native window onto conch's server, with
the tree, tabs and splits, waiting agents, the git tab and sessions — on
Linux and on Windows, without writing conch's logic three times.

## Design: three layers

```
 ┌────────────────┐  ┌──────────────────┐  ┌───────────────────┐
 │ Conch.app      │  │ conch for Linux  │  │ conch for Windows │   1. UI, one per OS
 │ Swift, AppKit  │  │ GTK 4 + VTE      │  │ WinUI 3 + terminal│      draws, sends intents
 └───────┬────────┘  └────────┬─────────┘  └─────────┬─────────┘
         │ stdio, JSON lines  │                      │ wsl.exe -d D …
 ┌───────▼────────────────────▼──────────────────────▼─────────┐
 │ conch desktop  (Go, inside the conch binary)                 │   2. bridge, written once
 │ machines · tree · counts · layout · notify · capabilities   │      decides
 └───────┬──────────────────────────────────────────┬──────────┘
         │ conch protocol (unix socket / ssh bridge)│
 ┌───────▼────────┐                       ┌─────────▼─────────┐
 │ conch server   │  this computer        │ conch server      │   3. servers, unchanged
 │ (WSL on Win.)  │                       │ on each machine   │      own the panes
 └────────────────┘                       └───────────────────┘

 Each pane: the UI's terminal widget runs the command the bridge names,
 e.g. `conch attach -raw machine|p3` (or `wsl.exe -d D conch attach …`).
```

### Layer 3: conch server

No change. It owns the pseudo-terminals, so agents keep running when a
window closes or the app updates — the same split Herdr (herdr-core) and
T3 Code (its Node server) make. On Windows it runs in WSL 2 to begin
with (phase 3); a native Windows server is phase 5, optional.

### Layer 2: the bridge, `conch desktop`

A subcommand of the `conch` binary the app starts and talks to over its
own stdin and stdout, one bridge per window process. Written once, in Go,
tested by the race suite on Linux and macOS like the rest of conch.

Why stdio: no port, no token, nothing listening; it crosses into WSL as
`wsl.exe -d <distro> conch desktop` and into a remote host as ssh does,
which is how Conch.app already reaches machines (`Connection(command:)`,
`conch machine bridge`). Why Go: what it does is what `internal/fleet`
and `internal/phone` already do for the phone gateway — a client per
machine reached in the background, a machine that cannot be reached
carrying why, panes named `machine:pane`, each machine's capabilities
checked against its own handshake.

**It owns** (moved out of the UIs, so none of them writes it):

- the machine list and connections (`internal/fleet`)
- the tree: machines → projects → branches → agents and terminals, with
  counts and agent states
- search, and the ⌘K palette's results
- the tab/split layout of each window, and which window holds a pane
  (a pane is shown in one place, as in the TUI and Conch.app)
- when to notify (the UI only delivers it)
- capability checks, said as text the UI shows as it is
- how to attach to a pane: the command line a terminal widget runs

**It does not carry terminal bytes.** Each pane's terminal runs its own
`conch attach` (`-raw` where the widget can take the program's own
output, `-view` for previews), as Conch.app does today. A busy pane
cannot slow the tree, and the attach path is already proven.

#### Messages

Newline-delimited JSON, the same framing as conch's protocol. The bridge
sends state; the UI sends intents and never edits state itself.

| Direction | Message | What |
| --- | --- | --- |
| UI → bridge | `hello` | UI name, version, OS, what its terminal can do (`raw`, `images`) |
| bridge → UI | `hello` | protocol version, capabilities, `conch` version |
| bridge → UI | `snapshot` | the whole state: machines, tree, agents, windows and layout |
| bridge → UI | `patch` | changes since the last, by id (`set`, `remove`), with a sequence number |
| bridge → UI | `notify` | an agent waiting, done or failed: title, body, the pane to show |
| bridge → UI | `attach` | the argv a pane's terminal runs, and its size rules (letterbox or not) |
| UI → bridge | `intent` | `pane.new`, `pane.close`, `pane.show`, `split`, `tab.move`, `agent.answer`, `prompt.send`, `branch.new`, `task.start`, … each with an id |
| bridge → UI | `result` | the intent's id, ok or an error to show |
| UI → bridge | `ask` | a one-off read: diff of a branch, sessions, file list, system stats |

Rules carried over from conch's protocol: every field after the first
release is optional; a new message or intent comes with a capability
string; an older UI ignores what it does not know, and a newer UI asks
`hello` before using something. A Go test pins every message's shape
(as `internal/phone/api.go`'s test does), and the shapes reuse the
phone's types (`Agent`, `Pane`, `Question`, `Project`) where they fit.

### Layer 1: a native app per OS

Each app is a renderer of the bridge's state plus the parts that only an
OS can do: windows, menus, keys, notifications, secrets, its terminal
widget, its installer.

| | macOS | Linux | Windows |
| --- | --- | --- | --- |
| Language, toolkit | Swift, AppKit + SwiftUI (exists) | Rust, GTK 4 + libadwaita (gtk4-rs) | C#, WinUI 3 (Windows App SDK, .NET 8) |
| Pane terminal | libghostty, SwiftTerm fallback | VTE 4 (vte4-rs) | Windows Terminal's control (`Microsoft.Terminal.Control` via a wrapper); libghostty-vt as the alternative |
| Notifications | UserNotifications | libnotify / portal | app notifications (toasts) |
| Secrets | Keychain | libsecret | Credential Manager |
| Browser pane | WebKit | WebKitGTK 6 | WebView2 |
| Global hotkey (scratch) | Carbon | GlobalShortcuts portal where offered | `RegisterHotKey` |

Settings need no UI of their own on any OS: the existing settings page
(`internal/configweb`) opens in the browser pane.

Each terminal widget sits behind one interface (Conch.app's
`TerminalHost.swift` is the model), so a better widget can replace it —
on Windows especially, where no embeddable terminal is official.

### Windows: WSL first, native later

- **Phase 3 (WSL):** the app is a native Windows program; the bridge
  and server run in a WSL 2 distribution. The app starts
  `wsl.exe -d <distro> conch desktop`, and the bridge's `attach` argv is
  `wsl.exe -d <distro> conch attach …`. Paths cross with `wslpath`.
  Projects should live in the distribution's filesystem: changes made
  from Windows under `/mnt/c` are not seen by inotify. T3 Code ships a
  WSL backend the same way.
- **Phase 5 (native, optional):** a Windows `conch` server on ConPTY
  (charmbracelet `x/xpty`), Job Objects for process groups, the peer pid
  for agent scoping (`SIO_AF_UNIX_GETPEERPID`; `peer_other.go`'s 0 would
  leave every agent unscoped), and **no hot reload** — Herdr ships
  Windows without its live handoff too. Then a distribution is one more
  machine, not a requirement.

## Packaging and updates

- **Linux:** a tarball and the existing install script (it already adds
  `conch app` to the desktop), then `.deb`, `.rpm` and AppImage. Not
  Flatpak: its sandbox fights starting agents and shells.
- **Windows:** Herdr's pattern — a PowerShell installer
  (`irm …/install.ps1 | iex`) into `%LOCALAPPDATA%\Programs\conch`, a
  Start-menu entry, no admin rights; it checks WSL 2 and installs
  `conch` in the chosen distribution. Signed with Azure Artifact Signing
  in CI. winget and an MSIX later.
- **Updates:** the app downloads its new version beside the running one
  and switches on next launch; the server keeps the panes (already how
  conch updates). The bridge comes with `conch`, so a server update
  updates it.

## Testing

- **Bridge:** Go tests with real servers (`startServer`), fake machines
  (`sshtest`), and the contract test pinning every message. Recorded
  sessions (`snapshot`, `patch`, `notify` sequences) are kept as
  fixtures.
- **Each app:** its own unit tests against a fake bridge that replays
  those fixtures — `cargo test` on a Linux runner, `dotnet test` on a
  Windows runner — so no UI test needs a server, an agent or WSL.
- **End to end:** rows in `docs/testing/end-to-end.md` for each app on
  a real desktop (GNOME and KDE, Wayland and X11; Windows 11 with WSL 2);
  9.190–9.191 already cover phase 0.

## Phases

| # | What | Done when |
| --- | --- | --- |
| 0 | This plan; links under WSL open in Windows' browser (`internal/desktop`) | merged with this branch |
| 1 | `conch desktop`: hello, snapshot, patch, notify, and the intents pane.new, pane.close and pane.attach, over `internal/fleet`; contract test (`TestBridgeContract`). **Built.** Tab and layout intents wait on open decision 3 | a script can watch the tree change and open panes through it |
| 2 | Linux app MVP: tree, tabs and splits, VTE panes, answering a waiting agent, notifications, install | daily use on Linux without the TUI |
| 3 | Windows app MVP against WSL: the same, with the installer | the same on Windows 11 |
| 4 | Parity in order of use: git tab (hunks, commits, reviews), sessions, tasks, System Monitor; scenes last or never | each feature moved into the bridge first, then drawn |
| 5 | Native Windows server (optional) | Windows without WSL, minus hot reload |
| 6 | Conch.app moves its logic into the bridge, a piece at a time (tree, counts, search, layout) | ConchKit only draws and connects |

## Risks

- **Three front ends to keep in step.** Every Conch.app feature costs
  up to three times its UI. The bridge keeps the logic single; the
  drawing is still triple. Mitigation: features land in the bridge first,
  and the Linux and Windows apps may lag on decorative ones.
- **Windows terminal widget.** Windows Terminal's control is not a
  supported public component. Keep it behind the seam; libghostty-vt
  plus a renderer of our own is the fallback (Herdr draws libghostty-vt
  with GPUI on all three platforms).
- **WSL limits.** Startup cost of `wsl.exe`, `/mnt/c` without file
  events, the distribution chosen by the person. Say each in `conch
  doctor` rather than fail quietly.
- **Layout moving into the bridge** changes where Conch.app's windows
  are saved (its preferences today). Phase 6 has to load the old place.

## Open decisions

1. **Linux toolkit:** Rust + GTK 4 + VTE (recommended: the most mature
   bindings, the native look on GNOME) — or one Rust/GPUI app with
   libghostty-vt for Linux *and* Windows, as Herdr does, which halves the
   UI work but rests on GPUI before 1.0.
2. **Windows terminal widget:** Windows Terminal's control (mature
   rendering, unofficial packaging) or libghostty-vt with our own
   renderer (same emulator as the Mac, more work).
3. **Does the bridge own the layout?** Recommended yes, so the three apps
   agree; the cost is phase 6's migration on the Mac.
4. **Native Windows server (phase 5):** do it, or stay WSL-only.

## Where things stand today

- **Linux:** the `conch` binary is fully supported (released for amd64
  and arm64, race suite in CI), and `conch app install` already puts
  conch in the desktop's applications in ghostty, kitty, foot or
  alacritty. Only Conch.app's own features are missing.
- **Windows:** `GOOS=windows go build ./...` fails at the first Unix
  calls (`internal/config/omzpreview.go`, `internal/detect/process.go`);
  about thirty files use them. conch runs under WSL 2 from Windows
  Terminal, which the installation page now says.
- **Conch.app:** `ConchKit` (~16k lines) imports only Foundation and
  would mostly build off the Mac; `ConchApp` (~29k lines) is AppKit and
  SwiftUI throughout and cannot. The bridge takes over ConchKit's job
  for the other two apps rather than porting it.

## Prior art read for this

- Herdr: the app is Rust on GPUI with libghostty-vt for terminals, one
  codebase for all three OSes, over herdr-core (Rust, ConPTY on
  Windows, protocol over stdio with `hc connect`); Windows installed by a
  PowerShell script, signed with Azure Artifact Signing, no live handoff
  on Windows.
- T3 Code: Electron over a Node server (`node-pty`, ConPTY); a WSL
  backend that copies the server into the distribution and talks to it
  on loopback; NSIS installer, Azure Trusted Signing, `electron-updater`,
  winget; AppImage and `.deb` on Linux.
