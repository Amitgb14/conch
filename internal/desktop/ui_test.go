package desktop

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/fleet"
)

// TestUILogic runs the page's plain functions with node, which the CI
// runners have. Without node it can't, and says so.
func TestUILogic(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the page's JavaScript tests (uitest/) were not run")
	}
	files, _ := filepath.Glob("uitest/*.test.mjs")
	if len(files) == 0 {
		t.Fatal("no tests in uitest/")
	}
	out, err := exec.Command(node, append([]string{"--test"}, files...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// Everything the page loads is embedded: the files index.html names, and
// every module those import. A missing one is a blank window.
func TestAssetsComplete(t *testing.T) {
	assets := Assets()
	refs := regexp.MustCompile(`(?:src|href)="([^"]+)"|from "\./([^"]+)"|url\("([^"]+)"\)`)
	seen := map[string]bool{}
	todo := []string{"index.html"}
	for len(todo) > 0 {
		name := todo[0]
		todo = todo[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		b, err := fs.ReadFile(assets, name)
		if err != nil {
			t.Fatalf("%s is loaded but not embedded: %v", name, err)
		}
		for _, m := range refs.FindAllStringSubmatch(string(b), -1) {
			ref := m[1] + m[2] + m[3]
			// An anchor on the page, or a value filled in by script, is
			// not a file.
			if strings.HasPrefix(ref, "#") || strings.Contains(ref, "${") {
				continue
			}
			if strings.Contains(ref, "://") {
				t.Errorf("%s loads %s from elsewhere; the page must work offline", name, ref)
				continue
			}
			todo = append(todo, strings.TrimPrefix(ref, "./"))
		}
	}
	for _, want := range []string{"app.mjs", "app.css", "term.mjs", "ansi.mjs", "lib.mjs", "icons.mjs",
		"fonts/Geist-Variable.woff2", "fonts/GeistMono-Variable.woff2"} {
		if !seen[want] {
			t.Errorf("%s is never loaded", want)
		}
	}
	// And nothing embedded is a test: those live in uitest/.
	fs.WalkDir(assets, ".", func(path string, d fs.DirEntry, err error) error {
		if strings.Contains(path, ".test.") {
			t.Errorf("a test is served: %s", path)
		}
		return nil
	})
}

func TestHandlerServesThePageUncached(t *testing.T) {
	h := New(Options{Pool: fleet.New(fleet.Options{})})
	defer h.Close()
	srv := httptest.NewServer(h.Handler(Assets()))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `src="app.mjs"`) {
		t.Fatalf("%d %.80s", resp.StatusCode, b)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control %q", got)
	}
	// The token is never served: not as config, not anywhere.
	for _, path := range []string{"/config.json", "/token", "/socket"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(b), h.Token()) {
			t.Fatalf("%s served the token", path)
		}
	}
	// No assets: no page, the socket only.
	bare := httptest.NewServer(h.Handler(nil))
	defer bare.Close()
	if resp, err := http.Get(bare.URL + "/"); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a bare handler served a page: %v %v", resp, err)
	}
}

// surfaceVars are the page's five surfaces, from the window up to what
// sits on a panel. app.css explains the ladder.
var surfaceVars = []string{"--bg", "--side", "--panel", "--raised", "--chrome"}

// themeBlock reads one `{ … }` of app.css, the one whose selector line
// contains sel, and returns the surface colours it sets.
func themeBlock(t *testing.T, css, sel string) map[string]string {
	t.Helper()
	i := strings.Index(css, sel)
	if i < 0 {
		t.Fatalf("app.css has no %q", sel)
	}
	open := strings.Index(css[i:], "{")
	end := strings.Index(css[i:], "}")
	if open < 0 || end < 0 {
		t.Fatalf("%q is not a block", sel)
	}
	body := css[i+open+1 : i+end]
	out := map[string]string{}
	for _, name := range surfaceVars {
		m := regexp.MustCompile(regexp.QuoteMeta(name) + `:\s*(#[0-9A-Fa-f]{6})`).FindStringSubmatch(body)
		if m != nil {
			out[name] = strings.ToUpper(m[1])
		}
	}
	return out
}

// lightness is a colour's grey value, which is all these greys differ by.
func lightness(t *testing.T, hex string) int {
	t.Helper()
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		t.Fatalf("colour %q: %v", hex, err)
	}
	return (r*299 + g*587 + b*114) / 1000
}

// Each surface is a colour of its own, in both themes, and rising a rung
// means lighter. The light theme once set --bg to the same #FFFFFF as
// --raised and --side to the same #F6F6F7 as --panel, so the thread was
// the window's own white and the sidebar the cards' own grey: the window
// read as one flat white sheet, which is what somebody said it looked
// like. Nothing but this notices — the page renders, every rule applies,
// and the colours are simply the same.
func TestThemeSurfacesAreDistinct(t *testing.T) {
	b, err := fs.ReadFile(Assets(), "app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, sel := range []string{":root {", `:root[data-theme="light"]`, `:root[data-theme="system"]`} {
		got := themeBlock(t, css, sel)
		for _, name := range surfaceVars {
			if got[name] == "" {
				t.Fatalf("%s sets no %s", sel, name)
			}
		}
		seen := map[string]string{}
		for _, name := range surfaceVars {
			if was, dup := seen[got[name]]; dup {
				t.Errorf("%s: %s and %s are both %s; nothing separates them on screen", sel, was, name, got[name])
			}
			seen[got[name]] = name
		}
		// The ladder, chrome aside: a rung is lighter than the one below.
		for i := 1; i < len(surfaceVars)-1; i++ {
			lo, hi := surfaceVars[i-1], surfaceVars[i]
			if lightness(t, got[lo]) >= lightness(t, got[hi]) {
				t.Errorf("%s: %s (%s) is not darker than %s (%s), so what is raised does not look it",
					sel, lo, got[lo], hi, got[hi])
			}
		}
	}
	// The two light blocks are the same palette written twice — one for
	// the theme chosen, one for the system's. A fix to one only is half a
	// fix, and the half nobody chose is the one that ships.
	chosen, system := themeBlock(t, css, `:root[data-theme="light"]`), themeBlock(t, css, `:root[data-theme="system"]`)
	for _, name := range surfaceVars {
		if chosen[name] != system[name] {
			t.Errorf("light %s is %s when chosen and %s when the system asks for it", name, chosen[name], system[name])
		}
	}
}
