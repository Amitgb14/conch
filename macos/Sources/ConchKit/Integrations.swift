import Foundation

// The Integrations window's logic (mocks 1–3 of docs/plans/marketplace.md):
// what conch talks to besides itself, from `conch integration ls -json`
// (what this computer has), `search -json` (what the registries list) and
// `info -json` (one's page), and which projects use what, from the
// server's integration.detect. Everything that decides — a card's state,
// the filters, the counts, the banner, the install sheet's steps — is here,
// so it is tested without AppKit; the views only draw it.

/// One integration this computer has, as `conch integration ls -json` says.
public struct IntegrationHere: Decodable, Equatable, Sendable {
    public var name: String
    /// How people write it (a sandbox's "boat.dev"), where that is not the name.
    public var label: String? = nil
    public var kind: String
    public var version: String
    public var source: String
    public var state: String
    public var uses: Bool?
}

public struct IntegrationList: Decodable, Equatable, Sendable {
    public var integrations: [IntegrationHere]
    public var problems: [String]?
}

public struct IntegrationHosts: Decodable, Equatable, Sendable {
    public var api: String
    public var follow: [String]?
    public init(api: String, follow: [String]? = nil) { self.api = api; self.follow = follow }
}

public struct IntegrationChecked: Decodable, Equatable, Sendable {
    public var date: String
    public var conch: String?
    public var detail: String?
}

public struct IntegrationRevocation: Decodable, Equatable, Sendable {
    public var version: Int
    public var reason: String
}

/// One integration a registry lists, as `conch integration search -json`
/// says.
public struct IntegrationListed: Decodable, Equatable, Sendable {
    public var kind: String
    public var name: String
    public var label: String?
    /// Something conch's own code reads (GitHub, Linear, Jira), listed so
    /// the catalog is one place; there is no file to install.
    public var builtin: Bool?
    public var version: Int
    public var hosts: IntegrationHosts
    public var keyEnv: String?
    public var userEnv: String?
    public var operations: [String]?
    public var tier: String?
    public var checked: IntegrationChecked?
    public var revoked: [IntegrationRevocation]?
    public var installs: Int?
    public var note: String?
    public var signed: Bool?

    enum CodingKeys: String, CodingKey {
        case kind, name, label, builtin, version, hosts, operations, tier, checked, revoked, installs, note, signed
        case keyEnv = "key_env", userEnv = "user_env"
    }

    /// Why this version is revoked, nil when it is not.
    public var revokedReason: String? { revoked?.first(where: { $0.version == version })?.reason }
}

/// A row of "what it does in conch".
public struct IntegrationFeature: Decodable, Equatable, Sendable {
    public var what: String
    public var `where`: String
    public var level: String
}

/// One integration's page, as `conch integration info -json` says.
public struct IntegrationInfo: Decodable, Equatable, Sendable {
    public var name: String
    public var label: String
    public var kind: String
    public var source: String
    public var installed: Bool
    /// conch's own code (GitHub, Linear, Jira): set up, not installed.
    public var builtin: Bool?
    public var registry: String?
    public var version: Int?
    public var signed: Bool?
    public var tier: String?
    public var checked: IntegrationChecked?
    public var revoked: String?
    /// What it does in conch. A conch that said null (a sandbox provider,
    /// before it said what one does) is read as nothing, not as a page
    /// that cannot be opened.
    public var does: [IntegrationFeature] { doesList ?? [] }
    var doesList: [IntegrationFeature]?
    public var hosts: IntegrationHosts
    public var keyEnv: String
    public var userEnv: String?
    public var authType: String?
    public var scopes: [String]?
    public var baseURL: String?
    public var keyHelp: String?
    public var keySet: Bool
    public var note: String?
    public var docs: String?
    public var adaptedBy: String?
    public var adapted: String?
    public var sha256: String?
    public var installs: Int?

    enum CodingKeys: String, CodingKey {
        case name, label, kind, source, installed, builtin, registry, version, signed, tier, checked, revoked, hosts, scopes, note, docs, sha256, installs, adapted
        case doesList = "does"
        case keyEnv = "key_env", userEnv = "user_env", authType = "auth_type", baseURL = "base_url", keyHelp = "key_help", keySet = "key_set", adaptedBy = "adapted_by"
    }

    /// The sha256 as the page shows it: its ends.
    public var shortHash: String? {
        guard let h = sha256, h.count > 10 else { return sha256 }
        return String(h.prefix(4)) + "…" + String(h.suffix(4))
    }

