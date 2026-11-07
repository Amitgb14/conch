import AppKit
import ConchKit
import SwiftUI

// A branch's git tab: Overview, Changes and Graph on one control, as the
// git mock has them (https://claude.ai/artifact/JubHtT6BUhXq62TAFuKCZa).
// The model is ChangesModel (ChangesView.swift); the graph is the
// project's commit graph (CommitsView.swift), inside the tab.

struct ChangesView: View {
    @ObservedObject var store: Store
    @ObservedObject var model: ChangesModel

    var info: BranchInfo? {
        store.snapshots[model.machine]?.project(model.project)?.branches?.first { $0.name == model.branch }
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Rectangle().fill(Theme.line).frame(height: 1)
            if model.handed != nil { HandoffStrip(store: store, model: model) }
            if let e = model.error {
                HStack {
                    Text(e).font(.ui(size: 12)).foregroundStyle(Theme.err).lineLimit(2)
                    Spacer()
                    Button { model.error = nil } label: { LucideIcon("x", size: 11).padding(4).contentShape(Rectangle()) }.buttonStyle(.plain)
                }
                .padding(.horizontal, 12).padding(.vertical, 6).background(Theme.err.opacity(0.1))
            }
            switch model.mode {
            case .overview: GitOverview(store: store, model: model, info: info)
            case .changes: GitChanges(store: store, model: model, info: info)
            case .graph:
                CommitsView(model: store.commits(for: commitsKey(model.machine, model.project)), store: store, embedded: true)
            }
        }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: 9))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(store.layout.focused == model.key ? Theme.accent : Theme.line))
    }

    var header: some View {
        HStack(spacing: 10) {
            LucideIcon(name: .gitBranch, color: Theme.text2, size: 12)
            Text(model.branch).font(.ui(size: 13, weight: .semibold, design: .monospaced)).foregroundStyle(Theme.text).lineLimit(1)
            if let ch = model.changes, ch.base != ch.branch {
                Chip(text: "into \(ch.base)" + ((info?.baseAhead ?? 0) > 0 ? " · ↑\(info!.baseAhead!)" : ""))
            } else if model.changes != nil {
                Chip(text: "base of the project")
            }
            if let pr = info?.pr {
                let conflicting = (pr.state ?? "OPEN") == "OPEN" && pr.conflicting == true
                Chip(text: "#\(pr.number)" + (conflicting ? " conflicts" : ""), tone: conflicting ? Theme.err : nil)
                    .help(conflicting ? "#\(pr.number) conflicts with its base — Overview has what to do" : "Pull request #\(pr.number)")
                CIChip(pr: pr)
            }
            if let ch = model.changes, !ch.files.isEmpty {
                let (a, d) = ch.totals
                Chip(text: "+\(a) −\(d) uncommitted", tone: Theme.warn)
            }
            if let a = model.agent?.agent {
                HStack(spacing: 4) { Mark(agent: a.name); Text(a.state == .working ? "working" : a.state.rawValue).font(.ui(size: 11.5)) }
                    .foregroundStyle(Theme.text2)
            }
            Spacer(minLength: 8)
            if let b = model.busy { Text(b + "…").font(.ui(size: 12)).foregroundStyle(Theme.muted).lineLimit(1) }
            Picker("", selection: Binding(get: { model.mode }, set: { model.chosen = $0; if $0 == .overview { Task { await model.loadRecent() } } })) {
                ForEach(ChangesModel.Mode.allCases, id: \.self) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented).labelsHidden().frame(width: 250)
            Menu {
                Button("Agent or terminal") { store.openBranch(project: key(model.machine, model.project), name: model.branch) }
                Button("Push") { model.push() }
                if model.changes?.canMerge == true {
                    Button("Merge into \(model.changes?.base ?? "base")…") { confirmMerge() }
                }
                Button(info?.pr == nil ? "Open pull request" : "Update pull request") { model.openPR() }
                if let ch = model.changes, ch.branch != ch.base {
                    Divider()
                    Button("Delete branch…") { store.deleteBranch(project: key(model.machine, model.project), branch: model.branch) }
                }
            } label: { Image(systemName: "ellipsis.circle") }
            .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize().disabled(model.busy != nil)
            .help("Agent or terminal, push, merge, pull request, delete")
        }
        .controlSize(.small)
        .padding(.horizontal, 12).frame(height: 40)
        .background(Theme.paneHeader)
    }

    /// Asks how to merge, as the TUI's merge does: squashed by default.
    func confirmMerge() {
        guard let ch = model.changes else { return }
        let n = ch.commits?.count ?? 0
        let a = NSAlert()
        a.messageText = "Merge \(ch.branch) into \(ch.base)?"
        a.informativeText = "\(n) commit\(n == 1 ? "" : "s"), merged where \(ch.base) is checked out. A merge that conflicts is undone and nothing changes."
            + (info?.pr != nil ? " Its pull request stays open: merging it on GitHub is the other way." : "")
        a.addButton(withTitle: "Squash and merge")
        a.addButton(withTitle: "Merge commit")
        a.addButton(withTitle: "Cancel")
        switch a.runModal() {
        case .alertFirstButtonReturn: model.merge(squash: true)
        case .alertSecondButtonReturn: model.merge(squash: false)
        default: break
        }
    }
}

/// Work handed to an agent from this tab, said under the header in every
/// view — Overview, Changes and the graph — as it goes: not begun, at it
/// (with the TUI's spinner), asking you something, done, or gone.
struct HandoffStrip: View {
    @ObservedObject var store: Store
    @ObservedObject var model: ChangesModel

    var body: some View {
        if let h = model.handed, let state = model.handedState {
            let p = store.snapshots[model.machine]?.panes.first { $0.id == model.handedPane }
            let who = p.map { $0.name.isEmpty ? ($0.agent?.name ?? "the agent") : $0.name } ?? "the agent"
            let tone: Color = state == .working || state == .starting ? Theme.work : state == .waiting ? Theme.warn : state == .finished ? Theme.ok : Theme.muted
            HStack(spacing: 10) {
                switch state {
                case .working, .starting:
                    TimelineView(.periodic(from: .now, by: spinnerInterval)) { ctx in
                        Text(spinnerFrame(at: ctx.date)).font(.ui(size: 13, weight: .semibold)).foregroundStyle(tone)
                    }
                case .waiting: Image(systemName: "exclamationmark.bubble.fill").foregroundStyle(tone)
                case .finished: Image(systemName: "checkmark.circle.fill").foregroundStyle(tone)
                case .gone: Image(systemName: "xmark.circle").foregroundStyle(tone)
                }
                Text(line(state, who: who, what: h.what)).font(.ui(size: 12.5, weight: .medium)).lineLimit(1)
                Text(agoText(h.at)).font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                Spacer(minLength: 8)
                if let id = p?.id, state != .gone {
                    Button(state == .waiting ? "Answer \(who)" : "Open \(who)") { store.open(key(model.machine, id)) }
                        .controlSize(.small).buttonStyle(.borderedProminent).tint(state == .waiting ? Theme.warn : Theme.accentDeep)
                }
                if state == .finished || state == .gone {
                    Button("Dismiss") { model.handed = nil }.controlSize(.small)
                }
            }
            .padding(.horizontal, 12).frame(height: 34)
            .background(tone.opacity(0.10))
            .overlay(alignment: .bottom) { Rectangle().fill(tone.opacity(0.35)).frame(height: 1) }
        }
    }

