import AppKit
import ConchKit
import SwiftUI

// Move a project to another machine — the TUI's T on a project, and
// `conch project move`: where (a machine), what moves (the plan, read from
// both machines by `conch project move -plan -json`, changing nothing),
// then the move itself, its steps drawn as `conch project move -y -json`
// says them. Closing the sheet doesn't stop it: the command runs on, and
// the status bar says when it is done.

/// The project a Move sheet is for (its machine|id key); cloud opens it
/// on Conch Cloud (Move to Conch Cloud…), pick on the machine chosen for
/// it already (Add project's From this Mac).
struct MoveProjectTarget: Identifiable {
    var id: String
    var cloud = false
    var pick: CloudPick?
}

@MainActor
final class MoveProjectModel: ObservableObject {
    enum Phase: Equatable { case pick, planning, review, moving, done }

    unowned let store: Store
    let project: String // machine|id
    @Published var phase: Phase = .pick
    @Published var dest: String?
    @Published var into = ""
    @Published var plan: MovePlan?
    @Published var steps = 0
    @Published var lines: [Line] = []
    @Published var result: MoveResult?
    @Published var problem = ""
    @Published var showThere = true
    /// Conch Cloud chosen: a cloud machine's id, or "new" for one made for
    /// the move. It is readied — made, or woken, and connected to — before
    /// the plan is read (Store.readyCloud), and then dest is its machine.
    @Published var cloudChoice: String?
    @Published var newName = ""
    @Published var newSize = ""
    @Published var prep: CloudPrep?
    var newNameEdited = false
    var cloudTouched = false
    /// Opened for Conch Cloud: it is chosen once the account's machines
    /// are read.
    let wantsCloud: Bool

    struct Line: Identifiable, Equatable {
        var id: String { kind + ":" + name }
        var kind: String
        var name: String
        var title: String
        var state = "wait" // wait, run, ok, fail
        var detail = ""
    }

    init(store: Store, project: String, cloud: Bool = false, pick: CloudPick? = nil) {
        self.store = store
        self.project = project
        wantsCloud = cloud || pick != nil
        switch pick {
        case .existing(let id)?:
            cloudChoice = id
            cloudTouched = true
        case .new(let n, let s)?:
            cloudChoice = "new"
            cloudTouched = true
            newName = n
            newNameEdited = true
            newSize = s
        case nil: break
        }
    }

    var machine: String { unkey(project).machine }
    var projectID: String { unkey(project).id }
    var info: ProjectInfo? { store.snapshots[machine]?.project(projectID) }

    /// The machines it could go to: every other one, online first. Cloud
    /// machines are Conch Cloud's group.
    var machines: [MachineLink] {
        let others = store.machines.filter { $0.id != machine && !$0.cloud }
        return others.filter { $0.state.isEmpty } + others.filter { !$0.state.isEmpty }
    }

    /// The account's cloud machines it could go to: not the one it is on.
    var cloudMachines: [CloudMachine] {
        let here = cloudMachine(forCatalog: machine, catalog: store.catalog, machines: store.cloudMachines)?.id
        return store.cloudMachines.filter { $0.pickable && $0.id != here }
    }

    /// The machine it goes to, when that is known: the one chosen, or the
    /// cloud machine's (none yet for a new one).
    var target: String? {
        guard let c = cloudChoice else { return dest }
        return catalogMachine(forCloud: c, in: store.catalog)
    }

    var targetLabel: String {
        if let t = target { return store.label(t) }
        return cloudChoice == "new" ? newName : ""
    }

    var canContinue: Bool {
        guard let c = cloudChoice else { return dest != nil }
        return store.cloudSignedIn && (c != "new" || cloudNewReady(store, name: newName, size: newSize))
    }

    func choose(_ m: String) {
        dest = m
        cloudChoice = nil
    }

    func chooseCloud(_ c: String) {
        cloudChoice = c
        cloudTouched = true
        dest = nil
        suggestName()
    }

    /// Opened for Conch Cloud: one running, else one asleep, else a new
    /// one — until somebody picks.
    func defaultCloud() {
        guard wantsCloud, dest == nil, !cloudTouched || cloudChoice == nil else { return }
        if let p = defaultCloudPick(cloudMachines), case .existing(let id) = p {
            cloudChoice = id
        } else {
            cloudChoice = "new"
        }
        suggestName()
    }

