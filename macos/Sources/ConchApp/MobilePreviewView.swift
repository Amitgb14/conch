import AppKit
import ConchKit
import SwiftUI

// A project's app on an iPhone, in a tab beside its agents: the iOS
// simulator's own screen, taken again a few times a
// second through `conch preview shot`, so an edit is seen landing. A click
// on the picture is a tap there, the field below types into it, and "To
// agent" hands the agent a screenshot to look at. Simulators are this
// Mac's, so the tab is only offered for this Mac's projects.

@MainActor
final class MobilePreviewModel: ObservableObject {
    let key: String
    let machine: String
    let project: String
    unowned let store: Store
    @Published var devices: [MobileDevice] = []
    @Published var notes: [String] = []
    @Published var chosen: String?
    @Published var image: NSImage?
    @Published var imageSize: CGSize = .zero
    /// What went wrong last, said under the picture; empty while all is well.
    @Published var error = ""
    @Published var loaded = false
    @Published var booting = false
    /// "Expo · React Native", from `conch preview detect`.
    @Published var kind = ""
    /// The checkout picked to run, by the workbench's agent ("" outside
    /// one): a pick in one agent's workbench is not another's.
    @Published private var picks: [String: String] = [:]
    /// A ▶ Run on its way to the phone: pressed again it would be typed
    /// into the build.
    @Published private(set) var running = false

    private var loop: Task<Void, Never>?
    /// The simulators asked to show their keyboard, since they last booted.
    private var keyboardAsked: Set<String> = []
    private let shotPath: String