    func line(_ s: HandoffState, who: String, what: String) -> String {
        switch s {
        case .starting: return "\(who) has it — \(what), not begun yet"
        case .working: return "\(who) is \(what)"
        case .waiting: return "\(who) is waiting for you while \(what)"
        case .finished: return "\(who) finished \(what) — check the result"
        case .gone: return "\(who) is gone; it was \(what)"
        }
    }
}

/// A pull request's CI in a few words, with the spinner while it runs:
/// in the header, so it shows in Overview, Changes and the graph alike,
/// and on the graph's commit the checks run on.
struct CIChip: View {
    @Environment(\.conchTheme) private var look
    let pr: PRInfo
    var number = false

    var body: some View {
        let _ = look
        if let (text, tone) = ciChip(pr) {
            let color: Color = tone == .busy ? Theme.work : tone == .bad ? Theme.err : Theme.ok
            HStack(spacing: 4) {
                if tone == .busy {
                    TimelineView(.periodic(from: .now, by: spinnerInterval)) { ctx in
                        Text(spinnerFrame(at: ctx.date)).font(.ui(size: 11, weight: .semibold))
                    }
                } else {
                    Image(systemName: tone == .bad ? "xmark.circle.fill" : "checkmark.circle.fill").font(.ui(size: 10))
                }
                Text((number ? "#\(pr.number) · " : "") + text).font(.ui(size: 11, weight: .medium, design: .monospaced)).lineLimit(1)
            }
            .foregroundStyle(color)
            .padding(.horizontal, 7).padding(.vertical, 2)
            .background(RoundedRectangle(cornerRadius: 5).fill(color.opacity(0.10)))
            .overlay(RoundedRectangle(cornerRadius: 5).stroke(color.opacity(0.45)))
            .fixedSize()
            .help(checksHelp(pr))
        }
    }
}

/// Each check on its own line, for a tooltip.
func checksHelp(_ pr: PRInfo) -> String {
    guard let runs = pr.runs, !runs.isEmpty else { return "Checks on #\(pr.number)" }
    return checksToRead(runs).map { ($0.state == "pass" ? "✓ " : $0.state == "fail" ? "✗ " : "… ") + $0.name }.joined(separator: "\n")
}

/// A pull request's checks one by one — failing first, then running, then
/// passed — each opening its run on GitHub.
struct ChecksList: View {
    @Environment(\.conchTheme) private var look
    let runs: [PRCheck]
    var limit = 8

    var body: some View {
        let _ = look
        let ordered = checksToRead(runs)
        VStack(alignment: .leading, spacing: 4) {
            // By place, not name: two workflows can each have a job called build.
            ForEach(Array(ordered.prefix(limit).enumerated()), id: \.offset) { _, r in
                HStack(spacing: 7) {
                    Group {
                        switch r.state {
                        case "pass": Image(systemName: "checkmark.circle.fill").foregroundStyle(Theme.ok)
                        case "fail": Image(systemName: "xmark.circle.fill").foregroundStyle(Theme.err)
                        default:
                            TimelineView(.periodic(from: .now, by: spinnerInterval)) { ctx in
                                Text(spinnerFrame(at: ctx.date)).font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.work)
                            }
                        }
                    }
                    .font(.ui(size: 11)).frame(width: 14)
                    // The icon says the state, so the name has the room.
                    Text(r.name).font(.ui(size: 12, design: .monospaced)).foregroundStyle(r.state == "pass" ? Theme.text2 : Theme.text)
                        .lineLimit(1).truncationMode(.middle)
                        .help(r.name + (r.state == "pass" ? " — passed" : r.state == "fail" ? " — failed" : " — running"))
                        .accessibilityLabel(r.name + (r.state == "pass" ? ", passed" : r.state == "fail" ? ", failed" : ", running"))
                    Spacer(minLength: 4)
                    if let u = r.url.flatMap(URL.init(string:)) {
                        Button { NSWorkspace.shared.open(u) } label: {
                            Image(systemName: "arrow.up.right.square").font(.ui(size: 11)).foregroundStyle(Theme.muted)
                                .padding(3).contentShape(Rectangle())
                        }
                        .buttonStyle(.plain).help("Open this check on GitHub")
                    }
                }
            }
            if ordered.count > limit {
                Text("and \(ordered.count - limit) more").font(.ui(size: 11)).foregroundStyle(Theme.muted)
            }
        }
    }
}

/// A small outlined label in the header.
struct Chip: View {
    @Environment(\.conchTheme) private var look
    let text: String
    var tone: Color? = nil

    var body: some View {
        let _ = look
        Text(text).font(.ui(size: 11, weight: .medium, design: .monospaced)).lineLimit(1)
            .foregroundStyle(tone ?? Theme.text2)
            .padding(.horizontal, 7).padding(.vertical, 2)
            .background(RoundedRectangle(cornerRadius: 5).fill(Theme.raise))
            .overlay(RoundedRectangle(cornerRadius: 5).stroke((tone ?? Theme.line).opacity(tone == nil ? 1 : 0.45)))
            .fixedSize()
    }
}

/// A card: a raised panel with a heading.
struct GitCard<Content: View>: View {
    @Environment(\.conchTheme) private var look
    let title: String
    var trailing: AnyView? = nil
    @ViewBuilder let content: Content

    var body: some View {
        let _ = look
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text(title.uppercased()).font(.ui(size: 11, weight: .semibold)).tracking(0.4).foregroundStyle(Theme.muted)
                Spacer()
                if let trailing { trailing }
            }
            content
        }
        .padding(14)
        .background(RoundedRectangle(cornerRadius: 11).fill(Theme.raise.opacity(0.55)))
        .overlay(RoundedRectangle(cornerRadius: 11).stroke(Theme.line))
    }
}

// MARK: - overview

struct GitOverview: View {
    @ObservedObject var store: Store
    @ObservedObject var model: ChangesModel
    let info: BranchInfo?
    @State private var day: Int?

