package desktop

import (
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
