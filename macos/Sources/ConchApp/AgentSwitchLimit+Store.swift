import ConchKit
import Foundation

// Another agent offered at a plan limit (agent.switch.limit.v1, #352
// phase 3): the server's offer on a card at the foot of the pane
// (LimitCardView), and the switch it made itself said, never silent.
extension Store {
    /// agent.switch.offered: the card, with the preview read for it; an
    /// offer withdrawn takes its card away; one nobody can take is said.
    func agentSwitchOffered(_ o: AgentSwitchOffer, on machine: String) {
        let k = ConchKit.key(machine, o.pane)
        if o.gone == true {
            if limitCard?.pane == k { limitCard = nil }
            return
        }
        let name = pane(k)?.displayName ?? o.pane
        guard let to = o.to, !to.isEmpty else {
            say("\(name): \(limitHeading(o, agents: agentStatus[machine] ?? []))\(o.why.map { "; " + $0 } ?? "")", .err)
            return
        }
        limitCard = LimitOffer(pane: k, offer: o)
        guard let c = conns[machine] else { return }
        Task {
            guard let pre = try? await c.call("agent.switch", AgentSwitchParams(id: o.pane, to: to, preview: true), as: AgentSwitchResult.self) else { return }
            if limitCard?.pane == k, limitCard?.offer == o { limitCard?.preview = pre }
        }
    }

    /// Continue in the agent offered: the card becomes the switch's.
    func continueAtLimit() {
        guard let card = limitCard, let to = card.offer.to else { return }
        limitCard = nil
        let pre = card.preview ?? AgentSwitchResult(from: card.offer.from, to: to, crossed: card.offer.crossed)
        switchCard = SwitchAsk(pane: card.pane, to: to, preview: pre, stage: .counting(until: Date()))
        switchNow()
    }

    /// Wait for the reset — or Cancel, for the switch the server would
    /// make itself: the offer called off on the server.
    func waitAtLimit() {
        guard let card = limitCard else { return }
        limitCard = nil
        let (m, id) = unkey(card.pane)
        let from = ConchKit.agentTitle(card.offer.from, among: agentStatus[m] ?? [])
        guard let c = conns[m] else { return }
        Task {
            do {
                try await c.call("agent.switch.cancel", AgentSwitchCancelParams(id: id))
                say(card.offer.auto == true ? "The switch was called off: \(from) waits for its plan to reset" : "\(from) waits for its plan to reset")
            } catch {
                say("Could not call off the switch of \(id): \(error.localizedDescription)", .err)
            }
        }
    }

    /// Choose…: the agents, in the sheet the switch card's Choose… opens.
    func chooseAtLimit() {
        guard let card = limitCard else { return }
        limitCard = nil
        switchPick = SwitchPick(pane: card.pane)
    }

    /// A switch the server made at a plan limit, said; true when it was one.
    func limitSwitched(_ r: AgentSwitchResult, key k: String, machine: String) -> Bool {
        guard r.reason == "limit" else { return false }
        if limitCard?.pane == k { limitCard = nil }
        let name = pane(k)?.displayName ?? unkey(k).id
        say("\(name): \(limitSwitchedText(r, agents: agentStatus[machine] ?? []))", r.error == nil ? .ok : .err)
        return true
    }

    /// The server's own switch waiting on background commands: the card
    /// counting down to it becomes the switch card, waiting.
    func limitSwitchWaits(_ s: AgentSwitching, key k: String) {
        guard let card = limitCard, card.pane == k else { return }
        limitCard = nil
        switchCard = SwitchAsk(pane: k, to: s.to, preview: card.preview ?? AgentSwitchResult(from: s.from, to: s.to), stage: .waiting(s.waits ?? []))
    }
}
