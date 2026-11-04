import Foundation

// conch doctor's checks, as the app shows them (Help → Check conch…), and
// the shortcuts sheet's way of writing a key.

/// One of `conch doctor -json`'s checks.
public struct DoctorCheck: Decodable, Equatable, Sendable, Identifiable {
    public var name: String
    public var status: String // ok, warn, fail
    public var detail: String
    public var fix: String?
    public var id: String { name }

    public init(name: String, status: String, detail: String, fix: String? = nil) {
        self.name = name
        self.status = status
        self.detail = detail
        self.fix = fix
    }

    /// Its outcome as the command marks it.
    public var mark: String { status == "fail" ? "✗" : status == "warn" ? "!" : "✓" }
}

/// What the checks come to, in a line.
public func doctorSummary(_ checks: [DoctorCheck]) -> String {
    let fail = checks.filter { $0.status == "fail" }.count, warn = checks.filter { $0.status == "warn" }.count
    switch (fail, warn) {
    case (0, 0): return "Everything conch needs is in order."
    case (0, _): return "\(warn) thing\(warn == 1 ? "" : "s") to look at; nothing stops conch working."
    default: return "\(fail) problem\(fail == 1 ? "" : "s") to fix" + (warn > 0 ? ", and \(warn) to look at." : ".")
    }
}

/// Runs conch and returns what it printed whatever its exit: doctor exits
/// 1 when a check fails, which is when its report matters most.
public func runConchAnyExit(_ binary: String, _ args: [String]) async throws -> Data {
    try await withCheckedThrowingContinuation { cont in
        let p = Process()
        p.executableURL = URL(fileURLWithPath: binary)
        p.arguments = args
        p.environment = conchEnvironment()
        let out = Pipe()
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        p.standardInput = FileHandle.nullDevice
        do { try p.run() } catch { cont.resume(throwing: error); return }
        // Off the caller's thread, which is the window's: doctor takes
        // seconds. Read before waiting — a report longer than the pipe
        // holds would otherwise leave conch blocked writing it.
        DispatchQueue.global(qos: .userInitiated).async {
            let data = out.fileHandleForReading.readDataToEndOfFile()
            p.waitUntilExit()
            cont.resume(returning: data)
        }
    }
}

/// A menu's key as a person reads it: ⌃⌥⇧⌘ then the key, an arrow or a
/// return drawn as its symbol.
public func shortcutLabel(key: String, command: Bool, shift: Bool, option: Bool, control: Bool) -> String {
    let special: [UInt32: String] = [0xF700: "↑", 0xF701: "↓", 0xF702: "←", 0xF703: "→", 0x0D: "↩", 0x1B: "⎋", 0x09: "⇥", 0x7F: "⌫"]
    var k = key
    if let u = key.unicodeScalars.first, key.unicodeScalars.count == 1, let s = special[u.value] { k = s } else { k = key.uppercased() }
    return (control ? "⌃" : "") + (option ? "⌥" : "") + (shift ? "⇧" : "") + (command ? "⌘" : "") + k
}
