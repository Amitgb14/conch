import AppKit
import ConchKit
import SwiftUI

// A project's files in a tab of their own, as the TUI's Files row opens
// them: a checkout as a tree, folders read as they are opened, beside the
// files open — each in a tab of the editor, saved with ⌘S (fs.write.v1), or
// read only where the server can't save. Any branch checked out in a
// worktree can be shown, the main checkout first. Beside the tree, a search
// of the whole checkout (FilesSearchView.swift); a file too big to hold
// whole is read a window of lines at a time (BigFileView.swift); and an
// agent can be asked to edit the file, its change shown to keep or undo
// (AgentEditView.swift).

/// What the side of the Files tab shows.
enum FilesSide { case tree, search }

/// An open file's state while another is shown.
struct FileBuffer {
    var preview: FSRead?
    var previewError = ""
    var text = ""
    var original = ""
    var baseHash: String?
    var diskChanged = false
    var dirty: Bool { preview?.editable == true && text != original }
}

@MainActor
final class FilesModel: ObservableObject {
    let key: String
    let machine: String
    let project: String
    unowned let store: Store
    /// Each folder read, by its path in the checkout ("" the top).
    @Published var dirs: [String: [FSEntry]] = [:]
    @Published var open: Set<String> = []
    @Published var selected: String?
    @Published var preview: FSRead?
    @Published var previewError = ""
    @Published var error = ""
    @Published var hidden = false
    /// The checkout shown: a worktree's path, or nil for the main checkout.
    @Published var checkoutPath: String?
    /// The editor's text, the file as read (or last saved), and the
    /// fingerprint a save is checked against.
    @Published var text = ""
    @Published var original = ""
    @Published var baseHash: String?
    @Published var saving = false
    /// The file changed on disk while there were edits not saved.
    @Published var diskChanged = false
    /// Reads under way, for ↻'s spin; and until when it keeps spinning, so
    /// a read that comes back at once is still seen to have happened.
    @Published private(set) var loading = 0
    @Published private(set) var spinUntil = Date.distantPast
    private var reads = Latest()
    /// The picked file as a file of this computer's, for a picture, a
    /// video, a PDF or a page to be shown from: the file itself here, a copy
    /// fetched into the cache from another machine.
    @Published var media: URL?
    @Published var mediaNote = ""
    /// How much of a remote file has come, while it is fetched.
    @Published var fetching: Double?
    /// A page shown as its source instead, to edit it.
    @Published var showSource = false
    /// Bumped when the file shown changes on disk, so its view opens it again.
    @Published var mediaVersion = 0
    private var fetches = Latest()
    /// What media was fetched for: the same file at the same size is not
    /// fetched again each time an agent writes something else.
    private var mediaFor: (root: String, path: String, size: Int64?)?
    /// The files open, in tabs, and the state of each not shown.
    @Published var tabs = OpenFiles()
    private var buffers: [String: FileBuffer] = [:]
    @Published var side = FilesSide.tree
    @Published var quickOpen = false
    /// Bumped to ask the editor for its find bar (and replace).
    @Published var findRequest = 0
    @Published var replaceRequest = false
    /// The editor's selection, for an agent asked about it.
    @Published var selection = NSRange(location: 0, length: 0)
    /// A line to put in view and select in the editor, and a counter so the
    /// same line can be asked for twice.
    @Published var reveal: (line: Int, token: Int)?
    /// The file shown when it is too big to hold whole.
    @Published var big: BigFileModel?
    @Published var agentPanel = false
    /// What agents were asked to do to files here, newest first.
    @Published var agentEdits: [AgentEdit] = []
    private(set) lazy var search = FilesSearchModel(files: self)

    init(key: String, store: Store) {
        self.key = key
        self.store = store
        (machine, project) = parseFilesKey(key) ?? ("local", "")
        load()
    }

