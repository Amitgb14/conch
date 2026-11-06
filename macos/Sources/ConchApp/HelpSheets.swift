import AppKit
import ConchKit
import SwiftUI

// Help → Keyboard Shortcuts and Help → Check conch…: the TUI's help
// overlay and `conch doctor`, in the app.

/// Every key the app's menus answer to, read off the menus themselves so
/// the sheet cannot fall behind them, and the keys no menu holds.
struct ShortcutsSheet: View {
    @ObservedObject var store: Store

    struct Row: Identifiable { var id: String { menu + title }; var menu: String; var title: String; var keys: String }

    static let others: [(String, String)] = [
        ("⇧PgUp  ⇧PgDn", "scroll a pane's history a page"),
        ("⇧Home  ⇧End", "a pane's oldest line, and back to the live screen"),
        ("⌘-click", "open a link a pane printed"),
        ("drag a split's edge", "resize the splits"),
        ("drag a tab onto another", "move it there"),
        ("⏎  ← →  ↑ ↓", "in the sidebar: open, fold, move"),
    ]

    var rows: [Row] {
        var out: [Row] = []
        for top in NSApp.mainMenu?.items ?? [] {
            for i in top.submenu?.items ?? [] where !i.keyEquivalent.isEmpty && !i.isSeparatorItem {
                let f = i.keyEquivalentModifierMask
                out.append(Row(menu: top.title, title: i.title,
                               keys: shortcutLabel(key: i.keyEquivalent, command: f.contains(.command), shift: f.contains(.shift),
                                                   option: f.contains(.option), control: f.contains(.control))))
            }
        }
        return out
    }

    var body: some View {
        let all = rows
        VStack(alignment: .leading, spacing: 10) {
            Text("Keyboard shortcuts").font(.ui(size: 15, weight: .semibold))
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    ForEach(Array(Set(all.map(\.menu))).sorted { a, b in (all.firstIndex { $0.menu == a } ?? 0) < (all.firstIndex { $0.menu == b } ?? 0) }, id: \.self) { m in
                        VStack(alignment: .leading, spacing: 3) {
                            Text(m.uppercased()).font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(.secondary)
                            ForEach(all.filter { $0.menu == m }) { r in line(r.keys, r.title) }
                        }
                    }
                    if let t = store.prefixKeys.table {
                        // The TUI's prefix table, as conch keys prints it:
                        // what moved in [keys.bind] shows as it is.
                        VStack(alignment: .leading, spacing: 3) {
                            Text("\(t.prefix.uppercased()), THEN").font(.system(size: 10.5, weight: .semibold)).foregroundStyle(.secondary)
                            ForEach(t.keys.filter { !$0.keys.isEmpty }, id: \.action) { b in
                                line(b.keys.map(showKey).joined(separator: "  "),
                                     prefixActionsInApp.contains(b.action) ? b.what : b.what + " (the TUI's)")
                            }
                            line("1 … 9  0", "the tab at that place")
                            line(showKey(t.prefix), "twice: the program gets it")
                        }
                    }
                    VStack(alignment: .leading, spacing: 3) {
                        Text("PANES AND THE MOUSE").font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(.secondary)
                        ForEach(Self.others, id: \.0) { k, what in line(k, what) }
                    }
                }
            }
            .frame(height: 440)
            HStack {
                Text("Agents and terminals keep their own keys inside a pane.").font(.ui(size: 11.5)).foregroundStyle(.secondary)
                Spacer()
                Button("Done") { store.helpOpen = false }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(18).frame(width: 480)
    }

    func line(_ keys: String, _ what: String) -> some View {
        HStack(alignment: .firstTextBaseline) {
            Text(keys).font(.ui(size: 12, design: .monospaced)).frame(width: 170, alignment: .leading)
            Text(what).font(.ui(size: 12.5))
            Spacer(minLength: 0)
        }
    }
}

/// `conch doctor`, its checks and how to fix each.
struct DoctorSheet: View {
    @ObservedObject var store: Store
    @State private var checks: [DoctorCheck]?
    @State private var error = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Check conch").font(.ui(size: 15, weight: .semibold))
            if let c = checks {
                Text(doctorSummary(c)).font(.ui(size: 12.5)).foregroundStyle(c.contains { $0.status == "fail" } ? Theme.err : .secondary)
                ScrollView {
                    VStack(alignment: .leading, spacing: 8) {
                        ForEach(c) { ch in
                            HStack(alignment: .firstTextBaseline, spacing: 8) {
                                Text(ch.mark).font(.ui(size: 13, weight: .bold))
                                    .foregroundStyle(ch.status == "fail" ? Theme.err : ch.status == "warn" ? Theme.warn : Theme.ok).frame(width: 14)
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(ch.name).font(.ui(size: 12.5, weight: .semibold))
                                    Text(ch.detail).font(.ui(size: 12)).foregroundStyle(.secondary).textSelection(.enabled)
                                        .fixedSize(horizontal: false, vertical: true)
                                    if let f = ch.fix, !f.isEmpty {
                                        Text(f).font(.ui(size: 11.5, design: .monospaced)).textSelection(.enabled)
                                            .fixedSize(horizontal: false, vertical: true)
                                    }
                                }
                            }
                        }
                    }
                }
                .frame(height: 380)
            } else if !error.isEmpty {
                Text(error).foregroundStyle(Theme.err).font(.ui(size: 12)).textSelection(.enabled)
            } else {
                HStack(spacing: 6) { ProgressView().controlSize(.small); Text("Checking…").foregroundStyle(.secondary) }
            }
            HStack {
                Text("conch doctor, on this computer").font(.ui(size: 11.5)).foregroundStyle(.secondary)
                Spacer()
                Button("Check again") { run() }.disabled(checks == nil && error.isEmpty)
                Button("Done") { store.doctorOpen = false }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(18).frame(width: 560)
        .onAppear { run() }
    }

    func run() {
        guard let b = store.binary else { error = "conch is not installed where this app can find it"; return }
        checks = nil
        error = ""
        Task {
            do {
                let out = try await runConchAnyExit(b, ["doctor", "-json"])
                checks = try JSONDecoder().decode([DoctorCheck].self, from: out)
            } catch {
                self.error = "conch doctor said nothing readable: \(error)"
            }
        }
    }
}
