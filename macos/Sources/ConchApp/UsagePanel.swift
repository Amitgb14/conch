import Charts
import ConchKit
import SwiftUI

// The status bar's usage: one chip, a mark per account that has reported
// its plan and the account in view's windows; a click opens a panel with a
// tab per account — its plan as rings with the failover line, the tokens it
// used by model over a day, week or month, and what kinds of tokens they
// were — and the panel maximizes into a window comparing every account.
// Tokens, not dollars: conch keeps no price list.

/// The model colours a chart stacks, in a fixed order (validated for colour
/// vision deficiency in light and dark), and a grey for the rest.
@MainActor func modelColors(_ n: Int) -> [Color] {
    let light: [UInt32] = [0x2A78D6, 0xEB6834, 0x1BAF7A]
    let dark: [UInt32] = [0x3987E5, 0xD95926, 0x199E70]
    let set = (Theme.current.light ? light : dark).map { Color(nsColor: NSColor(hex: $0)) }
    return (0..<n).map { $0 < set.count ? set[$0] : Theme.muted }
}

/// A level's colour: the accent with room, amber getting close or past the
/// line, red at the limit.
@MainActor func levelColor(_ l: UsageLevel) -> Color {
    switch l {
    case .room: Theme.accent
    case .close, .past: Theme.warn
    case .limit: Theme.err
    }
}

/// The agent's mark, small, ringed in the bar's colour so marks can overlap.
struct MarkStack: View {
    @Environment(\.conchTheme) private var look
    let accounts: [UsageAccount]
    let raised: String?

    var body: some View {
        let _ = look
        HStack(spacing: -5) {
            ForEach(accounts) { a in
                Mark(agent: a.agent)
                    .frame(width: 16, height: 16)
                    .background(Circle().fill(Theme.raise))
                    .overlay(Circle().stroke(a.id == raised ? Theme.accent : Theme.window, lineWidth: 1.5))
                    .offset(y: a.id == raised ? -1 : 0)
                    .help(a.agent == "claude" ? "Claude Code · \(a.tabLabel)" : a.tabLabel)
            }
        }
    }
}

/// The status bar chip: the marks, then the account in view's 5h and 7d.
struct AccountUsageChip: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store

    var body: some View {
        let _ = look
        let accounts = store.usageAccountsHere
        let view = store.agentInView
        let shown = chipAccount(accounts, agent: view?.agent, account: view?.account)
        HStack(spacing: 8) {
            MarkStack(accounts: chipMarks(accounts), raised: shown?.id)
            if let a = shown, let l = a.limits {
                Text(a.tabLabel).font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.text)
                let line = failoverLine(store.agentAccounts["local"], agent: a.agent)
                if let w = l.fiveHour { window("5h", w.usedPct, line) }
                if let w = l.week {
                    if l.fiveHour != nil { Rectangle().fill(Theme.line).frame(width: 1, height: 10) }
                    window("7d", w.usedPct, line)
                }
            }
        }
        .padding(.leading, 4).padding(.trailing, 9).padding(.vertical, 2)
        .background(Capsule().fill(Theme.raise.opacity(0.7)))
    }

    func window(_ name: String, _ pct: Double, _ line: Int) -> some View {
        let level = usageLevel(pct, line: line)
        return HStack(spacing: 4) {
            Text(name).foregroundStyle(Theme.muted)
            LevelBar(pct: pct, line: 0, color: levelColor(level), width: 30, height: 5)
            Text("\(Int(pct.rounded()))%").monospacedDigit().foregroundStyle(level == .room ? Theme.text2 : levelColor(level))
        }
    }
}

/// A bar of how much is used, with the failover line as a tick when there is one.
struct LevelBar: View {
    @Environment(\.conchTheme) private var look
    let pct: Double
    let line: Int
    let color: Color
    var width: CGFloat = 60
    var height: CGFloat = 7

    var body: some View {
        let _ = look
        ZStack(alignment: .leading) {
            Capsule().fill(Theme.line2)
            Capsule().fill(color).frame(width: max(width * min(max(pct, 0), 100) / 100, height))
            if line > 0 {
                RoundedRectangle(cornerRadius: 1).fill(Theme.text2.opacity(0.6))
                    .frame(width: 2, height: height + 6).offset(x: width * CGFloat(line) / 100 - 1)
            }
        }
        .frame(width: width, height: height)
    }
}

