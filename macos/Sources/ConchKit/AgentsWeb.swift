import Foundation

// Every agent on every machine in a tab (⌃⌘A), drawn by conch-ui
// (ui/src/views/Agents.tsx): a card each — what it is doing, where, and
// for one waiting on you its question with the answers as buttons — and
// the plan limits and what the agents last did beside them. Built from
// what the app already reads (the roster's cards, the questions, the
// plans); nothing is asked of a server for it.

/// The overview's place in the layout. One per window: the agents of
/// every machine are in it already.
public let agentsKey = "agents|"

public func isAgentsKey(_ k: String) -> Bool { k == agentsKey }

public struct AgentsWebView: Encodable, Equatable, Sendable {
    public struct Choice: Encodable, Equatable, Sendable {
        public var index: Int
        public var label: String
        public var `default`: Bool
    }
    public struct Ask: Encodable, Equatable, Sendable {
        public var text: String
        public var kind: String
        public var choices: [Choice]
    }
    public struct Card: Encodable, Equatable, Sendable {
        public var key: String
        public var title: String
        public var agent: String?
        public var color: String
        public var state: String
        public var running: Bool
        public var place: String
        public var ago: String?
        public var focused: Bool
        public var answering: Bool
        public var question: Ask?
    }
    public struct Limit: Encodable, Equatable, Sendable {
        public var label: String
        public var pct: Double
        public var agent: String
        public var color: String
    }
    public struct Event: Encodable, Equatable, Sendable {
        public var key: String
        public var agent: String
        public var text: String
        public var ago: String
        public var color: String
    }

    public var colors: WebColors
    public var agents: [Card]
    public var limits: [Limit]
    public var activity: [Event]
}

/// The overview, from the roster's cards (agents only; terminals have
/// their own place in the tree), the questions agents wait on, the keys
/// being answered, the split in focus, and every machine's plans.
public func agentsWebView(cards: [RosterCard], questions: [String: Question], answering: Set<String>, focused: String?,
                          plans: [PlanLimits], messages: [String: String] = [:], theme: ConchTheme, now: Date = Date()) -> AgentsWebView {
    func color(_ agent: String?) -> String { (markColor(agent, on: theme.bg, text: theme.fg) ?? theme.muted).hex }
    let agents = cards.filter { $0.agent != nil }
    let shown = agents.map { c -> AgentsWebView.Card in
        var ask: AgentsWebView.Ask?
        if c.running, c.state == .blocked {
            let q = questions[c.key]
            let msg = messages[c.key] ?? ""
            let text = cardText(q?.text.isEmpty == false ? q!.text : msg, choices: q?.choices ?? [])
            ask = .init(text: text, kind: questionKind(msg),
                        choices: (q?.choices ?? []).enumerated().map { .init(index: $0.offset, label: $0.element.label, default: $0.element.default == true) })
        }
        return .init(key: c.key, title: c.title, agent: c.agent, color: color(c.agent), state: c.state?.rawValue ?? "idle",
                     running: c.running, place: c.place, ago: c.since.map { shortAgo($0, now) }, focused: c.key == focused,
                     answering: answering.contains(c.key), question: ask)
    }
    let limits = plans.filter(\.reportsUse).sorted { ($0.agent, $0.account ?? "") < ($1.agent, $1.account ?? "") }.flatMap { p in
        p.allWindows.map { w in
            let who = p.agent + ((p.account ?? "").isEmpty ? "" : " (\(p.account!))")
            return AgentsWebView.Limit(label: "\(who) · \(PlanLimits.label(w.key))", pct: w.usedPct, agent: p.agent, color: color(p.agent))
        }
    }
    let events = activity(agents, limit: 8).map { a in
        AgentsWebView.Event(key: a.key, agent: a.agent, text: a.text, ago: shortAgo(a.since, now), color: color(a.agent))
    }
    return .init(colors: WebColors(theme), agents: shown, limits: limits, activity: events)
}

/// What the overview asks of the app.
public enum AgentsIntent: Equatable, Sendable {
    case ready
    case open(String)
    /// One of a waiting agent's answers, by its place in the question.
    case answer(key: String, choice: Int)
    case newTask
}

/// A message from the overview, checked.
public func agentsIntent(_ body: Any) -> AgentsIntent? {
    guard let m = webMessage(body), m.view == .agents else { return nil }
    switch m.type {
    case "ready": return .ready
    case "open": return m.text("key").map { .open($0) }
    case "answer":
        guard let k = m.text("key"), let c = m.int("choice"), c >= 0 else { return nil }
        return .answer(key: k, choice: c)
    case "newTask": return .newTask
    default: return nil
    }
}
