import AppKit
import ConchKit
import SwiftUI
import UniformTypeIdentifiers

/// An agent's conversation, read from what it saves (agent.chat), kept up
/// to date while it is on screen: asked for every second while the agent
/// works and every few while it rests — five times a second while it is
/// being stopped — one request at a time, and sent only what changed. The
/// terminal is still the agent: a message sent here is typed into it
/// (agent.prompt), with its attachments as paths at its end; Stop is the
/// agent's own interrupt key (agent.interrupt), and a question is answered
/// with its keys.
@MainActor
final class ChatModel: ObservableObject {
    let key: String
    let pane: String
    unowned let store: Store
    @Published private(set) var log = ChatLog() {
        didSet { blocks = chatBlocks(log.turns) }
    }
    /// What the turns draw as, made again only when they change: the view
    /// is redrawn on every change to the Store, and folding up to 1500
    /// turns each time was work for nothing.
    @Published private(set) var blocks: [ChatBlock] = []
    @Published var error = ""
    @Published var draft = ""
    /// Files on the message being written: chips above it.
    @Published var attachments: [ChatAttachment] = []
    /// The checkout's files the @ picker offers, or nil when it is shut.
    @Published var picker: AtPicker?
    /// Stop, from the press until the agent says it has stopped.
    @Published private(set) var stop = ChatStop()
    /// Messages sent and not yet in the conversation: shown at once, as a
    /// chat does, and dropped when the agent's transcript has them.
    @Published private(set) var pending: [PendingMessage] = []
    /// Files sent from outside the checkout, now in it: offered again by @.
    @Published private(set) var recent: [ChatAttachment] = []
    private var inFlight = false
    private var timer: Timer?
    private var running = false
    /// Where the conversation was when Stop was pressed: an "Interrupted"
    /// after it says the stop took.
    private var stopMark: ChatMark?
    private var finds = 0
    /// The composer's text view, for the @ button to type into.
    weak var input: NSTextView?

    struct PendingMessage: Identifiable, Equatable {
        let id = UUID()
        var text: String
        var attachments: [ChatAttachment] = []
        var after: ChatMark
        var failed: String?
    }

    /// The @ picker: what is typed after the @, where, and what it found.
    struct AtPicker: Equatable {
        var range: NSRange
        var query: String
        var found: [String] = []
        var selected = 0
        var note: String?
        /// Recently attached files that match, then the checkout's.
        var items: [AtItem] = []
    }

    enum AtItem: Equatable, Identifiable {
        case file(String)
        case recent(ChatAttachment)
        var id: String {
            switch self {
            case .file(let p): return "f:" + p
            case .recent(let a): return "r:" + a.id.uuidString
            }
        }
    }

    init(key: String, store: Store) {
        self.key = key
        self.pane = parseChatKey(key) ?? key
        self.store = store
    }

    func start() {
        running = true
        load()
        schedule()
    }

    func stopPolling() {
        running = false
        timer?.invalidate()
        timer = nil
    }

    private func schedule() {
        timer?.invalidate()
        guard running else { return }
        let state = store.pane(pane)?.agent?.state
        let busy = chatBusy(state: state, pending: pending.count, turns: log.turns)
        let every = stop.active ? ChatStop.poll : busy ? 1 : 4
        timer = Timer.scheduledTimer(withTimeInterval: every, repeats: false) { [weak self] _ in
            Task { @MainActor in
                self?.checkStop()
                self?.load()
                self?.schedule()
            }
        }
    }

    private var machine: String { unkey(pane).machine }

    private func can(_ cap: String) -> Bool {
        store.connection(machine)?.hello?.capabilities?.contains(cap) == true
    }

    /// Asks for what changed. One request at a time: the first read of a
    /// long conversation takes seconds, and a newer request discarding the
    /// answer to an older one left the chat empty while the agent worked.
    func load() {
        guard !inFlight else { return }
        let (m, id) = unkey(pane)
        guard let c = store.connection(m) else {
            error = "Not connected to \(store.label(m))"
            return
        }
        guard c.hello?.capabilities?.contains("agent.chat.v1") == true else {
            error = "The server on \(store.label(m)) is from before chats; reload it (right-click \(store.label(m)) → Reload Server) to see this agent's conversation here."
            return
        }
        struct P: Encodable { var id: String; var epoch: String?; var ver: Int64? }
        let p = P(id: id, epoch: log.epoch, ver: log.epoch == nil ? nil : log.ver)
        inFlight = true
        Task {
            defer { inFlight = false }
            do {
                let got = try await c.call("agent.chat", p, as: AgentChat.self)
                var next = log
                if next.apply(got) || !log.loaded { log = next }
                pending.removeAll { $0.failed == nil && log.has(sent: $0.text, after: $0.after) }
                error = ""
                checkStop()
            } catch {
                self.error = "\(error)"
            }
        }
    }

    /// The agent's checkout and its own folder, for the paths it is given.
    var checkout: (root: String?, cwd: String?) {
        let p = store.pane(pane)
        let proj = p?.projectID.flatMap { store.snapshots[machine]?.project($0) }
        return (chatCheckout(cwd: p?.cwd, project: proj), p?.cwd)
    }

    /// The draft and its attachments typed into the agent and submitted,
    /// as if at its prompt. Files from outside the checkout are copied into
    /// it first; one that cannot be leaves the message unsent, to retry.
    func send() {
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        let atts = attachmentsToSend(attachments)
        guard !text.isEmpty || !atts.isEmpty else { return }
        let (m, id) = unkey(pane)
        guard let c = store.connection(m) else { return }
        guard can("agent.prompt.v1") else {
            error = "The server on \(store.label(m)) predates prompting an agent; reload it"
            return
        }
        let msg = PendingMessage(text: text, attachments: atts, after: log.mark)
        pending.append(msg)
        draft = ""
        picker = nil
        // An amber chip stays, saying it was not sent.
        attachments.removeAll { a in atts.contains { $0.id == a.id } }
        struct P: Encodable { var id: String; var text: String }
        Task {
            do {
                let refs = try await deliver(atts, c)
                let agent = store.pane(pane)?.agent?.name ?? ""
                try await c.call("agent.prompt", P(id: id, text: attachmentPrompt(text, refs, agent: agent)))
                schedule()
            } catch {
                if let i = pending.firstIndex(where: { $0.id == msg.id }) { pending[i].failed = "\(error)" }
            }
        }
    }

    struct AttachError: LocalizedError, CustomStringConvertible {
        let description: String
        var errorDescription: String? { description }
    }

    /// The attachments as the agent finds them: a checkout's file by its
    /// path; anything else copied into the checkout's .conch/attachments
    /// (agent.attach makes the folder), here by a copy and on another
    /// machine through its server (conch upload, fs.upload in chunks).
    private func deliver(_ atts: [ChatAttachment], _ c: Connection) async throws -> [AttachedRef] {
        let (root, cwd) = checkout
        let copied = atts.filter(\.copied)
        let names = uniqueNames(copied.map { attachmentFileName($0.name) })
        var folder: AttachFolder?
        if !copied.isEmpty {
            guard can("agent.attach.v1") else {
                throw AttachError(description: "the server on \(store.label(machine)) is from before attachments; reload it (right-click \(store.label(machine)) → Reload Server) to send files from outside the checkout")
            }
            struct P: Encodable { var id: String }
            folder = try await c.call("agent.attach", P(id: unkey(pane).id), as: AttachFolder.self)
        }
        var refs: [AttachedRef] = []
        var k = 0
        for a in atts {
            if case .checkout(let rel) = a.source {
                refs.append(AttachedRef(path: checkoutPath(root: root ?? cwd ?? "", cwd: cwd, rel), image: a.image))
                continue
            }
            guard let f = folder else { continue }
            let name = names[k]
            k += 1
            let landed = try await copy(a, into: f.dir, as: name)
            refs.append(AttachedRef(path: attachedPath(folder: f, name: landed), image: a.image))
            let rel = pathFor(agentIn: f.root, (f.dir as NSString).appendingPathComponent(landed))
            remember(ChatAttachment(name: a.name, size: a.size, source: .checkout(rel)))
        }
        return refs
    }

