import Foundation

// The claude.ai artifacts Claude Code published, as each machine's server
// reads them from its transcripts (artifact.list.v1), and the Artifacts
// tab that lists them beside an agent: a browser pane whose first tab is
// the list and whose other tabs are the artifacts, opened on claude.ai in
// the browser's own WebKit, signed in once like any site.

/// The capability a server needs for the list and the live publishes.
public let artifactsCapability = "artifact.list.v1"

/// An artifact an agent published (proto.Artifact).
public struct Artifact: Decodable, Equatable, Sendable, Identifiable {
    public var url: String
    public var title: String
    public var kind: String
    public var versions: Int
    public var created: Date?
    public var updated: Date?
    /// The directory the publishing session ran in.
    public var dir: String?
    /// The panes on its machine whose agent published it.
    public var panes: [String]?
    /// The project whose checkout dir is in.
    public var project: String?

    public var id: String { url }

    public init(url: String, title: String, kind: String = "", versions: Int = 1, created: Date? = nil, updated: Date? = nil,
                dir: String? = nil, panes: [String]? = nil, project: String? = nil) {
        self.url = url
        self.title = title
        self.kind = kind
        self.versions = versions
        self.created = created
        self.updated = updated
        self.dir = dir
        self.panes = panes
        self.project = project
    }
}

public struct ArtifactList: Decodable, Sendable {
    public var artifacts: [Artifact]?
    public var note: String?
}

/// artifact.published: a pane's agent published an artifact, or a new
/// version of one.
public struct ArtifactPublished: Decodable, Equatable, Sendable {
    public var pane: String
    public var agent: String?
    public var artifact: Artifact

    public init(pane: String, agent: String? = nil, artifact: Artifact) {
        self.pane = pane
        self.agent = agent
        self.artifact = artifact
    }
}

/// Whose artifacts the tab lists.
public enum ArtifactScope: String, CaseIterable, Sendable {
    case agent, project, machine

    /// The segmented control's words; the machine's is "This Mac" here.
    public func label(machine: String) -> String {
        switch self {
        case .agent: return "This Agent"
        case .project: return "Project"
        case .machine: return machine == "local" ? "This Mac" : "This Machine"
        }
    }
}

// MARK: the tab's address

/// An Artifacts tab is a browser tab whose address is this and an agent's
/// pane, key(machine, pane): never a URL WebKit loads, since "|" is no
/// URL's.
public let artifactsAddressPrefix = "conch-artifacts:"

public func artifactsAddress(pane: String) -> String { artifactsAddressPrefix + pane }

/// The pane an Artifacts tab's address names, nil for any other address.
public func parseArtifactsAddress(_ a: String) -> String? {
    guard a.hasPrefix(artifactsAddressPrefix) else { return nil }
    let rest = String(a.dropFirst(artifactsAddressPrefix.count))
    return rest.isEmpty ? nil : rest
}

/// Whether a browser tab's address is WebKit's to load: not a new tab's
/// empty one, a view-source tab's or an Artifacts list's — URL(string:)
/// takes the list's, encoding its "|", and WebKit would be handed a
/// scheme nothing answers.
public func webLoads(_ address: String) -> Bool {
    !address.isEmpty && !isViewSource(address) && parseArtifactsAddress(address) == nil
}

/// Whether a page is Google's passkey step of a sign-in, or its failure.
/// WebKit in an app hands a passkey only to apps Apple lets act as a
/// browser, so in Conch's browser that step always fails ("Make sure
/// Bluetooth is on"): the tab says so, and how else to sign in.
public func isPasskeyChallenge(_ s: String) -> Bool {
    guard let u = URL(string: s), u.host?.lowercased() == "accounts.google.com" else { return false }
    let parts = u.path.split(separator: "/").map(String.init)
    guard let i = parts.firstIndex(of: "challenge"), i + 1 < parts.count else { return false }
    return parts[i + 1] == "pk"
}

