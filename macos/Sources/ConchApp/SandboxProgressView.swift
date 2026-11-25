import AppKit
import ConchKit
import SwiftUI

// New Sandbox's steps as they happen, in the sheet that asked for them
// (the mock: https://claude.ai/artifact/XMjpjSh5FATiZp473jubtH): each step
// with a tick, a spinner or a mark, what the one under way is doing, the
// raw lines folded under Details, and Run in Background to close the sheet
// while it carries on. Failed, it says why and asks what to do with the
// sandbox, which is still running; ready, it offers a terminal or an agent
// there.

struct SandboxProgressView: View {
    @ObservedObject var store: Store
    @ObservedObject var run: SandboxSetupRun
    let close: () -> Void
    @Environment(\.conchTheme) private var theme
    @State private var detailsOpen = false
    @State private var now = Date()
    private let tick = Timer.publish(every: 1, on: .main, in: .common).autoconnect()

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 0) {
            header
            if !run.finished {
                ProgressView(value: run.state.fraction).progressViewStyle(.linear).tint(Theme.accent)
                    .padding(.horizontal, 20).padding(.bottom, 4)
            }
            VStack(alignment: .leading, spacing: 2) {
                ForEach(SandboxStep.allCases, id: \.self) { step in stepRow(step) }
            }
            .padding(.horizontal, 14).padding(.top, 8)
            if let why = run.failure { failureBox(why) }
            if run.ready { readyBox }
            if !run.ready { details }
            Spacer(minLength: 16)
            Divider()
            footer
        }
        .frame(minHeight: 460)
        .onReceive(tick) { now = $0 }
    }

    // MARK: - parts

    private var header: some View {
        HStack(alignment: .top, spacing: 12) {
            Group {
                if run.ready {
                    Image(systemName: "checkmark").font(.system(size: 15, weight: .bold)).foregroundStyle(Theme.ok)
                } else if run.failed {
                    Image(systemName: "exclamationmark.circle").font(.system(size: 17, weight: .semibold)).foregroundStyle(Theme.err)
                } else {
                    LucideIcon("cloud", color: Theme.accent, size: 17)
                }
            }
            .frame(width: 34, height: 34)
            .background(RoundedRectangle(cornerRadius: 9).fill((run.ready ? Theme.ok : run.failed ? Theme.err : Theme.accent).opacity(0.12)))
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.ui(size: 15, weight: .semibold)).foregroundStyle(Theme.text)
                Text(subtitle).font(.ui(size: 12)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.middle)
            }
            Spacer()
            if let f = run.state.first, !run.finished {
                Text(SandboxSetupState.duration(now.timeIntervalSince(f)))
                    .font(.ui(size: 12, design: .monospaced)).foregroundStyle(Theme.muted)
            }
        }
        .padding(.horizontal, 20).padding(.top, 16).padding(.bottom, 12)
    }

    private var title: String {
        if run.ready { return "Your sandbox is ready" }
        if run.failed { return "The sandbox couldn't be set up" }
        return "Setting up a sandbox"
    }

    private var subtitle: String {
        var parts = [run.providerLabel]
        if !run.summary.isEmpty { parts.append(run.summary) }
        if let n = run.state.label ?? run.state.sandbox { parts.append(n) }
        if run.failed { parts.append(run.kept ? "still running" : "deleted") }
        if run.ready, let f = run.state.first, let l = run.state.last {
            parts.append("set up in " + SandboxSetupState.duration(l.timeIntervalSince(f)))
        }
        return parts.joined(separator: " · ")
    }

    @ViewBuilder
    private func stepRow(_ step: SandboxStep) -> some View {
        let st = run.state.state(step)
        // A step left undone by a run that ended: never reached.
        let shown: SandboxStepState = (run.finished && st == .now) ? (run.failed ? .failed : .done) : st
        HStack(spacing: 12) {
            StepMark(state: shown)
            VStack(alignment: .leading, spacing: 1) {
                Text(shown == .done ? step.doneTitle : step.title)
                    .font(.ui(size: 13, weight: shown == .now || shown == .failed ? .semibold : .regular))
                    .foregroundStyle(shown == .later ? Theme.muted : shown == .failed ? Theme.err : Theme.text)
                if shown == .now, let d = sandboxStepDetail(run.state.detail[step]) {
                    Text(d).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.middle)
                }
            }
            Spacer()
            if let t = run.state.time(step, now: now) {
                Text(t).font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.muted)
            }
        }
        .padding(.horizontal, 10).frame(minHeight: shown == .now ? 42 : 32)
        .background(RoundedRectangle(cornerRadius: 8).fill(shown == .now ? Theme.accent.opacity(0.07) : .clear))
    }

    private func failureBox(_ why: String) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(why).font(.ui(size: 12.5)).foregroundStyle(Theme.text).textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true).lineLimit(8)
            if run.pane != nil {
                Button("Show the Whole Log") { store.showSetupLog(run) }.buttonStyle(.link).font(.ui(size: 12))
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.err.opacity(0.06)))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Theme.err.opacity(0.25)))
        .padding(.horizontal, 20).padding(.top, 12)
    }

    private var readyBox: some View {
        HStack(alignment: .top, spacing: 24) {
            fact("Machine", run.state.machine ?? "—", mono: true)
            fact("Sandbox", run.state.sandbox ?? "—", mono: true)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Theme.line))
        .padding(.horizontal, 20).padding(.top, 12)
    }

    private func fact(_ k: String, _ v: String, mono: Bool) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(k).font(.ui(size: 11)).foregroundStyle(Theme.muted)
            Text(v).font(.ui(size: 12, design: mono ? .monospaced : .default)).foregroundStyle(Theme.text).lineLimit(1).truncationMode(.middle)
        }
    }

    private var details: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button { detailsOpen.toggle() } label: {
                HStack(spacing: 6) {
                    Image(systemName: detailsOpen ? "chevron.down" : "chevron.right").font(.system(size: 9, weight: .semibold))
                    Text("Details").font(.ui(size: 12))
                    Spacer()
                    Text("conch sandbox create").font(.ui(size: 11, design: .monospaced))
                }
                .foregroundStyle(Theme.muted).padding(.horizontal, 12).padding(.vertical, 7).contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            if detailsOpen {
                Divider()
                ScrollView {
                    Text(run.state.lines.suffix(40).joined(separator: "\n"))
                        .font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.text2)
                        .frame(maxWidth: .infinity, alignment: .leading).textSelection(.enabled)
                        .padding(.horizontal, 12).padding(.vertical, 8)
                }
                .frame(maxHeight: 120)
            }
        }
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Theme.line))
        .padding(.horizontal, 20).padding(.top, 12)
    }

    @ViewBuilder
    private var footer: some View {
        HStack(spacing: 10) {
            if run.ready {
                Button("Done") { close() }
                Spacer()
                if let m = run.state.machine {
                    Button("Open Terminal") {
                        store.newPane(machine: m, cwd: nil, inProject: false, agent: nil)
                        close()
                    }
                    Menu("Start an Agent") {
                        ForEach(store.startable(on: m), id: \.name) { a in
                            AgentMenuButton(agent: a.name, title: store.agentTitle(a.name)) {
                                store.newPane(machine: m, cwd: nil, inProject: false, agent: a.name)
                                close()
                            }
                        }
                    }
                    .fixedSize()
                }
            } else if run.failed {
                if run.kept {
                    Button(run.busy ?? "Delete Sandbox") { store.deleteSetupSandbox(run); close() }
                        .foregroundStyle(Theme.err).disabled(run.busy != nil)
                    Spacer()
                    Button("Keep It") { store.keepSetupSandbox(run); close() }
                    Button("Try the Install Again") { store.retrySandboxSetup(run) }
                        .keyboardShortcut(.defaultAction).buttonStyle(.borderedProminent)
                } else {
                    Spacer()
                    Button("Close") { store.dropSandboxSetup(run); close() }.keyboardShortcut(.defaultAction)
                }
            } else {
                Button("Cancel and Delete…") { store.cancelSandboxSetup(run) }.foregroundStyle(Theme.err)
                Spacer()
                Text("It keeps going if you close this").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                Button("Run in Background") { close() }
                    .keyboardShortcut(.defaultAction).buttonStyle(.borderedProminent)
            }
        }
        .controlSize(.regular)
        .padding(.horizontal, 20).padding(.vertical, 12)
    }
}

