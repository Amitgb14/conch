import AppKit
import ConchKit
import SwiftUI

// Direction A of the design (the Conch Desktop Directions artifact): one
// toolbar with the workspaces, a search field and what is waiting; the
// tree in a sidebar; underlined tabs; splits as cards with gaps; a status
// bar — and C's answer buttons on an agent waiting for you.

/// The window's colours: the current conch theme's (ConchKit/Themes.swift),
/// so changing it repaints everything that reads these.
@MainActor
enum Theme {
    static var current = ConchTheme.conch
    /// conch's `[ui] icons`, which decides how agents are marked.
    static var icons = IconMode.text

    static func ns(_ c: RGB) -> NSColor { NSColor(srgbRed: c.r, green: c.g, blue: c.b, alpha: 1) }
    static func color(_ c: RGB) -> Color { Color(nsColor: ns(c)) }

    static var terminal: NSColor { ns(current.bg) }
    static var fg: NSColor { ns(current.fg) }
    static var window: Color { color(current.bg) }
    static var chrome: Color { color(current.chrome) }
    static var side: Color { color(current.sidebar) }
    static var raise: Color { color(current.raised) }
    static var paneHeader: Color { color(current.paneHeader) }
    static var well: Color { color(current.well) }
    static var line: Color { color(current.border) }
    static var line2: Color { color(current.border2) }
    static var text: Color { color(current.fg) }
    static var text2: Color { color(current.fg2) }
    static var muted: Color { color(current.muted) }
    static var accent: Color { color(current.line) }
    static var accentDeep: Color { color(current.accent) }
    static var selectedText: Color { color(current.selFG) }
    static var ok: Color { color(current.ok) }
    static var warn: Color { color(current.warn) }
    static var onWarn: Color { color(current.onWarn) }
    static var err: Color { color(current.err) }
    static var work: Color { color(current.work) }

    /// An agent's own colour (agentColor), or muted for one it has none for.
    static func agent(_ name: String?) -> Color { agentColor(name).map(color) ?? muted }
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
    /// A working agent's: the TUI's spinner turns in front of the text.
    var spins = false

    var body: some View {
        Group {
            if spins {
                TimelineView(.periodic(from: .now, by: spinnerInterval)) { ctx in
                    Text(spinnerFrame(at: ctx.date) + (text.isEmpty ? "" : " " + text))
                }
            } else {
                Text(text)
            }
        }
        .font(.system(size: 11, weight: .semibold)).foregroundStyle(fg)
        .padding(.horizontal, 7).padding(.vertical, 2)
        .background(Capsule().fill(bg))
    }

    static func state(_ s: AgentState?) -> Pill? {
        switch s {
        case .working: return Pill(text: "working", fg: Theme.work, bg: Theme.work.opacity(0.16), spins: true)
        case .blocked: return Pill(text: "waiting", fg: Theme.onWarn, bg: Theme.warn)
        case .done: return Pill(text: "done", fg: Theme.ok, bg: Theme.ok.opacity(0.16))
        default: return nil
        }
    }
}

/// A row's name and its right side in the width there is, as the TUI
/// shares a tree line: both whole when they fit; otherwise the right side
/// keeps what the name leaves, but never less than its share (up to 40%),
/// so a long name and a long branch are both cut — the name at its end,
/// the branch at its start — rather than one squeezing the other away.
struct RowSplit: SwiftUI.Layout {
    var gap: CGFloat = 6
    var share: CGFloat = 0.4

    func sizeThatFits(proposal: ProposedViewSize, subviews: LayoutSubviews, cache: inout ()) -> CGSize {
        let h = subviews.map { $0.sizeThatFits(.unspecified).height }.max() ?? 0
        let ideal = subviews.reduce(0) { $0 + $1.sizeThatFits(.unspecified).width } + gap
        return CGSize(width: proposal.width ?? ideal, height: h)
    }

    func placeSubviews(in b: CGRect, proposal: ProposedViewSize, subviews: LayoutSubviews, cache: inout ()) {
        // A row with nothing at its right has only its name: all the room.
        guard subviews.count == 2 else {
            subviews.first?.place(at: CGPoint(x: b.minX, y: b.midY), anchor: .leading, proposal: ProposedViewSize(width: b.width, height: b.height))
            return
        }
        let name = subviews[0].sizeThatFits(.unspecified).width
        let right = subviews[1].sizeThatFits(.unspecified).width
        let room = max(b.width - gap, 0)
        let (nw, rw) = rowWidths(name: name, right: right, room: room, share: share)
        subviews[0].place(at: CGPoint(x: b.minX, y: b.midY), anchor: .leading, proposal: ProposedViewSize(width: nw, height: b.height))
        subviews[1].place(at: CGPoint(x: b.maxX, y: b.midY), anchor: .trailing, proposal: ProposedViewSize(width: rw, height: b.height))
    }
}

/// A checkout's port (CONCH_PORT) in a terminal's header, as the mock's
/// dev server shows ":4317". On this computer a click opens it in the
/// browser; elsewhere it is where the machine serves it.
struct PortLink: View {
    let port: Int
    let local: Bool

    var body: some View {
        Text(":\(port)").font(.system(size: 11.5, design: .monospaced)).foregroundStyle(Theme.muted)
            .help(local ? "CONCH_PORT of this checkout — click to open http://localhost:\(port)" : "CONCH_PORT of this checkout, on that machine")
            .onTapGesture {
                if local, let u = URL(string: "http://localhost:\(port)") { NSWorkspace.shared.open(u) }
            }
    }
}

/// The colours of the agents a folded machine holds: four, then how many
/// more.
struct FoldedAgents: View {
    let names: [String]

    var body: some View {
        HStack(spacing: 3) {
            ForEach(names.prefix(4), id: \.self) { Mark(agent: $0) }
            if names.count > 4 { Text("+\(names.count - 4)").font(.system(size: 11)).foregroundStyle(Theme.muted) }
        }
    }
}

