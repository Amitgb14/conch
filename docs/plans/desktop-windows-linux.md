# Evaluation: desktop support on Windows and Linux

Status: evaluation (2026-11-15). One fix landed with it (links on WSL,
below); everything else is a recommendation, not built.

"Desktop" here means three things conch has on macOS today, and the
answer is different for each:

1. **The TUI and server** — the `conch` binary.
2. **`conch app`** — conch in a terminal of its own, configured by conch,
   with an entry in the desktop's applications (`cmd/conch/app.go`,
   `appinstall.go`).
3. **Conch.app** — the native window (`macos/`, Swift).

## Summary

| | Linux | Windows |
| --- | --- | --- |
| TUI + server | **Supported.** Released for amd64/arm64, CI runs the full race suite on Linux. | **Does not build.** Runs under WSL 2 as Linux. |
| `conch app` | **Supported.** XDG desktop entry and icon under `$XDG_DATA_HOME`; ghostty, kitty, foot, alacritty. | Not applicable natively; under WSL 2 with WSLg it works as on Linux, but the entry lands in the WSL distribution, not the Start menu. |
| Native window | **None.** Conch.app is AppKit/SwiftUI. | **None.** |
| Recommended path | Keep `conch app`; consider a GTK window only if users ask for Conch.app's extras. | Document WSL 2 + Windows Terminal; make WSL a first-class Linux (this branch starts that). A native port is a large, separate project. |

## Linux

What was checked:

- `GOOS=linux go build ./...` and `go vet` are clean; CI (`ci.yml`) runs
  `make lint` and `go test -race` on Linux, so the whole server, pane and
  TUI path is exercised there on every change.
- Platform-specific parts all have a Linux side: socket peer credentials
  (`internal/server/peer_linux.go`, `SO_PEERCRED`), worktree watching
  (fsnotify/inotify, one descriptor for the lot, so not `budgeted()`),
  system stats (`internal/sysstat/linux.go`, `/proc`), foreground process
  (`internal/detect`), notifications (`notify-send`), sounds (`paplay`,
  `canberra-gtk-play`, `aplay`), clipboard (OSC 52, then `wl-copy`,
  `xclip`, `xsel`), links (`xdg-open`).
- `conch app install` writes `conch.desktop` and an icon under
  `~/.local/share` and refreshes the menu; it refuses to install when no
  known terminal is found, and looks past `PATH` (snap, flatpak,
  `~/.local/bin`) because a desktop entry runs with the session's `PATH`,
  not the shell's.

Gaps, in order of likely value:

1. **No native window.** Conch.app's extras — the git tab, System
   Monitor window, departure board, ⌘K preview, several windows — are
   macOS-only. The TUI covers the core (tree, tabs, diffs, sessions).
2. **Terminals.** `conch app` knows four terminals. GNOME's and KDE's
   default terminals (gnome-terminal/Console, Konsole) are what most
   desktops have and cannot be started with *only* conch's configuration,
   which is the point of `conch app`; WezTerm can (`--config-file`) and is
   the obvious fifth if anyone asks.
3. **Packaging.** No `.deb`/`.rpm`/AUR/Flatpak; the install script,
   Homebrew and `go install` cover it. A Flatpak would be a poor fit
   anyway: conch has to run the user's agents and shells outside a
   sandbox.
4. **Untested live:** `conch app` on a real GNOME/KDE session, and
   Wayland vs X11 clipboard tools, are only covered by fakes.

### A native Linux window, if wanted

Conch.app splits into `ConchKit` (16k lines, `import Foundation` only,
plus one `CoreGraphics` use in `SVGPath.swift` and a BSD-socket client in
`Connection.swift`) and `ConchApp` (29k lines: AppKit, SwiftUI, WebKit,
UserNotifications, Carbon). So:

- `ConchKit` is close to portable to swift-corelibs-foundation on Linux:
  the socket code uses `socket`/`sockaddr_un`, which Glibc has; the
  `CoreGraphics` file needs a small geometry type of its own. Not tried —
  no Swift toolchain on a Linux host here; that is the first step of any
  port, and cheap.
- `ConchApp` is not portable. A Linux window means a new UI: GTK 4 is the
  natural host because Ghostty's own Linux app is GTK and libghostty would
  give the same raw-pane drawing (`conch attach -raw`) the Mac app uses.
  That is a second front end to maintain feature-for-feature.
- Web technology for the window (Electron, Tauri) has been ruled out
  twice and is not proposed again.

Recommendation: don't start one now. `conch app` + the TUI is the Linux
desktop; revisit when someone asks for a Conch.app-only feature on Linux,
and start by building `ConchKit` and its tests on Linux in CI.

