import Foundation
import Testing
@testable import ConchKit

// As the server answers branch.previews: Vercel with a ready deploy at the
// head, a failed one, an older one; Netlify that could not be asked.
private let previewsJSON = """
{"integrations":[
 {"source":"vercel","service":"Vercel","redeploy":true,"promote":true,
  "head":"3f2a91c0ffee","build_log":true,
  "release":{"verb":"Promote","rebuilds":true,"what":"This commit is built again for production, as Vercel's “Promote to Production” does.","note":"It is built with production's environment variables."},
  "deploys":[
   {"id":"dpl_1","state":"ready","url":"https://web-app-3f2a91c-acme.vercel.app","commit":"3f2a91c0ffee","subject":"Pricing page: annual toggle","inspect":"https://vercel.com/acme/web-app/dpl_1","started":"2026-11-16T10:00:00.123456789Z","finished":"2026-11-16T10:01:00Z","at_head":true,"promotable":true},
   {"id":"dpl_2","state":"failed","commit":"9c41e0d","name":"web-app","error":"Build failed: type error in PlanCard.tsx","inspect":"https://vercel.com/acme/web-app/dpl_2","started":"2026-11-16T09:44:00Z"},
   {"id":"dpl_0","state":"building","commit":"77d0b4a"}]},
 {"source":"netlify","service":"Netlify","why":"set $NETLIFY_AUTH_TOKEN where conch's server runs"}]}
"""

private func previews() throws -> BranchPreviews {
    try conchDecoder().decode(BranchPreviews.self, from: Data(previewsJSON.utf8))
}