    var body: some View {
        let now = Date()
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                banner
                if let pr = info?.pr { pullRequest(pr) }
                HStack(alignment: .top, spacing: 14) {
                    VStack(alignment: .leading, spacing: 14) {
                        GitCard(title: "Recent commits", trailing: AnyView(Button("All in graph →") { model.chosen = .graph }.controlSize(.small))) {
                            if model.recent.isEmpty { Text("No commits in the last 30 days.").font(.ui(size: 12)).foregroundStyle(Theme.muted) }
                            ForEach(model.recent.prefix(8)) { c in
                                HStack(spacing: 10) {
                                    Text(c.short).font(.ui(size: 12, design: .monospaced)).foregroundStyle(Theme.work)
                                    Text(c.subject).font(.ui(size: 12.5)).lineLimit(1).truncationMode(.tail)
                                    Spacer(minLength: 6)
                                    Text(c.author ?? "").font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1)
                                    if let t = c.time { Text(agoText(t)).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).frame(minWidth: 44, alignment: .trailing) }
                                }
                                .padding(.vertical, 3).contentShape(Rectangle())
                                .onTapGesture { openInGraph(c.hash) }
                                .help("Open \(c.short) in the graph")
                                .accessibilityElement(children: .combine)
                                .accessibilityAddTraits(.isButton)
                                .accessibilityAction { openInGraph(c.hash) }
                            }
                        }
                        let who = authorsSince(model.recent, now: now, days: 7)
                        if !who.isEmpty {
                            GitCard(title: "Who changed it this week") {
                                ForEach(who.prefix(5), id: \.author) { w in
                                    HStack(spacing: 10) {
                                        Text(w.author).font(.ui(size: 12.5)).frame(width: 140, alignment: .leading).lineLimit(1)
                                        Meter(fraction: w.share)
                                        Text("\(w.count) · \(Int((w.share * 100).rounded()))%").font(.ui(size: 11.5, design: .monospaced))
                                            .foregroundStyle(Theme.muted).lineLimit(1).fixedSize().frame(minWidth: 70, alignment: .trailing)
                                    }
                                }
                            }
                        }
                    }
                    .frame(maxWidth: .infinity)
                    VStack(alignment: .leading, spacing: 14) {
                        sync
                        activity(now)
                        branches
                    }
                    .frame(width: 360)
                }
            }
            .padding(16)
        }
        .task { if model.recent.isEmpty { await model.loadRecent() } }
    }

    func openInGraph(_ hash: String) {
        let g = store.commits(for: commitsKey(model.machine, model.project))
        g.pick(hash, shift: false)
        g.reveal += 1
        model.chosen = .graph
    }

    var banner: some View {
        let files = model.changes?.files.count ?? 0
        return HStack(spacing: 16) {
            ZStack {
                Circle().fill((files == 0 ? Theme.ok : Theme.warn).opacity(0.16))
                Circle().stroke((files == 0 ? Theme.ok : Theme.warn).opacity(0.45))
                Image(systemName: files == 0 ? "checkmark" : "pencil").font(.ui(size: 20, weight: .semibold))
                    .foregroundStyle(files == 0 ? Theme.ok : Theme.warn)
            }
            .frame(width: 52, height: 52)
            VStack(alignment: .leading, spacing: 4) {
                Text(files == 0 ? "\(model.branch) is clean" : "\(files) uncommitted file\(files == 1 ? "" : "s") on \(model.branch)")
                    .font(.ui(size: 16, weight: .semibold))
                Text(files == 0 ? cleanLine(branch: model.branch, ahead: info?.ahead ?? 0, behind: info?.behind ?? 0, upstream: info?.upstream)
                     : "Review them hunk by hunk and commit, or hand them back to the agent with notes.")
                    .font(.ui(size: 12.5)).foregroundStyle(Theme.text2)
                if let last = model.recent.first {
                    Text("Last commit \(last.short) \(last.subject)\(last.time.map { " — " + agoText($0) } ?? "")")
                        .font(.ui(size: 12)).foregroundStyle(Theme.muted).lineLimit(1)
                }
            }
            Spacer()
            HStack(spacing: 8) {
                if files > 0 { Button("Review changes") { model.chosen = .changes }.buttonStyle(.borderedProminent) }
                if (info?.ahead ?? 0) > 0 { Button("Push \(info!.ahead!)") { model.push() } }
                Button("Agent or terminal") { store.openBranch(project: key(model.machine, model.project), name: model.branch) }
                Button("Open graph") { model.chosen = .graph }
            }
            .controlSize(.regular)
        }
        .padding(18)
        .background(RoundedRectangle(cornerRadius: 12).fill(LinearGradient(colors: [(files == 0 ? Theme.ok : Theme.warn).opacity(0.08), Theme.raise.opacity(0.4)],
                                                                           startPoint: .leading, endPoint: .trailing)))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(Theme.line))
    }

    /// The pull request: whether it merges, its checks as they run, the
    /// review — and what to do about a conflict or a failure.
    func pullRequest(_ pr: PRInfo) -> some View {
        let base = model.changes?.base ?? "the base"
        let st = prStatus(pr, base: base, conflicts: model.baseConflicts, baseUnpushed: model.baseUnpushed)
        let who = model.agent.map { $0.name.isEmpty ? ($0.agent?.name ?? "the agent") : $0.name }
        return GitCard(title: "Pull request #\(pr.number)", trailing: pr.url.flatMap(URL.init(string:)).map { u in
            AnyView(Button("Open on GitHub ↗") { NSWorkspace.shared.open(u) }.controlSize(.small))
        }) {
            if let t = pr.title, !t.isEmpty {
                Text(t).font(.ui(size: 13, weight: .semibold)).lineLimit(1)
            }
            HStack(alignment: .top, spacing: 18) {
                VStack(alignment: .leading, spacing: 8) {
                    prLine(st.merge, icon: st.conflicting ? "exclamationmark.triangle.fill" : st.merge.tone == .ok ? "checkmark.circle.fill" : "circle")
                    if let l = st.local {
                        prLine(l, icon: "exclamationmark.circle")
                            .help("GitHub merges with the \(base) it has, so the pull request is clean; pushing your local \(base) will make it conflict. Merge \(base) into this branch after pushing it, or push and ask the agent then.")
                    }
                    VStack(alignment: .leading, spacing: 4) {
                        prLine(st.ci, icon: st.ci.tone == .busy ? "clock" : st.ci.tone == .bad ? "xmark.circle.fill" : st.ci.tone == .ok ? "checkmark.circle.fill" : "circle")
                        if let f = st.progress, st.ci.tone == .busy || st.ci.tone == .bad {
                            ProgressView(value: f).progressViewStyle(.linear).tint(st.ci.tone == .bad ? Theme.err : Theme.work)
                                .frame(maxWidth: 260).padding(.leading, 22)
                        }
                        if let runs = pr.runs, !runs.isEmpty {
                            ChecksList(runs: runs, limit: 6).frame(maxWidth: 420).padding(.leading, 22).padding(.top, 2)
                        }
                    }
                    if let r = st.review { prLine(r, icon: r.tone == .ok ? "person.crop.circle.badge.checkmark" : "person.crop.circle") }
                }
                Spacer(minLength: 8)
                VStack(alignment: .trailing, spacing: 6) {
                    if st.conflicting {
                        Button(who.map { "Ask \($0) to resolve" } ?? "Start an agent to resolve") { model.resolveConflicts() }
                            .buttonStyle(.borderedProminent).tint(Theme.err).controlSize(.small)
                            .disabled(model.busy != nil)
                            .help("Merge \(base) in, resolve keeping both sides, test, commit and push — handed to the branch's agent")
                    }
                    if st.failing {
                        Button(who.map { "Send the failure to \($0)" } ?? "Start an agent on the failure") { model.fixCI() }
                            .controlSize(.small)
                            .help("The failed run's log, sent to the branch's agent, as the review queue's Fix")
                    }
                }
            }
        }
        .overlay(RoundedRectangle(cornerRadius: 11).stroke(st.conflicting || st.failing ? Theme.err.opacity(0.5) : .clear))
    }

    func prLine(_ l: PRStatus.Line, icon: String) -> some View {
        let tone: Color = l.tone == .ok ? Theme.ok : l.tone == .bad ? Theme.err : l.tone == .busy ? Theme.work : l.tone == .warn ? Theme.warn : Theme.muted
        return HStack(spacing: 8) {
            Image(systemName: icon).font(.ui(size: 12)).foregroundStyle(tone).frame(width: 14)
            Text(l.text).font(.ui(size: 12.5)).foregroundStyle(l.tone == .quiet ? Theme.text2 : Theme.text).lineLimit(2)
        }
    }

    var sync: some View {
        GitCard(title: "Sync") {
            if let up = info?.upstream, !up.isEmpty {
                row("Behind \(up)", "↓\(info?.behind ?? 0)", (info?.behind ?? 0) > 0 ? Theme.warn : Theme.text2)
                row("Ahead of \(up)", "↑\(info?.ahead ?? 0)", Theme.text2)
            } else {
                Text("No upstream: push it to make one.").font(.ui(size: 12)).foregroundStyle(Theme.muted)
            }
            if let ch = model.changes, ch.base != ch.branch {
                row("Against \(ch.base)", "↑\(info?.baseAhead ?? 0) ↓\(info?.baseBehind ?? 0)", Theme.text2)
            }
        }
    }

    func row(_ l: String, _ v: String, _ c: Color) -> some View {
        HStack {
            Text(l).font(.ui(size: 12.5)).foregroundStyle(Theme.text2).lineLimit(1)
            Spacer()
            Text(v).font(.ui(size: 12.5, weight: .semibold, design: .monospaced)).foregroundStyle(c)
        }
    }

    func activity(_ now: Date) -> some View {
        let days = commitsByDay(model.recent, now: now, days: 14)
        let top = max(days.max() ?? 1, 1)
        let total = days.reduce(0, +)
        return GitCard(title: "Last 14 days", trailing: AnyView(Text(day.map { "\(days[$0]) commit\(days[$0] == 1 ? "" : "s") that day" } ?? "\(total) commits")
            .font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.muted))) {
            HStack(alignment: .bottom, spacing: 4) {
                ForEach(Array(days.enumerated()), id: \.offset) { i, n in
                    RoundedRectangle(cornerRadius: 3)
                        .fill(Theme.accent.opacity(day == i ? 0.95 : 0.6))
                        .frame(height: max(3, CGFloat(n) / CGFloat(top) * 60))
                        .frame(maxWidth: .infinity)
                        .contentShape(Rectangle())
                        .onTapGesture { day = day == i ? nil : i }
                        .help("\(n) commit\(n == 1 ? "" : "s")")
                }
            }
            .frame(height: 64, alignment: .bottom)
            HStack {
                Text("2 weeks ago"); Spacer(); Text("today")
            }
            .font(.ui(size: 10.5, design: .monospaced)).foregroundStyle(Theme.muted)
        }
    }

    var branches: some View {
        let all = (store.snapshots[model.machine]?.project(model.project)?.branches ?? []).filter { $0.name != model.branch }
        let base = model.changes?.base ?? "the base"
        return GitCard(title: "Branches against \(base)") {
            if all.isEmpty { Text("No other branches.").font(.ui(size: 12)).foregroundStyle(Theme.muted) }
            ForEach(all.prefix(8), id: \.name) { b in
                HStack(spacing: 8) {
                    LucideIcon(name: .gitBranch, color: Theme.muted, size: 11)
                    Text(b.name).font(.ui(size: 12, design: .monospaced)).lineLimit(1).truncationMode(.middle)
                    Spacer(minLength: 4)
                    AheadBehind(ahead: b.baseAhead ?? 0, behind: b.baseBehind ?? 0)
                    if let pr = b.pr {
                        Text("#\(pr.number)\(pr.checks == "pass" ? " ✓" : pr.checks == "fail" ? " ✗" : "")").font(.ui(size: 11, design: .monospaced))
                            .foregroundStyle(pr.checks == "fail" ? Theme.err : Theme.ok)
                    }
                }
                .contentShape(Rectangle())
                .onTapGesture { store.openChanges(project: key(model.machine, model.project), branch: b.name) }
                .help("Open \(b.name)")
                .accessibilityElement(children: .combine)
                .accessibilityAddTraits(.isButton)
                .accessibilityAction { store.openChanges(project: key(model.machine, model.project), branch: b.name) }
            }
        }
    }
}

