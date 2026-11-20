import ConchKit
import Foundation
import Testing

// New Sandbox's steps, read from the lines `conch sandbox create -progress`
// writes (cmd/conch/sandboxprogress.go).

private func lines(_ ls: [String]) -> Data { Data((ls.joined(separator: "\n") + "\n").utf8) }

@Test func aSetupUnderWay() {
    let st = readSandboxSetup(lines([
        #"{"at":1000,"step":"create","state":"start","line":"Creating a Daytona sandbox"}"#,
        #"{"at":5000,"step":"create","sandbox":"sbx1"}"#,
        #"{"at":5000,"step":"create","state":"done"}"#,
        #"{"at":5000,"step":"answer","state":"start","line":"waiting for sandbox-sbx1 to answer"}"#,
        #"{"at":17000,"step":"answer","state":"done"}"#,
        #"{"at":17000,"step":"install","state":"start","line":"probing sandbox-sbx1"}"#,
        #"{"at":20000,"step":"install","line":"copying conch to sandbox-sbx1 (29 MB)"}"#,
    ]))
    #expect(st.sandbox == "sbx1")
    #expect(st.state(.create) == .done && st.state(.answer) == .done)
    #expect(st.state(.install) == .now && st.state(.check) == .later && st.state(.add) == .later)
    #expect(st.current == .install)
    #expect(st.doneCount == 2)
    #expect(abs(st.fraction - 2.5 / 5) < 0.001)
    #expect(st.detail[.install] == "copying conch to sandbox-sbx1 (29 MB)")
    #expect(sandboxStepDetail(st.detail[.install]) == "Copying conch to sandbox-sbx1 (29 MB)")
    #expect(st.time(.create) == "4s")
    #expect(st.time(.answer) == "12s")
    #expect(st.time(.install, now: Date(timeIntervalSince1970: 82)) == "1:05", "under way: counted to now")
    #expect(st.time(.check) == nil)
    #expect(st.end == nil)
    #expect(st.lines.count == 4)
}

@Test func aSetupThatIsReady() {
    let st = readSandboxSetup(lines([
        #"{"at":1000,"step":"create","state":"start"}"#,
        #"{"at":2000,"step":"create","state":"done"}"#,
        #"{"at":3000,"step":"add","state":"done","machine":"fix-login","label":"fix-login"}"#,
        #"{"at":3000,"end":"ready","machine":"fix-login","label":"fix-login"}"#,
    ]))
    #expect(st.end == .ready)
    #expect(st.machine == "fix-login" && st.label == "fix-login")
    #expect(st.fraction == 1)
    #expect(st.time(.add) == "0s", "a step only ever said done takes no time")
}

@Test func aSetupThatFailedAndKeptTheSandbox() {
    let st = readSandboxSetup(lines([
        #"{"at":1000,"step":"create","state":"start","sandbox":"sbx1"}"#,
        #"{"at":2000,"step":"install","state":"done"}"#,
        #"{"at":3000,"step":"check","state":"fail","error":"conch on x is still from an older build after installing"}"#,
        #"{"at":3000,"end":"failed","error":"conch on x is still from an older build after installing","kept":true}"#,
    ]))
    #expect(st.state(.check) == .failed)
    #expect(st.current == .check)
    #expect(st.end == .failed(error: "conch on x is still from an older build after installing", kept: true))
}

@Test func readingWhatIsNotYetWhole() {
    // A partial last line, a blank one, junk, and a later conch's step: skipped.
    let d = Data((#"{"at":1000,"step":"create","state":"start"}"# + "\n\nnot json\n" +
                  #"{"at":2000,"step":"teleport","state":"start"}"# + "\n" + #"{"at":3000,"step":"create","sta"#).utf8)
    let st = readSandboxSetup(d)
    #expect(st.state(.create) == .now)
    #expect(st.states.count == 1)
    #expect(readSandboxSetup(Data()) == SandboxSetupState())
    #expect(sandboxStepDetail(nil) == nil && sandboxStepDetail("  ") == nil)
}

@Test func durations() {
    #expect(SandboxSetupState.duration(-3) == "0s")
    #expect(SandboxSetupState.duration(59.4) == "59s")
    #expect(SandboxSetupState.duration(60) == "1:00")
    #expect(SandboxSetupState.duration(3725) == "62:05")
}

@Test func retryAndDeleteArguments() {
    #expect(sandboxRetryArguments(provider: "instabox", sandbox: "m_1", label: "web", progress: "/p")
            == ["sandbox", "-provider", "instabox", "add", "-progress", "/p", "-label", "web", "m_1"])
    #expect(sandboxRetryArguments(provider: "instabox", sandbox: "m_1", label: nil, progress: "/p")
            == ["sandbox", "-provider", "instabox", "add", "-progress", "/p", "m_1"])
    #expect(sandboxDeleteArguments(provider: "instabox", sandbox: "m_1") == ["sandbox", "-provider", "instabox", "rm", "-y", "m_1"])
}

@Test func everyStepHasItsWords() {
    #expect(SandboxStep.allCases.map(\.rawValue) == ["create", "answer", "install", "check", "add"])
    for s in SandboxStep.allCases { #expect(!s.title.isEmpty && !s.doneTitle.isEmpty && s.title != s.doneTitle) }
}
