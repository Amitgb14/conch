import AppKit
import ConchKit
import SwiftUI

// The palette's preview, after Rex's session picker: the pane the
// selection is on, live, beside the list — a second Ghostty surface fed
// the pane's output by `conch attach -raw -view`, which never resizes the
// pane or takes its size, so looking at a pane from ⌘K cannot shrink it
// for whoever is working in it. The pane is drawn whole at a font small
// enough to fit (fittingFontSize). ⇥ hands it the keyboard; an agent
// waiting is answered from the buttons under it.

@MainActor
final class PanePreview: ObservableObject {
    let pane: String
    let view: NSView
    /// The pane's size when the preview was made: the surface is that
    /// many cells, at the fitted font.
    let size: CellSize

    /// A preview of a pane, where this conch and the pane's server can
    /// give one raw and only looking; nil otherwise, for the palette to
    /// show the pane's text instead.
    init?(pane k: String, store: Store, width: Double, height: Double) {
        #if GHOSTTY
        guard GhosttyRuntime.shared.app != nil, let binary = store.binary,
              PaneTerminal.raws(binary), PaneTerminal.views(binary),
              let p = store.pane(k), p.running, let cols = p.cols, let rows = p.rows else { return nil }
        let (machine, id) = unkey(k)
        guard serverRaws(store.connection(machine)?.hello?.capabilities) else { return nil }
        // The cells of a 14pt pane, read off a terminal already open, or
        // near enough until one is.
        let base = store.terminals.values.compactMap { ($0.view as? GhosttyPaneView)?.metrics() }.first
            ?? CellMetrics(cellWidth: 8.4, cellHeight: 19, padX: GhosttyRuntime.padX, padY: GhosttyRuntime.padY)
        size = CellSize(cols: cols, rows: rows)
        let font = fittingFontSize(base: 14, metrics: base, pane: size, width: width, height: height)
        var env = conchEnvironment()
        env[attachControlEnv] = PaneTerminal.controlToken
        let args = onMachine(machine, ["attach", "-raw", "-view", id])
        let g = GhosttyPaneView(command: ([binary] + args).map(shellQuote).joined(separator: " "), environment: env,
                                raw: true, fontSize: Float(font))
        view = g
        pane = k
        #else
        return nil
        #endif
    }

    func stop() {
        #if GHOSTTY
        if let g = view as? GhosttyPaneView {
            g.onExit = nil
            g.free()
        }
        #endif
    }

    /// Where the surface goes in the space: the pane's cells exactly, from
    /// the top left, once Ghostty says what a cell measures at this font.
    func place(in fill: NSRect) -> NSRect {
        #if GHOSTTY
        guard let m = (view as? GhosttyPaneView)?.metrics() else { return fill }
        let (w, h) = m.points(for: size)
        return NSRect(x: fill.minX, y: fill.maxY - h, width: w, height: h)
        #else
        return fill
        #endif
    }

    /// ⇥: the keyboard to the preview, to type into the pane.
    func type() { view.window?.makeFirstResponder(view) }
}

struct PreviewHost: NSViewRepresentable {
    let preview: PanePreview
    /// Read so the box takes a new theme's background.
    @Environment(\.conchTheme) private var look

    func makeNSView(context: Context) -> NSView {
        let box = DropBox()
        box.place = { [weak preview] fill in preview?.place(in: fill) ?? fill }
        box.wantsLayer = true
        box.layer?.masksToBounds = true
        box.layer?.backgroundColor = Theme.terminal.cgColor
        preview.view.translatesAutoresizingMaskIntoConstraints = true
        box.addSubview(preview.view)
        // Ghostty knows its cells only once it has a size: place again then.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { box.needsLayout = true }
        return box
    }

    func updateNSView(_ box: NSView, context: Context) {
        let _ = look
        box.layer?.backgroundColor = Theme.terminal.cgColor
        if preview.view.superview !== box {
            box.subviews.forEach { $0.removeFromSuperview() }
            box.addSubview(preview.view)
            box.needsLayout = true
        }
    }
}

/// The right side of the palette: the pane, its header, and the answers.
struct PalettePreview: View {
    @ObservedObject var store: Store
    let pane: String
    let preview: PanePreview?
    @State private var lines: [String] = []

    var body: some View {
        let p = store.pane(pane)
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                if let a = p?.agent { Mark(agent: a.name) } else { LucideIcon(name: .terminal, color: Theme.muted, size: 12) }
                Text(p?.agent?.name ?? p?.name ?? pane).font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.text)
                if let b = p?.branch, !b.isEmpty {
                    Text(b).font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.text2)
                        .padding(.horizontal, 6).padding(.vertical, 1).background(RoundedRectangle(cornerRadius: 5).fill(Theme.raise))
                }
                Spacer()
                Pill.state(p?.agent?.state)
            }
            .padding(.horizontal, 10).frame(height: 32).background(Theme.paneHeader)
            .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
            if let preview {
                PreviewHost(preview: preview).id(preview.pane)
            } else {
                // No live preview to be had (SwiftTerm, an older conch or
                // server): the screen's text, as `conch read` gives it.
                ScrollView {
                    Text(lines.joined(separator: "\n")).font(.ui(size: 10.5, design: .monospaced)).foregroundStyle(Theme.text2)
                        .frame(maxWidth: .infinity, alignment: .leading).padding(10).textSelection(.enabled)
                }
                .background(Color(nsColor: Theme.terminal))
                .task(id: pane) { lines = await store.screenText(pane) }
            }
            HStack(spacing: 8) {
                if let q = store.questions[pane], !q.choices.isEmpty {
                    Text("Answer from here").font(.ui(size: 12)).foregroundStyle(Theme.text2)
                    ChoiceButtons(store: store, pane: pane, question: q)
                } else {
                    Text(preview == nil ? "The screen as it is now" : "Live · ⇥ to type into it").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                }
                Spacer()
            }
            .padding(.horizontal, 10).frame(minHeight: 40).background(Theme.paneHeader)
            .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
        }
    }
}
