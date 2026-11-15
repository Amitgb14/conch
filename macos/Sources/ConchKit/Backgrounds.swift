import Foundation

// A scene behind the window ([app] background): what it is, how it is
// coloured by day and by night, where its pieces are at a moment, and how
// far it shows through the window's surfaces. ConchApp/SceneView.swift
// draws it; everything here is decided without drawing, so it is tested.

/// The scenes the app draws. Each has a day and a night, chosen by the
/// theme (a light theme is day), as Meadow's sky is.
public enum BackgroundScene: String, CaseIterable, Equatable, Sendable {
    case none
    case reef
    case pines
    case lagoon
    case stars

    /// A saved choice; anything else, an older build's or a typo, is none —
    /// a window with no scene, never one somebody did not pick.
    public init(setting: String?) {
        let v = (setting ?? "").trimmingCharacters(in: .whitespaces).lowercased()
        self = BackgroundScene(rawValue: v) ?? .none
    }

    public var label: String {
        switch self {
        case .none: return "No scene"
        case .reef: return "Reef"
        case .pines: return "Snowy pines"
        case .lagoon: return "Lagoon"
        case .stars: return "Starfield"
        }
    }

    /// What config.toml says for it: none is nothing at all.
    public var setting: String? { self == .none ? nil : rawValue }

    /// The sky, top to bottom, by day or night.
    public func sky(night: Bool) -> [RGB] {
        switch (self, night) {
        case (.none, _): return []
        case (.reef, true): return [RGB("#0A4A5C"), RGB("#073246"), RGB("#041A26")]
        case (.reef, false): return [RGB("#8FE0EA"), RGB("#46B8CB"), RGB("#1F8296")]
        case (.pines, true): return [RGB("#0A1322"), RGB("#172741"), RGB("#2A3D5A")]
        case (.pines, false): return [RGB("#A9C9E8"), RGB("#D3E3F2"), RGB("#F0F5FA")]
        case (.lagoon, true): return [RGB("#0E3A47"), RGB("#0A2C38"), RGB("#08222C")]
        case (.lagoon, false): return [RGB("#9BE4EC"), RGB("#55C4D2"), RGB("#3AAEBB")]
        case (.stars, true): return [RGB("#1D1F3D"), RGB("#0B0C1A"), RGB("#05060C")]
        case (.stars, false): return [RGB("#2B2F5A"), RGB("#7A6A9E"), RGB("#E8B4A0")]
        }
    }

    /// The ground along the bottom: coral rock, snow, sand; a starfield has none.
    public func ground(night: Bool) -> RGB? {
        switch (self, night) {
        case (.reef, true): return RGB("#06303A")
        case (.reef, false): return RGB("#1C6F7E")
        case (.pines, true): return RGB("#DFE8F2")
        case (.pines, false): return RGB("#FFFFFF")
        case (.lagoon, true): return RGB("#3B3A2E")
        case (.lagoon, false): return RGB("#EAD8AB")
        default: return nil
        }
    }

    /// How many moving pieces it has: bubbles, flakes, stars.
    public var particles: Int {
        switch self {
        case .none, .lagoon: return 0
        case .reef: return 22
        case .pines: return 90
        case .stars: return 130
        }
    }
}

/// How much of the frame's own colour is laid over the scene.
public enum SceneFrost: String, CaseIterable, Equatable, Sendable {
    case off
    case light
    case strong

    /// "" and anything not understood is light, the default.
    public init(setting: String?) {
        self = SceneFrost(rawValue: (setting ?? "").trimmingCharacters(in: .whitespaces).lowercased()) ?? .light
    }

    /// The opacity of the frame's colour over the scene. Off still keeps a
    /// little, so text on the toolbar and sidebar never sits on it bare.
    public var tint: Double {
        switch self {
        case .off: return 0.3
        case .light: return 0.55
        case .strong: return 0.8
        }
    }

    /// Whether the scene is blurred behind the frame.
    public var blurs: Bool { self != .off }
}

