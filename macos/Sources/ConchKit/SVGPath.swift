import CoreGraphics
import Foundation

/// An SVG path's `d`, read into moves, lines, curves and closes in
/// absolute coordinates — what drawing Lucide's icons from their own
/// sources needs (lucide.dev's 24×24 paths, circles, rects and lines).
/// Arcs become cubic curves; a command it does not know ends the path
/// there, so a bad icon draws what it can rather than nothing.
public enum PathOp: Equatable, Sendable {
    case move(CGPoint)
    case line(CGPoint)
    case cubic(CGPoint, CGPoint, CGPoint) // control, control, end
    case quad(CGPoint, CGPoint) // control, end
    case close
}

public func parseSVGPath(_ d: String) -> [PathOp] {
    var ops: [PathOp] = []
    var tokens = svgTokens(d)[...]
    var cur = CGPoint.zero, start = CGPoint.zero
    var lastCubic: CGPoint?, lastQuad: CGPoint?
    var cmd: Character = "M"

    func num() -> CGFloat? {
        guard case .number(let n)? = tokens.first else { return nil }
        tokens.removeFirst()
        return n
    }
    func flag() -> Bool? {
        // Arc flags may be written run together ("a1 1 0 01 1 1"); the
        // tokenizer reads them as numbers, one digit at a time when they
        // are written as 0 or 1.
        guard let n = num() else { return nil }
        return n != 0
    }
    while true {
        if case .command(let c)? = tokens.first {
            tokens.removeFirst()
            cmd = c
            if c == "Z" || c == "z" {
                ops.append(.close)
                cur = start
                lastCubic = nil
                lastQuad = nil
                continue
            }
        } else if tokens.isEmpty {
            break
        }
        let rel = cmd.isLowercase
        let base = rel ? cur : .zero
        func pt(_ x: CGFloat, _ y: CGFloat) -> CGPoint { CGPoint(x: base.x + x, y: base.y + y) }
        switch cmd.uppercased().first! {
        case "M":
            guard let x = num(), let y = num() else { return ops }
            cur = pt(x, y)
            start = cur
            ops.append(.move(cur))
            cmd = rel ? "l" : "L" // further pairs are lines
            lastCubic = nil
            lastQuad = nil
        case "L":
            guard let x = num(), let y = num() else { return ops }
            cur = pt(x, y)
            ops.append(.line(cur))
            lastCubic = nil
            lastQuad = nil
        case "H":
            guard let x = num() else { return ops }
            cur = CGPoint(x: rel ? cur.x + x : x, y: cur.y)
            ops.append(.line(cur))
            lastCubic = nil
            lastQuad = nil
        case "V":
            guard let y = num() else { return ops }
            cur = CGPoint(x: cur.x, y: rel ? cur.y + y : y)
            ops.append(.line(cur))
            lastCubic = nil
            lastQuad = nil
        case "C":
            guard let x1 = num(), let y1 = num(), let x2 = num(), let y2 = num(), let x = num(), let y = num() else { return ops }
            let c1 = pt(x1, y1), c2 = pt(x2, y2), end = pt(x, y)
            ops.append(.cubic(c1, c2, end))
            lastCubic = c2
            lastQuad = nil
            cur = end
        case "S":
            guard let x2 = num(), let y2 = num(), let x = num(), let y = num() else { return ops }
            let c1 = lastCubic.map { CGPoint(x: 2 * cur.x - $0.x, y: 2 * cur.y - $0.y) } ?? cur
            let c2 = pt(x2, y2), end = pt(x, y)
            ops.append(.cubic(c1, c2, end))
            lastCubic = c2
            lastQuad = nil
            cur = end
        case "Q":
            guard let x1 = num(), let y1 = num(), let x = num(), let y = num() else { return ops }
            let c = pt(x1, y1), end = pt(x, y)
            ops.append(.quad(c, end))
            lastQuad = c
            lastCubic = nil
            cur = end
        case "T":
            guard let x = num(), let y = num() else { return ops }
            let c = lastQuad.map { CGPoint(x: 2 * cur.x - $0.x, y: 2 * cur.y - $0.y) } ?? cur
            let end = pt(x, y)
            ops.append(.quad(c, end))
            lastQuad = c
            lastCubic = nil
            cur = end
        case "A":
            guard let rx = num(), let ry = num(), let rot = num(), let large = flag(), let sweep = flag(),
                  let x = num(), let y = num() else { return ops }
            let end = pt(x, y)
            ops += arcToCubics(from: cur, to: end, rx: rx, ry: ry, rotation: rot, large: large, sweep: sweep)
            cur = end
            lastCubic = nil
            lastQuad = nil
        default:
            return ops
        }
    }
    return ops
}

private enum SVGToken: Equatable { case command(Character), number(CGFloat) }

