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
    /// How the chrome is drawn (View → Style): standard, or Ink.
    static var style = AppStyle.standard

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
    static func agent(_ name: String?) -> Color { markColor(name, on: current.bg, text: current.fg).map(color) ?? muted }
}

extension NSColor {
    convenience init(hex: UInt32) {
        self.init(srgbRed: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255,
                  blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
    }
}

// MARK: - pieces

struct Pill: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let text: String
    let fg: Color
    let bg: Color
    /// A working agent's: the TUI's spinner turns in front of the text.
    var spins = false

    var body: some View {
        let _ = look
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
        .background(RoundedRectangle(cornerRadius: Theme.ink ? 0 : 20).fill(bg))
        .overlay(RoundedRectangle(cornerRadius: 0).strokeBorder(Theme.ink ? Theme.inkLine : .clear, lineWidth: 1.5))
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
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let port: Int
    let local: Bool

    var body: some View {
        let _ = look
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
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let names: [String]

    var body: some View {
        let _ = look
        HStack(spacing: 3) {
            ForEach(names.prefix(4), id: \.self) { Mark(agent: $0) }
            if names.count > 4 { Text("+\(names.count - 4)").font(.system(size: 11)).foregroundStyle(Theme.muted) }
        }
    }
}

/// The sidebar's edge: a line, and a few points either side of it to drag
/// the sidebar wider or narrower, within sidebarRange.
struct SidebarEdge: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    @Binding var width: Double
    @State private var start: Double?

    var body: some View {
        let _ = look
        Rectangle().fill(Theme.ink ? Theme.inkLine : Theme.line.opacity(0.6)).frame(width: Theme.ink ? 3 : 1)
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
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let agent: String?

    var body: some View {
        let _ = look
        if Theme.icons != .off, let logo = AgentLogos.ops(agent) {
            // The agent's own logo, filled in its colour, wherever one is
            // known — the same in either way of marking agents.
            Lucide(ops: logo).fill(Theme.agent(agent), style: FillStyle(eoFill: AgentLogos.logo(agent)?.evenOdd ?? false))
                .frame(width: 13, height: 13).frame(width: 16)
        } else {
            mark
        }
    }

    @ViewBuilder var mark: some View {
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
            .fontDesign(nil) // its own font, whatever design the window asks for
            .frame(width: 16)
        }
    }
}

/// The rounded system face for the window's own text, when asked for.
struct RoundedChrome: ViewModifier {
    let on: Bool
    func body(content: Content) -> some View {
        if on { content.fontDesign(.rounded) } else { content }
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
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let key: String
    init(_ key: String) { self.key = key }

    var body: some View {
        let _ = look
        Text(key).font(.system(size: 10.5, weight: .medium)).foregroundStyle(Theme.text2)
            .padding(.horizontal, 4.5).frame(minWidth: 17, minHeight: 15)
            .background(RoundedRectangle(cornerRadius: Theme.ink ? 0 : 3.5).fill(Theme.ink ? Theme.window : Theme.raise))
            .overlay(RoundedRectangle(cornerRadius: Theme.ink ? 0 : 3.5).strokeBorder(Theme.ink ? Theme.inkLine : Theme.line2, lineWidth: Theme.ink ? 1.5 : 0.5))
    }
}

struct Dot: View {
    let color: Color
    var body: some View { Circle().fill(color).frame(width: 8, height: 8) }
}

// MARK: - toolbar contents

struct WorkspacesControl: View {
    @ObservedObject var store: Store

    /// Its own hosting view in the toolbar, outside the window's
    /// environment: given the theme here, for the views it holds.
    var body: some View { content.environment(\.conchTheme, store.themeLook) }

    @ViewBuilder var content: some View {
        HStack(spacing: 2) {
            ForEach(Array(store.workspaces.enumerated()), id: \.offset) { i, ws in
                let on = i == store.workspace
                Text(Workspace.label(i, ws)).font(.system(size: 12, weight: Theme.ink ? .bold : .regular))
                    .foregroundStyle(Theme.ink && on ? Theme.window : on ? Theme.text : Theme.text2)
                    .padding(.horizontal, 10).padding(.vertical, 3)
                    .background(RoundedRectangle(cornerRadius: Theme.corner(5, meadow: 20)).fill(on ? (Theme.ink ? Theme.inkLine : Theme.meadow ? Theme.window : Theme.raise) : .clear))
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
        .padding(Theme.ink ? 0 : 2)
        .background(RoundedRectangle(cornerRadius: Theme.corner(7, meadow: 22)).fill(Theme.meadow ? Theme.frost : Theme.window))
        .overlay(RoundedRectangle(cornerRadius: Theme.corner(7, meadow: 22)).strokeBorder(Theme.ink ? Theme.inkLine : Theme.meadow ? .clear : Theme.line, lineWidth: Theme.ink ? 2 : 1))
        .help("Workspaces, shared with conch's TUI")
    }
}

struct SearchField: View {
    @ObservedObject var store: Store

    /// Its own hosting view in the toolbar, outside the window's
    /// environment: given the theme here, for the views it holds.
    var body: some View { content.environment(\.conchTheme, store.themeLook) }

    @ViewBuilder var content: some View {
        Button { store.paletteOpen = true } label: {
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass").font(.system(size: 12)).foregroundStyle(Theme.muted)
                Text("Search agents, branches, files, actions").font(.system(size: 13)).foregroundStyle(Theme.muted)
                Spacer(minLength: 8)
                Text("⌘K").font(.system(size: 11, weight: .medium)).foregroundStyle(Theme.muted)
                    .padding(.horizontal, 5).overlay(RoundedRectangle(cornerRadius: 5).stroke(Theme.line2))
            }
            .padding(.horizontal, Theme.meadow ? 14 : 10).frame(width: 420, height: Theme.meadow ? 30 : 28)
            .background(RoundedRectangle(cornerRadius: Theme.corner(8, meadow: 15)).fill(Theme.ink ? Theme.window : Theme.meadow ? Theme.window.opacity(0.92) : Theme.well))
            .overlay(RoundedRectangle(cornerRadius: Theme.corner(8, meadow: 15)).strokeBorder(Theme.ink ? Theme.inkLine : Theme.meadow ? .clear : Theme.line, lineWidth: Theme.ink ? 2 : 1))
        }
        .buttonStyle(.plain)
    }
}

struct Counts: View {
    @ObservedObject var store: Store
    @State private var reviewing = false

