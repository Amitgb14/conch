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

/// The arguments of `conch ask` for a request, about a pane if one is
/// given.
public func askArguments(request: String, pane: String?) -> [String] {
    var args = ["ask"]
    if let pane, !pane.isEmpty { args += ["-pane", pane] }
    return args + [request]
}

/// One line of the list under the request: something worth asking now,
/// or something asked before. conch decides them (`conch ask -suggest`,
/// internal/brain/suggest.go), so the app offers what the TUI does and
/// says the same about what each costs.
public struct AskSuggestion: Decodable, Equatable, Hashable, Sendable {
    public var text: String
    public var note: String?
    public var recent: Bool?

    public init(text: String, note: String? = nil, recent: Bool? = nil) {
        self.text = text
        self.note = note
        self.recent = recent
    }

    /// One to finish — "tell codex to …" — goes in the box rather than
    /// being asked as it stands.
    public var fill: Bool { text.hasSuffix("…") }
    /// What it puts in the box.
    public var stem: String { fill ? String(text.dropLast()) : text }
    public var isRecent: Bool { recent ?? false }
}

/// What `conch ask -suggest` prints.
public struct AskSuggestions: Decodable, Equatable, Sendable {
    public var context: String?
    public var provider: String?
    public var fast: Bool?
    public var summary: String?
    public var suggestions: [AskSuggestion]

    public init(context: String? = nil, provider: String? = nil, fast: Bool? = nil, summary: String? = nil, suggestions: [AskSuggestion] = []) {
        self.context = context
        self.provider = provider
        self.fast = fast
        self.summary = summary
        self.suggestions = suggestions
    }
}

/// The arguments of `conch ask -suggest`: the pane in front, and earlier
/// requests oldest first, after `--` so one starting with a dash is not
/// read as a flag.
public func askSuggestArguments(pane: String?, history: [String]) -> [String] {
    var args = ["ask", "-suggest"]
    if let pane, !pane.isEmpty { args += ["-pane", pane] }
    return args + ["--"] + history.suffix(askRecentMax)
}

/// How many earlier requests are handed to conch to offer back.
public let askRecentMax = 4

/// The suggestions a typed request narrows the list to: those that hold
/// it, other than the one it already is — as the TUI narrows its own.
public func askMatching(_ opts: [AskSuggestion], typed: String) -> [AskSuggestion] {
    let t = typed.trimmingCharacters(in: .whitespaces).lowercased()
    if t.isEmpty { return opts }
    return opts.filter {
        let s = $0.stem.trimmingCharacters(in: .whitespaces).lowercased()
        return s != t && s.contains(t)
    }
}

/// The requests asked before, oldest first, as the TUI keeps its own:
/// the same one twice in a row is kept once, and only the last 50.
public func askRemember(_ history: [String], _ request: String) -> [String] {
    let r = request.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !r.isEmpty, history.last != r else { return history }
    return Array((history + [r]).suffix(50))
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
    // The end is said once the process has exited and both pipes are at
    // their end — not at the exit alone: a read the handler had already
    // taken but not yet added lost the last line, which is why it failed.
    private var outDone = false, errDone = false, finished = false
    private var exitStatus: Int32?

    /// Whether conch planned it alone, without the model.
    public var fast: Bool {
        lock.lock()
        defer { lock.unlock() }
        return errText.contains("planned without the model")
    }

    /// pane is the one "this" and "here" mean, on this computer.
    public init(binary: String, request: String, pane: String? = nil, environment: [String: String] = conchEnvironment()) {
        process.executableURL = URL(fileURLWithPath: binary)
        process.arguments = askArguments(request: request, pane: pane)
        process.environment = environment
        process.standardInput = input
        process.standardOutput = out
        process.standardError = err
    }

    public func start() {
        out.fileHandleForReading.readabilityHandler = { [weak self] h in
            let d = h.availableData
            guard let self else { return }
            self.lock.lock()
            if d.isEmpty {
                h.readabilityHandler = nil
                self.outDone = true
            } else {
                self.output += String(decoding: d, as: UTF8.self)
            }
            let ended = self.endIfOver()
            self.lock.unlock()
            if !d.isEmpty || ended { self.changed() }
        }
        err.fileHandleForReading.readabilityHandler = { [weak self] h in
            let d = h.availableData
            guard let self else { return }
            self.lock.lock()
            if d.isEmpty {
                h.readabilityHandler = nil
                self.errDone = true
            } else {
                self.errText += String(decoding: d, as: UTF8.self)
                if let n = askConfirmCount(self.errText), self.state == .thinking { self.state = .plan(n) }
            }
            let ended = self.endIfOver()
            self.lock.unlock()
            if !d.isEmpty || ended { self.changed() }
        }
        process.terminationHandler = { [weak self] p in
            guard let self else { return }
            self.lock.lock()
            self.exitStatus = p.terminationStatus
            let ended = self.endIfOver()
            self.lock.unlock()
            if ended { self.changed(); return }
            // Something the program started can hold a pipe open after it
            // exits; the session ends anyway, on what it has by then.
            DispatchQueue.global().asyncAfter(deadline: .now() + 2) { [weak self] in
                guard let self else { return }
                self.lock.lock()
                self.outDone = true
                self.errDone = true
                let ended = self.endIfOver()
                self.lock.unlock()
                if ended { self.changed() }
            }
        }
        do {
            try process.run()
        } catch {
            lock.lock()
            finished = true
            state = .failed("\(error)")
            lock.unlock()
            changed()
        }
    }

    /// Says how it ended, once: when the process has exited and everything
    /// it wrote has been read. Called with the lock held; true when it did.
    private func endIfOver() -> Bool {
        guard !finished, let status = exitStatus, outDone, errDone else { return false }
        finished = true
        out.fileHandleForReading.readabilityHandler = nil
        err.fileHandleForReading.readabilityHandler = nil
        if status == 0 {
            state = .done
        } else {
            // Its last line is what went wrong (no provider, a server not
            // answering); "thinking…" before it is not.
            let lines = errText.split(separator: "\n").map(String.init).filter { !$0.hasPrefix("thinking") }
            state = .failed(lines.last ?? "conch ask failed")
        }
        return true
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
    /// The relay's page a phone pairs through, when one is set.
    public var relay: String?
    public var relayConnected: Bool?
    /// The four words of this computer's key: the phone shows the same.
    public var keyWords: String?
    /// How conch web's link to the relay is, in a sentence.
    public var relayStatus: String?

    enum CodingKeys: String, CodingKey {
        case code, permission, expires, reach, url, link, `where`, running, relay
        case canStart = "can_start", relayConnected = "relay_connected", keyWords = "key_words", relayStatus = "relay_status"
    }

    /// What the pair window says about the relay: nil without one.
    public var relayLine: PairRelay? {
        guard let r = relay, !r.isEmpty else { return nil }
        let host = URL(string: r)?.host ?? r
        let words = (keyWords ?? "").split(separator: " ").map(String.init)
        let connected = relayConnected == true
        return PairRelay(host: host, words: words, connected: connected,
                         status: relayStatus ?? (connected ? "connected" : "not connected"))
    }
}

/// A pairing through a relay, as the pair window shows it.
public struct PairRelay: Equatable, Sendable {
    public var host: String
    /// The key's words, checked against the phone's.
    public var words: [String]
    public var connected: Bool
    public var status: String
}
