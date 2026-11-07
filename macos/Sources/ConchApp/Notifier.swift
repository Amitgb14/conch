import AppKit
import ConchKit
import UserNotifications

// Desktop notifications for agents that need you (ConchKit's attentionAlert
// decides which), and the Dock's badge for how many wait. A click on a
// notification opens the agent it is about.

@MainActor
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    static let shared = Notifier()
    /// Opens the pane a clicked notification is about.
    var onOpen: ((String) -> Void)?
    private var allowed: Bool?

    /// Asked once, as the app starts: macOS shows its own prompt the first
    /// time. A refusal, or a build macOS will not let post (an ad hoc
    /// signed bundle can be one), falls back to the system's own
    /// notification through osascript, as the TUI posts them.
    func start() {
        let c = UNUserNotificationCenter.current()
        c.delegate = self
        c.requestAuthorization(options: [.alert, .sound, .badge]) { ok, _ in
            Task { @MainActor in self.allowed = ok }
        }
    }

    func post(_ a: Alert, pane: String, sound: Bool, desktop: Bool) {
        post(a, id: "pane:" + pane, pane: pane, sound: sound, desktop: desktop)
    }

    /// A notification not about one pane (a plan limit): a click only
    /// brings the app forward. One per id: a newer one replaces the last.
    func post(_ a: Alert, id: String, pane: String? = nil, sound: Bool, desktop: Bool) {
        if sound && !desktop { NSSound(named: "Glass")?.play() }
        guard desktop else { return }
        if allowed == false {
            fallback(a, sound: sound)
            return
        }
        let content = UNMutableNotificationContent()
        content.title = a.title
        content.body = a.body
        if let pane { content.userInfo = ["pane": pane] }
        if sound { content.sound = .default }
        // One notification per id (a pane's): a newer state replaces the last.
        let r = UNNotificationRequest(identifier: id, content: content, trigger: nil)
        UNUserNotificationCenter.current().add(r) { err in
            if err != nil { Task { @MainActor in self.fallback(a, sound: sound) } }
        }
    }

    private func fallback(_ a: Alert, sound: Bool) {
        func q(_ s: String) -> String { "\"" + s.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"") + "\"" }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
        p.arguments = ["-e", "display notification \(q(a.body)) with title \(q(a.title))" + (sound ? " sound name \"Glass\"" : "")]
        try? p.run()
    }

    /// The Dock's badge, and a bounce of the icon while the app is in the
    /// background and someone has just come to wait.
    func badge(waiting: Int, newlyWaiting: Bool) {
        NSApp.dockTile.badgeLabel = dockBadge(waiting)
        if newlyWaiting && !NSApp.isActive { NSApp.requestUserAttention(.informationalRequest) }
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                            withCompletionHandler done: @escaping () -> Void) {
        let pane = response.notification.request.content.userInfo["pane"] as? String
        Task { @MainActor in
            NSApp.activate(ignoringOtherApps: true)
            if let pane { self.onOpen?(pane) }
            done()
        }
    }

    /// Shown even while the app is in front: you may be looking at another
    /// pane than the one that needs you.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent n: UNNotification,
                                            withCompletionHandler done: @escaping (UNNotificationPresentationOptions) -> Void) {
        done([.banner, .sound])
    }
}
