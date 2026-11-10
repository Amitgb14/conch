import AppKit
import ConchKit
import SwiftUI

// A file too big to hold whole — a log of a few gigabytes — read a window
// of lines at a time (fs.lines.v1): only the lines on screen and a few
// pieces around them are held, the scrollbar stands for the whole file,
// find runs on the server a piece at a time with its matches marked down
// the scrollbar, and a few lines can be edited and saved on their own
// (fs.write_range), checked against what was read. Past FileTier.editMax a
// save would copy too much, so the file is read and searched only.

@MainActor
final class BigFileModel: ObservableObject {
    unowned let files: FilesModel
    let path: String
    @Published var size: Int64
    @Published var window = LineWindow()
    @Published var top = 1
    @Published var rows = 40
    @Published var error = ""
    @Published var lossy = false
    /// Lines picked by their numbers, for editing.
    @Published var picked: ClosedRange<Int>?
    @Published var follow = false
    // Find, a piece at a time.
    @Published var query = ""
    @Published var matchCase = false
    @Published var regex = false
    @Published var matches: [Int] = []
    @Published var current: Int?
    @Published var searched: Double = 0
    @Published var searching = false
    @Published var truncatedMatches = false
    @Published var edit: RangeEdit?
    private var asked: Set<Int> = []
    private var finds = Latest()
    private var followTimer: Timer?

    /// The lines being edited: as read, where they are, and the fingerprint
    /// the save is checked against.
    struct RangeEdit: Identifiable {
        var id: Int { lines.lowerBound }
        var lines: ClosedRange<Int>
        var offset: Int64
        var length: Int64
        var base: String
        var text: String
        var original: String
    }

    static let matchesMax = 10_000

    init(files: FilesModel, path: String, size: Int64) {
        self.files = files
        self.path = path
        self.size = size
        fetch(1)
    }

    var tier: FileTier { FileTier(size: size) }
    var canEdit: Bool { tier != .huge && files.has("fs.lines.v1") }

    private struct LinesParams: Encodable { var root: String; var path: String; var line: Int?; var count: Int?; var tail: Bool? }

    /// Reads the piece starting at first, unless it is held or on its way.
    func fetch(_ first: Int) {
        guard window.line(first) == nil, !asked.contains(first), let c = files.store.connection(files.machine) else { return }
        asked.insert(first)
        let p = LinesParams(root: files.root, path: path, line: first, count: window.chunk)
        Task {
            defer { asked.remove(first) }
            do {
                let r = try await c.call("fs.lines", p, as: FSLines.self)
                if r.binary == true {
                    error = "Not text: nothing to show line by line."
                    return
                }
                window.total = r.lines
                size = r.size ?? size
                lossy = lossy || r.lossy == true
                window.put(first, r.texts, keepNear: top)
                error = ""
            } catch {
                self.error = "Could not read it: \(error)"
            }
        }
    }

    /// The pieces the screen needs.
    func fill() {
        for first in window.wanted(top: top, rows: rows) { fetch(first) }
    }

    func scroll(by lines: Int) {
        go(top + lines)
    }

    func go(_ line: Int) {
        let total = max(window.total, 1)
        top = max(1, min(line, max(total - rows + 1, 1)))
        fill()
    }

    func goTo(line n: Int) {
        go(topFor(line: n, rows: rows, total: window.total))
        picked = n...n
    }

    /// The file changed on disk: what is held is read again.
    func reload() {
        window.clear()
        asked = []
        fetch(window.start(of: top))
        fill()
    }

    func written() {
        if follow { tail() } else { reload() }
    }

    func tail() {
        guard let c = files.store.connection(files.machine) else { return }
        let p = LinesParams(root: files.root, path: path, count: 1, tail: true)
        Task {
            guard let r = try? await c.call("fs.lines", p, as: FSLines.self) else { return }
            window.total = r.lines
            size = r.size ?? size
            window.clear()
            go(r.lines - rows + 1)
        }
    }

