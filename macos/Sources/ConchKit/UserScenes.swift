import Foundation

// Scenes of your own and the departure board: what a scene file says,
// and what the board shows and how its flaps turn. ConchApp/SceneView.swift
// draws them; everything here is decided without drawing, so it is tested.

/// What moves in a scene of your own: one of the built-in scenes' pieces.
public enum ScenePieces: String, CaseIterable, Equatable, Sendable {
    case none = ""
    case bubbles
    case snow
    case stars
}

/// A scene of your own: $CONCH_HOME/scenes/NAME.toml. It describes a
/// scene — colours, a picture, which pieces move — and is never code, so
/// a file somebody hands you can do nothing but look like something.
///
///     name = "The bay"
///     sky = "#FFB37A #F07A5A #5A3A6A"     # top to bottom, 1 to 4 colours
///     night_sky = "#1A1030 #05040C"       # the day's if left out
///     ground = "#2A1A2A"                  # a band along the bottom
///     image = "bay.jpg"                   # a picture beside this file, filling the window
///     night_image = "bay-night.jpg"
///     pieces = "snow"                     # "bubbles", "snow", "stars"
///     count = 60                          # 1 to 200
public struct UserScene: Equatable, Sendable {
    /// Its day or its night.
    public struct Look: Equatable, Sendable {
        public var sky: [RGB]
        public var ground: RGB?
        /// The picture's whole path, checked to be in the scenes folder.
        public var image: String?
    }

    /// Its file's name: what config.toml calls it.
    public var id: String
    public var label: String
    public var day: Look
    public var night: Look
    public var pieces: ScenePieces
    public var count: Int

    public static let maxScenes = 64
    public static let maxCount = 200
    /// A picture's file may be no bigger: it is held in memory while drawn.
    public static let maxImageBytes = 25 << 20
    static let imageTypes: Set<String> = ["png", "jpg", "jpeg", "heic", "tiff", "gif", "webp"]

    public func look(night n: Bool) -> Look { n ? night : day }

    /// How many pieces it draws: none without a kind.
    public var particles: Int { pieces == .none ? 0 : count }
}