/// [app] background and the settings beside it.
public struct SceneLook: Equatable, Sendable {
    public var scene: BackgroundScene = .none
    /// How much of the scene shows through a pane, 0 (solid) to
    /// SceneLook.throughMax.
    public var through: Double = 0
    public var frost: SceneFrost = .light
    /// Drawn still: [app] background_motion = "still".
    public var still = false

    public static let throughMax = 0.6

    public init(scene: BackgroundScene = .none, through: Double = 0, frost: SceneFrost = .light, still: Bool = false) {
        self.scene = scene
        self.through = min(max(through, 0), Self.throughMax)
        self.frost = frost
        self.still = still
    }

    /// Read from config.toml's [app]; what is missing or not understood is
    /// the default, as the Go side reads it.
    public init(toml: String?) {
        scene = BackgroundScene(setting: tomlSetting("app", "background", in: toml))
        // A whole number of percent, as Go reads it (an int).
        if let v = tomlSetting("app", "background_through", in: toml).flatMap(Int.init) {
            through = min(max(Double(v) / 100, 0), Self.throughMax)
        }
        frost = SceneFrost(setting: tomlSetting("app", "background_frost", in: toml))
        still = tomlSetting("app", "background_motion", in: toml) == "still"
    }

    /// Whether a scene is drawn at all.
    public var on: Bool { scene != .none }

    /// A pane's opacity: solid with no scene, whatever through says.
    public var paneOpacity: Double { on ? 1 - through : 1 }

    /// Whether it moves now: never when asked to be still, when macOS's
    /// Reduce Motion is on, or in Low Power Mode.
    public func moves(reduceMotion: Bool, lowPower: Bool) -> Bool {
        on && !still && !reduceMotion && !lowPower
    }
}

/// One moving piece of a scene, placed once: where across (0…1) and down
/// (0…1), how big (0…1), how long one trip takes in seconds, and where in
/// it the piece starts (0…1).
public struct ScenePiece: Equatable, Sendable {
    public var x, y, size, period, phase: Double
}

public enum SceneMotion {
    /// n pieces, the same for the same seed: the scene is the same scene
    /// every time it is drawn, and still is a frame of it.
    public static func pieces(_ n: Int, seed: UInt64) -> [ScenePiece] {
        var s = seed &* 6364136223846793005 &+ 1442695040888963407
        func next() -> Double {
            s = s &* 6364136223846793005 &+ 1442695040888963407
            return Double(s >> 11) / Double(1 << 53)
        }
        return (0..<max(n, 0)).map { _ in
            ScenePiece(x: next(), y: next(), size: next(), period: 9 + next() * 14, phase: next())
        }
    }

    /// How far through its trip a piece is at time t, 0…1.
    public static func progress(_ p: ScenePiece, at t: Double) -> Double {
        let v = (t / p.period + p.phase).truncatingRemainder(dividingBy: 1)
        return v < 0 ? v + 1 : v
    }

    /// A bubble's height at t, from just below the bottom (1.05) to just
    /// above the top (-0.05).
    public static func rise(_ p: ScenePiece, at t: Double) -> Double { 1.05 - progress(p, at: t) * 1.1 }

    /// A flake's height at t, from just above the top to just below the bottom.
    public static func fall(_ p: ScenePiece, at t: Double) -> Double { -0.05 + progress(p, at: t) * 1.1 }

    /// A sideways sway of at most `by`, either way.
    public static func sway(_ p: ScenePiece, at t: Double, by: Double) -> Double {
        sin((t / p.period * 3 + p.phase) * 2 * .pi) * by
    }

    /// A star's brightness at t, 0.15…1.
    public static func twinkle(_ p: ScenePiece, at t: Double) -> Double {
        let period = 2 + p.size * 5
        return 0.15 + 0.85 * (0.5 + 0.5 * sin((t / period + p.phase) * 2 * .pi))
    }

    /// Across the window once every period seconds, 0…1 then again, with
    /// room to come in and go out of sight (-0.1…1.1).
    public static func cross(at t: Double, period: Double, offset: Double = 0) -> Double {
        let v = (t / period + offset).truncatingRemainder(dividingBy: 1)
        return -0.1 + (v < 0 ? v + 1 : v) * 1.2
    }
}
