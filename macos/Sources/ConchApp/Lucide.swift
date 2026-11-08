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
    let ops: [PathOp]

    init(name: LucideName) { ops = LucideIcons.ops(name.rawValue) }
    init(_ icon: String) { ops = LucideIcons.ops(icon) }
    /// Any 24×24 path already read: an agent's logo (AgentLogos).
    init(ops: [PathOp]) { self.ops = ops }

    func path(in r: CGRect) -> Path {
        let k = min(r.width, r.height) / 24
        let ox = r.minX + (r.width - 24 * k) / 2, oy = r.minY + (r.height - 24 * k) / 2
        func p(_ q: CGPoint) -> CGPoint { CGPoint(x: ox + q.x * k, y: oy + q.y * k) }
        var path = Path()
        for op in ops {
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
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
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
        let _ = look
        Lucide(icon)
            .stroke(color, style: StrokeStyle(lineWidth: 1.3, lineCap: .round, lineJoin: .round))
            .frame(width: size, height: size).frame(width: 14)
    }
}

/// A sidebar row's icon as [app] icons and icon_style say — the choice
/// Settings' Icons card makes for its own sections, which the tree
/// follows too. Lucide plain, the default, is LucideIcon as it always was.
struct TreeIcon: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let icon: String
    let set: SettingsIconSet
    let style: SettingsIconStyle
    var color: Color
    var size: CGFloat

    @MainActor init(name: LucideName, look: AppLook, color: Color? = nil, size: CGFloat = 13) {
        self.init(name.rawValue, look: look, color: color, size: size)
    }

    @MainActor init(_ icon: String, look: AppLook, color: Color? = nil, size: CGFloat = 13) {
        self.icon = icon
        set = look.iconSet
        style = look.iconStyle
        self.color = color ?? Theme.text2
        self.size = size
    }

    var body: some View {
        let _ = look
        if set == .lucide && style == .plain {
            LucideIcon(icon, color: color, size: size)
        } else {
            // A column as wide on every row, so the names still line up:
            // a tile is drawn larger than its glyph.
            SectionIcon(icon: icon, set: set, style: style, color: color, size: style == .tiles ? size * 0.92 : size)
                .frame(width: style == .tiles ? 18 : 15)
        }
    }
}

/// A machine: a laptop for this computer, a server for another, a cloud
/// for a sandbox — and its state as a dot on the corner: green online,
/// amber on the way, grey not there.
struct MachineIcon: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let name: LucideName
    let state: Color
    var app = AppLook()

    var body: some View {
        let _ = look
        TreeIcon(name: name, look: app, color: Theme.text2, size: 14)
            .overlay(alignment: .bottomTrailing) {
                Circle().fill(state).frame(width: 6, height: 6)
                    .overlay(Circle().stroke(Theme.side, lineWidth: 1.5))
                    .offset(x: 2, y: 1)
            }
    }
}

/// A Settings section's icon as [app] icons and icon_style say: Lucide's
/// outline or the SF Symbol for it, plain in color, or white on a tile of
/// the section's own colour.
struct SectionIcon: View {
    @Environment(\.conchTheme) private var look
    let icon: String
    let set: SettingsIconSet
    let style: SettingsIconStyle
    /// The plain glyph's colour; a tile's is white.
    var color: Color
    /// The glyph's size; a tile is a little larger around it.
    var size: CGFloat

    var body: some View {
        let _ = look
        switch style {
        case .plain:
            glyph(color: color, size: size)
        case .tinted:
            glyph(color: Theme.color(settingsTileColor(lucide: icon)), size: size)
        case .tiles:
            let tile = size * 1.45
            glyph(color: .white, size: size * 0.82)
                .frame(width: tile, height: tile)
                .background(
                    RoundedRectangle(cornerRadius: tile * 0.26, style: .continuous)
                        .fill(LinearGradient(colors: [Theme.color(settingsTileGradient(lucide: icon).top), Theme.color(settingsTileGradient(lucide: icon).bottom)],
                                             startPoint: .top, endPoint: .bottom))
                        .shadow(color: .black.opacity(0.35), radius: 1, y: 1))
        }
    }

    /// The SF Symbol, filled on a tile where the Mac has a filled one.
    static func symbol(_ icon: String, filled: Bool) -> String {
        let base = sfSymbol(lucide: icon)
        return filled && NSImage(systemSymbolName: base + ".fill", accessibilityDescription: nil) != nil ? base + ".fill" : base
    }

    @ViewBuilder func glyph(color: Color, size: CGFloat) -> some View {
        switch set {
        case .lucide:
            // Its own frame, not LucideIcon's 14-point column: the cards
            // and tiles draw it larger than a tree row does.
            Lucide(icon)
                .stroke(color, style: StrokeStyle(lineWidth: max(1.3, size / 11), lineCap: .round, lineJoin: .round))
                .frame(width: size, height: size)
        case .sf, .sfFill:
            Image(systemName: Self.symbol(icon, filled: set == .sfFill))
                .symbolRenderingMode(.monochrome)
                .font(.system(size: size * 0.92, weight: .regular))
                .foregroundStyle(color)
                .frame(width: size + 2, height: size + 2)
        }
    }
}