    /// One file put in the folder, by name; the name it landed as.
    private func copy(_ a: ChatAttachment, into dir: String, as name: String) async throws -> String {
        let fm = FileManager.default
        if machine == "local" {
            let dest = (dir as NSString).appendingPathComponent(name)
            switch a.source {
            case .data(let d): try d.write(to: URL(fileURLWithPath: dest))
            case .file(let p): try fm.copyItem(atPath: p, toPath: dest)
            case .checkout: break
            }
            return name
        }
        guard let b = store.binary else { throw AttachError(description: "conch was not found to copy files to \(store.label(machine))") }
        // conch upload names the copy after the file, so it goes from a
        // folder of its own under the name it is to have.
        let tmp = fm.temporaryDirectory.appendingPathComponent("conch-attach-" + UUID().uuidString)
        try fm.createDirectory(at: tmp, withIntermediateDirectories: true)
        defer { try? fm.removeItem(at: tmp) }
        let local = tmp.appendingPathComponent(name)
        switch a.source {
        case .data(let d): try d.write(to: local)
        case .file(let p): try fm.copyItem(atPath: p, toPath: local.path)
        case .checkout: break
        }
        let out = try await runConch(b, onMachine(machine, ["upload", "-to", dir, "-f", local.path]))
        guard let there = uploadedPaths(String(decoding: out, as: UTF8.self)).first else {
            throw AttachError(description: "\(a.name) did not reach \(store.label(machine))")
        }
        return (there as NSString).lastPathComponent
    }

    private func remember(_ a: ChatAttachment) {
        recent.removeAll { $0.source == a.source }
        recent.insert(a, at: 0)
        if recent.count > 5 { recent.removeLast(recent.count - 5) }
    }

    func retry(_ m: PendingMessage) {
        pending.removeAll { $0.id == m.id }
        draft = m.text
        attachments = m.attachments + attachments
        send()
    }

    func discard(_ m: PendingMessage) { pending.removeAll { $0.id == m.id } }

    // MARK: attachments

    /// Files from Finder, a picker or a paste: a file inside the checkout
    /// is named by its path (on this Mac only — elsewhere the checkout is
    /// another computer's), anything else copied when the message goes.
    func attach(_ urls: [URL]) {
        let root = machine == "local" ? checkout.root.map { URL(fileURLWithPath: $0).resolvingSymlinksInPath().path } : nil
        var skipped: [String] = []
        for u in urls where u.isFileURL {
            let path = u.resolvingSymlinksInPath().path
            var dir: ObjCBool = false
            guard FileManager.default.fileExists(atPath: path, isDirectory: &dir), !dir.boolValue else {
                skipped.append(u.lastPathComponent)
                continue
            }
            let size = ((try? FileManager.default.attributesOfItem(atPath: path))?[.size] as? NSNumber)?.int64Value ?? 0
            let name = u.lastPathComponent
            if let root, isInside(path, root), path != root {
                add(ChatAttachment(name: name, size: size, source: .checkout(pathFor(agentIn: root, path))))
            } else {
                add(ChatAttachment(name: name, size: size, source: .file(path)))
            }
        }
        if !skipped.isEmpty {
            error = "Folders are not attached (\(skipped.joined(separator: ", "))): attach the files in them"
        }
    }

    /// A picture pasted, as a PNG.
    func attachImage(_ png: Data) {
        let n = attachments.filter { if case .data = $0.source { return true }; return false }.count + 1
        add(ChatAttachment(name: pastedImageName(at: Date(), n: n), size: Int64(png.count), source: .data(png)))
    }

    /// A file of the checkout, by its path there; its size is not known.
    func attachCheckout(_ rel: String) {
        add(ChatAttachment(name: (rel as NSString).lastPathComponent, size: -1, source: .checkout(rel)))
    }

    private func add(_ a: ChatAttachment) {
        if case .checkout = a.source, attachments.contains(where: { $0.source == a.source }) { return }
        attachments.append(a)
    }

    func remove(_ a: ChatAttachment) { attachments.removeAll { $0.id == a.id } }

    /// The text or cursor moved: the @ picker opens on an "@word", follows
    /// it, and shuts when there is none.
    func cursorMoved(_ text: String, _ cursor: Int) {
        guard let (range, query) = atQuery(text, cursor: cursor) else {
            if picker != nil { picker = nil }
            return
        }
        if picker?.query == query && picker?.range == range { return }
        var p = picker ?? AtPicker(range: range, query: query)
        p.range = range
        p.query = query
        p.selected = 0
        picker = p
        find(query)
    }

    /// The @ button: an @ typed at the end of the message, the keyboard
    /// put there, and the picker opened.
    func startAt() {
        draft += draft.isEmpty || draft.hasSuffix(" ") || draft.hasSuffix("\n") ? "@" : " @"
        if let tv = input {
            tv.string = draft
            tv.window?.makeFirstResponder(tv)
            tv.setSelectedRange(NSRange(location: (draft as NSString).length, length: 0))
        }
        cursorMoved(draft, (draft as NSString).length)
    }

    private func find(_ q: String) {
        guard let root = checkout.root else {
            picker?.note = "This agent is not in a project's checkout"
            relist()
            return
        }
        guard can("fs.find.v1"), let c = store.connection(machine) else {
            picker?.note = "The server on \(store.label(machine)) is from before finding files; reload it"
            relist()
            return
        }
        finds += 1
        let n = finds
        struct P: Encodable { var root: String; var query: String; var limit: Int }
        Task {
            let r = try? await c.call("fs.find", P(root: root, query: q, limit: 8), as: FindResult.self)
            guard n == finds, picker != nil else { return }
            picker?.found = r?.paths ?? []
            picker?.note = r == nil ? "Could not search \((root as NSString).lastPathComponent)" : nil
            relist()
        }
        relist()
    }

    private func relist() {
        guard var p = picker else { return }
        let q = p.query.lowercased()
        let rec = recent.filter { q.isEmpty || $0.name.lowercased().contains(q) }.prefix(3).map { AtItem.recent($0) }
        p.items = p.found.map { AtItem.file($0) } + rec
        p.selected = min(p.selected, max(p.items.count - 1, 0))
        picker = p
    }

    enum PickerKey { case up, down, choose, close }

    /// A key while the picker is open; false when it is not, for the
    /// text to take.
    func pickerKey(_ k: PickerKey) -> Bool {
        guard let p = picker else { return false }
        switch k {
        case .up: picker?.selected = max(p.selected - 1, 0)
        case .down: picker?.selected = min(p.selected + 1, max(p.items.count - 1, 0))
        case .close: picker = nil
        case .choose:
            guard p.items.indices.contains(p.selected) else { picker = nil; return true }
            choose(p.items[p.selected])
        }
        return true
    }

    func choose(_ item: AtItem) {
        guard let p = picker else { return }
        draft = removingAt(draft, p.range)
        picker = nil
        switch item {
        case .file(let rel): attachCheckout(rel)
        case .recent(let a): add(ChatAttachment(name: a.name, size: a.size, source: a.source))
        }
    }

