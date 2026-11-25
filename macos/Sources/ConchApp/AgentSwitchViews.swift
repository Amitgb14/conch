import ConchKit
import SwiftUI

/// The switch card at the foot of a pane (mock: AppCard): where it goes,
/// what crosses, a countdown, and Switch Now, Choose… or Cancel.
struct SwitchCardView: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    let ask: SwitchAsk

    var body: some View {
        let _ = look
        let agents = store.agentStatus[unkey(ask.pane).machine] ?? []
        let to = agentTitle(ask.to, among: agents)
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Text("⇄").font(.ui(size: 13, weight: .bold)).foregroundStyle(Theme.accent)
                Mark(agent: ask.to)
                TimelineView(.periodic(from: .now, by: 1)) { ctx in
                    Text(heading(to, now: ctx.date)).font(.ui(size: 13, weight: .semibold))
                        .foregroundStyle(failed ? Theme.warn : Theme.text)
                }
                Spacer()
                Text(where_).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1)
            }
            ForEach(Array(lines(agents).enumerated()), id: \.offset) { _, l in
                HStack(alignment: .firstTextBaseline, spacing: 10) {
                    Text(l.label).font(.ui(size: 12)).foregroundStyle(Theme.muted).frame(width: 58, alignment: .leading)
                    Text(l.value).font(.ui(size: 12)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
                }
            }
            HStack(spacing: 8) {
                switch ask.stage {
                case .counting:
                    Button("Switch Now") { store.switchNow() }.buttonStyle(.borderedProminent).tint(Theme.accentDeep).keyboardShortcut(.defaultAction)
                    Button("Choose…") { store.chooseAgain() }
                    Button("Cancel") { store.dismissSwitchCard() }.keyboardShortcut(.cancelAction)
                case .asking, .waiting:
                    Button("Cancel") { store.dismissSwitchCard() }.keyboardShortcut(.cancelAction)
                case .failed:
                    Button("Close") { store.dismissSwitchCard() }.keyboardShortcut(.cancelAction)
                    Button("Choose…") { store.chooseAgain() }
                }
            }
            .controlSize(.small).padding(.top, 6)
            .focusEffectDisabled()
        }
        .padding(.horizontal, 16).padding(.vertical, 12)
        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(failed ? Theme.warn.opacity(0.7) : Theme.line))
        .shadow(color: .black.opacity(0.12), radius: 12, y: 6)
        .padding(16)
    }

    var failed: Bool { if case .failed = ask.stage { return true }; return false }

    func heading(_ to: String, now: Date) -> String {
        switch ask.stage {
        case .counting: return "Switching to \(to) in \(ask.secondsLeft(now: now) ?? 0)s"
        case .asking: return "Switching to \(to)…"
        case .waiting: return "Switching to \(to) when the turn ends"
        case .failed: return "Could not switch to \(to)"
        }
    }

    var where_: String {
        let p = store.pane(ask.pane)
        let name = p?.displayName ?? unkey(ask.pane).id
        guard case .counting = ask.stage else { return name }
        let busy = p?.agent?.state == .working || p?.agent?.state == .blocked
        return name + " · " + (busy ? "waits for the turn to end" : "the turn has ended")
    }

    func lines(_ agents: [AgentAvailability]) -> [(label: String, value: String)] {
        let from = agentTitle(ask.preview.from, among: agents)
        switch ask.stage {
        case .failed(let why): return [("Why", why), ("Keeps", "\(from) running as it was")]
        case .waiting(let waits): return [("Waits", waits.joined(separator: " and ")), ("Keeps", "\(from)'s session as it is, so you can switch back")]
        default: return switchCardLines(ask.preview, agents: agents)
        }
    }
}

