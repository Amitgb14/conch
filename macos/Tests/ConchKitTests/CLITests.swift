import Foundation
import Testing
@testable import ConchKit

// runConch reads a program's output while it runs: a pipe holds 64 KB, and
// reading only after the exit left a child printing more waiting for ever
// (`conch import conductor -n -json` with a hundred conversations).
private final class Once: @unchecked Sendable {
    let lock = NSLock()
    var done = false
    func first() -> Bool { lock.lock(); defer { lock.unlock() }; if done { return false }; done = true; return true }
}

/// The first of op and a deadline: a run blocked for ever never resumes its
/// continuation, which no time limit or cancellation can end, so the test
/// fails here instead of hanging the whole suite.
private func within<T: Sendable>(_ seconds: Double, _ op: @escaping @Sendable () async throws -> T) async throws -> T {
    let once = Once()
    return try await withCheckedThrowingContinuation { (cont: CheckedContinuation<T, Error>) in
        Task {
            do {
                let v = try await op()
                if once.first() { cont.resume(returning: v) }
            } catch {
                if once.first() { cont.resume(throwing: error) }
            }
        }
        Task {
            try? await Task.sleep(nanoseconds: UInt64(seconds * 1e9))
            if once.first() { cont.resume(throwing: ConchError(code: "test", message: "still running after \(seconds)s: blocked on a full pipe")) }
        }
    }
}

@Suite(.timeLimit(.minutes(1))) struct RunConchTests {
    /// A shell script standing in for conch, in a folder of its own.
    private func script(_ body: String) throws -> String {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("conch-cli-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let path = dir.appendingPathComponent("conch").path
        try ("#!/bin/sh\n" + body + "\n").write(toFile: path, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: path)
        return path
    }

    @Test func readsMoreThanAPipeHolds() async throws {
        // 300 000 bytes on stdout and 100 000 on stderr, then exit 0.
        let bin = try script("head -c 300000 /dev/zero | tr '\\0' a\nhead -c 100000 /dev/zero | tr '\\0' e >&2\nexit 0")
        defer { try? FileManager.default.removeItem(atPath: (bin as NSString).deletingLastPathComponent) }
        let out = try await within(20) { try await runConch(bin, []) }
        #expect(out.count == 300_000 && out.allSatisfy { $0 == UInt8(ascii: "a") })
    }

    @Test func failsWithBigOutputAndSaysStderr() async throws {
        let bin = try script("head -c 200000 /dev/zero | tr '\\0' a\nhead -c 100000 /dev/zero | tr '\\0' e >&2\necho ' it broke' >&2\nexit 1")
        defer { try? FileManager.default.removeItem(atPath: (bin as NSString).deletingLastPathComponent) }
        do {
            _ = try await within(20) { try await runConch(bin, ["x"]) }
            Issue.record("a failed run answered")
        } catch let e as ConchError {
            #expect(e.message.hasSuffix("it broke") && e.message.count > 100_000)
        }
    }

    @Test func inputVariantReadsBigOutput() async throws {
        let bin = try script("cat >/dev/null\nhead -c 250000 /dev/zero | tr '\\0' b\nexit 0")
        defer { try? FileManager.default.removeItem(atPath: (bin as NSString).deletingLastPathComponent) }
        let out = try await within(20) { try await runConch(bin, [], input: Data(repeating: 0x41, count: 200_000)) }
        #expect(out.count == 250_000)
    }

    @Test func emptyFailureNamesTheCommand() async throws {
        let bin = try script("exit 3")
        defer { try? FileManager.default.removeItem(atPath: (bin as NSString).deletingLastPathComponent) }
        await #expect(throws: ConchError(code: "conch", message: "conch a b failed")) { try await runConch(bin, ["a", "b"]) }
        await #expect(throws: (any Error).self) { try await runConch("/nonexistent/conch", []) }
    }
}
