import Foundation

// The views Conch.app draws with HTML (the hybrid design: the window and
// terminals native, rich views from conch-ui, ui/ in the repository, the
// same views a Linux and Windows app would draw). One page for all of
// them, which loads and runs nothing of its own: its policy allows no
// script, inline handlers included, and no network. conch-ui's script
// runs in a content world apart from the page's (webWorldName), as the
// browser's error watcher does, with the one message handler: markup that
// got past React's escaping could neither run nor reach the app. Each
// view is sent the whole of what it shows as JSON (webUpdateScript), and
// what it asks back is checked by that view's intent parser before the
// app acts on it.

/// A view conch-ui draws.
public enum WebViewKind: String, Sendable, CaseIterable {
    case inspector, changes, agents
}

/// The content world conch-ui's script and its message handler share.
public let webWorldName = "conch-ui"

/// The page every view is drawn on: conch-ui's styles and a root, and a
/// policy that lets it load and run nothing.
public let webPage = """
<!doctype html>
<html><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">
<style>
\(conchUIStyle)
</style></head>
<body><div id="root"></div></body></html>
"""

/// The script the host adds to the view's world: conch-ui, then the view
/// mounted on the page.
public func webBootScript(_ kind: WebViewKind) -> String {
    conchUIScript + "\n;window.conchUI && window.conchUI.mount(\"\(kind.rawValue)\");\n"
}

/// The script that hands a view what it shows: one call, the state as a
/// JSON literal. The line and paragraph separators are escaped, which
/// older JavaScript refused inside a string.
public func webUpdateScript<T: Encodable>(_ state: T) -> String {
    let data = (try? JSONEncoder().encode(state)) ?? Data("{}".utf8)
    let json = String(decoding: data, as: UTF8.self)
        .replacingOccurrences(of: "\u{2028}", with: "\\u2028")
        .replacingOccurrences(of: "\u{2029}", with: "\\u2029")
    return "window.conchUI && window.conchUI.update(\(json));"
}

/// A message from a view, as far as every view agrees: which view sent it
/// and what it asks. The rest is each view's parser's to check.
public struct WebMessage: @unchecked Sendable {
    public let view: WebViewKind
    public let type: String
    public let fields: [String: Any]

    /// A field that is a non-empty string.
    public func text(_ k: String) -> String? { (fields[k] as? String).flatMap { $0.isEmpty ? nil : $0 } }
    /// A field that is a string, empty or not.
    public func string(_ k: String) -> String? { fields[k] as? String }
    /// A field that is a whole number (JavaScript sends every number as a double).
    public func int(_ k: String) -> Int? {
        guard let n = fields[k] as? NSNumber, CFGetTypeID(n) != CFBooleanGetTypeID() else { return nil }
        let d = n.doubleValue
        return d.rounded() == d && abs(d) < 1e9 ? Int(d) : nil
    }
    public func bool(_ k: String) -> Bool? {
        guard let n = fields[k] as? NSNumber, CFGetTypeID(n) == CFBooleanGetTypeID() else { return nil }
        return n.boolValue
    }
    /// A link the app may open: http or https, with a host.
    public func url(_ k: String) -> URL? {
        guard let s = text(k), let u = URL(string: s), let scheme = u.scheme?.lowercased(),
              scheme == "https" || scheme == "http", u.host?.isEmpty == false else { return nil }
        return u
    }
}

/// The message a view posted (WKScriptMessage's body), or nil for
/// anything that is not one.
public func webMessage(_ body: Any) -> WebMessage? {
    guard let m = body as? [String: Any], let v = m["view"] as? String, let view = WebViewKind(rawValue: v),
          let type = m["type"] as? String, !type.isEmpty else { return nil }
    return WebMessage(view: view, type: type, fields: m)
}

/// The view's colours: the theme's, on the sidebar's surface so a view
/// reads as the window's own rather than a page pasted in.
public struct WebColors: Encodable, Equatable, Sendable {
    public var bg, fg, muted, border, raised, accent, ok, warn, err, work: String
    public var light: Bool

    public init(_ t: ConchTheme) {
        bg = t.sidebar.hex
        fg = t.fg.hex
        muted = t.muted.hex
        border = t.border.hex
        raised = t.raised.hex
        accent = t.line.hex
        ok = t.ok.hex
        warn = t.warn.hex
        err = t.err.hex
        work = t.work.hex
        light = t.light
    }
}