    /// Its own hosting view in the toolbar, outside the window's
    /// environment: given the theme here, for the views it holds.
    var body: some View { content.environment(\.conchTheme, store.themeLook) }

    /// Waiting, done and to review, always there — dimmed at nothing, so the
    /// corner says "nothing needs you" rather than looking unfinished.
    @ViewBuilder var content: some View {
        let (w, d) = store.inbox
        let r = store.reviewItems.count
        HStack(spacing: 8) {
            Button { store.nextWaiting() } label: {
                if Theme.ink {
                    BurstCount(text: "\(w) waiting\(w > 0 ? "!" : "")", loud: w > 0, ink: w > 0 ? Theme.inkLine : Theme.muted)
                } else {
                    Pill(text: "\(Theme.meadow ? "☀" : "⚑") \(w) waiting", fg: w > 0 ? Theme.onWarn : Theme.muted, bg: w > 0 ? Theme.warn : Theme.raise)
                }
            }
            .buttonStyle(.plain).disabled(w == 0)
            .help(w > 0 ? "Go to the next agent waiting for you (⌘.)" : "No agent is waiting for you")
            if Theme.ink {
                BurstCount(text: "\(d) done", loud: false, ink: d > 0 ? Theme.inkLine : Theme.muted)
            } else {
                Pill(text: "\(Theme.meadow ? "✿" : "✓") \(d) done", fg: d > 0 ? Theme.ok : Theme.muted,
                     bg: d > 0 ? Theme.ok.opacity(Theme.meadow ? 0.22 : 0.16) : Theme.raise)
            }
            Button { reviewing.toggle() } label: {
                Text("\(r) to review").font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(r > 0 ? Theme.text2 : Theme.muted)
                    .padding(.horizontal, 7).padding(.vertical, 2)
                    .overlay(RoundedRectangle(cornerRadius: Theme.corner(20, meadow: 20, cel: 7)).strokeBorder(r > 0 ? Theme.line2 : Theme.line))
            }
            .buttonStyle(.plain)
            .help("What needs a decision, on every machine: agents waiting or finished, branches with work nobody has shipped")
            .popover(isPresented: $reviewing, arrowEdge: .bottom) { ReviewPopover(store: store) { reviewing = false } }
        }
    }
}

/// The review queue: each row what needs a decision and why, the most
/// urgent first; a click opens the agent, or the branch's changes.
struct ReviewPopover: View {
    @ObservedObject var store: Store
    let close: () -> Void

    var body: some View {
        let items = store.reviewItems
        VStack(alignment: .leading, spacing: 0) {
            Text("To review").font(.system(size: 13, weight: .semibold)).padding(.bottom, 8)
            if items.isEmpty {
                Text("Nothing needs a decision.").foregroundStyle(.secondary).padding(.vertical, 6)
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(items, id: \.key) { it in
                        HStack(spacing: 4) {
                            Button { open(it) } label: {
                                HStack(alignment: .firstTextBaseline, spacing: 8) {
                                    Text(glyph(it.band)).foregroundStyle(color(it.band)).frame(width: 14)
                                    VStack(alignment: .leading, spacing: 1) {
                                        Text([it.projectName, it.branch].filter { !$0.isEmpty }.joined(separator: " · "))
                                            .font(.system(size: 12.5, weight: .medium)).lineLimit(1)
                                        Text(it.detail + (store.machines.count > 1 ? " · " + store.label(it.machine) : ""))
                                            .font(.system(size: 11.5)).foregroundStyle(.secondary).lineLimit(1)
                                    }
                                    Spacer(minLength: 0)
                                }
                                .padding(.vertical, 4).padding(.horizontal, 4).contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                            // The TUI's f and x, as buttons on the row.
                            if it.canFix && !it.branch.isEmpty {
                                Button("Fix") { store.fixCI(it) }.controlSize(.small)
                                    .help("Send CI's failure to the branch's agent (or start one in its worktree with it)")
                            }
                            Button { store.dismiss(it) } label: { LucideIcon("x", color: Theme.muted, size: 11) }
                                .buttonStyle(.plain).help("Dismiss: it comes back if it changes")
                        }
                        .contextMenu {
                            Button("Open") { open(it) }
                            if it.canFix && !it.branch.isEmpty { Button("Send CI's failure to its agent") { store.fixCI(it) } }
                            Button("Dismiss") { store.dismiss(it) }
                        }
                    }
                }
            }
            .frame(minHeight: items.isEmpty ? 0 : min(CGFloat(items.count) * 40, 360), maxHeight: 360)
        }
        .padding(14).frame(width: 380)
    }

    func open(_ it: ReviewItem) {
        if !it.pane.isEmpty {
            store.open(key(it.machine, it.pane))
        } else {
            store.openChanges(project: key(it.machine, it.project), branch: it.branch)
        }
        close()
    }

    func glyph(_ b: ReviewItem.Band) -> String {
        switch b {
        case .waiting: return "⚑"
        case .failed: return "✗"
        case .done: return "✓"
        case .unshipped: return "↑"
        case .dirty: return "±"
        }
    }

    @MainActor func color(_ b: ReviewItem.Band) -> Color {
        switch b {
        case .waiting: return Theme.warn
        case .failed: return Theme.err
        case .done: return Theme.ok
        default: return Theme.muted
        }
    }
}

// MARK: - the window

struct MainView: View {
    @ObservedObject var store: Store
    /// The sidebar's width, dragged at its edge and remembered.
    @AppStorage("sidebarWidth") private var sidebarWidth: Double = sidebarDefault
    /// The window's size, for what Cel folds in a small one.
    @State private var size = CGSize(width: 1320, height: 820)

