import AppKit
import ConchKit
import SwiftUI

// Edit with an agent (⌘I): the file shown, or the lines selected in it,
// handed to an agent with what to do. The agent runs in a pane of its own
// in the checkout, as any agent conch starts — it shows in the sidebar and
// can be talked to there — and edits the file on disk. What it changed is
// then shown here as a diff against the file as it was handed over, to
// keep, or to undo: undoing writes the old text back, checked against
// what the agent left (fs.write), so nothing written since is lost.

/// One ask of an agent about a file.
struct AgentEdit: Identifiable {
    enum State { case working, review, kept, undone }
    let id = UUID()
    var path: String
    var lines: ClosedRange<Int>?
    var ask: String
    var agent: String
    /// The agent's pane (its store key), once it started.
    var pane: String?
    /// The file as it was handed over.
    var before: String
    var state = State.working
    var started = Date()
}

extension FilesModel {
    /// The ask still open for the file shown, if any.
    var openEdit: AgentEdit? {
        guard let s = selected else { return nil }
        return agentEdits.first { $0.path == s && ($0.state == .working || $0.state == .review) }
    }

    /// The diff to look at: the file shown, changed by the agent.
    var reviewing: AgentEdit? { openEdit.flatMap { $0.state == .review ? $0 : nil } }

    func showAgentPanel() {
        guard selected != nil, preview?.editable == true else {
            store.say("Open a text file to have an agent edit it", .err)
            return
        }
        agentPanel = true
    }

    /// Hands the file (or the lines selected) to an agent.
    func sendToAgent(agent: String, ask: String, wholeFile: Bool, also: [String]) {
        guard let path = selected, let c = store.connection(machine) else { return }
        guard !ask.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        if dirty {
            store.say("Save \((path as NSString).lastPathComponent) first (⌘S): the agent edits the file on disk", .err)
            return
        }
        if openEdit != nil {
            store.say("An agent is already at work on this file: keep or undo its change first", .err)
            return
        }
        let lines = wholeFile ? nil : selectedLines(text, utf16: selection)
        let prompt = agentEditPrompt(path: path, lines: lines, ask: ask, also: also)
        var e = AgentEdit(path: path, lines: lines, ask: ask, agent: agent, before: text)
        agentEdits.insert(e, at: 0)
        struct Create: Encodable {
            var name: String
            var agent: String
            var prompt: String
            var cwd: String
            var cols: Int?
            var rows: Int?
        }
        let size = store.newPaneSize()
        let name = "\(agent) · \((path as NSString).lastPathComponent)"
        Task {
            do {
                let p = try await c.call("pane.create", Create(name: name, agent: agent, prompt: prompt, cwd: root, cols: size?.cols, rows: size?.rows),
                                         as: PaneInfo.self)
                store.added(machine: machine, pane: p)
                e.pane = ConchKit.key(machine, p.id)
                if let i = agentEdits.firstIndex(where: { $0.id == e.id }) { agentEdits[i].pane = e.pane }
                store.say("\(agent) is on it — its change shows here when it writes the file")
            } catch {
                agentEdits.removeAll { $0.id == e.id }
                store.say("Could not start \(agent): \(error)", .err)
            }
        }
    }

    /// The file was read again: if an agent was asked about it and it is
    /// not what was handed over, there is a change to look at.
    func agentChanged(_ path: String) {
        guard let i = agentEdits.firstIndex(where: { $0.path == path && $0.state == .working }) else { return }
        if text != agentEdits[i].before { agentEdits[i].state = .review }
    }

    func keepAgentEdit() {
        guard let e = openEdit, let i = agentEdits.firstIndex(where: { $0.id == e.id }) else { return }
        agentEdits[i].state = .kept
    }

    /// Writes back the file as it was handed over, if the agent's version
    /// is still what is on disk.
    func undoAgentEdit() {
        guard let e = openEdit, let path = selected, let c = store.connection(machine), canSave else { return }
        struct Params: Encodable { var root: String; var path: String; var data: String; var base: String? }
        Task {
            do {
                _ = try await c.call("fs.write", Params(root: root, path: path, data: e.before, base: baseHash), as: FSWriteResult.self)
                if let i = agentEdits.firstIndex(where: { $0.id == e.id }) { agentEdits[i].state = .undone }
                read(path)
                store.say("Undid \(e.agent)'s change to \((path as NSString).lastPathComponent)")
            } catch let err as ConchError where err.code == "file_changed" {
                store.say("The file changed again since — read it again before undoing", .err)
                read(path)
            } catch {
                store.say("Could not undo: \(error)", .err)
            }
        }
    }

    /// Stops waiting on an ask that changed nothing (an explanation).
    func dismissAgentEdit() {
        guard let e = openEdit, let i = agentEdits.firstIndex(where: { $0.id == e.id }) else { return }
        agentEdits[i].state = .kept
    }
}