/// The sidebar's edge: a line, and a few points either side of it to drag
/// the sidebar wider or narrower, within sidebarRange.
struct SidebarEdge: View {
    @Binding var width: Double
    @State private var start: Double?

    var body: some View {
        Rectangle().fill(Theme.line.opacity(0.6)).frame(width: 1)
            .overlay(
                Color.clear.frame(width: 8).contentShape(Rectangle())
                    .onHover { inside in if inside { NSCursor.resizeLeftRight.push() } else { NSCursor.pop() } }
                    .gesture(DragGesture(minimumDistance: 1, coordinateSpace: .global)
                        .onChanged { g in
                            let from = start ?? width
                            start = from
                            width = clampSidebar(from + g.translation.width)
                        }
                        .onEnded { _ in start = nil })
            )
            // Only a width out of range is corrected: writing it back
            // unchanged would store the default as if it had been chosen.
            .onAppear { if clampSidebar(width) != width { width = clampSidebar(width) } }
    }
}

/// An agent's mark, as conch's `[ui] icons` says: with "nerd" the TUI's
/// glyph in the agent's colour (Claude's asterisk), from a Nerd Font on
/// this Mac or else an SF Symbol of the same shape; otherwise the mock's
/// coloured dot, or nothing for "off".
struct Mark: View {
    let agent: String?

    var body: some View {
        switch Theme.icons {
        case .off:
            EmptyView()
        case .text:
            Dot(color: Theme.agent(agent))
        case .nerd:
            let mark = AgentMark.of(agent)
            Group {
                if let font = NerdFont.font(for: mark, size: 15) {
                    Text(mark.glyph).font(Font(font))
                } else if let sym = mark.symbol {
                    Image(systemName: sym).font(.system(size: 13, weight: .bold))
                } else {
                    Text(mark.glyph).font(.system(size: 15, weight: .bold))
                }
            }
            .foregroundStyle(Theme.agent(agent))
            .frame(width: 16)
        }
    }
}

/// A Nerd Font installed on this Mac, found once: the symbols-only one if
/// there is one, else any patched font, the monospaced cut first.
@MainActor
enum NerdFont {
    static let family: String? = {
        let all = NSFontManager.shared.availableFontFamilies
        for want in ["Symbols Nerd Font Mono", "Symbols Nerd Font"] where all.contains(want) { return want }
        return all.first { $0.hasSuffix("Nerd Font Mono") } ?? all.first { $0.contains("Nerd Font") }
    }()

    /// The font to draw a mark in, or nil when there is none or it lacks
    /// the glyph — a plain character (π) is left to the system.
    static func font(for mark: AgentMark, size: CGFloat) -> NSFont? {
        mark.glyphNeedsNerdFont ? font(glyph: mark.glyph, size: size) : nil
    }

    /// The Nerd Font, if it has every character of glyph.
    static func font(glyph: String, size: CGFloat) -> NSFont? {
        guard let family, let f = NSFontManager.shared.font(withFamily: family, traits: [], weight: 5, size: size) else { return nil }
        let have = f.coveredCharacterSet
        return glyph.unicodeScalars.allSatisfy { have.contains($0) } ? f : nil
    }
}

/// A key as a keycap: the key in a small outlined box, as macOS menus and
/// the mock's ⌘K draw them.
struct Keycap: View {
    let key: String
    init(_ key: String) { self.key = key }

    var body: some View {
        Text(key).font(.system(size: 10.5, weight: .medium)).foregroundStyle(Theme.text2)
            .padding(.horizontal, 4.5).frame(minWidth: 17, minHeight: 15)
            .background(RoundedRectangle(cornerRadius: 3.5).fill(Theme.raise))
            .overlay(RoundedRectangle(cornerRadius: 3.5).stroke(Theme.line2, lineWidth: 0.5))
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
                    .onTapGesture(count: 2) { store.renameWorkspace(i) }
                    .onTapGesture { store.workspace = i }
                    .contextMenu { Button("Rename…") { store.renameWorkspace(i) } }
                    .help("Workspace \(i + 1) — double-click or right-click to name it")
            }
            // A new workspace, shared with the TUI through ui.json.
            Text("+").font(.system(size: 12)).foregroundStyle(Theme.muted)
                .padding(.horizontal, 9).padding(.vertical, 3)
                .contentShape(Rectangle())
                .onTapGesture { store.newWorkspace() }
                .help("New workspace — conch's TUI has it too")
        }
        .padding(2)
        .background(RoundedRectangle(cornerRadius: 7).fill(Theme.window))
        .overlay(RoundedRectangle(cornerRadius: 7).stroke(Theme.line))
        .help("Workspaces, shared with conch's TUI")
    }
}

struct SearchField: View {
    @ObservedObject var store: Store

    var body: some View {
        Button { store.paletteOpen = true } label: {
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass").font(.system(size: 12)).foregroundStyle(Theme.muted)
                Text("Search agents, branches, files, actions").font(.system(size: 13)).foregroundStyle(Theme.muted)
                Spacer(minLength: 8)
                Text("⌘K").font(.system(size: 11, weight: .medium)).foregroundStyle(Theme.muted)
                    .padding(.horizontal, 5).overlay(RoundedRectangle(cornerRadius: 5).stroke(Theme.line2))
            }
            .padding(.horizontal, 10).frame(width: 420, height: 28)
            .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
        }
        .buttonStyle(.plain)
    }
}

struct Counts: View {
    @ObservedObject var store: Store

    var body: some View {
        let (w, d) = store.inbox
        HStack(spacing: 8) {
            if w > 0 {
                Button { store.nextWaiting() } label: { Pill(text: "⚑ \(w) waiting", fg: Theme.onWarn, bg: Theme.warn) }
                    .buttonStyle(.plain).help("Go to the next agent waiting for you (⌘.)")
            }
            if d > 0 { Pill(text: "✓ \(d) done", fg: Theme.ok, bg: Theme.ok.opacity(0.16)) }
        }
    }
}

// MARK: - the window

struct MainView: View {
    @ObservedObject var store: Store
    /// The sidebar's width, dragged at its edge and remembered.
    @AppStorage("sidebarWidth") private var sidebarWidth: Double = sidebarDefault