/// Whether a cookie is claude.ai's sign-in: whether Conch's browser is
/// signed in, asked by the cookie's name and domain alone — its value is
/// never read.
public func isClaudeSession(name: String, domain: String) -> Bool {
    let d = domain.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: "."))
    return name == "sessionKey" && d == "claude.ai"
}

/// Where an artifact opens when picked: in Conch's browser, or in the
/// person's own, where they may already be signed in — Conch's browser
/// cannot use another browser's sign-in, and a passkey cannot be used in
/// it at all.
public enum ArtifactOpening: String, CaseIterable, Sendable {
    /// In Conch when its browser is signed in to claude.ai, else outside.
    case automatic
    case conch
    case browser

    /// A saved choice; none (or anything else) is here: the person signs
    /// in to claude.ai once in Conch's browser and every artifact opens
    /// beside its agent.
    public init(setting: String?) { self = ArtifactOpening(rawValue: setting ?? "") ?? .conch }

    public var label: String {
        switch self {
        case .automatic: return "Automatically"
        case .conch: return "In Conch"
        case .browser: return "In My Browser"
        }
    }

    /// Whether it opens in the person's own browser. needsBrowser: this
    /// artifact failed to load in Conch's WebKit before (ArtifactHandOvers),
    /// so here it would be a blank tab whatever the choice.
    public func outside(signedIn: Bool, needsBrowser: Bool = false) -> Bool {
        switch self {
        case .automatic: return !signedIn || needsBrowser
        case .conch: return needsBrowser
        case .browser: return true
        }
    }
}

// MARK: artifacts WebKit cannot show

// claude.ai shows an artifact's page in a frame from a site of its own,
// ID.frame.claudeusercontent.com/_f/VERSION/. That frame is first asked
// for without a cookie and refused (403), then again with a one-time
// ?__frame_t= token, which answers the page and sets a cookie for the
// files under it. WebKit — Safari and Conch's browser alike — never sends
// a cookie to another site's frame, so those files fail: a plain HTML
// artifact, whole in the page, shows; one built from an artifact type,
// whose app.css and app.js are such files, comes up blank. Seen in a HAR
// from Conch's browser, 2026-11-25.

/// The path of an artifact's content frame after /_f/VERSION, and its
/// query; nil for any other URL.
private func artifactFramePath(_ s: String) -> (rest: [String], query: String)? {
    guard let u = URLComponents(string: s), u.scheme?.lowercased() == "https",
          let host = u.host?.lowercased(), host.hasSuffix(".frame.claudeusercontent.com"),
          host.count > ".frame.claudeusercontent.com".count else { return nil }
    let parts = u.path.split(separator: "/").map(String.init)
    guard parts.count >= 2, parts[0] == "_f", !parts[1].isEmpty else { return nil }
    return (Array(parts.dropFirst(2)), u.query ?? "")
}

/// Whether a URL is one of the files under an artifact's frame — its
/// stylesheet, script or fonts — rather than the frame's page itself.
public func isArtifactFrameFile(_ s: String) -> Bool {
    guard let f = artifactFramePath(s) else { return false }
    return !f.rest.isEmpty
}

/// Whether an answer is the frame's first, cookie-less try: refused, and
/// retried with a token. Every artifact gets it, so it is no error.
public func isArtifactFrameFirstTry(status: Int, url: String, mainFrame: Bool) -> Bool {
    guard !mainFrame, status == 403, let f = artifactFramePath(url), f.rest.isEmpty else { return false }
    return !f.query.contains("__frame_t=")
}

/// Whether a failure on a page hands its artifact to the person's own
/// browser: one of the artifact's own files did not load, on its
/// claude.ai page. Automatically or In Conch alike, since the tab would be
/// blank; In My Browser never opened it here.
public func artifactNeedsBrowser(opening: ArtifactOpening, page: String, issue: PageIssue) -> Bool {
    opening != .browser && issue.kind == .resource && isArtifactURL(page) && isArtifactFrameFile(issue.source)
}

