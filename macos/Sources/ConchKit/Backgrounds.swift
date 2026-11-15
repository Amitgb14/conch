import Foundation

// A scene behind the window ([app] background): what it is, how it is
// coloured by day and by night, where its pieces are at a moment, and how
// far it shows through the window's surfaces. ConchApp/SceneView.swift
// draws it; everything here is decided without drawing, so it is tested.

/// The scenes the app draws. Each has a day and a night, chosen by the
/// theme (a light theme is day), as Meadow's sky is. A scene of your own
/// is a file in $CONCH_HOME/scenes (UserScene), named by its file.
public enum BackgroundScene: Hashable, Sendable {
    case none
    case reef
    case pines
    case lagoon
    case stars
    /// A split-flap board of the window's agents, as departures.
    case board
    /// $CONCH_HOME/scenes/NAME.toml.
    case mine(String)

    /// The scenes conch draws itself, in the order Settings offers them.
    public static let builtIn: [BackgroundScene] = [.none, .reef, .pines, .lagoon, .stars, .board]

    /// A saved choice, among the built-in scenes and the names of your
    /// own (`mine`); anything else, an older build's, a typo or a file
    /// since removed, is none — a window with no scene, never one
    /// somebody did not pick.
    public init(setting: String?, mine: [String] = []) {
        let v = (setting ?? "").trimmingCharacters(in: .whitespaces).lowercased()
        if let b = Self.builtIn.first(where: { $0.rawValue == v }) {
            self = b
        } else if mine.contains(v) {
            self = .mine(v)
        } else {
            self = .none
        }
    }

    /// Its name in config.toml.
    public var rawValue: String {
        switch self {
        case .none: return "none"
        case .reef: return "reef"
        case .pines: return "pines"
        case .lagoon: return "lagoon"
        case .stars: return "stars"
        case .board: return "board"
        case .mine(let n): return n
        }
    }

