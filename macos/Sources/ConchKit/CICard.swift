import Foundation

// A red check from a service conch cannot read is a dead end unless the
// branch says so: the agent was not sent the log, and nothing else says
// why. So the git tab puts a card over the pull request — the check, the
// service, Install (the Integrations window's install sheet), Open in the
// service, and why conch thinks the project uses it — and lists every
// check with where it ran. The server marks the check readable once an
// integration reads it, and the card goes (docs/plans/marketplace-mocks m4).

/// How people write a CI source; the server's names for those it knows.
public func ciServiceLabel(_ source: String) -> String {
    switch source {
    case "github-actions": return "GitHub Actions"
    case "circleci": return "CircleCI"
    case "gitlab-ci": return "GitLab CI"
    case "buildkite": return "Buildkite"
    case "jenkins": return "Jenkins"
    case "bitbucket-pipelines": return "Bitbucket Pipelines"
    default: return source
    }
}

/// Where a check ran, for the right of its line: its link's host — or,
/// for a passing check of a service conch cannot read, that nothing needs
/// reading.
public func checkWhere(_ c: PRCheck) -> String {
    if c.state == "pass", c.readable != true, let s = c.source, !s.isEmpty, s != "github-actions" {
        return "no integration needed to see it"
    }
    guard let u = c.url, let host = URL(string: u)?.host else { return "" }
    return host
}

/// Whether a pull request has failed jobs to run again. A server that
/// says which checks can be run again (pr.check.rerun.v1, checksSay) is
/// taken at its word: a failing check of a CI whose integration has no
/// re-run offers none.
public func ciCanRerun(_ pr: PRInfo?, checksSay: Bool = false) -> Bool {
    guard let pr else { return false }
    if checksSay { return (pr.runs ?? []).contains { $0.state == "fail" && $0.rerun == true } }
    return pr.checks == "fail" || (pr.runs ?? []).contains { $0.state == "fail" }
}

/// The card for the first failing check of a service conch knows and
/// cannot read; nil when there is none. An older server names no source,
/// so it never has one.
public struct CICard: Equatable, Sendable {
    public let check: PRCheck
    public let service: String
    /// Why the project is thought to use it, from integration.detect.
    public let found: String?

    public init?(_ pr: PRInfo?, uses: [IntegrationUse]) {
        guard let c = pr?.runs?.first(where: { $0.state == "fail" && $0.readable != true && !($0.source ?? "").isEmpty && $0.source != "github-actions" }),
              let source = c.source else { return nil }
        check = c
        service = ciServiceLabel(source)
        found = uses.first { $0.name == source && !$0.why.isEmpty }.map { "Found " + $0.why }
    }

    /// The integration to install: the source's name.
    public var name: String { check.source ?? "" }
    public var install: String { "Install \(service)…" }
    public var open: String { "Open in \(service)" }

    public func text(agent: String?) -> String {
        "\(check.name) failed — conch can't read \(service) yet, so \(agent ?? "the agent") wasn't sent the log."
    }
}

/// A failed CI run as a note names it.
public struct NoteCI: Decodable, Equatable, Sendable {
    public var source: String?
    public var service: String?
    public var workflow: String?
    public var job: String?
    public var url: String?
    public var lines: Int?
    public var host: String?

    public init(source: String? = nil, service: String? = nil, workflow: String? = nil, job: String? = nil, url: String? = nil, lines: Int? = nil, host: String? = nil) {
        self.source = source
        self.service = service
        self.workflow = workflow
        self.job = job
        self.url = url
        self.lines = lines
        self.host = host
    }

    public func headline(branch: String) -> String {
        var parts: [String] = []
        if let s = [service, source].compactMap({ $0 }).first(where: { !$0.isEmpty }) { parts.append(s) }
        if let w = workflow, !w.isEmpty { parts.append("workflow " + w) }
        if let j = job, !j.isEmpty { parts.append("job " + j) }
        return "CI failed on \(branch)" + (parts.isEmpty ? "" : " — " + parts.joined(separator: " · "))
    }

    /// How much of its log was read, and from where; nil when none was.
    public var read: String? {
        guard let n = lines, n > 0 else { return nil }
        var s = n == 1 ? "read 1 line of the failed log" : "read \(n) lines of the failed log"
        if let h = host, !h.isEmpty { s += " from " + h }
        return s
    }
}
