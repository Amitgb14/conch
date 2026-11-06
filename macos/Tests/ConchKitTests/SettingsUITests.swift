import Foundation
import Testing
@testable import ConchKit

private func field(_ key: String, _ kind: String, options: [String] = [], min: Int? = nil, max: Int? = nil) throws -> SettingsField {
    var o: [String: Any] = ["key": key, "label": key, "kind": kind]
    if !options.isEmpty { o["options"] = options.map { ["value": $0, "label": $0.isEmpty ? "Default" : $0] } }
    if let min { o["min"] = min }
    if let max { o["max"] = max }
    return try JSONDecoder().decode(SettingsField.self, from: JSONSerialization.data(withJSONObject: o))
}

private func section(_ key: String, _ fields: [SettingsField], title: String = "T", description: String = "") -> SettingsSection {
    SettingsSection(id: key, key: key, title: title, description: description, icon: "x", fields: fields)
}

@Test func controlsForSettings() throws {
    let ui = section("ui", [])
    #expect(settingsControl(ui, try field("theme", "select", options: ["conch"])) == .theme)
    #expect(settingsControl(ui, try field("accent", "select", options: ["", "teal"])) == .swatches)
    #expect(settingsControl(ui, try field("mouse", "bool")) == .toggle)
    #expect(settingsControl(ui, try field("diff", "select", options: ["", "side", "unified"])) == .segments)
    // Four or more choices, or long labels: a menu.
    #expect(settingsControl(ui, try field("x", "select", options: ["a", "b", "c", "d"])) == .menu(agents: false))
    // Agents always get a menu with their marks, even with three.
    #expect(settingsControl(section("agents", []), try field("default", "select", options: ["claude", "codex"])) == .menu(agents: true))
    #expect(settingsControl(section("verify", []), try field("reviewer", "select", options: ["", "codex"])) == .menu(agents: true))
    #expect(settingsControl(section("app", []), try field("tabs", "select", options: ["", "left", "right"])) == .pictures)
    #expect(settingsControl(section("app", []), try field("text_size", "int")) == .slider(min: 80, max: 160, step: 5, unit: "%"))
    #expect(settingsControl(section("app", []), try field("font_size", "float")) == .stepper(step: 0.5))
    #expect(settingsControl(section("verify", []), try field("auto_fix", "int")) == .stepper(step: 1))
    #expect(settingsControl(section("keys", []), try field("prefix", "string")) == .keys)
    #expect(settingsControl(section("notify", []), try field("quiet_start", "string")) == .time)
    #expect(settingsControl(section("notify", []), try field("limit_at", "ints")) == .chips)
    #expect(settingsControl(section("brain", []), try field("base_url", "string")) == .url)
    #expect(settingsControl(section("web", []), try field("url", "string")) == .url)
    #expect(settingsControl(section("brain", []), try field("api_key_env", "string")) == .envVar)
    #expect(settingsControl(section("sandbox", []), try field("api_key", "secret")) == .secret)
    #expect(settingsControl(section("notify", []), try field("command", "text")) == .multiline)
    #expect(settingsControl(section("pane", []), try field("default_command", "string")) == .text)
}

@Test func settingsProblems() throws {
    let s = try field("x", "string")
    #expect(settingsProblem(.url, s, "") == nil && settingsProblem(.url, s, "https://api.example/v1") == nil)
    #expect(settingsProblem(.url, s, "api.example") != nil && settingsProblem(.url, s, "ftp://x") != nil && settingsProblem(.url, s, "http://") != nil)
    #expect(settingsProblem(.time, s, "22:00") == nil && settingsProblem(.time, s, "7:05") == nil)
    #expect(settingsProblem(.time, s, "24:00") != nil && settingsProblem(.time, s, "22") != nil && settingsProblem(.time, s, "22:5") != nil)
    #expect(settingsProblem(.envVar, s, "ANTHROPIC_API_KEY") == nil && settingsProblem(.envVar, s, "MY KEY") != nil && settingsProblem(.envVar, s, "1X") != nil)
    let port = try field("port", "int", min: 0, max: 65535)
    #expect(settingsProblem(.stepper(step: 1), port, "8722") == nil)
    #expect(settingsProblem(.stepper(step: 1), port, "70000") == "at most 65535")
    #expect(settingsProblem(.stepper(step: 1), port, "-1") == "at least 0")
    #expect(settingsProblem(.stepper(step: 1), port, "1.5") == "a whole number")
    #expect(settingsProblem(.stepper(step: 1), port, "x") == "a number")
    #expect(settingsProblem(.text, s, "anything") == nil)
}

