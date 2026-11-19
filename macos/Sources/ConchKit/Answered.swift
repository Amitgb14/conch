import Foundation

/// The questions answered from the app and not yet gone from the agent's
/// screen. Answering clears a card's question at once, but the agent is
/// still waiting for a moment — its menu still drawn — so the next read
/// found the same question and put the buttons back, then the card went
/// when the agent moved on: choices, none, choices, gone, which read as
/// a flicker. A question just answered is not shown again for a grace;
/// one that is still there after it is, since then the answer did not
/// take and the person needs the buttons back.
public struct AnsweredQuestions: Sendable {
    /// How long an answered question is not shown again.
    public var grace: TimeInterval
    private var answered: [String: (question: Question, at: Date)] = [:]

    public init(grace: TimeInterval = 8) { self.grace = grace }

    /// The question k was just answered.
    public mutating func note(_ k: String, _ q: Question, at now: Date = Date()) {
        answered[k] = (q, now)
    }

    /// Whether a question read for k is the one just answered, still on
    /// the agent's screen: not to be shown.
    public func hides(_ k: String, _ q: Question?, at now: Date = Date()) -> Bool {
        guard let a = answered[k], now.timeIntervalSince(a.at) < grace else { return false }
        guard let q else { return true } // nothing read yet: still answering
        // The same question, whichever choice the agent's cursor is on
        // now: answering moves it.
        return q.id == a.question.id && q.text == a.question.text
            && q.choices.map(\.label) == a.question.choices.map(\.label)
    }

    /// Whether k is being answered: what its card says meanwhile.
    public func answering(_ k: String, at now: Date = Date()) -> Bool {
        guard let a = answered[k] else { return false }
        return now.timeIntervalSince(a.at) < grace
    }

    /// The question k is being answered on, for its card's text meanwhile.
    public func question(_ k: String, at now: Date = Date()) -> Question? {
        answering(k, at: now) ? answered[k]?.question : nil
    }

    /// k stopped waiting, or its answer failed: forget it.
    public mutating func clear(_ k: String) { answered.removeValue(forKey: k) }
}