/// The artifacts that failed to load in Conch's browser, by address
/// (artifactBase): opened in the person's own browser from then on. Kept
/// in the app's preferences, the newest last, at most `max`.
public struct ArtifactHandOvers: Equatable, Sendable {
    public static let max = 200
    public private(set) var urls: [String] = []

    /// What was saved; anything but a list of strings is nothing.
    public init(setting: Any?) {
        var seen = Set<String>()
        for case let s as String in (setting as? [Any]) ?? [] where isArtifactURL(s) {
            let b = artifactBase(s)
            if seen.insert(b).inserted { urls.append(b) }
        }
        if urls.count > Self.max { urls.removeFirst(urls.count - Self.max) }
    }

    public func contains(_ url: String) -> Bool { urls.contains(artifactBase(url)) }

    /// Added; false when it was there already.
    @discardableResult
    public mutating func insert(_ url: String) -> Bool {
        let b = artifactBase(url)
        guard isArtifactURL(b), !urls.contains(b) else { return false }
        urls.append(b)
        if urls.count > Self.max { urls.removeFirst(urls.count - Self.max) }
        return true
    }
}

/// Whether a page is claude.ai's sign-in: where the Artifacts tab offers
/// to take the link claude.ai emails.
public func isClaudeSignInPage(_ s: String) -> Bool {
    guard let u = URL(string: s), u.scheme?.lowercased() == "https", u.host?.lowercased() == "claude.ai" else { return false }
    let first = u.path.split(separator: "/").first.map(String.init) ?? ""
    return first == "login" || first == "magic-link"
}

/// The sign-in link someone pasted, when it is claude.ai's over https:
/// opened in Conch's browser so the sign-in lands there rather than in
/// the browser Mail opens links in. Anything else is refused, since the
/// box loads what it is given.
public func claudeSignInLink(_ pasted: String) -> URL? {
    let t = pasted.trimmingCharacters(in: .whitespacesAndNewlines)
    guard let u = URL(string: t), u.scheme?.lowercased() == "https", u.host?.lowercased() == "claude.ai" else { return nil }
    return u
}

/// Whether a link is a claude.ai artifact's: what ⌘-click in a pane
/// opens in the Artifacts tab rather than the system browser.
public func isArtifactURL(_ s: String) -> Bool {
    guard let u = URL(string: s.trimmingCharacters(in: .whitespacesAndNewlines)), u.scheme?.lowercased() == "https",
          u.host?.lowercased() == "claude.ai" else { return false }
    let parts = u.path.split(separator: "/").map(String.init)
    // /artifact/ID or /code/artifact/ID, as Claude Code prints them.
    if parts.count == 2 { return parts[0] == "artifact" && !parts[1].isEmpty }
    if parts.count == 3 { return parts[0] == "code" && parts[1] == "artifact" && !parts[2].isEmpty }
    return false
}

/// The same artifact's address as the list keeps it: no fragment, query
/// or trailing slash, so a link to one of its artboards finds it.
public func artifactBase(_ s: String) -> String {
    guard var c = URLComponents(string: s.trimmingCharacters(in: .whitespacesAndNewlines)) else { return s }
    c.fragment = nil
    c.query = nil
    if c.path.hasSuffix("/") { c.path.removeLast() }
    return c.string ?? s
}

// MARK: what the list shows

/// The artifacts a scope shows, newest first, those whose title has every
/// word of the query. pane is the agent's id on its machine; project its
/// project's, nil when it is in none (Project then shows nothing).
public func artifactsShown(_ all: [Artifact], scope: ArtifactScope, pane: String, project: String?, query: String = "") -> [Artifact] {
    let words = query.lowercased().split(separator: " ").map(String.init)
    return all.filter { a in
        switch scope {
        case .agent: guard (a.panes ?? []).contains(pane) else { return false }
        case .project: guard let project, a.project == project else { return false }
        case .machine: break
        }
        let hay = (a.title + " " + a.kind).lowercased()
        return words.allSatisfy { hay.contains($0) }
    }
    .sorted { ($0.updated ?? .distantPast) > ($1.updated ?? .distantPast) }
}

