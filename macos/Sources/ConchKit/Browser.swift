import Foundation

// A browser pane's own tabs, as in any browser: a strip of pages in one
// pane (a split, a tab of conch's own, or a workbench's browser part),
// opened, closed, reopened and dragged into order. What can be decided
// without WebKit is here — which tab is in front after a close, where a
// link's new tab goes, the keys a page leaves to the browser, zoom's
// steps, what is saved for a relaunch — so it is tested; the app holds a
// WKWebView per tab beside it.

/// One page in a browser pane.
public struct BrowserTab: Codable, Equatable, Sendable, Identifiable {
    public var id: Int
    /// The page's address; empty for a new tab nothing was typed into.
    public var address: String
    public var title: String
    /// The page's zoom, 1 for 100%.
    public var zoom: Double
    /// A private tab keeps nothing: no cookies past its life, never saved,
    /// never reopened.
    public var isPrivate: Bool
    /// Its Develop menu's settings: the cache, JavaScript, images,
    /// Responsive Design Mode.
    public var develop: BrowserDevelop

    public init(id: Int, address: String, title: String = "", zoom: Double = 1, isPrivate: Bool = false, develop: BrowserDevelop = BrowserDevelop()) {
        self.id = id
        self.address = address
        self.title = title
        self.zoom = BrowserZoom.clamped(zoom)
        self.isPrivate = isPrivate
        self.develop = develop
    }

    /// What the tab strip says: the page's title, else its host, else
    /// what a blank tab is.
    public var label: String {
        if !title.isEmpty { return title }
        if let h = URL(string: address)?.host, !h.isEmpty { return h }
        return address.isEmpty ? "New Tab" : address
    }
}

/// A browser pane's tabs and which is in front.
public struct BrowserTabs: Equatable, Sendable {
    /// A closed tab, kept for ⌘⇧T with where it was.
    public struct Closed: Equatable, Sendable {
        public var tab: BrowserTab
        public var index: Int
    }

    /// How many closed tabs ⌘⇧T can bring back.
    public static let closedMax = 25

    public private(set) var tabs: [BrowserTab] = []
    public private(set) var activeID: Int = 0
    /// Newest last.
    public private(set) var closed: [Closed] = []
    private var nextID = 1

    /// A pane opened at an address: one tab on it; "about:" is a new tab.
    public init(address: String) {
        self.init()
        open(address.hasPrefix("about:") ? "" : address)
    }

    private init() {}

    /// A pane as it was saved, or — nothing saved, an older build's
    /// window — one tab at the pane's own address.
    public init(saved: SavedBrowser?, address: String) {
        let kept = saved?.tabs ?? []
        guard !kept.isEmpty else {
            self.init(address: address)
            return
        }
        self.init()
        for s in kept { open(s.address, title: s.title ?? "", zoom: s.zoom ?? 1, select: false, develop: s.develop ?? BrowserDevelop()) }
        let a = min(max(saved?.active ?? 0, 0), tabs.count - 1)
        activeID = tabs[a].id
    }

    public var active: BrowserTab? { tabs.first { $0.id == activeID } }
    public var activeIndex: Int { tabs.firstIndex { $0.id == activeID } ?? 0 }
    public func tab(_ id: Int) -> BrowserTab? { tabs.first { $0.id == id } }
    func index(_ id: Int) -> Int? { tabs.firstIndex { $0.id == id } }

    /// A new tab: at the end (⌘T), or right after `after` — the page a link
    /// opened it from — and in front unless `select` is false (⌘-click).
    @discardableResult
    public mutating func open(_ address: String, title: String = "", zoom: Double = 1, after: Int? = nil, select: Bool = true, isPrivate: Bool = false,
                              develop: BrowserDevelop = BrowserDevelop()) -> Int {
        let t = BrowserTab(id: nextID, address: address, title: title, zoom: zoom, isPrivate: isPrivate, develop: develop)
        nextID += 1
        if let after, let i = index(after) { tabs.insert(t, at: i + 1) } else { tabs.append(t) }
        if select || tabs.count == 1 { activeID = t.id }
        return t.id
    }

