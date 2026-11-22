@testable import ConchKit
import Foundation
import Testing

// The browser's security rules: what the address bar says of a page, a
// certificate error that never continues by itself, per-site permissions
// denied until allowed, downloads that ask before an executable, file://
// pages that read only themselves, and the Settings page that lists it.

@Test func theAddressBarSaysHowAPageArrived() {
    #expect(addressSecurity("https://go.dev/") == .secure)
    #expect(addressSecurity("HTTPS://GO.DEV/") == .secure)
    #expect(addressSecurity("http://example.com/") == .notSecure)
    #expect(addressSecurity("http://10.0.0.5:8080/") == .notSecure, "another machine on the network is not this one")
    #expect(addressSecurity("http://localhost:3000/") == .local)
    #expect(addressSecurity("http://LOCALHOST/") == .local)
    #expect(addressSecurity("http://127.0.0.1:5173/") == .local)
    #expect(addressSecurity("http://127.1.2.3/") == .local, "all of 127/8 is this machine")
    #expect(addressSecurity("http://[::1]:8000/") == .local)
    #expect(addressSecurity("http://printer.local/") == .local)
    #expect(addressSecurity("http://app.localhost:3000/") == .local)
    #expect(addressSecurity("http://localhost.evil.com/") == .notSecure, "a name that only starts with localhost")
    #expect(addressSecurity("http://evil-local/") == .notSecure)
    #expect(addressSecurity("http://local/") == .notSecure)
    #expect(addressSecurity("ws://example.com/") == .notSecure)
    #expect(addressSecurity("file:///Users/me/index.html") == .file)
    #expect(addressSecurity("about:blank") == .none)
    #expect(addressSecurity("") == .none)
    #expect(addressSecurity("not a url") == .none)
    #expect(addressSecurity("https://expired.badssl.com/", bypassed: true) == .bypassed)
    #expect(addressSecurity("http://example.com/", bypassed: true) == .notSecure, "a bypass is https's")
    #expect(AddressSecurity.notSecure.label == "Not Secure")
    #expect(AddressSecurity.secure.label == nil, "a lock, no words")
    #expect(AddressSecurity.bypassed.label == "Certificate Not Trusted")
}

