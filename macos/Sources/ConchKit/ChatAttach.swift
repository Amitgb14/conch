import Foundation

// MARK: - attachments to a chat message

/// The limits on one message's attachments: the server's
/// (proto.AttachMaxSize, proto.AttachMaxFiles).
public let attachMaxSize: Int64 = 20 << 20
public let attachMaxFiles = 10

/// A file put on a message before it is sent: a picture pasted, a file
/// dropped from Finder or picked, or one of the checkout's own picked
/// with @. Files in the checkout are never copied — the agent is given
/// their path — and the rest are copied into the checkout's
/// .conch/attachments when the message goes.
public struct ChatAttachment: Identifiable, Equatable, Sendable {
    public enum Source: Equatable, Sendable {
        /// In the agent's checkout, by its path relative to the checkout.
        case checkout(String)
        /// A file on this Mac, by its absolute path.
        case file(String)
        /// Bytes held here: a pasted picture.
        case data(Data)
    }

    public let id: UUID
    public var name: String
    public var size: Int64
    public var source: Source

    public init(id: UUID = UUID(), name: String, size: Int64, source: Source) {
        self.id = id
        self.name = name
        self.size = size
        self.source = source
    }

    public var image: Bool { isImageName(name) }

    /// Where it came from, as its chip says under the name.
    public var origin: String {
        switch source {
        case .checkout: return "in this checkout"
        case .file: return "from Finder"
        case .data: return "pasted"
        }
    }

    /// Whether it has to be copied to the agent rather than named.
    public var copied: Bool {
        if case .checkout = source { return false }
        return true
    }
}

/// Why an attachment will not be sent, or nil: over the size, or past the
/// number a message takes (by its place in the tray, the first ten go).
/// Its chip turns amber and says so.
public func attachmentProblem(_ list: [ChatAttachment], _ a: ChatAttachment) -> String? {
    if a.copied && a.size > attachMaxSize { return "over \(fileSize(attachMaxSize)) · not sent" }
    let fits = list.filter { !($0.copied && $0.size > attachMaxSize) }
    if let i = fits.firstIndex(where: { $0.id == a.id }), i >= attachMaxFiles {
        return "over \(attachMaxFiles) files · not sent"
    }
    return nil
}

/// The attachments that go with a message: those without a problem.
public func attachmentsToSend(_ list: [ChatAttachment]) -> [ChatAttachment] {
    list.filter { attachmentProblem(list, $0) == nil }
}

private let imageExtensions: Set<String> = ["png", "jpg", "jpeg", "gif", "webp", "heic", "bmp", "tif", "tiff"]

/// Whether a file is a picture, by its name.
public func isImageName(_ name: String) -> Bool {
    imageExtensions.contains((name as NSString).pathExtension.lowercased())
}

/// A file's name as it is copied into the checkout: what the agent is
/// given is a path in its prompt, so no spaces or quotes, which an agent's
/// @ would stop at. Letters, digits, ".", "-" and "_" are kept, any run of
/// anything else is one "-"; never hidden, never empty, never long.
public func attachmentFileName(_ raw: String) -> String {
    var out = ""
    var dash = false
    for ch in raw.unicodeScalars {
        if CharacterSet.alphanumerics.contains(ch) || ch == "." || ch == "_" || ch == "-" {
            out.unicodeScalars.append(ch)
            dash = false
        } else if !dash {
            out += "-"
            dash = true
        }
    }
    while out.hasPrefix(".") || out.hasPrefix("-") { out.removeFirst() }
    while out.hasSuffix("-") || out.hasSuffix(".") { out.removeLast() }
    if out.isEmpty { return "attachment" }
    if out.count > 100 {
        let ext = (out as NSString).pathExtension
        let keep = ext.isEmpty || ext.count > 10 ? "" : "." + ext
        out = String(out.prefix(100 - keep.count)) + keep
    }
    return out
}

/// Names made different from one another, a second "a.png" as "a-2.png":
/// a message's copies share one folder.
public func uniqueNames(_ names: [String]) -> [String] {
    var seen = Set<String>()
    return names.map { n in
        var name = n, i = 2
        let ext = (n as NSString).pathExtension, stem = (n as NSString).deletingPathExtension
        while seen.contains(name.lowercased()) {
            name = ext.isEmpty ? "\(stem)-\(i)" : "\(stem)-\(i).\(ext)"
            i += 1
        }
        seen.insert(name.lowercased())
        return name
    }
}

