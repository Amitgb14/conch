import AppKit
import ConchKit
import SwiftTerm
import SwiftUI

// The one seam between the window and the terminal in a pane. Each pane
// is a terminal running `conch attach PANE`: conch's server is the pane's
// real terminal, and this one shows the screen it keeps. It is Ghostty's
// (GhosttyHost.swift) when the package was built with its library, and
// SwiftTerm's otherwise; nothing else in the app knows which.

/// SwiftTerm's view. Which pane has the keyboard is followed by the
/// window watching clicks (Store.watchClicks), since SwiftTerm does not
/// let becomeFirstResponder be overridden.
final class FocusingTerminalView: LocalProcessTerminalView {}

@MainActor
final class PaneTerminal: NSObject, ObservableObject, LocalProcessTerminalViewDelegate {
    /// key(machine, pane)
    let pane: String
    let view: NSView
    @Published var ended: String? {
        didSet { if ended != nil, oldValue == nil { store?.terminalEnded(pane, lived: Date().timeIntervalSince(started)) } }
    }
    private weak var store: Store?
    private let started = Date()

    /// Which terminal the panes are drawn with, for the status bar.
    static var engine: String {
        #if GHOSTTY
        return GhosttyRuntime.shared.app == nil ? "SwiftTerm" : "Ghostty"
        #else
        return "SwiftTerm"
        #endif
    }

    /// Files dropped on this pane: their paths pasted into it, the files
    /// copied first when the pane is on another machine (Store.drop).
    func dropped(_ urls: [URL]) -> Bool {
        guard let store else { return false }
        store.drop(urls.map(\.path), on: pane)
        return true
    }

    /// The token this app's control pastes start with, made at launch and
    /// given only to the `conch attach` in each pane (attachControlEnv).
    static let controlToken = UUID().uuidString

    /// Tells this pane's `conch attach` something — a search to show — as a
    /// paste only it takes for one (attachControlBody).
    func control(_ c: AttachControl) {
        let body = attachControlBody(token: Self.controlToken, c)
        #if GHOSTTY
        if let g = view as? GhosttyPaneView {
            g.pasteText(body)
            return
        }
        #endif
        // SwiftTerm sends what it is given as typed, so the paste's
        // brackets are put round it here (attach turned bracketed paste on).
        (view as? FocusingTerminalView)?.send(txt: "\u{1b}[200~" + body + "\u{1b}[201~")
    }

    init(key k: String, binary: String?, store: Store) {
        self.pane = k
        self.store = store
        var env = conchEnvironment()
        env[attachControlEnv] = Self.controlToken
        // `conch attach`, with -m for a pane on another machine.
        let (machine, id) = unkey(k)
        let args = onMachine(machine, ["attach", id])
        #if GHOSTTY
        if let binary, GhosttyRuntime.shared.app != nil {
            let g = GhosttyPaneView(command: ([binary] + args).map(shellQuote).joined(separator: " "), environment: env)
            view = g
            super.init()
            g.onExit = { [weak self] in self?.ended = "detached" }
            return
        }
        #endif
        let st = FocusingTerminalView(frame: NSRect(x: 0, y: 0, width: 600, height: 400))
        view = st
        super.init()
        st.processDelegate = self
        st.font = NSFont(name: "JetBrains Mono", size: 14) ?? .monospacedSystemFont(ofSize: 14, weight: .regular) // as conch app's
        apply(Theme.current)
        st.optionAsMetaKey = true
        guard let binary else {
            st.feed(text: "conch is not installed where this app can find it.\r\nSet CONCH_BIN, or install conch.\r\n")
            return
        }
        st.startProcess(executable: binary, args: args, environment: env.map { "\($0.key)=\($0.value)" })
    }

    /// A theme for this pane's terminal. Ghostty's panes take theirs from
    /// the runtime's configuration (GhosttyRuntime.apply), all at once.
    func apply(_ t: ConchTheme) {
        if let st = view as? FocusingTerminalView {
            st.nativeBackgroundColor = Theme.ns(t.bg)
            st.nativeForegroundColor = Theme.ns(t.fg)
            st.caretColor = Theme.ns(t.line)
        }
    }