    var rows: [FileRow] { fileRows(dirs, open: open) }
    var info: ProjectInfo? { store.snapshots[machine]?.project(project) }
    var root: String { checkoutPath ?? info?.path ?? "" }
    var choices: [Checkout] { info.map(checkouts) ?? [] }
    var shown: Checkout? { choices.first { $0.path == root } ?? choices.first }
    var dirty: Bool { preview?.editable == true && text != original }
    var canSave: Bool { has("fs.write.v1") && preview?.editable == true }
    /// Whether this machine's server has a capability.
    func has(_ cap: String) -> Bool { store.connection(machine)?.hello?.capabilities?.contains(cap) == true }
    /// Whether a file open in a tab has edits not saved.
    func isDirty(_ path: String) -> Bool { path == selected ? dirty : buffers[path]?.dirty == true }
    var dirtyCount: Int { tabs.paths.filter(isDirty).count }
    var spinning: Bool { loading > 0 || spinUntil > Date() }

    private func busy(_ on: Bool) {
        loading = max(0, loading + (on ? 1 : -1))
        if on {
            spinUntil = Date().addingTimeInterval(0.6)
            // Stop the spin once the moment is over, even with nothing else changing.
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.65) { [weak self] in self?.objectWillChange.send() }
        }
    }

    /// The top again, and every folder open, and the file picked: what a
    /// change on disk, ↻ or the hidden-files switch asks for.
    func load() {
        guard let c = store.connection(machine) else {
            error = "\(store.label(machine)) is not connected"
            return
        }
        guard let caps = c.hello?.capabilities, caps.contains("fs.files.v1"), caps.contains("fs.read.v1") else {
            error = "conch on \(store.label(machine)) is older than this app and can't list a project's files: reload its server."
            return
        }
        error = ""
        list("")
        for d in open { list(d) }
        if let s = selected { reread(s) }
    }

    func list(_ dir: String) {
        guard let c = store.connection(machine), !root.isEmpty else { return }
        struct Params: Encodable { var root: String; var path: String; var files: Bool; var hidden: Bool }
        let p = Params(root: root, path: dir, files: true, hidden: hidden)
        busy(true)
        Task {
            defer { busy(false) }
            do {
                let l = try await c.call("fs.list", p, as: FSList.self)
                guard p.root == root else { return } // another checkout was picked meanwhile
                dirs[dir] = l.entries ?? []
            } catch {
                guard p.root == root else { return }
                if dir.isEmpty { self.error = "Could not list the files: \(error)" }
                // A folder that went (deleted while open) closes.
                dirs[dir] = nil
                open.remove(dir)
            }
        }
    }

    func toggle(_ r: FileRow) {
        if r.dir {
            if open.contains(r.path) {
                open.remove(r.path)
            } else {
                open.insert(r.path)
                list(r.path)
            }
        } else {
            openFile(r.path)
        }
    }

    /// A file shown in a tab: its own when it is open already (with its
    /// edits), read afresh when not.
    func openFile(_ path: String, line: Int? = nil) {
        if path != selected {
            // A line asked for in the last file is not this one's.
            if line == nil { reveal = nil }
            stash()
            tabs.open(path)
            selected = path
            if let b = buffers.removeValue(forKey: path), b.dirty {
                restore(b)
                reread(path)
            } else {
                clearShown()
                read(path)
            }
        }
        if let line { reveal = (line, (reveal?.token ?? 0) + 1) }
    }

    /// Closes a file's tab, asking first about edits not saved.
    func closeTab(_ path: String) {
        if isDirty(path) {
            let a = NSAlert()
            a.messageText = "Close \((path as NSString).lastPathComponent) without saving?"
            a.informativeText = "Its edits are lost."
            a.addButton(withTitle: "Close")
            a.addButton(withTitle: "Cancel")
            guard a.runModal() == .alertFirstButtonReturn else { return }
        }
        buffers[path] = nil
        tabs.close(path)
        guard path == selected else { return }
        selected = nil
        clearShown()
        if let next = tabs.active {
            tabs.active = nil
            openFile(next)
        }
    }

    /// The file shown, put aside while another is.
    private func stash() {
        guard let s = selected else { return }
        buffers[s] = FileBuffer(preview: preview, previewError: previewError, text: text, original: original,
                                baseHash: baseHash, diskChanged: diskChanged)
    }

    private func restore(_ b: FileBuffer) {
        preview = b.preview
        previewError = b.previewError
        text = b.text
        original = b.original
        baseHash = b.baseHash
        diskChanged = b.diskChanged
    }

    private func clearShown() {
        preview = nil
        previewError = ""
        text = ""
        original = ""
        baseHash = nil
        diskChanged = false
        big = nil
        showSource = false
    }

    /// Whether edits not saved may be left: asked, when there are some.
    func confirmLeaving(_ doing: String) -> Bool {
        let others = tabs.paths.filter { $0 != selected && isDirty($0) }
        if !others.isEmpty {
            let a = NSAlert()
            a.messageText = "\(dirtyCount) open file\(dirtyCount == 1 ? " has" : "s have") edits not saved"
            a.informativeText = "They are lost if you go on \(doing)."
            a.addButton(withTitle: "Discard Them")
            a.addButton(withTitle: "Cancel")
            return a.runModal() == .alertFirstButtonReturn
        }
        guard dirty, let path = selected else { return true }
        let a = NSAlert()
        a.messageText = "Save your changes to \((path as NSString).lastPathComponent)?"
        a.informativeText = "They are lost if you go on \(doing) without saving."
        a.addButton(withTitle: "Save")
        a.addButton(withTitle: "Don't Save")
        a.addButton(withTitle: "Cancel")
        switch a.runModal() {
        case .alertFirstButtonReturn:
            save()
            return true
        case .alertSecondButtonReturn:
            text = original
            return true
        default:
            return false
        }
    }

    /// A file's size as its folder's listing gave it.
    func knownSize(_ path: String) -> Int64? {
        let dir = (path as NSString).deletingLastPathComponent
        let name = (path as NSString).lastPathComponent
        return dirs[dir]?.first { $0.name == name }?.size
    }

    /// The file picked, read into the editor (and what it was saved against).
    func read(_ path: String) {
        guard let c = store.connection(machine), !root.isEmpty else { return }
        // A file that can be saved is read whole where the server can
        // (fs.lines.v1); a bigger one is then read by its lines.
        struct Params: Encodable { var root: String; var path: String; var whole: Bool? }
        let lines = has("fs.lines.v1")
        // A file the tree already knows to be too big to edit whole is not
        // read whole first: 4 MB through the socket only to be put aside
        // held up opening a big log by seconds.
        let whole = lines && (knownSize(path) ?? 0) <= FileTier.wholeMax
        let n = reads.next()
        busy(true)
        Task {
            defer { busy(false) }
            do {
                let r = try await c.call("fs.read", Params(root: root, path: path, whole: whole ? true : nil), as: FSRead.self)
                guard reads.isLatest(n) else { return } // another file was picked meanwhile
                preview = r
                previewError = ""
                text = r.data ?? ""
                original = text
                baseHash = r.hash
                diskChanged = false
                if lines, r.truncated == true, r.binary != true, MediaKind(path: path) == .text {
                    if big?.path != path { big = BigFileModel(files: self, path: path, size: r.size ?? 0) } else { big?.reload() }
                } else {
                    big = nil
                }
                agentChanged(path)
                fetchMedia(path, size: r.size)
            } catch {
                guard reads.isLatest(n) else { return }
                preview = nil
                previewError = "\(error)"
            }
        }
    }

    /// The picked file where a picture, player or page can open it.
    func fetchMedia(_ path: String, size: Int64?) {
        if let m = mediaFor, m.root == root, m.path == path, m.size == size, media != nil || fetching != nil { return }
        if let m = mediaFor, m.root == root, m.path == path { mediaVersion += 1 } // the same file, changed
        mediaFor = (root, path, size)
        let n = fetches.next()
        media = nil
        mediaNote = ""
        fetching = nil
        guard MediaKind(path: path).needsFile else { return }
        let full = (root as NSString).appendingPathComponent(path)
        if machine == "local" {
            media = URL(fileURLWithPath: full)
            return
        }
        guard let c = store.connection(machine), c.hello?.capabilities?.contains("fs.transfer.v1") == true else {
            mediaNote = "conch on \(store.label(machine)) can't send files yet — reload its server to see this here."
            return
        }
        if let size, size > mediaFetchMax {
            mediaNote = "\(fileSize(size)) is too big to fetch for a look — use conch download."
            return
        }
        let dest = mediaCache(machine: machine, path: full)
        fetching = 0
        Task {
            do {
                try await fetchFile(c, full, to: dest) { done, total in
                    if self.fetches.isLatest(n), total > 0 { self.fetching = Double(done) / Double(total) }
                    return self.fetches.isLatest(n)
                }
                guard fetches.isLatest(n) else { return }
                fetching = nil
                media = dest
            } catch {
                guard fetches.isLatest(n) else { return }
                fetching = nil
                mediaNote = "Could not fetch it: \(error)"
            }
        }
    }

    /// The file picked, again: as it is on disk when there are no edits;
    /// with edits, only said to have changed, so a keystroke is never lost
    /// to an agent writing the same file.
    func reread(_ path: String) {
        guard dirty else { return read(path) }
        guard let c = store.connection(machine), !root.isEmpty else { return }
        struct Params: Encodable { var root: String; var path: String }
        let at = root
        Task {
            if let r = try? await c.call("fs.read", Params(root: at, path: path), as: FSRead.self), selected == path, root == at {
                diskChanged = r.hash != baseHash
            }
        }
    }

    /// ⌘S: the edits written, if the file is still what they began from;
    /// otherwise asked whether to take what is on disk or keep these.
    func save(force: Bool = false) {
        guard let c = store.connection(machine), let path = selected, canSave, dirty || force, !saving else { return }
        struct Params: Encodable { var root: String; var path: String; var data: String; var base: String?; var force: Bool? }
        let data = text
        saving = true
        Task {
            defer { saving = false }
            do {
                let r = try await c.call("fs.write", Params(root: root, path: path, data: data, base: baseHash, force: force ? true : nil), as: FSWriteResult.self)
                original = data
                baseHash = r.hash
                if mediaHasSource(path) {
                    // The page shown again as saved: a copy fetched afresh from another machine.
                    if machine == "local" { mediaVersion += 1 } else { mediaFor = nil; fetchMedia(path, size: Int64(data.utf8.count)) }
                }
                diskChanged = false
                store.say("Saved \((path as NSString).lastPathComponent)")
            } catch let e as ConchError where e.code == "file_changed" {
                conflict(path)
            } catch {
                store.say("Could not save \((path as NSString).lastPathComponent): \(error)", .err)
            }
        }
    }

    /// The file changed on disk since it was opened — an agent wrote it.
    func conflict(_ path: String) {
        let a = NSAlert()
        a.messageText = "\((path as NSString).lastPathComponent) changed on disk since you opened it"
        a.informativeText = "Something else — an agent, most likely — wrote it. Reload it to see their version (your edits are dropped), or overwrite it with yours (theirs is lost)."
        a.addButton(withTitle: "Reload Theirs")
        a.addButton(withTitle: "Overwrite with Mine")
        a.addButton(withTitle: "Cancel")
        switch a.runModal() {
        case .alertFirstButtonReturn:
            text = original
            read(path)
        case .alertSecondButtonReturn:
            save(force: true)
        default:
            diskChanged = true
        }
    }

    /// Another checkout of the project: a branch's worktree, or the main one.
    func choose(_ co: Checkout) {
        guard let path = co.path, path != root, confirmLeaving("to \(co.branch)") else { return }
        checkoutPath = co.main ? nil : path
        showSource = false
        dirs = [:]
        open = []
        selected = nil
        tabs = OpenFiles()
        buffers = [:]
        big = nil
        search.reset()
        preview = nil
        previewError = ""
        text = ""
        original = ""
        baseHash = nil
        diskChanged = false
        load()
    }

    /// Files in this checkout were written: read again what is on screen.
    func written(worktree: String) {
        guard worktree == root else { return }
        load()
        big?.written()
    }
}