/// The side panel: which agent, what it sees, what to do.
struct AgentEditPanel: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: FilesModel
    @ObservedObject var store: Store
    @State private var agent = ""
    @State private var ask = ""
    @State private var wholeFile = false
    @State private var also = ""
    @FocusState private var focused: Bool

    var body: some View {
        let _ = look
        let agents = store.startable(on: model.machine).filter(\.installed)
        let names = agents.isEmpty ? ["claude"] : agents.map(\.name)
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Image(systemName: "sparkles").foregroundStyle(Theme.color(agentColor(agent) ?? Theme.current.line))
                Text("Edit with agent").font(.ui(size: 13, weight: .semibold))
                Spacer()
                Button { model.agentPanel = false } label: { Image(systemName: "xmark") }
                    .buttonStyle(.borderless).help("Close").accessibilityLabel("Close the panel")
            }
            .padding(.horizontal, 12).frame(height: 40)
            Rectangle().fill(Theme.line).frame(height: 1)
            VStack(alignment: .leading, spacing: 10) {
                label("AGENT")
                FlowRow(spacing: 6) {
                    ForEach(names, id: \.self) { n in
                        Button { agent = n } label: {
                            HStack(spacing: 6) {
                                Circle().fill(Theme.color(agentColor(n) ?? Theme.current.muted)).frame(width: 7, height: 7)
                                Text(n).font(.ui(size: 12, weight: agent == n ? .medium : .regular))
                            }
                            .padding(.horizontal, 10).frame(height: 26)
                            .background(agent == n ? Theme.raise : .clear, in: RoundedRectangle(cornerRadius: 6))
                            .overlay(RoundedRectangle(cornerRadius: 6).stroke(agent == n ? Theme.text2 : Theme.line))
                        }
                        .buttonStyle(.plain)
                    }
                }
                label("WHAT IT SEES")
                Picker("", selection: $wholeFile) {
                    Text(selectionLabel).tag(false)
                    Text("Whole file").tag(true)
                }
                .pickerStyle(.segmented).labelsHidden().controlSize(.small)
                TextField("Other files it may need (comma between)", text: $also)
                    .textFieldStyle(.roundedBorder).font(.ui(size: 11.5, design: .monospaced)).controlSize(.small)
                TextEditor(text: $ask)
                    .font(.ui(size: 12.5)).frame(height: 76).focused($focused)
                    .scrollContentBackground(.hidden).padding(4)
                    .background(Theme.window, in: RoundedRectangle(cornerRadius: 6))
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line))
                    .overlay(alignment: .topLeading) {
                        if ask.isEmpty {
                            Text("Ask \(agent) to change this…").font(.ui(size: 12.5)).foregroundStyle(Theme.muted)
                                .padding(.horizontal, 9).padding(.vertical, 4).allowsHitTesting(false)
                        }
                    }
                HStack {
                    Text("Its change shows here, to keep or undo").font(.ui(size: 11)).foregroundStyle(Theme.muted)
                    Spacer()
                    Button("Send") { send(ask) }
                        .buttonStyle(.borderedProminent).controlSize(.small)
                        .keyboardShortcut(.return, modifiers: .command)
                        .disabled(ask.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.openEdit != nil)
                }
                FlowRow(spacing: 6) {
                    ForEach(agentQuickAsks, id: \.label) { q in
                        Button(q.label) { send(q.ask) }.controlSize(.small).disabled(model.openEdit != nil)
                    }
                }
            }
            .padding(12)
            Rectangle().fill(Theme.line).frame(height: 1)
            label("THIS CHECKOUT'S ASKS").padding(.horizontal, 12).padding(.top, 10)
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(model.agentEdits) { e in historyRow(e) }
                    if model.agentEdits.isEmpty {
                        Text("Nothing asked yet.").font(.ui(size: 12)).foregroundStyle(Theme.muted).padding(12)
                    }
                }
            }
            Rectangle().fill(Theme.line).frame(height: 1)
            Text("The agent runs in a pane of its own in this checkout, as one started from the sidebar would: it shows there and can be talked to.")
                .font(.ui(size: 11)).foregroundStyle(Theme.muted).padding(12)
        }
        .background(Theme.paneHeader)
        .onAppear {
            if agent.isEmpty { agent = names.first ?? "claude" }
            focused = true
        }
    }

    var selectionLabel: String {
        let l = selectedLines(model.text, utf16: model.selection)
        return l.count == 1 ? "Line \(l.lowerBound)" : "Lines \(l.lowerBound)–\(l.upperBound)"
    }

    func send(_ text: String) {
        model.sendToAgent(agent: agent, ask: text, wholeFile: wholeFile, also: globList(also))
        if text == ask { ask = "" }
    }

    func label(_ s: String) -> some View {
        Text(s).font(.ui(size: 10.5, weight: .bold)).tracking(0.8).foregroundStyle(Theme.muted)
    }

    func historyRow(_ e: AgentEdit) -> some View {
        let (word, colour): (String, Color) = {
            switch e.state {
            case .working: return ("working", Theme.warn)
            case .review: return ("to review", Theme.color(Theme.current.work))
            case .kept: return ("kept", Theme.ok)
            case .undone: return ("undone", Theme.muted)
            }
        }()
        return VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 6) {
                Circle().fill(Theme.color(agentColor(e.agent) ?? Theme.current.muted)).frame(width: 6, height: 6)
                Text("\(e.agent) · \((e.path as NSString).lastPathComponent)").font(.ui(size: 11)).foregroundStyle(Theme.text2)
                Text(e.started, style: .relative).font(.ui(size: 11)).foregroundStyle(Theme.muted)
                Spacer()
                Text(word).font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(colour)
            }
            Text(e.ask).font(.ui(size: 12)).foregroundStyle(Theme.text).lineLimit(2)
            if let p = e.pane, e.state == .working || e.state == .review {
                Button("Open \(e.agent)'s pane") { store.open(p) }.buttonStyle(.plain).font(.ui(size: 11)).foregroundStyle(Theme.accent)
            }
        }
        .padding(.horizontal, 12).padding(.vertical, 8)
        .contentShape(Rectangle())
        .onTapGesture { if e.path != model.selected { model.openFile(e.path) } }
    }
}

