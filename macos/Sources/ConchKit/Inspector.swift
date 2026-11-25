import Foundation

// The inspector: a column at the window's right edge saying where the
// focused split's branch stands — what it changes, the notes on it, its
// pull request's checks — without opening its Changes tab. It is the
// first view drawn by a web view rather than SwiftUI (the hybrid design:
// the shell and terminals native, rich views in HTML shared across the
// platforms), so the page and its bridge are here, where they are
// tested: what the page is sent (InspectorView, one JSON object), the
// page itself (no network, everything it draws escaped), and what it may
// ask back (InspectorIntent, checked before the app acts on it).

/// The branch the inspector shows.
public struct InspectorTarget: Equatable, Hashable, Sendable {
    public var machine: String
    public var project: String
    public var branch: String

    public init(machine: String, project: String, branch: String) {
        self.machine = machine
        self.project = project
        self.branch = branch
    }

    /// Its Changes tab's key, which also keys the model the tab reads.
    public var changes: String { changesKey(machine, project, branch) }
}

/// The branch of the focused split: a Changes tab's own, or the branch a
/// pane is on — named by the server, or else the deepest worktree holding
/// its folder. Nothing for a pane outside a project, or anything else (a
/// browser, a file).
public func inspectorTarget(focused: String?, pane: PaneInfo?, project: ProjectInfo?) -> InspectorTarget? {
    guard let focused else { return nil }
    if let c = parseChangesKey(focused) { return InspectorTarget(machine: c.machine, project: c.project, branch: c.branch) }
    guard let pane, let pid = pane.projectID, let project, project.id == pid else { return nil }
    let machine = unkey(focused).machine
    if let b = pane.branch, !b.isEmpty { return InspectorTarget(machine: machine, project: pid, branch: b) }
    guard let cwd = pane.cwd, !cwd.isEmpty else { return nil }
    let holding = (project.worktrees ?? []).filter { w in
        let root = w.path.hasSuffix("/") ? String(w.path.dropLast()) : w.path
        return w.branch?.isEmpty == false && (cwd == root || cwd.hasPrefix(root + "/"))
    }
    guard let w = holding.max(by: { $0.path.count < $1.path.count }), let b = w.branch else { return nil }
    return InspectorTarget(machine: machine, project: pid, branch: b)
}

/// How many files and notes the column lists before "and N more": it is
/// a glance, and the Changes tab holds the rest.
public let inspectorFileCap = 6
public let inspectorNoteCap = 5
public let inspectorCheckCap = 8

/// Everything the page draws, sent to it whole on every change.
public struct InspectorView: Encodable, Equatable, Sendable {
    public struct Colors: Encodable, Equatable, Sendable {
        public var bg, fg, muted, border, raised, accent, ok, warn, err, work: String
        public var light: Bool
    }
    public struct File: Encodable, Equatable, Sendable {
        public var path: String
        public var code: String
        public var added: Int
        public var deleted: Int
    }
    public struct Note: Encodable, Equatable, Sendable {
        public var id: String
        public var file: String
        public var line: Int?
        public var note: String
        public var by: String?
        public var github: Bool
        public var replies: Int
        public var sent: Bool
        /// conch's own notes are resolved here; GitHub's are resolved there.
        public var canResolve: Bool
    }
    public struct Check: Encodable, Equatable, Sendable {
        public var name: String
        public var state: String
        public var url: String?
    }
    public struct PR: Encodable, Equatable, Sendable {
        public var number: Int
        public var title: String?
        public var state: String?
        public var url: String?
        public var checks: String?
        public var passed: Int?
        public var total: Int?
        public var review: String?
        public var runs: [Check]
        public var moreRuns: Int
    }

    public var colors: Colors
    /// Set when there is no branch to show, saying why.
    public var empty: String?
    public var project = ""
    public var branch = ""
    public var base: String?
    /// The branch's commits not on its base.
    public var ahead: Int?
    public var loading = false
    public var error: String?
    public var added = 0
    public var deleted = 0
    public var files: [File] = []
    public var moreFiles = 0
    public var notes: [Note] = []
    public var moreNotes = 0
    public var pr: PR?
}

extension InspectorView.Colors {
    /// The theme's, on the sidebar's surface so the column reads as the
    /// window's own rather than a page pasted in.
    public init(_ t: ConchTheme) {
        bg = t.sidebar.hex
        fg = t.fg.hex
        muted = t.muted.hex
        border = t.border.hex
        raised = t.raised.hex
        accent = t.line.hex
        ok = t.ok.hex
        warn = t.warn.hex
        err = t.err.hex
        work = t.work.hex
        light = t.light
    }
}