    /// What its page offers: a key to give (Connect, Change Key) for one
    /// that takes a key, Install for one not here; nothing for a built-in
    /// with no key of conch's (GitHub's, through gh), or a revoked one.
    public var offersKey: Bool {
        revoked == nil && !(builtin == true && keyEnv.isEmpty) && !(isSandbox && installed)
    }

    /// A sandbox provider: installed as one, its key kept in Settings →
    /// Sandboxes rather than given here.
    public var isSandbox: Bool { kind == IntegrationKind.sandbox.rawValue }

    /// The verified chip in full: "✓ verified 2026-11-12 with conch 0.2.1 (12 pipelines read)".
    public var verifiedWords: String? {
        guard let c = checked else { return nil }
        var s = "✓ verified \(c.date)"
        if let v = c.conch, !v.isEmpty { s += " with conch \(v)" }
        if let d = c.detail, !d.isEmpty { s += " (\(d))" }
        return s
    }
}

/// A service a project uses, from the server's integration.detect.
public struct IntegrationUse: Decodable, Equatable, Sendable {
    public var name: String
    public var label: String
    public var kind: String
    public var why: String
    public var installed: Bool?
    public var connected: Bool?
    public var keyEnv: String?
    /// The repository's .conch/integrations.toml names it.
    public var team: Bool?
    enum CodingKeys: String, CodingKey { case name, label, kind, why, installed, connected, team; case keyEnv = "key_env" }
}

public struct IntegrationDetect: Decodable, Equatable, Sendable {
    public var integrations: [IntegrationUse]
    /// The repository's .conch/integrations.toml, from a server with
    /// integration.team.v1.
    public var team: IntegrationTeam?
}

/// A repository's .conch/integrations.toml: the names it lists, and
/// whether the person has said to use them ("ask", "used", "ignored").
public struct IntegrationTeam: Decodable, Equatable, Sendable {
    public var path: String
    public var names: [String]?
    public var state: String
    public var problem: String?
}

/// The kinds, in the sidebar's order, with how people write them.
public enum IntegrationKind: String, CaseIterable, Sendable {
    case host, ci, tickets, sandbox, notify
    public var title: String {
        switch self {
        case .host: return "Code hosts"
        case .ci: return "CI"
        case .tickets: return "Ticket trackers"
        case .sandbox: return "Sandboxes"
        case .notify: return "Notifications"
        }
    }
    /// The one line under a section's heading: what the kind is for in conch.
    public var purpose: String {
        switch self {
        case .host: return "pull requests, checks, merge, review comments"
        case .ci: return "failed logs to agents, re-runs, the CI panel"
        case .tickets: return "tickets to tasks, intake"
        case .sandbox: return "machines in the cloud for agents"
        case .notify: return "agent waiting, done, merged — elsewhere"
        }
    }
}

/// One of the registry's kinds — a folder of it — and what people call it,
/// as `conch integration icons -json` gives them. A kind this conch has
/// no code for is still listed; its cards say they need a newer conch.
public struct IntegrationKindInfo: Decodable, Equatable, Hashable, Sendable {
    public var kind: String
    public var label: String
    public var about: String?
    public init(kind: String, label: String, about: String? = nil) {
        self.kind = kind
        self.label = label
        self.about = about
    }
    /// Whether this conch reads integrations of this kind.
    public var known: Bool { IntegrationKind(rawValue: kind) != nil }
}

/// The registry's kinds and each integration's icon, downloaded and
/// checked by conch: `conch integration icons -json`.
public struct IntegrationIcons: Decodable, Equatable, Sendable {
    public var kinds: [IntegrationKindInfo]?
    /// A file here for each integration that has an icon, by name.
    public var icons: [String: String]?
    public var cached: Bool?
    public var problems: [String]?
    public init(kinds: [IntegrationKindInfo]? = nil, icons: [String: String]? = nil) {
        self.kinds = kinds
        self.icons = icons
    }
}

/// A card's standing: the chip in its footer.
public enum IntegrationStanding: Equatable, Sendable {
    case builtIn, installed, verified(String), notChecked, revoked(String)

    public var words: String {
        switch self {
        case .builtIn, .installed: return "✓ installed"
        case .verified(let d): return "✓ verified \(d)"
        case .notChecked: return "not checked live"
        case .revoked: return "revoked"
        }
    }
}

