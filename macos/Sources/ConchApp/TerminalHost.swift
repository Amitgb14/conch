import AppKit
import ConchKit
import SwiftTerm
import SwiftUI

// The one seam between the window and the terminal in a pane. Each pane
// is a terminal running `conch attach PANE`: conch's server is the pane's
// real terminal, and this one shows the screen it keeps. Today it is
// SwiftTerm; Ghostty's (libghostty) takes its place here once it can be
// built, and nothing else changes.

/// SwiftTerm's view. Which pane has the keyboard is followed by the
/// window watching clicks (Store.watchClicks), since SwiftTerm does not
/// let becomeFirstResponder be overridden.
final class FocusingTerminalView: LocalProcessTerminalView {}

@MainActor
final class PaneTerminal: NSObject, ObservableObject, LocalProcessTerminalViewDelegate {
    let pane: String
    let view: FocusingTerminalView
    @Published var ended: String?

    init(pane: String, binary: String?, store: Store) {
        self.pane = pane
        view = FocusingTerminalView(frame: NSRect(x: 0, y: 0, width: 600, height: 400))
        super.init()
        view.processDelegate = self
        view.font = NSFont(name: "JetBrains Mono", size: 12.5) ?? .monospacedSystemFont(ofSize: 12.5, weight: .regular)
        view.nativeBackgroundColor = Theme.terminal
        view.nativeForegroundColor = Theme.fg
        view.optionAsMetaKey = true
        guard let binary else {
            view.feed(text: "conch is not installed where this app can find it.\r\nSet CONCH_BIN, or install conch.\r\n")
            return
        }
        let env = conchEnvironment().map { "\($0.key)=\($0.value)" }
        view.startProcess(executable: binary, args: ["attach", pane], environment: env)
    }

    func stop() {
        view.processDelegate = nil
        view.terminate()
    }

    // MARK: LocalProcessTerminalViewDelegate

    nonisolated func sizeChanged(source: LocalProcessTerminalView, newCols: Int, newRows: Int) {}
    nonisolated func setTerminalTitle(source: LocalProcessTerminalView, title: String) {}
    nonisolated func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?) {}
    nonisolated func processTerminated(source: TerminalView, exitCode: Int32?) {
        MainActor.assumeIsolated { self.ended = "detached" }
    }
}

/// A pane's terminal in SwiftUI. The view is the store's, so moving a
/// pane to another split moves its terminal rather than starting another.
struct TerminalHost: NSViewRepresentable {
    let terminal: PaneTerminal

    func makeNSView(context: Context) -> NSView {
        let box = NSView()
        box.wantsLayer = true
        box.layer?.backgroundColor = Theme.terminal.cgColor
        mount(in: box)
        return box
    }

    func updateNSView(_ box: NSView, context: Context) {
        if terminal.view.superview !== box { mount(in: box) }
    }

    private func mount(in box: NSView) {
        let v = terminal.view
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