    func stop() {
        #if GHOSTTY
        if let g = view as? GhosttyPaneView {
            g.onExit = nil
            g.free()
            return
        }
        #endif
        if let st = view as? FocusingTerminalView {
            st.processDelegate = nil
            st.terminate()
        }
    }

    // MARK: LocalProcessTerminalViewDelegate

    nonisolated func sizeChanged(source: LocalProcessTerminalView, newCols: Int, newRows: Int) {}
    nonisolated func setTerminalTitle(source: LocalProcessTerminalView, title: String) {}
    nonisolated func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?) {}
    nonisolated func processTerminated(source: TerminalView, exitCode: Int32?) {
        MainActor.assumeIsolated { self.ended = "detached" }
    }
}

/// A word for a shell: Ghostty runs its command through one.
func shellQuote(_ s: String) -> String {
    "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'"
}

/// A pane's terminal in SwiftUI. The view is the store's, so moving a
/// pane to another split moves its terminal rather than starting another.
struct TerminalHost: NSViewRepresentable {
    let terminal: PaneTerminal
    /// Read so the box round the terminal takes a new theme's background:
    /// the window is repainted in place, and the box is kept.
    @Environment(\.conchTheme) private var look

    func makeNSView(context: Context) -> NSView {
        let box = DropBox()
        box.onDrop = { [weak terminal] urls in
            guard let t = terminal else { return false }
            return t.dropped(urls)
        }
        box.wantsLayer = true
        box.layer?.backgroundColor = Theme.terminal.cgColor
        mount(in: box)
        return box
    }

    func updateNSView(_ box: NSView, context: Context) {
        let _ = look
        box.layer?.backgroundColor = Theme.terminal.cgColor
        if terminal.view.superview !== box { mount(in: box) }
    }

    /// Puts this pane's terminal in the box, and takes out whichever was
    /// there: SwiftUI reuses the box when the same place shows another
    /// pane (switching between single-pane tabs), and a terminal left
    /// behind — a Ghostty surface drawing through Metal — went on showing
    /// over the new one.
    private func mount(in box: NSView) {
        let v = terminal.view
        for old in box.subviews where old !== v { old.removeFromSuperview() }
        v.removeFromSuperview()
        v.translatesAutoresizingMaskIntoConstraints = false
        box.addSubview(v)
        NSLayoutConstraint.activate([
            v.leadingAnchor.constraint(equalTo: box.leadingAnchor, constant: 8),
            v.trailingAnchor.constraint(equalTo: box.trailingAnchor, constant: -4),
            v.topAnchor.constraint(equalTo: box.topAnchor, constant: 6),
            v.bottomAnchor.constraint(equalTo: box.bottomAnchor, constant: -4),
        ])
    }
}

/// The box a pane's terminal sits in, which takes files dropped on it:
/// neither terminal view does, so a file dragged onto a pane did nothing.
/// The terminal views do not register for drags, so a drop over them is
/// this box's. An image dragged as data with no file behind it — from a
/// browser, or a screenshot's thumbnail — is saved to a file first.
final class DropBox: NSView {
    var onDrop: (([URL]) -> Bool)?

    override init(frame: NSRect) {
        super.init(frame: frame)
        registerForDraggedTypes([.fileURL, .png, .tiff])
    }

    required init?(coder: NSCoder) { fatalError("not from a nib") }

    override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
        urls(sender.draggingPasteboard, saving: false).isEmpty ? [] : .copy
    }

    override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
        let u = urls(sender.draggingPasteboard, saving: true)
        return !u.isEmpty && (onDrop?(u) ?? false)
    }

    /// The files on the pasteboard, or the image on it saved as one.
    private func urls(_ pb: NSPasteboard, saving: Bool) -> [URL] {
        if let files = pb.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL], !files.isEmpty {
            return files
        }
        guard let image = NSImage(pasteboard: pb) else { return [] }
        guard saving else { return [URL(fileURLWithPath: "/")] } // there is something to drop
        guard let tiff = image.tiffRepresentation, let rep = NSBitmapImageRep(data: tiff),
              let png = rep.representation(using: .png, properties: [:]) else { return [] }
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("conch-drops", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let f = dir.appendingPathComponent("dropped-\(Int(Date().timeIntervalSince1970 * 1000)).png")
        do { try png.write(to: f) } catch { return [] }
        return [f]
    }
}
