import Foundation

// An MCP server's token in this Mac's Keychain, under the service
// conch-mcp and its variable's name — where the conch server reads it for
// every pane it starts (internal/server/secrets.go). Kept through
// /usr/bin/security, as the server reads it, so neither ever prompts: an
// item security made is one security may read. The token goes to it on
// stdin (`security -i`), never in its arguments, where any process could
// see it.

enum Keychain {
    static let service = "conch-mcp"

    struct Failed: Error, CustomStringConvertible { let description: String }

    static func keep(_ token: String, as variable: String) throws {
        guard !token.contains(where: { $0 == "\"" || $0 == "\\" || $0.isNewline }) else {
            throw Failed(description: "the token has a quote, a backslash or a line break in it")
        }
        try run(interactive: "add-generic-password -U -s \(service) -a \(variable) -l \"conch: \(variable)\" -w \"\(token)\"\n")
    }

    static func forget(_ variable: String) throws {
        try run(interactive: "delete-generic-password -s \(service) -a \(variable)\n")
    }

    static func read(_ variable: String) -> String? {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/security")
        p.arguments = ["find-generic-password", "-s", service, "-a", variable, "-w"]
        let out = Pipe()
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        guard (try? p.run()) != nil else { return nil }
        let data = out.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        guard p.terminationStatus == 0 else { return nil }
        let v = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .newlines)
        return v.isEmpty ? nil : v
    }

    private static func run(interactive line: String) throws {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/security")
        p.arguments = ["-i"]
        let input = Pipe(), err = Pipe()
        p.standardInput = input
        p.standardOutput = FileHandle.nullDevice
        p.standardError = err
        try p.run()
        input.fileHandleForWriting.write(Data(line.utf8))
        try? input.fileHandleForWriting.close()
        let msg = String(decoding: err.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
        p.waitUntilExit()
        // security -i says what went wrong but exits 0: its stderr is the verdict.
        if p.terminationStatus != 0 || msg.contains("security:") || msg.lowercased().contains("error") {
            throw Failed(description: "the Keychain refused it: " + msg.trimmingCharacters(in: .whitespacesAndNewlines))
        }
    }
}
