import Foundation

// The New integration sheet (Integrations → ✦ Adapt from API docs…,
// docs/plans/marketplace-mocks m7): what it asks for, as `conch
// integration adapt` takes it, and the progress list it draws from the
// lines conch prints (AdaptProgress). The key never passes through here:
// conch reads it, and only once the person has pressed the button that
// says where it goes.

/// What the sheet asks for.
public struct IntegrationAdaptForm: Equatable, Sendable {
    public var kind: IntegrationKind = .ci
    public var service = ""
    public var docs = ""
    public var agent = "claude"
    /// The checkout the live check reads the runs of.
    public var project: String?
    /// Adapt it even when conch ships it or a registry lists it.
    public var anyway = false
    public init() {}

    /// The kinds this conch adapts; the others are shown, and say so.
    public static let adaptable: Set<IntegrationKind> = [.ci, .sandbox]

    /// The integration's name: the service as typed, made a name, else
    /// the docs' host as conch reads it (docs.semaphoreci.com is semaphoreci).
    public var name: String {
        let typed = service.trimmingCharacters(in: .whitespaces).lowercased()
            .replacingOccurrences(of: " ", with: "-")
        if !typed.isEmpty { return typed }
        var f = AdaptForm()
        f.url = docs
        return adaptName(f)
    }

    public func arguments() -> Result<[String], NewSandboxError> {
        guard Self.adaptable.contains(kind) else {
            return .failure(.problem("conch adapts CI services and sandbox providers so far; \(kind.title.lowercased()) are still to come"))
        }
        let u = docs.trimmingCharacters(in: .whitespaces)
        guard let parsed = URL(string: u), let scheme = parsed.scheme, ["http", "https"].contains(scheme), parsed.host != nil else {
            return .failure(.problem("the address of the service's API docs, starting https://"))
        }
        var args = ["integration", "adapt", "-json", "-kind", kind.rawValue, "-agent", agent.isEmpty ? "claude" : agent]
        let typed = service.trimmingCharacters(in: .whitespaces)
        if !typed.isEmpty {
            guard name.range(of: "^[a-z][a-z0-9-]{1,30}$", options: .regularExpression) != nil else {
                return .failure(.problem("service: letters, digits and -, starting with a letter"))
            }
            args += ["-name", name]
        }
        if anyway { args.append("-new") }
        if kind != .sandbox, let p = project, !p.isEmpty { args += ["-cwd", p] }
        return .success(args + [u])
    }

    /// Checks the draft with the key: the person's yes to where it goes.
    public func checkArguments() -> [String] {
        var a = ["integration", "adapt", "-json", "-check"]
        if let p = project, !p.isEmpty { a += ["-cwd", p] }
        return a + [name]
    }

    public func saveArguments() -> [String] { ["integration", "adapt", "-save", name] }

    /// -y: the sheet said where the key goes before Publish was pressed.
    public func publishArguments() -> [String] {
        var a = ["integration", "publish", "-json", "-y"]
        if let p = project, !p.isEmpty { a += ["-cwd", p] }
        return a + [name]
    }
}

/// Where publishing has got to.
public struct IntegrationPublishState: Equatable, Sendable {
    public var running = false
    public var url = ""
    public var error = ""
    public init() {}
}

/// One row of the progress list.
public struct IntegrationAdaptStep: Equatable, Sendable, Identifiable {
    public enum State: Equatable, Sendable { case done, running, waiting, failed }
    public var id: String
    public var title: String
    public var detail: String
    public var state: State
}

public enum IntegrationAdaptSteps {
    /// The four rows of mock 7: Drafted, Offline check, Live check,
    /// Publish — each waiting until the one before it is done.
    public static func steps(_ p: AdaptProgress, name: String, checking: Bool = false, publish: IntegrationPublishState = .init()) -> [IntegrationAdaptStep] {
        var out: [IntegrationAdaptStep] = []
        let file = name.isEmpty ? "the file" : name + ".toml"
        // Drafted
        if !p.file.isEmpty {
            var d = p.operations.joined(separator: ", ")
            if !p.note.isEmpty { d += (d.isEmpty ? "" : "; ") + p.note }
            out.append(.init(id: "draft", title: "Drafted \(file)", detail: d, state: .done))
        } else if p.phase == .failed {
            out.append(.init(id: "draft", title: "Drafting \(file)", detail: p.error, state: .failed))
        } else if p.phase == .docs || p.phase == .writing {
            let who = p.agent.isEmpty ? "The agent" : p.agent.capitalized
            out.append(.init(id: "draft", title: "\(who) is writing \(file)", detail: p.status.isEmpty ? (p.docs.last ?? "reading the docs…") : p.status,
                             state: p.blocked ? .waiting : .running))
        } else {
            out.append(.init(id: "draft", title: "Draft \(file)", detail: "an agent reads the API docs, in a pane of its own", state: .waiting))
        }
        // Offline check
        if p.offline {
            out.append(.init(id: "offline", title: "Offline check: 0 problems", detail: "", state: .done))
        } else if !p.problems.isEmpty {
            out.append(.init(id: "offline", title: "Offline check: \(p.problems.count) problem\(p.problems.count == 1 ? "" : "s")",
                             detail: p.problems.first ?? "", state: .failed))
        } else {
            out.append(.init(id: "offline", title: "Offline check", detail: "", state: .waiting))
        }
        // Live check
        if checking {
            out.append(.init(id: "live", title: "Live check with your key…", detail: p.consent, state: .running))
        } else if let l = p.live {
            out.append(.init(id: "live", title: l.ok ? "Live check with your key" : "Live check: not passed", detail: l.words, state: l.ok ? .done : .failed))
        } else {
            out.append(.init(id: "live", title: "Live check with your key", detail: p.consent.isEmpty ? "the agent never sees the key" : "sends " + p.consent,
                             state: .waiting))
        }
        // Publish
        if publish.running {
            out.append(.init(id: "publish", title: "Publishing to conch-integrations…", detail: "a pull request, through gh", state: .running))
        } else if !publish.url.isEmpty {
            out.append(.init(id: "publish", title: "Proposed to conch-integrations", detail: publish.url, state: .done))
        } else if !publish.error.isEmpty {
            out.append(.init(id: "publish", title: "Publish to conch-integrations", detail: publish.error, state: .failed))
        } else {
            out.append(.init(id: "publish", title: "Publish to conch-integrations", detail: "a pull request, with the live check's result", state: .waiting))
        }
        return out
    }

    /// Whether the live check can be asked for: a clean draft, not yet checked.
    public static func canCheck(_ p: AdaptProgress) -> Bool { p.offline && p.error.isEmpty && p.live?.ok != true }
    /// Whether Publish can be pressed: a draft that worked with the key.
    public static func canPublish(_ p: AdaptProgress) -> Bool { p.offline && p.live?.ok == true }
}
