# Evaluation: Conch.app on Windows and Linux

Status: evaluation (2026-11-15). Nothing of Conch.app is ported; one
small fix for conch under WSL landed alongside (links, below).

The question is the desktop app — Conch.app, the native window in
`macos/` — not the `conch` binary, which already runs on Linux and runs
on Windows inside WSL 2.

## Summary

- **Conch.app cannot be built for Linux or Windows as it is.** Its
  interface (`ConchApp`, ~29k lines of Swift) is AppKit and SwiftUI from
  top to bottom, and neither exists off Apple's platforms. A Linux or
  Windows app is a **new front end**, not a port.
- **What carries over is everything below the interface.** `ConchKit`
  (~16k lines: protocol, socket and process clients, tree, layout,
  search, git view model, windows) imports only Foundation, and the app
  already treats conch as something it talks to over a stream and runs
  in each pane (`conch attach`). That boundary is what makes a second
  front end affordable.
- **Windows needs no native conch.** The app already reaches other
  machines through a process's stdin and stdout (`Connection(command:)`,
  `conch machine bridge`), and `conch bridge` exposes the local server
  the same way — so a Windows app can drive conch inside WSL with
  `wsl.exe -d <distro> conch bridge` and run each pane as `wsl.exe -d
  <distro> conch attach …`. That sidesteps porting ptys, hot reload and
  process groups to Windows, which is the large part.
- **Recommendation:** Linux first, Windows second, both only if there is
  demand Conch.app's extras justify; until then `conch app` (Linux) and
  WSL 2 + Windows Terminal (Windows) are the desktop story, and the docs
  now say so. The first step either way is cheap and informative: build
  and test `ConchKit` on Linux in CI.

## What Conch.app is made of

| Layer | Where | Portable? |
| --- | --- | --- |
| Protocol, client, tree, layout, search, git model, windows | `ConchKit` (Foundation only; `CoreGraphics` in `SVGPath.swift`; `sockaddr_un` socket in `Connection.swift`) | **Mostly.** swift-corelibs-foundation on Linux and Windows; the socket needs Glibc / WinSock spellings, `SVGPath` a geometry type of its own. Untried. |
| Terminal in each pane | `TerminalHost.swift` seam: `GhosttyHost.swift` (libghostty, Metal) or SwiftTerm's AppKit view | **No as written.** See below. |
| Windows, sidebar, tabs, sheets, settings | `ConchApp`: AppKit + SwiftUI (≈50 files each) | **No.** |
| Browser and media panes | `BrowserView`, `MediaView`: WebKit, PDFKit, AVKit | No; WebKitGTK / WebView2 instead |
| System Monitor | `SystemMonitor.swift`: Swift Charts | No; draw it |
| Notifications, Dock badge | `Notifier.swift`: UserNotifications | No; libnotify / Windows toasts |
| Scratch layer hotkey | `Scratch.swift`: Carbon | No; per-desktop global shortcuts (and Wayland mostly forbids them) |
| MCP tokens | `Keychain.swift` | No; libsecret / Windows Credential Manager |
| Self-update | `AppUpdater.swift` + `conch app update` (bundle swap) | No; packaging-specific |
| Server, agents, git, sessions | the `conch` binary | Linux: yes. Windows: through WSL |

### The terminal is the deciding piece

Each pane is a terminal emulator running `conch attach` (Ghostty's
panes take `-raw` and draw the program's own output at the server's
size). Off macOS:

- **libghostty** is what makes the Mac app's panes good, but its
  embedding API is used only by Ghostty's macOS app; Ghostty on Linux is
  a GTK application written in Zig, not a library a Swift or C front end
  embeds, and there is no Windows Ghostty. Embedding it on Linux means
  tracking an unstable, unofficial path.
- **SwiftTerm**'s emulator core builds on Linux, but its views are
  AppKit and UIKit; a GTK or Windows view would be written from scratch.
