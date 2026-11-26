import Foundation
import Testing
@testable import ConchKit

/// Bytes malloc holds live right now, across every zone.
private func liveBytes() -> Int {
    var s = malloc_statistics_t()
    malloc_zone_statistics(nil, &s)
    return Int(s.size_in_use)
}

/// A connection's reader is a thread that runs as long as the connection,
/// and Foundation's JSON objects are autoreleased: with no pool of its own,
/// every message it read stayed in memory until the connection ended — a
/// running Conch.app held 1,628 JSON writers' buffers and 160k dictionaries.
/// handle() drains its own, so a long-lived thread keeps nothing.
@Test func handlingMessagesOnALongLivedThreadKeepsNothing() throws {
    let c = try Connection(command: "/bin/cat", args: [], environment: [:], queue: DispatchQueue(label: "mem"))
    defer { c.shutdown() }
    // A result of many small objects, as a session or pane list is.
    let items = (0..<2000).map { i in "{\"id\":\"s\(i)\",\"title\":\"session number \(i) in a project\",\"dir\":\"/src/api\"}" }
    let line = Data("{\"id\":\"none\",\"result\":{\"sessions\":[\(items.joined(separator: ","))]}}".utf8)
    let event = Data("{\"event\":\"session.changed\",\"data\":{\"sessions\":[\(items.joined(separator: ","))]}}".utf8)

    var grew = 0
    let done = DispatchSemaphore(value: 0)
    let t = Thread {
        // Warm up first: the first messages allocate caches that stay.
        for _ in 0..<5 { c.handle(line); c.handle(event) }
        let before = liveBytes()
        for _ in 0..<200 { c.handle(line); c.handle(event) }
        grew = liveBytes() - before
        done.signal()
    }
    t.start()
    done.wait()
    // 400 messages of ~200 KB: kept, that is ~100 MB of objects; drained,
    // next to nothing. The margin leaves room for other tests running.
    #expect(grew < 30_000_000, "the reader kept \(grew / 1_000_000) MB")
}