/// One card of the Discover grid.
public struct IntegrationCard: Equatable, Identifiable, Sendable {
    public var name: String
    public var label: String
    public var kind: String
    /// built in, ships with conch, a registry's name, or a file of your own.
    public var source: String
    public var installed: Bool
    public var builtIn: Bool
    public var standing: IntegrationStanding
    public var auth: String
    public var summary: String
    /// The projects here that use it.
    public var usedIn: [String]
    /// A project uses it and it has no key, or nothing here reads it: the
    /// card a detection points to, drawn with the accent and a primary
    /// Install.
    public var wanted: Bool
    public var id: String { name }

    public var community: Bool { source != "built in" && source != "ships with conch" && standing == .notChecked }
}

/// The Discover filter chips.
public enum IntegrationFilter: String, CaseIterable, Sendable {
    case all = "All", verified = "Verified", builtIn = "Built in", community = "Community"
}

public enum IntegrationCatalog {
    /// What a kind's file does, in two lines, from its operations.
    public static func summary(kind: String, operations: [String]) -> String {
        switch kind {
        case "tickets":
            var parts = ["Open tickets in the Tickets view", "tasks from a ticket"]
            if operations.contains("comment") || operations.contains("move") { parts.append("comment and move") }
            return parts.isEmpty ? "" : parts.joined(separator: ", ") + "."
        case "notify":
            return operations.contains("send") ? "Agent waiting, done and merged, sent there by conch's server." : ""
        case "ci":
            break
        default:
            return ""
        }
        var parts: [String] = []
        if operations.contains("failed_log") { parts.append("A failed job's output to the branch's agent") }
        if operations.contains("rerun") { parts.append("re-run from failed") }
        if operations.contains("runs") { parts.append("runs for a branch") }
        return parts.isEmpty ? "" : parts.joined(separator: ", ") + "."
    }

    /// How the key is given, in a chip's word.
    public static func authWord(userEnv: String?, builtIn: Bool, name: String) -> String {
        if builtIn { return name == "linear" || name == "jira" ? "API key" : "gh login" }
        if let u = userEnv, !u.isEmpty { return "user + token" }
        return "token"
    }

    /// Every card: this computer's integrations, then what the registries
    /// list that is not here, each with the projects that use it.
    public static func cards(here: IntegrationList?, listed: [IntegrationListed], uses: [String: [IntegrationUse]]) -> [IntegrationCard] {
        var out: [IntegrationCard] = []
        var seen = Set<String>()
        // A sandbox is the machine's, not a project's: Settings sets one up
        // for every project at once, so "used in" would name them all.
        let projectUse: (IntegrationUse, String) -> Bool = { u, name in u.name == name && u.kind != IntegrationKind.sandbox.rawValue }
        let usedIn: (String) -> [String] = { name in
            uses.filter { $0.value.contains(where: { projectUse($0, name) }) }.map(\.key).sorted()
        }
        let wantedNow: (String) -> Bool = { name in
            uses.values.contains { $0.contains { projectUse($0, name) && ($0.installed != true || $0.connected != true) } }
        }
        for h in here?.integrations ?? [] {
            seen.insert(h.name)
            let entry = listed.first(where: { $0.name == h.name })
            let builtIn = h.source == "built in"
            var standing: IntegrationStanding = builtIn ? .builtIn : .installed
            if let e = entry, let why = e.revokedReason { standing = .revoked(why) }
            out.append(IntegrationCard(name: h.name, label: entry?.label ?? h.label ?? labels[h.name] ?? h.name, kind: h.kind, source: h.source,
                                       installed: true, builtIn: builtIn, standing: standing,
                                       auth: authWord(userEnv: entry?.userEnv ?? (h.name == "jenkins" ? "JENKINS_USER" : nil), builtIn: builtIn, name: h.name),
                                       summary: builtIn ? (builtInSummary[h.name] ?? entry?.note ?? "")
                                           : entry.map { summary(kind: $0.kind, operations: $0.operations ?? []) } ?? "",
                                       usedIn: usedIn(h.name), wanted: !builtIn && wantedNow(h.name)))
        }
        for e in listed where !seen.contains(e.name) {
            seen.insert(e.name)
            if e.builtin == true {
                // conch's own, though this conch's list did not have it: as built in.
                out.append(IntegrationCard(name: e.name, label: e.label ?? e.name, kind: e.kind, source: "built in", installed: true, builtIn: true,
                                           standing: .builtIn, auth: authWord(userEnv: nil, builtIn: true, name: e.name),
                                           summary: e.note ?? builtInSummary[e.name] ?? "", usedIn: usedIn(e.name), wanted: false))
                continue
            }
            var standing: IntegrationStanding = e.checked.map { .verified($0.date) } ?? .notChecked
            if let why = e.revokedReason { standing = .revoked(why) }
            out.append(IntegrationCard(name: e.name, label: e.label ?? e.name, kind: e.kind, source: "registry",
                                       installed: false, builtIn: false, standing: standing,
                                       auth: authWord(userEnv: e.userEnv, builtIn: false, name: e.name),
                                       summary: summary(kind: e.kind, operations: e.operations ?? []),
                                       usedIn: usedIn(e.name), wanted: wantedNow(e.name)))
        }
        return out
    }

