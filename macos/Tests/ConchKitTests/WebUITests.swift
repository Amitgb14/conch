import Foundation
import Testing
import WebKit
@testable import ConchKit

// conch-ui in Conch.app: the page every view is drawn on, the script and
// the messages, and — through ConchUIPage — each view in a real web view,
// set up the way ConchUIHost sets it up, its buttons clicked as a person
// would and what it says back read.

/// A view in a real web view, the way the app hosts it: conch-ui in a
/// world of its own with the handler. What it says is collected, and
/// scripts are run in either world.
@MainActor
final class ConchUIPage: NSObject, WKScriptMessageHandler {
    let web: WKWebView
    let world = WKContentWorld.world(name: webWorldName)
    var said: [[String: Any]] = []
    /// What reached the handler from the page's own world.
    var fromPage = 0

    init(_ kind: WebViewKind, width: CGFloat = 1100) {
        let c = WKWebViewConfiguration()
        c.websiteDataStore = .nonPersistent()
        web = WKWebView(frame: NSRect(x: 0, y: 0, width: width, height: 800), configuration: c)
        super.init()
        c.userContentController.addUserScript(WKUserScript(source: webBootScript(kind), injectionTime: .atDocumentEnd, forMainFrameOnly: true, in: world))
        c.userContentController.add(self, contentWorld: world, name: "conch")
        web.loadHTMLString(webPage, baseURL: nil)
    }

    func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        if message.world != world { fromPage += 1 }
        if let m = message.body as? [String: Any] { said.append(m) }
    }

    /// How long a view is given to say something. A page boots in under a
    /// second alone, but these tests run side by side, each starting a web
    /// content process and parsing conch-ui, and on a machine running other
    /// suites one took over 10 s; a view that never answers still fails.
    static let patience: TimeInterval = 30

    /// Waits, turning the main run loop, until the view has said n things.
    func await(_ n: Int) async throws {
        let until = Date().addingTimeInterval(Self.patience)
        while said.count < n, Date() < until { try await Task.sleep(nanoseconds: 20_000_000) }
        try #require(said.count >= n, "the view said \(said.count) of \(n): \(said)")
    }

    /// A script in conch-ui's world, as the app sends a view its state.
    func run(_ js: String) async throws -> Any? { try await web.evaluateJavaScript(js, in: nil, contentWorld: world) }
    /// A script in the page's own world: what would run had anything got
    /// past React's escaping.
    func inPage(_ js: String) async throws -> Any? { try await web.evaluateJavaScript(js, in: nil, contentWorld: .page) }

    func show<T: Encodable>(_ state: T) async throws { _ = try await run(webUpdateScript(state)) }

    func text() async throws -> String { try #require(try await run("document.getElementById('root').innerText") as? String) }

    /// Clicks the first button (or tab, menu item or label) whose text is label,
    /// else whose text holds it; shift held if asked.
    func click(_ label: String, shift: Bool = false) async throws {
        let js = """
            (function (label, shift) {
              var all = Array.prototype.slice.call(document.querySelectorAll('button, [role=tab], [role=menuitem], label'));
              var el = all.find(function (b) { return b.textContent.trim() === label || b.getAttribute('aria-label') === label; })
                    || all.find(function (b) { return b.textContent.indexOf(label) >= 0; });
              if (!el) return false;
              el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, shiftKey: shift }));
              return true;
            })(\(jsString(label)), \(shift))
            """
        let ok = try await run(js) as? Bool
        try #require(ok == true, "no button \(label)")
    }

    /// Types into the text box with this label, as React hears typing, and
    /// leaves it, which hands the words over.
    func type(_ label: String, _ text: String, thenPress key: String? = nil, meta: Bool = false) async throws {
        let js = """
            (function (label, text, key, meta) {
              var el = document.querySelector('textarea[aria-label="' + label + '"]');
              if (!el) return false;
              el.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
              Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(el, text);
              el.dispatchEvent(new Event('input', { bubbles: true }));
              if (key) el.dispatchEvent(new KeyboardEvent('keydown', { key: key, metaKey: meta, bubbles: true, cancelable: true }));
              else el.dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
              return true;
            })(\(jsString(label)), \(jsString(text)), \(key.map(jsString) ?? "null"), \(meta))
            """
        let ok = try await run(js) as? Bool
        try #require(ok == true, "no text box \(label)")
    }

    /// The messages said since the first n, as their types.
    func types(from n: Int) -> [String] { said.dropFirst(n).compactMap { $0["type"] as? String } }
}