    // MARK: Stop

    /// Stop pressed, by the button or escape: the agent's interrupt at
    /// once, then watched closely until it says it stopped.
    func interrupt() {
        let wasIdle = stop.phase == .idle || stop.phase == .stuck
        let act = stop.press(at: Date())
        if wasIdle && act == .interrupt { stopMark = log.mark }
        perform(act)
        schedule()
    }

    /// A look at whether the stop took.
    func checkStop() {
        guard stop.phase != .idle else { return }
        guard let p = store.pane(pane), p.running else {
            stop.reset()
            return
        }
        let interrupted = stopMark.map { log.interrupted(after: $0) } ?? false
        let act = stop.check(at: Date(), stopped: p.agent?.state != .working || interrupted)
        perform(act)
    }

    private func perform(_ act: ChatStopAction) {
        guard act != .none else { return }
        let (m, id) = unkey(pane)
        guard let c = store.connection(m) else { return }
        let hard = act == .hard
        if can("agent.interrupt.v1") {
            struct P: Encodable { var id: String; var hard: Bool? }
            struct R: Decodable { var keys: [String]?; var state: String? }
            Task {
                do {
                    let r = try await c.call("agent.interrupt", P(id: id, hard: hard ? true : nil), as: R.self)
                    if !hard { stop.sent = stopKeysText(r.keys ?? ["escape"]) }
                    checkStop()
                } catch {
                    self.error = "Could not stop the agent: \(error)"
                }
            }
        } else {
            // A server from before agent.interrupt: the key as any key.
            struct P: Encodable { var id: String; var keys: [String] }
            Task { try? await c.call("pane.send_keys", P(id: id, keys: [hard ? "ctrl+c" : "escape"])) }
        }
    }
}

struct ChatView: View {
    @ObservedObject var store: Store
    @ObservedObject var model: ChatModel
    /// Tool runs and tools opened by hand, by their turn's seq.
    @State private var open: Set<Int> = []
    /// Whether the end of the conversation is on screen. Held apart from
    /// this view's state, and watched only by the Latest button: as state
    /// here, each change redrew the list, which moved the end in or out of
    /// view again — a loop that kept the window at full processor.
    @State private var follow = FollowState()
    /// Files dragged over the composer: how many, and how many pictures.
    @State private var drop = DropState()

