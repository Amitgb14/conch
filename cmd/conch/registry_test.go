package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/sandbox"
)

// cmdRegistry is the registry's Pages site: an index and the files it
// lists.
type cmdRegistry struct {
	mu      sync.Mutex
	entries []sandbox.RegistryEntry
	files   map[string]string
}

func newCmdRegistry(t *testing.T) *cmdRegistry {
	t.Helper()
	r := &cmdRegistry{files: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		path := strings.TrimPrefix(req.URL.Path, "/")
		if path == "index.json" {
			json.NewEncoder(w).Encode(sandbox.Index{Format: 1, Providers: r.entries})
			return
		}
		if b, ok := r.files[path]; ok {
			w.Write([]byte(b))
			return
		}
		http.NotFound(w, req)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CONCH_PROVIDERS_URL", srv.URL)
	return r
}

// publish lists body as name's file, with the hosts the file names.
func (r *cmdRegistry) publish(name, body string, change func(*sandbox.RegistryEntry)) sandbox.RegistryEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	sum := sha256.Sum256([]byte(body))
	f, _, _ := sandbox.ParseProviderFile(name, []byte(body))
	api, ssh := "", ""
	if f != nil {
		api, ssh = sandbox.FileHosts(f)
	}
	e := sandbox.RegistryEntry{Name: name, Label: "Acme", APIHost: api, SSHHost: ssh, KeyEnv: "ACME_API_KEY", Format: 1,
		File: "providers/" + name + ".toml", SHA256: hex.EncodeToString(sum[:]), AdaptedBy: "claude", Adapted: "2026-11-08",
		Docs: "https://docs.acme.test/api", Conch: "0.1.10", Note: "Acme bills by the second."}
	if change != nil {
		change(&e)
	}
	r.files[e.File] = body
	for i := range r.entries {
		if r.entries[i].Name == name {
			r.entries[i] = e
			return e
		}
	}
	r.entries = append(r.entries, e)
	return e
}

