import AppKit
import ConchKit
import SwiftUI

// The Files tab's editor: AppKit's text view, set up for code — the
// window's monospace face, a gutter of line numbers, the line the caret is
// on lit, the theme's colours for comments, strings, numbers and keywords,
// undo, the find bar (⌘F), and ⌘S to save. No completion, no language
// server: it is for a quick fix beside an agent, not a second IDE.

struct CodeEditor: NSViewRepresentable {
    @Binding var text: String
    let language: CodeLanguage
    /// The theme (and text size) the editor is drawn in: a change redraws it.
    @Environment(\.conchTheme) private var look
    let fontSize: CGFloat
    /// Off for a file that can't be saved: still selectable and searchable.
    var editable = true
    var onSave: () -> Void
    /// Where the selection is, for an agent asked about it.
    var selection: Binding<NSRange>? = nil
    /// Bumped to show the find bar — its replace field too with replace.
    var findRequest = 0
    var replace = false
    /// A line to select and scroll to; its token changes to ask again.
    var reveal: (line: Int, token: Int)? = nil

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = true
        scroll.autohidesScrollers = true
        scroll.drawsBackground = true
        scroll.clipsToBounds = true
        let tv = CodeTextView()
        tv.onSave = { context.coordinator.parent.onSave() }
        tv.isRichText = false
        tv.importsGraphics = false
        tv.allowsUndo = true
        tv.usesFindBar = true
        tv.isIncrementalSearchingEnabled = true
        // Code is not prose: no curly quotes, no dashes, no spelling fixes.
        tv.isAutomaticQuoteSubstitutionEnabled = false
        tv.isAutomaticDashSubstitutionEnabled = false
        tv.isAutomaticTextReplacementEnabled = false
        tv.isAutomaticSpellingCorrectionEnabled = false
        tv.isContinuousSpellCheckingEnabled = false
        tv.isGrammarCheckingEnabled = false
        tv.isAutomaticLinkDetectionEnabled = false
        tv.smartInsertDeleteEnabled = false
        // Long lines run on, as in a terminal, the view scrolling sideways.
        tv.isHorizontallyResizable = true
        tv.isVerticallyResizable = true
        tv.autoresizingMask = [.width]
        tv.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        tv.textContainer?.widthTracksTextView = false
        tv.textContainer?.containerSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        tv.textContainerInset = NSSize(width: 6, height: 10)
        tv.delegate = context.coordinator
        tv.string = text
        tv.isEditable = editable
        scroll.documentView = tv
        let ruler = LineNumberRuler(textView: tv)
        scroll.verticalRulerView = ruler
        scroll.hasVerticalRuler = true
        scroll.rulersVisible = true
        context.coordinator.textView = tv
        context.coordinator.style(scroll)
        context.coordinator.recolour()
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        let c = context.coordinator
        c.parent = self
        guard let tv = c.textView else { return }
        if tv.isEditable != editable { tv.isEditable = editable }
        if tv.string != text {
            // Read again from disk, or another file: keep the caret near where it was.
            let sel = tv.selectedRange()
            tv.string = text
            tv.setSelectedRange(NSRange(location: min(sel.location, (text as NSString).length), length: 0))
            c.recolour()
            (scroll.verticalRulerView as? LineNumberRuler)?.fit()
        }
        if c.drawn != look + "|\(fontSize)|\(language.rawValue)" {
            c.style(scroll)
            c.recolour()
        }
        if findRequest != c.findShown {
            c.findShown = findRequest
            let item = NSMenuItem()
            item.tag = Int((replace ? NSTextFinder.Action.showReplaceInterface : .showFindInterface).rawValue)
            DispatchQueue.main.async {
                tv.window?.makeFirstResponder(tv)
                tv.performTextFinderAction(item)
            }
        }
        if let r = reveal, r.token != c.revealed {
            c.revealed = r.token
            let range = lineRange(tv.string as NSString, line: r.line)
            DispatchQueue.main.async {
                tv.setSelectedRange(range)
                tv.scrollRangeToVisible(range)
                tv.showFindIndicator(for: range)
            }
        }
    }

    @MainActor final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: CodeEditor
        weak var textView: CodeTextView?
        var drawn = ""
        var findShown = 0
        var revealed = 0
        private var pending: DispatchWorkItem?

        init(_ p: CodeEditor) {
            parent = p
            findShown = p.findRequest
            revealed = p.reveal?.token ?? 0
            // A file opened at a line (a search result) goes there at once.
            if p.reveal != nil { revealed = -1 }
        }

        func style(_ scroll: NSScrollView) {
            guard let tv = textView else { return }
            drawn = parent.look + "|\(parent.fontSize)|\(parent.language.rawValue)"
            let t = Theme.current
            let font = NSFont.monospacedSystemFont(ofSize: parent.fontSize, weight: .regular)
            tv.font = font
            tv.typingAttributes = [.font: font, .foregroundColor: Theme.ns(t.fg)]
            tv.backgroundColor = Theme.ns(t.bg)
            tv.insertionPointColor = Theme.ns(t.line)
            tv.selectedTextAttributes = [.backgroundColor: Theme.ns(t.accent).withAlphaComponent(0.35)]
            tv.lineColor = Theme.ns(t.fg).withAlphaComponent(t.light ? 0.05 : 0.045)
            scroll.backgroundColor = Theme.ns(t.bg)
            if let r = scroll.verticalRulerView as? LineNumberRuler {
                r.font = NSFont.monospacedDigitSystemFont(ofSize: max(9, parent.fontSize - 2), weight: .regular)
                r.textColor = Theme.ns(t.muted)
                r.current = Theme.ns(t.fg).withAlphaComponent(0.85)
                r.background = Theme.ns(t.sidebar)
                r.rule = Theme.ns(t.border)
                r.fit()
                r.needsDisplay = true
            }
            tv.needsDisplay = true
        }

        /// The colours of the text, from its language and the theme.
        func recolour() {
            guard let tv = textView, let store = tv.textStorage else { return }
            let t = Theme.current
            let all = NSRange(location: 0, length: store.length)
            store.beginEditing()
            store.setAttributes([.font: tv.font ?? NSFont.monospacedSystemFont(ofSize: parent.fontSize, weight: .regular),
                                 .foregroundColor: Theme.ns(t.fg)], range: all)
            for run in highlight(store.string, parent.language) where NSMaxRange(run.range) <= store.length {
                let colour: NSColor
                switch run.token {
                case .comment: colour = Theme.ns(t.muted)
                case .string: colour = Theme.ns(t.ok)
                case .number: colour = Theme.ns(t.warn)
                case .keyword: colour = Theme.ns(t.work)
                case .heading: colour = Theme.ns(t.line)
                }
                store.addAttribute(.foregroundColor, value: colour, range: run.range)
            }
            store.endEditing()
        }

        func textDidChange(_ note: Notification) {
            guard let tv = textView else { return }
            parent.text = tv.string
            // Coloured a moment after the typing stops, not at every key.
            pending?.cancel()
            let work = DispatchWorkItem { [weak self] in self?.recolour() }
            pending = work
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15, execute: work)
            (tv.enclosingScrollView?.verticalRulerView as? LineNumberRuler)?.fit()
            tv.enclosingScrollView?.verticalRulerView?.needsDisplay = true
        }

        func textViewDidChangeSelection(_ note: Notification) {
            if let tv = textView, let sel = parent.selection, sel.wrappedValue != tv.selectedRange() {
                let r = tv.selectedRange()
                DispatchQueue.main.async { sel.wrappedValue = r }
            }
            textView?.needsDisplay = true
            textView?.enclosingScrollView?.verticalRulerView?.needsDisplay = true
        }
    }
}