    var body: some View {
        ZStack {
            VStack(spacing: 0) {
                // Ink: the toolbar ends on a rule, as a page's top panel.
                if Theme.ink { Rectangle().fill(Theme.inkLine).frame(height: AppStyle.ink.border) }
                // Cel: every agent as a card across the top.
                if Theme.style.roster { RosterBar(store: store, whole: AppStyle.celRosterWhole(height: size.height)) }
                HStack(spacing: 0) {
                    if store.sidebarVisible {
                        Sidebar(store: store).frame(width: sidebarWidth)
                        SidebarEdge(width: $sidebarWidth)
                    } else if Theme.style.roster {
                        ProjectRail(store: store)
                    }
                    VStack(spacing: 0) {
                        TabBar(store: store)
                        if store.connected, let tab = store.layout.current {
                            // Zoomed: the focused split alone, filling the tab.
                            SplitArea(store: store, node: tab.zoomed ? .leaf(tab.focused) : tab.root).padding(Theme.style.gutter)
                        } else {
                            Empty(store: store)
                        }
                    }
                    .background(Theme.window)
                    if Theme.style.roster && AppStyle.celSideFits(width: size.width) { CelSide(store: store).background(Theme.window) }
                }
                StatusBar(store: store)
            }
            if store.paletteOpen {
                Palette(store: store)
            }
        }
        .background { GeometryReader { g in Color.clear.onAppear { size = g.size }.onChange(of: g.size) { size = $0 } } }
        .sheet(item: $store.taskDraft) { d in TaskSheet(store: store, draft: d) }
        .sheet(item: $store.broadcastDraft) { d in BroadcastSheet(store: store, draft: d) }
        .sheet(isPresented: $store.helpOpen) { ShortcutsSheet(store: store) }
        .sheet(isPresented: $store.doctorOpen) { DoctorSheet(store: store) }
        .background(Theme.window)
        // Meadow paints the title bar itself: a sky behind the toolbar's
        // controls (AppKit's, so drawn over it), lifted into the title bar
        // by its height — over the backgrounds that reach up there too.
        .overlay(alignment: .top) {
            if Theme.meadow {
                GeometryReader { g in Sky().frame(height: g.safeAreaInsets.top).offset(y: -g.safeAreaInsets.top) }
                    .allowsHitTesting(false)
            }
        }
        // Meadow's rounded face, and only Meadow's: a font design set over
        // the window, even .default, overrode the Nerd Font agents' marks
        // are drawn in, and every mark became a missing-glyph box.
        .modifier(RoundedChrome(on: Theme.meadow))
        .preferredColorScheme(store.theme.light ? .light : .dark)
        // A new theme, style or way of marking agents repaints every view
        // in place: views that watch the store are drawn again as it
        // publishes, and the others read this. It once rebuilt the window
        // instead (.id), which a theme previewed in Settings did at every
        // card the pointer crossed — taking the terminals' keyboard and
        // whatever was typed in the palette or a new task with it.
        .environment(\.conchTheme, store.themeLook)
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
                            SectionHeading(text: "NEEDS YOU")
                            Spacer()
                            Text("\(needs.count)").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                        }
                        .padding(.horizontal, 8).padding(.top, 10).padding(.bottom, 2)
                        ForEach(needs, id: \.key) { n in
                            NeedsYouCard(store: store, key: n.key, pane: n.pane, machine: n.machine)
                        }
                    }
                    SectionHeading(text: "MACHINES")
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
        .background {
            ZStack {
                if Theme.meadow {
                    // Warm paper, a shade deeper at the top.
                    LinearGradient(colors: [Theme.raise, Theme.side], startPoint: .top, endPoint: UnitPoint(x: 0.5, y: 0.4))
                } else {
                    Theme.side
                }
                if Theme.ink { Screentone() }
            }
        }
    }
}

/// A heading in the sidebar: small capitals, or lettering on an Ink page.
struct SectionHeading: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let text: String
    var body: some View {
        let _ = look
        if Theme.ink {
            Lettering(text: text, size: 13, color: Theme.inkLine)
        } else {
            Text(text).font(.system(size: 11, weight: .semibold)).tracking(Theme.meadow ? 1.2 : 0).foregroundStyle(Theme.muted)
        }
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
    /// On an Ink page the focused pane's row is solid ink, its name in paper.
    var inverted: Bool { Theme.ink && selected }
    var rowFill: Color {
        if Theme.ink { return selected ? Theme.inkLine : hovering || cursor ? Theme.raise : .clear }
        // Meadow: a soft wash of the accent, like a leaf's shadow.
        if Theme.meadow { return cursor ? Theme.accentDeep.opacity(0.32) : selected ? Theme.accentDeep.opacity(0.2) : hovering ? Theme.raise : .clear }
        return cursor ? Theme.accentDeep.opacity(0.75) : selected ? Theme.accentDeep.opacity(0.4) : hovering ? Theme.raise : .clear
    }
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
                Text(open && !isSessions ? "▾" : "▸").font(.system(size: 10)).foregroundStyle(inverted ? Theme.window : Theme.muted).frame(width: 10)
                    .contentShape(Rectangle().inset(by: -4))
                    // An agent's helpers and subagents fold at the arrow: a
                    // click on the row itself opens the agent.
                    .onTapGesture { if isPane { withAnimation(.easeOut(duration: 0.12)) { store.toggle(node) } } else { activateRow() } }
            } else {
                Spacer().frame(width: 10)
            }
            icon
            RowSplit {
                Text(node.title).font(isBranch ? .system(size: 12, design: .monospaced) : .system(size: 13, weight: inverted ? .semibold : .regular))
                    .foregroundStyle(inverted ? Theme.window : isMore ? Theme.muted : isSection || isSessions ? Theme.text2 : Theme.text)
                    .lineLimit(1).truncationMode(.tail)
                badge
            }
        }
        .padding(.leading, CGFloat(depth) * 14 + 8).padding(.trailing, 8).frame(height: Theme.meadow ? 28 : 26)
        .background(RoundedRectangle(cornerRadius: Theme.corner(6, meadow: 10)).fill(rowFill))
        .overlay(RoundedRectangle(cornerRadius: Theme.corner(6, meadow: 10)).strokeBorder(cursor ? (Theme.ink ? Theme.spot : Theme.accent) : .clear, lineWidth: Theme.ink ? 2 : 1))
        .contentShape(Rectangle())
        .id(node.id)
        .onHover { hovering = $0 }
        .onTapGesture { activateRow() }
        // A click is a tap gesture, which accessibility cannot see: the row
        // is a button to VoiceOver and the rest, pressed as a click is.
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isButton)
        .accessibilityAction { activateRow() }
        .contextMenu { RowMenu(store: store, node: node) }
    }

    /// A badge part's colour in the theme.
    func tone(_ t: BadgePart.Tone) -> Color {
        switch t {
        case .muted: return Theme.muted
        case .ok, .added: return Theme.ok
        case .warn: return Theme.warn
        case .err, .removed: return Theme.err
        case .work: return Theme.work
        case .merged: return Theme.color(RGB("#A371F7")) // as the commit graph draws a merge
        }
    }

    func activateRow() {
        // A click in the tree gives the tree the keyboard, as in the TUI.
        store.focusSidebar()
        store.treeCursor = node.id
        activate()
    }

    var isPane: Bool {
        switch node.kind {
        case .agent, .terminal: return true
        default: return false
        }
    }

    var isSection: Bool { if case .section = node.kind { return true }; return false }
    var isMore: Bool { if case .more = node.kind { return true }; return false }

    /// A section row's project, from its id ("s:machine|project:agents").
    func sectionProject(_ id: String) -> String? {
        guard id.hasPrefix("s:"), let colon = id.lastIndex(of: ":"), colon > id.index(id.startIndex, offsetBy: 2) else { return nil }
        return String(id[id.index(id.startIndex, offsetBy: 2)..<colon])
    }
    /// A row that opens a tab rather than folding: Sessions, Files.
    var isSessions: Bool {
        switch node.kind {
        case .sessions, .files: return true
        default: return false
        }
    }
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
        case .files: LucideIcon("folder-open", color: Theme.muted, size: 12)
        case .subagent:
            // At work inside its agent, as the TUI draws it: the spinner.
            TimelineView(.periodic(from: .now, by: spinnerInterval)) { ctx in
                Text(spinnerFrame(at: ctx.date)).font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.work)
            }
            .frame(width: 16)
        case .more: Color.clear.frame(width: 14, height: 12) // the names line up with the branches'
        case .savedHost: LucideIcon(name: .globe, color: Theme.muted, size: 12)
        case .section(let name):
            // Each group with an icon of its own, as Files and Sessions have.
            LucideIcon(name == "Agents" ? "bot" : name == "Branches" ? "git-branch" : "square-terminal", color: Theme.muted, size: 12)
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
                HStack(spacing: 6) {
                    // Its branch, muted, as the TUI's row has it: what it is
                    // working on, cut in the middle before the state gives
                    // way — the state is what a glance is for.
                    if let b = p.branch, !b.isEmpty {
                        Text(b).font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.muted)
                            .lineLimit(1).truncationMode(.middle)
                            .help(b)
                    }
                    // What it has spent, as the TUI's row says it.
                    let (m, pid) = unkey(id)
                    let c = store.costChip(machine: m, pane: pid)
                    if !c.isEmpty {
                        Text(c).font(.system(size: 11)).foregroundStyle(Theme.muted).fixedSize()
                            .help(p.agent?.tokens.map(tokenSummary) ?? c)
                    }
                    if let end = paneEndBadge(p) {
                        // exited, exit N or failed in red, a terminal's # / ~ alert.
                        Text(end.text).font(.system(size: 11.5, weight: end.tone == .err ? .semibold : .regular)).foregroundStyle(tone(end.tone))
                            .fixedSize().layoutPriority(1)
                    } else if let pill = Pill.state(p.agent?.state) {
                        pill.fixedSize().layoutPriority(1)
                    }
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
                    let c = store.costChip(machine: m)
                    if !c.isEmpty { muted(c).padding(.trailing, 2) }
                    if let link = store.machines.first(where: { $0.id == m }), !link.behind.isEmpty {
                        // Its server is older than this app's conch: what
                        // it lacks, and what brings it.
                        Text("behind").font(.system(size: 11.5)).foregroundStyle(Theme.warn)
                            .help("conch there is from an older build and lacks \(link.behind.joined(separator: ", ")). Right-click → \(m == "local" ? "Reload server" : "Upgrade conch there").")
                    }
                    counts(node.badge)
                    // Folded, it says which agents it holds, as the mock's
                    // busybox row does.
                    if !open { FoldedAgents(names: store.snapshots[m]?.agentNames(in: store.ws, machine: m) ?? []) }
                }.fixedSize()
            }
        case .project(let pk):
            HStack(spacing: 6) {
                let c = store.costChip(machine: unkey(pk).machine, project: unkey(pk).id)
                if !c.isEmpty { muted(c) }
                counts(node.badge)
            }.fixedSize()
        case .section(let name) where name == "Agents":
            HStack(spacing: 6) {
                if let pk = sectionProject(node.id) {
                    let c = store.costChip(machine: unkey(pk).machine, project: unkey(pk).id, agentsOnly: true)
                    if !c.isEmpty { muted(c) }
                }
                if !node.badge.isEmpty { muted(node.badge) }
            }.fixedSize()
        case .branch:
            // Each part in its own colour, as the TUI draws a branch row.
            node.parts.enumerated().reduce(Text("")) { acc, e in
                acc + Text((e.offset == 0 ? "" : " ") + e.element.text).foregroundColor(tone(e.element.tone))
            }
            .font(.system(size: 11.5, design: .monospaced))
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
        case .files(let pk): store.openFiles(project: pk)
        case .more(let pk, _): store.toggleAllBranches(pk)
        case .savedHost(let h): store.connectSaved(h)
        // A subagent has no screen of its own: its agent's is shown.
        case .subagent(let pane, _): store.open(pane)
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

