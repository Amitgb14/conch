import Foundation
import Testing
@testable import ConchKit

// Taken from what `conch integration ls -json` and `info -json jenkins`
// print, so a change of shape on either side fails here.
private let lsJSON = """
{"integrations":[{"name":"github","kind":"host","version":"-","source":"built in","state":"through gh"},{"name":"github-actions","kind":"ci","version":"-","source":"built in","state":"through gh"},{"name":"github-issues","kind":"tickets","version":"-","source":"built in","state":"through gh"},{"name":"linear","kind":"tickets","version":"-","source":"built in","state":"key: $LINEAR_API_KEY not set"},{"name":"jira","kind":"tickets","version":"-","source":"built in","state":"key: $JIRA_API_TOKEN not set"},{"name":"buildkite","kind":"ci","version":"-","source":"ships with conch","state":"key: $BUILDKITE_TOKEN not set, nor in the Keychain"},{"name":"circleci","kind":"ci","version":"-","source":"ships with conch","state":"key: $CIRCLECI_TOKEN not set, nor in the Keychain","uses":true},{"name":"gitlab-ci","kind":"ci","version":"-","source":"ships with conch","state":"key: $GITLAB_TOKEN not set, nor in the Keychain"},{"name":"jenkins","kind":"ci","version":"-","source":"ships with conch","state":"key: $JENKINS_TOKEN not set, nor in the Keychain"}],"problems":null}
"""

private let jenkinsJSON = """
{"name":"jenkins","label":"Jenkins","kind":"ci","source":"ships with conch","installed":true,"does":[{"what":"A failed job's output goes to the branch's agent","where":"The autopilot's auto_fix, conch branch fix-ci, and the review queue's f","level":"yes"},{"what":"Re-run what failed","where":"conch branch rerun-ci","level":"no"},{"what":"Runs for a branch and commit","where":"Which run a red check came from, its link and duration","level":"yes"},{"what":"CI panel statistics","where":"Durations and pass rate, from the recent runs only","level":"partly"},{"what":"Checks on the pull request","where":"Already there: the service reports to GitHub, and conch reads GitHub's checks","level":"built-in"}],"hosts":{"api":""},"key_env":"JENKINS_TOKEN","user_env":"JENKINS_USER","auth_type":"basic","base_url":"ask","key_help":"Jenkins → your name → Security → API Token → Add new token; the user name goes in $JENKINS_USER.","key_set":false,"note":"Give the address in config.toml.","docs":"https://www.jenkins.io/doc/book/using/remote-access-api/","adapted_by":"claude","adapted":"2026-11-15"}
"""

// As `search -json` prints a registry's entries, with a field a newer
// conch might add.
private let searchJSON = """
[{"kind":"ci","name":"semaphore","label":"Semaphore","version":3,"format":3,"file":"integrations/semaphore.toml","sha256":"9f2c00000000000041ab","hosts":{"api":"api.semaphoreci.com"},"key_env":"SEMAPHORE_TOKEN","operations":["runs","failed_log","rerun"],"tier":"verified","checked":{"date":"2026-11-12","conch":"0.2.1","detail":"12 pipelines read"},"signed":true,"later":1},
 {"kind":"ci","name":"drone","label":"Drone","version":1,"format":3,"file":"integrations/drone.toml","sha256":"ab","hosts":{"api":"drone.io"},"key_env":"DRONE_TOKEN","operations":["runs"],"tier":"community","signed":false},
 {"kind":"ci","name":"woodpecker","label":"Woodpecker","version":2,"format":3,"file":"integrations/woodpecker.toml","sha256":"cd","hosts":{"api":"ci.woodpecker.dev"},"key_env":"WP_TOKEN","revoked":[{"version":2,"reason":"sent logs elsewhere"}],"signed":true},
 {"kind":"ci","name":"circleci","label":"CircleCI","version":4,"format":3,"file":"integrations/circleci.toml","sha256":"ef","hosts":{"api":"circleci.com","follow":["circle-production-action-output.s3.amazonaws.com"]},"key_env":"CIRCLECI_TOKEN","operations":["runs","failed_log","rerun"],"signed":true}]
"""

