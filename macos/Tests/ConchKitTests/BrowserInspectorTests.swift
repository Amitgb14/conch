@testable import ConchKit
import Foundation
import Testing
import WebKit

// Develop ▸ Web Inspector leans on WebKit's private names. Without the
// developer-extras preference `show` did nothing and said nothing.

@MainActor @Test func webKitStillHasWhatTheInspectorNeeds() throws {
    let c = WKWebViewConfiguration()
    #expect(c.preferences.responds(to: NSSelectorFromString("_setDeveloperExtrasEnabled:")))
    c.preferences.setValue(true, forKey: "developerExtrasEnabled")
    #expect(c.preferences.value(forKey: "developerExtrasEnabled") as? Bool == true)
    let w = WKWebView(frame: .zero, configuration: c)
    let get = NSSelectorFromString("_inspector")
    try #require(w.responds(to: get))
    let i = try #require(w.perform(get)?.takeUnretainedValue() as? NSObject)
    for name in ["show", "close"] { #expect(i.responds(to: NSSelectorFromString(name)), "\(name)") }
}

// The app's side has no test target, so it is read.
@Test func everyBrowserPageAllowsTheInspector() throws {
    let path = (#filePath as NSString).deletingLastPathComponent + "/../../Sources/ConchApp/BrowserView.swift"
    let code = try String(contentsOfFile: path, encoding: .utf8)
    let allow = try #require(code.range(of: "allowWebInspector(configuration)"), "every page's configuration, a pop-up's too")
    let made = try #require(code.range(of: "web = FocusingWebView(frame: .zero, configuration: configuration)"))
    #expect(allow.lowerBound < made.lowerBound, "before its web view is made")
    #expect(code.contains("setValue(true, forKey: \"developerExtrasEnabled\")"))
    #expect(!code.contains("WebHost(web:") && code.contains("WebHost(view: page.holder)"), "a docked inspector stays with its page as tabs switch")
    #expect(code.contains("page.frame = bounds"), "and fills no more than its pane after one")
    #expect(code.contains("closeWebInspector(web)"), "a closed page's inspector goes with it")
}