/// The text view: the caret's line lit behind the text, and ⌘S to save
/// even before SwiftUI's shortcut sees it.
final class CodeTextView: NSTextView {
    var onSave: (() -> Void)?
    var lineColor = NSColor.clear

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        if event.modifierFlags.intersection(.deviceIndependentFlagsMask) == .command, event.charactersIgnoringModifiers == "s" {
            onSave?()
            return true
        }
        return super.performKeyEquivalent(with: event)
    }

    override func drawBackground(in rect: NSRect) {
        super.drawBackground(in: rect)
        guard let lm = layoutManager, let tc = textContainer, selectedRange().length == 0 else { return }
        let ns = string as NSString
        let caret = min(selectedRange().location, ns.length)
        var lineRect: NSRect
        if ns.length == 0 || caret == ns.length && (ns.length == 0 || ns.character(at: ns.length - 1) == 10) {
            lineRect = lm.extraLineFragmentRect
        } else {
            let glyph = lm.glyphIndexForCharacter(at: min(caret, max(ns.length - 1, 0)))
            lineRect = lm.lineFragmentRect(forGlyphAt: glyph, effectiveRange: nil)
        }
        _ = tc
        lineRect.origin.x = 0
        lineRect.size.width = bounds.width
        lineRect.origin.y += textContainerOrigin.y
        lineColor.setFill()
        lineRect.fill()
    }
}

