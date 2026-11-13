import Foundation

/// The conch program the app runs: for `conch attach` in each pane, and
/// for what is easier to ask conch than to do twice (reading a waiting
/// agent's menu, starting the server). $CONCH_BIN when set; else the one
/// inside the app; else where the installer and Homebrew put it.
public func conchBinary(env: [String: String] = ProcessInfo.processInfo.environment,
                        bundle: Bundle = .main,
                        exists: (String) -> Bool = { FileManager.default.isExecutableFile(atPath: $0) }) -> String? {
    if let b = env["CONCH_BIN"], !b.isEmpty { return b }
    var candidates: [String] = []
    if let res = bundle.resourcePath { candidates.append((res as NSString).appendingPathComponent("conch")) }
    let home = env["HOME"] ?? NSHomeDirectory()
    candidates += [(home as NSString).appendingPathComponent(".local/bin/conch"), "/opt/homebrew/bin/conch", "/usr/local/bin/conch"]
    for dir in (env["PATH"] ?? "").split(separator: ":") {
        candidates.append((String(dir) as NSString).appendingPathComponent("conch"))
    }
    return candidates.first(where: exists)
}

/// The environment a conch the app starts runs with: this one, without a
/// pane it may have been started from (a conch inside a pane refuses some
/// things, and this is not inside one), with a terminal type every
/// terminal view understands.
public func conchEnvironment(_ env: [String: String] = ProcessInfo.processInfo.environment,
                             source: String? = Bundle.main.object(forInfoDictionaryKey: "ConchSource") as? String,
                             exists: (String) -> Bool = { FileManager.default.fileExists(atPath: $0) }) -> [String: String] {
    var out = env
    out.removeValue(forKey: "CONCH_PANE_ID")
    out["TERM"] = "xterm-256color"
    out["COLORTERM"] = "truecolor"
    // Upgrading a machine builds conch for it from source, which the conch
    // inside the bundle cannot find by looking above itself: bundle.sh
    // writes where it was built from, given here while it is still there.
    if (out["CONCH_SOURCE"] ?? "").isEmpty, let src = source, !src.isEmpty, exists(src + "/go.mod") {
        out["CONCH_SOURCE"] = src
    }
    // Started from the Dock or the Finder an app gets launchd's short PATH,
    // without Homebrew or Go, which that build needs.
    let home = env["HOME"] ?? NSHomeDirectory()
    var path = (out["PATH"] ?? "/usr/bin:/bin:/usr/sbin:/sbin").split(separator: ":").map(String.init)
    for dir in ["/opt/homebrew/bin", "/usr/local/bin", "/usr/local/go/bin", home + "/go/bin", home + "/.local/bin"] where !path.contains(dir) {
        path.append(dir)
    }
    out["PATH"] = path.joined(separator: ":")
    return out
}

/// What a conch run in a terminal of the server's (runCLIPane) is given
/// of conchEnvironment: the server starts that terminal with its own
/// environment, not the app's, so without CONCH_SOURCE the conch there
/// could not build itself for a new sandbox and installed whatever older
/// build its cache held — which the check after the install then refused.
public func cliPaneEnv(_ env: [String: String] = conchEnvironment()) -> [String] {
    guard let src = env["CONCH_SOURCE"], !src.isEmpty else { return [] }
    return ["CONCH_SOURCE=" + src]
}