/// A plan window as a ring: used, the failover line as a tick, the share in
/// the middle, and what that means in words under it.
struct PlanRing: View {
    @Environment(\.conchTheme) private var look
    let label: String
    let pct: Double
    let resets: Date?
    let line: Int

    var body: some View {
        let _ = look
        let level = usageLevel(pct, line: line)
        VStack(spacing: 3) {
            ZStack {
                Circle().stroke(Theme.line2, lineWidth: 7)
                Circle().trim(from: 0, to: min(max(pct, 0.6), 100) / 100)
                    .stroke(levelColor(level), style: StrokeStyle(lineWidth: 7, lineCap: .round))
                    .rotationEffect(.degrees(-90))
                if line > 0 {
                    Capsule().fill(Theme.text2).frame(width: 1.5, height: 11)
                        .offset(y: -31).rotationEffect(.degrees(Double(line) * 3.6))
                }
                Text("\(Int(pct.rounded()))%").font(.ui(size: 15, weight: .semibold, design: .monospaced)).foregroundStyle(Theme.text)
            }
            .frame(width: 66, height: 66).padding(4)
            Text(label).font(.ui(size: 11.5, weight: .medium)).foregroundStyle(Theme.text2)
            HStack(spacing: 3) {
                if level != .room { Image(systemName: level == .limit ? "xmark.octagon.fill" : "exclamationmark.triangle.fill").font(.system(size: 9)) }
                Text(level.words)
            }
            .font(.ui(size: 10.5, weight: level == .room ? .regular : .semibold))
            .foregroundStyle(level == .room ? Theme.muted : levelColor(level))
            if let r = resets, r.timeIntervalSince1970 > 0 {
                TimelineView(.periodic(from: .now, by: 60)) { ctx in
                    Text(r > ctx.date ? "resets in \(elapsed(ctx.date, r))" : "reset").font(.ui(size: 10.5)).foregroundStyle(Theme.muted)
                }
            }
        }
        .frame(maxWidth: .infinity).padding(.vertical, 8)
        .background(RoundedRectangle(cornerRadius: 9).fill(Theme.raise2))
        .help("\(label): \(Int(pct.rounded()))% used\(line > 0 ? ". Failover at \(line)%." : "")")
    }
}

/// Tokens by model over a span, stacked, with the bucket under the pointer
/// said in full.
struct TokenChart: View {
    @Environment(\.conchTheme) private var look
    let buckets: [UsageBucket]
    let span: UsageSpan
    @State private var hover: Date?

    var body: some View {
        let _ = look
        let models = chartModels(buckets)
        let colors = modelColors(models.count).enumerated().map { models[$0.offset] == otherModel ? Theme.muted : $0.element }
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 12) {
                ForEach(Array(models.enumerated()), id: \.element) { i, m in
                    HStack(spacing: 5) {
                        RoundedRectangle(cornerRadius: 2).fill(colors[i]).frame(width: 9, height: 9)
                        Text(m).font(.ui(size: 11)).foregroundStyle(Theme.text2).lineLimit(1)
                    }
                }
            }
            if models.isEmpty {
                Text("No tokens \(span.phrase).").font(.ui(size: 12)).foregroundStyle(Theme.muted).frame(height: 110)
            } else {
                Chart {
                    ForEach(buckets, id: \.start) { b in
                        ForEach(models, id: \.self) { m in
                            BarMark(x: .value("When", b.start, unit: span == .day ? .hour : .day),
                                    y: .value("Tokens", bucketTokens(b, model: m, shown: models)))
                                .foregroundStyle(by: .value("Model", m))
                                .cornerRadius(2)
                        }
                    }
                    if let h = hover, let b = bucket(at: h) {
                        RuleMark(x: .value("When", b.start, unit: span == .day ? .hour : .day))
                            .foregroundStyle(Theme.accent.opacity(0.12))
                            .lineStyle(StrokeStyle(lineWidth: span == .month ? 8 : 20))
                            .annotation(position: .top, alignment: .center, overflowResolution: .init(x: .fit(to: .chart), y: .disabled)) {
                                tip(b, models, colors)
                            }
                    }
                }
                .chartForegroundStyleScale(domain: models, range: colors)
                .chartLegend(.hidden)
                .chartXSelection(value: $hover)
                .chartYAxis {
                    AxisMarks(position: .leading, values: .automatic(desiredCount: 3)) { v in
                        AxisGridLine().foregroundStyle(Theme.line)
                        AxisValueLabel { if let n = v.as(Int.self) { Text(humanCount(n)).font(.ui(size: 9.5)).foregroundStyle(Theme.muted) } }
                    }
                }
                .chartXAxis {
                    AxisMarks(values: .automatic(desiredCount: span == .week ? 7 : 6)) { v in
                        AxisValueLabel(format: span == .day ? .dateTime.hour() : span == .week ? .dateTime.weekday(.abbreviated) : .dateTime.month(.abbreviated).day())
                            .font(.ui(size: 9.5)).foregroundStyle(Theme.muted)
                    }
                }
                .frame(height: 120)
            }
        }
    }

    func bucket(at d: Date) -> UsageBucket? {
        buckets.last { $0.start <= d }
    }

    func tip(_ b: UsageBucket, _ models: [String], _ colors: [Color]) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(b.start, format: span == .day ? .dateTime.weekday().hour() : .dateTime.weekday(.wide).month().day())
                .font(.ui(size: 11.5, weight: .semibold))
            ForEach(Array(models.enumerated()), id: \.element) { i, m in
                HStack(spacing: 5) {
                    RoundedRectangle(cornerRadius: 2).fill(colors[i]).frame(width: 8, height: 8)
                    Text(m).foregroundStyle(Theme.text2)
                    Spacer(minLength: 10)
                    Text(humanCount(bucketTokens(b, model: m, shown: models))).monospacedDigit()
                }
            }
            Divider()
            HStack { Text("Total"); Spacer(); Text(humanCount(b.tokens.total)).monospacedDigit() }.fontWeight(.semibold)
        }
        .font(.ui(size: 11)).padding(8).frame(width: 210)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise).shadow(radius: 6, y: 2))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
    }
}

