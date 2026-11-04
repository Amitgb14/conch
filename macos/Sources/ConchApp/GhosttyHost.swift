#if GHOSTTY
import AppKit
import ConchKit
import GhosttyKit

// Ghostty's terminal in a pane: a libghostty surface (upstream, pinned to
// a release, no fork) running `conch attach PANE`. This is the smallest
// host libghostty needs, after Ghostty's own macOS app
// (macos/Sources/Ghostty in its repository): one app object, a view per
// surface that hands it keys, the mouse, its size and focus, and the
// clipboard. Ghostty's renderer puts its own Metal layer on the view.
//
// Left out on purpose, and the reason for each: Ghostty's splits, tabs
// and windows (the window here is conch's), its config files (conch writes
// the settings, as `conch app` does), and its inspector.

/// The one libghostty app the window's surfaces belong to.
@MainActor
final class GhosttyRuntime {
    static let shared = GhosttyRuntime()
    private(set) var app: ghostty_app_t?
    private(set) var failure: String?

    private init() {
        if ghostty_init(UInt(CommandLine.argc), CommandLine.unsafeArgv) != GHOSTTY_SUCCESS {
            failure = "Ghostty's library would not start"
            return
        }
        guard let cfg = ghostty_config_new() else {
            failure = "Ghostty's configuration could not be made"
            return
        }
        // conch's settings, and nothing of the person's own Ghostty: the
        // same rule `conch app` keeps for the terminal it opens.
        let path = Self.writeConfig(Theme.current)
        path.withCString { ghostty_config_load_file(cfg, $0) }
        ghostty_config_finalize(cfg)
        theme = Theme.current

        var rt = ghostty_runtime_config_s(
            userdata: nil,
            supports_selection_clipboard: false,
            wakeup_cb: { _ in DispatchQueue.main.async { GhosttyRuntime.shared.tick() } },
            // A link the pane printed, ⌘-clicked: opened in the browser when
            // it is the web's or mail's (openableURL). Every other action is
            // Ghostty's own app's business, not this one's.
            action_cb: { _, _, action in
                guard action.tag == GHOSTTY_ACTION_OPEN_URL, let p = action.action.open_url.url else { return false }
                let bytes = UnsafeRawBufferPointer(start: p, count: Int(action.action.open_url.len))
                let text = String(decoding: bytes, as: UTF8.self)
                guard let url = openableURL(text) else { return true }
                DispatchQueue.main.async { NSWorkspace.shared.open(url) }
                return true
            },
            read_clipboard_cb: { ud, loc, state, mimes, n, list in
                GhosttyPaneView.readClipboard(ud, loc, state, mimes, n, list)
            },
            confirm_read_clipboard_cb: { ud, _, state, _ in
                // Only a program asking to read the clipboard (OSC 52) gets
                // here; conch's panes are not given it.
                if let v = GhosttyPaneView.from(ud), let s = v.surface { ghostty_surface_deny_clipboard_request(s, state) }
            },
            write_clipboard_cb: { _, loc, content, len, _ in
                GhosttyPaneView.writeClipboard(loc, content, len)
            },
            close_surface_cb: { ud, _ in
                DispatchQueue.main.async { GhosttyPaneView.from(ud)?.onExit?() }
            }
        )
        app = ghostty_app_new(&rt, cfg)
        if app == nil { failure = "Ghostty could not make its app" }
        config = cfg
        if let app { ghostty_app_set_focus(app, NSApp.isActive) }
        NotificationCenter.default.addObserver(forName: NSApplication.didBecomeActiveNotification, object: nil, queue: .main) { _ in
            MainActor.assumeIsolated { if let a = GhosttyRuntime.shared.app { ghostty_app_set_focus(a, true) } }
        }
        NotificationCenter.default.addObserver(forName: NSApplication.didResignActiveNotification, object: nil, queue: .main) { _ in
            MainActor.assumeIsolated { if let a = GhosttyRuntime.shared.app { ghostty_app_set_focus(a, false) } }
        }
    }