    init(key: String, store: Store) {
        self.key = key
        self.store = store
        (machine, project) = parseMobileKey(key) ?? ("local", "")
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("conch-preview", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        // One file per tab, written beside and renamed by conch, so a read
        // never meets half a picture.
        shotPath = dir.appendingPathComponent("live-\(abs(key.hashValue)).png").path
        reload()
    }

    var device: MobileDevice? { previewChoice(devices, chosen: chosen) }
    /// The agent whose workbench the phone is seen in, nil outside one.
    private var benchAgent: String? { store.layout.previewTab(key)?.benchAgent }
    var checkouts: [PreviewCheckout] {
        store.snapshots[machine]?.project(project).map { previewCheckouts($0, kinds: store.mobileKinds) } ?? []
    }
    /// What ▶ Run builds, said above the phone; nil until a branch is picked.
    var target: PreviewCheckout? {
        guard let p = store.snapshots[machine]?.project(project) else { return nil }
        let agent = benchAgent
        return previewTarget(p, kinds: store.mobileKinds, picked: picks[agent ?? ""],
                             agentCwd: agent.map { store.pane($0)?.cwd ?? "" })
    }

    /// Picks the checkout to run; nil goes back to the one conch would.
    func pick(_ path: String?) {
        picks[benchAgent ?? ""] = path
        detect()
    }

    var hasPick: Bool { picks[benchAgent ?? ""] != nil }
    var projectName: String { store.snapshots[machine]?.project(project)?.name ?? project }

    private func conch(_ args: [String]) async throws -> Data {
        guard let b = store.binary else { throw ConchError(code: "conch", message: "conch was not found: install it, or set CONCH_BIN") }
        return try await runConch(b, args)
    }

    /// Lists the devices again: after a boot, or when asked; and what the
    /// app is again, since the checkout holding it may have changed.
    func reload() {
        detect()
        Task {
            do {
                let l = previewShown(try JSONDecoder().decode(MobileDeviceList.self, from: try await conch(["preview", "devices", "-json"])))
                // A simulator that started meanwhile (▶ Run's) is shown.
                let follow = previewFollow(old: devices, new: l.devices, chosen: chosen)
                devices = l.devices
                keyboardAsked = previewStillBooted(keyboardAsked, devices)
                if follow != chosen { choose(follow) }
                notes = l.notes ?? []
                if device?.booted == true { booting = false }
            } catch {
                self.error = "\(error)"
            }
            loaded = true
        }
    }

    private func detect() {
        if let p = store.snapshots[machine]?.project(project) { store.detectMobile(mobileDirs(p)) }
        guard let p = target?.path else {
            kind = ""
            return
        }
        struct Detected: Decodable { var kind: String?; var label: String? }
        Task {
            if let d = try? JSONDecoder().decode(Detected.self, from: try await conch(["preview", "detect", p])) {
                kind = d.label ?? ""
            }
        }
    }

    /// What ▶ Run runs for the device and the checkout shown, if conch
    /// knows how.
    var runCommand: String? {
        target.flatMap { previewRunCommand(kind: $0.kind, device: device, conch: store.binary ?? "conch") }
    }

    /// Puts the checkout's app on the phone: typed into the workbench's
    /// own terminal when that was started in the same checkout, else run
    /// in a new terminal there, so it has that checkout's port and branch.
    func run() {
        guard !running, let t = target, runCommand != nil else { return }
        guard let c = store.connection(machine) else {
            store.say("Running \(t.short) failed: conch is not connected", .err)
            return
        }
        let binary = store.binary ?? "conch"
        let status = previewReported(t.kind)
            ? (shotPath as NSString).deletingLastPathComponent + "/run-\(UUID().uuidString.prefix(8)).json"
            : nil
        if let tab = store.layout.previewTab(key), let i = store.layout.tabs.firstIndex(where: { $0.id == tab.id }),
           let shell = store.layout.benchParts(i)?.shell, isPaneKey(shell), let cwd = store.pane(shell)?.cwd,
           let p = store.snapshots[machine]?.project(project), checkoutHolding(cwd, mobileDirs(p)) == t.path,
           let line = previewRunCommand(kind: t.kind, device: device, conch: binary, status: status, dir: t.path) {
            struct Send: Encodable { var id: String; var text: String }
            launch(t, status: status, place: "in the workbench's terminal") { _ in
                try await c.call("pane.send_text", Send(id: unkey(shell).id, text: line + "\r"))
            }
            return
        }
        guard let cmd = previewRunCommand(kind: t.kind, device: device, conch: binary, status: status) else { return }
        struct Create: Encodable { var name: String; var cwd: String; var command: [String] }
        let machine = machine
        launch(t, status: status, place: "in a new terminal, in the sidebar") { store in
            let p = try await c.call("pane.create", Create(name: "Run · \(t.short)", cwd: t.path, command: ["/bin/sh", "-lc", cmd]), as: PaneInfo.self)
            store.snapshots[machine, default: Snapshot()].upsert(p)
        }
    }

    /// Starts a run, then says in the status bar how it goes until it is
    /// over: its terminal may be out of sight, and gone once it succeeds.
    /// The window's store says it, so closing the tab does not silence it.
    private func launch(_ t: PreviewCheckout, status: String?, place: String,
                        _ start: @escaping @MainActor (Store) async throws -> Void) {
        running = true
        let store = store
        let job = UUID()
        store.progress(job, "Running \(t.short)")
        Task { [weak self] in
            defer {
                store.progress(job, nil)
                self?.running = false
            }
            do {
                try await start(store)
            } catch {
                // Not only under the phone: the next screenshot clears that.
                store.say("Running \(t.short) failed: \(error)", .err)
                return
            }
            guard let status else {
                store.say("Running \(t.short) \(place)")
                return
            }
            defer { try? FileManager.default.removeItem(atPath: status) }
            let began = Date()
            func report() -> PreviewRunReport? {
                FileManager.default.contents(atPath: status).flatMap { try? JSONDecoder().decode(PreviewRunReport.self, from: $0) }
            }
            while true {
                var r = report()
                let alive = r.map { $0.pid > 0 && (kill($0.pid, 0) == 0 || errno == EPERM) } ?? true
                // Gone since it was read: what it wrote last is the word.
                if !alive { r = report() }
                switch previewRunSays(r, branch: t.short, alive: alive, waited: Date().timeIntervalSince(began)) {
                case .busy(let what):
                    store.progress(job, what)
                case .over(let what, let tone):
                    store.say(what, tone)
                    self?.reload()
                    return
                }
                try? await Task.sleep(for: .milliseconds(400))
            }
        }
    }

    func choose(_ id: String?) {
        chosen = id
        image = nil
        error = ""
    }

    /// Takes the screen again and again while the tab is on screen; each
    /// shot is waited for before the next is asked, so a slow simulator
    /// is never asked twice at once. Off screen, nothing runs.
    func start() {
        guard loop == nil else { return }
        loop = Task { [weak self] in
            var idle = 0, shots = 0
            while !Task.isCancelled {
                guard let self else { return }
                if let d = self.device, d.booted {
                    idle = 0
                    await self.shoot(d)
                    // The list again now and then, so a simulator that
                    // ▶ Run started elsewhere is followed, and the list
                    // beside the phone says what is running.
                    shots += 1
                    if shots % 10 == 0 { self.reload() }
                    try? await Task.sleep(for: .seconds(previewRefreshInterval))
                } else {
                    // Waiting on a boot, or on nothing: ask again now and
                    // then, so a device started elsewhere shows up.
                    idle += 1
                    if idle % 4 == 0 { self.reload() }
                    try? await Task.sleep(for: .seconds(0.75))
                }
            }
        }
    }

    func stop() {
        loop?.cancel()
        loop = nil
    }

    private func shoot(_ d: MobileDevice) async {
        do {
            _ = try await conch(previewArgs("shot", device: d.id, ["-o", shotPath]))
            guard d.id == device?.id else { return } // another was picked meanwhile
            if let img = NSImage(contentsOfFile: shotPath), let rep = img.representations.first {
                image = img
                // The picture's own pixels, which taps are given in.
                imageSize = CGSize(width: rep.pixelsWide, height: rep.pixelsHigh)
                error = ""
            }
            // Once it has a screen it can be asked: a simulator that has
            // seen a hardware keyboard hides its own, and a tap on a
            // field would open nothing.
            if keyboardAsked.insert(d.id).inserted {
                act(previewArgs("keyboard", device: d.id, ["show"]))
            }
        } catch {
            self.error = "\(error)"
            // Shut down meanwhile: read the list again to say so.
            reload()
        }
    }

    func boot() {
        guard let d = device else { return }
        booting = true
        Task {
            do {
                _ = try await conch(["preview", "boot", d.id])
                chosen = d.id
            } catch {
                booting = false
                self.error = "\(error)"
            }
            reload()
        }
    }

    /// Stops a simulator: one left running after its preview was closed
    /// keeps its memory until something stops it.
    func shutdown(_ d: MobileDevice) {
        manage(["preview", "shutdown", d.id], "\(d.name) is shut down")
    }

    func shutdownAll() {
        manage(["preview", "shutdown", "-all"], "Every simulator is shut down")
    }

    /// Deletes a simulator for good (asked first, by the view).
    func delete(_ d: MobileDevice) {
        if chosen == d.id { choose(nil) }
        manage(["preview", "delete", d.id], "\(d.name) is deleted")
    }

    private func manage(_ args: [String], _ done: String) {
        Task {
            do {
                _ = try await conch(args)
                store.say(done)
                error = ""
            } catch { self.error = "\(error)" }
            reload()
        }
    }

    func tap(at p: CGPoint, screen: CGRect) {
        guard let d = device, d.booted, let px = devicePixel(at: p, image: imageSize, screen: screen) else { return }
        act(previewArgs("tap", device: d.id, [String(px.x), String(px.y)]))
    }

    func type(_ text: String) {
        guard let d = device, d.booted, !text.isEmpty else { return }
        act(previewArgs("type", device: d.id, [text]))
    }

    func button(_ name: String) {
        guard let d = device, d.booted else { return }
        act(previewArgs("button", device: d.id, [name]))
    }

    private func act(_ args: [String]) {
        Task {
            do { _ = try await conch(args) } catch { self.error = "\(error)" }
        }
    }

    /// The project's agents, the one with the keyboard last first.
    var agents: [PaneInfo] {
        let preferred = store.layout.focused.map { unkey($0) }.flatMap { $0.machine == machine ? $0.id : nil }
        return previewAgents(store.snapshots[machine] ?? Snapshot(), project: project, preferred: preferred)
    }

    /// A screenshot of its own, kept, pasted into the agent's prompt as a
    /// dropped file is — the agent attaches the picture, and the person
    /// adds what to look at and presses Enter.
    func sendShot(to agent: PaneInfo) {
        guard let d = device, d.booted, let c = store.connection(machine) else { return }
        let stamp = Int(Date().timeIntervalSince1970 * 1000)
        let path = (shotPath as NSString).deletingLastPathComponent + "/\(d.name.replacingOccurrences(of: " ", with: "-"))-\(stamp).png"
        Task {
            do {
                _ = try await conch(previewArgs("shot", device: d.id, ["-o", path]))
                struct Paste: Encodable { var id: String; var text: String; var paste: Bool }
                try await c.call("pane.send_text", Paste(id: agent.id, text: dropText([path]), paste: true))
                let k = ConchKit.key(machine, agent.id)
                store.focus(k)
                store.focusTerminal()
                store.say("Screenshot of \(d.name) put in \(agent.displayName)'s prompt")
            } catch {
                self.error = "\(error)"
            }
        }
    }
}

struct MobilePreviewView: View {
    @ObservedObject var model: MobilePreviewModel
    @ObservedObject var store: Store
    @State private var typed = ""
    /// The list of simulators over the phone. Not remembered: it covers
    /// the phone, so it is open only while somebody is using it.
    @State private var showList = false
    @State private var deleting: MobileDevice?

