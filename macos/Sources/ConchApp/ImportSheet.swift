import AppKit
import ConchKit
import SwiftUI

/// Integrations → Import… (the mock's screens 2 and 3): another app's
/// conversations by workspace, ticked to come over, then what came over.
/// The logic is ConchKit's ImportFlow; conch reads and writes.
struct ImportSheet: View {
    @ObservedObject var model: IntegrationsModel
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        let f = model.importing ?? ImportFlow(name: "", label: "", reader: "")
        return VStack(alignment: .leading, spacing: 12) {
            Text(f.phase == .done ? "Imported from \(f.label)" : "Import from \(f.label)").font(.ui(size: 15, weight: .semibold))
            Text("Each conversation becomes a handoff an agent can read and carry on from. Read from a copy on this Mac; nothing is sent anywhere.")
                .font(.ui(size: 11.5)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            Divider()
            switch f.phase {
            case .scanning:
                HStack { ProgressView().controlSize(.small); Text("Reading \(f.label)'s conversations…").font(.ui(size: 12.5)) }
                    .frame(maxWidth: .infinity, minHeight: 120)
            case .choosing, .importing:
                choose(f)
            case .done:
                done(f)
            case .failed(let why):
                Text(why).font(.ui(size: 12)).foregroundStyle(Theme.err).textSelection(.enabled)
                    .frame(maxWidth: .infinity, minHeight: 80, alignment: .topLeading)
            }
            HStack {
                if f.phase == .choosing || f.phase == .importing {
                    Text(f.footer).font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                }
                if f.phase == .done, let dir = f.folder {
                    Button("Show in Finder") { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: dir)]) }
                }
                Spacer()
                Button(f.phase == .done ? "Done" : "Cancel") { model.importing = nil }
                    .keyboardShortcut(.cancelAction)
                if f.phase == .choosing || f.phase == .importing {
                    if f.phase == .importing { ProgressView().controlSize(.small) }
                    Button(f.button) { model.runImport() }
                        .keyboardShortcut(.defaultAction).disabled(!f.canImport)
                }
            }
        }
        .padding(20)
        .frame(width: 620)
    }

    func choose(_ f: ImportFlow) -> some View {
        Group {
            if f.conversations.isEmpty {
                Text("\(f.label) has no conversation with anything said in it.").font(.ui(size: 12.5)).foregroundStyle(Theme.muted)
                    .frame(maxWidth: .infinity, minHeight: 80)
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 14) {
                        ForEach(f.groups) { g in
                            VStack(alignment: .leading, spacing: 4) {
                                Toggle(isOn: Binding(get: { Set(g.conversations.map(\.id)).isSubset(of: model.importing?.chosen ?? []) },
                                                     set: { _ in model.importing?.toggle(group: g) })) {
                                    Text(g.title).font(.ui(size: 12.5, weight: .semibold))
                                }
                                ForEach(g.conversations) { c in row(c) }
                            }
                        }
                    }
                    .padding(.vertical, 4)
                }
                .frame(minHeight: 200, maxHeight: 380)
                .disabled(f.phase == .importing)
            }
        }
    }

    func row(_ c: ImportConversation) -> some View {
        Toggle(isOn: Binding(get: { model.importing?.chosen.contains(c.id) ?? false }, set: { _ in model.importing?.toggle(c.id) })) {
            VStack(alignment: .leading, spacing: 1) {
                Text(c.title.isEmpty ? "Untitled" : c.title).font(.ui(size: 12.5)).lineLimit(1)
                Text("\(c.agent) · \(c.messages) message\(c.messages == 1 ? "" : "s") · \(c.state)" + (c.imported == true ? " · imported already" : ""))
                    .font(.ui(size: 11)).foregroundStyle(c.where == "gone" ? Theme.warn : Theme.muted).lineLimit(1)
            }
        }
        .padding(.leading, 18)
    }

    func done(_ f: ImportFlow) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(f.doneWords).font(.ui(size: 12.5, weight: .semibold))
            ScrollView {
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(f.results) { c in
                        HStack(alignment: .top, spacing: 8) {
                            Text(c.where == "gone" ? "!" : "✓").foregroundStyle(c.where == "gone" ? Theme.warn : Theme.ok).frame(width: 12)
                            VStack(alignment: .leading, spacing: 1) {
                                Text(c.title.isEmpty ? "Untitled" : c.title).font(.ui(size: 12.5)).lineLimit(1)
                                Text(c.where == "gone" ? "its worktree and repository are not on this Mac: carry it on anywhere" : (c.dir ?? ""))
                                    .font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.middle)
                            }
                            Spacer()
                            Button("Copy Prompt") {
                                NSPasteboard.general.clearContents()
                                NSPasteboard.general.setString(ImportFlow.prompt(c), forType: .string)
                            }
                            .help("Copy what to tell an agent started there: read the handoff and carry on")
                            Button("Show") { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: c.file)]) }
                        }
                    }
                }
            }
            .frame(minHeight: 120, maxHeight: 320)
            Text("To carry one on, start an agent on its branch and paste the prompt, or drag the file into its prompt. Importing again later replaces the same files.")
                .font(.ui(size: 11)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
        }
    }
}
