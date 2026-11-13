import AppKit
import Combine
import ConchKit
import SwiftUI

/// One of the app's windows (#194): an NSWindow with the unified toolbar
/// the traffic lights sit in, around its own Store's SwiftUI. The first is
/// made as the app starts; File → New Window, a project's Open in New
/// Window and a workspace's make more (AppDelegate.openWindow).
@MainActor
final class AppWindow: NSObject, NSToolbarDelegate, NSWindowDelegate {
    let store: Store
    let window: NSWindow
    private weak var app: AppDelegate?
    private var titling: AnyCancellable?

    init(store: Store, app: AppDelegate, frame: String?) {
        self.store = store
        self.app = app
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1320, height: 820),
                          styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                          backing: .buffered, defer: false)
        super.init()
        window.title = store.windowTitle
        window.titleVisibility = .hidden
        // conch's own tabs are the window's; macOS's would be a second bar
        // of them above, so a window is never merged into another's.
        window.tabbingMode = .disallowed
        // Kept when closed: the first window comes back with its tabs, and
        // AppDelegate lets go of any other.
        window.isReleasedWhenClosed = false
        // Meadow paints its own sky behind the toolbar.
        window.titlebarAppearsTransparent = store.style.paintsTitleBar
        window.backgroundColor = Theme.ns(store.theme.chrome)
        window.minSize = NSSize(width: 760, height: 440)
        window.delegate = self

        let toolbar = NSToolbar(identifier: "conch.toolbar")
        toolbar.delegate = self
        toolbar.displayMode = .iconOnly
        toolbar.allowsUserCustomization = false
        toolbar.centeredItemIdentifiers = [.search]
        window.toolbar = toolbar
        window.toolbarStyle = store.appLook.compact ? .unifiedCompact : .unified

        store.mainWindow = window
        // Its title follows the workspace on screen, or the project's name
        // once its machine has said it — for the Window menu and Mission
        // Control, which is how one window is told from another.
        titling = store.objectWillChange.debounce(for: .milliseconds(200), scheduler: RunLoop.main).sink { [weak self] _ in
            guard let self else { return }
            let t = self.store.windowTitle
            if self.window.title != t { self.window.title = t }
        }
        AppWindows.shared.add(store)
        app.wire(store, self)
        let host = NSHostingView(rootView: MainView(store: store))
        // The window's size is its own (minSize above), not the view's: by
        // default a hosting view measures its whole tree for a minimum,
        // ideal and maximum size on every layout, and a spinner's frame is
        // a layout — which kept the app near a whole core while agents
        // worked.
        host.sizingOptions = []
        window.contentView = host
        if store.primary {
            window.setFrameAutosaveName("conch.main")
            if !window.setFrameUsingName("conch.main") { window.center() }
        } else if let frame {
            window.setFrame(from: frame)
        } else {
            window.center()
        }
    }

    /// The TUI may have changed workspaces or the theme while this window
    /// was in the background.
    func windowDidBecomeKey(_ notification: Notification) {
        store.loadWorkspaces()
        store.loadTheme()
        window.title = store.windowTitle
        app?.updateThemeMenu()
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool { app?.shouldClose(self) ?? true }

    func windowWillClose(_ notification: Notification) { app?.closed(self) }

    func windowDidMove(_ notification: Notification) { if !store.primary { app?.saveWindows() } }

    func windowDidEndLiveResize(_ notification: Notification) { if !store.primary { app?.saveWindows() } }

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
}
