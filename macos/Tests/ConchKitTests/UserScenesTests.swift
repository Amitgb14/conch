import Foundation
import Testing
@testable import ConchKit

@Suite struct UserScenesTests {
    // MARK: names

    @Test func aSceneOfYoursIsKnownByItsFile() {
        #expect(BackgroundScene(setting: "bay", mine: ["bay"]) == .mine("bay"))
        #expect(BackgroundScene(setting: " BAY ", mine: ["bay"]) == .mine("bay"))
        // A file since removed, or never there, is no scene.
        #expect(BackgroundScene(setting: "bay") == .none)
        // A built-in name is the built-in scene, whatever files there are.
        #expect(BackgroundScene(setting: "reef", mine: ["reef"]) == .reef)
        #expect(BackgroundScene(setting: "board") == .board && BackgroundScene.board.setting == "board")
        #expect(BackgroundScene.mine("bay").rawValue == "bay" && BackgroundScene.mine("bay").setting == "bay")
        let l = SceneLook(toml: "[app]\nbackground = \"bay\"\n[app.project_backgrounds]\napi = \"dusk\"\nweb = \"gone\"\n", mine: ["bay", "dusk"])
        #expect(l.scene == .mine("bay") && l.projects == ["api": .mine("dusk")])
        #expect(l.scene(for: "web") == .mine("bay"))
    }

    // MARK: files

    func read(_ text: String, files: [String: Int] = [:]) -> Result<UserScene, ThemeFileError> {
        userScene(id: "bay", text: text, dir: "/s", size: { files[$0] })
    }

    @Test func readsAScene() throws {
        let s = try read("""
        # the bay at dusk
        name = "The bay"
        sky = "#FFB37A #F07A5A #5A3A6A"
        night_sky = "#1A1030 #05040C"
        ground = "#2A1A2A"   # sand
        image = "bay.jpg"
        pieces = "SNOW"
        count = 40
        """, files: ["bay.jpg": 1000]).get()
        #expect(s.id == "bay" && s.label == "The bay" && s.pieces == .snow && s.count == 40 && s.particles == 40)
        #expect(s.day.sky == [RGB("#FFB37A"), RGB("#F07A5A"), RGB("#5A3A6A")] && s.day.image == "/s/bay.jpg")
        // A night with a sky of its own has no picture unless it says so.
        #expect(s.night.sky == [RGB("#1A1030"), RGB("#05040C")] && s.night.image == nil)
        #expect(s.night.ground == RGB("#2A1A2A"))
    }

    @Test func whatIsLeftOutIsTheDay() throws {
        let s = try read("sky = \"#102030\"").get()
        #expect(s.label == "bay" && s.night == s.day && s.pieces == .none && s.particles == 0 && s.count == 60)
        let pic = try read("image = \"p.png\"", files: ["p.png": 1]).get()
        #expect(pic.night.image == "/s/p.png") // a picture alone, day and night
    }

    @Test func aWrongFileSaysWhatIsWrong() {
        func problem(_ text: String, files: [String: Int] = [:]) -> String {
            if case .failure(let e) = read(text, files: files) { return e.message }
            return ""
        }
        #expect(problem("name = \"x\"").contains("needs a sky or an image"))
        #expect(problem("sky = \"red\"").contains("not a colour"))
        #expect(problem("sky = \"#000000 #111111 #222222 #333333 #444444\"").contains("1 to 4"))
        #expect(problem("sky = \"#000000\"\nwind = \"strong\"").contains("not a scene key"))
        #expect(problem("sky = \"#000000\"\ncount = 0").contains("1 to 200"))
        #expect(problem("sky = \"#000000\"\ncount = 201").contains("1 to 200"))
        #expect(problem("sky = \"#000000\"\npieces = \"fish\"").contains("not \"bubbles\""))
        #expect(problem("sky = \"#000000\"\nsky").contains("line 2"))
        #expect(problem("sky = \"#000000").contains("not closed"))
        // A picture is a file beside the scene, by name, and a picture.
        #expect(problem("image = \"../../secret.png\"").contains("by name alone"))
        #expect(problem("image = \"/etc/x.png\"").contains("by name alone"))
        #expect(problem("image = \".x.png\"").contains("by name alone"))
        #expect(problem("image = \"run.sh\"", files: ["run.sh": 1]).contains("not a picture"))
        #expect(problem("image = \"gone.png\"").contains("not in the scenes folder"))
        #expect(problem("image = \"huge.png\"", files: ["huge.png": UserScene.maxImageBytes + 1]).contains("over 25 MB"))
    }