/// Behind and ahead as a two-sided bar: behind grey to the left, ahead
/// green to the right.
struct AheadBehind: View {
    @Environment(\.conchTheme) private var look
    let ahead: Int
    let behind: Int

    var body: some View {
        let _ = look
        let scale: (Int) -> CGFloat = { min(CGFloat($0) * 6, 40) }
        HStack(spacing: 2) {
            HStack { Spacer(minLength: 0); RoundedRectangle(cornerRadius: 2).fill(Theme.muted.opacity(0.7)).frame(width: scale(behind), height: 6) }.frame(width: 40)
            RoundedRectangle(cornerRadius: 2).fill(Theme.ok).frame(width: scale(ahead), height: 6).frame(width: 40, alignment: .leading)
        }
        .help("\(behind) behind, \(ahead) ahead")
    }
}

// MARK: - changes

struct GitChanges: View {
    @ObservedObject var store: Store
    @ObservedObject var model: ChangesModel
    let info: BranchInfo?

    var body: some View {
        HStack(spacing: 0) {
            GitFileList(model: model).frame(width: 260)
            Rectangle().fill(Theme.line).frame(width: 1)
            GitDiff(model: model)
            Rectangle().fill(Theme.line).frame(width: 1)
            GitSide(store: store, model: model, info: info).frame(width: 280)
        }
    }
}

/// A tick box with three states: whole, some, none.
struct TickBox: View {
    @Environment(\.conchTheme) private var look
    let state: Tick
    let action: () -> Void