    /// A new machine is named after the project, until its name is typed.
    func suggestName() {
        guard !newNameEdited else { return }
        newName = defaultCloudMachineName(for: info?.name ?? "", taken: store.cloudMachines.map(\.name))
    }

    /// The project there cloned from the same origin, which a move joins.
    func cloneThere(_ m: String) -> ProjectInfo? {
        cloneOf(info?.remote, in: store.snapshots[m]?.projects ?? [])
    }

    func makePlan() {
        guard store.binary != nil, canContinue else { return }
        phase = .planning
        problem = ""
        Task {
            // A cloud machine is readied first: made, or woken (as the move
            // would itself), and connected to, so the project can be shown
            // there when it has moved.
            if let c = cloudChoice {
                let pick: CloudPick = c == "new" ? .new(name: newName, size: newSize) : .existing(id: c)
                do {
                    let m = try await store.readyCloud(pick) { self.prep = $0 }
                    dest = m
                    // Made now: asked again, it is only connected to.
                    if let id = store.catalog.first(where: { $0.id == m })?.cloudID { cloudChoice = id }
                } catch {
                    prep = nil
                    problem = "\(error)"
                    phase = .pick
                    return
                }
                prep = nil
            }
            await readPlan()
        }
    }

    private func readPlan() async {
        guard let d = dest, let b = store.binary else {
            phase = .pick
            return
        }
        plan = nil
        let args = moveArgs(project: projectID, from: machine, to: d, into: into, plan: true)
        await stream(b, args) { line in
            switch line {
            case .plan(let p, let n):
                self.plan = p
                self.steps = n
            case .error(let e):
                self.problem = e
            default: break
            }
        }
        phase = plan != nil && problem.isEmpty ? .review : .pick
    }

    func move() {
        guard let d = dest, let p = plan, let b = store.binary else { return }
        phase = .moving
        problem = ""
        lines = planned(p)
        let args = moveArgs(project: projectID, from: machine, to: d, into: into, plan: false)
        let label = store.label(d)
        store.status = "Moving \(p.name) to \(label)…"
        Task {
            await stream(b, args) { line in
                switch line {
                case .event(let e):
                    self.steps = e.steps
                    if let i = self.lines.firstIndex(where: { $0.kind == e.kind && $0.name == e.name }) {
                        self.lines[i].state = e.state == "start" ? "run" : e.state
                        self.lines[i].detail = e.detail ?? ""
                    }
                case .result(let r):
                    self.result = r
                case .error(let e):
                    self.problem = e
                case .plan: break
                }
            }
            phase = .done
            if let r = result {
                store.status = summary(p.name, label, r)
            } else if !problem.isEmpty {
                store.status = "Moving \(p.name) to \(label): \(problem)"
            }
        }
    }

    /// The status bar's line when it is done.
    func summary(_ name: String, _ label: String, _ r: MoveResult) -> String {
        var s = "Moved \(name) to \(label)"
        let moved = r.moved?.count ?? 0, started = r.started?.count ?? 0
        if moved > 0 { s += " · \(moved) branch" + (moved == 1 ? "" : "es") }
        if started > 0 { s += " · " + moveCount(started, "agent continues", "agents continue") + " there" }
        if let f = r.failed, !f.isEmpty { s += " · stayed here: " + f.joined(separator: "; ") }
        return s
    }

    /// The checklist, before anything has run: a line a step.
    func planned(_ p: MovePlan) -> [Line] {
        var out: [Line] = []
        if p.target == nil {
            out.append(p.bundle == true
                ? Line(kind: "clone", name: p.name, title: "Copy the repository into \(p.cloneTo ?? "")")
                : Line(kind: "clone", name: p.remote ?? "", title: "Clone \(remoteLabel(p.remote)) into \(p.cloneTo ?? "")"))
        }
        for b in p.branches ?? [] { out.append(Line(kind: "branch", name: b.name, title: "\(b.name) — \(branchWhy(b))")) }
        for w in p.worktrees ?? [] {
            let agents = w.agents?.count ?? 0
            let what = w.there != nil ? "there already" : moveTakes(w.info).joined(separator: ", ")
            out.append(Line(kind: "worktree", name: w.branch, title: "\(w.branch) — \(what)"
                            + (agents > 0 ? " · \(agents) agent\(agents == 1 ? "" : "s")" : "")))
        }
        if let m = p.mainAgents, !m.isEmpty {
            out.append(Line(kind: "agents", name: p.name, title: moveCount(m.count, "agent", "agents") + " in the main checkout " + (m.count == 1 ? "continues" : "continue") + " there"))
        }
        return out
    }

