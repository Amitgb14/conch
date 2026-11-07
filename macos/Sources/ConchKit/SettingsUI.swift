import Foundation

// How the Settings window draws each setting, decided here so it can be
// tested: which control a field gets (a switch, segments, a menu with
// pictures, a stepper, a slider, a key recorder…), what a value typed into
// one must look like, the sidebar's groups and search, the quiet hours as a
// day, and the autopilot as the line a finished branch takes.

/// The control a setting is drawn with.
public enum SettingsControl: Equatable, Sendable {
    case toggle
    /// A few choices side by side, the chosen one lit.
    case segments
    /// A menu; agents shows each choice with its agent's mark.
    case menu(agents: Bool)
    /// A number with − and + (step), kept within min…max.
    case stepper(step: Double)
    /// A number on a slider, unit after it (text size, in percent).
    case slider(min: Double, max: Double, step: Double, unit: String)
    /// The accent: a dot of each colour.
    case swatches
    /// Pictures of the layout each choice gives ([app] tabs).
    case pictures
    /// A key, recorded by pressing it ([keys] prefix).
    case keys
    /// A time of day, HH:MM.
    case time
    /// An address: http:// or https://.
    case url
    /// The name of an environment variable holding a secret.
    case envVar
    case secret
    /// Numbers as chips, added with return ([notify] limit_at).
    case chips
    case text
    /// Several lines: a command, a list.
    case multiline
    /// The theme pictures.
    case theme
    /// A name typed, or picked from what is offered ([shell] omz_theme:
    /// the themes installed).
    case combo
    /// A model, picked from what the provider lists or typed; summary
    /// puts the small, fast ones first.
    case model(summary: Bool)
    /// [app] icons: each set as a card of the sections' icons in it.
    case iconSets
}

public func settingsControl(_ s: SettingsSection, _ f: SettingsField) -> SettingsControl {
    switch (s.key, f.key) {
    case ("ui", "theme"), ("ui", "light_theme"): return f.kind == "select" ? .theme : .menu(agents: false)
    case ("ui", "accent"): return f.kind == "select" ? .swatches : .text
    case ("app", "tabs"): return f.kind == "select" ? .pictures : .segments
    case ("app", "icons"): return f.kind == "select" ? .iconSets : .text
    case ("app", "text_size"): return .slider(min: 80, max: 160, step: 5, unit: "%")
    case ("keys", "prefix"): return .keys
    case ("notify", "quiet_start"), ("notify", "quiet_end"): return .time
    case ("notify", "limit_at"): return .chips
    case (_, "url"), (_, "base_url"), (_, "api_url"): return .url
    case (_, "api_key_env"): return .envVar
    case ("brain", "model"): return f.kind == "string" ? .model(summary: false) : .text
    case ("brain", "summary_model"): return f.kind == "string" ? .model(summary: true) : .text
    default: break
    }
    if f.kind == "string", let s = f.suggest, !s.isEmpty { return .combo }
    switch f.kind {
    case "bool": return .toggle
    case "select":
        let opts = f.options ?? []
        let agents = ["default", "reviewer", "agent"].contains(f.key)
        if !agents && opts.count >= 2 && opts.count <= 3 && opts.allSatisfy({ $0.label.count <= 30 }) { return .segments }
        return .menu(agents: agents)
    case "int": return .stepper(step: 1)
    case "float": return .stepper(step: 0.5)
    case "secret": return .secret
    case "ints": return .chips
    case "text", "list": return .multiline
    default: return .text
    }
}

// MARK: - Values

