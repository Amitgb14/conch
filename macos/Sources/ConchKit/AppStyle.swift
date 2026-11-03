import Foundation

/// How Conch.app draws its chrome, apart from its colours (the theme):
/// the standard window, or Ink, which lays it out as a manga page — ink
/// borders, screentone, square panels with white gutters, a waiting
/// agent's question as a speech bubble (the Conch Desktop Styles mock, E).
/// The app's alone, kept in its defaults: the TUI draws cells and has no
/// borders to thicken.
public enum AppStyle: String, CaseIterable, Equatable, Sendable {
    case standard
    case ink

    /// A saved choice; anything else, an older build's or none, is standard.
    public init(setting: String?) {
        self = AppStyle(rawValue: (setting ?? "").trimmingCharacters(in: .whitespaces).lowercased()) ?? .standard
    }

    public var label: String {
        switch self {
        case .standard: return "Standard"
        case .ink: return "Ink"
        }
    }

    /// The theme the style is drawn for, which choosing it puts on: Ink's
    /// borders are its text colour, so any theme works, but black on paper
    /// is the page it was drawn as. nil leaves the theme alone.
    public var theme: String? {
        switch self {
        case .standard: return nil
        case .ink: return "ink"
        }
    }

    /// The gutter between panels, and how thick a panel's ink is.
    public var gutter: Double { self == .ink ? 12 : 8 }
    public var border: Double { self == .ink ? 3 : 1 }
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
