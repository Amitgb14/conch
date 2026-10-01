package phone

import (
	"embed"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// The phone's app, served from the binary so it always matches the
// gateway: a page, its script and styles, and what makes it installable.
// There is no build step; what is in ui/ is what is served.
//
//go:embed ui
var uiFiles embed.FS

// uiTypes is every kind of file the app has. A file of another kind is not
// served: nothing gets out of the binary by being dropped into ui/.
var uiTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".webmanifest": "application/manifest+json",
}

// appPage matches the addresses that are views of the app rather than
// files: an agent (where a notification points), its terminal, the new
// task form and the settings.
var appPage = regexp.MustCompile(`^/(agent/p[0-9]+(/terminal)?/?|new/?|settings/?)$`)

// uiHandler serves the app. Anyone may load it — pairing happens on it —
// and nothing in it is secret.
func uiHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// A file is served at its own address and no other spelling of it.
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" || appPage.MatchString(r.URL.Path) {
			name = "index.html"
		}
		ctype, ok := uiTypes[path.Ext(name)]
		if !ok || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
			http.NotFound(w, r)
			return
		}
		b, err := uiFiles.ReadFile("ui/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		// Asked for again each time, so a new build's app is the one that
		// runs; the service worker keeps a copy for when the laptop is away.
		w.Header().Set("Cache-Control", "no-cache")
		if name == "sw.js" {
			w.Header().Set("Service-Worker-Allowed", "/")
		}
		if r.Method == http.MethodHead {
			return
		}
		w.Write(b)
	})
}

// safeHost is a Host header fit to repeat in a header of our own.
var safeHost = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]]{1,255}$`)

// contentPolicy lets the page load and reach this gateway and nothing
// else. The socket's address is spelled out because not every browser
// takes 'self' to cover it.
func contentPolicy(host string) string {
	connect := "connect-src 'self'"
	if safeHost.MatchString(host) {
		connect += " wss://" + host + " ws://" + host
	}
	return "default-src 'self'; " + connect + "; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
}
