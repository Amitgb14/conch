import Foundation

// A project's integrations (mock 5 of docs/plans/marketplace.md): the sheet
// a project's Integrations… opens, one row per kind — what it is hosted
// on, which CI and tracker it uses, the sandboxes set up — each with its
// state, from the server's integration.detect. And the repository's
// .conch/integrations.toml: asked about once, saved from the checkbox.

/// A use's state, as a chip.
public enum ProjectIntegrationChip: Equatable, Sendable {
    case connected, keyNotSet, notInstalled, setUp

    public var words: String {
        switch self {
        case .connected: return "connected"
        case .keyNotSet: return "key not set"
        case .notInstalled: return "not installed"
        case .setUp: return "set up"
        }
    }
}

/// What a row's button does.
public enum ProjectIntegrationAction: Equatable, Sendable {
    /// Give an installed integration its key: the install sheet at its key.
    case connect(name: String, label: String)
    /// Install one the project uses and nothing here reads.
    case install(name: String, label: String)
    /// The Integrations window, at the kind.
    case browse(IntegrationKind)

    public var words: String {
        switch self {
        case .connect(_, let l): return "Connect \(l)…"
        case .install(_, let l): return "Install \(l)…"
        case .browse: return "Browse…"
        }
    }
}

/// One row of the sheet.
public struct ProjectIntegrationRow: Equatable, Identifiable, Sendable {
    public var kind: IntegrationKind
    public var title: String
    /// "CircleCI, GitHub Actions", or "none".
    public var names: String
    /// Why: "CircleCI from .circleci/config.yml · GitHub Actions from .github/workflows".
    public var detail: String
    public var chips: [ProjectIntegrationChip]
    public var actions: [ProjectIntegrationAction]
    public var id: String { kind.rawValue }
}

public enum ProjectIntegrations {
    static func title(_ k: IntegrationKind) -> String {
        switch k {
        case .host: return "Code host"
        case .ci: return "CI"
        case .tickets: return "Tickets"
        case .sandbox: return "Sandboxes"
        case .notify: return "Notifications"
        case .deploy: return "Deploy previews"
        }
    }

    /// What a row with nothing says under "none": what there is to have.
    static func examples(_ k: IntegrationKind) -> String {
        switch k {
        case .host: return "no remote conch knows"
        case .ci: return "CircleCI, GitLab CI, Buildkite, Jenkins…"
        case .tickets: return "Linear, Jira, GitHub Issues…"
        case .sandbox: return "Daytona, E2B, boat.dev…"
        case .notify: return "Slack, Discord, ntfy…"
        case .deploy: return "Vercel, Netlify…"
        }
    }

    public static func chip(_ u: IntegrationUse) -> ProjectIntegrationChip {
        if u.kind == IntegrationKind.sandbox.rawValue { return .setUp }
        if u.installed != true { return .notInstalled }
        return u.connected == true ? .connected : .keyNotSet
    }

    /// Why a use is there, in the sheet's words.
    static func why(_ u: IntegrationUse) -> String {
        u.why.hasSuffix(" remote") ? "from the \(u.why)" : u.why
    }

    /// The rows, one per kind in the sidebar's order.
    public static func rows(_ d: IntegrationDetect?) -> [ProjectIntegrationRow] {
        IntegrationKind.allCases.map { k in
            let uses = (d?.integrations ?? []).filter { $0.kind == k.rawValue }
            guard !uses.isEmpty else {
                return ProjectIntegrationRow(kind: k, title: title(k), names: "none", detail: examples(k), chips: [], actions: [.browse(k)])
            }
            let detail: String
            if uses.count == 1 {
                detail = why(uses[0])
            } else {
                detail = uses.map { "\($0.label) from \(why($0))" }.joined(separator: " · ")
            }
            var actions: [ProjectIntegrationAction] = []
            for u in uses {
                switch chip(u) {
                case .keyNotSet: actions.append(.connect(name: u.name, label: u.label))
                case .notInstalled: actions.append(.install(name: u.name, label: u.label))
                default: break
                }
            }
            return ProjectIntegrationRow(kind: k, title: title(k), names: uses.map(\.label).joined(separator: ", "),
                                         detail: detail, chips: uses.map(chip), actions: actions)
        }
    }

    /// What ticking "Save in .conch/integrations.toml for the team" writes:
    /// the names of what the project uses that something here reads, and
    /// whatever the repository's file already lists, which saving must not
    /// drop — a name, never a key. Sandboxes are this computer's, not the
    /// repository's.
    public static func teamNames(_ d: IntegrationDetect?) -> [String] {
        var names = (d?.integrations ?? []).filter { $0.installed == true && $0.kind != IntegrationKind.sandbox.rawValue }.map(\.name)
        if let t = d?.team, t.problem == nil { names += t.names ?? [] }
        return Array(Set(names)).sorted()
    }

    /// Whether the checkbox is ticked: the file is there, sound, and used.
    public static func saved(_ d: IntegrationDetect?) -> Bool {
        guard let t = d?.team else { return false }
        return t.problem == nil && t.state == "used"
    }

    /// The question asked once per repository, nil when there is none to
    /// ask: "This repository's .conch/integrations.toml says it uses
    /// CircleCI and Jira. Use them?"
    public static func question(_ d: IntegrationDetect?) -> String? {
        guard let t = d?.team, t.state == "ask", t.problem == nil, let names = t.names, !names.isEmpty else { return nil }
        let labels = names.map { IntegrationCatalog.labels[$0] ?? $0 }
        let list = labels.count == 1 ? labels[0] : labels.dropLast().joined(separator: ", ") + " and " + labels.last!
        return "This repository's \(t.path) says it uses \(list). Use \(labels.count == 1 ? "it" : "them")?"
    }
}