/// How many artifacts each pane's agent published: the tree's counts.
public func artifactCounts(_ all: [Artifact]) -> [String: Int] {
    var out: [String: Int] = [:]
    for a in all {
        for p in Set(a.panes ?? []) { out[p, default: 0] += 1 }
    }
    return out
}

/// The list's sections: Today, Yesterday, Earlier — only those with
/// something in them, in that order.
public func artifactSections(_ list: [Artifact], now: Date, calendar: Calendar = .current) -> [(title: String, items: [Artifact])] {
    var today: [Artifact] = [], yesterday: [Artifact] = [], earlier: [Artifact] = []
    for a in list {
        let d = a.updated ?? .distantPast
        if calendar.isDate(d, inSameDayAs: now) {
            today.append(a)
        } else if let y = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(d, inSameDayAs: y) {
            yesterday.append(a)
        } else {
            earlier.append(a)
        }
    }
    return [("Today", today), ("Yesterday", yesterday), ("Earlier", earlier)].filter { !$0.1.isEmpty }
}

/// When it was last published, as a row says it.
public func artifactWhen(_ d: Date?, now: Date, calendar: Calendar = .current) -> String {
    guard let d, d > Date(timeIntervalSince1970: 0) else { return "" }
    let s = now.timeIntervalSince(d)
    if s < 60 { return "just now" }
    if s < 3600 { return "\(Int(s / 60)) min ago" }
    if calendar.isDate(d, inSameDayAs: now) { return "\(Int(s / 3600)) h ago" }
    if let y = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(d, inSameDayAs: y) { return "yesterday" }
    let f = DateFormatter()
    f.calendar = calendar
    f.timeZone = calendar.timeZone
    f.locale = Locale(identifier: "en_US_POSIX")
    f.dateFormat = calendar.component(.year, from: d) == calendar.component(.year, from: now) ? "MMM d" : "MMM d, yyyy"
    return f.string(from: d)
}

/// A row's second line: its type, its version and when.
public func artifactDetail(_ a: Artifact, now: Date, calendar: Calendar = .current) -> String {
    [a.kind.isEmpty ? "Artifact" : a.kind, "v\(max(a.versions, 1))", artifactWhen(a.updated, now: now, calendar: calendar)]
        .filter { !$0.isEmpty }.joined(separator: " · ")
}

/// The Lucide icon for an artifact's type.
public func artifactIcon(kind: String) -> String {
    switch kind.lowercased() {
    case "design": return "pen-tool"
    case "page", "dashboard": return "app-window"
    case "doc", "document", "docs": return "file-text"
    default: return "layers"
    }
}

// MARK: what the app keeps

/// Every machine's artifacts as its server last listed them, and which
/// were published while the app was watching and not yet opened.
public struct ArtifactShelf: Equatable, Sendable {
    /// By machine.
    public private(set) var lists: [String: [Artifact]] = [:]
    /// Why a machine has no list: an older conch, an error.
    public private(set) var notes: [String: String] = [:]
    /// key(machine, url) of artifacts published since the app started
    /// watching and not opened since.
    public private(set) var unseen: Set<String> = []

    public init() {}

    public func list(_ machine: String) -> [Artifact] { lists[machine] ?? [] }

    /// A machine's whole list, as artifact.list answered.
    /// Only claude.ai artifact links are kept: what a server sends is
    /// opened with a click — in the person's browser, or a tab here — so a
    /// server that was not conch (one on a machine somebody else controls)
    /// could otherwise hand over a custom-scheme or file:// link.
    public mutating func load(_ machine: String, _ list: [Artifact]) {
        lists[machine] = list.filter { isArtifactURL($0.url) }
        notes[machine] = nil
    }

    /// A machine without a list, and why.
    public mutating func failed(_ machine: String, _ why: String) {
        lists[machine] = nil
        notes[machine] = why
    }