    var body: some View {
        let _ = look
        Button(action: action) {
            ZStack {
                RoundedRectangle(cornerRadius: 3.5).fill(state == .on ? Theme.accent : .clear)
                RoundedRectangle(cornerRadius: 3.5).stroke(state == .off ? Theme.muted : Theme.accent, lineWidth: 1.4)
                if state == .on { Image(systemName: "checkmark").font(.ui(size: 8, weight: .heavy)).foregroundStyle(Theme.window) }
                if state == .mixed { RoundedRectangle(cornerRadius: 1).fill(Theme.accent).frame(width: 7, height: 2) }
            }
            .frame(width: 13, height: 13)
            // A plain button takes clicks only where it draws, and an empty
            // box draws nothing but its 1.4-point outline: a click inside it
            // fell through to the row and opened the file instead. The whole
            // square, and a margin round it, is the box.
            .padding(4)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .padding(-4)
    }
}

struct GitFileList: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: ChangesModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 1) {
                if let ch = model.changes {
                    if ch.worktree != nil {
                        HStack(spacing: 7) {
                            // Every file at once, or none; a dash while some are ticked.
                            let paths = ch.files.map(\.path)
                            if !paths.isEmpty {
                                TickBox(state: model.picks.allState(paths, hunks: model.hunkOrder)) {
                                    model.picks.set(paths, on: model.picks.allState(paths, hunks: model.hunkOrder) != .on)
                                    model.tickAnchor = nil
                                }
                                .help("Tick every file, or none — then Stage or Commit. Shift-click a file's box to tick a run of them.")
                                .accessibilityLabel("Tick every uncommitted file")
                            }
                            heading("Uncommitted · \(ch.files.count)")
                            Spacer()
                            if !model.picks.isEmpty { Chip(text: model.commitWhat) }
                        }
                        .padding(.horizontal, 10).padding(.top, 8).padding(.bottom, 4)
                        if ch.files.isEmpty { muted("Nothing uncommitted") }
                        ForEach(ch.files) { f in row(f, committed: false) }
                        if let c = ch.committedFiles, !c.isEmpty {
                            heading("Committed on \(ch.branch) · \(c.count)").padding(.horizontal, 10).padding(.top, 12).padding(.bottom, 4)
                            ForEach(c) { f in row(f, committed: true) }
                        }
                    } else {
                        heading("Changes against \(ch.base)").padding(.horizontal, 10).padding(.top, 8).padding(.bottom, 4)
                        if ch.files.isEmpty { muted("Nothing changed against \(ch.base)") }
                        ForEach(ch.files) { f in row(f, committed: true) }
                    }
                    if let commits = ch.commits, !commits.isEmpty {
                        heading("Commits · \(commits.count)").padding(.horizontal, 10).padding(.top, 12).padding(.bottom, 4)
                        ForEach(commits.prefix(20)) { c in
                            HStack(spacing: 8) {
                                Text(String(c.hash.prefix(7))).font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.work)
                                Text(c.subject).font(.ui(size: 12)).foregroundStyle(Theme.text2).lineLimit(1)
                            }
                            .padding(.horizontal, 10).frame(height: 22)
                        }
                    }
                } else if model.error == nil {
                    muted("Reading…")
                }
            }
            .padding(.vertical, 6)
        }
        .background(Theme.side)
    }

    func heading(_ t: String) -> some View {
        Text(t.uppercased()).font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
    }

    func muted(_ t: String) -> some View {
        Text(t).font(.ui(size: 12)).foregroundStyle(Theme.muted).padding(.horizontal, 10)
    }

    func row(_ f: FileChange, committed: Bool) -> some View {
        let on = model.selected == f.path && model.selectedCommitted == committed
        let name = (f.path as NSString).lastPathComponent
        let dir = (f.path as NSString).deletingLastPathComponent
        let notes = openComments(model.comments, file: f.path) + model.pending.filter { $0.file == f.path }.count
        return HStack(spacing: 7) {
            if !committed && model.changes?.worktree != nil {
                TickBox(state: model.picks.state(f.path, of: model.hunkOrder[f.path])) {
                    // Shift ticks the run from the box clicked last, as Finder does.
                    if NSEvent.modifierFlags.contains(.shift) {
                        model.picks.extend(to: f.path, from: model.tickAnchor, in: model.changes?.files.map(\.path) ?? [])
                    } else {
                        model.picks.toggleFile(f.path)
                    }
                    model.tickAnchor = f.path
                }
                .help("Tick to stage or commit this file (shift-click ticks a run); tick hunks in the diff for part of it")
            }
            Text(f.code).font(.ui(size: 11, weight: .bold, design: .monospaced)).foregroundStyle(fileCodeColor(f.code)).frame(width: 12)
            Text(name).font(.ui(size: 12, design: .monospaced)).foregroundStyle(Theme.text).lineLimit(1)
            Text(dir).font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.head)
            Spacer(minLength: 4)
            if notes > 0 {
                Text("\(notes)").font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(Theme.onWarn)
                    .padding(.horizontal, 5).background(Capsule().fill(Theme.warn)).fixedSize().help("\(notes) comment\(notes == 1 ? "" : "s")")
            }
            if model.live.contains(f.path) {
                Circle().fill(Theme.agent(model.agent?.agent?.name)).frame(width: 7, height: 7).help("Being written right now")
            }
            if !committed && f.stage != .off {
                // An icon, not a word: in a column this narrow a word wrapped
                // under the name ("stage / d").
                Image(systemName: f.stage == .on ? "checkmark.circle.fill" : "circle.lefthalf.filled")
                    .font(.ui(size: 11)).foregroundStyle(Theme.ok).fixedSize()
                    .help(f.stage == .on ? "Staged: all of it is in the index (git add)" : "Part staged: some of it is in the index, and some is not")
                    .accessibilityLabel(f.stage == .on ? "staged" : "part staged")
            }
            if f.binary == true {
                Text("binary").font(.ui(size: 11)).foregroundStyle(Theme.muted)
            } else {
                Text("+\(f.added)").font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.ok).fixedSize()
                Text("−\(f.deleted)").font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.err).fixedSize()
            }
        }
        .padding(.horizontal, 10).frame(height: 26)
        .background(RoundedRectangle(cornerRadius: 6).fill(on ? Theme.accentDeep.opacity(0.45) : .clear).padding(.horizontal, 4))
        .contentShape(Rectangle())
        .onTapGesture { model.select(f.path, committed: committed) }
        .help(f.path)
        // A container, not one combined element: its tick box stays a
        // control of its own, which combining hid from VoiceOver.
        .accessibilityElement(children: .contain)
        .accessibilityAddTraits(.isButton)
        .accessibilityLabel(f.path)
        .accessibilityAction { model.select(f.path, committed: committed) }
        .contextMenu {
            if !committed && model.changes?.worktree != nil && model.canStage {
                if f.stage != .on || f.unstaged == true { Button("Stage \(name)") { model.stage(file: f.path, unstage: false) } }
                if f.stage != .off { Button("Unstage \(name)") { model.stage(file: f.path, unstage: true) } }
            }
            Button("Copy Path") { NSPasteboard.general.clearContents(); NSPasteboard.general.setString(f.path, forType: .string) }
        }
    }
}

struct GitDiff: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: ChangesModel

    var body: some View {
        VStack(spacing: 0) {
            if let path = model.selected {
                HStack(spacing: 10) {
                    Text(path).font(.ui(size: 12.5, design: .monospaced)).lineLimit(1).truncationMode(.head)
                    if model.live.contains(path) {
                        Chip(text: "being written · follows live", tone: Theme.agent(model.agent?.agent?.name))
                    }
                    Spacer()
                    Picker("", selection: $model.split) {
                        Text("Unified").tag(false)
                        Text("Split").tag(true)
                    }
                    .pickerStyle(.segmented).labelsHidden().frame(width: 140).controlSize(.small)
                }
                .padding(.horizontal, 12).frame(height: 36)
                .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
            }
            GeometryReader { geo in
                ScrollView([.vertical, .horizontal]) {
                    LazyVStack(alignment: .leading, spacing: 10) {
                        if model.selected == nil {
                            Text(model.changes == nil ? "" : "Nothing to show.").foregroundStyle(Theme.muted).padding(14)
                        } else if model.fileHunks.hunks.isEmpty {
                            // A committed file: its diff, with comments on lines.
                            DiffBlock(model: model, lines: model.diff.filter { $0.kind != .meta }, hunk: nil, width: geo.size.width - 20)
                        } else {
                            ForEach(model.fileHunks.hunks) { h in
                                DiffBlock(model: model, lines: h.lines, hunk: h, width: geo.size.width - 20)
                            }
                        }
                    }
                    .padding(10)
                    .frame(minWidth: geo.size.width, minHeight: geo.size.height, alignment: .topLeading)
                }
            }
        }
        .background(Theme.window)
    }
}