/// What the agent changed in the file, to keep or undo.
struct AgentReviewView: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: FilesModel
    @ObservedObject var store: Store
    let edit: AgentEdit

    var body: some View {
        let _ = look
        let diff = lineDiff(edit.before, model.text)
        let hunks = diffHunks(diff)
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Circle().fill(Theme.color(agentColor(edit.agent) ?? Theme.current.muted)).frame(width: 8, height: 8)
                Text(edit.agent).font(.ui(size: 12, weight: .semibold))
                Text(edit.ask).font(.ui(size: 12)).foregroundStyle(Theme.text2).lineLimit(1)
                Spacer()
                Text(diffStat(diff)).font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.muted)
            }
            .padding(.horizontal, 12).frame(height: 34)
            Rectangle().fill(Theme.line).frame(height: 1)
            ScrollView([.vertical, .horizontal]) {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(hunks.enumerated()), id: \.offset) { _, l in
                        if let l { line(l) } else {
                            Text("⋯").foregroundStyle(Theme.muted).frame(maxWidth: .infinity, alignment: .leading)
                                .padding(.leading, 12).frame(height: 18).background(Theme.paneHeader)
                        }
                    }
                }
                .font(.system(size: 12, design: .monospaced))
                .padding(.vertical, 6)
            }
            Rectangle().fill(Theme.line).frame(height: 1)
            HStack(spacing: 8) {
                Button("Keep") { model.keepAgentEdit() }
                    .buttonStyle(.borderedProminent).keyboardShortcut(.return, modifiers: .command)
                    .help("Keep the agent's change (⌘↩)")
                Button("Undo It") { model.undoAgentEdit() }
                    .help("Write back the file as it was handed over")
                if let p = edit.pane {
                    Button("Open \(edit.agent)'s pane") { store.open(p) }.buttonStyle(.plain).foregroundStyle(Theme.accent)
                }
                Spacer()
                Text("Already on disk: undoing writes the old text back").font(.ui(size: 11)).foregroundStyle(Theme.muted)
            }
            .padding(.horizontal, 12).frame(height: 40)
        }
    }

    func line(_ l: EditDiffLine) -> some View {
        let (sign, fg, bg): (String, Color, Color) = {
            switch l.kind {
            case .removed: return ("−", Theme.err, Theme.err.opacity(0.12))
            case .added: return ("+", Theme.ok, Theme.ok.opacity(0.12))
            case .same: return (" ", Theme.text, .clear)
            }
        }()
        return HStack(spacing: 0) {
            Text(l.old.map(String.init) ?? "").foregroundStyle(Theme.muted).frame(width: 44, alignment: .trailing)
            Text(l.new.map(String.init) ?? "").foregroundStyle(Theme.muted).frame(width: 44, alignment: .trailing)
            Text(sign).foregroundStyle(fg).frame(width: 18)
            Text(l.text).foregroundStyle(fg).fixedSize()
            Spacer(minLength: 0)
        }
        .frame(height: 18)
        .background(bg)
    }
}

/// Buttons in rows, wrapping at the width given.
struct FlowRow: SwiftUI.Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? 320
        var x: CGFloat = 0, y: CGFloat = 0, line: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x > 0, x + s.width > width {
                x = 0
                y += line + spacing
                line = 0
            }
            x += s.width + spacing
            line = max(line, s.height)
        }
        return CGSize(width: width, height: y + line)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, line: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x > bounds.minX, x + s.width > bounds.maxX {
                x = bounds.minX
                y += line + spacing
                line = 0
            }
            v.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(s))
            x += s.width + spacing
            line = max(line, s.height)
        }
    }
}