    var body: some View {
        let p = store.pane(model.pane)
        let focused = store.layout.focused == model.key
        VStack(spacing: 0) {
            header(p)
            Rectangle().fill(Theme.line).frame(height: 1)
            if p == nil {
                note("This agent has ended. Its conversation is saved: open it from Sessions to resume.")
            } else {
                messages(p)
            }
            if let q = store.questions[model.pane], !q.choices.isEmpty {
                question(q)
            }
            if p?.running == true { composer(p) }
        }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: CGFloat(Theme.style.corner)))
        .modifier(CardFrame(focused: focused))
        .contentShape(Rectangle())
        .onTapGesture { store.focus(model.key) }
        .onAppear { model.start() }
        .onDisappear { model.stopPolling() }
        .onChange(of: p?.agent?.state) { _ in model.checkStop(); model.load() }
    }

    var squared: Bool { Theme.style == .jetbrains }

    func header(_ p: PaneInfo?) -> some View {
        HStack(spacing: 8) {
            if let a = p?.agent { Mark(agent: a.name) } else { Image(systemName: "bubble.left").font(.system(size: 11)).foregroundStyle(Theme.muted) }
            Text(p?.agent.map { agentDisplayName($0.name) } ?? "Agent").font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.text)
            if let b = p?.branch, !b.isEmpty {
                Text(b).font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.text2).lineLimit(1)
                    .padding(.horizontal, 6).padding(.vertical, 1).background(RoundedRectangle(cornerRadius: 5).fill(Theme.raise))
            }
            Spacer(minLength: 4)
            Pill.state(p?.agent?.state)
            if store.layout.current?.benchAgent == model.pane {
                // The same conversation as its terminal: one or the other
                // where there is room for one.
                ChatTerminalSwitch(store: store, chat: true)
            } else {
                Button { store.open(model.pane) } label: { Image(systemName: "terminal").font(.system(size: 11)) }
                    .buttonStyle(.plain).foregroundStyle(Theme.text2).help("Show the agent's terminal")
            }
        }
        .padding(.horizontal, 10).frame(height: 30)
        .font(.ui(size: 12))
        .background(Theme.paneHeader)
    }

    func messages(_ p: PaneInfo?) -> some View {
        let blocks = model.blocks
        let working = p?.agent?.state == .working
        let lastID = blocks.last?.id
        return ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 14) {
                    if !model.error.isEmpty {
                        Text(model.error).font(.ui(size: 12)).foregroundStyle(Theme.err).textSelection(.enabled)
                    }
                    if blocks.isEmpty && model.pending.isEmpty && model.error.isEmpty {
                        VStack(spacing: 8) {
                            if !model.log.loaded { ProgressView().controlSize(.small) }
                            Text(!model.log.loaded ? "Reading the conversation…"
                                 : model.log.note ?? "Nothing said yet. Write below; what the agent does shows here as it works.")
                                .font(.ui(size: 12)).foregroundStyle(Theme.muted).multilineTextAlignment(.center)
                        }
                        .frame(maxWidth: .infinity).padding(.top, 40)
                    }
                    ForEach(blocks) { b in
                        block(b, live: working && b.id == lastID).id(b.id)
                    }
                    ForEach(model.pending) { m in pendingBubble(m).id(m.id) }
                    if working, let since = p?.agent?.since {
                        WorkingRow(since: since, message: p?.agent?.message, agent: p?.agent?.name)
                    }
                    Color.clear.frame(height: 1).id("end")
                        .onAppear { follow.set(true) }
                        .onDisappear { follow.set(false) }
                }
                .padding(14)
            }
            .overlay(alignment: .bottom) {
                if !blocks.isEmpty {
                    LatestButton(follow: follow) { withAnimation(.easeOut(duration: 0.15)) { proxy.scrollTo("end", anchor: .bottom) } }
                }
            }
            // Followed while at the bottom; left alone while reading back.
            .onChange(of: model.log.ver) { _ in if follow.atBottom { proxy.scrollTo("end", anchor: .bottom) } }
            .onChange(of: model.pending.count) { _ in proxy.scrollTo("end", anchor: .bottom) }
            .onChange(of: model.log.loaded) { _ in proxy.scrollTo("end", anchor: .bottom) }
            .onAppear { proxy.scrollTo("end", anchor: .bottom) }
        }
    }

    @ViewBuilder
    func block(_ b: ChatBlock, live: Bool) -> some View {
        switch b {
        case .user(let s, _):
            let (text, refs) = splitAttached(s)
            HStack {
                Spacer(minLength: 40)
                VStack(alignment: .trailing, spacing: 6) {
                    if !text.isEmpty {
                        Text(text).font(.ui(size: 13)).foregroundStyle(Theme.text).textSelection(.enabled)
                    }
                    if !refs.isEmpty { sentChips(refs.map { SentChip(name: ($0.path as NSString).lastPathComponent, image: $0.image, path: $0.path) }) }
                }
                .padding(.horizontal, 11).padding(.vertical, 8)
                .background(RoundedRectangle(cornerRadius: squared ? 6 : 12).fill(Theme.raise))
                .overlay(RoundedRectangle(cornerRadius: squared ? 6 : 12).stroke(Theme.line))
            }
        case .agent(let s, _):
            HStack(alignment: .top, spacing: 9) {
                if let a = store.pane(model.pane)?.agent { Mark(agent: a.name).padding(.top, 2) }
                MarkdownView(text: s).frame(maxWidth: .infinity, alignment: .leading)
            }
            .contextMenu {
                Button("Copy") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(s, forType: .string)
                }
            }
        case .tools(let run, let at):
            toolRun(run, at: at, live: live)
        case .note(let s, _):
            HStack {
                Spacer()
                Text(s).font(.ui(size: 11)).foregroundStyle(Theme.muted)
                    .padding(.horizontal, 9).padding(.vertical, 2)
                    .background(Capsule().fill(Theme.raise))
                Spacer()
            }
        }
    }

    /// A run of tools between two messages: one line saying what was done,
    /// opening to each tool. The run the agent is in the middle of stays
    /// open, so its newest tool can be watched.
    @ViewBuilder
    func toolRun(_ run: [ChatTurn], at: Int, live: Bool) -> some View {
        if run.count == 1, let t = run.first {
            ToolCard(turn: t, live: live, open: binding(t.seq ?? at)).padding(.leading, 26)
        } else {
            let shown = open.contains(at) || live
            VStack(alignment: .leading, spacing: 6) {
                Button {
                    if open.contains(at) { open.remove(at) } else { open.insert(at) }
                } label: {
                    HStack(spacing: 7) {
                        Image(systemName: shown ? "chevron.down" : "chevron.right").font(.system(size: 9, weight: .semibold)).frame(width: 10)
                        Image(systemName: "wrench.and.screwdriver").font(.system(size: 11))
                        Text(toolsSummary(run)).font(.ui(size: 12)).foregroundStyle(Theme.text2).lineLimit(1)
                        if run.contains(where: { $0.error == true }) {
                            Image(systemName: "exclamationmark.circle.fill").font(.system(size: 10)).foregroundStyle(Theme.err)
                        }
                        Spacer(minLength: 0)
                    }
                    .foregroundStyle(Theme.muted)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                if shown {
                    ForEach(Array(run.enumerated()), id: \.offset) { i, t in
                        ToolCard(turn: t, live: live && i == run.count - 1, open: binding(t.seq ?? (at * 1000 + i)))
                    }
                }
            }
            .padding(.leading, 26)
        }
    }

    func binding(_ id: Int) -> Binding<Bool> {
        Binding(get: { open.contains(-id - 1) }, set: { if $0 { open.insert(-id - 1) } else { open.remove(-id - 1) } })
    }

    func pendingBubble(_ m: ChatModel.PendingMessage) -> some View {
        HStack {
            Spacer(minLength: 40)
            VStack(alignment: .trailing, spacing: 4) {
                VStack(alignment: .trailing, spacing: 6) {
                    if !m.text.isEmpty {
                        Text(m.text).font(.ui(size: 13)).foregroundStyle(Theme.text).textSelection(.enabled)
                    }
                    if !m.attachments.isEmpty { sentChips(m.attachments.map { SentChip(name: $0.name, image: $0.image, attachment: $0) }) }
                }
                    .padding(.horizontal, 11).padding(.vertical, 8)
                    .background(RoundedRectangle(cornerRadius: squared ? 6 : 12).fill(Theme.raise))
                    .overlay(RoundedRectangle(cornerRadius: squared ? 6 : 12).stroke(m.failed == nil ? Theme.line : Theme.err.opacity(0.6)))
                    .opacity(m.failed == nil ? 0.7 : 1)
                if let f = m.failed {
                    HStack(spacing: 8) {
                        Text("Not sent: \(f)").foregroundStyle(Theme.err).lineLimit(2)
                        Button("Retry") { model.retry(m) }.buttonStyle(.plain).foregroundStyle(Theme.accent)
                        Button("Discard") { model.discard(m) }.buttonStyle(.plain).foregroundStyle(Theme.muted)
                    }
                    .font(.ui(size: 11))
                } else {
                    Text("Sent").font(.ui(size: 10.5)).foregroundStyle(Theme.muted)
                }
            }
        }
    }

    func question(_ q: Question) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 7) {
                Circle().fill(Theme.warn).frame(width: 7, height: 7)
                Text(q.text.isEmpty ? "Waiting for your answer" : q.text).font(.ui(size: 12.5, weight: .semibold)).foregroundStyle(Theme.text)
                    .fixedSize(horizontal: false, vertical: true)
            }
            FlowChoices(store: store, pane: model.pane, question: q)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: squared ? 6 : 10).fill(Theme.warn.opacity(0.09)))
        .overlay(RoundedRectangle(cornerRadius: squared ? 6 : 10).stroke(Theme.warn.opacity(0.5)))
        .padding(.horizontal, 10).padding(.bottom, 6)
    }

    func composer(_ p: PaneInfo?) -> some View {
        let lines = max(model.draft.components(separatedBy: "\n").count, 1) + model.draft.count / 70
        let height = min(max(CGFloat(lines) * 17 + 8, 26), 150)
        let toSend = attachmentsToSend(model.attachments)
        let empty = model.draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && toSend.isEmpty
        let busy = chatBusy(state: p?.agent?.state, pending: model.pending.count, turns: model.log.turns)
        let name = p?.agent.map { agentDisplayName($0.name) } ?? "the agent"
        let stopping = model.stop.phase != .idle
        return VStack(alignment: .leading, spacing: 6) {
            if !model.attachments.isEmpty {
                ChoiceFlow(spacing: 6) {
                    ForEach(model.attachments) { a in
                        AttachmentChip(attachment: a, problem: attachmentProblem(model.attachments, a), root: model.checkout.root,
                                       local: unkey(model.pane).machine == "local") { model.remove(a) }
                    }
                }
            }
            ChatInput(text: $model.draft, placeholder: "Reply to \(name)…",
                      onSend: { model.send() },
                      onEscape: {
                          if model.pickerKey(.close) { return true }
                          guard busy || stopping else { return false }
                          model.interrupt()
                          return true
                      },
                      onCursor: { text, cursor in model.cursorMoved(text, cursor) },
                      onPicker: { model.pickerKey($0) },
                      onPasteFiles: { model.attach($0) },
                      onPasteImage: { model.attachImage($0) },
                      onMake: { model.input = $0 })
                .frame(height: height)
            HStack(spacing: 8) {
                Button { pickFiles() } label: {
                    Image(systemName: "paperclip").font(.system(size: 12)).frame(width: 24, height: 22)
                        .background(RoundedRectangle(cornerRadius: squared ? 4 : 6).stroke(Theme.line2))
                }
                .buttonStyle(.plain).foregroundStyle(Theme.text2)
                .keyboardShortcut("a", modifiers: [.command, .shift])
                .help("Attach files (⌘⇧A) · or paste, or drop them here")
                Button { model.startAt() } label: {
                    Text("@").font(.ui(size: 12, weight: .semibold)).frame(width: 24, height: 22)
                        .background(RoundedRectangle(cornerRadius: squared ? 4 : 6).stroke(Theme.line2))
                }
                .buttonStyle(.plain).foregroundStyle(Theme.text2)
                .help("A file in the agent's checkout")
                Text(hint(busy: busy, name: name))
                    .font(.ui(size: 11)).foregroundStyle(model.stop.phase == .stuck || model.stop.phase == .harder ? Theme.warn : Theme.muted)
                    .lineLimit(1).truncationMode(.tail)
                Spacer()
                if busy || stopping {
                    stopButton
                }
                if !(busy || stopping) || !empty {
                Button { model.send() } label: {
                    Image(systemName: "arrow.up").font(.system(size: 12, weight: .bold))
                        .foregroundStyle(Theme.selectedText).frame(width: 26, height: 24)
                        .background(RoundedRectangle(cornerRadius: squared ? 4 : 7).fill(empty ? Theme.muted.opacity(0.4) : Theme.accentDeep))
                }
                .buttonStyle(.plain).disabled(empty)
                .help("Send to the agent, as if typed at its prompt")
                }
            }
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: squared ? 6 : 11).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: squared ? 6 : 11).stroke(Theme.line2))
        .overlay {
            if drop.count != nil {
                RoundedRectangle(cornerRadius: squared ? 6 : 11).strokeBorder(Theme.accent, style: StrokeStyle(lineWidth: 2, dash: [6, 4]))
                    .background(RoundedRectangle(cornerRadius: squared ? 6 : 11).fill(Theme.accent.opacity(0.08)))
                    .overlay(Text(dropOverlayText(count: drop.count ?? 0, images: drop.images))
                        .font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.accent))
                    .allowsHitTesting(false)
            }
        }
        .overlay(alignment: .topLeading) {
            if let pk = model.picker { atPicker(pk).offset(y: -8).alignmentGuide(.top) { $0[.bottom] } }
        }
        .onDrop(of: [.fileURL, .image], delegate: AttachDrop(state: $drop, model: model))
        .padding([.horizontal, .bottom], 10)
    }

    func hint(busy: Bool, name: String) -> String {
        if let h = model.stop.hint(agent: name) { return h }
        if model.picker != nil { return "↑↓ choose · ⏎ attach · esc close" }
        return busy ? "Working · what you send is queued · esc stops"
            : "Paste or drop images and files · @ for a file in the checkout · ⏎ send"
    }

    /// Stop while the agent works: Stopping… at once, then Stop harder.
    var stopButton: some View {
        let st = model.stop
        return Button { model.interrupt() } label: {
            HStack(spacing: 5) {
                if st.phase == .idle || st.phase == .stuck {
                    Image(systemName: "stop.fill").font(.system(size: 10, weight: .bold))
                } else if st.phase != .harder {
                    ProgressView().controlSize(.mini)
                }
                if st.phase != .idle { Text(st.label).font(.ui(size: 11.5, weight: .semibold)) }
            }
            .foregroundStyle(st.pressable ? Theme.selectedText : Theme.text2)
            .padding(.horizontal, st.phase == .idle ? 0 : 9).frame(minWidth: 26, minHeight: 24)
            .background(RoundedRectangle(cornerRadius: squared ? 4 : 7).fill(st.pressable ? Theme.err : Theme.err.opacity(0.3)))
        }
        .buttonStyle(.plain)
        .disabled(!st.pressable)
        .help(st.phase == .harder ? "Send ctrl+c: some agents need it to stop" : "Stop what the agent is doing (its interrupt key, esc)")
        .accessibilityLabel(st.label)
    }

    /// The checkout's files matching what is typed after the @, above the
    /// composer.
    func atPicker(_ pk: ChatModel.AtPicker) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text("Files in \((model.checkout.root.map { ($0 as NSString).lastPathComponent }) ?? "the checkout")")
                .font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(Theme.muted).textCase(.uppercase)
                .padding(.horizontal, 8).padding(.top, 4).padding(.bottom, 2)
            if pk.items.isEmpty {
                Text(pk.note ?? (pk.query.isEmpty ? "Type to find a file" : "No file matches “\(pk.query)”"))
                    .font(.ui(size: 12)).foregroundStyle(Theme.muted).padding(.horizontal, 8).padding(.vertical, 5)
            }
            ForEach(Array(pk.items.enumerated()), id: \.element.id) { i, item in
                Button { model.choose(item) } label: {
                    HStack(spacing: 8) {
                        switch item {
                        case .file(let path):
                            FileMark(name: path)
                            Text(path).font(.ui(size: 12, design: .monospaced)).lineLimit(1).truncationMode(.head)
                        case .recent(let a):
                            Image(systemName: a.image ? "photo" : "clock").font(.system(size: 11)).foregroundStyle(Theme.muted).frame(width: 14)
                            Text(a.name).font(.ui(size: 12, design: .monospaced)).lineLimit(1)
                            Text("recently attached").font(.ui(size: 10.5)).foregroundStyle(Theme.muted)
                        }
                        Spacer(minLength: 0)
                    }
                    .foregroundStyle(i == pk.selected ? Theme.text : Theme.text2)
                    .padding(.horizontal, 8).padding(.vertical, 4)
                    .background(RoundedRectangle(cornerRadius: 5).fill(i == pk.selected ? Theme.accent.opacity(0.16) : .clear))
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            }
        }
        .padding(5)
        .frame(width: 340, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise2))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line2))
        .shadow(color: .black.opacity(0.35), radius: 12, y: 4)
    }

    func pickFiles() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = true
        panel.canChooseDirectories = false
        panel.canChooseFiles = true
        panel.message = "Attach files to your message"
        panel.prompt = "Attach"
        if let r = model.checkout.root, unkey(model.pane).machine == "local" { panel.directoryURL = URL(fileURLWithPath: r) }
        if panel.runModal() == .OK { model.attach(panel.urls) }
    }

    /// Chips under a sent message: what went with it.
    func sentChips(_ chips: [SentChip]) -> some View {
        ChoiceFlow(spacing: 5) {
            ForEach(Array(chips.enumerated()), id: \.offset) { _, c in
                HStack(spacing: 6) {
                    AttachmentThumb(name: c.name, image: c.image, file: c.file(cwd: store.pane(model.pane)?.cwd, local: unkey(model.pane).machine == "local"),
                                    data: c.data, side: 20)
                    Text(c.name).font(.ui(size: 11.5)).foregroundStyle(Theme.text2).lineLimit(1).truncationMode(.middle)
                }
                .padding(.leading, 3).padding(.trailing, 7).padding(.vertical, 3)
                .background(RoundedRectangle(cornerRadius: 6).fill(Theme.window.opacity(0.6)))
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line))
                .frame(maxWidth: 220)
            }
        }
    }

    func note(_ s: String) -> some View {
        VStack { Text(s).font(.ui(size: 12)).foregroundStyle(Theme.muted).padding(20); Spacer() }.frame(maxWidth: .infinity)
    }
}