- **VTE** (GNOME's terminal widget) on Linux and **Windows Terminal's
  control** or **xterm.js** in WebView2 on Windows are the mature
  embeddable terminals. Each would run `conch attach` the non-raw way, so
  the letterboxing and raw-stream work done for Ghostty
  (`ConchKit/Window.swift`, `internal/pane/raw.go`) would be partly
  re-done per terminal.

### Choosing a toolkit

Ruled out: Electron and Tauri — a web front end for the desktop has been
turned down twice.

| Option | Linux | Windows | Reuses ConchKit | Notes |
| --- | --- | --- | --- | --- |
| Swift + GTK 4 (e.g. Adwaita for Swift bindings) + VTE | yes | poor (GTK on Windows is second-class) | **yes, directly** | Closest to the Mac app's code; one language. Bindings are young. |
| Swift + WinUI (via Swift/WinRT) | no | yes | yes | Swift on Windows is official; WinRT bindings are early. |
| Go + GTK 4 (gotk4) + VTE | yes | poor | no — rewrite ConchKit in Go, though much of it mirrors `internal/proto` and `internal/tui` already | One language with conch; no Swift toolchain on Linux. |
| Qt (C++ or Python) + qtermwidget | yes | yes | no | One codebase for both, at the cost of a third language and a full rewrite of ConchKit. |

If both platforms are wanted with one codebase, Qt is the only option
that covers both well, and it shares nothing with the Mac app. If
Linux alone is wanted, Swift + GTK 4 reuses ConchKit and is the cheapest
credible path.

## Linux

The binary is fully supported (released for amd64 and arm64; CI runs the
race suite on Linux). `conch app install` already puts conch in the
desktop's applications as an XDG entry that opens it in ghostty, kitty,
foot or alacritty with conch's own configuration. What Linux lacks is
only what is Conch.app's alone: the git tab's hunk staging and PR
review, System Monitor window, scenes and departure board, ⌘K preview,
several windows, side tabs, the scratch layer.

Plan, if pursued:

1. Build `ConchKit` and run `ConchKitTests` on Linux in CI
   (`swift test` in a swift:6 container). Fix the socket and `SVGPath`
   spots. This alone says how much of the 16k lines really is portable.
2. A GTK 4 shell: sidebar tree, tabs and splits, each pane a VTE running
   `conch attach`, connections local and per machine exactly as the Mac
   app makes them.
3. Then features in the order people use them: git tab, sessions,
   settings; the decorative ones (scenes, board) last or never.
4. Ship as a tarball and `.deb`/`.rpm`; not Flatpak — the app must start
   `conch` and agents outside a sandbox.

## Windows

The `conch` binary does not build for Windows (`GOOS=windows go build`
fails first at `internal/config/omzpreview.go` and
`internal/detect/process.go`; about thirty files use Unix calls), and
porting it is the large job: ConPTY in place of `creack/pty`, no
`syscall.Exec` for hot reload, no foreground process group, a peer pid
for agent scoping (`peer_other.go` returns 0, which would leave every
agent unscoped, silently), no ssh `ControlMaster`. And the agents conch
drives mostly recommend WSL on Windows themselves.

So a Windows Conch.app should run **natively on Windows against conch in
WSL 2**:

- server: `Connection(command: "wsl.exe", args: ["-d", distro, "conch",
  "bridge"])` — the same process transport the Mac app uses for remote
  machines;
- panes: `wsl.exe -d <distro> conch attach <id>` in the embedded
  terminal;
- files, links and paths cross over with `wslpath`; projects should live
  in the distribution's filesystem, since changes made under `/mnt/c`
  from Windows are not seen by inotify.

Until then: Windows Terminal + WSL 2 is the supported setup (now on the
installation page), and links opened by conch under WSL now reach
Windows' browser (this branch: `internal/desktop`, `wslview` or
`rundll32.exe url.dll,FileProtocolHandler`; before, `xdg-open` with no
browser behind it opened nothing).

## Effort, roughly

| Step | Size |
| --- | --- |
| ConchKit on Linux in CI | days |
| Linux GTK app with tree, tabs, splits, panes, sessions | weeks |
| Linux parity with today's Conch.app | months, and a second front end to keep in step on every feature |
| Windows app against WSL | similar to Linux once a toolkit is chosen; less if Qt was chosen for both |
| Native Windows conch | months on the pane and reload core; not recommended |

The ongoing cost matters more than the first build: every Conch.app
feature (the AGENTS.md map lists dozens) would need doing two or three
times. That argues for keeping new desktop features in `ConchKit` and the
server wherever possible, whatever is decided.

## Not tested

Nothing here was built or run on Linux Swift or Windows. The WSL link
choice is tested with a fake host (`internal/desktop/open_test.go`);
end-to-end rows 9.178–9.179 cover the real thing.
