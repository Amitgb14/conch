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
public func conchEnvironment(_ env: [String: String] = ProcessInfo.processInfo.environment) -> [String: String] {
    var out = env
    out.removeValue(forKey: "CONCH_PANE_ID")
    out["TERM"] = "xterm-256color"
    out["COLORTERM"] = "truecolor"
    return out
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

/// What a waiting agent asks, or nil when it is not waiting.
public func readQuestion(_ binary: String, pane: String) async throws -> Question? {
    let data = try await runConch(binary, ["agent", "question", pane])
    if String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) == "null" { return nil }
    return try JSONDecoder().decode(Question.self, from: data)
}

/// Picks a choice of a waiting agent's menu — refused if the question has
/// changed since it was read.
public func answer(_ binary: String, pane: String, question: Question, choice: Choice) async throws {
    _ = try await runConch(binary, ["agent", "answer", "-question", question.id, pane, choice.choice])
}