/// An attachment under a sent message: by its path in the message, or
/// the attachment itself while the message is on its way.
struct SentChip {
    var name: String
    var image: Bool
    var path: String?
    var attachment: ChatAttachment?

    init(name: String, image: Bool, path: String? = nil, attachment: ChatAttachment? = nil) {
        self.name = name
        self.image = image
        self.path = path
        self.attachment = attachment
    }

    var data: Data? {
        if case .data(let d)? = attachment?.source { return d }
        return nil
    }

    /// The file on this Mac a thumbnail is drawn from, where there is one.
    func file(cwd: String?, local: Bool) -> String? {
        if case .file(let p)? = attachment?.source { return p }
        guard local, image, let path else { return nil }
        if path.hasPrefix("/") { return path }
        guard let cwd else { return nil }
        return (cwd as NSString).appendingPathComponent(path)
    }
}

/// A picture's thumbnail, or a file's mark, in a square.
struct AttachmentThumb: View {
    @Environment(\.conchTheme) private var look
    let name: String
    let image: Bool
    var file: String?
    var data: Data?
    var side: CGFloat = 28

    var body: some View {
        let _ = look
        let img: NSImage? = image ? (data.flatMap { NSImage(data: $0) } ?? file.flatMap { NSImage(contentsOfFile: $0) }) : nil
        Group {
            if let img {
                Image(nsImage: img).resizable().aspectRatio(contentMode: .fill)
            } else {
                FileMark(name: name)
            }
        }
        .frame(width: side, height: side)
        .background(RoundedRectangle(cornerRadius: 5).fill(Theme.raise2))
        .clipShape(RoundedRectangle(cornerRadius: 5))
    }
}

