import Foundation

// The tables of config.toml as the Settings window edits them: an array of
// tables ([[actions]], [[permissions]]) as rows to add, change, reorder and
// remove, and tables keyed by name ([sandbox.daytona]) as entries. A field
// inside one is addressed by where it is (SettingsPlace), so the same row
// of a form draws a setting wherever it lives.

/// Where a field's value is: the section's own table, a row of an array of
/// tables (table is that field's key), an entry of tables keyed by name
/// (table is that field's key, "" when the section's table is the keyed one,
/// as [sandbox] is), or one table of fixed fields inside the section's
/// ([trackers.linear]: table is "linear").
public enum SettingsPlace: Hashable, Sendable {
    case top
    case row(table: String, index: Int)
    case entry(table: String, name: String)
    case nested(table: String)
}

extension SettingsField {
    /// Whether the window draws it: the simple kinds, tables and keyed
    /// tables as lists, and a map (a project's check, a key rebound) as
    /// pairs.
    public var drawn: Bool { simple || kind == "tables" || kind == "keyed" || kind == "map" || kind == "table" }
}

extension SettingsForm {
    // MARK: arrays of tables

    /// The rows of an array of tables, each a table of its fields' values.
    public func rows(_ s: SettingsSection, _ f: SettingsField) -> [[String: JSONValue]] {
        (value(s, f)?.array ?? []).map { $0.object ?? [:] }
    }

    mutating func setRows(_ s: SettingsSection, _ f: SettingsField, _ rows: [[String: JSONValue]]) {
        set(s, f, rows.isEmpty ? nil : .array(rows.map(JSONValue.object)))
    }

    /// Adds an empty row at the end and says where it is.
    @discardableResult
    public mutating func addRow(_ s: SettingsSection, _ f: SettingsField) -> Int {
        var r = rows(s, f)
        r.append([:])
        setRows(s, f, r)
        return r.count - 1
    }

    public mutating func removeRow(_ s: SettingsSection, _ f: SettingsField, at i: Int) {
        var r = rows(s, f)
        guard r.indices.contains(i) else { return }
        r.remove(at: i)
        setRows(s, f, r)
    }

    /// Moves row i by delta (−1 up, +1 down), within the list: the order of
    /// permission rules decides which answers.
    public mutating func moveRow(_ s: SettingsSection, _ f: SettingsField, at i: Int, by delta: Int) {
        var r = rows(s, f)
        let j = i + delta
        guard r.indices.contains(i), r.indices.contains(j) else { return }
        r.swapAt(i, j)
        setRows(s, f, r)
    }

    // MARK: tables keyed by name

    /// The keyed tables themselves: the section's table when the field's
    /// key is "" ([sandbox]), else the field's.
    func keyedTable(_ s: SettingsSection, _ f: SettingsField) -> [String: JSONValue] {
        if f.key.isEmpty { return config[s.key]?.object ?? [:] }
        return value(s, f)?.object ?? [:]
    }

    mutating func setKeyedTable(_ s: SettingsSection, _ f: SettingsField, _ t: [String: JSONValue]) {
        if f.key.isEmpty {
            config[s.key] = t.isEmpty ? nil : .object(t)
        } else {
            set(s, f, t.isEmpty ? nil : .object(t))
        }
    }

    /// The names of the entries, sorted.
    public func entries(_ s: SettingsSection, _ f: SettingsField) -> [String] {
        keyedTable(s, f).keys.sorted()
    }

    /// Adds an entry by name; a name already there, or blank, adds nothing.
    /// It says whether it added.
    @discardableResult
    public mutating func addEntry(_ s: SettingsSection, _ f: SettingsField, _ name: String) -> Bool {
        let n = name.trimmingCharacters(in: .whitespaces)
        var t = keyedTable(s, f)
        guard !n.isEmpty, t[n] == nil else { return false }
        t[n] = .object([:])
        setKeyedTable(s, f, t)
        return true
    }

    public mutating func removeEntry(_ s: SettingsSection, _ f: SettingsField, _ name: String) {
        var t = keyedTable(s, f)
        t[name] = nil
        setKeyedTable(s, f, t)
    }

    // MARK: maps

    /// A map's pairs, by key: [verify] commands by project, [keys.bind]
    /// by what the key does. A value that is not text is shown as none.
    public func mapEntries(_ s: SettingsSection, _ f: SettingsField) -> [(key: String, value: String)] {
        (value(s, f)?.object ?? [:]).map { (key: $0.key, value: $0.value.string ?? "") }.sorted { $0.key < $1.key }
    }

