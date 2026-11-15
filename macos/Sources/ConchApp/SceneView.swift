import AppKit
import ConchKit
import SwiftUI

// [app] background: a scene behind the window — a reef, snowy pines, a
// lagoon, a starfield — by day under a light theme and by night under a
// dark one. ConchKit/Backgrounds.swift decides what it is and where its
// pieces are; this draws it. The frame is frosted over it (Frost), and a
// pane lets it through only as far as background_through says.

extension Theme {
    /// The scene behind the window, from [app] (Store.appLook).
    static var scene = SceneLook()
    /// Night for a scene: a dark theme.
    static var sceneNight: Bool { !current.light }
    /// What fills the space between the window's surfaces: the theme's
    /// background, or nothing, so the scene shows.
    static var ground: Color { scene.on ? .clear : window }
    /// A pane's background, as far as the scene shows through it.
    static var paneFill: Color { window.opacity(scene.paneOpacity) }
    /// The same behind a pane's terminal, for AppKit.
    static var paneTerminal: NSColor { terminal.withAlphaComponent(scene.paneOpacity) }
}

/// A surface of the frame — the sidebar, the tab bar, the status bar —
/// filled with its own colour, or frosted over the scene when there is one.
struct Frost: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let color: Color

    var body: some View {
        let _ = look
        if Theme.scene.on {
            ZStack {
                if Theme.scene.frost.blurs { Rectangle().fill(.ultraThinMaterial) }
                color.opacity(Theme.scene.frost.tint)
            }
        } else {
            color
        }
    }
}

/// The scene, filling where it is put, moving while the app is on screen
/// and nothing asks for stillness.
struct SceneView: View {
    @Environment(\.conchTheme) private var look
    let scene: SceneLook
    @State private var visible = NSApp.occlusionState.contains(.visible)
    @State private var reduceMotion = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
    @State private var lowPower = ProcessInfo.processInfo.isLowPowerModeEnabled

    var moves: Bool { visible && scene.moves(reduceMotion: reduceMotion, lowPower: lowPower) }

    var body: some View {
        let _ = look
        let night = Theme.sceneNight
        let s = scene.scene
        let pieces = SceneMotion.pieces(s.particles, seed: 7)
        let moving = moves
        TimelineView(.animation(minimumInterval: 1.0 / 30, paused: !moving)) { tl in
            // Still is one frame of it, the same every time.
            let t = moving ? tl.date.timeIntervalSinceReferenceDate : 6
            Canvas { ctx, size in
                SceneDraw.draw(s, night: night, pieces: pieces, t: t, in: &ctx, size: size)
            }
        }
        .drawingGroup()
        .allowsHitTesting(false)
        .accessibilityHidden(true)
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didChangeOcclusionStateNotification)) { _ in
            visible = NSApp.occlusionState.contains(.visible)
        }
        .onReceive(NSWorkspace.shared.notificationCenter.publisher(for: NSWorkspace.accessibilityDisplayOptionsDidChangeNotification)) { _ in
            reduceMotion = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
        }
        .onReceive(NotificationCenter.default.publisher(for: .NSProcessInfoPowerStateDidChange)) { _ in
            lowPower = ProcessInfo.processInfo.isLowPowerModeEnabled
        }
    }
}

/// The drawing of each scene: all paths and gradients, no artwork.
enum SceneDraw {
    static func c(_ rgb: RGB, _ a: Double = 1) -> Color { Color(.sRGB, red: rgb.r, green: rgb.g, blue: rgb.b, opacity: a) }
    static func c(_ hex: String, _ a: Double = 1) -> Color { c(RGB(hex), a) }