/// One scene file read: the scene, or what is wrong with it. `exists`
/// says whether a file is in the folder and how big, so the reading is
/// tested without a disk.
public func userScene(id: String, text: String, dir: String, size: (String) -> Int?) -> Result<UserScene, ThemeFileError> {
    var v: [String: String] = [:]
    var count: Int?
    for (n, raw) in text.components(separatedBy: "\n").enumerated() {
        let line = raw.trimmingCharacters(in: .whitespaces)
        if line.isEmpty || line.hasPrefix("#") { continue }
        guard let eq = line.firstIndex(of: "=") else { return .failure(.init(message: "line \(n + 1): expected key = \"value\"")) }
        let key = line[..<eq].trimmingCharacters(in: .whitespaces)
        var value = line[line.index(after: eq)...].trimmingCharacters(in: .whitespaces)
        if value.hasPrefix("\"") {
            guard let close = value.dropFirst().firstIndex(of: "\"") else { return .failure(.init(message: "line \(n + 1): a string is not closed")) }
            value = String(value[value.index(after: value.startIndex)..<close])
        } else if let hash = value.firstIndex(of: "#") {
            value = value[..<hash].trimmingCharacters(in: .whitespaces)
        }
        switch key {
        case "name", "sky", "night_sky", "ground", "night_ground", "image", "night_image", "pieces":
            v[key] = value
        case "count":
            guard let c = Int(value), c >= 1, c <= UserScene.maxCount else {
                return .failure(.init(message: "count = \(value) is not a whole number from 1 to \(UserScene.maxCount)"))
            }
            count = c
        default:
            return .failure(.init(message: "\"\(key)\" is not a scene key (name, sky, night_sky, ground, night_ground, image, night_image, pieces, count)"))
        }
    }
    func colour(_ s: String, _ key: String) -> Result<RGB, ThemeFileError> {
        s.range(of: #"^#[0-9A-Fa-f]{6}$"#, options: .regularExpression) != nil
            ? .success(RGB(s)) : .failure(.init(message: "\(key): \"\(s)\" is not a colour; write it as \"#rrggbb\""))
    }
    func sky(_ key: String) -> Result<[RGB]?, ThemeFileError> {
        guard let s = v[key] else { return .success(nil) }
        let words = s.split(separator: " ").map(String.init)
        guard (1...4).contains(words.count) else { return .failure(.init(message: "\(key) is 1 to 4 colours, top to bottom, in one string")) }
        var out: [RGB] = []
        for w in words {
            switch colour(w, key) {
            case .success(let c): out.append(c)
            case .failure(let e): return .failure(e)
            }
        }
        return .success(out)
    }
    func ground(_ key: String) -> Result<RGB?, ThemeFileError> {
        guard let s = v[key], !s.isEmpty else { return .success(nil) }
        return colour(s, key).map { $0 }
    }
    // A picture is a file in the scenes folder itself, by name: never a
    // path, so a scene cannot point at somebody's other files.
    func image(_ key: String) -> Result<String?, ThemeFileError> {
        guard let name = v[key], !name.isEmpty else { return .success(nil) }
        guard !name.contains("/"), !name.hasPrefix("."), name != ".." else {
            return .failure(.init(message: "\(key) = \"\(name)\" is a file beside the scene, by name alone"))
        }
        guard UserScene.imageTypes.contains((name as NSString).pathExtension.lowercased()) else {
            return .failure(.init(message: "\(key) = \"\(name)\" is not a picture (png, jpg, heic, tiff, gif, webp)"))
        }
        guard let bytes = size(name) else { return .failure(.init(message: "\(key) = \"\(name)\" is not in the scenes folder")) }
        guard bytes <= UserScene.maxImageBytes else { return .failure(.init(message: "\(key) = \"\(name)\" is over 25 MB")) }
        return .success((dir as NSString).appendingPathComponent(name))
    }
    let pieces = ScenePieces(rawValue: (v["pieces"] ?? "").lowercased())
    guard let pieces else {
        return .failure(.init(message: "pieces = \"\(v["pieces"]!)\" is not \"bubbles\", \"snow\" or \"stars\""))
    }
    do {
        let daySky = try sky("sky").get(), nightSky = try sky("night_sky").get()
        let dayImage = try image("image").get(), nightImage = try image("night_image").get()
        guard daySky != nil || dayImage != nil else {
            return .failure(.init(message: "a scene needs a sky or an image"))
        }
        let dayGround = try ground("ground").get()
        let nightGround = v["night_ground"] != nil ? try ground("night_ground").get() : dayGround
        // A night left out is the day; a sky left out under a picture is
        // the picture's own dark, for while it loads.
        let day = UserScene.Look(sky: daySky ?? [RGB("#000000")], ground: dayGround, image: dayImage)
        let night = UserScene.Look(sky: nightSky ?? day.sky, ground: nightGround,
                                   image: nightImage ?? (nightSky == nil ? dayImage : nil))
        let name = (v["name"] ?? "").trimmingCharacters(in: .whitespaces)
        return .success(UserScene(id: id, label: name.isEmpty ? id : name, day: day, night: night,
                                  pieces: pieces, count: count ?? 60))
    } catch let e as ThemeFileError {
        return .failure(e)
    } catch {
        return .failure(.init(message: "\(error)"))
    }
}

/// Every scene file in dir ($CONCH_HOME/scenes), with what is wrong with
/// the ones that cannot be used — said, as themes' are, not dropped.
public func userScenes(in dir: String) -> (scenes: [UserScene], problems: [String]) {
    let fm = FileManager.default
    let names = ((try? fm.contentsOfDirectory(atPath: dir)) ?? []).filter { $0.hasSuffix(".toml") }.sorted()
    var out: [UserScene] = []
    var problems: [String] = []
    for file in names {
        let id = String(file.dropLast(5))
        if id.isEmpty || id.range(of: #"^[a-z0-9][a-z0-9_-]*$"#, options: .regularExpression) == nil {
            problems.append("\(file): a scene's file name is its name, in lower case letters, digits, - and _")
            continue
        }
        if BackgroundScene.builtIn.contains(where: { $0.rawValue == id }) {
            problems.append("\(file): \"\(id)\" is a built-in scene's name; call the file something else")
            continue
        }
        if out.count == UserScene.maxScenes {
            problems.append("\(file): more than \(UserScene.maxScenes) scenes of your own; the rest are left out")
            continue
        }
        guard let text = try? String(contentsOfFile: (dir as NSString).appendingPathComponent(file), encoding: .utf8) else {
            problems.append("\(file): could not be read")
            continue
        }
        let size = { (name: String) -> Int? in
            let path = (dir as NSString).appendingPathComponent(name)
            var isDir: ObjCBool = false
            guard fm.fileExists(atPath: path, isDirectory: &isDir), !isDir.boolValue else { return nil }
            return (try? fm.attributesOfItem(atPath: path)[.size] as? Int) ?? nil
        }
        switch userScene(id: id, text: text, dir: dir, size: size) {
        case .success(let s): out.append(s)
        case .failure(let e): problems.append("\(file): \(e.message)")
        }
    }
    return (out, problems)
}

// MARK: the departure board

/// One agent on the board.
public struct Departure: Equatable, Sendable {
    public var time: Date?
    public var agent: String
    /// Where it is going: its project and branch.
    public var to: String
    public var state: AgentState
    public init(time: Date?, agent: String, to: String, state: AgentState) {
        self.time = time
        self.agent = agent
        self.to = to
        self.state = state
    }

    /// A state as a board says it.
    public static func status(_ s: AgentState) -> String {
        switch s {
        case .working: return "BOARDING"
        case .blocked: return "NEEDS YOU"
        case .done: return "ARRIVED"
        case .idle, .unknown: return "ON TIME"
        }
    }
}

/// The board's rows, `width` letters each: a time, the agent, where to,
/// and its status at the end — those waiting on you first, then by time.
/// Rows past `rows` are left off; a board with nobody says so.
public func departureRows(_ ds: [Departure], width: Int, rows: Int, calendar: Calendar = .current) -> [String] {
    guard width > 0, rows > 0 else { return [] }
    let rank: (AgentState) -> Int = { $0 == .blocked ? 0 : $0 == .working ? 1 : 2 }
    let sorted = ds.sorted { a, b in
        rank(a.state) != rank(b.state) ? rank(a.state) < rank(b.state)
            : (a.time ?? .distantFuture) != (b.time ?? .distantFuture) ? (a.time ?? .distantFuture) < (b.time ?? .distantFuture)
            : a.agent < b.agent
    }
    func fit(_ s: String, _ n: Int) -> String {
        n <= 0 ? "" : s.count >= n ? String(s.prefix(n)) : s + String(repeating: " ", count: n - s.count)
    }
    if sorted.isEmpty { return [fit("NO DEPARTURES", width)] }
    let status = 9, time = 5, agent = 8
    return sorted.prefix(rows).map { d in
        let hm = d.time.map { t in
            let c = calendar.dateComponents([.hour, .minute], from: t)
            return String(format: "%02d:%02d", c.hour ?? 0, c.minute ?? 0)
        } ?? "--:--"
        // In capitals, as the flaps have them, cut after: a letter can
        // grow in upper case (ß is SS).
        let line = fit(hm, time) + " " + fit(d.agent.uppercased(), agent) + " "
        let room = width - line.count - status - 1
        guard room > 0 else { return fit(line + Departure.status(d.state), width) }
        return line + fit(d.to.uppercased(), room) + " " + fit(Departure.status(d.state), status)
    }
}

/// A split-flap board: rows of letters, each turning through the
/// alphabet from what it showed to what it is told, one flap at a time,
/// as a station's board does. It is drawn only while some flap turns.
public struct SplitFlap: Equatable, Sendable {
    public static let alphabet: [Character] = Array(" ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789:.-/·")
    /// One flap, in seconds.
    public static let flap = 0.045
    /// Each column starts a little after the one before.
    public static let stagger = 0.012

    public let width: Int
    public let rows: Int
    var from: [[Int]]
    var to: [[Int]]
    var at: Double = 0

    public init(width: Int, rows: Int) {
        self.width = max(width, 0)
        self.rows = max(rows, 0)
        let blank = Array(repeating: Array(repeating: 0, count: self.width), count: self.rows)
        from = blank
        to = blank
    }

    /// A letter as the board has it: upper case, and anything it has no
    /// flap for a dot (a space for a space).
    static func index(_ c: Character) -> Int {
        let u = Character(c.uppercased())
        if u == "_" { return alphabet.firstIndex(of: "-")! }
        return alphabet.firstIndex(of: u) ?? alphabet.firstIndex(of: ".")!
    }

    func target(_ lines: [String]) -> [[Int]] {
        (0..<rows).map { r in
            let l = r < lines.count ? Array(lines[r]) : []
            return (0..<width).map { c in c < l.count ? Self.index(l[c]) : 0 }
        }
    }

    /// Turn to these rows from whatever shows at t.
    public mutating func show(_ lines: [String], at t: Double) {
        let now = indices(at: t), next = target(lines)
        guard next != to || now != next else { return }
        from = now
        to = next
        at = t
    }

    /// These rows at once, no flap turning: a still board, or a first look.
    public mutating func set(_ lines: [String]) {
        to = target(lines)
        from = to
        at = 0
    }

    /// How many flaps a letter turns, going forward only, as real ones do.
    static func flaps(_ a: Int, _ b: Int) -> Int { (b - a + alphabet.count) % alphabet.count }

    func indices(at t: Double) -> [[Int]] {
        (0..<rows).map { r in
            (0..<width).map { c in
                let a = from[r][c], b = to[r][c]
                let gone = t - at - Double(c) * Self.stagger
                // A hair over, so the flap due at `settles` has landed by
                // then rather than a rounding short of it.
                let k = gone <= 0 ? 0 : min(Self.flaps(a, b), Int(gone / Self.flap + 1e-6))
                return (a + k) % Self.alphabet.count
            }
        }
    }

    /// The letters showing at t.
    public func shown(at t: Double) -> [String] {
        indices(at: t).map { String($0.map { Self.alphabet[$0] }) }
    }

    /// When the last flap lands.
    public var settles: Double {
        var end = at
        for r in 0..<rows {
            for c in 0..<width where from[r][c] != to[r][c] {
                end = max(end, at + Double(c) * Self.stagger + Double(Self.flaps(from[r][c], to[r][c])) * Self.flap)
            }
        }
        return end
    }

    /// Whether a flap is still turning at t.
    public func turning(at t: Double) -> Bool { t < settles }
}
