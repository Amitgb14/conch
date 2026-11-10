import AppKit
import ConchKit
import SwiftUI

// The Files tab's search of the whole checkout (⌘⇧F): one box for file
// names and text in files alike (fs.search.v1), narrowed by folders and
// globs, and a replace of what it found (fs.replace.v1) — each file checked
// against the content it was searched in, so a file an agent wrote since is
// left alone and said. And ⌘P: a file by a few letters of its name
// (fs.find.v1).

@MainActor
final class FilesSearchModel: ObservableObject {
    unowned let files: FilesModel
    @Published var q = SearchQuery()
    @Published var replace = ""
    @Published var showReplace = false
    @Published var result: FSSearchResult?
    @Published var searching = false
    @Published var error = ""
    /// Files and lines struck from a replace, and files folded away.
    @Published var skipFiles: Set<String> = []
    @Published var skipLines: Set<String> = []
    @Published var folded: Set<String> = []
    @Published var replacing = false
    /// Bumped to put the keyboard in the search box.
    @Published var focus = 0
    private var runs = Latest()
    private var pending: DispatchWorkItem?

    init(files: FilesModel) { self.files = files }

    var canSearch: Bool { files.has("fs.search.v1") }
    var canReplace: Bool { files.has("fs.replace.v1") }
    var plan: ReplacePlan { ReplacePlan(result?.files ?? [], skipFiles: skipFiles, skipLines: skipLines) }

    func reset() {
        result = nil
        error = ""
        skipFiles = []
        skipLines = []
        folded = []
    }

    /// Searches a moment after the typing stops.
    func changed() {
        pending?.cancel()
        let w = DispatchWorkItem { [weak self] in self?.run() }
        pending = w
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.3, execute: w)
    }

    func run() {
        pending?.cancel()
        guard canSearch else {
            error = "conch on \(files.store.label(files.machine)) can't search a project yet — reload its server."
            return
        }
        guard let c = files.store.connection(files.machine), !files.root.isEmpty else { return }
        let query = q
        guard !query.query.isEmpty else {
            reset()
            return
        }
        struct Params: Encodable {
            var root: String
            var query: String
            var regex: Bool?
            var `case`: Bool?
            var word: Bool?
            var names: Bool?
            var text: Bool?
            var include: [String]?
            var exclude: [String]?
            var ignored: Bool?
        }
        let p = query.pattern
        let params = Params(root: files.root, query: p.query, regex: p.regex, case: p.case, word: p.word,
                            names: query.names ? true : nil, text: query.text ? true : nil,
                            include: query.includes.isEmpty ? nil : query.includes,
                            exclude: query.excludes.isEmpty ? nil : query.excludes, ignored: query.ignored ? true : nil)
        let n = runs.next()
        searching = true
        Task {
            defer { if runs.isLatest(n) { searching = false } }
            do {
                let r = try await c.call("fs.search", params, as: FSSearchResult.self)
                guard runs.isLatest(n) else { return }
                result = r
                error = ""
                skipFiles = []
                skipLines = []
            } catch {
                guard runs.isLatest(n) else { return }
                result = nil
                self.error = "\(error)"
            }
        }
    }

    /// Replaces in every file and line not struck out — or in one file.
    func replaceAll(only path: String? = nil) {
        guard canReplace, let c = files.store.connection(files.machine) else {
            files.store.say("conch on \(files.store.label(files.machine)) can't replace yet — reload its server.", .err)
            return
        }
        var plan = self.plan
        if let path { plan.files = plan.files.filter { $0.path == path } }
        guard !plan.files.isEmpty else { return }
        let open = plan.files.map(\.path).filter(files.isDirty)
        if !open.isEmpty {
            files.store.say("Save \(open.map { ($0 as NSString).lastPathComponent }.joined(separator: ", ")) first: edits not saved", .err)
            return
        }
        struct File: Encodable { var path: String; var hash: String; var lines: [Int] }
        struct Params: Encodable {
            var root: String
            var query: String
            var regex: Bool?
            var `case`: Bool?
            var word: Bool?
            var replace: String
            var files: [File]
        }
        let p = q.pattern
        let params = Params(root: files.root, query: p.query, regex: p.regex, case: p.case, word: p.word, replace: replace,
                            files: plan.files.map { File(path: $0.path, hash: $0.hash, lines: $0.lines) })
        replacing = true
        Task {
            defer { replacing = false }
            do {
                let r = try await c.call("fs.replace", params, as: FSReplaceResult.self)
                files.store.say(r.summary, (r.skipped ?? []).isEmpty ? .ok : .err)
                files.load()
                run()
            } catch {
                files.store.say("Could not replace: \(error)", .err)
            }
        }
    }
}

