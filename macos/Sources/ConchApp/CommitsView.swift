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
    /// The graph's search: commits that do not match are dimmed, never
    /// taken out, so the lanes stay whole.
    @Published var query = ""
    /// A second commit picked with shift: the range compared runs between
    /// it and the one picked.
    @Published var compareWith: String?
    /// What the side panel shows: the commit picked, or the range.
    @Published var detail: CommitDetail?
    @Published var detailError = ""
    /// One file's diff opened from the side panel.
    @Published var peekFile: String?
    @Published var peek: [DiffLine] = []
    private var details = Latest()
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

    /// Picks a commit, or with shift a second one to compare with it.
    func pick(_ hash: String, shift: Bool) {
        if shift, let s = selected, s != hash {
            compareWith = hash
        } else {
            selected = hash
            compareWith = nil
        }
        loadDetail()
    }

    /// The range being compared, the older commit first, by the graph's order.
    var range: (from: String, to: String)? {
        guard let a = selected, let b = compareWith else { return nil }
        return compareRange(a, b, order: rows.map(\.commit.hash))
    }

    /// Hashes inside the compared range, for dimming the rest.
    var inRange: Set<String>? {
        guard let r = range, let lo = rows.firstIndex(where: { $0.commit.hash == r.to }),
              let hi = rows.firstIndex(where: { $0.commit.hash == r.from }) else { return nil }
        return Set(rows[lo...hi].map(\.commit.hash))
    }

    var canShowDetail: Bool { store.connection(machine)?.hello?.capabilities?.contains("project.commit.v1") == true }

    func loadDetail() {
        peekFile = nil
        peek = []
        guard let c = store.connection(machine), let sel = selected else { detail = nil; return }
        guard canShowDetail else {
            detail = nil
            detailError = "conch on \(store.label(machine)) is too old to open a commit: reload its server"
            return
        }
        let params = range.map { CommitParams(projectID: project, hash: $0.to, from: $0.from) } ?? CommitParams(projectID: project, hash: sel)
        let n = details.next()
        Task {
            do {
                let d = try await c.call("project.commit", params, as: CommitDetail.self)
                guard details.isLatest(n) else { return }
                detail = d
                detailError = ""
            } catch {
                guard details.isLatest(n) else { return }
                detail = nil
                detailError = "\(error)"
            }
        }
    }

    /// One file's diff in the commit, or across the range.
    func openPeek(_ file: String) {
        guard let c = store.connection(machine), let sel = selected else { return }
        if peekFile == file { peekFile = nil; peek = []; return }
        peekFile = file
        let params = range.map { CommitParams(projectID: project, hash: $0.to, from: $0.from, file: file) } ?? CommitParams(projectID: project, hash: sel, file: file)
        Task {
            if let d = try? await c.call("project.commit", params, as: CommitDetail.self), peekFile == file {
                peek = parseDiff(d.diff ?? "").filter { $0.kind != .meta }
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
    /// Inside a branch's git tab: no frame of its own, which the tab has.
    var embedded = false

    var body: some View {
        HStack(spacing: 0) {
            graph
            if model.selected != nil {
                Rectangle().fill(Theme.line).frame(width: 1)
                CommitDetailPanel(model: model, store: store).frame(width: 340)
            }
        }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: embedded ? 0 : 9))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(embedded ? .clear : Theme.line))
    }

    var graph: some View {
        VStack(spacing: 0) {
            header
            Rectangle().fill(Theme.line).frame(height: 1)
            if model.range != nil {
                HStack(spacing: 8) {
                    Circle().fill(Theme.merged).frame(width: 7, height: 7)
                    Text("Comparing \(String(model.range!.from.prefix(7))) … \(String(model.range!.to.prefix(7)))").font(.system(size: 12))
                    Spacer()
                    Button("Done") { model.compareWith = nil; model.loadDetail() }.controlSize(.small)
                }
                .padding(.horizontal, 12).frame(height: 30).background(Theme.merged.opacity(0.14))
            }
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
            HStack(spacing: 6) {
                LucideIcon("search", color: Theme.muted, size: 11)
                TextField("Search commits, hashes, authors", text: $model.query).textFieldStyle(.plain).font(.system(size: 12)).frame(width: 220)
            }
            .padding(.horizontal, 8).frame(height: 24)
            .background(RoundedRectangle(cornerRadius: 6).fill(Theme.window))
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line))
            Text("shift-click to compare").font(.system(size: 11)).foregroundStyle(Theme.muted)
            Button { model.load() } label: { LucideIcon("refresh-cw", size: 12).padding(4).contentShape(Rectangle()) }.buttonStyle(.plain).help("Read the history again")
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
        let cmp = model.compareWith == c.hash
        let dim = !commitMatches(c, model.query) || (model.inRange.map { !$0.contains(c.hash) } ?? false)
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
        .opacity(dim ? 0.3 : 1)
        .background(cmp ? Theme.merged.opacity(0.22) : on ? Theme.accentDeep.opacity(0.3) : hovering ? Theme.raise : .clear)
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
        .onTapGesture { model.pick(c.hash, shift: NSEvent.modifierFlags.contains(.shift)) }
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

