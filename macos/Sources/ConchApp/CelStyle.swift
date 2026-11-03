import ConchKit
import SwiftUI

// The Cel style's pieces (AppStyle.cel): flat fills, one hard shadow, a
// thick dark outline — and its own layout, a roster of agents across the
// top, a column of projects, and on the right the question someone is
// waiting on with a log of what the agents last did.

extension Theme {
    static var cel: Bool { style == .cel }
    /// The outline everything is drawn with: near-black, a little softer
    /// on a light theme.
    static var celLine: Color { current.light ? Color.black.opacity(0.82) : Color(nsColor: NSColor(hex: 0x07080F)) }
}

extension View {
    /// A cel: a flat fill, a dark outline, and one hard shadow down and to
    /// the right (in `lift`, the outline's colour unless told otherwise).
    func celBox(_ radius: CGFloat = 10, fill: Color, lift: Color? = nil, shadow: CGFloat = 4) -> some View {
        let shape = RoundedRectangle(cornerRadius: radius)
        return self
            .background(shape.fill(fill))
            // An opaque base, so a fill with some see-through to it never
            // shows the shadow through the cel.
            .background(shape.fill(Theme.window))
            .overlay(shape.strokeBorder(Theme.celLine, lineWidth: AppStyle.cel.border))
            .background(shape.fill(lift ?? Theme.celLine).offset(x: shadow, y: shadow))
    }
}

/// The roster: every agent and terminal the workspace shows, as cards.
struct RosterBar: View {
    @ObservedObject var store: Store
    /// Cards whole, or one line each in a short window.
    var whole = true

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 14) {
                ForEach(store.rosterCards, id: \.key) { c in RosterCardView(store: store, card: c, whole: whole) }
                Button { store.newTerminal(split: nil) } label: {
                    Text("+").font(.system(size: whole ? 24 : 16, weight: .bold)).foregroundStyle(Theme.muted)
                        .frame(width: whole ? 60 : 40, height: whole ? 104 : 34)
                        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Theme.line, style: StrokeStyle(lineWidth: 2, dash: [6, 4])))
                }
                .buttonStyle(.plain).focusable(false).help("New terminal (⌘T)")
            }
            .padding(.horizontal, 16).padding(.top, whole ? 12 : 8).padding(.bottom, whole ? 16 : 11)
        }
        .background(Stripes())
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.celLine).frame(height: 2) }
    }
}

/// The roster's backing: faint diagonal stripes.
struct Stripes: View {
    var body: some View {
        let a = Theme.side, b = Theme.raise.opacity(0.55)
        return Canvas { ctx, size in
            ctx.fill(Path(CGRect(origin: .zero, size: size)), with: .color(a))
            var x: CGFloat = -size.height
            while x < size.width {
                var p = Path()
                p.move(to: CGPoint(x: x, y: size.height))
                p.addLine(to: CGPoint(x: x + size.height, y: 0))
                p.addLine(to: CGPoint(x: x + size.height + 14, y: 0))
                p.addLine(to: CGPoint(x: x + 14, y: size.height))
                p.closeSubpath()
                ctx.fill(p, with: .color(b))
                x += 28
            }
        }
        .allowsHitTesting(false)
    }
}

/// One card: the agent's mark as a face, its name and place, a bar for its
/// state, and since when.
struct RosterCardView: View {
    @ObservedObject var store: Store
    let card: RosterCard
    var whole = true

    var color: Color {
        if !card.running { return Theme.muted }
        guard card.agent != nil else { return Theme.muted }
        switch card.state {
        case .blocked: return Theme.warn
        case .working: return Theme.work
        case .done: return Theme.ok
        default: return Theme.muted
        }
    }

    var body: some View {
        let on = store.layout.focused == card.key
        if whole { full(on) } else { line(on) }
    }

    /// A short window's card: the mark, the name, the state's word.
    func line(_ on: Bool) -> some View {
        HStack(spacing: 8) {
            Mark(agent: card.agent)
            Text(card.title).font(.system(size: 12, weight: .heavy)).foregroundStyle(Theme.text).lineLimit(1)
            Spacer(minLength: 4)
            Text(card.bar.word.uppercased()).font(.system(size: 10, weight: .heavy)).foregroundStyle(color).lineLimit(1)
        }
        .padding(.horizontal, 10).frame(width: 214, height: 34)
        .celBox(9, fill: on ? Theme.accentDeep.opacity(0.28) : Theme.raise, lift: on ? Theme.accentDeep : nil, shadow: 3)
        .contentShape(Rectangle())
        .onTapGesture {
            store.open(card.key)
            store.focusTerminal()
        }
        .help(card.agent.map { "\($0) · \(card.title)" } ?? card.title)
    }

