import Foundation
import Testing
@testable import ConchKit

private let circle = PRCheck(name: "ci/circleci: build-and-test", state: "fail",
                             url: "https://app.circleci.com/pipelines/github/acme/api/8812", source: "circleci")

private func pr(_ runs: [PRCheck]) -> PRInfo {
    var p = PRInfo(number: 412, state: "OPEN", checks: "fail")
    p.runs = runs
    return p
}

@Test func ciCardFromAnUnreadableFailure() {
    let jenkins = PRCheck(name: "continuous-integration/jenkins", state: "pass", url: "https://ci.example.org/job/api/1", source: "jenkins")
    let actions = PRCheck(name: "lint", state: "fail", url: "https://github.com/a/b/actions/runs/1", source: "github-actions", readable: true)
    let card = CICard(pr([jenkins, actions, circle]), uses: [IntegrationUse(name: "circleci", label: "CircleCI", kind: "ci", why: ".circleci/config.yml")])
    #expect(card?.check == circle)
    #expect(card?.service == "CircleCI")
    #expect(card?.found == "Found .circleci/config.yml")
    #expect(card?.text(agent: "Claude") == "ci/circleci: build-and-test failed — conch can't read CircleCI yet, so Claude wasn't sent the log.")
    #expect(card?.text(agent: nil).hasSuffix("so the agent wasn't sent the log.") == true)
    #expect(card?.install == "Install CircleCI…" && card?.open == "Open in CircleCI")
    #expect(CICard(pr([circle]), uses: [])?.found == nil, "no detection, no reason")
}

@Test func ciCardAbsent() {
    var readable = circle
    readable.readable = true
    var passing = circle
    passing.state = "pass"
    let unknown = PRCheck(name: "deploy", state: "fail", url: "https://deploy.example.org/1")
    let old = PRCheck(name: "ci/circleci: build-and-test", state: "fail", url: circle.url) // an older server names no source
    for runs in [[readable], [passing], [unknown], [old], []] {
        #expect(CICard(pr(runs), uses: []) == nil, "\(runs)")
    }
    #expect(CICard(nil, uses: []) == nil)
}

@Test func checkSourcesSaid() {
    #expect(ciServiceLabel("circleci") == "CircleCI")
    #expect(ciServiceLabel("github-actions") == "GitHub Actions")
    #expect(ciServiceLabel("gitlab-ci") == "GitLab CI")
    #expect(ciServiceLabel("woodpecker") == "woodpecker")
    #expect(checkWhere(circle) == "app.circleci.com")
    let jenkins = PRCheck(name: "continuous-integration/jenkins", state: "pass", url: "https://ci.example.org/job/api/1", source: "jenkins")
    #expect(checkWhere(jenkins) == "no integration needed to see it")
    #expect(checkWhere(PRCheck(name: "x", state: "pass")) == "")
    #expect(checkWhere(PRCheck(name: "x", state: "fail", url: "not a url")) == "")
}

@Test func ciCanRerun() {
    #expect(ciCanRerun(pr([circle])))
    var green = PRInfo(number: 1, state: "OPEN", checks: "pass")
    #expect(!ciCanRerun(green))
    green.runs = [PRCheck(name: "x", state: "fail")]
    #expect(ciCanRerun(green), "a failing run though the rollup lags")
    #expect(!ciCanRerun(nil))
}

// The autopilot's note about a failed run reads as the feed shows it.
@Test func noteCIHeadline() throws {
    let json = #"{"project_id":"p","branch":"rgw-lc-fix","text":"t","ci":{"source":"circleci","service":"CircleCI","workflow":"build-and-test","job":"unittest-rgw","lines":212,"host":"circleci.com"}}"#
    let n = try JSONDecoder().decode(AutopilotNote.self, from: Data(json.utf8))
    #expect(n.headline == "CI failed on rgw-lc-fix — CircleCI · workflow build-and-test · job unittest-rgw")
    #expect(n.ci?.read == "read 212 lines of the failed log from circleci.com")
    let plain = try JSONDecoder().decode(AutopilotNote.self, from: Data(#"{"project_id":"p","branch":"b","text":"merged #4"}"#.utf8))
    #expect(plain.headline == "merged #4", "a note about anything else is its text")
    #expect(plain.ci == nil)
    let one = NoteCI(source: "github-actions", workflow: "ci", lines: 1)
    #expect(one.read == "read 1 line of the failed log")
    #expect(NoteCI().read == nil)
    #expect(NoteCI().headline(branch: "b") == "CI failed on b")
}
