import Foundation

// How the window is laid out and how a pane's terminal is sized, apart
// from drawing either: config.toml's [app] (internal/config AppCfg), the
// tabs grouped by machine for the sidebar, the letterbox a pane is drawn
// in while another window holds its size, the scratch layer's pane, and
// the ports a pane listens on.

// MARK: [app]

/// Where the tabs go.
public enum TabPlacement: String, Equatable, Sendable {
    /// Above the panes, as the TUI's tab bar.
    case top
    /// In the sidebar, above the tree, grouped under each machine.
    case left
    /// Down the far side of the panes.
    case right
}

/// config.toml's [app]: how Conch.app draws its window.
public struct AppLook: Equatable, Sendable {
    public var tabs: TabPlacement = .top
    public var compact = false
    public var glass = false
    public var ports = true
    public var scratchKey = false
    /// The window's own text, as a scale (text_size in percent), and the
    /// terminal's font size in points (font_size), each kept in range.
    public var textScale: Double = 1
    public var fontSize: Double = AppLook.fontDefault

    public static let textRange = 0.8...1.6
    public static let fontRange = 9.0...28.0
    public static let fontDefault = 14.0
    /// The steps ⇧⌘+ and ⇧⌘− move the window's text through, and the
    /// settings page offers.
    public static let textSteps: [Double] = [0.8, 0.9, 1.0, 1.1, 1.25, 1.4, 1.6]

    public init(tabs: TabPlacement = .top, compact: Bool = false, glass: Bool = false, ports: Bool = true, scratchKey: Bool = false) {
        self.tabs = tabs
        self.compact = compact
        self.glass = glass
        self.ports = ports
        self.scratchKey = scratchKey
    }

    /// The next step of the window's text, bigger or smaller, from where it is.
    public static func textStep(from scale: Double, bigger: Bool) -> Double {
        if bigger { return textSteps.first { $0 > scale + 0.001 } ?? textSteps.last! }
        return textSteps.last { $0 < scale - 0.001 } ?? textSteps.first!
    }

    /// The terminal's font a point bigger or smaller, in range.
    public static func fontStep(from size: Double, bigger: Bool) -> Double {
        min(max((size + (bigger ? 1 : -1)).rounded(), fontRange.lowerBound), fontRange.upperBound)
    }

    /// Read from config.toml; what is missing or not understood is the
    /// default, as the Go side reads it.
    public init(toml: String?) {
        tabs = TabPlacement(rawValue: tomlSetting("app", "tabs", in: toml) ?? "") ?? .top
        compact = tomlSetting("app", "density", in: toml) == "compact"
        glass = tomlSetting("app", "sidebar", in: toml) == "glass"
        ports = tomlSetting("app", "ports", in: toml) != "false"
        scratchKey = tomlSetting("app", "scratch_key", in: toml) == "true"
        if let v = tomlSetting("app", "text_size", in: toml).flatMap(Double.init), v > 0 {
            textScale = min(max(v / 100, Self.textRange.lowerBound), Self.textRange.upperBound)
        }
        if let v = tomlSetting("app", "font_size", in: toml).flatMap(Double.init), v > 0 {
            fontSize = min(max(v, Self.fontRange.lowerBound), Self.fontRange.upperBound)
        }
    }
}

// MARK: tabs by machine

/// One machine's tabs in the sidebar, by their place in the layout (for
/// ⌘1–9 and selecting one).
public struct TabGroup: Equatable, Sendable {
    public var machine: String
    public var indices: [Int]
    public init(machine: String, indices: [Int]) {
        self.machine = machine
        self.indices = indices
    }
}

/// The machine a tab belongs to: its focused pane's, or that of the
/// branch, project or files it shows.
public func machine(ofKey k: String) -> String {
    if let c = parseChangesKey(k) { return c.machine }
    if let s = parseSessionsKey(k) { return s.machine }
    if let c = parseCommitsKey(k) { return c.machine }
    if let f = parseFilesKey(k) { return f.machine }
    if let s = parseSetupKey(k) { return s.machine }
    return unkey(k).machine
}

