import Foundation
import Testing
@testable import ConchKit

// Signed in is cloud.json being there, as conch itself decides: a conch
// that never signed in is asked nothing.
@Test func cloudSignedInIsTheFile() throws {
    let dir = (NSTemporaryDirectory() as NSString).appendingPathComponent("cloudtest-\(UUID().uuidString)")
    try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(atPath: dir) }
    #expect(!CloudModel.signedIn(conchDir: dir))
    FileManager.default.createFile(atPath: CloudModel.accountFile(conchDir: dir), contents: Data("{}".utf8))
    #expect(CloudModel.signedIn(conchDir: dir))
}

// `conch account -json`, signed in and not, as conch prints it.
@Test func cloudAccountDecodes() throws {
    let out = """
    {"signed_in":true,"api":"http://127.0.0.1:8787","device_id":"d_1","me":{"id":"u_1","handle":"ada","plan":"pro",
     "allowance":{"running_seconds":360000,"running_seconds_used":45000,"storage_gb":100,"storage_gb_used":12.5,"max_machines":3,"period_end":"2026-12-01T00:00:00Z"}},
     "devices":[{"id":"d_1","name":"Conch.app on mac","client":"mac_app","created_at":"2026-11-10T10:00:00Z","last_seen_at":"2026-11-10T10:00:00Z","current":true},
                {"id":"d_2","name":"conch on box","client":"cli","created_at":"2026-11-10T10:00:00Z","last_seen_at":"2026-11-10T10:00:00Z","current":false}]}
    """
    let a = try JSONDecoder().decode(CloudAccount.self, from: Data(out.utf8))
    #expect(a.signedIn && a.me?.handle == "ada" && a.thisDevice?.id == "d_1")
    let lines = CloudModel.allowanceLines(a.me!.allowance)
    #expect(lines.hours == "12.5 h of 100 h")
    #expect(lines.storage == "12.5 of 100 GB")
    #expect(CloudModel.usedFraction(a.me!.allowance) == 0.125)
    let out2 = #"{"signed_in": false, "api": "https://api.cloud.conch.dev"}"#
    let b = try JSONDecoder().decode(CloudAccount.self, from: Data(out2.utf8))
    #expect(!b.signedIn && b.me == nil && b.thisDevice == nil)
}

@Test func cloudAllowanceEdges() {
    let zero = CloudAllowance(runningSeconds: 0, runningSecondsUsed: 10, storageGB: 0, storageGBUsed: 0, maxMachines: 0)
    #expect(CloudModel.usedFraction(zero) == 0)
    let over = CloudAllowance(runningSeconds: 3600, runningSecondsUsed: 7200, storageGB: 10, storageGBUsed: 3, maxMachines: 1)
    #expect(CloudModel.usedFraction(over) == 1)
    #expect(CloudModel.allowanceLines(over).storage == "3 of 10 GB")
    #expect(CloudModel.hours(0) == "0 h" && CloudModel.hours(5400) == "1.5 h")
}

// Machines from `conch cloud machine ls -json`: their state in words,
// what may be asked of each, and a state conch does not know passed
// through as it is.
@Test func cloudMachines() throws {
    let out = """
    {"machines":[
     {"id":"m_1","name":"box","size":"standard","region":"us-west","state":"running","sleep":{},"created_at":"2026-11-10T10:00:00Z",
      "provider":{"id":"p_1","name":"Tensorlake","kind":"tensorlake","owner":"conch"},"web_url":"http://127.0.0.1:8787/machines/m_1"},
     {"id":"m_2","name":"gpu","size":"large","region":"","state":"error","state_detail":"its VM never said hello","sleep":{},"created_at":"2026-11-10T10:00:00Z"},
     {"id":"m_3","name":"old","size":"small","region":"eu-central","state":"asleep","sleep":{},"created_at":"2026-11-10T10:00:00Z"},
     {"id":"m_4","name":"new","size":"small","region":"eu-central","state":"resizing","sleep":{},"created_at":"2026-11-10T10:00:00Z"}]}
    """
    let ms = try JSONDecoder().decode(CloudMachines.self, from: Data(out.utf8)).machines
    #expect(ms.count == 4)
    #expect(ms[0].canSleep && !ms[0].canWake && ms[0].tone == .on && ms[0].subtitle == "standard · us-west · Tensorlake")
    #expect(ms[0].webURL == "http://127.0.0.1:8787/machines/m_1")
    #expect(ms[1].stateText == "error: its VM never said hello" && ms[1].tone == .error && ms[1].subtitle == "large")
    #expect(!ms[1].canSleep && !ms[1].canWake)
    #expect(ms[2].canWake && ms[2].tone == .resting)
    #expect(ms[3].stateText == "resizing" && !ms[3].canWake && !ms[3].canSleep)
}

// A sign-in, event by event, as `conch login -json` says it.
@Test func cloudLoginEvents() {
    var s = CloudLoginState.starting
    let code = CloudLoginEvent.parse(#"{"event":"code","user_code":"ABCD-1234","verification_uri":"http://x/device","verification_uri_complete":"http://x/device?code=ABCD-1234","expires_in":600}"#)!
    s = s.after(code)
    #expect(s == .code("ABCD-1234", link: "http://x/device?code=ABCD-1234"))
    #expect(CloudLoginEvent.parse("not json") == nil)
    s = s.after(CloudLoginEvent.parse(#"{"event":"signed_in","handle":"ada"}"#)!)
    #expect(s == .done)
    #expect(s.exited(1) == .done)
    let failed = CloudLoginState.code("X", link: nil).after(CloudLoginEvent.parse(#"{"event":"error","error":"the sign-in was denied"}"#)!)
    #expect(failed == .failed("the sign-in was denied"))
    // conch gone without a last word.
    #expect(CloudLoginState.code("X", link: nil).exited(2) == .failed("conch login stopped (status 2)"))
    // A link without the code in it is still a link.
    #expect(CloudLoginEvent.parse(#"{"event":"code","user_code":"A","verification_uri":"http://x/device"}"#)!.link == "http://x/device")
}

// A cloud machine in machines.json: its id, how the sidebar names it, and
// its state read from why its bridge ended.
@Test func cloudMachineEntry() {
    let e = MachineEntry(id: "box", label: "box", target: "conch:m_i806gqdxqcmoyx2y")
    #expect(e.cloudID == "m_i806gqdxqcmoyx2y" && e.sandboxProvider == nil && e.displayLabel == "box · cloud")
    for t in ["conch:m_short", "conch:", "daytona:sb1", "me@host", "conch:m_I806GQDXQCMOYX2Y"] {
        #expect(MachineEntry(id: "x", label: "x", target: t).cloudID == nil, "\(t)")
    }
    #expect(MachineEntry.cloudState(fromReason: "conch: box is asleep; run: conch cloud machine wake box") == "asleep")
    #expect(MachineEntry.cloudState(fromReason: "box is sleeping") == "sleeping")
    #expect(MachineEntry.cloudState(fromReason: "the relay for box: gone") == nil)
}