    /// Runs conch, handing each line of JSON it prints to take, on the
    /// main actor; a failure with nothing said is said as one.
    func stream(_ binary: String, _ args: [String], take: @escaping @MainActor (MoveLine) -> Void) async {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: binary)
        p.arguments = args
        p.environment = conchEnvironment()
        let out = Pipe(), err = Pipe()
        p.standardOutput = out
        p.standardError = err
        p.standardInput = FileHandle.nullDevice
        var said = false
        do { try p.run() } catch {
            take(.error("\(error)"))
            return
        }
        let h = out.fileHandleForReading
        let lines = AsyncStream<String> { cont in
            Task.detached {
                var buf = Data()
                while true {
                    let d = h.availableData
                    if d.isEmpty { break }
                    buf.append(d)
                    while let nl = buf.firstIndex(of: 0x0a) {
                        let line = String(data: buf[buf.startIndex..<nl], encoding: .utf8) ?? ""
                        buf.removeSubrange(buf.startIndex...nl)
                        cont.yield(line)
                    }
                }
                cont.finish()
            }
        }
        for await line in lines {
            if let l = parseMoveLine(line) {
                if case .error = l { said = true }
                take(l)
            }
        }
        p.waitUntilExit()
        if p.terminationStatus != 0 && !said && result == nil {
            let e = String(data: err.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            take(.error(e.isEmpty ? "conch project move failed" : e))
        }
    }

    func remoteLabel(_ r: String?) -> String {
        let n = normalRemote(r ?? "")
        let parts = n.split(separator: "/")
        return parts.count >= 2 ? parts.suffix(2).joined(separator: "/") : n
    }

    func revealThere() {
        guard let d = dest, let r = result else { return }
        store.treeCursor = "p:" + key(d, r.target)
    }
}

struct MoveProjectSheet: View {
    @ObservedObject var store: Store
    @StateObject private var model: MoveProjectModel
    @Environment(\.conchTheme) private var theme

    init(store: Store, project: String, cloud: Bool = false, pick: CloudPick? = nil) {
        self.store = store
        _model = StateObject(wrappedValue: MoveProjectModel(store: store, project: project, cloud: cloud, pick: pick))
    }