@Test func steppersStayInBounds() throws {
    let f = try field("auto_fix", "int", min: 0)
    #expect(stepped(nil, by: 1, f) == 1 && stepped(2, by: -1, f) == 1 && stepped(0, by: -1, f) == 0)
    let size = try field("font_size", "float")
    #expect(stepped(nil, by: 0.5, size, fallback: 14) == 14.5)
    #expect(stepped(0.2, by: -0.5, size) == 0) // no lower bound given: never below 0
    let port = try field("port", "int", min: 0, max: 10)
    #expect(stepped(10, by: 1, port) == 10)
}

@Test func quietHoursAsADay() {
    #expect(parseTime("00:00") == 0 && parseTime("23:59") == 1439 && parseTime(" 8:30 ") == 510)
    #expect(parseTime("") == nil && parseTime("8:3") == nil && parseTime("25:00") == nil && parseTime("a:bc") == nil)
    #expect(quietSpans("09:00", "17:00") == [0.375...(17.0 / 24)])
    // Past midnight: two spans, to the end and from the start.
    let s = quietSpans("22:00", "08:00")
    #expect(s.count == 2 && s[0] == (22.0 / 24)...1 && s[1] == 0...(8.0 / 24))
    #expect(quietSpans("22:00", nil).isEmpty && quietSpans(nil, nil).isEmpty && quietSpans("10:00", "10:00").isEmpty && quietSpans("x", "08:00").isEmpty)
    #expect(accentColors["teal"] == "#0F7B8A" && accentColors.count == 8)
}

@Test func keysRecorded() {
    #expect(keyNotation(ctrl: true, alt: false, shift: false, key: "b") == "ctrl+b")
    #expect(keyNotation(ctrl: true, alt: false, shift: false, key: "A") == "ctrl+a")
    #expect(keyNotation(ctrl: false, alt: true, shift: true, key: "x") == "alt+shift+x")
    #expect(keyNotation(ctrl: true, alt: false, shift: false, key: " ") == "ctrl+space")
    #expect(keyNotation(ctrl: false, alt: false, shift: false, key: "b") == nil) // would take a letter from typing
    #expect(keyNotation(ctrl: true, alt: false, shift: false, key: "") == nil)
    #expect(keyCaps("ctrl+b") == ["⌃", "B"] && keyCaps("alt+shift+left") == ["⌥", "⇧", "←"] && keyCaps("ctrl+space") == ["⌃", "Space"])
}

