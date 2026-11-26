import Foundation
import Testing
@testable import ConchKit

// What WebKit sends on its own; the tab's words go after it.
private let webKit = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko)"
private let tahoe = OperatingSystemVersion(majorVersion: 26, minorVersion: 1, patchVersion: 0)

@Test func aTabSaysSafarisVersion() {
    #expect(browserApplicationName(safariVersion: "26.1", system: tahoe) == "Version/26.1 Safari/605.1.15")
    #expect(browserApplicationName(safariVersion: " 18.6.2\n", system: tahoe) == "Version/18.6.2 Safari/605.1.15")
}

/// The two tests claude.ai's artifacts make before switching their features
/// off as an embedded web view's (frame-shell, 2026-11-25).
@Test func claudeAIReadsItAsABrowser() {
    let ua = webKit + " " + browserApplicationName(safariVersion: "26.1", system: tahoe)
    #expect(ua.contains("Safari/"))
    #expect(!ua.hasSuffix("(KHTML, like Gecko)"))
    // WebKit alone fails both, which is the bug.
    #expect(!webKit.contains("Safari/") && webKit.hasSuffix("(KHTML, like Gecko)"))
}

@Test func noSafariOrANonsenseOneFallsBackToTheSystems() {
    for v in [nil, "", "  ", "Safari", "26.1 beta", "26.", "1.2.3.4", "-1"] as [String?] {
        #expect(browserApplicationName(safariVersion: v, system: tahoe) == "Version/26.1 Safari/605.1.15", "\(String(describing: v))")
    }
    let x0 = OperatingSystemVersion(majorVersion: 26, minorVersion: 0, patchVersion: 0)
    #expect(browserApplicationName(safariVersion: nil, system: x0) == "Version/26.0 Safari/605.1.15")
    let sonoma = OperatingSystemVersion(majorVersion: 14, minorVersion: 6, patchVersion: 1)
    #expect(browserApplicationName(safariVersion: nil, system: sonoma) == "Version/17.0 Safari/605.1.15")
    let sequoia = OperatingSystemVersion(majorVersion: 15, minorVersion: 2, patchVersion: 0)
    #expect(browserApplicationName(safariVersion: nil, system: sequoia) == "Version/18.0 Safari/605.1.15")
}

@Test func safarisVersionIsReadFromItsBundle() throws {
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: dir) }
    let plist = dir.appendingPathComponent("Info.plist")
    #expect(installedSafariVersion(at: plist.path) == nil) // missing
    try Data("not a plist".utf8).write(to: plist)
    #expect(installedSafariVersion(at: plist.path) == nil) // malformed
    try PropertyListSerialization.data(fromPropertyList: ["CFBundleShortVersionString": 26], format: .xml, options: 0).write(to: plist)
    #expect(installedSafariVersion(at: plist.path) == nil) // not a string
    try PropertyListSerialization.data(fromPropertyList: ["CFBundleShortVersionString": "26.1"], format: .xml, options: 0).write(to: plist)
    #expect(installedSafariVersion(at: plist.path) == "26.1")
}
