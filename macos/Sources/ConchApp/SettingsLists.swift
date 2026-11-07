import ConchKit
import SwiftUI

// config.toml's tables in Settings: an array of tables ([[actions]],
// [[permissions]]) as a list of cards — each headed by what it does in a
// few words, opened to its own fields, moved up or down (the first
// permission rule that matches decides) and removed — and tables keyed by
// name ([sandbox.daytona]) as a card each, added from the names conch
// knows or typed. Every change is the form's, saved with the rest.

/// An array of tables, as cards.
struct TableList: View {
    @ObservedObject var model: SettingsModel
    let section: SettingsSection
    let field: SettingsField
    @State private var open: Set<Int> = []
    @Environment(\.conchTheme) private var theme

    var rows: [[String: JSONValue]] { model.form?.rows(section, field) ?? [] }
    /// The order matters only for rules: the first that matches decides.
    var ordered: Bool { field.key == "permissions" }
    var noun: String { field.key == "permissions" ? "rule" : field.key == "actions" ? "action" : "entry" }

    var body: some View {
        let _ = theme
        let rs = rows
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                Text(field.label.uppercased()).font(.ui(size: 11, weight: .semibold)).tracking(0.6).foregroundStyle(Theme.text2)
                Text("\(rs.count)").font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                    .padding(.horizontal, 6).padding(.vertical, 1).background(Capsule().fill(Theme.well))
                Spacer()
                Button {
                    withAnimation(.easeOut(duration: 0.15)) {
                        if let i = model.form?.addRow(section, field) { open.insert(i) }
                    }
                } label: {
                    Label("Add \(noun)", systemImage: "plus").font(.ui(size: 12, weight: .medium))
                }
                .buttonStyle(.plain).foregroundStyle(Theme.accent)
            }
            if ordered && rs.count > 1 {
                Text("Checked from the top: the first rule that matches answers.").font(.ui(size: 12)).foregroundStyle(Theme.muted)
            }
            if rs.isEmpty {
                VStack(spacing: 6) {
                    Image(systemName: field.key == "permissions" ? "lock.open" : "bolt").font(.system(size: 18)).foregroundStyle(Theme.muted)
                    Text("No \(noun)s yet").font(.ui(size: 13, weight: .medium)).foregroundStyle(Theme.text2)
                    Text(section.description).font(.ui(size: 12)).foregroundStyle(Theme.muted).multilineTextAlignment(.center)
                }
                .frame(maxWidth: .infinity).padding(.vertical, 22)
                .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise))
                .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.line, style: StrokeStyle(lineWidth: 1, dash: [4, 3])))
            }
            ForEach(Array(rs.enumerated()), id: \.offset) { i, row in
                rowCard(i, row, count: rs.count)
            }
        }
    }

    func rowCard(_ i: Int, _ row: [String: JSONValue], count: Int) -> some View {
        let isOpen = open.contains(i)
        return VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 10) {
                if ordered {
                    Text("\(i + 1)").font(.ui(size: 11, weight: .bold, design: .monospaced)).foregroundStyle(Theme.muted)
                        .frame(width: 20, height: 20).background(Circle().fill(Theme.well))
                }
                Image(systemName: "chevron.right").font(.system(size: 10, weight: .bold)).foregroundStyle(Theme.muted)
                    .rotationEffect(.degrees(isOpen ? 90 : 0))
                if field.key == "permissions" {
                    let deny = row["action"]?.string == "deny"
                    Text(deny ? "deny" : "allow").font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(deny ? Theme.err : Theme.ok)
                        .padding(.horizontal, 6).padding(.vertical, 1)
                        .overlay(Capsule().stroke((deny ? Theme.err : Theme.ok).opacity(0.5)))
                }
                VStack(alignment: .leading, spacing: 1) {
                    Text(title(row)).font(.ui(size: 13, weight: .medium, design: field.key == "permissions" ? .monospaced : .default))
                        .foregroundStyle(Theme.text).lineLimit(1)
                    let d = settingsRowDetail(field.key, row)
                    if !d.isEmpty { Text(d).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1) }
                }
                Spacer(minLength: 8)
                if ordered {
                    iconButton("arrow.up", "Move up", disabled: i == 0) { model.form?.moveRow(section, field, at: i, by: -1); swap(i, i - 1) }
                    iconButton("arrow.down", "Move down", disabled: i == count - 1) { model.form?.moveRow(section, field, at: i, by: 1); swap(i, i + 1) }
                }
                iconButton("trash", "Remove") { model.form?.removeRow(section, field, at: i); removed(i) }
            }
            .padding(.horizontal, 14).padding(.vertical, 10)
            .contentShape(Rectangle())
            .onTapGesture { withAnimation(.easeOut(duration: 0.15)) { if isOpen { open.remove(i) } else { open.insert(i) } } }
            if isOpen {
                ForEach(field.fields ?? [], id: \.key) { sub in
                    FieldRow(model: model, section: section, field: sub, place: .row(table: field.key, index: i))
                }
            }
        }
        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(isOpen ? Theme.line2 : Theme.line, lineWidth: 1))
    }

    /// The heading of a row: the words for it, its command for an action
    /// without a name.
    func title(_ row: [String: JSONValue]) -> String {
        let t = settingsRowTitle(field.key, row)
        return field.key == "permissions" ? String(t.drop(while: { $0 != " " }).dropFirst()) : t
    }

    func iconButton(_ name: String, _ help: String, disabled: Bool = false, _ act: @escaping () -> Void) -> some View {
        Button { withAnimation(.easeOut(duration: 0.15)) { act() } } label: {
            Image(systemName: name).font(.system(size: 11, weight: .medium)).frame(width: 24, height: 24).contentShape(Rectangle())
        }
        .buttonStyle(.plain).foregroundStyle(disabled ? Theme.muted.opacity(0.4) : Theme.text2).disabled(disabled).help(help)
    }

    /// Keeps a row open as it moves.
    func swap(_ a: Int, _ b: Int) {
        let ao = open.contains(a), bo = open.contains(b)
        open.remove(a); open.remove(b)
        if ao { open.insert(b) }
        if bo { open.insert(a) }
    }

    /// The rows after one removed move up, and stay as open as they were.
    func removed(_ i: Int) {
        open = Set(open.compactMap { $0 == i ? nil : $0 > i ? $0 - 1 : $0 })
    }
}