    var body: some View {
        VStack(spacing: 0) {
            header
            Rectangle().fill(Theme.line).frame(height: 1)
            if !model.checkouts.isEmpty {
                runBar
                Rectangle().fill(Theme.line).frame(height: 1)
            }
            screen.frame(maxWidth: .infinity, maxHeight: .infinity).background(Theme.well)
                .overlay { if showList { floatingList } }
            Rectangle().fill(Theme.line).frame(height: 1)
            footer
        }
        .background(Theme.window)
        // A card like the workbench's other parts, kept to its column: a
        // phone's column is narrow, and nothing here may push past it.
        .frame(minWidth: 0, maxWidth: .infinity)
        .clipShape(RoundedRectangle(cornerRadius: CGFloat(Theme.style.corner)))
        .modifier(CardFrame(focused: store.layout.focused == model.key))
        .confirmationDialog("Delete \(deleting?.title ?? "")?", isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } })) {
            if let d = deleting {
                Button("Delete", role: .destructive) { model.delete(d) }
            }
        } message: {
            Text("The simulator and everything installed on it are deleted for good. Xcode can make it again.")
        }
        .onAppear { model.start() }
        .onDisappear { model.stop() }
    }

    private var icon: some View { LucideIcon("smartphone", color: Theme.accent, size: 13) }

    private var title: some View {
        Text("Mobile Preview").font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.text).lineLimit(1).fixedSize()
    }

    @ViewBuilder private var kindLabel: some View {
        if !model.kind.isEmpty { Text(model.kind).font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(1).fixedSize() }
    }

    private var header: some View {
        // The first that fits the column whole: the kind gives way first,
        // then the title and the icon, then Lock and Side fold into a menu,
        // and last the device's name becomes a phone. Nothing is cut off.
        ViewThatFits(in: .horizontal) {
            bar { icon; title; kindLabel }
            bar { icon; title }
            bar { icon }
            bar { EmptyView() }
            bar(folded: true) { EmptyView() }
            bar(folded: true, named: false) { EmptyView() }
            bar(folded: true, named: false, spacing: 3) { EmptyView() }
        }
        .padding(.horizontal, 10).frame(height: 30)
        .background(Theme.paneHeader)
    }

    private func bar(folded: Bool = false, named: Bool = true, spacing: CGFloat = 8, @ViewBuilder lead: () -> some View) -> some View {
        HStack(spacing: spacing) {
            lead()
            Spacer(minLength: 0)
            deviceMenu(named: named)
            if !model.checkouts.isEmpty { runButton }
            if model.device?.booted == true {
                hardwareButtons(folded: folded)
                agentMenu
            }
            listButton
        }
    }

    private func deviceMenu(named: Bool) -> some View {
        Menu {
            ForEach(model.devices) { d in
                Button { model.choose(d.id) } label: {
                    Text((d.booted ? "● " : "") + d.title + (d.booted || d.state == "shutdown" ? "" : " (\(d.state))"))
                }
            }
            Divider()
            Button("List Devices Again") { model.reload() }
        } label: {
            if named {
                Text(model.device?.name ?? "No device").font(.ui(size: 11.5, design: .monospaced)).lineLimit(1)
            } else {
                Image(systemName: "iphone").font(.system(size: 12))
            }
        }
        .menuStyle(.borderlessButton).fixedSize()
        .disabled(model.devices.isEmpty)
        .accessibilityLabel(model.device?.name ?? "No device")
        .help(model.device?.title ?? "The simulator shown")
    }

    private var runButton: some View {
        let t = model.target
        let cmd = model.runCommand
        return Button { model.run() } label: {
            Image(systemName: "play.fill").font(.system(size: 11))
        }
        .buttonStyle(.plain).foregroundStyle(cmd == nil ? Theme.muted : Theme.accent)
        .disabled(cmd == nil || model.running)
        .accessibilityLabel("Run")
        .help(cmd.map { "Put \(t?.branch ?? "the app") on this phone: \($0)" }
              ?? (t == nil ? "Pick the branch to run first" : "conch knows no command to run this app on a simulator"))
    }

    /// Home always; Lock and Side beside it, or in a menu where they don't fit.
    @ViewBuilder private func hardwareButtons(folded: Bool) -> some View {
        ForEach(previewButtons.filter { !folded || !$0.folds }, id: \.name) { b in
            Button { model.button(b.name) } label: { Image(systemName: b.symbol).font(.system(size: 11)) }
                .buttonStyle(.plain).foregroundStyle(Theme.text2)
                .accessibilityLabel(b.help).help(b.help)
        }
        if folded {
            Menu {
                ForEach(previewButtons.filter(\.folds), id: \.name) { b in
                    Button(b.help) { model.button(b.name) }
                }
            } label: {
                Image(systemName: "ellipsis").font(.system(size: 12))
            }
            .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize()
            .accessibilityLabel("More buttons").help("Lock and the side button")
        }
    }

    @ViewBuilder private var agentMenu: some View {
        let agents = model.agents
        Menu {
            ForEach(agents) { a in
                Button(a.displayName) { model.sendShot(to: a) }
            }
        } label: {
            // A menu's label draws an SF Symbol, not a Lucide path.
            Image(systemName: "camera").font(.system(size: 12))
        }
        .accessibilityLabel("To agent")
        .menuStyle(.borderlessButton).fixedSize()
        .disabled(agents.isEmpty)
        .help(agents.isEmpty ? "No agent is running in \(model.projectName)" : "Put a screenshot in an agent's prompt")
    }

    private var listButton: some View {
        Button { showList.toggle() } label: {
            Image(systemName: "sidebar.right").font(.system(size: 12))
        }
        .buttonStyle(.plain).foregroundStyle(showList ? Theme.accent : Theme.muted)
        .accessibilityLabel("Simulators")
        .help(showList ? "Hide the simulators" : "Every simulator, to shut down or delete")
    }

    /// Which branch ▶ builds, said before it is pressed, and the others
    /// that hold an app to build instead.
    private var runBar: some View {
        let t = model.target
        return HStack(spacing: 6) {
            Text(t == nil ? "▶ needs a branch:" : t?.kind == "xcode" ? "▶ builds" : "▶ runs")
                .font(.ui(size: 11)).foregroundStyle(t == nil ? Theme.warn : Theme.muted).fixedSize()
            Menu {
                ForEach(model.checkouts) { c in
                    Button((c.path == t?.path ? "✓ " : "") + c.branch) { model.pick(c.path) }
                }
                if model.hasPick {
                    Divider()
                    Button("Choose Automatically") { model.pick(nil) }
                }
            } label: {
                Text(t?.short ?? "Pick one").font(.ui(size: 11, design: .monospaced)).lineLimit(1)
            }
            .menuStyle(.borderlessButton)
            .accessibilityLabel("Branch to run")
            .help(t.map { "\($0.branch) — \($0.path)" } ?? "This agent's checkout has no app: pick the branch to run")
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 10).frame(height: 24)
        .background(Theme.paneHeader)
    }

    /// The list over the phone, which keeps its size under it; a click
    /// beside the list closes it rather than tapping the phone.
    private var floatingList: some View {
        ZStack(alignment: .topTrailing) {
            Color.clear.contentShape(Rectangle()).onTapGesture { showList = false }
            deviceList
                .frame(maxWidth: 240, maxHeight: 420)
                .clipShape(RoundedRectangle(cornerRadius: 8))
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
                .shadow(color: .black.opacity(0.3), radius: 10, y: 4)
                .padding(6)
        }
    }

    @ViewBuilder private var screen: some View {
        if let d = model.device, d.booted, let img = model.image {
            GeometryReader { geo in
                let box = CGSize(width: geo.size.width - 32, height: geo.size.height - 32)
                let l = phoneLayout(image: model.imageSize, box: box, tablet: d.name.hasPrefix("iPad"))
                ZStack(alignment: .topLeading) {
                    // The phone's body; the shot brings its own corners
                    // and Dynamic Island, masked black by the simulator.
                    RoundedRectangle(cornerRadius: l.bodyRadius, style: .continuous)
                        .fill(Color.black)
                        .overlay(RoundedRectangle(cornerRadius: l.bodyRadius, style: .continuous).stroke(Color(white: 0.32), lineWidth: 1.5))
                        .shadow(color: .black.opacity(0.45), radius: 18, y: 10)
                        .frame(width: l.body.width, height: l.body.height)
                        .offset(x: 16 + l.body.minX, y: 16 + l.body.minY)
                    Image(nsImage: img)
                        .resizable()
                        .interpolation(.high)
                        .frame(width: l.screen.width, height: l.screen.height)
                        .clipShape(RoundedRectangle(cornerRadius: l.screenRadius, style: .continuous))
                        .offset(x: 16 + l.screen.minX, y: 16 + l.screen.minY)
                }
                .frame(width: geo.size.width, height: geo.size.height, alignment: .topLeading)
                .contentShape(Rectangle())
                .onTapGesture(coordinateSpace: .local) { p in
                    model.tap(at: CGPoint(x: p.x - 16, y: p.y - 16), screen: l.screen)
                }
            }
        } else {
            VStack(spacing: 12) {
                LucideIcon("smartphone", color: Theme.muted, size: 28)
                if !model.loaded {
                    Text("Looking for simulators…").foregroundStyle(Theme.muted)
                } else if let d = model.device {
                    if d.booted || model.booting {
                        ProgressView().controlSize(.small)
                        Text(model.booting ? "Starting \(d.name)…" : "Waiting for \(d.name)'s screen…").foregroundStyle(Theme.muted)
                    } else {
                        Text("\(d.title) is \(d.state).").foregroundStyle(Theme.text2)
                        if d.state == "shutdown" { Button("Start \(d.name)") { model.boot() }.controlSize(.large) }
                        // ▶ Run starts it too, and puts the app on it.
                        if let cmd = model.runCommand {
                            Button { model.run() } label: { Label("Run the app on it", systemImage: "play.fill") }
                                .controlSize(.large).disabled(model.running).help("\(model.target?.branch ?? ""): \(cmd)")
                        }
                    }
                } else {
                    Text("No iOS simulator on this Mac.").foregroundStyle(Theme.text2)
                    Text("Install Xcode, which brings the iPhone and iPad simulators, then list the devices again.")
                        .font(.ui(size: 12)).foregroundStyle(Theme.muted).multilineTextAlignment(.center)
                    Button("List Devices Again") { model.reload() }
                }
                ForEach(model.notes, id: \.self) { n in
                    Text(n).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).multilineTextAlignment(.center)
                }
            }
            .padding(24)
        }
    }

    /// Every simulator, the running ones first: a simulator keeps running
    /// after its preview is closed, so this is where it is stopped — or
    /// deleted, which is asked first.
    private var deviceList: some View {
        let g = previewListGroups(model.devices)
        return VStack(spacing: 0) {
            HStack {
                Text("Simulators").font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.text2)
                Spacer()
                Button { model.reload() } label: { Image(systemName: "arrow.clockwise").font(.system(size: 11)) }
                    .buttonStyle(.plain).foregroundStyle(Theme.muted).help("List the simulators again")
            }
            .padding(.horizontal, 10).frame(height: 26)
            Rectangle().fill(Theme.line).frame(height: 1)
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    section("Running · \(g.running.count)", g.running)
                    section("Shut down · \(g.other.count)", g.other)
                }
            }
            if !g.running.isEmpty {
                Rectangle().fill(Theme.line).frame(height: 1)
                Button("Shut Down All") { model.shutdownAll() }
                    .controlSize(.small).padding(6)
                    .help("Stop every running simulator")
            }
        }
        .background(Theme.window)
    }

    @ViewBuilder private func section(_ title: String, _ devices: [MobileDevice]) -> some View {
        if !devices.isEmpty {
            Text(title.uppercased()).font(.ui(size: 10, weight: .semibold)).foregroundStyle(Theme.muted)
                .padding(.horizontal, 10).padding(.top, 8).padding(.bottom, 3)
            ForEach(devices) { d in deviceRow(d) }
        }
    }

    private func deviceRow(_ d: MobileDevice) -> some View {
        let shown = model.device?.id == d.id
        return HStack(spacing: 6) {
            Circle().fill(d.booted ? Theme.ok : Theme.line).frame(width: 6, height: 6)
            VStack(alignment: .leading, spacing: 1) {
                Text(d.name).font(.ui(size: 11.5, weight: shown ? .semibold : .regular)).foregroundStyle(Theme.text).lineLimit(1)
                Text(d.os ?? d.kind).font(.ui(size: 10)).foregroundStyle(Theme.muted).lineLimit(1)
            }
            Spacer(minLength: 2)
            if d.booted {
                Button { model.shutdown(d) } label: { Image(systemName: "stop.circle").font(.system(size: 12)) }
                    .buttonStyle(.plain).foregroundStyle(Theme.muted)
                    .accessibilityLabel("Shut down \(d.name)").help("Shut down \(d.name)")
            }
            Button { deleting = d } label: { Image(systemName: "trash").font(.system(size: 11)) }
                .buttonStyle(.plain).foregroundStyle(Theme.muted)
                .accessibilityLabel("Delete \(d.name)").help("Delete \(d.name)")
        }
        .padding(.horizontal, 10).padding(.vertical, 4)
        .background(shown ? Theme.accent.opacity(0.12) : Color.clear)
        .contentShape(Rectangle())
        .onTapGesture {
            model.choose(d.id)
            showList = false
        }
        .help(d.title)
    }

    private var footer: some View {
        HStack(spacing: 8) {
            TextField("Type into the device…", text: $typed)
                .textFieldStyle(.plain).font(.ui(size: 12))
                .onSubmit { model.type(typed); typed = "" }
                .disabled(model.device?.booted != true)
            if !model.error.isEmpty {
                Text(model.error).font(.ui(size: 11)).foregroundStyle(Theme.err).lineLimit(1).truncationMode(.middle)
                    .help(model.error)
            } else if model.device?.booted == true {
                Text("click to tap").font(.ui(size: 11)).foregroundStyle(Theme.muted)
            }
        }
        .padding(.horizontal, 10).frame(height: 30)
        .background(Theme.paneHeader)
    }
}