@Test func certificateErrorsAreToldApart() {
    for code in [-1201, -1202, -1203, -1204] { #expect(isCertificateError(domain: NSURLErrorDomain, code: code), "\(code)") }
    #expect(!isCertificateError(domain: NSURLErrorDomain, code: -1200), "a TLS failure is not a certificate to accept")
    #expect(!isCertificateError(domain: NSURLErrorDomain, code: -1009))
    #expect(!isCertificateError(domain: NSURLErrorDomain, code: -1205), "a client certificate is another matter")
    #expect(!isCertificateError(domain: "WebKitErrorDomain", code: -1202))
}

@Test func visitAnywayIsForThatHostAndThisSessionOnly() {
    var e = CertExceptions()
    #expect(!e.allows("self-signed.local"))
    e.allow("Self-Signed.LOCAL")
    #expect(e.allows("self-signed.local"))
    #expect(e.allows("SELF-SIGNED.local"))
    #expect(!e.allows("other.self-signed.local"), "not its subdomains")
    #expect(!e.allows("signed.local"))
    #expect(!e.allows(""))
    e.allow("")
    #expect(e.hosts == ["self-signed.local"], "an empty host is never allowed")
    e.forget("self-signed.local")
    #expect(!e.allows("self-signed.local"))
    // Kept only in memory: not a thing that can be saved.
    #expect(!((CertExceptions() as Any) is Encodable))
}

@Test func permissionsAreDeniedUntilAllowed() {
    var p = SitePermissions()
    #expect(p.decision("https://meet.example", .camera) == nil, "never asked")
    #expect(!p.allowed("https://meet.example", .camera), "unasked is denied")
    for k in BrowserPermission.allCases { #expect(!p.allowed("http://localhost:3000", k)) }
    p.set("https://meet.example", .camera, allow: true)
    #expect(p.allowed("https://meet.example", .camera))
    #expect(!p.allowed("https://meet.example", .microphone), "camera is not microphone")
    #expect(!p.allowed("https://other.example", .camera), "one site's yes is not another's")
    #expect(!p.allowed("http://meet.example", .camera), "nor the same name over http")
    p.set("https://meet.example", .microphone, allow: false)
    #expect(p.decision("https://meet.example", .microphone) == false)
    #expect(p.sites.map(\.origin) == ["https://meet.example"])
    #expect(p.sites[0].decisions == [.camera: true, .microphone: false])
    p.revoke("https://meet.example", .camera)
    #expect(p.decision("https://meet.example", .camera) == nil)
    p.revoke("https://meet.example", .microphone)
    #expect(p.sites.isEmpty, "a site with nothing left is not listed")
    // Location and notifications have no way to be asked: never allowed.
    p.set("https://maps.example", .location, allow: true)
    p.set("https://maps.example", .notifications, allow: true)
    #expect(!p.allowed("https://maps.example", .location))
    #expect(!p.allowed("https://maps.example", .notifications))
    #expect(!BrowserPermission.location.askable && BrowserPermission.camera.askable)
}

@Test func permissionsSaveAndReadTolerantly() throws {
    var p = SitePermissions()
    p.set("https://a.example", .camera, allow: true)
    let back = try JSONDecoder().decode(SitePermissions.self, from: JSONEncoder().encode(p))
    #expect(back == p)
    // A kind a later build added is dropped, not the whole list.
    let json = #"{"sites":{"https://a.example":{"camera":true,"telepathy":true},"https://b.example":{"microphone":false}}}"#
    let odd = try JSONDecoder().decode(SitePermissions.self, from: Data(json.utf8))
    #expect(odd.allowed("https://a.example", .camera))
    #expect(odd.decision("https://b.example", .microphone) == false)
    #expect(SitePermissions.load(nil) == SitePermissions())
    #expect(SitePermissions.load(Data("garbage".utf8)) == SitePermissions())
}

@Test func aSitesOriginIsSchemeHostAndPort() {
    #expect(permissionOrigin(URL(string: "https://Meet.Example/room?x=1")!) == "https://meet.example")
    #expect(permissionOrigin(URL(string: "http://localhost:3000/a")!) == "http://localhost:3000")
    #expect(permissionOrigin(URL(string: "https://a.example:443/")!) == "https://a.example:443")
    #expect(permissionOrigin(URL(string: "file:///tmp/x.html")!) == nil, "a file is no site to give a camera to")
    #expect(permissionOrigin(URL(string: "about:blank")!) == nil)
}

@Test func aFilePageReadsOnlyItself() {
    let u = URL(fileURLWithPath: "/Users/me/site/index.html")
    #expect(fileReadAccess(for: u) == u, "not the folder, so not its neighbours")
}

@Test func downloadsAskBeforeAnExecutable() {
    for n in ["Setup.app.zip", "tool.pkg", "Installer.dmg", "run.command", "x.sh", "a.EXE", "b.jar", "c.scpt", "d.terminal", "e.webloc", "f.app", "g.mpkg", "h.workflow", "i.py", "j.ps1", "k.bat", "l.msi", "m.dylib"] {
        #expect(isExecutableDownload(n), "\(n)")
    }
    for n in ["report.csv", "photo.png", "notes.txt", "data.json", "paper.pdf", "song.mp3", "archive.tar.gz", "noext", ""] {
        #expect(!isExecutableDownload(n), "\(n)")
    }
}

@Test func downloadNamesAreSafeAndNeverOverwrite() {
    #expect(safeDownloadName("report.csv") == "report.csv")
    #expect(safeDownloadName("../../.ssh/authorized_keys") == "authorized_keys", "a path in a name is not followed")
    #expect(safeDownloadName(".bashrc") == "bashrc", "never a hidden file")
    #expect(safeDownloadName("a/b\\c.txt") == "c.txt")
    #expect(safeDownloadName("") == "download")
    #expect(safeDownloadName("...") == "download")
    #expect(safeDownloadName("bad\u{0}name.txt") == "badname.txt")
    let taken: Set<String> = ["report.csv", "report (1).csv", "noext"]
    #expect(uniqueDownloadName("report.csv") { taken.contains($0) } == "report (2).csv")
    #expect(uniqueDownloadName("new.csv") { taken.contains($0) } == "new.csv")
    #expect(uniqueDownloadName("noext") { taken.contains($0) } == "noext (1)")
    #expect(uniqueDownloadName("archive.tar.gz") { $0 == "archive.tar.gz" } == "archive (1).tar.gz", "the whole double extension kept")
}

@Test func browserSettingsHaveAPageOfTheirOwn() {
    // After the Mac app's own page, in General; only in a schema with one.
    #expect(SettingsNav.sidebar(["ui", "app", "agents"]) == ["ui", "app", SettingsNav.browser, "agents", "skills"])
    #expect(SettingsNav.sidebar(["ui"]) == ["ui", "skills"])
    #expect(SettingsNav.grouped([ "app", SettingsNav.browser]).first?.1 == ["app", SettingsNav.browser])
    #expect(SettingsNav.title(of: SettingsNav.browser) == "Browser")
    #expect(SettingsNav.title(of: "skills") == SettingsNav.skillsTitle)
    #expect(SettingsNav.icon(of: SettingsNav.browser) == "globe")
    let schema = [SettingsSection(id: "app", key: "app", title: "Mac App", description: "", icon: "laptop", fields: [])]
    #expect(SettingsNav.search("cookies", in: schema) == [SettingsNav.browser])
    #expect(SettingsNav.search("camera", in: schema) == [SettingsNav.browser])
}

@Test func clearingBrowsingDataForgetsClosedTabsAndPrivateTabsAreTheirOwn() {
    var b = BrowserTabs(address: "http://a")
    b.close(b.open("http://b"))
    #expect(b.closed.count == 1)
    b.forgetClosed()
    #expect(b.closed.isEmpty)
    #expect(b.reopen() == nil)
    // A private tab's copy, and a tab a private page opens, stay private.
    let p = b.open("https://p.example", isPrivate: true)
    let d = b.duplicate(p)!
    #expect(b.tab(d)?.isPrivate == true)
    let q = b.open("https://q.example", after: p, isPrivate: b.tab(p)!.isPrivate)
    #expect(b.tab(q)?.isPrivate == true)
    #expect(b.saved?.tabs.map(\.address) == ["http://a"], "none of them saved")
}

// No bridge from a page into conch: no message handler a page can reach,
// no script in the page's own world, nothing it can call. The one channel
// back from a page is the error watcher (BrowserContent.swift), and only
// that file may set up a script or a handler: one-way (no reply), in a
// content world of its own, and none in a private tab. The other scripts
// conch runs (the icon's link, the user agent for About Browser) run in
// WebKit's client world, which the page cannot reach. A source check, since
// a bridge would be one line that nothing else would notice.
@Test func pagesHaveNoBridgeIntoConch() throws {
    let dir = (#filePath as NSString).deletingLastPathComponent + "/../../Sources/ConchApp"
    let channel = "BrowserContent.swift"
    var scripts = 0
    var sawChannel = false
    for f in try FileManager.default.contentsOfDirectory(atPath: dir) where f.hasSuffix(".swift") {
        let code = try String(contentsOfFile: dir + "/" + f, encoding: .utf8).components(separatedBy: "\n")
            .filter { !$0.trimmingCharacters(in: .whitespaces).hasPrefix("//") && !$0.trimmingCharacters(in: .whitespaces).hasPrefix("///") }
        for (i, line) in code.enumerated() {
            // Never anywhere: a handler that answers, a script in the
            // page's world, a call the page could await.
            for bad in ["WKScriptMessageHandlerWithReply", "addScriptMessageHandler", "callAsyncJavaScript", "contentWorld: .page", "in: .page"] {
                #expect(!line.contains(bad), "\(f):\(i + 1): \(bad) would let a page reach conch")
            }
            if f != channel {
                for bad in ["WKScriptMessageHandler", "WKUserScript", "userContentController", "WKContentWorld.world"] {
                    #expect(!line.contains(bad), "\(f):\(i + 1): \(bad) belongs in \(channel), under its rules")
                }
            }
            if line.contains("evaluateJavaScript") {
                scripts += 1
                #expect(line.contains("in: .defaultClient"), "\(f):\(i + 1): a script in the page's own world")
            }
        }
        if f == channel {
            sawChannel = true
            let all = code.joined(separator: "\n")
            #expect(all.contains("WKContentWorld.world(name: \"conch-errors\")"), "the watcher's world is its own")
            #expect(all.contains("in: browserIssueWorld") && all.contains("contentWorld: browserIssueWorld"), "script and handler both in it")
            #expect(all.components(separatedBy: "addUserScript(").count == 2, "one script")
            #expect(all.components(separatedBy: ".add(sink").count == 2, "one handler")
            #expect(all.contains("if !p {"), "none in a private tab")
            #expect(all.contains("PageIssue.fromScript(m.body)"), "what it posts is checked")
            #expect(all.contains("m.world == browserIssueWorld"), "and only from its world")
        }
    }
    #expect(sawChannel)
    #expect(scripts <= 2, "the icon's and the user agent's")
}
