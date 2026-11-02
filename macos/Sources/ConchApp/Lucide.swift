import ConchKit
import SwiftUI

// The sidebar's icons: Lucide's (lucide.dev), the set shadcn/ui ships, so
// the tree reads as one family of outlines — each drawn from Lucide's own
// 24×24 paths, a corner of radius 2 as a quadratic curve and an arc by its
// points, stroked at an even width.

enum LucideName {
    case folder, folderOpen, gitBranch, terminal, squareTerminal, history, laptop, server, cloud, globe, layers
}

struct Lucide: Shape {
    let name: LucideName

    func path(in r: CGRect) -> Path {
        let k = min(r.width, r.height) / 24
        let ox = r.minX + (r.width - 24 * k) / 2, oy = r.minY + (r.height - 24 * k) / 2
        func p(_ x: CGFloat, _ y: CGFloat) -> CGPoint { CGPoint(x: ox + x * k, y: oy + y * k) }
        var path = Path()
        // An arc by its points, from one angle to another in degrees on
        // the screen (y down), the way it is written.
        func arc(_ cx: CGFloat, _ cy: CGFloat, _ rad: CGFloat, _ from: CGFloat, _ to: CGFloat, move: Bool = false) {
            let steps = max(Int(abs(to - from) / 6), 2)
            for i in 0...steps {
                let a = (from + (to - from) * CGFloat(i) / CGFloat(steps)) * .pi / 180
                let pt = p(cx + rad * cos(a), cy + rad * sin(a))
                if i == 0 && move { path.move(to: pt) } else { path.addLine(to: pt) }
            }
        }
        func circle(_ cx: CGFloat, _ cy: CGFloat, _ rad: CGFloat) {
            path.addEllipse(in: CGRect(x: ox + (cx - rad) * k, y: oy + (cy - rad) * k, width: 2 * rad * k, height: 2 * rad * k))
        }
        func rrect(_ x: CGFloat, _ y: CGFloat, _ w: CGFloat, _ h: CGFloat, _ rad: CGFloat) {
            path.addRoundedRect(in: CGRect(x: ox + x * k, y: oy + y * k, width: w * k, height: h * k), cornerSize: CGSize(width: rad * k, height: rad * k))
        }
        func line(_ pts: (CGFloat, CGFloat)...) {
            path.move(to: p(pts[0].0, pts[0].1))
            for q in pts.dropFirst() { path.addLine(to: p(q.0, q.1)) }
        }
        switch name {
        case .folder:
            line((4, 3), (7.93, 3))
            path.addQuadCurve(to: p(9.6, 3.9), control: p(9, 3))
            path.addLine(to: p(10.41, 5.1))
            path.addQuadCurve(to: p(12.1, 6), control: p(11, 6))
            path.addLine(to: p(20, 6))
            path.addQuadCurve(to: p(22, 8), control: p(22, 6))
            path.addLine(to: p(22, 18))
            path.addQuadCurve(to: p(20, 20), control: p(22, 20))
            path.addLine(to: p(4, 20))
            path.addQuadCurve(to: p(2, 18), control: p(2, 20))
            path.addLine(to: p(2, 5))
            path.addQuadCurve(to: p(4, 3), control: p(2, 3))
            path.closeSubpath()
        case .folderOpen:
            line((6, 14), (7.5, 11.1))
            path.addQuadCurve(to: p(9.24, 10), control: p(8, 10))
            path.addLine(to: p(20, 10))
            path.addQuadCurve(to: p(21.94, 12.5), control: p(22.4, 10))
            path.addLine(to: p(20.4, 18.5))
            path.addQuadCurve(to: p(18.45, 20), control: p(20, 20))
            path.addLine(to: p(4, 20))
            path.addQuadCurve(to: p(2, 18), control: p(2, 20))
            path.addLine(to: p(2, 5))
            path.addQuadCurve(to: p(4, 3), control: p(2, 3))
            path.addLine(to: p(7.9, 3))
            path.addQuadCurve(to: p(9.59, 3.9), control: p(9, 3))
            path.addLine(to: p(10.4, 5.1))
            path.addQuadCurve(to: p(12.07, 6), control: p(11, 6))
            path.addLine(to: p(18, 6))
            path.addQuadCurve(to: p(20, 8), control: p(20, 6))
            path.addLine(to: p(20, 10))
        case .gitBranch:
            line((6, 3), (6, 15))
            circle(18, 6, 3)
            circle(6, 18, 3)
            arc(9, 9, 9, 0, 90, move: true) // M18 9 a9 9 0 0 1 -9 9
        case .terminal:
            line((4, 17), (10, 11), (4, 5))
            line((12, 19), (20, 19))
        case .squareTerminal:
            line((7, 11), (9, 9), (7, 7))
            line((11, 13), (15, 13))
            rrect(3, 3, 18, 18, 2)
        case .history:
            arc(12, 12, 9, 180, -135, move: true) // round from the left, the long way
            path.addLine(to: p(3, 8))
            line((3, 3), (3, 8), (8, 8))
            line((12, 7), (12, 12), (16, 14))
        case .laptop:
            line((20, 16), (20, 7))
            path.addQuadCurve(to: p(18, 5), control: p(20, 5))
            path.addLine(to: p(6, 5))
            path.addQuadCurve(to: p(4, 7), control: p(4, 5))
            path.addLine(to: p(4, 16))
            line((2.72, 18.55), (4, 16), (20, 16), (21.28, 18.55))
            path.addQuadCurve(to: p(20.38, 20), control: p(21.6, 20))
            path.addLine(to: p(3.62, 20))
            path.addQuadCurve(to: p(2.72, 18.55), control: p(2.4, 20))
        case .server:
            rrect(2, 2, 20, 8, 2)
            rrect(2, 14, 20, 8, 2)
            line((6, 6), (6.01, 6))
            line((6, 18), (6.01, 18))
        case .cloud:
            line((17.5, 19), (9, 19))
            arc(9, 12, 7, 90, 343.4)
            path.addLine(to: p(17.5, 10))
            arc(17.5, 14.5, 4.5, -90, 90)
            path.closeSubpath()
        case .layers:
            // Lucide's layers: a diamond, and two more edges under it.
            line((12, 2.5), (22, 7.5), (12, 12.5), (2, 7.5))
            path.closeSubpath()
            line((2, 12.5), (12, 17.5), (22, 12.5))
            line((2, 17), (12, 22), (22, 17))
        case .globe:
            circle(12, 12, 10)
            path.addEllipse(in: CGRect(x: ox + 7.5 * k, y: oy + 2 * k, width: 9 * k, height: 20 * k))
            line((2, 12), (22, 12))
        }
        return path
    }
}

/// A Lucide icon in the sidebar's column, stroked as shadcn/ui draws them.
struct LucideIcon: View {
    let name: LucideName
    var color: Color = Theme.text2
    var size: CGFloat = 13

    var body: some View {
        Lucide(name: name)
            .stroke(color, style: StrokeStyle(lineWidth: 1.3, lineCap: .round, lineJoin: .round))
            .frame(width: size, height: size).frame(width: 14)
    }
}

/// A machine: a laptop for this computer, a server for another, a cloud
/// for a sandbox — and its state as a dot on the corner: green online,
/// amber on the way, grey not there.
struct MachineIcon: View {
    let name: LucideName
    let state: Color

    var body: some View {
        LucideIcon(name: name, color: Theme.text2, size: 14)
            .overlay(alignment: .bottomTrailing) {
                Circle().fill(state).frame(width: 6, height: 6)
                    .overlay(Circle().stroke(Theme.side, lineWidth: 1.5))
                    .offset(x: 2, y: 1)
            }
    }
}
