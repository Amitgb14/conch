import CoreGraphics
import Foundation

// A project's app on a phone beside the agent writing it: the real iOS
// simulator or Android emulator, seen through `conch preview` (shot, tap,
// type, button), which runs the platforms' own tools. What the tab shows
// is the device's own screen, not a drawing of a layout.

/// A Mobile Preview tab's key in the layout: "mobile|machine|project".
public func mobileKey(_ machine: String, _ project: String) -> String { "mobile|\(machine)|\(project)" }

public func parseMobileKey(_ k: String) -> (machine: String, project: String)? {
    let parts = k.split(separator: "|", maxSplits: 2, omittingEmptySubsequences: false).map(String.init)
    guard parts.count == 3, parts[0] == "mobile", !parts[1].isEmpty, !parts[2].isEmpty else { return nil }
    return (parts[1], parts[2])
}

/// A phone to preview on, as `conch preview devices -json` lists it.
public struct MobileDevice: Decodable, Equatable, Sendable, Identifiable {
    public var id: String
    public var name: String
    public var platform: String
    public var kind: String
    public var os: String?
    public var state: String

    public init(id: String, name: String, platform: String, kind: String = "simulator", os: String? = nil, state: String) {
        self.id = id
        self.name = name
        self.platform = platform
        self.kind = kind
        self.os = os
        self.state = state
    }

    public var booted: Bool { state == "booted" }
    public var isAndroid: Bool { platform == "android" }

    /// "iPhone 16 Pro · iOS 18.2", "Pixel 9 · emulator".
    public var title: String {
        let about = (os ?? "").isEmpty ? kind : os!
        return about.isEmpty ? name : "\(name) · \(about)"
    }
}

public struct MobileDeviceList: Decodable, Equatable, Sendable {
    public var devices: [MobileDevice]
    public var notes: [String]?

    public init(devices: [MobileDevice], notes: [String]? = nil) {
        self.devices = devices
        self.notes = notes
    }
}

/// What the tab offers of what `conch preview devices` found: iPhone and
/// iPad simulators alone. Android's emulators and phones are left out, and
/// so is what went wrong listing them, which would only be noise here;
/// `conch preview` itself still reaches them.
public func previewShown(_ l: MobileDeviceList) -> MobileDeviceList {
    MobileDeviceList(devices: l.devices.filter { $0.platform == "ios" },
                     notes: l.notes.map { $0.filter { !$0.hasPrefix("Android:") } })
}

/// The device a tab shows: the one picked before while it is still
/// listed, else the first running one, else the first. A device that was
/// picked and then shut down stays picked, so the tab can offer to start
/// it again rather than jumping to another phone.
public func previewChoice(_ devices: [MobileDevice], chosen: String?) -> MobileDevice? {
    if let c = chosen, let d = devices.first(where: { $0.id == c }) { return d }
    return devices.first(where: \.booted) ?? devices.first
}

/// The device a tab shows once the list is read again: one that has
/// started since the last listing — ▶ Run's Expo or build booting a
/// simulator of its own choosing, or one started in Xcode — is followed,
/// since that is where the app is; else the pick stands. The first
/// listing (`old` empty) starts nothing: all of it was running before.
public func previewFollow(old: [MobileDevice], new: [MobileDevice], chosen: String?) -> String? {
    guard !old.isEmpty else { return chosen }
    let wasBooted = Set(old.filter(\.booted).map(\.id))
    guard let started = new.first(where: { $0.booted && !wasBooted.contains($0.id) }) else { return chosen }
    return started.id
}

/// The tab's list of simulators: running ones (which keep running after
/// the tab is closed, until somebody shuts them down), then the rest.
public func previewListGroups(_ devices: [MobileDevice]) -> (running: [MobileDevice], other: [MobileDevice]) {
    (devices.filter(\.booted), devices.filter { !$0.booted })
}

/// Where a picture of `image` size sits drawn to fit inside `box`,
/// centred, with its proportions kept.
public func aspectFit(_ image: CGSize, in box: CGSize) -> CGRect {
    guard image.width > 0, image.height > 0, box.width > 0, box.height > 0 else { return .zero }
    let s = min(box.width / image.width, box.height / image.height)
    let w = image.width * s, h = image.height * s
    return CGRect(x: (box.width - w) / 2, y: (box.height - h) / 2, width: w, height: h)
}