    func tick() {
        if let app { ghostty_app_tick(app) }
    }

    /// The configuration in effect, which a surface made after a change
    /// is given (Ghostty hands a new surface the one the app began with),
    /// and the one before it, kept until a newer one replaces it in case a
    /// surface is still reading it.
    private(set) var config: ghostty_config_t?
    private var previous: ghostty_config_t?
    private var theme: ConchTheme?

    /// A new theme for every pane at once.
    func apply(_ t: ConchTheme) {
        guard let app, t != theme, let cfg = ghostty_config_new() else { return }
        Self.writeConfig(t).withCString { ghostty_config_load_file(cfg, $0) }
        ghostty_config_finalize(cfg)
        ghostty_app_update_config(app, cfg)
        if let p = previous { ghostty_config_free(p) }
        previous = config
        config = cfg
        theme = t
    }

    /// The settings conch gives Ghostty, written where the app keeps its
    /// own files; rewritten each launch, since they are conch's.
    @discardableResult
    static func writeConfig(_ theme: ConchTheme) -> String {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Conch", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let file = dir.appendingPathComponent("ghostty.conf")
        let text = """
        # Written by Conch.app: the terminal in each pane. Rewritten at every launch.
        # The font as conch app's window has it (cmd/conch/app.go), so a pane
        # reads the same here as in the TUI: Ghostty's own JetBrains Mono, 14pt.
        font-size = 14
        \(theme.ghosttyColors)
        window-padding-x = 8
        window-padding-y = 6
        window-padding-balance = true
        adjust-cell-height = 12%
        macos-option-as-alt = left
        confirm-close-surface = false
        shell-integration = none
        term = xterm-256color
        clipboard-read = deny
        mouse-hide-while-typing = true

        """
        try? text.write(to: file, atomically: true, encoding: .utf8)
        return file.path
    }
}

final class GhosttyPaneView: NSView {
    private(set) var surface: ghostty_surface_t?
    var onExit: (() -> Void)?
    private var tracking: NSTrackingArea?

    static func from(_ ud: UnsafeMutableRawPointer?) -> GhosttyPaneView? {
        guard let ud else { return nil }
        return Unmanaged<GhosttyPaneView>.fromOpaque(ud).takeUnretainedValue()
    }

    @MainActor
    init(command: String, environment: [String: String]) {
        super.init(frame: NSRect(x: 0, y: 0, width: 600, height: 400))
        guard let app = GhosttyRuntime.shared.app else { return }
        var cfg = ghostty_surface_config_new()
        cfg.userdata = Unmanaged.passUnretained(self).toOpaque()
        cfg.platform_tag = GHOSTTY_PLATFORM_MACOS
        cfg.platform = ghostty_platform_u(macos: ghostty_platform_macos_s(nsview: Unmanaged.passUnretained(self).toOpaque()))
        cfg.scale_factor = Double(NSScreen.main?.backingScaleFactor ?? 2)
        cfg.font_size = 0
        cfg.context = GHOSTTY_SURFACE_CONTEXT_WINDOW
        // Keys and values kept alive for the call: Ghostty copies them.
        let keys = environment.keys.map { strdup($0) }
        let values = environment.keys.map { strdup(environment[$0]!) }
        defer { (keys + values).forEach { Darwin.free($0) } }
        var env = zip(keys, values).map { ghostty_env_var_s(key: $0, value: $1) }
        surface = command.withCString { cmd in
            env.withUnsafeMutableBufferPointer { buf in
                cfg.command = cmd
                cfg.env_vars = buf.baseAddress
                cfg.env_var_count = buf.count
                return ghostty_surface_new(app, &cfg)
            }
        }
        // Ghostty gives a new surface the configuration the app began with;
        // after a theme change that is the wrong one.
        if let s = surface, let cfg = GhosttyRuntime.shared.config { ghostty_surface_update_config(s, cfg) }
        updateTrackingAreas()
    }