/// What is wrong with a value typed into a control, or nil when it will do.
/// An empty value is the setting's default, and always fine.
public func settingsProblem(_ c: SettingsControl, _ f: SettingsField, _ text: String) -> String? {
    let t = text.trimmingCharacters(in: .whitespaces)
    if t.isEmpty { return nil }
    switch c {
    case .url:
        guard let u = URL(string: t), let scheme = u.scheme, ["http", "https"].contains(scheme), u.host != nil else {
            return "an address starting http:// or https://"
        }
        return nil
    case .time:
        return parseTime(t) == nil ? "a time of day, like 22:00" : nil
    case .envVar:
        return t.range(of: #"^[A-Za-z_][A-Za-z0-9_]*$"#, options: .regularExpression) == nil ? "a variable's name: letters, digits and _" : nil
    case .stepper:
        guard let n = Double(t) else { return "a number" }
        if f.kind == "int" && n != n.rounded() { return "a whole number" }
        if let lo = f.min, n < Double(lo) { return "at least \(lo)" }
        if let hi = f.max, n > Double(hi) { return "at most \(hi)" }
        return nil
    default:
        return nil
    }
}

/// A number a stepper moves to: by step, kept within the field's bounds
/// (0 when it gives no lower one, since every number setting here counts
/// something), and rounded so 0.5s don't drift.
public func stepped(_ value: Double?, by step: Double, _ f: SettingsField, fallback: Double = 0) -> Double {
    var n = (value ?? fallback) + step
    n = Swift.max(n, Double(f.min ?? 0))
    if let hi = f.max { n = Swift.min(n, Double(hi)) }
    return (n * 100).rounded() / 100
}

/// Minutes past midnight for HH:MM (or H:MM), nil for anything else.
public func parseTime(_ s: String) -> Int? {
    let parts = s.trimmingCharacters(in: .whitespaces).split(separator: ":", omittingEmptySubsequences: false)
    guard parts.count == 2, parts[1].count == 2, let h = Int(parts[0]), let m = Int(parts[1]),
          (0..<24).contains(h), (0..<60).contains(m) else { return nil }
    return h * 60 + m
}

/// The quiet hours as spans of the day, each from…to as a fraction of it:
/// one span, or two when they pass midnight. None when either end is unset
/// or they are the same time.
public func quietSpans(_ start: String?, _ end: String?) -> [ClosedRange<Double>] {
    guard let a = start.flatMap(parseTime), let b = end.flatMap(parseTime), a != b else { return [] }
    let fa = Double(a) / 1440, fb = Double(b) / 1440
    return a < b ? [fa...fb] : [fa...1, 0...fb]
}

/// The accent colours by name, as the TUI has them (accentColors in
/// internal/tui/theme.go).
public let accentColors: [String: String] = [
    "teal": "#0F7B8A", "blue": "#2F6FDB", "green": "#2E7D4F", "orange": "#C2571A",
    "pink": "#C2407D", "red": "#C0392B", "gray": "#5A6472", "purple": "#7D56F4",
]

// MARK: - Keys

/// A key as the prefix setting writes it (Bubble Tea's notation): ctrl+b,
/// alt+a, ctrl+shift+left. nil for a key alone without ctrl or alt, which
/// would take a letter away from typing.
public func keyNotation(ctrl: Bool, alt: Bool, shift: Bool, key: String) -> String? {
    guard ctrl || alt else { return nil }
    let named: [String: String] = [" ": "space", "\t": "tab", "\r": "enter", "\u{1b}": "esc"]
    let k = named[key] ?? key.lowercased()
    if k.isEmpty { return nil }
    return ([ctrl ? "ctrl" : nil, alt ? "alt" : nil, shift ? "shift" : nil].compactMap { $0 } + [k]).joined(separator: "+")
}

/// A key in that notation as the caps to draw: ctrl+b → ⌃ B.
public func keyCaps(_ notation: String) -> [String] {
    let sym = ["ctrl": "⌃", "alt": "⌥", "shift": "⇧", "cmd": "⌘", "space": "Space", "enter": "↩", "tab": "⇥", "esc": "esc",
               "left": "←", "right": "→", "up": "↑", "down": "↓"]
    return notation.split(separator: "+").map { p in sym[String(p)] ?? p.uppercased() }
}

// MARK: - Sidebar

extension SettingsNav {
    /// The sidebar's groups, each section in the first that names it; one
    /// the list does not know goes in the last, so a new section in the
    /// schema still shows.
    public static let groups: [(String, [String])] = [
        ("General", ["ui", "keys", "notify"]),
        ("Agents", ["agents", skills, "verify", "brain"]),
        ("Workspace", ["git", "shell", "pane", "app"]),
        ("Machines", ["remote", "web", "sandbox"]),
        ("Advanced", ["update", "actions", "permissions"]),
    ]

    /// ids grouped for the sidebar, in the groups' order, empty groups left out.
    public static func grouped(_ ids: [String]) -> [(String, [String])] {
        var out: [(String, [String])] = []
        var placed = Set<String>()
        for (g, members) in groups {
            let here = members.filter { ids.contains($0) && !placed.contains($0) }
            placed.formUnion(here)
            if !here.isEmpty { out.append((g, here)) }
        }
        let rest = ids.filter { !placed.contains($0) }
        if !rest.isEmpty {
            if let i = out.indices.last { out[i].1 += rest } else { out.append(("Settings", rest)) }
        }
        return out
    }

    /// The sections a search finds, by their titles, descriptions and the
    /// labels and help of their settings — "merge" finds Checks & autopilot
    /// and Git. Words are all needed, in any order; empty finds every one.
    public static func search(_ query: String, in schema: [SettingsSection]) -> [String] {
        let all = sidebar(schema.map(\.id))
        let words = query.lowercased().split(whereSeparator: \.isWhitespace).map(String.init)
        if words.isEmpty { return all }
        return all.filter { id in
            let hay: String
            if let s = schema.first(where: { $0.id == id }) {
                hay = ([s.title, s.description] + s.fields.flatMap { [$0.label, $0.help ?? "", $0.key] }).joined(separator: " ").lowercased()
            } else {
                hay = "\(skillsTitle) skill skills mcp server servers library conch-factory conch-team conch-review conch-ship conch-autopilot autopilot".lowercased()
            }
            return words.allSatisfy { hay.contains($0) }
        }
    }
}

/// How many settings of a section differ from what was saved.
public func settingsChanged(_ f: SettingsForm, _ s: SettingsSection) -> Int {
    let saved = SettingsForm(f.payload)
    return s.fields.filter { f.value(s, $0) != saved.value(s, $0) }.count
}

/// How many settings differ from what was saved, across every section.
public func settingsChanged(_ f: SettingsForm) -> Int {
    f.payload.schema.reduce(0) { $0 + settingsChanged(f, $1) }
}

// MARK: - Autopilot

/// A step of the line a finished branch takes ([verify]): what it is, what
/// it says about itself, and whether it is on. key is the setting a click
/// changes; nil for a step that is always there or set elsewhere.
public struct PipelineStep: Equatable, Sendable {
    public var title: String
    public var detail: String
    public var on: Bool
    public var key: String?
}

/// The [verify] section as a line: the agent finishes, the project's check,
/// the reviewer, the pull request, sending CI back, the merge.
public func autopilotSteps(_ v: [String: JSONValue]) -> [PipelineStep] {
    let checks = v["commands"]?.object?.count ?? 0
    let reviewer = v["reviewer"]?.string ?? ""
    let fix = Int(v["auto_fix"]?.number ?? 0)
    return [
        PipelineStep(title: "Agent finishes", detail: "always", on: true, key: nil),
        PipelineStep(title: "Check", detail: checks == 0 ? "no project has one" : checks == 1 ? "1 project" : "\(checks) projects", on: checks > 0, key: nil),
        PipelineStep(title: "Review", detail: reviewer.isEmpty ? "off" : reviewer, on: !reviewer.isEmpty, key: "reviewer"),
        PipelineStep(title: "Pull request", detail: "auto_pr", on: v["auto_pr"]?.bool == true, key: "auto_pr"),
        PipelineStep(title: "Fix CI", detail: fix == 0 ? "off" : fix == 1 ? "1 try" : "\(fix) tries", on: fix > 0, key: "auto_fix"),
        PipelineStep(title: "Merge", detail: "auto_merge", on: v["auto_merge"]?.bool == true, key: "auto_merge"),
    ]
}

/// The value a click on a step sets: a switch flips, the fix count goes
/// between 0 and 2, the reviewer between none and lastReviewer (codex when
/// none was ever chosen). nil for a step a click does not change.
public func autopilotToggle(_ step: PipelineStep, lastReviewer: String?) -> JSONValue? {
    switch step.key {
    case "auto_pr", "auto_merge": return .bool(!step.on)
    case "auto_fix": return .number(step.on ? 0 : 2)
    case "reviewer": return .string(step.on ? "" : (lastReviewer.flatMap { $0.isEmpty ? nil : $0 } ?? "codex"))
    default: return nil
    }
}

// MARK: - Models

/// A model a provider offers (brain.Model), as /api/models lists them.
public struct BrainModel: Codable, Equatable, Sendable {
    public var id: String
    public var label: String
    public var small: Bool?

    public init(id: String, label: String, small: Bool? = nil) {
        self.id = id
        self.label = label
        self.small = small
    }
}

/// What /api/models answers: the models, and why they are only the known
/// ones when they are.
public struct BrainModels: Codable, Equatable, Sendable {
    public var models: [BrainModel]
    public var error: String?

    public init(models: [BrainModel], error: String? = nil) {
        self.models = models
        self.error = error
    }
}

/// The query /api/models is asked with: the provider as the form has it,
/// saved or not.
public func modelsQuery(_ brain: [String: JSONValue]?) -> [URLQueryItem] {
    let b = brain ?? [:]
    return ["provider", "base_url", "api_key_env"].map { URLQueryItem(name: $0, value: b[$0]?.string ?? "") }
}

/// A model menu's choices: the provider's default first (an empty value),
/// then the models — the small ones first for summaries — with a model set
/// in the file and not listed kept, so it still shows as chosen.
public func modelOptions(_ models: [BrainModel], summary: Bool, value: String) -> [SettingsOption] {
    let ordered = summary ? models.filter { $0.small == true } + models.filter { $0.small != true } : models
    var out = [SettingsOption(value: "", label: summary ? "A small, fast model (the default)" : "The provider's default")]
    out += ordered.map { SettingsOption(value: $0.id, label: $0.label == $0.id ? $0.id : "\($0.label) — \($0.id)") }
    return keepingSaved(out, value, label: { $0 })
}
