import ConchKit
import SwiftUI

// The Meadow style's pieces (AppStyle.meadow): a sky painted behind the
// toolbar, a strip of grass along the status bar, rounder and softer
// cards. By day under a light theme; under a dark one the sky is night,
// with a moon, stars and fireflies (the Firefly theme is its pair).

extension Theme {
    static var meadow: Bool { style == .meadow }
    /// A corner's radius in the style: the standard one, square in Ink,
    /// rounder in Meadow.
    static func corner(_ standard: CGFloat, meadow: CGFloat, cel: CGFloat = 9) -> CGFloat {
        ink ? 0 : self.meadow ? meadow : self.cel ? cel : standard
    }
    /// Night, for the sky and the grass: a dark theme.
    static var night: Bool { !current.light }

    static var skyTop: Color { night ? Color(nsColor: NSColor(hex: 0x1B2440)) : Color(nsColor: NSColor(hex: 0xA9D8EF)) }
    static var skyBottom: Color { night ? Color(nsColor: NSColor(hex: 0x2B3A52)) : Color(nsColor: NSColor(hex: 0xDFF0E6)) }
    static var grass: Color { night ? Color(nsColor: NSColor(hex: 0x2F4A2C)) : Color(nsColor: NSColor(hex: 0x7FB05F)) }
    static var grassDeep: Color { night ? Color(nsColor: NSColor(hex: 0x243A22)) : Color(nsColor: NSColor(hex: 0x5F9A48)) }
    /// What a control on the sky is set in: a pane of light frosting.
    static var frost: Color { night ? Color.white.opacity(0.09) : Color.white.opacity(0.5) }
    /// The yellow of the sun, and of a firefly.
    static var sun: Color { night ? Color(nsColor: NSColor(hex: 0xF2D46B)) : Color(nsColor: NSColor(hex: 0xFFF3C4)) }
}

/// The sky behind the toolbar: a gradient with soft clouds and the sun by
/// day; by night a moon, a few stars and fireflies. All drawn, no artwork.
struct Sky: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    var body: some View {
        let _ = look
        // Colours are read here, on the main actor; the canvas only draws.
        let top = Theme.skyTop, bottom = Theme.skyBottom, sun = Theme.sun, night = Theme.night
        return Canvas { ctx, size in
            let r = CGRect(origin: .zero, size: size)
            ctx.fill(Path(r), with: .linearGradient(Gradient(colors: [top, bottom]), startPoint: .zero, endPoint: CGPoint(x: 0, y: size.height)))
            if night {
                for (i, c) in cloudLayout(width: size.width * 2).enumerated() {
                    // Stars: two per cloud's place, small.
                    let p = CGPoint(x: c.x * size.width, y: (0.15 + Double(i % 3) * 0.25) * size.height)
                    ctx.fill(Path(ellipseIn: CGRect(x: p.x, y: p.y, width: 1.6, height: 1.6)), with: .color(.white.opacity(0.7)))
                }
                var glow = ctx
                glow.addFilter(.blur(radius: 2.5))
                for (i, c) in cloudLayout(width: size.width).enumerated() where i % 2 == 0 {
                    let p = CGPoint(x: c.x * size.width + 40, y: c.y * size.height + 6)
                    glow.fill(Path(ellipseIn: CGRect(x: p.x - 3, y: p.y - 3, width: 6, height: 6)), with: .color(sun.opacity(0.8)))
                }
                let moon = CGRect(x: size.width * 0.8, y: size.height * 0.18, width: 22, height: 22)
                ctx.fill(Path(ellipseIn: moon), with: .color(Color(white: 0.96)))
            } else {
                var soft = ctx
                soft.addFilter(.blur(radius: 9))
                soft.fill(Path(ellipseIn: CGRect(x: size.width * 0.8 - 20, y: -14, width: 60, height: 60)), with: .color(sun))
                for c in cloudLayout(width: size.width) {
                    let w = c.w * size.width, h = c.h * size.height
                    let cloud = CGRect(x: c.x * size.width - w / 2, y: c.y * size.height - h / 2, width: w, height: h)
                    soft.fill(Path(ellipseIn: cloud), with: .color(.white.opacity(0.75)))
                }
            }
        }
        .allowsHitTesting(false)
    }
}

/// A strip of grass: a row of round tufts along the top of the status bar.
struct GrassEdge: Shape {
    var tuft: CGFloat = 14

    func path(in r: CGRect) -> Path {
        var p = Path()
        p.move(to: CGPoint(x: r.minX, y: r.maxY))
        var x = r.minX
        while x < r.maxX {
            p.addQuadCurve(to: CGPoint(x: min(x + tuft, r.maxX), y: r.maxY), control: CGPoint(x: x + tuft / 2, y: r.minY - r.height * 0.6))
            x += tuft
        }
        p.addLine(to: CGPoint(x: r.maxX, y: r.maxY))
        p.closeSubpath()
        return p
    }
}

extension View {
    /// A card in the meadow: soft corners, a warm shadow, and the focused
    /// one ringed in the accent.
    func meadowCard(focused: Bool) -> some View {
        let shape = RoundedRectangle(cornerRadius: AppStyle.meadow.corner)
        return self
            .overlay(shape.strokeBorder(focused ? Theme.accentDeep : Theme.line, lineWidth: focused ? 1.5 : 1))
            .background(shape.stroke(focused ? Theme.accentDeep.opacity(0.18) : .clear, lineWidth: 8))
            .shadow(color: Color(nsColor: NSColor(hex: 0x6B5A2A)).opacity(Theme.night ? 0 : 0.14), radius: 6, y: 3)
    }

    /// A note pinned in the meadow: rounded, on the page's colour, a dashed
    /// amber edge.
    func meadowNote() -> some View {
        let shape = RoundedRectangle(cornerRadius: 14)
        return self.background(shape.fill(Theme.window))
            .overlay(shape.strokeBorder(Theme.warn, style: StrokeStyle(lineWidth: 1.5, dash: [5, 4])))
    }
}
