import Foundation
import Testing
@testable import ConchKit

@Suite struct BackgroundsTests {
    @Test func readsTheSettings() {
        let toml = """
        [ui]
        background = "stars"

        [app]
        background = "reef"
        background_through = 24
        background_frost = "strong"
        background_motion = "still"
        """
        let l = SceneLook(toml: toml)
        #expect(l.scene == .reef) // [ui]'s is not the app's
        #expect(abs(l.through - 0.24) < 1e-9)
        #expect(l.frost == .strong && l.still && l.on)
        #expect(AppLook(toml: toml).scene == l)
    }

    @Test func whatIsMissingOrWrongIsTheDefault() {
        let none = SceneLook(toml: nil)
        #expect(none == SceneLook() && !none.on && none.frost == .light && !none.still && none.through == 0)
        let odd = SceneLook(toml: """
        [app]
        background = "Volcano"
        background_through = 12.5
        background_frost = "frosted"
        background_motion = "fast"
        """)
        #expect(odd.scene == .none && odd.through == 0 && odd.frost == .light && !odd.still)
        #expect(BackgroundScene(setting: "  LAGOON ") == .lagoon)
        #expect(BackgroundScene(setting: "") == .none && BackgroundScene.none.setting == nil)
    }

    @Test func throughIsKeptInRange() {
        #expect(SceneLook(toml: "[app]\nbackground_through = 95\n").through == SceneLook.throughMax)
        #expect(SceneLook(toml: "[app]\nbackground_through = -5\n").through == 0)
        #expect(SceneLook(through: 2).through == SceneLook.throughMax)
        // Panes are solid with no scene, whatever through says.
        #expect(SceneLook(scene: .none, through: 0.3).paneOpacity == 1)
        #expect(abs(SceneLook(scene: .reef, through: 0.3).paneOpacity - 0.7) < 1e-9)
    }

