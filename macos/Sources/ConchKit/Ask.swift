import Foundation

// The status bar's ✦ Ask, the TUI's brain bar: a request in plain words,
// a plan of actions, and nothing run until it is confirmed. It is
// `conch ask REQUEST`, driven through its own confirmation: the plan it
// prints is the one confirmed — asking again with -y would plan again,
// and a model can plan differently the second time.

/// How many actions an ask's confirmation offers ("Run 2 action(s)?
/// [y/N] "), or nil when the text holds no such question yet.
public func askConfirmCount(_ text: String) -> Int? {
    guard let r = text.range(of: #"Run (\d+) action\(s\)\? \[y/N\]"#, options: .regularExpression) else { return nil }
    let digits = text[r].dropFirst(4).prefix { $0.isNumber }
    return Int(digits)
}

/// One request to the brain, from asking to the end of what it ran.
public final class AskSession: @unchecked Sendable {
    public enum State: Equatable, Sendable {
        /// The brain is making a plan.
        case thinking
        /// A plan of n actions, waiting to be confirmed.
        case plan(Int)
        /// Confirmed: the actions are running.
        case running
        /// Over: what it said and did, or why it failed.
        case done
        case failed(String)
    }

    /// What the plan and its results say, as `conch ask` prints them.
    public private(set) var output = ""
    public private(set) var state = State.thinking
    /// Told on the main queue whenever output or state changes.
    public var onChange: (() -> Void)?

    private let process = Process()
    private let input = Pipe(), out = Pipe(), err = Pipe()
    private var errText = ""
    private let lock = NSLock()

    public init(binary: String, request: String, environment: [String: String] = conchEnvironment()) {
        process.executableURL = URL(fileURLWithPath: binary)
        process.arguments = ["ask", request]
        process.environment = environment
        process.standardInput = input
        process.standardOutput = out
        process.standardError = err
    }

    public func start() {
        out.fileHandleForReading.readabilityHandler = { [weak self] h in
            let d = h.availableData
            guard let self, !d.isEmpty else { return }
            self.lock.lock()
            self.output += String(decoding: d, as: UTF8.self)
            self.lock.unlock()
            self.changed()
        }
        err.fileHandleForReading.readabilityHandler = { [weak self] h in
            let d = h.availableData
            guard let self, !d.isEmpty else { return }
            self.lock.lock()
            self.errText += String(decoding: d, as: UTF8.self)
            let n = askConfirmCount(self.errText)
            if let n, self.state == .thinking { self.state = .plan(n) }
            self.lock.unlock()
            self.changed()
        }
        process.terminationHandler = { [weak self] p in
            guard let self else { return }
            // What is still in the pipes arrives before the end is said.
            let rest = self.out.fileHandleForReading.readDataToEndOfFile()
            let restErr = self.err.fileHandleForReading.readDataToEndOfFile()
            self.out.fileHandleForReading.readabilityHandler = nil
            self.err.fileHandleForReading.readabilityHandler = nil
            self.lock.lock()
            self.output += String(decoding: rest, as: UTF8.self)
            self.errText += String(decoding: restErr, as: UTF8.self)
            if p.terminationStatus == 0 {
                self.state = .done
            } else {
                // Its last line is what went wrong (no provider, a server
                // not answering); "thinking…" before it is not.
                let lines = self.errText.split(separator: "\n").map(String.init).filter { !$0.hasPrefix("thinking") }
                self.state = .failed(lines.last ?? "conch ask failed")
            }
            self.lock.unlock()
            self.changed()
        }
        do {
            try process.run()
        } catch {
            lock.lock()
            state = .failed("\(error)")
            lock.unlock()
            changed()
        }
    }

    /// Answers the plan: run it, or not.
    public func confirm(_ yes: Bool) {
        lock.lock()
        guard case .plan = state else { lock.unlock(); return }
        state = yes ? .running : .done
        lock.unlock()
        input.fileHandleForWriting.write(Data((yes ? "y\n" : "n\n").utf8))
        try? input.fileHandleForWriting.close()
        changed()
    }

    /// Stops it, whatever it is doing.
    public func cancel() {
        if process.isRunning { process.terminate() }
    }

    private func changed() {
        DispatchQueue.main.async { [weak self] in self?.onChange?() }
    }
}

/// conch web pair -json: a phone's pairing, as a window draws it.
public struct PairInfo: Decodable, Equatable, Sendable {
    public var code: String
    public var permission: String
    public var expires: Date?
    public var reach: String?
    public var url: String?
    /// What a QR code carries; only where a phone can pair (https).
    public var link: String?
    /// What to do to pair, said as the plain pairing says it.
    public var `where`: String?
    public var running: Bool?
    public var canStart: Bool?

    enum CodingKeys: String, CodingKey {
        case code, permission, expires, reach, url, link, `where`, running
        case canStart = "can_start"
    }
}
