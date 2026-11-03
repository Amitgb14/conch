import AppKit
import ConchKit
import SwiftUI

// A project's commit graph, in a tab of its own: every branch and tag of
// the repository, newest first, each commit a dot on its branch's lane
// with the lines git log --graph draws, its refs as chips, its subject,
// author and age. Opened from a project's or a branch's menu; a branch
// opens it at that branch's newest commit.

@MainActor
final class CommitsModel: ObservableObject {
    let key: String
    let machine: String
    let project: String
    unowned let store: Store
    @Published var rows: [GraphRow] = []
    @Published var more = false
    @Published var loading = false
    @Published var error = ""
    /// The commit picked, and a branch to bring into view once read.
    @Published var selected: String?
    @Published var focusBranch: String?
    /// Counts the times the picked commit is to be brought into view: a
    /// branch's tip already picked is shown again, which a change of the
    /// pick alone never said.
    @Published var reveal = 0
    /// The repository's remotes, as the server names them (nil: an older
    /// server, and origin and upstream are taken).
    @Published var remotes: Set<String>?
    private var limit = proto_LogLimit
    /// Reads in flight: only the newest one's reply is taken, or a read for
    /// 300 landing after one for 600 put the shorter history back.
    private var reads = Latest()

    init(key: String, store: Store) {
        self.key = key
        self.store = store
        (machine, project) = parseCommitsKey(key) ?? ("local", "")
        load()
    }

    var count: Int { rows.count }

    /// Lanes the graph column is wide, the same for every row so hashes
    /// and subjects line up; a history wider than 16 lanes is clipped
    /// rather than pushing the text off the row.
    var lanes: Int { min(max(rows.map(\.width).max() ?? 1, 1), 16) }

    func load() {
        guard let c = store.connection(machine) else {
            error = "\(store.label(machine)) is not connected"
            return
        }
        guard c.hello?.capabilities?.contains("project.log.v1") == true else {
            error = "conch on \(store.label(machine)) is older than this app and can't read a commit graph: reload its server."
            return
        }
        struct Params: Encodable { var project_id: String; var limit: Int }
        loading = true
        let n = reads.next()
        Task {
            do {
                let r = try await c.call("project.log", Params(project_id: project, limit: limit), as: LogResult.self)
                guard reads.isLatest(n) else { return }
                loading = false
                rows = commitGraph(r.commits ?? [])
                more = r.more ?? false
                remotes = r.remotes.map(Set.init)
                error = ""
                // The branch asked for, picked and brought into view if it is
                // among those read; either way, asked once.
                if let b = focusBranch {
                    focusBranch = nil
                    if let hit = rows.first(where: { CommitRef.parse($0.commit.refs, remotes: remotes ?? ["origin", "upstream"]).contains { $0.name == b } }) {
                        selected = hit.commit.hash
                        reveal += 1
                    }
                }
            } catch {
                guard reads.isLatest(n) else { return }
                loading = false
                self.error = "Could not read the history: \(error)"
            }
        }
    }

    /// Whether older commits can still be read: there are more, and fewer
    /// than the server's 2000 have been asked for.
    var canReadMore: Bool { more && limit < 2000 }

    /// Older commits, 300 at a time, up to the server's 2000.
    func loadMore() {
        guard canReadMore, !loading else { return }
        limit = min(limit + proto_LogLimit, 2000)
        load()
    }
}

/// project.log's own page size (proto.LogLimit).
let proto_LogLimit = 300

struct CommitsView: View {
    @ObservedObject var model: CommitsModel
    @ObservedObject var store: Store

