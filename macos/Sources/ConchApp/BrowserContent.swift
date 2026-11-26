import ConchKit
import WebKit

// What a tab's page is given besides itself: the error watcher behind the
// errors chip, and the rule that blocks images when Disable Images is on.
//
// The watcher is the one script conch adds to a page, and the one way
// anything comes back from one. It is one-way and read-only, and only
// this file may set it up (a test says so):
// - It runs in a content world of its own (WKContentWorld "conch-errors"),
//   not the page's: the page cannot see it, call it or reach its message
//   handler, and it sees the page's DOM and events but none of the page's
//   script values.
// - Its handler takes messages and answers none (a WKScriptMessageHandler,
//   never one with a reply), and exposes nothing of conch: no token, no
//   socket, no path.
// - What it posts is checked as data (PageIssue.fromScript): a known kind,
//   text cut short, at most 200 a page load in the script and 200 kept.
// - A private tab has none of it.
// What it hears: uncaught errors, unhandled promise rejections, images,
// scripts and stylesheets that failed to load, and Content-Security-Policy
// blocks. A page's own console.error calls are not among them: catching
// those would mean a script in the page's own world, which it could see
// and talk to.

/// The world the error watcher runs in.
let browserIssueWorld = WKContentWorld.world(name: "conch-errors")
private let issueHandlerName = "issues"

private let issueWatcher = """
(() => {
  const handler = window.webkit && webkit.messageHandlers && webkit.messageHandlers.\(issueHandlerName);
  if (!handler) return;
  let sent = 0;
  const send = (kind, message, source, line, column) => {
    if (++sent > 200) return;
    try {
      handler.postMessage({ kind, message: String(message || '').slice(0, 500), source: String(source || '').slice(0, 300),
                            line: Number(line) || 0, column: Number(column) || 0 });
    } catch (_) {}
  };
  addEventListener('error', e => {
    const t = e.target;
    if (t && t !== window && t.tagName) {
      send('resource', 'Failed to load ' + t.tagName.toLowerCase(), t.currentSrc || t.src || t.href || '', 0, 0);
    } else {
      send('error', e.message, e.filename, e.lineno, e.colno);
    }
  }, true);
  addEventListener('unhandledrejection', e => {
    let why = '';
    try { why = e.reason && e.reason.message ? e.reason.message : String(e.reason); } catch (_) { why = 'a promise was rejected'; }
    send('rejection', why, '', 0, 0);
  }, true);
  addEventListener('securitypolicyviolation', e => {
    send('csp', 'Blocked by Content-Security-Policy: ' + e.violatedDirective, e.blockedURI, e.lineNumber, e.columnNumber);
  }, true);
})();
"""

/// Takes what the watcher posts and hands it, checked, to its page. Held
/// by the content controller, so it holds the page weakly.
final class BrowserIssueSink: NSObject, WKScriptMessageHandler {
    weak var page: BrowserPage?

    nonisolated func userContentController(_ c: WKUserContentController, didReceive m: WKScriptMessage) {
        MainActor.assumeIsolated {
            guard m.world == browserIssueWorld, m.name == issueHandlerName, let issue = PageIssue.fromScript(m.body) else { return }
            page?.report(issue)
        }
    }
}

/// Gives a page's configuration its content controller, a new one of its
/// own: the error watcher, unless private, and no images when the tab says
/// so (once the rule is compiled).
@MainActor
func prepareContent(_ config: WKWebViewConfiguration, private p: Bool, sink: BrowserIssueSink, noImages: Bool) {
    let c = WKUserContentController()
    if !p {
        c.addUserScript(WKUserScript(source: issueWatcher, injectionTime: .atDocumentStart, forMainFrameOnly: false, in: browserIssueWorld))
        c.add(sink, contentWorld: browserIssueWorld, name: issueHandlerName)
    }
    if noImages, let r = BrowserNoImages.rules { c.add(r) }
    config.userContentController = c
}

/// Disable Images: a content rule blocking every image, compiled once.
@MainActor
enum BrowserNoImages {
    static private(set) var rules: WKContentRuleList?
    private static let json = #"[{"trigger":{"url-filter":".*","resource-type":["image"]},"action":{"type":"block"}}]"#

    /// The rule, compiled the first time it is wanted.
    static func load(_ then: @escaping @MainActor (WKContentRuleList?) -> Void) {
        if let rules { return then(rules) }
        WKContentRuleListStore.default().compileContentRuleList(forIdentifier: "conch-no-images", encodedContentRuleList: json) { r, _ in
            MainActor.assumeIsolated {
                rules = r
                then(r)
            }
        }
    }

    /// Turns it on or off in a page already made.
    static func set(_ on: Bool, in web: WKWebView, then: @escaping @MainActor () -> Void) {
        let c = web.configuration.userContentController
        load { r in
            if let r {
                if on { c.add(r) } else { c.remove(r) }
            }
            then()
        }
    }
}
