import AppKit
import ConchKit
import SwiftUI

// The tabs down a side, as Rex can have them ([app] tabs = "left" or
// "right"): on the left at the top of the sidebar, grouped under each
// machine so a remote tab reads as remote before it is opened, the tree
// below; on the right a column of their own. Each row is the tab bar's
// tab on its side — its number for ⌘1–9, its agent, its name, and what the
// agents in it are doing.

struct SideTabs: View {
    @ObservedObject var store: Store

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            let groups = tabGroups(store.layout.tabs, machines: store.machines.map(\.id))
            ForEach(groups, id: \.machine) { g in
                HStack(spacing: 7) {
                    LucideIcon(g.machine == "local" ? "laptop" : "server", color: Theme.muted, size: 11)
                    Text(g.machine == "local" ? "THIS MAC" : store.label(g.machine).uppercased())
                        .font(.system(size: 11, weight: .semibold)).tracking(0.4).foregroundStyle(Theme.muted)
                    Spacer()
                    Text("\(g.indices.count) tab\(g.indices.count == 1 ? "" : "s")").font(.system(size: 11)).foregroundStyle(Theme.muted)
                }
                .padding(.horizontal, 8).padding(.top, 10).padding(.bottom, 4)
                ForEach(g.indices, id: \.self) { i in
                    if store.layout.tabs.indices.contains(i) {
                        SideTabRow(store: store, index: i, tab: store.layout.tabs[i])
                    }
                }
            }
            if groups.isEmpty {
                Text("No tabs: open an agent or a terminal below, or press ⌘T.")
                    .font(.system(size: 12)).foregroundStyle(Theme.muted).padding(8)
            }
            Button { store.newTerminal(split: nil) } label: {
                HStack(spacing: 7) {
                    Text("+").font(.system(size: 14)).frame(width: 12)
                    Text("New terminal tab").font(.system(size: 12.5))
                    Spacer()
                    Keycap("⌘T")
                }
                .foregroundStyle(Theme.muted).padding(.horizontal, 8).frame(height: 28).contentShape(Rectangle())
            }
            .buttonStyle(.plain).focusable(false)
        }
    }
}

struct SideTabRow: View {
    @ObservedObject var store: Store
    let index: Int
    let tab: ConchKit.Tab
    @State private var hover = false

    var body: some View {
        let on = index == store.layout.active
        let p = store.pane(tab.focused)
        let panes = tab.root.panes.compactMap { store.pane($0) }
        HStack(spacing: 8) {
            Text(index < 9 ? "\(index + 1)" : "").font(.system(size: 10.5, weight: .medium, design: .monospaced))
                .foregroundStyle(Theme.muted).frame(width: 12, alignment: .trailing)
            if let a = p?.agent {
                Mark(agent: a.name)
            } else {
                LucideIcon(name: parseChangesKey(tab.focused) != nil ? .gitBranch : p?.isSSH == true ? .globe : .terminal,
                           color: Theme.muted, size: 12)
            }
            Text(TabButton(store: store, index: index, tab: tab).title(changes: parseChangesKey(tab.focused), pane: p))
                .font(.system(size: 13)).foregroundStyle(on ? Theme.text : Theme.text2).lineLimit(1).truncationMode(.tail)
            Spacer(minLength: 4)
            if tab.root.panes.count > 1 {
                Text("⊞\(tab.root.panes.count)").font(.system(size: 11)).foregroundStyle(Theme.muted).help("\(tab.root.panes.count) splits")
            }
            if panes.contains(where: { $0.agent?.state == .blocked }) {
                // Waiting for you: Rex's dot, in the warn colour.
                Circle().fill(Theme.warn).frame(width: 8, height: 8)
                    .overlay(Circle().stroke(Theme.warn.opacity(0.25), lineWidth: 3))
                    .help("waiting for you")
            } else if panes.contains(where: { $0.agent?.state == .working }) {
                TimelineView(.periodic(from: .now, by: spinnerInterval)) { ctx in
                    Text(spinnerFrame(at: ctx.date)).font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.work)
                }
            } else if panes.contains(where: { $0.agent?.state == .done }) {
                Text("✓").font(.system(size: 12, weight: .bold)).foregroundStyle(Theme.ok).help("finished")
            }
        }
        .padding(.horizontal, 8).frame(height: 30)
        .background(RoundedRectangle(cornerRadius: 7).fill(on ? Theme.accentDeep.opacity(0.45) : hover ? Theme.raise : .clear))
        .overlay(alignment: .leading) {
            if on { UnevenRoundedRectangle(bottomTrailingRadius: 3, topTrailingRadius: 3).fill(Theme.accent).frame(width: 3).padding(.vertical, 7).offset(x: -8) }
        }
        .contentShape(Rectangle())
        .onHover { hover = $0 }
        .onTapGesture {
            store.layout.select(index)
            store.focusTerminal()
        }
        .onTapGesture(count: 2) { store.renameTab(index) }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isButton)
        .accessibilityAction {
            store.layout.select(index)
            store.focusTerminal()
        }
        .contextMenu {
            Button("Rename Tab…") { store.renameTab(index) }
            if tab.name != nil { Button("Name It by Its Pane") { store.layout.rename(index, to: "") } }
            Button("Close Tab") {
                store.layout.select(index)
                store.closeSplit()
            }
        }
        .onDrag { NSItemProvider(object: tab.id.uuidString as NSString) }
        .onDrop(of: [.text], isTargeted: nil) { items in
            guard let item = items.first else { return false }
            _ = item.loadObject(ofClass: NSString.self) { obj, _ in
                guard let id = obj as? String else { return }
                DispatchQueue.main.async {
                    if let from = store.layout.tabs.firstIndex(where: { $0.id.uuidString == id }) {
                        store.layout.moveTab(from: from, to: index)
                    }
                }
            }
            return true
        }
    }
}

/// What stands above the splits when the tabs are down a side: the tab on
/// screen, where it is, and the keys for splitting and the layer.
struct CrumbBar: View {
    @ObservedObject var store: Store

    var body: some View {
        HStack(spacing: 8) {
            if let tab = store.layout.current {
                Text(TabButton(store: store, index: store.layout.active, tab: tab).title(changes: parseChangesKey(tab.focused), pane: store.pane(tab.focused)))
                    .font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.text).lineLimit(1)
                Text(store.scope(of: tab.focused)).font(.system(size: 12)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.head)
            }
            Spacer()
            HStack(spacing: 5) { Keycap("⌘D"); Text("split") }
            HStack(spacing: 5) { Keycap("⌥Space"); Text("layer") }
        }
        .font(.system(size: 12)).foregroundStyle(Theme.muted)
        .padding(.horizontal, 14).frame(height: store.appLook.compact ? 24 : 30)
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
    }
}

/// The sidebar's glass ([app] sidebar = "glass"): the desktop behind the
/// window, blurred, as Finder's sidebar has it.
struct Glass: NSViewRepresentable {
    func makeNSView(context: Context) -> NSVisualEffectView {
        let v = NSVisualEffectView()
        v.material = .sidebar
        v.blendingMode = .behindWindow
        v.state = .followsWindowActiveState
        return v
    }

    func updateNSView(_ v: NSVisualEffectView, context: Context) {}
}
