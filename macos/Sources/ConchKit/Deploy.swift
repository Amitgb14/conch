import Foundation

// A branch's deploy previews (Vercel, Netlify) in the git tab, as the
// deploy mock has them: the newest deploy's state as a chip in the
// branch header, and on the overview a row per deploy — state, commit,
// the preview's link or why it failed, when — with Build log, Redeploy,
// and Promote/Publish to production behind a sheet in the service's own
// words. The server says what may be released and how it is put
// (branch.previews.v1); this decides how it is drawn, so it is tested
// without AppKit.

public let branchPreviewsCapability = "branch.previews.v1"

/// How releasing to production is said for a service.
public struct DeployRelease: Decodable, Equatable, Sendable {
    public var verb: String
    public var what: String
    public var note: String?
    /// Releasing starts a new production build (Vercel); else it is
    /// immediate (Netlify).
    public var rebuilds: Bool?
    public init(verb: String, what: String, note: String? = nil, rebuilds: Bool? = nil) {
        self.verb = verb; self.what = what; self.note = note; self.rebuilds = rebuilds
    }
}

/// One deploy, newest first in its list.
public struct DeployInfo: Decodable, Equatable, Identifiable, Sendable {
    public var id: String
    public var name: String?
    public var state: String
    public var url: String?
    public var environment: String?
    public var commit: String?
    public var subject: String?
    public var error: String?
    public var inspect: String?
    public var started: Date?
    public var finished: Date?
    public var atHead: Bool?
    public var promotable: Bool?
    public var notPromotable: String?

    enum CodingKeys: String, CodingKey {
        case id, name, state, url, environment, commit, subject, error, inspect, started, finished, promotable
        case atHead = "at_head", notPromotable = "not_promotable"
    }

    public init(id: String, state: String, url: String? = nil, environment: String? = nil, commit: String? = nil, subject: String? = nil,
                error: String? = nil, inspect: String? = nil, started: Date? = nil, finished: Date? = nil,
                atHead: Bool? = nil, promotable: Bool? = nil, notPromotable: String? = nil, name: String? = nil) {
        self.id = id; self.state = state; self.url = url; self.environment = environment; self.commit = commit
        self.subject = subject; self.error = error; self.inspect = inspect; self.started = started; self.finished = finished
        self.atHead = atHead; self.promotable = promotable; self.notPromotable = notPromotable; self.name = name
    }
}

/// One deploy integration's word on a branch.
public struct DeploySource: Decodable, Equatable, Identifiable, Sendable {
    public var source: String
    public var service: String
    public var why: String?
    /// Nothing on the service builds from this repository, in its words.
    public var missing: String?
    /// The pull request's head the deploys were read at; nil for a branch
    /// with none, whose deploys are its history.
    public var head: String?
    /// Its failed_log reads a build's output; else (Netlify) a failed
    /// deploy's log is its error message.
    public var buildLog: Bool?
    public var deploys: [DeployInfo]?
    public var redeploy: Bool?
    public var promote: Bool?
    public var release: DeployRelease?
    public var id: String { source }

    enum CodingKeys: String, CodingKey {
        case source, service, why, missing, head, deploys, redeploy, promote, release
        case buildLog = "build_log"
    }

    public init(source: String, service: String, why: String? = nil, missing: String? = nil, head: String? = nil, buildLog: Bool? = nil,
                deploys: [DeployInfo]? = nil, redeploy: Bool? = nil, promote: Bool? = nil, release: DeployRelease? = nil) {
        self.source = source; self.service = service; self.why = why; self.missing = missing; self.head = head; self.buildLog = buildLog
        self.deploys = deploys; self.redeploy = redeploy; self.promote = promote; self.release = release
    }
}

public struct BranchPreviews: Decodable, Equatable, Sendable {
    public var integrations: [DeploySource]
    public init(integrations: [DeploySource]) { self.integrations = integrations }
}

public struct BranchDeployDone: Decodable, Equatable, Sendable {
    public var source: String
    public var service: String
    public var deploy: String
}

/// A deploy's state, as the chip and the row draw it.
public enum DeployTone: Equatable, Sendable { case ok, bad, busy, quiet }

public enum Deploys {
    /// "Ready", "Failed", "Building", "Canceled" — else the state itself.
    public static func stateText(_ state: String) -> String {
        switch state {
        case "ready": return "Ready"
        case "failed": return "Failed"
        case "building": return "Building"
        case "canceled": return "Canceled"
        default: return state.isEmpty ? "Unknown" : state.prefix(1).uppercased() + state.dropFirst()
        }
    }

    public static func tone(_ state: String) -> DeployTone {
        switch state {
        case "ready": return .ok
        case "failed": return .bad
        case "building": return .busy
        default: return .quiet
        }
    }

    /// A commit as the rows write it: seven characters.
    public static func short(_ sha: String?) -> String { String((sha ?? "").prefix(7)) }

