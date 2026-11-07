import Foundation
import Testing
@testable import ConchKit

/// A table as conch keys -json prints one: the defaults for a few
/// actions, close_split unbound.
let sampleKeysJSON = """
{
  "prefix": "ctrl+b",
  "keys": [
    {"action": "close_split", "keys": [], "what": "close the split (ends its pane)"},
    {"action": "focus_left", "keys": ["left", "h"], "what": "focus the split to the left"},
    {"action": "layout_even_h", "keys": ["alt+1"], "what": "layout: even horizontal"},
    {"action": "next_layout", "keys": [" "], "what": "next layout"},
    {"action": "split_right", "keys": ["v", "%", "|"], "what": "split right"},
    {"action": "changes_tab", "keys": ["C"], "what": "this split's branch changes in a tab"},
    {"action": "zoom", "keys": ["z"], "what": "zoom the split"}
  ],
  "problems": ["nope is not a key conch binds (conch keys lists them)"]
}
"""

func sampleTable() throws -> KeyTable {
    try JSONDecoder().decode(KeyTable.self, from: Data(sampleKeysJSON.utf8))
}

@Test func keyTableDecodesConchKeysJSON() throws {
    let t = try sampleTable()
    #expect(t.prefix == "ctrl+b")
    #expect(t.action(for: "%") == "split_right")
    #expect(t.action(for: "h") == "focus_left")
    #expect(t.action(for: " ") == "next_layout")
    #expect(t.action(for: "x") == nil, "close_split is unbound, so x does nothing")
    #expect(t.action(for: "Z") == nil, "case matters: Z is not z")
    #expect(t.key(for: "split_right") == "v")
    #expect(t.key(for: "close_split") == nil)
    #expect(t.key(for: "no_such_action") == nil)
    #expect(t.problems?.count == 1)
    // A table with no problems leaves the field out.
    let bare = try JSONDecoder().decode(KeyTable.self, from: Data(#"{"prefix":"ctrl+a","keys":[]}"#.utf8))
    #expect(bare.problems == nil && bare.keys.isEmpty)
}

@Test func eventKeysAreNamedAsTheTUINamesThem() {
    func name(_ code: UInt16, _ c: String?, ctrl: Bool = false, alt: Bool = false, shift: Bool = false, cmd: Bool = false) -> String? {
        conchKeyName(keyCode: code, characters: c, ctrl: ctrl, alt: alt, shift: shift, command: cmd)
    }
    #expect(name(11, "b", ctrl: true) == "ctrl+b")
    #expect(name(11, "B", ctrl: true, shift: true) == "ctrl+b", "no ctrl+B apart from ctrl+b")
    #expect(name(9, "v") == "v")
    #expect(name(23, "%", shift: true) == "%", "shift's character, not shift+5")
    #expect(name(8, "C", shift: true) == "C", "capitals are their own keys")
    #expect(name(18, "1", alt: true) == "alt+1")
    #expect(name(123, "\u{F702}") == "left")
    #expect(name(126, "\u{F700}") == "up")
    #expect(name(116, nil) == "pgup")
    #expect(name(49, " ") == " ")
    #expect(name(49, " ", shift: true) == " ", "shift-space is still space")
    #expect(name(48, "\t", shift: true) == "shift+tab")
    #expect(name(53, "\u{1B}") == "esc")
    #expect(name(11, "b", cmd: true) == nil, "⌘ keys are the menus'")
    #expect(name(11, "b", ctrl: true, cmd: true) == nil)
    #expect(name(0, nil) == nil, "no character, no name")
    #expect(name(0, "") == nil)
    #expect(name(0, "ab") == nil, "a dead key's leftovers are not one key")
}

@Test func prefixThenAKey() throws {
    var p = PrefixKeys(table: try sampleTable())
    #expect(p.press("v") == .pass, "not armed: every key is the program's")
    #expect(p.press("ctrl+b") == .arm)
    #expect(p.armed)
    #expect(p.press("%") == .run("split_right"))
    #expect(!p.armed, "one key after the prefix, then the program's again")
    #expect(p.press("%") == .pass)

    #expect(p.press("ctrl+b") == .arm)
    #expect(p.press("ctrl+b") == .typePrefix, "the prefix twice types it")
    #expect(p.press("ctrl+b") == .arm, "and the next one arms again")
    #expect(p.press("alt+1") == .run("layout_even_h"))
}

@Test func prefixDigitsAreTabs() throws {
    var p = PrefixKeys(table: try sampleTable())
    for (k, want) in [("1", 0), ("9", 8), ("0", 9)] {
        _ = p.press("ctrl+b")
        #expect(p.press(k) == .tab(want), "ctrl+b \(k)")
    }
}

@Test func prefixUnboundGoesToTheSidebar() throws {
    var p = PrefixKeys(table: try sampleTable())
    _ = p.press("ctrl+b")
    #expect(p.press("x") == .sidebar, "close_split is unbound: x is the TUI's 'back to the tree'")
    _ = p.press("ctrl+b")
    #expect(p.press("esc") == .sidebar)
    _ = p.press("ctrl+b")
    #expect(p.press(nil) == .pass, "a ⌘ key after the prefix is the menus'")
    #expect(!p.armed)
}

@Test func prefixOffOrMissing() throws {
    var none = PrefixKeys()
    #expect(none.press("ctrl+b") == .pass, "no table yet: nothing is conch's")
    #expect(!none.armed)
    var empty = PrefixKeys(table: KeyTable(prefix: "", keys: []))
    #expect(empty.press("ctrl+b") == .pass)
    #expect(empty.press("") == .pass, "an empty prefix never matches an empty name")
    // A rebound prefix: ctrl+b is the program's again.
    var a = PrefixKeys(table: KeyTable(prefix: "ctrl+a", keys: [.init(action: "zoom", keys: ["z"], what: "")]))
    #expect(a.press("ctrl+b") == .pass)
    #expect(a.press("ctrl+a") == .arm)
    a.disarm()
    #expect(!a.armed)
    #expect(a.press("z") == .pass, "disarmed: z is the program's")
    // The table going away while armed leaves nothing armed.
    _ = a.press("ctrl+a")
    a.table = nil
    #expect(a.press("z") == .pass)
    #expect(!a.armed)
}

@Test func keysShownAndHinted() throws {
    #expect(showKey(" ") == "space")
    #expect(showKey("left") == "←")
    #expect(showKey("alt+up") == "alt+↑")
    #expect(showKey("ctrl+b") == "ctrl+b")
    #expect(showKey("+") == "+", "the plus key is not a modifier")
    #expect(showKey("alt++") == "alt++")
    let t = try sampleTable()
    #expect(prefixHint(t, "zoom") == "ctrl+b z")
    #expect(prefixHint(t, "next_layout") == "ctrl+b space")
    #expect(prefixHint(t, "focus_left") == "ctrl+b ←")
    #expect(prefixHint(t, "close_split") == nil, "unbound: no hint")
    #expect(prefixHint(nil, "zoom") == nil, "prefix keys off: no hint")
}

@Test func nextSplitGoesRoundAndEqualizeEvens() {
    var l = Layout()
    l.nextSplit() // no tab: nothing to do, nothing to crash
    l.open("a")
    l.nextSplit()
    #expect(l.focused == "a", "one split: stays")
    l.split(.horizontal, with: "b")
    l.split(.vertical, with: "c")
    #expect(l.focused == "c")
    l.nextSplit()
    #expect(l.focused == "a", "after the last, the first")
    l.nextSplit()
    #expect(l.focused == "b")
    l.tabs[l.active].weights[""] = [0.7, 0.3]
    l.equalize()
    #expect(l.tabs[l.active].weights.isEmpty)
    #expect(l.tabs[l.active].shares("", 2) == [0.5, 0.5])
}

@Test func prefixRowShowsWhatIsBound() throws {
    #expect(prefixRow(nil).isEmpty)
    let row = prefixRow(try sampleTable())
    // The sample binds split_right and zoom of the row's actions; the rest
    // are not in it, so they are left out rather than offered keyless.
    #expect(row.map(\.0) == ["v", "z"])
    #expect(row.map(\.1) == ["split", "zoom"])
}

/// Every action the app says it does is a real one: the names are a
/// promise to config.toml (internal/keymap), and a misspelt one here would
/// be a key that silently says "the TUI's".
@Test func appPrefixActionsAreRealActions() {
    let real: Set<String> = [
        "split_right", "split_down", "close_split", "focus_left", "focus_right", "focus_up", "focus_down",
        "next_split", "last_split", "swap_split_left", "swap_split_right", "split_numbers", "equalize",
        "next_layout", "layout_even_h", "layout_even_v", "layout_main_h", "layout_main_v", "layout_tiled",
        "zoom", "back", "new_tab", "changes_tab", "next_tab", "prev_tab", "last_tab", "move_tab",
        "move_tab_left", "move_tab_right", "close_tab", "rename_tab", "tab_picker", "new_workspace",
        "prev_workspace", "next_workspace", "rename_workspace", "close_workspace", "scroll_mode", "redraw",
        "open_links", "monitor_quiet", "monitor_output", "sync_panes", "next_waiting", "palette", "ask",
        "help", "detach",
    ]
    #expect(prefixActionsInApp.isSubset(of: real), "\(prefixActionsInApp.subtracting(real))")
    #expect(!prefixActionsInApp.contains("detach"), "the app has nothing to detach from")
}