/// The name for a pasted picture: a screenshot by when it was taken.
public func pastedImageName(at d: Date, n: Int = 1) -> String {
    let f = DateFormatter()
    f.locale = Locale(identifier: "en_US_POSIX")
    f.dateFormat = "yyyy-MM-dd 'at' HH.mm.ss"
    return "Pasted \(f.string(from: d))\(n > 1 ? " \(n)" : "").png"
}

/// The checkout an agent's folder is in: the deepest of the project's
/// main checkout and its worktrees holding it, or nil.
public func chatCheckout(cwd: String?, project: ProjectInfo?) -> String? {
    guard let cwd, !cwd.isEmpty, let project else { return nil }
    let cands = [project.path] + (project.worktrees ?? []).map(\.path)
    return cands.filter { !$0.isEmpty && isInside(cwd, $0) }.max { $0.count < $1.count }
}

/// Whether path is folder or inside it.
public func isInside(_ path: String, _ folder: String) -> Bool {
    let f = folder.hasSuffix("/") && folder.count > 1 ? String(folder.dropLast()) : folder
    return path == f || path.hasPrefix(f == "/" ? "/" : f + "/")
}

/// A path as the agent in dir is given it: relative where it is inside,
/// as written in a checkout; absolute where it is not.
public func pathFor(agentIn dir: String, _ path: String) -> String {
    guard isInside(path, dir) else { return path }
    let f = dir.hasSuffix("/") && dir.count > 1 ? String(dir.dropLast()) : dir
    let rest = String(path.dropFirst(f.count)).drop { $0 == "/" }
    return rest.isEmpty ? "." : String(rest)
}

/// A file named in a message: where the agent finds it, and whether it
/// is a picture.
public struct AttachedRef: Equatable, Sendable {
    public var path: String
    public var image: Bool
    public init(path: String, image: Bool) {
        self.path = path
        self.image = image
    }
}

/// Agents that take "@path" in a message as the file itself: Claude Code
/// reads it (a picture as a picture), Gemini CLI's @ takes files and
/// images. The others are given the path, which they open themselves.
private let atAgents: Set<String> = ["claude", "gemini"]

/// Agents that read pictures from a path, by their own docs: Claude Code
/// and Gemini CLI. Codex and OpenCode are not said either way until tried
/// with the real agents.
private let imageAgents: Set<String> = ["claude", "gemini"]

/// Agents known not to read pictures: given the path and told so.
private let noImageAgents: Set<String> = ["devin"]

/// The note an image gets for an agent that cannot look at it.
public let attachImageNote = "(a picture; if you cannot view images, say so)"

/// One line of a message naming an attachment, for an agent:
/// "Attached: @src/a.go", "Attached image: .conch/attachments/7f3a/a.png".
public func attachmentLine(_ r: AttachedRef, agent: String) -> String {
    let spaced = r.path.contains { $0.isWhitespace || $0 == "\"" }
    var p = spaced ? "\"\(r.path)\"" : r.path
    if atAgents.contains(agent) && !spaced { p = "@" + p }
    var line = (r.image ? "Attached image: " : "Attached: ") + p
    if r.image && noImageAgents.contains(agent) { line += " " + attachImageNote }
    return line
}

/// Whether an agent is known to read a picture given by path.
public func agentReadsImages(_ agent: String) -> Bool { imageAgents.contains(agent) }

/// What a message of files alone says after them, so that it ends on
/// words rather than on a path.
public let attachOnlyNote = "Look at the attached files."

/// A message and its attachments as the text the agent is sent: a line
/// naming each file, a blank line, then what was written. The files come
/// first because Claude Code opens its file menu on an @path being typed,
/// and the Enter that sends the message picked from that menu instead —
/// the message sat in the agent's input, unsent. Sending stays
/// agent.prompt, so this is the same for every agent and every machine.
public func attachmentPrompt(_ text: String, _ refs: [AttachedRef], agent: String) -> String {
    let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
    let lines = refs.map { attachmentLine($0, agent: agent) }
    if lines.isEmpty { return t }
    return lines.joined(separator: "\n") + "\n\n" + (t.isEmpty ? attachOnlyNote : t)
}