    var body: some View {
        ZStack {
            VStack(spacing: 0) {
                HStack(spacing: 0) {
                    if store.sidebarVisible {
                        Sidebar(store: store).frame(width: sidebarWidth)
                        SidebarEdge(width: $sidebarWidth)
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
        .sheet(item: $store.taskDraft) { d in TaskSheet(store: store, draft: d) }
        .background(Theme.window)
        .preferredColorScheme(store.theme.light ? .light : .dark)
        .id(store.theme.id) // a new theme repaints every view
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
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 1) {
                    // Direction C's "Needs you": every waiting agent as a
                    // card, answered from here, above the tree.
                    let needs = store.needsYou
                    if !needs.isEmpty {
                        HStack {
                            Text("NEEDS YOU").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                            Spacer()
                            Text("\(needs.count)").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                        }
                        .padding(.horizontal, 8).padding(.top, 10).padding(.bottom, 2)
                        ForEach(needs, id: \.key) { n in
                            NeedsYouCard(store: store, key: n.key, pane: n.pane, machine: n.machine)
                        }
                    }
                    Text("MACHINES").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                        .padding(.horizontal, 8).padding(.top, 10).padding(.bottom, 4)
                    ForEach(store.tree) { node in
                        NodeView(store: store, node: node, depth: 0)
                    }
                }
                .padding(8)
            }
            // The keyboard's row stays in sight as it moves.
            .onChange(of: store.treeCursor) { id in
                if let id { withAnimation(.easeOut(duration: 0.1)) { proxy.scrollTo(id) } }
            }
        }
        .background(Theme.side)
    }
}

struct NodeView: View {
    @ObservedObject var store: Store
    let node: TreeNode
    let depth: Int
    var open: Bool { store.isOpen(node) }
    /// The pointer is over the row: a tint under it, as the TUI's hover.
    @State private var hovering = false

    var paneID: String? {
        switch node.kind {
        case .agent(let id), .terminal(let id): return id
        default: return nil
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            row
            if open, let kids = node.children {
                ForEach(kids) { NodeView(store: store, node: $0, depth: depth + 1) }
            }
        }
    }

    var selected: Bool { paneID != nil && paneID == store.layout.focused }
    /// The keyboard's row, while the sidebar has the keyboard.
    var cursor: Bool { !store.terminalHasKeyboard && store.treeCursor == node.id }

    /// A row as the mock draws it: a fold arrow, what it is (a machine's
    /// state, an agent's colour), its name, and at the right its state or
    /// counts as pills.
    var row: some View {
        HStack(spacing: 7) {
            if node.children != nil || isSessions {
                // Sessions folds like the sections beside it; it opens the
                // TUI's Sessions view.
                Text(open && !isSessions ? "▾" : "▸").font(.system(size: 10)).foregroundStyle(Theme.muted).frame(width: 10)
            } else {
                Spacer().frame(width: 10)
            }
            icon
            RowSplit {
                Text(node.title).font(isBranch ? .system(size: 12, design: .monospaced) : .system(size: 13))
                    .foregroundStyle(isSection || isSessions ? Theme.text2 : Theme.text)
                    .lineLimit(1).truncationMode(.tail)
                badge
            }
        }
        .padding(.leading, CGFloat(depth) * 14 + 8).padding(.trailing, 8).frame(height: 26)
        .background(RoundedRectangle(cornerRadius: 6).fill(cursor ? Theme.accentDeep.opacity(0.75) : selected ? Theme.accentDeep.opacity(0.4) : hovering ? Theme.raise : .clear))
        .overlay(RoundedRectangle(cornerRadius: 6).stroke(cursor ? Theme.accent : .clear, lineWidth: 1))
        .contentShape(Rectangle())
        .id(node.id)
        .onHover { hovering = $0 }
        .onTapGesture {
            // A click in the tree gives the tree the keyboard, as in the TUI.
            store.focusSidebar()
            store.treeCursor = node.id
            activate()
        }
        .contextMenu { RowMenu(store: store, node: node) }
    }

    var isSection: Bool { if case .section = node.kind { return true }; return false }
    var isSessions: Bool { if case .sessions = node.kind { return true }; return false }
    var isBranch: Bool { if case .branch = node.kind { return true }; return false }

    @ViewBuilder var icon: some View {
        switch node.kind {
        case .machine(let m):
            let link = store.machines.first { $0.id == m }
            MachineIcon(name: m == "local" ? .laptop : link?.sandbox == true ? .cloud : .server,
                        state: link?.online == true ? Theme.ok : link?.state == "connecting" ? Theme.warn : Theme.muted)
        case .project: LucideIcon(name: open ? .folderOpen : .folder)
        case .cli: LucideIcon(name: .squareTerminal)
        case .workspace: LucideIcon(name: .layers)
        case .agent(let id): Mark(agent: store.pane(id)?.agent?.name)
        case .terminal(let id): LucideIcon(name: store.pane(id)?.isSSH == true ? .globe : .terminal, color: Theme.muted, size: 12)
        case .branch(_, _, let checkedOut):
            // In a worktree of its own: the accent; only a branch: muted.
            LucideIcon(name: .gitBranch, color: checkedOut ? Theme.accent : Theme.muted, size: 12)
                .help(checkedOut ? "checked out in a worktree of its own" : "a branch, not checked out")
        case .sessions: LucideIcon(name: .history, size: 12)
        case .section: EmptyView()
        }
    }

    /// A machine or project row's counts as the mock's pills: working in
    /// blue, waiting in amber, done in green.
    func counts(_ badge: String) -> some View {
        HStack(spacing: 4) {
            ForEach(badge.split(separator: " ").map(String.init), id: \.self) { part in
                if part.hasPrefix("⚑") {
                    Pill(text: String(part.dropFirst()), fg: Theme.onWarn, bg: Theme.warn)
                } else if part.hasPrefix("⠋") {
                    Pill(text: String(part.dropFirst()), fg: Theme.work, bg: Theme.work.opacity(0.18))
                } else if part.hasPrefix("✓") {
                    Pill(text: String(part.dropFirst()), fg: Theme.ok, bg: Theme.ok.opacity(0.16))
                }
            }
        }
    }