    /// Closes a tab, the one to its right (else its left) coming to the
    /// front; true when none are left, and the pane is the caller's to
    /// close.
    @discardableResult
    public mutating func close(_ id: Int) -> Bool {
        guard let i = index(id) else { return tabs.isEmpty }
        let t = tabs.remove(at: i)
        if !t.isPrivate {
            closed.append(Closed(tab: t, index: i))
            if closed.count > Self.closedMax { closed.removeFirst(closed.count - Self.closedMax) }
        }
        if activeID == id, !tabs.isEmpty { activeID = tabs[min(i, tabs.count - 1)].id }
        return tabs.isEmpty
    }

    /// ⌘⇧T: the tab closed last, back where it was and in front.
    @discardableResult
    public mutating func reopen() -> Int? {
        guard let c = closed.popLast() else { return nil }
        var t = c.tab
        t.id = nextID
        nextID += 1
        tabs.insert(t, at: min(max(c.index, 0), tabs.count))
        activeID = t.id
        return t.id
    }

    /// Clear Browsing Data: no closed tab to bring back.
    public mutating func forgetClosed() { closed = [] }

    /// Every tab but this one closed (each can come back with ⌘⇧T).
    public mutating func closeOthers(_ id: Int) {
        guard index(id) != nil else { return }
        for t in tabs.reversed() where t.id != id { close(t.id) }
        activeID = id
    }

    /// The same page again, right after it and in front.
    @discardableResult
    public mutating func duplicate(_ id: Int) -> Int? {
        guard let t = tab(id) else { return nil }
        return open(t.address, title: t.title, zoom: t.zoom, after: id, isPrivate: t.isPrivate, develop: t.develop)
    }

    /// Takes a tab out to go somewhere else (its own pane): not closed, so
    /// not reopened. The only tab stays — it is the pane.
    public mutating func take(_ id: Int) -> BrowserTab? {
        guard tabs.count > 1, let i = index(id) else { return nil }
        let t = tabs.remove(at: i)
        if activeID == id { activeID = tabs[min(i, tabs.count - 1)].id }
        return t
    }

    /// A tab dragged to a place in the strip.
    public mutating func move(_ id: Int, to place: Int) {
        guard let i = index(id) else { return }
        let t = tabs.remove(at: i)
        tabs.insert(t, at: min(max(place, 0), tabs.count))
    }

    public mutating func select(_ id: Int) {
        if index(id) != nil { activeID = id }
    }

    /// ⌘1–⌘8 that tab; ⌘9 the last, however many there are.
    public mutating func select(number n: Int) {
        guard !tabs.isEmpty, n >= 1 else { return }
        if n == 9 { activeID = tabs[tabs.count - 1].id } else if n <= tabs.count { activeID = tabs[n - 1].id }
    }

    /// ⌃Tab and ⌃⇧Tab, round from one end to the other.
    public mutating func next() { step(1) }
    public mutating func previous() { step(-1) }
    private mutating func step(_ d: Int) {
        guard !tabs.isEmpty else { return }
        activeID = tabs[(activeIndex + d + tabs.count) % tabs.count].id
    }

    /// What the page says now; nil leaves that part as it was.
    public mutating func update(_ id: Int, address: String? = nil, title: String? = nil) {
        guard let i = index(id) else { return }
        if let address { tabs[i].address = address }
        if let title { tabs[i].title = title }
    }

    public mutating func setZoom(_ id: Int, _ z: Double) {
        guard let i = index(id) else { return }
        tabs[i].zoom = BrowserZoom.clamped(z)
    }

    public mutating func setDevelop(_ id: Int, _ d: BrowserDevelop) {
        guard let i = index(id) else { return }
        tabs[i].develop = d
    }

    // MARK: saved

    /// What a relaunch brings back: the tabs with a page, never a private
    /// one or a page's source (its text is not kept); nil when there are
    /// none.
    public var saved: SavedBrowser? {
        let kept = tabs.filter { !$0.isPrivate && !$0.address.isEmpty && !$0.address.hasPrefix("about:") && !isViewSource($0.address) }
        guard !kept.isEmpty else { return nil }
        // The tab in front, or the nearest kept one before it.
        let before = tabs.prefix(activeIndex + 1).filter { t in kept.contains { $0.id == t.id } }
        let active = before.last.flatMap { a in kept.firstIndex { $0.id == a.id } } ?? 0
        return SavedBrowser(tabs: kept.map { SavedBrowserTab(address: $0.address, title: $0.title.isEmpty ? nil : $0.title, zoom: $0.zoom == 1 ? nil : $0.zoom,
                                                         develop: $0.develop.isDefault ? nil : $0.develop) },
                            active: active)
    }

