import AppKit
import ConchKit
import SwiftUI

// The Agents overview (⌃⌘A): every agent on every machine as a card, a
// waiting one answered from its card, plan limits and recent activity —
// conch-ui's Agents view (ConchKit's AgentsWeb.swift) in a tab of its own.
// Built from what the store already reads.

struct AgentsTab: View {
    @ObservedObject var store: Store

    var body: some View {
        let cards = store.overviewCards
        let waiting = cards.filter { $0.state == .blocked && $0.running }.map(\.key)
        var messages: [String: String] = [:]
        for k in waiting { messages[k] = store.pane(k)?.agent?.message ?? "" }
        let v = agentsWebView(cards: cards, questions: store.questions,
                              answering: Set(waiting.filter { store.answered.answering($0) }),
                              focused: nil,
                              plans: store.machines.flatMap { store.plans.on($0.id) },
                              messages: messages, theme: Theme.current)
        return ConchUIView(host: store.agentsHost, script: webUpdateScript(v)) { body in
            if let i = agentsIntent(body) { act(i, cards: cards) }
        }
        .background(Theme.side)
        .clipShape(RoundedRectangle(cornerRadius: 9))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(store.layout.focused == agentsKey ? Theme.accent : Theme.line))
    }

    private func act(_ intent: AgentsIntent, cards: [RosterCard]) {
        switch intent {
        case .ready:
            break
        case .open(let k):
            guard cards.contains(where: { $0.key == k }) else { return }
            store.open(k)
        case let .answer(k, index):
            guard let q = store.questions[k], q.choices.indices.contains(index) else { return }
            store.answer(k, q, q.choices[index])
        case .newTask:
            store.command("newTask")
        }
    }
}
