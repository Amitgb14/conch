import Foundation
import Testing
@testable import ConchKit

// As `conch import conductor -n -json` prints it.
private let scanJSON = """
[{"id":"a1","title":"Fix the build","agent":"Claude Code","repo":"shop","branch":"fix/build","workspace":"/w/victoria","dir":"/w/victoria","messages":12,"file":"/c/imports/conductor/shop/fix-the-build-a1.md","where":"workspace","last_used":"2026-11-10T15:21:00Z","imported":false},
 {"id":"b2","title":"Old idea","agent":"Claude Code","repo":"shop","branch":"fix/build","workspace":"/w/victoria","dir":"/w/victoria","messages":3,"file":"/c/imports/conductor/shop/old-idea-b2.md","where":"workspace","last_used":"2026-11-01T10:00:00Z","imported":true},
 {"id":"c3","title":"Docs","agent":"Codex","repo":"site","branch":"docs","workspace":"/w/oslo","dir":"/src/site","messages":4,"file":"/c/imports/conductor/site/docs-c3.md","where":"repository","last_used":"2026-11-12T09:00:00Z","imported":false,"later":true},
 {"id":"d4","title":"Gone","agent":"Claude Code","messages":2,"file":"/c/imports/conductor/unknown-repository/gone-d4.md","where":"gone","imported":false}]
"""

@Test func importScanGroupsAndChoice() throws {
    var f = ImportFlow(name: "conductor", label: "Conductor", reader: "conductor")
    #expect(f.scanArguments == ["import", "conductor", "-n", "-json"])
    f.scanned(Data(scanJSON.utf8))
    #expect(f.phase == .choosing)
    // Imported already: not ticked, since importing again only replaces it.
    #expect(f.chosen == ["a1", "c3", "d4"])
    #expect(f.footer == "3 of 4 conversations · 1 imported already")
    #expect(f.button == "Import 3" && f.canImport)
    let g = f.groups
    #expect(g.map(\.title) == ["site · docs", "shop · fix/build", "Unknown repository"])
    #expect(g[1].conversations.map(\.id) == ["a1", "b2"]) // newest first
    #expect(f.conversations[2].state == "worktree removed · carries on in the repository")
    #expect(f.conversations[3].state.hasPrefix("neither"))

    f.toggle(group: g[1]) // a1 ticked, b2 not: the whole group
    #expect(f.chosen.isSuperset(of: ["a1", "b2"]))
    f.toggle(group: g[1])
    #expect(f.chosen == ["c3", "d4"])
    f.toggle("c3")
    f.toggle("d4")
    #expect(!f.canImport && f.button == "Import 0")
    f.toggle("d4")
    f.toggle("a1")
    // In the scan's order, whatever order they were ticked in.
    #expect(f.runArguments == ["import", "conductor", "-json", "-ids", "a1,d4"])
}

// Newest first by the time, not the text: an offset, fractional seconds or
// a lower-case z must not change the order the strings would give.
@Test func importGroupsSortByTimeNotText() {
    var f = ImportFlow(name: "conductor", label: "Conductor", reader: "conductor")
    func c(_ id: String, _ repo: String, _ t: String?) -> ImportConversation {
        ImportConversation(id: id, title: id, agent: "Claude Code", repo: repo, branch: "b", messages: 1, file: "/f/" + id, lastUsed: t)
    }
    f.conversations = [
        c("plain", "r", "2026-11-10T12:00:00Z"),           // 12:00 UTC
        c("offset", "r", "2026-11-10T15:00:00+05:30"),     // 09:30 UTC: older, though its text is bigger
        c("frac", "r", "2026-11-10T12:00:00.5Z"),          // half a second after plain; as text, "." < "Z"
        c("nano", "r", "2026-11-10T11:59:59.123456789-00:00"),
        c("none", "r", nil),
        c("bad", "r", "yesterday"),
        c("west", "s", "2026-11-10T08:00:00-05:00"),       // 13:00 UTC: newest of all
    ]
    let g = f.groups
    #expect(g.map(\.repo) == ["s", "r"])
    #expect(g[1].conversations.map(\.id) == ["frac", "plain", "nano", "offset", "none", "bad"])
    #expect(parseRFC3339("2026-11-10T12:00:00Z") != nil && parseRFC3339("not a time") == nil)
}