/// One kind of token, and how strongly the accent draws it.
struct TokenKind: Identifiable {
    let name: String
    let count: Int
    let shade: Double
    var id: String { name }
}

/// What kinds of tokens a span used: one bar split four ways, and each with
/// its count.
struct TokenKinds: View {
    @Environment(\.conchTheme) private var look
    let tokens: UsageTokens

    var kinds: [TokenKind] {
        [TokenKind(name: "Input", count: tokens.input, shade: 1), TokenKind(name: "Output", count: tokens.output, shade: 0.7),
         TokenKind(name: "Cache write", count: tokens.cacheWrite, shade: 0.45), TokenKind(name: "Cache read", count: tokens.cacheRead, shade: 0.22)]
    }

    var body: some View {
        let _ = look
        VStack(alignment: .leading, spacing: 7) {
            GeometryReader { g in KindsBar(kinds: kinds.filter { $0.count > 0 }, total: max(tokens.total, 1), width: g.size.width) }
                .frame(height: 12)
            HStack {
                ForEach(kinds) { k in KindLabel(kind: k) }
            }
            .font(.ui(size: 11))
        }
    }
}

struct KindsBar: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let kinds: [TokenKind]
    let total: Int
    let width: CGFloat

    var body: some View {
        let _ = look
        HStack(spacing: 2) {
            ForEach(kinds) { k in
                let share = Double(k.count) / Double(total)
                Rectangle().fill(Theme.accent.opacity(k.shade))
                    .frame(width: max(width * CGFloat(share) - 2, 2))
                    .help("\(k.name): \(humanCount(k.count)) tokens (\(Int((share * 100).rounded()))%)")
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: 4))
    }
}

