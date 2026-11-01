import AppKit
import ConchKit
import SwiftUI

// Direction A of the design (the Conch Desktop Directions artifact): one
// toolbar with the workspaces, a search field and what is waiting; the
// tree in a sidebar; underlined tabs; splits as cards with gaps; a status
// bar — and C's answer buttons on an agent waiting for you.

enum Theme {
    static let terminal = NSColor(hex: 0x15191E)
    static let fg = NSColor(hex: 0xD5DBE2)
    static let window = Color(nsColor: NSColor(hex: 0x15191E))
    static let chrome = Color(nsColor: NSColor(hex: 0x1F242A))
    static let side = Color(nsColor: NSColor(hex: 0x1A1E24))
    static let raise = Color(nsColor: NSColor(hex: 0x232931))
    static let paneHeader = Color(nsColor: NSColor(hex: 0x181C22))
    static let line = Color(nsColor: NSColor(hex: 0x2D333B))
    static let line2 = Color(nsColor: NSColor(hex: 0x3A414B))
    static let text = Color(nsColor: fg)
    static let text2 = Color(nsColor: NSColor(hex: 0x9AA4AF))
    static let muted = Color(nsColor: NSColor(hex: 0x6E7781))
    static let accent = Color(nsColor: NSColor(hex: 0x2AA5B5))
    static let accentDeep = Color(nsColor: NSColor(hex: 0x0F7B8A))
    static let ok = Color(nsColor: NSColor(hex: 0x3FB950))
    static let warn = Color(nsColor: NSColor(hex: 0xD29922))
    static let err = Color(nsColor: NSColor(hex: 0xF85149))
    static let work = Color(nsColor: NSColor(hex: 0x58A6FF))

    /// An agent's own colour, as the TUI's agentColors.
    static func agent(_ name: String?) -> Color {
        switch name {
        case "claude": return Color(nsColor: NSColor(hex: 0xD97757))
        case "codex": return Color(nsColor: NSColor(hex: 0x10A37F))
        case "gemini": return Color(nsColor: NSColor(hex: 0x4285F4))
        case "opencode": return Color(nsColor: NSColor(hex: 0xC792EA))
        case "devin": return Color(nsColor: NSColor(hex: 0xE3B341))
        default: return muted
        }
    }
}

extension NSColor {
    convenience init(hex: UInt32) {
        self.init(srgbRed: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255,
                  blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
    }
}

// MARK: - pieces

struct Pill: View {
    let text: String
    let fg: Color
    let bg: Color

    var body: some View {
        Text(text).font(.system(size: 11, weight: .semibold)).foregroundStyle(fg)
            .padding(.horizontal, 7).padding(.vertical, 2)
            .background(Capsule().fill(bg))
    }

    static func state(_ s: AgentState?) -> Pill? {
        switch s {
        case .working: return Pill(text: "working", fg: Theme.work, bg: Theme.work.opacity(0.16))
        case .blocked: return Pill(text: "waiting", fg: .black, bg: Theme.warn)
        case .done: return Pill(text: "done", fg: Theme.ok, bg: Theme.ok.opacity(0.16))
        default: return nil
        }
    }
}

struct Dot: View {
    let color: Color
    var body: some View { Circle().fill(color).frame(width: 8, height: 8) }
}

// MARK: - toolbar contents

struct WorkspacesControl: View {
    @ObservedObject var store: Store

    var body: some View {
        HStack(spacing: 2) {
            ForEach(Array(store.workspaces.enumerated()), id: \.offset) { i, ws in
                Text(Workspace.label(i, ws)).font(.system(size: 12))
                    .foregroundStyle(i == store.workspace ? Theme.text : Theme.text2)
                    .padding(.horizontal, 10).padding(.vertical, 3)
                    .background(RoundedRectangle(cornerRadius: 5).fill(i == store.workspace ? Theme.raise : .clear))
                    .contentShape(Rectangle())
                    .onTapGesture { store.workspace = i }
            }
        }
        .padding(2)
        .background(RoundedRectangle(cornerRadius: 7).fill(Theme.window))
        .overlay(RoundedRectangle(cornerRadius: 7).stroke(Theme.line))
        .help("Workspaces, as conch's TUI keeps them")
    }
}

struct SearchField: View {
    @ObservedObject var store: Store

