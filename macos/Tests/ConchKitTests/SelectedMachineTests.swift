import Foundation
import Testing
@testable import ConchKit

// Every kind of tab says its machine: panes "machine|id", the rest
// "kind|machine|…"; a pane id alone is this computer's.
@Test func tabMachines() {
    #expect(tabMachine(key("bot", "p3")) == "bot")
    #expect(tabMachine("p3") == "local")
    #expect(tabMachine(filesKey("busybox", "api")) == "busybox")
    #expect(tabMachine(changesKey("bot", "api", "feature/x|y")) == "bot")
    #expect(tabMachine(commitsKey("local", "api")) == "local")
    #expect(tabMachine(sessionsKey("bot", "api")) == "bot")
    #expect(tabMachine(setupKey("bot", "api", dir: "/home/conch/api")) == "bot")
    #expect(tabMachine(taskKey("bot", "g", "b")) == "bot")
    #expect(tabMachine("files||api") == "local")
    // A machine whose id is a tab kind's word is still a pane's machine.
    #expect(tabMachine("files|p3") == "files")
}

@Test func machinePlaces() {
    let mac = machinePlace(id: "local", label: "local", cloud: false, sandbox: false, online: true)
    #expect(mac.kind == .local && mac.title == "This Mac")
    #expect(mac.help == "What is selected is on this Mac")
    let bot = machinePlace(id: "c_bot", label: "bot", cloud: true, sandbox: false, online: true)
    #expect(bot.kind == .cloud && bot.title == "bot · Conch Cloud")
    let box = machinePlace(id: "busybox", label: "busybox", cloud: false, sandbox: false, online: false)
    #expect(box.title == "busybox · remote")
    #expect(box.help.hasSuffix("(not connected)"))
    let sb = machinePlace(id: "x", label: "x · daytona", cloud: false, sandbox: true, online: true)
    #expect(sb.title == "x · sandbox")
    #expect(machinePlace(id: "m9", label: "", cloud: false, sandbox: false, online: true).name == "m9")
}

// The sidebar row wins, then the tab in front, then nothing.
@Test func selectedMachines() {
    #expect(selectedMachine(row: "busybox", focusedTab: key("bot", "p1")) == "busybox")
    #expect(selectedMachine(row: nil, focusedTab: filesKey("bot", "api")) == "bot")
    #expect(selectedMachine(row: nil, focusedTab: nil) == nil)
}

// A cloud machine's menu sleeps or wakes it and deletes it; delete runs
// with -y, the app having asked.
@Test func cloudMachineMenus() {
    #expect(cloudMachineMenu(machine: "c", online: true).map(\.title) == ["", "Sleep", "Delete Machine…"])
    #expect(cloudMachineMenu(machine: "c", online: false).map(\.title) == ["", "Wake", "Delete Machine…"])
    #expect(cloudMachineMenu(machine: "c", online: false).last?.action == .cloud(machine: "c", verb: "rm"))
    #expect(cloudMachineArgs(verb: "rm", id: "m_1") == ["cloud", "machine", "rm", "m_1", "-y"])
    #expect(cloudMachineArgs(verb: "wake", id: "m_1") == ["cloud", "machine", "wake", "m_1"])
}
