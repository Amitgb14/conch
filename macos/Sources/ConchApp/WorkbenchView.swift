import AppKit
import ConchKit
import SwiftUI

// A workbench: an agent's branch in one tab — its files, a terminal in its
// checkout, the agent as a chat, and its own terminal — arranged by one of
// four layouts (BenchLayout). The parts are the tab's ordinary splits, so
// they resize, focus and close as any split does; the layout only decides
// where each starts and which wait hidden.

extension Store {
    /// The layout a new workbench takes: the last one chosen.
    var benchLayout: BenchLayout {
        get { BenchLayout(setting: UserDefaults.standard.string(forKey: "benchLayout")) }
        set { UserDefaults.standard.set(newValue.rawValue, forKey: "benchLayout") }
    }

    func chat(for k: String) -> ChatModel {
        if let m = chatViews[k] { return m }
        let m = ChatModel(key: k, store: self)
        chatViews[k] = m
        return m
    }

    /// An agent's workbench, opened or brought to the front: the agent's
    /// files at its checkout, a new terminal there, its chat and its own
    /// terminal. One already open is shown as it was left.
    func openWorkbench(_ agent: String, layout l: BenchLayout? = nil) {
        if let i = layout.benchTab(of: agent) {
            layout.select(i)
            if let l, layout.tabs[i].bench != l { setBench(l) }
            return
        }
        let (m, _) = unkey(agent)
        guard let p = pane(agent), p.agent != nil else {
            say("A workbench is for an agent: pick one in the sidebar", .err)
            return
        }
        guard let pid = p.projectID, let proj = snapshots[m]?.project(pid) else {
            say("\(p.displayName) is not in a project, so it has no files to show beside it", .err)
            return
        }
        let fk = filesKey(m, pid)
        let fm = files(for: fk)
        // The agent's own checkout: its worktree, not the project's main one.
        if let cwd = p.cwd, !cwd.isEmpty {
            if let co = fm.choices.first(where: { $0.path == cwd }) {
                if co.path != fm.root { fm.choose(co) }
            } else if fm.info == nil {
                fm.checkoutPath = cwd == proj.path ? nil : cwd
            }
        }
        if fileViews[fk] == nil || fm.dirs.isEmpty { fm.load() }
        let chosen = l ?? benchLayout
        let placeholder = benchShellKey(agent)
        layout.openBench(chosen, BenchParts(files: fk, shell: placeholder, chat: chatKey(agent), agent: agent))
        terminalHasKeyboard = false
        startBenchShell(for: agent, cwd: p.cwd)
    }

    /// The workbench's terminal: a shell in the agent's checkout, put in the
    /// place kept for it once the server has started it.
    func startBenchShell(for agent: String, cwd: String?) {
        let m = unkey(agent).machine
        guard let c = connection(m) else { return }
        if let p = reusableShell(snapshots[m]?.panes ?? [], cwd: cwd, shown: { isShown(key(m, $0)) || $0 == unkey(agent).id }) {
            layout.replace(benchShellKey(agent), with: key(m, p.id))
            return
        }
        struct Create: Encodable { var cwd: String? }
        Task {
            do {
                let p = try await c.call("pane.create", Create(cwd: cwd), as: PaneInfo.self)
                snapshots[m, default: Snapshot()].upsert(p)
                joinWorkspace(machine: m, pane: p)
                layout.replace(benchShellKey(agent), with: key(m, p.id))
            } catch {
                benchShellErrors[agent] = "\(error)"
                objectWillChange.send()
            }
        }
    }

    /// The workbench on screen in another layout; remembered for the next.
    func setBench(_ l: BenchLayout) {
        benchLayout = l
        layout.setBench(l)
    }

    func toggleBench(_ leaf: String) { layout.toggleHidden(leaf) }

    func swapBenchChat() { layout.swapChat() }

    /// Opens a workbench for the agent in focus, or the first agent in the
    /// tab on screen.
    func workbenchForFocused() {
        if let t = layout.current, let a = t.benchAgent {
            openWorkbench(a)
            return
        }
        let candidates = ([layout.focused].compactMap { $0 } + (layout.current?.root.panes ?? []))
            .map { parseChatKey($0) ?? $0 }
        guard let a = candidates.first(where: { pane($0)?.agent != nil }) else {
            say("Focus an agent first: a workbench is opened for one", .err)
            return
        }
        openWorkbench(a)
    }
}

/// The place a workbench's terminal takes while its shell starts, or why it
/// did not.
struct BenchShellPlaceholder: View {
    @ObservedObject var store: Store
    let agent: String

    var body: some View {
        VStack(spacing: 10) {
            if let e = store.benchShellErrors[agent] {
                Text("The terminal did not start: \(e)").font(.ui(size: 12)).foregroundStyle(Theme.err).multilineTextAlignment(.center)
                Button("Try Again") {
                    store.benchShellErrors[agent] = nil
                    store.startBenchShell(for: agent, cwd: store.pane(agent)?.cwd)
                }
            } else {
                ProgressView().controlSize(.small)
                Text("Starting a terminal…").font(.ui(size: 12)).foregroundStyle(Theme.muted)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: CGFloat(Theme.style.corner)))
        .modifier(CardFrame(focused: false))
    }
}

/// Above a workbench (all but JetBrains, which has its strips): where it
/// is, the layout, and a switch for each part.
struct BenchBar: View {
    @ObservedObject var store: Store
    let tab: ConchKit.Tab