/// Tables keyed by name, as cards: a sandbox provider each.
struct KeyedList: View {
    @ObservedObject var model: SettingsModel
    let section: SettingsSection
    let field: SettingsField
    @State private var open: Set<String> = []
    @State private var naming = false
    @State private var name = ""
    @Environment(\.conchTheme) private var theme

    var names: [String] { model.form?.entries(section, field) ?? [] }

    var body: some View {
        let _ = theme
        let ns = names
        let offer = entrySuggestions(field, have: ns)
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                Text(field.label.uppercased()).font(.ui(size: 11, weight: .semibold)).tracking(0.6).foregroundStyle(Theme.text2)
                Text("\(ns.count)").font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                    .padding(.horizontal, 6).padding(.vertical, 1).background(Capsule().fill(Theme.well))
                Spacer()
                Menu {
                    ForEach(offer, id: \.self) { n in Button(n) { add(n) } }
                    if !offer.isEmpty { Divider() }
                    Button("Another, by name…") { naming = true }
                } label: {
                    Label("Add \((field.keyLabel ?? "entry").lowercased())", systemImage: "plus").font(.ui(size: 12, weight: .medium))
                }
                .menuStyle(.borderlessButton).fixedSize().foregroundStyle(Theme.accent)
            }
            if naming {
                HStack(spacing: 8) {
                    TextField(field.keyLabel ?? "Name", text: $name).textFieldStyle(.plain).font(.ui(size: 12.5))
                        .padding(.horizontal, 10).frame(width: 220, height: 30)
                        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
                        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.accent))
                        .onSubmit { add(name) }
                    Button("Add") { add(name) }.disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || ns.contains(name.trimmingCharacters(in: .whitespaces)))
                    Button("Cancel") { naming = false; name = "" }.buttonStyle(.plain).foregroundStyle(Theme.text2)
                }
                .font(.ui(size: 12))
            }
            if ns.isEmpty && !naming {
                VStack(spacing: 6) {
                    Image(systemName: "shippingbox").font(.system(size: 18)).foregroundStyle(Theme.muted)
                    Text("No \((field.keyLabel ?? "entrie").lowercased())s set up").font(.ui(size: 13, weight: .medium)).foregroundStyle(Theme.text2)
                    if !offer.isEmpty {
                        Text("conch knows " + offer.joined(separator: ", ") + ".").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                    }
                }
                .frame(maxWidth: .infinity).padding(.vertical, 22)
                .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise))
                .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.line, style: StrokeStyle(lineWidth: 1, dash: [4, 3])))
            }
            ForEach(ns, id: \.self) { n in card(n) }
        }
    }

    func add(_ n: String) {
        let t = n.trimmingCharacters(in: .whitespaces)
        withAnimation(.easeOut(duration: 0.15)) {
            if model.form?.addEntry(section, field, t) == true { open.insert(t) }
        }
        naming = false
        name = ""
    }

    func card(_ n: String) -> some View {
        let isOpen = open.contains(n)
        let known = (field.suggest ?? []).contains(n)
        return VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 10) {
                Image(systemName: "chevron.right").font(.system(size: 10, weight: .bold)).foregroundStyle(Theme.muted)
                    .rotationEffect(.degrees(isOpen ? 90 : 0))
                Image(systemName: "shippingbox").font(.system(size: 12)).foregroundStyle(Theme.accent)
                Text(n).font(.ui(size: 13, weight: .medium)).foregroundStyle(Theme.text)
                if !known {
                    Text("not a provider this conch knows").font(.ui(size: 10.5)).foregroundStyle(Theme.warn)
                        .padding(.horizontal, 6).padding(.vertical, 1).overlay(Capsule().stroke(Theme.warn.opacity(0.5)))
                }
                Spacer()
                Button { withAnimation(.easeOut(duration: 0.15)) { model.form?.removeEntry(section, field, n); open.remove(n) } } label: {
                    Image(systemName: "trash").font(.system(size: 11, weight: .medium)).frame(width: 24, height: 24).contentShape(Rectangle())
                }
                .buttonStyle(.plain).foregroundStyle(Theme.text2).help("Remove")
            }
            .padding(.horizontal, 14).padding(.vertical, 10)
            .contentShape(Rectangle())
            .onTapGesture { withAnimation(.easeOut(duration: 0.15)) { if isOpen { open.remove(n) } else { open.insert(n) } } }
            if isOpen {
                ForEach(field.fields ?? [], id: \.key) { sub in
                    FieldRow(model: model, section: section, field: sub, place: .entry(table: field.key, name: n))
                }
            }
        }
        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(isOpen ? Theme.line2 : Theme.line, lineWidth: 1))
    }
}
