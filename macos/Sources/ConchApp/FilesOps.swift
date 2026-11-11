import AppKit
import ConchKit
import SwiftUI

// The Files tab's own handling of a checkout's files: new files and
// folders, rename, duplicate and delete (fs.ops.v1), copying a path,
// showing it in Finder or a terminal. Every change is the server's to make,
// inside the checkout and never over something already there; the tree is
// read again after, and the open tabs follow a file that moved.

extension FilesModel {
    var canChangeFiles: Bool { has("fs.ops.v1") }

    /// Says why a file can't be changed from here, on an older server.
    private func needOps() -> Bool {
        guard canChangeFiles else {
            store.say("conch on \(store.label(machine)) can't change files from here yet — reload its server.", .err)
            return false
        }
        return true
    }

    /// One line asked for in a small window: a new name, a path.
    func askLine(_ title: String, info: String, value: String, button: String) -> String? {
        let a = NSAlert()
        a.messageText = title
        a.informativeText = info
        let field = NSTextField(string: value)
        field.frame = NSRect(x: 0, y: 0, width: 320, height: 24)
        a.accessoryView = field
        a.addButton(withTitle: button)
        a.addButton(withTitle: "Cancel")
        a.window.initialFirstResponder = field
        // The name selected without its extension, as Finder does.
        DispatchQueue.main.async {
            let ext = (value as NSString).pathExtension
            let stem = ext.isEmpty ? value.count : value.count - ext.count - 1
            field.currentEditor()?.selectedRange = NSRange(location: 0, length: max(0, stem))
        }
        guard a.runModal() == .alertFirstButtonReturn else { return nil }
        let v = field.stringValue.trimmingCharacters(in: .whitespaces)
        return v.isEmpty ? nil : v
    }

    struct PathParams: Encodable { var root: String; var path: String; var to: String? }

    /// Makes an empty file where the row is, and opens it.
    func newFile(in row: FileRow?) {
        guard has("fs.write.v1"), let c = store.connection(machine) else {
            store.say("conch on \(store.label(machine)) can't make files from here yet — reload its server.", .err)
            return
        }
        guard let name = askLine("New file", info: "A / in the name makes the folders on the way.", value: "", button: "Create") else { return }
        let path = newEntryPath(in: row, name: name)
        struct Params: Encodable { var root: String; var path: String; var data: String; var create: Bool }
        Task {
            do {
                // Refused if it is there already: no base, and not forced.
                _ = try await c.call("fs.write", Params(root: root, path: path, data: "", create: true), as: FSWriteResult.self)
                reopen(around: path)
                openFile(path)
            } catch let e as ConchError where e.code == "file_changed" {
                store.say("\(path) is there already", .err)
            } catch {
                store.say("Could not make \(path): \(error)", .err)
            }
        }
    }

    func newFolder(in row: FileRow?) {
        guard needOps(), let c = store.connection(machine) else { return }
        guard let name = askLine("New folder", info: "A / in the name makes the folders on the way.", value: "", button: "Create") else { return }
        let path = newEntryPath(in: row, name: name)
        struct Params: Encodable { var root: String; var path: String }
        Task {
            do {
                _ = try await c.call("fs.mkdir", Params(root: root, path: path), as: FSList.self)
                reopen(around: path + "/x")
            } catch {
                store.say("Could not make \(path): \(error)", .err)
            }
        }
    }

    func rename(_ row: FileRow) {
        guard needOps(), let c = store.connection(machine) else { return }
        if isDirty(row.path) || tabs.paths.contains(where: { $0.hasPrefix(row.path + "/") && isDirty($0) }) {
            store.say("Save \(row.name) first: it has edits not saved", .err)
            return
        }
        guard let name = askLine("Rename \(row.name)", info: "A new name, or a path from the top of the checkout to move it.",
                                 value: row.name, button: "Rename") else { return }
        let to = renamedPath(row.path, to: name)
        guard to != row.path else { return }
        Task {
            do {
                _ = try await c.call("fs.rename", PathParams(root: root, path: row.path, to: to), as: FSPathResult.self)
                moved(from: row.path, to: to)
                reopen(around: to)
            } catch {
                store.say("Could not rename \(row.name): \(error)", .err)
            }
        }
    }

    func duplicate(_ row: FileRow) {
        guard needOps(), let c = store.connection(machine) else { return }
        let names = Set(rows.map(\.path))
        let to = duplicateName(row.path) { names.contains($0) }
        Task {
            do {
                _ = try await c.call("fs.copy", PathParams(root: root, path: row.path, to: to), as: FSPathResult.self)
                reopen(around: to)
            } catch {
                store.say("Could not duplicate \(row.name): \(error)", .err)
            }
        }
    }

    func delete(_ row: FileRow) {
        guard needOps(), let c = store.connection(machine) else { return }
        let a = NSAlert()
        a.messageText = "Delete \(row.name)\(row.dir ? " and everything in it" : "")?"
        a.informativeText = "It is deleted from \(store.label(machine)) at once, not moved to the Trash. Whatever git has of it can be had back from git."
        a.alertStyle = .warning
        a.addButton(withTitle: "Delete")
        a.addButton(withTitle: "Cancel")
        guard a.runModal() == .alertFirstButtonReturn else { return }
        Task {
            do {
                _ = try await c.call("fs.delete", PathParams(root: root, path: row.path), as: FSPathResult.self)
                moved(from: row.path, to: nil)
                load()
            } catch {
                store.say("Could not delete \(row.name): \(error)", .err)
            }
        }
    }

