import ConchKit
import Foundation
import Testing

// What makes a click in Conch.app wait (#293): conch attach's usage read
// on the main thread the first time a pane opened, and a pane's box laid
// out again — its letterbox's dot pattern drawn again — on every change
// of the store.

private final class Reads: @unchecked Sendable {
    private let lock = NSLock()
    private var n: [String: Int] = [:]
    private var threads: [Bool] = []
    func note(_ b: String) {
        lock.lock()
        n[b, default: 0] += 1
        threads.append(Thread.isMainThread)
        lock.unlock()
    }
    func count(_ b: String) -> Int { lock.lock(); defer { lock.unlock() }; return n[b] ?? 0 }
    var onMain: Bool { lock.lock(); defer { lock.unlock() }; return threads.contains(true) }
}

@Test func attachUsageIsReadInTheBackgroundAndOnce() async {
    let reads = Reads()
    let cache = AttachUsageCache { b in
        reads.note(b)
        return "usage: conch attach [-hold] [-raw] [-view] ID (\(b))"
    }
    await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
        cache.warm("/a/conch") { done.resume() }
    }
    #expect(!reads.onMain, "warm read it on the main thread")
    // The pane opening finds it read: no second run.
    #expect(cache.usage("/a/conch").contains("(/a/conch)"))
    #expect(reads.count("/a/conch") == 1)
    // Warmed again: nothing more.
    await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
        cache.warm("/a/conch") { done.resume() }
    }
    #expect(reads.count("/a/conch") == 1)
    // Another binary (an update moved it) is read for itself.
    #expect(cache.usage("/b/conch").contains("(/b/conch)"))
    #expect(reads.count("/b/conch") == 1)
}

@Test func usageWithoutWarmingIsStillRead() {
    let cache = AttachUsageCache { _ in "usage: conch attach -hold ID" }
    #expect(cache.usage("/x") == "usage: conch attach -hold ID")
}

@Test func aBoxIsLaidOutOnlyWhenWhatItIsLaidOutFromChanged() {
    var m = LayoutMemo<[Int]>()
    var got: [Bool] = []
    for key in [[800, 600, 1], [800, 600, 1], [800, 600, 1], [800, 580, 1], [800, 600, 1]] { got.append(m.changed(key)) }
    // First always; a store change that moved nothing, twice; the split
    // dragged; and back.
    #expect(got == [true, false, false, true, true])
    m.reset() // a terminal just put in the box
    let again = m.changed([800, 600, 1])
    #expect(again)
}
