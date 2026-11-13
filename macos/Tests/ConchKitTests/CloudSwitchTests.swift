import Foundation
import Testing
@testable import ConchKit

// The switch is conch's (config.CloudEnabled): the same cases as its Go
// test, so the app offers Cloud exactly when the conch it runs does.
@Test func cloudSwitchAsConchDecidesIt() {
    let on = "[cloud]\nenabled = true\n", off = "[ui]\ntheme = \"nord\"\n"
    let cases: [(env: String?, toml: String?, want: Bool)] = [
        (nil, nil, false),            // the release: hidden
        (nil, off, false),
        (nil, "[cloud]\napi = \"https://x\"\n", false), // a server alone is not the switch
        (nil, on, true),
        (nil, "[cloud]\nenabled = false\n", false),
        (nil, "[cloud.x]\nenabled = true\n", false),    // another table
        (nil, "[ui]\nenabled = true\n", false),
        (nil, "[cloud]\nenabled = true # tried\n", true),
        ("1", off, true), ("true", nil, true), (" YES ", nil, true), ("on", nil, true),
        ("0", on, false), ("false", on, false), ("off", on, false), // the variable wins either way
        ("2", nil, false), ("enabled", on, false),                  // only the words for on
        ("", on, true), ("   ", on, true),                          // blank is unset
    ]
    for c in cases {
        let env = c.env.map { [cloudEnv: $0] } ?? [:]
        #expect(cloudEnabled(toml: c.toml, env: env) == c.want, "CONCH_CLOUD=\(c.env ?? "unset") toml=\(c.toml ?? "nil")")
    }
}

// Hidden: Where offers no Conch Cloud, and a cloud machine somebody
// already has is a machine like the others. On: as before.
@Test func projectPlacesWithCloudHidden() {
    let ms = [PlaceMachine(id: "local", label: "local", online: true),
              PlaceMachine(id: "box", label: "box · cloud", online: true, cloud: true),
              PlaceMachine(id: "old", label: "old", online: false)]
    let hidden = projectPlaces(machines: ms, signedIn: true, cloudMachines: 2, offerCloud: false)
    #expect(hidden.map(\.place) == [.machine("local"), .machine("box"), .machine("old")])
    #expect(!hidden.contains { $0.title.contains("Conch Cloud") })
    #expect(projectPlaces(machines: [], signedIn: false, cloudMachines: 0, offerCloud: false).isEmpty)
    let shown = projectPlaces(machines: ms, signedIn: true, cloudMachines: 2, offerCloud: true)
    #expect(shown.map(\.place) == [.machine("local"), .machine("old"), .cloud])
    #expect(listedWithMachines(cloud: false, offerCloud: true))
    #expect(listedWithMachines(cloud: true, offerCloud: false))
    #expect(!listedWithMachines(cloud: true, offerCloud: true))
}

// Move to Conch Cloud… only while it is offered; Move to Another Machine
// either way. A cloud machine's own Wake, Sleep and Delete stay: it is a
// machine somebody has.
@Test func moveToCloudOnlyWhenOffered() {
    var snap = fixture()
    let node = TreeNode(id: "p:local|r1", kind: .project("local|r1"), title: "api")
    let i = snap.projects.firstIndex { $0.id == "r1" }!
    snap.projects[i].git = true
    let isCloudMove: (RowMenuItem) -> Bool = { if case .moveToCloud = $0.action { return true }; return false }
    let hidden = snap.menu(for: node, machine: "local")
    #expect(!hidden.contains(where: isCloudMove))
    #expect(hidden.contains(RowMenuItem("Move to Another Machine…", .moveProject(project: "local|r1"))))
    #expect(snap.menu(for: node, machine: "local", offerCloud: true).contains(where: isCloudMove))
    #expect(cloudMachineMenu(machine: "box", online: false).contains(RowMenuItem("Wake", .cloud(machine: "box", verb: "wake"))))
}
