package phone

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// cssScheme reads the custom properties of one :root block of app.css:
// the first is the light scheme, the one inside the dark media query the
// dark one.
func cssScheme(t *testing.T, css string, dark bool) map[string]string {
	t.Helper()
	at := strings.Index(css, ":root {")
	if dark {
		m := strings.Index(css, "@media (prefers-color-scheme: dark)")
		if m < 0 {
			t.Fatal("app.css has no dark scheme")
		}
		at = m + strings.Index(css[m:], ":root {")
	}
	if at < 0 {
		t.Fatal("app.css has no :root")
	}
	block := css[at : at+strings.Index(css[at:], "}")]
	vars := map[string]string{}
	for _, m := range regexp.MustCompile(`--([a-z0-9-]+):\s*([^;]+);`).FindAllStringSubmatch(block, -1) {
		vars[m[1]] = strings.ToLower(strings.TrimSpace(m[2]))
	}
	return vars
}

func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	if len(hex) != 7 || hex[0] != '#' {
		t.Fatalf("%q is not a #rrggbb colour", hex)
	}
	var l [3]float64
	for i := range l {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			t.Fatal(err)
		}
		c := float64(v) / 255
		if c <= 0.03928 {
			l[i] = c / 12.92
		} else {
			l[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*l[0] + 0.7152*l[1] + 0.0722*l[2]
}

func contrast(t *testing.T, a, b string) float64 {
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// The phone app wears the look of the Conch iOS design: near-black and
// off-white neutrals, one orange accent, amber for an agent that needs
// you, a bold sans for titles where there was a serif, and drawn icons in
// the tab bar where there were text glyphs. Every pair of colours that
// carries text reads at 4.5:1 in both schemes.
func TestUIStyleFollowsTheDesign(t *testing.T) {
	b, _ := uiFiles.ReadFile("ui/app.css")
	css := string(b)
	light, dark := cssScheme(t, css, false), cssScheme(t, css, true)

	for name, want := range map[string][2]string{
		"bg":    {"#f6f6f4", "#0d0e10"},
		"card":  {"#ffffff", "#17181b"},
		"fg":    {"#141416", "#f2f2f3"},
		"quiet": {"#5e6068", "#a1a3a9"},
		// Light takes the design's text accent for fills too: white on its
		// #b9561b button reads 3.9:1.
		"accent":  {"#a84c14", "#f2a66b"},
		"waiting": {"#8a5a00", "#f5b544"},
		"working": {"#1f5fcc", "#8db9ff"},
		"done":    {"#1c7a43", "#5bd08a"},
		"bad":     {"#b3261e", "#ff8a7a"},
	} {
		if light[name] != want[0] || dark[name] != want[1] {
			t.Errorf("--%s is %s / %s, the design's is %s / %s", name, light[name], dark[name], want[0], want[1])
		}
	}

	for _, s := range []struct {
		name string
		v    map[string]string
	}{{"light", light}, {"dark", dark}} {
		for _, p := range [][2]string{
			{"fg", "bg"}, {"fg", "card"}, {"quiet", "bg"}, {"quiet", "card"},
			{"on-accent", "accent"}, {"accent-text", "bg"}, {"accent-text", "card"},
			{"waiting", "bg"}, {"waiting", "waiting-bg"}, {"working", "bg"}, {"done", "bg"}, {"bad", "bg"},
			{"screen-fg", "screen"},
		} {
			if s.v[p[0]] == "" || s.v[p[1]] == "" {
				t.Errorf("%s: --%s or --%s is missing", s.name, p[0], p[1])
				continue
			}
			if c := contrast(t, s.v[p[0]], s.v[p[1]]); c < 4.5 {
				t.Errorf("%s: --%s on --%s is %.2f:1, under 4.5", s.name, p[0], p[1], c)
			}
		}
	}

	if strings.Contains(css, "var(--serif)") {
		t.Error("app.css still sets titles in a serif")
	}
	if !strings.Contains(css, "Inter") || !strings.Contains(css, "JetBrains Mono") {
		t.Error("app.css does not name the design's faces first")
	}

	page, _ := uiFiles.ReadFile("ui/index.html")
	for _, m := range []struct{ scheme, want string }{{"light", light["bg"]}, {"dark", dark["bg"]}} {
		meta := `<meta name="theme-color" content="` + m.want + `" media="(prefers-color-scheme: ` + m.scheme + `)">`
		if !strings.Contains(strings.ToLower(string(page)), meta) {
			t.Errorf("index.html has no %s", meta)
		}
	}
	tabs := string(page)[strings.Index(string(page), `<nav id="tabs"`):]
	tabs = tabs[:strings.Index(tabs, "</nav>")]
	if n := strings.Count(tabs, "<svg"); n != 4 {
		t.Errorf("the tab bar draws %d icons, want 4", n)
	}
	if strings.Contains(tabs, "<i ") {
		t.Error("the tab bar still uses text glyphs for icons")
	}

	var manifest struct {
		Background string `json:"background_color"`
		Theme      string `json:"theme_color"`
	}
	mb, _ := uiFiles.ReadFile("ui/manifest.webmanifest")
	if err := json.Unmarshal(mb, &manifest); err != nil {
		t.Fatal(err)
	}
	if strings.ToLower(manifest.Background) != light["bg"] || strings.ToLower(manifest.Theme) != light["bg"] {
		t.Errorf("manifest colours %s / %s, want the page's %s", manifest.Background, manifest.Theme, light["bg"])
	}
}