    static func draw(_ s: BackgroundScene, night: Bool, pieces: [ScenePiece], t: Double, in ctx: inout GraphicsContext, size: CGSize) {
        guard s != .none else { return }
        let r = CGRect(origin: .zero, size: size)
        let sky = s.sky(night: night).map { c($0) }
        if s == .stars {
            ctx.fill(Path(r), with: .radialGradient(Gradient(colors: sky), center: CGPoint(x: size.width * 0.7, y: size.height * 0.15),
                                                     startRadius: 0, endRadius: max(size.width, size.height) * 0.9))
        } else {
            ctx.fill(Path(r), with: .linearGradient(Gradient(colors: sky), startPoint: .zero, endPoint: CGPoint(x: 0, y: size.height)))
        }
        switch s {
        case .reef: reef(night: night, pieces: pieces, t: t, &ctx, size)
        case .pines: pines(night: night, pieces: pieces, t: t, &ctx, size)
        case .lagoon: lagoon(night: night, t: t, &ctx, size)
        case .stars: stars(night: night, pieces: pieces, t: t, &ctx, size)
        case .none: break
        }
    }

    // MARK: reef

    static func reef(night: Bool, pieces: [ScenePiece], t: Double, _ ctx: inout GraphicsContext, _ size: CGSize) {
        let w = size.width, h = size.height
        // Light from the surface: long soft bands that sway.
        let ray = c("#A0F0FF")
        for (i, (x, width, period)) in [(0.14, 0.09, 9.0), (0.41, 0.055, 12.0), (0.7, 0.12, 15.0)].enumerated() {
            let sway = sin(t / period * 2 * .pi + Double(i))
            var g = ctx
            g.opacity = (night ? 0.22 : 0.32) + 0.12 * sway
            g.translateBy(x: w * x + sway * 30, y: -40)
            g.rotate(by: .degrees(16 + 2 * sway))
            let band = Path(CGRect(x: 0, y: 0, width: w * width, height: h * 1.4))
            g.fill(band, with: .linearGradient(Gradient(colors: [ray.opacity(0.55), ray.opacity(0)]),
                                               startPoint: .zero, endPoint: CGPoint(x: 0, y: h * 1.2)))
        }
        // Bubbles rising.
        for p in pieces {
            let d = 4 + p.size * 12
            let x = (p.x + SceneMotion.sway(p, at: t, by: 0.008)) * w
            let y = SceneMotion.rise(p, at: t) * h
            let circle = Path(ellipseIn: CGRect(x: x - d / 2, y: y - d / 2, width: d, height: d))
            ctx.fill(circle, with: .color(c("#C8FAFF", 0.12)))
            ctx.stroke(circle, with: .color(c("#C8FAFF", 0.6)), lineWidth: 1)
        }
        // Two fish, one each way.
        fish(&ctx, x: SceneMotion.cross(at: t, period: 38) * w, y: h * 0.28, length: 64, body: c("#F2A65A"), tail: c("#E0884A"), left: false)
        fish(&ctx, x: (1 - SceneMotion.cross(at: t, period: 52, offset: 0.4)) * w, y: h * 0.6, length: 42, body: c("#7FDBCA"), tail: c("#5CC4B0"), left: true)
        // Rock and coral along the bottom.
        let ground = c(BackgroundScene.reef.ground(night: night)!)
        var rock = Path()
        rock.move(to: CGPoint(x: 0, y: h))
        rock.addLine(to: CGPoint(x: 0, y: h - 50))
        rock.addQuadCurve(to: CGPoint(x: w * 0.09, y: h - 52), control: CGPoint(x: w * 0.05, y: h - 90))
        rock.addQuadCurve(to: CGPoint(x: w * 0.18, y: h - 58), control: CGPoint(x: w * 0.13, y: h - 110))
        rock.addQuadCurve(to: CGPoint(x: w * 0.33, y: h), control: CGPoint(x: w * 0.27, y: h - 80))
        rock.closeSubpath()
        rock.move(to: CGPoint(x: w * 0.6, y: h))
        rock.addQuadCurve(to: CGPoint(x: w * 0.77, y: h - 60), control: CGPoint(x: w * 0.68, y: h - 110))
        rock.addQuadCurve(to: CGPoint(x: w * 0.88, y: h - 50), control: CGPoint(x: w * 0.83, y: h - 120))
        rock.addQuadCurve(to: CGPoint(x: w, y: h - 42), control: CGPoint(x: w * 0.95, y: h - 80))
        rock.addLine(to: CGPoint(x: w, y: h))
        rock.closeSubpath()
        ctx.fill(rock, with: .color(ground))
        coral(&ctx, x: w * 0.1, base: h, color: c("#E86A6A", night ? 0.85 : 1))
        coral(&ctx, x: w * 0.78, base: h, color: c("#F2A65A", night ? 0.85 : 1))
    }