struct FilesView: View {
    @ObservedObject var model: FilesModel
    @ObservedObject var store: Store

    var body: some View {
        VStack(spacing: 0) {
            header
            Rectangle().fill(Theme.line).frame(height: 1)
            if !model.error.isEmpty {
                Text(model.error).foregroundStyle(Theme.err).padding(16).frame(maxWidth: .infinity, alignment: .leading)
                Spacer()
            } else {
                GeometryReader { g in
                HStack(spacing: 0) {
                    Group {
                        switch model.side {
                        case .tree: treeSide
                        case .search: FilesSearchPanel(model: model.search)
                        }
                    }
                    // Its usual width, or less in a narrow card (a
                    // workbench's), so the file beside it keeps room.
                    .frame(width: filesSideWidth(search: model.side == .search, card: g.size.width))
                    Rectangle().fill(Theme.line).frame(width: 1)
                    VStack(spacing: 0) {
                        if !model.tabs.paths.isEmpty {
                            FileTabsBar(model: model)
                            Rectangle().fill(Theme.line).frame(height: 1)
                        }
                        FilePreview(model: model, store: store)
                    }
                    if model.agentPanel {
                        Rectangle().fill(Theme.line).frame(width: 1)
                        AgentEditPanel(model: model, store: store).frame(width: 340)
                    }
                }
                }
            }
        }
        .sheet(isPresented: $model.quickOpen) { QuickOpenSheet(model: model) }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: CGFloat(Theme.style.corner)))
        .overlay(RoundedRectangle(cornerRadius: CGFloat(Theme.style.corner)).stroke(Theme.line))
    }

    var header: some View {
        let name = model.info?.name ?? model.project
        return HStack(spacing: 10) {
            LucideIcon("folder-open", color: Theme.accent, size: 14)
            Text("Files").font(.ui(size: 13, weight: .semibold))
            Text(name).font(.ui(size: 12)).foregroundStyle(Theme.text2)
            branchMenu
            Text(model.root).font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.head)
            Spacer(minLength: 4)
            // In a narrow card (a workbench's) the buttons lose their words
            // before they would run off its edge.
            ViewThatFits(in: .horizontal) {
                headerButtons(compact: false)
                headerButtons(compact: true)
            }
            // ↻ turns while files are read, and a moment after.
            Button { model.load() } label: {
                TimelineView(.animation(paused: !model.spinning)) { ctx in
                    LucideIcon("refresh-cw", color: model.spinning ? Theme.accent : Theme.text2, size: 12)
                        .rotationEffect(.degrees(model.spinning ? ctx.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 0.9) / 0.9 * 360 : 0))
                }
                .padding(4).contentShape(Rectangle())
            }
            .buttonStyle(.plain).help("Read the files again")
        }
        .padding(.horizontal, 12).frame(height: 40)
        .background(Theme.paneHeader)
    }

    /// The checkout shown, and the others to switch to: each branch with a
    /// worktree; one without is listed, greyed, to say why it can't be.
    var branchMenu: some View {
        Menu {
            ForEach(model.choices) { co in
                Button(co.path == model.root ? "✓ " + co.label : co.path == nil ? "\(co.branch) — no worktree" : "   " + co.label) {
                    model.choose(co)
                }
                .disabled(co.path == nil)
            }
        } label: {
            HStack(spacing: 5) {
                LucideIcon(name: .gitBranch, color: Theme.text2, size: 11)
                Text(model.shown?.branch ?? "branch").font(.ui(size: 12, design: .monospaced)).lineLimit(1)
            }
        }
        .menuStyle(.borderlessButton).fixedSize()
        .help("Show another branch's files: any branch checked out in a worktree")
    }

    func headerButtons(compact: Bool) -> some View {
        HStack(spacing: 10) {
            headerButton("Go to File", icon: "doc", key: "⌘P", compact: compact) { model.quickOpen = true }
                .help("Open a file by a few letters of its name (⌘P)")
            headerButton(model.side == .search ? "Show Tree" : "Search", icon: model.side == .search ? "list.bullet.indent" : "magnifyingglass", key: "⌘⇧F", compact: compact) {
                model.side = model.side == .search ? .tree : .search
                if model.side == .search { model.search.focus &+= 1 }
            }
            .help("Search the whole project for file names and text, and replace (⌘⇧F)")
            Toggle(compact ? "Hidden" : "Hidden files", isOn: Binding(get: { model.hidden }, set: { model.hidden = $0; model.load() }))
                .toggleStyle(.checkbox).font(.ui(size: 12)).foregroundStyle(Theme.text2).fixedSize()
                .help("Show hidden files")
        }
    }

    func headerButton(_ title: String, icon: String, key: String, compact: Bool = false, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 5) {
                Image(systemName: icon).font(.system(size: 11))
                if !compact {
                    Text(title).font(.ui(size: 12))
                    Text(key).font(.ui(size: 10.5, design: .monospaced)).foregroundStyle(Theme.muted)
                }
            }
            .fixedSize()
            .padding(.horizontal, 8).frame(height: 24)
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain).foregroundStyle(Theme.text2)
    }

    /// The tree, under its own small toolbar.
    var treeSide: some View {
        VStack(spacing: 0) {
            HStack(spacing: 2) {
                Text("EXPLORER").font(.ui(size: 10.5, weight: .bold)).tracking(0.8).foregroundStyle(Theme.muted)
                Spacer()
                toolButton("doc.badge.plus", "New file") { model.newFile(in: model.selectedRow) }
                toolButton("folder.badge.plus", "New folder") { model.newFolder(in: model.selectedRow) }
                    .disabled(!model.canChangeFiles)
                toolButton("arrow.up.and.down.text.horizontal", "Collapse all folders") { model.collapseAll() }
            }
            .padding(.leading, 12).padding(.trailing, 6).frame(height: 30)
            Rectangle().fill(Theme.line.opacity(0.6)).frame(height: 1)
            tree
        }
        .contextMenu {
            Button("New File…") { model.newFile(in: nil) }
            Button("New Folder…") { model.newFolder(in: nil) }.disabled(!model.canChangeFiles)
            Button("Open Terminal Here") { model.terminalHere(nil) }
        }
    }

    func toolButton(_ icon: String, _ help: String, action: @escaping () -> Void) -> some View {
        Button(action: action) { Image(systemName: icon).font(.system(size: 12)).frame(width: 22, height: 22).contentShape(Rectangle()) }
            .buttonStyle(.plain).foregroundStyle(Theme.text2).help(help).accessibilityLabel(help)
    }

    var tree: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 0) {
                ForEach(model.rows) { r in FileRowView(model: model, row: r) }
                if model.dirs[""]?.isEmpty == true {
                    Text("Nothing here.").foregroundStyle(Theme.muted).padding(12)
                }
            }
            .padding(.vertical, 6)
        }
    }
}

