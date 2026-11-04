import AppKit
import ConchKit
import SwiftUI

// A project's files in a tab of its own, as the TUI's Files row opens
// them: its main checkout as a tree, folders read as they are opened,
// beside the start of the file picked. Read only.

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
    private var reads = Latest()

    init(key: String, store: Store) {
        self.key = key
        self.store = store
        (machine, project) = parseFilesKey(key) ?? ("local", "")
        load()
    }

    var rows: [FileRow] { fileRows(dirs, open: open) }
    var root: String { store.snapshots[machine]?.project(project)?.path ?? "" }

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
        if let s = selected { read(s) }
    }

    func list(_ dir: String) {
        guard let c = store.connection(machine), !root.isEmpty else { return }
        struct Params: Encodable { var root: String; var path: String; var files: Bool; var hidden: Bool }
        let p = Params(root: root, path: dir, files: true, hidden: hidden)
        Task {
            do {
                let l = try await c.call("fs.list", p, as: FSList.self)
                dirs[dir] = l.entries ?? []
            } catch {
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
            selected = r.path
            read(r.path)
        }
    }

    func read(_ path: String) {
        guard let c = store.connection(machine), !root.isEmpty else { return }
        struct Params: Encodable { var root: String; var path: String }
        let n = reads.next()
        Task {
            do {
                let r = try await c.call("fs.read", Params(root: root, path: path), as: FSRead.self)
                guard reads.isLatest(n) else { return } // another file was picked meanwhile
                preview = r
                previewError = ""
            } catch {
                guard reads.isLatest(n) else { return }
                preview = nil
                previewError = "\(error)"
            }
        }
    }

    /// Files in this checkout were written: read again what is on screen.
    func written(worktree: String) {
        guard worktree == root else { return }
        load()
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
                HStack(spacing: 0) {
                    tree.frame(width: 300)
                    Rectangle().fill(Theme.line).frame(width: 1)
                    FilePreview(model: model)
                }
            }
        }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: CGFloat(Theme.style.corner)))
        .overlay(RoundedRectangle(cornerRadius: CGFloat(Theme.style.corner)).stroke(Theme.line))
    }

    var header: some View {
        let name = store.snapshots[model.machine]?.project(model.project)?.name ?? model.project
        return HStack(spacing: 10) {
            LucideIcon("folder-open", color: Theme.accent, size: 14)
            Text("Files").font(.system(size: 13, weight: .semibold))
            Text(name).font(.system(size: 12)).foregroundStyle(Theme.text2)
            Text(model.root).font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.head)
            Spacer()
            Toggle("Hidden files", isOn: Binding(get: { model.hidden }, set: { model.hidden = $0; model.load() }))
                .toggleStyle(.checkbox).font(.system(size: 12)).foregroundStyle(Theme.text2)
            Button { model.load() } label: { LucideIcon("refresh-cw", size: 12) }.buttonStyle(.plain).help("Read the files again")
        }
        .padding(.horizontal, 12).frame(height: 40)
        .background(Theme.paneHeader)
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
            Text(row.dir ? (row.open ? "▾" : "▸") : " ").font(.system(size: 10)).foregroundStyle(Theme.muted).frame(width: 10)
            LucideIcon(row.dir ? (row.open ? "folder-open" : "folder") : "file-text", color: row.dir ? Theme.accent : Theme.muted, size: 12)
            Text(row.name).font(.system(size: 12.5)).foregroundStyle(row.ignored ? Theme.muted : Theme.text).lineLimit(1).truncationMode(.middle)
            Spacer(minLength: 4)
            if !row.status.isEmpty {
                Text(row.status).font(.system(size: 11, weight: .bold, design: .monospaced)).foregroundStyle(statusColor(row.status))
            }
        }
        .padding(.leading, CGFloat(row.depth) * 14 + 8).padding(.trailing, 10).frame(height: 24)
        .background(on ? Theme.accentDeep.opacity(0.35) : hovering ? Theme.raise : .clear)
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
        .onTapGesture { model.toggle(row) }
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

/// The start of the file picked: its text, or what it is when it has none.
struct FilePreview: View {
    /// Read so this is drawn again when the theme changes: its model does
    /// not change with it.
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: FilesModel

    var body: some View {
        let _ = look
        Group {
            if let p = model.preview, let path = model.selected {
                VStack(alignment: .leading, spacing: 0) {
                    HStack(spacing: 8) {
                        Text(path).font(.system(size: 12, design: .monospaced)).foregroundStyle(Theme.text).lineLimit(1).truncationMode(.head)
                        Spacer()
                        Text(fileSize(p.size ?? 0)).font(.system(size: 11)).foregroundStyle(Theme.muted)
                    }
                    .padding(.horizontal, 12).frame(height: 30)
                    Rectangle().fill(Theme.line).frame(height: 1)
                    if p.binary == true || p.data == nil {
                        note("A binary file\(p.mime.map { " (\($0))" } ?? ""): nothing to show as text.")
                    } else {
                        // At least the size it is shown in, from the top left: a
                        // scroll view both ways centres what is smaller. A geometry
                        // reader is not drawn again when only the theme changes,
                        // which left the text in the last theme's colour, so the
                        // theme is its identity: a new one draws it afresh.
                        GeometryReader { g in
                            ScrollView([.vertical, .horizontal]) {
                                VStack(alignment: .leading, spacing: 0) {
                                    Text(p.data ?? "").font(.system(size: 12, design: .monospaced)).foregroundStyle(Theme.text)
                                        .textSelection(.enabled).fixedSize().padding(12)
                                    if p.truncated == true {
                                        Text("The file goes on: this is its start.").font(.system(size: 11)).foregroundStyle(Theme.muted)
                                            .padding(.horizontal, 12).padding(.bottom, 12)
                                    }
                                }
                                .frame(minWidth: g.size.width, minHeight: g.size.height, alignment: .topLeading)
                            }
                        }
                        .id(look)
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

    func note(_ s: String) -> some View {
        VStack { Spacer(); Text(s).foregroundStyle(Theme.muted); Spacer() }.frame(maxWidth: .infinity)
    }
}
