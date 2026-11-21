import AppKit
import ConchKit
import Darwin
import SwiftUI
import WebKit

// How much memory each browser tab uses: the footprint of the WebContent
// process its page runs in, as Activity Monitor's Memory column shows it.
// Read only while somebody looks — the Browser Memory window, or a tab's
// tooltip as the pointer comes onto it — never on a timer of its own.

/// The WebContent process a page runs in. WebKit gives no public way to
/// it, so this reads the private `_webProcessIdentifier`; nil when a
/// WebKit no longer has it, and the memory then reads "—" rather than
/// failing. The one place conch reaches for it.
@MainActor
func webContentPID(_ w: WKWebView) -> Int32? {
    let name = "_webProcessIdentifier"
    guard w.responds(to: NSSelectorFromString(name)), let n = w.value(forKey: name) as? NSNumber, n.int32Value > 0 else { return nil }
    return n.int32Value
}

/// A process's physical footprint (RUSAGE_INFO_V4's ri_phys_footprint);
/// nil when it cannot be read — gone, or not ours to read.
func physicalFootprint(_ pid: Int32) -> UInt64? {
    var info = rusage_info_v4()
    let r = withUnsafeMutablePointer(to: &info) { p in
        p.withMemoryRebound(to: rusage_info_t?.self, capacity: 1) { proc_pid_rusage(pid, RUSAGE_INFO_V4, $0) }
    }
    return r == 0 ? info.ri_phys_footprint : nil
}

/// One tab as the memory window lists it.
struct BrowserMemoryRow: Identifiable {
    var id: Int
    var pane: String
    var tab: BrowserTab
}

extension Store {
    /// Every browser tab in this window with its memory, read now.
    func browserMemory() -> (rows: [BrowserMemoryRow], memory: TabMemory) {
        var rows: [BrowserMemoryRow] = []
        var procs: [TabProcess] = []
        for k in browserViews.keys.sorted() {
            guard let m = browserViews[k] else { continue }
            for t in m.tabs.tabs {
                let n = rows.count
                rows.append(BrowserMemoryRow(id: n, pane: k, tab: t))
                let page = m.pages[t.id]
                procs.append(TabProcess(tab: n, pid: page.flatMap { webContentPID($0.web) }, loaded: page != nil))
            }
        }
        var foot: [Int32: UInt64] = [:]
        for p in Set(procs.compactMap(\.pid)) { foot[p] = physicalFootprint(p) }
        return (rows, tabMemory(procs, footprints: foot))
    }

    /// One tab's memory line, read now: for its tooltip.
    func browserMemory(pane: String, tab: Int) -> String? {
        let r = browserMemory()
        guard let row = r.rows.first(where: { $0.pane == pane && $0.tab.id == tab }) else { return nil }
        return r.memory.lines[row.id]?.text
    }
}

/// ⋯ → Browser Memory: every tab, its memory, the total; Reload and Close
/// for each. Read every two seconds while it is open.
struct BrowserMemoryView: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    let done: () -> Void
    @State private var rows: [BrowserMemoryRow] = []
    @State private var memory = TabMemory(lines: [:], total: 0, unknown: 0)
    private let tick = Timer.publish(every: 2, on: .main, in: .common).autoconnect()

    var body: some View {
        let _ = look
        return VStack(alignment: .leading, spacing: 10) {
            Text("Browser Memory").font(.ui(size: 15, weight: .semibold))
            Text("Each tab's web process, as Activity Monitor counts it.").font(.ui(size: 12)).foregroundStyle(Theme.text2)
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(rows) { r in
                        HStack(spacing: 10) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(r.tab.label).font(.ui(size: 12.5)).foregroundStyle(Theme.text).lineLimit(1)
                                if !r.tab.address.isEmpty {
                                    Text(r.tab.address).font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.middle)
                                }
                            }
                            Spacer(minLength: 8)
                            Text(memory.lines[r.id]?.text ?? "—").font(.ui(size: 12, design: .monospaced)).foregroundStyle(Theme.text2)
                            Button("Reload") { store.browserViews[r.pane]?.reload(r.tab.id); read() }.controlSize(.small)
                            Button("Close") { store.browserViews[r.pane]?.close(r.tab.id); read() }.controlSize(.small)
                        }
                        .padding(.vertical, 6)
                        Divider()
                    }
                    if rows.isEmpty { Text("No browser tabs.").foregroundStyle(Theme.muted).padding(.vertical, 12) }
                }
            }
            .frame(minHeight: 160, maxHeight: 360)
            HStack {
                Text("Total: " + formatFootprint(memory.total) + (memory.unknown > 0 ? " · \(memory.unknown) not known" : ""))
                    .font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.text)
                Spacer()
                Button("Done") { done() }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(18).frame(width: 560)
        .onAppear(perform: read)
        .onReceive(tick) { _ in read() }
    }

    func read() {
        let r = store.browserMemory()
        rows = r.rows
        memory = r.memory
    }
}