    static func fish(_ ctx: inout GraphicsContext, x: Double, y: Double, length: Double, body: Color, tail: Color, left: Bool) {
        var g = ctx
        g.translateBy(x: x, y: y)
        if left { g.scaleBy(x: -1, y: 1) }
        let l = length, hgt = length * 0.45
        var b = Path()
        b.move(to: CGPoint(x: 0, y: 0))
        b.addQuadCurve(to: CGPoint(x: l * 0.7, y: 0), control: CGPoint(x: l * 0.35, y: -hgt))
        b.addQuadCurve(to: CGPoint(x: 0, y: 0), control: CGPoint(x: l * 0.35, y: hgt))
        g.fill(b, with: .color(body))
        var tl = Path()
        tl.move(to: CGPoint(x: l * 0.68, y: 0))
        tl.addLine(to: CGPoint(x: l, y: -hgt * 0.6))
        tl.addLine(to: CGPoint(x: l * 0.92, y: 0))
        tl.addLine(to: CGPoint(x: l, y: hgt * 0.6))
        tl.closeSubpath()
        g.fill(tl, with: .color(tail))
        g.fill(Path(ellipseIn: CGRect(x: l * 0.12, y: -hgt * 0.18, width: 3, height: 3)), with: .color(c("#0B1B24")))
    }

    static func coral(_ ctx: inout GraphicsContext, x: Double, base: Double, color: Color) {
        var p = Path()
        p.move(to: CGPoint(x: x, y: base))
        p.addLine(to: CGPoint(x: x + 6, y: base - 96))
        p.move(to: CGPoint(x: x + 4, y: base - 64))
        p.addLine(to: CGPoint(x: x - 16, y: base - 88))
        p.move(to: CGPoint(x: x + 5, y: base - 76))
        p.addLine(to: CGPoint(x: x + 26, y: base - 104))
        ctx.stroke(p, with: .color(color), style: StrokeStyle(lineWidth: 7, lineCap: .round))
    }

    // MARK: pines

    static func pines(night: Bool, pieces: [ScenePiece], t: Double, _ ctx: inout GraphicsContext, _ size: CGSize) {
        let w = size.width, h = size.height
        // The moon by night, a pale sun by day.
        let orb = CGRect(x: w * 0.82, y: h * 0.09, width: 70, height: 70)
        ctx.fill(Path(ellipseIn: orb.insetBy(dx: -40, dy: -40)),
                 with: .radialGradient(Gradient(colors: [c("#F4EFD8", night ? 0.25 : 0.4), c("#F4EFD8", 0)]),
                                       center: CGPoint(x: orb.midX, y: orb.midY), startRadius: 30, endRadius: 75))
        ctx.fill(Path(ellipseIn: orb), with: .color(night ? c("#F4EFD8") : c("#FFF8E0")))
        let hills = night ? c("#26364F") : c("#B9CBDD")
        let back = night ? c("#1A2A3E") : c("#3E5F4C")
        let front = night ? c("#0F1B2B") : c("#28432F")
        let snow = c(BackgroundScene.pines.ground(night: night)!)
        var hill = Path()
        hill.move(to: CGPoint(x: 0, y: h))
        hill.addLine(to: CGPoint(x: 0, y: h - 90))
        hill.addQuadCurve(to: CGPoint(x: w * 0.5, y: h - 100), control: CGPoint(x: w * 0.25, y: h - 150))
        hill.addQuadCurve(to: CGPoint(x: w, y: h - 104), control: CGPoint(x: w * 0.75, y: h - 160))
        hill.addLine(to: CGPoint(x: w, y: h))
        hill.closeSubpath()
        ctx.fill(hill, with: .color(hills))
        for (x, tall) in [(0.05, 150.0), (0.15, 190), (0.34, 130), (0.66, 180), (0.74, 120), (0.92, 170)] {
            tree(&ctx, x: w * x, base: h - 30, tall: tall, color: back)
        }
        var drift = Path()
        drift.move(to: CGPoint(x: 0, y: h))
        drift.addLine(to: CGPoint(x: 0, y: h - 38))
        drift.addQuadCurve(to: CGPoint(x: w * 0.62, y: h - 40), control: CGPoint(x: w * 0.3, y: h - 66))
        drift.addQuadCurve(to: CGPoint(x: w, y: h - 50), control: CGPoint(x: w * 0.82, y: h - 28))
        drift.addLine(to: CGPoint(x: w, y: h))
        drift.closeSubpath()
        ctx.fill(drift, with: .color(snow))
        for (x, tall) in [(0.22, 140.0), (0.49, 170), (0.83, 120)] {
            tree(&ctx, x: w * x, base: h - 18, tall: tall, color: front)
        }
        // Snow falling, drifting a little as it goes.
        let flake = night ? c("#FFFFFF", 0.9) : c("#FFFFFF", 1)
        for p in pieces {
            let d = 2 + p.size * 4
            let x = (p.x + SceneMotion.progress(p, at: t) * 0.04 + SceneMotion.sway(p, at: t, by: 0.006)) * w
            let y = SceneMotion.fall(p, at: t) * h
            var g = ctx
            g.opacity = 0.4 + p.size * 0.6
            g.fill(Path(ellipseIn: CGRect(x: x, y: y, width: d, height: d)), with: .color(flake))
        }
    }