    func muted(_ text: String) -> some View {
        Text(text).font(.system(size: 12)).foregroundStyle(Theme.muted).lineLimit(1)
    }

    /// The row's right side, as the mock has it.
    @ViewBuilder var badge: some View {
        switch node.kind {
        case .agent(let id), .terminal(let id):
            if let p = store.pane(id) {
                if !p.running {
                    muted("exited").fixedSize()
                } else if let pill = Pill.state(p.agent?.state) {
                    pill.fixedSize()
                }
            }
        case .machine(let m):
            if let link = store.machines.first(where: { $0.id == m }), !link.online {
                Text(link.state).font(.system(size: 12)).foregroundStyle(link.state == "offline" ? Theme.err : Theme.muted)
                    .padding(.trailing, 2)
                    .help(link.why.isEmpty ? link.state : link.why)
                    .fixedSize()
            } else {
                HStack(spacing: 4) {
                    counts(node.badge)
                    // Folded, it says which agents it holds, as the mock's
                    // busybox row does.
                    if !open { FoldedAgents(names: store.snapshots[m]?.agentNames(in: store.ws, machine: m) ?? []) }
                }.fixedSize()
            }
        case .project: counts(node.badge).fixedSize()
        case .branch:
            Text(node.badge).font(.system(size: 11.5, design: .monospaced))
                .foregroundStyle(node.badge.contains("✓") ? Theme.ok : node.badge.contains("✗") ? Theme.err : Theme.muted)
                .lineLimit(1).truncationMode(.head)
        default:
            if !node.badge.isEmpty { muted(node.badge).fixedSize() }
        }
    }

    func activate() {
        switch node.kind {
        case .agent(let id), .terminal(let id): store.open(id)
        case .branch(let pr, let name, _): store.openChanges(project: pr, branch: name)
        case .sessions(let pk): store.openSessions(project: pk)
        default: withAnimation(.easeOut(duration: 0.12)) { store.toggle(node) }
        }
    }
}

/// A row's right-click menu: the TUI's row menu (m), for what the app does
/// itself. Starting an agent is a submenu of the agents installed there.
struct RowMenu: View {
    @ObservedObject var store: Store
    let node: TreeNode

    var body: some View {
        let items = store.menu(for: node)
        ForEach(Array(items.enumerated()), id: \.offset) { _, it in
            if it.action == .divider {
                Divider()
            } else if it.picksAgent, let m = node.machine {
                Menu(it.title) {
                    ForEach(store.startable(on: m), id: \.name) { a in
                        Button(a.title) { store.perform(it.action, on: node, agent: a.name) }
                    }
                }
            } else {
                Button(it.title) { store.perform(it.action, on: node) }
            }
        }
    }
}

/// A new task: a prompt, the agent to give it to, and, if wanted, the
/// branch's name — `conch task`, from a sheet. The server makes the branch
/// and its worktree and starts the agent there.
struct TaskSheet: View {
    @ObservedObject var store: Store
    @State var draft: TaskDraft
    @FocusState private var promptFocused: Bool

    var project: ProjectInfo? {
        let (m, id) = unkey(draft.project)
        return store.snapshots[m]?.project(id)
    }

    var body: some View {
        let (m, _) = unkey(draft.project)
        VStack(alignment: .leading, spacing: 12) {
            Text("New task").font(.system(size: 15, weight: .semibold))
            Text([project?.name ?? "", m == "local" ? "" : store.label(m)].filter { !$0.isEmpty }.joined(separator: " · ")
                 + (project?.git == false ? " — the agent works in the folder itself" : " — on a branch and worktree of its own"))
                .font(.system(size: 12)).foregroundStyle(.secondary)
            TextEditor(text: $draft.prompt)
                .font(.system(size: 13)).frame(minHeight: 120)
                .scrollContentBackground(.hidden).padding(6)
                .background(RoundedRectangle(cornerRadius: 6).fill(Color(nsColor: .textBackgroundColor)))
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.secondary.opacity(0.3)))
                .focused($promptFocused)
            HStack(spacing: 12) {
                Picker("Agent", selection: $draft.agent) {
                    ForEach(store.startable(on: m), id: \.name) { Text($0.title).tag($0.name) }
                }
                .frame(maxWidth: 240)
                if project?.git != false {
                    TextField("Branch (conch names one)", text: $draft.branch).frame(maxWidth: 260)
                }
            }
            HStack {
                Spacer()
                Button("Cancel") { store.taskDraft = nil }.keyboardShortcut(.cancelAction)
                Button("Start") { store.startTask(draft) }
                    .keyboardShortcut(.return, modifiers: .command)
                    .disabled(draft.prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            Text("⌘↩ starts it").font(.system(size: 11)).foregroundStyle(.secondary)
        }
        .padding(18)
        .frame(width: 520)
        .onAppear { promptFocused = true }
    }
}

/// A waiting agent as direction C's card: its mark and name with what it
/// wants as a pill, the question in a line or two, where it is, and its
/// choices as buttons — the agent's own cursor's in amber. A click on the
/// card, not a button, opens the agent.
struct NeedsYouCard: View {
    @ObservedObject var store: Store
    let key: String
    let pane: PaneInfo
    let machine: String
    @State private var hovering = false