    /// The page the pane is named for when saved: the one in front.
    public var savedAddress: String? {
        saved.map { $0.tabs[$0.active].address }
    }
}

/// A browser pane's tabs as saved.
public struct SavedBrowser: Codable, Equatable, Sendable {
    public var tabs: [SavedBrowserTab]
    public var active: Int

    public init(tabs: [SavedBrowserTab], active: Int) {
        self.tabs = tabs
        self.active = active
    }
}

public struct SavedBrowserTab: Codable, Equatable, Sendable {
    public var address: String
    public var title: String?
    public var zoom: Double?
    public var develop: BrowserDevelop?

    public init(address: String, title: String? = nil, zoom: Double? = nil, develop: BrowserDevelop? = nil) {
        self.address = address
        self.title = title
        self.zoom = zoom
        self.develop = develop
    }
}

// MARK: zoom

/// A page's zoom, in Safari's steps.
public enum BrowserZoom {
    public static let steps: [Double] = [0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3]

    public static func clamped(_ z: Double) -> Double {
        guard z.isFinite else { return 1 }
        return min(max(z, steps[0]), steps[steps.count - 1])
    }

    public static func bigger(_ z: Double) -> Double { steps.first { $0 > z + 0.001 } ?? steps[steps.count - 1] }
    public static func smaller(_ z: Double) -> Double { steps.last { $0 < z - 0.001 } ?? steps[0] }
    public static func label(_ z: Double) -> String { "\(Int((z * 100).rounded()))%" }
}

// MARK: keys

/// What a key does while a browser pane has the keyboard.
public enum BrowserKey: Equatable, Sendable {
    case newTab, closeTab, reopenTab, privateTab
    case address, reload, back, forward
    case nextTab, previousTab, tab(Int)
    case find, findNext, findPrevious
    case zoomIn, zoomOut, zoomReset
    case inspector, viewSource, hardReload, responsive
}

/// The browser's keys, from a key-down: `characters` is what AppKit gives
/// ignoring modifiers. A key with neither ⌘ nor ⌃ is never the browser's —
/// letters typed into a page are the page's — and ⌘-keys the browser does
/// not name stay conch's (⌘D, ⌘K, ⇧⌘], ⌥⌘1…).
public func browserKeyAction(characters: String, keyCode: UInt16, command: Bool, shift: Bool, option: Bool, control: Bool) -> BrowserKey? {
    if control && !command && !option && keyCode == 48 { return shift ? .previousTab : .nextTab } // ⌃Tab
    let c = characters.lowercased()
    if control && command && !option && !shift { return c == "r" ? .responsive : nil } // ⌃⌘R
    guard command, !control else { return nil }
    if option {
        guard !shift else { return nil }
        switch c {
        case "i": return .inspector
        case "u": return .viewSource
        default: return nil
        }
    }
    if shift {
        switch c {
        case "t": return .reopenTab
        case "n": return .privateTab
        case "g": return .findPrevious
        case "r": return .hardReload
        default: return nil
        }
    }
    switch c {
    case "t": return .newTab
    case "w": return .closeTab
    case "l": return .address
    case "r": return .reload
    case "[": return .back
    case "]": return .forward
    case "f": return .find
    case "g": return .findNext
    case "=", "+": return .zoomIn
    case "-": return .zoomOut
    case "0": return .zoomReset
    case "1", "2", "3", "4", "5", "6", "7", "8", "9": return .tab(Int(c)!)
    default: return nil
    }
}

/// The browser's keys for Help → Keyboard Shortcuts.
public let browserKeysHelp: [(keys: String, what: String)] = [
    ("⌘T  ⌘W", "new tab, close tab"),
    ("⇧⌘T", "reopen the tab closed last"),
    ("⇧⌘N", "new private tab: keeps nothing"),
    ("⌘L  ⌘R", "address bar, reload"),
    ("⌘[  ⌘]", "back, forward"),
    ("⌃Tab  ⌃⇧Tab", "next, previous tab"),
    ("⌘1 … ⌘8  ⌘9", "that tab, the last tab"),
    ("⌘F  ⌘G  ⇧⌘G", "find in page, next, previous"),
    ("⌘+  ⌘−  ⌘0", "zoom in, out, actual size"),
    ("⌥⌘I  ⌥⌘U", "Web Inspector, page source"),
    ("⇧⌘R", "reload from the server, not the cache"),
    ("⌃⌘R", "Responsive Design Mode: the page at a phone's size"),
]