/// The tabs grouped by machine, machines in the order given (this computer
/// first, as the tree has them) and any not in it after, tabs in their
/// own order within each. A machine with no tabs has no group.
public func tabGroups(_ tabs: [Tab], machines order: [String]) -> [TabGroup] {
    var groups: [TabGroup] = []
    for (i, t) in tabs.enumerated() {
        let m = machine(ofKey: t.focused)
        if let g = groups.firstIndex(where: { $0.machine == m }) {
            groups[g].indices.append(i)
        } else {
            groups.append(TabGroup(machine: m, indices: [i]))
        }
    }
    let rank: (String) -> Int = { order.firstIndex(of: $0) ?? order.count }
    let firstSeen = groups.map(\.machine)
    return groups.sorted { a, b in
        let ra = rank(a.machine), rb = rank(b.machine)
        if ra != rb { return ra < rb }
        return (firstSeen.firstIndex(of: a.machine) ?? 0) < (firstSeen.firstIndex(of: b.machine) ?? 0)
    }
}

// MARK: letterbox

/// A pane's size in cells.
public struct CellSize: Equatable, Sendable {
    public var cols: Int
    public var rows: Int
    public init(cols: Int, rows: Int) {
        self.cols = cols
        self.rows = rows
    }
}

/// What a terminal's cells measure, and the padding round its grid, in
/// points.
public struct CellMetrics: Equatable, Sendable {
    public var cellWidth: Double
    public var cellHeight: Double
    public var padX: Double
    public var padY: Double
    public init(cellWidth: Double, cellHeight: Double, padX: Double, padY: Double) {
        self.cellWidth = cellWidth
        self.cellHeight = cellHeight
        self.padX = padX
        self.padY = padY
    }

    /// How many cells a space of this many points holds: what to ask the
    /// server for.
    public func room(width: Double, height: Double) -> CellSize? {
        guard cellWidth > 0, cellHeight > 0 else { return nil }
        let c = Int(((width - 2 * padX) / cellWidth).rounded(.down))
        let r = Int(((height - 2 * padY) / cellHeight).rounded(.down))
        guard c > 0, r > 0 else { return nil }
        return CellSize(cols: c, rows: r)
    }

    /// The points a terminal of exactly this many cells takes, padding
    /// included: the letterbox's terminal.
    public func points(for s: CellSize) -> (width: Double, height: Double) {
        (Double(s.cols) * cellWidth + 2 * padX, Double(s.rows) * cellHeight + 2 * padY)
    }
}

/// Whether a pane is drawn letterboxed: its size is known and is not what
/// this window has room for — another window holds it. Unknown either way
/// is not a letterbox: the terminal fills its space, as before.
public func letterboxed(pane: CellSize?, room: CellSize?) -> Bool {
    guard let pane, let room, pane.cols > 0, pane.rows > 0 else { return false }
    return pane != room
}

/// What the letterbox says over the space it leaves.
public func letterboxNote(_ pane: CellSize) -> String {
    "Sized \(pane.cols)×\(pane.rows) for another window, which was typed in last · click or type here to take it"
}

/// A font size at which a pane of this many cells fits a space, for the
/// palette's preview, which shows a pane whole without resizing it: no
/// bigger than base, and no smaller than a size that can still be read.
public func fittingFontSize(base: Double, metrics: CellMetrics, pane: CellSize, width: Double, height: Double, smallest: Double = 6) -> Double {
    guard metrics.cellWidth > 0, metrics.cellHeight > 0, pane.cols > 0, pane.rows > 0 else { return base }
    let (w, h) = metrics.points(for: pane)
    let scale = min(1, width / w, height / h)
    return max(smallest, (base * scale * 2).rounded(.down) / 2)
}