/// One hunk as a card — its box to stage it, its lines, a + on each line
/// for a comment, and the comments under the lines they are on — or a
/// committed file's whole diff the same way, without the box.
struct DiffBlock: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: ChangesModel
    let lines: [DiffLine]
    let hunk: Hunk?
    /// The diff's width: a card is at least that wide, wider for a long line.
    var width: CGFloat = 0

    /// One character of the diff's font: a card is as wide as its longest
    /// line, so a long line scrolls sideways rather than being cut off.
    static let cell = ("0" as NSString).size(withAttributes: [.font: NSFont.monospacedSystemFont(ofSize: 12, weight: .regular)]).width

    /// The card's width: the diff's, or the longest line's with its gutter.
    var full: CGFloat {
        let longest = CGFloat(lines.map(\.text.count).max() ?? 0) * Self.cell
        return model.split ? max(width, 400) : max(width, longest + 140)
    }

    var body: some View {
        let path = model.selected ?? ""
        let w = full
        let staged = hunk.map { model.picks.whole.contains(path) || (model.picks.hunks[path]?.contains($0.text) ?? false) } ?? false
        VStack(alignment: .leading, spacing: 0) {
            if let h = hunk {
                HStack(spacing: 10) {
                    TickBox(state: staged ? .on : .off) {
                        model.picks.toggleHunk(path, header: model.fileHunks.header, hunk: h.text, all: model.fileHunks.hunks)
                    }
                    .help("Commit this hunk")
                    Text(h.header).font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.text2).lineLimit(1)
                    Spacer(minLength: 0)
                }
                .padding(.horizontal, 10).frame(height: 28)
                .frame(width: w, alignment: .leading)
                .background(staged ? Theme.accentDeep.opacity(0.22) : Theme.paneHeader)
                .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
            }
            if model.split {
                ForEach(Array(splitRows(lines).enumerated()), id: \.offset) { _, r in
                    HStack(spacing: 0) {
                        cell(r.left, side: .removed)
                        Rectangle().fill(Theme.line).frame(width: 1)
                        cell(r.right, side: .added)
                    }
                    .frame(width: w, alignment: .leading)
                    .fixedSize(horizontal: false, vertical: true) // both halves as tall as the taller
                    threads(for: r.right ?? r.left)
                }
            } else {
                ForEach(lines) { l in
                    DiffRow(model: model, line: l, width: w)
                    threads(for: l)
                }
            }
        }
        .background(Theme.window)
        .clipShape(RoundedRectangle(cornerRadius: 9))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(staged ? Theme.accent.opacity(0.6) : Theme.line))
    }

    func cell(_ l: DiffLine?, side: DiffLine.Kind) -> some View {
        // Each side is half the card and a long line wraps inside it: side
        // by side is for comparing, which a side scrolled out of view is not.
        HStack(alignment: .top, spacing: 0) {
            Text((side == .removed ? l?.old : l?.new).map(String.init) ?? "").frame(width: 40, alignment: .trailing).foregroundStyle(Theme.muted)
            Text(" " + (l?.text ?? "")).foregroundStyle(l.map { diffColor($0.kind) } ?? Theme.text)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
        }
        .font(.ui(size: 12, design: .monospaced)).padding(.vertical, 1)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(l.map { diffBackground($0.kind) } ?? .clear)
    }

    @ViewBuilder func threads(for l: DiffLine?) -> some View {
        if let l, let n = commentLine(l) {
            let path = model.selected ?? ""
            let here = comments(model.comments, file: path, line: n)
            let pending = model.pending.filter { $0.file == path && $0.line == n }
            // Threads keep to the width on screen, however wide the card.
            Group {
                ForEach(here) { c in CommentThread(model: model, comment: c) }
                ForEach(model.sent.filter { $0.file == path && $0.line == n }) { m in SentRow(model: model, sent: m) }
                ForEach(pending) { p in PendingRow(model: model, pending: p) }
                if model.composingFile == path && model.composingLine == n {
                    CommentComposer(model: model, file: path, line: n)
                }
            }
            .frame(width: width > 0 ? width : nil, alignment: .leading)
        }
    }
}

/// Removed and added lines side by side, paired in order; context on both.
func splitRows(_ lines: [DiffLine]) -> [(left: DiffLine?, right: DiffLine?)] {
    var out: [(DiffLine?, DiffLine?)] = []
    var removed: [DiffLine] = [], added: [DiffLine] = []
    func flush() {
        for i in 0..<max(removed.count, added.count) {
            out.append((i < removed.count ? removed[i] : nil, i < added.count ? added[i] : nil))
        }
        removed = []
        added = []
    }
    for l in lines {
        switch l.kind {
        case .removed: removed.append(l)
        case .added: added.append(l)
        default:
            flush()
            out.append((l, l))
        }
    }
    flush()
    return out
}

@MainActor func diffColor(_ k: DiffLine.Kind) -> Color {
    switch k {
    case .added: return Theme.ok
    case .removed: return Theme.err
    case .hunk: return Theme.work
    case .meta: return Theme.muted
    case .context: return Theme.text
    }
}

@MainActor func diffBackground(_ k: DiffLine.Kind) -> Color {
    switch k {
    case .added: return Theme.ok.opacity(0.10)
    case .removed: return Theme.err.opacity(0.10)
    default: return .clear
    }
}

/// One line of a diff, with a + in its gutter on hover to comment on it.
struct DiffRow: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: ChangesModel
    let line: DiffLine
    var width: CGFloat = 0
    @State private var hover = false

    var body: some View {
        let path = model.selected ?? ""
        let n = commentLine(line)
        let commented = n.map { !comments(model.comments, file: path, line: $0).isEmpty || model.pending.contains { $0.file == path && $0.line == n } } ?? false
        HStack(spacing: 0) {
            ZStack {
                if hover, let n {
                    Button { model.compose(file: path, line: n) } label: {
                        Image(systemName: "plus").font(.ui(size: 9, weight: .bold)).foregroundStyle(.white)
                            .frame(width: 16, height: 16).background(RoundedRectangle(cornerRadius: 4).fill(Theme.accentDeep))
                    }
                    .buttonStyle(.plain).help("Comment on this line")
                }
            }
            .frame(width: 20)
            Rectangle().fill(commented ? Theme.warn : .clear).frame(width: 2)
            Text(line.old.map(String.init) ?? "").frame(width: 40, alignment: .trailing).foregroundStyle(Theme.muted)
            Text(line.new.map(String.init) ?? "").frame(width: 40, alignment: .trailing).foregroundStyle(Theme.muted)
            Text(line.kind == .added ? " + " : line.kind == .removed ? " − " : "   ").foregroundStyle(diffColor(line.kind))
            Text(line.text.isEmpty ? " " : line.text).foregroundStyle(diffColor(line.kind)).fixedSize()
            Spacer(minLength: 0)
        }
        .font(.ui(size: 12, design: .monospaced))
        .padding(.vertical, 1)
        .frame(width: width > 0 ? width : nil, alignment: .leading)
        .background(diffBackground(line.kind))
        .contentShape(Rectangle())
        .onHover { hover = $0 }
    }
}

