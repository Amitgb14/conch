import CoreGraphics
import Foundation
import Testing
@testable import ConchKit

@Test func mobileKeyRoundTrips() {
    let k = mobileKey("local", "r1")
    #expect(k == "mobile|local|r1")
    #expect(parseMobileKey(k)?.machine == "local")
    #expect(parseMobileKey(k)?.project == "r1")
    // A project ID with a "|" in it stays whole.
    #expect(parseMobileKey("mobile|box|a|b")?.project == "a|b")
    for bad in ["mobile|local", "mobile||r1", "mobile|local|", "files|local|r1", "", "local|p3"] {
        #expect(parseMobileKey(bad) == nil, "\(bad)")
    }
    // It is a view of the app's, not a pane: not searched as a terminal,
    // not counted as a pane, shown on its project's row and machine.
    #expect(!isPaneKey(k))
    #expect(!isTerminalKey(k))
    #expect(machine(ofKey: "mobile|box|r1") == "box")
    #expect(treeRow(forTab: k) == "p:\(key("local", "r1"))")
}

@Test func previewDevicesDecode() throws {
    let json = #"{"devices":[{"id":"A1","name":"iPhone 16 Pro","platform":"ios","kind":"simulator","os":"iOS 18.2","state":"booted"},{"id":"avd:Pixel_9","name":"Pixel 9","platform":"android","kind":"emulator","state":"shutdown"}],"notes":["Android: adb: no"]}"#
    let l = try JSONDecoder().decode(MobileDeviceList.self, from: Data(json.utf8))
    #expect(l.devices.count == 2)
    #expect(l.devices[0].booted && !l.devices[0].isAndroid)
    #expect(l.devices[0].title == "iPhone 16 Pro · iOS 18.2")
    #expect(l.devices[1].title == "Pixel 9 · emulator")
    #expect(l.devices[1].isAndroid && !l.devices[1].booted)
    #expect(l.notes == ["Android: adb: no"])
    // A computer with nothing: no notes key at all.
    let empty = try JSONDecoder().decode(MobileDeviceList.self, from: Data(#"{"devices":[]}"#.utf8))
    #expect(empty.devices.isEmpty && empty.notes == nil)
    #expect(MobileDevice(id: "x", name: "Phone", platform: "android", kind: "", state: "offline").title == "Phone")
}

@Test func previewChoiceKeepsWhatWasPicked() {
    let a = MobileDevice(id: "A", name: "iPhone SE", platform: "ios", state: "shutdown")
    let b = MobileDevice(id: "B", name: "iPhone 16", platform: "ios", state: "booted")
    let c = MobileDevice(id: "C", name: "Pixel", platform: "android", state: "booted")
    #expect(previewChoice([a, b, c], chosen: nil)?.id == "B")       // first running
    #expect(previewChoice([a, b, c], chosen: "C")?.id == "C")       // picked
    #expect(previewChoice([a, b, c], chosen: "A")?.id == "A")       // picked, though shut down
    #expect(previewChoice([a, b, c], chosen: "gone")?.id == "B")    // picked, then gone
    #expect(previewChoice([a], chosen: nil)?.id == "A")             // nothing running
    #expect(previewChoice([], chosen: "A") == nil)
}

@Test func aspectFitLetterboxes() {
    // A tall phone in a wide box: bars left and right.
    let r = aspectFit(CGSize(width: 1000, height: 2000), in: CGSize(width: 400, height: 400))
    #expect(r == CGRect(x: 100, y: 0, width: 200, height: 400))
    // A wide picture in a tall box: bars above and below.
    #expect(aspectFit(CGSize(width: 200, height: 100), in: CGSize(width: 100, height: 300)) == CGRect(x: 0, y: 125, width: 100, height: 50))
    #expect(aspectFit(.zero, in: CGSize(width: 10, height: 10)) == .zero)
    #expect(aspectFit(CGSize(width: 10, height: 10), in: .zero) == .zero)
}

@Test func devicePixelUnderTheClick() {
    let img = CGSize(width: 1000, height: 2000), box = CGSize(width: 400, height: 400)
    // The picture is at x 100…300, each point 5 pixels.
    #expect(devicePixel(at: CGPoint(x: 100, y: 0), image: img, box: box)! == (0, 0))
    #expect(devicePixel(at: CGPoint(x: 200, y: 200), image: img, box: box)! == (500, 1000))
    // The far edge is the last pixel, not one past it.
    #expect(devicePixel(at: CGPoint(x: 300, y: 400), image: img, box: box)! == (999, 1999))
    // In the letterbox, or with nothing drawn: nothing touched.
    #expect(devicePixel(at: CGPoint(x: 50, y: 200), image: img, box: box) == nil)
    #expect(devicePixel(at: CGPoint(x: 350, y: 200), image: img, box: box) == nil)
    #expect(devicePixel(at: CGPoint(x: 1, y: 1), image: .zero, box: box) == nil)
    // A 1×1 box: still a pixel inside the picture.
    let one = devicePixel(at: CGPoint(x: 0.5, y: 0.5), image: CGSize(width: 1, height: 1), box: CGSize(width: 1, height: 1))
    #expect(one! == (0, 0))
}

@Test func previewAgentsOfTheProject() {
    var s = Snapshot()
    s.panes = [
        PaneInfo(id: "p1", name: "claude", agent: AgentStatus(name: "claude", state: .working), projectID: "r1"),
        PaneInfo(id: "p2", name: "zsh", projectID: "r1"),                                                   // no agent
        PaneInfo(id: "p3", name: "codex", agent: AgentStatus(name: "codex", state: .idle), projectID: "r1"),
        PaneInfo(id: "p4", name: "claude", agent: AgentStatus(name: "claude", state: .idle), projectID: "r2"), // another project
        PaneInfo(id: "p5", name: "claude", state: "exited", agent: AgentStatus(name: "claude", state: .done), projectID: "r1"),
    ]
    #expect(previewAgents(s, project: "r1").map(\.id) == ["p1", "p3"])
    #expect(previewAgents(s, project: "r1", preferred: "p3").map(\.id) == ["p3", "p1"])
    #expect(previewAgents(s, project: "r1", preferred: "p4").map(\.id) == ["p1", "p3"])
    #expect(previewAgents(s, project: "none").isEmpty)
}

@Test func previewArgsNameTheDevice() {
    #expect(previewArgs("shot", device: "A1", ["-o", "/tmp/x.png"]) == ["preview", "shot", "-device", "A1", "-o", "/tmp/x.png"])
    #expect(previewArgs("button", device: "emu", ["back"]) == ["preview", "button", "-device", "emu", "back"])
}

@Test func projectMenuOffersMobilePreviewOnThisMac() {
    let s = fixture()
    let here = projects(s.tree(machine: "local"))[0]
    #expect(s.menu(for: here, machine: "local").contains { $0.action == .mobilePreview(project: key("local", "r1")) })
    // The simulators are this Mac's: another machine's project has none.
    let there = projects(s.tree(machine: "box"))[0]
    #expect(!s.menu(for: there, machine: "box").contains { if case .mobilePreview = $0.action { return true }; return false })
}

// The tab shows iOS simulators alone: Android's devices and what went
// wrong listing them are left out, iOS's own notes kept.
@Test func previewShowsIOSOnly() {
    let l = MobileDeviceList(devices: [
        MobileDevice(id: "emu", name: "Pixel 9", platform: "android", kind: "emulator", state: "booted"),
        MobileDevice(id: "A1", name: "iPhone 16 Pro", platform: "ios", state: "shutdown"),
        MobileDevice(id: "A2", name: "iPad Air", platform: "ios", state: "booted"),
    ], notes: ["Android: adb: cannot connect to daemon", "iOS: simctl not found: install Xcode"])
    let shown = previewShown(l)
    #expect(shown.devices.map(\.id) == ["A1", "A2"])
    #expect(shown.notes == ["iOS: simctl not found: install Xcode"])
    // With an Android phone running and no iPhone, nothing is picked.
    #expect(previewChoice(previewShown(MobileDeviceList(devices: [l.devices[0]])).devices, chosen: "emu") == nil)
    #expect(previewShown(MobileDeviceList(devices: [])).devices.isEmpty)
}

// The phone drawn around the screen: a black body with an even bezel,
// fitted to the room, its screen where a click is a tap.
@Test func phoneLayoutFitsTheBody() {
    let img = CGSize(width: 1206, height: 2622)
    let box = CGSize(width: 600, height: 700)
    let l = phoneLayout(image: img, box: box)
    // The body is inside the box, centred, and touches its height.
    #expect(l.body.minX >= 0 && l.body.maxX <= box.width + 0.001 && l.body.minY >= -0.001 && l.body.maxY <= box.height + 0.001)
    #expect(abs(l.body.height - box.height) < 0.01)
    #expect(abs(l.body.midX - box.width / 2) < 0.01)
    // The screen keeps the picture's proportions, with the same bezel on every side.
    #expect(abs(l.screen.width / l.screen.height - img.width / img.height) < 0.001)
    let side = l.screen.minX - l.body.minX
    #expect(side > 0 && abs((l.body.maxX - l.screen.maxX) - side) < 0.01 && abs((l.screen.minY - l.body.minY) - side) < 0.01)
    // The body's corner follows the screen's, a bezel further out.
    #expect(abs(l.bodyRadius - (l.screenRadius + side)) < 0.01 && l.screenRadius > 0)
    // A click on the bezel taps nothing; on the screen, its pixel.
    #expect(devicePixel(at: CGPoint(x: l.body.minX + side / 2, y: l.body.midY), image: img, screen: l.screen) == nil)
    let mid = devicePixel(at: CGPoint(x: l.screen.midX, y: l.screen.midY), image: img, screen: l.screen)!
    #expect(abs(mid.x - 603) <= 1 && abs(mid.y - 1311) <= 1)
    // An iPad's corners are tighter.
    let pad = phoneLayout(image: CGSize(width: 2064, height: 2752), box: box, tablet: true)
    #expect(pad.screenRadius / pad.screen.width < l.screenRadius / l.screen.width)
    // Nothing to draw: nothing laid out.
    #expect(phoneLayout(image: .zero, box: box).body == .zero)
    #expect(phoneLayout(image: img, box: .zero).body == .zero)
}

@Test func mobilePreviewSettingsSection() {
    // In the app's Settings, under Workspace, with an icon in every set.
    #expect(SettingsNav.groups.first { $0.0 == "Workspace" }?.1.contains("preview") == true)
    #expect(LucideIcons.all["tablet-smartphone"] != nil && sfSymbols["tablet-smartphone"] != nil && tileColors["tablet-smartphone"] != nil)
}

// What ▶ Run types to put the app on the iPhone shown: each kind's own
// way of building for an iOS simulator, for that simulator where it can be
// named; nothing where conch knows no command (Xcode builds in Xcode).
@Test func devCommandForTheDevice() {
    let d = MobileDevice(id: "U-1", name: "iPhone 17 Pro", platform: "ios", state: "booted")
    #expect(previewRunCommand(kind: "expo", device: d) == "npx expo start --ios")
    #expect(previewRunCommand(kind: "react-native", device: d) == "npx react-native run-ios --udid U-1")
    #expect(previewRunCommand(kind: "flutter", device: d) == "flutter run -d U-1")
    #expect(previewRunCommand(kind: "flutter", device: nil) == "flutter run")
    #expect(previewRunCommand(kind: "react-native", device: nil) == "npx react-native run-ios")
    #expect(previewRunCommand(kind: "xcode", device: d) == nil)
    #expect(previewRunCommand(kind: "", device: d) == nil)
    #expect(previewRunCommand(kind: "android", device: d) == nil)
}
