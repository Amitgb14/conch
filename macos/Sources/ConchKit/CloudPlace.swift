import Foundation

// Where a project is added or moved to: this Mac, another machine, or
// Conch Cloud — one of the account's cloud machines, or a new one made for
// it. A cloud machine is readied before anything goes there: made (`conch
// cloud machine new NAME -size S -wait`) or woken, then connected to like
// any machine. What the Add project and Move sheets decide is here, so it
// can be tested.

/// A machine as the Where choice needs it.
public struct PlaceMachine: Equatable, Sendable {
    public var id: String
    public var label: String
    public var online: Bool
    /// A Conch Cloud machine: listed under Conch Cloud, not with the rest.
    public var cloud: Bool

    public init(id: String, label: String, online: Bool, cloud: Bool = false) {
        self.id = id
        self.label = label
        self.online = online
        self.cloud = cloud
    }
}

/// Where a project goes: a machine by its id, or Conch Cloud.
public enum ProjectPlace: Hashable, Sendable {
    case machine(String)
    case cloud
}

/// One choice of Where.
public struct PlaceOption: Identifiable, Equatable, Sendable {
    public var place: ProjectPlace
    public var title: String
    /// Said under it: offline, Sign in to use, 2 machines.
    public var note: String
    public var enabled: Bool

    public var id: String {
        switch place {
        case .machine(let m): return "m:" + m
        case .cloud: return "cloud"
        }
    }
}

/// The Where choices: this Mac, each other machine (one offline shown but
/// not offered), and Conch Cloud last — which, signed out, says to sign in.
/// Cloud machines are Conch Cloud's to list. excluding is the machine the
/// project is on already, for a move.
public func projectPlaces(machines: [PlaceMachine], signedIn: Bool, cloudMachines: Int, excluding: String? = nil) -> [PlaceOption] {
    var out: [PlaceOption] = []
    for m in machines where !m.cloud && m.id != excluding {
        out.append(PlaceOption(place: .machine(m.id), title: m.id == "local" ? "This Mac" : m.label,
                               note: m.online ? "" : "offline", enabled: m.online))
    }
    let note = !signedIn ? "Sign in to use"
        : cloudMachines == 0 ? "a new machine"
        : cloudMachines == 1 ? "1 machine" : "\(cloudMachines) machines"
    out.append(PlaceOption(place: .cloud, title: "Conch Cloud", note: note, enabled: true))
    return out
}

/// The cloud machine a project goes to: one the account has, or one made
/// for it.
public enum CloudPick: Hashable, Sendable {
    case existing(id: String)
    case new(name: String, size: String)
}

/// A step of readying a cloud machine, as the sheet says it.
public enum CloudPrep: Equatable, Sendable {
    case making(String)
    case waking(String)
    case connecting(String)

    public var text: String {
        switch self {
        case .making(let n): return "Making \(n)…"
        case .waking(let n): return "Waking \(n)…"
        case .connecting(let n): return "Connecting to \(n)…"
        }
    }
}

/// What readying pick takes: a new machine is made, an asleep one woken,
/// and either way it is connected to.
public func cloudPrep(_ pick: CloudPick, machines: [CloudMachine]) -> [CloudPrep] {
    switch pick {
    case .new(let name, _):
        return [.making(name), .connecting(name)]
    case .existing(let id):
        let m = machines.first { $0.id == id }
        let name = m?.name ?? id
        return m?.asleep == true ? [.waking(name), .connecting(name)] : [.connecting(name)]
    }
}

extension CloudMachine {
    /// Asleep, or on its way there: woken before anything goes there (as
    /// cloud.Client's waitRunning does).
    public var asleep: Bool { ["asleep", "sleeping", "archived"].contains(state) }

    /// Whether a project can be sent there: not one being destroyed or in
    /// error.
    public var pickable: Bool { !["destroying", "error"].contains(state) }
}

/// The machine offered first: one running, else one asleep or on its
/// way; nil to make a new one.
public func defaultCloudPick(_ machines: [CloudMachine]) -> CloudPick? {
    let ok = machines.filter(\.pickable)
    if let m = ok.first(where: { $0.state == "running" }) ?? ok.first { return .existing(id: m.id) }
    return nil
}