    public var label: String {
        switch self {
        case .none: return "No scene"
        case .reef: return "Reef"
        case .pines: return "Snowy pines"
        case .lagoon: return "Lagoon"
        case .stars: return "Starfield"
        case .board: return "Departure board"
        case .mine(let n): return n
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
        // A board is a board, day or night.
        case (.board, _): return [RGB("#121417"), RGB("#16191D"), RGB("#121417")]
        // Yours are drawn from your file (UserScene).
        case (.mine, _): return []
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
        case .none, .lagoon, .board, .mine: return 0
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
    /// A project's own scene, by name ([app.project_backgrounds]); .none
    /// there is no scene for that project.
    public var projects: [String: BackgroundScene] = [:]
    /// Fireworks when a pull request merges: on unless
    /// background_fireworks = false.
    public var fireworks = true

    public static let throughMax = 0.6

    public init(scene: BackgroundScene = .none, through: Double = 0, frost: SceneFrost = .light, still: Bool = false) {
        self.scene = scene
        self.through = min(max(through, 0), Self.throughMax)
        self.frost = frost
        self.still = still
    }

    /// Read from config.toml's [app]; what is missing or not understood is
    /// the default, as the Go side reads it.
    public init(toml: String?, mine: [String] = []) {
        scene = BackgroundScene(setting: tomlSetting("app", "background", in: toml), mine: mine)
        // A whole number of percent, as Go reads it (an int).
        if let v = tomlSetting("app", "background_through", in: toml).flatMap(Int.init) {
            through = min(max(Double(v) / 100, 0), Self.throughMax)
        }
        frost = SceneFrost(setting: tomlSetting("app", "background_frost", in: toml))
        still = tomlSetting("app", "background_motion", in: toml) == "still"
        fireworks = tomlSetting("app", "background_fireworks", in: toml) != "false"
        for (name, v) in tomlTable("app.project_backgrounds", in: toml) {
            // A typo is no choice at all, so the window's scene stands;
            // only "none" says no scene.
            let s = BackgroundScene(setting: v, mine: mine)
            if s != .none || v.trimmingCharacters(in: .whitespaces).lowercased() == "none" { projects[name] = s }
        }
    }

    /// Whether a scene is drawn anywhere: the window's, or a project's.
    /// The frame is frosted whenever one is, so that a window moving
    /// between projects does not change its whole look.
    public var on: Bool { scene != .none || projects.values.contains { $0 != .none } }

    /// The scene for a window showing a project (by name): its own, or
    /// the window's.
    public func scene(for project: String?) -> BackgroundScene {
        project.flatMap { projects[$0] } ?? scene
    }

    /// Just the scene drawn for that project, to hand to the drawing.
    public func showing(project: String?) -> SceneLook {
        var l = self
        l.scene = scene(for: project)
        l.projects = [:]
        return l
    }

    /// A pane's opacity: solid with no scene, whatever through says.
    public var paneOpacity: Double { on ? 1 - through : 1 }

    /// Whether it moves now: never when asked to be still, when macOS's
    /// Reduce Motion is on, or in Low Power Mode.
    public func moves(reduceMotion: Bool, lowPower: Bool) -> Bool {
        scene != .none && !still && !reduceMotion && !lowPower
    }
}

/// A TOML table's keys and string values: [app.project_backgrounds], as
/// Go writes it (a key quoted when it is not bare, `"my.site" = "reef"`).
/// Empty when the table is missing.
public func tomlTable(_ table: String, in toml: String?) -> [String: String] {
    guard let toml else { return [:] }
    var out: [String: String] = [:]
    var inside = false
    func unquote(_ v: Substring) -> String {
        var v = v.trimmingCharacters(in: .whitespaces)
        if v.hasPrefix("\""), let close = v.dropFirst().firstIndex(of: "\"") { return String(v[v.index(after: v.startIndex)..<close]) }
        if v.hasPrefix("'"), let close = v.dropFirst().firstIndex(of: "'") { return String(v[v.index(after: v.startIndex)..<close]) }
        if let hash = v.firstIndex(of: "#") { v = String(v[..<hash]).trimmingCharacters(in: .whitespaces) }
        return v
    }
    for raw in toml.split(whereSeparator: \.isNewline) {
        let line = raw.trimmingCharacters(in: .whitespaces)
        if line.hasPrefix("[") {
            let head = line.split(separator: "]", maxSplits: 1).first.map { $0.dropFirst() } ?? ""
            inside = head.trimmingCharacters(in: .whitespaces) == table && !line.hasPrefix("[[")
            continue
        }
        guard inside, !line.hasPrefix("#"), let eq = line.firstIndex(of: "=") else { continue }
        let k = unquote(line[..<eq]), v = unquote(line[line.index(after: eq)...])
        if !k.isEmpty { out[k] = v }
    }
    return out
}

/// A pull request that merged between two readings of its project.
public struct MergedPR: Equatable, Sendable {
    public var project: String
    public var branch: String
    public var number: Int
}

/// The pull requests that were open in old and are merged in new: what
/// the fireworks are for. Only a change seen happen counts — a project
/// read for the first time, or a pull request first seen merged, is
/// history, not news.
public func newlyMerged(old: ProjectInfo?, new: ProjectInfo) -> [MergedPR] {
    guard let old else { return [] }
    let was = Dictionary((old.branches ?? []).compactMap { b in b.pr.map { (b.name, $0) } }, uniquingKeysWith: { a, _ in a })
    return (new.branches ?? []).compactMap { b in
        guard let pr = b.pr, pr.state?.uppercased() == "MERGED",
              let before = was[b.name], before.number == pr.number,
              before.state?.uppercased() == "OPEN" else { return nil }
        return MergedPR(project: new.name, branch: b.name, number: pr.number)
    }
}

/// Fireworks over the scene: where each burst is and how far it has
/// gone, decided here so it is tested; SceneView.swift draws them.
public enum Fireworks {
    /// How long they play, in seconds; the note stays as long.
    public static let duration = 4.0
    /// How long one burst takes.
    public static let burst = 1.4

    /// The bursts: spread across 0.1…0.9, each in a slot of its own so
    /// they do not bunch, down 0.15…0.55 (above the panes' middle), each
    /// starting `phase` of the way through the show; the same every time
    /// for a seed.
    public static func bursts(_ n: Int, seed: UInt64) -> [ScenePiece] {
        SceneMotion.pieces(n, seed: seed).enumerated().map { i, p in
            let x = 0.1 + 0.8 * (Double(i) + 0.2 + 0.6 * p.x) / Double(n)
            return ScenePiece(x: x, y: 0.15 + p.y * 0.4, size: 0.6 + p.size * 0.4, period: burst, phase: p.phase)
        }
    }

    /// How far a burst is at t seconds since the merge: nil before it
    /// starts and after it ends, else 0…1.
    public static func progress(_ p: ScenePiece, at t: Double) -> Double? {
        let start = p.phase * (duration - burst)
        let v = (t - start) / burst
        return v < 0 || v > 1 ? nil : v
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
