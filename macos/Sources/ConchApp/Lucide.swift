import ConchKit
import SwiftUI

// The sidebar's icons: Lucide's (lucide.dev), the set shadcn/ui ships, so
// the tree reads as one family of outlines — each drawn from Lucide's own
// 24×24 paths, a corner of radius 2 as a quadratic curve and an arc by its
// points, stroked at an even width.

/// The tree's icons by what they stand for, each a Lucide name.
enum LucideName: String {
    case folder, folderOpen = "folder-open", gitBranch = "git-branch", terminal, squareTerminal = "square-terminal", history,
         laptop, server, cloud, globe, layers
}

/// A Lucide icon drawn from its own path data (ConchKit's LucideIcons,
/// read by parseSVGPath), scaled from its 24×24 box to fit.
struct Lucide: Shape {
    let icon: String

    init(name: LucideName) { icon = name.rawValue }
    init(_ icon: String) { self.icon = icon }

    func path(in r: CGRect) -> Path {
        let k = min(r.width, r.height) / 24
        let ox = r.minX + (r.width - 24 * k) / 2, oy = r.minY + (r.height - 24 * k) / 2
        func p(_ q: CGPoint) -> CGPoint { CGPoint(x: ox + q.x * k, y: oy + q.y * k) }
        var path = Path()
        for op in LucideIcons.ops(icon) {
            switch op {
            case .move(let q): path.move(to: p(q))
            case .line(let q): path.addLine(to: p(q))
            case .cubic(let c1, let c2, let e): path.addCurve(to: p(e), control1: p(c1), control2: p(c2))
            case .quad(let c, let e): path.addQuadCurve(to: p(e), control: p(c))
            case .close: path.closeSubpath()
            }
        }
        return path
    }
}

/// A Lucide icon in the sidebar's column, stroked as shadcn/ui draws them.
struct LucideIcon: View {
    let icon: String
    var color: Color
    var size: CGFloat

    @MainActor init(name: LucideName, color: Color? = nil, size: CGFloat = 13) {
        icon = name.rawValue
        self.color = color ?? Theme.text2
        self.size = size
    }

    @MainActor init(_ icon: String, color: Color? = nil, size: CGFloat = 13) {
        self.icon = icon
        self.color = color ?? Theme.text2
        self.size = size
    }

    var body: some View {
        Lucide(icon)
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