/// The side of the graph: the commit picked — who, when, its message whole,
/// the files it changed with a click to see one's diff — or, with a second
/// commit shift-clicked, every file changed across the range.
struct CommitDetailPanel: View {
    @ObservedObject var model: CommitsModel
    @ObservedObject var store: Store

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 10) {
                if !model.detailError.isEmpty {
                    Text(model.detailError).font(.system(size: 12)).foregroundStyle(Theme.err)
                }
                if let d = model.detail {
                    if let r = model.range {
                        Text("Compare").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.merged)
                        Text("\(d.commits ?? 0) commit\((d.commits ?? 0) == 1 ? "" : "s"), \(d.files?.count ?? 0) file\((d.files?.count ?? 0) == 1 ? "" : "s")")
                            .font(.system(size: 14, weight: .semibold))
                        Text("\(String(r.from.prefix(7))) → \(String(r.to.prefix(7)))").font(.system(size: 11.5, design: .monospaced)).foregroundStyle(Theme.muted)
                    } else {
                        HStack {
                            Text(String(d.hash.prefix(10))).font(.system(size: 12, weight: .semibold, design: .monospaced)).foregroundStyle(Theme.work)
                            if (d.parents?.count ?? 0) > 1 { Text("merge").font(.system(size: 10.5)).foregroundStyle(Theme.muted) }
                            Spacer()
                            Button("Copy") {
                                NSPasteboard.general.clearContents()
                                NSPasteboard.general.setString(d.hash, forType: .string)
                            }.controlSize(.small)
                        }
                        Text(d.subject ?? "").font(.system(size: 14, weight: .semibold)).fixedSize(horizontal: false, vertical: true)
                        HStack(spacing: 6) {
                            Text(d.author ?? "").font(.system(size: 12)).foregroundStyle(Theme.text2)
                            if let t = d.time { Text("· \(agoText(t))").font(.system(size: 12)).foregroundStyle(Theme.muted) }
                        }
                        if let b = d.body, !b.isEmpty {
                            Text(b).font(.system(size: 12)).foregroundStyle(Theme.text2).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                        }
                        if let ps = d.parents, !ps.isEmpty {
                            HStack(spacing: 6) {
                                Text("Parents").font(.system(size: 11.5)).foregroundStyle(Theme.muted)
                                ForEach(ps, id: \.self) { p in
                                    Button(String(p.prefix(7))) { model.pick(p, shift: false); model.reveal += 1 }
                                        .buttonStyle(.link).font(.system(size: 11.5, design: .monospaced))
                                }
                            }
                        }
                    }
                    Rectangle().fill(Theme.line).frame(height: 1)
                    Text("FILES · \(d.files?.count ?? 0)").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
                    ForEach(d.files ?? []) { f in
                        VStack(alignment: .leading, spacing: 0) {
                            HStack(spacing: 8) {
                                Text(f.code).font(.system(size: 11, weight: .bold, design: .monospaced)).foregroundStyle(fileCodeColor(f.code)).frame(width: 12)
                                Text(f.path).font(.system(size: 12, design: .monospaced)).lineLimit(1).truncationMode(.head)
                                Spacer(minLength: 4)
                                Text("+\(f.added)").font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.ok)
                                Text("−\(f.deleted)").font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.err)
                            }
                            .padding(.vertical, 3).contentShape(Rectangle())
                            .onTapGesture { model.openPeek(f.path) }
                            .accessibilityElement(children: .combine)
                            .accessibilityAddTraits(.isButton)
                            .accessibilityAction { model.openPeek(f.path) }
                            if model.peekFile == f.path {
                                VStack(alignment: .leading, spacing: 0) {
                                    ForEach(model.peek.prefix(120)) { l in
                                        Text((l.kind == .added ? "+ " : l.kind == .removed ? "− " : l.kind == .hunk ? "" : "  ") + l.text)
                                            .font(.system(size: 11, design: .monospaced)).lineLimit(1)
                                            .foregroundStyle(l.kind == .added ? Theme.ok : l.kind == .removed ? Theme.err : l.kind == .hunk ? Theme.work : Theme.text2)
                                            .frame(maxWidth: .infinity, alignment: .leading)
                                            .background(l.kind == .added ? Theme.ok.opacity(0.1) : l.kind == .removed ? Theme.err.opacity(0.1) : .clear)
                                    }
                                }
                                .padding(6).background(RoundedRectangle(cornerRadius: 6).fill(Theme.side))
                            }
                        }
                    }
                } else if model.detailError.isEmpty {
                    ProgressView().controlSize(.small)
                }
            }
            .padding(14)
        }
        .background(Theme.side)
    }
}

/// The colour of a file's status letter.
@MainActor
func fileCodeColor(_ code: String) -> Color {
    switch code {
    case "A", "?": return Theme.ok
    case "D": return Theme.err
    case "U": return Theme.warn
    case "R", "C": return Theme.merged
    default: return Theme.warn
    }
}
