import Foundation
import Testing
@testable import ConchKit

// What `conch sandbox adapt -json` says, folded into what the sheet draws.
@Test func adaptProgressFoldsTheLines() {
    var p = AdaptProgress()
    #expect(p.phase == .idle && !p.canSave)
    p.take(#"{"event":"docs","detail":"https://x.test/llms.txt answered 404"}"#)
    p.take(#"{"event":"docs","detail":"https://x.test/openapi.yaml (40 KB)","ok":true}"#)
    #expect(p.phase == .docs && p.docs == ["https://x.test/openapi.yaml (40 KB)"] && p.misses.count == 1)
    p.take(#"{"event":"agent","name":"claude","pane":"p7","detail":"claude is writing the file in pane p7"}"#)
    #expect(p.phase == .writing && p.agent == "claude" && p.pane == "p7")
    p.take(#"{"event":"blocked","pane":"p7","detail":"claude is asking something in pane p7"}"#)
    #expect(p.blocked)
    p.take(#"{"event":"waiting","detail":"claude is reading the docs…"}"#)
    #expect(!p.blocked)
    p.take("not json at all")          // a newer conch may say more: ignored
    p.take(#"{"event":"unheard-of"}"#)
    p.take(#"{"event":"file","name":"claude","path":"/h/drafts/acme/provider.toml","text":"name = \"acme\"","mapping":[{"does":"New sandbox","call":"POST /sandboxes","reads":"id"}]}"#)
    #expect(p.phase == .review && p.mapping.first?.call == "POST /sandboxes" && p.file.contains("acme") && p.canSave)
    p.take(#"{"event":"check","check":{"signed":false,"all":0,"mine":0,"why":"set $ACME_API_KEY","key_env":"ACME_API_KEY"}}"#)
    #expect(p.check?.signed == false && p.check?.keyEnv == "ACME_API_KEY")
    #expect(p.canSave) // a key not set yet does not stop a save
    p.take(#"{"event":"done","detail":"save it with: conch sandbox provider save acme","ok":false}"#)
    #expect(p.next.contains("save acme"))

    // Problems keep the review open, so the agent can be asked to fix them.
    var q = p
    q.take(#"{"event":"problems","problems":["[states] maps nothing to started"]}"#)
    q.take(#"{"event":"error","detail":"the file is not right yet"}"#)
    #expect(q.phase == .review && !q.canSave && q.problems.count == 1)
    // A revision clears them.
    q.take(#"{"event":"agent","name":"claude","pane":"p7","detail":"claude is changing the file"}"#)
    #expect(q.problems.isEmpty && q.error.isEmpty && q.phase == .writing)

    // Failing before any file is a failure, not a review.
    var r = AdaptProgress()
    r.take(#"{"event":"error","detail":"found no docs there"}"#)
    #expect(r.phase == .failed && r.error == "found no docs there" && !r.canSave)
}

@Test func adaptFormArguments() throws {
    var f = AdaptForm()
    #expect(f.arguments() == .failure(.problem("the provider's API or docs address, starting https://")))
    f.url = "ftp://x.test"
    #expect((try? f.arguments().get()) == nil)
    f.url = " https://api.tensorlake.ai/sandboxes "
    #expect(try f.arguments().get() == ["sandbox", "adapt", "-json", "-agent", "claude", "https://api.tensorlake.ai/sandboxes"])
    #expect(adaptName(f) == "tensorlake")
    f.agent = "codex"
    f.name = "Bad Name"
    #expect((try? f.arguments().get()) == nil)
    f.name = "Acme"
    #expect(try f.arguments().get() == ["sandbox", "adapt", "-json", "-agent", "codex", "-name", "acme", "https://api.tensorlake.ai/sandboxes"])
    #expect(adaptName(f) == "acme")
    var g = AdaptForm()
    g.url = "https://acme.co.uk/api"
    #expect(adaptName(g) == "acme")
    g.url = "https://localhost/x"
    #expect(adaptName(g) == "")
}

@Test func lineBufferHoldsHalfALine() {
    let b = LineBuffer()
    #expect(b.add(Data("{\"a\":1}\n{\"b\"".utf8)) == ["{\"a\":1}"])
    #expect(b.add(Data(":2}\n\n".utf8)) == ["{\"b\":2}", ""])
    #expect(b.add(Data("tail".utf8)).isEmpty)
    #expect(b.flush() == ["tail"])
    #expect(b.flush().isEmpty)
}

@Test func adaptedProvidersDecode() throws {
    let j = #"[{"name":"daytona","label":"Daytona","ready":true},{"name":"acme","label":"Acme","ready":false,"why":"set $ACME_API_KEY","adapted":true,"file":"/h/providers/acme.toml"}]"#
    let ps = try JSONDecoder().decode([SandboxProviderInfo].self, from: Data(j.utf8))
    #expect(ps[0].adapted == nil && ps[1].adapted == true && ps[1].file == "/h/providers/acme.toml")
}

// Set key… is offered where a built-in provider lacks its key, and only
// there: Tensorlake, new and with no card below, had no way to get one.
@Test func setKeyIsOfferedWhereAKeyIsMissing() {
    #expect(SandboxProviderInfo(name: "tensorlake", label: "Tensorlake", ready: false, why: "set $TENSORLAKE_API_KEY").offersSetKey)
    #expect(!SandboxProviderInfo(name: "daytona", label: "Daytona", ready: true).offersSetKey)
    // An adapted one is not: what it lacks may be more than a key.
    #expect(!SandboxProviderInfo(name: "hetzner", label: "Hetzner Cloud", ready: false, adapted: true).offersSetKey)
    #expect(!SandboxProviderInfo(name: "acme", label: "Acme", ready: true, adapted: true).offersSetKey)
    // adapted left out by an older conch is built in.
    #expect(SandboxProviderInfo(name: "boat", label: "boat.dev", ready: false, adapted: nil).offersSetKey)
}