    func setFollow(_ on: Bool) {
        follow = on
        followTimer?.invalidate()
        followTimer = nil
        guard on else { return }
        tail()
        // Changes are told when the file is in a watched worktree; a log
        // elsewhere, or a machine that can't watch, is asked every so often.
        followTimer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.tail() }
        }
    }

    // MARK: find

    func find() {
        let n = finds.next()
        matches = []
        current = nil
        truncatedMatches = false
        searched = 0
        guard !query.isEmpty, files.has("fs.search.v1"), let c = files.store.connection(files.machine) else {
            searching = false
            return
        }
        searching = true
        struct Params: Encodable {
            var root: String; var query: String; var regex: Bool?; var `case`: Bool?
            var path: String; var offset: Int64; var line: Int; var limit: Int
        }
        let root = files.root, q = query, re = regex, mc = matchCase
        Task {
            var offset: Int64 = 0, line = 1
            while finds.isLatest(n) {
                let p = Params(root: root, query: q, regex: re ? true : nil, case: mc ? true : nil, path: path, offset: offset, line: line, limit: 2000)
                let r: FSSearchResult
                do { r = try await c.call("fs.search", p, as: FSSearchResult.self) } catch {
                    guard finds.isLatest(n) else { return }
                    self.error = "\(error)"
                    break
                }
                guard finds.isLatest(n) else { return }
                matches += (r.files ?? []).flatMap { $0.lines.map(\.line) }
                if current == nil, let first = matches.first(where: { $0 >= top }) ?? matches.first { select(first) }
                let s = max(r.size ?? size, 1)
                searched = Double(r.next ?? s) / Double(s)
                if matches.count >= Self.matchesMax {
                    truncatedMatches = true
                    break
                }
                if r.done == true { break }
                offset = r.next ?? offset
                line = r.nextLine ?? line
            }
            if finds.isLatest(n) { searching = false }
        }
    }

    func stopFind() {
        _ = finds.next()
        searching = false
    }

    func select(_ line: Int) {
        current = line
        goTo(line: line)
    }

    func nextMatch(back: Bool) {
        guard !matches.isEmpty else { return }
        let from = current ?? top
        let next = back ? matches.last(where: { $0 < from }) ?? matches.last! : matches.first(where: { $0 > from }) ?? matches.first!
        select(next)
    }

    var matchLabel: String {
        guard !matches.isEmpty else { return searching ? "Searching…" : query.isEmpty ? "" : "No matches" }
        let i = current.flatMap { matches.firstIndex(of: $0) }.map { "\($0 + 1) of " } ?? ""
        return "\(i)\(matches.count)\(truncatedMatches || searching ? "+" : "")"
    }

    // MARK: edit a range

    /// Picks a line by its number: on its own, or with shift up to it.
    func pick(_ n: Int, extend: Bool) {
        if extend, let p = picked {
            picked = min(p.lowerBound, n)...max(p.upperBound, n)
        } else {
            picked = n...n
        }
    }

    func beginEdit() {
        guard canEdit, let lines = picked, let c = files.store.connection(files.machine) else { return }
        guard lines.count <= 2000 else {
            files.store.say("Pick fewer lines: up to 2,000 are edited at once", .err)
            return
        }
        let p = LinesParams(root: files.root, path: path, line: lines.lowerBound, count: lines.count)
        Task {
            do {
                let r = try await c.call("fs.lines", p, as: FSLines.self)
                guard r.editable, let data = r.data, let hash = r.hash else {
                    files.store.say("Those lines aren't plain UTF-8 text, so they can't be saved back as they were", .err)
                    return
                }
                edit = RangeEdit(lines: lines, offset: r.offset, length: r.length, base: hash, text: data, original: data)
            } catch {
                files.store.say("Could not read those lines: \(error)", .err)
            }
        }
    }

    func saveEdit() {
        guard let e = edit, let c = files.store.connection(files.machine) else { return }
        struct Params: Encodable { var root: String; var path: String; var offset: Int64; var length: Int64; var base: String; var data: String }
        Task {
            do {
                _ = try await c.call("fs.write_range", Params(root: files.root, path: path, offset: e.offset, length: e.length, base: e.base, data: e.text),
                                     as: FSWriteResult.self)
                edit = nil
                files.store.say("Saved lines \(e.lines.lowerBound)–\(e.lines.upperBound) of \((path as NSString).lastPathComponent)")
                reload()
            } catch let err as ConchError where err.code == "file_changed" {
                files.store.say("Those lines changed on disk since you opened them: read them again to edit", .err)
            } catch {
                files.store.say("Could not save: \(error)", .err)
            }
        }
    }
}

