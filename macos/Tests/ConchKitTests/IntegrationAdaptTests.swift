import Foundation
import Testing
@testable import ConchKit

@Test func integrationAdaptFormArguments() {
    var f = IntegrationAdaptForm()
    f.docs = "https://docs.semaphoreci.com/reference/api-v1alpha"
    #expect(f.name == "semaphoreci")
    #expect(f.arguments() == .success(["integration", "adapt", "-json", "-kind", "ci", "-agent", "claude",
                                        "https://docs.semaphoreci.com/reference/api-v1alpha"]))
    f.service = " Semaphore "
    f.agent = "codex"
    f.project = "/src/cachemind"
    #expect(f.name == "semaphore")
    #expect(f.arguments() == .success(["integration", "adapt", "-json", "-kind", "ci", "-agent", "codex", "-name", "semaphore",
                                        "-cwd", "/src/cachemind", "https://docs.semaphoreci.com/reference/api-v1alpha"]))
    #expect(f.checkArguments() == ["integration", "adapt", "-json", "-check", "-cwd", "/src/cachemind", "semaphore"])
    #expect(f.saveArguments() == ["integration", "adapt", "-save", "semaphore"])
    #expect(f.publishArguments() == ["integration", "publish", "-json", "-y", "-cwd", "/src/cachemind", "semaphore"])
    // A sandbox has no project to check against: conch sandbox adapt checks its own way.
    f.kind = .sandbox
    if case .success(let a) = f.arguments() { #expect(!a.contains("-cwd") && a.contains("sandbox")) } else { Issue.record("sandbox") }
    // Two words make a name; anything else is said.
    f.kind = .ci
    f.service = "Semaphore CI"
    #expect(f.name == "semaphore-ci")
    f.service = "9lives"
    #expect(f.arguments() == .failure(.problem("service: letters, digits and -, starting with a letter")))
    f.service = ""
    f.anyway = true
    if case .success(let a) = f.arguments() { #expect(a.contains("-new")) } else { Issue.record("anyway") }
    // Kinds conch does not adapt yet, and a docs address that is not one.
    f.kind = .host
    if case .success = f.arguments() { Issue.record("a code host adapted") }
    for k in [IntegrationKind.tickets, .notify] {
        f.kind = k
        if case .failure(let e) = f.arguments() { Issue.record("\(k) refused: \(e)") }
    }
    f.kind = .ci
    for bad in ["", "docs.semaphoreci.com", "ftp://x.test/a", "https://"] {
        f.docs = bad
        if case .success = f.arguments() { Issue.record("took \(bad)") }
    }
    #expect(IntegrationAdaptForm().name == "")
}

// The lines of `conch integration adapt -json`, as the four rows of mock 7.
@Test func integrationAdaptSteps() {
    var p = AdaptProgress()
    var s = IntegrationAdaptSteps.steps(p, name: "semaphore")
    #expect(s.map(\.state) == [.waiting, .waiting, .waiting, .waiting])
    #expect(s.map(\.id) == ["draft", "offline", "live", "publish"])
    p.take(#"{"event":"docs","detail":"https://docs.semaphoreci.test/api (40 KB)","ok":true}"#)
    p.take(#"{"event":"agent","name":"claude","pane":"p9","detail":"claude is writing the file in pane p9"}"#)
    s = IntegrationAdaptSteps.steps(p, name: "semaphore")
    #expect(s[0].state == .running && s[0].title == "Claude is writing semaphore.toml")
    p.take(#"{"event":"blocked","detail":"claude is asking something"}"#)
    #expect(IntegrationAdaptSteps.steps(p, name: "semaphore")[0].state == .waiting)

    // A file with problems: drafted, offline failed; nothing to check yet.
    var q = p
    q.take(#"{"event":"file","name":"claude","path":"/d/semaphore.toml","text":"x","kind":"ci","operations":["runs","failed_log"]}"#)
    q.take(#"{"event":"problems","problems":["[states]: red is not a state"]}"#)
    q.take(#"{"event":"error","detail":"the file is not right yet"}"#)
    s = IntegrationAdaptSteps.steps(q, name: "semaphore")
    #expect(s[0].state == .done && s[1].state == .failed && s[1].title == "Offline check: 1 problem")
    #expect(!IntegrationAdaptSteps.canCheck(q) && !IntegrationAdaptSteps.canPublish(q))

    // Clean: the key's destination is said, and nothing sent until asked.
    p.take(#"{"event":"file","name":"claude","path":"/d/semaphore.toml","text":"x","kind":"ci","operations":["runs","failed_log","rerun"],"detail":"no stats endpoint"}"#)
    p.take(#"{"event":"offline","detail":"0 problems","ok":true}"#)
    p.take(#"{"event":"consent","name":"semaphore","detail":"$SEMAPHORE_TOKEN to https://api.semaphoreci.test"}"#)
    p.take(#"{"event":"done","detail":"keep it","ok":true}"#)
    s = IntegrationAdaptSteps.steps(p, name: "semaphore")
    #expect(s[0] == IntegrationAdaptStep(id: "draft", title: "Drafted semaphore.toml", detail: "runs, failed_log, rerun; no stats endpoint", state: .done))
    #expect(s[1].title == "Offline check: 0 problems" && s[1].state == .done)
    #expect(s[2].state == .waiting && s[2].detail == "sends $SEMAPHORE_TOKEN to https://api.semaphoreci.test")
    #expect(IntegrationAdaptSteps.canCheck(p) && !IntegrationAdaptSteps.canPublish(p))
    #expect(IntegrationAdaptSteps.steps(p, name: "semaphore", checking: true)[2].state == .running)

    // A failed check says what the service answered; a passed one opens Publish.
    var bad = p
    bad.take(#"{"event":"live","live":{"ok":false,"runs":0,"why":"answered 401","key_env":"SEMAPHORE_TOKEN"}}"#)
    #expect(IntegrationAdaptSteps.steps(bad, name: "semaphore")[2].detail == "answered 401")
    #expect(IntegrationAdaptSteps.canCheck(bad) && !IntegrationAdaptSteps.canPublish(bad))
    p.take(#"{"event":"live","live":{"ok":true,"runs":12,"project":"me/cachemind","branch":"main","key_env":"SEMAPHORE_TOKEN"}}"#)
    s = IntegrationAdaptSteps.steps(p, name: "semaphore")
    #expect(s[2].state == .done && s[2].detail == "read 12 runs of me/cachemind on main")
    #expect(!IntegrationAdaptSteps.canCheck(p) && IntegrationAdaptSteps.canPublish(p))

    var pub = IntegrationPublishState()
    pub.running = true
    #expect(IntegrationAdaptSteps.steps(p, name: "semaphore", publish: pub)[3].state == .running)
    pub.running = false
    pub.url = "https://github.com/x/y/pull/7"
    #expect(IntegrationAdaptSteps.steps(p, name: "semaphore", publish: pub)[3].detail == "https://github.com/x/y/pull/7")
    pub = IntegrationPublishState()
    pub.error = "gh auth login"
    #expect(IntegrationAdaptSteps.steps(p, name: "semaphore", publish: pub)[3].state == .failed)

    // Failing before a file.
    var r = AdaptProgress()
    r.take(#"{"event":"error","detail":"found no docs there"}"#)
    #expect(IntegrationAdaptSteps.steps(r, name: "x")[0].state == .failed)
}