    var body: some View {
        let q = store.questions[key]
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Mark(agent: pane.agent?.name)
                Text(pane.displayName).font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.text).lineLimit(1)
                Spacer(minLength: 4)
                Pill(text: questionKind(pane.agent?.message), fg: Theme.onWarn, bg: Theme.warn)
            }
            let text = cardText(q?.text.isEmpty == false ? q!.text : (pane.agent?.message ?? ""), choices: q?.choices ?? [])
            if !text.isEmpty {
                Text(text).font(.system(size: 11.5, design: .monospaced)).foregroundStyle(Theme.text2).lineLimit(2)
            }
            let place = [store.snapshots[machine]?.place(of: pane) ?? "", machine == "local" ? "" : store.label(machine)]
                .filter { !$0.isEmpty }.joined(separator: " · ")
            if !place.isEmpty {
                Text(place).font(.system(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1)
            }
            if let q, !q.choices.isEmpty {
                ChoiceButtons(store: store, pane: key, question: q)
            } else {
                Button("Open to answer") { store.open(key) }.buttonStyle(.plain)
                    .font(.system(size: 11.5, weight: .medium)).foregroundStyle(Theme.accent)
            }
        }
        .padding(.horizontal, 11).padding(.vertical, 10)
        .background(RoundedRectangle(cornerRadius: 10).fill(hovering ? Theme.raise : Theme.raise.opacity(0.7)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.warn.opacity(0.55)))
        .contentShape(RoundedRectangle(cornerRadius: 10))
        .onHover { hovering = $0 }
        .onTapGesture { store.open(key) }
        .padding(.bottom, 6)
    }
}

/// A question's choices as C's buttons: side by side when they are short
/// (Allow · Always · Deny), one under another when they are not; the one
/// the agent's cursor is on in amber.
struct ChoiceButtons: View {
    @ObservedObject var store: Store
    let pane: String
    let question: Question