    required init?(coder: NSCoder) { fatalError("not from a nib") }

    func free() {
        if let s = surface { ghostty_surface_free(s) }
        surface = nil
    }

    override var acceptsFirstResponder: Bool { true }

    override func becomeFirstResponder() -> Bool {
        if let s = surface { ghostty_surface_set_focus(s, true) }
        return true
    }

    override func resignFirstResponder() -> Bool {
        if let s = surface { ghostty_surface_set_focus(s, false) }
        return true
    }

    // MARK: size

    override func setFrameSize(_ size: NSSize) {
        super.setFrameSize(size)
        guard let s = surface else { return }
        let px = convertToBacking(NSRect(origin: .zero, size: size)).size
        ghostty_surface_set_size(s, UInt32(max(px.width, 1)), UInt32(max(px.height, 1)))
    }

    override func viewDidChangeBackingProperties() {
        super.viewDidChangeBackingProperties()
        if let w = window {
            CATransaction.begin()
            CATransaction.setDisableActions(true)
            layer?.contentsScale = w.backingScaleFactor
            CATransaction.commit()
        }
        guard let s = surface, frame.width > 0 else { return }
        let fb = convertToBacking(frame)
        ghostty_surface_set_content_scale(s, fb.width / frame.width, fb.height / frame.height)
        setFrameSize(frame.size)
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        viewDidChangeBackingProperties()
        if let s = surface { ghostty_surface_set_occlusion(s, window != nil) }
    }

    // MARK: keys

    static func mods(_ f: NSEvent.ModifierFlags) -> ghostty_input_mods_e {
        var m: UInt32 = GHOSTTY_MODS_NONE.rawValue
        if f.contains(.shift) { m |= GHOSTTY_MODS_SHIFT.rawValue }
        if f.contains(.control) { m |= GHOSTTY_MODS_CTRL.rawValue }
        if f.contains(.option) { m |= GHOSTTY_MODS_ALT.rawValue }
        if f.contains(.command) { m |= GHOSTTY_MODS_SUPER.rawValue }
        if f.contains(.capsLock) { m |= GHOSTTY_MODS_CAPS.rawValue }
        let raw = f.rawValue
        if raw & UInt(NX_DEVICERSHIFTKEYMASK) != 0 { m |= GHOSTTY_MODS_SHIFT_RIGHT.rawValue }
        if raw & UInt(NX_DEVICERCTLKEYMASK) != 0 { m |= GHOSTTY_MODS_CTRL_RIGHT.rawValue }
        if raw & UInt(NX_DEVICERALTKEYMASK) != 0 { m |= GHOSTTY_MODS_ALT_RIGHT.rawValue }
        if raw & UInt(NX_DEVICERCMDKEYMASK) != 0 { m |= GHOSTTY_MODS_SUPER_RIGHT.rawValue }
        return ghostty_input_mods_e(m)
    }

    /// A key as Ghostty's own view builds it (NSEvent+Extension.swift):
    /// control and command never make text, so they are the mods not
    /// consumed; the text of a control key is sent without control, since
    /// Ghostty encodes control itself; function keys send no text.
    private func key(_ event: NSEvent, _ action: ghostty_input_action_e, translation: NSEvent.ModifierFlags? = nil,
                     text: String? = nil, composing: Bool = false) {
        guard let s = surface else { return }
        var k = ghostty_input_key_s()
        k.action = action
        k.keycode = UInt32(event.keyCode)
        k.mods = Self.mods(event.modifierFlags)
        k.consumed_mods = Self.mods((translation ?? event.modifierFlags).subtracting([.control, .command]))
        k.composing = composing
        if event.type == .keyDown || event.type == .keyUp,
           let c = event.characters(byApplyingModifiers: [])?.unicodeScalars.first {
            k.unshifted_codepoint = c.value
        }
        // A control character as text is left to Ghostty to encode, as a
        // key, and so is a lone ASCII control the input method passed on.
        if let text, !text.isEmpty, !(text.count == 1 && (text.unicodeScalars.first?.value ?? 0x20) < 0x20) {
            text.withCString { k.text = $0; _ = ghostty_surface_key(s, k) }
        } else {
            _ = ghostty_surface_key(s, k)
        }
    }