    /// A machine gone: its list and its unseen go with it.
    public mutating func drop(_ machine: String) {
        lists[machine] = nil
        notes[machine] = nil
        unseen = unseen.filter { !$0.hasPrefix(machine + "|") }
    }

    /// A publish told by the server: into the list, newest, marked unseen.
    /// False for a link that is not a claude.ai artifact's, which is
    /// dropped (see load).
    @discardableResult
    public mutating func published(_ machine: String, _ ev: ArtifactPublished) -> Bool {
        guard isArtifactURL(ev.artifact.url) else { return false }
        var l = lists[machine] ?? []
        var a = ev.artifact
        if let i = l.firstIndex(where: { $0.url == a.url }) {
            // A list read before keeps whatever other panes it named.
            a.panes = Array(Set((l[i].panes ?? []) + (a.panes ?? []))).sorted()
            if a.project == nil { a.project = l[i].project }
            l.remove(at: i)
        }
        l.insert(a, at: 0)
        lists[machine] = l
        unseen.insert(machine + "|" + a.url)
        return true
    }

    /// Opened: no longer news.
    public mutating func opened(_ machine: String, _ url: String) {
        unseen.remove(machine + "|" + artifactBase(url))
    }

    public func isUnseen(_ machine: String, _ url: String) -> Bool { unseen.contains(machine + "|" + url) }

    /// How many a pane's agent published, and whether one is unseen.
    public func count(_ machine: String, pane: String) -> (count: Int, unseen: Bool) {
        let mine = list(machine).filter { ($0.panes ?? []).contains(pane) }
        return (mine.count, mine.contains { isUnseen(machine, $0.url) })
    }

    /// Whether an agent's header offers its Artifacts tab: only once it
    /// has published something. A chip reading "Artifacts" over an agent
    /// that never made one read as a name, or an empty promise; ⌥⌘A still
    /// opens the tab for any agent.
    public func showsChip(_ machine: String, pane: String) -> Bool {
        count(machine, pane: pane).count > 0
    }

    /// An artifact by its address, on any machine; the machine it is on.
    public func find(_ url: String) -> (machine: String, artifact: Artifact)? {
        let base = artifactBase(url)
        for (m, l) in lists.sorted(by: { $0.key < $1.key }) {
            if let a = l.first(where: { $0.url == base }) { return (m, a) }
        }
        return nil
    }
}

/// An agent's Artifacts ▸ on its row's menu: its newest, then Show All.
/// None yet still offers the tab, which says why it is empty.
public func artifactsMenu(pane k: String, artifacts: [Artifact], most: Int = 8) -> RowMenuItem {
    var kids = artifacts.prefix(most).map { RowMenuItem($0.title.isEmpty ? $0.url : $0.title, .openArtifact(pane: k, url: $0.url)) }
    if !kids.isEmpty { kids.append(.divider) }
    kids.append(RowMenuItem("Show All", .artifacts(pane: k)))
    return RowMenuItem("Artifacts", .divider, children: kids)
}

/// ⌘K's artifacts: every machine's, newest first, each saying whose and
/// when — at most `most`, since a machine can hold hundreds. label names
/// a machine in a window showing more than one (nil: no machine named);
/// agent names a pane, by machine and pane id.
public func artifactPaletteItems(_ shelf: ArtifactShelf, machines: [String], label: (String) -> String?,
                                 agent: (String, String) -> String?, now: Date, most: Int = 40) -> [PaletteItem] {
    var all: [(machine: String, a: Artifact)] = []
    for m in machines { all += shelf.list(m).map { (m, $0) } }
    all.sort { ($0.a.updated ?? .distantPast) > ($1.a.updated ?? .distantPast) }
    return all.prefix(most).map { m, a in
        let who = (a.panes ?? []).compactMap { agent(m, $0) }.first
        let detail = [who, artifactWhen(a.updated, now: now), label(m)].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
        return PaletteItem(title: a.title.isEmpty ? a.url : a.title, detail: detail, kind: "artifact", action: .artifact(machine: m, url: a.url))
    }
}