/// The device pixel under a point of the box the picture is fitted in
/// (top-left origin, as SwiftUI gives it), or nil off the picture —
/// a click in the letterbox touches nothing.
public func devicePixel(at p: CGPoint, image: CGSize, box: CGSize) -> (x: Int, y: Int)? {
    let r = aspectFit(image, in: box)
    // Its far edges are the picture's too (CGRect.contains leaves them out).
    guard r.width > 0, p.x >= r.minX, p.x <= r.maxX, p.y >= r.minY, p.y <= r.maxY else { return nil }
    let x = Int(((p.x - r.minX) / r.width * image.width).rounded(.down))
    let y = Int(((p.y - r.minY) / r.height * image.height).rounded(.down))
    return (min(x, Int(image.width) - 1), min(y, Int(image.height) - 1))
}

/// The device pixel under a point of the screen drawn at `screen`, or nil
/// off it — on the phone's bezel, or beside it.
public func devicePixel(at p: CGPoint, image: CGSize, screen: CGRect) -> (x: Int, y: Int)? {
    guard screen.width > 0, image.width > 0, p.x >= screen.minX, p.x <= screen.maxX, p.y >= screen.minY, p.y <= screen.maxY else { return nil }
    let x = Int(((p.x - screen.minX) / screen.width * image.width).rounded(.down))
    let y = Int(((p.y - screen.minY) / screen.height * image.height).rounded(.down))
    return (min(x, Int(image.width) - 1), min(y, Int(image.height) - 1))
}

/// Where a phone is drawn in the tab: its black body, the screen inside
/// it with an even bezel, and their corners. The screenshot already has
/// the device's own rounded corners and Dynamic Island (simctl's mask), so
/// the body is all that is added for it to read as an iPhone.
public struct PhoneLayout: Equatable, Sendable {
    public var body: CGRect
    public var screen: CGRect
    public var bodyRadius: CGFloat
    public var screenRadius: CGFloat
}

public func phoneLayout(image: CGSize, box: CGSize, tablet: Bool = false) -> PhoneLayout {
    let zero = PhoneLayout(body: .zero, screen: .zero, bodyRadius: 0, screenRadius: 0)
    guard image.width > 0, image.height > 0, box.width > 0, box.height > 0 else { return zero }
    // The bezel and the screen's corner, as parts of the screen's width:
    // an iPhone's display corner is about 55pt of 402, an iPad's 18 of 1032.
    let bezel: CGFloat = tablet ? 0.04 : 0.035
    let corner: CGFloat = tablet ? 0.03 : 0.135
    let body = aspectFit(CGSize(width: image.width * (1 + 2 * bezel), height: image.height + image.width * 2 * bezel), in: box)
    let w = body.width / (1 + 2 * bezel)
    let side = w * bezel
    let screen = CGRect(x: body.minX + side, y: body.minY + side, width: w, height: body.height - 2 * side)
    return PhoneLayout(body: body, screen: screen, bodyRadius: w * corner + side, screenRadius: w * corner)
}

/// What ▶ Run types to put a project's app on the simulator shown, by the
/// kind `conch preview detect` said; nil where conch knows no command.
/// An Xcode project has no dev server of its own, so conch builds,
/// installs and opens it (`conch preview run`), named by the app's own
/// conch so the shell's PATH does not decide which one.
public func previewRunCommand(kind: String, device: MobileDevice?, conch: String = "conch", status: String? = nil) -> String? {
    let id = device?.id ?? ""
    switch kind {
    case "expo": return "npx expo start --ios"
    case "react-native": return "npx react-native run-ios" + (id.isEmpty ? "" : " --udid \(id)")
    case "flutter": return "flutter run" + (id.isEmpty ? "" : " -d \(id)")
    case "xcode":
        return shellQuote(conch) + " preview run" + (id.isEmpty ? "" : " -device \(shellQuote(id))")
            + (status.map { " -status \(shellQuote($0))" } ?? "")
    default: return nil
    }
}

/// The same command naming the folder the app is in, for typing into a
/// terminal: where that was started says nothing of where somebody has
/// cd'd since, and the preview says which branch it runs. `conch preview
/// run` takes the folder; the others get a sh of their own, which any
/// login shell can start and which leaves the terminal where it was.
public func previewRunCommand(kind: String, device: MobileDevice?, conch: String = "conch", status: String? = nil, dir: String) -> String? {
    guard let cmd = previewRunCommand(kind: kind, device: device, conch: conch, status: status) else { return nil }
    if kind == "xcode" { return cmd + " " + shellQuote(dir) }
    return "/bin/sh -c " + shellQuote("cd \(shellQuote(dir)) && \(cmd)")
}

