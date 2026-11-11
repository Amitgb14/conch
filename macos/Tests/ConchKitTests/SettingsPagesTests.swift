import Foundation
import Testing
@testable import ConchKit

private func payload(_ config: String) throws -> SettingsPayload {
    let json = """
    {"config":\(config),"raw":"","version":"v1","path":"/c/config.toml","exists":true,
     "schema":[
      {"id":"git","key":"git","title":"Git","description":"d","icon":"git-merge","fields":[{"key":"squash","label":"Squash","kind":"bool"}]},
      {"id":"verify","key":"verify","title":"Checks & autopilot","description":"d","icon":"shield-check","fields":[
        {"key":"commands","label":"Check command per project","kind":"map","keyLabel":"Project ID"},
        {"key":"auto_pr","label":"PR","kind":"bool"}]},
      {"id":"keys","key":"keys","title":"Keys","description":"d","icon":"keyboard","fields":[
        {"key":"prefix","label":"Prefix","kind":"string"},
        {"key":"bind","label":"Rebind","kind":"map","suggest":["next_tab","zoom"]}]}
     ]}
    """
    return try JSONDecoder().decode(SettingsPayload.self, from: Data(json.utf8))
}

// A map is pairs: read sorted by key, set one at a time, taken out of the
// file when its last pair goes, and an empty value kept — in [keys.bind]
// it unbinds the key.
@Test func settingsMapPairs() throws {
    let p = try payload(#"{"verify":{"commands":{"r2":"make test","r1":"go test ./..."}}}"#)
    var f = SettingsForm(p)
    let verify = p.schema[1], commands = verify.fields[0], keys = p.schema[2], bind = keys.fields[1]
    #expect(commands.drawn && bind.drawn && verify.tableNote == nil)
    #expect(f.mapEntries(verify, commands).map(\.key) == ["r1", "r2"])
    #expect(f.mapEntries(verify, commands)[0].value == "go test ./...")
    #expect(f.mapEntries(keys, bind).isEmpty) // absent

    do { let changed = f.setMapEntry(verify, commands, " r3 ", "npm test"); #expect(changed) } // trimmed
    #expect(f.mapEntries(verify, commands).map(\.key) == ["r1", "r2", "r3"] && f.dirty)
    do { let changed = f.setMapEntry(verify, commands, "r3", "npm test"); #expect(!changed) } // no change
    do { let changed = f.setMapEntry(verify, commands, "  ", "x"); #expect(!changed) } // a blank key sets nothing
    do { let changed = f.setMapEntry(verify, commands, "r1", "go vet ./..."); #expect(changed) }
    #expect(f.mapEntries(verify, commands)[0].value == "go vet ./...")
    // The other keys of the table are left alone.
    #expect(f.config["verify"]?.object?["commands"]?.object?.count == 3)

    // An empty value is a value: unbinding a key.
    do { let changed = f.setMapEntry(keys, bind, "zoom", ""); #expect(changed) }
    #expect(f.config["keys"]?.object?["bind"]?.object?["zoom"] == .string(""))

    // The last pair taken out takes the map out, rather than leaving [keys.bind] empty.
    do { let changed = f.setMapEntry(keys, bind, "zoom", nil); #expect(changed) }
    #expect(f.config["keys"]?.object?["bind"] == nil)
    do { let changed = f.setMapEntry(keys, bind, "zoom", nil); #expect(!changed) } // already gone

    for k in ["r1", "r2", "r3"] { f.setMapEntry(verify, commands, k, nil) }
    #expect(f.config["verify"]?.object?["commands"] == nil)
}

// Git's page shows Checks & autopilot, so its count of unsaved changes
// includes [verify]'s, and the save bar's total counts each once.
@Test func gitPageCountsChecks() throws {
    let p = try payload(#"{}"#)
    var f = SettingsForm(p)
    let git = p.schema[0], verify = p.schema[1]
    #expect(settingsChangedShown(f, git) == 0)
    f.set(verify, verify.fields[1], .bool(true))
    f.setMapEntry(verify, verify.fields[0], "r1", "go test ./...")
    #expect(settingsChangedShown(f, git) == 2)
    f.set(git, git.fields[0], .bool(true))
    #expect(settingsChangedShown(f, git) == 3 && settingsChanged(f) == 3)
    f.revert()
    #expect(settingsChangedShown(f, git) == 0)
}

// Settings → Remote machines: this Mac first, then machines.json's in its
// order — turned off ones too — each said as it is now.
@Test func machineRowsInSettings() {
    let catalog = [
        MachineEntry(id: "m1", label: "busybox", target: "aghadge@10.0.0.115"),
        MachineEntry(id: "m2", label: "box", target: "daytona:abc"),
        MachineEntry(id: "m3", label: "old", target: "old.example", enabled: false),
        MachineEntry(id: "m4", label: "new", target: "new.example"),
    ]
    let states = [
        "local": MachineState(state: "", version: "0.1.11"),
        "m1": MachineState(state: "", version: "0.1.10", behind: 3),
        "m2": MachineState(state: "stopped", why: "sandbox box is stopped"),
    ]
    let r = machineRows(catalog: catalog, states: states)
    #expect(r.map(\.id) == ["local", "m1", "m2", "m3", "m4"])
    #expect(r[0].label == "This Mac" && r[0].isLocal && r[0].online && r[0].status == "online" && r[0].version == "0.1.11")
    #expect(r[1].detail == "aghadge@10.0.0.115" && r[1].online && r[1].status == "older build" && r[1].behind == 3)
    #expect(r[2].detail == "daytona sandbox" && !r[2].online && r[2].status == "stopped" && r[2].why == "sandbox box is stopped")
    #expect(r[3].status == "turned off" && !r[3].enabled && !r[3].online)
    #expect(r[4].status == "connecting" && r[4].enabled) // not connected yet
    // No machines: this Mac alone, connecting until it answers.
    let lone = machineRows(catalog: [], states: [:])
    #expect(lone.count == 1 && lone[0].status == "connecting")
    // An online machine's why is not shown as a fault.
    let ok = machineRows(catalog: [], states: ["local": MachineState(state: "", why: "old reason")])
    #expect(ok[0].why == "")
}