    /// What an empty list says: a search that found nothing, else why
    /// there is nothing (Verified, while no file has been checked live).
    public static func emptyWords(filter: IntegrationFilter, query: String) -> String {
        if !query.isEmpty { return "No integration matches." }
        switch filter {
        case .verified: return "None is verified yet: an integration is verified once it has been checked live against its service."
        case .community: return "No community integration yet."
        default: return "Nothing here yet."
        }
    }

    static let labels = ["github": "GitHub", "github-actions": "GitHub Actions", "github-issues": "GitHub Issues", "linear": "Linear", "jira": "Jira",
                         "circleci": "CircleCI", "gitlab-ci": "GitLab CI", "buildkite": "Buildkite", "jenkins": "Jenkins"]
    static let builtInSummary = [
        "github": "Pull requests, checks, merge, reviews, issues.",
        "github-actions": "Failed steps' logs to the branch's agent, re-run failed jobs.",
        "github-issues": "Issues to tasks, intake and triage.",
        "linear": "Tickets to tasks, comments and links.",
        "jira": "Tickets to tasks, comments and links.",
    ]

    /// The kinds the sidebar lists, in order: the registry's, else conch's
    /// own five (a conch whose registry predates kinds, or that has not
    /// read it yet), then any kind a card has that neither names.
    public static func kinds(_ registry: [IntegrationKindInfo]?, cards: [IntegrationCard]) -> [IntegrationKindInfo] {
        var out = registry?.isEmpty == false ? registry! : IntegrationKind.allCases.map { IntegrationKindInfo(kind: $0.rawValue, label: $0.title, about: $0.purpose) }
        for k in cards.map(\.kind) where !out.contains(where: { $0.kind == k }) {
            out.append(IntegrationKindInfo(kind: k, label: k.prefix(1).uppercased() + k.dropFirst()))
        }
        return out
    }

    /// The cards a filter, a kind (nil for every kind) and a search keep.
    public static func filter(_ cards: [IntegrationCard], filter: IntegrationFilter, kind: String?, query: String) -> [IntegrationCard] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        return cards.filter { c in
            if let k = kind, c.kind != k { return false }
            switch filter {
            case .all: break
            case .verified: if case .verified = c.standing {} else { return false }
            case .builtIn: if !c.builtIn { return false }
            case .community: if !c.community { return false }
            }
            return q.isEmpty || c.name.contains(q) || c.label.lowercased().contains(q) || c.kind == q
        }
    }

    /// The sidebar's `installed / listed` for a kind.
    public static func count(_ cards: [IntegrationCard], kind: String) -> (installed: Int, listed: Int) {
        let of = cards.filter { $0.kind == kind }
        return (of.filter(\.installed).count, of.count)
    }

    /// The detection banner: each project that uses a service with no key
    /// or nothing to read it, in words — "ceph builds on CircleCI
    /// (.circleci/config.yml)". Sandboxes and notifications are not what a
    /// project uses but where its work runs and what it tells you, so they
    /// are not found in it. Empty when there is nothing to say.
    public static func banner(_ uses: [String: [IntegrationUse]]) -> [String] {
        var out: [String] = []
        for project in uses.keys.sorted() {
            for u in uses[project] ?? [] where found(u) {
                let verb = u.kind == "host" ? "is on" : u.kind == "tickets" ? "tracks its work in" : "builds on"
                out.append("\(project) \(verb) \(u.label) (\(u.why))")
            }
        }
        return out
    }

    /// Whether a use is one the banner names: a CI, host or tracker with
    /// no key or nothing here to read it.
    static func found(_ u: IntegrationUse) -> Bool {
        ["ci", "host", "tickets"].contains(u.kind) && (u.installed != true || u.connected != true)
    }

    /// The names the banner's Show button filters to.
    public static func bannerNames(_ uses: [String: [IntegrationUse]]) -> Set<String> {
        Set(uses.values.flatMap { $0 }.filter(found).map(\.name))
    }
}

