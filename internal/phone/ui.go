package phone

import (
	"bytes"
	"compress/gzip"
	"embed"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
//
// An agent on another machine is `machine:pane`, so its page is
// /agent/busybox:p1 — and this is the cold load of it: a notification
// tapped into a new window, or the address opened by hand. Matching a
// bare pane left those a 404 served to somebody who had just been told
// an agent was waiting.
var appPage = regexp.MustCompile(`^/(agent/([a-z0-9-]+:)?p[0-9]+(/terminal)?/?|new/?|settings/?)$`)

// The app is served gzipped where the phone takes it, which is every
// browser that runs this app. There is no build step and what is in ui/
// never changes while conch runs, so each file is compressed once, on
// first use, rather than per request: the page and its script and styles
// are 101 KB as they are and 32 KB deflated, over a tailnet that may be
// a phone on mobile data.
//
// A PNG is already compressed and gzips larger, so it is left alone —
// which the rule below decides by measuring rather than by trusting a
// list of types.
var uiDeflated = sync.OnceValue(func() map[string][]byte {
	out := map[string][]byte{}
	entries, err := uiFiles.ReadDir("ui")
	if err != nil {
		return out
	}
	for _, e := range entries {
		b, err := uiFiles.ReadFile("ui/" + e.Name())
		if err != nil {
			continue
		}
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			continue
		}
		if _, err := zw.Write(b); err != nil || zw.Close() != nil {
			continue
		}
		// Only when it is actually smaller: a file that grew would cost
		// the phone the deflating as well as the bytes.
		if buf.Len() < len(b) {
			out[e.Name()] = buf.Bytes()
		}
	}
	return out
})

// takesGzip reports whether the client asked for gzip. A q of 0 is a
// refusal — "gzip;q=0" means anything but gzip — and identity is not
// something this has to decide, since the plain file is the fallback.
func takesGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), "gzip") {
			continue
		}
		for _, p := range fields[1:] {
			if k, v, ok := strings.Cut(strings.TrimSpace(p), "="); ok && strings.EqualFold(k, "q") {
				if q, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && q == 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}

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
		// Said whether or not this answer is compressed: anything between
		// here and the phone has to know the bytes depend on it.
		w.Header().Set("Vary", "Accept-Encoding")
		if gz, ok := uiDeflated()[name]; ok && takesGzip(r) {
			w.Header().Set("Content-Encoding", "gzip")
			b = gz
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
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