@Test func sidebarGroupsAndSearch() throws {
    let ids = ["ui", "keys", "notify", "agents", "verify", "git", "brain", "shell", "pane", "remote", "update", "web", "app", "sandbox", "actions", "permissions"]
    let g = SettingsNav.grouped(SettingsNav.sidebar(ids))
    #expect(g.map(\.0) == ["General", "Agents", "Workspace", "Machines", "Advanced"])
    #expect(g[1].1 == ["agents", "skills", "verify", "brain"])
    #expect(g.flatMap(\.1).count == ids.count + 1) // every section once, Skills & MCP added
    // A section the groups don't know still shows, at the end.
    #expect(SettingsNav.grouped(["ui", "newthing"]).last!.1.last == "newthing")
    #expect(SettingsNav.grouped(["newthing"]).map(\.1) == [["newthing"]])
    #expect(SettingsNav.grouped([]).isEmpty)

    let schema = [
        section("ui", [try field("mouse", "bool")], title: "Appearance", description: "Theme and mouse"),
        section("verify", [try field("auto_merge", "bool")], title: "Checks & autopilot"),
        section("git", [try field("squash", "bool")], title: "Git", description: "How a merge commit looks"),
    ]
    #expect(SettingsNav.search("", in: schema) == ["ui", "verify", "git", "skills"])
    #expect(SettingsNav.search("merge", in: schema) == ["verify", "git"]) // a key, and a description
    #expect(SettingsNav.search("MOUSE", in: schema) == ["ui"])
    #expect(SettingsNav.search("mcp", in: schema) == ["skills"])
    #expect(SettingsNav.search("factory", in: schema) == ["skills"])
    #expect(SettingsNav.search("theme mouse", in: schema) == ["ui"] && SettingsNav.search("theme git", in: schema).isEmpty)
}

@Test func autopilotAsALine() {
    var v: [String: JSONValue] = ["auto_pr": .bool(true), "auto_fix": .number(2), "reviewer": .string("codex"),
                                  "commands": .object(["r1": .string("go test ./...")])]
    let s = autopilotSteps(v)
    #expect(s.map(\.title) == ["Agent finishes", "Check", "Review", "Pull request", "Fix CI", "Merge"])
    #expect(s.map(\.on) == [true, true, true, true, true, false])
    #expect(s[1].detail == "1 project" && s[2].detail == "codex" && s[4].detail == "2 tries")
    #expect(autopilotToggle(s[0], lastReviewer: nil) == nil && autopilotToggle(s[1], lastReviewer: nil) == nil)
    #expect(autopilotToggle(s[3], lastReviewer: nil) == .bool(false) && autopilotToggle(s[5], lastReviewer: nil) == .bool(true))
    #expect(autopilotToggle(s[4], lastReviewer: nil) == .number(0))
    #expect(autopilotToggle(s[2], lastReviewer: nil) == .string(""))
    v = [:]
    let off = autopilotSteps(v)
    #expect(off.map(\.on) == [true, false, false, false, false, false] && off[1].detail == "no project has one")
    #expect(autopilotToggle(off[4], lastReviewer: nil) == .number(2))
    #expect(autopilotToggle(off[2], lastReviewer: "claude") == .string("claude") && autopilotToggle(off[2], lastReviewer: "") == .string("codex"))
}

@Test func skillCoverageAcrossFolders() {
    let r = AgentSkillResult(changes: [
        SkillChange(skill: "conch", path: "/a/conch/SKILL.md", agents: ["claude"], action: "same"),
        SkillChange(skill: "conch-team", path: "/a/conch-team/SKILL.md", agents: ["claude"], action: "create"),
        SkillChange(skill: "conch", path: "/b/conch/SKILL.md", agents: ["codex"], action: "same"),
        SkillChange(skill: "conch-team", path: "/b/conch-team/SKILL.md", agents: ["codex"], action: "skip"),
        SkillChange(path: "/c/conch/SKILL.md", agents: ["devin"], action: "update"), // an older server: conch
    ])
    let c = skillCoverage(r)
    #expect(c.map(\.name) == ["conch", "conch-team"])
    #expect(c[0].have == 2 && c[0].of == 3 && !c[0].everywhere)
    #expect(c[1].have == 0 && c[1].of == 1) // the person's own is not counted
    #expect(skillCoverage(AgentSkillResult(changes: [])).isEmpty)
    #expect(SkillCoverage(name: "x", have: 0, of: 0).everywhere == false)
    #expect(conchSkillBlurbs.count == 6 && conchSkillBlurbs["conch-autopilot"] != nil)
}
