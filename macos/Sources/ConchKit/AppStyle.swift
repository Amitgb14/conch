import Foundation

/// How Conch.app draws its chrome, apart from its colours (the theme):
/// the standard window, or Ink, which lays it out as a manga page — ink
/// borders, screentone, square panels with white gutters, a waiting
/// agent's question as a speech bubble (the Conch Desktop Styles mock, E);
/// or Meadow, hand-painted after the anime films — a painted sky behind
/// the toolbar, rounded cards on warm paper, a strip of grass (F).
/// The app's alone, kept in its defaults: the TUI draws cells and has no
/// borders to thicken.
public enum AppStyle: String, CaseIterable, Equatable, Sendable {
    case standard
    case ink
    case meadow

    /// A saved choice; anything else, an older build's or none, is standard.
    public init(setting: String?) {
        self = AppStyle(rawValue: (setting ?? "").trimmingCharacters(in: .whitespaces).lowercased()) ?? .standard
    }

    public var label: String {
        switch self {
        case .standard: return "Standard"
        case .ink: return "Ink"
        case .meadow: return "Meadow"
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
        }
    }

    /// The gutter between panels, and how thick a panel's ink is.
    public var gutter: Double { self == .standard ? 8 : 12 }
    public var border: Double { self == .ink ? 3 : 1 }

    /// How round a card's corners are: square on a page, soft in a meadow.
    public var corner: Double {
        switch self {
        case .standard: return 9
        case .ink: return 0
        case .meadow: return 16
        }
    }

    /// Whether the window paints its own title bar (Meadow's sky), so the
    /// title bar is made transparent over it.
    public var paintsTitleBar: Bool { self == .meadow }
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