/// An attachment on the message being written: its thumbnail or mark,
/// name, where it came from and size — amber when it will not be sent —
/// and ✕.
struct AttachmentChip: View {
    @Environment(\.conchTheme) private var look
    let attachment: ChatAttachment
    let problem: String?
    let root: String?
    let local: Bool
    let onRemove: () -> Void

    var body: some View {
        let _ = look
        let a = attachment
        HStack(spacing: 7) {
            AttachmentThumb(name: a.name, image: a.image, file: file, data: data)
            VStack(alignment: .leading, spacing: 1) {
                Text(a.name).font(.ui(size: 12)).foregroundStyle(Theme.text).lineLimit(1).truncationMode(.middle)
                Text(problem.map { "\(fileSize(a.size)) · \($0)" } ?? (a.size >= 0 ? "\(a.origin) · \(fileSize(a.size))" : a.origin))
                    .font(.ui(size: 10.5)).foregroundStyle(problem == nil ? Theme.muted : Theme.warn).lineLimit(1)
            }
            Button(action: onRemove) {
                Image(systemName: "xmark").font(.system(size: 9, weight: .bold)).foregroundStyle(Theme.muted)
            }
            .buttonStyle(.plain).help("Take it off the message")
        }
        .padding(.leading, 4).padding(.trailing, 8).padding(.vertical, 4)
        .frame(maxWidth: 240, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 7).fill(Theme.window.opacity(0.7)))
        .overlay(RoundedRectangle(cornerRadius: 7).stroke(problem == nil ? Theme.line : Theme.warn.opacity(0.6)))
        .help(problem.map { "\(a.name) is \($0)" } ?? a.name)
    }

    var data: Data? {
        if case .data(let d) = attachment.source { return d }
        return nil
    }

    var file: String? {
        switch attachment.source {
        case .file(let p): return p
        case .checkout(let rel): return local ? root.map { ($0 as NSString).appendingPathComponent(rel) } : nil
        case .data: return nil
        }
    }
}

/// What is being dragged over the composer, while it is.
struct DropState: Equatable {
    var count: Int?
    var images = 0
}

/// Files dropped on the composer: from Finder by their URLs, pictures
/// from elsewhere (a browser, a screenshot thumbnail) as PNGs.
struct AttachDrop: DropDelegate {
    @Binding var state: DropState
    let model: ChatModel

    func validateDrop(info: DropInfo) -> Bool { info.hasItemsConforming(to: [.fileURL, .image]) }

    func dropEntered(info: DropInfo) {
        let files = info.itemProviders(for: [.fileURL, .image])
        let images = files.filter { $0.hasItemConformingToTypeIdentifier(UTType.image.identifier) }.count
        state = DropState(count: files.count, images: images)
    }

    func dropExited(info: DropInfo) { state = DropState() }

    func performDrop(info: DropInfo) -> Bool {
        state = DropState()
        let providers = info.itemProviders(for: [.fileURL, .image])
        for p in providers {
            if p.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier) {
                _ = p.loadObject(ofClass: URL.self) { url, _ in
                    guard let url else { return }
                    Task { @MainActor in model.attach([url]) }
                }
            } else if p.canLoadObject(ofClass: NSImage.self) {
                _ = p.loadObject(ofClass: NSImage.self) { img, _ in
                    guard let png = (img as? NSImage).flatMap(pngData) else { return }
                    Task { @MainActor in model.attachImage(png) }
                }
            }
        }
        return !providers.isEmpty
    }
}

/// A picture as PNG.
func pngData(_ img: NSImage) -> Data? {
    guard let tiff = img.tiffRepresentation, let rep = NSBitmapImageRep(data: tiff) else { return nil }
    return rep.representation(using: .png, properties: [:])
}

/// Whether a chat's end is on screen; changed only when it changes.
final class FollowState: ObservableObject {
    @Published private(set) var atBottom = true
    func set(_ v: Bool) { if atBottom != v { atBottom = v } }
}

/// "↓ Latest", over a chat scrolled back from its end.
struct LatestButton: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var follow: FollowState
    let action: () -> Void

    var body: some View {
        let _ = look
        if !follow.atBottom {
            Button(action: action) {
                HStack(spacing: 5) { Image(systemName: "arrow.down"); Text("Latest") }
                    .font(.ui(size: 11.5, weight: .semibold)).foregroundStyle(Theme.text)
                    .padding(.horizontal, 10).padding(.vertical, 5)
                    .background(Capsule().fill(Theme.raise2)).overlay(Capsule().stroke(Theme.line2))
                    .shadow(color: .black.opacity(0.2), radius: 4, y: 2)
            }
            .buttonStyle(.plain).padding(.bottom, 8)
        }
    }
}

/// The line under a conversation while its agent works: how long, and what
/// it says it is doing.
struct WorkingRow: View {
    @Environment(\.conchTheme) private var look
    let since: Date
    let message: String?
    let agent: String?

    var body: some View {
        let _ = look
        TimelineView(.periodic(from: spinnerEpoch, by: spinnerInterval)) { ctx in
            HStack(spacing: 8) {
                Text(spinnerFrame(at: ctx.date)).font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.work)
                Text("\(agentDisplayName(agent ?? "agent")) is working · \(elapsed(since, ctx.date))")
                    .font(.ui(size: 12, weight: .medium)).foregroundStyle(Theme.text2)
                if let m = message, !m.isEmpty {
                    Text(m).font(.ui(size: 12)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.tail)
                }
            }
        }
        .padding(.leading, 26)
    }
}

/// A tool the agent used: what it was, on what, and whether it worked;
/// opening to what it was given (an edit as its lines out and in) and what
/// came back.
struct ToolCard: View {
    @Environment(\.conchTheme) private var look
    let turn: ChatTurn
    let live: Bool
    @Binding var open: Bool

