import ConchKit
import Foundation

// Switching an agent's conversation to another agent in the same pane
// (agent.switch.v1): Switch Agent ▸ on its menu previews the switch, the
// card counts down and asks, the server waits for the turn to end, and the
// pane then says where its conversation came from (AgentSwitchViews.swift).
// The pane keeps its id, so nothing has to be put anywhere afterwards.
extension Store {
    /// Whether machine's server switches agents.
    func canSwitchAgents(on machine: String) -> Bool {
        conns[machine]?.hello?.capabilities?.contains(agentSwitchCapability) == true
    }

    /// Pane k's Switch Agent ▸.
    func switchMenu(for k: String) -> RowMenuItem? {
        guard let p = pane(k), let a = p.agent?.name, p.running else { return nil }
        let m = unkey(k).machine
        return switchAgentMenu(pane: k, from: a, agents: agentStatus[m] ?? agents[m] ?? [], canSwitch: canSwitchAgents(on: m),
                               waiting: switchingAgents[k])
    }

    /// Asks what switching pane k to agent would carry, and puts up the
    /// card that counts down to it.
    func askSwitch(_ k: String, to agent: String) {
        let (m, id) = unkey(k)
        guard let c = conns[m] else { return }
        Task {
            do {
                let pre = try await c.call("agent.switch", AgentSwitchParams(id: id, to: agent, preview: true), as: AgentSwitchResult.self)
                switchCard = SwitchAsk(pane: k, to: agent, preview: pre, stage: .counting(until: Date().addingTimeInterval(switchCountdown)))
                countDown(k)
            } catch {
                say("Can't switch \(id): \(error.localizedDescription)", .err)
            }
        }
    }

    /// Asks by itself once the card's countdown is over, unless it was
    /// put away, sent early or replaced by another card meanwhile.
    private func countDown(_ k: String) {
        guard case .counting(let until)? = switchCard?.stage else { return }
        Task {
            try? await Task.sleep(nanoseconds: UInt64(max(until.timeIntervalSinceNow, 0) * 1_000_000_000))
            guard let card = switchCard, card.pane == k, case .counting(let u) = card.stage, u == until else { return }
            switchNow()
        }
    }

    /// The card's switch, asked for now. Never with now: a turn under
    /// way is waited for, which the answer says.
    func switchNow() {
        guard var card = switchCard, case .counting = card.stage else { return }
        let (m, id) = unkey(card.pane)
        guard let c = conns[m] else { return }
        card.stage = .asking
        switchCard = card
        Task {
            do {
                let res = try await c.call("agent.switch", AgentSwitchParams(id: id, to: card.to), as: AgentSwitchResult.self)
                if res.pending == true {
                    switchingAgents[card.pane] = AgentSwitching(pane: id, from: res.from, to: card.to, waits: res.waits)
                    if switchCard?.pane == card.pane { switchCard?.stage = .waiting(res.waits ?? []) }
                } else {
                    if switchCard?.pane == card.pane { switchCard = nil }
                    say("\(id): \(switchedText(res))")
                }
            } catch {
                if switchCard?.pane == card.pane {
                    switchCard?.stage = .failed(error.localizedDescription)
                } else {
                    say("Could not switch \(id): \(error.localizedDescription)", .err)
                }
            }
        }
    }

    /// The card put away; a switch the server holds called off.
    func dismissSwitchCard() {
        guard let card = switchCard else { return }
        switchCard = nil
        if card.held { cancelSwitch(card.pane) }
    }

    /// Back to the agents, from the card.
    func chooseAgain() {
        guard let card = switchCard, !card.held else { return }
        switchCard = nil
        switchPick = SwitchPick(pane: card.pane)
    }

    /// A waiting switch called off.
    func cancelSwitch(_ k: String) {
        let (m, id) = unkey(k)
        let to = switchingAgents[k]?.to ?? switchCard?.to
        switchingAgents[k] = nil
        guard let c = conns[m] else { return }
        Task {
            do {
                try await c.call("agent.switch.cancel", AgentSwitchCancelParams(id: id))
                say("The switch of \(id)\(to.map { " to " + agentTitle($0) } ?? "") was called off")
            } catch {
                say("Could not call off the switch of \(id): \(error.localizedDescription)", .err)
            }
        }
    }

    func agentSwitching(_ s: AgentSwitching, on machine: String) {
        let k = ConchKit.key(machine, s.pane)
        switchingAgents[k] = s
        if switchCard?.pane == k, switchCard?.stage == .asking { switchCard?.stage = .waiting(s.waits ?? []) }
        limitSwitchWaits(s, key: k)
    }

    func agentSwitched(_ r: AgentSwitchResult, on machine: String) {
        guard let id = r.pane?.id else { return }
        let k = ConchKit.key(machine, id)
        let waited = switchingAgents.removeValue(forKey: k) != nil
        let onCard = switchCard?.pane == k
        if !waited, !onCard, limitSwitched(r, key: k, machine: machine) { return }
        if let err = r.error {
            if onCard { switchCard?.stage = .failed(err) } else { say("\(id): the switch to \(agentTitle(r.to)) failed: \(err)", .err) }
            return
        }
        if onCard, case .waiting? = switchCard?.stage {
            switchCard = nil
            say("\(id): \(switchedText(r))")
        } else if waited {
            say("\(id): \(switchedText(r))")
        }
    }

    /// The banner over a switched pane, unless put away.
    func switchBannerText(_ k: String) -> String? {
        guard let p = pane(k), !bannerGone.contains(switchBannerKey(k, p)) else { return nil }
        return switchBanner(p, agents: agentStatus[unkey(k).machine] ?? [])
    }

    func dismissBanner(_ k: String) {
        guard let p = pane(k) else { return }
        bannerGone.insert(switchBannerKey(k, p))
    }

    private func switchedText(_ r: AgentSwitchResult) -> String {
        if r.crossed == SwitchWay.handoff {
            return "switched to \(agentTitle(r.to)) with the handoff document" + ((r.why ?? "").isEmpty ? "" : " (\(r.why!))")
        }
        return "switched to \(agentTitle(r.to)), with the whole conversation"
    }
}

/// Choose… on the card: the agents again, in a sheet, since a context
/// menu cannot be opened for somebody.
struct SwitchPick: Identifiable, Equatable {
    let pane: String
    var id: String { pane }
}
