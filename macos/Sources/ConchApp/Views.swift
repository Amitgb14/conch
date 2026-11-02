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
        case .blocked: return Pill(text: "waiting", fg: Theme.onWarn, bg: Theme.warn)
        case .done: return Pill(text: "done", fg: Theme.ok, bg: Theme.ok.opacity(0.16))
        default: return nil
        }
    }
}

/// An agent's mark, drawn as the TUI draws it under conch's `[ui] icons`:
/// the Nerd Font glyph in the agent's own colour, the coloured dot, or
/// nothing. On the selected row the glyph takes the selection's text
/// colour, as the TUI's whole selected line does.
struct Mark: View {
    let mark: AgentMark
    var selected = false

    var body: some View {
        switch Theme.icons {
        case .off:
            EmptyView()
        case .text:
            Text(mark.dot).font(.system(size: 10)).frame(width: 16)
        case .nerd:
            Group {
                if let font = NerdFont.font(for: mark, size: 13) {
                    Text(mark.glyph).font(Font(font))
                } else if let sym = mark.symbol {
                    // No Nerd Font here: the same shape from SF Symbols.
                    Image(systemName: sym).font(.system(size: 11, weight: .bold))
                } else {
                    Text(mark.glyph).font(.system(size: 13, weight: .bold))
                }
            }
            .foregroundStyle(selected ? Theme.selectedText : mark.color.map(Theme.color) ?? Theme.muted)
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

    /// The font to draw text in, or nil when there is none or it lacks a
    /// character of it — a plain character (π) is left to the system.
    static func font(for mark: AgentMark, size: CGFloat) -> NSFont? {
        guard mark.glyphNeedsNerdFont, let family, let f = NSFontManager.shared.font(withFamily: family, traits: [], weight: 5, size: size)
        else { return nil }
        let have = f.coveredCharacterSet
        return mark.glyph.unicodeScalars.allSatisfy { have.contains($0) } ? f : nil
    }
}

/// A pane's state glyph, as the TUI's paneGlyph draws it in front of the
/// row: the spinner turns at the TUI's pace while an agent works.
struct Status: View {
    let pane: PaneInfo?
    var selected = false

    var body: some View {
        if let p = pane {
            if StateGlyph.of(p).spins {
                TimelineView(.periodic(from: .now, by: StateGlyph.spinInterval)) { tl in
                    glyph(StateGlyph.of(p, frame: Int(tl.date.timeIntervalSinceReferenceDate / StateGlyph.spinInterval)))
                }
            } else {
                glyph(StateGlyph.of(p))
            }
        } else {
            glyph(StateGlyph(glyph: "?", tone: .muted))
        }
    }

    func glyph(_ g: StateGlyph) -> some View {
        Text(g.glyph).font(.system(size: 12, weight: .semibold, design: .monospaced))
            .foregroundStyle(selected ? Theme.selectedText : color(g.tone))
            .frame(width: 10)
    }

    func color(_ t: StateGlyph.Tone) -> Color {
        switch t {
        case .muted: return Theme.muted
        case .work: return Theme.work
        case .warn: return Theme.warn
        case .ok: return Theme.ok
        case .err: return Theme.err
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
            // Workspaces are made in the TUI (ctrl+b N); + opens it there.
            Text("+").font(.system(size: 12)).foregroundStyle(Theme.muted)
                .padding(.horizontal, 9).padding(.vertical, 3)
                .contentShape(Rectangle())
                .onTapGesture { store.openTUI() }
                .help("New workspace: made in conch's TUI with ctrl+b N, which this opens")
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

    var body: some View {
        ZStack {
            VStack(spacing: 0) {
                HStack(spacing: 0) {
                    if store.sidebarVisible {
                        Sidebar(store: store).frame(width: 300)
                        Rectangle().fill(Theme.line.opacity(0.6)).frame(width: 1)
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
        ScrollView {
            VStack(alignment: .leading, spacing: 1) {
                Text("MACHINES").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                    .padding(.horizontal, 8).padding(.top, 10).padding(.bottom, 4)
                ForEach(store.tree) { node in
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
    var open: Bool { store.isOpen(node) }

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
            } else if isSessions {
                // The mock folds it like the sections beside it; it opens
                // the TUI's Sessions view.
                Image(systemName: "chevron.right").font(.system(size: 9, weight: .semibold))
                    .foregroundStyle(Theme.muted).frame(width: 10)
            } else {
                Spacer().frame(width: 10)
            }
            icon
            // The name first: the branch is on the pane's own card, and a
            // narrow sidebar gives it up before the name.
            Text(node.title).font(isBranch ? .system(size: 12, design: .monospaced) : .system(size: 13, weight: isHeading ? .semibold : .regular))
                .foregroundStyle(selected ? Theme.selectedText : isSection || isSessions ? Theme.text2 : Theme.text)
                .lineLimit(1).truncationMode(.tail)
                .layoutPriority(1)
            Spacer(minLength: 4)
            badge.fixedSize()
        }
        .padding(.leading, CGFloat(depth) * 14 + 6).padding(.trailing, 8).frame(height: 26)
        .background(RoundedRectangle(cornerRadius: 5).fill(selected ? Theme.accentDeep : .clear))
        .contentShape(Rectangle())
        .onTapGesture { activate() }
    }

    var isGroup: Bool { if case .project = node.kind { return true }; return false }
    var isSection: Bool { if case .section = node.kind { return true }; return false }
    /// The TUI's bold group rows: Workspace and CLI.
    var isHeading: Bool {
        switch node.kind {
        case .workspace, .cli: return true
        default: return false
        }
    }
    var isSessions: Bool { if case .sessions = node.kind { return true }; return false }
    var isBranch: Bool { if case .branch = node.kind { return true }; return false }

    @ViewBuilder var icon: some View {
        switch node.kind {
        case .machine(let m):
            let link = store.machines.first { $0.id == m }
            Dot(color: link?.online == true ? Theme.ok : link?.state == "connecting" ? Theme.warn : Theme.muted)
        case .project: Text("◆").font(.system(size: 10)).foregroundStyle(Theme.accent)
        case .cli: Text("❯").font(.system(size: 11)).foregroundStyle(Theme.accent)
        case .workspace: Text("▤").font(.system(size: 12)).foregroundStyle(Theme.accent)
        case .agent(let id):
            // The TUI's row: what it is doing, then which agent it is.
            HStack(spacing: 5) {
                Status(pane: store.pane(id), selected: selected)
                Mark(mark: AgentMark.of(store.pane(id)?.agent?.name), selected: selected)
            }
        case .terminal(let id): Status(pane: store.pane(id), selected: selected)
        case .branch(_, _, let checkedOut):
            // ◇ checked out in a worktree of its own, · not checked out:
            // the TUI's marks.
            Text(checkedOut ? "◇" : "·").font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.text2).frame(width: 9)
        case .section, .sessions: EmptyView()
        }
    }

    /// A count pill: the machine and project rows' working and waiting.
    func counts(_ badge: String) -> some View {
        HStack(spacing: 4) {
            ForEach(badge.split(separator: " ").map(String.init), id: \.self) { part in
                if part.hasPrefix("⚑") {
                    Pill(text: String(part.dropFirst()), fg: Theme.onWarn, bg: Theme.warn)
                } else if part.hasPrefix("⠋") {
                    Pill(text: String(part.dropFirst()), fg: Theme.work, bg: Theme.work.opacity(0.18))
                } else if part.hasPrefix("✓") {
                    Pill(text: String(part.dropFirst()), fg: Theme.ok, bg: Theme.ok.opacity(0.16))
                } else {
                    Text(part).font(.system(size: 12)).foregroundStyle(Theme.muted)
                }
            }
        }
    }

    @ViewBuilder var badge: some View {
        switch node.kind {
        case .agent(let id):
            if let a = store.pane(id)?.agent {
                switch a.state {
                case .working: Pill(text: "working", fg: Theme.work, bg: Theme.work.opacity(0.18))
                case .blocked: Pill(text: "waiting", fg: Theme.onWarn, bg: Theme.warn)
                case .done: Pill(text: "done", fg: Theme.ok, bg: Theme.ok.opacity(0.16))
                default: EmptyView()
                }
            }
        case .terminal: if node.badge == "exited" { Text("exited").font(.system(size: 11)).foregroundStyle(Theme.muted) }
        case .machine(let m):
            if let link = store.machines.first(where: { $0.id == m }), !link.online {
                Text(link.state).font(.system(size: 11)).foregroundStyle(link.state == "offline" ? Theme.err : Theme.muted)
                    .padding(.trailing, 2)
                    .help(link.why.isEmpty ? link.state : link.why)
            } else {
                counts(node.badge)
            }
        case .project: counts(node.badge)
        case .branch:
            Text(node.badge).font(.system(size: 11.5, design: .monospaced))
                .foregroundStyle(node.badge.contains("✓") ? Theme.ok : node.badge.contains("✗") ? Theme.err : Theme.muted)
        default:
            if !node.badge.isEmpty { Text(node.badge).font(.system(size: 12)).foregroundStyle(Theme.muted) }
        }
    }

    func activate() {
        switch node.kind {
        case .agent(let id), .terminal(let id): store.open(id)
        case .branch(let pr, let name, _): store.openChanges(project: pr, branch: name)
        case .sessions: store.openTUI() // conch's Sessions view is the TUI's for now
        default: withAnimation(.easeOut(duration: 0.12)) { store.toggle(node) }
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
        .background(RoundedRectangle(cornerRadius: 9).fill(Theme.raise))
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
                        .foregroundStyle(c.default == true ? Theme.onWarn : Theme.text)
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
            // Where the tab on screen is: its project and machine, as the
            // TUI's tab bar ends.
            if let k = store.layout.focused {
                Text(store.scope(of: k)).font(.system(size: 12)).foregroundStyle(Theme.muted)
                    .lineLimit(1).padding(.bottom, 9)
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

    var body: some View {
        let on = index == store.layout.active
        let p = store.pane(tab.focused)
        HStack(spacing: 7) {
            let changes = parseChangesKey(tab.focused)
            if let a = p?.agent {
                Mark(mark: AgentMark.of(a.name))
            } else {
                Text(changes == nil ? "›" : "◇").foregroundStyle(Theme.muted)
            }
            // A branch's changes are named as the mock names them.
            Text(changes.map { "\($0.branch) changes" } ?? p?.displayName ?? tab.focused)
                .font(.system(size: 12.5)).lineLimit(1).truncationMode(.tail)
                .frame(maxWidth: 200, alignment: .leading).fixedSize(horizontal: true, vertical: false)
            if tab.root.panes.contains(where: { store.pane($0)?.agent?.state == .blocked }) {
                Pill(text: "!", fg: Theme.onWarn, bg: Theme.warn)
            }
            if on {
                Button { store.closeSplit() } label: { Image(systemName: "xmark").font(.system(size: 9, weight: .semibold)) }
                    .buttonStyle(.plain).foregroundStyle(Theme.muted)
            }
        }
        .foregroundStyle(on ? Theme.text : Theme.text2)
        .padding(.horizontal, 12).frame(height: 30)
        .background(UnevenRoundedRectangle(topLeadingRadius: 7, topTrailingRadius: 7).fill(on ? Theme.raise.opacity(0.7) : .clear))
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
                ChangesView(store: store, model: store.changes(for: id))
            } else {
                PaneCard(store: store, pane: id, terminal: store.terminal(for: id))
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
                if let a = p?.agent { Mark(mark: AgentMark.of(a.name)) } else { Text("›").foregroundStyle(Theme.muted) }
                Text(p?.agent?.name ?? p?.name ?? pane).font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.text)
                if let b = p?.branch, !b.isEmpty {
                    Text(b).font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.text2)
                        .padding(.horizontal, 6).padding(.vertical, 1).background(RoundedRectangle(cornerRadius: 5).fill(Theme.raise))
                }
                Spacer()
                if p?.running == false {
                    Text("exited \(p?.exitCode ?? 0)").font(.system(size: 11)).foregroundStyle(Theme.muted)
                }
                if let a = p?.agent, a.state == .working, let since = a.since {
                    // How long it has been at it, as the mock's "working · 41s".
                    TimelineView(.periodic(from: .now, by: 1)) { ctx in
                        Pill(text: "working · \(elapsed(since, ctx.date))", fg: Theme.work, bg: Theme.work.opacity(0.18))
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

    static let treeKeys = [("click", "open"), ("⌘K", "search"), ("⌘T", "terminal"), ("⌘D", "split"), ("⌘.", "next waiting")]
    static let paneKeys = [("⌘W", "close split"), ("⌘D", "split"), ("⇧⌘]", "next tab"), ("⌘K", "search"), ("⌘.", "next waiting")]

    var body: some View {
        HStack(spacing: 16) {
            // Where the keyboard is, as the TUI's chip says: a pane, or the
            // window around it.
            let inPane = store.layout.focused != nil && store.terminalHasKeyboard
            // The mock's mode: the word in the accent, no chip behind it.
            Text(inPane ? "PANE" : "TREE").font(.system(size: 12, weight: .semibold))
                .foregroundStyle(inPane ? Theme.ok : Theme.accent)
            if !store.connected { Text("○ not connected").foregroundStyle(Theme.err) }
            // The keys for where the keyboard is, as the mock's bar lists
            // them: the key bright, what it does muted.
            Text((inPane ? Self.paneKeys : Self.treeKeys).map { "\($0.0) \($0.1)" }.joined(separator: " · "))
                .foregroundStyle(Theme.muted).lineLimit(1)
            if let e = store.error {
                Text(e).foregroundStyle(Theme.err).lineLimit(1)
                    .onTapGesture { store.error = nil }
            }
            Spacer()
            // The plan the agents run on, as the TUI's status bar shows it.
            ForEach(store.limits.filter { $0.fiveHour != nil }, id: \.agent) { l in
                HStack(spacing: 6) {
                    Text("\(l.agent.prefix(1).uppercased() + l.agent.dropFirst()) 5h")
                    Meter(fraction: (l.fiveHour?.usedPct ?? 0) / 100)
                    Text("\(Int((l.fiveHour?.usedPct ?? 0).rounded()))%")
                    if let w = l.week { Text("· 7d \(Int(w.usedPct.rounded()))%") }
                }
                .foregroundStyle(Theme.muted)
            }
            if !store.serverVersion.isEmpty {
                Text("v\(store.serverVersion)").foregroundStyle(Theme.muted)
                    .help("conch \(store.serverVersion) · panes drawn with \(PaneTerminal.engine)")
            }
        }
        .font(.system(size: 12))
        .padding(.horizontal, 12).frame(height: 26)
        .background(Theme.side)
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
    }
}

/// A plan's use, as a bar: green, amber from 80%, red past it.
struct Meter: View {
    let fraction: Double

    var body: some View {
        ZStack(alignment: .leading) {
            Capsule().fill(Theme.raise)
            Capsule().fill(fraction >= 1 ? Theme.err : fraction >= 0.8 ? Theme.warn : Theme.ok)
                .frame(width: 60 * min(max(fraction, 0), 1))
        }
        .frame(width: 60, height: 6)
    }
}

/// "41s", "6m", "2h 5m": how long ago a time was.
func elapsed(_ since: Date, _ now: Date) -> String {
    let s = max(Int(now.timeIntervalSince(since)), 0)
    if s < 60 { return "\(s)s" }
    if s < 3600 { return "\(s / 60)m" }
    return "\(s / 3600)h \(s % 3600 / 60)m"
}

// MARK: - palette

struct Palette: View {
    @ObservedObject var store: Store
    @State private var query = ""
    @State private var sel = 0
    @FocusState private var focused: Bool

    var items: [PaletteItem] { filterPalette(store.paletteItems, query: query) }

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
            .background(RoundedRectangle(cornerRadius: 12).fill(Theme.chrome))
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
