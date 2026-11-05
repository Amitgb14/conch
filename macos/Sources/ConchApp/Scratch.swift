import AppKit
import Carbon.HIToolbox
import ConchKit
import SwiftUI

// The scratch layer, after Rex's floating layers: option-space shows a
// shell over the splits, in the project on screen, and hides it again. It
// is an ordinary conch pane named "Scratch" — the TUI lists it under the
// project's terminals, it survives a reload and the app quitting — that
// is shown floating rather than in a split until it is docked into one or
// made a tab. A pane is shown in exactly one place, so one already in a
// split is gone to rather than floated as well.

extension Store {
    /// Where the scratch layer is for: the focused pane's machine and
    /// project (nil outside every project), and the folder to start one in
    /// — the focused pane's own, so a branch's worktree when it is on one,
    /// else the project's checkout.
    func scratchPlace() -> (machine: String, project: String?, cwd: String?) {
        guard let k = layout.focused else { return ("local", nil, nil) }
        let m = machine(ofKey: k)
        if let c = parseChangesKey(k) {
            return (m, c.project, snapshots[m]?.project(c.project)?.path)
        }
        let p = pane(k)
        let project = p?.projectID ?? parseSessionsKey(k)?.project ?? parseCommitsKey(k)?.project ?? parseFilesKey(k)?.project
        return (m, project, p?.cwd ?? project.flatMap { snapshots[m]?.project($0)?.path })
    }

    /// option-space: the project's scratch layer shown, started on first
    /// use; shown already, hidden.
    func toggleScratch() {
        if scratch != nil {
            hideScratch()
            return
        }
        let (m, project, cwd) = scratchPlace()
        let panes = snapshots[m]?.panes ?? []
        if let p = scratchPane(panes, project: project) {
            showScratch(key(m, p.id))
            return
        }
        guard let c = connection(m) else { NSSound.beep(); return }
        let starting = "\(m)|\(project ?? "")"
        guard scratchStarting != starting else { return } // option-space twice while it starts
        scratchStarting = starting
        struct Create: Encodable {
            var name: String
            var cwd: String?
            var no_project: Bool?
        }
        Task {
            defer { self.scratchStarting = nil }
            do {
                let p = try await c.call("pane.create", Create(name: scratchName, cwd: cwd, no_project: project == nil ? true : nil), as: PaneInfo.self)
                self.snapshots[m, default: Snapshot()].upsert(p)
                self.showScratch(key(m, p.id))
            } catch {
                self.error = "\(error)"
            }
        }
    }

    /// Shows a pane as the scratch layer, or goes to it where it is shown
    /// already.
    func showScratch(_ k: String) {
        if let t = layout.tab(showing: k) {
            layout.select(t)
            layout.focus(k)
            focusTerminal()
            return
        }
        // Its terminal made before the layer is drawn, not while it is.
        let t = terminal(for: k)
        scratch = k
        DispatchQueue.main.async {
            if t.view.window?.makeFirstResponder(t.view) == true { self.terminalHasKeyboard = true }
        }
    }

    /// Hides the layer; its pane keeps running, to be shown again.
    func hideScratch() {
        scratch = nil
        focusTerminal()
    }

    /// option-command-return: the layer becomes a split beside the
    /// focused pane.
    func dockScratch() {
        guard let k = scratch else { return }
        scratch = nil
        if layout.current == nil { layout.open(k) } else { layout.split(.horizontal, with: k) }
        focusTerminal()
    }

    /// option-command-T: the layer becomes a tab of its own.
    func scratchToTab() {
        guard let k = scratch else { return }
        scratch = nil
        layout.open(k)
        focusTerminal()
    }
}

/// The layer itself: the scratch pane's terminal in a floating card over
/// the dimmed splits.
struct ScratchLayer: View {
    @ObservedObject var store: Store
    let pane: String
    @ObservedObject var terminal: PaneTerminal

    var body: some View {
        let p = store.pane(pane)
        ZStack(alignment: .top) {
            Color.black.opacity(0.45).onTapGesture { store.hideScratch() }
            VStack(spacing: 0) {
                HStack(spacing: 9) {
                    LucideIcon("layers", color: Theme.accent, size: 13)
                    Text(scratchName).font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.text)
                    if let b = p?.branch, !b.isEmpty {
                        Text(b).font(.system(size: 11, design: .monospaced)).foregroundStyle(Theme.text2)
                            .padding(.horizontal, 6).padding(.vertical, 1).background(RoundedRectangle(cornerRadius: 5).fill(Theme.raise))
                    }
                    Spacer()
                    Text(store.scope(of: pane)).font(.system(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1)
                }
                .padding(.horizontal, 12).frame(height: 34).background(Theme.paneHeader)
                .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
                TerminalHost(terminal: terminal, paneSize: p.flatMap { i in i.cols.flatMap { c in i.rows.map { CellSize(cols: c, rows: $0) } } })
                HStack(spacing: 16) {
                    HStack(spacing: 5) { Keycap("⌥Space"); Text("hide") }
                    HStack(spacing: 5) { Keycap("⌥⌘↩"); Text("dock as a split") }
                    HStack(spacing: 5) { Keycap("⌥⌘T"); Text("make it a tab") }
                    Spacer()
                    Text("keeps running while hidden")
                }
                .font(.system(size: 11.5)).foregroundStyle(Theme.muted)
                .padding(.horizontal, 12).frame(height: 30).background(Theme.side)
                .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
            }
            .frame(maxWidth: 980, maxHeight: 640)
            .background(Theme.window)
            .clipShape(RoundedRectangle(cornerRadius: 12))
            .overlay(RoundedRectangle(cornerRadius: 12).stroke(Theme.line2))
            .shadow(color: .black.opacity(0.6), radius: 40, y: 24)
            .padding(.horizontal, 40).padding(.top, 50).padding(.bottom, 60)
        }
    }
}

/// option-space from anywhere, the app in the background or not, when
/// [app] scratch_key says so. A Carbon hot key, which needs no
/// Accessibility permission, unlike watching every key the system sees.
final class ScratchHotKey {
    static let shared = ScratchHotKey()
    private var ref: EventHotKeyRef?
    private var handler: EventHandlerRef?
    var fire: (() -> Void)?

    /// On or off, as [app] scratch_key has it.
    func set(_ on: Bool) {
        if on == (ref != nil) { return }
        if on {
            if handler == nil {
                var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
                InstallEventHandler(GetApplicationEventTarget(), { _, _, _ in
                    DispatchQueue.main.async { ScratchHotKey.shared.fire?() }
                    return noErr
                }, 1, &spec, nil, &handler)
            }
            let id = EventHotKeyID(signature: OSType(0x636F6E63), id: 1) // "conc"
            RegisterEventHotKey(UInt32(kVK_Space), UInt32(optionKey), id, GetApplicationEventTarget(), 0, &ref)
        } else if let r = ref {
            UnregisterEventHotKey(r)
            ref = nil
        }
    }
}
