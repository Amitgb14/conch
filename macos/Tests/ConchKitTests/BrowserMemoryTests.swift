import ConchKit
import Testing

// How much memory each browser tab uses: the footprint of the WebContent
// process its page runs in, said once per process — tabs that share one
// say so rather than each counting it — and "—" where it is not known.

@Test func footprintsReadAsActivityMonitorsDo() {
    #expect(formatFootprint(0) == "0 KB")
    #expect(formatFootprint(512) == "1 KB", "anything at all is at least a kilobyte")
    #expect(formatFootprint(980 * 1024) == "980 KB")
    #expect(formatFootprint(1024 * 1024) == "1 MB")
    #expect(formatFootprint(312 * 1024 * 1024 + 400 * 1024) == "312 MB")
    #expect(formatFootprint(1024 * 1024 * 1024) == "1.0 GB")
    #expect(formatFootprint(UInt64(1.25 * 1024 * 1024 * 1024)) == "1.3 GB")
    #expect(formatFootprint(UInt64.max).hasSuffix("GB"), "no overflow")
}

@Test func eachTabSaysItsOwnProcess() {
    let mb: UInt64 = 1 << 20
    let r = tabMemory([TabProcess(tab: 1, pid: 100), TabProcess(tab: 2, pid: 200)], footprints: [100: 300 * mb, 200: 50 * mb])
    #expect(r.lines[1]?.text == "300 MB")
    #expect(r.lines[2]?.text == "50 MB")
    #expect(r.lines[1]?.sharedWith == 0)
    #expect(r.total == 350 * mb)
}

@Test func tabsSharingAProcessCountItOnce() {
    let mb: UInt64 = 1 << 20
    let r = tabMemory([TabProcess(tab: 1, pid: 100), TabProcess(tab: 2, pid: 100), TabProcess(tab: 3, pid: 100), TabProcess(tab: 4, pid: 7)],
                      footprints: [100: 400 * mb, 7: 10 * mb])
    #expect(r.lines[1]?.text == "400 MB · shared with 2 tabs")
    #expect(r.lines[3]?.sharedWith == 2)
    #expect(r.total == 410 * mb, "the shared process once, not three times")
    let two = tabMemory([TabProcess(tab: 1, pid: 5), TabProcess(tab: 2, pid: 5)], footprints: [5: mb])
    #expect(two.lines[2]?.text == "1 MB · shared with 1 tab")
}

@Test func unknownMemoryIsADashNeverAFailure() {
    let mb: UInt64 = 1 << 20
    // No pid (WebKit gave none), a pid whose footprint could not be read,
    // a tab not loaded yet.
    let r = tabMemory([TabProcess(tab: 1, pid: nil), TabProcess(tab: 2, pid: 9), TabProcess(tab: 3, pid: 8), TabProcess(tab: 4, pid: nil, loaded: false)],
                      footprints: [8: 20 * mb])
    #expect(r.lines[1]?.text == "—")
    #expect(r.lines[1]?.bytes == nil)
    #expect(r.lines[2]?.text == "—")
    #expect(r.lines[3]?.text == "20 MB")
    #expect(r.lines[4]?.text == "Not loaded")
    #expect(r.total == 20 * mb, "only what is known is added up")
    #expect(r.unknown == 2)
    let none = tabMemory([], footprints: [:])
    #expect(none.lines.isEmpty && none.total == 0 && none.unknown == 0)
}

@Test func aTabsTooltipSaysItsMemory() {
    #expect(tabTooltip(label: "Docs", address: "https://go.dev/", memory: nil) == "Docs\nhttps://go.dev/")
    #expect(tabTooltip(label: "Docs", address: "https://go.dev/", memory: "300 MB") == "Docs\nhttps://go.dev/\nMemory: 300 MB")
    #expect(tabTooltip(label: "New Tab", address: "", memory: "—") == "New Tab\nMemory: —")
}

@Test func theSystemMonitorNamesAWebProcessByItsTabs() {
    #expect(browserProcessOrigin(["Docs"]) == "Browser · Docs")
    #expect(browserProcessOrigin(["Docs", "PR #331", "App"]) == "Browser · Docs +2")
    #expect(browserProcessOrigin([]) == "Browser")
}