private func decoded() throws -> (IntegrationList, [IntegrationListed], [String: [IntegrationUse]]) {
    let here = try JSONDecoder().decode(IntegrationList.self, from: Data(lsJSON.utf8))
    let listed = try JSONDecoder().decode([IntegrationListed].self, from: Data(searchJSON.utf8))
    let uses: [String: [IntegrationUse]] = [
        "ceph": [IntegrationUse(name: "github", label: "GitHub", kind: "host", why: "github.com remote", installed: true, connected: true),
                 IntegrationUse(name: "circleci", label: "CircleCI", kind: "ci", why: ".circleci/config.yml", installed: true, connected: false, keyEnv: "CIRCLECI_TOKEN")],
        "pdp-web": [IntegrationUse(name: "semaphore", label: "Semaphore", kind: "ci", why: "check semaphore/ci", installed: false)],
    ]
    return (here, listed, uses)
}

@Test func integrationCards() throws {
    let (here, listed, uses) = try decoded()
    let cards = IntegrationCatalog.cards(here: here, listed: listed, uses: uses)
    let by = Dictionary(uniqueKeysWithValues: cards.map { ($0.name, $0) })
    #expect(cards.count == 12) // nine here, and three listed that are not
    #expect(by["github"]?.standing == .builtIn && by["github"]?.auth == "gh login" && by["github"]?.wanted == false)
    #expect(by["linear"]?.auth == "API key")
    #expect(by["circleci"]?.installed == true && by["circleci"]?.source == "ships with conch")
    #expect(by["circleci"]?.usedIn == ["ceph"] && by["circleci"]?.wanted == true) // used, and no key
    #expect(by["jenkins"]?.auth == "user + token")
    #expect(by["semaphore"]?.standing == .verified("2026-11-12") && by["semaphore"]?.installed == false && by["semaphore"]?.wanted == true)
    #expect(by["semaphore"]?.summary == "A failed job's output to the branch's agent, re-run from failed, runs for a branch.")
    #expect(by["drone"]?.standing == .notChecked && by["drone"]?.community == true)
    #expect(by["woodpecker"]?.standing == .revoked("sent logs elsewhere"))
    #expect(by["woodpecker"]?.standing.words == "revoked")
}

@Test func integrationFiltersAndCounts() throws {
    let (here, listed, uses) = try decoded()
    let cards = IntegrationCatalog.cards(here: here, listed: listed, uses: uses)
    let names = { (f: IntegrationFilter, k: IntegrationKind?, q: String) in
        IntegrationCatalog.filter(cards, filter: f, kind: k, query: q).map(\.name).sorted()
    }
    #expect(names(.verified, nil, "") == ["semaphore"])
    #expect(names(.community, nil, "") == ["drone"])
    #expect(names(.builtIn, .ci, "") == ["github-actions"])
    #expect(names(.all, .host, "") == ["github"])
    #expect(names(.all, nil, "circle") == ["circleci"])
    #expect(names(.all, nil, "  Jenk ") == ["jenkins"])
    #expect(names(.all, nil, "nothing") == [])
    let ci = IntegrationCatalog.count(cards, kind: .ci)
    #expect(ci.installed == 5 && ci.listed == 8)
    #expect(IntegrationCatalog.count(cards, kind: .notify) == (0, 0))
}

@Test func integrationBanner() throws {
    let (_, _, uses) = try decoded()
    #expect(IntegrationCatalog.banner(uses) == ["ceph builds on CircleCI (.circleci/config.yml)", "pdp-web builds on Semaphore (check semaphore/ci)"])
    #expect(IntegrationCatalog.bannerNames(uses) == ["circleci", "semaphore"])
    #expect(IntegrationCatalog.banner([:]).isEmpty)
    // A host that is connected says nothing; one that is not is "on".
    let gitlab = ["web": [IntegrationUse(name: "gitlab", label: "GitLab", kind: "host", why: "gitlab.com remote", installed: false)]]
    #expect(IntegrationCatalog.banner(gitlab) == ["web is on GitLab (gitlab.com remote)"])
}

@Test func integrationInfoDecodes() throws {
    let info = try JSONDecoder().decode(IntegrationInfo.self, from: Data(jenkinsJSON.utf8))
    #expect(info.does.count == 5 && info.does[1].level == "no")
    #expect(info.authType == "basic" && info.userEnv == "JENKINS_USER" && info.baseURL == "ask" && !info.keySet)
    #expect(info.verifiedWords == nil && info.shortHash == nil)
    var listed = info
    listed.checked = IntegrationChecked(date: "2026-11-12", conch: "0.2.1", detail: "12 pipelines read")
    listed.sha256 = "9f2c000000000041ab"
    #expect(listed.verifiedWords == "✓ verified 2026-11-12 with conch 0.2.1 (12 pipelines read)")
    #expect(listed.shortHash == "9f2c…41ab")
}