    /// Text the input method committed, typed as it is: no key behind it.
    private func committed(_ text: String) {
        guard let s = surface, !text.isEmpty else { return }
        var k = ghostty_input_key_s()
        k.action = GHOSTTY_ACTION_PRESS
        k.mods = GHOSTTY_MODS_NONE
        k.consumed_mods = GHOSTTY_MODS_NONE
        text.withCString { k.text = $0; _ = ghostty_surface_key(s, k) }
    }

    /// An event's own text, Ghostty's way: a control character without
    /// control (Ghostty encodes control), and none for a function key.
    private static func characters(_ e: NSEvent) -> String? {
        guard let chars = e.characters else { return nil }
        if chars.count == 1, let sc = chars.unicodeScalars.first {
            if sc.value < 0x20 { return e.characters(byApplyingModifiers: e.modifierFlags.subtracting(.control)) }
            if sc.value >= 0xF700 && sc.value <= 0xF8FF { return nil }
        }
        return chars
    }

    // MARK: input methods
    //
    // Keys go through the system's input methods first (interpretKeyEvents),
    // as in any Mac text view: a Japanese or Korean IME composes, a dead key
    // (option-e) waits for the next. What is being composed is Ghostty's
    // preedit, drawn at the cursor; what the input method commits arrives
    // in insertText and is sent as typed text. This follows Ghostty's own
    // view (SurfaceView_AppKit.swift, keyDown and NSTextInputClient).

    /// Text the input method is composing, not yet the program's.
    private var marked = NSMutableAttributedString()
    /// Set while a key is being handled: what the input method committed
    /// during it, sent once it is known whether the key itself is wanted.
    private var accumulated: [String]?

    override func keyDown(with event: NSEvent) {
        guard let s = surface else {
            interpretKeyEvents([event])
            return
        }
        // Left option is alt (macos-option-as-alt = left): Ghostty says which
        // modifiers make text for this key, and the input method is given
        // the key with only those.
        let wanted = Self.flags(ghostty_surface_key_translation_mods(s, Self.mods(event.modifierFlags)))
        var mods = event.modifierFlags
        for f in [NSEvent.ModifierFlags.shift, .control, .option, .command] {
            if wanted.contains(f) { mods.insert(f) } else { mods.remove(f) }
        }
        let translated = mods == event.modifierFlags ? event : NSEvent.keyEvent(
            with: event.type, location: event.locationInWindow, modifierFlags: mods, timestamp: event.timestamp,
            windowNumber: event.windowNumber, context: nil,
            characters: event.characters(byApplyingModifiers: mods) ?? "",
            charactersIgnoringModifiers: event.charactersIgnoringModifiers ?? "",
            isARepeat: event.isARepeat, keyCode: event.keyCode) ?? event
        let action = event.isARepeat ? GHOSTTY_ACTION_REPEAT : GHOSTTY_ACTION_PRESS

        let wasComposing = marked.length > 0
        accumulated = []
        defer { accumulated = nil }
        interpretKeyEvents([translated])
        syncPreedit(clear: wasComposing)
        let composing = marked.length > 0 || wasComposing
        let texts = accumulated ?? []

        if wasComposing && !texts.isEmpty {
            // The key finished a composition: the committed text goes as
            // typed, and the key itself only when it still means something
            // after it — an arrow moves on from the committed word.
            for t in texts where !Self.controlWhileComposing(t, composing) { committed(t) }
            if [123, 124, 125, 126].contains(Int(event.keyCode)) {
                key(event, action, translation: translated.modifierFlags)
            }
            return
        }
        if !texts.isEmpty {
            for t in texts where !Self.controlWhileComposing(t, composing) {
                key(event, action, translation: translated.modifierFlags, text: t)
            }
            return
        }
        if Self.controlWhileComposing(event.characters, composing) { return }
        key(event, action, translation: translated.modifierFlags, text: Self.characters(translated), composing: composing)
    }

