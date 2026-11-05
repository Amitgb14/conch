import Foundation

// The TUI's prefix keys in the app's panes: [keys] prefix (ctrl+b), then a
// key from [keys.bind] — tmux's, as the TUI has them. The table is
// `conch keys -json`, the TUI's own reading of config.toml, so one file
// gives the same keys in both and the app never parses [keys.bind] a
// second way.

/// conch keys -json: the prefix, and every action with the keys it is on.
public struct KeyTable: Decodable, Equatable, Sendable {
    public struct Binding: Decodable, Equatable, Sendable {
        public var action: String
        /// Empty for an action somebody unbound.
        public var keys: [String]
        public var what: String

        public init(action: String, keys: [String], what: String) {
            self.action = action
            self.keys = keys
            self.what = what
        }
    }

    public var prefix: String
    public var keys: [Binding]
    /// What was wrong with [keys.bind], as conch keys says it.
    public var problems: [String]?

    public init(prefix: String, keys: [Binding], problems: [String]? = nil) {
        self.prefix = prefix
        self.keys = keys
        self.problems = problems
    }

    /// What a key after the prefix does, or nil when it does nothing.
    public func action(for key: String) -> String? {
        keys.first { $0.keys.contains(key) }?.action
    }

    /// The first key an action is on, or nil when it is unbound.
    public func key(for action: String) -> String? {
        keys.first { $0.action == action }?.keys.first
    }
}

/// A key as the TUI names it — "ctrl+b", "alt+1", "left", "%", "C", " " —
/// from what an NSEvent says of it, or nil for one that is never conch's:
/// anything with ⌘, which belongs to the menus.
/// - Parameters:
///   - characters: the event's charactersIgnoringModifiers, which still
///     has shift's effect (shift-5 is "%"), as the TUI's keys do.
public func conchKeyName(keyCode: UInt16, characters: String?, ctrl: Bool, alt: Bool, shift: Bool, command: Bool) -> String? {
    if command { return nil }
    var name: String
    if let n = namedKeys[keyCode] {
        name = n
        if shift && n != " " { name = "shift+" + name }
    } else {
        guard let c = characters, c.count == 1 else { return nil }
        // ctrl with a letter is the letter, lower-case: the terminal has no
        // ctrl+B apart from ctrl+b, and neither does the TUI.
        name = ctrl ? c.lowercased() : c
    }
    if alt { name = "alt+" + name }
    if ctrl { name = "ctrl+" + name }
    return name
}

/// The keys with a name rather than a character, by their key code, in
/// the spelling internal/keymap uses.
private let namedKeys: [UInt16: String] = [
    123: "left", 124: "right", 125: "down", 126: "up",
    116: "pgup", 121: "pgdown", 115: "home", 119: "end",
    36: "enter", 76: "enter", 48: "tab", 53: "esc", 51: "backspace", 117: "delete",
    49: " ",
]

/// What a key does, said by the prefix state.
public enum PrefixStep: Equatable, Sendable {
    /// Not conch's: the key goes on to the program, or the menus.
    case pass
    /// The prefix: the next key is conch's.
    case arm
    /// The prefix twice: the program is given its own prefix key.
    case typePrefix
    /// A bound action, by its name in [keys.bind].
    case run(String)
    /// A digit: the tab at that place (1 the first, 0 the tenth).
    case tab(Int)
    /// A key bound to nothing: as in the TUI, the keyboard goes to the
    /// sidebar — the prefix alone is how to reach the tree.
    case sidebar
}

/// The prefix, pressed or not, and what the next key does.
public struct PrefixKeys: Equatable, Sendable {
    public var table: KeyTable?
    /// The prefix was pressed and the next key is conch's.
    public private(set) var armed = false

    public init(table: KeyTable? = nil) { self.table = table }

    /// A key pressed: nil for one conchKeyName gives no name (a ⌘ key).
    public mutating func press(_ key: String?) -> PrefixStep {
        guard let t = table, !t.prefix.isEmpty else {
            armed = false
            return .pass
        }
        guard armed else {
            if key == t.prefix {
                armed = true
                return .arm
            }
            return .pass
        }
        armed = false
        guard let key else { return .pass } // ⌘ after the prefix: the menus have it
        // Settled before anything is looked up, as the TUI does: it is how
        // a program below conch gets its own ctrl+b, so nothing may take it.
        if key == t.prefix { return .typePrefix }
        if key.count == 1, let d = Int(key) { return .tab(d == 0 ? 9 : d - 1) }
        if let a = t.action(for: key) { return .run(a) }
        return .sidebar
    }

    /// Forgets a prefix pressed: the window lost the keyboard, a sheet
    /// came up.
    public mutating func disarm() { armed = false }
}

/// A key as it is shown: the arrows as arrows, space as a word — as
/// keymap.Show does.
public func showKey(_ key: String) -> String {
    let named = [" ": "space", "up": "↑", "down": "↓", "left": "←", "right": "→"]
    if let s = named[key] { return s }
    if let plus = key.lastIndex(of: "+"), plus != key.index(before: key.endIndex) {
        let mod = key[..<plus], rest = String(key[key.index(after: plus)...])
        if let s = named[rest] { return mod + "+" + s }
    }
    return key
}

/// The prefix and an action's key as a label — "ctrl+b x" — or nil when
/// the action is unbound or prefix keys are off.
public func prefixHint(_ table: KeyTable?, _ action: String) -> String? {
    guard let t = table, !t.prefix.isEmpty, let k = t.key(for: action) else { return nil }
    return t.prefix + " " + showKey(k)
}

/// The status bar's row while the prefix is pressed: a few keys and what
/// they do, bare (the prefix has been pressed), leaving out any unbound.
public func prefixRow(_ table: KeyTable?) -> [(String, String)] {
    guard let t = table else { return [] }
    let row: [(String, String)] = [("split_right", "split"), ("zoom", "zoom"), ("new_tab", "tab"), ("next_tab", "next"),
                                   ("palette", "go to"), ("help", "keys")]
    return row.compactMap { a, what in t.key(for: a).map { (showKey($0), what) } }
}

/// The actions the app does after the prefix; the rest (scroll mode,
/// layouts, links, sync, detach…) are the TUI's, and the app says so
/// rather than doing nothing.
public let prefixActionsInApp: Set<String> = [
    "split_right", "split_down", "close_split", "focus_left", "focus_right", "focus_up", "focus_down",
    "next_split", "equalize", "zoom", "new_tab", "next_tab", "prev_tab", "move_tab_left", "move_tab_right",
    "close_tab", "rename_tab", "changes_tab", "tab_picker", "palette", "ask", "new_workspace",
    "prev_workspace", "next_workspace", "rename_workspace", "close_workspace", "monitor_quiet",
    "monitor_output", "next_waiting", "help",
]