    var body: some View {
        Button { store.paletteOpen = true } label: {
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass").font(.system(size: 12)).foregroundStyle(Theme.muted)
                Text("Search agents, branches, actions").font(.system(size: 13)).foregroundStyle(Theme.muted)
                Spacer(minLength: 8)
                Text("⌘K").font(.system(size: 11, weight: .medium)).foregroundStyle(Theme.muted)
                    .padding(.horizontal, 5).overlay(RoundedRectangle(cornerRadius: 5).stroke(Theme.line2))
            }
            .padding(.horizontal, 10).frame(width: 420, height: 28)
            .background(RoundedRectangle(cornerRadius: 8).fill(Color(nsColor: NSColor(hex: 0x0F1216))))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
        }
        .buttonStyle(.plain)
    }
}

struct Counts: View {
    @ObservedObject var store: Store

    var body: some View {
        let (w, d) = store.snapshot.inbox(in: store.ws)
        HStack(spacing: 8) {
            if w > 0 {
                Button { store.nextWaiting() } label: { Pill(text: "⚑ \(w) waiting", fg: .black, bg: Theme.warn) }
                    .buttonStyle(.plain).help("Go to the next agent waiting for you (⌘.)")
            }
            if d > 0 { Pill(text: "✓ \(d) done", fg: Theme.ok, bg: Theme.ok.opacity(0.16)) }
        }
    }
}

// MARK: - the window

struct MainView: View {
    @ObservedObject var store: Store

    var body: some View {
        ZStack {
            VStack(spacing: 0) {
                HStack(spacing: 0) {
                    if store.sidebarVisible {
                        Sidebar(store: store).frame(width: 270)
                        Rectangle().fill(Color.black.opacity(0.4)).frame(width: 1)
                    }
                    VStack(spacing: 0) {
                        TabBar(store: store)
                        if store.connected, let tab = store.layout.current {
                            SplitArea(store: store, node: tab.root).padding(8)
                        } else {
                            Empty(store: store)
                        }
                    }
                    .background(Theme.window)
                }
                StatusBar(store: store)
            }
            if store.paletteOpen {
                Palette(store: store)
            }
        }
        .background(Theme.window)
        .preferredColorScheme(.dark)
    }
}

struct Empty: View {
    @ObservedObject var store: Store

    var body: some View {
        VStack(spacing: 14) {
            Spacer()
            if store.connected {
                Text("Open an agent or a terminal from the sidebar, or press ⌘T.").foregroundStyle(Theme.text2)
            } else {
                Text(store.status).foregroundStyle(Theme.text2).multilineTextAlignment(.center).frame(maxWidth: 520)
                HStack {
                    Button("Start conch's server") { store.startServer() }.disabled(store.binary == nil)
                    Button("Try again") { store.connect() }
                }
                if store.binary == nil {
                    Text("conch is not installed where this app looks (~/.local/bin, Homebrew, PATH). Set CONCH_BIN to its path.")
                        .font(.system(size: 12)).foregroundStyle(Theme.muted)
                }
            }
            Spacer()
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

// MARK: - sidebar

struct Sidebar: View {
    @ObservedObject var store: Store

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 1) {
                Text("MACHINES").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                    .padding(.horizontal, 8).padding(.top, 10).padding(.bottom, 4)
                ForEach(store.snapshot.tree(in: store.ws)) { node in
                    NodeView(store: store, node: node, depth: 0)
                }
            }
            .padding(8)
        }
        .background(Theme.side)
    }
}

struct NodeView: View {
    @ObservedObject var store: Store
    let node: TreeNode
    let depth: Int
    @State private var open = true