    /// The open tabs after a file or folder moved or went.
    func moved(from old: String, to new: String?) {
        let wasShown = selected
        tabs.moved(from: old, to: new)
        if let s = wasShown, case .some(let n) = movedPath(s, from: old, to: new) {
            if let n {
                selected = n
            } else {
                selected = nil
                preview = nil
                text = ""
                original = ""
                big = nil
                if let next = tabs.active {
                    tabs.active = nil
                    openFile(next)
                }
            }
        }
    }

    /// Reads the tree again with the folders down to path open.
    func reopen(around path: String) {
        var dir = (path as NSString).deletingLastPathComponent
        while !dir.isEmpty {
            open.insert(dir)
            dir = (dir as NSString).deletingLastPathComponent
        }
        load()
    }

    func copyPath(_ row: FileRow, relative: Bool) {
        let p = relative ? row.path : (root as NSString).appendingPathComponent(row.path)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(p, forType: .string)
        store.say("Copied \(p)")
    }

    var isLocal: Bool { machine == "local" }

    func revealInFinder(_ row: FileRow) {
        NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: (root as NSString).appendingPathComponent(row.path))])
    }

    /// A terminal in the row's folder, in a tab of its own.
    func terminalHere(_ row: FileRow?) {
        let dir = row.map { $0.dir ? $0.path : ($0.path as NSString).deletingLastPathComponent } ?? ""
        store.newPane(machine: machine, cwd: dir.isEmpty ? root : (root as NSString).appendingPathComponent(dir), inProject: true, agent: nil)
    }

    /// The search, narrowed to a folder.
    func findIn(_ row: FileRow) {
        let dir = row.dir ? row.path : (row.path as NSString).deletingLastPathComponent
        search.q.include = dir.isEmpty ? "" : dir + "/"
        side = .search
        search.focus &+= 1
    }

    /// Every folder closed.
    func collapseAll() { open = [] }
}

extension FilesModel {
    /// The row of the file shown, for "new file here".
    var selectedRow: FileRow? { selected.flatMap { s in rows.first { $0.path == s } } }

    /// The editor's find bar, or with replace its replace field (⌘F, ⌥⌘F).
    func findInFile(replace: Bool) {
        // A big file's own find bar is always shown.
        guard selected != nil, big == nil else { return }
        replaceRequest = replace
        findRequest &+= 1
    }
}

/// The tabs of the files open.
struct FileTabsBar: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: FilesModel

    var body: some View {
        let _ = look
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 0) {
                ForEach(model.tabs.paths, id: \.self) { p in tab(p) }
            }
        }
        .frame(height: 32)
        .background(Theme.paneHeader)
    }

    func tab(_ p: String) -> some View {
        let on = p == model.selected
        let name = (p as NSString).lastPathComponent
        return HStack(spacing: 6) {
            FileMark(name: name)
            Text(name).font(.ui(size: 12, weight: on ? .medium : .regular)).foregroundStyle(on ? Theme.text : Theme.text2)
            CloseDot(dirty: model.isDirty(p)) { model.closeTab(p) }
        }
        .padding(.leading, 12).padding(.trailing, 6).frame(height: 32)
        .background(on ? Theme.window : .clear)
        .overlay(alignment: .top) { if on { Rectangle().fill(Theme.accent).frame(height: 2) } }
        .overlay(alignment: .trailing) { Rectangle().fill(Theme.line).frame(width: 1) }
        .contentShape(Rectangle())
        .onTapGesture { model.openFile(p) }
        .help(p)
    }
}

/// A tab's close button: a dot while it has edits not saved, an ✕ under the
/// pointer.
struct CloseDot: View {
    @Environment(\.conchTheme) private var look
    let dirty: Bool
    let close: () -> Void
    @State private var hovering = false

    var body: some View {
        let _ = look
        return Button(action: close) {
            Group {
                if dirty && !hovering {
                    Circle().fill(Theme.text2).frame(width: 7, height: 7)
                } else {
                    Image(systemName: "xmark").font(.system(size: 9, weight: .semibold)).foregroundStyle(Theme.muted)
                }
            }
            .frame(width: 16, height: 16).contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .help(dirty ? "Edited, not saved — close" : "Close")
        .accessibilityLabel("Close")
    }
}

/// A row's right-click menu.
struct FileRowMenu: View {
    @ObservedObject var model: FilesModel
    let row: FileRow

    var body: some View {
        if !row.dir {
            Button("Open") { model.openFile(row.path) }
            Button("Edit with Agent…") {
                model.openFile(row.path)
                model.agentPanel = true
            }
            .disabled(!model.has("fs.write.v1"))
            Divider()
        }
        Button("New File Here…") { model.newFile(in: row) }
        Button("New Folder Here…") { model.newFolder(in: row) }.disabled(!model.canChangeFiles)
        Button("Rename…") { model.rename(row) }.disabled(!model.canChangeFiles)
        Button("Duplicate") { model.duplicate(row) }.disabled(!model.canChangeFiles)
        Divider()
        Button("Find in \(row.dir ? "This Folder" : "Its Folder")…") { model.findIn(row) }
        Button("Copy Path") { model.copyPath(row, relative: false) }
        Button("Copy Relative Path") { model.copyPath(row, relative: true) }
        Button("Open Terminal Here") { model.terminalHere(row) }
        if model.isLocal { Button("Reveal in Finder") { model.revealInFinder(row) } }
        Divider()
        Button("Delete…", role: .destructive) { model.delete(row) }.disabled(!model.canChangeFiles)
    }
}
