import Foundation

// What the status bar says for a moment, as the TUI's flash does: that
// something was done, or why it was not — and, while a long job runs
// (a push, a merge, an upgrade), what it is doing. Actions used to finish
// without a word, so a click that worked and one that did nothing looked
// alike.

public struct Flash: Equatable, Sendable {
    public enum Tone: Sendable { case info, ok, err }
    public let id = UUID()
    public var text: String
    public var tone: Tone

    public init(_ text: String, _ tone: Tone = .ok) {
        self.text = text
        self.tone = tone
    }

    /// How long it stays: an error long enough to be read.
    public var lasts: TimeInterval { tone == .err ? 9 : 4 }
}

/// The jobs running, as the status bar says them: the first, and how many
/// more. nil with none.
public func jobSummary(_ jobs: [String]) -> String? {
    guard let first = jobs.first else { return nil }
    return jobs.count == 1 ? first + "…" : "\(first)… and \(jobs.count - 1) more"
}
