import Foundation
import ConchKit

// Counting a project's saved conversations again, for its Sessions row:
// when it is added, when an agent in it stops working or ends
// (sessionsMayHaveChanged), a burst coalesced per project (SessionRecounts).
extension Store {
    /// Asks for project pk (key(machine, id)) to be counted delay from now.
    func recountSessions(_ pk: String, after delay: TimeInterval = SessionRecounts.settle) {
        sessionRecounts.note(pk, now: Date(), after: delay)
        scheduleRecount()
    }

    /// One wait, for whichever project is due first.
    private func scheduleRecount() {
        sessionRecountTask?.cancel()
        guard let next = sessionRecounts.next else { sessionRecountTask = nil; return }
        let wait = max(0, next.timeIntervalSinceNow)
        sessionRecountTask = Task { [weak self] in
            if wait > 0 { try? await Task.sleep(nanoseconds: UInt64(wait * 1_000_000_000)) }
            guard !Task.isCancelled, let self else { return }
            for pk in self.sessionRecounts.take(now: Date()) { self.countSessions(project: pk) }
            if self.sessionRecounts.next != nil { self.scheduleRecount() } else { self.sessionRecountTask = nil }
        }
    }

    /// Counts one project's saved conversations, for its Sessions row.
    func countSessions(project pk: String) {
        let (m, pid) = unkey(pk)
        guard let c = connection(m), snapshots[m]?.project(pid) != nil else { return }
        struct List: Encodable { var project_id: String; var limit: Int }
        Task {
            if let n = try? await c.call("session.list", List(project_id: pid, limit: sessionCountLimit), as: SessionCount.self) {
                if sessions[pk] != n.count { sessions[pk] = n.count }
            }
        }
    }
}
