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

/// The `conch preview` arguments for a device, by its ID.
public func previewArgs(_ verb: String, device: String, _ rest: [String] = []) -> [String] {
    ["preview", verb, "-device", device] + rest
}
