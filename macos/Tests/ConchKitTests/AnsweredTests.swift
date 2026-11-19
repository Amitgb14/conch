import Foundation
import Testing
@testable import ConchKit

private func q(_ id: String = "q1", _ text: String = "What next?", cursor: Int = 0) -> Question {
    Question(id: id, text: text, choices: ["Code", "Explore", "Type something."].enumerated().map { i, l in
        Choice(choice: "\(i + 1)", label: l, default: i == cursor)
    })
}

// Answered, the same question still on the agent's screen is not shown
// again — the flicker was the buttons coming back for a moment — even
// with the agent's cursor moved by the answer.
@Test func answeredQuestionStaysHidden() {
    var a = AnsweredQuestions(grace: 8)
    let t0 = Date(timeIntervalSince1970: 1000)
    #expect(!a.hides("local|p1", q(), at: t0))
    a.note("local|p1", q(), at: t0)
    #expect(a.hides("local|p1", q(), at: t0.addingTimeInterval(0.5)))
    #expect(a.hides("local|p1", q(cursor: 2), at: t0.addingTimeInterval(1)))
    // Nothing read yet is still answering, not a reason to show buttons.
    #expect(a.hides("local|p1", nil, at: t0.addingTimeInterval(1)))
    #expect(a.answering("local|p1", at: t0.addingTimeInterval(1)))
    #expect(a.question("local|p1", at: t0.addingTimeInterval(1))?.text == "What next?")
    // Another pane is not this one.
    #expect(!a.hides("local|p2", q(), at: t0.addingTimeInterval(1)))
}

// The agent's next question is shown at once: a different one is not the
// answered one.
@Test func nextQuestionShows() {
    var a = AnsweredQuestions()
    let t0 = Date()
    a.note("local|p1", q(), at: t0)
    #expect(!a.hides("local|p1", q("q2", "And then?"), at: t0.addingTimeInterval(1)))
    #expect(!a.hides("local|p1", q("q1", "Something else?"), at: t0.addingTimeInterval(1)))
    var other = q(); other.choices.removeLast()
    #expect(!a.hides("local|p1", other, at: t0.addingTimeInterval(1)))
}

// Still there after the grace, the answer did not take: the buttons come
// back. Cleared — the agent stopped waiting, or the answer failed — it
// is forgotten at once.
@Test func answeredQuestionComesBackAfterGrace() {
    var a = AnsweredQuestions(grace: 8)
    let t0 = Date()
    a.note("local|p1", q(), at: t0)
    #expect(!a.hides("local|p1", q(), at: t0.addingTimeInterval(8)))
    #expect(!a.answering("local|p1", at: t0.addingTimeInterval(9)))
    #expect(a.question("local|p1", at: t0.addingTimeInterval(9)) == nil)
    a.note("local|p1", q(), at: t0)
    a.clear("local|p1")
    #expect(!a.hides("local|p1", q(), at: t0))
    #expect(!a.answering("local|p1", at: t0))
}
