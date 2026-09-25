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
		{name: ".bashrc", want: "  "}, // a dotfile's name is not an extension
		{name: ".", want: "  "},
		{name: "..", want: "  "},
		{name: "", want: "  "},
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
