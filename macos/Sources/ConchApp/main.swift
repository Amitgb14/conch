import AppKit
import ConchKit
import SwiftUI

// The window: AppKit for what macOS draws itself — the unified toolbar the
// traffic lights sit in, the menu bar and its shortcuts — and SwiftUI for
// everything inside (Views.swift).

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate {
    /// The first window's store, which says what is said once for every
    /// window (Windows+Store.swift).
    let store = Store()
    /// Every window, the first one's first (AppWindow.swift).
    var windows: [AppWindow] = []
    var window: NSWindow! { windows.first?.window }
    /// The store of the window the menus act on: the key window's.
    var keyStore: Store { AppWindows.shared.keyStore ?? store }
    /// Where the windows are kept when the app quits, to open again.
    static let windowsKey = "conch.windows"
    /// Under Quit: how many panes go on running without the app.
    let runOnItem: NSMenuItem = {
        let i = NSMenuItem(title: "", action: nil, keyEquivalent: "")
        i.isEnabled = false
        return i
    }()

    /// Shown only while an update has left the app before it to go back to.
    lazy var rollBackItem: NSMenuItem = {
        let i = NSMenuItem(title: "Roll Back Update", action: #selector(runCommand(_:)), keyEquivalent: "")
        i.representedObject = "rollBackUpdate"
        i.target = self
        i.isHidden = true
        return i
    }()

    /// The conch menu opening: what quitting leaves running, said now.
    func menuNeedsUpdate(_ menu: NSMenu) {
        // A theme file added or fixed since is in the menu as it opens.
        if menu === themeMenu {
            store.loadTheme()
            return
        }
        if menu === benchMenu {
            updateBenchMenu()
            return
        }
        rollBackItem.isHidden = !store.updater.canRollBack
        runOnItem.attributedTitle = NSAttributedString(string: runOnNote(panes: store.runningPanes),
                                                       attributes: [.font: NSFont.systemFont(ofSize: 11), .foregroundColor: NSColor.secondaryLabelColor])
    }

    /// [app]'s parts AppKit draws: the toolbar's height, option-space
    /// from anywhere.
    func applyAppLook(_ look: AppLook) {
        for w in windows { w.window.toolbarStyle = look.compact ? .unifiedCompact : .unified }
        #if GHOSTTY
        GhosttyRuntime.shared.setFontSize(look.fontSize)
        #endif
        // SwiftTerm's panes — every pane without Ghostty, and any a Ghostty
        // build still draws with it — take it too, or ⌘+ said a size it
        // never applied.
        PaneTerminal.fontSize = look.fontSize
        for s in AppWindows.shared.stores { for t in s.terminals.values { t.setFontSize(look.fontSize) } }
        // Not for a -background run: the key is global, so a scripted copy
        // beside the person's own Conch would take their ⌥Space — and come
        // to the front with it, taking the keys typed next.
        ScratchHotKey.shared.set(look.scratchKey && !UserDefaults.standard.bool(forKey: "background"))
    }

    func applicationDidFinishLaunching(_ note: Notification) {
        NSApp.setActivationPolicy(.regular)
        NSApp.appearance = NSAppearance(named: store.theme.light ? .aqua : .darkAqua)
        buildMenu()

        NSWindow.allowsAutomaticWindowTabbing = false
        AppWindows.shared.onBringForward = { [weak self] s in self?.bringForward(s) }
        AppWindows.shared.onOpenWindow = { [weak self] ws, project in self?.openWindow(workspace: ws, project: project) }
        let main = AppWindow(store: store, app: self, frame: nil)
        windows = [main]
        // Notifications, and the pane a clicked one opens: in the window
        // that holds it, its project or its workspace (windowFor).
        Notifier.shared.onOpen = { [weak self] k in
            guard let self else { return }
            let s = AppWindows.shared.store(forPane: k) ?? self.store
            self.bringForward(s)
            s.open(k)
            s.focusTerminal()
        }
        Notifier.shared.start()
        ScratchHotKey.shared.fire = { [weak self] in
            guard let self else { return }
            NSApp.activate(ignoringOtherApps: true)
            let s = self.keyStore
            self.bringForward(s)
            s.toggleScratch()
        }
        applyAppLook(store.appLook)
        let settings = SettingsWindow(store: store)
        settingsWindow = settings
        let system = SystemMonitorWindow(store: store)
        systemWindow = system
        if UserDefaults.standard.bool(forKey: "background") {
            // -background YES: opened behind whatever is in front and not
            // made active, for a scripted run beside somebody at work —
            // it takes neither their keyboard nor their screen.
            main.window.orderBack(nil)
        } else {
            main.window.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
        }
        store.connect()
        // Conch.app's own updates: once for the app, not for each window.
        AppUpdater.shared.start()
        // The windows open when the app last quit, behind the first.
        for saved in loadSavedWindows(UserDefaults.standard.data(forKey: Self.windowsKey)) {
            let s = Store(windowID: saved.id, workspace: saved.workspace, project: saved.project, layout: saved.layout.layout())
            let w = AppWindow(store: s, app: self, frame: saved.frame)
            windows.append(w)
            w.window.orderBack(nil)
            s.connect()
        }
        main.window.orderFront(nil)
    }

    /// What every window's store is told, wherever it was set: a theme,
    /// a style, the Settings window, the menus' ticks.
    func wire(_ s: Store, _ w: AppWindow) {
        s.onThemeChoice = { [weak self] in self?.updateThemeMenu() }
        s.onTheme = { [weak self, weak w] t in
            NSApp.appearance = NSAppearance(named: t.light ? .aqua : .darkAqua)
            w?.window.backgroundColor = Theme.ns(t.chrome)
            self?.updateThemeMenu()
        }
        s.onStyle = { [weak w] st in w?.window.titlebarAppearsTransparent = st.paintsTitleBar }
        s.onAppLook = { [weak self] look in self?.applyAppLook(look) }
        s.onThemeList = { [weak self] in self?.fillThemeMenu() }
        s.onOpenSettings = { [weak self] in self?.settingsWindow?.show(section: $0) }
        s.onOpenSystemMonitor = { [weak self] in self?.systemWindow?.show() }
        // "Same as conch (Night Owl)" names conch's theme as it changes.
        s.onConfigChange = { [weak self] in self?.updateThemeMenu() }
    }

    // MARK: windows

    func bringForward(_ s: Store) {
        guard let w = windows.first(where: { $0.store === s })?.window else { return }
        if w.isMiniaturized { w.deminiaturize(nil) }
        if !w.isVisible { reopen(windows.first { $0.store === s }) }
        w.makeKeyAndOrderFront(nil)
    }

    /// File → New Window: the first window again if it was closed, else a
    /// window of its own on the key window's workspace — or, from a
    /// project's row or a workspace's menu, on that.
    func openWindow(workspace: String? = nil, project: String? = nil) {
        if workspace == nil, project == nil, let main = windows.first, !main.window.isVisible {
            reopen(main)
            main.window.makeKeyAndOrderFront(nil)
            return
        }
        let from = keyStore
        let s = Store(windowID: "w-" + UUID().uuidString.prefix(8).lowercased(),
                      workspace: workspace ?? from.homeWs?.id ?? "first", project: project)
        let w = AppWindow(store: s, app: self, frame: nil)
        // A step down and across from the window it came from, so both
        // are seen, and kept on that window's screen.
        if let at = from.mainWindow, at.isVisible {
            var f = at.frame.offsetBy(dx: 28, dy: -28)
            if let screen = at.screen { f = w.window.constrainFrameRect(f, to: screen) }
            w.window.setFrame(f, display: false)
        }
        windows.append(w)
        w.window.makeKeyAndOrderFront(nil)
        s.connect()
        saveWindows()
    }

    /// The first window shown again after it was closed: what other
    /// windows took meanwhile stays with them.
    private func reopen(_ w: AppWindow?) {
        guard let w else { return }
        w.store.dropHeldElsewhere()
        w.window.makeKeyAndOrderFront(nil)
    }

    /// A window closed. The first is kept, to come back with its tabs
    /// (File → New Window, the Dock); any other ends, and is not opened
    /// again with the app.
    func closed(_ w: AppWindow) {
        guard w.store !== store else { return }
        windows.removeAll { $0 === w }
        AppWindows.shared.remove(w.store)
        w.store.closeWindow()
        saveWindows()
    }

    /// The windows besides the first, kept for the next launch.
    func saveWindows() {
        guard !quitting else { return }
        let saved = windows.dropFirst().filter { $0.window.isVisible || $0.window.isMiniaturized }
            .map { $0.store.saved(frame: $0.window.frameDescriptor) }
        UserDefaults.standard.set(encodeSavedWindows(Array(saved)), forKey: Self.windowsKey)
    }

    /// ⌘` and ⇧⌘`: the next window, round the end (nextWindow).
    func cycleWindows(back: Bool) {
        let shown = windows.filter { $0.window.isVisible && !$0.window.isMiniaturized }
        let cur = shown.first { $0.window.isKeyWindow }?.store.windowID
        guard let id = nextWindow(after: cur, in: shown.map(\.store.windowID), back: back),
              let w = shown.first(where: { $0.store.windowID == id }) else { NSSound.beep(); return }
        w.window.makeKeyAndOrderFront(nil)
    }

    func applicationShouldHandleReopen(_ app: NSApplication, hasVisibleWindows: Bool) -> Bool {
        if !hasVisibleWindows { reopen(windows.first) }
        return true
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ app: NSApplication) -> Bool { true }

    /// Quitting. The windows are saved first, while they are still on
    /// screen — by applicationWillTerminate they have been taken down, and
    /// each one's closing would take it out of what is saved. Then, with
    /// edits not saved in any window — a file in a Files tab or a
    /// workbench, a task's brief — it asks; saving is waited for, and a save
    /// that fails keeps the app open with the edits still there.
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        saveWindows()
        let items = AppWindows.shared.stores.flatMap { $0.unsaved() }
        guard !items.isEmpty else {
            quitting = true
            return .terminateNow
        }
        switch askUnsaved(items) {
        case .alertFirstButtonReturn:
            quitting = true
            Task { @MainActor in
                let saved = await saveAll(items)
                if !saved { self.quitting = false }
                NSApp.reply(toApplicationShouldTerminate: saved)
            }
            return .terminateLater
        case .alertSecondButtonReturn:
            quitting = true
            return .terminateNow
        default:
            return .terminateCancel
        }
    }

    /// Save, Don't Save or Cancel, for edits that would be lost.
    func askUnsaved(_ items: [Unsaved]) -> NSApplication.ModalResponse {
        let q = unsavedQuestion(items.map(\.name))
        let a = NSAlert()
        a.messageText = q.title
        a.informativeText = q.info
        a.addButton(withTitle: items.count == 1 ? "Save" : "Save All")
        a.addButton(withTitle: "Don't Save")
        a.addButton(withTitle: "Cancel")
        return a.runModal()
    }

    /// A window closing (AppWindow's windowShouldClose). The last one on
    /// screen is quitting, so with edits not saved anywhere it is asked as
    /// quitting is, and the window stays when that is cancelled —
    /// otherwise it closed first, and a cancelled quit left the app running
    /// with no window. Any other window but the first ends with its store,
    /// so its own edits are asked about; the first is kept, edits and all.
    func shouldClose(_ w: AppWindow) -> Bool {
        let others = windows.filter { $0 !== w && ($0.window.isVisible || $0.window.isMiniaturized) }
        if others.isEmpty {
            guard !AppWindows.shared.stores.flatMap({ $0.unsaved() }).isEmpty else { return true }
            NSApp.terminate(nil)
            return false
        }
        guard w.store !== store else { return true }
        let items = w.store.unsaved()
        guard !items.isEmpty else { return true }
        switch askUnsaved(items) {
        case .alertFirstButtonReturn:
            Task { @MainActor in if await saveAll(items) { w.window.close() } }
            return false
        case .alertSecondButtonReturn:
            return true
        default:
            return false
        }
    }

    private var quitting = false

    func applicationWillTerminate(_ notification: Notification) {
        for s in AppWindows.shared.stores { s.shutdown() }
    }

    // MARK: menu

    func buildMenu() {
        let main = NSMenu()
        func menu(_ title: String, _ items: [NSMenuItem]) {
            let m = NSMenu(title: title)
            items.forEach(m.addItem)
            let top = NSMenuItem(title: title, action: nil, keyEquivalent: "")
            top.submenu = m
            main.addItem(top)
        }
        func item(_ title: String, _ key: String, _ mods: NSEvent.ModifierFlags = .command, _ cmd: String) -> NSMenuItem {
            let i = NSMenuItem(title: title, action: #selector(runCommand(_:)), keyEquivalent: key)
            i.keyEquivalentModifierMask = mods
            i.representedObject = cmd
            i.target = self
            return i
        }
        menu("conch", [
            NSMenuItem(title: "About Conch (Beta)", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: ""),
            item("Check for Updates…", "", [], "checkForUpdates"),
            rollBackItem,
            .separator(),
            item("Settings…", ",", .command, "settings"),
            item("Run in Terminal", "", [], "openTUI"),
            .separator(),
            NSMenuItem(title: "Hide Conch (Beta)", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h"),
            NSMenuItem(title: "Quit Conch (Beta)", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q"),
            runOnItem,
        ])
        main.items.first?.submenu?.delegate = self
        menu("File", [
            item("New Window", "n", [.command, .option], "newWindow"),
            .separator(),
            item("New Task…", "n", .command, "newTask"),
            item("Start Agent", "n", [.command, .shift], "startAgent"),
            item("New Terminal Tab", "t", .command, "newTab"),
            item("New Browser Tab", "b", [.command, .shift], "newBrowser"),
            .separator(),
            item("Add Project…", "o", .command, "addProject"),
            item("SSH to a Host…", "h", [.command, .shift], "sshToHost"),
            item("Add Machine…", "m", [.command, .shift], "addMachine"),
            item("New Sandbox…", "", .command, "newSandbox"),
            .separator(),
            item("Split Right", "d", .command, "splitRight"),
            item("Split Down", "d", [.command, .shift], "splitDown"),
            item("Zoom Split", "\r", [.command, .shift], "zoomSplit"),
            item("Select Split Left", String(UnicodeScalar(NSLeftArrowFunctionKey)!), [.command, .option], "splitLeft"),
            item("Select Split Right", String(UnicodeScalar(NSRightArrowFunctionKey)!), [.command, .option], "splitRightward"),
            item("Select Split Above", String(UnicodeScalar(NSUpArrowFunctionKey)!), [.command, .option], "splitAbove"),
            item("Select Split Below", String(UnicodeScalar(NSDownArrowFunctionKey)!), [.command, .option], "splitBelow"),
            .separator(),
            item("Show or Hide Scratch Layer", " ", .option, "scratch"),
            item("Dock Scratch Layer as a Split", "\r", [.command, .option], "dockScratch"),
            item("Make Scratch Layer a Tab", "t", [.command, .option], "scratchTab"),
            .separator(),
            item("Close Split", "w", .command, "closeSplit"),
            item("Close Workspace…", "", [], "closeWorkspace"),
        ])
        menu("Edit", [
            NSMenuItem(title: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c"),
            NSMenuItem(title: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v"),
            NSMenuItem(title: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a"),
            .separator(),
            item("Find…", "f", .command, "find"),
            item("Find and Replace in File…", "f", [.command, .option], "replaceInFile"),
            item("Find Older", "g", .command, "findOlder"),
            item("Find Newer", "g", [.command, .shift], "findNewer"),
        ])
        var go = [
            item("Search…", "k", .command, "palette"),
            item("Go to File…", "p", .command, "goToFile"),
            item("Search in Files…", "f", [.command, .shift], "searchFiles"),
            item("Edit with Agent…", "i", .command, "editWithAgent"),
            item("Sidebar", "0", .command, "focusSidebar"),
            item("Next Agent Waiting", ".", .command, "nextWaiting"),
            .separator(),
            item("Next Tab", "]", [.command, .shift], "nextTab"),
            item("Previous Tab", "[", [.command, .shift], "previousTab"),
        ]
        for n in 1...9 { go.append(item("Tab \(n)", "\(n)", .command, "tab\(n)")) }
        menu("Go", go)
        let themes = NSMenu(title: "Theme")
        let same = NSMenuItem(title: "Same as conch", action: #selector(chooseTheme(_:)), keyEquivalent: "")
        same.representedObject = ""
        same.target = self
        themes.addItem(same)
        themes.delegate = self
        themeMenu = themes
        fillThemeMenu()
        let themeItem = NSMenuItem(title: "Theme", action: nil, keyEquivalent: "")
        themeItem.submenu = themes
        // How the chrome is drawn, apart from its colours.
        let styles = NSMenu(title: "Style")
        for st in AppStyle.allCases {
            let m = NSMenuItem(title: st.label, action: #selector(chooseStyle(_:)), keyEquivalent: "")
            m.representedObject = st.rawValue
            m.target = self
            styles.addItem(m)
        }
        styleMenu = styles
        let styleItem = NSMenuItem(title: "Style", action: nil, keyEquivalent: "")
        styleItem.submenu = styles
        // A workbench: an agent's files, a terminal, its chat, its own
        // terminal and, on this Mac, the phone in one tab, in one of five
        // layouts.
        let bench = NSMenu(title: "Workbench")
        bench.addItem(item("Open Workbench", "b", [.command, .option], "workbench"))
        bench.addItem(.separator())
        for (n, l) in BenchLayout.allCases.enumerated() {
            let m = item(l.label + " Layout", "\(n + 1)", [.command, .option], "bench:" + l.rawValue)
            m.toolTip = l.detail
            bench.addItem(m)
        }
        benchMenu = bench
        bench.delegate = self
        let benchItem = NSMenuItem(title: "Workbench", action: nil, keyEquivalent: "")
        benchItem.submenu = bench
        menu("View", [item("Show or Hide Sidebar", "s", [.command, .control], "toggleSidebar"), .separator(), benchItem, .separator(), styleItem, themeItem,
                      .separator(),
                      // The usual Mac keys; ⌘0 is the sidebar's, so the
                      // defaults are control-command-0.
                      item("Bigger Terminal Text", "=", .command, "biggerFont"),
                      item("Smaller Terminal Text", "-", .command, "smallerFont"),
                      item("Default Terminal Text", "0", [.command, .control], "defaultFont"),
                      .separator(),
                      item("Bigger Window Text", "+", [.command, .shift], "biggerText"),
                      item("Smaller Window Text", "_", [.command, .shift], "smallerText"),
                      item("Default Window Text", "0", [.command, .control, .shift], "defaultText")])
        updateStyleMenu()
        updateThemeMenu()
        let windowMenu = NSMenu(title: "Window")
        windowMenu.addItem(NSMenuItem(title: "Minimize", action: #selector(NSWindow.performMiniaturize(_:)), keyEquivalent: "m"))
        windowMenu.addItem(.separator())
        // Here rather than left to macOS, so a terminal that takes ⌘` for
        // itself cannot keep it from the windows.
        windowMenu.addItem(item("Cycle Through Windows", "`", .command, "cycleWindows"))
        windowMenu.addItem(item("Cycle Back Through Windows", "~", [.command, .shift], "cycleWindowsBack"))
        windowMenu.addItem(NSMenuItem(title: "Bring All to Front", action: #selector(NSApplication.arrangeInFront(_:)), keyEquivalent: ""))
        windowMenu.addItem(.separator())
        windowMenu.addItem(item("System Monitor", "m", [.command, .option], "systemMonitor"))
        let wtop = NSMenuItem(title: "Window", action: nil, keyEquivalent: "")
        wtop.submenu = windowMenu
        main.addItem(wtop)
        NSApp.windowsMenu = windowMenu
        let help = NSMenu(title: "Help")
        [item("Welcome to Conch", "", [], "welcome"), item("Keyboard Shortcuts", "/", .command, "shortcuts"), item("Check conch…", "", [], "doctor")].forEach(help.addItem)
        let helpTop = NSMenuItem(title: "Help", action: nil, keyEquivalent: "")
        helpTop.submenu = help
        main.addItem(helpTop)
        NSApp.helpMenu = help
        NSApp.mainMenu = main
    }

    var themeMenu: NSMenu?
    var benchMenu: NSMenu?

    /// A tick on the layout of the workbench on screen, else the one a new
    /// workbench takes.
    func updateBenchMenu() {
        let cur = keyStore.layout.current?.bench ?? keyStore.benchLayout
        for m in benchMenu?.items ?? [] {
            guard let c = m.representedObject as? String, c.hasPrefix("bench:") else { continue }
            m.state = c == "bench:" + cur.rawValue ? .on : .off
            m.isHidden = c == "bench:" + BenchLayout.chat.rawValue && !store.offersChat
        }
    }
    var styleMenu: NSMenu?

    @objc func chooseStyle(_ sender: NSMenuItem) {
        // The app's look is every window's.
        for s in AppWindows.shared.stores { s.chooseStyle(AppStyle(setting: sender.representedObject as? String)) }
        updateStyleMenu()
        updateThemeMenu()
    }

    /// A tick on the style in use.
    func updateStyleMenu() {
        for m in styleMenu?.items ?? [] {
            m.state = (m.representedObject as? String) == store.style.rawValue ? .on : .off
        }
    }
    var settingsWindow: SettingsWindow?
    var systemWindow: SystemMonitorWindow?

    @objc func chooseTheme(_ sender: NSMenuItem) {
        for s in AppWindows.shared.stores { s.chooseTheme(sender.representedObject as? String ?? "") }
        updateThemeMenu()
    }

    /// A tick on the theme chosen, and the one "Same as conch" stands for.
    /// The Theme menu's themes: the built-in ones, dark then light, then
    /// your own, with any theme file that did not read said under them.
    func fillThemeMenu() {
        guard let themes = themeMenu else { return }
        while themes.items.count > 1 { themes.removeItem(at: 1) }
        themes.addItem(.separator())
        for (i, t) in ConchTheme.all.enumerated() {
            if i > 0, t.light && !ConchTheme.all[i - 1].light { themes.addItem(.separator()) }
            themes.addItem(themeItem(t))
        }
        if !ConchTheme.user.isEmpty || !store.userThemeProblems.isEmpty {
            themes.addItem(.separator())
            let head = NSMenuItem(title: "Your Themes", action: nil, keyEquivalent: "")
            head.isEnabled = false
            themes.addItem(head)
            ConchTheme.user.forEach { themes.addItem(themeItem($0)) }
            for p in store.userThemeProblems {
                let m = NSMenuItem(title: "⚠ " + p, action: nil, keyEquivalent: "")
                m.isEnabled = false
                themes.addItem(m)
            }
        }
        themes.addItem(.separator())
        let folder = NSMenuItem(title: "Open Themes Folder", action: #selector(openThemesFolder), keyEquivalent: "")
        folder.target = self
        themes.addItem(folder)
        updateThemeMenu()
    }

    private func themeItem(_ t: ConchTheme) -> NSMenuItem {
        let m = NSMenuItem(title: t.label, action: #selector(chooseTheme(_:)), keyEquivalent: "")
        m.representedObject = t.id
        m.target = self
        return m
    }

    /// Where themes of your own go, made if it is not there yet.
    @objc func openThemesFolder() {
        let dir = (store.conchDir as NSString).appendingPathComponent("themes")
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        NSWorkspace.shared.open(URL(fileURLWithPath: dir))
    }

    func updateThemeMenu() {
        guard let themes = themeMenu else { return }
        for m in themes.items {
            guard let id = m.representedObject as? String else { continue }
            m.state = id == store.themeChoice ? .on : .off
            if id.isEmpty { m.title = "Same as conch (\(store.conchTheme.label))" }
        }
    }

    @objc func runCommand(_ sender: NSMenuItem) {
        guard let cmd = sender.representedObject as? String else { return }
        // A window's command is the key window's; the app's (Settings) are
        // the first window's store's, which the Settings window shows.
        let store = keyStore
        switch cmd {
        case "newWindow": openWindow()
        case "cycleWindows": cycleWindows(back: false)
        case "cycleWindowsBack": cycleWindows(back: true)
        case "settings": self.store.command(cmd)
        case "palette": store.paletteOpen.toggle()
        case let t where t.hasPrefix("tab"):
            if let n = Int(t.dropFirst(3)) {
                store.layout.select(n - 1)
                store.focusTerminal()
            }
        default: store.command(cmd)
        }
    }
}

extension NSToolbarItem.Identifier {
    static let workspaces = NSToolbarItem.Identifier("conch.workspaces")
    static let search = NSToolbarItem.Identifier("conch.search")
    static let counts = NSToolbarItem.Identifier("conch.counts")
}

// The thin scrollers macOS shows while scrolling, whatever "Show scroll
// bars" says system-wide: with a mouse connected it is the old always-there
// bars, which sit in the sidebar like a seam. Set in the app's own domain,
// so it is conch's alone and the system's setting is left as it is; before
// the application starts, which is when AppKit reads it.
UserDefaults.standard.set("WhenScrolling", forKey: "AppleShowScrollBars")

// And for every other pipe the app writes to (a conch it runs, a pane's
// terminal): a reader gone is an error where it is written, never the end
// of the app.
signal(SIGPIPE, SIG_IGN)

let app = NSApplication.shared
let delegate = MainActor.assumeIsolated { AppDelegate() }
app.delegate = delegate
app.run()
