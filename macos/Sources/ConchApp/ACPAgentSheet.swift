import AppKit
import ConchKit
import SwiftUI

/// Add an ACP Agent (or, opened on one, change it): the machine, the
/// command, its name and label, folders and environment; Check starts the
/// command once there and says what it offers, and Add writes it. Add
/// does not wait for a Check: an agent not signed in, or not installed
/// yet, is still worth describing.
struct ACPAgentSheet: View {
    @ObservedObject var store: Store
    @Environment(\.dismiss) private var dismiss
    @Environment(\.conchTheme) private var theme

    @State private var machine: String
    @State private var form: ACPForm
    @State private var more: Bool
    @State private var checking = false
    @State private var check: ACPCheck?
    /// The spec the check was for: a field changed since makes it stale.
    @State private var checkedSpec: ACPSpec?
    @State private var adding = false
    @State private var problem = ""

    init(store: Store, target: ACPSheetTarget) {
        self.store = store
        _machine = State(initialValue: target.machine)
        let f = target.agent.map { ACPForm($0) } ?? ACPForm()
        _form = State(initialValue: f)
        _more = State(initialValue: !f.dirs.isEmpty || !f.env.isEmpty)
    }

    var editing: Bool { form.editing != nil }
    var machineName: String { machine == "local" ? "This Mac" : store.label(machine) }
    var formProblem: String? { form.problem(supported: store.supportedAgents(on: machine)) }
    var shownCheck: ACPCheck? { checkedSpec == form.spec ? check : nil }

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 6) {
                Text(editing ? "\(form.label.isEmpty ? form.resolvedName : form.label) on \(machineName)" : "Add an ACP Agent")
                    .font(.ui(size: 16, weight: .semibold))
                Text("An agent that speaks the Agent Client Protocol on its standard input and output. conch runs it in a pane of its own and draws the conversation. Check the command against the agent's --help: these flags change between releases.")
                    .font(.ui(size: 12.5)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
            }
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 12, verticalSpacing: 10) {
                GridRow {
                    label("Machine")
                    HStack(spacing: 8) {
                        Picker("Machine", selection: $machine) {
                            ForEach(store.acpMachines) { m in Text(m.id == "local" ? "This Mac" : m.label).tag(m.id) }
                        }
                        .labelsHidden().fixedSize().disabled(editing || busy)
                        Text("each machine keeps its own").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                    }
                }
                GridRow {
                    label("Command")
                    VStack(alignment: .leading, spacing: 6) {
                        TextField("", text: $form.command, prompt: Text("gemini --acp"))
                            .textFieldStyle(.roundedBorder).font(.system(size: 12.5, design: .monospaced)).disabled(busy)
                        HStack(spacing: 6) {
                            Text("Known:").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                            ForEach(knownACPCommands, id: \.self) { cmd in
                                Button(cmd) { form.command = cmd }
                                    .buttonStyle(.plain).font(.system(size: 11, design: .monospaced))
                                    .padding(.horizontal, 7).padding(.vertical, 2)
                                    .background(Capsule().fill(form.command == cmd ? Theme.accentDeep.opacity(0.25) : Theme.raise))
                                    .disabled(busy)
                            }
                        }
                    }
                }
                GridRow {
                    label("Name")
                    HStack(spacing: 8) {
                        TextField("", text: $form.name, prompt: Text(acpName(for: form.command).isEmpty ? "from the command" : acpName(for: form.command)))
                            .textFieldStyle(.roundedBorder).font(.system(size: 12.5, design: .monospaced)).frame(width: 180)
                            .disabled(editing || busy)
                        Text(editing ? "the file's name stays" : "lower case, digits and dashes · for -agent and the file name")
                            .font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                    }
                }
                GridRow {
                    label("Shown as")
                    TextField("", text: $form.label, prompt: Text(form.resolvedName.isEmpty ? "Gemini (ACP)" : form.resolvedName))
                        .textFieldStyle(.roundedBorder).frame(width: 260).disabled(busy)
                }
                GridRow(alignment: .top) {
                    label("More")
                    VStack(alignment: .leading, spacing: 6) {
                        Button(more ? "Hide folders and environment" : "Folders and environment…") { more.toggle() }
                            .buttonStyle(.plain).font(.ui(size: 12.5)).foregroundStyle(Theme.accent)
                        if more {
                            VStack(alignment: .leading, spacing: 4) {
                                Text("Folders to look in before PATH, one a line").font(.ui(size: 11.5)).foregroundStyle(Theme.text2)
                                TextEditor(text: $form.dirs).font(.system(size: 12, design: .monospaced)).frame(height: 40)
                                    .overlay(RoundedRectangle(cornerRadius: 5).stroke(Theme.line))
                                Text("Environment, NAME=value a line").font(.ui(size: 11.5)).foregroundStyle(Theme.text2)
                                TextEditor(text: $form.env).font(.system(size: 12, design: .monospaced)).frame(height: 40)
                                    .overlay(RoundedRectangle(cornerRadius: 5).stroke(Theme.line))
                                Text("No API keys here: the file is plain text. Sign the agent in its own way.")
                                    .font(.ui(size: 11)).foregroundStyle(Theme.muted)
                            }
                            .disabled(busy)
                        }
                    }
                }
            }
            checkBox
            if let p = formProblem, !form.command.isEmpty {
                Text(p).font(.ui(size: 12)).foregroundStyle(Theme.warn)
            }
            if !problem.isEmpty {
                Text(problem).font(.ui(size: 12)).foregroundStyle(Theme.err).fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
            Text("Writes agents/\(form.resolvedName.isEmpty ? "NAME" : form.resolvedName).toml on \(machineName) — as conch agent add \(form.resolvedName.isEmpty ? "NAME" : form.resolvedName) -acp '\(form.spec.command)' would.")
                .font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(2)
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button(editing ? "Save" : "Add Agent") { add() }
                    .keyboardShortcut(.defaultAction).disabled(busy || formProblem != nil)
            }
        }
        .padding(22).frame(width: 640)
        .onAppear { if store.agentStatus[machine] == nil { store.checkAgents(on: machine) } }
    }

    var busy: Bool { checking || adding }

    func label(_ s: String) -> some View {
        Text(s).font(.ui(size: 12.5)).foregroundStyle(Theme.text2).gridColumnAlignment(.trailing)
    }

    var checkBox: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 10) {
                Text("Check").font(.ui(size: 13, weight: .semibold))
                Text("starts it once on \(machineName) and asks what it offers").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                Spacer()
                Button(shownCheck == nil ? "Check" : "Check Again") { runCheck() }
                    .disabled(busy || form.spec.command.isEmpty)
            }
            .padding(.horizontal, 12).padding(.vertical, 9)
            Divider()
            Group {
                if checking {
                    HStack(spacing: 8) {
                        ProgressView().controlSize(.small)
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Starting \(form.spec.command) and sending initialize…").font(.ui(size: 12.5)).foregroundStyle(Theme.text2)
                            Text("An agent written in node can take a minute to start the first time.").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                        }
                    }
                } else if let c = shownCheck {
                    checkResult(c)
                } else {
                    Text("Not checked yet. Add works without it; Check says now what would otherwise show in the agent's pane.")
                        .font(.ui(size: 12.5)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
                }
            }
            .padding(12)
        }
        .background(RoundedRectangle(cornerRadius: 9).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(Theme.line))
    }

    @ViewBuilder
    func checkResult(_ c: ACPCheck) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 7) {
                Image(systemName: c.speaksACP ? "checkmark.circle.fill" : "exclamationmark.circle.fill")
                    .foregroundStyle(c.speaksACP ? Theme.ok : Theme.err)
                Text(acpCheckHeadline(c)).font(.ui(size: 12.5, weight: .semibold))
            }
            if c.speaksACP {
                LazyVGrid(columns: [GridItem(.flexible(), alignment: .topLeading), GridItem(.flexible(), alignment: .topLeading)], spacing: 6) {
                    ForEach(Array(acpCheckLines(c).enumerated()), id: \.offset) { _, l in
                        HStack(alignment: .top, spacing: 6) {
                            Text(l.mark == .yes ? "✓" : l.mark == .warn ? "!" : "–").font(.ui(size: 12, weight: .bold))
                                .foregroundStyle(l.mark == .yes ? Theme.ok : Theme.warn).frame(width: 12)
                            VStack(alignment: .leading, spacing: 1) {
                                Text(l.what).font(.ui(size: 12))
                                Text(l.why).font(.ui(size: 11)).foregroundStyle(Theme.muted)
                            }
                        }
                    }
                }
            } else {
                if let m = c.message, c.problem != "not_found" {
                    Text(m).font(.ui(size: 12)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
                }
                if let install = c.install, !install.isEmpty {
                    HStack(spacing: 8) {
                        Text("Install it:").font(.ui(size: 12)).foregroundStyle(Theme.text2)
                        Text(install).font(.system(size: 12, design: .monospaced)).textSelection(.enabled)
                        Button {
                            NSPasteboard.general.clearContents()
                            NSPasteboard.general.setString(install, forType: .string)
                        } label: { Image(systemName: "doc.on.doc").font(.system(size: 11)) }
                            .buttonStyle(.plain).foregroundStyle(Theme.accent).help("Copy the command")
                    }
                    Text("then Check Again.").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                }
                if let o = c.output, !o.isEmpty {
                    Text(o).font(.system(size: 11.5, design: .monospaced)).foregroundStyle(Theme.text2)
                        .padding(8).frame(maxWidth: .infinity, alignment: .leading)
                        .background(RoundedRectangle(cornerRadius: 6).fill(Theme.well)).lineLimit(6)
                }
                Text("Add is still allowed.").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
            }
        }
    }

    func runCheck() {
        let spec = form.spec
        checking = true
        problem = ""
        Task {
            defer { checking = false }
            do {
                check = try await store.checkACP(spec, on: machine)
                checkedSpec = spec
            } catch {
                problem = "Could not check it: \(error)"
            }
        }
    }

    func add() {
        let spec = form.spec
        adding = true
        problem = ""
        Task {
            defer { adding = false }
            do {
                try await store.addACP(spec, replace: editing, on: machine)
                dismiss()
            } catch {
                problem = "\(error)"
            }
        }
    }
}