struct CommentThread: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: ChangesModel
    let comment: ReviewComment
    @State private var replying = false
    @State private var answer = ""
    @FocusState private var typing: Bool

    var state: NoteState { noteState(comment, agentStopped: model.agentStopped(comment.sentPane)) }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            entry(by: comment.by ?? "you", note: comment.note, at: comment.at, chip: chip)
            ForEach(Array((comment.replies ?? []).enumerated()), id: \.offset) { _, r in
                Rectangle().fill(Theme.line).frame(height: 1)
                let agent = !(r.by ?? "").isEmpty
                entry(by: agent ? r.by! : "you", note: r.note, at: r.at, chip: nil)
                    .background(agent && !comment.fromGitHub ? Theme.agent(model.agent?.agent?.name).opacity(0.06) : .clear)
            }
            if case .sent(_, true) = state {
                Rectangle().fill(Theme.line).frame(height: 1)
                Text("The agent finished without answering this one here — its session has what it said.")
                    .font(.ui(size: 11.5)).foregroundStyle(Theme.muted).padding(.horizontal, 10).padding(.vertical, 6)
            }
            if replying {
                Rectangle().fill(Theme.line).frame(height: 1)
                HStack(spacing: 8) {
                    TextField("Answer back — it waits to be sent again with the thread", text: $answer)
                        .textFieldStyle(.roundedBorder).font(.ui(size: 12.5)).focused($typing)
                        .onSubmit { send() }
                    Button("Reply") { send() }.controlSize(.small).buttonStyle(.borderedProminent)
                        .disabled(answer.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    Button("Cancel") { replying = false; answer = "" }.controlSize(.small)
                }
                .padding(8)
                .onAppear { typing = true }
            }
            Rectangle().fill(Theme.line).frame(height: 1)
            HStack(spacing: 8) {
                switch state {
                case .github:
                    if let u = comment.url.flatMap(URL.init(string:)) { Button("Reply on GitHub") { NSWorkspace.shared.open(u) }.controlSize(.small) }
                    if comment.outdated == true { Text("outdated").font(.ui(size: 11)).foregroundStyle(Theme.muted) }
                case .waiting:
                    SendNotesMenu(store: model.store, model: model, title: "Send to an agent")
                case .sent, .replied:
                    if let pane = comment.sentPane, let to = comment.sentTo {
                        Button("Open \(to)") { model.store?.open(key(model.machine, pane)) }.controlSize(.small).lineLimit(1)
                    }
                    if model.canThread && !replying { Button("Reply") { replying = true }.controlSize(.small) }
                }
                Spacer()
                if !comment.fromGitHub { Button("Resolve") { model.resolve(comment) }.controlSize(.small) }
            }
            .padding(.horizontal, 10).padding(.vertical, 6).background(Theme.side)
        }
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(border))
        .padding(.leading, 60).padding(.trailing, 10).padding(.vertical, 5)
        .frame(maxWidth: 720, alignment: .leading)
    }

    func send() {
        model.reply(comment, answer)
        answer = ""
        replying = false
    }

    var border: Color {
        if case .replied = state { return Theme.ok.opacity(0.45) }
        return Theme.line2
    }

    /// What the note's state is, said in its chip.
    var chip: (String, Color)? {
        switch state {
        case .github: return ("on the pull request", Theme.text2)
        case .waiting: return ((comment.replies ?? []).isEmpty ? "note for the agent" : "answered back · waiting to be sent", Theme.agent(model.agent?.agent?.name))
        case .sent(let to, let stopped): return (stopped ? "sent to \(to) · no answer" : "sent to \(to) · working", stopped ? Theme.muted : Theme.work)
        case .replied(let by): return ("answered by \(by)", Theme.ok)
        }
    }

    func entry(by: String, note: String, at: Date?, chip: (String, Color)?) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 8) {
                Text(by).font(.ui(size: 12, weight: .semibold)).lineLimit(1)
                if let (kind, tone) = chip {
                    Text(kind).font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(tone).lineLimit(1)
                        .padding(.horizontal, 6).padding(.vertical, 1).overlay(Capsule().stroke(tone.opacity(0.5)))
                }
                if let at { Text(agoText(at)).font(.ui(size: 11)).foregroundStyle(Theme.muted) }
            }
            Text(note).font(.ui(size: 12.5)).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// A comment waiting in the review, to go with it to the pull request.
struct PendingRow: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: ChangesModel
    let pending: PendingComment

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 8) {
                    Text("you").font(.ui(size: 12, weight: .semibold))
                    Text("pending · in your review").font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(Theme.warn)
                        .padding(.horizontal, 6).padding(.vertical, 1).overlay(Capsule().stroke(Theme.warn.opacity(0.5)))
                }
                Text(pending.note).font(.ui(size: 12.5)).fixedSize(horizontal: false, vertical: true)
            }
            Spacer()
            Button("Remove") { model.dropPending(pending) }.controlSize(.small)
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.warn.opacity(0.4)))
        .padding(.leading, 60).padding(.trailing, 10).padding(.vertical, 5)
        .frame(maxWidth: 720, alignment: .leading)
    }
}

struct CommentComposer: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: ChangesModel
    let file: String
    let line: Int
    @State private var text = ""
    @FocusState private var typing: Bool

    var empty: Bool { text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    // Two actions rather than a mode and a button: a switch beside "Add
    // note" was taken for the send button, and clicking it did nothing.
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            TextEditor(text: $text).font(.ui(size: 12.5)).frame(minHeight: 60, maxHeight: 120).focused($typing)
                .scrollContentBackground(.hidden).padding(6)
                .background(RoundedRectangle(cornerRadius: 6).fill(Theme.window))
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.accent))
                .overlay(alignment: .topLeading) {
                    if text.isEmpty {
                        Text("Comment on line \(line)").font(.ui(size: 12.5)).foregroundStyle(Theme.muted).padding(11).allowsHitTesting(false)
                    }
                }
                // Return adds the note, as a chat box sends; shift-return is a new line.
                .onKeyPress(.return, phases: .down) { press in
                    if press.modifiers.contains(.shift) { return .ignored }
                    if !empty { model.addComment(text, file: file, line: line, forPR: false) }
                    return .handled
                }
            HStack(spacing: 8) {
                Text("↩ note for the agent · ⇧↩ new line").font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(1)
                Spacer(minLength: 6)
                Button("Cancel") { model.composingFile = nil }.controlSize(.small).keyboardShortcut(.cancelAction)
                Button("Add to PR review") { model.addComment(text, file: file, line: line, forPR: true) }
                    .controlSize(.small).disabled(empty || !model.prOpen)
                    .help(model.prOpen ? "Waits in your review until you submit it on the right" : "No pull request is open for this branch")
                Button("Add note for the agent") { model.addComment(text, file: file, line: line, forPR: false) }
                    .buttonStyle(.borderedProminent).controlSize(.small)
                    .keyboardShortcut(.return, modifiers: .command)
                    .disabled(empty)
                    .help("Kept by conch on this line — every client sees it; Send on the right hands the notes to the agent")
            }
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.accent.opacity(0.6)))
        .padding(.leading, 60).padding(.trailing, 10).padding(.vertical, 5)
        .frame(maxWidth: 720, alignment: .leading)
        .onAppear { typing = true }
    }
}

/// Where the notes go: an agent on the branch, picked by name, or a new
/// one — a branch like the base can have a dozen agents, and the server's
/// own choice (the oldest not waiting) is rarely the one meant.
struct SendNotesMenu: View {
    @Environment(\.conchTheme) private var look
    let store: Store?
    @ObservedObject var model: ChangesModel
    var title: String

    var body: some View {
        let agents = model.branchAgents
        let startable = (store?.agents[model.machine] ?? []).filter(\.installed)
        Group {
            if model.canPick {
                Menu(title) {
                    if !agents.isEmpty {
                        Section("On \(model.branch)") {
                            ForEach(agents, id: \.id) { a in
                                Button(label(a)) { model.sendNotes(to: a.id) }
                                    .disabled(a.agent?.state == .blocked)
                            }
                        }
                    }
                    Section("Start one with the notes") {
                        ForEach(startable, id: \.name) { a in
                            Button("New \(a.title)") { model.sendNotes(start: a.name) }
                        }
                    }
                }
                .menuStyle(.borderlessButton).fixedSize()
            } else {
                // An older server chooses the agent itself.
                Button(title) { model.sendNotes() }.disabled(model.agent == nil)
            }
        }
        .controlSize(.small)
        .disabled(model.notes.isEmpty || model.busy != nil)
        .help("Hand the notes to an agent on \(model.branch), or start one with them")
    }

    func label(_ a: PaneInfo) -> String {
        let name = a.name.isEmpty ? (a.agent?.name ?? a.id) : a.name
        let state = a.agent?.state == .blocked ? " — waiting on you" : a.agent?.state == .working ? " — working" : ""
        return name + state
    }
}