struct FilesSearchPanel: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: FilesSearchModel
    @FocusState private var focused: Bool

    var body: some View {
        let _ = look
        VStack(spacing: 0) {
            form
            Rectangle().fill(Theme.line).frame(height: 1)
            if !model.error.isEmpty {
                Text(model.error).font(.ui(size: 12)).foregroundStyle(Theme.err).padding(12)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            if let r = model.result {
                HStack {
                    Text(searchSummary(r, scope: model.q.scope)).font(.ui(size: 11)).foregroundStyle(Theme.text2).lineLimit(1)
                    Spacer()
                    if (r.files ?? []).count > 1 {
                        Button(model.folded.isEmpty ? "Collapse all" : "Expand all") {
                            model.folded = model.folded.isEmpty ? Set((r.files ?? []).map(\.path)) : []
                        }
                        .buttonStyle(.plain).font(.ui(size: 11)).foregroundStyle(Theme.accent)
                    }
                }
                .padding(.horizontal, 12).frame(height: 26)
                Rectangle().fill(Theme.line).frame(height: 1)
                results(r)
            } else {
                Spacer()
            }
        }
        .onAppear { focused = true }
        .onChange(of: model.focus) { _, _ in focused = true }
    }

    var form: some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack(spacing: 4) {
                Button { model.showReplace.toggle() } label: {
                    Image(systemName: model.showReplace ? "chevron.down" : "chevron.right").font(.system(size: 10)).foregroundStyle(Theme.muted)
                }
                .buttonStyle(.plain).frame(width: 14).help("Replace")
                field("Search the project", text: $model.q.query)
                    .focused($focused)
                    .onSubmit { model.run() }
                toggle("Aa", $model.q.matchCase, "Match case")
                toggle("ab", $model.q.word, "Whole word")
                toggle(".*", $model.q.regex, "Regular expression")
            }
            Picker("", selection: $model.q.scope) {
                Text("Everything").tag(SearchScope.everything)
                Text("File names").tag(SearchScope.names)
                Text("Text").tag(SearchScope.text)
            }
            .pickerStyle(.segmented).labelsHidden().controlSize(.small).padding(.leading, 18)
            if model.showReplace {
                HStack(spacing: 6) {
                    field("Replace with", text: $model.replace).onSubmit { model.replaceAll() }
                    Button(model.replacing ? "Replacing…" : "Replace All") { model.replaceAll() }
                        .controlSize(.small)
                        .disabled(model.plan.files.isEmpty || model.replacing || !model.canReplace || model.q.scope == .names)
                        .help(model.plan.summary + " — each file only if it is still as it was searched")
                }
                .padding(.leading, 18)
            }
            HStack(spacing: 6) {
                field("files to include", text: $model.q.include, mono: true).onSubmit { model.run() }
                field("files to exclude", text: $model.q.exclude, mono: true).onSubmit { model.run() }
            }
            .padding(.leading, 18)
            Toggle("Search what git ignores too", isOn: $model.q.ignored)
                .toggleStyle(.checkbox).font(.ui(size: 11.5)).foregroundStyle(Theme.text2).padding(.leading, 18)
        }
        .padding(10)
        .onChange(of: model.q) { _, _ in model.changed() }
    }

    func field(_ hint: String, text: Binding<String>, mono: Bool = false) -> some View {
        TextField(hint, text: text)
            .textFieldStyle(.roundedBorder)
            .font(.ui(size: mono ? 11.5 : 12, design: mono ? .monospaced : .default))
            .controlSize(.small)
    }

    func toggle(_ label: String, _ on: Binding<Bool>, _ help: String) -> some View {
        Button { on.wrappedValue.toggle() } label: {
            Text(label).font(.ui(size: 11, weight: .semibold, design: .monospaced))
                .foregroundStyle(on.wrappedValue ? Theme.color(Theme.current.selFG) : Theme.muted)
                .padding(.horizontal, 4).padding(.vertical, 2)
                .background(on.wrappedValue ? Theme.accentDeep : .clear, in: RoundedRectangle(cornerRadius: 3))
        }
        .buttonStyle(.plain).help(help).accessibilityLabel(help).accessibilityValue(on.wrappedValue ? "on" : "off")
    }

    /// One row of the results, named uniquely across the whole list: a
    /// lazy list takes rows by their identity, and lines named only by
    /// their numbers collided between files — line 6 of one file drew
    /// nothing, taken for line 6 of another.
    enum ResultRow: Identifiable {
        case heading(String)
        case name(String)
        case file(FSSearchFile)
        case line(FSSearchFile, FSSearchLine)

        var id: String {
            switch self {
            case .heading(let s): return "h:" + s
            case .name(let p): return "n:" + p
            case .file(let f): return "f:" + f.path
            case .line(let f, let l): return "l:" + lineKey(f.path, l.line)
            }
        }
    }

    func rows(_ r: FSSearchResult) -> [ResultRow] {
        var out: [ResultRow] = []
        if model.q.scope != .text, let names = r.names, !names.isEmpty {
            out.append(.heading("FILE NAMES"))
            out += names.map { .name($0) }
        }
        if model.q.scope != .names, let fs = r.files, !fs.isEmpty {
            if model.q.scope == .everything { out.append(.heading("TEXT")) }
            for f in fs {
                out.append(.file(f))
                if !model.folded.contains(f.path) { out += f.lines.map { .line(f, $0) } }
            }
        }
        return out
    }

    func results(_ r: FSSearchResult) -> some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 0) {
                ForEach(rows(r)) { row in
                    switch row {
                    case .heading(let s): heading(s)
                    case .name(let p): nameRow(p)
                    case .file(let f): fileHeader(f)
                    case .line(let f, let l): lineRow(f, l)
                    }
                }
            }
            .padding(.vertical, 4)
        }
    }

    func heading(_ s: String) -> some View {
        Text(s).font(.ui(size: 10.5, weight: .bold)).tracking(0.8).foregroundStyle(Theme.muted)
            .padding(.horizontal, 12).padding(.top, 8).padding(.bottom, 3)
    }

    func nameRow(_ p: String) -> some View {
        let name = (p as NSString).lastPathComponent
        let marks = fuzzyMarks(name, model.q.query)
        let dir = (p as NSString).deletingLastPathComponent
        return HStack(spacing: 6) {
            FileMark(name: name)
            marked(name, marks).font(.ui(size: 12.5)).lineLimit(1)
            Text(dir).font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.head)
            Spacer(minLength: 0)
        }
        .padding(.leading, 20).padding(.trailing, 10).frame(height: 24)
        .contentShape(Rectangle())
        .onTapGesture { model.files.openFile(p) }
        .help(p)
    }

    func fileHeader(_ f: FSSearchFile) -> some View {
        let name = (f.path as NSString).lastPathComponent
        let skipped = model.skipFiles.contains(f.path)
        return HStack(spacing: 6) {
            Image(systemName: model.folded.contains(f.path) ? "chevron.right" : "chevron.down").font(.system(size: 9)).foregroundStyle(Theme.muted).frame(width: 10)
            FileMark(name: name)
            Text(name).font(.ui(size: 12.5, weight: .medium)).foregroundStyle(skipped ? Theme.muted : Theme.text).strikethrough(skipped)
            Text((f.path as NSString).deletingLastPathComponent).font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.head)
            Spacer(minLength: 4)
            if model.showReplace && model.canReplace && f.hash != nil {
                Button { model.replaceAll(only: f.path) } label: { Image(systemName: "arrow.2.squarepath") }
                    .buttonStyle(.borderless).help("Replace in this file").disabled(skipped)
                Button {
                    if skipped { model.skipFiles.remove(f.path) } else { model.skipFiles.insert(f.path) }
                } label: { Image(systemName: skipped ? "arrow.uturn.backward" : "xmark") }
                    .buttonStyle(.borderless).help(skipped ? "Replace in it again" : "Leave this file out of Replace All")
            }
            Text("\(f.lines.count)").font(.ui(size: 10.5)).padding(.horizontal, 6).background(Theme.raise, in: Capsule())
        }
        .padding(.horizontal, 10).frame(height: 26)
        .contentShape(Rectangle())
        .onTapGesture {
            if model.folded.contains(f.path) { model.folded.remove(f.path) } else { model.folded.insert(f.path) }
        }
        .help(f.path)
    }

    func lineRow(_ f: FSSearchFile, _ l: FSSearchLine) -> some View {
        let key = lineKey(f.path, l.line)
        let struck = model.skipLines.contains(key) || model.skipFiles.contains(f.path)
        let replacing = model.showReplace && model.q.scope != .names
        return HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text("\(l.line)").font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.muted).frame(width: 40, alignment: .trailing)
            lineText(l, replacing: replacing && !struck).font(.ui(size: 11.5, design: .monospaced)).lineLimit(1)
                .opacity(struck ? 0.45 : 1)
            Spacer(minLength: 0)
            if replacing && f.hash != nil {
                Button {
                    if model.skipLines.contains(key) { model.skipLines.remove(key) } else { model.skipLines.insert(key) }
                } label: { Image(systemName: model.skipLines.contains(key) ? "arrow.uturn.backward" : "xmark").font(.system(size: 9)) }
                    .buttonStyle(.borderless).help(model.skipLines.contains(key) ? "Replace on this line again" : "Leave this line as it is")
            }
        }
        .padding(.leading, 18).padding(.trailing, 10).frame(height: 22)
        .contentShape(Rectangle())
        .onTapGesture { model.files.openFile(f.path, line: l.line) }
    }

    /// A result's line, its matches lit — or, while replacing, struck and
    /// followed by what replaces them.
    func lineText(_ l: FSSearchLine, replacing: Bool) -> Text {
        l.runs.reduce(Text("")) { acc, run in
            guard run.match else { return acc + Text(run.text).foregroundColor(Theme.text2) }
            if replacing {
                return acc + Text(run.text).foregroundColor(Theme.err).strikethrough()
                    + Text(model.replace).foregroundColor(Theme.ok)
            }
            return acc + Text(run.text).foregroundColor(Theme.text).bold()
        }
    }

    func marked(_ s: String, _ marks: [Bool]) -> Text {
        zip(s, marks).reduce(Text("")) { acc, p in
            acc + Text(String(p.0)).foregroundColor(p.1 ? Theme.accent : Theme.text).fontWeight(p.1 ? .bold : .regular)
        }
    }
}