    /// A link as shown: no scheme, no trailing slash.
    public static func shown(_ url: String?) -> String {
        var s = url ?? ""
        if let r = s.range(of: "://") { s = String(s[r.upperBound...]) }
        while s.hasSuffix("/") { s.removeLast() }
        return s
    }

    /// The header's chip: the newest deploy of the first integration with
    /// one — "Vercel · Ready" — and its tone; nil when there is none.
    public static func chip(_ p: BranchPreviews?) -> (text: String, tone: DeployTone, source: DeploySource, deploy: DeployInfo)? {
        for s in p?.integrations ?? [] {
            if let d = s.deploys?.first {
                return ("\(s.service) · \(stateText(d.state))", tone(d.state), s, d)
            }
        }
        return nil
    }

    /// The newest deploy that is ready, with its link: what the pull
    /// request's line opens.
    public static func newestReady(_ p: BranchPreviews?) -> (source: DeploySource, deploy: DeployInfo)? {
        for s in p?.integrations ?? [] {
            if let d = s.deploys?.first(where: { $0.state == "ready" && ($0.url ?? "").isEmpty == false }) { return (s, d) }
        }
        return nil
    }

    /// Whether the overview has anything to show: an integration, with
    /// deploys or with why it has none.
    public static func hasAny(_ p: BranchPreviews?) -> Bool { !(p?.integrations.isEmpty ?? true) }

    /// How often an open git tab asks for its previews again; the server
    /// keeps an answer as long itself.
    public static let askEvery: TimeInterval = 15

    /// Whether to ask again: never asked, asked askEvery ago, or a clock
    /// that moved back.
    public static func askAgain(lastAt: Date?, now: Date = Date()) -> Bool {
        guard let at = lastAt else { return true }
        let gone = now.timeIntervalSince(at)
        return gone >= askEvery || gone < 0
    }

    /// What the tab says when the previews could not be read.
    public static func readFailure(_ error: String) -> String {
        error.isEmpty ? "Could not read the deploy previews." : "Could not read the deploy previews: \(error)"
    }

    /// What a row's actions are, from what the server says of it and its
    /// integration.
    public static func actions(_ d: DeployInfo, in s: DeploySource) -> DeployActions {
        let failed = d.state == "failed"
        return DeployActions(release: d.promotable == true && s.promote == true && s.release != nil,
                             notReleasable: d.state == "ready" ? d.notPromotable : nil,
                             log: failed, redeploy: failed && s.redeploy == true, send: failed)
    }

    /// The line under a deploy's commit: the commit's subject, read from
    /// this repository's git by the server. A deploy's name (Vercel's
    /// project name) is not a commit message, so without git's there is
    /// none.
    public static func message(_ d: DeployInfo) -> String { d.subject ?? "" }

    /// What a building or canceled deploy says where a link or error goes.
    public static func quietText(_ d: DeployInfo) -> String? {
        switch d.state {
        case "building": return "Building…"
        case "canceled": return "Canceled before it was ready"
        default: return nil
        }
    }

    /// Above the table: which deploys these are.
    public static func scopeLine(_ s: DeploySource) -> String {
        if let h = s.head, !h.isEmpty {
            return "Deploys at the pull request's head, \(short(h)), newest first. Earlier commits' deploys aren't listed."
        }
        return "No pull request: the branch's deploys, newest first."
    }

    /// A failed deploy's button: its build log, or (Netlify) its error.
    public static func logLabel(_ s: DeploySource) -> String { s.buildLog == true ? "Build log" : "Error message" }

    /// Under an error shown in place of a log: why there is no log.
    public static func noLogNote(_ s: DeploySource) -> String? {
        s.buildLog == true ? nil : "\(s.service)'s API gives no build log: this is the deploy's error message. The full log is on \(s.service)'s deploy page."
    }

    /// Beside Send to the branch's agent: what it is sent.
    public static func sendWhat(_ s: DeploySource) -> String {
        s.buildLog == true ? "As a failed CI run is: the end of the log, with the commit." : "As a failed CI run is: the error message, with the commit."
    }

    /// A reason's first sentence, for a row; the whole goes under the table.
    public static func firstSentence(_ s: String) -> String {
        if let r = s.range(of: ". ") { return String(s[..<r.lowerBound]) }
        return s.hasSuffix(".") ? String(s.dropLast()) : s
    }

    /// The reasons a ready deploy here may not be released, each once.
    public static func notReleasableNotes(_ s: DeploySource) -> [String] {
        var out: [String] = []
        for d in s.deploys ?? [] {
            if let w = d.notPromotable, !w.isEmpty, !out.contains(w) { out.append(w) }
        }
        return out
    }