/// One message to several panes, as the TUI's broadcast: who it goes to,
/// ticked as the TUI ticks them, and what it says. ⌘↩ sends.
struct BroadcastSheet: View {
    @ObservedObject var store: Store
    @State var draft: BroadcastDraft
    @FocusState private var typing: Bool

    var body: some View {
        let n = draft.targets.filter(\.ticked).count
        let shells = draft.targets.contains { $0.ticked && $0.agent == nil }
        VStack(alignment: .leading, spacing: 12) {
            Text("Broadcast · \(draft.title)").font(.system(size: 15, weight: .semibold))
            Text("An agent takes it as its next message; a terminal runs it as a command.")
                .font(.system(size: 12)).foregroundStyle(.secondary)
            TextEditor(text: $draft.text)
                .font(.system(size: 13)).frame(minHeight: 90)
                .scrollContentBackground(.hidden).padding(6)
                .background(RoundedRectangle(cornerRadius: 6).fill(Color(nsColor: .textBackgroundColor)))
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.secondary.opacity(0.3)))
                .focused($typing)
            ScrollView {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach($draft.targets) { $t in
                        HStack(spacing: 8) {
                            Toggle("", isOn: $t.ticked).toggleStyle(.checkbox).labelsHidden()
                            if let a = t.agent { Mark(agent: a) } else { LucideIcon(name: .terminal, color: Theme.muted, size: 12) }
                            Text(t.name).font(.system(size: 12.5)).lineLimit(1)
                            if t.waiting {
                                Text("waiting on an answer: this would answer it").font(.system(size: 11)).foregroundStyle(Theme.warn)
                            }
                            Spacer()
                        }
                    }
                }
            }
            .frame(maxHeight: 220)
            HStack {
                if shells { Text("Terminals ticked run it as a command.").font(.system(size: 11.5)).foregroundStyle(Theme.warn) }
                Spacer()
                Button("Cancel") { store.broadcastDraft = nil }.keyboardShortcut(.cancelAction)
                Button("Send to \(n)") { store.sendBroadcast(draft) }
                    .keyboardShortcut(.return, modifiers: .command)
                    .disabled(n == 0 || draft.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(18)
        .frame(width: 520)
        .onAppear { typing = true }
    }
}