struct FileRowView: View {
    /// Read so this is drawn again when the theme changes: its model does
    /// not change with it.
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: FilesModel
    let row: FileRow
    @State private var hovering = false

    var body: some View {
        let _ = look
        let on = model.selected == row.path
        HStack(spacing: 6) {
            Text(row.dir ? (row.open ? "▾" : "▸") : " ").font(.ui(size: 10)).foregroundStyle(Theme.muted).frame(width: 10)
            if row.dir {
                LucideIcon(row.open ? "folder-open" : "folder", color: Theme.accent, size: 12)
            } else {
                FileMark(name: row.name)
            }
            Text(row.name).font(.ui(size: 12.5)).foregroundStyle(row.ignored ? Theme.muted : Theme.text).lineLimit(1).truncationMode(.middle)
            if !row.dir && model.isDirty(row.path) { Circle().fill(Theme.text2).frame(width: 6, height: 6).help("Edited, not saved") }
            Spacer(minLength: 4)
            if !row.status.isEmpty {
                Text(row.status).font(.ui(size: 11, weight: .bold, design: .monospaced)).foregroundStyle(statusColor(row.status))
            }
        }
        .padding(.leading, CGFloat(row.depth) * 14 + 8).padding(.trailing, 10).frame(height: 24)
        .background(on ? Theme.accentDeep.opacity(0.35) : hovering ? Theme.raise : .clear)
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
        .onTapGesture { model.toggle(row) }
        .contextMenu { FileRowMenu(model: model, row: row) }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isButton)
        .accessibilityAction { model.toggle(row) }
        .help(row.path)
    }
}

