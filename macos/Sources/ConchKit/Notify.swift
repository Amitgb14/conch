import Foundation

// Telling you an agent needs you, as the TUI does (internal/tui/notify.go
// and model.go notifyAttention): when one comes to wait for an answer or
// finishes and you are not looking at it, a desktop notification, a sound
// if asked for — by [notify] in config.toml, quiet hours included.

/// config.toml's [notify], what the app acts on, with the TUI's defaults
/// (config.Default): on, desktop, for waiting and done, no sound.
public struct NotifySettings: Equatable, Sendable {
    public var enabled = true
    public var desktop = true
    public var sound = false
    public var waiting = true
    public var done = true
    public var quietStart = ""
    public var quietEnd = ""

    public init() {}

    public init(toml: String?) {
        func flag(_ k: String, _ d: Bool) -> Bool {
            switch tomlSetting("notify", k, in: toml)?.lowercased() {
            case "true": return true
            case "false": return false
            default: return d
            }
        }
        enabled = flag("enabled", true)
        desktop = flag("desktop", true)
        sound = flag("sound", false)
        waiting = flag("waiting", true)
        done = flag("done", true)
        quietStart = tomlSetting("notify", "quiet_start", in: toml) ?? ""
        quietEnd = tomlSetting("notify", "quiet_end", in: toml) ?? ""
    }

    /// Whether t is within quiet hours ("22:00" to "08:00", across
    /// midnight too); never when either end is unset or they are equal.
    public func quiet(_ t: Date, calendar: Calendar = .current) -> Bool {
        func minutes(_ s: String) -> Int? {
            let p = s.trimmingCharacters(in: .whitespaces).split(separator: ":")
            guard p.count == 2, let h = Int(p[0]), let m = Int(p[1]), (0...23).contains(h), (0...59).contains(m) else { return nil }
            return h * 60 + m
        }
        guard let s = minutes(quietStart), let e = minutes(quietEnd), s != e else { return false }
        let c = calendar.dateComponents([.hour, .minute], from: t)
        let now = (c.hour ?? 0) * 60 + (c.minute ?? 0)
        return s < e ? (now >= s && now < e) : (now >= s || now < e)
    }
}

/// A notification to post.
public struct Alert: Equatable, Sendable {
    public var title: String
    public var body: String
}

/// What changing from old to new is worth telling you, or nil: an agent
/// that has just come to wait or to finish, not one you are looking at,
/// within what [notify] asks for, outside quiet hours. machine is the
/// machine's name when it is not this computer.
public func attentionAlert(old: PaneInfo?, new p: PaneInfo, machine: String?, viewing: Bool,
                           settings s: NotifySettings, now: Date = Date()) -> Alert? {
    guard s.enabled, s.desktop || s.sound, !viewing, let a = p.agent, a.state == .blocked || a.state == .done else { return nil }
    if old?.agent?.state == a.state { return nil } // it said so already
    if a.state == .blocked && !s.waiting { return nil }
    if a.state == .done && !s.done { return nil }
    if s.quiet(now) { return nil } // the sidebar still shows it
    let body: String
    if a.state == .blocked {
        body = (a.message?.isEmpty == false) ? a.message! : p.displayName + " is waiting for you"
    } else if a.failed == true {
        body = p.displayName + " stopped: " + (a.message ?? "")
    } else {
        body = p.displayName + " finished"
    }
    return Alert(title: "conch · " + a.name + (machine.map { " on " + $0 } ?? ""), body: body)
}

/// The Dock's badge: how many agents wait for you, on every machine and in
/// every workspace (as the TUI's host status counts), or nothing.
public func dockBadge(_ waiting: Int) -> String { waiting > 0 ? "\(waiting)" : "" }