    var paneID: String? {
        switch node.kind {
        case .agent(let id), .terminal(let id): return id
        default: return nil
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            row
            if let id = paneID, let q = store.questions[id] {
                AnswerCard(store: store, pane: id, question: q).padding(.leading, CGFloat(depth) * 14 + 22).padding(.vertical, 3)
            }
            if open, let kids = node.children {
                ForEach(kids) { NodeView(store: store, node: $0, depth: depth + 1) }
            }
        }
    }

    var selected: Bool { paneID != nil && paneID == store.layout.focused }

    var row: some View {
        HStack(spacing: 7) {
            if node.children != nil {
                Image(systemName: open ? "chevron.down" : "chevron.right").font(.system(size: 9, weight: .semibold))
                    .foregroundStyle(Theme.muted).frame(width: 10)
            } else {
                Spacer().frame(width: 10)
            }
            icon
            // The name first: the branch is on the pane's own card, and a
            // narrow sidebar gives it up before the name.
            Text(node.title).font(.system(size: 13))
                .foregroundStyle(isSection ? Theme.text2 : Theme.text).lineLimit(1).truncationMode(.tail)
                .layoutPriority(1)
            Spacer(minLength: 4)
            badge.fixedSize()
        }
        .padding(.leading, CGFloat(depth) * 14 + 6).padding(.trailing, 8).frame(height: 26)
        .background(RoundedRectangle(cornerRadius: 6).fill(selected ? Theme.accentDeep.opacity(0.55) : .clear))
        .contentShape(Rectangle())
        .onTapGesture { activate() }
    }

    var isGroup: Bool { if case .project = node.kind { return true }; return false }
    var isSection: Bool { if case .section = node.kind { return true }; return false }

    @ViewBuilder var icon: some View {
        switch node.kind {
        case .machine: Dot(color: Theme.ok)
        case .project: Text("◆").font(.system(size: 10)).foregroundStyle(Theme.accent)
        case .cli: Text("❯").font(.system(size: 11)).foregroundStyle(Theme.accent)
        case .agent(let id): Dot(color: Theme.agent(store.snapshot.pane(id)?.agent?.name))
        case .terminal: Text("›").foregroundStyle(Theme.muted)
        case .branch: Text("◇").font(.system(size: 11)).foregroundStyle(Theme.text2)
        case .section: EmptyView()
        }
    }

    @ViewBuilder var badge: some View {
        switch node.kind {
        case .agent(let id):
            switch store.snapshot.pane(id)?.agent?.state {
            case .working: Text("⠋").font(.system(size: 12)).foregroundStyle(Theme.work).help("working")
            case .blocked: Pill(text: "waiting", fg: .black, bg: Theme.warn)
            case .done: Text("✓").foregroundStyle(Theme.ok).help("done")
            default: EmptyView()
            }
        case .terminal: if node.badge == "exited" { Text("exited").font(.system(size: 11)).foregroundStyle(Theme.muted) }
        default:
            if !node.badge.isEmpty {
                Text(node.badge).font(.system(size: 12, design: isSection ? .default : .monospaced))
                    .foregroundStyle(node.badge.hasPrefix("⚑") ? Theme.warn : Theme.muted)
            }
        }
    }

    func activate() {
        switch node.kind {
        case .agent(let id), .terminal(let id): store.open(id)
        case .branch(let pr, let name): store.openBranch(project: pr, name: name)
        default: withAnimation(.easeOut(duration: 0.12)) { open.toggle() }
        }
    }
}

/// What a waiting agent asks, with its choices as buttons: answered from
/// here without opening it — direction C's card. The first is the one the
/// agent's own cursor is on.
struct AnswerCard: View {
    @ObservedObject var store: Store
    let pane: String
    let question: Question

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            if !question.text.isEmpty {
                Text(question.text).font(.system(size: 12)).foregroundStyle(Theme.text).lineLimit(3)
            }
            if question.choices.isEmpty {
                Button("Open to answer") { store.open(pane) }.buttonStyle(.plain)
                    .font(.system(size: 12)).foregroundStyle(Theme.accent)
            } else {
                FlowButtons(store: store, pane: pane, question: question)
            }
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 9).fill(Color(nsColor: NSColor(hex: 0x1D2228))))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(Theme.warn.opacity(0.55)))
    }
}