/// A new task: a prompt, the agent to give it to, and, if wanted, the
/// branch's name — `conch task`, from a sheet. The server makes the branch
/// and its worktree and starts the agent there.
struct TaskSheet: View {
    @ObservedObject var store: Store
    @State var draft: TaskDraft
    @State private var issues: [IssueInfo] = []
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
            HStack(spacing: 12) {
                if !issues.isEmpty {
                    Picker("Issue", selection: Binding(get: { draft.issue ?? 0 }, set: { draft.issue = $0 == 0 ? nil : $0 })) {
                        Text("None").tag(0)
                        ForEach(issues) { Text("#\($0.number) \($0.title)").lineLimit(1).tag($0.number) }
                    }
                    .frame(maxWidth: 300)
                    .help("Work on a GitHub issue: its title and body are the prompt, and anything typed above is added")
                }
                TextField("When: now, 14:05, +30m, every 1d, queue, after BRANCH", text: $draft.when)
                    .frame(maxWidth: 360)
                    .help("Later is kept by the server, and starts with conch closed too")
            }
            HStack {
                Spacer()
                Button("Cancel") { store.taskDraft = nil }.keyboardShortcut(.cancelAction)
                Button(taskWhenArgs(draft.when).isEmpty ? "Start" : "Schedule") { store.startTask(draft) }
                    .keyboardShortcut(.return, modifiers: .command)
                    .disabled(draft.prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && (draft.issue ?? 0) == 0)
            }
            Text("⌘↩ starts it").font(.system(size: 11)).foregroundStyle(.secondary)
        }
        .padding(18)
        .frame(width: 560)
        .onAppear { promptFocused = true }
        .task { issues = await store.issues(for: draft.project) }
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
        .padding(.horizontal, Theme.ink ? 16 : 11).padding(.top, Theme.ink ? 12 : 10).padding(.bottom, Theme.ink ? 24 : 10)
        .background {
            if Theme.ink {
                InkBubble()
            } else if Theme.meadow {
                // A note pinned in the tree.
                Color.clear.meadowNote()
            } else {
                RoundedRectangle(cornerRadius: 10).fill(hovering ? Theme.raise : Theme.raise.opacity(0.7))
            }
        }
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.ink || Theme.meadow ? .clear : Theme.warn.opacity(0.55)))
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
                        .foregroundStyle(Theme.ink ? (on ? Theme.window : Theme.inkLine) : on ? Theme.onWarn : Theme.text)
                        .padding(.horizontal, 9).padding(.vertical, 3)
                        .frame(maxWidth: short ? nil : .infinity, alignment: .leading)
                        .modifier(AnswerLook(on: on))
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
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.ink ? Theme.inkLine : Theme.line).frame(height: Theme.ink ? AppStyle.ink.border : 1) }
    }
}

struct TabButton: View {
    @ObservedObject var store: Store
    let index: Int
    let tab: ConchKit.Tab

    /// What the tab is called: a branch's changes as the mock names them,
    /// a project's sessions or commits by the project, else the pane.
    func title(changes: (machine: String, project: String, branch: String)?, pane p: PaneInfo?) -> String {
        if let n = tab.name { return n }
        if let c = changes { return "\(c.branch) changes" }
        func project(_ m: String, _ id: String) -> String { store.snapshots[m]?.project(id)?.name ?? id }
        if let s = parseSessionsKey(tab.focused) { return "Sessions · " + project(s.machine, s.project) }
        if let c = parseCommitsKey(tab.focused) { return "Commits · " + project(c.machine, c.project) }
        if let f = parseFilesKey(tab.focused) { return "Files · " + project(f.machine, f.project) }
        if let s = parseSetupKey(tab.focused) { return "Agent setup · " + project(s.machine, s.project) }
        return p?.displayName ?? tab.focused
    }