/// A git status letter's colour, as the changes view draws it.
@MainActor func statusColor(_ s: String) -> Color {
    switch s {
    case "A", "?": return Theme.ok
    case "D", "U": return Theme.err
    default: return Theme.warn
    }
}

/// The file picked: in the editor when it can be saved, read only when not
/// (an older server, a file too big to hold whole), described when binary.
struct FilePreview: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: FilesModel
    @ObservedObject var store: Store

    var body: some View {
        let _ = look
        Group {
            if let p = model.preview, let path = model.selected {
                VStack(alignment: .leading, spacing: 0) {
                    // A big file has a bar of its own.
                    if model.big?.path != path {
                        bar(p, path)
                        Rectangle().fill(Theme.line).frame(height: 1)
                    }
                    if model.diskChanged { changedBanner(path) }
                    let kind = MediaKind(path: path)
                    if let big = model.big, big.path == path {
                        BigFileView(model: big, store: store)
                    } else if let e = model.reviewing {
                        AgentReviewView(model: model, store: store, edit: e)
                    } else if kind.needsFile && !(mediaHasSource(path) && model.showSource) {
                        media(kind)
                    } else if !p.isText {
                        note("A binary file\(p.mime.map { " (\($0))" } ?? ""): nothing to show as text.")
                    } else {
                        if let e = model.openEdit { workingBanner(e) }
                        CodeEditor(text: $model.text, language: CodeLanguage(path: path),
                                   fontSize: CGFloat(12.5 * store.appLook.textScale), editable: model.canSave,
                                   onSave: { model.save() },
                                   selection: $model.selection, findRequest: model.findRequest, replace: model.replaceRequest,
                                   reveal: model.reveal)
                            .id(path)
                    }
                }
            } else if !model.previewError.isEmpty {
                note("Could not read it: \(model.previewError)")
            } else {
                note("Pick a file to see it.")
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    /// A picture, a player, a PDF or a page — once the file is where it can open.
    @ViewBuilder func media(_ kind: MediaKind) -> some View {
        if let url = model.media {
            let here = model.machine == "local" && !model.root.isEmpty
            MediaView(kind: kind, url: url, readAccess: here ? URL(fileURLWithPath: model.root, isDirectory: true) : nil)
                .id("\(url.path)#\(model.mediaVersion)")
        } else if let f = model.fetching {
            VStack(spacing: 10) {
                Spacer()
                ProgressView(value: f).frame(width: 220)
                Text("Fetching from \(store.label(model.machine))…").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                Spacer()
            }.frame(maxWidth: .infinity)
        } else if !model.mediaNote.isEmpty {
            note(model.mediaNote)
        } else {
            note("Opening…")
        }
    }

    func bar(_ p: FSRead, _ path: String) -> some View {
        let kind = MediaKind(path: path)
        return HStack(spacing: 8) {
            Text(path).font(.ui(size: 12, design: .monospaced)).foregroundStyle(Theme.text).lineLimit(1).truncationMode(.head)
            if model.dirty {
                Text("edited").font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(Theme.warn)
                    .padding(.horizontal, 6).padding(.vertical, 1).overlay(Capsule().stroke(Theme.warn.opacity(0.5)))
            }
            Spacer()
            let rendered = kind.needsFile && !(mediaHasSource(path) && model.showSource)
            if mediaHasSource(path) {
                Picker("", selection: $model.showSource) {
                    Text(kind == .html ? "Page" : "Picture").tag(false)
                    Text("Source").tag(true)
                }
                .pickerStyle(.segmented).labelsHidden().fixedSize().controlSize(.small)
                .help("Rendered, or its source to edit")
            }
            if kind.needsFile, let url = model.media {
                if kind == .html {
                    Button { store.openBrowser(url.absoluteString) } label: { Image(systemName: "globe") }
                        .buttonStyle(.borderless).help("Open in a browser tab")
                }
                Button { NSWorkspace.shared.open(url) } label: { Image(systemName: "arrow.up.forward.app") }
                    .buttonStyle(.borderless)
                    .help(kind == .html ? "Open in your browser" : "Open in its app (Preview, QuickTime…)")
            }
            if !rendered && p.binary != true && model.big == nil {
                Button { model.findInFile(replace: true) } label: { Image(systemName: "text.magnifyingglass") }
                    .buttonStyle(.borderless).help("Find and replace in this file (⌥⌘F)")
                if model.canSave {
                    Button { model.showAgentPanel() } label: {
                        HStack(spacing: 4) { Image(systemName: "sparkles"); Text("Edit with Agent").font(.ui(size: 12)) }
                    }
                    .buttonStyle(.borderless).help("Have an agent edit this file or the lines selected (⌘I)")
                }
            }
            Text(kind == .text ? CodeLanguage(path: path).label : kind == .html ? "HTML" : mediaHasSource(path) ? "SVG" : kind.rawValue).font(.ui(size: 11)).foregroundStyle(Theme.muted)
            Text(fileSize(model.dirty ? Int64(model.text.utf8.count) : (p.size ?? 0))).font(.ui(size: 11)).foregroundStyle(Theme.muted)
            if model.canSave && !rendered {
                Button(model.saving ? "Saving…" : "Save") { model.save() }
                    .buttonStyle(.borderedProminent).controlSize(.small)
                    .disabled(!model.dirty || model.saving)
                    .keyboardShortcut("s", modifiers: .command)
                    .help("Save (⌘S) — refused if the file changed on disk since you opened it")
            } else if rendered {
                EmptyView()
            } else if p.truncated == true {
                Text("read only: too big to hold whole").font(.ui(size: 11)).foregroundStyle(Theme.muted)
            } else if p.binary != true {
                Text("read only: reload conch's server to edit").font(.ui(size: 11)).foregroundStyle(Theme.muted)
                    .help("conch on \(store.label(model.machine)) predates fs.write.v1")
            }
        }
        .padding(.horizontal, 12).frame(height: 32)
    }

    /// An agent was asked about this file and has not written it yet.
    func workingBanner(_ e: AgentEdit) -> some View {
        HStack(spacing: 10) {
            ProgressView().controlSize(.small)
            Text("\(e.agent) is working on \(e.lines.map { $0.count == 1 ? "line \($0.lowerBound)" : "lines \($0.lowerBound)–\($0.upperBound)" } ?? "this file")…")
                .font(.ui(size: 12))
            Spacer()
            if let p = e.pane { Button("Open its pane") { store.open(p) }.controlSize(.small) }
            Button("Stop Waiting") { model.dismissAgentEdit() }.controlSize(.small)
                .help("An ask that changes nothing — an explanation — is read in the agent's pane")
        }
        .padding(.horizontal, 12).frame(height: 34)
        .background(Theme.color(Theme.current.work).opacity(0.12))
    }

    /// The file was written while you were editing it.
    func changedBanner(_ path: String) -> some View {
        HStack(spacing: 10) {
            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(Theme.warn)
            Text("\((path as NSString).lastPathComponent) changed on disk while you were editing.").font(.ui(size: 12))
            Spacer()
            Button("Reload Theirs") { model.text = model.original; model.read(path) }.controlSize(.small)
            Button("Keep Mine") { model.diskChanged = false }.controlSize(.small)
                .help("Keep editing; saving will ask before it overwrites their version")
        }
        .padding(.horizontal, 12).frame(height: 34)
        .background(Theme.warn.opacity(0.12))
    }

    func note(_ s: String) -> some View {
        VStack { Spacer(); Text(s).foregroundStyle(Theme.muted); Spacer() }.frame(maxWidth: .infinity)
    }
}

/// A file's mark: its language's logo, filled, where there is one; else
/// the outline for its kind. Drawn in the brand colour, or the text's
/// where that would vanish into the background.
struct FileMark: View {
    @Environment(\.conchTheme) private var look
    let name: String

    var body: some View {
        let _ = look
        let f = fileIcon(name)
        let t = Theme.current
        let tint = Theme.color(fileTint(f, on: t.bg, text: t.fg, muted: t.muted))
        if Theme.icons != .off, let slug = f.logo, let ops = FileLogos.ops(slug) {
            Lucide(ops: ops).fill(tint).frame(width: 11, height: 11).frame(width: 14)
        } else {
            LucideIcon(f.icon, color: tint, size: 12)
        }
    }
}