    override func keyUp(with event: NSEvent) { key(event, GHOSTTY_ACTION_RELEASE) }

    /// A lone control character while composing belongs to the input
    /// method (backspace over a half-made syllable), not the program.
    private static func controlWhileComposing(_ text: String?, _ composing: Bool) -> Bool {
        guard composing, let text, text.unicodeScalars.count == 1, let sc = text.unicodeScalars.first else { return false }
        return sc.value < 0x20
    }

    static func flags(_ m: ghostty_input_mods_e) -> NSEvent.ModifierFlags {
        var f = NSEvent.ModifierFlags()
        if m.rawValue & GHOSTTY_MODS_SHIFT.rawValue != 0 { f.insert(.shift) }
        if m.rawValue & GHOSTTY_MODS_CTRL.rawValue != 0 { f.insert(.control) }
        if m.rawValue & GHOSTTY_MODS_ALT.rawValue != 0 { f.insert(.option) }
        if m.rawValue & GHOSTTY_MODS_SUPER.rawValue != 0 { f.insert(.command) }
        return f
    }

    fileprivate var markedLength: Int { marked.length }

    fileprivate func setMarked(_ text: NSMutableAttributedString) {
        let had = marked.length > 0
        marked = text
        // Outside a key (a layout switched mid-composition) it shows at once.
        if accumulated == nil && (had || text.length > 0) { syncPreedit() }
    }

    /// Inside a key, committed text waits for the key's handling to finish.
    fileprivate func accumulatedText(_ text: String) -> Bool {
        guard accumulated != nil else { return false }
        accumulated?.append(text)
        return true
    }

    private func syncPreedit(clear: Bool = true) {
        guard let s = surface else { return }
        if marked.length > 0 {
            let str = marked.string
            str.withCString { ghostty_surface_preedit(s, $0, UInt(str.utf8.count)) }
        } else if clear {
            ghostty_surface_preedit(s, nil, 0)
        }
    }

    override func flagsChanged(with event: NSEvent) {
        let bit: UInt32
        switch event.keyCode {
        case 0x38, 0x3C: bit = GHOSTTY_MODS_SHIFT.rawValue
        case 0x3B, 0x3E: bit = GHOSTTY_MODS_CTRL.rawValue
        case 0x3A, 0x3D: bit = GHOSTTY_MODS_ALT.rawValue
        case 0x37, 0x36: bit = GHOSTTY_MODS_SUPER.rawValue
        default: return
        }
        if marked.length > 0 { return } // a modifier while composing is the input method's
        key(event, Self.mods(event.modifierFlags).rawValue & bit != 0 ? GHOSTTY_ACTION_PRESS : GHOSTTY_ACTION_RELEASE)
    }