/// A checkout of a project that holds its app: one ▶ Run can build.
public struct PreviewCheckout: Equatable, Sendable, Identifiable {
    public var path: String
    /// Its branch, or its folder's name when it is on none.
    public var branch: String
    /// The branch without the project's own "name/" before it, which every
    /// task's branch has and a phone's narrow column has no room for.
    public var short: String
    /// The app's kind, as conch preview detect names it.
    public var kind: String
    public var id: String { path }
}

public func previewCheckouts(_ p: ProjectInfo, kinds: [String: String]) -> [PreviewCheckout] {
    mobileDirs(p).compactMap { dir in
        guard let kind = kinds[dir], !kind.isEmpty else { return nil }
        let named = p.worktrees?.first { $0.path == dir }?.branch ?? ""
        let branch = named.isEmpty ? (dir as NSString).lastPathComponent : named
        let prefix = p.name + "/"
        let short = branch.hasPrefix(prefix) ? String(branch.dropFirst(prefix.count)) : branch
        return PreviewCheckout(path: dir, branch: branch, short: short, kind: kind)
    }
}

/// The checkout a folder is in: the longest that holds it, since a
/// worktree can be kept inside the main checkout.
public func checkoutHolding(_ path: String, _ dirs: [String]) -> String? {
    dirs.filter { path == $0 || path.hasPrefix($0 + "/") }.max { $0.count < $1.count }
}

/// What ▶ Run builds: the checkout picked; else the checkout of the agent
/// whose workbench the phone is in (`agentCwd`, nil outside one); else,
/// outside a workbench, the first with an app. nil is "ask": the pick is
/// gone, or the agent's checkout has no app and several others do —
/// another agent's branch is never built in its place unasked.
public func previewTarget(_ p: ProjectInfo, kinds: [String: String], picked: String?, agentCwd: String?) -> PreviewCheckout? {
    let all = previewCheckouts(p, kinds: kinds)
    if let picked { return all.first { $0.path == picked } }
    guard let agentCwd else { return all.first }
    let own = checkoutHolding(agentCwd, mobileDirs(p))
    return all.first { $0.path == own } ?? (all.count == 1 ? all.first : nil)
}

extension Layout {
    /// The tab a preview is being looked at in: the one on screen when it
    /// holds it — one project's phone can be in several workbenches.
    public func previewTab(_ key: String) -> Tab? {
        if let t = current, t.root.panes.contains(key) { return t }
        return tab(showing: key).map { tabs[$0] }
    }
}

/// The agents a screenshot can be handed to: running ones in the
/// project, the one the person was last working with first.
public func previewAgents(_ snapshot: Snapshot, project: String, preferred: String? = nil) -> [PaneInfo] {
    let mine = snapshot.panes.filter { $0.projectID == project && $0.agent != nil && $0.running }
    guard let p = preferred, let i = mine.firstIndex(where: { $0.id == p }) else { return mine }
    var out = mine
    out.insert(out.remove(at: i), at: 0)
    return out
}

/// How often a running device's screen is taken again while its tab is
/// on screen: often enough to watch a reload land, not so often that a
/// slow simulator is asked again before it has answered (a shot is never
/// started while the last is still being taken).
public let previewRefreshInterval: Double = 0.35

/// Of the simulators already asked to show their keyboard, those still
/// running: one that shut down is asked again at its next boot.
public func previewStillBooted(_ ids: Set<String>, _ devices: [MobileDevice]) -> Set<String> {
    ids.intersection(devices.filter(\.booted).map(\.id))
}

/// The hardware buttons beside the phone: `conch preview button`'s
/// names, the SF Symbol each is drawn with, and what it does.
public struct PreviewButton: Sendable, Equatable {
    public var name: String
    public var symbol: String
    public var help: String
    /// It goes into a menu where the bar is too narrow for every button.
    public var folds = false
}

public let previewButtons = [
    PreviewButton(name: "home", symbol: "circle", help: "Home"),
    PreviewButton(name: "lock", symbol: "lock", help: "Lock the screen", folds: true),
    PreviewButton(name: "side", symbol: "button.vertical.right.press", help: "Side button", folds: true),
]

