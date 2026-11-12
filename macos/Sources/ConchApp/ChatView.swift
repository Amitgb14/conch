import AppKit
import ConchKit
import SwiftUI

/// An agent's conversation, read from what it saves (agent.chat): kept up to
/// date while it is on screen — every two seconds while the agent works,
/// less often while it waits or rests — and drawn as a chat beside its
/// terminal. The terminal is still the agent: a message sent here is typed
/// into it (agent.prompt), and a question is answered with its keys.
@MainActor
final class ChatModel: ObservableObject {
    let key: String
    let pane: String
    unowned let store: Store
    @Published var chat: AgentChat?
    @Published var error = ""
    @Published var draft = ""
    @Published var sending = false
    private var reads = Latest()
    private var timer: Timer?

    init(key: String, store: Store) {
        self.key = key
        self.pane = parseChatKey(key) ?? key
        self.store = store
    }

    var blocks: [ChatBlock] { chatBlocks(chat?.turns ?? []) }

    /// Starts reading, and keeps reading while shown.
    func start() {
        load()
        schedule()
    }

    func stop() {
        timer?.invalidate()
        timer = nil
    }

    private func schedule() {
        timer?.invalidate()
        let busy = store.pane(pane)?.agent?.state == .working
        timer = Timer.scheduledTimer(withTimeInterval: busy ? 2 : 6, repeats: false) { [weak self] _ in
            Task { @MainActor in
                self?.load()
                self?.schedule()
            }
        }
    }

    func load() {
        let (m, id) = unkey(pane)
        guard let c = store.connection(m) else {
            error = "Not connected to \(store.label(m))"
            return
        }
        guard c.hello?.capabilities?.contains("agent.chat.v1") == true else {
            error = "The server on \(store.label(m)) is from before chats; reload it (right-click \(store.label(m)) → Reload Server) to see this agent's conversation here."
            return
        }
        struct P: Encodable { var id: String }
        let n = reads.next()
        Task {
            do {
                let got = try await c.call("agent.chat", P(id: id), as: AgentChat.self)
                guard reads.isLatest(n) else { return }
                if got != chat { chat = got }
                error = ""
            } catch {
                guard reads.isLatest(n) else { return }
                self.error = "\(error)"
            }
        }
    }

    /// The draft typed into the agent and submitted, as if at its prompt.
    func send() {
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !sending else { return }
        let (m, id) = unkey(pane)
        guard let c = store.connection(m) else { return }
        guard c.hello?.capabilities?.contains("agent.prompt.v1") == true else {
            error = "The server on \(store.label(m)) predates prompting an agent; reload it"
            return
        }
        struct P: Encodable { var id: String; var text: String }
        sending = true
        Task {
            do {
                try await c.call("agent.prompt", P(id: id, text: text))
                draft = ""
                // What was sent is in its transcript a moment later.
                try? await Task.sleep(nanoseconds: 600_000_000)
                load()
            } catch {
                self.error = "\(error)"
            }
            sending = false
        }
    }
}

struct ChatView: View {
    @ObservedObject var store: Store
    @ObservedObject var model: ChatModel
    @State private var open: Set<Int> = []