struct KindLabel: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let kind: TokenKind

    var body: some View {
        let _ = look
        VStack(alignment: .leading, spacing: 1) {
            HStack(spacing: 4) {
                RoundedRectangle(cornerRadius: 2).fill(Theme.accent.opacity(kind.shade)).frame(width: 8, height: 8)
                Text(kind.name).foregroundStyle(Theme.muted)
            }
            Text(humanCount(kind.count)).fontWeight(.semibold).monospacedDigit()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// The span picker: Day, Week, Month.
struct SpanPicker: View {
    @Binding var span: UsageSpan

    var body: some View {
        Picker("", selection: $span) {
            ForEach(UsageSpan.allCases, id: \.self) { Text($0.title).tag($0) }
        }
        .pickerStyle(.segmented).labelsHidden().frame(width: 170)
    }
}

/// One account: its plan, its tokens over the span, and their kinds.
struct AccountUsageDetail: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    let account: UsageAccount
    @Binding var span: UsageSpan

    var body: some View {
        let _ = look
        let line = failoverLine(store.agentAccounts["local"], agent: account.agent)
        VStack(alignment: .leading, spacing: 16) {
            section("Plan", line > 0 ? "failover at \(line)% · the tick on each ring" : "failover off") {
                if let l = account.limits, l.reportsUse {
                    HStack(spacing: 8) {
                        ForEach(l.allWindows, id: \.key) { w in
                            PlanRing(label: PlanLimits.label(w.key), pct: w.usedPct, resets: w.resetsAt, line: line)
                        }
                    }
                } else {
                    Text("No plan reported yet. It shows once \(store.agentTitle(account.agent)) on \(account.tabLabel) has had a few replies.")
                        .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
                }
            }
            if account.usage == nil && !store.keepsAccountUsage {
                Text("conch here is older and cannot say what each account used: reload its server.")
                    .font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
            } else {
                let total = spanTotal(account.usage, span)
                section("Tokens", nil, trailing: AnyView(SpanPicker(span: $span))) {
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        Text(humanCount(total?.tokens.total ?? 0)).font(.ui(size: 19, weight: .semibold)).monospacedDigit()
                        Text("\(span.phrase) · \(total?.tokens.messages ?? 0) responses").font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                    }
                    TokenChart(buckets: spanBuckets(account.usage, span), span: span)
                }
                section("Kinds", nil) { TokenKinds(tokens: total?.tokens ?? UsageTokens()) }
            }
            if let d = account.info?.dir, !d.isEmpty {
                Text(d).font(.ui(size: 10.5, design: .monospaced)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.middle)
            }
        }
    }

    @ViewBuilder func section<C: View>(_ title: String, _ sub: String?, trailing: AnyView? = nil, @ViewBuilder content: () -> C) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline) {
                Text(title.uppercased()).font(.ui(size: 10.5, weight: .semibold)).tracking(0.6).foregroundStyle(Theme.muted)
                Spacer()
                if let sub { Text(sub).font(.ui(size: 11)).foregroundStyle(Theme.muted) }
                trailing
            }
            content()
        }
    }
}

/// The panel the chip opens: a tab per account, the one in view first
/// chosen, and a button to maximize it.
struct UsagePanel: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    let maximize: () -> Void
    @State private var tab: String?
    @State private var span: UsageSpan = .week

    var body: some View {
        let _ = look
        let accounts = store.usageAccountsHere
        let view = store.agentInView
        let chip = chipAccount(accounts, agent: view?.agent, account: view?.account)
        let current = accounts.first { $0.id == (tab ?? panelTab(accounts, chip: chip)) } ?? accounts.first
        VStack(spacing: 0) {
            HStack(alignment: .bottom, spacing: 0) {
                ForEach(accounts) { a in
                    tabButton(a, selected: a.id == current?.id, inView: a.id == chip?.id && view != nil,
                              line: failoverLine(store.agentAccounts["local"], agent: a.agent))
                }
                Spacer(minLength: 4)
                Button(action: maximize) { Image(systemName: "arrow.up.left.and.arrow.down.right").font(.system(size: 11, weight: .semibold)) }
                    .buttonStyle(.plain).foregroundStyle(Theme.muted).help("Every account side by side").padding(.bottom, 8).padding(.trailing, 4)
            }
            .padding(.horizontal, 8).padding(.top, 6)
            Divider()
            if let a = current {
                AccountUsageDetail(store: store, account: a, span: $span).padding(14)
            } else {
                Text("No agent has reported its plan or saved a conversation yet.").font(.ui(size: 12)).foregroundStyle(Theme.muted).padding(14)
            }
        }
        .frame(width: 500)
        .onAppear { store.refreshAccountUsage() }
    }

    func tabButton(_ a: UsageAccount, selected: Bool, inView: Bool, line: Int) -> some View {
        let near = (a.limits?.allWindows.map(\.usedPct).max()).map { usageLevel($0, line: line) != .room } ?? false
        return Button { tab = a.id } label: {
            HStack(spacing: 5) {
                Mark(agent: a.agent).frame(width: 14, height: 14)
                Text(a.tabLabel).lineLimit(1).truncationMode(.tail)
                if near { Image(systemName: "exclamationmark.triangle.fill").font(.system(size: 9)).foregroundStyle(Theme.warn) }
                if inView { Circle().fill(Theme.accent).frame(width: 6, height: 6).help("The agent in view runs on it") }
            }
            .font(.ui(size: 12, weight: selected ? .semibold : .regular))
            .foregroundStyle(selected ? Theme.text : Theme.text2)
            .padding(.horizontal, 8).padding(.vertical, 6)
            .overlay(alignment: .bottom) { Rectangle().fill(selected ? Theme.accent : .clear).frame(height: 2) }
        }
        .buttonStyle(.plain)
    }
}