func jsString(_ s: String) -> String {
    String(data: try! JSONEncoder().encode(s), encoding: .utf8)!
}

// MARK: the page and the script

@Test func pageRunsNothingOfItsOwn() {
    #expect(webPage.contains("Content-Security-Policy"))
    #expect(webPage.contains("default-src 'none'"))
    #expect(!webPage.contains("<script") && !webPage.contains("script-src"))
    #expect(!webPage.contains("http://") && !webPage.contains("https://"))
    // The styles are conch-ui's, inline.
    #expect(webPage.contains(String(conchUIStyle.prefix(40))))
}

@Test func bootMountsTheViewItNames() {
    for k in WebViewKind.allCases {
        let s = webBootScript(k)
        #expect(s.hasPrefix(conchUIScript))
        #expect(s.hasSuffix(";window.conchUI && window.conchUI.mount(\"\(k.rawValue)\");\n"))
    }
    #expect(conchUIVersion.count == 12)
}

@Test func updateScriptIsOneCall() {
    struct S: Encodable { var t: String }
    let js = webUpdateScript(S(t: "line\u{2028}sep\u{2029}end </script>"))
    #expect(js.hasPrefix("window.conchUI && window.conchUI.update("))
    #expect(js.hasSuffix(");"))
    // Separators JavaScript once refused inside a string are escaped.
    #expect(!js.contains("\u{2028}") && !js.contains("\u{2029}"))
    #expect(js.contains("\\u2028") && js.contains("\\u2029"))
}

@Test func messagesNameTheirView() {
    let m = webMessage(["view": "changes", "type": "select", "path": "a.go", "committed": false, "line": 3, "half": 2.5, "big": 1e12])
    #expect(m?.view == .changes && m?.type == "select")
    #expect(m?.text("path") == "a.go" && m?.bool("committed") == false)
    #expect(m?.int("line") == 3)
    #expect(m?.int("half") == nil && m?.int("big") == nil, "a fraction or a huge number is not a line")
    #expect(m?.int("committed") == nil && m?.bool("line") == nil, "a boolean is not a number, nor the other way")
    #expect(webMessage(["view": "nothing", "type": "x"]) == nil)
    #expect(webMessage(["view": "changes", "type": ""]) == nil)
    #expect(webMessage(["type": "ready"]) == nil)
    #expect(webMessage("ready") == nil)
}

// MARK: in WebKit

@MainActor @Test func everyViewMountsAndAsks() async throws {
    for k in WebViewKind.allCases {
        let page = ConchUIPage(k)
        try await page.await(1)
        #expect(page.said.first?["view"] as? String == k.rawValue && page.said.first?["type"] as? String == "ready")
        #expect(page.fromPage == 0)
    }
}

@MainActor @Test func bridgeIsOutOfThePagesReach() async throws {
    let page = ConchUIPage(.inspector)
    try await page.await(1)
    // The page's own world cannot see the handler or conch-ui.
    #expect(try await page.inPage("typeof (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.conch)") as? String == "undefined")
    #expect(try await page.inPage("typeof window.conchUI") as? String == "undefined")
    // Markup that got past the escaping runs nothing: its policy allows no
    // script, inline handlers included.
    _ = try await page.inPage("""
        document.getElementById('root').insertAdjacentHTML('beforeend',
          '<img src="data:," onerror="document.title=1"><svg onload="document.title=2"></svg><a id="j" href="javascript:document.title=3">x</a>');
        document.getElementById('j').click();
        true
        """)
    try await Task.sleep(nanoseconds: 300_000_000)
    #expect(try await page.inPage("document.title") as? String == "")
    #expect(page.fromPage == 0 && page.said.count == 1)
}