/// The line under a switched pane's header (mock: AppAfter): where its
/// conversation came from, Switch back, and × to put it away.
struct SwitchBannerView: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    let pane: String
    let text: String

    var body: some View {
        let _ = look
        HStack(spacing: 10) {
            Text(text).font(.ui(size: 12)).foregroundStyle(Theme.ok).lineLimit(1).truncationMode(.tail)
            Spacer(minLength: 8)
            if let back = store.pane(pane)?.switchedFrom?.agent {
                Button("Switch Back") { store.askSwitch(pane, to: back) }
                    .buttonStyle(.plain).font(.ui(size: 12)).foregroundStyle(Theme.accent).underline()
                    .help("Switch the conversation back to \(agentTitle(back)), with what was said since")
            }
            Button { store.dismissBanner(pane) } label: {
                Image(systemName: "xmark").font(.system(size: 9, weight: .semibold)).foregroundStyle(Theme.muted)
            }
            .buttonStyle(.plain).help("Hide this")
        }
        .padding(.horizontal, 12).padding(.vertical, 5)
        .background(Theme.ok.opacity(0.10))
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.ok.opacity(0.25)).frame(height: 1) }
    }
}

/// Choose… from the card: the same agents as Switch Agent ▸, in a sheet.
struct SwitchPickSheet: View {
    @Environment(\.dismiss) private var dismiss
    @ObservedObject var store: Store
    let pick: SwitchPick

    var body: some View {
        let menu = store.switchMenu(for: pick.pane)
        VStack(alignment: .leading, spacing: 8) {
            Text("Switch “\(store.pane(pick.pane)?.displayName ?? unkey(pick.pane).id)” to").font(.ui(size: 15, weight: .semibold))
            ForEach(Array((menu?.children ?? []).enumerated()), id: \.offset) { _, it in
                switch it.action {
                case .switchAgent(let k, let to):
                    Button { dismiss(); store.askSwitch(k, to: to) } label: {
                        HStack(spacing: 7) { Mark(agent: to); Text(it.title); Spacer() }.contentShape(Rectangle())
                    }
                    .buttonStyle(.plain).padding(.vertical, 3)
                case .divider:
                    Divider()
                default:
                    Text(it.title).font(.ui(size: 12)).foregroundStyle(Theme.muted)
                }
            }
            HStack { Spacer(); Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction) }
        }
        .padding(20).frame(width: 400)
    }
}

/// The out-of-plan card at the foot of a pane (mock: AppLimit): every
/// account full, the agent that could carry on, what would cross, and
/// Continue in it, Wait for the Reset or Choose… — or, when the server
/// will switch by itself, its countdown with Continue Now and Cancel.
struct LimitCardView: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    let card: LimitOffer

    var body: some View {
        let _ = look
        let agents = store.agentStatus[unkey(card.pane).machine] ?? []
        let buttons = limitButtons(card.offer, agents: agents)
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Text("⚠").font(.ui(size: 13, weight: .bold)).foregroundStyle(Theme.warn)
                Text(limitHeading(card.offer, agents: agents)).font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.text)
                Spacer()
                Text(limitFullness(card.offer)).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1)
            }
            TimelineView(.periodic(from: .now, by: 1)) { ctx in
                VStack(alignment: .leading, spacing: 3) {
                    ForEach(Array(limitLines(card, agents: agents, now: ctx.date).enumerated()), id: \.offset) { i, l in
                        HStack(spacing: 6) {
                            if i == 0, let to = card.offer.to { Mark(agent: to) }
                            Text(l).font(.ui(size: 12)).foregroundStyle(i == 0 ? Theme.text : Theme.text2)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
            }
            HStack(spacing: 8) {
                Button(buttons[0]) { store.continueAtLimit() }.buttonStyle(.borderedProminent).tint(Theme.accentDeep).keyboardShortcut(.defaultAction)
                Button(buttons[1]) { store.waitAtLimit() }.keyboardShortcut(.cancelAction)
                Button(buttons[2]) { store.chooseAtLimit() }
            }
            .controlSize(.small).padding(.top, 6)
            .focusEffectDisabled()
            if card.offer.auto != true, (card.offer.setting ?? "").isEmpty, let to = card.offer.to {
                Text("To do this without asking: Settings › Agents › When out of plan, or [agents] switch_at_limit = \"\(to)\"")
                    .font(.ui(size: 11)).foregroundStyle(Theme.muted).padding(.top, 2)
            }
        }
        .padding(.horizontal, 16).padding(.vertical, 12)
        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.warn.opacity(0.7)))
        .shadow(color: .black.opacity(0.12), radius: 12, y: 6)
        .padding(16)
    }
}