// MARK: conch attach

/// Whether this conch's attach takes -raw: the program's own output for
/// the pane's terminal to draw (pane.raw.v1), rather than conch's screen.
public func attachRaws(_ usage: String) -> Bool { usage.contains("[-raw]") }

/// Whether a machine's server gives raw output.
public func serverRaws(_ capabilities: [String]?) -> Bool { capabilities?.contains("pane.raw.v1") == true }

// MARK: the scratch layer

/// The name a project's scratch layer pane is given, which is how it is
/// found again, by the TUI too.
public let scratchName = "Scratch"

/// The project's scratch pane if it has one running: named so, in that
/// project (nil: a machine's, outside every project), started by nobody —
/// an agent's helper is not one.
public func scratchPane(_ panes: [PaneInfo], project: String?) -> PaneInfo? {
    panes.first { $0.running && $0.projectID == project && $0.name == scratchName && $0.createdBy == nil && $0.agent == nil }
}

// MARK: ports

/// A listening port as a pane's header says it: ":4317" here, the machine
/// named for one elsewhere.
public func portLabel(_ port: Int, machine: String) -> String {
    machine == "local" ? ":\(port)" : "\(machine):\(port)"
}

/// What reaches a port on another machine from this one: an ssh tunnel,
/// to copy and run, then opened as this computer's own.
public func forwardCommand(host: String, port: Int) -> String {
    "ssh -N -L \(port):localhost:\(port) \(host)"
}

// MARK: quitting

/// What the Conch menu says under Quit: the panes go on without the app.
public func runOnNote(panes: Int) -> String {
    switch panes {
    case 0: return "Nothing is running in conch server."
    case 1: return "1 pane keeps running in conch server. Open Conch again, or the TUI, to see it."
    default: return "\(panes) panes keep running in conch server. Open Conch again, or the TUI, to see them."
    }
}

/// What a find bar says of Ghostty's search: which match of how many.
public func rawSearchNote(selected: Int?, total: Int) -> String {
    if total == 0 { return "Not found" }
    if let s = selected { return "\(s + 1) of \(total)" }
    return total == 1 ? "1 match" : "\(total) matches"
}

/// Whether this conch's attach takes -view: a raw view that never resizes
/// the pane or takes its size, for the palette's preview.
public func attachViews(_ usage: String) -> Bool { usage.contains("[-view]") }

// MARK: who a click gives the keyboard to

/// Who a click in the main window gives the keyboard to.
public enum ClickOwner: Equatable, Sendable {
    /// The terminal it landed in.
    case terminal
    /// The sidebar, whose keys then move its cursor.
    case sidebar
    /// Nobody new: the toolbar, the status bar's buttons (the monitor, the
    /// usage chip, Settings), a split's header — the keyboard stays where
    /// it was. Once every click outside a terminal gave it to the
    /// sidebar, which lit the sidebar's cursor on some row no one had
    /// touched, and took typing away from the pane.
    case unchanged
}

/// Who a click gives the keyboard to, from where it landed: inTerminal
/// when it hit a terminal's view; x and y in the window's points from its
/// bottom left; the sidebar's width when it is on screen (nil when not),
/// and the status bar's height along the bottom.
public func clickOwner(inTerminal: Bool, x: Double, y: Double, sidebarWidth: Double?, statusBar: Double, windowHeight: Double, toolbar: Double) -> ClickOwner {
    if inTerminal { return .terminal }
    guard let w = sidebarWidth, x >= 0, x <= w, y > statusBar, y < windowHeight - toolbar else { return .unchanged }
    return .sidebar
}

/// The room a scaled window's text needs in the sidebar on top of the width
/// dragged to: the default width's share of the scale, so a drag still
/// moves the edge with the pointer.
public func sidebarExtra(scale: Double, base: Double = 250) -> Double { max(0, (scale - 1) * base) }