/// The commands and numbers of a `d`, numbers as SVG writes them: "-.5",
/// "1e-3", "1.5.5" (two numbers), "0 01" (arc flags run together).
private func svgTokens(_ d: String) -> [SVGToken] {
    var out: [SVGToken] = []
    let chars = Array(d)
    var i = 0
    var lastCmd: Character = " "
    var argIndex = 0
    while i < chars.count {
        let c = chars[i]
        if c.isLetter && c != "e" && c != "E" {
            out.append(.command(c))
            lastCmd = c
            argIndex = 0
            i += 1
            continue
        }
        if c == " " || c == "," || c == "\n" || c == "\t" {
            i += 1
            continue
        }
        // An arc's two flags are single digits, and may have no space.
        if lastCmd == "a" || lastCmd == "A", argIndex % 7 == 3 || argIndex % 7 == 4, c == "0" || c == "1" {
            out.append(.number(c == "1" ? 1 : 0))
            argIndex += 1
            i += 1
            continue
        }
        var j = i
        var s = ""
        if chars[j] == "-" || chars[j] == "+" { s.append(chars[j]); j += 1 }
        var dot = false
        while j < chars.count {
            let d = chars[j]
            if d.isNumber { s.append(d); j += 1 }
            else if d == "." && !dot { dot = true; s.append(d); j += 1 }
            else if (d == "e" || d == "E") && !s.isEmpty {
                s.append(d); j += 1
                if j < chars.count, chars[j] == "-" || chars[j] == "+" { s.append(chars[j]); j += 1 }
            } else { break }
        }
        if let n = Double(s) {
            out.append(.number(CGFloat(n)))
            argIndex += 1
            i = max(j, i + 1)
        } else {
            i += 1 // a stray character: skip it
        }
    }
    return out
}

/// An elliptical arc as cubic curves (the SVG implementation notes' way:
/// to the centre form, then a curve per quarter turn at most).
func arcToCubics(from p0: CGPoint, to p1: CGPoint, rx rxIn: CGFloat, ry ryIn: CGFloat,
                 rotation: CGFloat, large: Bool, sweep: Bool) -> [PathOp] {
    if p0 == p1 { return [] }
    var rx = abs(rxIn), ry = abs(ryIn)
    if rx == 0 || ry == 0 { return [.line(p1)] }
    let phi = rotation * .pi / 180
    let cosP = cos(phi), sinP = sin(phi)
    let dx = (p0.x - p1.x) / 2, dy = (p0.y - p1.y) / 2
    let x1 = cosP * dx + sinP * dy, y1 = -sinP * dx + cosP * dy
    let lambda = (x1 * x1) / (rx * rx) + (y1 * y1) / (ry * ry)
    if lambda > 1 { rx *= sqrt(lambda); ry *= sqrt(lambda) }
    let num = max(rx * rx * ry * ry - rx * rx * y1 * y1 - ry * ry * x1 * x1, 0)
    let den = rx * rx * y1 * y1 + ry * ry * x1 * x1
    var coef = den == 0 ? 0 : sqrt(num / den)
    if large == sweep { coef = -coef }
    let cx1 = coef * rx * y1 / ry, cy1 = -coef * ry * x1 / rx
    let cx = cosP * cx1 - sinP * cy1 + (p0.x + p1.x) / 2
    let cy = sinP * cx1 + cosP * cy1 + (p0.y + p1.y) / 2
    func angle(_ ux: CGFloat, _ uy: CGFloat, _ vx: CGFloat, _ vy: CGFloat) -> CGFloat {
        let a = atan2(ux * vy - uy * vx, ux * vx + uy * vy)
        return a
    }
    let theta1 = angle(1, 0, (x1 - cx1) / rx, (y1 - cy1) / ry)
    var dTheta = angle((x1 - cx1) / rx, (y1 - cy1) / ry, (-x1 - cx1) / rx, (-y1 - cy1) / ry)
    if !sweep && dTheta > 0 { dTheta -= 2 * .pi }
    if sweep && dTheta < 0 { dTheta += 2 * .pi }
    let segments = max(Int(ceil(abs(dTheta) / (.pi / 2))), 1)
    let delta = dTheta / CGFloat(segments)
    let t = 4 / 3 * tan(delta / 4)
    var ops: [PathOp] = []
    var th = theta1
    func point(_ a: CGFloat) -> CGPoint {
        CGPoint(x: cx + rx * cos(a) * cosP - ry * sin(a) * sinP, y: cy + rx * cos(a) * sinP + ry * sin(a) * cosP)
    }
    func deriv(_ a: CGFloat) -> CGPoint {
        CGPoint(x: -rx * sin(a) * cosP - ry * cos(a) * sinP, y: -rx * sin(a) * sinP + ry * cos(a) * cosP)
    }
    for i in 0..<segments {
        let a0 = th, a1 = th + delta
        let s = point(a0), e = i == segments - 1 ? p1 : point(a1)
        let d0 = deriv(a0), d1 = deriv(a1)
        ops.append(.cubic(CGPoint(x: s.x + t * d0.x, y: s.y + t * d0.y), CGPoint(x: e.x - t * d1.x, y: e.y - t * d1.y), e))
        th = a1
    }
    return ops
}

/// A circle, rect or line of an SVG icon as a path `d`, so every element
/// is drawn the one way.
public func svgCircle(_ cx: CGFloat, _ cy: CGFloat, _ r: CGFloat) -> String {
    "M\(cx - r) \(cy)a\(r) \(r) 0 1 0 \(2 * r) 0a\(r) \(r) 0 1 0 \(-2 * r) 0"
}

public func svgRect(_ x: CGFloat, _ y: CGFloat, _ w: CGFloat, _ h: CGFloat, _ r: CGFloat = 0) -> String {
    if r <= 0 { return "M\(x) \(y)h\(w)v\(h)h\(-w)Z" }
    return "M\(x + r) \(y)h\(w - 2 * r)a\(r) \(r) 0 0 1 \(r) \(r)v\(h - 2 * r)a\(r) \(r) 0 0 1 \(-r) \(r)h\(-(w - 2 * r))a\(r) \(r) 0 0 1 \(-r) \(-r)v\(-(h - 2 * r))a\(r) \(r) 0 0 1 \(r) \(-r)Z"
}

public func svgLine(_ x1: CGFloat, _ y1: CGFloat, _ x2: CGFloat, _ y2: CGFloat) -> String { "M\(x1) \(y1)L\(x2) \(y2)" }