    var body: some View {
        let p = store.pane(model.pane)
        let focused = store.layout.focused == model.key
        VStack(spacing: 0) {
            header(p)
            Rectangle().fill(Theme.line).frame(height: 1)
            if p == nil {
                note("This agent has ended. Its conversation is saved: open it from Sessions to resume.")
            } else {
                messages
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

    var messages: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 12) {
                    if !model.error.isEmpty {
                        Text(model.error).font(.ui(size: 12)).foregroundStyle(Theme.err).textSelection(.enabled)
                    }
                    let blocks = model.blocks
                    if blocks.isEmpty && model.error.isEmpty {
                        Text(model.chat == nil ? "Reading the conversation…"
                             : model.chat?.note ?? "Nothing said yet. Write below, and what the agent does shows here as it works.")
                            .font(.ui(size: 12)).foregroundStyle(Theme.muted).frame(maxWidth: .infinity).padding(.top, 30)
                    }
                    ForEach(blocks) { b in block(b).id(b.id) }
                    Color.clear.frame(height: 1).id("end")
                }
                .padding(14)
            }
            .onChange(of: model.chat?.turns?.count) { _ in withAnimation(.easeOut(duration: 0.15)) { proxy.scrollTo("end", anchor: .bottom) } }
            .onAppear { proxy.scrollTo("end", anchor: .bottom) }
        }
    }

    @ViewBuilder
    func block(_ b: ChatBlock) -> some View {
        switch b {
        case .user(let s, _):
            HStack {
                Spacer(minLength: 40)
                Text(s).font(.ui(size: 13)).foregroundStyle(Theme.text).textSelection(.enabled)
                    .padding(.horizontal, 11).padding(.vertical, 8)
                    .background(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 6 : 12).fill(Theme.raise))
                    .overlay(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 6 : 12).stroke(Theme.line))
            }
        case .agent(let s, _):
            HStack(alignment: .top, spacing: 9) {
                if let a = store.pane(model.pane)?.agent { Mark(agent: a.name) }
                Text(markdown(s)).font(.ui(size: 13)).foregroundStyle(Theme.text).textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        case .tools(let run, let at):
            toolRun(run, at: at)
        }
    }

    func toolRun(_ run: [ChatTurn], at: Int) -> some View {
        let shown = open.contains(at)
        return VStack(alignment: .leading, spacing: 0) {
            Button {
                if shown { open.remove(at) } else { open.insert(at) }
            } label: {
                HStack(spacing: 7) {
                    Image(systemName: shown ? "chevron.down" : "chevron.right").font(.system(size: 9, weight: .semibold)).frame(width: 10)
                    if run.count == 1, let t = run.first {
                        Image(systemName: toolSymbol(t.text ?? "")).font(.system(size: 11))
                        Text(toolLabel(t.text ?? "")).font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.text)
                        Text(t.detail ?? "").font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.text2)
                            .lineLimit(1).truncationMode(.middle)
                    } else {
                        Image(systemName: "wrench.and.screwdriver").font(.system(size: 11))
                        Text(toolsSummary(run)).font(.ui(size: 12)).foregroundStyle(Theme.text2).lineLimit(1)
                    }
                    Spacer(minLength: 0)
                }
                .foregroundStyle(Theme.muted)
                .padding(.horizontal, 9).padding(.vertical, 5)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            if shown {
                Rectangle().fill(Theme.line).frame(height: 1)
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(Array(run.enumerated()), id: \.offset) { _, t in
                        HStack(alignment: .firstTextBaseline, spacing: 7) {
                            Image(systemName: toolSymbol(t.text ?? "")).font(.system(size: 10)).foregroundStyle(Theme.muted).frame(width: 14)
                            Text(toolLabel(t.text ?? "")).font(.ui(size: 11.5, weight: .semibold)).foregroundStyle(Theme.text)
                            Text(t.detail ?? "").font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.text2)
                                .textSelection(.enabled).lineLimit(3)
                        }
                    }
                }
                .padding(.horizontal, 10).padding(.vertical, 7)
            }
        }
        .background(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 5 : 8).fill(Theme.raise.opacity(0.6)))
        .overlay(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 5 : 8).stroke(Theme.line))
        .padding(.leading, 26)
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
        .background(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 6 : 10).fill(Theme.warn.opacity(0.09)))
        .overlay(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 6 : 10).stroke(Theme.warn.opacity(0.5)))
        .padding(.horizontal, 10).padding(.bottom, 6)
    }

    func composer(_ p: PaneInfo?) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            ChatInput(text: $model.draft, placeholder: "Reply to \(p?.agent.map { agentDisplayName($0.name) } ?? "the agent")…") { model.send() }
                .frame(height: 40)
            HStack(spacing: 8) {
                Text("⏎ send · ⇧⏎ new line").font(.ui(size: 11)).foregroundStyle(Theme.muted)
                Spacer()
                Button { model.send() } label: {
                    Image(systemName: model.sending ? "ellipsis" : "arrow.up").font(.system(size: 12, weight: .bold))
                        .foregroundStyle(Theme.selectedText).frame(width: 26, height: 24)
                        .background(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 4 : 7).fill(Theme.accentDeep))
                }
                .buttonStyle(.plain)
                .disabled(model.draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.sending)
                .help("Send to the agent, as if typed at its prompt")
            }
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 6 : 11).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: Theme.style == .jetbrains ? 6 : 11).stroke(Theme.line2))
        .padding([.horizontal, .bottom], 10)
    }

    func note(_ s: String) -> some View {
        VStack { Text(s).font(.ui(size: 12)).foregroundStyle(Theme.muted).padding(20); Spacer() }.frame(maxWidth: .infinity)
    }

    /// An answer's markdown — bold, code, links — kept on its lines.
    func markdown(_ s: String) -> AttributedString {
        (try? AttributedString(markdown: s, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))) ?? AttributedString(s)
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
                if NSEvent.modifierFlags.contains(.shift) || NSEvent.modifierFlags.contains(.option) {
                    tv.insertNewlineIgnoringFieldEditor(nil)
                } else {
                    parent.onSend()
                }
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

extension NSTextView {
    /// NSTextView draws a placeholder through this key; it has no API.
    var placeholderString: String {
        get { (value(forKey: "placeholderString") as? String) ?? "" }
        set { setValue(newValue, forKey: "placeholderString") }
    }
}