    /// Sets one pair of a map; nil takes it out, and a map left empty is
    /// taken out of the file rather than written as an empty table. A
    /// blank key sets nothing. It says whether it changed anything.
    @discardableResult
    public mutating func setMapEntry(_ s: SettingsSection, _ f: SettingsField, _ key: String, _ v: String?) -> Bool {
        let k = key.trimmingCharacters(in: .whitespaces)
        guard !k.isEmpty else { return false }
        var m = value(s, f)?.object ?? [:]
        let next = v.map(JSONValue.string)
        guard m[k] != next else { return false }
        m[k] = next
        set(s, f, m.isEmpty ? nil : .object(m))
        return true
    }

    // MARK: a field wherever it is

    /// The table field a place inside one belongs to.
    func parent(_ s: SettingsSection, _ table: String, kind: String) -> SettingsField? {
        s.fields.first { $0.key == table && $0.kind == kind }
    }

    /// A field's value at a place: what is set there, else its default.
    public func value(_ s: SettingsSection, _ f: SettingsField, at place: SettingsPlace) -> JSONValue? {
        switch place {
        case .top:
            return value(s, f)
        case .row(let table, let i):
            guard let p = parent(s, table, kind: "tables") else { return nil }
            let r = rows(s, p)
            guard r.indices.contains(i) else { return nil }
            return r[i][f.key] ?? f.default
        case .entry(let table, let name):
            guard let p = parent(s, table, kind: "keyed") else { return nil }
            return keyedTable(s, p)[name]?.object?[f.key] ?? f.default
        case .nested(let table):
            guard let p = parent(s, table, kind: "table") else { return nil }
            return value(s, p)?.object?[f.key] ?? f.default
        }
    }

    /// Sets a field at a place; nil takes the key out.
    public mutating func set(_ s: SettingsSection, _ f: SettingsField, at place: SettingsPlace, _ v: JSONValue?) {
        switch place {
        case .top:
            set(s, f, v)
        case .row(let table, let i):
            guard let p = parent(s, table, kind: "tables") else { return }
            var r = rows(s, p)
            guard r.indices.contains(i) else { return }
            r[i][f.key] = v
            setRows(s, p, r)
        case .entry(let table, let name):
            guard let p = parent(s, table, kind: "keyed") else { return }
            var t = keyedTable(s, p)
            guard var e = t[name]?.object else { return }
            e[f.key] = v
            t[name] = .object(e)
            setKeyedTable(s, p, t)
        case .nested(let table):
            // A table emptied is taken out of the file rather than written
            // as [trackers.linear] with nothing under it.
            guard let p = parent(s, table, kind: "table") else { return }
            var o = value(s, p)?.object ?? [:]
            o[f.key] = v
            set(s, p, o.isEmpty ? nil : .object(o))
        }
    }
}

/// A row in a few words, for its heading: an action by its name, a
/// permission rule as what it answers to what, anything else by its first
/// value. "New" names a row with nothing in it yet.
public func settingsRowTitle(_ table: String, _ row: [String: JSONValue]) -> String {
    func s(_ k: String) -> String { row[k]?.string?.trimmingCharacters(in: .whitespaces) ?? "" }
    switch table {
    case "actions":
        if !s("name").isEmpty { return s("name") }
        if !s("run").isEmpty { return s("run") }
        return "New action"
    case "permissions":
        let tool = s("tool"), match = s("match"), action = s("action")
        if tool.isEmpty && match.isEmpty && action.isEmpty { return "New rule" }
        let verb = action.isEmpty ? "allow" : action
        let what = [tool.isEmpty ? "any tool" : tool, match].filter { !$0.isEmpty }.joined(separator: " ")
        return "\(verb.prefix(1).uppercased() + verb.dropFirst()) \(what)"
    default:
        for k in row.keys.sorted() { if let v = row[k]?.string, !v.isEmpty { return v } }
        return "New"
    }
}

/// What a row adds beside its heading: where an action is offered, and who
/// a permission rule is for.
public func settingsRowDetail(_ table: String, _ row: [String: JSONValue]) -> String {
    switch table {
    case "actions":
        let on = (row["on"]?.array ?? []).compactMap(\.string)
        return on.isEmpty ? "every row" : on.joined(separator: ", ")
    case "permissions":
        let agent = row["agent"]?.string ?? "", project = row["project"]?.string ?? ""
        return [agent.isEmpty ? "every agent" : agent, project.isEmpty ? "every project" : project].joined(separator: " · ")
    default:
        return ""
    }
}

/// The names a keyed field offers to add: its suggestions not added yet.
public func entrySuggestions(_ f: SettingsField, have: [String]) -> [String] {
    (f.suggest ?? []).filter { !have.contains($0) }
}