    @Test func readsTheFolder() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("scenes-\(UUID().uuidString)").path
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(atPath: dir) }
        func write(_ name: String, _ text: String) throws { try text.write(toFile: (dir as NSString).appendingPathComponent(name), atomically: true, encoding: .utf8) }
        try write("bay.toml", "sky = \"#102030\"\nimage = \"bay.png\"")
        try write("bay.png", "not really a picture, but there")
        try write("dusk.toml", "sky = \"#000000\"\npieces = \"stars\"")
        try write("reef.toml", "sky = \"#000000\"")
        try write("Bad Name.toml", "sky = \"#000000\"")
        try write("broken.toml", "sky = 3")
        try write("notes.txt", "x")
        try FileManager.default.createDirectory(atPath: (dir as NSString).appendingPathComponent("folder.png"), withIntermediateDirectories: true)
        try write("pic.toml", "image = \"folder.png\"")
        let r = userScenes(in: dir)
        #expect(r.scenes.map(\.id) == ["bay", "dusk"])
        #expect(r.scenes[0].day.image == (dir as NSString).appendingPathComponent("bay.png"))
        #expect(r.problems.count == 4)
        #expect(r.problems.contains { $0.hasPrefix("reef.toml") && $0.contains("built-in") })
        #expect(r.problems.contains { $0.hasPrefix("Bad Name.toml") })
        #expect(r.problems.contains { $0.hasPrefix("broken.toml") })
        #expect(r.problems.contains { $0.hasPrefix("pic.toml") && $0.contains("not in the scenes folder") })
        #expect(userScenes(in: dir + "-missing").scenes.isEmpty)
    }

    // MARK: the board

    var utc: Calendar {
        var c = Calendar(identifier: .gregorian)
        c.timeZone = TimeZone(identifier: "UTC")!
        return c
    }

    @Test func departuresAreRowsOfTheirWidth() {
        let at = Date(timeIntervalSince1970: 9 * 3600 + 5 * 60) // 09:05
        let ds = [
            Departure(time: at.addingTimeInterval(600), agent: "codex", to: "web/search", state: .done),
            Departure(time: at, agent: "claude", to: "api/login-with-a-very-long-branch-name", state: .working),
            Departure(time: nil, agent: "gemini", to: "docs", state: .blocked),
        ]
        let rows = departureRows(ds, width: 40, rows: 10, calendar: utc)
        #expect(rows.allSatisfy { $0.count == 40 })
        // Waiting on you first, then working, then by time.
        #expect(rows[0].hasPrefix("--:-- GEMINI   DOCS") && rows[0].hasSuffix("NEEDS YOU"))
        #expect(rows[1].hasPrefix("09:05 CLAUDE   API/LOGIN") && rows[1].hasSuffix("BOARDING "))
        #expect(rows[2].hasPrefix("09:15 CODEX") && rows[2].hasSuffix("ARRIVED  "))
        #expect(departureRows(ds, width: 40, rows: 2, calendar: utc).count == 2)
        #expect(departureRows([], width: 20, rows: 3) == ["NO DEPARTURES       "])
        #expect(departureRows(ds, width: 0, rows: 3).isEmpty && departureRows(ds, width: 10, rows: 0).isEmpty)
        // Too narrow for a destination: still the width.
        #expect(departureRows(ds, width: 18, rows: 3, calendar: utc).allSatisfy { $0.count == 18 })
        #expect(Departure.status(.idle) == "ON TIME" && Departure.status(.unknown) == "ON TIME")
    }

    @Test func flapsTurnForwardToTheirLetter() {
        var f = SplitFlap(width: 4, rows: 2)
        #expect(f.shown(at: 0) == ["    ", "    "] && !f.turning(at: 0))
        f.show(["AB", "z_?"], at: 10)
        // Before anything turns, the old letters; at the end, the new.
        #expect(f.shown(at: 10) == ["    ", "    "])
        #expect(f.turning(at: 10.01))
        #expect(f.shown(at: f.settles) == ["AB  ", "Z-. "]) // upper case; _ a dash; no flap for ? a dot
        #expect(!f.turning(at: f.settles))
        // Part way, a letter is somewhere between, never past it.
        let mid = f.shown(at: 10 + SplitFlap.flap * 1.5)[0]
        #expect(mid.first == "A")
        // The same rows again turn nothing.
        let end = f.settles
        f.show(["AB", "z_?"], at: end + 1)
        #expect(f.settles == end && f.shown(at: end + 1) == ["AB  ", "Z-. "])
        // Changed mid-turn: from what shows then, forward only.
        f.show(["C"], at: end + 2)
        #expect(f.shown(at: f.settles) == ["C   ", "    "])
        #expect(SplitFlap.flaps(3, 1) == SplitFlap.alphabet.count - 2)
        // Set: at once.
        f.set(["DONE"])
        #expect(f.shown(at: 0) == ["DONE", "    "] && !f.turning(at: 0))
        // Longer rows than the board are cut; a board of nothing is nothing.
        f.set(["TOO LONG", "", "THIRD"])
        #expect(f.shown(at: 0) == ["TOO ", "    "])
        #expect(SplitFlap(width: -1, rows: -1).shown(at: 0).isEmpty)
    }
}