/// The gutter: a number for each line beside it, the caret's brighter.
final class LineNumberRuler: NSRulerView {
    var font = NSFont.monospacedDigitSystemFont(ofSize: 11, weight: .regular)
    var textColor = NSColor.secondaryLabelColor
    var current = NSColor.labelColor
    var background = NSColor.clear
    var rule = NSColor.separatorColor

    init(textView: NSTextView) {
        super.init(scrollView: textView.enclosingScrollView, orientation: .verticalRuler)
        clientView = textView
        ruleThickness = 44
        // Views stopped clipping to their bounds by default (macOS 14): the
        // gutter's rule was drawn up through the header above the editor.
        clipsToBounds = true
        NotificationCenter.default.addObserver(self, selector: #selector(redraw), name: NSView.boundsDidChangeNotification, object: nil)
    }

    required init(coder: NSCoder) { fatalError("not used") }

    @objc private func redraw() { needsDisplay = true }

    /// Wide enough for the last line's number, settled here rather than
    /// while drawing: a width changed in the middle of drawing could leave
    /// the text where the old width put it, its first letters under the
    /// numbers until the window was resized. The scroll view is laid out
    /// again at once so the text moves with it.
    func fit() {
        guard let tv = clientView as? NSTextView else { return }
        let digit = ("8" as NSString).size(withAttributes: [.font: font]).width
        let want = gutterWidth(lines: editorLines(tv.string), digit: digit)
        guard abs(ruleThickness - want) > 0.5 else { return }
        ruleThickness = want
        scrollView?.tile()
        needsDisplay = true
    }

    override func drawHashMarksAndLabels(in rect: NSRect) {
        guard let tv = clientView as? NSTextView, let lm = tv.layoutManager, let tc = tv.textContainer else { return }
        let area = rect.intersection(visibleRect)
        background.setFill()
        area.fill()
        rule.setFill()
        NSRect(x: bounds.maxX - 1, y: area.minY, width: 1, height: area.height).fill()
        let ns = tv.string as NSString
        let visible = tv.visibleRect
        let glyphs = lm.glyphRange(forBoundingRect: visible, in: tc)
        let firstChar = lm.characterIndexForGlyph(at: glyphs.location)
        // The line number of the first visible character.
        var line = 1
        ns.enumerateSubstrings(in: NSRange(location: 0, length: firstChar), options: [.byLines, .substringNotRequired]) { _, _, _, _ in line += 1 }
        if firstChar > 0, ns.character(at: firstChar - 1) != 10 { line -= 1 }
        let caretLine = ns.substring(to: min(tv.selectedRange().location, ns.length)).filter { $0 == "\n" }.count + 1
        let inset = tv.textContainerOrigin.y
        var char = firstChar
        while char < NSMaxRange(lm.characterRange(forGlyphRange: glyphs, actualGlyphRange: nil)) || (char == ns.length && char == firstChar) {
            let lineRange = ns.lineRange(for: NSRange(location: char, length: 0))
            let glyph = lm.glyphIndexForCharacter(at: min(char, max(ns.length - 1, 0)))
            var r = lm.lineFragmentRect(forGlyphAt: glyph, effectiveRange: nil)
            if ns.length == 0 { r = lm.extraLineFragmentRect }
            let y = r.minY + inset - visible.minY
            draw(line, y: y, height: r.height, on: line == caretLine)
            line += 1
            if NSMaxRange(lineRange) <= char { break }
            char = NSMaxRange(lineRange)
            if char >= ns.length { break }
        }
        // The empty last line after a final newline gets its number too.
        if ns.length > 0, ns.character(at: ns.length - 1) == 10 {
            let r = lm.extraLineFragmentRect
            if r.height > 0 { draw(line, y: r.minY + inset - visible.minY, height: r.height, on: line == caretLine) }
        }
    }

    private func draw(_ n: Int, y: CGFloat, height: CGFloat, on: Bool) {
        let s = NSAttributedString(string: "\(n)", attributes: [.font: font, .foregroundColor: on ? current : textColor])
        let size = s.size()
        s.draw(at: NSPoint(x: ruleThickness - size.width - 10, y: y + (height - size.height) / 2))
    }
}

