import ConchKit
import Foundation
import Testing

// Moving an agent between accounts, as the app says and offers it.

private let ranks = [
    AccountRank(account: "backup2", room: 96, reported: true, signedIn: true, agents: 3, usable: true, why: "96% room"),
    AccountRank(account: "backup", room: 41, reported: true, signedIn: true, agents: 1, usable: true, why: "41% room"),
    AccountRank(account: "work", room: 100, signedIn: false, usable: false, why: "not signed in"),
    AccountRank(account: "", room: 0, reported: true, signedIn: true, agents: 17, usable: false, why: "full · resets in 2h 10m"),
]

@Test func theMenuMarksConchsPickAndSaysWhyNot() {
    let m = switchAccountMenu(pane: "local|p12", on: nil, ranks: ranks, movedFrom: nil)
    #expect(m.title == "Switch Account")
    let titles = m.children.map(\.title)
    // The account it is on is not offered; conch's pick is marked once.
    #expect(!titles.contains { $0.hasPrefix("default") })
    #expect(titles.first == "backup2  ·  96% room  ·  conch's pick")
    #expect(titles.filter { $0.contains("conch's pick") }.count == 1)
    // One it cannot go to is said, and does nothing.
    let work = m.children.first { $0.title.hasPrefix("work") }
    #expect(work?.action == .divider && work?.title.contains("not signed in") == true)
    #expect(m.children.first { $0.title.hasPrefix("backup2") }?.action == .moveAgent(pane: "local|p12", account: "backup2"))
    #expect(m.children.last?.action == .moveAgent(pane: "local|p12", account: nil))
    #expect(!titles.contains { $0.hasPrefix("Move Back") })
}

@Test func moveBackOnlyOnceThereIsRoom() {
    // On backup2, having come off default, which is full: no move back.
    let onBackup = [AccountRank(account: "", room: 0, reported: true, usable: false, why: "full"),
                    AccountRank(account: "backup", room: 41, reported: true, usable: true)]
    #expect(moveBack(movedFrom: "default", ranks: onBackup) == nil)
    let m = switchAccountMenu(pane: "local|p20", on: "backup2", ranks: onBackup, movedFrom: "default")
    #expect(!m.children.map(\.title).contains { $0.hasPrefix("Move Back") })
    // default has reset: offered, and in the menu.
    let reset = [AccountRank(account: "", room: 97, reported: true, usable: true, why: "97% room")] + onBackup.dropFirst()
    #expect(moveBack(movedFrom: "default", ranks: reset) == "default")
    let m2 = switchAccountMenu(pane: "local|p20", on: "backup2", ranks: reset, movedFrom: "default")
    #expect(m2.children.contains { $0.title == "Move Back to default" && $0.action == .moveAgent(pane: "local|p20", account: "default") })
    // Never moved, or moved from an account no longer there: nothing.
    #expect(moveBack(movedFrom: nil, ranks: reset) == nil)
    #expect(moveBack(movedFrom: "", ranks: reset) == nil)
    #expect(moveBack(movedFrom: "gone", ranks: reset) == nil)
}

@Test func aMenuWithNowhereToGo() {
    let m = switchAccountMenu(pane: "local|p1", on: nil, ranks: [ranks[3]], movedFrom: nil)
    #expect(m.children.map(\.title) == ["No Other Account"])
    let none = switchAccountMenu(pane: "local|p1", on: nil, ranks: [], movedFrom: nil)
    #expect(none.children.map(\.title) == ["No Other Account"])
}

@Test func theCountdown() {
    let now = Date(timeIntervalSince1970: 1000)
    let m = AgentMoving(pane: "p12", agent: "claude", from: "default", to: "backup2", at: now.addingTimeInterval(4.2), reason: "limit")
    #expect(secondsUntil(m, now: now) == 5)
    #expect(secondsUntil(m, now: now.addingTimeInterval(4.2)) == 0)
    #expect(secondsUntil(m, now: now.addingTimeInterval(60)) == 0)
}

@Test func whatTheServerSaysDecodes() throws {
    let moving = try conchDecoder().decode(AgentMoving.self, from: Data(#"{"pane":"p12","agent":"claude","from":"default","to":"backup2","at":"2026-11-19T22:00:05Z","reason":"limit"}"#.utf8))
    #expect(moving.to == "backup2" && moving.reason == "limit")
    let moved = try conchDecoder().decode(AgentMoved.self, from: Data(#"{"pane":"p20","replaces":"p12","agent":"claude","from":"default","to":"backup2","reason":"limit"}"#.utf8))
    #expect(moved.replaces == "p12")
    let sg = try conchDecoder().decode(AccountSuggestion.self, from: Data(#"{"agent":"claude","accounts":[{"account":"","room":0,"reported":true,"usable":false,"why":"full","resets_at":"2026-11-19T23:00:00Z"},{"account":"backup2","room":96,"usable":true,"signed_in":true}]}"#.utf8))
    #expect(sg.accounts.map(\.label) == ["default", "backup2"] && sg.accounts[1].signedIn == true && sg.accounts[0].resetsAt != nil)
    let p = try conchDecoder().decode(PaneInfo.self, from: Data(#"{"id":"p20","name":"x","state":"running","account":"backup2","moved_from":"default","replaces":"p12"}"#.utf8))
    #expect(p.movedFrom == "default" && p.replaces == "p12")
    let e = JSONEncoder()
    e.outputFormatting = .sortedKeys
    #expect(String(decoding: try e.encode(AgentMoveParams(id: "p12", account: "backup2", continue: true, reason: "limit")), as: UTF8.self)
        == #"{"account":"backup2","continue":true,"id":"p12","reason":"limit"}"#)
    #expect(String(decoding: try e.encode(AgentMoveParams(id: "p12")), as: UTF8.self) == #"{"id":"p12"}"#)
    #expect(accountLabel(nil) == "default" && accountLabel("") == "default" && accountLabel("work") == "work")
}

@Test func theNewPaneTakesTheOldOnesPlace() {
    var l = Layout()
    l.open("local|p1")
    l.split(.horizontal, with: "local|p12")
    l.open("local|p30") // a tab of its own
    l.focus("local|p12")
    l.replace("local|p12", with: "local|p20")
    #expect(l.tabs.count == 2)
    #expect(l.tabs[0].root.panes == ["local|p1", "local|p20"])
    #expect(l.focused == "local|p20")
    // A pane not shown: nothing happens.
    l.replace("local|p99", with: "local|p100")
    #expect(!l.tabs.flatMap(\.root.panes).contains("local|p100"))
}