/// Runs conch with args and answers what it printed, or why it failed.
public func runConch(_ binary: String, _ args: [String]) async throws -> Data {
    try await withCheckedThrowingContinuation { cont in
        let p = Process()
        p.executableURL = URL(fileURLWithPath: binary)
        p.arguments = args
        p.environment = conchEnvironment()
        let out = Pipe(), err = Pipe()
        p.standardOutput = out
        p.standardError = err
        p.standardInput = FileHandle.nullDevice
        p.terminationHandler = { proc in
            let o = out.fileHandleForReading.readDataToEndOfFile()
            let e = err.fileHandleForReading.readDataToEndOfFile()
            if proc.terminationStatus == 0 {
                cont.resume(returning: o)
            } else {
                let msg = String(data: e, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                cont.resume(throwing: ConchError(code: "conch", message: msg.isEmpty ? "conch \(args.joined(separator: " ")) failed" : msg))
            }
        }
        do { try p.run() } catch { cont.resume(throwing: error) }
    }
}

/// Runs conch with args and input on its stdin, and answers what it
/// printed, or why it failed — `conch commit-message` takes the diff so.
public func runConch(_ binary: String, _ args: [String], input: Data) async throws -> Data {
    try await withCheckedThrowingContinuation { cont in
        let p = Process()
        p.executableURL = URL(fileURLWithPath: binary)
        p.arguments = args
        p.environment = conchEnvironment()
        let out = Pipe(), err = Pipe(), inp = Pipe()
        p.standardOutput = out
        p.standardError = err
        p.standardInput = inp
        p.terminationHandler = { proc in
            let o = out.fileHandleForReading.readDataToEndOfFile()
            let e = err.fileHandleForReading.readDataToEndOfFile()
            if proc.terminationStatus == 0 {
                cont.resume(returning: o)
            } else {
                let msg = String(data: e, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                cont.resume(throwing: ConchError(code: "conch", message: msg.isEmpty ? "conch \(args.joined(separator: " ")) failed" : msg))
            }
        }
        do {
            try p.run()
            DispatchQueue.global().async {
                inp.fileHandleForWriting.write(input)
                try? inp.fileHandleForWriting.close()
            }
        } catch {
            cont.resume(throwing: error)
        }
    }
}

/// conch's arguments for a machine: none for this computer, -m otherwise.
public func onMachine(_ machine: String, _ args: [String]) -> [String] {
    machine == "local" ? args : ["-m", machine] + args
}

/// What a waiting agent asks, or nil when it is not waiting.
public func readQuestion(_ binary: String, pane: String, machine: String = "local") async throws -> Question? {
    let data = try await runConch(binary, onMachine(machine, ["agent", "question", pane]))
    if String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) == "null" { return nil }
    return try JSONDecoder().decode(Question.self, from: data)
}

/// Picks a choice of a waiting agent's menu — refused if the question has
/// changed since it was read.
public func answer(_ binary: String, pane: String, machine: String = "local", question: Question, choice: Choice) async throws {
    _ = try await runConch(binary, onMachine(machine, ["agent", "answer", "-question", question.id, pane, choice.choice]))
}

/// Runs a program and returns what it printed, whatever its exit: ps
/// prints its list even when it could not read every process.
public func runProgram(_ path: String, _ args: [String]) async throws -> String {
    try await withCheckedThrowingContinuation { cont in
        let p = Process()
        p.executableURL = URL(fileURLWithPath: path)
        p.arguments = args
        let out = Pipe()
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        p.standardInput = FileHandle.nullDevice
        do { try p.run() } catch {
            cont.resume(throwing: error)
            return
        }
        DispatchQueue.global().async {
            let data = out.fileHandleForReading.readDataToEndOfFile()
            p.waitUntilExit()
            cont.resume(returning: String(data: data, encoding: .utf8) ?? "")
        }
    }
}

/// The address in `conch config web`'s first line, "conch settings: URL".
public func settingsPageURL(_ line: String) -> URL? {
    guard let r = line.range(of: "http") else { return nil }
    return URL(string: String(line[r.lowerBound...]).trimmingCharacters(in: .whitespaces))
}

/// A file's modification time and size, to tell that it changed; "" for
/// one that is not there.
public func fileStamp(_ path: String) -> String {
    guard let a = try? FileManager.default.attributesOfItem(atPath: path),
          let t = a[.modificationDate] as? Date, let n = a[.size] as? NSNumber else { return "" }
    return "\(t.timeIntervalSince1970):\(n.int64Value)"
}

/// A path as a terminal pastes a dropped file's: every character a shell
/// would take apart escaped with a backslash, as Terminal and Ghostty do,
/// so an agent's prompt or a shell reads it as one path.
public func shellEscapedPath(_ path: String) -> String {
    let special = Set(" \t\\'\"`$&|;<>()[]{}*?!#~=%^,")
    var out = ""
    for ch in path {
        if special.contains(ch) { out.append("\\") }
        out.append(ch)
    }
    return out
}

/// What a drop of these paths pastes: each escaped, a space between, and
/// one after, so typing goes on after the last.
public func dropText(_ paths: [String]) -> String {
    paths.isEmpty ? "" : paths.map(shellEscapedPath).joined(separator: " ") + " "
}

/// The paths `conch -m MACHINE upload` printed, one a line, where each
/// file landed there.
public func uploadedPaths(_ output: String) -> [String] {
    output.split(separator: "\n").map { $0.trimmingCharacters(in: .whitespaces) }.filter { $0.hasPrefix("/") || $0.hasPrefix("~") }
}

/// A link a pane printed that may be opened on a click: the web's and
/// mail's, and nothing else — not file: or a custom scheme, which a
/// program printing it could use to make a click run something.
public func openableURL(_ s: String) -> URL? {
    let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
    guard let u = URL(string: t), let scheme = u.scheme?.lowercased(), ["http", "https", "mailto"].contains(scheme) else { return nil }
    if scheme != "mailto" && (u.host ?? "").isEmpty { return nil }
    return u
}

/// Whether a conch's attach takes -hold (keeping an ended pane's screen):
/// asked once of the binary, since one from before it refuses the flag and
/// every pane would fail to start. Its usage line says.
public func attachHolds(_ usage: String) -> Bool { usage.contains("[-hold]") }

/// What `conch attach` says it takes, run with nothing to attach; "" when
/// it cannot be run.
public func attachUsageLine(_ binary: String) -> String {
    let p = Process()
    p.executableURL = URL(fileURLWithPath: binary)
    p.arguments = ["attach"]
    p.environment = conchEnvironment()
    let err = Pipe()
    p.standardOutput = FileHandle.nullDevice
    p.standardError = err
    p.standardInput = FileHandle.nullDevice
    do { try p.run() } catch { return "" }
    let data = err.fileHandleForReading.readDataToEndOfFile()
    p.waitUntilExit()
    return String(decoding: data, as: UTF8.self)
}

/// Runs conch with args and hands each line it prints to onLine as it
/// comes, answering its exit status: `conch sandbox adapt -json` takes
/// minutes, and the sheet shows it moving.
public func runConchLines(_ binary: String, _ args: [String], onLine: @escaping @Sendable (String) -> Void) async throws -> Int32 {
    try await withCheckedThrowingContinuation { cont in
        let p = Process()
        p.executableURL = URL(fileURLWithPath: binary)
        p.arguments = args
        p.environment = conchEnvironment()
        let out = Pipe()
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        p.standardInput = FileHandle.nullDevice
        let buffer = LineBuffer()
        out.fileHandleForReading.readabilityHandler = { h in
            let d = h.availableData
            if d.isEmpty { return }
            for line in buffer.add(d) { onLine(line) }
        }
        p.terminationHandler = { proc in
            out.fileHandleForReading.readabilityHandler = nil
            let rest = out.fileHandleForReading.readDataToEndOfFile()
            for line in buffer.add(rest) + buffer.flush() { onLine(line) }
            cont.resume(returning: proc.terminationStatus)
        }
        do { try p.run() } catch { cont.resume(throwing: error) }
    }
}

/// Bytes into whole lines, holding back a line not yet ended.
public final class LineBuffer: @unchecked Sendable {
    private var pending = Data()
    private let lock = NSLock()
    public init() {}
    public func add(_ d: Data) -> [String] {
        lock.lock(); defer { lock.unlock() }
        pending.append(d)
        var lines: [String] = []
        while let i = pending.firstIndex(of: 0x0A) {
            let line = pending[pending.startIndex..<i]
            lines.append(String(decoding: line, as: UTF8.self))
            pending.removeSubrange(pending.startIndex...i)
        }
        return lines
    }
    public func flush() -> [String] {
        lock.lock(); defer { lock.unlock() }
        defer { pending = Data() }
        return pending.isEmpty ? [] : [String(decoding: pending, as: UTF8.self)]
    }
}
