import AppKit
import ConchKit
import SwiftUI

// The window: AppKit for what macOS draws itself — the unified toolbar the
// traffic lights sit in, the menu bar and its shortcuts — and SwiftUI for
// everything inside (Views.swift).

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSToolbarDelegate, NSWindowDelegate, NSMenuDelegate {
    let store = Store()
    var window: NSWindow!
    /// Under Quit: how many panes go on running without the app.
    let runOnItem: NSMenuItem = {
        let i = NSMenuItem(title: "", action: nil, keyEquivalent: "")
        i.isEnabled = false
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
        runOnItem.attributedTitle = NSAttributedString(string: runOnNote(panes: store.runningPanes),
                                                       attributes: [.font: NSFont.systemFont(ofSize: 11), .foregroundColor: NSColor.secondaryLabelColor])
    }

    /// [app]'s parts AppKit draws: the toolbar's height, option-space
    /// from anywhere.
    func applyAppLook(_ look: AppLook) {
        window?.toolbarStyle = look.compact ? .unifiedCompact : .unified
        #if GHOSTTY
        GhosttyRuntime.shared.setFontSize(look.fontSize)
        #endif
        // SwiftTerm's panes — every pane without Ghostty, and any a Ghostty
        // build still draws with it — take it too, or ⌘+ said a size it
        // never applied.
        PaneTerminal.fontSize = look.fontSize
        for t in store.terminals.values { t.setFontSize(look.fontSize) }
        // Not for a -background run: the key is global, so a scripted copy
        // beside the person's own Conch would take their ⌥Space — and come
        // to the front with it, taking the keys typed next.
        ScratchHotKey.shared.set(look.scratchKey && !UserDefaults.standard.bool(forKey: "background"))
    }

    func applicationDidFinishLaunching(_ note: Notification) {
        NSApp.setActivationPolicy(.regular)
        NSApp.appearance = NSAppearance(named: store.theme.light ? .aqua : .darkAqua)
        buildMenu()

        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1320, height: 820),
                          styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                          backing: .buffered, defer: false)
        window.title = "conch"
        window.titleVisibility = .hidden
        // Meadow paints its own sky behind the toolbar.
        window.titlebarAppearsTransparent = store.style.paintsTitleBar
        window.backgroundColor = Theme.ns(store.theme.chrome)
        window.minSize = NSSize(width: 760, height: 440)
        window.setFrameAutosaveName("conch.main")
        window.delegate = self

        let toolbar = NSToolbar(identifier: "conch.toolbar")
        toolbar.delegate = self
        toolbar.displayMode = .iconOnly
        toolbar.allowsUserCustomization = false
        toolbar.centeredItemIdentifiers = [.search]
        window.toolbar = toolbar
        window.toolbarStyle = .unified

        store.mainWindow = window
        // Notifications, and the pane a clicked one opens.
        Notifier.shared.onOpen = { [weak self] k in
            self?.store.open(k)
            self?.store.focusTerminal()
        }
        Notifier.shared.start()
        store.onThemeChoice = { [weak self] in self?.updateThemeMenu() }
        let host = NSHostingView(rootView: MainView(store: store))
        // The window's size is its own (minSize above), not the view's: by
        // default a hosting view measures its whole tree for a minimum,
        // ideal and maximum size on every layout, and a spinner's frame is
        // a layout — which kept the app near a whole core while agents
        // worked.
        host.sizingOptions = []
        window.contentView = host
        store.onTheme = { [weak self] t in
            NSApp.appearance = NSAppearance(named: t.light ? .aqua : .darkAqua)
            self?.window.backgroundColor = Theme.ns(t.chrome)
            self?.updateThemeMenu()
        }
        store.onStyle = { [weak self] st in self?.window.titlebarAppearsTransparent = st.paintsTitleBar }
        store.onAppLook = { [weak self] look in self?.applyAppLook(look) }
        store.onThemeList = { [weak self] in self?.fillThemeMenu() }
        ScratchHotKey.shared.fire = { [weak self] in
            NSApp.activate(ignoringOtherApps: true)
            self?.window.makeKeyAndOrderFront(nil)
            self?.store.toggleScratch()
        }
        applyAppLook(store.appLook)
        let settings = SettingsWindow(store: store)
        settingsWindow = settings
        store.onOpenSettings = { settings.show(section: $0) }
        // "Same as conch (Night Owl)" names conch's theme as it changes.
        store.onConfigChange = { [weak self] in self?.updateThemeMenu() }
        if !window.setFrameUsingName("conch.main") { window.center() }
        if UserDefaults.standard.bool(forKey: "background") {
            // -background YES: opened behind whatever is in front and not
            // made active, for a scripted run beside somebody at work —
            // it takes neither their keyboard nor their screen.
            window.orderBack(nil)
        } else {
            window.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
        }
        store.connect()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ app: NSApplication) -> Bool { true }

    func applicationWillTerminate(_ notification: Notification) { store.shutdown() }

    /// The TUI may have changed workspaces or the theme while this window
    /// was in the background.
    func windowDidBecomeKey(_ notification: Notification) {
        store.loadWorkspaces()
        store.loadTheme()
        updateThemeMenu()
    }

    // MARK: toolbar

    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        [.workspaces, .flexibleSpace, .search, .counts]
    }

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        [.workspaces, .flexibleSpace, .search, .flexibleSpace, .counts]
    }

    func toolbar(_ toolbar: NSToolbar, itemForItemIdentifier id: NSToolbarItem.Identifier, willBeInsertedIntoToolbar: Bool) -> NSToolbarItem? {
        let item = NSToolbarItem(itemIdentifier: id)
        let view: NSView
        switch id {
        case .workspaces: view = WorkspacesHost(rootView: WorkspacesControl(store: store))
        case .search: view = NSHostingView(rootView: SearchField(store: store))
        case .counts: view = NSHostingView(rootView: Counts(store: store).fixedSize())
        default: return nil
        }
        view.setFrameSize(view.fittingSize)
        item.view = view
        item.visibilityPriority = .high
        // No glass bubble round it: the header is the mock's band, with the
        // controls drawn on it as they are (macOS 26 wraps each toolbar
        // item in one otherwise).
        item.isBordered = false
        return item
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
        // A workbench: an agent's files, a terminal, its chat and its own
        // terminal in one tab, in one of four layouts.
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
        let cur = store.layout.current?.bench ?? store.benchLayout
        for m in benchMenu?.items ?? [] {
            guard let c = m.representedObject as? String, c.hasPrefix("bench:") else { continue }
            m.state = c == "bench:" + cur.rawValue ? .on : .off
        }
    }
    var styleMenu: NSMenu?

    @objc func chooseStyle(_ sender: NSMenuItem) {
        store.chooseStyle(AppStyle(setting: sender.representedObject as? String))
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

    @objc func chooseTheme(_ sender: NSMenuItem) {
        store.chooseTheme(sender.representedObject as? String ?? "")
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
        switch cmd {
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