    var body: some View {
        let on = index == store.layout.active
        let p = store.pane(tab.focused)
        HStack(spacing: 7) {
            let changes = parseChangesKey(tab.focused)
            // Its number, for ⌘1–⌘9.
            if index < 9 {
                Text("\(index + 1)").font(.system(size: 10.5, weight: .medium, design: .monospaced)).foregroundStyle(Theme.muted)
            }
            if let a = p?.agent {
                Mark(agent: a.name)
            } else {
                if parseSessionsKey(tab.focused) != nil {
                    LucideIcon("history", color: Theme.accent, size: 12)
                } else if parseCommitsKey(tab.focused) != nil {
                    LucideIcon("git-commit-horizontal", color: Theme.accent, size: 12)
                } else if parseSetupKey(tab.focused) != nil {
                    LucideIcon("bot", color: Theme.accent, size: 12)
                } else if parseFilesKey(tab.focused) != nil {
                    LucideIcon("folder-open", color: Theme.accent, size: 12)
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
            if tab.root.panes.count > 1 {
                // More than one split in it, as the TUI's tab bar marks it.
                Text("⊞").font(.system(size: 11)).foregroundStyle(Theme.muted).help("\(tab.root.panes.count) splits")
            }
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
        .foregroundStyle(Theme.ink && on ? Theme.window : on ? Theme.text : Theme.text2)
        .font(.system(size: 12.5, weight: Theme.ink ? .semibold : .regular))
        .padding(.horizontal, 12).frame(height: 30)
        .background {
            if Theme.ink {
                // A paper tab, inked; the one on screen solid ink.
                Rectangle().fill(on ? Theme.inkLine : Theme.window)
                    .overlay(Rectangle().strokeBorder(Theme.inkLine, lineWidth: 2))
            } else {
                UnevenRoundedRectangle(topLeadingRadius: Theme.meadow ? 12 : 7, topTrailingRadius: Theme.meadow ? 12 : 7).fill(on ? Theme.raise : .clear)
            }
        }
        .overlay(alignment: .topTrailing) {
            // Meadow: a leaf on the tab on screen, where the others have none.
            if on && Theme.meadow { Text("❧").font(.system(size: 11)).foregroundStyle(Theme.accentDeep).rotationEffect(.degrees(-20)).offset(x: 1, y: -1) }
        }
        .overlay(alignment: .bottom) {
            if on && !Theme.ink && !Theme.meadow { RoundedRectangle(cornerRadius: 1).fill(Theme.accent).frame(height: 2).padding(.horizontal, 8).offset(y: 1) }
        }
        .contentShape(Rectangle())
        .onTapGesture {
            store.layout.select(index)
            store.focusTerminal()
        }
        .onTapGesture(count: 2) { store.renameTab(index) }
        // Pressed as a click is, its close button still its own.
        .accessibilityElement(children: .contain)
        .accessibilityAddTraits(.isButton)
        .accessibilityAction {
            store.layout.select(index)
            store.focusTerminal()
        }
        .contextMenu {
            Button("Rename Tab…") { store.renameTab(index) }
            if tab.name != nil { Button("Name It by Its Pane") { store.layout.rename(index, to: "") } }
        }
        // Dragged onto another tab, it takes that one's place.
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

// MARK: - splits

struct SplitArea: View {
    @ObservedObject var store: Store
    let node: SplitNode
    /// The node's path from the tab's root, which its sizes are kept by.
    var path = ""

    var body: some View {
        switch node {
        case .leaf(let id):
            if parseChangesKey(id) != nil {
                ChangesView(store: store, model: store.changes(for: id)).id(id)
            } else if parseSessionsKey(id) != nil {
                SessionsView(model: store.sessions(for: id), store: store).id(id)
            } else if parseCommitsKey(id) != nil {
                CommitsView(model: store.commits(for: id), store: store).id(id)
            } else if parseSetupKey(id) != nil {
                SetupView(model: store.setup(for: id), store: store).id(id)
            } else if parseFilesKey(id) != nil {
                FilesView(model: store.files(for: id), store: store).id(id)
            } else {
                // Its own card for each pane, so moving to another tab
                // never shows one pane's terminal under another's header.
                PaneCard(store: store, pane: id, terminal: store.terminal(for: id)).id(id)
            }
        case .split(let axis, let kids):
            SplitStack(store: store, axis: axis, kids: kids, path: path)
        }
    }
}

/// A split's children side by side or stacked, each its share of the
/// length (Tab.shares), and between each two a gap that drags to move the
/// boundary — tmux's resize, by the mouse.
struct SplitStack: View {
    @ObservedObject var store: Store
    let axis: SplitNode.Axis
    let kids: [SplitNode]
    let path: String
    @State private var dragged: CGFloat = 0

    var body: some View {
        GeometryReader { g in
            let gap = CGFloat(Theme.style.gutter)
            let total = (axis == .horizontal ? g.size.width : g.size.height) - gap * CGFloat(kids.count - 1)
            let w = store.layout.current?.shares(path, kids.count) ?? Array(repeating: 1 / Double(kids.count), count: kids.count)
            let layout = axis == .horizontal ? AnyLayout(HStackLayout(spacing: 0)) : AnyLayout(VStackLayout(spacing: 0))
            layout {
                ForEach(Array(kids.enumerated()), id: \.offset) { i, k in
                    let len = max(total * CGFloat(w[i]), 0)
                    SplitArea(store: store, node: k, path: path.isEmpty ? "\(i)" : "\(path).\(i)")
                        .frame(width: axis == .horizontal ? len : nil, height: axis == .vertical ? len : nil)
                    if i < kids.count - 1 { handle(after: i, gap: gap, total: total) }
                }
            }
        }
    }

    func handle(after i: Int, gap: CGFloat, total: CGFloat) -> some View {
        Color.clear
            .frame(width: axis == .horizontal ? gap : nil, height: axis == .vertical ? gap : nil)
            .contentShape(Rectangle())
            .onHover { inside in
                if inside { (axis == .horizontal ? NSCursor.resizeLeftRight : NSCursor.resizeUpDown).push() } else { NSCursor.pop() }
            }
            .gesture(DragGesture(minimumDistance: 1)
                .onChanged { v in
                    let now = axis == .horizontal ? v.translation.width : v.translation.height
                    guard total > 0 else { return }
                    store.resizeSplit(path, n: kids.count, after: i, by: Double((now - dragged) / total))
                    dragged = now
                }
                .onEnded { _ in dragged = 0 })
            .help("Drag to resize the splits")
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
                // Its context against the model's window, output and cost,
                // as the TUI's pane header has them.
                if store.showCost, let t = p?.agent?.tokens {
                    Text(tokenSummary(t)).font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.muted).lineLimit(1)
                }
                Spacer()
                if let p, p.agent == nil, let port = store.port(of: pane) {
                    PortLink(port: port, local: unkey(pane).machine == "local")
                }
                if p?.running == false {
                    Text("exited \(p?.exitCode ?? 0)").font(.system(size: 11)).foregroundStyle(Theme.muted)
                }
                if Theme.ink, let a = p?.agent, let word = inkState(a) {
                    // Ink: the state as lettering, and a working agent's
                    // sound effect beside it.
                    if a.state == .working {
                        Lettering(text: "tap tap", size: 12, color: Theme.spot).rotationEffect(.degrees(-6))
                    }
                    if a.state == .working, let since = a.since {
                        TimelineView(.periodic(from: .now, by: 1)) { ctx in
                            Lettering(text: "\(word) \(elapsed(since, ctx.date))", size: 12, color: Theme.work)
                        }
                    } else {
                        Lettering(text: word, size: 12, color: a.state == .blocked ? Theme.spot : Theme.ok)
                    }
                } else if let a = p?.agent, a.state == .working, let since = a.since {
                    // How long it has been at it, as the mock's "working · 41s".
                    TimelineView(.periodic(from: .now, by: spinnerInterval)) { ctx in
                        Pill(text: "\(spinnerFrame(at: ctx.date)) working · \(elapsed(since, ctx.date))", fg: Theme.work, bg: Theme.work.opacity(0.18))
                    }
                } else {
                    Pill.state(p?.agent?.state)
                }
            }
            .padding(.horizontal, 10).frame(height: 30)
            .background(Theme.ink ? Theme.window : Theme.paneHeader)
            .overlay(alignment: .bottom) { Rectangle().fill(Theme.ink ? Theme.inkLine : Theme.line).frame(height: Theme.ink ? 2 : 1) }
            if let q = store.questions[pane], !q.choices.isEmpty {
                AnswerStrip(store: store, pane: pane, question: q)
            }
            TerminalHost(terminal: terminal)
                .overlay(alignment: .topTrailing) {
                    if store.find?.pane == pane { FindBar(store: store).padding(8) }
                }
        }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: CGFloat(Theme.style.corner)))
        .modifier(CardFrame(focused: focused))
        .onTapGesture { store.focus(pane) }
    }
}

