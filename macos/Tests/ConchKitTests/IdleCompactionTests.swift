import Foundation
import Testing
@testable import ConchKit

/// Settings → Agents → "Compact Claude while idle": [agents]
/// claude_idle_compaction, drawn from the Go schema as a toggle. Absent is
/// Claude's own default (on), and turning it off writes false beside the
/// section's other keys.
@Test func idleCompactionToggleReadsAndWritesTheKey() throws {
    let json = """
    {"config": {"agents": {"default": "codex"}}, "raw": "", "version": "v1", "path": "/x/config.toml", "exists": true,
     "schema": [{"id": "agents", "key": "agents", "title": "Agents", "description": "", "icon": "bot",
       "fields": [{"key": "claude_idle_compaction", "label": "Compact Claude while idle", "kind": "bool", "default": true}]}]}
    """
    let payload = try JSONDecoder().decode(SettingsPayload.self, from: Data(json.utf8))
    var form = SettingsForm(payload)
    let s = payload.schema[0], f = s.fields[0]
    #expect(settingsControl(s, f) == .toggle)
    #expect(form.value(s, f) == .bool(true))
    #expect(!form.dirty)

    form.set(s, f, .bool(false))
    #expect(form.value(s, f) == .bool(false))
    #expect(form.dirty)
    let body = try JSONSerialization.jsonObject(with: form.saveBody()) as? [String: Any]
    let agents = (body?["config"] as? [String: Any])?["agents"] as? [String: Any]
    #expect(agents?["claude_idle_compaction"] as? Bool == false)
    #expect(agents?["default"] as? String == "codex")

    // A file that already says false reads as off.
    var saved = payload
    saved.config["agents"] = .object(["claude_idle_compaction": .bool(false)])
    #expect(SettingsForm(saved).value(s, f) == .bool(false))
}
