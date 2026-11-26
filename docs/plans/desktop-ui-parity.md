# Conch for Linux and Windows — UI parity spec

2026-11-25 · status: proposed

## Summary

The Linux and Windows app (`desktop/`, Electron) must look and behave like Conch.app: same window layout, same tree, same tabs and splits, same views, same words. Someone who moves between a Mac and a Linux or Windows machine should not have to relearn anything except Ctrl in place of ⌘.

- **Shared:** the Go server and protocol (unchanged), and conch-ui (`ui/`), which already draws the inspector, Changes and Agents overview in both apps.
- **Per platform:** the window frame, menus, the terminal (Ghostty on the Mac, xterm.js on Linux and Windows) and how the server is reached (WSL on Windows).
- **Today:** the Electron preview has the tree, Needs you, one pane per tab and the Agents overview. It has no splits, no ⌘K, no status-bar controls, no Changes view and no settings. This spec closes that gap.

Not in scope: Mobile Preview (it needs Xcode, so it stays Mac-only), Conch.app's Swift code (unchanged), and any server change.

## Principles

1. **Conch.app is the reference.** When the two disagree, the Electron app changes. A capture of Conch.app (the docs' `desktop-*.png`, or `Conch --render-*` captures) is the spec for each screen.
2. **Same layout, same order.** Title band, sidebar, tab bar, splits, status bar sit where they do on the Mac, with the same sizes (sidebar 262 pt, tab bar 40, status bar 26) and the same density.
3. **Same words.** Menu items, buttons, pills, empty states and errors use Conch.app's text, copied rather than paraphrased. A string that exists in both is defined once in conch-ui.
4. **Same keys, Ctrl for ⌘.** Every ⌘ shortcut is Ctrl on Linux and Windows. Where Ctrl would collide with the terminal (Ctrl+C, Ctrl+D), the app's shortcut takes Ctrl+Shift, as Linux terminals do. The prefix keys (`ctrl+b`) behave as in the Mac app.
5. **Same theme engine.** Themes, styles (Standard, Terminal) and text sizes come from `config.toml [ui]`/`[app]` and the same palettes (`Themes.swift` today, moved to shared data).
6. **Rich views are shared, not ported.** Anything that is not the window frame or a terminal is a conch-ui view, drawn by both apps. A view moves from SwiftUI to conch-ui only when the Electron app needs it, and the Mac app switches to it at the same time.
7. **The server decides.** Neither app keeps state the other cannot see: layout, sizes and questions go through the protocol as they do today.

## Conch.app's interface, area by area

What the Electron app reproduces, and where each area lives in `macos/Sources/ConchApp`.

| Area | What it shows | Where |
| --- | --- | --- |
| Title band | Workspace pills (`1`, `+`), the search field "Search agents, branches, files, actions ⌘K", counts: *N waiting*, *N done*, *N to review* (review popover) | `Views.swift` WorkspacesControl, SearchField, Counts |
| Sidebar · Needs you | A card per waiting agent: mark, name, *waiting* pill, question text, place, choice buttons (Allow / Always / Deny…) or *Open to Answer* | NeedsYouCard, ChoiceButtons |
| Sidebar · tree | Machines → Workspace → project → Agents, Branches, Terminals, Files, Sessions, with counts and state pills; right-click row menus; folders; drag a pane onto a tab | Sidebar, NodeView, RowMenu, Folders |
| Tab bar | Numbered tabs, agent mark or icon, title, close, `+`, scope at the right ("ledger · local"); tabs on the left or right as an option | TabBar, TabButton, SideTabs |
| Splits | tmux-like splits with draggable gaps, zoom, a pane card per leaf | SplitArea, SplitStack, PaneCard |
| Pane card | Header: mark, agent, branch chip, state pill (*working · 16s*), ports, pane id chip; focus border in the accent; find bar; answer strip; letterbox note | PaneCard, FindBar, AnswerStrip, LetterboxNote |
| Status bar | Mode pill (PANE / TREE), key hints, Usage, Ask, system monitor sparkline, phone, Settings, version | StatusBar, UsageChip, MonitorIcon |
| ⌘K palette | Waiting agents first, panes, projects and branches, commands with their keys | Palette, PaletteRow |
| Tabs of other kinds | Changes (Overview / Changes / Graph), Commits graph, Files, Sessions, Agent setup, Browser, Workbench, Task page, Chat, Agents overview | GitTab, CommitsView, FilesView, SessionsView, SetupView, BrowserView, WorkbenchView, TaskPage, ChatView, AgentsTab |
| Side layers | Inspector column (⌃⌘I), scratch layer (⌥Space) | InspectorPanel, Scratch |
| Sheets | New Task, Start Agent, Broadcast, Add Project, Move Project, Move Agent, Switch Agent, New Sandbox, Welcome, Shortcuts, Doctor, Updates | TaskSheet, AddProjectSheet, … |
| Windows | Settings, System Monitor, Tickets, Integrations, more app windows (⌥⌘N) | SettingsWindow, SystemMonitor, TicketsWindow, IntegrationsWindow |
| Look | Themes (Conch, Dracula, Nord, … and user themes), styles (Standard, Terminal, Cel, Ink, Meadow), text and terminal sizes, background scenes | Themes.swift, AppStyle, SceneView |
| Menus | conch, File, Edit, Go, View, Develop, Window, Help; about 60 items with keys | main.swift |

## Parity matrix

P0 makes the app read as Conch.app at a glance; P1 is what people use daily; P2 rounds it out. "Electron today" is as of this spec.

| Conch.app | Electron today | What to build | Priority |
| --- | --- | --- | --- |
| Title band: workspaces, search field, counts | Partial | Counts live in the status bar today. Move them up; add workspace pills and the search field that opens Ctrl+K | P0 |
| Sidebar tree (machines → workspace → project → Agents / Branches / Terminals / Files / Sessions) | Partial | One flat level today. Build the full tree as conch-ui's `Tree` view, with counts, folding, the keyboard (↑↓ ⏎ t c n) and Conch.app's icons | P0 |
| Needs you cards with answer buttons | Partial | Show the question's choices and answer from the card, as the Mac app does | P0 |
| Splits (⌘D / ⇧⌘D, drag gaps, zoom) | Missing | Port `Layout.swift`'s split tree to shared code; the same keys with Ctrl | P0 |
| Tab bar (numbers, marks, scope at the right, tabs left or right) | Partial | Add numbers, marks, the scope label and Ctrl+1–9 | P0 |
| Pane card header (mark, branch chip, state pill, ports, focus border) | Partial | Draw the same header over each xterm | P0 |
| ⌘K palette | Missing | A conch-ui `Palette` view fed the same items (`paletteItems`) | P0 |
| Agents overview | Done | The same conch-ui view. Add answering from cards | P1 |
| Status bar (mode, hints, Usage, Ask, monitor, Settings, version) | Missing | Build it as conch-ui's `StatusBar`, used by both apps later | P0 |
| Changes tab (Overview / Changes / Graph) | Missing | Changes is conch-ui already; move its state builder from Swift to shared code (Architecture) | P1 |
| Inspector column | Missing | Same: the view exists, the builder moves | P1 |
| Files, Sessions, Agent setup | Missing | New conch-ui views, then the Mac app moves to them | P1 |
| Settings window | Missing | Reuse the settings page (`internal/configweb`) in a window | P1 |
| Browser tabs, Workbench | Missing | Electron's own `WebContentsView` for pages; Workbench after splits | P2 |
| Scratch layer (⌥Space) | Missing | An overlay terminal; Alt+Space on Linux, Ctrl+` on Windows | P2 |
| Themes, styles, text sizes | Missing | Read `[ui]` / `[app]`; palettes moved to shared JSON | P1 |
| Remote machines and sandboxes | Missing | One `conch machine bridge ID` per machine in machines.json, as Conch.app does | P2 |
| System Monitor, Tickets, Integrations windows | Missing | New conch-ui views, one window each | P2 |

## Platform adaptations

The only places Linux and Windows may differ from the Mac.

| Topic | Conch.app | Linux and Windows |
| --- | --- | --- |
| Modifier | ⌘ | Ctrl; Ctrl+Shift where Ctrl belongs to the terminal (copy Ctrl+Shift+C, paste Ctrl+Shift+V, new tab Ctrl+Shift+T, close Ctrl+Shift+W) |
| Shortcut table | menus in `main.swift` | One shared table (id, title, Mac key, PC key) generated into both apps; `paletteCommands` already has ids and titles |
| Window frame | Native title bar with traffic lights, toolbar in the title band | Frameless window with the title band drawn by the app and the system's window buttons: right on Windows (`titleBarOverlay`), left or right per the desktop on Linux |
| Menus | Menu bar | Hidden menu bar; the same items in a ☰ menu at the left of the title band, and all of them in Ctrl+K |
| Fonts | SF Pro for the window, the user's terminal font | Inter for the window (bundled), the configured terminal font, else JetBrains Mono (bundled) |
| Terminal | Ghostty (Metal) or SwiftTerm, `conch attach -raw` | xterm.js with the WebGL renderer on the visible panes (at most 16 contexts), the DOM renderer on the rest |
| Server | Local socket, `conch machine bridge` for remotes | `conch bridge`; on Windows `wsl.exe -e sh -lc 'exec "$0" bridge' conch`, `CONCH_WSL_DISTRO` to pick one |
| Paths shown | macOS paths | WSL paths stay Linux paths (`/home/…`); "Open in Explorer" uses `\\wsl$\<distro>\…` |
| Notifications and badge | Notification Center, Dock badge | Electron `Notification`, the taskbar overlay icon (Windows) and Unity count (Linux) |
| Install and update | DMG, updater | AppImage, deb and tar.gz; a Windows installer (NSIS) and zip; update by checking GitHub releases, as Conch.app does |

## Architecture

```
                     conch server (Go) · unchanged
                 socket, machine bridge │ conch bridge (WSL)
              ┌─────────────────────────┴─────────────────────────┐
   Conch.app · Swift (macOS)                     Conch · Electron (Linux, Windows)
   window, menus, sidebar, splits (SwiftUI)      window, menus, splits (renderer)
   terminals: Ghostty, conch attach -raw         terminals: xterm.js on pane.raw
   web views: ConchUIHost (WKWebView)            web views: the renderer, a preload bridge
              ▲                                                   ▲
              └──────────── draws its views ──────────────────────┘
                     conch-ui (ui/) · shared by both apps
      views: Tree, Palette, Status bar, Changes, Inspector, Agents, Files, Sessions
      model: each view's state from protocol data, moved here from the Swift builders
      bridge: post(view, type) out, update(state) in; the host checks every message
```

The one structural change: each view's state is worked out in conch-ui, not in Swift. Today `ChangesWeb.swift`, `Inspector.swift` and `AgentsWeb.swift` build the JSON the Mac app sends its web views, and the Electron app would have to write all of it again.

- **Model in conch-ui.** A view takes the protocol's own data (`project.changes`, `review.list`, `pane.list`, the PR info) plus the UI's state (selection, ticks, split), and works out what to draw in TypeScript, tested with node.
- **Hosts send inputs, not views.** Conch.app's `ConchUIHost` sends the same inputs it gathers now (`ChangesWebInput` as JSON). The Electron renderer sends what it reads from the server. The Swift builders are deleted once the Mac app has switched.
- **Actions stay native.** A click becomes an intent, as now. The Mac app runs it on `ChangesModel`; the Electron app runs it with the same protocol calls, kept in a shared `actions.ts` with the methods each intent needs.
- **Frame and terminals stay per app.** SwiftUI and AppKit draw the Mac window and Ghostty its terminals. The renderer draws the Electron window and xterm.js its terminals. Splits get one shared layout model (`Layout.swift`, ported to TypeScript) so both apps split, resize and zoom alike.
- **Security unchanged.** The page runs nothing it did not ship. The Electron preload allows a fixed list of methods, which grows with each view and is checked by a test, as `pagesHaveNoBridgeIntoConch` does on the Mac.

## Phases and acceptance

Four phases, each shipped and reviewed against Conch.app before the next starts. The P0 rows of the parity matrix are phase 1.

1. **Looks like Conch.app.** Title band (workspaces, search, counts), the full sidebar tree, Needs you with answer buttons, the tab bar with numbers and marks, pane card headers, the status bar, Ctrl+K palette, the shared shortcut table, frameless window per platform. *Gate:* side-by-side captures match (below), every P0 row is Done.
2. **Works like Conch.app.** Splits with the shared layout model, Ctrl+D / Ctrl+Shift+D, zoom, drag gaps; themes and text sizes from config.toml; remote machines through `conch machine bridge`. *Gate:* end-to-end rows 9.269 and 9.270 pass on a real Linux desktop and on Windows with WSL.
3. **One set of views.** The model moves into conch-ui; Changes and the inspector run in Electron; the Mac app sends inputs instead of views and its Swift builders are deleted. *Gate:* both apps pass the same view tests, and Conch.app shows no change in its captures.
4. **The rest.** Files, Sessions, Agent setup, Settings (the settings page in a window), browser tabs, Workbench, scratch layer, System Monitor, Tickets, Integrations; installers and updates. *Gate:* the parity matrix has no Missing row left except Mobile Preview.

How parity is checked:

- **Captures, not memory.** Both apps can photograph their own windows without screen permission: Conch.app with `CONCH_DEBUG_DRIVE` / `--render-*`, Electron with `CONCH_DESKTOP_DRIVE` (`snap NAME`). A script drives both against the same isolated server and demo project (`scripts/tui-capture`'s demo) and puts the pairs side by side for review.
- **Same strings.** A test lists every user-facing string in conch-ui and the Electron renderer and fails on one Conch.app does not have, unless it is a listed platform difference.
- **Same keys.** The shared shortcut table is the source for both apps' menus; a test fails when either app has a shortcut the table does not.
- **Views in a real web view.** conch-ui views keep their WebKit tests on the Mac (`ConchUIPage`) and get the same tests in Electron (Playwright for Electron) in CI on Linux and Windows.

## Open questions and risks

Questions to decide:

- [ ] Should the Mac app's sidebar and status bar move to conch-ui too, so the two cannot drift, or stay SwiftUI and be matched by captures?
- [ ] Which Conch.app styles come along: Standard and Terminal only, or Cel, Ink and Meadow as well?
- [ ] Windows without WSL: refuse with instructions (today), or a native Windows server later?
- [ ] Where releases are published (the same GitHub release as the DMG?) and whether Windows builds are signed.

Risks:

- **Terminal quality.** xterm.js is behind Ghostty on speed and fidelity; at most 16 WebGL contexts. Mitigation: WebGL on visible panes only, a 20-agent stress run before phase 2 closes.
- **Drift.** Two apps change at different speeds. Mitigation: the capture pairs and the string and shortcut tests run in CI, so a change to one shows up as a failure in the other.
- **Moving the model.** Phase 3 changes Conch.app's Changes tab underneath it. Mitigation: `[app] web_views = false` still brings back the SwiftUI view until the move has been in a release.
- **WSL.** Paths, PATH in a login shell and the distribution picked differ per machine. Mitigation: the bridge command is tested, and the window says exactly what failed and how to fix it.
- **Untested platforms.** The packages are built on a Mac and have not run on Linux or Windows yet; rows 9.269 and 9.270 are the first real runs.