    static func tree(_ ctx: inout GraphicsContext, x: Double, base: Double, tall: Double, color: Color) {
        let half = tall * 0.32
        var p = Path()
        // Three tiers, each a little narrower than the one below.
        for k in 0..<3 {
            let top = base - tall + Double(k) * tall * 0.22
            let bottom = base - Double(2 - k) * tall * 0.18
            let wid = half * (0.65 + Double(k) * 0.18)
            p.move(to: CGPoint(x: x, y: top))
            p.addLine(to: CGPoint(x: x + wid, y: bottom))
            p.addLine(to: CGPoint(x: x - wid, y: bottom))
            p.closeSubpath()
        }
        p.addRect(CGRect(x: x - 4, y: base - 8, width: 8, height: 12))
        ctx.fill(p, with: .color(color))
    }

    // MARK: lagoon

    static func lagoon(night: Bool, t: Double, _ ctx: inout GraphicsContext, _ size: CGSize) {
        let w = size.width, h = size.height
        // Sunlight through the water: soft patches that drift and swell.
        let light = night ? c("#BFE6F0", 0.12) : c("#FFFFFF", 0.32)
        let spots: [(Double, Double, Double, Double)] = [(0.2, 0.3, 90, 14), (0.6, 0.2, 80, 19), (0.8, 0.6, 110, 14), (0.35, 0.75, 90, 19), (0.48, 0.48, 80, 23), (0.1, 0.85, 80, 17)]
        for (i, (x, y, rad, period)) in spots.enumerated() {
            let k = sin(t / period * 2 * .pi + Double(i) * 1.7)
            let center = CGPoint(x: w * x - 40 * k, y: h * y + 22 * k)
            let radius = rad * (1 + 0.15 * k)
            ctx.fill(Path(ellipseIn: CGRect(x: center.x - radius, y: center.y - radius, width: radius * 2, height: radius * 2)),
                     with: .radialGradient(Gradient(colors: [light, light.opacity(0)]), center: center, startRadius: radius * 0.3, endRadius: radius))
        }
        // A turtle crossing, corner to corner.
        let k = SceneMotion.cross(at: t, period: 46)
        turtle(&ctx, x: k * w, y: h * (0.8 - k * 0.65), t: t, night: night)
        // Sand.
        var sand = Path()
        sand.move(to: CGPoint(x: 0, y: h))
        sand.addLine(to: CGPoint(x: 0, y: h - 70))
        sand.addQuadCurve(to: CGPoint(x: w * 0.5, y: h - 66), control: CGPoint(x: w * 0.25, y: h - 100))
        sand.addQuadCurve(to: CGPoint(x: w, y: h - 80), control: CGPoint(x: w * 0.77, y: h - 30))
        sand.addLine(to: CGPoint(x: w, y: h))
        sand.closeSubpath()
        ctx.fill(sand, with: .color(c(BackgroundScene.lagoon.ground(night: night)!)))
    }

