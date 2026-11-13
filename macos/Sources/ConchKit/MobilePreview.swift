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
