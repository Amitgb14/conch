import Foundation

/// conch's settings as its settings page has them (internal/configweb):
/// the schema of every key of config.toml, the values, and a save — so the
/// app's Settings panel draws the very form the browser page does, from
/// the same Go schema, and never drifts from it.

/// A JSON value, for config values whose shape the schema says.
public indirect enum JSONValue: Codable, Equatable, Sendable {
    case null, bool(Bool), number(Double), string(String), array([JSONValue]), object([String: JSONValue])

    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { self = .null }
        else if let b = try? c.decode(Bool.self) { self = .bool(b) }
        else if let n = try? c.decode(Double.self) { self = .number(n) }
        else if let s = try? c.decode(String.self) { self = .string(s) }
        else if let a = try? c.decode([JSONValue].self) { self = .array(a) }
        else { self = .object(try c.decode([String: JSONValue].self)) }
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .null: try c.encodeNil()
        case .bool(let b): try c.encode(b)
        case .number(let n):
            // Whole numbers as integers: an int setting must stay one.
            if n.rounded() == n, abs(n) < 1e15 { try c.encode(Int64(n)) } else { try c.encode(n) }
        case .string(let s): try c.encode(s)
        case .array(let a): try c.encode(a)
        case .object(let o): try c.encode(o)
        }
    }

    public var bool: Bool? { if case .bool(let b) = self { return b }; return nil }
    public var number: Double? { if case .number(let n) = self { return n }; return nil }
    public var string: String? { if case .string(let s) = self { return s }; return nil }
    public var array: [JSONValue]? { if case .array(let a) = self { return a }; return nil }
    public var object: [String: JSONValue]? { if case .object(let o) = self { return o }; return nil }
}

public struct SettingsOption: Codable, Equatable, Sendable {
    public var value: String
    public var label: String

    public init(value: String, label: String) {
        self.value = value
        self.label = label
    }
}

public struct SettingsField: Codable, Equatable, Sendable {
    public var key: String
    public var label: String
    public var help: String?
    public var kind: String
    public var options: [SettingsOption]?
    public var placeholder: String?
    public var `default`: JSONValue?
    public var min: Int?
    public var max: Int?
    public var fields: [SettingsField]?
    public var keyLabel: String?
    public var suggest: [String]?

    /// The kinds a form of fields draws; tables, keyed tables and maps are
    /// left to config.toml, edited as text.
    public var simple: Bool { ["bool", "string", "secret", "text", "int", "float", "select", "list", "ints"].contains(kind) }
}

public struct SettingsSection: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var key: String
    public var title: String
    public var description: String
    public var icon: String
    public var fields: [SettingsField]

    /// Whether everything in it is drawn here, or some of it is the page's.
    public var simple: Bool { fields.allSatisfy(\.drawn) }

    /// What the panel says above a section it draws only part of: what is
    /// left (a map, such as [verify]'s commands by project) is edited in
    /// config.toml itself. nil when it draws all of it — tables included,
    /// which are lists here.
    public var tableNote: String? {
        if simple { return nil }
        return fields.allSatisfy { !$0.drawn } ? "These are tables of their own, edited in config.toml."
            : "Some of these are tables, edited in config.toml."
    }
}

public struct SettingsPayload: Codable, Equatable, Sendable {
    public var config: [String: JSONValue]
    public var raw: String
    public var version: String
    public var path: String
    public var exists: Bool
    public var schema: [SettingsSection]
    public var problems: [String]?
}

/// The form: the settings as read, and as edited since.
public struct SettingsForm: Equatable, Sendable {
    public private(set) var payload: SettingsPayload
    public internal(set) var config: [String: JSONValue]

    public init(_ payload: SettingsPayload) {
        self.payload = payload
        config = payload.config
    }

    public var dirty: Bool { config != payload.config }

    /// A field's value: what is set, else its default, else nil (the key is
    /// absent, which config.toml reads as zero or empty). A section of key
    /// "" is the top level.
    public func value(_ s: SettingsSection, _ f: SettingsField) -> JSONValue? {
        let v = s.key.isEmpty ? config[f.key] : config[s.key]?.object?[f.key]
        return v ?? f.default
    }

    public mutating func set(_ s: SettingsSection, _ f: SettingsField, _ v: JSONValue?) {
        if s.key.isEmpty {
            config[f.key] = v
            return
        }
        var table = config[s.key]?.object ?? [:]
        table[f.key] = v
        config[s.key] = .object(table)
    }

    public mutating func revert() { config = payload.config }

    /// What a save sends: the whole configuration, with the version it was
    /// read at, so one made over a newer file is refused rather than lost.
    public func saveBody() throws -> Data {
        struct Save: Encodable {
            var version: String
            var config: [String: JSONValue]
        }
        return try JSONEncoder().encode(Save(version: payload.version, config: config))
    }

    /// Taken in after a save, or a reading of a newer file.
    public mutating func reset(_ p: SettingsPayload) {
        payload = p
        config = p.config
    }
}

/// The token in the settings page's address: its fragment, "#t=…".
public func settingsToken(_ url: URL) -> String? {
    guard let f = url.fragment, f.hasPrefix("t="), f.count > 2 else { return nil }
    return String(f.dropFirst(2))
}

/// A list setting as the panel edits it: one entry a line.
public func listText(_ v: JSONValue?) -> String {
    (v?.array ?? []).compactMap { $0.string ?? $0.number.map { String(Int($0)) } }.joined(separator: "\n")
}

public func listValue(_ text: String, ints: Bool = false) -> JSONValue {
    let lines = text.split(whereSeparator: { $0 == "\n" || $0 == "," }).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
    return .array(lines.compactMap { ints ? Double($0).map(JSONValue.number) : .string($0) })
}

/// Whether a save changed conch's theme for the app: [ui] theme, or the
/// light_theme the app takes under the light appearance.
public func savedThemeChanged(_ before: [String: JSONValue], _ after: [String: JSONValue]) -> Bool {
    let b = before["ui"]?.object ?? [:], a = after["ui"]?.object ?? [:]
    return ["theme", "light_theme"].contains { b[$0] != a[$0] }
}

/// What saving says when the theme chosen is not the one on screen now:
/// a light_theme chosen while macOS is dark (or theme while it is light,
/// with a light_theme set) is right not to show, which looks like nothing
/// happened. nil when what changed is what the window now shows.
public func themeSaveNote(_ before: [String: JSONValue], _ after: [String: JSONValue], dark: Bool) -> String? {
    let b = before["ui"]?.object ?? [:], a = after["ui"]?.object ?? [:]
    let light = a["light_theme"]?.string ?? ""
    if dark, b["light_theme"] != a["light_theme"], !light.isEmpty {
        return "Saved — \(ConchTheme.named(light).label) is the window's while macOS is in light appearance; it is dark now"
    }
    if !dark, b["theme"] != a["theme"], !light.isEmpty {
        let t = ConchTheme.named(a["theme"]?.string).label
        return "Saved — \(t) is the window's while macOS is in dark appearance; it is light now, so \(ConchTheme.named(light).label) shows"
    }
    return nil
}

/// A select's options with the value config.toml holds kept among them
/// when it is not one: a light_theme set to a dark theme before only light
/// ones were offered still shows as chosen, as the settings page shows it
/// ("from the file"), so opening Settings does not seem to have changed it.
public func keepingSaved(_ options: [SettingsOption], _ value: String, label: (String) -> String) -> [SettingsOption] {
    guard !value.isEmpty, !options.contains(where: { $0.value == value }) else { return options }
    return options + [SettingsOption(value: value, label: label(value) + " (from the file)")]
}