    static func turtle(_ ctx: inout GraphicsContext, x: Double, y: Double, t: Double, night: Bool) {
        var g = ctx
        g.translateBy(x: x, y: y)
        g.rotate(by: .degrees(-14))
        g.scaleBy(x: 1.6, y: 1.6)
        if night { g.opacity = 0.75 }
        let flip = sin(t / 2.4 * 2 * .pi) * 16
        for (dy, sign) in [(-24.0, -1.0), (24.0, 1.0)] {
            var f = g
            f.translateBy(x: -10, y: dy * 0.8)
            f.rotate(by: .degrees(sign * (30 + flip)))
            f.fill(Path(ellipseIn: CGRect(x: -22, y: -8, width: 44, height: 16)), with: .color(c("#5F8F5A")))
        }
        g.fill(Path(ellipseIn: CGRect(x: 26, y: -10, width: 26, height: 20)), with: .color(c("#6F9F68")))
        g.fill(Path(ellipseIn: CGRect(x: -36, y: -28, width: 72, height: 56)), with: .color(c("#3F6B45")))
        var shell = Path()
        shell.move(to: CGPoint(x: -20, y: 0))
        shell.addLine(to: CGPoint(x: 0, y: -18))
        shell.addLine(to: CGPoint(x: 20, y: 0))
        shell.addLine(to: CGPoint(x: 0, y: 18))
        shell.closeSubpath()
        g.stroke(shell, with: .color(c("#8FBF7F")), lineWidth: 2)
        g.fill(Path(ellipseIn: CGRect(x: 42, y: -5, width: 4, height: 4)), with: .color(c("#10282C")))
    }

    // MARK: stars

    static func stars(night: Bool, pieces: [ScenePiece], t: Double, _ ctx: inout GraphicsContext, _ size: CGSize) {
        let w = size.width, h = size.height
        // The band of the galaxy across the sky.
        var band = ctx
        band.translateBy(x: w / 2, y: h * 0.4)
        band.rotate(by: .degrees(-18))
        // A circle squashed into a band, so the glow fades to nothing at
        // its edge every way round.
        let bw = w * 0.75
        band.scaleBy(x: 1, y: 0.28)
        band.fill(Path(ellipseIn: CGRect(x: -bw, y: -bw, width: bw * 2, height: bw * 2)),
                  with: .radialGradient(Gradient(colors: [c("#C4A7E7", night ? 0.22 : 0.14), c("#C4A7E7", night ? 0.08 : 0.05), c("#C4A7E7", 0)]),
                                        center: .zero, startRadius: 0, endRadius: bw))
        // By day (a dusk) only the brightest show, in the upper sky.
        for p in pieces where night || (p.size > 0.55 && p.y < 0.55) {
            let d = 1 + p.size * 2.2
            var g = ctx
            g.opacity = SceneMotion.twinkle(p, at: t) * (night ? 1 : 0.7)
            g.fill(Path(ellipseIn: CGRect(x: p.x * w, y: p.y * h, width: d, height: d)), with: .color(.white))
        }
        // A shooting star now and then: one in every eleven seconds, a second long.
        let cycle = (t / 11).truncatingRemainder(dividingBy: 1)
        if cycle < 0.12 {
            let k = cycle / 0.12
            var g = ctx
            g.opacity = k < 0.3 ? k / 0.3 : 1 - (k - 0.3) / 0.7
            g.translateBy(x: w * (0.92 - 0.4 * k), y: h * (0.1 + 0.28 * k))
            g.rotate(by: .degrees(-24))
            g.fill(Path(CGRect(x: 0, y: -1, width: 140, height: 2)),
                   with: .linearGradient(Gradient(colors: [.white, .white.opacity(0)]), startPoint: .zero, endPoint: CGPoint(x: 140, y: 0)))
        }
    }
}
