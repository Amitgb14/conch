import Foundation
import Testing
@testable import ConchKit

private func tablesForm() throws -> (SettingsForm, SettingsSection, SettingsSection, SettingsSection) {
    let json = #"""
    {"config":{"permissions":[{"tool":"Bash","match":"go test *","action":"allow"},{"tool":"Bash","match":"git push --force*","action":"deny"}],
               "sandbox":{"daytona":{"api_key_env":"DAYTONA_API_KEY"}}},
     "raw":"","version":"v1","path":"/x/config.toml","exists":true,
     "schema":[
      {"id":"permissions","key":"","title":"Permission rules","description":"d","icon":"lock","fields":[
        {"key":"permissions","label":"Rules","kind":"tables","fields":[
          {"key":"tool","label":"Tool","kind":"string"},{"key":"match","label":"Matching","kind":"string"},
          {"key":"action","label":"Answer","kind":"select","options":[{"value":"allow","label":"allow"},{"value":"deny","label":"deny"}]},
          {"key":"keep","label":"Keep","kind":"bool","default":true}]}]},
      {"id":"sandbox","key":"sandbox","title":"Sandboxes","description":"d","icon":"box","fields":[
        {"key":"","label":"Providers","kind":"keyed","keyLabel":"Provider","suggest":["daytona","boat"],"fields":[
          {"key":"api_key_env","label":"Key","kind":"string"}]}]},
      {"id":"actions","key":"","title":"Actions","description":"d","icon":"zap","fields":[
        {"key":"actions","label":"Actions","kind":"tables","fields":[{"key":"name","label":"Name","kind":"string"}]}]}]}
    """#
    let p = try JSONDecoder().decode(SettingsPayload.self, from: Data(json.utf8))
    return (SettingsForm(p), p.schema[0], p.schema[1], p.schema[2])
}

// Rows of an array of tables: read, changed in place, added, moved within
// bounds and removed; an emptied list leaves the key out.
@Test func settingsTableRows() throws {
    var (f, perms, _, actions) = try tablesForm()
    let rules = perms.fields[0], tool = rules.fields![0], match = rules.fields![1], keep = rules.fields![3]
    #expect(perms.simple && perms.tableNote == nil)
    #expect(f.rows(perms, rules).count == 2)
    #expect(f.value(perms, match, at: .row(table: "permissions", index: 1)) == .string("git push --force*"))
    #expect(f.value(perms, keep, at: .row(table: "permissions", index: 0)) == .bool(true)) // absent: its default
    #expect(f.value(perms, tool, at: .row(table: "permissions", index: 9)) == nil)
    #expect(f.value(perms, tool, at: .row(table: "nope", index: 0)) == nil)

    f.set(perms, tool, at: .row(table: "permissions", index: 0), .string("Edit"))
    #expect(f.rows(perms, rules)[0]["tool"] == .string("Edit") && f.dirty)
    f.set(perms, tool, at: .row(table: "permissions", index: 7), .string("x")) // out of range: nothing
    #expect(f.rows(perms, rules).count == 2)

    f.moveRow(perms, rules, at: 1, by: -1)
    #expect(f.rows(perms, rules)[0]["action"] == .string("deny"))
    f.moveRow(perms, rules, at: 0, by: -1) // already first
    f.moveRow(perms, rules, at: 1, by: 1)  // already last
    #expect(f.rows(perms, rules)[0]["action"] == .string("deny") && f.rows(perms, rules).count == 2)

    let i = f.addRow(perms, rules)
    #expect(i == 2 && f.rows(perms, rules)[2].isEmpty)
    f.removeRow(perms, rules, at: 2)
    f.removeRow(perms, rules, at: 5) // nothing there
    #expect(f.rows(perms, rules).count == 2)
    f.removeRow(perms, rules, at: 0)
    f.removeRow(perms, rules, at: 0)
    #expect(f.rows(perms, rules).isEmpty && f.config["permissions"] == nil)

    // A list that was not there at all starts with the first row added.
    let acts = actions.fields[0]
    #expect(f.rows(actions, acts).isEmpty)
    f.addRow(actions, acts)
    f.set(actions, acts.fields![0], at: .row(table: "actions", index: 0), .string("Run tests"))
    #expect(f.config["actions"] == .array([.object(["name": .string("Run tests")])]))
}

// Tables keyed by name: [sandbox]'s providers are the section's own table.
@Test func settingsKeyedEntries() throws {
    var (f, _, sandbox, _) = try tablesForm()
    let providers = sandbox.fields[0], key = providers.fields![0]
    #expect(f.entries(sandbox, providers) == ["daytona"])
    #expect(f.value(sandbox, key, at: .entry(table: "", name: "daytona")) == .string("DAYTONA_API_KEY"))
    #expect(entrySuggestions(providers, have: f.entries(sandbox, providers)) == ["boat"])

    let added = f.addEntry(sandbox, providers, " boat ")
    let again = f.addEntry(sandbox, providers, "boat") // there already
    let blank = f.addEntry(sandbox, providers, "   ")  // blank
    #expect(added && !again && !blank)
    #expect(f.entries(sandbox, providers) == ["boat", "daytona"])
    f.set(sandbox, key, at: .entry(table: "", name: "boat"), .string("BOAT_KEY"))
    #expect(f.config["sandbox"]?.object?["boat"] == .object(["api_key_env": .string("BOAT_KEY")]))
    f.set(sandbox, key, at: .entry(table: "", name: "missing"), .string("x")) // no such entry: nothing
    #expect(f.entries(sandbox, providers) == ["boat", "daytona"])

    f.removeEntry(sandbox, providers, "daytona")
    f.removeEntry(sandbox, providers, "boat")
    #expect(f.entries(sandbox, providers).isEmpty && f.config["sandbox"] == nil)
}

// A row's heading and detail, in a few words.
@Test func settingsRowWords() {
    #expect(settingsRowTitle("actions", ["name": .string("Run tests"), "run": .string("go test ./...")]) == "Run tests")
    #expect(settingsRowTitle("actions", ["run": .string("gh pr create")]) == "gh pr create")
    #expect(settingsRowTitle("actions", [:]) == "New action")
    #expect(settingsRowTitle("permissions", ["tool": .string("Bash"), "match": .string("go test *"), "action": .string("deny")]) == "Deny Bash go test *")
    #expect(settingsRowTitle("permissions", ["match": .string("*")]) == "Allow any tool *")
    #expect(settingsRowTitle("permissions", [:]) == "New rule")
    #expect(settingsRowTitle("other", ["b": .string("second"), "a": .string("first")]) == "first")
    #expect(settingsRowTitle("other", ["a": .bool(true)]) == "New")
    #expect(settingsRowDetail("actions", ["on": .array([.string("branch"), .string("project")])]) == "branch, project")
    #expect(settingsRowDetail("actions", [:]) == "every row")
    #expect(settingsRowDetail("permissions", ["agent": .string("codex")]) == "codex · every project")
    #expect(settingsRowDetail("other", [:]) == "")
}

// Model and theme fields: a model menu for the brain's two, a combo for a
// string field that offers names; models ordered for summaries, a model
// set in the file and not listed kept.
@Test func settingsModelsAndCombos() {
    let brain = SettingsSection(id: "brain", key: "brain", title: "Brain", description: "", icon: "", fields: [])
    let shell = SettingsSection(id: "shell", key: "shell", title: "T", description: "", icon: "", fields: [])
    #expect(settingsControl(brain, SettingsField(key: "model", label: "Model", kind: "string")) == .model(summary: false))
    #expect(settingsControl(brain, SettingsField(key: "summary_model", label: "S", kind: "string")) == .model(summary: true))
    #expect(settingsControl(shell, SettingsField(key: "omz_theme", label: "T", kind: "string", suggest: ["agnoster"])) == .combo)
    #expect(settingsControl(shell, SettingsField(key: "omz_theme", label: "T", kind: "string", suggest: [])) == .text)
    #expect(settingsControl(shell, SettingsField(key: "omz_theme", label: "T", kind: "string")) == .text)

    let ms = [BrainModel(id: "opus", label: "Opus"), BrainModel(id: "haiku", label: "Haiku", small: true), BrainModel(id: "qwen3", label: "qwen3")]
    let big = modelOptions(ms, summary: false, value: "")
    #expect(big.map(\.value) == ["", "opus", "haiku", "qwen3"])
    #expect(big[1].label == "Opus — opus" && big[3].label == "qwen3")
    #expect(modelOptions(ms, summary: true, value: "").map(\.value) == ["", "haiku", "opus", "qwen3"])
    let kept = modelOptions(ms, summary: false, value: "my-model")
    #expect(kept.last?.value == "my-model" && kept.last?.label.contains("from the file") == true)
    #expect(modelOptions([], summary: false, value: "").map(\.value) == [""])

    let q = modelsQuery(["provider": .string("openai"), "base_url": .string("http://localhost:11434/v1")])
    #expect(q.map(\.name) == ["provider", "base_url", "api_key_env"] && q[0].value == "openai" && q[2].value == "")
    #expect(modelsQuery(nil).allSatisfy { $0.value == "" })
    let decoded = try? JSONDecoder().decode(BrainModels.self, from: Data(#"{"models":[{"id":"haiku","label":"Haiku","small":true}],"error":"set $K"}"#.utf8))
    #expect(decoded?.models.first?.small == true && decoded?.error == "set $K")
}

@Test func zshPrompts() {
    let shell = SettingsSection(id: "shell", key: "shell", title: "T", description: "", icon: "", fields: [])
    let brain = SettingsSection(id: "brain", key: "brain", title: "B", description: "", icon: "", fields: [])
    #expect(settingsShowsPrompts(shell, SettingsField(key: "omz_theme", label: "T", kind: "string")))
    #expect(!settingsShowsPrompts(brain, SettingsField(key: "omz_theme", label: "T", kind: "string")))
    #expect(!settingsShowsPrompts(shell, SettingsField(key: "other", label: "T", kind: "string")))
    #expect(zshPromptQuery(" ys ").first?.value == "ys" && zshPromptQuery("").first?.name == "theme")

    let json = ##"{"prompt":{"theme":"bira","lines":[[{"text":"╭─","fg":"#cc6666"},{"text":"amit","bold":true}],[{"text":"╰─$ ","bg":"#1d1f21"}]],"right":[{"text":"‹main›"}]}}"##
    let a = try? JSONDecoder().decode(ZshPromptAnswer.self, from: Data(json.utf8))
    #expect(a?.prompt?.text == ["╭─amit", "╰─$ "])
    #expect(a?.prompt?.lines[0][0].fg == "#cc6666" && a?.prompt?.lines[0][1].bold == true && a?.prompt?.lines[0][1].fg == nil)
    #expect(a?.prompt?.right?.first?.text == "‹main›" && a?.error == nil)
    let failed = try? JSONDecoder().decode(ZshPromptAnswer.self, from: Data(#"{"error":"no one theme: .zshrc names none"}"#.utf8))
    #expect(failed?.prompt == nil && failed?.error?.contains(".zshrc") == true)
    let noRight = try? JSONDecoder().decode(ZshPrompt.self, from: Data(#"{"theme":"x","lines":[]}"#.utf8))
    #expect(noRight?.right == nil && noRight?.text == [])
    #expect(zshPromptBG == RGB("#1d1f21"))
}

@Test func powerlineFonts() {
    #expect(withoutPowerline("\u{E0B0} \u{E0A0} main \u{E0B2}x") == "▶ ⎇ main ◀x")
    #expect(withoutPowerline("") == "" && withoutPowerline("plain ✗") == "plain ✗")
    let fams = ["Menlo", "Anonymice Powerline", "Hack Nerd Font", "JetBrainsMono Nerd Font Mono", "Helvetica"]
    #expect(powerlineFamily(fams) { _ in true } == "JetBrainsMono Nerd Font Mono")
    #expect(powerlineFamily(fams) { $0 != "JetBrainsMono Nerd Font Mono" } == "Hack Nerd Font")
    #expect(powerlineFamily(fams) { $0.contains("Powerline") } == "Anonymice Powerline")
    #expect(powerlineFamily(fams) { _ in false } == nil)
    #expect(powerlineFamily(["Menlo", "Helvetica"]) { _ in true } == nil)
    #expect(powerlineFamily([]) { _ in true } == nil)
}

// A provider added, or its key typed in, is an unsaved change: the
// section's own keyed table has no value as a field, and was counted as
// none, so the Save bar never came up for Tensorlake's key.
@Test func settingsKeyedEntriesCountAsChanges() throws {
    var (f, _, sandbox, _) = try tablesForm()
    let providers = sandbox.fields[0], key = providers.fields![0]
    #expect(settingsChanged(f, sandbox) == 0 && settingsChanged(f) == 0 && !f.dirty)
    f.addEntry(sandbox, providers, "boat")
    #expect(settingsChanged(f, sandbox) == 1 && settingsChanged(f) == 1 && f.dirty)
    #expect(settingsChangedShown(f, sandbox) == 1)
    f.set(sandbox, key, at: .entry(table: "", name: "boat"), .string("BOAT_KEY"))
    #expect(settingsChanged(f) == 1)
    // A key changed in a provider that was there already counts too.
    f.removeEntry(sandbox, providers, "boat")
    #expect(settingsChanged(f) == 0 && !f.dirty)
    f.set(sandbox, key, at: .entry(table: "", name: "daytona"), .string("OTHER"))
    #expect(settingsChanged(f) == 1)
    // And back as it was is no change.
    f.set(sandbox, key, at: .entry(table: "", name: "daytona"), .string("DAYTONA_API_KEY"))
    #expect(settingsChanged(f) == 0)
}

// A table of fixed fields inside a section — [trackers.linear] — is drawn
// as a card and edited in place: before, the window skipped the kind, so
// Trackers showed only its Projects list and no way to set a key.
@Test func settingsNestedTable() throws {
    let json = #"""
    {"config":{"trackers":{"linear":{"team":"ENG"}}},
     "raw":"","version":"v1","path":"/x/config.toml","exists":true,
     "schema":[
      {"id":"trackers","key":"trackers","title":"Trackers","description":"d","icon":"ticket","fields":[
        {"key":"linear","label":"Linear","kind":"table","fields":[
          {"key":"api_key","label":"API key","kind":"secret"},
          {"key":"team","label":"Team","kind":"string"},
          {"key":"api_url","label":"API address","kind":"string","default":"https://api.linear.app/graphql"}]},
        {"key":"jira","label":"Jira Cloud","kind":"table","fields":[
          {"key":"url","label":"Site","kind":"string"}]},
        {"key":"projects","label":"Projects","kind":"tables","fields":[
          {"key":"project","label":"Project","kind":"string"}]}]}]}
    """#
    let p = try JSONDecoder().decode(SettingsPayload.self, from: Data(json.utf8))
    var f = SettingsForm(p)
    let tr = p.schema[0], linear = tr.fields[0], jira = tr.fields[1]
    let key = linear.fields![0], team = linear.fields![1], url = linear.fields![2], site = jira.fields![0]
    // Every kind in it is drawn, so nothing is left to "edit in config.toml".
    #expect(tr.simple && tr.tableNote == nil && linear.drawn)

    #expect(f.value(tr, team, at: .nested(table: "linear")) == .string("ENG"))
    #expect(f.value(tr, url, at: .nested(table: "linear")) == .string("https://api.linear.app/graphql")) // its default
    #expect(f.value(tr, key, at: .nested(table: "linear")) == nil)
    #expect(f.value(tr, site, at: .nested(table: "jira")) == nil) // a table not in the file yet
    #expect(f.value(tr, team, at: .nested(table: "nope")) == nil)

    f.set(tr, key, at: .nested(table: "linear"), .string("lin_api_x"))
    #expect(f.config["trackers"]?.object?["linear"]?.object?["api_key"] == .string("lin_api_x") && f.dirty)
    #expect(f.config["trackers"]?.object?["linear"]?.object?["team"] == .string("ENG")) // the rest kept

    // A table not there yet is made by its first field.
    f.set(tr, site, at: .nested(table: "jira"), .string("https://acme.atlassian.net"))
    #expect(f.value(tr, site, at: .nested(table: "jira")) == .string("https://acme.atlassian.net"))
    // And emptied, it leaves the file rather than stay as an empty table.
    f.set(tr, site, at: .nested(table: "jira"), nil)
    #expect(f.config["trackers"]?.object?["jira"] == nil)
    // A table that is not one of its fields sets nothing.
    f.set(tr, team, at: .nested(table: "nope"), .string("x"))
    #expect(f.config["trackers"]?.object?["nope"] == nil)
}