    var body: some View {
        VStack(spacing: 0) {
            header
            Rectangle().fill(Theme.line).frame(height: 1)
            if !model.error.isEmpty {
                Text(model.error).foregroundStyle(Theme.err).padding(16).frame(maxWidth: .infinity, alignment: .leading)
            }
            if model.rows.isEmpty && !model.loading && model.error.isEmpty {
                VStack { Spacer(); Text("No commits yet.").foregroundStyle(Theme.muted); Spacer() }.frame(maxWidth: .infinity)
            } else {
                ScrollViewReader { reader in
                    ScrollView {
                        LazyVStack(spacing: 0) {
                            ForEach(model.rows, id: \.commit.hash) { r in
                                CommitRow(model: model, store: store, row: r).id(r.commit.hash)
                            }
                            if model.canReadMore {
                                Button("Show older commits") { model.loadMore() }
                                    .buttonStyle(.link).padding(12).disabled(model.loading)
                            } else if model.more {
                                Text("The newest \(model.count), as many as are read").font(.system(size: 12))
                                    .foregroundStyle(Theme.muted).padding(12)
                            }
                        }
                        .padding(.vertical, 6)
                    }
                    .onChange(of: model.reveal) { _, _ in
                        if let h = model.selected { withAnimation { reader.scrollTo(h, anchor: .center) } }
                    }
                }
            }
        }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: 9))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(Theme.line))
    }

    var header: some View {
        let name = store.snapshots[model.machine]?.project(model.project)?.name ?? model.project
        return HStack(spacing: 10) {
            LucideIcon("git-commit-horizontal", color: Theme.accent, size: 14)
            Text("Commits").font(.system(size: 13, weight: .semibold))
            Text(name).font(.system(size: 12)).foregroundStyle(Theme.text2)
            Text(model.more ? "newest \(model.count)" : "\(model.count)").font(.system(size: 11)).foregroundStyle(Theme.muted)
            if model.loading { ProgressView().controlSize(.small) }
            Spacer()
            Button { model.load() } label: { LucideIcon("refresh-cw", size: 12) }.buttonStyle(.plain).help("Read the history again")
        }
        .padding(.horizontal, 12).frame(height: 40)
        .background(Theme.paneHeader)
    }
}

/// The colours lanes take in turn: the theme's accent for the first (the
/// newest branch, usually the one being worked on), then its other
/// signal colours, so neighbouring branches differ.
@MainActor
func laneColor(_ i: Int) -> Color {
    let palette = lanePalette(Theme.current)
    return Theme.color(palette[((i % palette.count) + palette.count) % palette.count])
}

struct CommitRow: View {
    @ObservedObject var model: CommitsModel
    @ObservedObject var store: Store
    let row: GraphRow
    @State private var hovering = false

    static let height: CGFloat = 26
    static let laneWidth: CGFloat = 14

    var body: some View {
        let c = row.commit
        let refs = CommitRef.parse(c.refs, remotes: model.remotes ?? ["origin", "upstream"])
        let on = model.selected == c.hash
        HStack(spacing: 8) {
            GraphCell(row: row).frame(width: CGFloat(model.lanes) * Self.laneWidth + 6, height: Self.height).clipped()
            Text(c.short).font(.system(size: 11.5, design: .monospaced)).foregroundStyle(Theme.muted)
            ForEach(Array(refs.enumerated()), id: \.offset) { _, r in RefChip(ref: r) }
            Text(c.subject).font(.system(size: 12.5)).foregroundStyle(Theme.text).lineLimit(1).truncationMode(.tail)
                .layoutPriority(-1)
            Spacer(minLength: 8)
            Text(c.author ?? "").font(.system(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1)
            Text(agoText(c.time)).font(.system(size: 11.5)).foregroundStyle(Theme.muted).frame(minWidth: 52, alignment: .trailing)
        }
        .padding(.horizontal, 10).frame(height: Self.height)
        .background(on ? Theme.accentDeep.opacity(0.3) : hovering ? Theme.raise : .clear)
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
        .onTapGesture { model.selected = c.hash }
        .help(c.hash)
        .contextMenu {
            Button("Copy hash") { copy(c.hash) }
            Button("Copy subject") { copy(c.subject) }
            let branches = refs.filter { $0.kind == .head || $0.kind == .branch }.map(\.name).filter { $0 != "HEAD" }
            if !branches.isEmpty { Divider() }
            ForEach(branches, id: \.self) { b in
                Button("View changes on \(b)") { store.openChanges(project: key(model.machine, model.project), branch: b) }
            }
        }
    }

    func copy(_ s: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
    }
}

/// A ref as a chip: HEAD's branch in the accent, a branch in the working
/// colour, a remote quieter, a tag outlined.
struct RefChip: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let ref: CommitRef

