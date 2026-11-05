import Foundation

// What the window shows besides terminals and code: a file's picture,
// video, sound, PDF or page in the Files tab, and web pages in a browser
// tab of its own — a dev server's port, a page a file links to.

/// How the Files tab shows a file, by its name.
public enum MediaKind: String, Sendable, Equatable {
    case image, video, audio, pdf, html, text

    public init(path: String) {
        switch (path as NSString).pathExtension.lowercased() {
        case "png", "jpg", "jpeg", "gif", "webp", "bmp", "tif", "tiff", "heic", "heif", "ico", "icns", "svg": self = .image
        case "mp4", "m4v", "mov", "qt": self = .video
        case "mp3", "m4a", "aac", "wav", "aif", "aiff", "caf", "flac": self = .audio
        case "pdf": self = .pdf
        case "html", "htm", "xhtml": self = .html
        default: self = .text
        }
    }

    /// Whether it is shown from its bytes (fetched whole from a remote
    /// machine) rather than read as text: everything but text; HTML both.
    public var needsFile: Bool { self != .text }
}

/// Whether a file shown rendered is also text to edit — a page, an SVG —
/// and so gets a switch to its source.
public func mediaHasSource(_ path: String) -> Bool {
    ["html", "htm", "xhtml", "svg"].contains((path as NSString).pathExtension.lowercased())
}

/// The most a remote file is fetched for showing: a video worth a look,
/// not an archive.
public let mediaFetchMax: Int64 = 512 << 20

/// A browser tab's key in the layout: "web|" and its first address,
/// percent-encoded so a "|" in it can't be read as the key's own.
public func browserKey(_ url: String) -> String {
    "web|" + (url.addingPercentEncoding(withAllowedCharacters: .alphanumerics.union(CharacterSet(charactersIn: "-._~:/?#[]@!$&'()*+,;=%"))) ?? url)
}

public func parseBrowserKey(_ k: String) -> String? {
    guard k.hasPrefix("web|") else { return nil }
    let rest = String(k.dropFirst(4))
    return rest.removingPercentEncoding ?? rest
}

/// What the address bar loads for what was typed: a full address as it
/// is; a host with a port, localhost or an address on this network over
/// http, since that is what a dev server speaks; anything else with a dot
/// over https; and nil for what is no address at all.
public func browserURL(_ typed: String) -> URL? {
    let s = typed.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !s.isEmpty, !s.contains(" ") else { return nil }
    if s.range(of: #"^[a-zA-Z][a-zA-Z0-9+.-]*://"#, options: .regularExpression) != nil { return URL(string: s) }
    if s.hasPrefix("file:") || s.hasPrefix("about:") { return URL(string: s) }
    let host = s.split(separator: "/", maxSplits: 1).first.map(String.init) ?? s
    let bare = host.split(separator: ":").first.map(String.init) ?? host
    let local = bare == "localhost" || bare.hasSuffix(".local") || bare.hasSuffix(".localhost")
        || bare.range(of: #"^(127\.|10\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.|0\.0\.0\.0)"#, options: .regularExpression) != nil
    if local || host.range(of: #":\d+$"#, options: .regularExpression) != nil { return URL(string: "http://" + s) }
    if bare.contains(".") { return URL(string: "https://" + s) }
    return nil
}

/// Where a remote file is fetched to for showing: a folder per machine and
/// path under the cache, so two files of one name don't take each other's
/// place, and the file keeps its own name — its extension is how AppKit
/// knows a .mov from a .png.
public func mediaCachePath(base: URL, machine: String, path: String) -> URL {
    // FNV-1a: stable from run to run, unlike Hasher.
    var h: UInt64 = 0xcbf29ce484222325
    for b in (machine + "\u{0}" + path).utf8 { h = (h ^ UInt64(b)) &* 0x100000001b3 }
    let safe = machine.map { $0.isLetter || $0.isNumber || $0 == "-" || $0 == "." ? $0 : "_" }
    var name = (path as NSString).lastPathComponent
    if name.isEmpty || name == "." || name == ".." || name == "/" { name = "file" }
    return base.appendingPathComponent("media", isDirectory: true)
        .appendingPathComponent(String(safe), isDirectory: true)
        .appendingPathComponent(String(h, radix: 16), isDirectory: true)
        .appendingPathComponent(name)
}