/// A note already handed to an agent, kept on its line: to whom, when, and
/// a way to that agent.
struct SentRow: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: ChangesModel
    let sent: SentNote

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 8) {
                    Text("you").font(.ui(size: 12, weight: .semibold))
                    Text("sent to \(sent.to)").font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(Theme.ok).lineLimit(1)
                        .padding(.horizontal, 6).padding(.vertical, 1).overlay(Capsule().stroke(Theme.ok.opacity(0.5)))
                    Text(agoText(sent.at)).font(.ui(size: 11)).foregroundStyle(Theme.muted)
                }
                Text(sent.note).font(.ui(size: 12.5)).foregroundStyle(Theme.text2).textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(10)
            Rectangle().fill(Theme.line).frame(height: 1)
            HStack(spacing: 8) {
                Button("Open \(sent.to)") { model.store?.open(key(model.machine, sent.pane)) }.controlSize(.small).lineLimit(1)
                Spacer()
                Button("Dismiss") { model.sent.removeAll { $0.id == sent.id } }.controlSize(.small)
            }
            .padding(.horizontal, 10).padding(.vertical, 6).background(Theme.side)
        }
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise.opacity(0.6)))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.ok.opacity(0.35)))
        .padding(.leading, 60).padding(.trailing, 10).padding(.vertical, 5)
        .frame(maxWidth: 720, alignment: .leading)
    }
}

// MARK: - the side: commit, review, checks

struct GitSide: View {
    @ObservedObject var store: Store
    @ObservedObject var model: ChangesModel
    let info: BranchInfo?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                if model.changes?.worktree != nil { commit }
                // Checks before the review: what CI says is what a review
                // waits on, and below the review's form it was off screen.
                if let pr = info?.pr { checks(pr) }
                review
            }
            .padding(12)
        }
        .background(Theme.side)
    }

    var commit: some View {
        let files = model.changes?.files.count ?? 0
        return GitCard(title: "Commit") {
            Text(files == 0 ? "Nothing to commit." : "Commits \(model.commitWhat).").font(.ui(size: 12)).foregroundStyle(Theme.text2)
            TextEditor(text: $model.message).font(.ui(size: 12.5, design: .monospaced)).frame(minHeight: 90, maxHeight: 180)
                .scrollContentBackground(.hidden).padding(6)
                .background(RoundedRectangle(cornerRadius: 7).fill(Theme.window))
                .overlay(RoundedRectangle(cornerRadius: 7).stroke(Theme.line2))
            HStack(spacing: 8) {
                Button { model.writeMessage() } label: {
                    HStack(spacing: 5) {
                        if model.writing { ProgressView().controlSize(.mini) } else { Text("✦") }
                        Text("Write")
                    }
                }
                .controlSize(.small).disabled(files == 0 || model.writing)
                .help("Draft the message from what is ticked (or everything), with the brain set in Settings")
                Spacer()
                if model.canStage {
                    Button("Stage") { model.stage() }.controlSize(.small)
                        .disabled(model.picks.isEmpty || model.busy != nil)
                        .help("git add: put what is ticked in the index without committing it")
                }
                Button("Commit") { model.commit() }.buttonStyle(.borderedProminent).controlSize(.small)
                    .disabled(files == 0 || model.message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.busy != nil)
                    .keyboardShortcut(.return, modifiers: .command)
            }
            if let a = model.agent?.agent {
                Toggle(isOn: $model.credit) { Text("Credit \(a.name) (Co-Authored-By)").font(.ui(size: 12)) }.toggleStyle(.checkbox)
            }
        }
    }

    var review: some View {
        let notes = model.notes
        let pr = info?.pr
        return GitCard(title: pr.map { "Review · #\($0.number)" } ?? "Review") {
            HStack(spacing: 8) {
                Text("\(notes.count) note\(notes.count == 1 ? "" : "s") for the agent").font(.ui(size: 12.5))
                Spacer()
                SendNotesMenu(store: store, model: model, title: "Send")
            }
            if let pr, pr.state == "OPEN" {
                Text("\(model.pending.count) comment\(model.pending.count == 1 ? "" : "s") waiting for the pull request").font(.ui(size: 12.5)).foregroundStyle(Theme.text2)
                if let done = model.reviewed {
                    HStack(spacing: 6) {
                        Image(systemName: done == .requestChanges ? "xmark.circle.fill" : "checkmark.circle.fill")
                        Text(done == .approve ? "You approved #\(pr.number)" : done == .requestChanges ? "You requested changes" : "You commented")
                    }
                    .font(.ui(size: 12.5, weight: .semibold)).foregroundStyle(done == .requestChanges ? Theme.err : Theme.ok)
                    Button("Review again") { model.reviewed = nil }.controlSize(.small)
                } else {
                    Picker("", selection: $model.event) {
                        ForEach(ReviewEvent.allCases, id: \.self) { e in
                            VStack(alignment: .leading) {
                                Text(e.title)
                                Text(e.help).font(.ui(size: 11)).foregroundStyle(Theme.muted)
                            }
                            .tag(e)
                        }
                    }
                    .pickerStyle(.radioGroup).labelsHidden()
                    TextEditor(text: $model.reviewBody).font(.ui(size: 12.5)).frame(minHeight: 56, maxHeight: 120)
                        .scrollContentBackground(.hidden).padding(6)
                        .background(RoundedRectangle(cornerRadius: 7).fill(Theme.window))
                        .overlay(RoundedRectangle(cornerRadius: 7).stroke(Theme.line2))
                        .overlay(alignment: .topLeading) {
                            if model.reviewBody.isEmpty { Text("Summary").font(.ui(size: 12.5)).foregroundStyle(Theme.muted).padding(11).allowsHitTesting(false) }
                        }
                    Button("Submit review" + (model.pending.isEmpty ? "" : " with \(model.pending.count) comment\(model.pending.count == 1 ? "" : "s")")) { model.submitReview() }
                        .buttonStyle(.borderedProminent).controlSize(.small)
                        .disabled(!model.event.canSubmit(body: model.reviewBody, comments: model.pending.count) || model.busy != nil)
                }
            } else {
                Text("No pull request open: comments for one wait until there is.").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                Button("Open pull request") { model.openPR() }.controlSize(.small).disabled(model.busy != nil)
            }
        }
    }

    func checks(_ pr: PRInfo) -> some View {
        let st = prStatus(pr, base: model.changes?.base ?? "the base", conflicts: model.baseConflicts, baseUnpushed: model.baseUnpushed)
        return GitCard(title: "Checks") {
            HStack(spacing: 8) {
                Image(systemName: pr.checks == "pass" ? "checkmark.circle.fill" : pr.checks == "fail" ? "xmark.circle.fill" : "clock")
                    .foregroundStyle(pr.checks == "pass" ? Theme.ok : pr.checks == "fail" ? Theme.err : Theme.work)
                Text(st.ci.text).font(.ui(size: 12.5)).lineLimit(1)
                Spacer()
            }
            if let f = st.progress, pr.checks == "pending" || pr.checks == "fail" {
                ProgressView(value: f).progressViewStyle(.linear).tint(pr.checks == "fail" ? Theme.err : Theme.work)
            }
            if let runs = pr.runs, !runs.isEmpty { ChecksList(runs: runs, limit: 10) }
            if st.failing {
                Button(model.agent.map { "Send the failure to \($0.name.isEmpty ? ($0.agent?.name ?? "the agent") : $0.name)" } ?? "Start an agent on the failure") { model.fixCI() }
                    .controlSize(.small)
            }
            if let u = pr.url.flatMap(URL.init(string:)) { Button("Open #\(pr.number) on GitHub") { NSWorkspace.shared.open(u) }.buttonStyle(.link).font(.ui(size: 12)) }
        }
    }
}