struct BigFileView: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: BigFileModel
    @ObservedObject var store: Store
    @State private var gotoText = ""
    let rowHeight: CGFloat = 18

    var body: some View {
        let _ = look
        VStack(spacing: 0) {
            bar
            Rectangle().fill(Theme.line).frame(height: 1)
            findBar
            Rectangle().fill(Theme.line).frame(height: 1)
            if !model.error.isEmpty {
                Text(model.error).foregroundStyle(Theme.err).padding(12).frame(maxWidth: .infinity, alignment: .leading)
            }
            GeometryReader { geo in
                HStack(spacing: 0) {
                    lines
                        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                        .clipped()
                        .overlay(WheelCatcher { dy in model.scroll(by: dy) })
                    scrollbar(height: geo.size.height)
                }
                .onAppear { setRows(geo.size.height) }
                .onChange(of: geo.size.height) { _, h in setRows(h) }
            }
            Rectangle().fill(Theme.line).frame(height: 1)
            status
        }
        .sheet(item: $model.edit) { _ in RangeEditSheet(model: model, store: store) }
    }

    func setRows(_ h: CGFloat) {
        model.rows = max(1, Int(h / rowHeight))
        model.fill()
    }

    var bar: some View {
        HStack(spacing: 8) {
            Text(model.path).font(.ui(size: 12, design: .monospaced)).lineLimit(1).truncationMode(.head)
            Text(model.tier == .huge ? "huge · read only" : "large · read in pieces")
                .font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(Theme.color(Theme.current.work))
                .padding(.horizontal, 6).padding(.vertical, 1).overlay(Capsule().stroke(Theme.color(Theme.current.work).opacity(0.5)))
            Spacer()
            TextField("Go to line", text: $gotoText)
                .textFieldStyle(.roundedBorder).font(.ui(size: 11.5, design: .monospaced)).frame(width: 100).controlSize(.small)
                .onSubmit {
                    if let n = Int(gotoText.filter(\.isNumber)) { model.goTo(line: n) }
                }
            Toggle("Follow end", isOn: Binding(get: { model.follow }, set: { model.setFollow($0) }))
                .toggleStyle(.checkbox).font(.ui(size: 12)).help("Keep the last lines in view as they are written")
            if model.canEdit {
                Button("Edit Lines…") { model.beginEdit() }
                    .controlSize(.small).disabled(model.picked == nil)
                    .help("Edit the lines picked by their numbers (shift-click to pick more) and save just those")
            }
            Text(fileSize(model.size)).font(.ui(size: 11)).foregroundStyle(Theme.muted)
        }
        .padding(.horizontal, 12).frame(height: 34)
    }

    var findBar: some View {
        HStack(spacing: 8) {
            TextField("Find in the file", text: $model.query)
                .textFieldStyle(.roundedBorder).font(.ui(size: 12, design: .monospaced)).frame(width: 280).controlSize(.small)
                .onSubmit { model.find() }
            Toggle("Aa", isOn: $model.matchCase).toggleStyle(.button).controlSize(.small).help("Match case")
            Toggle(".*", isOn: $model.regex).toggleStyle(.button).controlSize(.small).help("Regular expression")
            Text(model.matchLabel).font(.ui(size: 12)).foregroundStyle(Theme.text2)
            if model.searching {
                ProgressView(value: model.searched).frame(width: 110)
                Text("\(Int(model.searched * 100))% searched").font(.ui(size: 11)).foregroundStyle(Theme.muted)
                Button("Stop") { model.stopFind() }.buttonStyle(.plain).foregroundStyle(Theme.accent)
            }
            Button { model.nextMatch(back: true) } label: { Image(systemName: "chevron.up") }
                .buttonStyle(.borderless).disabled(model.matches.isEmpty).help("Previous match")
            Button { model.nextMatch(back: false) } label: { Image(systemName: "chevron.down") }
                .buttonStyle(.borderless).disabled(model.matches.isEmpty).help("Next match")
            Spacer()
        }
        .padding(.horizontal, 12).frame(height: 34)
        .background(Theme.paneHeader)
    }

    var lines: some View {
        let width = CGFloat(String(max(model.window.total, 1)).count) * 7.5 + 16
        return VStack(alignment: .leading, spacing: 0) {
            ForEach(0..<model.rows, id: \.self) { i in
                let n = model.top + i
                if n <= max(model.window.total, 1) {
                    row(n, width: width)
                }
            }
        }
        .font(.system(size: 12, design: .monospaced))
    }

    func row(_ n: Int, width: CGFloat) -> some View {
        let picked = model.picked?.contains(n) == true
        let match = model.current == n
        return HStack(spacing: 0) {
            Text("\(n)").foregroundStyle(picked ? Theme.text : Theme.muted)
                .frame(width: width, alignment: .trailing).padding(.trailing, 10)
                .contentShape(Rectangle())
                .onTapGesture { model.pick(n, extend: NSEvent.modifierFlags.contains(.shift)) }
                .help("Pick this line; shift-click to pick up to it")
            Text(model.window.line(n) ?? "").foregroundStyle(Theme.text).lineLimit(1).textSelection(.enabled)
            Spacer(minLength: 0)
        }
        .frame(height: rowHeight)
        .background(match ? Theme.warn.opacity(0.22) : picked ? Theme.color(Theme.current.work).opacity(0.12) : .clear)
    }

    /// The whole file, top to bottom: where the screen is, and the matches.
    func scrollbar(height: CGFloat) -> some View {
        let total = CGFloat(max(model.window.total, 1))
        let thumbH = max(14, height * CGFloat(model.rows) / total)
        let y = (height - thumbH) * CGFloat(model.top - 1) / max(total - CGFloat(model.rows), 1)
        return ZStack(alignment: .topLeading) {
            Theme.paneHeader
            ForEach(Array(model.matches.prefix(400).enumerated()), id: \.offset) { _, m in
                Rectangle().fill(Theme.warn).frame(height: 2).offset(y: height * CGFloat(m - 1) / total).padding(.horizontal, 3)
            }
            RoundedRectangle(cornerRadius: 3).fill(Theme.text2.opacity(0.35))
                .frame(height: thumbH).offset(y: min(max(y, 0), height - thumbH)).padding(.horizontal, 3)
        }
        .frame(width: 16)
        .contentShape(Rectangle())
        .gesture(DragGesture(minimumDistance: 0).onChanged { g in
            let f = min(max(g.location.y / max(height, 1), 0), 1)
            model.go(Int(f * total) - model.rows / 2)
        })
        .accessibilityLabel("Position in the file")
    }

    var status: some View {
        HStack(spacing: 16) {
            Text("Ln \(model.top.formatted()) of \(model.window.total.formatted())")
            if model.lossy { Text("not all UTF-8: shown with replacement characters").foregroundStyle(Theme.warn) }
            Spacer()
            Text(model.tier == .huge ? "over \(fileSize(FileTier.editMax)): read and find only" : "lines are saved on their own, checked against what you read")
        }
        .font(.ui(size: 11)).foregroundStyle(Theme.text2)
        .padding(.horizontal, 12).frame(height: 24)
    }
}