/// Whether Conch Cloud takes name for a machine: ^[a-z][a-z0-9-]{0,30}$.
public func validCloudMachineName(_ name: String) -> Bool {
    guard let f = name.first, f >= "a" && f <= "z", name.count <= 31 else { return false }
    return name.allSatisfy { ($0 >= "a" && $0 <= "z") || ($0 >= "0" && $0 <= "9") || $0 == "-" }
}

/// The name a new machine is offered: the project's, made one Conch Cloud
/// takes, with -2, -3… after it when the account has it already; "box"
/// when the project gives nothing.
public func defaultCloudMachineName(for project: String, taken: [String]) -> String {
    var s = ""
    for c in project.lowercased() {
        if (c >= "a" && c <= "z") || (c >= "0" && c <= "9") {
            s.append(c)
        } else if !s.hasSuffix("-") {
            s.append("-")
        }
    }
    s = String(s.drop(while: { $0 == "-" }))
    while s.hasSuffix("-") { s.removeLast() }
    if s.isEmpty { s = "box" } else if let f = s.first, !(f >= "a" && f <= "z") { s = "box-" + s }
    func fit(_ base: String, _ suffix: String) -> String {
        var b = String(base.prefix(31 - suffix.count))
        while b.hasSuffix("-") { b.removeLast() }
        return b + suffix
    }
    let have = Set(taken)
    var name = fit(s, "")
    var n = 2
    while have.contains(name) {
        name = fit(s, "-\(n)")
        n += 1
    }
    return name
}

/// The catalog machine (machines.json) of cloud machine id: the one whose
/// target is conch:<id>, as `conch cloud machine ls` keeps them.
public func catalogMachine(forCloud id: String, in catalog: [MachineEntry]) -> String? {
    catalog.first { $0.cloudID == id }?.id
}

/// The cloud machine a catalog machine is, if it is one.
public func cloudMachine(forCatalog machine: String, catalog: [MachineEntry], machines: [CloudMachine]) -> CloudMachine? {
    guard let id = catalog.first(where: { $0.id == machine })?.cloudID else { return nil }
    return machines.first { $0.id == id }
}

/// A size the account may make a machine in (`conch cloud machine sizes
/// -json`, the contract's Size).
public struct CloudSize: Decodable, Equatable, Sendable, Identifiable {
    public var id: String
    public var name: String
    public var vcpu: Int
    public var memoryGB: Int
    public var diskGB: Int
    public var plans: [String]

    enum CodingKeys: String, CodingKey {
        case id, name, vcpu, plans
        case memoryGB = "memory_gb", diskGB = "disk_gb"
    }

    public init(id: String, name: String, vcpu: Int, memoryGB: Int, diskGB: Int, plans: [String]) {
        self.id = id
        self.name = name
        self.vcpu = vcpu
        self.memoryGB = memoryGB
        self.diskGB = diskGB
        self.plans = plans
    }

    /// "Standard · 4 vCPU · 8 GB".
    public var label: String { "\(name) · \(vcpu) vCPU · \(memoryGB) GB" }

    /// Whether plan may make it; with no plan known, or none listed, yes.
    public func offered(to plan: String?) -> Bool {
        guard let plan, !plans.isEmpty else { return true }
        return plans.contains(plan)
    }
}

public struct CloudRegion: Decodable, Equatable, Sendable {
    public var id: String
    public var name: String
}

/// `conch cloud machine sizes -json`.
public struct CloudSizes: Decodable, Equatable, Sendable {
    public var sizes: [CloudSize]
    public var regions: [CloudRegion]?
}

/// The size a new machine is offered: standard when the plan has it (the
/// CLI's default too), else the first the plan has.
public func defaultCloudSize(_ sizes: [CloudSize], plan: String?) -> String? {
    let ok = sizes.filter { $0.offered(to: plan) }
    return (ok.first { $0.id == "standard" } ?? ok.first)?.id
}
