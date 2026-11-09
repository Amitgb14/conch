import Foundation
import Testing
@testable import ConchKit

@Test func registryProvidersDecode() throws {
    // As `conch sandbox provider search -json` prints it, with a field a
    // newer conch might add.
    let json = """
    [{"name":"hetzner","label":"Hetzner Cloud","api":"https://api.hetzner.cloud/v1","api_host":"api.hetzner.cloud",
      "ssh_host":"{reply.server.public_net.ipv4.ip}","key_env":"HCLOUD_TOKEN","format":2,"note":"billed while stopped",
      "file":"providers/hetzner.toml","sha256":"ab","adapted_by":"claude","adapted":"2026-11-08",
      "docs":"https://docs.hetzner.cloud","conch":"0.1.10","checked":null,"usable":true,"later":1},
     {"name":"acme","label":"Acme","api_host":"api.acme.dev","ssh_host":"ssh.acme.dev","key_env":"ACME_KEY",
      "checked":{"date":"2026-11-07","conch":"0.1.10","sandboxes":3},"here":"installed","usable":true},
     {"name":"next","label":"Next","api_host":"a","ssh_host":"b","key_env":"K","checked":null,"here":"a file of your own","usable":false}]
    """
    let ps = registryProviders(Data(json.utf8))
    #expect(ps.count == 3)
    #expect(ps[0].sshWords == "the sandbox's own address, as the provider's API gives it")
    #expect(ps[0].liveCheck == "not checked live yet")
    #expect(ps[0].installBlocker == nil)
    #expect(ps[0].note == "billed while stopped")
    #expect(ps[1].sshWords == "ssh.acme.dev")
    #expect(ps[1].liveCheck == "checked 2026-11-07")
    #expect(ps[1].installBlocker == "installed")
    #expect(ps[2].installBlocker == "needs a newer conch")
    var own = ps[2]
    own.usable = true
    #expect(own.installBlocker == "you have a provider of this name already")
    // Not a list: nothing, rather than a failure.
    #expect(registryProviders(Data("{}".utf8)).isEmpty)
    #expect(registryProviders(Data()).isEmpty)
    let r = try JSONDecoder().decode(PublishResult.self, from: Data(#"{"url":"https://github.com/x/y/pull/7"}"#.utf8))
    #expect(r.url.hasSuffix("/pull/7"))
}

@Test func adaptMeetsTheRegistry() throws {
    var p = AdaptProgress()
    p.take(#"{"event":"registry","name":"hetzner","detail":"Hetzner Cloud is in the provider registry already"}"#)
    #expect(p.phase == .registry)
    #expect(p.registry == "hetzner")
    #expect(p.registryDetail.contains("registry already"))
    #expect(!p.canSave)
    // Adapting anyway asks conch not to stop at the registry.
    var f = AdaptForm()
    f.url = "https://docs.hetzner.cloud"
    #expect(try f.arguments().get().contains("-new") == false)
    f.anyway = true
    let args = try f.arguments().get()
    #expect(args.contains("-new"))
    #expect(args.last == "https://docs.hetzner.cloud")
}