/// A step's mark: a tick, a spinner, an empty ring, or a red one.
private struct StepMark: View {
    let state: SandboxStepState

    var body: some View {
        switch state {
        case .done:
            Image(systemName: "checkmark").font(.system(size: 9, weight: .heavy)).foregroundStyle(.white)
                .frame(width: 18, height: 18).background(Circle().fill(Theme.ok))
        case .now:
            ProgressView().controlSize(.small).scaleEffect(0.8).frame(width: 18, height: 18)
        case .failed:
            Text("!").font(.system(size: 11, weight: .heavy)).foregroundStyle(.white)
                .frame(width: 18, height: 18).background(Circle().fill(Theme.err))
        case .later:
            Circle().strokeBorder(Theme.muted.opacity(0.6), lineWidth: 1.5).frame(width: 18, height: 18)
        }
    }
}

/// The status bar's word on setups still going, or failed and not yet
/// dealt with: a click shows the sheet again.
struct SandboxSetupChip: View {
    @ObservedObject var store: Store
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        let runs = store.pendingSandboxSetups
        if let run = runs.first {
            Button { store.showSandboxSetup(run) } label: {
                HStack(spacing: 5) {
                    if run.failed {
                        Image(systemName: "exclamationmark.circle.fill").font(.system(size: 10)).foregroundStyle(Theme.err)
                    } else {
                        ProgressView().controlSize(.mini).scaleEffect(0.7).frame(width: 10, height: 10)
                    }
                    Text(label(run, more: runs.count - 1)).font(.ui(size: 11.5))
                        .foregroundStyle(run.failed ? Theme.err : Theme.accent)
                }
                .padding(.horizontal, 8).padding(.vertical, 2)
                .background(Capsule().fill((run.failed ? Theme.err : Theme.accent).opacity(0.1)))
            }
            .buttonStyle(.plain)
            .help(run.failed ? "A sandbox couldn't be set up — click to see why and what to do with it"
                             : "A sandbox is being set up — click to see its steps")
        }
    }

    private func label(_ run: SandboxSetupRun, more: Int) -> String {
        let base = run.failed ? "Sandbox failed" : "Sandbox \(run.state.doneCount + 1)/\(SandboxStep.allCases.count)"
        return more > 0 ? base + " +\(more)" : base
    }
}