@Test func integrationInstallFlow() throws {
    let info = try JSONDecoder().decode(IntegrationInfo.self, from: Data(jenkinsJSON.utf8))
    var flow = IntegrationInstallFlow(info: info)
    #expect(flow.asksBase && flow.asksUser)
    #expect(flow.hostsWords == "Your Jenkins key will be sent to the address you give only.")
    #expect(flow.blocker == "agree to where the key goes")
    flow.agreed = true
    #expect(flow.blocker == "the address of your server, https://…")
    flow.base = "http://ci.corp"
    #expect(flow.blocker == "the address of your server, https://…")
    flow.base = "https://ci.corp/jenkins"
    #expect(flow.hostsWords == "Your Jenkins key will be sent to ci.corp only.")
    #expect(flow.blocker == "your user name")
    flow.user = "amit"
    #expect(flow.blocker == "the key")
    flow.key = "  tok  "
    #expect(flow.blocker == nil)
    flow.checkIn = "/src/ceph"
    // Shipped already: key, not install; the key is never an argument.
    #expect(flow.arguments == ["integration", "key", "-stdin", "-base", "https://ci.corp/jenkins", "-cwd", "/src/ceph", "jenkins"])
    #expect(!flow.arguments.contains("tok"))
    flow.check = .running
    #expect(flow.blocker == "checking…")

    flow.finished(output: "kept in the Keychain (conch-mcp, JENKINS_TOKEN)\nchecked: read 12 runs of main on Jenkins\n", error: nil)
    #expect(flow.check == .ok("read 12 runs of main on Jenkins"))
    flow.finished(output: "the check failed: GET /job answered 401 (check $JENKINS_TOKEN)\n", error: nil)
    #expect(flow.check == .failed("the check failed: GET /job answered 401 (check $JENKINS_TOKEN)"))
    flow.finished(output: "not checked: run conch integration install from a project that uses it\n", error: nil)
    #expect(flow.check == .ok("not checked: run conch integration install from a project that uses it"))
    flow.finished(output: "", error: "no registry lists jenkins")
    #expect(flow.check == .failed("no registry lists jenkins"))

    // From a registry, not here yet: install, with the follow host said.
    var circle = info
    circle.name = "circleci"; circle.label = "CircleCI"; circle.installed = false; circle.baseURL = nil; circle.userEnv = nil
    circle.hosts = IntegrationHosts(api: "circleci.com", follow: ["logs.example"])
    var f2 = IntegrationInstallFlow(info: circle)
    #expect(f2.hostsWords == "Your CircleCI key will be sent to circleci.com only. Logs are read from logs.example without it.")
    f2.agreed = true; f2.key = "k"
    #expect(f2.arguments == ["integration", "install", "-y", "-key-stdin", "circleci"])
    circle.revoked = "sent logs elsewhere"
    #expect(IntegrationInstallFlow(info: circle).blocker == "revoked: sent logs elsewhere")
    // A key set already need not be given again.
    circle.revoked = nil; circle.keySet = true
    var f3 = IntegrationInstallFlow(info: circle)
    f3.agreed = true
    #expect(f3.blocker == nil)
}

@Test func summariesForTicketsAndNotify() {
    #expect(IntegrationCatalog.summary(kind: "tickets", operations: ["list", "get", "comment", "move"]) == "Open tickets in the Tickets view, tasks from a ticket, comment and move.")
    #expect(IntegrationCatalog.summary(kind: "tickets", operations: ["list", "get"]) == "Open tickets in the Tickets view, tasks from a ticket.")
    #expect(IntegrationCatalog.summary(kind: "notify", operations: ["send"]) == "Agent waiting, done and merged, sent there by conch's server.")
    #expect(IntegrationCatalog.summary(kind: "notify", operations: []) == "")
    #expect(IntegrationCatalog.summary(kind: "host", operations: ["list"]) == "")
}

// A built-in the registry lists that this conch's own list lacks is still
// built in: installed, with nothing to install.
@Test func integrationBuiltInListed() throws {
    let listed = try JSONDecoder().decode([IntegrationListed].self, from: Data("""
    [{"kind":"host","name":"gitlab-forge","label":"GitLab","builtin":true,"version":0,"hosts":{"api":""},"note":"Merge requests."}]
    """.utf8))
    let cards = IntegrationCatalog.cards(here: IntegrationList(integrations: [], problems: nil), listed: listed, uses: [:])
    #expect(cards.count == 1 && cards[0].builtIn && cards[0].installed && cards[0].standing == .builtIn && cards[0].summary == "Merge requests.")
}