    var body: some View {
        let short = question.choices.count <= 3 && question.choices.map(\.label.count).reduce(0, +) <= 30
        let layout = short ? AnyLayout(HStackLayout(spacing: 6)) : AnyLayout(VStackLayout(alignment: .leading, spacing: 5))
        layout {
            ForEach(question.choices, id: \.choice) { c in
                let on = c.default == true
                Button { store.answer(pane, question, c) } label: {
                    Text(c.label).font(.system(size: 11.5, weight: on ? .semibold : .regular)).lineLimit(1).truncationMode(.tail)
                        .foregroundStyle(on ? Theme.onWarn : Theme.text)
                        .padding(.horizontal, 9).padding(.vertical, 3)
                        .frame(maxWidth: short ? nil : .infinity, alignment: .leading)
                        .background(RoundedRectangle(cornerRadius: 6).fill(on ? Theme.warn : Theme.raise))
                        .overlay(RoundedRectangle(cornerRadius: 6).stroke(on ? Theme.warn : Theme.line2))
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
    /// How far the scrolling strip fades out at each end.
    static let fade: CGFloat = 24

    var tabs: some View {
        HStack(alignment: .bottom, spacing: 2) {
            ForEach(Array(store.layout.tabs.enumerated()), id: \.element.id) { i, tab in
                TabButton(store: store, index: i, tab: tab).id(tab.id)
            }
        }
    }

    var plus: some View {
        Button { store.newTerminal(split: nil) } label: {
            Text("+").font(.system(size: 15)).foregroundStyle(Theme.muted).frame(width: 26, height: 30)
        }
        .buttonStyle(.plain).focusable(false).help("New terminal tab (⌘T)")
    }

    var body: some View {
        HStack(alignment: .bottom, spacing: 2) {
            // The tabs as a row while they fit, + just after them; when
            // there are more than fit, a strip that scrolls sideways with the
            // one on screen kept in sight. Where it is stays put at the
            // right, so nothing is pushed out of the window.
            ViewThatFits(in: .horizontal) {
                HStack(alignment: .bottom, spacing: 2) { tabs; plus }
                HStack(alignment: .bottom, spacing: 2) {
                    ScrollViewReader { proxy in
                        ScrollView(.horizontal, showsIndicators: false) {
                            // Padded by the fade, so a tab scrolled to the
                            // very end is whole and clear; only one the
                            // edge cuts fades out.
                            tabs.padding(.bottom, 1).padding(.horizontal, TabBar.fade)
                        }
                        .mask(HStack(spacing: 0) {
                            LinearGradient(colors: [.clear, .black], startPoint: .leading, endPoint: .trailing).frame(width: TabBar.fade)
                            Rectangle()
                            LinearGradient(colors: [.black, .clear], startPoint: .leading, endPoint: .trailing).frame(width: TabBar.fade)
                        })
                        .onChange(of: store.layout.active) { i in
                            if store.layout.tabs.indices.contains(i) {
                                withAnimation(.easeOut(duration: 0.15)) { proxy.scrollTo(store.layout.tabs[i].id) }
                            }
                        }
                        .onAppear {
                            if store.layout.tabs.indices.contains(store.layout.active) { proxy.scrollTo(store.layout.tabs[store.layout.active].id) }
                        }
                    }
                    plus
                }
            }
            .layoutPriority(1)
            Spacer(minLength: 8)
            // Where the tab on screen is: its project and machine, as the
            // TUI's tab bar ends — cut, never pushed out.
            if let k = store.layout.focused {
                Text(store.scope(of: k)).font(.system(size: 12)).foregroundStyle(Theme.muted)
                    .lineLimit(1).truncationMode(.head)
                    .frame(maxWidth: 220, alignment: .trailing).fixedSize(horizontal: true, vertical: false)
                    .layoutPriority(2) // its room before the tabs': they scroll, it cannot
                    .padding(.bottom, 9)
            }
        }
        .padding(.horizontal, 10).frame(height: 36)
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
    }
}

struct TabButton: View {
    @ObservedObject var store: Store
    let index: Int
    let tab: ConchKit.Tab

    /// What the tab is called: a branch's changes as the mock names them,
    /// a project's sessions or commits by the project, else the pane.
    func title(changes: (machine: String, project: String, branch: String)?, pane p: PaneInfo?) -> String {
        if let c = changes { return "\(c.branch) changes" }
        func project(_ m: String, _ id: String) -> String { store.snapshots[m]?.project(id)?.name ?? id }
        if let s = parseSessionsKey(tab.focused) { return "Sessions · " + project(s.machine, s.project) }
        if let c = parseCommitsKey(tab.focused) { return "Commits · " + project(c.machine, c.project) }
        return p?.displayName ?? tab.focused
    }

    var body: some View {
        let on = index == store.layout.active
        let p = store.pane(tab.focused)
        HStack(spacing: 7) {
            let changes = parseChangesKey(tab.focused)
            if let a = p?.agent {
                Mark(agent: a.name)
            } else {
                if parseSessionsKey(tab.focused) != nil {
                    LucideIcon("history", color: Theme.accent, size: 12)
                } else if parseCommitsKey(tab.focused) != nil {
                    LucideIcon("git-commit-horizontal", color: Theme.accent, size: 12)
                } else if changes == nil {
                    LucideIcon(name: p?.isSSH == true ? .globe : .terminal, color: Theme.muted, size: 12)
                } else {
                    LucideIcon(name: .gitBranch, color: Theme.accent, size: 12)
                }
            }
            // A branch's changes are named as the mock names them; a
            // project's sessions by the project.
            Text(title(changes: changes, pane: p))
                .font(.system(size: 12.5)).lineLimit(1).truncationMode(.tail)
                .frame(maxWidth: 200, alignment: .leading).fixedSize(horizontal: true, vertical: false)
            if tab.root.panes.contains(where: { store.pane($0)?.agent?.state == .blocked }) {
                // The mock's waiting badge: a round amber one with a bold !.
                Text("!").font(.system(size: 10.5, weight: .heavy)).foregroundStyle(Theme.onWarn)
                    .frame(width: 15, height: 15).background(Circle().fill(Theme.warn))
                    .help("waiting for you")
            } else if tab.root.panes.contains(where: { store.pane($0)?.agent?.state == .working }) {
                // An agent at work in it: the TUI's spinner, turning.
                TimelineView(.periodic(from: .now, by: spinnerInterval)) { ctx in
                    Text(spinnerFrame(at: ctx.date)).font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.work)
                }
            }
            if on {
                Button { store.closeSplit() } label: { Image(systemName: "xmark").font(.system(size: 9, weight: .semibold)) }
                    .buttonStyle(.plain).foregroundStyle(Theme.muted)
            }
        }
        .foregroundStyle(on ? Theme.text : Theme.text2)
        .padding(.horizontal, 12).frame(height: 30)
        .background(UnevenRoundedRectangle(topLeadingRadius: 7, topTrailingRadius: 7).fill(on ? Theme.raise : .clear))
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
            if parseChangesKey(id) != nil {
                ChangesView(store: store, model: store.changes(for: id)).id(id)
            } else if parseSessionsKey(id) != nil {
                SessionsView(model: store.sessions(for: id), store: store).id(id)
            } else if parseCommitsKey(id) != nil {
                CommitsView(model: store.commits(for: id), store: store).id(id)
            } else {
                // Its own card for each pane, so moving to another tab
                // never shows one pane's terminal under another's header.
                PaneCard(store: store, pane: id, terminal: store.terminal(for: id)).id(id)
            }
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
        let p = store.pane(pane)
        let focused = store.layout.focused == pane
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                if let a = p?.agent { Mark(agent: a.name) } else { LucideIcon(name: p?.isSSH == true ? .globe : .terminal, color: Theme.muted, size: 12) }
                Text(p?.agent?.name ?? p?.name ?? pane).font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.text)
                if let b = p?.branch, !b.isEmpty {
                    Text(b).font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.text2)
                        .padding(.horizontal, 6).padding(.vertical, 1).background(RoundedRectangle(cornerRadius: 5).fill(Theme.raise))
                }
                Spacer()
                if let p, p.agent == nil, let port = store.port(of: pane) {
                    PortLink(port: port, local: unkey(pane).machine == "local")
                }
                if p?.running == false {
                    Text("exited \(p?.exitCode ?? 0)").font(.system(size: 11)).foregroundStyle(Theme.muted)
                }
                if let a = p?.agent, a.state == .working, let since = a.since {
                    // How long it has been at it, as the mock's "working · 41s".
                    TimelineView(.periodic(from: .now, by: spinnerInterval)) { ctx in
                        Pill(text: "\(spinnerFrame(at: ctx.date)) working · \(elapsed(since, ctx.date))", fg: Theme.work, bg: Theme.work.opacity(0.18))
                    }
                } else {
                    Pill.state(p?.agent?.state)
                }
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
                    .foregroundStyle(c.default == true ? Theme.onWarn : Theme.text)
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
    @State private var showLimits = false
    @State private var showMonitor = false
    @State private var monitorHover = false

    static let treeKeys = [("⏎", "open"), ("t", "task"), ("c", "agent"), ("n", "terminal"), ("⌘D", "split")]
    static let paneKeys = [("⌘W", "close split"), ("⌘D", "split"), ("⇧⌘]", "next tab"), ("⌘K", "search"), ("⌘.", "next waiting")]

    var body: some View {
        HStack(spacing: 16) {
            // Where the keyboard is, as the TUI's chip says: a pane, or the
            // window around it.
            let inPane = store.layout.focused != nil && store.terminalHasKeyboard
            // The mode as a small chip, as the TUI's: the accent's for the
            // tree, green for a pane.
            let mode = inPane ? Theme.ok : Theme.accent
            Text(inPane ? "PANE" : "TREE").font(.system(size: 10.5, weight: .bold)).tracking(0.6)
                .foregroundStyle(mode)
                .padding(.horizontal, 6).padding(.vertical, 1.5)
                .background(RoundedRectangle(cornerRadius: 4).fill(mode.opacity(0.16)))
            if !store.connected { Text("○ not connected").foregroundStyle(Theme.err) }
            // The keys for where the keyboard is: each a keycap, then what
            // it does, muted.
            HStack(spacing: 12) {
                ForEach(inPane ? Self.paneKeys : Self.treeKeys, id: \.0) { key, what in
                    HStack(spacing: 5) {
                        Keycap(key)
                        Text(what).foregroundStyle(Theme.muted)
                    }
                    .fixedSize()
                }
            }
            .lineLimit(1)
            if let e = store.error {
                Text(e).foregroundStyle(Theme.err).lineLimit(1)
                    .onTapGesture { store.error = nil }
            }
            Spacer()
            // The plan the agents run on, as the TUI's status bar shows it;
            // a click lists every window, as the TUI's limits view does.
            if store.limits.contains(where: { $0.fiveHour != nil }) {
                Button { showLimits.toggle() } label: {
                    HStack(spacing: 8) {
                        ForEach(store.limits.filter { $0.fiveHour != nil }, id: \.agent) { l in
                            UsageChip(limits: l)
                        }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(store.limits.map(usageHelp).joined(separator: "\n") + "\nClick for every window")
                .popover(isPresented: $showLimits, arrowEdge: .top) { LimitsPopover(store: store) }
                .onChange(of: showLimits) { if $0 { store.refreshLimits() } }
            }
            if !store.serverVersion.isEmpty {
                Text("v\(store.serverVersion)").foregroundStyle(Theme.muted)
                    .help("conch \(store.serverVersion) · panes drawn with \(PaneTerminal.engine)")
            }
            // Settings, as the TUI's bar ends: conch's settings page.
            Button { store.openSettings() } label: {
                Text("⚙ Settings").font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.selectedText)
                    .padding(.horizontal, 7).padding(.vertical, 1)
                    .background(RoundedRectangle(cornerRadius: 4).fill(Theme.accentDeep))
            }
            .buttonStyle(.plain).help("conch's settings, in the browser (conch config web); an open TUI takes in what you save")
            // What conch uses on this computer, in the corner as in the TUI.
            Button { showMonitor.toggle() } label: {
                MonitorIcon(color: showMonitor ? Theme.accent : Theme.muted)
                    .frame(width: 22, height: 18)
                    .background(RoundedRectangle(cornerRadius: 4).fill(showMonitor || monitorHover ? Theme.raise : .clear))
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain).help("Memory and CPU conch uses on this computer")
            .onHover { monitorHover = $0 }
            .popover(isPresented: $showMonitor, arrowEdge: .top) { MonitorPopover(store: store) }
            .onChange(of: showMonitor) { if $0 { store.startMonitor() } else { store.stopMonitor() } }
        }
        .font(.system(size: 12))
        .padding(.horizontal, 12).frame(height: 26)
        .background(Theme.side)
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
    }
}

/// Every window of every agent's plan, with when it resets: the TUI's
/// limits view.
struct LimitsPopover: View {
    @ObservedObject var store: Store

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Plan limits").font(.system(size: 13, weight: .semibold))
            if store.limits.isEmpty {
                Text("No agent has reported its plan yet.").foregroundStyle(.secondary)
            }
            ForEach(store.limits, id: \.agent) { l in
                VStack(alignment: .leading, spacing: 6) {
                    HStack(spacing: 6) {
                        Mark(agent: l.agent)
                        Text(l.agent.prefix(1).uppercased() + l.agent.dropFirst()).font(.system(size: 12.5, weight: .semibold))
                        Spacer()
                        if let at = l.at, at.timeIntervalSince1970 > 0 {
                            TimelineView(.periodic(from: .now, by: 30)) { ctx in
                                Text("reported \(elapsed(at, ctx.date)) ago").font(.system(size: 11)).foregroundStyle(.secondary)
                            }
                        }
                    }
                    ForEach(l.allWindows, id: \.key) { w in
                        HStack(spacing: 8) {
                            Text(PlanLimits.label(w.key)).frame(width: 110, alignment: .leading)
                            Meter(fraction: w.usedPct / 100)
                            Text("\(Int(w.usedPct.rounded()))%").frame(width: 38, alignment: .trailing).monospacedDigit()
                            if let r = w.resetsAt, r.timeIntervalSince1970 > 0 {
                                TimelineView(.periodic(from: .now, by: 30)) { ctx in
                                    Text(r > ctx.date ? "resets in \(elapsed(ctx.date, r))" : "reset").foregroundStyle(.secondary)
                                }
                            }
                        }
                        .font(.system(size: 12))
                    }
                }
            }
            Text("What the agents last reported; conch counts only what it has seen.").font(.system(size: 11)).foregroundStyle(.secondary)
        }
        .padding(14).frame(width: 400)
    }
}

/// The TUI's monitor icon, ▁▃▆, drawn as three bars rising from one
/// baseline rather than as characters a font sets at uneven heights.
struct MonitorIcon: View {
    let color: Color