/// A message as sent, parted into what was written and the attachments
/// named in it, for drawing a sent message with its chips: named first
/// and then a blank line, or named at its end as messages were before.
public func splitAttached(_ s: String) -> (text: String, refs: [AttachedRef]) {
    var lines = s.trimmingCharacters(in: .whitespacesAndNewlines).components(separatedBy: "\n")
    var refs: [AttachedRef] = []
    // Files first: a run of them from the top, then a blank line (or nothing).
    var head: [AttachedRef] = []
    var i = 0
    while i < lines.count, let r = parseAttachedLine(lines[i]) { head.append(r); i += 1 }
    if !head.isEmpty && (i == lines.count || lines[i].trimmingCharacters(in: .whitespaces).isEmpty) {
        var text = lines[i...].joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
        if text == attachOnlyNote { text = "" }
        return (text, head)
    }
    while let last = lines.last, let r = parseAttachedLine(last) {
        refs.insert(r, at: 0)
        lines.removeLast()
    }
    let text = lines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
    return (text, refs)
}

func parseAttachedLine(_ l: String) -> AttachedRef? {
    var rest: Substring
    let image: Bool
    if l.hasPrefix("Attached image: ") {
        rest = l.dropFirst(16)
        image = true
    } else if l.hasPrefix("Attached: ") {
        rest = l.dropFirst(10)
        image = false
    } else {
        return nil
    }
    if rest.hasSuffix(" " + attachImageNote) { rest = rest.dropLast(attachImageNote.count + 1) }
    if rest.hasPrefix("@") { rest = rest.dropFirst() }
    if rest.count >= 2, rest.hasPrefix("\""), rest.hasSuffix("\"") { rest = rest.dropFirst().dropLast() }
    guard !rest.isEmpty else { return nil }
    return AttachedRef(path: String(rest), image: image)
}

/// The "@word" being typed at the cursor, for the checkout's file picker:
/// an @ at the start or after a space, and no space since. Its range in
/// the text (UTF-16, as the text view counts) and the letters after it.
public func atQuery(_ text: String, cursor: Int) -> (range: NSRange, query: String)? {
    let ns = text as NSString
    guard cursor >= 1, cursor <= ns.length else { return nil }
    var i = cursor - 1
    while i >= 0 {
        let c = ns.character(at: i)
        if c == 0x40 { // @
            if i > 0, let prev = UnicodeScalar(ns.character(at: i - 1)), !CharacterSet.whitespacesAndNewlines.contains(prev) { return nil }
            let r = NSRange(location: i, length: cursor - i)
            return (r, ns.substring(with: NSRange(location: i + 1, length: cursor - i - 1)))
        }
        if let u = UnicodeScalar(c), CharacterSet.whitespacesAndNewlines.contains(u) { return nil }
        i -= 1
    }
    return nil
}

/// The text with the "@word" a pick replaced taken out.
public func removingAt(_ text: String, _ r: NSRange) -> String {
    let ns = text as NSString
    guard r.location >= 0, r.location + r.length <= ns.length else { return text }
    var out = ns.replacingCharacters(in: r, with: "")
    // "fix @x please" leaves one space, not two.
    out = out.replacingOccurrences(of: "  ", with: " ")
    return out
}

/// What the drop overlay says over the composer.
public func dropOverlayText(count: Int, images: Int) -> String {
    let n = count <= 0 ? "files" : count == 1 ? "1 file" : "\(count) files"
    return "Drop to attach \(n)" + (images > 0 ? " · images go in as pictures" : "")
}

/// What agent.attach answers: the folder for one message's copies, the
/// checkout it is in and the agent's own folder.
public struct AttachFolder: Decodable, Equatable, Sendable {
    public var dir: String
    public var root: String
    public var cwd: String
    public var maxSize: Int64?
    public var maxFiles: Int?

    enum CodingKeys: String, CodingKey {
        case dir, root, cwd
        case maxSize = "max_size"
        case maxFiles = "max_files"
    }

    public init(dir: String, root: String, cwd: String, maxSize: Int64? = nil, maxFiles: Int? = nil) {
        self.dir = dir
        self.root = root
        self.cwd = cwd
        self.maxSize = maxSize
        self.maxFiles = maxFiles
    }
}

/// Where one attachment lands in the folder agent.attach made, and the
/// path the agent is given for it.
public func attachedPath(folder: AttachFolder, name: String) -> String {
    pathFor(agentIn: folder.cwd, (folder.dir as NSString).appendingPathComponent(name))
}

/// The path the agent is given for a file of its checkout, from the
/// checkout's root and the agent's folder.
public func checkoutPath(root: String, cwd: String?, _ rel: String) -> String {
    let abs = (root as NSString).appendingPathComponent(rel)
    guard let cwd, !cwd.isEmpty else { return rel }
    return pathFor(agentIn: cwd, abs)
}
