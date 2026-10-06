package phone

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
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
	// This is the source, not what crosses the wire: the gateway deflates
	// it, and TestUIIsServedDeflated holds what a phone really downloads
	// to 48 KiB (34 KB today). This cap is still here because the source
	// is what gets read and changed, and 104 KiB is a number somebody has
	// to argue with rather than whatever the app happens to weigh.
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

// fetchWith makes a request with headers of its own, and without the
// transport quietly asking for gzip and undoing it: these tests are about
// what really crosses the wire.
func fetchWith(t *testing.T, method, url string, header http.Header) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = header
	c := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, b
}

// The app is served deflated to a phone that takes it: 101 KB of page,
// script and styles is 32 KB that way, over a tailnet that may be a phone
// on mobile data, and it is compressed once rather than per request since
// what is in ui/ cannot change while conch runs.
func TestUIIsServedDeflated(t *testing.T) {
	f := newFixture(t)
	want, err := uiFiles.ReadFile("ui/app.mjs")
	if err != nil {
		t.Fatal(err)
	}

	res, body := fetchWith(t, "GET", f.web.URL+"/app.mjs", http.Header{"Accept-Encoding": {"gzip, deflate, br"}})
	if res.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding %q", res.Header.Get("Content-Encoding"))
	}
	// Anything between here and the phone has to know the bytes depend on
	// what was asked for.
	if res.Header.Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary %q", res.Header.Get("Vary"))
	}
	if got := res.Header.Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length %q for %d bytes", got, len(body))
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") {
		t.Errorf("Content-Type %q", res.Header.Get("Content-Type"))
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("the body is not gzip: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("the deflated file is not the file")
	}
	if len(body) >= len(want)/2 {
		t.Errorf("deflating %d bytes gave %d; it is meant to be worth doing", len(want), len(body))
	}

	// Asked for plainly: the file itself, and still Vary, or a cache in
	// between would hand these bytes to somebody who asked for gzip.
	res, plain := fetchWith(t, "GET", f.web.URL+"/app.mjs", http.Header{})
	if res.Header.Get("Content-Encoding") != "" {
		t.Errorf("it compressed what was not asked for: %q", res.Header.Get("Content-Encoding"))
	}
	if res.Header.Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary %q on a plain answer", res.Header.Get("Vary"))
	}
	if !bytes.Equal(plain, want) {
		t.Error("the plain answer is not the file")
	}

	// A q of nought is a refusal, not an acceptance with a number on it.
	for _, refused := range []string{"gzip;q=0", "gzip; q=0", "br, gzip;q=0.0", "identity", ""} {
		res, body := fetchWith(t, "GET", f.web.URL+"/app.mjs", http.Header{"Accept-Encoding": {refused}})
		if res.Header.Get("Content-Encoding") != "" || !bytes.Equal(body, want) {
			t.Errorf("Accept-Encoding %q was served %q", refused, res.Header.Get("Content-Encoding"))
		}
	}
	for _, taken := range []string{"gzip", "gzip;q=0.5", "deflate, gzip", "GZIP"} {
		res, _ := fetchWith(t, "GET", f.web.URL+"/app.mjs", http.Header{"Accept-Encoding": {taken}})
		if res.Header.Get("Content-Encoding") != "gzip" {
			t.Errorf("Accept-Encoding %q was not served gzip", taken)
		}
	}

	// The rule measures rather than trusting a list of types, which is
	// how the icons came to be served smaller too: a PNG is deflated
	// already, but these ones still give a few per cent, and the work was
	// done once at start-up. What matters is that nothing is served
	// *larger* than it is, and that what comes back is the file.
	for _, name := range []string{"icon-180.png", "icon-512.png", "manifest.webmanifest"} {
		raw, err := uiFiles.ReadFile("ui/" + name)
		if err != nil {
			t.Fatal(err)
		}
		res, got := fetchWith(t, "GET", f.web.URL+"/"+name, http.Header{"Accept-Encoding": {"gzip"}})
		if len(got) > len(raw) {
			t.Errorf("%s: served %d bytes for a file of %d", name, len(got), len(raw))
		}
		if res.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(bytes.NewReader(got))
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if got, err = io.ReadAll(zr); err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(got, raw) {
			t.Errorf("%s is not itself", name)
		}
	}

	// HEAD says what a GET would send, and sends none of it.
	res, body = fetchWith(t, "HEAD", f.web.URL+"/app.mjs", http.Header{"Accept-Encoding": {"gzip"}})
	if len(body) != 0 {
		t.Errorf("HEAD sent %d bytes", len(body))
	}
	if res.Header.Get("Content-Encoding") != "gzip" || res.Header.Get("Content-Length") == "" {
		t.Errorf("HEAD headers: encoding %q length %q", res.Header.Get("Content-Encoding"), res.Header.Get("Content-Length"))
	}

	// What a phone really downloads to start the app: the page, its
	// script, its styles and the rest of the shell — not the icons, which
	// are fetched when it is put on a Home Screen. This is the number the
	// size budget in TestUIFiles is a stand-in for.
	shell, deflated, icons := 0, 0, 0
	entries, err := uiFiles.ReadDir("ui")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, _ := uiFiles.ReadFile("ui/" + e.Name())
		served := len(raw)
		if gz, ok := uiDeflated()[e.Name()]; ok {
			served = len(gz)
		}
		if path.Ext(e.Name()) == ".png" {
			icons += served
			continue
		}
		shell, deflated = shell+len(raw), deflated+served
	}
	t.Logf("the app's shell is %d bytes, %d served (%.0f%%); its icons are %d served",
		shell, deflated, 100*float64(deflated)/float64(shell), icons)
	if deflated > 48<<10 {
		t.Errorf("a phone downloads %d bytes to start the app; keep it under 48 KiB", deflated)
	}
}

// A page of the app is answered with the app, including an agent on
// another machine — the cold load of /agent/busybox:p1, which is what a
// notification tapped into a new window asks for. A bare pane left that a
// 404 for somebody who had just been told an agent was waiting.
func TestUIServesAPageForAnAgentAnywhere(t *testing.T) {
	f := newFixture(t)
	page, err := uiFiles.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/agent/busybox:p1", "/agent/busybox:p1/", "/agent/gpu-1:p12/terminal", "/agent/local:p3", "/agent/p3"} {
		res, body := fetch(t, "GET", f.web.URL+p)
		if res.StatusCode != 200 || body != string(page) {
			t.Errorf("%s: %d, %d bytes", p, res.StatusCode, len(body))
		}
	}
	// And nothing else reaches the page through the path.
	for _, p := range []string{"/agent/BUSYBOX:p1", "/agent/busy box:p1", "/agent/../p1", "/agent/a:b:p1", "/agent/busybox:", "/agent/busybox:x1", "/agent/:p1"} {
		if res, _ := fetch(t, "GET", f.web.URL+p); res.StatusCode != 404 {
			t.Errorf("%s was served %d", p, res.StatusCode)
		}
	}
}