/// The install sheet (mock 3): agree to the hosts, give the key — and the
/// address of a server of your own, and a user for basic auth — then one
/// read-only check. Its state, and the conch command it runs.
public struct IntegrationInstallFlow: Equatable, Sendable {
    public var info: IntegrationInfo
    public var agreed = false
    public var base = ""
    public var user = ""
    public var key = ""
    /// The checkout of a project that uses it, to check the key against.
    public var checkIn: String?
    public enum Check: Equatable, Sendable { case idle, running, ok(String), failed(String) }
    public var check: Check = .idle

    public init(info: IntegrationInfo) { self.info = info }

    /// Connecting one already installed, from a project's sheet: where its
    /// key goes was agreed when it was installed, so the sheet opens at the
    /// key.
    public init(connecting info: IntegrationInfo) {
        self.info = info
        agreed = info.installed && info.revoked == nil
    }

    /// The step the sheet is on: the first not done.
    public var step: Int {
        if !agreed || (asksBase && base.isEmpty) { return 1 }
        if key.isEmpty && !info.keySet { return 2 }
        if case .ok = check { return 4 }
        return 3
    }

    public var asksBase: Bool { info.baseURL == "ask" }
    public var asksUser: Bool { (info.userEnv ?? "").isEmpty == false }

    /// The hosts in step 1's words: "circleci.com only", or a server of
    /// your own.
    public var hostsWords: String {
        let api = asksBase ? (base.isEmpty ? "the address you give" : hostOf(base)) : info.hosts.api
        var s = "Your \(info.label) key will be sent to \(api) only."
        if let f = info.hosts.follow, !f.isEmpty { s += " Logs are read from \(f.joined(separator: ", ")) without it." }
        return s
    }

    /// What stops Install, in words; nil when it can be pressed.
    public var blocker: String? {
        if let why = info.revoked { return "revoked: \(why)" }
        if !agreed { return "agree to where the key goes" }
        if asksBase && !base.lowercased().hasPrefix("https://") { return "the address of your server, https://…" }
        if asksUser && user.trimmingCharacters(in: .whitespaces).isEmpty { return "your user name" }
        if key.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && !info.keySet && !info.isSandbox { return "the key" }
        if case .running = check { return "checking…" }
        return nil
    }

    /// The conch command install runs; the key goes on its stdin, never
    /// in these.
    public var arguments: [String] {
        if info.isSandbox { return ["integration", "install", "-y", info.name] }
        var args = info.installed ? ["integration", "key", "-stdin"] : ["integration", "install", "-y", "-key-stdin"]
        if asksBase && !base.isEmpty { args += ["-base", base] }
        if let dir = checkIn, !dir.isEmpty { args += ["-cwd", dir] }
        return args + [info.name]
    }

    /// Reads what conch said back into the check's state.
    public mutating func finished(output: String, error: String?) {
        if let e = error {
            check = .failed(e)
            return
        }
        let line = output.split(separator: "\n").map(String.init).first(where: { $0.hasPrefix("checked:") || $0.hasPrefix("the check failed") || $0.hasPrefix("not checked") })
        switch line {
        case let l? where l.hasPrefix("checked:"): check = .ok(String(l.dropFirst("checked: ".count)))
        case let l? where l.hasPrefix("the check failed"): check = .failed(l)
        case let l?: check = .ok(l)
        case nil:
            // No check was made (a built-in's key, a file with no project
            // to read): what conch said last, "kept in the Keychain …".
            let said = output.split(separator: "\n").map { $0.trimmingCharacters(in: .whitespaces) }.last { !$0.isEmpty }
            check = .ok(said ?? "installed")
        }
    }
}

func hostOf(_ url: String) -> String {
    var s = url
    if let r = s.range(of: "://") { s = String(s[r.upperBound...]) }
    return String(s.split(separator: "/", maxSplits: 1).first ?? "")
}
