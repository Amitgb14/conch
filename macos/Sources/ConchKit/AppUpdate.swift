import Foundation

// Conch.app's own update (#204): what `conch app update check -json` says,
// when to ask it, and what a newer release found does — offered in the
// status bar, installed unasked when [app] auto_update says so, or nothing
// once the person has skipped that version. The asking, downloading and
// verifying are the bundled conch's (internal/update/app.go), so they are
// tested in Go against a fake release server.

/// What the check found. Optional where the Go side omits an empty field.
public struct AppUpdateCheck: Codable, Equatable, Sendable {
    public var current: String
    public var dev: Bool
    public var latest: String?
    public var newer: Bool
    public var notes: String?
    public var asset: String?
    public var unavailable: String?

    public init(current: String, dev: Bool = false, latest: String? = nil, newer: Bool = false,
                notes: String? = nil, asset: String? = nil, unavailable: String? = nil) {
        self.current = current
        self.dev = dev
        self.latest = latest
        self.newer = newer
        self.notes = notes
        self.asset = asset
        self.unavailable = unavailable
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        current = try c.decode(String.self, forKey: .current)
        dev = try c.decodeIfPresent(Bool.self, forKey: .dev) ?? false
        latest = try c.decodeIfPresent(String.self, forKey: .latest)
        newer = try c.decodeIfPresent(Bool.self, forKey: .newer) ?? false
        notes = try c.decodeIfPresent(String.self, forKey: .notes)
        asset = try c.decodeIfPresent(String.self, forKey: .asset)
        unavailable = try c.decodeIfPresent(String.self, forKey: .unavailable)
    }

    /// What the status bar says of it.
    public var banner: String { "Conch \(latest ?? "") is available" }

    /// Whether there is an archive for this Mac to install.
    public var canInstall: Bool { newer && !(asset ?? "").isEmpty }
}

/// A build from source, which updates from source and never asks.
public func isDevBuild(_ version: String) -> Bool {
    version.isEmpty || version.contains("dev")
}

/// [app] check_updates (on unless false) and auto_update (off unless true).
public struct AppUpdatePrefs: Equatable, Sendable {
    public var check: Bool
    public var auto: Bool
    public init(check: Bool = true, auto: Bool = false) {
        self.check = check
        self.auto = auto
    }
    public init(toml: String?) {
        check = tomlSetting("app", "check_updates", in: toml) != "false"
        auto = tomlSetting("app", "auto_update", in: toml) == "true"
    }
}

/// When the daily check is due. last is when it last asked, kept by the
/// app between launches.
public struct UpdateSchedule: Equatable, Sendable {
    public static let interval: TimeInterval = 24 * 3600
    public var last: Date?
    public init(last: Date?) { self.last = last }

    /// Due a day after the last check, or at once when there was none —
    /// or when the clock now reads earlier than it, since a day counted
    /// from a time that has not come would be no day at all. Never when
    /// it is turned off or this is a build from source.
    public func due(now: Date, prefs: AppUpdatePrefs, version: String) -> Bool {
        guard prefs.check, !isDevBuild(version) else { return false }
        guard let last else { return true }
        return now < last || now.timeIntervalSince(last) >= Self.interval
    }

    /// When the next check falls.
    public func next(now: Date) -> Date {
        guard let last, last <= now else { return now }
        return max(now, last.addingTimeInterval(Self.interval))
    }
}

/// What a check's answer leads to.
public enum AppUpdateStep: Equatable, Sendable {
    /// Nothing to say.
    case none
    /// Said in the status bar, with Install, Later and Skip This Version.
    case offer(AppUpdateCheck)
    /// [app] auto_update: installed without asking.
    case install(String)
    /// Asked for by hand: nothing newer than this.
    case upToDate(String)
    /// Asked for by hand on a build from source.
    case fromSource(String)
}

/// The step for a check's answer. manual is Check for Updates… from the
/// menu, which says what it found even when that is nothing, offers a
/// skipped version again, and never installs unseen.
public func appUpdateStep(_ c: AppUpdateCheck, prefs: AppUpdatePrefs, skipped: String?, manual: Bool) -> AppUpdateStep {
    if c.dev { return manual ? .fromSource(c.current) : .none }
    guard c.newer, let latest = c.latest else { return manual ? .upToDate(c.current) : .none }
    if !manual && skipped == latest { return .none }
    if !manual && prefs.auto && c.canInstall { return .install(latest) }
    return .offer(c)
}

/// Whether this launch is the first of a version an update installed:
/// the app notes the version it installed before it relaunches, and the
/// launch running it offers to reload the server onto its conch.
public func updatedSince(installed: String?, running: String) -> Bool {
    guard let installed, !installed.isEmpty else { return false }
    return installed == running
}

/// What relaunches the app once this process has quit: a shell waiting for
/// the pid to be gone, then open. The app's path is the script's $0.
public func relaunchCommand(pid: Int32, app: String) -> [String] {
    ["/bin/sh", "-c", "while kill -0 \(pid) 2>/dev/null; do sleep 0.2; done; exec /usr/bin/open \"$0\"", app]
}

/// Where the app an update replaced is kept, hidden beside it and without
/// .app so LaunchServices offers no second Conch: internal/update's
/// previousApp, which puts it there.
public func previousAppPath(_ app: String) -> String {
    let dir = (app as NSString).deletingLastPathComponent
    return (dir as NSString).appendingPathComponent("." + (app as NSString).lastPathComponent + ".previous")
}