@Test func importScanEdges() {
    var f = ImportFlow(name: "conductor", label: "Conductor", reader: "conductor")
    f.scanned(Data("null".utf8))
    #expect(f.phase == .choosing && f.conversations.isEmpty && !f.canImport && f.footer == "0 of 0 conversations")
    f.scanned(Data("[{".utf8))
    if case .failed(let why) = f.phase { #expect(why.contains("conch import conductor")) } else { Issue.record("a truncated scan was read") }
}

@Test func importResults() throws {
    var f = ImportFlow(name: "conductor", label: "Conductor", reader: "conductor")
    f.imported(Data(scanJSON.utf8))
    #expect(f.phase == .done && f.results.count == 4)
    #expect(f.doneWords == "Imported 4 conversations from Conductor.")
    #expect(f.folder == "/c/imports/conductor")
    #expect(ImportFlow.prompt(f.results[0]) == "Read /c/imports/conductor/shop/fix-the-build-a1.md and carry on from where it leaves off.")
    f.imported(Data("[]".utf8))
    #expect(f.doneWords == "Nothing was imported." && f.folder == nil)
}

// As a conch that writes Claude conversations as Claude Code sessions
// prints `conch import conductor -json`: a session has no "file", a Codex
// one is a handoff and says why, and one carried on in conch since is left
// alone. An older app required "file" and could read none of this.
private let resumedJSON = """
[{"id":"a1","title":"Fix the build","agent":"Claude Code","repo":"shop","branch":"fix/build","workspace":"/w/victoria","dir":"/w/victoria","messages":12,"status":"written","agentName":"claude","session":"5b0c","sessionFile":"/h/.claude/projects/-w-victoria/5b0c.jsonl","where":"workspace","imported":true},
 {"id":"c3","title":"Docs","agent":"Codex","repo":"site","branch":"docs","dir":"/src/site","messages":4,"status":"markdown","why":"Codex conversations come over as Markdown for now","agentName":"codex","file":"/c/imports/conductor/site/docs-c3.md","where":"repository","imported":true},
 {"id":"e5","title":"Kept going","agent":"Claude Code","repo":"shop","branch":"b","dir":"/w/b","messages":9,"status":"carried-on","why":"its session changed since","agentName":"claude","session":"77aa","where":"workspace","imported":true},
 {"id":"d4","title":"Gone","agent":"Claude Code","messages":2,"status":"markdown","why":"Claude Code cannot resume it: no folder to resume it in","agentName":"claude","file":"/c/imports/conductor/unknown-repository/gone-d4.md","where":"gone","imported":true}]
"""

@Test func importResumable() throws {
    var f = ImportFlow(name: "conductor", label: "Conductor", reader: "conductor")
    f.scanned(Data(resumedJSON.utf8))
    #expect(f.phase == .choosing && f.conversations.count == 4)
    f.imported(Data(resumedJSON.utf8))
    #expect(f.phase == .done)
    let r = Dictionary(uniqueKeysWithValues: f.results.map { ($0.id, $0) })
    #expect(r["a1"]!.resumes && r["a1"]!.how == "resumes in Claude Code" && r["a1"]!.file == nil)
    #expect(r["a1"]!.resumeArguments == ["new", "-agent", "claude", "-cwd", "/w/victoria", "--", "--resume", "5b0c"])
    #expect(ImportFlow.prompt(r["a1"]!) == nil)
    #expect(!r["c3"]!.resumes && r["c3"]!.how == "as Markdown: Codex conversations come over as Markdown for now")
    #expect(r["c3"]!.resumeArguments == nil && ImportFlow.prompt(r["c3"]!) != nil)
    #expect(r["e5"]!.resumes && r["e5"]!.how.hasPrefix("left alone") && r["e5"]!.resumeArguments != nil)
    #expect(r["d4"]!.resumeArguments == nil)
    // The folder is the first handoff's, past the session that has none.
    #expect(f.folder == "/c/imports/conductor")
    #expect(f.doneWords == "Imported 4 conversations from Conductor. 2 resume in their agents; 2 as Markdown.")
    f.imported(Data(#"[{"id":"a1","title":"t","agent":"Claude Code","dir":"/w","messages":1,"status":"written","agentName":"claude","session":"s"}]"#.utf8))
    #expect(f.doneWords == "Imported 1 conversation from Conductor. Each resumes in its agent." && f.folder == nil)
    f.imported(Data(#"[{"id":"a1","title":"t","agent":"Claude Code","dir":"/w","messages":1,"status":"written","agentName":"claude","session":"s","project":"shop1"}]"#.utf8))
    #expect(f.results[0].project == "shop1")
    #expect(f.doneWords == "Imported 1 conversation from Conductor. Each resumes in its agent. Their repositories are in conch now.")
    // No session id, or nowhere to run it: nothing to resume.
    #expect(ImportConversation(id: "x", title: "", agent: "Claude Code", messages: 1, status: "written", session: "s").resumeArguments == nil)
    #expect(ImportConversation(id: "x", title: "", agent: "Claude Code", dir: "/w", messages: 1, status: "written").resumeArguments == nil)
}

// The registry's Conductor entry, as `conch integration search -json` and
// `info -json` print it.
@Test func importCardsAndPage() throws {
    let listed = try JSONDecoder().decode([IntegrationListed].self, from: Data("""
    [{"kind":"import","name":"conductor","label":"Conductor","version":1,"format":3,"file":"integrations/conductor.toml","sha256":"df","hosts":{"api":"","follow":[]},"key_env":"","operations":[],"tier":"community","platforms":["darwin"],"reader":"conductor","min_reader":1,"signed":false,"found":["/Users/x/Library/Application Support/com.conductor.app/conductor.db"]},
     {"kind":"import","name":"cursor","label":"Cursor","version":1,"format":3,"file":"integrations/cursor.toml","sha256":"aa","hosts":{"api":""},"key_env":"","reader":"cursor","min_reader":1,"signed":false,"needs_newer":"reader \\"cursor\\": this conch has no reader by that name, so it needs a newer conch"}]
    """.utf8))
    let cards = IntegrationCatalog.cards(here: IntegrationList(integrations: [], problems: nil), listed: listed, uses: [:])
    let by = Dictionary(uniqueKeysWithValues: cards.map { ($0.name, $0) })
    #expect(by["conductor"]?.summary.hasPrefix("Found on this Mac") == true && by["conductor"]?.wanted == true && by["conductor"]?.needsNewer == nil)
    #expect(by["cursor"]?.needsNewer?.hasSuffix("needs a newer conch") == true && by["cursor"]?.wanted == false)
    #expect(by["cursor"]?.summary == "Not found on this Mac.")

    // Installed: ls's state says whether it is here.
    let here = IntegrationList(integrations: [IntegrationHere(name: "conductor", kind: "import", version: "1", source: "conch-integrations", state: "not found on this machine")], problems: nil)
    let installed = IntegrationCatalog.cards(here: here, listed: listed, uses: [:]).first { $0.name == "conductor" }
    #expect(installed?.installed == true && installed?.summary == "Not found on this Mac." && installed?.wanted == false)

    // A conch whose registry predates kinds still lists Import.
    let kinds = IntegrationCatalog.kinds(nil, cards: cards)
    #expect(kinds.last?.kind == "import" && kinds.last?.known == true)
    #expect(IntegrationKindInfo(kind: "import", label: "Import").known)

    let info = try JSONDecoder().decode(IntegrationInfo.self, from: Data("""
    {"name":"conductor","label":"Conductor","kind":"import","source":"registry conch-integrations","installed":false,"does":[],"hosts":{"api":""},"key_env":"","key_set":false,"reader":"conductor","min_reader":1,"path":"~/Library/Application Support/com.conductor.app/conductor.db","found":["/x/conductor.db"]}
    """.utf8))
    #expect(info.isImport && !info.offersKey && info.reader == "conductor" && info.minReader == 1 && info.found == ["/x/conductor.db"])
}

@Test func importedAlreadyKeepsOnlyWhatCameOverNewestFirst() {
    let data = Data("""
    [{"id":"a","title":"Old","agent":"claude","repo":"shop","branch":"x","messages":2,"file":"/i/shop/a.md","imported":true,"last_used":"2026-11-01T10:00:00Z"},
     {"id":"b","title":"Not yet","agent":"codex","repo":"shop","branch":"x","messages":1,"file":"/i/shop/b.md"},
     {"id":"c","title":"New","agent":"claude","repo":"shop","branch":"x","messages":3,"file":"/i/shop/c.md","imported":true,"last_used":"2026-11-02T10:00:00Z"},
     {"id":"d","title":"False","agent":"claude","messages":1,"file":"/i/d.md","imported":false}]
    """.utf8)
    #expect(ImportFlow.importedAlready(data).map(\.id) == ["c", "a"])
}

@Test func importedAlreadyOfNothingOrJunkIsEmpty() {
    #expect(ImportFlow.importedAlready(Data()).isEmpty)
    #expect(ImportFlow.importedAlready(Data("null".utf8)).isEmpty)
    #expect(ImportFlow.importedAlready(Data("[]".utf8)).isEmpty)
    #expect(ImportFlow.importedAlready(Data("not json".utf8)).isEmpty)
}
