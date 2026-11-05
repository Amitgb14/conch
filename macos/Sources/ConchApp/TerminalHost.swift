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
    /// Given the program's own output (attach -raw) for Ghostty to draw,
    /// rather than conch's screen repainted: where this conch and the
    /// pane's server both can.
    let raw: Bool
    /// The pane's size while another window holds it: the terminal is
    /// drawn at that size, top left, the rest dotted (the letterbox).
    @Published private(set) var letterbox: CellSize?
    /// Lines of Ghostty's history below what is in view, for a raw pane
    /// scrolled back.
    @Published private(set) var linesBelow = 0
    /// What Ghostty's search found in a raw pane: how many, and which.
    @Published private(set) var searchTotal: Int?
    @Published private(set) var searchSelected: Int?
    /// The pane's size, as the server last said it.
    var paneSize: CellSize?
    /// The room this window has for the pane, last told to attach.
    private var room: CellSize?
    /// Says the room again while the pane is another size: the first
    /// telling can come before attach is reading (it is said as soon as
    /// the view has a size), and attach ignores a room it already has.
    private var roomTimer: Timer?

    /// Which terminal the panes are drawn with, for the status bar.
    static var engine: String {
        #if GHOSTTY
        return GhosttyRuntime.shared.app == nil ? "SwiftTerm" : "Ghostty"
        #else
        return "SwiftTerm"
        #endif
    }

    /// How this pane is drawn, for the status bar: Ghostty given the
    /// program's output ("direct"), or conch's screen repainted.
    var drawnBy: String { "\(Self.engine) · \(raw ? "direct" : "repainted")" }

    /// Files dropped on this pane: their paths pasted into it, the files
    /// copied first when the pane is on another machine (Store.drop).
    func dropped(_ urls: [URL]) -> Bool {
        guard let store else { return false }
        store.drop(urls.map(\.path), on: pane)
        return true
    }

    /// Whether this conch's attach keeps an ended pane's screen (-hold),
    /// asked once.
    private static var usageFor: (String, String)?
    private static func usage(_ binary: String) -> String {
        if let (b, u) = usageFor, b == binary { return u }
        let u = attachUsageLine(binary)
        usageFor = (binary, u)
        return u
    }
    static func holds(_ binary: String?) -> Bool {
        guard let binary else { return false }
        return attachHolds(usage(binary))
    }

    /// Whether this conch's attach takes -raw, asked once.
    static func raws(_ binary: String?) -> Bool {
        guard let binary else { return false }
        return attachRaws(usage(binary))
    }

    /// Whether it takes -view too, for the palette's preview.
    static func views(_ binary: String?) -> Bool {
        guard let binary else { return false }
        return attachViews(usage(binary))
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
        #if GHOSTTY
        let ghostty = GhosttyRuntime.shared.app != nil
        #else
        let ghostty = false
        #endif
        // Raw only into Ghostty: SwiftTerm's panes stay repainted.
        let raw = ghostty && Self.raws(binary) && serverRaws(store.connection(machine)?.hello?.capabilities)
        self.raw = raw
        let args = onMachine(machine, ["attach"] + (Self.holds(binary) ? ["-hold"] : []) + (raw ? ["-raw"] : []) + [id])
        #if GHOSTTY
        if let binary, ghostty {
            let g = GhosttyPaneView(command: ([binary] + args).map(shellQuote).joined(separator: " "), environment: env,
                                    raw: raw, fontSize: 0)
            view = g
            super.init()
            g.onExit = { [weak self] in self?.ended = "detached" }
            g.onReport = { [weak self] r in self?.took(r) }
            if raw {
                roomTimer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
                    MainActor.assumeIsolated { self?.sayRoomAgain() }
                }
            }
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

    // MARK: raw panes

    #if GHOSTTY
    private func took(_ r: GhosttyPaneView.Report) {
        switch r {
        case .searchTotal(let n): searchTotal = n
        case .searchSelected(let n): searchSelected = n
        case .scrollbar(let total, let offset, let len):
            let below = max(total - offset - len, 0)
            if below != linesBelow { linesBelow = below }
        }
    }
    #endif

    /// Where the terminal goes in the space it has (fill): all of it, or,
    /// for a raw pane another window holds the size of, exactly the pane's
    /// size from the top left. The room the space holds is told to this
    /// pane's attach as it changes, which asks the server for it.
    func place(in fill: NSRect) -> NSRect {
        #if GHOSTTY
        guard raw, let g = view as? GhosttyPaneView, let m = g.metrics(),
              let room = m.room(width: fill.width, height: fill.height) else { return fill }
        if room != self.room {
            self.room = room
            control(.room(room))
        }
        guard letterboxed(pane: paneSize, room: room), let p = paneSize else {
            if letterbox != nil { DispatchQueue.main.async { self.letterbox = nil } }
            return fill
        }
        if letterbox != p { DispatchQueue.main.async { self.letterbox = p } }
        let (w, h) = m.points(for: p)
        return NSRect(x: fill.minX, y: fill.maxY - h, width: w, height: h)
        #else
        return fill
        #endif
    }

    private func sayRoomAgain() {
        guard let r = room, ended == nil, paneSize != r else { return }
        control(.room(r))
    }

    /// Takes the pane's size for this window, as typing would: a click on
    /// the letterbox.
    func take() { if raw { control(.take) } }

    /// Ghostty's own search, for a raw pane: its history is Ghostty's.
    /// nil ends it.
    func search(_ query: String?) {
        #if GHOSTTY
        guard let g = view as? GhosttyPaneView else { return }
        if let q = query, !q.isEmpty {
            g.binding("search:" + q)
        } else {
            g.binding("end_search")
            searchTotal = nil
            searchSelected = nil
        }
        #endif
    }

    /// The next match of Ghostty's search, older (back) or newer.
    func searchNext(back: Bool) {
        #if GHOSTTY
        (view as? GhosttyPaneView)?.binding(back ? "navigate_search:previous" : "navigate_search:next")
        #endif
    }

    /// Back down to what the program is printing now.
    func scrollToBottom() {
        #if GHOSTTY
        (view as? GhosttyPaneView)?.binding("scroll_to_bottom")
        #endif
    }

    func stop() {
        roomTimer?.invalidate()
        roomTimer = nil
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
    /// The pane's size as the server says it, so a raw pane held by
    /// another window is drawn letterboxed at it.
    var paneSize: CellSize?
    /// Read so the box round the terminal takes a new theme's background:
    /// the window is repainted in place, and the box is kept.
    @Environment(\.conchTheme) private var look

    func makeNSView(context: Context) -> NSView {
        let box = DropBox()
        box.onDrop = { [weak terminal] urls in
            guard let t = terminal else { return false }
            return t.dropped(urls)
        }
        box.place = { [weak terminal] fill in terminal?.place(in: fill) ?? fill }
        box.onTake = { [weak terminal] in terminal?.take() }
        box.wantsLayer = true
        box.layer?.masksToBounds = true
        box.layer?.backgroundColor = Theme.terminal.cgColor
        terminal.paneSize = paneSize
        mount(in: box)
        return box
    }

    func updateNSView(_ box: NSView, context: Context) {
        let _ = look
        if terminal.view.superview !== box { mount(in: box) }
        terminal.paneSize = paneSize
        // The box's colours, dots or not, are set where it is laid out,
        // which knows whether the terminal fills it.
        box.needsLayout = true
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
        // Placed by the box (DropBox.layout), not constrained: a letterbox
        // is a size of its own, not the space's.
        v.translatesAutoresizingMaskIntoConstraints = true
        box.addSubview(v)
        box.needsLayout = true
    }
}

/// The box a pane's terminal sits in, which takes files dropped on it:
/// neither terminal view does, so a file dragged onto a pane did nothing.
/// The terminal views do not register for drags, so a drop over them is
/// this box's. An image dragged as data with no file behind it — from a
/// browser, or a screenshot's thumbnail — is saved to a file first.
final class DropBox: NSView {
    var onDrop: (([URL]) -> Bool)?
    /// Where the terminal goes in the space it has (PaneTerminal.place).
    var place: ((NSRect) -> NSRect)?
    /// A click on the box outside the terminal: the letterbox's dots.
    var onTake: (() -> Void)?

    /// The space a terminal has: the box less a margin, wider on the left
    /// as the cards have always had it.
    static func fill(_ b: NSRect) -> NSRect {
        NSRect(x: b.minX + 8, y: b.minY + 4, width: max(b.width - 12, 1), height: max(b.height - 10, 1))
    }

    /// The letterbox's edge: a dashed line along the right and bottom of a
    /// terminal smaller than its space, so where the pane ends is seen.
    private let edge: CAShapeLayer = {
        let l = CAShapeLayer()
        l.fillColor = nil
        l.lineWidth = 1
        l.lineDashPattern = [4, 3]
        return l
    }()

    override func layout() {
        super.layout()
        let fill = Self.fill(bounds)
        let r = place?(fill) ?? fill
        for v in subviews where v.frame != r { v.frame = r }
        let lettered = r != fill
        layer?.backgroundColor = lettered ? Self.dots.cgColor : Theme.terminal.cgColor
        if lettered {
            if edge.superlayer == nil { layer?.addSublayer(edge) }
            let path = CGMutablePath()
            path.move(to: CGPoint(x: r.maxX + 0.5, y: r.maxY))
            path.addLine(to: CGPoint(x: r.maxX + 0.5, y: r.minY - 0.5))
            path.addLine(to: CGPoint(x: r.minX, y: r.minY - 0.5))
            edge.path = path
            edge.strokeColor = Theme.ns(Theme.current.fg).withAlphaComponent(0.3).cgColor
            edge.zPosition = 10
        } else {
            edge.removeFromSuperlayer()
        }
    }

    override func mouseDown(with event: NSEvent) {
        if let t = subviews.first, !t.frame.contains(convert(event.locationInWindow, from: nil)) {
            onTake?()
            t.window?.makeFirstResponder(t)
            return
        }
        super.mouseDown(with: event)
    }

    /// The letterbox's dots: a faint dot every few points over the
    /// terminal's colour, as tmux fills what a smaller client leaves.
    static var dots: NSColor {
        let size = NSSize(width: 8, height: 8)
        let img = NSImage(size: size, flipped: false) { r in
            Theme.terminal.setFill()
            r.fill()
            Theme.ns(Theme.current.fg).withAlphaComponent(0.22).setFill()
            NSBezierPath(ovalIn: NSRect(x: 3.2, y: 3.2, width: 1.6, height: 1.6)).fill()
            return true
        }
        return NSColor(patternImage: img)
    }

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
