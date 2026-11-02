import AppKit
import ConchKit
import SwiftUI

// The window: AppKit for what macOS draws itself — the unified toolbar the
// traffic lights sit in, the menu bar and its shortcuts — and SwiftUI for
// everything inside (Views.swift).

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSToolbarDelegate, NSWindowDelegate {
    let store = Store()
    var window: NSWindow!

    func applicationDidFinishLaunching(_ note: Notification) {
        NSApp.setActivationPolicy(.regular)
        NSApp.appearance = NSAppearance(named: store.theme.light ? .aqua : .darkAqua)
        buildMenu()

        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1320, height: 820),
                          styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                          backing: .buffered, defer: false)
        window.title = "conch"
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = false
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

        window.contentView = NSHostingView(rootView: MainView(store: store))
        store.onTheme = { [weak self] t in
            NSApp.appearance = NSAppearance(named: t.light ? .aqua : .darkAqua)
            self?.window.backgroundColor = Theme.ns(t.chrome)
            self?.updateThemeMenu()
        }
        let settings = SettingsWindow(store: store)
        settingsWindow = settings
        store.onOpenSettings = { settings.show() }
        // "Same as conch (Night Owl)" names conch's theme as it changes.
        store.onConfigChange = { [weak self] in self?.updateThemeMenu() }
        if !window.setFrameUsingName("conch.main") { window.center() }
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
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
        case .workspaces: view = NSHostingView(rootView: WorkspacesControl(store: store))
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
            NSMenuItem(title: "About conch", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: ""),
            .separator(),
            item("Settings…", ",", .command, "settings"),
            item("Open conch in a terminal", "", [], "openTUI"),
            .separator(),
            NSMenuItem(title: "Hide conch", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h"),
            NSMenuItem(title: "Quit conch", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q"),
        ])
        menu("File", [
            item("New Task…", "n", .command, "newTask"),
            item("Start Agent", "n", [.command, .shift], "startAgent"),
            item("New Terminal Tab", "t", .command, "newTab"),
            item("Split Right", "d", .command, "splitRight"),
            item("Split Down", "d", [.command, .shift], "splitDown"),
            .separator(),
            item("Close Split", "w", .command, "closeSplit"),
        ])
        menu("Edit", [
            NSMenuItem(title: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c"),
            NSMenuItem(title: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v"),
            NSMenuItem(title: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a"),
        ])
        var go = [
            item("Search…", "k", .command, "palette"),
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
        themes.addItem(.separator())
        for (i, t) in ConchTheme.all.enumerated() {
            if t.light && !ConchTheme.all[i - 1].light { themes.addItem(.separator()) }
            let m = NSMenuItem(title: t.label, action: #selector(chooseTheme(_:)), keyEquivalent: "")
            m.representedObject = t.id
            m.target = self
            themes.addItem(m)
        }
        themeMenu = themes
        let themeItem = NSMenuItem(title: "Theme", action: nil, keyEquivalent: "")
        themeItem.submenu = themes
        menu("View", [item("Show or Hide Sidebar", "s", [.command, .control], "toggleSidebar"), .separator(), themeItem])
        updateThemeMenu()
        let windowMenu = NSMenu(title: "Window")
        windowMenu.addItem(NSMenuItem(title: "Minimize", action: #selector(NSWindow.performMiniaturize(_:)), keyEquivalent: "m"))
        let wtop = NSMenuItem(title: "Window", action: nil, keyEquivalent: "")
        wtop.submenu = windowMenu
        main.addItem(wtop)
        NSApp.windowsMenu = windowMenu
        NSApp.mainMenu = main
    }

    var themeMenu: NSMenu?
    var settingsWindow: SettingsWindow?

    @objc func chooseTheme(_ sender: NSMenuItem) {
        store.chooseTheme(sender.representedObject as? String ?? "")
        updateThemeMenu()
    }

    /// A tick on the theme chosen, and the one "Same as conch" stands for.
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

let app = NSApplication.shared
let delegate = MainActor.assumeIsolated { AppDelegate() }
app.delegate = delegate
app.run()