    var body: some View {
        let agent = tab.benchAgent ?? ""
        let p = store.pane(agent)
        let parts = store.layout.benchParts(store.layout.active)
        HStack(spacing: 10) {
            if let a = p?.agent { Mark(agent: a.name) }
            Text(store.projectName(of: agent) ?? "Workbench").font(.ui(size: 12)).foregroundStyle(Theme.text2)
            if let b = p?.branch, !b.isEmpty {
                Text("/").foregroundStyle(Theme.muted)
                Text(b).font(.ui(size: 12, weight: .semibold, design: .monospaced)).foregroundStyle(Theme.text).lineLimit(1)
            }
            Pill.state(p?.agent?.state)
            Spacer(minLength: 8)
            if let parts {
                toggle("Files", icon: "folder", leaf: parts.files)
                toggle("Terminal", icon: "terminal", leaf: parts.shell)
                toggle("Chat", icon: "bubble.left.and.bubble.right", leaf: parts.chat)
                toggle("Agent", icon: "apple.terminal", leaf: parts.agent)
                Rectangle().fill(Theme.line).frame(width: 1, height: 16)
            }
            LayoutPicker(store: store, current: tab.bench ?? .ide)
        }
        .padding(.horizontal, 12).frame(height: 34)
        .background(Theme.window)
    }

    func toggle(_ name: String, icon: String, leaf: String) -> some View {
        let on = tab.shows(leaf)
        return Button { store.toggleBench(leaf) } label: {
            Image(systemName: icon).font(.system(size: 12))
                .foregroundStyle(on ? Theme.accent : Theme.muted)
                .frame(width: 26, height: 22)
                .background(RoundedRectangle(cornerRadius: 5).fill(on ? Theme.accent.opacity(0.14) : .clear))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(on ? "Hide \(name)" : "Show \(name)")
        .accessibilityLabel(on ? "Hide \(name)" : "Show \(name)")
    }
}

/// The four layouts, as a segmented control.
struct LayoutPicker: View {
    @ObservedObject var store: Store
    let current: BenchLayout

    var body: some View {
        HStack(spacing: 0) {
            ForEach(BenchLayout.allCases, id: \.self) { l in
                let on = l == current
                Button { store.setBench(l) } label: {
                    Text(l.label).font(.ui(size: 11.5, weight: on ? .semibold : .regular))
                        .foregroundStyle(on ? Theme.text : Theme.text2)
                        .padding(.horizontal, 9).padding(.vertical, 3)
                        .background(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 3 : 5).fill(on ? Theme.window : .clear))
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(l.detail)
                .accessibilityLabel("\(l.label) layout")
            }
        }
        .padding(2)
        .background(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 4 : 7).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 4 : 7).stroke(Theme.line))
    }
}

/// IntelliJ's strip of tool-window icons down an edge of a JetBrains
/// workbench: each opens or closes its window; the lit ones are open.
struct ToolStripe: View {
    @ObservedObject var store: Store
    let tab: ConchKit.Tab
    let leading: Bool

    var body: some View {
        let parts = store.layout.benchParts(store.layout.active)
        VStack(spacing: 4) {
            if let parts {
                if leading {
                    icon("folder", "Project", leaf: parts.files)
                    Spacer()
                    icon("terminal", "Terminal", leaf: parts.shell)
                } else {
                    icon("bubble.left.and.bubble.right", "Agent Chat", leaf: parts.chat, badge: store.questions[parts.agent] != nil)
                    icon("apple.terminal", "Agent Terminal", leaf: parts.agent)
                    Spacer()
                    Menu {
                        ForEach(BenchLayout.allCases, id: \.self) { l in
                            Button((l == tab.bench ? "✓ " : "   ") + l.label) { store.setBench(l) }
                        }
                    } label: { Image(systemName: "rectangle.3.group").font(.system(size: 13)) }
                    .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize()
                    .foregroundStyle(Theme.text2).frame(width: 30, height: 30)
                    .help("Workbench layout")
                }
            }
        }
        .padding(.vertical, 8)
        .frame(width: 40)
        .frame(maxHeight: .infinity)
        .background(Theme.chrome)
        .overlay(alignment: leading ? .trailing : .leading) { Rectangle().fill(Theme.line).frame(width: 1) }
    }

    func icon(_ symbol: String, _ name: String, leaf: String, badge: Bool = false) -> some View {
        let on = tab.shows(leaf)
        return Button { store.toggleBench(leaf) } label: {
            Image(systemName: symbol).font(.system(size: 14))
                .foregroundStyle(on ? Theme.text : Theme.text2)
                .frame(width: 30, height: 30)
                .background(RoundedRectangle(cornerRadius: 6).fill(on ? Theme.raise : .clear))
                .overlay(alignment: .topTrailing) {
                    if badge { Circle().fill(Theme.warn).frame(width: 7, height: 7).padding(4) }
                }
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(on ? "Hide \(name)" : "Show \(name)")
        .accessibilityLabel(on ? "Hide \(name)" : "Show \(name)")
    }
}

/// A workbench tab with its chrome: the bar above, or IntelliJ's strips
/// either side.
struct BenchFrame<Content: View>: View {
    @ObservedObject var store: Store
    let tab: ConchKit.Tab
    @ViewBuilder var content: Content

    var body: some View {
        if tab.bench?.stripes == true {
            HStack(spacing: 0) {
                ToolStripe(store: store, tab: tab, leading: true)
                content
                ToolStripe(store: store, tab: tab, leading: false)
            }
        } else {
            VStack(spacing: 0) {
                BenchBar(store: store, tab: tab)
                Rectangle().fill(Theme.line).frame(height: 1)
                content
            }
        }
    }
}
