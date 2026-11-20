import ConchKit
import SwiftUI

/// An agent the server will move off its account, counting down: Move Now,
/// Choose… another, or Keep it where it is.
struct MovingCard: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    let key: String
    let moving: AgentMoving

    var body: some View {
        let _ = look
        let p = store.pane(key)
        VStack(alignment: .leading, spacing: 7) {
            HStack(spacing: 6) {
                Mark(agent: moving.agent)
                Text(p?.displayName ?? moving.pane).font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.text).lineLimit(1)
            }
            TimelineView(.periodic(from: .now, by: 1)) { ctx in
                let s = secondsUntil(moving, now: ctx.date)
                Text(moving.reason == "limit"
                     ? "Hit its plan limit on \(moving.from). Moving it to \(moving.to), the account with most room, in \(s)s."
                     : "\(moving.from) is past its failover line. Moving it to \(moving.to) in \(s)s.")
                    .font(.ui(size: 11.5)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
            }
            HStack(spacing: 6) {
                Button("Move Now") { store.moveNow(key) }.buttonStyle(.borderedProminent).tint(Theme.accentDeep).controlSize(.small)
                Button("Choose…") { store.askMove(key, to: nil) }.controlSize(.small)
                Button("Keep on \(moving.from)") { store.cancelMove(key) }.controlSize(.small)
            }
            .focusEffectDisabled()
        }
        .padding(11)
        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise.opacity(0.85)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.accent.opacity(0.6)))
        .padding(.bottom, 6)
    }
}

/// An agent moved off an account that has room again: Move Back, or Later.
struct MoveBackCard: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    let key: String
    let pane: PaneInfo
    let to: String

    var body: some View {
        let _ = look
        VStack(alignment: .leading, spacing: 7) {
            HStack(spacing: 6) {
                Mark(agent: pane.agent?.name)
                Text(pane.displayName).font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.text).lineLimit(1)
            }
            Text("\(to) has room again. It moved this agent off \(to) to \(accountLabel(pane.account)).")
                .font(.ui(size: 11.5)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
            HStack(spacing: 6) {
                Button("Move Back") { store.moveAgent(key, to: to, reason: "back") }.buttonStyle(.borderedProminent).tint(Theme.accentDeep).controlSize(.small)
                Button("Later") { store.backLater.insert(key) }.controlSize(.small)
            }
            .focusEffectDisabled()
        }
        .padding(11)
        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise.opacity(0.85)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.line))
        .padding(.bottom, 6)
    }
}

/// Moving an agent by hand: from where to where, both accounts' plans
/// with the failover line, what happens, and the account to choose when
/// conch was asked to.
struct MoveAgentSheet: View {
    @Environment(\.conchTheme) private var look
    @Environment(\.dismiss) private var dismiss
    @ObservedObject var store: Store
    let ask: MoveAsk
    @State private var to: String = ""

    var body: some View {
        let _ = look
        let ranks = store.ranks(for: ask.pane).filter { $0.label != ask.from }
        let usable = ranks.filter(\.usable)
        let target = to.isEmpty ? (ask.to ?? usable.first?.label ?? "") : to
        let accounts = store.usageAccountsHere
        let line = failoverLine(store.agentAccounts[unkey(ask.pane).machine], agent: ask.agent)
        let name = store.pane(ask.pane)?.displayName ?? unkey(ask.pane).id
        VStack(alignment: .leading, spacing: 14) {
            Text("Move “\(name)” to \(target.isEmpty ? "another account" : target)?").font(.ui(size: 15, weight: .semibold))
            HStack(alignment: .top, spacing: 10) {
                card(ask.from, accounts: accounts, line: line, to: false)
                Image(systemName: "arrow.right").foregroundStyle(Theme.muted).padding(.top, 22)
                card(target, accounts: accounts, line: line, to: true)
            }
            if ranks.count > 1 || ask.to == nil {
                Picker("Account", selection: Binding(get: { target }, set: { to = $0 })) {
                    ForEach(ranks) { r in
                        Text("\(r.label) · \(r.why ?? "")").tag(r.label)
                    }
                }
                .pickerStyle(.menu)
            }
            VStack(alignment: .leading, spacing: 3) {
                Text("1. Stops the agent; it is not in the middle of a turn.")
                Text("2. Copies its conversation into \(target.isEmpty ? "the account" : target)'s folder.")
                Text("3. Resumes it there, in a pane that takes this one's place.")
            }
            .font(.ui(size: 12)).foregroundStyle(Theme.text2)
            Text("Each account has its own CLAUDE.md, skills and MCP servers, and the first replies after a move re-read the conversation.")
                .font(.ui(size: 11.5)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            if let r = ranks.first(where: { $0.label == target }), !r.usable {
                Text("\(target) can't take it: \(r.why ?? "unavailable").").font(.ui(size: 12)).foregroundStyle(Theme.warn)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Move to \(target.isEmpty ? "…" : target)") {
                    store.moveAgent(ask.pane, to: target, reason: "hand")
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(target.isEmpty || ranks.first(where: { $0.label == target })?.usable == false)
            }
        }
        .padding(20).frame(width: 480)
        .focusEffectDisabled()
    }

    func card(_ account: String, accounts: [UsageAccount], line: Int, to: Bool) -> some View {
        let a = accounts.first { $0.agent == ask.agent && accountLabel($0.name) == account }
        return VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 6) {
                Mark(agent: ask.agent)
                Text(account.isEmpty ? "—" : account).font(.ui(size: 13, weight: .semibold))
                if to, a?.info?.signedIn == true {
                    Text("✓ Signed in").font(.ui(size: 10, weight: .semibold)).foregroundStyle(Theme.ok)
                }
            }
            if let l = a?.limits, l.reportsUse {
                if let w = l.fiveHour { bar("5h", w.usedPct, line) }
                if let w = l.week { bar("7d", w.usedPct, line) }
            } else {
                Text("No plan reported yet").font(.ui(size: 11)).foregroundStyle(Theme.muted)
            }
        }
        .padding(10).frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 9).fill(to ? Theme.accent.opacity(0.06) : Theme.raise2))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(to ? Theme.accent.opacity(0.6) : Theme.line))
    }

    func bar(_ label: String, _ pct: Double, _ line: Int) -> some View {
        let level = usageLevel(pct, line: line)
        return HStack(spacing: 6) {
            Text(label).font(.ui(size: 11)).foregroundStyle(Theme.muted).frame(width: 18, alignment: .leading)
            LevelBar(pct: pct, line: line, color: levelColor(level), width: 110, height: 6)
            Text("\(Int(pct.rounded()))%").font(.ui(size: 11)).monospacedDigit().foregroundStyle(level == .room ? Theme.text2 : levelColor(level))
        }
    }
}
