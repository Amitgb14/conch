import Foundation
import Testing
@testable import ConchKit

// What `conch app update check -json` says, read as the Go side writes it:
// fields left out when empty.
@Test func appUpdateCheckDecodes() throws {
    let newer = try JSONDecoder().decode(AppUpdateCheck.self, from: Data("""
    {"current":"0.2.0","latest":"0.2.1","newer":true,"notes":"https://github.com/Amitgb14/conch/releases/tag/v0.2.1","asset":"Conch_0.2.1_macos_arm64.zip"}
    """.utf8))
    #expect(newer.newer && newer.latest == "0.2.1" && newer.asset == "Conch_0.2.1_macos_arm64.zip" && !newer.dev)
    let dev = try JSONDecoder().decode(AppUpdateCheck.self, from: Data(#"{"current":"0.2.1-dev","dev":true}"#.utf8))
    #expect(dev.dev && !dev.newer && dev.latest == nil && dev.notes == nil)
    let same = try JSONDecoder().decode(AppUpdateCheck.self, from: Data(#"{"current":"0.2.1","latest":"0.2.1"}"#.utf8))
    #expect(!same.newer)
    #expect(throws: (any Error).self) { try JSONDecoder().decode(AppUpdateCheck.self, from: Data("not json".utf8)) }
}

@Test func devBuilds() {
    #expect(isDevBuild("0.2.1-dev"))
    #expect(isDevBuild("dev"))
    #expect(isDevBuild(""))
    #expect(!isDevBuild("0.2.1"))
    #expect(!isDevBuild("0.3.0-rc.1"))
}

@Test func appUpdatePrefsFromToml() {
    #expect(AppUpdatePrefs(toml: nil) == AppUpdatePrefs(check: true, auto: false))
    #expect(AppUpdatePrefs(toml: "[ui]\ntheme = \"nord\"\n") == AppUpdatePrefs(check: true, auto: false))
    #expect(AppUpdatePrefs(toml: "[app]\ncheck_updates = false\nauto_update = true\n") == AppUpdatePrefs(check: false, auto: true))
    // Another section's key of the same name is not [app]'s.
    #expect(AppUpdatePrefs(toml: "[other]\nauto_update = true\n[app]\ntabs = \"left\"\n") == AppUpdatePrefs(check: true, auto: false))
}

// Once a day, by a clock the test holds.
@Test func checkSchedule() {
    let t0 = Date(timeIntervalSince1970: 1_800_000_000)
    let on = AppUpdatePrefs(check: true, auto: false)
    var s = UpdateSchedule(last: nil)
    #expect(s.due(now: t0, prefs: on, version: "0.2.0"))              // never asked
    s.last = t0
    #expect(!s.due(now: t0, prefs: on, version: "0.2.0"))
    #expect(!s.due(now: t0.addingTimeInterval(23 * 3600), prefs: on, version: "0.2.0"))
    #expect(s.due(now: t0.addingTimeInterval(24 * 3600), prefs: on, version: "0.2.0"))
    #expect(s.due(now: t0.addingTimeInterval(30 * 24 * 3600), prefs: on, version: "0.2.0"))
    // A clock set back past the last check asks rather than waiting a day
    // from a time that has not come.
    #expect(s.due(now: t0.addingTimeInterval(-3600), prefs: on, version: "0.2.0"))
    // Turned off, or a development build: never.
    #expect(!s.due(now: t0.addingTimeInterval(48 * 3600), prefs: AppUpdatePrefs(check: false, auto: false), version: "0.2.0"))
    #expect(!s.due(now: t0.addingTimeInterval(48 * 3600), prefs: on, version: "0.2.1-dev"))
    // When the next is due: a day after the last, or now.
    #expect(s.next(now: t0.addingTimeInterval(3600)) == t0.addingTimeInterval(24 * 3600))
    #expect(UpdateSchedule(last: nil).next(now: t0) == t0)
}

@Test func whatAFoundReleaseDoes() {
    let found = AppUpdateCheck(current: "0.2.0", latest: "0.2.1", newer: true, notes: "n", asset: "Conch_0.2.1_macos_arm64.zip")
    let ask = AppUpdatePrefs(check: true, auto: false)
    let auto = AppUpdatePrefs(check: true, auto: true)
    #expect(appUpdateStep(found, prefs: ask, skipped: nil, manual: false) == .offer(found))
    #expect(appUpdateStep(found, prefs: auto, skipped: nil, manual: false) == .install("0.2.1"))
    // Asked for by hand, it is offered rather than installed unseen.
    #expect(appUpdateStep(found, prefs: auto, skipped: nil, manual: true) == .offer(found))
    // Skipped: not offered by the daily check, but by hand it is, and a
    // newer one than the one skipped is.
    #expect(appUpdateStep(found, prefs: ask, skipped: "0.2.1", manual: false) == .none)
    #expect(appUpdateStep(found, prefs: auto, skipped: "0.2.1", manual: false) == .none)
    #expect(appUpdateStep(found, prefs: ask, skipped: "0.2.1", manual: true) == .offer(found))
    #expect(appUpdateStep(found, prefs: ask, skipped: "0.2.0", manual: false) == .offer(found))
    // Up to date: said only when asked for.
    let same = AppUpdateCheck(current: "0.2.1", latest: "0.2.1")
    #expect(appUpdateStep(same, prefs: ask, skipped: nil, manual: false) == .none)
    #expect(appUpdateStep(same, prefs: ask, skipped: nil, manual: true) == .upToDate("0.2.1"))
    // A development build says so when asked for.
    let dev = AppUpdateCheck(current: "0.2.1-dev", dev: true)
    #expect(appUpdateStep(dev, prefs: auto, skipped: nil, manual: false) == .none)
    #expect(appUpdateStep(dev, prefs: auto, skipped: nil, manual: true) == .fromSource("0.2.1-dev"))
    // No archive for this Mac: offered (the notes), never installed.
    let intel = AppUpdateCheck(current: "0.2.0", latest: "0.2.1", newer: true, notes: "n", unavailable: "Apple Silicon only")
    #expect(appUpdateStep(intel, prefs: auto, skipped: nil, manual: false) == .offer(intel))
}

@Test func whatTheBarSays() {
    let found = AppUpdateCheck(current: "0.2.0", latest: "0.2.1", newer: true)
    #expect(found.banner == "Conch 0.2.1 is available")
    #expect(found.canInstall == false)
    var withAsset = found
    withAsset.asset = "Conch_0.2.1_macos_arm64.zip"
    #expect(withAsset.canInstall)
}

// The launch after an update offers to reload the server onto the new
// conch, once.
@Test func justUpdated() {
    #expect(updatedSince(installed: "0.2.1", running: "0.2.1") == true)
    #expect(updatedSince(installed: nil, running: "0.2.1") == false)
    // Still the old one running (relaunch not done, or rolled back): no.
    #expect(updatedSince(installed: "0.2.1", running: "0.2.0") == false)
}

// The relaunch waits for this process to be gone before opening the app
// again, so the new launch is not handed to the old one; the path is an
// argument, never part of the script.
@Test func relaunchWaitsThenOpens() {
    let argv = relaunchCommand(pid: 4242, app: "/Applications/My \"Conch\".app")
    #expect(argv.first == "/bin/sh" && argv[1] == "-c")
    #expect(argv[2].contains("kill -0 4242") && argv[2].contains("/usr/bin/open"))
    #expect(!argv[2].contains("Conch"))
    #expect(argv.last == "/Applications/My \"Conch\".app")
}

// Where the app replaced by an update is kept — the same place
// internal/update's previousApp says.
@Test func previousAppIsBeside() {
    #expect(previousAppPath("/Applications/Conch.app") == "/Applications/.Conch.app.previous")
    #expect(previousAppPath("/Users/a/Apps/Conch (Beta).app") == "/Users/a/Apps/.Conch (Beta).app.previous")
}

@Test func paletteChecksForUpdates() {
    #expect(paletteCommands.contains { $0.id == "checkForUpdates" && $0.title == "Check for Updates…" })
}