    func full(_ on: Bool) -> some View {
        let bar = card.bar
        return VStack(alignment: .leading, spacing: 7) {
            HStack(spacing: 9) {
                face
                VStack(alignment: .leading, spacing: 2) {
                    Text(card.title).font(.system(size: 13, weight: .heavy)).foregroundStyle(Theme.text).lineLimit(1)
                    Text(card.place.isEmpty ? " " : card.place).font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.muted).lineLimit(1)
                }
                Spacer(minLength: 0)
                if on { Text("▶").font(.system(size: 11)).foregroundStyle(Theme.accentDeep) }
            }
            // The bar, flat with a highlight on its upper half.
            GeometryReader { g in
                ZStack(alignment: .leading) {
                    Theme.window
                    Rectangle().fill(color).frame(width: g.size.width * bar.fill)
                        .overlay(alignment: .top) { Rectangle().fill(.white.opacity(0.25)).frame(height: g.size.height * 0.45) }
                }
            }
            .frame(height: 9)
            .clipShape(RoundedRectangle(cornerRadius: 5))
            .overlay(RoundedRectangle(cornerRadius: 5).strokeBorder(Theme.celLine, lineWidth: 2))
            HStack {
                Text(bar.word.uppercased()).font(.system(size: 11, weight: .heavy)).foregroundStyle(color)
                Spacer()
                if let t = card.since, card.agent != nil {
                    TimelineView(.periodic(from: .now, by: 30)) { ctx in
                        Text(shortAgo(t, ctx.date)).font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                    }
                }
            }
        }
        .padding(.horizontal, 12).padding(.vertical, 10)
        .frame(width: 214)
        .celBox(12, fill: on ? Theme.accentDeep.opacity(0.28) : Theme.raise, lift: on ? Theme.accentDeep : nil, shadow: 5)
        .contentShape(Rectangle())
        .onTapGesture {
            store.open(card.key)
            store.focusTerminal()
        }
        .help(card.agent.map { "\($0) · \(card.title)" } ?? card.title)
    }

    /// The agent's mark large on a tile of its colour, its lower half in
    /// shade — the cel's one shadow.
    var face: some View {
        ZStack {
            RoundedRectangle(cornerRadius: 10).fill(card.agent.map { Theme.agent($0).opacity(0.28) } ?? Theme.window)
            if card.agent != nil {
                Mark(agent: card.agent).scaleEffect(1.35)
            } else {
                Text("$_").font(.system(size: 14, weight: .heavy, design: .monospaced)).foregroundStyle(Theme.text2)
            }
        }
        .frame(width: 38, height: 38)
        .overlay(alignment: .bottom) { Rectangle().fill(.black.opacity(0.14)).frame(height: 19).allowsHitTesting(false) }
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Theme.celLine, lineWidth: 2.5))
    }
}

/// The column of projects in place of the tree: the tree itself at the
/// top, every project as its two letters (a click narrows the roster to
/// it), and all of them.
struct ProjectRail: View {
    @ObservedObject var store: Store

    func tile(_ text: String, on: Bool, help: String, size: CGFloat = 13, _ act: @escaping () -> Void) -> some View {
        Button(action: act) {
            Text(text).font(.system(size: size, weight: .heavy)).foregroundStyle(on ? Theme.selectedText : Theme.text)
                .frame(width: 38, height: 38)
                .celBox(10, fill: on ? Theme.accentDeep : Theme.raise, shadow: 3)
        }
        .buttonStyle(.plain).focusable(false).help(help)
    }

    var body: some View {
        let only = store.rosterFilter
        // Scrolls, so a long list of projects never pushes the window's
        // status bar out of it.
        ScrollView(.vertical, showsIndicators: false) {
            VStack(spacing: 10) {
                tile("☰", on: false, help: "Show the tree (⌃⌘S)", size: 16) { store.sidebarVisible = true }
                tile("all", on: only == nil, help: "Every agent and terminal", size: 11) { store.rosterProject = nil }
                ForEach(store.railProjects, id: \.key) { p in
                    tile(railLetters(p.name), on: only == p.key, help: p.machine.isEmpty ? p.name : "\(p.name) on \(p.machine)") {
                        store.rosterProject = only == p.key ? nil : p.key
                    }
                }
            }
            .padding(.vertical, 12).padding(.trailing, 4).padding(.leading, 2)
        }
        .frame(width: 58)
        .background(Theme.side)
        .overlay(alignment: .trailing) { Rectangle().fill(Theme.celLine).frame(width: 2) }
    }
}

