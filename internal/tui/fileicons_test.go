package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func allIcons() map[string]fileIcon {
	all := map[string]fileIcon{"default": iconDefault, "folder": iconFolder, "open": iconFolderOpen, "link": iconLink}
	for k, v := range iconNames {
		all["name "+k] = v
	}
	for k, v := range iconExts {
		all["ext "+k] = v
	}
	return all
}

func TestIconTableMeasures(t *testing.T) {
	for key, ic := range allIcons() {
		if w := ansi.StringWidth(ic.nerd); w < 1 || w > iconWidth {
			t.Errorf("%s: nerd glyph %q is %d cells", key, ic.nerd, w)
		}
		if len([]rune(ic.nerd)) != 1 {
			t.Errorf("%s: nerd glyph %q is not one rune", key, ic.nerd)
		}
		if w := ansi.StringWidth(ic.text); w != iconWidth {
			t.Errorf("%s: text tag %q is %d cells", key, ic.text, w)
		}
		if ic.role < roleFile || ic.role > roleFolder {
			t.Errorf("%s: role %d", key, ic.role)
		}
		// Every mode draws exactly the column, coloured or not.
		for _, mode := range iconModes {
			want := iconWidth
			if mode == iconsOff {
				want = 0
			}
			for _, plain := range []bool{false, true} {
				if w := ansi.StringWidth(renderIcon(ic, mode, plain)); w != want {
					t.Errorf("%s in %s (plain %v): %d cells", key, mode, plain, w)
				}
			}
		}
	}
}

func TestIconRolesInEveryTheme(t *testing.T) {
	defer applyTheme("conch", "")
	for _, th := range themes {
		applyTheme(th.name, "")
		for r := roleFile; r <= roleFolder; r++ {
			if c := roleColor(th, r); c == "" {
				t.Errorf("%s: role %d has no colour", th.name, r)
			}
			if _, ok := roleStyles[r]; !ok {
				t.Errorf("%s: role %d has no style", th.name, r)
			}
		}
	}
}

func TestIconFor(t *testing.T) {
	for _, c := range []struct {
		name              string
		dir, open, broken bool
		want              string // text tag, or the icon's nerd glyph for folders
	}{
		{name: "main.go", want: "go"},
		{name: "app.ts", want: "ts"},
		{name: "App.TSX", want: "tx"}, // case does not matter
		{name: "go.mod", want: "go"},
		{name: "Dockerfile", want: "dk"},
		{name: "Dockerfile.dev", want: "dk"},
		{name: ".env.local", want: "en"},
		{name: "README.md", want: "rd"}, // the name wins over .md
		{name: "notes.md", want: "md"},
		{name: "package.json", want: "np"}, // the name wins over .json
		{name: "data.json", want: "{}"},
		{name: "archive.tar.gz", want: "zp"}, // the last extension
		{name: "Makefile", want: "mk"},
		{name: "LICENSE", want: "li"},
		{name: "noext", want: "  "},
		{name: "weird.unknownext", want: "  "},
		{name: ".bashrc", want: "sh"},    // a known dotfile is matched whole
		{name: ".unknownrc", want: "  "}, // a dotfile's name is not an extension
		{name: ".", want: "  "},
		{name: "..", want: "  "},
		{name: "", want: "  "},
		{name: "mix.exs", want: "ex"}, // the name wins over .exs
		{name: "server.exs", want: "ex"},
		{name: "Analysis.R", want: "r "}, // one-letter extensions
		{name: "start.s", want: "as"},
		{name: "libfoo.so", want: "bn"},
		{name: "main.tf", want: "tf"},
		{name: "notes.ipynb", want: "nb"},
		{name: "report.xlsx", want: "xl"},
		{name: "id_rsa.pub", want: "ky"},
		{name: "site.tar.zst", want: "zp"},
		{name: "CHANGELOG.md", want: "ch"}, // the name wins over .md
		{name: "Jenkinsfile", want: "jk"},
		{name: "café.go", want: "go"},
		{name: "éclair.py", want: "py"}, // combining mark
		{name: "🚀.rs", want: "rs"},
		{name: "link", broken: true, want: "->"},
	} {
		if got := iconFor(c.name, c.dir, c.open, c.broken).text; got != c.want {
			t.Errorf("iconFor(%q) = %q, want %q", c.name, got, c.want)
		}
	}
	if iconFor("src", true, false, false) != iconFolder || iconFor("src", true, true, false) != iconFolderOpen {
		t.Error("folders")
	}
	// .ts and .tsx differ in glyph and colour: that is the point.
	ts, tsx := iconFor("a.ts", false, false, false), iconFor("a.tsx", false, false, false)
	if ts.nerd == tsx.nerd || ts.role == tsx.role {
		t.Errorf("ts %+v tsx %+v", ts, tsx)
	}
}

