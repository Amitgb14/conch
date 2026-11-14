import Foundation
import Testing
@testable import ConchKit

private func cm(_ name: String, _ state: String, id: String? = nil) -> CloudMachine {
    CloudMachine(id: id ?? "m_" + String(repeating: "0", count: 16 - name.count) + name, name: name, size: "standard", region: "us-west", state: state)
}

// Where: this Mac, then the other machines (cloud ones are Conch Cloud's),
// then Conch Cloud, which says to sign in when nobody has.
@Test func projectPlacesListsMachinesThenCloud() {
    let ms = [PlaceMachine(id: "local", label: "local", online: true),
              PlaceMachine(id: "devbox", label: "devbox", online: true),
              PlaceMachine(id: "old", label: "old", online: false),
              PlaceMachine(id: "box", label: "box · cloud", online: false, cloud: true)]
    let out = projectPlaces(machines: ms, signedIn: false, cloudMachines: 0, offerCloud: true)
    #expect(out.map(\.place) == [.machine("local"), .machine("devbox"), .machine("old"), .cloud])
    #expect(out.map(\.title) == ["This Mac", "devbox", "old", "Conch Cloud"])
    #expect(out.map(\.enabled) == [true, true, false, true])
    #expect(out.last?.note == "Sign in to use")
    #expect(out[2].note == "offline")

    let signed = projectPlaces(machines: ms, signedIn: true, cloudMachines: 2, offerCloud: true, excluding: "local")
    #expect(signed.map(\.place) == [.machine("devbox"), .machine("old"), .cloud])
    #expect(signed.last?.note == "2 machines")
    #expect(projectPlaces(machines: ms, signedIn: true, cloudMachines: 1, offerCloud: true).last?.note == "1 machine")
    #expect(projectPlaces(machines: ms, signedIn: true, cloudMachines: 0, offerCloud: true).last?.note == "a new machine")
    #expect(Set(out.map(\.id)).count == out.count)
}

// The name a new machine is offered: the project's, made one Conch Cloud
// takes (^[a-z][a-z0-9-]{0,30}$), and not one the account has.
@Test func cloudMachineNames() {
    #expect(defaultCloudMachineName(for: "api", taken: []) == "api")
    #expect(defaultCloudMachineName(for: "My API_server", taken: []) == "my-api-server")
    #expect(defaultCloudMachineName(for: "2fa", taken: []) == "box-2fa")
    #expect(defaultCloudMachineName(for: "", taken: []) == "box")
    #expect(defaultCloudMachineName(for: "---", taken: []) == "box")
    #expect(defaultCloudMachineName(for: "api", taken: ["api"]) == "api-2")
    #expect(defaultCloudMachineName(for: "api", taken: ["api", "api-2"]) == "api-3")
    let long = defaultCloudMachineName(for: String(repeating: "a", count: 50), taken: [String(repeating: "a", count: 31)])
    #expect(long.count <= 31 && long.hasSuffix("-2") && validCloudMachineName(long))
    #expect(validCloudMachineName("box") && validCloudMachineName("a-1"))
    #expect(!validCloudMachineName("") && !validCloudMachineName("1box") && !validCloudMachineName("Box")
            && !validCloudMachineName("a b") && !validCloudMachineName(String(repeating: "a", count: 32)))
}

// The machine offered first: one running, else one asleep, else a new one;
// one being destroyed or in error is never it.
@Test func cloudPickDefault() {
    #expect(defaultCloudPick([cm("a", "asleep"), cm("b", "running")]) == .existing(id: cm("b", "running").id))
    #expect(defaultCloudPick([cm("a", "error"), cm("b", "asleep")]) == .existing(id: cm("b", "asleep").id))
    #expect(defaultCloudPick([cm("a", "destroying"), cm("b", "error")]) == nil)
    #expect(defaultCloudPick([]) == nil)
    #expect(cm("a", "destroying").pickable == false && cm("a", "error").pickable == false && cm("a", "waking").pickable)
}

// What readying a machine takes, said in the sheet as it happens.
@Test func cloudPrepSteps() {
    let ms = [cm("a", "asleep"), cm("b", "running"), cm("c", "sleeping")]
    #expect(cloudPrep(.new(name: "box", size: "standard"), machines: ms) == [.making("box"), .connecting("box")])
    #expect(cloudPrep(.existing(id: ms[0].id), machines: ms) == [.waking("a"), .connecting("a")])
    #expect(cloudPrep(.existing(id: ms[1].id), machines: ms) == [.connecting("b")])
    #expect(cloudPrep(.existing(id: ms[2].id), machines: ms) == [.waking("c"), .connecting("c")])
    #expect(CloudPrep.making("box").text == "Making box…")
    #expect(CloudPrep.waking("box").text == "Waking box…")
    #expect(CloudPrep.connecting("box").text == "Connecting to box…")
    #expect(cm("a", "archived").asleep && !cm("a", "waking").asleep)
}

// A cloud machine's id is its catalog machine's target, conch:<id>.
@Test func cloudCatalogMachine() {
    let id = "m_0123456789abcdef"
    let cat = [MachineEntry(id: "devbox", label: "devbox", target: "devbox.lan"),
               MachineEntry(id: "box-1", label: "box", target: "conch:" + id)]
    #expect(catalogMachine(forCloud: id, in: cat) == "box-1")
    #expect(catalogMachine(forCloud: "m_ffffffffffffffff", in: cat) == nil)
    #expect(cloudMachine(forCatalog: "box-1", catalog: cat, machines: [cm("box", "asleep", id: id)])?.name == "box")
    #expect(cloudMachine(forCatalog: "devbox", catalog: cat, machines: [cm("box", "asleep", id: id)]) == nil)
}

// `conch cloud machine sizes -json`; the size offered is standard when
// the plan has it.
@Test func cloudSizesDecodeAndDefault() throws {
    let out = """
    {"sizes":[{"id":"small","name":"Small","vcpu":2,"memory_gb":4,"disk_gb":30,"plans":["free","pro"]},
              {"id":"standard","name":"Standard","vcpu":4,"memory_gb":8,"disk_gb":60,"plans":["pro"]},
              {"id":"xl","name":"XL","vcpu":16,"memory_gb":32,"disk_gb":240,"plans":[]}],
     "regions":[{"id":"us-west","name":"US West"}]}
    """
    let s = try JSONDecoder().decode(CloudSizes.self, from: Data(out.utf8))
    #expect(s.sizes.map(\.id) == ["small", "standard", "xl"])
    #expect(s.sizes[1].label == "Standard · 4 vCPU · 8 GB")
    #expect(s.sizes[0].offered(to: "free") && !s.sizes[1].offered(to: "free") && s.sizes[2].offered(to: "free"))
    #expect(s.sizes[1].offered(to: nil))
    #expect(defaultCloudSize(s.sizes, plan: "pro") == "standard")
    #expect(defaultCloudSize(s.sizes, plan: "free") == "small")
    #expect(defaultCloudSize([], plan: "pro") == nil)
}

// From this Mac moves a project there, so it is Conch Cloud's alone.
@Test func addProjectModesByPlace() {
    #expect(AddProjectMode.offered(cloud: false) == [.folder, .clone, .create])
    #expect(AddProjectMode.offered(cloud: true) == [.clone, .create, .fromMac])
    #expect(AddProjectMode.fromMac.title == "From this Mac")
}