    /// Control keys reach the terminal rather than the menus (ctrl+/,
    /// ctrl+enter…); ⌘ keys stay the window's — its menu has them first.
    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        guard event.type == .keyDown, window?.firstResponder === self,
              event.modifierFlags.contains(.control), !event.modifierFlags.contains(.command) else { return false }
        keyDown(with: event)
        return true
    }

    @objc func copy(_ sender: Any?) {
        if let s = surface { _ = "copy_to_clipboard".withCString { ghostty_surface_binding_action(s, $0, UInt(strlen($0))) } }
    }

    @objc func paste(_ sender: Any?) {
        if let s = surface { _ = "paste_from_clipboard".withCString { ghostty_surface_binding_action(s, $0, UInt(strlen($0))) } }
    }

    @objc override func selectAll(_ sender: Any?) {
        if let s = surface { _ = "select_all".withCString { ghostty_surface_binding_action(s, $0, UInt(strlen($0))) } }
    }

    // MARK: mouse

    override func updateTrackingAreas() {
        if let t = tracking { removeTrackingArea(t) }
        let t = NSTrackingArea(rect: bounds, options: [.mouseEnteredAndExited, .mouseMoved, .inVisibleRect, .activeAlways], owner: self)
        addTrackingArea(t)
        tracking = t
        super.updateTrackingAreas()
    }

    /// A mouse event's modifiers as Ghostty takes them. Every pane runs
    /// `conch attach`, which asks for mouse reports, so a click is the
    /// program's — and a ⌘-click on a link it printed never reached
    /// Ghostty's link handling. Shift is how Ghostty lets a click past a
    /// program's capture, so ⌘ carries it: a ⌘-click opens the link, a
    /// plain click still goes to the program.
    static func mouseMods(_ f: NSEvent.ModifierFlags) -> ghostty_input_mods_e {
        f.contains(.command) ? mods(f.union(.shift)) : mods(f)
    }

    private func pos(_ e: NSEvent) {
        guard let s = surface else { return }
        let p = convert(e.locationInWindow, from: nil)
        ghostty_surface_mouse_pos(s, p.x, frame.height - p.y, Self.mouseMods(e.modifierFlags))
    }

    private func button(_ e: NSEvent, _ state: ghostty_input_mouse_state_e, _ b: ghostty_input_mouse_button_e) {
        guard let s = surface else { return }
        pos(e)
        _ = ghostty_surface_mouse_button(s, state, b, Self.mouseMods(e.modifierFlags))
    }

    override func mouseDown(with e: NSEvent) {
        window?.makeFirstResponder(self)
        button(e, GHOSTTY_MOUSE_PRESS, GHOSTTY_MOUSE_LEFT)
    }
    override func mouseUp(with e: NSEvent) { button(e, GHOSTTY_MOUSE_RELEASE, GHOSTTY_MOUSE_LEFT) }
    override func rightMouseDown(with e: NSEvent) { button(e, GHOSTTY_MOUSE_PRESS, GHOSTTY_MOUSE_RIGHT) }
    override func rightMouseUp(with e: NSEvent) { button(e, GHOSTTY_MOUSE_RELEASE, GHOSTTY_MOUSE_RIGHT) }
    override func otherMouseDown(with e: NSEvent) { button(e, GHOSTTY_MOUSE_PRESS, GHOSTTY_MOUSE_MIDDLE) }
    override func otherMouseUp(with e: NSEvent) { button(e, GHOSTTY_MOUSE_RELEASE, GHOSTTY_MOUSE_MIDDLE) }
    override func mouseMoved(with e: NSEvent) { pos(e) }
    override func mouseDragged(with e: NSEvent) { pos(e) }
    override func rightMouseDragged(with e: NSEvent) { pos(e) }
    override func mouseEntered(with e: NSEvent) { pos(e) }
    override func mouseExited(with e: NSEvent) {
        guard let s = surface, NSEvent.pressedMouseButtons == 0 else { return }
        ghostty_surface_mouse_pos(s, -1, -1, Self.mods(e.modifierFlags))
    }

    override func scrollWheel(with e: NSEvent) {
        guard let s = surface else { return }
        var x = e.scrollingDeltaX, y = e.scrollingDeltaY
        // Ghostty's packed scroll mods: bit 0 precise, bits 1–3 momentum.
        var m: Int32 = 0
        if e.hasPreciseScrollingDeltas {
            x *= 2
            y *= 2
            m |= 1
        }
        let momentum: Int32
        switch e.momentumPhase {
        case .began: momentum = 1
        case .stationary: momentum = 2
        case .changed: momentum = 3
        case .ended: momentum = 4
        case .cancelled: momentum = 5
        case .mayBegin: momentum = 6
        default: momentum = 0
        }
        m |= momentum << 1
        ghostty_surface_mouse_scroll(s, x, y, ghostty_input_scroll_mods_t(m))
    }

    // MARK: clipboard

    static func readClipboard(_ ud: UnsafeMutableRawPointer?, _ loc: ghostty_clipboard_e, _ state: UnsafeMutableRawPointer?,
                              _ mimes: UnsafePointer<UnsafePointer<CChar>?>?, _ n: Int, _ list: Bool) -> ghostty_clipboard_read_result_e {
        guard loc == GHOSTTY_CLIPBOARD_STANDARD, let v = from(ud), let s = v.surface,
              let text = NSPasteboard.general.string(forType: .string) else { return GHOSTTY_CLIPBOARD_READ_UNAVAILABLE }
        let data = Array(text.utf8)
        "text/plain".withCString { mime in
            data.withUnsafeBufferPointer { buf in
                buf.baseAddress!.withMemoryRebound(to: CChar.self, capacity: max(buf.count, 1)) { ptr in
                    var content = ghostty_clipboard_content_s(mime: mime, data: ptr, len: buf.count)
                    withUnsafePointer(to: &content) { cp in
                        var done = ghostty_clipboard_complete_s(contents: cp, contents_len: 1, available: nil, available_len: 0,
                                                                confirmed: false, remember: false)
                        ghostty_surface_complete_clipboard_request(s, &done, state)
                    }
                }
            }
        }
        return GHOSTTY_CLIPBOARD_READ_STARTED
    }

    static func writeClipboard(_ loc: ghostty_clipboard_e, _ content: UnsafePointer<ghostty_clipboard_content_s>?, _ len: Int) {
        guard loc == GHOSTTY_CLIPBOARD_STANDARD, let content else { return }
        for i in 0..<len where String(cString: content[i].mime) == "text/plain" {
            let c = content[i]
            let text = String(decoding: UnsafeRawBufferPointer(start: c.data, count: c.len), as: UTF8.self)
            DispatchQueue.main.async {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(text, forType: .string)
            }
        }
    }
}

