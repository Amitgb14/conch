import ConchKit
import SwiftUI

// The Ink style's pieces (AppStyle.ink): ink is the theme's text colour,
// the spot colour its accent, the paper its background, so the style reads
// in any theme and as drawn in the Ink one.

extension Theme {
    static var ink: Bool { style == .ink }
    /// The ink lines are drawn in.
    static var inkLine: Color { text }
    /// The one colour a page is allowed besides ink.
    static var spot: Color { accentDeep }
}

/// Screentone: a grid of small dots, the only shading a manga page has.
struct Screentone: View {
    var spacing: CGFloat = 7
    var body: some View {
        // Colours are read here, on the main actor; the canvas only draws.
        // Light enough that the tree's muted text still reads over it.
        let dot = Theme.line.opacity(0.32)
        return Canvas { ctx, size in
            var y: CGFloat = spacing / 2
            while y < size.height {
                var x: CGFloat = spacing / 2
                while x < size.width {
                    ctx.fill(Path(ellipseIn: CGRect(x: x - 0.8, y: y - 0.8, width: 1.6, height: 1.6)), with: .color(dot))
                    x += spacing
                }
                y += spacing
            }
        }
        .allowsHitTesting(false)
    }
}

/// A lettering burst around a count (burstPoints).
struct BurstShape: Shape {
    func path(in r: CGRect) -> Path {
        let pts = burstPoints(width: r.width, height: r.height, spikes: 11, depth: 0.22)
        var p = Path()
        for (i, pt) in pts.enumerated() {
            let q = CGPoint(x: r.minX + pt.x, y: r.minY + pt.y)
            if i == 0 { p.move(to: q) } else { p.addLine(to: q) }
        }
        p.closeSubpath()
        return p
    }
}

/// Lettering: a heavy, slanted face, as a manga's sound effects and counts.
struct Lettering: View {
    let text: String
    var size: CGFloat = 13
    var color: Color = Theme.spot

    var body: some View {
        Text(text.uppercased()).font(.system(size: size, weight: .black).italic()).tracking(0.4)
            .foregroundStyle(color)
            .lineLimit(1).fixedSize()
    }
}

/// A count in a burst: the spot colour for what is waiting, paper for
/// what is done.
struct BurstCount: View {
    let text: String
    let loud: Bool
    /// The theme's colours, as values: the toolbar is outside the window's
    /// repaint, and a burst whose properties are unchanged is not drawn
    /// again, so colours read inside it stayed the old theme's.
    var ink = Theme.inkLine, spot = Theme.spot, paper = Theme.window, onSpot = Theme.selectedText

    var body: some View {
        Lettering(text: text, size: 12, color: loud ? onSpot : ink)
            .padding(.horizontal, 16).padding(.vertical, 7)
            .background(BurstShape().fill(loud ? spot : paper))
            .overlay(BurstShape().stroke(ink, lineWidth: 2))
    }
}

/// A speech bubble: a round-ended balloon with a tail at its bottom left
/// pointing down into whoever said it, filled with paper and inked, the
/// tail open where it meets the balloon.
struct InkBubble: View {
    var tail: CGFloat = 12

    var body: some View {
        let paper = GraphicsContext.Shading.color(Theme.window)
        let ink = GraphicsContext.Shading.color(Theme.inkLine)
        let tail = self.tail
        return Canvas { ctx, size in
            let body = CGRect(x: 1, y: 1, width: size.width - 2, height: size.height - tail - 2)
            let balloon = Path(roundedRect: body, cornerRadius: min(body.height / 2, 22))
            let x = body.minX + 34
            var tip = Path()
            tip.move(to: CGPoint(x: x, y: body.maxY))
            tip.addLine(to: CGPoint(x: x - 5, y: size.height - 1))
            tip.addLine(to: CGPoint(x: x + 14, y: body.maxY))
            ctx.fill(balloon, with: paper)
            ctx.stroke(balloon, with: ink, lineWidth: 2.5)
            // The tail: paper over the balloon's edge where it opens, then
            // its two sides.
            var gap = tip
            gap.closeSubpath()
            ctx.fill(gap.offsetBy(dx: 0, dy: -2), with: paper)
            ctx.fill(gap, with: paper)
            ctx.stroke(tip, with: ink, style: StrokeStyle(lineWidth: 2.5, lineJoin: .round))
        }
        .allowsHitTesting(false)
    }
}

extension View {
    /// A panel on the page: square, inked, and the focused one lifted off
    /// it by an offset shadow in the spot colour.
    func inkPanel(focused: Bool) -> some View {
        self
            .overlay(Rectangle().strokeBorder(Theme.inkLine, lineWidth: AppStyle.ink.border))
            .background(Rectangle().fill(focused ? Theme.spot : .clear).offset(x: 5, y: 5))
    }

    /// An inked box around a control: square, a 2pt line.
    func inkBox(fill: Color = .clear) -> some View {
        self.background(Rectangle().fill(fill)).overlay(Rectangle().strokeBorder(Theme.inkLine, lineWidth: 2))
    }
}
