import Foundation

/// How Conch.app draws its chrome, apart from its colours (the theme):
/// the standard window, or Ink, which lays it out as a manga page — ink
/// borders, screentone, square panels with white gutters, a waiting
/// agent's question as a speech bubble (the Conch Desktop Styles mock, E);
/// or Meadow, hand-painted after the anime films — a painted sky behind
/// the toolbar, rounded cards on warm paper, a strip of grass (F); or
/// Cel, cel-shaded, flat colour and hard shadows, with every agent on a
/// card in a roster across the top and a column of projects for the tree
/// (G); or Terminal, today's window on a terminal's ground — herdr's ink
/// black and hairline borders, and one monospace face for all of it, the
/// layout, pills and teal accent as they are (the Restyle mock, C).
/// The app's alone, kept in its defaults: the TUI draws cells and has no
/// borders to thicken.
public enum AppStyle: String, CaseIterable, Equatable, Sendable {
    case standard
    case ink
    case meadow
    case cel
    case terminal

    /// A saved choice; anything else, an older build's or none, is standard.
    public init(setting: String?) {
        self = AppStyle(rawValue: (setting ?? "").trimmingCharacters(in: .whitespaces).lowercased()) ?? .standard
    }

    public var label: String {
        switch self {
        case .standard: return "Standard"
        case .ink: return "Ink"
        case .meadow: return "Meadow"
        case .cel: return "Cel"
        case .terminal: return "Terminal"
        }
    }

    /// The theme the style is drawn for, which choosing it puts on: Ink's
    /// borders are its text colour, so any theme works, but black on paper
    /// is the page it was drawn as; Meadow's is its day, and its sky turns
    /// to night under a dark theme (Firefly). nil leaves the theme alone.
    public var theme: String? {
        switch self {
        case .standard: return nil
        case .ink: return "ink"
        case .meadow: return "meadow"
        case .cel: return "cel"
        case .terminal: return "terminal"
        }
    }

    /// The gutter between panels, and how thick a panel's ink is.
    public var gutter: Double { self == .standard || self == .terminal ? 8 : 12 }
    public var border: Double {
        switch self {
        case .ink: return 3
        case .cel: return 2.5
        default: return 1
        }
    }

    /// How round a card's corners are: square on a page, soft in a meadow.
    public var corner: Double {
        switch self {
        case .standard, .terminal: return 9
        case .ink: return 0
        case .meadow: return 16
        case .cel: return 12
        }
    }

    /// Whether every word in the window is set in the monospace face, so
    /// the chrome reads as part of the terminals it holds.
    public var mono: Bool { self == .terminal }

    /// Monospace runs wider than the system face: the size a word of that
    /// face is set at to take the room the system face's did, near enough.
    public func fontSize(_ size: Double) -> Double { mono ? (size * 0.93 * 2).rounded() / 2 : size }

    /// The sidebar's width with this face: the tree's names need more room
    /// in monospace, or the right column (branches, counts) is cut.
    public func sidebarWidth(_ w: Double) -> Double { mono ? w + 70 : w }

    /// Whether the window shows its agents as a roster of cards, with a
    /// column of projects in place of the tree until the tree is asked for.
    public var roster: Bool { self == .cel }

    /// Whether a Cel window this wide keeps its right column (the question
    /// and the log): narrower, the column would leave the terminal less
    /// than half the window, so it folds away and the question is still in
    /// the pane's strip and its card.
    public static func celSideFits(width: Double) -> Bool { width >= 1080 }

    /// Whether a Cel window this tall draws its roster's cards whole:
    /// shorter, each is one line — mark, name, state — so the terminal
    /// keeps its room and the window can shrink to its smallest.
    public static func celRosterWhole(height: Double) -> Bool { height >= 620 }

    /// Whether the window paints its own title bar, so the title bar is
    /// made transparent over it: Meadow's sky, and Terminal's panel colour —
    /// macOS's own title bar, lighter than an ink-black window, read as a
    /// grey band over it.
    public var paintsTitleBar: Bool { self == .meadow || self == .terminal }
}

/// Where a meadow's clouds sit in a band `width` wide: one every ~260
/// points, at a height and size that vary but repeat with the width, so a
/// window keeps the same sky as it is resized a little. Each is (x, y,
/// width, height) as fractions of the band, x and y its centre.
public func cloudLayout(width: Double) -> [(x: Double, y: Double, w: Double, h: Double)] {
    let n = max(Int(width / 260), 1)
    return (0..<n).map { i in
        let f = (Double(i) + 0.5) / Double(n)
        let wobble = Double((i * 7) % 5) / 5 // 0, .4, .8, .2, .6, …
        return (x: f, y: 0.35 + wobble * 0.35, w: (110 + wobble * 70) / max(width, 1), h: 0.34 + wobble * 0.12)
    }
}


/// The points of a lettering burst — the jagged balloon a manga count is
/// drawn in — around a rect: `spikes` points out, each with a notch
/// between, the notches `depth` (0–1) of the way in from the edge. Starts
/// at the top middle and goes clockwise; every point stays in the rect.
public func burstPoints(width: Double, height: Double, spikes: Int = 9, depth: Double = 0.16) -> [(x: Double, y: Double)] {
    let n = max(spikes, 3)
    let d = min(max(depth, 0), 0.9)
    let cx = width / 2, cy = height / 2
    var pts: [(Double, Double)] = []
    for i in 0..<(n * 2) {
        let a = -Double.pi / 2 + Double(i) * Double.pi / Double(n)
        let r = i % 2 == 0 ? 1.0 : 1.0 - d
        pts.append((cx + cos(a) * cx * r, cy + sin(a) * cy * r))
    }
    return pts.map { (x: min(max($0.0, 0), width), y: min(max($0.1, 0), height)) }
}