/// The panel maximized: every account's room and tokens compared on the
/// left, the chosen one in full on the right.
struct UsageWindow: View {
    @Environment(\.conchTheme) private var look
    @Environment(\.dismiss) private var dismiss
    @ObservedObject var store: Store
    @State private var chosen: String?
    @State private var span: UsageSpan = .week

    init(store: Store, start: String? = nil) {
        self.store = store
        _chosen = State(initialValue: start)
    }

    var body: some View {
        let _ = look
        let accounts = store.usageAccountsHere
        let current = accounts.first { $0.id == chosen } ?? accounts.first
        VStack(spacing: 0) {
            HStack {
                Text("Usage · This Mac").font(.ui(size: 14, weight: .semibold))
                Spacer()
                SpanPicker(span: $span)
                Button("Done") { dismiss() }.keyboardShortcut(.cancelAction)
            }
            .padding(14)
            Divider()
            HStack(alignment: .top, spacing: 0) {
                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        compare("Room left · fullest window", accounts) { a in
                            let line = failoverLine(store.agentAccounts["local"], agent: a.agent)
                            let f = a.limits?.allWindows.map(\.usedPct).max()
                            return (f.map { "\(Int($0.rounded()))%" } ?? "—",
                                    AnyView(LevelBar(pct: f ?? 0, line: line, color: f.map { levelColor(usageLevel($0, line: line)) } ?? Theme.line2, width: 170, height: 9)))
                        }
                        let most = max(accounts.map { spanTotal($0.usage, span)?.tokens.total ?? 0 }.max() ?? 0, 1)
                        compare("Tokens \(span.phrase)", accounts) { a in
                            let n = spanTotal(a.usage, span)?.tokens.total ?? 0
                            return (humanCount(n), AnyView(LevelBar(pct: Double(n) / Double(most) * 100, line: 0, color: Theme.accent.opacity(0.7), width: 170, height: 9)))
                        }
                    }
                    .padding(16)
                }
                .frame(width: 340)
                Divider()
                ScrollView {
                    if let a = current {
                        VStack(alignment: .leading, spacing: 10) {
                            HStack(spacing: 6) {
                                Mark(agent: a.agent)
                                Text("\(store.agentTitle(a.agent)) · \(a.isOwn ? "default" : a.name)").font(.ui(size: 13, weight: .semibold))
                            }
                            AccountUsageDetail(store: store, account: a, span: $span)
                        }
                        .padding(16)
                    }
                }
            }
        }
        .frame(minWidth: 860, minHeight: 600)
        .onAppear { store.refreshAccountUsage() }
    }

    func compare(_ title: String, _ accounts: [UsageAccount], row: @escaping (UsageAccount) -> (String, AnyView)) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title.uppercased()).font(.ui(size: 10.5, weight: .semibold)).tracking(0.6).foregroundStyle(Theme.muted)
            ForEach(accounts) { a in
                let (value, bar) = row(a)
                Button { chosen = a.id } label: {
                    HStack(spacing: 8) {
                        Mark(agent: a.agent).frame(width: 14)
                        Text(a.tabLabel).lineLimit(1).frame(width: 90, alignment: .leading)
                        bar
                        Text(value).monospacedDigit().frame(width: 44, alignment: .trailing)
                    }
                    .font(.ui(size: 12))
                    .padding(.horizontal, 6).padding(.vertical, 4)
                    .background(RoundedRectangle(cornerRadius: 6).fill(a.id == (chosen ?? accounts.first?.id) ? Theme.raise2 : .clear))
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            }
        }
    }
}

extension Store {
    /// Whether this computer's server says what each account used.
    var keepsAccountUsage: Bool { conns["local"]?.hello?.capabilities?.contains(usageAccountsCapability) == true }
}