@Test func deployPreviewsDecode() throws {
    let p = try previews()
    #expect(p.integrations.count == 2)
    let v = p.integrations[0]
    #expect(v.release?.verb == "Promote" && v.redeploy == true && v.deploys?.count == 3)
    let d = v.deploys![0]
    #expect(d.atHead == true && d.promotable == true && d.finished != nil && d.started != nil)
    #expect(p.integrations[1].why?.contains("NETLIFY_AUTH_TOKEN") == true && p.integrations[1].deploys == nil)
    // An older server's previews, or none: nothing to show, nothing breaks.
    let empty = try conchDecoder().decode(BranchPreviews.self, from: Data(#"{"integrations":[]}"#.utf8))
    #expect(!Deploys.hasAny(empty) && Deploys.chip(empty) == nil && !Deploys.hasAny(nil))
}

@Test func deployChipAndRows() throws {
    let p = try previews()
    let chip = try #require(Deploys.chip(p))
    #expect(chip.text == "Vercel · Ready" && chip.tone == .ok && chip.deploy.id == "dpl_1")
    #expect(Deploys.newestReady(p)?.deploy.id == "dpl_1")
    let v = p.integrations[0]
    let (ready, failed, building) = (v.deploys![0], v.deploys![1], v.deploys![2])
    #expect(Deploys.stateText("failed") == "Failed" && Deploys.tone("building") == .busy && Deploys.tone("canceled") == .quiet)
    #expect(Deploys.stateText("") == "Unknown" && Deploys.stateText("queued") == "Queued")
    #expect(Deploys.short(ready.commit) == "3f2a91c" && Deploys.short(nil) == "")
    #expect(Deploys.shown(ready.url) == "web-app-3f2a91c-acme.vercel.app" && Deploys.shown("https://x.app/") == "x.app")
    // The commit's message is git's, by sha; a deploy's name is not one.
    #expect(Deploys.message(ready) == "Pricing page: annual toggle" && Deploys.message(failed) == "" && Deploys.message(building) == "")
    #expect(Deploys.when(ready, now: ready.finished!.addingTimeInterval(120)) == agoText(ready.finished!, now: ready.finished!.addingTimeInterval(120)))
    #expect(Deploys.when(building) == "")

    #expect(Deploys.actions(ready, in: v) == DeployActions(release: true, notReleasable: nil, log: false, redeploy: false, send: false))
    #expect(Deploys.actions(failed, in: v) == DeployActions(release: false, notReleasable: nil, log: true, redeploy: true, send: true))
    #expect(Deploys.actions(building, in: v) == DeployActions(release: false, notReleasable: nil, log: false, redeploy: false, send: false))
    // A file that cannot redeploy or promote offers neither.
    var bare = v
    bare.redeploy = nil; bare.promote = nil
    #expect(!Deploys.actions(failed, in: bare).redeploy && !Deploys.actions(ready, in: bare).release)
}

// Netlify will not publish a deploy preview: the row says so where the
// button would be, as the mock does, and offers no button.
@Test func deployNetlifyPreviewNotPublishable() {
    let s = DeploySource(source: "netlify", service: "Netlify", redeploy: true, promote: true,
                         release: DeployRelease(verb: "Publish", what: "This deploy becomes the site's published deploy.", note: "Nothing is built again."))
    let preview = DeployInfo(id: "d1", state: "ready", environment: "deploy-preview", commit: "3f2a91c",
                             notPromotable: "A deploy preview can't be published: merge, then publish the production branch's deploy")
    let a = Deploys.actions(preview, in: s)
    #expect(!a.release && a.notReleasable?.hasPrefix("A deploy preview can't be published") == true)
    let prod = DeployInfo(id: "d2", state: "ready", url: "https://web-app.netlify.app", environment: "production", commit: "e18b03f", promotable: true)
    #expect(Deploys.actions(prod, in: s).release)
    let c = DeployConfirm(source: s, deploy: prod)
    #expect(c.title == "Publish e18b03f to production?" && c.verb == "Publish" && c.note == "Nothing is built again.")
    #expect(c.change.from == "Netlify production" && c.change.to == "e18b03f · web-app.netlify.app")
}

@Test func deployConfirmWords() throws {
    let v = try previews().integrations[0]
    let c = DeployConfirm(source: v, deploy: v.deploys![0])
    #expect(c.title == "Promote 3f2a91c to production?" && c.body.contains("built again for production"))
    #expect(c.id == "vercel|dpl_1")
    // No commit: the deploy's id; no words from the server: plain ones.
    let plain = DeployConfirm(source: DeploySource(source: "acme", service: "Acme"), deploy: DeployInfo(id: "dpl_9", state: "ready"))
    #expect(plain.title == "Promote dpl_9 to production?" && plain.body.contains("Acme") && plain.note == nil && plain.change.to == "dpl_9 · dpl_9")
}

@Test func deployFixArguments() {
    #expect(fixDeployArguments(dir: "/w", branch: "feat", agent: "claude", source: "vercel", deploy: "dpl_2")
        == ["branch", "fix-ci", "-cwd", "/w", "-branch", "feat", "-agent", "claude", "-source", "vercel", "-deploy", "dpl_2"])
}

// Every state has its words, colour and line where a link or error goes.
@Test func deployFourStates() {
    #expect(["ready", "building", "failed", "canceled"].map(Deploys.stateText) == ["Ready", "Building", "Failed", "Canceled"])
    #expect(["ready", "building", "failed", "canceled"].map(Deploys.tone) == [.ok, .busy, .bad, .quiet])
    #expect(Deploys.quietText(DeployInfo(id: "a", state: "building")) == "Building…")
    #expect(Deploys.quietText(DeployInfo(id: "a", state: "canceled")) == "Canceled before it was ready")
    #expect(Deploys.quietText(DeployInfo(id: "a", state: "ready")) == nil)
}

// With a pull request, the deploys at its head; without, the history.
@Test func deployScopeLine() throws {
    var v = try previews().integrations[0]
    #expect(Deploys.scopeLine(v) == "Deploys at the pull request's head, 3f2a91c, newest first. Earlier commits' deploys aren't listed.")
    v.head = nil
    #expect(Deploys.scopeLine(v) == "No pull request: the branch's deploys, newest first.")
}

// Nothing builds from here: said, with no table.
@Test func deployMissing() throws {
    let p = try conchDecoder().decode(BranchPreviews.self, from: Data(#"""
    {"integrations":[{"source":"netlify","service":"Netlify","missing":"No Netlify site in your account builds from acme/web-app. Only your first 100 sites are read."}]}
    """#.utf8))
    #expect(p.integrations[0].missing?.hasPrefix("No Netlify site") == true)
    #expect(Deploys.chip(p) == nil && Deploys.hasAny(p))
}

// Netlify's API has no build log: the panel is the error message, said so.
@Test func deployLogWords() {
    let netlify = DeploySource(source: "netlify", service: "Netlify", buildLog: false)
    let vercel = DeploySource(source: "vercel", service: "Vercel", buildLog: true)
    #expect(Deploys.logLabel(netlify) == "Error message" && Deploys.logLabel(vercel) == "Build log")
    #expect(Deploys.noLogNote(netlify) == "Netlify's API gives no build log: this is the deploy's error message. The full log is on Netlify's deploy page.")
    #expect(Deploys.noLogNote(vercel) == nil)
    #expect(Deploys.sendWhat(netlify) == "As a failed CI run is: the error message, with the commit.")
    #expect(Deploys.sendWhat(vercel) == "As a failed CI run is: the end of the log, with the commit.")
}

// The row says the first sentence of why it can't be published; the note
// under the table says it all, once.
@Test func deployNotReleasableShort() {
    let why = "A deploy preview can't be published. Publish a production-branch deploy — when auto publishing is locked a ready one waits for this — or an earlier one to roll back."
    #expect(Deploys.firstSentence(why) == "A deploy preview can't be published")
    let s = DeploySource(source: "netlify", service: "Netlify", deploys: [DeployInfo(id: "d1", state: "ready", notPromotable: why), DeployInfo(id: "d0", state: "ready", notPromotable: why)])
    #expect(Deploys.notReleasableNotes(s) == [why])
    #expect(Deploys.notReleasableNotes(DeploySource(source: "vercel", service: "Vercel")).isEmpty)
}

// Vercel's promote is a new production build: shown as Building until the
// service lists it; Netlify's publish is immediate, said as a banner.
@Test func deployAfterRelease() throws {
    let p = try previews()
    let v = p.integrations[0]
    let c = DeployConfirm(source: v, deploy: v.deploys![0])
    let pending = try #require(Deploys.pendingBuild(c))
    #expect(pending.state == "building" && pending.environment == "production" && pending.commit == "3f2a91c0ffee")
    let merged = Deploys.with(p, pending: ["vercel": pending])
    #expect(merged.integrations[0].deploys?.first == pending && merged.integrations[0].deploys?.count == 4)
    // Once the service lists a production build of that commit, it is that.
    var listed = p
    listed.integrations[0].deploys!.insert(DeployInfo(id: "dpl_9", state: "building", environment: "production", commit: "3f2a91c0ffee"), at: 0)
    #expect(Deploys.with(listed, pending: ["vercel": pending]).integrations[0].deploys?.first?.id == "dpl_9")
    #expect(Deploys.with(listed, pending: ["vercel": pending]).integrations[0].deploys?.count == 4)
    // Netlify: no pending build; the words say it is live.
    let n = DeploySource(source: "netlify", service: "Netlify", release: DeployRelease(verb: "Publish", what: "x"))
    let nc = DeployConfirm(source: n, deploy: DeployInfo(id: "d2", state: "ready", url: "https://web-app.netlify.app", commit: "e18b03f"))
    #expect(Deploys.pendingBuild(nc) == nil)
    #expect(Deploys.releasedWords(nc) == "Netlify now serves e18b03f: it published the deploy as it was built.")
    #expect(Deploys.releasedWords(c) == "Vercel is building 3f2a91c for production: the production domains move to it once that build is ready.")
}