/// The `conch preview` arguments for a device, by its ID.
public func previewArgs(_ verb: String, device: String, _ rest: [String] = []) -> [String] {
    ["preview", verb, "-device", device] + rest
}

/// The folders that say whether a project is a mobile app: its own and
/// every checkout's, since the app may so far be only on the branch an
/// agent is making it on.
public func mobileDirs(_ p: ProjectInfo) -> [String] {
    var seen = Set<String>()
    return ([p.path] + (p.worktrees ?? []).map(\.path)).filter { !$0.isEmpty && seen.insert($0).inserted }
}

/// What `conch preview detect DIR DIR…` said, a JSON line a folder: the
/// kind of mobile app each holds, "" for none. A line that does not read
/// is left out, so it is asked again rather than taken as no app.
public func parseDetected(_ data: Data) -> [String: String] {
    struct Line: Decodable { var dir: String; var kind: String? }
    var out: [String: String] = [:]
    for l in data.split(separator: UInt8(ascii: "\n")) {
        if let d = try? JSONDecoder().decode(Line.self, from: Data(l)), !d.dir.isEmpty { out[d.dir] = d.kind ?? "" }
    }
    return out
}

/// The projects that are mobile apps, by what each folder was found to
/// hold: one is when any of its folders is. Only these are offered the
/// Mobile Preview, the workbench's phone and its Mobile layout.
public func mobileProjects(_ projects: [ProjectInfo], kinds: [String: String]) -> Set<String> {
    Set(projects.filter { p in mobileDirs(p).contains { !(kinds[$0] ?? "").isEmpty } }.map(\.id))
}

/// Whether a kind's ▶ Run says how it is going (`conch preview run
/// -status`): the others start a dev server, which never ends.
public func previewReported(_ kind: String) -> Bool { kind == "xcode" }

/// What `conch preview run -status` last wrote.
public struct PreviewRunReport: Decodable, Equatable, Sendable {
    public var pid: Int32
    /// starting, building, installing, opening, then open or failed; nil
    /// before the first.
    public var step: String?
    public var device: String?
    /// The step it failed at.
    public var failed: String?
    public var error: String?

    public init(pid: Int32, step: String? = nil, device: String? = nil, failed: String? = nil, error: String? = nil) {
        self.pid = pid
        self.step = step
        self.device = device
        self.failed = failed
        self.error = error
    }
}

/// What the status bar says of a run: while it goes, or once it is over.
public enum PreviewRunSays: Equatable, Sendable {
    case busy(String)
    case over(String, Flash.Tone)
}

/// How long a command typed at a terminal has to start: one that has said
/// nothing by then went to whatever was running there.
public let previewRunStartLimit: TimeInterval = 20
/// A little over the half hour conch preview run gives a build.
public let previewRunLimit: TimeInterval = 32 * 60

/// A run in the status bar's words. alive is whether the reporting
/// process is still there; waited, how long since Run was pressed.
public func previewRunSays(_ r: PreviewRunReport?, branch: String, alive: Bool, waited: TimeInterval) -> PreviewRunSays {
    let running = "Running \(branch)"
    guard let r else {
        return waited < previewRunStartLimit ? .busy(running)
            : .over("\(branch) hasn't started: its terminal may be busy", .err)
    }
    let phone = r.device ?? "the simulator"
    switch r.step {
    case "open":
        return .over("\(branch) is open on \(phone)", .ok)
    case "failed":
        if r.failed == "building" { return .over("Building \(branch) failed: the errors are in its terminal", .err) }
        // The bar is one line.
        let why = r.error?.split(whereSeparator: \.isNewline).first.map(String.init) ?? "see its terminal"
        return .over("\(running) failed: \(why)", .err)
    default:
        break
    }
    if !alive { return .over("\(running) stopped before it finished", .err) }
    if waited >= previewRunLimit { return .over("\(running) is taking too long: see its terminal", .err) }
    switch r.step {
    case "starting": return .busy("Starting \(phone) for \(branch)")
    case "building": return .busy("Building \(branch) for \(phone)")
    case "installing": return .busy("Installing \(branch) on \(phone)")
    case "opening": return .busy("Opening \(branch) on \(phone)")
    default: return .busy(running)
    }
}