## Windows

What was checked: `GOOS=windows go build ./...` fails at the first
packages that use Unix calls (`internal/config/omzpreview.go`:
`Setsid`, `syscall.Kill`; `internal/detect/process.go`: `TIOCGPGRP`),
and every package above them fails with them. Past those leaves, about
thirty files outside build tags use Unix-only APIs. What they stand for is
what matters:

| Part | Unix mechanism | Windows equivalent | Effort |
| --- | --- | --- | --- |
| Panes (`internal/pane`) | `creack/pty` v1, a `select` read loop | ConPTY (`CreatePseudoConsole`); creack/pty v1 has none | Large: the core of conch |
| Hot reload (`internal/server/reload.go`) | `syscall.Exec` keeping pty and listener fds | No exec-in-place; would need handle inheritance to a child and a handover protocol | Large |
| Detect the foreground program (`internal/detect`) | `TIOCGPGRP` on the pty | None for ConPTY; walk the process tree from the shell | Medium, and less exact |
| Agent scoping (`internal/server/scope.go`) | Socket peer pid (`SO_PEERCRED`/`LOCAL_PEERPID`) | `SIO_AF_UNIX_GETPEERPID` exists on AF_UNIX; `peer_other.go` today returns pid 0 | Medium — and **must** be done: pid 0 means every agent acts unscoped, silently |
| Socket | unix socket | AF_UNIX works on Windows 10 1803+ and in Go | Small |
| Session detach (`Setsid`, process groups, signals) | `Setsid`, `syscall.Kill(-pgid)` | Job objects, `GenerateConsoleCtrlEvent` | Medium |
| Remote machines | OpenSSH with `ControlMaster` | Windows OpenSSH has no connection sharing | Medium: every probe, install and bridge opens a fresh connection |
| Agents themselves | — | Claude Code, Codex and others run natively on Windows only in part; most document WSL | Out of conch's hands |
| Desktop bits | `notify-send`, `xdg-open`, `pbcopy` | toast notifications, `start`, `clip.exe` | Small |
| Native window | AppKit | WinUI/WPF, or libghostty when it gains Windows | Large, and a third front end |

The agents row decides it: the agents conch
drives are themselves most at home in WSL on Windows, and a native conch
would be driving agents that the people using them already run under
WSL. A native port is months of work on the pane and reload core —
exactly the parts with the subtlest bugs on macOS (see "Panes on macOS",
"Hot reload" and "One owner per pty" in AGENTS.md) — for a platform whose
users mostly run the agents elsewhere.

Recommendation: **support Windows through WSL 2, and make WSL a
first-class Linux.** Concretely:

1. *Done here:* links open in Windows' browser under WSL
   (`internal/desktop`): `wslview` when wslu is installed, else
   `rundll32.exe url.dll,FileProtocolHandler`, else `xdg-open`. Before,
   both the TUI and the commands ran `xdg-open`, which on WSL is usually
   there with no browser behind it, so nothing opened.
2. *Docs:* the installation page now says Windows means WSL 2 from
   Windows Terminal.
3. *Next, small:* desktop notifications under WSL (`notify-send` is
   rarely there; a PowerShell toast through `powershell.exe` is), and a
   `conch doctor` check that says when conch is in WSL 1 (no proper ptys
   for some programs, slow file access on `/mnt/c`) or when a project
   lives under `/mnt/c`, where fsnotify sees no changes made from Windows.
4. *Next, medium:* `conch app install` under WSL could write a Windows
   Start-menu shortcut that runs `wt.exe wsl.exe -d <distro> conch`
   (Windows Terminal with a conch profile), the Windows counterpart of
   the XDG entry. Windows Terminal honours OSC 52, so copying already works.
5. *Guard against drift:* if a native Windows build is ever started,
   begin by making `GOOS=windows go build ./...` pass with stubs and add
   it to CI, and fix `peer_other.go` before anything else — an unscoped
   agent is the one failure here that is silent.

Clipboard under WSL was left as it is on purpose: OSC 52 already reaches
Windows Terminal's clipboard, and `clip.exe` mangles anything beyond
ASCII unless it is converted to UTF-16 first.

## Not tested

Nothing here was run on a real Windows machine or under real WSL; the WSL
choices are tested with a fake host (`internal/desktop/open_test.go`).
Row candidates for `docs/testing/end-to-end.md`: a link opened from the
TUI under WSL 2 with and without wslu; `conch app install` on GNOME and
KDE under Wayland and X11.