/// The column with nothing to show, and why.
public func inspectorEmpty(_ why: String, theme: ConchTheme) -> InspectorView {
    InspectorView(colors: .init(theme), empty: why)
}

/// The column for a branch, from what the Changes tab's model has read:
/// its changes (nil while the first read is out), its notes, and the
/// branch as the project lists it (its pull request).
public func inspectorView(_ t: InspectorTarget, project: String, branch: BranchInfo?, changes: Changes?,
                          comments: [ReviewComment], error: String?, theme: ConchTheme) -> InspectorView {
    var v = InspectorView(colors: .init(theme))
    v.project = project
    v.branch = t.branch
    v.base = changes?.base
    v.ahead = branch?.baseAhead
    v.error = error
    v.loading = changes == nil && error == nil
    if let ch = changes {
        (v.added, v.deleted) = ch.totals
        v.files = ch.files.prefix(inspectorFileCap).map { .init(path: $0.path, code: $0.code, added: $0.added, deleted: $0.deleted) }
        v.moreFiles = max(0, ch.files.count - inspectorFileCap)
    }
    let newest = comments.sorted { ($0.at ?? .distantPast) > ($1.at ?? .distantPast) }
    v.notes = newest.prefix(inspectorNoteCap).map { c in
        .init(id: c.id ?? "", file: c.file, line: c.line, note: c.note, by: c.by, github: c.fromGitHub,
              replies: c.replies?.count ?? 0, sent: c.sentTo != nil, canResolve: !c.fromGitHub && !(c.id ?? "").isEmpty)
    }
    v.moreNotes = max(0, comments.count - inspectorNoteCap)
    if let pr = branch?.pr {
        let runs = pr.runs ?? []
        v.pr = .init(number: pr.number, title: pr.title, state: pr.state, url: pr.url, checks: pr.checks,
                     passed: pr.passed, total: pr.total, review: pr.review,
                     runs: runs.prefix(inspectorCheckCap).map { .init(name: $0.name, state: $0.state, url: $0.url) },
                     moreRuns: max(0, runs.count - inspectorCheckCap))
    }
    return v
}

/// What the page asks of the app. Nothing else is acted on.
public enum InspectorIntent: Equatable, Sendable {
    /// The page has loaded and wants the view.
    case ready
    case openChanges
    case openFile(String)
    /// A conch note taken away.
    case resolve(String)
    /// A pull request or a check, in the browser: http and https only.
    case openURL(URL)
}

/// A message from the page (WKScriptMessage's body), checked: the page is
/// ours, but what it draws — branch names, notes, check links — is not.
public func inspectorIntent(_ body: Any) -> InspectorIntent? {
    guard let m = body as? [String: Any], let type = m["type"] as? String else { return nil }
    func text(_ k: String) -> String? { (m[k] as? String).flatMap { $0.isEmpty ? nil : $0 } }
    switch type {
    case "ready": return .ready
    case "openChanges": return .openChanges
    case "openFile": return text("path").map { .openFile($0) }
    case "resolve": return text("id").map { .resolve($0) }
    case "openURL":
        guard let s = text("url"), let u = URL(string: s), let scheme = u.scheme?.lowercased(),
              scheme == "https" || scheme == "http", u.host?.isEmpty == false else { return nil }
        return .openURL(u)
    default: return nil
    }
}

/// The script that hands the page a view: one call, the view as a JSON
/// literal. The line and paragraph separators are escaped, which older
/// JavaScript refused inside a string.
public func inspectorUpdateScript(_ v: InspectorView) -> String {
    let data = (try? JSONEncoder().encode(v)) ?? Data("{}".utf8)
    let json = String(decoding: data, as: UTF8.self)
        .replacingOccurrences(of: "\u{2028}", with: "\\u2028")
        .replacingOccurrences(of: "\u{2029}", with: "\\u2029")
    return "window.conchInspector && window.conchInspector.update(\(json));"
}

/// The page's script. renderInspector(view) is the whole of what it
/// draws, a string of HTML with every value escaped; the rest binds it to
/// the document and sends clicks on [data-intent] to the app.
public let inspectorScript = #"""
"use strict";
function esc(s) {
  return String(s === undefined || s === null ? "" : s)
    .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}