/// A pane's find bar, as a terminal's ⌘F: the query, which way to look,
/// and what the last look said. Return looks older, as the TUI's / does —
/// what was just printed is usually what is wanted — and the arrows both
/// ways; esc closes it.
struct FindBar: View {
    @ObservedObject var store: Store
    @FocusState private var typing: Bool

    var body: some View {
        let f = store.find
        HStack(spacing: 6) {
            LucideIcon("search", color: Theme.muted, size: 12)
            TextField("Find in pane", text: Binding(get: { store.find?.query ?? "" }, set: { store.find?.query = $0 }))
                .textFieldStyle(.plain).font(.system(size: 12)).frame(width: 170)
                .focused($typing)
                .onSubmit { store.findNext(back: true); typing = true }
                .onExitCommand { store.closeFind() }
            if let note = f?.note, !note.isEmpty {
                Text(note).font(.system(size: 11)).foregroundStyle(note == "Not found" || note.hasPrefix("conch") ? Theme.err : Theme.muted)
                    .lineLimit(1).frame(maxWidth: 180)
            }
            Button { store.findNext(back: true) } label: { LucideIcon("chevron-up", size: 12) }
                .buttonStyle(.plain).help("Older (⌘G)").disabled(f?.query.isEmpty ?? true)
            Button { store.findNext(back: false) } label: { LucideIcon("chevron-down", size: 12) }
                .buttonStyle(.plain).help("Newer (⇧⌘G)").disabled(f?.query.isEmpty ?? true)
            Button { store.closeFind() } label: { LucideIcon("x", size: 12) }
                .buttonStyle(.plain).help("Close (esc)")
        }
        .padding(.horizontal, 10).padding(.vertical, 6)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.paneHeader))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
        .shadow(color: .black.opacity(0.25), radius: 6, y: 2)
        .onAppear { typing = true }
        .onChange(of: store.findFocus) { _ in typing = true }
    }
}

/// The same answers along the top of the agent's own card.
struct AnswerStrip: View {
    @ObservedObject var store: Store
    let pane: String
    let question: Question