extension GhosttyPaneView: NSTextInputClient {
    func insertText(_ string: Any, replacementRange: NSRange) {
        let text: String
        switch string {
        case let v as NSAttributedString: text = v.string
        case let v as String: text = v
        default: return
        }
        unmarkText()
        if accumulatedText(text) { return }
        committed(text) // not inside a key: dictation, the character viewer
    }

    func setMarkedText(_ string: Any, selectedRange: NSRange, replacementRange: NSRange) {
        switch string {
        case let v as NSAttributedString: setMarked(NSMutableAttributedString(attributedString: v))
        case let v as String: setMarked(NSMutableAttributedString(string: v))
        default: return
        }
    }

    func unmarkText() { setMarked(NSMutableAttributedString()) }
    func hasMarkedText() -> Bool { markedLength > 0 }
    func markedRange() -> NSRange { markedLength > 0 ? NSRange(location: 0, length: markedLength) : NSRange(location: NSNotFound, length: 0) }
    func selectedRange() -> NSRange { NSRange(location: NSNotFound, length: 0) }
    func validAttributesForMarkedText() -> [NSAttributedString.Key] { [] }
    func attributedSubstring(forProposedRange range: NSRange, actualRange: NSRangePointer?) -> NSAttributedString? { nil }
    func characterIndex(for point: NSPoint) -> Int { 0 }

    /// Where the input method's candidate window goes: the cursor, as
    /// Ghostty reports it, in screen coordinates.
    func firstRect(forCharacterRange range: NSRange, actualRange: NSRangePointer?) -> NSRect {
        var x = 0.0, y = 0.0, w = 0.0, h = 0.0
        if let s = surface { ghostty_surface_ime_point(s, &x, &y, &w, &h) }
        let r = convert(NSRect(x: x, y: frame.height - y, width: range.length == 0 ? 0 : w, height: max(h, 16)), to: nil)
        return window?.convertToScreen(r) ?? r
    }

    /// Keys the input method does not turn into text (moveLeft:,
    /// insertNewline:…) are sent as keys by keyDown; this only keeps
    /// AppKit from beeping at them.
    override func doCommand(by selector: Selector) {}
}
#endif