struct FlowButtons: View {
    @ObservedObject var store: Store
    let pane: String
    let question: Question

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            ForEach(question.choices, id: \.choice) { c in
                Button { store.answer(pane, question, c) } label: {
                    Text(c.label).font(.system(size: 12, weight: c.default == true ? .semibold : .regular))
                        .lineLimit(1).truncationMode(.tail)
                        .foregroundStyle(c.default == true ? Color.black : Theme.text)
                        .padding(.horizontal, 9).padding(.vertical, 4)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(RoundedRectangle(cornerRadius: 6).fill(c.default == true ? Theme.warn : Theme.raise))
                        .overlay(RoundedRectangle(cornerRadius: 6).stroke(c.default == true ? Theme.warn : Theme.line2))
                }
                .buttonStyle(.plain)
                .help("Answer “\(c.label)” without opening the agent")
            }
        }
    }
}

// MARK: - tabs

struct TabBar: View {
    @ObservedObject var store: Store

    var body: some View {
        HStack(alignment: .bottom, spacing: 2) {
            ForEach(Array(store.layout.tabs.enumerated()), id: \.element.id) { i, tab in
                TabButton(store: store, index: i, tab: tab)
            }
            Button { store.newTerminal(split: nil) } label: {
                Text("+").font(.system(size: 15)).foregroundStyle(Theme.muted).frame(width: 26, height: 30)
            }
            .buttonStyle(.plain).focusable(false).help("New terminal tab (⌘T)")
            Spacer()
        }
        .padding(.horizontal, 10).frame(height: 36)
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
    }
}

struct TabButton: View {
    @ObservedObject var store: Store
    let index: Int
    let tab: ConchKit.Tab

    var body: some View {
        let on = index == store.layout.active
        let p = store.snapshot.pane(tab.focused)
        HStack(spacing: 7) {
            if let a = p?.agent { Dot(color: Theme.agent(a.name)) } else { Text("›").foregroundStyle(Theme.muted) }
            Text(p?.displayName ?? tab.focused).font(.system(size: 12.5)).lineLimit(1).truncationMode(.tail)
                .frame(maxWidth: 200, alignment: .leading).fixedSize(horizontal: true, vertical: false)
            if tab.root.panes.contains(where: { store.snapshot.pane($0)?.agent?.state == .blocked }) {
                Pill(text: "!", fg: .black, bg: Theme.warn)
            }
            if on {
                Button { store.closeSplit() } label: { Image(systemName: "xmark").font(.system(size: 9, weight: .semibold)) }
                    .buttonStyle(.plain).foregroundStyle(Theme.muted)
            }
        }
        .foregroundStyle(on ? Theme.text : Theme.text2)
        .padding(.horizontal, 12).frame(height: 30)
        .background(UnevenRoundedRectangle(topLeadingRadius: 7, topTrailingRadius: 7).fill(on ? Color(nsColor: NSColor(hex: 0x1B2026)) : .clear))
        .overlay(alignment: .bottom) {
            if on { RoundedRectangle(cornerRadius: 1).fill(Theme.accent).frame(height: 2).padding(.horizontal, 8).offset(y: 1) }
        }
        .contentShape(Rectangle())
        .onTapGesture {
            store.layout.select(index)
            store.focusTerminal()
        }
    }
}

// MARK: - splits

struct SplitArea: View {
    @ObservedObject var store: Store
    let node: SplitNode

    var body: some View {
        switch node {
        case .leaf(let id):
            PaneCard(store: store, pane: id, terminal: store.terminal(for: id))
        case .split(.horizontal, let kids):
            HStack(spacing: 8) { ForEach(Array(kids.enumerated()), id: \.offset) { SplitArea(store: store, node: $0.element) } }
        case .split(.vertical, let kids):
            VStack(spacing: 8) { ForEach(Array(kids.enumerated()), id: \.offset) { SplitArea(store: store, node: $0.element) } }
        }
    }
}

