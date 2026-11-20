import ConchKit
import Foundation

// Moving an agent to another of its accounts (agent.move.v1): asked for
// from its Switch Account ▸, said when the server schedules one by itself
// (a card counting down, with Move Now and Keep), and its new pane put
// where the old one was when it lands.
extension Store {
    /// Whether machine's server moves agents between accounts.
    func canMoveAgents(on machine: String) -> Bool {
        conns[machine]?.hello?.capabilities?.contains(agentMoveCapability) == true
    }

    /// Asks machine again how it would rank agent's accounts.
    func loadRanks(on machine: String, agent: String) {
        guard let c = conns[machine], canMoveAgents(on: machine) else { return }
        Task {
            guard let sg = try? await c.call("account.suggest", AccountSuggestParams(agent: agent), as: AccountSuggestion.self) else { return }
            accountRanks[machine + "|" + agent] = sg.accounts
        }
    }

    /// The ranks of the agent in pane k's accounts.
    func ranks(for k: String) -> [AccountRank] {
        guard let p = pane(k), let a = p.agent?.name else { return [] }
        return accountRanks[unkey(k).machine + "|" + a] ?? []
    }

    /// Opens the sheet for moving pane k's agent by hand: to account, or
    /// nil for the one conch picks.
    func askMove(_ k: String, to account: String?) {
        guard let p = pane(k), let a = p.agent?.name else { return }
        loadRanks(on: unkey(k).machine, agent: a)
        moveAsk = MoveAsk(pane: k, agent: a, from: accountLabel(p.account), to: account)
    }

    /// Moves pane k's agent to account (nil: conch picks), and puts the
    /// pane it comes back in where this one was.
    func moveAgent(_ k: String, to account: String?, continue cont: Bool = false, reason: String = "hand") {
        let (m, id) = unkey(k)
        guard let c = conns[m] else { return }
        movingAgents[k] = nil
        job("Moving \(id) to \(account ?? "the account with most room")", done: nil) {
            do {
                let nw = try await c.call("agent.move", AgentMoveParams(id: id, account: account, continue: cont ? true : nil, reason: reason), as: PaneInfo.self)
                self.landed(old: k, new: ConchKit.key(m, nw.id))
                self.say("\(id) now on \(accountLabel(nw.account)), as \(nw.id)")
            } catch {
                self.say("Could not move \(id): \(error.localizedDescription)", .err)
            }
        }
    }

    /// A scheduled move made at once.
    func moveNow(_ k: String) {
        guard let mv = movingAgents[k] else { return }
        moveAgent(k, to: mv.to, continue: mv.reason == "limit", reason: mv.reason)
    }

    /// A scheduled move called off: the agent stays where it is until it
    /// has worked again.
    func cancelMove(_ k: String) {
        let (m, id) = unkey(k)
        let from = movingAgents[k]?.from
        movingAgents[k] = nil
        guard let c = conns[m] else { return }
        Task {
            do {
                try await c.call("agent.move.cancel", AgentMoveCancelParams(id: id))
                say("\(id) stays on \(from ?? "its account")")
            } catch {
                say("Could not stop the move of \(id): \(error.localizedDescription)", .err)
            }
        }
    }

    func agentMoving(_ m: AgentMoving, on machine: String) {
        movingAgents[ConchKit.key(machine, m.pane)] = m
    }

    func agentMoved(_ m: AgentMoved, on machine: String) {
        let old = ConchKit.key(machine, m.replaces)
        movingAgents[old] = nil
        landed(old: old, new: ConchKit.key(machine, m.pane))
        if m.reason != "hand" {
            say("\(m.replaces) moved from \(m.from) to \(m.to)\(m.reason == "limit" ? " at its limit" : m.reason == "line" ? " past the failover line" : "")")
        }
        loadRanks(on: machine, agent: m.agent)
    }

    /// The new pane in the old one's place, once, wherever it is shown.
    func landed(old: String, new: String) {
        guard layout.tab(showing: old) != nil else { return }
        layout.replace(old, with: new)
    }

    /// Agents that moved off an account that has room again, to offer the
    /// move back for; Later puts one off for this session.
    var moveBackOffers: [(key: String, pane: PaneInfo, to: String)] {
        var out: [(key: String, pane: PaneInfo, to: String)] = []
        for (m, snap) in snapshots where canMoveAgents(on: m) {
            for p in snap.panes where p.running && p.movedFrom?.isEmpty == false {
                let k = ConchKit.key(m, p.id)
                guard !backLater.contains(k), let a = p.agent?.name,
                      let to = moveBack(movedFrom: p.movedFrom, ranks: accountRanks[m + "|" + a] ?? []) else { continue }
                out.append((k, p, to))
            }
        }
        return out.sorted { $0.key < $1.key }
    }

    /// The running agents on machine moved off account (by its label),
    /// whatever its room now.
    func movedOff(_ account: String, on machine: String) -> [String] {
        (snapshots[machine]?.panes ?? []).filter { $0.running && $0.movedFrom == account }.map { ConchKit.key(machine, $0.id) }.sorted()
    }

    /// Every agent moved off account moved back to it, one after another.
    func moveAllBack(to account: String, on machine: String) {
        let keys = movedOff(account, on: machine)
        guard !keys.isEmpty else { return }
        for k in keys { backLater.remove(k) }
        say("Moving \(keys.count) agent\(keys.count == 1 ? "" : "s") back to \(account)")
        for k in keys { moveAgent(k, to: account, reason: "back") }
    }

    /// The move-back offers, one per account they would go back to.
    var moveBackGroups: [(to: String, machine: String, offers: [(key: String, pane: PaneInfo, to: String)])] {
        var out: [(to: String, machine: String, offers: [(key: String, pane: PaneInfo, to: String)])] = []
        for o in moveBackOffers {
            let m = unkey(o.key).machine
            if let i = out.firstIndex(where: { $0.to == o.to && $0.machine == m }) {
                out[i].offers.append(o)
            } else {
                out.append((o.to, m, [o]))
            }
        }
        return out
    }
}
