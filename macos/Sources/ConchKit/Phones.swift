import Foundation

/// `conch web devices -json`: the paired phones, which are on now, and how
/// conch web and its relay are — what the status bar's phone icon and the
/// list of phones show.
public struct PhonesInfo: Decodable, Equatable, Sendable {
    public var running: Bool?
    public var relay: String?
    public var relayConnected: Bool?
    public var relayStatus: String?
    public var devices: [PhoneDevice]?

    enum CodingKeys: String, CodingKey {
        case running, relay, devices
        case relayConnected = "relay_connected", relayStatus = "relay_status"
    }

    public init(running: Bool? = nil, relay: String? = nil, relayConnected: Bool? = nil, relayStatus: String? = nil, devices: [PhoneDevice]? = nil) {
        self.running = running
        self.relay = relay
        self.relayConnected = relayConnected
        self.relayStatus = relayStatus
        self.devices = devices
    }

    /// The phones on now.
    public var onNow: [PhoneDevice] { (devices ?? []).filter { $0.connected == true } }

    /// What the phone icon shows: on while a phone is, amber while a relay
    /// is set and conch web runs but isn't on it — phones away from home
    /// can't reach this computer then, and nothing else would say so.
    public var icon: PhoneIcon {
        if !onNow.isEmpty { return .on(onNow.count) }
        if let r = relay, !r.isEmpty, running == true, relayConnected != true { return .relayDown }
        return .idle
    }

    /// The icon's tooltip.
    public var tooltip: String {
        let on = onNow
        switch icon {
        case .on:
            let names = on.map { d in "\(d.name) (\(d.route == "relay" ? "through the relay" : "direct"))" }
            return (on.count == 1 ? "On now: " : "\(on.count) on now: ") + names.joined(separator: ", ")
        case .relayDown:
            return "Not on the relay: \(relayStatus ?? "not connected"). Phones away from this network can't reach it."
        case .idle:
            return "Pair a phone, to see and answer your agents from it"
        }
    }
}

public enum PhoneIcon: Equatable, Sendable {
    case idle, relayDown
    case on(Int)
}

/// A paired phone, as `conch web devices -json` gives it.
public struct PhoneDevice: Decodable, Equatable, Sendable, Identifiable {
    public var id: String
    public var name: String
    public var permission: String
    public var machines: [String]?
    public var created: Date?
    public var connected: Bool?
    /// "relay" or "direct": how it came, now or last time.
    public var route: String?
    public var since: Date?
    public var lastSeen: Date?

    enum CodingKeys: String, CodingKey {
        case id, name, permission, machines, created, connected, route, since
        case lastSeen = "last_seen"
    }

    public init(id: String, name: String, permission: String, machines: [String]? = nil, created: Date? = nil,
                connected: Bool? = nil, route: String? = nil, since: Date? = nil, lastSeen: Date? = nil) {
        self.id = id
        self.name = name
        self.permission = permission
        self.machines = machines
        self.created = created
        self.connected = connected
        self.route = route
        self.since = since
        self.lastSeen = lastSeen
    }

    /// Where it reaches, as the list says it: this Mac, and the machines
    /// given to it by name.
    public var reach: String {
        let m = machines ?? []
        return m.isEmpty ? "reaches this Mac" : "reaches this Mac, " + m.joined(separator: ", ")
    }

    /// Its route now, or when it was last seen.
    public func seen(now: Date = Date(), calendar: Calendar = .current) -> String {
        if connected == true { return route == "relay" ? "via relay" : "direct" }
        guard let l = lastSeen else { return "never connected" }
        let s = now.timeIntervalSince(l)
        if s < 60 { return "just now" }
        if s < 3600 { return "\(Int(s / 60)) min ago" }
        if calendar.isDate(l, inSameDayAs: now) || s < 86400 { return "\(Int(s / 3600)) h ago" }
        let days = calendar.dateComponents([.day], from: calendar.startOfDay(for: l), to: calendar.startOfDay(for: now)).day ?? 0
        return days == 1 ? "yesterday" : "\(days) days ago"
    }
}