    var body: some View {
        HStack(spacing: 6) {
            Text(Theme.ink ? "!?" : Theme.meadow ? "☀" : "⚑").font(.system(size: 12, weight: Theme.ink ? .black : .regular)).foregroundStyle(Theme.ink ? Theme.spot : Theme.warn)
            Text(question.text.isEmpty ? "Waiting for your answer" : question.text).font(.system(size: 12))
                .foregroundStyle(Theme.text).lineLimit(1)
            Spacer(minLength: 8)
            ForEach(question.choices.prefix(4), id: \.choice) { c in
                Button(c.label) { store.answer(pane, question, c) }
                    .buttonStyle(.plain).font(.system(size: 12, weight: c.default == true ? .semibold : .regular))
                    .lineLimit(1).frame(maxWidth: 200)
                    .foregroundStyle(Theme.ink ? (c.default == true ? Theme.window : Theme.inkLine) : c.default == true ? Theme.onWarn : Theme.text)
                    .padding(.horizontal, 9).padding(.vertical, 3)
                    .modifier(AnswerLook(on: c.default == true, edged: false))
            }
        }
        .padding(.horizontal, Theme.ink ? 18 : Theme.meadow ? 16 : 10).padding(.top, Theme.ink ? 8 : Theme.meadow ? 10 : 6).padding(.bottom, Theme.ink ? 20 : Theme.meadow ? 10 : 6)
        .background {
            if Theme.ink {
                // Ink: the question in a speech bubble, its tail down into
                // the agent that asked.
                InkBubble().padding(.horizontal, 6).padding(.top, 4)
            } else if Theme.meadow {
                // Meadow: a note pinned along the top of the card.
                Color.clear.meadowNote().padding(.horizontal, 6).padding(.vertical, 4)
            } else {
                Theme.warn.opacity(0.10)
            }
        }
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.ink || Theme.meadow ? .clear : Theme.warn.opacity(0.4)).frame(height: 1) }
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
            // What was just done, or why not, in place of the keys while
            // it is said; a click puts the keys back.
            if let f = store.flash {
                Text(f.text).foregroundStyle(f.tone == .err ? Theme.err : f.tone == .ok ? Theme.ok : Theme.text2)
                    .lineLimit(1).truncationMode(.tail)
                    .onTapGesture { store.dismissFlash() }
                    .help(f.text)
            } else {
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
            }
            if let j = jobSummary(store.jobs.map(\.what)) {
                HStack(spacing: 6) {
                    ProgressView().controlSize(.mini)
                    Text(j).foregroundStyle(Theme.text2).lineLimit(1)
                }
                .fixedSize()
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
            // ✦ Ask, report a problem and pair a phone, as the TUI's bar has.
            StatusExtras(store: store)
            if !store.serverVersion.isEmpty {
                Text("v\(store.serverVersion)").foregroundStyle(Theme.muted)
                    .help("conch \(store.serverVersion) · panes drawn with \(PaneTerminal.engine)")
            }
            // Settings, as the TUI's bar ends: conch's settings page.
            Button { store.openSettings() } label: {
                Text("⚙ Settings").font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.selectedText)
                    .padding(.horizontal, 7).padding(.vertical, 1)
                    .background(RoundedRectangle(cornerRadius: Theme.corner(4, meadow: 9)).fill(Theme.accentDeep))
                    .overlay(Rectangle().strokeBorder(Theme.ink ? Theme.inkLine : .clear, lineWidth: 1.5))
            }
            .buttonStyle(.plain).help("conch's settings (⌘,); an open TUI takes in what you save")
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
        .font(.system(size: 12, weight: Theme.ink ? .semibold : .regular))
        .padding(.horizontal, 12).frame(height: Theme.ink ? 28 : 26)
        .background { ZStack { Theme.side; if Theme.ink { Screentone() } } }
        .overlay(alignment: .top) {
            if Theme.meadow {
                // A strip of grass the bar sits under.
                GrassEdge().fill(LinearGradient(colors: [Theme.grass, Theme.grassDeep], startPoint: .top, endPoint: .bottom))
                    .frame(height: 7).offset(y: -4)
            } else {
                Rectangle().fill(Theme.ink ? Theme.inkLine : Theme.line).frame(height: Theme.ink ? AppStyle.ink.border : 1)
            }
        }
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
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let limits: PlanLimits

    var body: some View {
        let _ = look
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
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let fraction: Double
    var width: CGFloat = 60
    /// The empty part's colour: the raised one, or a line's on a raised
    /// background, where the raised one would vanish.
    var track: Color = Theme.raise

    var body: some View {
        let _ = look
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
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let item: PaletteItem
    let selected: Bool

    var body: some View {
        let _ = look
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

// MARK: - the Ink style's frames

/// A pane's frame: the accent's hairline round a card, or on an Ink page a
/// square inked panel, the focused one with its spot-colour shadow.
struct CardFrame: ViewModifier {
    /// Read so this is drawn again when the theme changes: a modifier
    /// whose own properties are unchanged is otherwise not.
    @Environment(\.conchTheme) private var look
    let focused: Bool
    func body(content: Content) -> some View {
        let _ = look
        if Theme.ink {
            content.inkPanel(focused: focused)
        } else if Theme.meadow {
            content.meadowCard(focused: focused)
        } else if Theme.cel {
            // A cel: outlined, its hard shadow in the accent when focused.
            let shape = RoundedRectangle(cornerRadius: AppStyle.cel.corner)
            content
                .overlay(shape.strokeBorder(Theme.celLine, lineWidth: AppStyle.cel.border))
                .background(shape.fill(focused ? Theme.accentDeep : Theme.celLine).offset(x: 5, y: 5))
        } else {
            content
                .overlay(RoundedRectangle(cornerRadius: 9).stroke(focused ? Theme.accent : Theme.line, lineWidth: 1))
                .shadow(color: focused ? Theme.accent.opacity(0.25) : .clear, radius: 0.5)
        }
    }
}

/// An answer button: amber for the one the agent's cursor is on, or on an
/// Ink page a square inked box, that one solid ink.
struct AnswerLook: ViewModifier {
    /// Read so this is drawn again when the theme changes: a modifier
    /// whose own properties are unchanged is otherwise not.
    @Environment(\.conchTheme) private var look
    let on: Bool
    /// The tree's buttons have an edge; the strip's, on its amber wash, none.
    var edged = true
    func body(content: Content) -> some View {
        let _ = look
        if Theme.ink {
            content.inkBox(fill: on ? Theme.inkLine : Theme.window)
        } else if Theme.meadow {
            content.background(Capsule().fill(on ? Theme.warn : Theme.raise))
        } else if Theme.cel {
            content.celBox(7, fill: on ? Theme.warn : Theme.window, shadow: on ? 2 : 0)
        } else {
            content
                .background(RoundedRectangle(cornerRadius: 6).fill(on ? Theme.warn : Theme.raise))
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(edged ? (on ? Theme.warn : Theme.line2) : .clear))
        }
    }
}

/// An agent's state as Ink letters it; nil for one with nothing to say.
@MainActor func inkState(_ a: AgentStatus) -> String? {
    switch a.state {
    case .working: return "working"
    case .blocked: return "waiting!"
    case .done: return "done"
    default: return nil
    }
}