func TestProviderSearchAndInstall(t *testing.T) {
	a4Env(t)
	site := adaptSite(t)
	reg := newCmdRegistry(t)
	file := adaptFile(site.URL)
	reg.publish("acme", file, func(e *sandbox.RegistryEntry) {
		e.Checked = &sandbox.RegistryCheck{Date: "2026-11-07", Conch: "0.1.10", Sandboxes: 2}
	})
	reg.publish("future", strings.Replace(adaptFile(site.URL), `name = "acme"`, `name = "future"`, 1), func(e *sandbox.RegistryEntry) { e.Format = 9; e.Label = "Future" })

	out, _ := a4Capture(t, "", func() {
		if err := sandboxProviderCmd([]string{"search"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "acme") || !strings.Contains(out, "checked 2026-11-07") || !strings.Contains(out, "needs a newer conch") {
		t.Fatalf("search:\n%s", out)
	}
	out, _ = a4Capture(t, "", func() { sandboxProviderCmd([]string{"search", "-json", "fut"}) })
	var found []searchJSON
	if err := json.Unmarshal([]byte(out), &found); err != nil || len(found) != 1 || found[0].Usable || found[0].Here != "" {
		t.Fatalf("search -json: %s %v", out, err)
	}
	out, _ = a4Capture(t, "", func() { sandboxProviderCmd([]string{"search", "zzz"}) })
	if !strings.Contains(out, "no provider in the registry matches") {
		t.Fatal(out)
	}

	// Without -y and nobody to answer, nothing is installed — after the
	// hosts are said.
	var err error
	out, _ = a4Capture(t, "", func() { err = sandboxProviderCmd([]string{"install", "acme"}) })
	if err == nil || !strings.Contains(out, "goes to   127.0.0.1") || !strings.Contains(out, "ssh.acme.test") || !strings.Contains(out, "bills by the second") {
		t.Fatalf("asked: %v\n%s", err, out)
	}
	if _, err := os.Stat(sandbox.ProviderFilePath("acme")); !os.IsNotExist(err) {
		t.Fatal("installed without a yes")
	}
	// No key: installed, and told where the key goes.
	out, _ = a4Capture(t, "", func() { err = sandboxProviderCmd([]string{"install", "-y", "acme"}) })
	if err != nil || !strings.Contains(out, "put your key in $ACME_API_KEY") {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if !sandbox.Known("acme") {
		t.Fatal("not a provider")
	}
	out, _ = a4Capture(t, "", func() { sandboxProviderCmd([]string{"search", "-json"}) })
	if !strings.Contains(out, `"here":"installed"`) {
		t.Fatal(out)
	}
	// Twice: update is the way.
	if err := sandboxProviderCmd([]string{"install", "-y", "acme"}); err == nil || !strings.Contains(err.Error(), "provider update acme") {
		t.Fatalf("twice: %v", err)
	}
	// A file of one's own of that name is not replaced.
	os.Remove(sandbox.ProviderFilePath("acme"))
	sandbox.ForgetInstalled("acme")
	os.WriteFile(sandbox.ProviderFilePath("acme"), []byte(file), 0o600)
	if err := sandboxProviderCmd([]string{"install", "-y", "acme"}); err == nil || !strings.Contains(err.Error(), "a file of your own") {
		t.Fatalf("own: %v", err)
	}
	if err := sandboxProviderCmd([]string{"rm", "acme"}); err != nil {
		t.Fatal(err)
	}

	// With the key, the install checks it.
	t.Setenv("ACME_API_KEY", "acme-key")
	out, _ = a4Capture(t, "", func() { err = sandboxProviderCmd([]string{"install", "-y", "acme"}) })
	if err != nil || !strings.Contains(out, "2 sandboxes (1 of them conch's)") {
		t.Fatalf("checked install: %v\n%s", err, out)
	}
	// rm forgets the record, so a later install is a new one.
	if err := sandboxProviderCmd([]string{"rm", "acme"}); err != nil {
		t.Fatal(err)
	}
	if m, _ := sandbox.ReadInstalled(); len(m) != 0 {
		t.Fatalf("record kept: %v", m)
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"install", "-y", "nope"}, "no provider nope"},
		{[]string{"install", "-y", "future"}, "newer conch"},
		{[]string{"install"}, "usage"}, // nobody to pick one
		{[]string{"install", "a", "b"}, "usage"},
	} {
		if err := sandboxProviderCmd(c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v", c.args, err)
		}
	}
	// The index has to agree with the file about the hosts it shows.
	reg.publish("acme", file, func(e *sandbox.RegistryEntry) { e.APIHost = "elsewhere.test" })
	if err := sandboxProviderCmd([]string{"install", "-y", "acme"}); err == nil || !strings.Contains(err.Error(), "not the hosts the index lists") {
		t.Fatalf("hosts: %v", err)
	}
	if _, err := os.Stat(sandbox.ProviderFilePath("acme")); !os.IsNotExist(err) {
		t.Fatal("kept with the wrong hosts")
	}
	// And about the variable the key is read from: the file's is what is
	// sent, so one that differs from the index's is never kept.
	reg.publish("acme", strings.Replace(file, `key_env = "ACME_API_KEY"`, `key_env = "GITHUB_TOKEN"`, 1), nil)
	if err := sandboxProviderCmd([]string{"install", "-y", "acme"}); err == nil || !strings.Contains(err.Error(), "reads your key from $GITHUB_TOKEN, not the $ACME_API_KEY") {
		t.Fatalf("key_env: %v", err)
	}
	if _, err := os.Stat(sandbox.ProviderFilePath("acme")); !os.IsNotExist(err) {
		t.Fatal("kept with another key_env")
	}
	if m, _ := sandbox.ReadInstalled(); len(m) != 0 {
		t.Fatalf("recorded: %v", m)
	}
	// A registry that cannot be reached is said.
	t.Setenv("CONCH_PROVIDERS_URL", "http://127.0.0.1:1")
	if err := sandboxProviderCmd([]string{"search"}); err == nil || !strings.Contains(err.Error(), "provider registry") {
		t.Fatalf("unreachable: %v", err)
	}
}

func TestProviderUpdate(t *testing.T) {
	a4Env(t)
	site := adaptSite(t)
	reg := newCmdRegistry(t)
	v1 := adaptFile(site.URL)
	reg.publish("acme", v1, nil)
	if out, _ := a4Capture(t, "", func() { sandboxProviderCmd([]string{"update"}) }); !strings.Contains(out, "nothing here was installed") {
		t.Fatal(out)
	}
	if err := sandboxProviderCmd([]string{"update", "acme"}); err == nil || !strings.Contains(err.Error(), "not installed from the registry") {
		t.Fatal(err)
	}
	a4Capture(t, "", func() {
		if err := sandboxProviderCmd([]string{"install", "-y", "acme"}); err != nil {
			t.Fatal(err)
		}
	})
	update := func(args ...string) (string, error) {
		var err error
		out, _ := a4Capture(t, "", func() { err = sandboxProviderCmd(append([]string{"update"}, args...)) })
		return out, err
	}
	if out, err := update(); err != nil || !strings.Contains(out, "acme: up to date") {
		t.Fatalf("%v %s", err, out)
	}
	// A new version, same hosts: taken.
	v2 := strings.Replace(v1, `label = "Acme"`, `label = "Acme Cloud"`, 1)
	reg.publish("acme", v2, nil)
	if out, err := update("acme"); err != nil || !strings.Contains(out, "acme: updated") {
		t.Fatalf("%v %s", err, out)
	}
	if b, _ := os.ReadFile(sandbox.ProviderFilePath("acme")); string(b) != v2 {
		t.Fatal("not the new file")
	}
	// New hosts: never taken without a yes.
	v3 := strings.Replace(v2, `host = "ssh.acme.test"`, `host = "gw.elsewhere.test"`, 1)
	reg.publish("acme", v3, nil)
	for _, args := range [][]string{{"-y"}, {}} {
		out, err := update(args...)
		if err != nil || !strings.Contains(out, "now sends your API key") || !strings.Contains(out, "hosts changed: left as it is") {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	if b, _ := os.ReadFile(sandbox.ProviderFilePath("acme")); string(b) != v2 {
		t.Fatal("new hosts taken without a yes")
	}
	// The same hosts, but another of the person's variables sent as the
	// key: asked like new hosts, and never taken with -y.
	if m, _ := sandbox.ReadInstalled(); m["acme"].KeyEnv != "ACME_API_KEY" {
		t.Fatalf("key_env not recorded: %+v", m["acme"])
	}
	v4 := strings.Replace(v2, `key_env = "ACME_API_KEY"`, `key_env = "GITHUB_TOKEN"`, 1)
	reg.publish("acme", v4, func(e *sandbox.RegistryEntry) { e.KeyEnv = "GITHUB_TOKEN" })
	for _, args := range [][]string{{"-y"}, {}} {
		out, err := update(args...)
		if err != nil || !strings.Contains(out, "now sends $GITHUB_TOKEN as your API key") || !strings.Contains(out, "was $ACME_API_KEY") ||
			!strings.Contains(out, "the key it sends changed: left as it is") {
			t.Fatalf("key_env %v: %v %s", args, err, out)
		}
	}
	if b, _ := os.ReadFile(sandbox.ProviderFilePath("acme")); string(b) != v2 {
		t.Fatal("a new key_env taken without a yes")
	}
	// So is the header it goes in.
	v5 := strings.Replace(v2, `key_env = "ACME_API_KEY"`, "key_env = \"ACME_API_KEY\"\nauth_header = \"X-Api-Key\"\nauth_value = \"{key}\"", 1)
	reg.publish("acme", v5, nil)
	if out, _ := update("-y"); !strings.Contains(out, "as X-Api-Key: {key} (was $ACME_API_KEY, as Authorization: Bearer {key})") ||
		!strings.Contains(out, "the key it sends changed: left as it is") {
		t.Fatalf("auth header: %s", out)
	}
	// A record from a conch that kept no key_env: asked once, since what
	// it sent before is not known.
	m, _ := sandbox.ReadInstalled()
	old := m["acme"]
	old.KeyEnv, old.AuthHeader, old.AuthValue = "", "", ""
	raw, _ := json.Marshal(map[string]any{"acme": map[string]string{"sha256": old.SHA256, "api_host": old.APIHost, "ssh_host": old.SSHHost, "installed": old.Installed}})
	os.WriteFile(filepath.Join(sandbox.ProvidersDir(), "registry.json"), raw, 0o600)
	v6 := strings.Replace(v2, `label = "Acme Cloud"`, `label = "Acme Cloud 2"`, 1)
	reg.publish("acme", v6, nil)
	if out, _ := update("-y"); !strings.Contains(out, "was not recorded") || !strings.Contains(out, "the key it sends changed: left as it is") {
		t.Fatalf("old record: %s", out)
	}
	if b, _ := os.ReadFile(sandbox.ProviderFilePath("acme")); string(b) != v2 {
		t.Fatal("taken over an old record without a yes")
	}
	// Edited here: left alone.
	os.WriteFile(sandbox.ProviderFilePath("acme"), []byte(v2+"\n# mine\n"), 0o600)
	if out, _ := update(); !strings.Contains(out, "edited here since it was installed") {
		t.Fatal(out)
	}
	// A new file this conch cannot run is not taken over a working one.
	os.WriteFile(sandbox.ProviderFilePath("acme"), []byte(v2), 0o600)
	reg.publish("acme", strings.Replace(v2, "[ssh]", "bogus = 1\n[ssh]", 1), nil)
	if out, err := update(); err == nil || !strings.Contains(out, "cannot be used here") {
		t.Fatalf("%v %s", err, out)
	}
	// Gone from the registry: kept.
	reg.mu.Lock()
	reg.entries = nil
	reg.mu.Unlock()
	if out, _ := update(); !strings.Contains(out, "no longer in the registry; kept") {
		t.Fatal(out)
	}
	if !sandbox.Known("acme") {
		t.Fatal("dropped")
	}
}

// fakeGH answers the GitHub calls publish makes, and logs them.
func fakeGH(t *testing.T, push bool) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	p := "false"
	if push {
		p = "true"
	}
	script := `#!/bin/sh
echo "$*" >> "` + log + `"
case "$*" in *"--input -"*) cat >> "` + log + `"; echo >> "` + log + `";; esac
case "$4" in
  user) echo '{"login":"me"}';;
  repos/Amitgb14/conch-sandbox-providers) echo '{"permissions":{"push":` + p + `}}';;
  repos/Amitgb14/conch-sandbox-providers/forks) echo '{"full_name":"me/conch-sandbox-providers"}';;
  */merge-upstream) echo '{}';;
  */git/ref/heads/main) echo '{"object":{"sha":"abc123"}}';;
  */git/refs) echo '{}';;
  *"/contents/"*"?ref="*) echo 'gh: Not Found (HTTP 404)' >&2; exit 1;;
  */contents/*) echo '{}';;
  */pulls) echo '{"html_url":"https://github.com/Amitgb14/conch-sandbox-providers/pull/7"}';;
  *) echo "unexpected $*" >&2; exit 1;;
esac
`
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_GH", gh)
	return log
}

func TestProviderPublish(t *testing.T) {
	a4Env(t)
	site := adaptSite(t)
	newCmdRegistry(t)
	old := publishNow
	publishNow = func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { publishNow = old })
	file := strings.Replace(adaptFile(site.URL), `adapted_by = "claude"`, "adapted_by = \"claude\"\ndocs = \"https://docs.acme.test/api\"\nadapted = \"2026-11-01\"", 1)
	os.MkdirAll(sandbox.ProvidersDir(), 0o700)

	for _, c := range []struct {
		setup func()
		args  []string
		want  string
	}{
		{nil, []string{"publish"}, "usage"},
		{nil, []string{"publish", "daytona"}, "built into conch"},
		{nil, []string{"publish", "acme"}, "save it first"},
		{func() { os.WriteFile(sandbox.ProviderFilePath("acme"), []byte("name = \"acme\"\n"), 0o600) }, []string{"publish", "acme"}, "problems to fix first"},
		{func() {
			os.WriteFile(sandbox.ProviderFilePath("acme"), []byte(strings.Replace(file, "docs = \"https://docs.acme.test/api\"\n", "", 1)), 0o600)
		}, []string{"publish", "acme"}, "says no docs"},
		// Published files are ones that worked: no key, no proposal.
		{func() { os.WriteFile(sandbox.ProviderFilePath("acme"), []byte(file), 0o600) }, []string{"publish", "acme"}, "with your key first"},
	} {
		if c.setup != nil {
			c.setup()
		}
		if err := sandboxProviderCmd(c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.want, err)
		}
	}

	t.Setenv("ACME_API_KEY", "acme-key")
	out, _ := a4Capture(t, "", func() {
		if err := sandboxProviderCmd([]string{"publish", "-dry-run", "acme"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"would propose to Amitgb14/conch-sandbox-providers: Add Acme", "providers/acme/meta.json",
		`"checked": {`, `"sandboxes": 2`, `"date": "2026-11-08"`, `"adapted": "2026-11-01"`, "API key goes to `127.0.0.1`"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "acme-key") {
		t.Fatal("the key is in what would be published")
	}

	// Not signed in to gh: said.
	if err := sandboxProviderCmd([]string{"publish", "acme"}); err == nil || !strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("no gh: %v", err)
	}

	// Through a fork, for somebody who cannot push to the registry.
	log := fakeGH(t, false)
	out, _ = a4Capture(t, "", func() {
		if err := sandboxProviderCmd([]string{"publish", "acme"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "pull/7") {
		t.Fatal(out)
	}
	b, _ := os.ReadFile(log)
	calls := string(b)
	for _, want := range []string{
		"api --method POST repos/Amitgb14/conch-sandbox-providers/forks",
		"api --method POST repos/me/conch-sandbox-providers/merge-upstream",
		"api --method POST repos/me/conch-sandbox-providers/git/refs",
		`"ref":"refs/heads/provider-acme-20261108-120000","sha":"abc123"`,
		"api --method PUT repos/me/conch-sandbox-providers/contents/providers/acme/provider.toml",
		"api --method PUT repos/me/conch-sandbox-providers/contents/providers/acme/meta.json",
		`"head":"me:provider-acme-20261108-120000"`,
		"api --method POST repos/Amitgb14/conch-sandbox-providers/pulls",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("no %q in\n%s", want, calls)
		}
	}
	// The file goes as it is, and the key goes nowhere.
	if !strings.Contains(calls, base64.StdEncoding.EncodeToString([]byte(file))) || strings.Contains(calls, "acme-key") {
		t.Fatal("the file was changed, or the key sent")
	}

	// Straight to the registry for somebody who can push, as JSON.
	log = fakeGH(t, true)
	out, _ = a4Capture(t, "", func() {
		if err := sandboxProviderCmd([]string{"publish", "-json", "acme"}); err != nil {
			t.Fatal(err)
		}
	})
	var res map[string]string
	if json.Unmarshal([]byte(out), &res) != nil || !strings.HasSuffix(res["url"], "/pull/7") {
		t.Fatal(out)
	}
	b, _ = os.ReadFile(log)
	if calls = string(b); strings.Contains(calls, "forks") || !strings.Contains(calls, `"head":"provider-acme-20261108-120000"`) ||
		!strings.Contains(calls, "PUT repos/Amitgb14/conch-sandbox-providers/contents/") {
		t.Fatalf("pushed:\n%s", calls)
	}

	// The registry's own file, unchanged, is not proposed again.
	reg := newCmdRegistry(t)
	os.Remove(sandbox.ProviderFilePath("acme"))
	reg.publish("acme", file, nil)
	a4Capture(t, "", func() { sandboxProviderCmd([]string{"install", "-y", "acme"}) })
	if err := sandboxProviderCmd([]string{"publish", "acme"}); err == nil || !strings.Contains(err.Error(), "nothing to publish") {
		t.Fatalf("own: %v", err)
	}
}

func TestAdaptAsksTheRegistryFirst(t *testing.T) {
	a4Env(t)
	site := adaptSite(t)
	reg := newCmdRegistry(t)
	reg.publish("acme", adaptFile(site.URL), nil)
	// The URL's host is the registry entry's docs host: no agent is
	// started, and the app is told.
	var err error
	out, _ := a4Capture(t, "", func() { err = sandboxAdapt([]string{"-json", "https://docs.acme.test/api"}) })
	if err != nil || !strings.Contains(out, `"event":"registry"`) || !strings.Contains(out, `"name":"acme"`) {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(draftDir("acme")); !os.IsNotExist(err) {
		t.Fatal("a draft was started")
	}
	// On a terminal nobody answers here: the hosts are said and nothing is
	// installed or adapted.
	out, _ = a4Capture(t, "", func() { err = sandboxAdapt([]string{"-name", "acme", site.URL + "/openapi.yaml"}) })
	if err != nil || !strings.Contains(out, "in the provider registry already") || !strings.Contains(out, "goes to") {
		t.Fatalf("%v\n%s", err, out)
	}
	if sandbox.Known("acme") {
		t.Fatal("installed without a yes")
	}
	// -new adapts it all the same: it gets as far as the docs.
	out, _ = a4Capture(t, "", func() { err = sandboxAdapt([]string{"-json", "-new", "-name", "acme", site.URL + "/openapi.yaml"}) })
	if strings.Contains(out, `"event":"registry"`) || !strings.Contains(out, `"event":"docs"`) {
		t.Fatalf("-new: %v\n%s", err, out)
	}
}
