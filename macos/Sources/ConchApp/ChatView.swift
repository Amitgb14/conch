import AppKit
import ConchKit
import SwiftUI

/// An agent's conversation, read from what it saves (agent.chat), kept up
/// to date while it is on screen: asked for every second while the agent
/// works and every few while it rests, one request at a time, and sent only
/// what changed. The terminal is still the agent: a message sent here is
/// typed into it (agent.prompt), Stop is its escape key, and a question is
/// answered with its keys.
@MainActor
final class ChatModel: ObservableObject {
    let key: String
    let pane: String
    unowned let store: Store
    @Published private(set) var log = ChatLog()
    @Published var error = ""
    @Published var draft = ""
    /// Messages sent and not yet in the conversation: shown at once, as a
    /// chat does, and dropped when the agent's transcript has them.
    @Published private(set) var pending: [PendingMessage] = []
    private var inFlight = false
    private var timer: Timer?
    private var running = false

    struct PendingMessage: Identifiable, Equatable {
        let id = UUID()
        var text: String
        var after: Int
        var failed: String?
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

    func stop() {
        running = false
        timer?.invalidate()
        timer = nil
    }

    private func schedule() {
        timer?.invalidate()
        guard running else { return }
        let state = store.pane(pane)?.agent?.state
        let busy = chatBusy(state: state, pending: pending.count, turns: log.turns)
        timer = Timer.scheduledTimer(withTimeInterval: busy ? 1 : 4, repeats: false) { [weak self] _ in
            Task { @MainActor in
                self?.load()
                self?.schedule()
            }
        }
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
            } catch {
                self.error = "\(error)"
            }
        }
    }

    /// The draft typed into the agent and submitted, as if at its prompt.
    func send() {
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        let (m, id) = unkey(pane)
        guard let c = store.connection(m) else { return }
        guard c.hello?.capabilities?.contains("agent.prompt.v1") == true else {
            error = "The server on \(store.label(m)) predates prompting an agent; reload it"
            return
        }
        let msg = PendingMessage(text: text, after: log.turns.count)
        pending.append(msg)
        draft = ""
        struct P: Encodable { var id: String; var text: String }
        Task {
            do {
                try await c.call("agent.prompt", P(id: id, text: text))
                schedule()
            } catch {
                if let i = pending.firstIndex(where: { $0.id == msg.id }) { pending[i].failed = "\(error)" }
            }
        }
    }

    func retry(_ m: PendingMessage) {
        pending.removeAll { $0.id == m.id }
        draft = m.text
        send()
    }

    func discard(_ m: PendingMessage) { pending.removeAll { $0.id == m.id } }

    /// Stops what the agent is doing: its escape key, as in its terminal.
    func interrupt() {
        let (m, id) = unkey(pane)
        guard let c = store.connection(m) else { return }
        struct P: Encodable { var id: String; var keys: [String] }
        Task { try? await c.call("pane.send_keys", P(id: id, keys: ["escape"])) }
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
        .onDisappear { model.stop() }
        .onChange(of: p?.agent?.state) { _ in model.load() }
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
        let blocks = chatBlocks(model.log.turns)
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
            HStack {
                Spacer(minLength: 40)
                Text(s).font(.ui(size: 13)).foregroundStyle(Theme.text).textSelection(.enabled)
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
                Text(m.text).font(.ui(size: 13)).foregroundStyle(Theme.text).textSelection(.enabled)
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
        let empty = model.draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        let busy = chatBusy(state: p?.agent?.state, pending: model.pending.count, turns: model.log.turns)
        return VStack(alignment: .leading, spacing: 6) {
            ChatInput(text: $model.draft, placeholder: "Reply to \(p?.agent.map { agentDisplayName($0.name) } ?? "the agent")…",
                      onSend: { model.send() },
                      onEscape: {
                          guard busy else { return false }
                          model.interrupt()
                          return true
                      })
                .frame(height: height)
            HStack(spacing: 8) {
                Text(busy ? "Working · what you send is queued · esc stops" : "⏎ send · ⇧⏎ new line")
                    .font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(1)
                Spacer()
                if busy {
                    // As other agent chats: Stop where Send is while it works,
                    // and Send beside it once there is something to queue.
                    Button { model.interrupt() } label: {
                        Image(systemName: "stop.fill").font(.system(size: 10, weight: .bold))
                            .foregroundStyle(Theme.selectedText).frame(width: 26, height: 24)
                            .background(RoundedRectangle(cornerRadius: squared ? 4 : 7).fill(Theme.err))
                    }
                    .buttonStyle(.plain)
                    .help("Stop what the agent is doing (its escape key)")
                }
                if !busy || !empty {
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
        .padding([.horizontal, .bottom], 10)
    }

    func note(_ s: String) -> some View {
        VStack { Text(s).font(.ui(size: 12)).foregroundStyle(Theme.muted).padding(20); Spacer() }.frame(maxWidth: .infinity)
    }
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
        TimelineView(.periodic(from: .now, by: spinnerInterval)) { ctx in
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
/// on shift-return, as a chat's box does.
struct ChatInput: NSViewRepresentable {
    /// Read so its text takes a new theme's colour.
    @Environment(\.conchTheme) private var look
    @Binding var text: String
    var placeholder: String
    var onSend: () -> Void
    /// Escape: true when it was taken (the agent stopped), so the key does
    /// nothing else.
    var onEscape: () -> Bool = { false }

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSTextView.scrollableTextView()
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        let tv = scroll.documentView as! NSTextView
        tv.delegate = context.coordinator
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
        context.coordinator.placeholder(tv)
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
        }

        func textView(_ tv: NSTextView, doCommandBy sel: Selector) -> Bool {
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

        func placeholder(_ tv: NSTextView) {
            let p = tv.string.isEmpty ? parent.placeholder : ""
            if tv.placeholderString != p { tv.placeholderString = p }
        }
    }
}

extension NSTextView {
    /// NSTextView draws a placeholder through this key; it has no API.
    var placeholderString: String {
        get { (value(forKey: "placeholderString") as? String) ?? "" }
        set { setValue(newValue, forKey: "placeholderString") }
    }
}