function plural(n, one, many) { return n + " " + (n === 1 ? one : many); }
function stateClass(s) {
  s = String(s || "").toLowerCase();
  if (s === "pass" || s === "success") return "ok";
  if (s === "fail" || s === "failure" || s === "error") return "err";
  if (s === "pending" || s === "running" || s === "queued") return "work";
  return "muted";
}
function renderInspector(v) {
  if (v.empty) return '<p class="empty">' + esc(v.empty) + '</p>';
  var h = '<header><div class="branch">' + esc(v.branch) + '</div><div class="sub">' + esc(v.project);
  if (v.base) h += ' · into ' + esc(v.base);
  if (v.ahead) h += ' · ↑' + esc(v.ahead);
  h += '</div>';
  if (v.pr) {
    h += '<button class="pill" data-intent="openURL" data-url="' + esc(v.pr.url) + '"' + (v.pr.url ? '' : ' disabled') + '>#' + esc(v.pr.number) + '</button>';
  }
  h += '</header>';
  if (v.error) h += '<p class="error">' + esc(v.error) + '</p>';

  h += '<section><div class="head"><span>Changes</span>';
  if (!v.loading && !v.error) {
    var n = v.files.length + v.moreFiles;
    h += '<span><span class="ok">+' + esc(v.added) + '</span> <span class="err">−' + esc(v.deleted) + '</span> · ' + plural(n, "file", "files") + '</span>';
  }
  h += '</div>';
  if (v.loading) {
    h += '<p class="muted">Reading the branch…</p>';
  } else if (!v.error && v.files.length === 0) {
    h += '<p class="muted">Nothing uncommitted.</p>';
  }
  for (var i = 0; i < v.files.length; i++) {
    var f = v.files[i];
    h += '<button class="row" data-intent="openFile" data-path="' + esc(f.path) + '"><span class="code">' + esc(f.code) +
      '</span><span class="path">' + esc(f.path) + '</span><span class="ok">+' + esc(f.added) + '</span>' +
      (f.deleted ? '<span class="err">−' + esc(f.deleted) + '</span>' : '') + '</button>';
  }
  if (v.moreFiles) h += '<p class="muted">and ' + esc(v.moreFiles) + ' more</p>';
  h += '<button class="primary" data-intent="openChanges">Open changes</button></section>';

  if (v.notes.length) {
    h += '<section><div class="head"><span>Notes</span><span>' + esc(v.notes.length + v.moreNotes) + '</span></div>';
    for (var j = 0; j < v.notes.length; j++) {
      var c = v.notes[j];
      h += '<div class="note"><div class="who"><strong>' + esc(c.by || (c.github ? "GitHub" : "you")) + '</strong> · ' +
        esc(c.file) + (c.line ? ':' + esc(c.line) : '') + (c.sent ? ' · sent' : '') + '</div><p>' + esc(c.note) + '</p>';
      if (c.replies) h += '<div class="muted">' + plural(c.replies, "reply", "replies") + '</div>';
      if (c.canResolve) h += '<button class="link" data-intent="resolve" data-id="' + esc(c.id) + '">Resolve</button>';
      h += '</div>';
    }
    if (v.moreNotes) h += '<p class="muted">and ' + esc(v.moreNotes) + ' more</p>';
    h += '</section>';
  }

  if (v.pr) {
    var p = v.pr;
    h += '<section><div class="head"><span>Checks</span>';
    if (p.total) h += '<span>' + esc(p.passed || 0) + ' of ' + esc(p.total) + '</span>';
    h += '</div>';
    if (p.title) h += '<p class="title">' + esc(p.title) + '</p>';
    if (p.total) {
      var pct = Math.max(0, Math.min(100, Math.round((p.passed || 0) / p.total * 100)));
      h += '<div class="bar"><div style="width:' + pct + '%"></div></div>';
    }
    if (!p.runs.length) h += '<p class="muted">' + (p.checks ? esc(p.checks) : 'No checks reported') + '</p>';
    for (var k = 0; k < p.runs.length; k++) {
      var r = p.runs[k];
      h += '<button class="row" data-intent="openURL" data-url="' + esc(r.url) + '"' + (r.url ? '' : ' disabled') + '><span class="dot ' +
        stateClass(r.state) + '">●</span><span class="path">' + esc(r.name) + '</span><span class="muted">' + esc(r.state) + '</span></button>';
    }
    if (p.moreRuns) h += '<p class="muted">and ' + esc(p.moreRuns) + ' more</p>';
    h += '</section>';
  }
  return h;
}
if (typeof document !== "undefined") {
  var post = function (m) {
    if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.conch) window.webkit.messageHandlers.conch.postMessage(m);
  };
  window.conchInspector = {
    update: function (v) {
      var s = document.documentElement.style, c = v.colors || {};
      ["bg", "fg", "muted", "border", "raised", "accent", "ok", "warn", "err", "work"].forEach(function (k) { if (c[k]) s.setProperty("--" + k, c[k]); });
      s.colorScheme = c.light ? "light" : "dark";
      document.getElementById("root").innerHTML = renderInspector(v);
    }
  };
  document.addEventListener("click", function (e) {
    var el = e.target.closest ? e.target.closest("[data-intent]") : null;
    if (!el || el.disabled) return;
    var m = { type: el.getAttribute("data-intent") };
    ["path", "url", "id"].forEach(function (k) { var a = el.getAttribute("data-" + k); if (a) m[k] = a; });
    post(m);
  });
  post({ type: "ready" });
}
"""#

/// The page: the script and its styles, and a policy that lets it load
/// nothing at all — no network, no frames, no other script.
public let inspectorPage = """
<!doctype html>
<html><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'">
<style>
:root { --bg: #1b1f24; --fg: #c9d1d9; --muted: #8b949e; --border: #444c56; --raised: #262c33; --accent: #2aa5b5;
  --ok: #3fb950; --warn: #d29922; --err: #f85149; --work: #58a6ff; color-scheme: dark; }
* { box-sizing: border-box; }
html, body { margin: 0; background: var(--bg); color: var(--fg); font: 12.5px/1.45 -apple-system, BlinkMacSystemFont, sans-serif;
  -webkit-user-select: none; cursor: default; }
#root { padding: 12px; display: flex; flex-direction: column; gap: 12px; }
header { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 8px; }
.branch { font: 600 13px ui-monospace, Menlo, monospace; overflow-wrap: anywhere; flex: 1 1 auto; }
.sub { width: 100%; order: 3; color: var(--muted); font-size: 11.5px; }
section { background: var(--raised); border: 1px solid var(--border); border-radius: 10px; padding: 10px; display: flex; flex-direction: column; gap: 4px; }
.head { display: flex; justify-content: space-between; color: var(--muted); font-size: 11.5px; margin-bottom: 4px; }
button { font: inherit; color: inherit; background: none; border: 0; padding: 0; text-align: left; cursor: pointer; }
button:disabled { cursor: default; }
.row { display: flex; align-items: center; gap: 8px; padding: 3px 4px; margin: 0 -4px; border-radius: 6px; width: calc(100% + 8px); }
.row:not(:disabled):hover { background: color-mix(in srgb, var(--fg) 8%, transparent); }
.path { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-family: ui-monospace, Menlo, monospace; font-size: 11.5px; }
.code { color: var(--warn); font-weight: 600; width: 10px; }
.primary { margin-top: 6px; background: var(--accent); color: #fff; border-radius: 7px; padding: 5px 0; text-align: center; font-weight: 600; }
.pill { background: color-mix(in srgb, var(--work) 22%, transparent); color: var(--work); border-radius: 999px; padding: 0 8px; font-size: 11px; }
.link { color: var(--accent); font-size: 11.5px; }
.note { padding: 6px 0; border-top: 1px solid color-mix(in srgb, var(--border) 60%, transparent); }
.note:first-of-type { border-top: 0; padding-top: 0; }
.note p { margin: 2px 0 4px; overflow-wrap: anywhere; }
.who { color: var(--muted); font-size: 11.5px; overflow-wrap: anywhere; }
.who strong { color: var(--fg); font-weight: 600; }
.title { margin: 0 0 4px; overflow-wrap: anywhere; }
.bar { height: 5px; border-radius: 3px; background: color-mix(in srgb, var(--fg) 12%, transparent); margin-bottom: 4px; }
.bar div { height: 5px; border-radius: 3px; background: var(--accent); }
.ok { color: var(--ok); } .err { color: var(--err); } .work { color: var(--work); } .muted { color: var(--muted); }
p { margin: 0; }
.dot { font-size: 9px; }
.empty { color: var(--muted); text-align: center; margin-top: 40px; padding: 0 12px; }
.error { color: var(--err); overflow-wrap: anywhere; }
</style></head>
<body><div id="root"></div>
<script>
\(inspectorScript)
</script>
</body></html>
"""