    var body: some View {
        let _ = look
        let squared = Theme.style == .jetbrains
        let has = !(turn.input ?? "").isEmpty || !(turn.output ?? "").isEmpty
        VStack(alignment: .leading, spacing: 0) {
            Button { if has { open.toggle() } } label: {
                HStack(spacing: 7) {
                    Image(systemName: has ? (open ? "chevron.down" : "chevron.right") : "circle.fill")
                        .font(.system(size: has ? 9 : 4, weight: .semibold)).frame(width: 10).foregroundStyle(Theme.muted)
                    status
                    Image(systemName: toolSymbol(turn.text ?? "")).font(.system(size: 11)).foregroundStyle(Theme.muted)
                    Text(toolLabel(turn.text ?? "")).font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.text)
                    Text(turn.detail ?? "").font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.text2)
                        .lineLimit(1).truncationMode(.middle)
                    Spacer(minLength: 0)
                    if let n = diffCounts { Text(n).font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.muted) }
                }
                .padding(.horizontal, 9).padding(.vertical, 5)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            if open && has {
                Rectangle().fill(Theme.line).frame(height: 1)
                VStack(alignment: .leading, spacing: 8) {
                    if let i = turn.input, !i.isEmpty { section(i, kind: turn.inputKind ?? "") }
                    if let o = turn.output, !o.isEmpty {
                        Text(turn.error == true ? "ERROR" : "OUTPUT").font(.ui(size: 9.5, weight: .bold)).tracking(0.6)
                            .foregroundStyle(turn.error == true ? Theme.err : Theme.muted)
                        section(o, kind: "output")
                    }
                }
                .padding(9)
            }
        }
        .background(RoundedRectangle(cornerRadius: squared ? 4 : 8).fill(Theme.raise.opacity(0.6)))
        .overlay(RoundedRectangle(cornerRadius: squared ? 4 : 8).stroke(turn.error == true ? Theme.err.opacity(0.45) : Theme.line))
    }

    @ViewBuilder
    var status: some View {
        if turn.error == true {
            Image(systemName: "xmark.circle.fill").font(.system(size: 10)).foregroundStyle(Theme.err)
        } else if turn.done == true {
            Image(systemName: "checkmark.circle.fill").font(.system(size: 10)).foregroundStyle(Theme.ok)
        } else if turn.done == nil {
            EmptyView() // a server from before results
        } else if live {
            ProgressView().controlSize(.mini).frame(width: 10, height: 10)
        } else {
            Image(systemName: "circle.dashed").font(.system(size: 10)).foregroundStyle(Theme.muted)
        }
    }

    /// An edit's lines out and in, as "−3 +5".
    var diffCounts: String? {
        guard turn.inputKind == "diff", let i = turn.input else { return nil }
        let ls = i.components(separatedBy: "\n")
        let minus = ls.filter { $0.hasPrefix("- ") || $0 == "-" }.count, plus = ls.filter { $0.hasPrefix("+ ") || $0 == "+" }.count
        return "−\(minus) +\(plus)"
    }

    func section(_ s: String, kind: String) -> some View {
        ScrollView([.vertical, .horizontal]) {
            if kind == "diff" {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(s.components(separatedBy: "\n").enumerated()), id: \.offset) { _, l in
                        let c: Color? = l.hasPrefix("+") ? Theme.ok : l.hasPrefix("-") ? Theme.err : nil
                        Text(l.isEmpty ? " " : l).font(.ui(size: 11.5, design: .monospaced))
                            .foregroundStyle(c ?? Theme.text2)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background((c ?? .clear).opacity(0.10))
                    }
                }
                .textSelection(.enabled)
            } else {
                Text(s).font(kind == "" ? .ui(size: 12) : .ui(size: 11.5, design: .monospaced))
                    .foregroundStyle(kind == "output" && turn.error == true ? Theme.err : Theme.text2)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .frame(maxHeight: 260)
        .fixedSize(horizontal: false, vertical: true)
        .padding(8)
        .background(RoundedRectangle(cornerRadius: 5).fill(Theme.window))
    }
}

/// An answer's markdown laid out as blocks: headings, lists, code with a
/// copy button, tables, quotes; inline bold, code and links within each.
struct MarkdownView: View {
    @Environment(\.conchTheme) private var look
    let text: String

    var body: some View {
        let _ = look
        VStack(alignment: .leading, spacing: 8) {
            ForEach(Array(markdownBlocks(text).enumerated()), id: \.offset) { _, b in block(b) }
        }
        .textSelection(.enabled)
    }

    func inline(_ s: String) -> AttributedString {
        (try? AttributedString(markdown: s, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))) ?? AttributedString(s)
    }

    @ViewBuilder
    func block(_ b: MDBlock) -> some View {
        switch b {
        case .heading(let level, let s):
            Text(inline(s)).font(.ui(size: level <= 1 ? 17 : level == 2 ? 15 : 13.5, weight: .semibold))
                .foregroundStyle(Theme.text).padding(.top, 4)
        case .paragraph(let s):
            Text(inline(s)).font(.ui(size: 13)).foregroundStyle(Theme.text).fixedSize(horizontal: false, vertical: true)
        case .list(let items):
            VStack(alignment: .leading, spacing: 3) {
                ForEach(Array(items.enumerated()), id: \.offset) { _, it in
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text(it.marker).font(.ui(size: 13)).foregroundStyle(Theme.muted)
                            .frame(minWidth: 14, alignment: .trailing)
                        Text(inline(it.text)).font(.ui(size: 13)).foregroundStyle(Theme.text).fixedSize(horizontal: false, vertical: true)
                    }
                    .padding(.leading, CGFloat(it.depth) * 16)
                }
            }
        case .code(let lang, let s):
            CodeBlock(language: lang, code: s)
        case .quote(let s):
            HStack(alignment: .top, spacing: 8) {
                Rectangle().fill(Theme.line2).frame(width: 3)
                Text(inline(s)).font(.ui(size: 13)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
            }
        case .table(let head, let rows):
            ScrollView(.horizontal) {
                Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 5) {
                    GridRow {
                        ForEach(Array(head.enumerated()), id: \.offset) { _, h in
                            Text(inline(h)).font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.text)
                        }
                    }
                    Divider().gridCellUnsizedAxes(.horizontal)
                    ForEach(Array(rows.enumerated()), id: \.offset) { _, r in
                        GridRow {
                            ForEach(Array(r.enumerated()), id: \.offset) { _, c in
                                Text(inline(c)).font(.ui(size: 12)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
                            }
                        }
                    }
                }
                .padding(10)
            }
            .background(RoundedRectangle(cornerRadius: 6).fill(Theme.raise.opacity(0.5)))
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line))
        case .rule:
            Rectangle().fill(Theme.line).frame(height: 1).padding(.vertical, 4)
        }
    }
}

/// Code in an answer: monospace on its own ground, scrolling sideways
/// rather than wrapping, with its language and a copy button.
struct CodeBlock: View {
    @Environment(\.conchTheme) private var look
    let language: String
    let code: String
    @State private var copied = false

    var body: some View {
        let _ = look
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text(language.isEmpty ? "code" : language).font(.ui(size: 10.5, design: .monospaced)).foregroundStyle(Theme.muted)
                Spacer()
                Button {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(code, forType: .string)
                    copied = true
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { copied = false }
                } label: {
                    Label(copied ? "Copied" : "Copy", systemImage: copied ? "checkmark" : "doc.on.doc").font(.ui(size: 10.5))
                }
                .buttonStyle(.plain).foregroundStyle(Theme.text2)
            }
            .padding(.horizontal, 10).padding(.vertical, 4)
            Rectangle().fill(Theme.line).frame(height: 1)
            ScrollView(.horizontal) {
                Text(code).font(.ui(size: 12, design: .monospaced)).foregroundStyle(Theme.text)
                    .textSelection(.enabled).padding(10)
            }
        }
        .background(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 4 : 7).fill(Theme.well))
        .overlay(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 4 : 7).stroke(Theme.line))
    }
}

/// A question's choices, wrapping onto more lines in a narrow chat.
struct FlowChoices: View {
    @ObservedObject var store: Store
    let pane: String
    let question: Question

    var body: some View {
        ChoiceFlow(spacing: 6) {
            ForEach(Array(question.choices.prefix(6).enumerated()), id: \.offset) { i, c in
                Button { store.answer(pane, question, c) } label: {
                    HStack(spacing: 6) {
                        Text(c.label).lineLimit(2).multilineTextAlignment(.leading)
                        Text("\(i + 1)").font(.ui(size: 10.5)).opacity(0.6)
                    }
                    .font(.ui(size: 12, weight: c.default == true ? .semibold : .regular))
                    .foregroundStyle(c.default == true ? Theme.selectedText : Theme.text)
                    .padding(.horizontal, 10).padding(.vertical, 5)
                    .background(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 4 : 7).fill(c.default == true ? Theme.accentDeep : Theme.raise))
                    .overlay(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 4 : 7).stroke(c.default == true ? .clear : Theme.line2))
                }
                .buttonStyle(.plain)
            }
        }
    }
}

