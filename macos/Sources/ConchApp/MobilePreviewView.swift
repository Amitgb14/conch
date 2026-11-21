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
    /// Its kind as conch preview detect names it (expo, flutter…).
    @Published var kindID = ""

    private var loop: Task<Void, Never>?
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
    var projectPath: String? { store.snapshots[machine]?.project(project)?.path }
    /// Where the app is: the project's folder, or the checkout that holds
    /// it when only a branch does so far.
    var appPath: String? { store.mobileDir(project: project) ?? projectPath }
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
        guard let p = appPath else { return }
        struct Detected: Decodable { var kind: String?; var label: String? }
        Task {
            if let d = try? JSONDecoder().decode(Detected.self, from: try await conch(["preview", "detect", p])) {
                kind = d.label ?? ""
                kindID = d.kind ?? ""
            }
        }
    }

    /// What ▶ Run types for the device shown, if conch knows one.
    var runCommand: String? { previewRunCommand(kind: kindID, device: device, conch: conchBinary() ?? "conch") }

    /// Puts the app on the phone: the command typed into the workbench's
    /// own terminal when the phone is in one — the agent's checkout, so it
    /// is that branch's app — else run in a new terminal in the project.
    func run() {
        guard let cmd = runCommand, let c = store.connection(machine) else { return }
        let tab = store.layout.tabs.firstIndex { $0.root.panes.contains(key) }
        if let i = tab, let shell = store.layout.benchParts(i)?.shell, isPaneKey(shell), store.pane(shell) != nil {
            struct Send: Encodable { var id: String; var text: String }
            Task {
                do {
                    try await c.call("pane.send_text", Send(id: unkey(shell).id, text: cmd + "\r"))
                    store.say("Running \(cmd) in the workbench's terminal")
                } catch { self.error = "\(error)" }
            }
            return
        }
        guard let dir = appPath else { return }
        struct Create: Encodable { var name: String; var cwd: String; var command: [String] }
        Task {
            do {
                let p = try await c.call("pane.create", Create(name: "Run · \(projectName)", cwd: dir, command: ["/bin/sh", "-lc", cmd]), as: PaneInfo.self)
                store.snapshots[machine, default: Snapshot()].upsert(p)
                store.say("Running \(cmd) in a new terminal")
            } catch { self.error = "\(error)" }
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
    /// The list of simulators beside the phone, open or not.
    @AppStorage("mobilePreviewList") private var showList = false
    @State private var deleting: MobileDevice?

    var body: some View {
        VStack(spacing: 0) {
            header
            Rectangle().fill(Theme.line).frame(height: 1)
            HStack(spacing: 0) {
                screen.frame(maxWidth: .infinity, maxHeight: .infinity).background(Theme.well)
                if showList {
                    Rectangle().fill(Theme.line).frame(width: 1)
                    deviceList.frame(width: 220)
                }
            }
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
        HStack(spacing: 8) {
            // In a narrow column the kind gives way first, then the title,
            // then the icon — each whole rather than cut to a sliver — so the
            // device and its buttons always have their room.
            ViewThatFits(in: .horizontal) {
                HStack(spacing: 8) { icon; title; kindLabel }
                HStack(spacing: 8) { icon; title }
                icon
                Color.clear.frame(width: 0, height: 0)
            }
            .layoutPriority(-1)
            Spacer(minLength: 4)
            Menu {
                ForEach(model.devices) { d in
                    Button { model.choose(d.id) } label: {
                        Text((d.booted ? "● " : "") + d.title + (d.booted || d.state == "shutdown" ? "" : " (\(d.state))"))
                    }
                }
                Divider()
                Button("List Devices Again") { model.reload() }
            } label: {
                Text(model.device?.name ?? "No device").font(.ui(size: 11.5, design: .monospaced)).lineLimit(1)
            }
            .menuStyle(.borderlessButton).fixedSize()
            .disabled(model.devices.isEmpty)
            .help(model.device?.title ?? "The simulator shown")
            if let cmd = model.runCommand {
                Button { model.run() } label: {
                    Image(systemName: "play.fill").font(.system(size: 11))
                }
                .buttonStyle(.plain).foregroundStyle(Theme.accent)
                .accessibilityLabel("Run")
                .help("Put the app on this phone: \(cmd)")
            }
            if model.device?.booted == true {
                ForEach(previewButtons, id: \.name) { b in
                    Button { model.button(b.name) } label: { Image(systemName: b.symbol).font(.system(size: 11)) }
                        .buttonStyle(.plain).foregroundStyle(Theme.text2)
                        .accessibilityLabel(b.help).help(b.help)
                }
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
            Button { showList.toggle() } label: {
                Image(systemName: "sidebar.right").font(.system(size: 12))
            }
            .buttonStyle(.plain).foregroundStyle(showList ? Theme.accent : Theme.muted)
            .accessibilityLabel("Simulators")
            .help(showList ? "Hide the simulators" : "Every simulator, to shut down or delete")
        }
        .padding(.horizontal, 10).frame(height: 30)
        .background(Theme.paneHeader)
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
                            Button { model.run() } label: { Label("Run \(model.projectName) on it", systemImage: "play.fill") }
                                .controlSize(.large).help(cmd)
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
        .onTapGesture { model.choose(d.id) }
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