    @Test func frostNeverLeavesTextBare() {
        for f in SceneFrost.allCases { #expect(f.tint >= 0.3 && f.tint < 1) }
        #expect(SceneFrost.off.tint < SceneFrost.light.tint && SceneFrost.light.tint < SceneFrost.strong.tint)
        #expect(!SceneFrost.off.blurs && SceneFrost.light.blurs)
    }

    @Test func movesOnlyWhenNothingSaysStill() {
        let reef = SceneLook(scene: .reef)
        #expect(reef.moves(reduceMotion: false, lowPower: false))
        #expect(!reef.moves(reduceMotion: true, lowPower: false))
        #expect(!reef.moves(reduceMotion: false, lowPower: true))
        #expect(!SceneLook(scene: .reef, still: true).moves(reduceMotion: false, lowPower: false))
        #expect(!SceneLook().moves(reduceMotion: false, lowPower: false))
    }

    @Test func everySceneHasADayAndANight() {
        for s in BackgroundScene.builtIn where s != .none && s != .board {
            #expect(s.sky(night: true).count == 3 && s.sky(night: false).count == 3)
            #expect(s.sky(night: true) != s.sky(night: false))
            // Night is the darker of the two at the top of the sky.
            #expect(s.sky(night: true)[0].luminance < s.sky(night: false)[0].luminance)
            #expect(s.label != BackgroundScene.none.label)
        }
        #expect(BackgroundScene.none.sky(night: true).isEmpty && BackgroundScene.stars.ground(night: true) == nil)
    }

    @Test func piecesAreTheSameEveryTime() {
        let a = SceneMotion.pieces(50, seed: 7), b = SceneMotion.pieces(50, seed: 7)
        #expect(a == b && a.count == 50)
        #expect(a != SceneMotion.pieces(50, seed: 8))
        #expect(SceneMotion.pieces(0, seed: 1).isEmpty && SceneMotion.pieces(-3, seed: 1).isEmpty)
        for p in a {
            #expect((0..<1).contains(p.x) && (0..<1).contains(p.y) && (0..<1).contains(p.phase))
            #expect(p.period >= 9 && p.period < 23)
        }
    }

    @Test func motionStaysInSightAndRepeats() {
        let ps = SceneMotion.pieces(40, seed: 3)
        for p in ps {
            for t in stride(from: -50.0, through: 500, by: 7.3) {
                let up = SceneMotion.rise(p, at: t), down = SceneMotion.fall(p, at: t)
                #expect(up >= -0.05 - 1e-9 && up <= 1.05 + 1e-9)
                #expect(down >= -0.05 - 1e-9 && down <= 1.05 + 1e-9)
                let tw = SceneMotion.twinkle(p, at: t)
                #expect(tw >= 0.15 - 1e-9 && tw <= 1 + 1e-9)
                #expect(abs(SceneMotion.sway(p, at: t, by: 0.02)) <= 0.02 + 1e-9)
            }
            // One whole trip later it is where it was.
            #expect(abs(SceneMotion.rise(p, at: 3) - SceneMotion.rise(p, at: 3 + p.period)) < 1e-6)
        }
        #expect(abs(SceneMotion.cross(at: 0, period: 40) - -0.1) < 1e-9)
        #expect(abs(SceneMotion.cross(at: -10, period: 40) - SceneMotion.cross(at: 30, period: 40)) < 1e-9)
    }

    // MARK: a project's own scene

    @Test func aProjectHasItsOwnScene() {
        let toml = """
        [app]
        background = "reef"
        background_fireworks = false

        [app.project_backgrounds]
        conch = "pines"
        "my.site" = "none" # quiet
        typo = "volcano"

        [ui]
        background = "stars"
        """
        let l = SceneLook(toml: toml)
        #expect(l.scene == .reef && !l.fireworks)
        #expect(l.projects == ["conch": .pines, "my.site": .none]) // a typo leaves the window's
        #expect(l.scene(for: "conch") == .pines)
        #expect(l.scene(for: "my.site") == .none)
        #expect(l.scene(for: "typo") == .reef && l.scene(for: nil) == .reef && l.scene(for: "other") == .reef)
        // What is drawn for one is just that scene.
        let shown = l.showing(project: "conch")
        #expect(shown.scene == .pines && shown.projects.isEmpty && shown.frost == l.frost)
        #expect(!l.showing(project: "my.site").moves(reduceMotion: false, lowPower: false))
        // Fireworks are on unless said otherwise.
        #expect(SceneLook(toml: "[app]\nbackground = \"reef\"\n").fireworks)
    }

    @Test func aProjectsSceneAloneTurnsTheFrameFrosted() {
        let l = SceneLook(toml: "[app.project_backgrounds]\nconch = \"lagoon\"\n")
        #expect(l.scene == .none && l.on)
        #expect(l.scene(for: "conch") == .lagoon && l.scene(for: "api") == .none)
        // A project set to none only is no scene anywhere.
        #expect(!SceneLook(toml: "[app.project_backgrounds]\nconch = \"none\"\n").on)
    }

    @Test func readsATableAndNothingElse() {
        let toml = """
        [app]
        a = "1"
        [app.project_backgrounds]
        b = 'two'
        c = "three # not a comment"
        d = four # a comment
        # e = "commented"
        [[actions]]
        f = "x"
        """
        #expect(tomlTable("app.project_backgrounds", in: toml) == ["b": "two", "c": "three # not a comment", "d": "four"])
        #expect(tomlTable("app.project_backgrounds", in: nil).isEmpty)
        #expect(tomlTable("missing", in: toml).isEmpty)
    }

    // MARK: fireworks

    func project(_ prs: [(String, Int, String?)]) -> ProjectInfo {
        ProjectInfo(id: "p1", name: "conch", path: "/x", branches: prs.map { b in
            var i = BranchInfo(name: b.0)
            i.pr = PRInfo(number: b.1, state: b.2)
            return i
        })
    }

    @Test func aMergeIsOnlyOneSeenHappen() {
        let open = project([("a", 1, "OPEN"), ("b", 2, "OPEN"), ("c", 3, "CLOSED")])
        let after = project([("a", 1, "MERGED"), ("b", 2, "OPEN"), ("c", 3, "MERGED"), ("d", 4, "MERGED")])
        #expect(newlyMerged(old: open, new: after) == [MergedPR(project: "conch", branch: "a", number: 1)])
        // First read, already merged, the same reading again: nothing.
        #expect(newlyMerged(old: nil, new: after).isEmpty)
        #expect(newlyMerged(old: after, new: after).isEmpty)
        // Another pull request on the same branch is not that one merging.
        #expect(newlyMerged(old: project([("a", 9, "OPEN")]), new: project([("a", 1, "MERGED")])).isEmpty)
        // A branch gone with its merge is not seen merging.
        #expect(newlyMerged(old: open, new: project([])).isEmpty)
        // No state, or lower case, as an older server might say.
        #expect(newlyMerged(old: project([("a", 1, nil)]), new: project([("a", 1, "MERGED")])).isEmpty)
        #expect(newlyMerged(old: project([("a", 1, "open")]), new: project([("a", 1, "merged")])).count == 1)
    }

    @Test func burstsStayInTheWindowAndInTime() {
        let bs = Fireworks.bursts(6, seed: 3)
        #expect(bs.count == 6 && bs == Fireworks.bursts(6, seed: 3))
        #expect(Fireworks.bursts(0, seed: 3).isEmpty && Fireworks.bursts(-1, seed: 3).isEmpty)
        // Spread out: each in its own sixth of the width.
        for (i, b) in bs.enumerated() { #expect(Int((b.x - 0.1) / 0.8 * 6) == i) }
        for b in bs {
            #expect(b.x >= 0.1 && b.x <= 0.9 && b.y >= 0.15 && b.y <= 0.55)
            #expect(Fireworks.progress(b, at: -0.01) == nil)
            // Every burst starts and ends within the show.
            let start = b.phase * (Fireworks.duration - Fireworks.burst)
            #expect(Fireworks.progress(b, at: start) == 0)
            #expect(Fireworks.progress(b, at: start + Fireworks.burst / 2).map { abs($0 - 0.5) < 1e-9 } == true)
            #expect(Fireworks.progress(b, at: start + Fireworks.burst + 0.01) == nil)
            #expect(start + Fireworks.burst <= Fireworks.duration)
        }
    }
}