    var name: String { model.info?.name ?? "the project" }
    var destLabel: String { model.dest.map { store.label($0) } ?? "" }

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 0) {
            header
            stepsBar.padding(.horizontal, 20).padding(.bottom, 12)
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    switch model.phase {
                    case .pick, .planning: wherePage
                    case .review: planPage
                    case .moving, .done: movingPage
                    }
                    if !model.problem.isEmpty {
                        HStack(alignment: .top, spacing: 6) {
                            Image(systemName: "exclamationmark.triangle.fill").font(.system(size: 11))
                            Text(model.problem).font(.ui(size: 12)).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                        }
                        .foregroundStyle(Theme.err).padding(.top, 4)
                    }
                }
                .padding(.horizontal, 20).padding(.bottom, 8)
            }
            .frame(minHeight: 260, maxHeight: 470)
            footer
        }
        .frame(width: 720)
        .background(Theme.raise)
        .onAppear {
            store.refreshCloud()
            model.defaultCloud()
        }
        .onChange(of: store.cloudMachines) { _, _ in model.defaultCloud() }
        .onChange(of: store.cloud) { _, _ in model.defaultCloud() } // signed in here, or read again
    }

    var header: some View {
        HStack(spacing: 12) {
            Image(systemName: "shippingbox.and.arrow.backward").font(.system(size: 16, weight: .semibold)).foregroundStyle(Theme.accent)
                .frame(width: 36, height: 36).background(RoundedRectangle(cornerRadius: 9).fill(Theme.accentDeep.opacity(0.3)))
            VStack(alignment: .leading, spacing: 2) {
                Text("Move \(name) to \(model.wantsCloud ? "Conch Cloud" : "another machine")").font(.ui(size: 16, weight: .semibold))
                Text("Everything an agent is working on goes with it: branches, worktrees, uncommitted work and the agents' conversations.")
                    .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.horizontal, 20).padding(.top, 16).padding(.bottom, 12)
    }

    var stepIndex: Int {
        switch model.phase {
        case .pick, .planning: return 1
        case .review: return 2
        case .moving, .done: return 3
        }
    }

    var stepsBar: some View {
        HStack(spacing: 6) {
            ForEach(Array(["Where", "What moves", "Moving"].enumerated()), id: \.offset) { i, t in
                let n = i + 1, on = n == stepIndex, past = n < stepIndex
                HStack(spacing: 7) {
                    Text(past ? "✓" : "\(n)").font(.ui(size: 10, weight: .bold))
                        .foregroundStyle(on || past ? Theme.selectedText : Theme.muted)
                        .frame(width: 18, height: 18)
                        .background(Circle().fill(on ? Theme.accent : past ? Theme.ok : .clear))
                        .overlay(Circle().stroke(on || past ? .clear : Theme.line2))
                    Text(t).font(.ui(size: 11.5)).foregroundStyle(on ? Theme.text : Theme.muted)
                    Spacer(minLength: 0)
                }
                .padding(.horizontal, 10).frame(height: 30)
                .background(RoundedRectangle(cornerRadius: 7).fill(on ? Theme.accentDeep.opacity(0.28) : Theme.well))
                .overlay(RoundedRectangle(cornerRadius: 7).stroke(on ? Theme.accent : Theme.line))
            }
        }
    }

    // MARK: 1 · where

    var wherePage: some View {
        VStack(alignment: .leading, spacing: 12) {
            if !model.machines.isEmpty {
                LazyVGrid(columns: [GridItem(.flexible(), spacing: 10), GridItem(.flexible(), spacing: 10)], spacing: 10) {
                    ForEach(model.machines, id: \.id) { m in machineCard(m) }
                }
            }
            cloudGroup
            HStack(spacing: 8) {
                Text("Into").font(.ui(size: 12.5)).foregroundStyle(Theme.text2)
                TextField("", text: $model.into, prompt: Text(intoPrompt))
                    .textFieldStyle(.plain).font(.ui(size: 12.5, design: .monospaced))
                    .padding(.horizontal, 9).frame(height: 28)
                    .background(RoundedRectangle(cornerRadius: 7).fill(Theme.well))
                    .overlay(RoundedRectangle(cornerRadius: 7).stroke(Theme.line2))
                    .disabled(model.target.flatMap { model.cloneThere($0) } != nil)
            }
            if model.phase == .planning {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text(model.prep?.text ?? "Reading what moves, on \(store.label(model.machine)) and \(model.targetLabel)…")
                        .font(.ui(size: 12)).foregroundStyle(Theme.muted)
                }
            }
        }
    }

    var intoPrompt: String {
        if let d = model.target, let p = model.cloneThere(d) { return "joins \(p.name) there (\(p.path))" }
        return "where it is here, under the home folder"
    }

    // MARK: Conch Cloud

    /// The account's machines, each with its state — one asleep is woken
    /// before the plan — and a new one; signed out, the sign-in.
    var cloudGroup: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                LucideIcon("cloud", color: Theme.text2, size: 12)
                Text("CONCH CLOUD").font(.ui(size: 11, weight: .semibold)).tracking(0.5).foregroundStyle(Theme.text2)
                Spacer()
            }
            if !store.cloudSignedIn {
                CloudSignInPrompt(store: store)
            } else {
                LazyVGrid(columns: [GridItem(.flexible(), spacing: 10), GridItem(.flexible(), spacing: 10)], spacing: 10) {
                    ForEach(model.cloudMachines) { m in cloudCard(m) }
                    newCloudCard
                }
                if model.cloudChoice == "new" {
                    CloudNewMachineFields(store: store,
                                          name: Binding(get: { model.newName }, set: { model.newName = $0; model.newNameEdited = true }),
                                          size: $model.newSize)
                        .disabled(model.phase == .planning)
                }
            }
        }
    }

    func cloudCard(_ m: CloudMachine) -> some View {
        let on = model.cloudChoice == m.id
        let there = catalogMachine(forCloud: m.id, in: store.catalog)
        let online = there.flatMap { t in store.machines.first { $0.id == t } }?.online == true
        let clone = online ? there.flatMap { model.cloneThere($0) } : nil
        return Button { model.chooseCloud(m.id) } label: {
            HStack(alignment: .top, spacing: 10) {
                LucideIcon("cloud", color: Theme.text2, size: 14)
                    .frame(width: 30, height: 30)
                    .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
                VStack(alignment: .leading, spacing: 4) {
                    Text(m.name).font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.text)
                    HStack(spacing: 6) {
                        tag(m.stateText, cloudToneColor(m.tone))
                        if m.asleep { tag("woken first", Theme.muted) }
                        if let c = clone { tag("has \(c.name)", Theme.accent) }
                    }
                }
                Spacer(minLength: 0)
            }
            .padding(12)
            .background(RoundedRectangle(cornerRadius: 10).fill(on ? Theme.accentDeep.opacity(0.28) : Theme.raise2))
            .overlay(RoundedRectangle(cornerRadius: 10).stroke(on ? Theme.accent : Theme.line2, lineWidth: on ? 1.5 : 1))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(model.phase == .planning)
    }

    var newCloudCard: some View {
        let on = model.cloudChoice == "new"
        return Button { model.chooseCloud("new") } label: {
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: "plus").font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(Theme.text2).frame(width: 30, height: 30)
                    .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
                VStack(alignment: .leading, spacing: 4) {
                    Text("New cloud machine…").font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.text)
                    Text("made for it, then moved to").font(.ui(size: 11)).foregroundStyle(Theme.muted)
                }
                Spacer(minLength: 0)
            }
            .padding(12)
            .background(RoundedRectangle(cornerRadius: 10).fill(on ? Theme.accentDeep.opacity(0.28) : Theme.raise2))
            .overlay(RoundedRectangle(cornerRadius: 10).stroke(on ? Theme.accent : Theme.line2, lineWidth: on ? 1.5 : 1))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(model.phase == .planning)
    }

    func machineCard(_ m: MachineLink) -> some View {
        let online = m.state.isEmpty
        let on = model.dest == m.id && model.cloudChoice == nil
        let clone = model.cloneThere(m.id)
        return Button { if online { model.choose(m.id) } } label: {
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: m.id == "local" ? "laptopcomputer" : "server.rack").font(.system(size: 14))
                    .foregroundStyle(Theme.text2).frame(width: 30, height: 30)
                    .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
                VStack(alignment: .leading, spacing: 4) {
                    Text(m.label).font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.text)
                    HStack(spacing: 6) {
                        tag(online ? "online" : "offline", online ? Theme.ok : Theme.muted)
                        if online {
                            if let c = clone { tag("has \(c.name)", Theme.accent) } else { tag("no clone yet", Theme.muted) }
                        }
                    }
                }
                Spacer(minLength: 0)
            }
            .padding(12)
            .background(RoundedRectangle(cornerRadius: 10).fill(on ? Theme.accentDeep.opacity(0.28) : Theme.raise2))
            .overlay(RoundedRectangle(cornerRadius: 10).stroke(on ? Theme.accent : Theme.line2, lineWidth: on ? 1.5 : 1))
            .opacity(online ? 1 : 0.5)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(!online || model.phase == .planning)
    }

    func tag(_ t: String, _ c: Color) -> some View {
        Text(t).font(.ui(size: 10.5)).foregroundStyle(c)
            .padding(.horizontal, 6).padding(.vertical, 1)
            .overlay(Capsule().stroke(c.opacity(0.45)))
    }

    // MARK: 2 · what moves

    @ViewBuilder var planPage: some View {
        if let p = model.plan {
            let agents = p.agents
            HStack(spacing: 8) {
                pill("to", destLabel)
                let nb = (p.worktrees?.count ?? 0) + (p.branches?.count ?? 0), nw = p.worktrees?.count ?? 0
                pill("\(nb)", nb == 1 ? "branch" : "branches")
                pill("\(nw)", nw == 1 ? "worktree" : "worktrees")
                pill("\(agents.count)", agents.count == 1 ? "agent" : "agents")
            }
            .padding(.bottom, 2)
            card("Repository", icon: "shippingbox", note: p.target != nil ? "\(destLabel) has it already"
                    : p.bundle == true ? "no origin: copied there, every branch and tag" : "cloned there from its origin") {
                row(left: Text(p.bundle == true ? p.name : model.remoteLabel(p.remote)).font(.ui(size: 12, design: .monospaced)),
                    right: [p.target == nil ? "→ \(p.cloneTo ?? "")" : "joins \(p.targetName ?? "") · \(p.targetPath ?? "")"])
            }
            if let w = p.worktrees, !w.isEmpty {
                card("Worktrees", icon: "arrow.triangle.branch", note: "commits it lacks, staged, unstaged and untracked files, .env") {
                    ForEach(w, id: \.branch) { x in
                        row(left: Text(x.branch).font(.ui(size: 12, design: .monospaced)),
                            right: x.there != nil ? ["there already · agents only"] : moveTakes(x.info))
                    }
                }
            }
            if let b = p.branches, !b.isEmpty {
                card("Branches with no worktree", icon: "arrow.triangle.branch", note: "given a worktree here, then moved as commits") {
                    ForEach(b, id: \.name) { x in
                        row(left: Text(x.name).font(.ui(size: 12, design: .monospaced)), right: [branchWhy(x)])
                    }
                }
            }
            if !agents.isEmpty {
                card("Agents", icon: "cpu", note: "continue there with their conversations, then close here") {
                    ForEach(agents, id: \.id) { a in
                        HStack(spacing: 8) {
                            Mark(agent: a.agent?.name)
                            Text(a.displayName).font(.ui(size: 12.5))
                            Spacer()
                            if a.agent?.state == .working {
                                tag("working · stopped mid-task", Theme.warn)
                            } else {
                                tag(a.agent?.state.rawValue ?? "idle", Theme.muted)
                            }
                        }
                        .padding(.horizontal, 12).padding(.vertical, 7)
                        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
                    }
                }
            }
            if let a = p.already, !a.isEmpty {
                card("On \(destLabel) already", icon: "checkmark.circle", note: "an earlier move's; left as they are there") {
                    ForEach(a, id: \.self) { b in row(left: Text(b).font(.ui(size: 12, design: .monospaced)), right: []) }
                }
            }
            let stays = staysHere(p)
            if !stays.isEmpty {
                VStack(alignment: .leading, spacing: 0) {
                    Label("Stays here", systemImage: "exclamationmark.triangle").font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.warn)
                        .padding(.horizontal, 12).padding(.top, 10).padding(.bottom, 4)
                    ForEach(stays, id: \.self) { s in
                        Text(s).font(.ui(size: 12)).foregroundStyle(Theme.text2).padding(.horizontal, 12).padding(.vertical, 5)
                    }
                }
                .padding(.bottom, 6)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(RoundedRectangle(cornerRadius: 10).fill(Theme.warn.opacity(0.07)))
                .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.warn.opacity(0.35)))
            }
            Toggle("Show the project on \(destLabel) when it's done", isOn: $model.showThere).toggleStyle(.checkbox).font(.ui(size: 12.5))
            Text("Everything here is kept, so nothing is lost if something goes wrong: remove the worktrees later.")
                .font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
        }
    }

    func staysHere(_ p: MovePlan) -> [String] {
        var out: [String] = []
        if let n = p.mainDirty, n > 0 { out.append("\(n) uncommitted file\(n == 1 ? "" : "s") in the main checkout — commit or stash them first to take them") }
        if let n = p.terminals, n > 0 { out.append("\(n) terminal\(n == 1 ? "" : "s")") }
        for s in p.skipped ?? [] { out.append("worktree \(s)") }
        return out
    }

    func pill(_ a: String, _ b: String) -> some View {
        (Text(a).bold() + Text(" " + b)).font(.ui(size: 12))
            .padding(.horizontal, 9).padding(.vertical, 4)
            .background(RoundedRectangle(cornerRadius: 7).fill(Theme.well))
            .overlay(RoundedRectangle(cornerRadius: 7).stroke(Theme.line))
    }

    func card<C: View>(_ title: String, icon: String, note: String, @ViewBuilder _ content: () -> C) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 6) {
                Image(systemName: icon).font(.system(size: 10))
                Text(title.uppercased()).font(.ui(size: 11, weight: .semibold)).tracking(0.5)
                Spacer()
                Text(note).font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(1)
            }
            .foregroundStyle(Theme.text2)
            .padding(.horizontal, 12).padding(.top, 10).padding(.bottom, 6)
            content()
        }
        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise2))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.line))
    }

    func row(left: Text, right: [String]) -> some View {
        HStack(spacing: 6) {
            left
            Spacer()
            ForEach(right, id: \.self) { r in
                Text(r).font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.text2)
                    .padding(.horizontal, 5).padding(.vertical, 1)
                    .background(RoundedRectangle(cornerRadius: 5).fill(Theme.well))
                    .overlay(RoundedRectangle(cornerRadius: 5).stroke(Theme.line))
            }
        }
        .padding(.horizontal, 12).padding(.vertical, 7)
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
    }

    // MARK: 3 · moving

    var movingPage: some View {
        VStack(alignment: .leading, spacing: 0) {
            let done = model.lines.filter { $0.state == "ok" || $0.state == "fail" }.count
            ProgressView(value: Double(done), total: Double(max(model.lines.count, 1))).tint(Theme.accent).padding(.bottom, 12)
            ForEach(model.lines) { l in
                HStack(spacing: 10) {
                    Group {
                        switch l.state {
                        case "run": ProgressView().controlSize(.small)
                        case "ok": Image(systemName: "checkmark.circle.fill").foregroundStyle(Theme.ok)
                        case "fail": Image(systemName: "xmark.circle.fill").foregroundStyle(Theme.err)
                        default: Image(systemName: "circle").foregroundStyle(Theme.muted)
                        }
                    }
                    .frame(width: 18)
                    Text(l.title).font(.ui(size: 12.5)).foregroundStyle(l.state == "wait" ? Theme.muted : Theme.text)
                    Spacer()
                    Text(l.detail).font(.ui(size: 11, design: .monospaced)).foregroundStyle(l.state == "fail" ? Theme.err : Theme.muted)
                        .lineLimit(1).truncationMode(.head)
                }
                .padding(.vertical, 7)
                .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
            }
            if model.phase == .done, let r = model.result {
                let failed = r.failed ?? []
                VStack(spacing: 4) {
                    Image(systemName: failed.isEmpty ? "checkmark.seal.fill" : "exclamationmark.triangle.fill")
                        .font(.system(size: 26)).foregroundStyle(failed.isEmpty ? Theme.ok : Theme.warn)
                    Text("Moved \(name) to \(destLabel)").font(.ui(size: 15, weight: .semibold))
                    Text(moveCount(r.moved?.count ?? 0, "branch", "branches") + " · "
                         + moveCount(r.started?.count ?? 0, "agent continues", "agents continue") + " there · " + r.targetPath)
                        .font(.ui(size: 12)).foregroundStyle(Theme.muted)
                    ForEach(failed, id: \.self) { f in Text("Stayed here: " + f).font(.ui(size: 12)).foregroundStyle(Theme.warn) }
                }
                .frame(maxWidth: .infinity).padding(.top, 14)
            }
        }
    }

    // MARK: footer

    var footer: some View {
        HStack(spacing: 10) {
            Text(hint).font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            Spacer()
            if model.phase == .review {
                Button("Back") { model.phase = .pick }.buttonStyle(.plain).foregroundStyle(Theme.text2)
            }
            if model.phase != .moving {
                Button(model.phase == .done ? "Done" : "Cancel") { close() }.keyboardShortcut(.cancelAction)
            }
            switch model.phase {
            case .pick, .planning:
                Button("Continue") { model.makePlan() }.keyboardShortcut(.defaultAction)
                    .disabled(!model.canContinue || model.phase == .planning)
            case .review:
                Button("Move \(name) to \(destLabel)") { model.move() }.keyboardShortcut(.defaultAction)
            case .moving:
                Button("Hide") { close() }
            case .done:
                if model.result != nil {
                    Button("Show on \(destLabel)") { model.revealThere(); close() }.keyboardShortcut(.defaultAction)
                }
            }
        }
        .padding(.horizontal, 20).padding(.vertical, 12)
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
    }

    var hint: String {
        switch model.phase {
        case .pick: return "Pick where \(name) goes."
        case .planning: return "Nothing changes yet."
        case .review: return "Nothing moves until you press Move."
        case .moving: return "You can hide this: the move goes on, and the status bar says when it's done."
        case .done: return model.result == nil ? "Nothing moved." : "The worktrees here are kept."
        }
    }

    func close() {
        if model.phase == .done, model.showThere, model.result != nil { model.revealThere() }
        store.moveProjectTarget = nil
    }
}