struct PaneCard: View {
    @ObservedObject var store: Store
    let pane: String
    @ObservedObject var terminal: PaneTerminal

    var body: some View {
        let p = store.snapshot.pane(pane)
        let focused = store.layout.focused == pane
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                if let a = p?.agent { Dot(color: Theme.agent(a.name)) } else { Text("›").foregroundStyle(Theme.muted) }
                Text(p?.agent?.name ?? p?.name ?? pane).font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.text)
                if let b = p?.branch, !b.isEmpty {
                    Text(b).font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.text2)
                        .padding(.horizontal, 6).padding(.vertical, 1).background(RoundedRectangle(cornerRadius: 5).fill(Theme.raise))
                }
                if let title = p?.displayName, p?.agent != nil, title != p?.agent?.name {
                    Text(title).font(.system(size: 12)).foregroundStyle(Theme.text2).lineLimit(1)
                }
                Spacer()
                if p?.running == false {
                    Text("exited \(p?.exitCode ?? 0)").font(.system(size: 11)).foregroundStyle(Theme.muted)
                }
                Pill.state(p?.agent?.state)
            }
            .padding(.horizontal, 10).frame(height: 30)
            .background(Theme.paneHeader)
            .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
            if let q = store.questions[pane], !q.choices.isEmpty {
                AnswerStrip(store: store, pane: pane, question: q)
            }
            TerminalHost(terminal: terminal)
        }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: 9))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(focused ? Theme.accent : Theme.line, lineWidth: 1))
        .shadow(color: focused ? Theme.accent.opacity(0.25) : .clear, radius: 0.5)
        .onTapGesture { store.focus(pane) }
    }
}

/// The same answers along the top of the agent's own card.
struct AnswerStrip: View {
    @ObservedObject var store: Store
    let pane: String
    let question: Question

    var body: some View {
        HStack(spacing: 6) {
            Text("⚑").foregroundStyle(Theme.warn)
            Text(question.text.isEmpty ? "Waiting for your answer" : question.text).font(.system(size: 12))
                .foregroundStyle(Theme.text).lineLimit(1)
            Spacer(minLength: 8)
            ForEach(question.choices.prefix(4), id: \.choice) { c in
                Button(c.label) { store.answer(pane, question, c) }
                    .buttonStyle(.plain).font(.system(size: 12, weight: c.default == true ? .semibold : .regular))
                    .lineLimit(1).frame(maxWidth: 200)
                    .foregroundStyle(c.default == true ? Color.black : Theme.text)
                    .padding(.horizontal, 9).padding(.vertical, 3)
                    .background(RoundedRectangle(cornerRadius: 6).fill(c.default == true ? Theme.warn : Theme.raise))
            }
        }
        .padding(.horizontal, 10).padding(.vertical, 6)
        .background(Theme.warn.opacity(0.10))
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.warn.opacity(0.4)).frame(height: 1) }
    }
}

// MARK: - status bar

struct StatusBar: View {
    @ObservedObject var store: Store

    var body: some View {
        HStack(spacing: 16) {
            Text(store.connected ? "● local" : "○ not connected").foregroundStyle(store.connected ? Theme.ok : Theme.muted)
            Text("⌘K search · ⌘T tab · ⌘D split · ⌘. next waiting").foregroundStyle(Theme.muted)
            if let e = store.error {
                Text(e).foregroundStyle(Theme.err).lineLimit(1)
                    .onTapGesture { store.error = nil }
            }
            Spacer()
            if !store.serverVersion.isEmpty { Text("conch \(store.serverVersion)").foregroundStyle(Theme.muted) }
        }
        .font(.system(size: 12))
        .padding(.horizontal, 12).frame(height: 26)
        .background(Color(nsColor: NSColor(hex: 0x13171C)))
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
    }
}

// MARK: - palette

struct Palette: View {
    @ObservedObject var store: Store
    @State private var query = ""
    @State private var sel = 0
    @FocusState private var focused: Bool

    var items: [PaletteItem] { filterPalette(store.snapshot.paletteItems(in: store.ws), query: query) }

