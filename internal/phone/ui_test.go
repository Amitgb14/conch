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

	"github.com/Amitgb14/conch/internal/pane"
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
	for _, p := range []string{"/", "/index.html", "/agent/p3", "/agent/p12/", "/agent/p3/terminal", "/agent/p3/terminal/", "/new", "/new/", "/settings"} {
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
		"/%2e%2e/ui.go", "/ui.go", "/app.mjs/", "/newer", "/.hidden.js", "/index.html/", "/agent/p3/terminals", "/agent/reviewer/terminal", "/terminal"} {
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
	total, images := 0, 0
	var names []string
	err := fs.WalkDir(uiFiles, "ui", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := uiFiles.ReadFile(p)
		if path.Ext(p) == ".png" {
			images += len(b)
		} else {
			total += len(b)
		}
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
	// Small enough to load fast on a phone over a tailnet: no framework,
	// no build. The PNG icons are fetched when the app is put on a Home
	// Screen, not on each visit.
	//
	// The gateway serves these as they are, so this is what crosses the
	// wire — measured at 32 KB gzipped, which nothing does for it yet.
	// The limit was 96 KiB and the machine headings in the drawer went
	// through it; it is 104 KiB now rather than whatever the app happens
	// to weigh, so it stays a budget somebody has to argue with. Trim or
	// compress before raising it again.
	if total > 104<<10 {
		t.Errorf("the app is %d bytes; keep it under 104 KiB", total)
	}
	if images > 64<<10 {
		t.Errorf("the icons are %d bytes; keep them under 64 KiB", images)
	}

	// Buttons and links are given a display of their own; without this the
	// hidden attribute stops hiding them (Settings once showed "Turn off"
	// with notifications off).
	css, _ := uiFiles.ReadFile("ui/app.css")
	if !strings.Contains(string(css), "[hidden] { display: none !important; }") {
		t.Error("app.css lets a display override the hidden attribute")
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
	have := map[string]bool{"": true, "new": true, "settings": true} // the page itself, and its views
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

	// The version the app expects is the gateway's. Written out rather
	// than built from APIVersion: the point is that both were changed.
	if !strings.Contains(string(app), "const API_VERSION = 2\n") || APIVersion != 2 {
		t.Error("app.mjs and the gateway disagree on the API version; change both")
	}
	// The app draws the machines: a tag on a row that is somewhere else,
	// and a strip for a machine that is not answering, so an empty list is
	// an empty list rather than a phone quietly missing half of them.
	for _, want := range []string{`case "machines":`, "function machineStrip", "machine-tag",
		`p.machine === "local"`, "state.machines = state.hello.machines"} {
		if !strings.Contains(string(app), want) {
			t.Errorf("app.mjs does not have %q: the machines would not show", want)
		}
	}
	style, _ := uiFiles.ReadFile("ui/app.css")
	for _, want := range []string{".machine-tag", ".machines", ".machine.offline", ".machine.connecting"} {
		if !strings.Contains(string(style), want) {
			t.Errorf("app.css has no %s", want)
		}
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
		if _, ok := socketNeeds[m[1]]; !ok && m[1] != "button" && m[1] != "checkbox" {
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
	for _, args := range [][]string{
		{"--test", "uitest/lib.test.mjs", "uitest/sw.test.mjs"},
		// The rest has no tests of its own; it must at least parse.
		{"--check", "ui/app.mjs"},
		{"--check", "ui/sw.js"},
	} {
		cmd := exec.Command(node, args...)
		cmd.Env = append(os.Environ(), "NODE_OPTIONS=", "NO_COLOR=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("node %v: %v\n%s", args, err, out)
		}
	}
}

// Every key on the terminal view's bar is a key the server knows by that
// name: a bar key it didn't would come back as an error, not a keystroke.
func TestKeyBarNamesAreKeys(t *testing.T) {
	lib, _ := uiFiles.ReadFile("ui/lib.mjs")
	bar := regexp.MustCompile(`(?s)export const TERMINAL_KEYS = \[(.*?)\n\]`).FindSubmatch(lib)
	if bar == nil {
		t.Fatal("no TERMINAL_KEYS in lib.mjs")
	}
	names := regexp.MustCompile(`key: "([^"]+)"`).FindAllSubmatch(bar[1], -1)
	if len(names) < 8 {
		t.Fatalf("only %d keys on the bar", len(names))
	}
	for _, m := range names {
		if _, err := pane.ParseKey(string(m[1])); err != nil {
			t.Errorf("the bar's %q: %v", m[1], err)
		}
	}
}
