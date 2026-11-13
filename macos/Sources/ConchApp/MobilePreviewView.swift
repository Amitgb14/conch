import AppKit
import ConchKit
import SwiftUI

// A project's app on a phone, in a tab beside its agents: the iOS
// simulator's or Android emulator's own screen, taken again a few times a
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
        detect()
    }

    var device: MobileDevice? { previewChoice(devices, chosen: chosen) }
    var projectPath: String? { store.snapshots[machine]?.project(project)?.path }
    var projectName: String { store.snapshots[machine]?.project(project)?.name ?? project }

    private func conch(_ args: [String]) async throws -> Data {
        guard let b = store.binary else { throw ConchError(code: "conch", message: "conch was not found: install it, or set CONCH_BIN") }
        return try await runConch(b, args)
    }

    /// Lists the devices again: after a boot, or when asked.
    func reload() {
        Task {
            do {
                let l = try JSONDecoder().decode(MobileDeviceList.self, from: try await conch(["preview", "devices", "-json"]))
                devices = l.devices
                notes = l.notes ?? []
                if device?.booted == true { booting = false }
            } catch {
                self.error = "\(error)"
            }
            loaded = true
        }
    }

    private func detect() {
        guard let p = projectPath else { return }
        struct Detected: Decodable { var label: String? }
        Task {
            if let d = try? JSONDecoder().decode(Detected.self, from: try await conch(["preview", "detect", p])) {
                kind = d.label ?? ""
            }
        }
    }

    func choose(_ id: String) {
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
            var idle = 0
            while !Task.isCancelled {
                guard let self else { return }
                if let d = self.device, d.booted {
                    idle = 0
                    await self.shoot(d)
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

    func tap(at p: CGPoint, in box: CGSize) {
        guard let d = device, d.booted, let px = devicePixel(at: p, image: imageSize, box: box) else { return }
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

    var body: some View {
        VStack(spacing: 0) {
            header
            Rectangle().fill(Theme.line).frame(height: 1)
            screen.frame(maxWidth: .infinity, maxHeight: .infinity).background(Theme.well)
            Rectangle().fill(Theme.line).frame(height: 1)
            footer
        }
        .background(Theme.window)
        .onAppear { model.start() }
        .onDisappear { model.stop() }
    }

    private var header: some View {
        HStack(spacing: 8) {
            LucideIcon("smartphone", color: Theme.accent, size: 13)
            Text("Mobile preview").font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.text)
            if !model.kind.isEmpty {
                Text(model.kind).font(.ui(size: 11)).foregroundStyle(Theme.muted)
            }
            Spacer(minLength: 8)
            Menu {
                ForEach(model.devices) { d in
                    Button { model.choose(d.id) } label: {
                        Text((d.booted ? "● " : "") + d.title + (d.booted || d.state == "shutdown" ? "" : " (\(d.state))"))
                    }
                }
                Divider()
                Button("List Devices Again") { model.reload() }
            } label: {
                Text(model.device?.title ?? "No device").font(.ui(size: 11.5, design: .monospaced))
            }
            .menuStyle(.borderlessButton).fixedSize()
            .disabled(model.devices.isEmpty)
            .help("The simulator or emulator shown")
            if model.device?.booted == true {
                Button { model.button("home") } label: { LucideIcon("circle", size: 12) }
                    .buttonStyle(.plain).help("Home")
                if model.device?.isAndroid == true {
                    Button { model.button("back") } label: { LucideIcon("arrow-left", size: 12) }
                        .buttonStyle(.plain).help("Back")
                }
                let agents = model.agents
                Menu {
                    ForEach(agents) { a in
                        Button(a.displayName) { model.sendShot(to: a) }
                    }
                } label: {
                    HStack(spacing: 5) { LucideIcon("camera", size: 12); Text("To agent").font(.ui(size: 11.5)) }
                }
                .menuStyle(.borderlessButton).fixedSize()
                .disabled(agents.isEmpty)
                .help(agents.isEmpty ? "No agent is running in \(model.projectName)" : "Put a screenshot in an agent's prompt")
            }
        }
        .padding(.horizontal, 10).frame(height: 30)
        .background(Theme.paneHeader)
    }

    @ViewBuilder private var screen: some View {
        if let d = model.device, d.booted, let img = model.image {
            GeometryReader { geo in
                let box = CGSize(width: geo.size.width - 32, height: geo.size.height - 32)
                let r = aspectFit(model.imageSize, in: box)
                Image(nsImage: img)
                    .resizable()
                    .interpolation(.high)
                    .frame(width: r.width, height: r.height)
                    // A phone's screen has round corners; the shot is square.
                    .clipShape(RoundedRectangle(cornerRadius: d.isAndroid ? r.width * 0.06 : r.width * 0.12, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: d.isAndroid ? r.width * 0.06 : r.width * 0.12, style: .continuous).stroke(Theme.line2, lineWidth: 1))
                    .position(x: 16 + r.midX, y: 16 + r.midY)
                    .contentShape(Rectangle())
                    .onTapGesture(coordinateSpace: .local) { p in
                        model.tap(at: CGPoint(x: p.x - 16, y: p.y - 16), in: box)
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
                    }
                } else {
                    Text("No iOS simulator or Android emulator on this Mac.").foregroundStyle(Theme.text2)
                    Text("Install Xcode for iOS simulators, or Android Studio for emulators, then list the devices again.")
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
