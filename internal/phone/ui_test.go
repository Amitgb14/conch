package phone

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"testing"
)

func fetch(t *testing.T, method, url string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

// The app is served to anyone — pairing happens on it — with each file's
// own type, and the app's views all answer with the one page.
func TestUIIsServed(t *testing.T) {
	f := newFixture(t)
	page, _ := uiFiles.ReadFile("ui/index.html")
	for _, p := range []string{"/", "/index.html", "/agent/p3", "/agent/p12/", "/new", "/new/"} {
		res, body := fetch(t, "GET", f.web.URL+p)
		if res.StatusCode != 200 || body != string(page) || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
			t.Errorf("%s: %d %s", p, res.StatusCode, res.Header.Get("Content-Type"))
		}
	}
	for p, ctype := range map[string]string{
		"/app.mjs": "text/javascript", "/lib.mjs": "text/javascript", "/sw.js": "text/javascript",
		"/app.css": "text/css", "/manifest.webmanifest": "application/manifest+json",
		"/icon.svg": "image/svg+xml", "/icon-180.png": "image/png", "/icon-512.png": "image/png",
	} {
		res, body := fetch(t, "GET", f.web.URL+p)
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), ctype) || body == "" {
			t.Errorf("%s: %d %q", p, res.StatusCode, res.Header.Get("Content-Type"))
		}
		if res.Header.Get("Cache-Control") != "no-cache" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: headers %v", p, res.Header)
		}
	}
	// What isn't the app isn't there — nor is anything reached by walking
	// out of it.
	for _, p := range []string{"/agent/", "/agent/reviewer", "/agent/p3/x", "/nope.js", "/ui/index.html", "/../ui.go",
		"/%2e%2e/ui.go", "/ui.go", "/app.mjs/", "/newer", "/.hidden.js", "/index.html/"} {
		if res, body := fetch(t, "GET", f.web.URL+p); res.StatusCode == 200 {
			t.Errorf("%s: served %.60q", p, body)
		}
	}
	if res, body := fetch(t, "HEAD", f.web.URL+"/app.mjs"); res.StatusCode != 200 || body != "" {
		t.Errorf("HEAD: %d %q", res.StatusCode, body)
	}
	if res, _ := fetch(t, "POST", f.web.URL+"/"); res.StatusCode != 405 {
		t.Errorf("POST /: %d", res.StatusCode)
	}

	// The page may load and reach this gateway, and nothing else.
	res, _ := fetch(t, "GET", f.web.URL+"/")
	host := strings.TrimPrefix(f.web.URL, "http://")
	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "connect-src 'self' wss://" + host + " ws://" + host, "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe") {
		t.Errorf("CSP %q", csp)
	}
}

// A Host that isn't one is not repeated into the policy.
func TestContentPolicyHost(t *testing.T) {
	for host, in := range map[string]bool{
		"laptop.tail1234.ts.net": true, "100.101.102.103:8722": true, "[fd7a:115c:a1e0::53]:8722": true,
		"": false, "evil.example; script-src *": false, "a b": false, "x'y": false, strings.Repeat("a", 300): false,
	} {
		if got := strings.Contains(contentPolicy(host), "wss://"); got != in {
			t.Errorf("host %q: in the policy = %v", host, got)
		}
		if p := contentPolicy(host); strings.Contains(p, "script-src") || strings.Count(p, ";") != 4 {
			t.Errorf("host %q: %q", host, p)
		}
	}
}

