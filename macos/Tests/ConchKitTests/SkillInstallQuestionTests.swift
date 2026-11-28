import ConchKit
import Foundation
import Testing

// The question asked before conch's skills are installed or removed: a
// line per skills folder, not per file — nine skills in five folders was
// forty-five lines, and the question's buttons went off the screen.
@Test func theInstallQuestionIsALinePerFolder() {
    let home = "/Users/me"
    let skills = ["conch", "conch-autopilot", "conch-cicd", "conch-factory", "conch-qa", "conch-review", "conch-setup", "conch-status", "conch-team"]
    var writes: [SkillChange] = []
    let folders: [(String, [String])] = [
        ("/Users/me/.claude/skills", ["claude"]),
        ("/Users/me/.agents/skills", ["codex", "gemini", "opencode", "copilot", "cline", "qwen", "goose"]),
        ("/Users/me/.config/devin/skills", ["devin"]),
        ("/Users/me/.gemini/config/skills", ["gemini"]),
        ("/opt/shared/skills", ["kiro"]),
    ]
    for (f, agents) in folders {
        for s in skills {
            writes.append(SkillChange(skill: s == "conch" ? nil : s, path: "\(f)/\(s)/SKILL.md", agents: agents, action: "create"))
        }
    }
    let text = skillWritesSummary(writes, home: home)
    let lines = text.components(separatedBy: "\n")
    #expect(lines.count == 5, "\(lines.count) lines")
    #expect(lines[0] == "~/.claude/skills — 9 skills for claude")
    #expect(lines[1] == "~/.agents/skills — 9 skills for codex, gemini, opencode, copilot, cline, qwen, goose")
    #expect(lines[4] == "/opt/shared/skills — 9 skills for kiro") // outside home: as it is

    // One skill, one folder: named.
    let one = skillWritesSummary([SkillChange(skill: "conch-qa", path: "/Users/me/.claude/skills/conch-qa/SKILL.md", agents: ["claude"], action: "remove")], home: home)
    #expect(one == "~/.claude/skills — conch-qa for claude")
    // Another machine's paths, with no home given: as they are.
    let remote = skillWritesSummary([SkillChange(path: "/home/ag/.claude/skills/conch/SKILL.md", agents: ["claude"], action: "create")])
    #expect(remote == "/home/ag/.claude/skills — conch for claude")
    // A home that is only a prefix of another folder's name is not home.
    let near = skillWritesSummary([SkillChange(path: "/Users/meg/.claude/skills/conch/SKILL.md", agents: ["claude"], action: "create")], home: home)
    #expect(near.hasPrefix("/Users/meg/"))
    #expect(skillWritesSummary([], home: home).isEmpty)
}

// A question listing what it acts on stops at a screenful: deleting 87
// old branches listed 87 lines and the buttons went off the screen.
@Test func aLongListIsCapped() {
    let names = (1...87).map { "conch/branch-\($0)" }
    let text = cappedLines(names)
    let lines = text.components(separatedBy: "\n")
    #expect(lines.count == 16)
    #expect(lines.first == "conch/branch-1" && lines[14] == "conch/branch-15")
    #expect(lines.last == "…and 72 more")
    #expect(cappedLines(Array(names.prefix(15))).components(separatedBy: "\n").count == 15) // exactly the limit: all of them
    #expect(cappedLines(["one"]) == "one")
    #expect(cappedLines([]) == "")
    #expect(cappedLines(names, max: 0).components(separatedBy: "\n").count == 87) // no limit asked: as before
}

