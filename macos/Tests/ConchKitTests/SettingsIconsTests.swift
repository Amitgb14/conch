import AppKit
import Foundation
import Testing
@testable import ConchKit

// Every section icon the schema names (internal/configweb/schema.go), and
// the Skills & MCP servers page's own.
let sectionIcons = ["palette", "keyboard", "bell", "bot", "shield-check", "git-merge", "sparkles", "terminal",
                    "square-terminal", "server", "refresh-cw", "smartphone", "laptop", "box", "zap", "lock", SettingsNav.skillsIcon]

@Test func settingsIconChoice() {
    #expect(settingsIcons(nil) == (.lucide, .plain))
    #expect(settingsIcons([:]) == (.lucide, .plain))
    #expect(settingsIcons(["icons": .string("sf"), "icon_style": .string("tiles")]) == (.sf, .tiles))
    #expect(settingsIcons(["icons": .string("sf-fill"), "icon_style": .string("tinted")]) == (.sfFill, .tinted))
    #expect(SettingsIconSet.allCases.map(\.rawValue) == ["", "sf", "sf-fill"])
    #expect(SettingsIconStyle.allCases.map(\.rawValue) == ["", "tinted", "tiles"])
    #expect(SettingsIconSet.allCases.allSatisfy { !$0.name.isEmpty && !$0.detail.isEmpty })
    #expect(SettingsIconKeys == ["icons", "icon_style"])
    #expect(settingsIcons(["icons": .string("phosphor"), "icon_style": .string("glass")]) == (.lucide, .plain))
    #expect(settingsIcons(["icons": .number(1)]) == (.lucide, .plain))

    let app = SettingsSection(id: "app", key: "app", title: "Mac app", description: "", icon: "laptop", fields: [])
    #expect(settingsControl(app, SettingsField(key: "icons", label: "I", kind: "select",
                                               options: [SettingsOption(value: "", label: "Lucide"), SettingsOption(value: "sf", label: "SF Symbols")])) == .iconSets)
    #expect(settingsControl(app, SettingsField(key: "icons", label: "I", kind: "string")) == .text)
    #expect(settingsControl(app, SettingsField(key: "icon_style", label: "S", kind: "select",
                                               options: [SettingsOption(value: "", label: "Plain"), SettingsOption(value: "tinted", label: "Tinted"),
                                                         SettingsOption(value: "tiles", label: "Tiles")])) == .segments)
}

// Each section has an SF Symbol this Mac draws, and a colour of its own
// for its tile; one nobody mapped falls back rather than drawing nothing.
@Test func settingsIconsCoverEverySection() {
    for icon in sectionIcons {
        let name = sfSymbol(lucide: icon)
        #expect(sfSymbols[icon] != nil, "no SF Symbol for \(icon)")
        #expect(NSImage(systemSymbolName: name, accessibilityDescription: nil) != nil, "\(name) is not an SF Symbol here")
        // SF Symbols Fill falls back to the line one where there is no fill.
        #expect(tileColors[icon] != nil, "no tile colour for \(icon)")
    }
    #expect(sfSymbol(lucide: "something-new") == "gearshape")
    #expect(settingsTileColor(lucide: "something-new") == RGB("#8e8e93"))
    let g = settingsTileGradient(lucide: "bell")
    #expect(g.bottom == RGB("#ff453a") && g.top != g.bottom)
    // Neighbours in the sidebar are told apart by colour.
    let general = ["palette", "keyboard", "bell"].map { settingsTileColor(lucide: $0) }
    #expect(Set(general.map(\.description)).count == 3)
}

// An icon setting edited counts on Appearance, where it is shown, not on
// Mac app, where it is kept.
@Test func iconChangesCountOnAppearance() throws {
    let json = ##"""
    {"config":{"ui":{"theme":"conch"},"app":{"tabs":""}},"raw":"","version":"v","path":"/x","exists":true,
     "schema":[{"id":"ui","key":"ui","title":"Appearance","description":"d","icon":"palette","fields":[{"key":"theme","label":"Theme","kind":"string"}]},
      {"id":"app","key":"app","title":"Mac app","description":"d","icon":"laptop","fields":[{"key":"tabs","label":"Tabs","kind":"string"},
        {"key":"icons","label":"Icon set","kind":"select","options":[{"value":"","label":"Lucide"},{"value":"sf","label":"SF Symbols"}]},
        {"key":"icon_style","label":"Style","kind":"select","options":[{"value":"","label":"Plain"},{"value":"tiles","label":"Tiles"}]}]},
      {"id":"git","key":"git","title":"Git","description":"d","icon":"git-merge","fields":[{"key":"x","label":"X","kind":"string"}]}]}
    """##
    let p = try JSONDecoder().decode(SettingsPayload.self, from: Data(json.utf8))
    var f = SettingsForm(p)
    let ui = p.schema[0], app = p.schema[1], git = p.schema[2]
    f.set(app, app.fields[1], .string("sf"))
    f.set(app, app.fields[2], .string("tiles"))
    #expect(settingsChangedShown(f, ui) == 2 && settingsChangedShown(f, app) == 0)
    #expect(settingsChanged(f, app) == 2) // the file's own count still sees them
    f.set(app, app.fields[0], .string("left"))
    f.set(ui, ui.fields[0], .string("nord"))
    #expect(settingsChangedShown(f, ui) == 3 && settingsChangedShown(f, app) == 1 && settingsChangedShown(f, git) == 0)
    #expect(settingsChanged(f) == 4)
}