// The app is self-contained, as its policy demands: no inline script or
// style, nothing loaded from elsewhere, nothing written as markup. And it
// stays small enough to load fast on a phone.
func TestUIFiles(t *testing.T) {
	total := 0
	var names []string
	err := fs.WalkDir(uiFiles, "ui", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := uiFiles.ReadFile(p)
		total += len(b)
		names = append(names, strings.TrimPrefix(p, "ui/"))
		if _, ok := uiTypes[path.Ext(p)]; !ok || strings.Count(p, "/") != 1 {
			t.Errorf("%s would not be served", p)
		}
		if path.Ext(p) == ".png" {
			return nil
		}
		text := string(b)
		for _, bad := range []string{"http://", "//cdn", "innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function"} {
			if strings.Contains(strings.ReplaceAll(text, "http://www.w3.org/2000/svg", ""), bad) {
				t.Errorf("%s contains %q", p, bad)
			}
		}
		if strings.Contains(text, "https://") {
			t.Errorf("%s names another site", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total > 64<<10 {
		t.Errorf("the app is %d bytes; keep it under 64 KiB", total)
	}

	page, _ := uiFiles.ReadFile("ui/index.html")
	if regexp.MustCompile(`(?i)<script(?:\s+type="module")?\s*>|<style|\son[a-z]+=|style="`).Match(page) {
		t.Error("index.html has inline script or style, which its policy blocks")
	}
	// Every file the page and the service worker name is there, by an
	// address from the root — the page is also served at /agent/p3.
	refs := regexp.MustCompile(`(?:href|src)="([^"]+)"`).FindAllStringSubmatch(string(page), -1)
	sw, _ := uiFiles.ReadFile("ui/sw.js")
	shell := regexp.MustCompile(`const SHELL = \[([^\]]*)\]`).FindString(string(sw))
	for _, m := range regexp.MustCompile(`"(/[a-z0-9.\-]*)"`).FindAllStringSubmatch(shell, -1) {
		refs = append(refs, m)
	}
	app, _ := uiFiles.ReadFile("ui/app.mjs")
	for _, m := range regexp.MustCompile(`from "([^"]+)"`).FindAllStringSubmatch(string(app), -1) {
		refs = append(refs, m)
	}
	if len(refs) < 12 {
		t.Fatalf("only %d references found", len(refs))
	}
	have := map[string]bool{"": true, "new": true} // the page itself, and its views
	for _, n := range names {
		have[n] = true
	}
	for _, m := range refs {
		if !strings.HasPrefix(m[1], "/") || !have[strings.TrimPrefix(m[1], "/")] {
			t.Errorf("reference %q: not a file of the app, from the root", m[1])
		}
	}

	// The service worker keeps the app's files and never an API answer.
	if !strings.Contains(string(sw), `url.pathname.startsWith("/api/") || url.pathname === "/pair"`) {
		t.Error("sw.js no longer steps aside for /api and /pair")
	}
	if strings.Contains(shell, "/api") || strings.Contains(shell, "/pair") {
		t.Errorf("sw.js lists an API answer to keep: %s", shell)
	}

	var manifest struct {
		Name, Display string
		StartURL      string `json:"start_url"`
		Icons         []struct{ Src, Sizes, Type string }
	}
	b, _ := uiFiles.ReadFile("ui/manifest.webmanifest")
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "conch" || manifest.Display != "standalone" || manifest.StartURL != "/" || len(manifest.Icons) < 2 {
		t.Errorf("manifest %+v", manifest)
	}
	for _, icon := range manifest.Icons {
		if !have[strings.TrimPrefix(icon.Src, "/")] {
			t.Errorf("manifest icon %s is missing", icon.Src)
		}
	}

	// The version the app expects is the gateway's.
	if !strings.Contains(string(app), "const API_VERSION = 1\n") || APIVersion != 1 {
		t.Error("app.mjs and the gateway disagree on the API version; change both")
	}
	// Every route the app calls is one the gateway has.
	served := map[string]bool{"POST /pair": true}
	for _, rt := range routes {
		served[rt.method+" "+rt.path] = true
	}
	calls := regexp.MustCompile(`api\("([A-Z]+)", "([^"]+)"`).FindAllStringSubmatch(string(app), -1)
	if len(calls) < 5 {
		t.Fatalf("only %d api calls found in app.mjs", len(calls))
	}
	for _, m := range calls {
		if !served[m[1]+" "+m[2]] {
			t.Errorf("app.mjs calls %s %s, which the gateway doesn't serve", m[1], m[2])
		}
	}
	for _, m := range regexp.MustCompile(`type: "([a-z.]+)"`).FindAllStringSubmatch(string(app), -1) {
		if _, ok := socketNeeds[m[1]]; !ok && m[1] != "button" {
			t.Errorf("app.mjs sends %q, which isn't a socket message", m[1])
		}
	}
}

// The app's logic — reading a frame's styling, ordering the list, routes —
// is tested with node, which the CI runners have. Without node it can't
// be, and says so.
func TestUILogic(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the app's JavaScript tests (uitest/) were not run")
	}
	cmd := exec.Command(node, "--test", "uitest/lib.test.mjs")
	cmd.Env = append(os.Environ(), "NODE_OPTIONS=", "NO_COLOR=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test: %v\n%s", err, out)
	}
}