/// The right of a Cel window: the question the longest-waiting agent is
/// asking, as a dialogue box with its name tag, and what the agents last
/// did.
struct CelSide: View {
    @ObservedObject var store: Store

    var body: some View {
        VStack(spacing: 16) {
            if let n = store.needsYou.first {
                dialogue(n.key, n.pane)
            } else {
                Text("Nobody is waiting on you.").font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.muted)
                    .frame(maxWidth: .infinity).padding(.vertical, 14)
                    .celBox(12, fill: Theme.raise)
            }
            log
        }
        .padding(.top, 14).padding(.trailing, 14).padding(.bottom, 12)
        .frame(width: 310)
    }

    func dialogue(_ key: String, _ pane: PaneInfo) -> some View {
        let q = store.questions[key]
        return VStack(alignment: .leading, spacing: 7) {
            Text(q?.text.isEmpty == false ? q!.text : questionKind(pane.agent?.message) == "permission" ? "Allow this?" : "Waiting for you")
                .font(.system(size: 13, weight: .bold)).foregroundStyle(Theme.text).lineLimit(3)
            if let m = pane.agent?.message, !m.isEmpty {
                Text(m).font(.system(size: 11.5, design: .monospaced)).foregroundStyle(Theme.text2).lineLimit(2)
            }
            if let q, !q.choices.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(q.choices, id: \.choice) { c in
                        let on = c.default == true
                        Button { store.answer(key, q, c) } label: {
                            Text((on ? "▶ " : "") + c.label).font(.system(size: 12, weight: .bold)).lineLimit(1)
                                .foregroundStyle(on ? Theme.selectedText : Theme.text)
                                .padding(.horizontal, 10).padding(.vertical, 5).frame(maxWidth: .infinity, alignment: .leading)
                                .celBox(8, fill: on ? Theme.accentDeep : Theme.window, shadow: on ? 3 : 0)
                        }
                        .buttonStyle(.plain).help("Answer “\(c.label)” without opening the agent")
                    }
                }
            } else {
                Button("Open to answer") { store.open(key) }.buttonStyle(.plain)
                    .font(.system(size: 12, weight: .bold)).foregroundStyle(Theme.accentDeep)
            }
        }
        .padding(.horizontal, 14).padding(.top, 22).padding(.bottom, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .celBox(12, fill: Theme.raise, shadow: 5)
        // The name tag, sat on the box's edge.
        .overlay(alignment: .topLeading) {
            HStack(spacing: 6) {
                Mark(agent: pane.agent?.name)
                Text(pane.displayName).font(.system(size: 12, weight: .heavy)).foregroundStyle(Theme.text).lineLimit(1)
            }
            .padding(.horizontal, 9).padding(.vertical, 3)
            .celBox(7, fill: Theme.work.opacity(0.85), shadow: 2)
            .offset(x: 12, y: -12)
        }
        .padding(.top, 12)
        .contentShape(Rectangle())
        .onTapGesture { store.open(key) }
    }

    var log: some View {
        let lines = activity(store.rosterCards)
        return VStack(alignment: .leading, spacing: 9) {
            Text("WHAT HAPPENED").font(.system(size: 11, weight: .heavy)).tracking(0.8).foregroundStyle(Theme.muted)
            if lines.isEmpty {
                Text("Nothing yet.").font(.system(size: 12)).foregroundStyle(Theme.muted)
            }
            TimelineView(.periodic(from: .now, by: 30)) { ctx in
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(lines, id: \.key) { l in
                        HStack(alignment: .firstTextBaseline, spacing: 8) {
                            Text(shortAgo(l.since, ctx.date)).font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.muted)
                                .frame(width: 34, alignment: .leading)
                            Mark(agent: l.agent)
                            Text(l.text).font(.system(size: 12)).foregroundStyle(Theme.text).lineLimit(2)
                        }
                        .contentShape(Rectangle())
                        .onTapGesture { store.open(l.key) }
                    }
                }
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .celBox(12, fill: Theme.raise, shadow: 5)
    }
}