    var body: some View {
        let _ = look
        let color: Color = switch ref.kind {
        case .head: Theme.accent
        case .branch: Theme.work
        case .remote: Theme.muted
        case .tag: Theme.warn
        }
        HStack(spacing: 3) {
            if ref.kind == .tag { LucideIcon("tag", color: color, size: 9) }
            Text(ref.kind == .head && ref.name != "HEAD" ? "HEAD → \(ref.name)" : ref.name)
        }
        .font(.system(size: 10.5, weight: .semibold)).foregroundStyle(color).lineLimit(1)
        .padding(.horizontal, 6).padding(.vertical, 1.5)
        .background(RoundedRectangle(cornerRadius: 4).fill(ref.kind == .tag ? .clear : color.opacity(0.16)))
        .overlay(RoundedRectangle(cornerRadius: 4).stroke(ref.kind == .tag ? color.opacity(0.6) : .clear))
        .fixedSize()
    }
}

/// One row's share of the graph: the lines through it and the commit's dot.
struct GraphCell: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let row: GraphRow

    var body: some View {
        let _ = look
        // Colours are read here, on the main actor; the canvas only draws.
        let lanes = Set(row.colors.keys).union([row.lane])
        let color = Dictionary(uniqueKeysWithValues: lanes.map { ($0, laneColor(row.colors[$0] ?? $0)) })
        let background = Theme.window
        let head = CommitRef.parse(row.commit.refs).contains { $0.kind == .head }
        return Canvas { ctx, size in
            let h = size.height, mid = h / 2, w = CommitRow.laneWidth
            func x(_ i: Int) -> CGFloat { 3 + w / 2 + CGFloat(i) * w }
            func stroke(_ p: Path, _ lane: Int) {
                ctx.stroke(p, with: .color(color[lane] ?? .gray), style: StrokeStyle(lineWidth: 1.6, lineCap: .round))
            }
            for i in row.through {
                stroke(Path { $0.move(to: CGPoint(x: x(i), y: 0)); $0.addLine(to: CGPoint(x: x(i), y: h)) }, i)
            }
            // Children arrive from the top; a lane bending in curves to
            // the dot, as git's / and \ do.
            for i in row.into {
                stroke(Path { p in
                    p.move(to: CGPoint(x: x(i), y: 0))
                    if i == row.lane {
                        p.addLine(to: CGPoint(x: x(i), y: mid))
                    } else {
                        p.addCurve(to: CGPoint(x: x(row.lane), y: mid), control1: CGPoint(x: x(i), y: mid * 0.7),
                                   control2: CGPoint(x: x(row.lane), y: mid * 0.3))
                    }
                }, i)
            }
            for k in row.outOf {
                stroke(Path { p in
                    p.move(to: CGPoint(x: x(row.lane), y: mid))
                    if k == row.lane {
                        p.addLine(to: CGPoint(x: x(k), y: h))
                    } else {
                        p.addCurve(to: CGPoint(x: x(k), y: h), control1: CGPoint(x: x(row.lane), y: mid + mid * 0.7),
                                   control2: CGPoint(x: x(k), y: mid + mid * 0.3))
                    }
                }, k)
            }
            let dotColor = color[row.lane] ?? .gray
            let r: CGFloat = head ? 4.5 : row.merge ? 3 : 3.8
            let dot = Path(ellipseIn: CGRect(x: x(row.lane) - r, y: mid - r, width: 2 * r, height: 2 * r))
            if head || row.merge {
                ctx.fill(dot, with: .color(dotColor))
            } else {
                ctx.fill(dot, with: .color(background))
                ctx.stroke(dot, with: .color(dotColor), lineWidth: 1.6)
            }
        }
    }
}