    /// After a release that starts a new production build (Vercel): that
    /// build, shown as building until the service lists it.
    public static func pendingBuild(_ c: DeployConfirm) -> DeployInfo? {
        guard c.source.release?.rebuilds == true else { return nil }
        return DeployInfo(id: "pending-production-" + c.deploy.id, state: "building", url: nil, environment: "production",
                          commit: c.deploy.commit, subject: c.deploy.subject, started: Date())
    }

    /// The previews with a pending production build first in its
    /// integration's list, until the service lists a production deploy of
    /// that commit itself.
    public static func with(_ p: BranchPreviews, pending: [String: DeployInfo]) -> BranchPreviews {
        var out = p
        for i in out.integrations.indices {
            guard let b = pending[out.integrations[i].source] else { continue }
            let deps = out.integrations[i].deploys ?? []
            if deps.contains(where: { $0.environment == "production" && $0.commit == b.commit && $0.id != b.id }) { continue }
            out.integrations[i].deploys = [b] + deps.filter { $0.id != b.id }
        }
        return out
    }

    /// What is said once a release was asked for.
    public static func releasedWords(_ c: DeployConfirm) -> String {
        if c.source.release?.rebuilds == true {
            return "\(c.source.service) is building \(c.what) for production: the production domains move to it once that build is ready."
        }
        return "\(c.source.service) now serves \(c.what): it published the deploy as it was built."
    }

    /// When it was built, in the rows' words: "2 min ago".
    public static func when(_ d: DeployInfo, now: Date = Date()) -> String {
        guard let t = d.finished ?? d.started else { return "" }
        return agoText(t, now: now)
    }
}

/// The buttons a deploy's row has.
public struct DeployActions: Equatable, Sendable {
    /// Promote / Publish to production.
    public var release: Bool
    /// Why a ready deploy cannot be released (a Netlify deploy preview),
    /// said where the button would be.
    public var notReleasable: String?
    public var log: Bool
    public var redeploy: Bool
    /// Send the build's log to the branch's agent.
    public var send: Bool
}

/// The confirmation before releasing to production, in the service's own
/// words: "Promote 3f2a91c to production?".
public struct DeployConfirm: Equatable, Identifiable, Sendable {
    public var source: DeploySource
    public var deploy: DeployInfo
    public var id: String { source.source + "|" + deploy.id }

    public init(source: DeploySource, deploy: DeployInfo) { self.source = source; self.deploy = deploy }

    public var verb: String { source.release?.verb ?? "Promote" }
    /// The deploy as the title names it: its commit, else its id.
    public var what: String { Deploys.short(deploy.commit).isEmpty ? deploy.id : Deploys.short(deploy.commit) }
    public var title: String { "\(verb) \(what) to production?" }
    public var body: String { source.release?.what ?? "This deploy is released to production on \(source.service)." }
    public var note: String? { source.release?.note }
    /// The box under the words: where it is and what goes there.
    public var change: (from: String, to: String) {
        ("\(source.service) production", "\(what) · \(Deploys.shown(deploy.url).isEmpty ? deploy.id : Deploys.shown(deploy.url))")
    }
}

/// The arguments of `conch branch fix-ci` that send one deploy's build log
/// to the branch's agent, as the app's other fixes run it.
public func fixDeployArguments(dir: String, branch: String, agent: String, source: String, deploy: String) -> [String] {
    ["branch", "fix-ci", "-cwd", dir, "-branch", branch, "-agent", agent, "-source", source, "-deploy", deploy]
}

/// Where a deploy integration's previews show, for its page in the
/// Integrations window: the projects here that deploy there.
public enum DeployPlaces {
    /// Each project that uses the integration named, by name, with why.
    public static func projects(_ name: String, uses: [String: [IntegrationUse]]) -> [(project: String, why: String)] {
        uses.keys.sorted().compactMap { p in
            uses[p]?.first(where: { $0.name == name }).map { (p, $0.why) }
        }
    }

    /// What the page says when no project here deploys there.
    public static func none(_ info: IntegrationInfo) -> String {
        let lead = "No project here deploys to \(info.label). A branch's previews show in its git tab once a project"
        let say = " Or turn it on in a project's Integrations sheet, for one \(info.label) links from its own side."
        switch info.name {
        case "vercel": return lead + " has vercel.json or .vercel/project.json, or a pull request with a Vercel check." + say
        case "netlify": return lead + " has netlify.toml, or a pull request with a netlify/… check." + say
        default: return lead + " uses it." + say
        }
    }
}

extension IntegrationInfo {
    /// The key's help as conch shows it. Vercel's is a personal account's
    /// token: a project owned by a team needs a teamId no file can ask for
    /// yet, so it says so rather than suggest scoping it to one.
    public var keyHelpShown: String? {
        guard name == "vercel", kind == IntegrationKind.deploy.rawValue else { return keyHelp }
        return "Vercel → Account Settings → Tokens → Create Token, for your personal account. Projects owned by a Vercel team aren't supported yet."
    }
}