/// The lines picked, in an editor, saved on their own.
struct RangeEditSheet: View {
    @ObservedObject var model: BigFileModel
    @ObservedObject var store: Store
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(spacing: 0) {
            if let e = model.edit {
                HStack {
                    Text("Lines \(e.lines.lowerBound.formatted())–\(e.lines.upperBound.formatted()) of \((model.path as NSString).lastPathComponent)")
                        .font(.ui(size: 13, weight: .semibold))
                    Spacer()
                    Text("Saved only if these lines are unchanged on disk").font(.ui(size: 11)).foregroundStyle(Theme.muted)
                }
                .padding(12)
                CodeEditor(text: Binding(get: { model.edit?.text ?? "" }, set: { model.edit?.text = $0 }),
                           language: CodeLanguage(path: model.path), fontSize: CGFloat(12.5 * store.appLook.textScale),
                           onSave: { model.saveEdit() })
                    .frame(minHeight: 320)
                HStack {
                    Spacer()
                    Button("Cancel") { model.edit = nil }.keyboardShortcut(.cancelAction)
                    Button("Save Lines") { model.saveEdit() }.keyboardShortcut("s", modifiers: .command)
                        .buttonStyle(.borderedProminent).disabled(e.text == e.original)
                }
                .padding(12)
            }
        }
        .frame(width: 760, height: 480)
        .background(Theme.window)
    }
}

/// The wheel over the lines, as lines to scroll by: the view holds only
/// the lines on screen, so there is nothing for a scroll view to move.
struct WheelCatcher: NSViewRepresentable {
    var onScroll: (Int) -> Void

    func makeNSView(context: Context) -> WheelView {
        let v = WheelView()
        v.onScroll = onScroll
        return v
    }

    func updateNSView(_ v: WheelView, context: Context) { v.onScroll = onScroll }

    final class WheelView: NSView {
        var onScroll: ((Int) -> Void)?
        private var carry: CGFloat = 0

        override func scrollWheel(with e: NSEvent) {
            // A trackpad gives points, a mouse wheel lines.
            let dy = e.hasPreciseScrollingDeltas ? -e.scrollingDeltaY / 18 : -e.scrollingDeltaY * 3
            carry += dy
            let whole = Int(carry)
            if whole != 0 {
                carry -= CGFloat(whole)
                onScroll?(whole)
            }
        }

        // Clicks go through to the lines beneath.
        override func hitTest(_ point: NSPoint) -> NSView? {
            NSApp.currentEvent?.type == .scrollWheel ? super.hitTest(point) : nil
        }
    }
}