/// Lays its children left to right, onto the next line when one runs out.
struct ChoiceFlow: SwiftUI.Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? 400
        var x: CGFloat = 0, y: CGFloat = 0, row: CGFloat = 0, widest: CGFloat = 0
        for s in subviews {
            let z = s.sizeThatFits(ProposedViewSize(width: width, height: nil))
            if x > 0 && x + z.width > width { x = 0; y += row + spacing; row = 0 }
            x += z.width + spacing
            row = max(row, z.height)
            widest = max(widest, x - spacing)
        }
        return CGSize(width: min(widest, width), height: y + row)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, row: CGFloat = 0
        for s in subviews {
            let z = s.sizeThatFits(ProposedViewSize(width: bounds.width, height: nil))
            if x > bounds.minX && x + z.width > bounds.maxX { x = bounds.minX; y += row + spacing; row = 0 }
            s.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(z))
            x += z.width + spacing
            row = max(row, z.height)
        }
    }
}

/// Chat | Terminal: which of the agent's two faces a workbench shows where
/// it has room for one.
struct ChatTerminalSwitch: View {
    @ObservedObject var store: Store
    let chat: Bool

    var body: some View {
        HStack(spacing: 0) {
            seg("Chat", on: chat)
            seg("Terminal", on: !chat)
        }
        .padding(2)
        .background(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 4 : 6).fill(Theme.raise))
        .help("The agent as a chat, or its own terminal — the same conversation")
    }

    func seg(_ s: String, on: Bool) -> some View {
        Button { if !on { store.swapBenchChat() } } label: {
            Text(s).font(.ui(size: 11, weight: on ? .semibold : .regular))
                .foregroundStyle(on ? Theme.text : Theme.text2)
                .padding(.horizontal, 8).padding(.vertical, 2)
                .background(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 3 : 5).fill(on ? Theme.window : .clear))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Show the agent as \(s)")
    }
}

/// A text field of several lines that sends on return and takes a new line
/// on shift-return, as a chat's box does. Pasted files and pictures go on
/// the message as attachments rather than into the text, and while the @
/// picker is open the arrows, return and escape are its.
struct ChatInput: NSViewRepresentable {
    /// Read so its text takes a new theme's colour.
    @Environment(\.conchTheme) private var look
    @Binding var text: String
    var placeholder: String
    var onSend: () -> Void
    /// Escape: true when it was taken (the agent stopped), so the key does
    /// nothing else.
    var onEscape: () -> Bool = { false }
    /// The text and the cursor (UTF-16), after either moves.
    var onCursor: (String, Int) -> Void = { _, _ in }
    /// A key for the @ picker: true when it took it.
    var onPicker: (ChatModel.PickerKey) -> Bool = { _ in false }
    var onPasteFiles: ([URL]) -> Void = { _ in }
    var onPasteImage: (Data) -> Void = { _ in }
    /// The text view, once made: for the @ button to put the keyboard in it.
    var onMake: (NSTextView) -> Void = { _ in }

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSScrollView()
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        let tv = AttachingTextView(frame: .zero)
        tv.minSize = NSSize(width: 0, height: 0)
        tv.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        tv.isVerticallyResizable = true
        tv.isHorizontallyResizable = false
        tv.autoresizingMask = [.width]
        tv.textContainer?.widthTracksTextView = true
        tv.textContainer?.containerSize = NSSize(width: 0, height: CGFloat.greatestFiniteMagnitude)
        scroll.documentView = tv
        tv.delegate = context.coordinator
        tv.coordinator = context.coordinator
        tv.drawsBackground = false
        tv.isRichText = false
        tv.allowsUndo = true
        tv.isAutomaticQuoteSubstitutionEnabled = false
        tv.isAutomaticDashSubstitutionEnabled = false
        tv.font = NSFont.systemFont(ofSize: 13)
        tv.textColor = Theme.fg
        tv.insertionPointColor = Theme.fg
        tv.textContainerInset = NSSize(width: 0, height: 3)
        tv.string = text
        // A drop is the composer's, to attach the files, not the text's,
        // which would type their paths in.
        tv.unregisterDraggedTypes()
        context.coordinator.placeholder(tv)
        onMake(tv)
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        context.coordinator.parent = self
        guard let tv = scroll.documentView as? NSTextView else { return }
        _ = look
        if tv.string != text { tv.string = text }
        tv.textColor = Theme.fg
        tv.insertionPointColor = Theme.fg
        context.coordinator.placeholder(tv)
    }

    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: ChatInput
        init(_ p: ChatInput) { parent = p }

        func textDidChange(_ n: Notification) {
            guard let tv = n.object as? NSTextView else { return }
            parent.text = tv.string
            placeholder(tv)
            parent.onCursor(tv.string, tv.selectedRange().location)
        }

        func textViewDidChangeSelection(_ n: Notification) {
            guard let tv = n.object as? NSTextView else { return }
            // After the update that may have moved it (the text replaced
            // from the model), not during it.
            let (text, at, report) = (tv.string, tv.selectedRange().location, parent.onCursor)
            DispatchQueue.main.async { report(text, at) }
        }

        func textView(_ tv: NSTextView, doCommandBy sel: Selector) -> Bool {
            if !tv.hasMarkedText() {
                switch sel {
                case #selector(NSResponder.moveUp(_:)): if parent.onPicker(.up) { return true }
                case #selector(NSResponder.moveDown(_:)): if parent.onPicker(.down) { return true }
                case #selector(NSResponder.insertTab(_:)): if parent.onPicker(.choose) { return true }
                case #selector(NSResponder.insertNewline(_:)):
                    if !NSEvent.modifierFlags.contains(.shift), parent.onPicker(.choose) { return true }
                default: break
                }
            }
            if sel == #selector(NSResponder.insertNewline(_:)) {
                // Return mid-composition (an input method) is the method's.
                if tv.hasMarkedText() { return false }
                if NSEvent.modifierFlags.contains(.shift) || NSEvent.modifierFlags.contains(.option) {
                    tv.insertNewlineIgnoringFieldEditor(nil)
                } else {
                    parent.onSend()
                }
                return true
            }
            if sel == #selector(NSResponder.cancelOperation(_:)), !tv.hasMarkedText() {
                return parent.onEscape()
            }
            return false
        }

        /// A paste: files as attachments, a picture as one, text as text.
        func paste(from pb: NSPasteboard) -> Bool {
            let urls = (pb.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL]) ?? []
            if !urls.isEmpty {
                parent.onPasteFiles(urls)
                return true
            }
            if pb.availableType(from: [.png, .tiff]) != nil, let img = NSImage(pasteboard: pb), let png = pngData(img) {
                parent.onPasteImage(png)
                return true
            }
            return false
        }

        func placeholder(_ tv: NSTextView) {
            let p = tv.string.isEmpty ? parent.placeholder : ""
            if tv.placeholderString != p { tv.placeholderString = p }
        }
    }
}

/// The composer's text view: a paste of files or a picture attaches them.
final class AttachingTextView: NSTextView {
    weak var coordinator: ChatInput.Coordinator?

    override func paste(_ sender: Any?) {
        if coordinator?.paste(from: .general) == true { return }
        pasteAsPlainText(sender)
    }
}

extension NSTextView {
    /// NSTextView draws a placeholder through this key; it has no API.
    var placeholderString: String {
        get { (value(forKey: "placeholderString") as? String) ?? "" }
        set { setValue(newValue, forKey: "placeholderString") }
    }
}