    var body: some View {
        ZStack(alignment: .top) {
            Color.black.opacity(0.35).ignoresSafeArea().onTapGesture { store.paletteOpen = false }
            VStack(spacing: 0) {
                HStack(spacing: 10) {
                    Image(systemName: "magnifyingglass").foregroundStyle(Theme.accent)
                    TextField("Agents, branches, projects, commands", text: $query)
                        .textFieldStyle(.plain).font(.system(size: 16)).focused($focused)
                        .onSubmit(run)
                        .onChange(of: query) { _ in sel = 0 }
                    Text(query.isEmpty ? "" : "\(items.count) results").font(.system(size: 12)).foregroundStyle(Theme.muted)
                }
                .padding(.horizontal, 16).padding(.vertical, 14)
                Rectangle().fill(Theme.line).frame(height: 1)
                ScrollViewReader { proxy in
                    ScrollView {
                        VStack(spacing: 0) {
                            ForEach(Array(items.prefix(60).enumerated()), id: \.offset) { i, it in
                                PaletteRow(item: it, selected: i == sel).id(i)
                                    .onTapGesture { sel = i; run() }
                            }
                            if items.isEmpty {
                                Text("Nothing matches “\(query)”").foregroundStyle(Theme.muted).padding(16)
                            }
                        }
                    }
                    .frame(maxHeight: 380)
                    .onChange(of: sel) { proxy.scrollTo($0) }
                }
                Rectangle().fill(Theme.line).frame(height: 1)
                HStack(spacing: 14) {
                    Text("↑↓ move"); Text("⏎ open"); Text("esc close")
                    Spacer()
                }
                .font(.system(size: 11.5)).foregroundStyle(Theme.muted).padding(.horizontal, 16).padding(.vertical, 8)
            }
            .frame(width: 620)
            .background(RoundedRectangle(cornerRadius: 12).fill(Color(nsColor: NSColor(hex: 0x1C2127))))
            .overlay(RoundedRectangle(cornerRadius: 12).stroke(Theme.line2))
            .shadow(color: .black.opacity(0.6), radius: 30, y: 20)
            .padding(.top, 60)
        }
        .onAppear { focused = true }
        .onExitCommand { store.paletteOpen = false }
        .background(KeyCatcher(up: { sel = max(sel - 1, 0) }, down: { sel = min(sel + 1, max(items.count - 1, 0)) }))
    }

    func run() {
        let list = items
        guard list.indices.contains(sel) else { return }
        store.run(list[sel].action)
    }
}

struct PaletteRow: View {
    let item: PaletteItem
    let selected: Bool

    var body: some View {
        HStack(spacing: 10) {
            if item.waiting { Text("⚑").foregroundStyle(Theme.warn) }
            Text(item.title).font(.system(size: 13.5)).foregroundStyle(Theme.text).lineLimit(1)
            if !item.detail.isEmpty { Text(item.detail).font(.system(size: 12)).foregroundStyle(Theme.text2).lineLimit(1) }
            Spacer()
            Text(item.kind).font(.system(size: 12)).foregroundStyle(Theme.muted)
        }
        .padding(.horizontal, 16).padding(.vertical, 7)
        .background(selected ? Theme.accentDeep.opacity(0.7) : .clear)
        .contentShape(Rectangle())
    }
}

/// Arrow keys for the palette while its text field has the keyboard.
struct KeyCatcher: NSViewRepresentable {
    let up: () -> Void
    let down: () -> Void

    final class Coordinator {
        var monitor: Any?
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeNSView(context: Context) -> NSView {
        context.coordinator.monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { e in
            switch e.keyCode {
            case 126: up(); return nil
            case 125: down(); return nil
            default: return e
            }
        }
        return NSView()
    }

    func updateNSView(_ nsView: NSView, context: Context) {}

    static func dismantleNSView(_ nsView: NSView, coordinator: Coordinator) {
        if let m = coordinator.monitor { NSEvent.removeMonitor(m) }
    }
}