@MainActor @Test func pageFetchesNothing() async throws {
    // The policy holds in WebKit, not only in the text: WebKit itself
    // reports each request the page tried, before any request is made.
    // Loopback's discard port, so a broken policy still reaches nothing.
    let page = ConchUIPage(.inspector)
    try await page.await(1)
    _ = try await page.inPage("""
        window.seen = [];
        document.addEventListener('securitypolicyviolation', function (e) { window.seen.push(e.effectiveDirective); });
        fetch('https://127.0.0.1:9/').catch(function () {});
        new Image().src = 'https://127.0.0.1:9/x.png';
        var s = document.createElement('script'); s.src = 'https://127.0.0.1:9/x.js'; document.body.appendChild(s);
        true
        """)
    var seen = ""
    let until = Date().addingTimeInterval(ConchUIPage.patience)
    while Date() < until {
        seen = try await page.inPage("window.seen.slice().sort().join(',')") as? String ?? ""
        if seen.split(separator: ",").count >= 3 { break }
        try await Task.sleep(nanoseconds: 20_000_000)
    }
    #expect(seen == "connect-src,img-src,script-src-elem" || seen == "connect-src,img-src,script-src", "\(seen)")
}

// The app's side has no test target, so it is read, as BrowserView is.
@Test func hostKeepsThePageToItself() throws {
    let path = (#filePath as NSString).deletingLastPathComponent + "/../../Sources/ConchApp/ConchUIHost.swift"
    let code = try String(contentsOfFile: path, encoding: .utf8)
    #expect(code.contains("websiteDataStore = .nonPersistent()"), "nothing the page does is kept")
    #expect(code.contains("message.world == webWorld, message.frameInfo.isMainFrame"), "only its world, only the main frame")
    #expect(code.contains("let m = webMessage(message.body), m.view == kind"), "and only its own view")
    #expect(code.contains("decisionHandler(first ? .allow : .cancel)"), "it navigates nowhere")
    #expect(!code.contains("isInspectable = true"))
}

// The bundle in ConchKit is a build of ui/: it says what made it.
@Test func bundleIsGenerated() throws {
    let path = (#filePath as NSString).deletingLastPathComponent + "/../../Sources/ConchKit/ConchUIBundle.swift"
    let code = try String(contentsOfFile: path, encoding: .utf8)
    #expect(code.hasPrefix("// Generated by ui/scripts/swift.mjs"))
    #expect(conchUIScript.contains("conchUI") && conchUIScript.count > 10_000)
    #expect(!conchUIScript.contains("dangerouslySetInnerHTML") || !uiSourcesUseRawHTML())
}

/// Whether ui/src sets raw HTML anywhere: React escapes everything else.
func uiSourcesUseRawHTML() -> Bool {
    let dir = (#filePath as NSString).deletingLastPathComponent + "/../../../ui/src"
    guard let walk = FileManager.default.enumerator(atPath: dir) else { return false }
    for case let f as String in walk where f.hasSuffix(".tsx") || f.hasSuffix(".ts") || f.hasSuffix(".mjs") {
        if let s = try? String(contentsOfFile: dir + "/" + f, encoding: .utf8),
           s.contains("dangerouslySetInnerHTML") || s.contains("innerHTML") || s.contains("insertAdjacentHTML") { return true }
    }
    return false
}

@Test func viewsNeverSetRawHTML() {
    #expect(!uiSourcesUseRawHTML(), "conch-ui draws everything through React, which escapes it")
}
