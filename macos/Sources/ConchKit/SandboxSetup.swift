import Foundation

// Setting up a sandbox, as New Sandbox's sheet shows it: the steps
// `conch sandbox create -progress FILE` writes as JSON lines
// (cmd/conch/sandboxprogress.go), read back into where it is.

/// One step of setting up a sandbox, in order.
public enum SandboxStep: String, CaseIterable, Sendable {
    case create, answer, install, check, add

    /// The step as the sheet names it while it is to come or under way.
    public var title: String {
        switch self {
        case .create: return "Create the sandbox"
        case .answer: return "Wait for it to answer"
        case .install: return "Install conch"
        case .check: return "Check it is this build"
        case .add: return "Add it to the sidebar"
        }
    }

    /// The step once done.
    public var doneTitle: String {
        switch self {
        case .create: return "Created the sandbox"
        case .answer: return "It answered"
        case .install: return "Installed conch"
        case .check: return "Checked it is this build"
        case .add: return "Added it to the sidebar"
        }
    }
}

public enum SandboxStepState: Equatable, Sendable { case later, now, done, failed }

/// Where a setup is, from the lines written so far.
public struct SandboxSetupState: Equatable, Sendable {
    public var states: [SandboxStep: SandboxStepState] = [:]
    /// When each step started and ended, for its time.
    public var started: [SandboxStep: Date] = [:]
    public var ended: [SandboxStep: Date] = [:]
    /// The last line under each step: what it is doing now.
    public var detail: [SandboxStep: String] = [:]
    /// Every line, in order: the sheet's Details.
    public var lines: [String] = []
    public var sandbox: String?
    public var machine: String?
    public var label: String?
    public var first: Date?
    public var last: Date?
    public enum End: Equatable, Sendable { case ready, failed(error: String, kept: Bool) }
    public var end: End?

    public init() {}

    public func state(_ s: SandboxStep) -> SandboxStepState { states[s] ?? .later }

    /// The step that failed, else the one under way.
    public var current: SandboxStep? {
        SandboxStep.allCases.first { states[$0] == .failed } ?? SandboxStep.allCases.last { states[$0] == .now }
    }

    /// How many steps are done, for "3 of 5".
    public var doneCount: Int { SandboxStep.allCases.filter { states[$0] == .done }.count }

    /// How far along, 0…1: done steps, and half of the one under way.
    public var fraction: Double {
        if end == .ready { return 1 }
        let n = Double(SandboxStep.allCases.count)
        return (Double(doneCount) + (states.values.contains(.now) ? 0.5 : 0)) / n
    }

    /// A step's time, "12s" or "1:04"; nil before it starts.
    public func time(_ s: SandboxStep, now: Date = Date()) -> String? {
        guard let a = started[s] else { return nil }
        return Self.duration((ended[s] ?? (end == nil ? now : last ?? now)).timeIntervalSince(a))
    }

    public static func duration(_ t: TimeInterval) -> String {
        let s = max(0, Int(t.rounded()))
        return s < 60 ? "\(s)s" : String(format: "%d:%02d", s / 60, s % 60)
    }
}

private struct Line: Decodable {
    /// Unix milliseconds.
    var at: Double?
    var step: String?
    var state: String?
    var line: String?
    var sandbox: String?
    var machine: String?
    var label: String?
    var end: String?
    var error: String?
    var kept: Bool?
}

/// Reads the file's lines into where the setup is. A line that is not
/// JSON (a partial last line while it is being written, or a later
/// conch's) is skipped, so the sheet shows what it can.
public func readSandboxSetup(_ data: Data) -> SandboxSetupState {
    var st = SandboxSetupState()
    let dec = JSONDecoder()
    for raw in data.split(separator: UInt8(ascii: "\n")) {
        guard let l = try? dec.decode(Line.self, from: Data(raw)) else { continue }
        let t = l.at.flatMap { $0 > 0 ? Date(timeIntervalSince1970: $0 / 1000) : nil }
        if let t { st.first = st.first ?? t; st.last = t }
        if let id = l.sandbox, !id.isEmpty { st.sandbox = id }
        if let m = l.machine, !m.isEmpty { st.machine = m }
        if let lb = l.label, !lb.isEmpty { st.label = lb }
        let step = l.step.flatMap(SandboxStep.init(rawValue:))
        if let text = l.line, !text.isEmpty {
            st.lines.append(text)
            if let step { st.detail[step] = text }
        }
        if let step, let s = l.state {
            switch s {
            case "start": st.states[step] = .now; if let t { st.started[step] = t }
            case "done":
                st.states[step] = .done
                if let t { st.ended[step] = t; if st.started[step] == nil { st.started[step] = t } }
            case "fail": st.states[step] = .failed; if let t { st.ended[step] = t }
            default: break
            }
        }
        switch l.end {
        case "ready": st.end = .ready
        case "failed": st.end = .failed(error: l.error ?? "", kept: l.kept ?? false)
        default: break
        }
    }
    return st
}

/// What the sheet says under a step while it runs: the last line, made
/// to read as a sentence ("copying conch to x (29 MB)" → "Copying conch
/// to x (29 MB)").
public func sandboxStepDetail(_ line: String?) -> String? {
    guard let l = line?.trimmingCharacters(in: .whitespaces), !l.isEmpty else { return nil }
    return l.prefix(1).uppercased() + l.dropFirst()
}

/// The arguments that set up again a sandbox whose setup failed but which
/// was kept: `sandbox add`, writing to the same kind of file.
public func sandboxRetryArguments(provider: String, sandbox: String, label: String?, progress: String) -> [String] {
    var a = ["sandbox", "-provider", provider, "add", "-progress", progress]
    if let label, !label.isEmpty { a += ["-label", label] }
    return a + [sandbox]
}

/// The arguments that delete a sandbox, without asking.
public func sandboxDeleteArguments(provider: String, sandbox: String) -> [String] {
    ["sandbox", "-provider", provider, "rm", "-y", sandbox]
}