    var body: some View {
        HStack(alignment: .bottom, spacing: 1.5) {
            ForEach([3.0, 6.5, 10.5], id: \.self) { h in
                RoundedRectangle(cornerRadius: 0.8).fill(color).frame(width: 3, height: h)
            }
        }
    }
}

/// The memory and CPU conch uses on this computer, every two seconds
/// while open: the TUI's monitor box, line for line — this window, the
/// server, the panes with each one's tree under them, the total.
struct MonitorPopover: View {
    @ObservedObject var store: Store

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            Text("monitor").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.accent).padding(.bottom, 4)
            if let r = store.resources {
                line(name: "", mem: "memory", cpu: Text("    cpu").foregroundStyle(Theme.muted), bold: false, header: true)
                ForEach(Array(r.rows.enumerated()), id: \.offset) { _, row in
                    line(name: (row.indent ? "  " : "") + row.name, mem: formatMemory(row.rssKiB), cpu: cpu(row.cpuPct), bold: false,
                         muted: row.indent)
                }
                line(name: r.total.name, mem: formatMemory(r.total.rssKiB), cpu: cpu(r.total.cpuPct), bold: true)
            } else {
                Text("reading processes…").foregroundStyle(Theme.muted)
            }
            Text("this computer · every 2s · cpu 100% = one core").foregroundStyle(Theme.muted).padding(.top, 8)
        }
        .font(.system(size: 12, design: .monospaced))
        .fixedSize() // as wide as its lines: the CPU bars are never cut
        .padding(12)
        .background(Theme.chrome)
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.accent.opacity(0.6)))
    }

    func line(name: String, mem: String, cpu: Text, bold: Bool, muted: Bool = false, header: Bool = false) -> some View {
        HStack(spacing: 8) {
            Text(name).lineLimit(1).truncationMode(.tail).frame(width: 220, alignment: .leading)
                .foregroundStyle(header || muted ? Theme.muted : Theme.text).fontWeight(bold ? .bold : .regular)
            Text(mem).frame(width: 72, alignment: .trailing).foregroundStyle(header ? Theme.muted : Theme.accent)
            cpu.lineLimit(1).fixedSize()
        }
    }

    /// "  3.2% ██░░░░░░░░" in the TUI's colours; "…" before a second sample.
    func cpu(_ pct: Double?) -> Text {
        guard let pct else { return Text("      …").foregroundStyle(Theme.muted) + Text(" " + String(repeating: "░", count: 10)).foregroundStyle(Theme.muted) }
        let color: Color = cpuLevel(pct) == .hot ? Theme.err : cpuLevel(pct) == .busy ? Theme.warn : Theme.ok
        let n = cpuCells(pct)
        return Text(String(format: "%6.1f%%", pct)).foregroundStyle(color)
            + Text(" " + String(repeating: "█", count: n)).foregroundStyle(color)
            + Text(String(repeating: "░", count: 10 - n)).foregroundStyle(Theme.muted)
    }
}