func TestIconMode(t *testing.T) {
	for in, want := range map[string]string{"": iconsText, "text": iconsText, "NERD": iconsNerd, " off ": iconsOff, "emoji": iconsText} {
		if got := iconMode(in); got != want {
			t.Errorf("iconMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFitCells(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"", 2, "  "},
		{"a", 2, "a "},
		{"ab", 2, "ab"},
		{"abc", 2, "ab"},
		{"界", 2, "界"},
		{"界界", 2, "界"},
		{"a界", 2, "a "}, // a wide rune never gets cut in half
	} {
		if got := fitCells(c.in, c.n); got != c.want {
			t.Errorf("fitCells(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

// TestIconTableIsSane guards the two things a wrong codepoint or a careless
// tag would break: a glyph that is not a Nerd Font glyph at all (it would
// draw as an ordinary character in every font), and two letters that mean
// one kind of file in one row and another kind in the next.
func TestIconTableIsSane(t *testing.T) {
	for key, ic := range allIcons() {
		r := []rune(ic.nerd)[0]
		// The Private Use Area, where Nerd Fonts put their glyphs.
		if !(r >= 0xE000 && r <= 0xF8FF) && !(r >= 0xF0000 && r <= 0xFFFFD) {
			t.Errorf("%s: glyph %q (U+%04X) is outside the Private Use Area", key, ic.nerd, r)
		}
		for _, c := range ic.text {
			if c < 0x20 || c > 0x7e {
				t.Errorf("%s: tag %q is not plain ASCII, so it needs a font too", key, ic.text)
			}
		}
	}
	// Within the extensions, one tag means one kind of file: the tag is all
	// somebody has to go on in a list of names.
	roles := map[string]string{}
	for ext, ic := range iconExts {
		if was, ok := roles[ic.text]; ok && was != "" {
			if prev := iconExts[was]; prev.role != ic.role {
				t.Errorf("tag %q is %v for %s and %v for %s", ic.text, prev.role, was, ic.role, ext)
			}
		}
		roles[ic.text] = ext
	}
}

// TestIconsCoverWhatRepositoriesHold is the list this table exists for: the
// files somebody opening the explorer on a real project sees. A miss here is
// a blank column, so a new one is added rather than the case deleted.
func TestIconsCoverWhatRepositoriesHold(t *testing.T) {
	for _, name := range []string{
		"main.go", "server.ts", "App.tsx", "index.js", "app.vue", "page.svelte", "site.astro",
		"main.rs", "lib.c", "lib.h", "node.cc", "Main.java", "App.kt", "app.swift", "Program.cs",
		"main.zig", "app.ex", "node.erl", "core.clj", "types.ml", "Main.fs", "plot.jl", "model.r",
		"train.py", "notebook.ipynb", "app.rb", "index.php", "init.lua", "run.sh", "build.ps1",
		"notes.md", "spec.rst", "book.tex", "paper.pdf", "slides.pptx", "budget.xlsx", "letter.docx",
		"config.json", "values.yaml", "Cargo.toml", "app.ini", "nginx.conf", "main.tf", "flake.nix",
		"schema.prisma", "api.graphql", "query.sql", "users.csv", "events.jsonl", "store.sqlite",
		"logo.png", "icon.svg", "shot.heic", "theme.psd", "talk.mp4", "tune.flac", "Inter.woff2",
		"dist.tar.gz", "pack.zst", "app.dmg", "lib.jar", "conch.exe", "module.wasm",
		"server.pem", "id_ed25519.pub", "build.log", "fix.patch", "bundle.js.map",
		"Dockerfile", "Makefile", "package.json", "go.mod", "pom.xml", "Jenkinsfile", ".prettierrc",
		".gitlab-ci.yml", ".bashrc", "poetry.lock", "bun.lockb", "CHANGELOG.md", "CONTRIBUTING.md",
	} {
		if ic := iconFor(name, false, false, false); ic == iconDefault {
			t.Errorf("%s has no icon of its own", name)
		}
	}
}