/// ⌘P: a file of the checkout by a few letters of its name.
struct QuickOpenSheet: View {
    @Environment(\.conchTheme) private var look
    @Environment(\.dismiss) private var dismiss
    @ObservedObject var model: FilesModel
    @State private var query = ""
    @State private var found: [String] = []
    @State private var pick = 0
    @State private var finds = Latest()
    @FocusState private var focused: Bool

    var body: some View {
        let _ = look
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Image(systemName: "doc").foregroundStyle(Theme.muted)
                TextField("Go to file", text: $query)
                    .textFieldStyle(.plain).font(.ui(size: 15)).focused($focused)
                    .onSubmit { open(found.indices.contains(pick) ? found[pick] : nil) }
                Text(model.shown?.branch ?? "").font(.ui(size: 11)).foregroundStyle(Theme.muted)
            }
            .padding(.horizontal, 14).frame(height: 46)
            Rectangle().fill(Theme.line).frame(height: 1)
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    if query.isEmpty, !model.tabs.paths.isEmpty {
                        Text("OPEN").font(.ui(size: 10.5, weight: .bold)).foregroundStyle(Theme.muted).padding(.horizontal, 12).padding(.top, 8)
                    }
                    ForEach(Array(found.enumerated()), id: \.element) { i, p in
                        row(p, on: i == pick).onTapGesture { open(p) }
                    }
                    if !query.isEmpty, found.isEmpty {
                        Text("No file matches.").foregroundStyle(Theme.muted).padding(14)
                    }
                }
                .padding(6)
            }
            .frame(height: 340)
            Rectangle().fill(Theme.line).frame(height: 1)
            HStack(spacing: 16) {
                Text("↩ open"); Text("↑↓ move"); Text("esc close")
                Spacer()
            }
            .font(.ui(size: 11)).foregroundStyle(Theme.muted).padding(.horizontal, 14).frame(height: 30)
        }
        .frame(width: 620)
        .background(Theme.window)
        .onAppear {
            focused = true
            found = model.tabs.paths
        }
        .onChange(of: query) { _, q in find(q) }
        .onKeyPress(.downArrow) { pick = min(pick + 1, max(found.count - 1, 0)); return .handled }
        .onKeyPress(.upArrow) { pick = max(pick - 1, 0); return .handled }
        .onExitCommand { dismiss() }
    }

    func row(_ p: String, on: Bool) -> some View {
        let name = (p as NSString).lastPathComponent
        return HStack(spacing: 8) {
            FileMark(name: name)
            zip(name, fuzzyMarks(name, query)).reduce(Text("")) { acc, c in
                acc + Text(String(c.0)).foregroundColor(c.1 ? Theme.accent : Theme.text).fontWeight(c.1 ? .bold : .regular)
            }
            .font(.ui(size: 13))
            Text((p as NSString).deletingLastPathComponent).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.head)
            Spacer()
            if model.isDirty(p) { Circle().fill(Theme.text2).frame(width: 6, height: 6) }
        }
        .padding(.horizontal, 10).frame(height: 30)
        .background(on ? Theme.accentDeep.opacity(0.45) : .clear, in: RoundedRectangle(cornerRadius: 6))
        .contentShape(Rectangle())
    }

    func find(_ q: String) {
        pick = 0
        guard !q.trimmingCharacters(in: .whitespaces).isEmpty else {
            found = model.tabs.paths
            return
        }
        guard let c = model.store.connection(model.machine), !model.root.isEmpty else { return }
        struct Params: Encodable { var root: String; var query: String; var limit: Int }
        let n = finds.next()
        let root = model.root
        Task {
            let r = try? await c.call("fs.find", Params(root: root, query: q, limit: 50), as: FindResult.self)
            guard finds.isLatest(n) else { return }
            found = r?.paths ?? []
        }
    }

    func open(_ p: String?) {
        guard let p else { return }
        dismiss()
        model.openFile(p)
    }
}