/// One agent's plan in the status bar: its mark, then each window — 5h,
/// 7d — as a label, a short bar and the share used, the bar and figure
/// coloured by how near the limit it is. A capsule, so it reads as one
/// thing to click.
struct UsageChip: View {
    let limits: PlanLimits

    var body: some View {
        HStack(spacing: 8) {
            Mark(agent: limits.agent)
            window("5h", limits.fiveHour?.usedPct)
            if let w = limits.week {
                Rectangle().fill(Theme.line).frame(width: 1, height: 10)
                window("7d", w.usedPct)
            }
        }
        .padding(.horizontal, 8).padding(.vertical, 2)
        .background(Capsule().fill(Theme.raise.opacity(0.7)))
    }

    @ViewBuilder func window(_ name: String, _ pct: Double?) -> some View {
        let p = pct ?? 0
        HStack(spacing: 5) {
            Text(name).foregroundStyle(Theme.muted)
            Meter(fraction: p / 100, width: 36, track: Theme.line2)
            Text("\(Int(p.rounded()))%").monospacedDigit().foregroundStyle(usageColor(p))
        }
    }
}

/// How near its limit a window is: fine, getting there from 80%, at it.
@MainActor func usageColor(_ pct: Double) -> Color { pct >= 100 ? Theme.err : pct >= 80 ? Theme.warn : Theme.text2 }

/// An agent's windows and when each resets, for the chip's tooltip.
@MainActor func usageHelp(_ l: PlanLimits) -> String {
    let name = l.agent.prefix(1).uppercased() + l.agent.dropFirst()
    let parts = l.allWindows.map { w -> String in
        var s = "\(PlanLimits.label(w.key)) \(Int(w.usedPct.rounded()))%"
        if let r = w.resetsAt, r > Date() { s += ", resets in \(elapsed(Date(), r))" }
        return s
    }
    return "\(name): " + parts.joined(separator: " · ")
}

/// A plan's use, as a bar: green, amber from 80%, red past it.
struct Meter: View {
    let fraction: Double
    var width: CGFloat = 60
    /// The empty part's colour: the raised one, or a line's on a raised
    /// background, where the raised one would vanish.
    var track: Color = Theme.raise

    var body: some View {
        ZStack(alignment: .leading) {
            Capsule().fill(track)
            Capsule().fill(fraction >= 1 ? Theme.err : fraction >= 0.8 ? Theme.warn : Theme.ok)
                .frame(width: width * min(max(fraction, 0), 1))
        }
        .frame(width: width, height: 6)
    }
}

/// "41s", "6m", "2h 5m": how long ago a time was.
func elapsed(_ since: Date, _ now: Date) -> String {
    let s = max(Int(now.timeIntervalSince(since)), 0)
    if s < 60 { return "\(s)s" }
    if s < 3600 { return "\(s / 60)m" }
    if s < 86400 { return "\(s / 3600)h \(s % 3600 / 60)m" }
    return "\(s / 86400)d \(s % 86400 / 3600)h"
}

// MARK: - palette

struct Palette: View {
    @ObservedObject var store: Store
    @State private var query = ""
    @State private var sel = 0
    @FocusState private var focused: Bool

    /// Agents, panes and places, then the files found, then the commands —
    /// the mock's order.
    var items: [PaletteItem] {
        let found = filterPalette(store.paletteItems, query: query)
        let isCommand = { (it: PaletteItem) -> Bool in if case .command = it.action { return true }; return false }
        return found.filter { !isCommand($0) } + (query.isEmpty ? [] : store.fileHits) + found.filter(isCommand)
    }

    var body: some View {
        ZStack(alignment: .top) {
            Color.black.opacity(0.35).ignoresSafeArea().onTapGesture { store.paletteOpen = false }
            VStack(spacing: 0) {
                HStack(spacing: 10) {
                    Image(systemName: "magnifyingglass").foregroundStyle(Theme.accent)
                    TextField("Agents, branches, files, projects, commands", text: $query)
                        .textFieldStyle(.plain).font(.system(size: 16)).focused($focused)
                        .onSubmit(run)
                        .onChange(of: query) { q in
                            sel = 0
                            store.searchFiles(q)
                        }
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
                    if store.filesUnsearchable {
                        Text("files: conch's server predates searching them — reload it").help("In the TUI: m on the machine → Reload server")
                    }
                }
                .font(.system(size: 11.5)).foregroundStyle(Theme.muted).padding(.horizontal, 16).padding(.vertical, 8)
            }
            .frame(width: 620)
            .background(RoundedRectangle(cornerRadius: 12).fill(Theme.chrome))
            .overlay(RoundedRectangle(cornerRadius: 12).stroke(Theme.line2))
            .shadow(color: .black.opacity(0.6), radius: 30, y: 20)
            .padding(.top, 60)
        }
        .onAppear {
            focused = true
            store.searchFiles("")
        }
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
