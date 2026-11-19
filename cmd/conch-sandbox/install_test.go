package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/conch/internal/integration"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// signedRegistry is the provider registry as its Pages site serves it: a
// format 2 index signed with a key the test knows (CONCH_PROVIDERS_KEY),
// and the files it lists. Nothing reaches the network.
type signedRegistry struct {
	mu       sync.Mutex
	files    map[string][]byte
	entries  []sandbox.RegistryEntry
	disabled []sandbox.RegistryEntry
	priv     string
	unsigned bool
	hits     map[string]int
}

func newSignedRegistry(t *testing.T) *signedRegistry {
	t.Helper()
	priv, pub, err := integration.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	r := &signedRegistry{files: map[string][]byte{}, priv: priv, hits: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		path := strings.TrimPrefix(req.URL.Path, "/")
		r.hits[path]++
		b, ok := r.files[path]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CONCH_PROVIDERS_URL", srv.URL)
	t.Setenv("CONCH_PROVIDERS_KEY", pub)
	r.write(t)
	return r
}

// publish lists body as provider name, with what the registry's CI works
// out of it; change alters the entry after, as a tampered index would.
func (r *signedRegistry) publish(t *testing.T, name, body string, disabled bool, change func(*sandbox.RegistryEntry)) {
	t.Helper()
	f, _, err := sandbox.ParseProviderFile(name, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	api, ssh := sandbox.FileHosts(f)
	sum := sha256.Sum256([]byte(body))
	e := sandbox.RegistryEntry{Name: name, Label: f.Label, APIHost: api, SSHHost: ssh, KeyEnv: f.KeyEnv, Format: 4,
		File: "providers/" + name + ".toml", SHA256: hex.EncodeToString(sum[:]), Capabilities: sandbox.FileCapabilities(f)}
	if change != nil {
		change(&e)
	}
	r.mu.Lock()
	r.files[e.File] = []byte(body)
	if disabled {
		r.disabled = append(r.disabled, e)
	} else {
		r.entries = append(r.entries, e)
	}
	r.mu.Unlock()
	r.write(t)
}

func (r *signedRegistry) write(t *testing.T) {
	t.Helper()
	type entry struct {
		Kind string `json:"kind"`
		sandbox.RegistryEntry
	}
	ix := struct {
		Format   int     `json:"format"`
		Entries  []entry `json:"entries"`
		Disabled []entry `json:"disabled"`
	}{Format: 2, Entries: []entry{}, Disabled: []entry{}}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries {
		ix.Entries = append(ix.Entries, entry{"sandbox", e})
	}
	for _, e := range r.disabled {
		ix.Disabled = append(ix.Disabled, entry{"sandbox", e})
	}
	data, _ := json.Marshal(ix)
	r.files["index.json"] = data
	delete(r.files, "index.json.sig")
	if !r.unsigned {
		sig, err := integration.Sign(r.priv, data)
		if err != nil {
			t.Fatal(err)
		}
		r.files["index.json.sig"] = []byte(sig)
	}
}

// noConchCommand fails when a message sends somebody to conch, which a
// person with only conch-sandbox does not have.
func noConchCommand(t *testing.T, s string) {
	t.Helper()
	for _, bad := range []string{"conch sandbox ", "update conch", "conch update", "Settings →"} {
		if strings.Contains(s, bad) {
			t.Errorf("says %q: %q", bad, s)
		}
	}
}

// Somebody with only conch-sandbox: the provider a tool names is fetched
// from the signed index, installed, and used, in the one command.
func TestInstallsFromTheRegistry(t *testing.T) {
	isolate(t)
	reg := newSignedRegistry(t)
	api, body := fakeProviderFile(t, "fakebox", tokenKind)
	reg.publish(t, "fakebox", body, false, nil)

	code, out, errs := run(t, "", "create", "--provider", "fakebox", "--name", "x", "--json", "--yes")
	if code != 0 || decode[result](t, out).ID != "box1" || !api.has("box1") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if !strings.Contains(errs, "installed "+sandbox.ProviderFilePath("fakebox")) || !strings.Contains(errs, "$FAKEBOX_API_KEY") {
		t.Fatalf("not said: %q", errs)
	}
	noConchCommand(t, errs)
	if b, _ := os.ReadFile(sandbox.ProviderFilePath("fakebox")); string(b) != body {
		t.Fatal("the file installed is not the registry's")
	}
	if inst, _ := sandbox.ReadInstalled(); inst["fakebox"].SHA256 == "" {
		t.Fatalf("not recorded: %+v", inst)
	}
	// Installed now: the registry is not asked again.
	reg.mu.Lock()
	before := reg.hits["index.json"]
	reg.mu.Unlock()
	if code, _, errs := run(t, "", "get", "--provider", "fakebox", "box1"); code != 0 {
		t.Fatalf("%q", errs)
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if reg.hits["index.json"] != before {
		t.Fatal("asked the registry for an installed provider")
	}
}

// What cannot be trusted is never installed, and nothing is made.
func TestRegistryRefusals(t *testing.T) {
	for _, c := range []struct {
		name     string
		setup    func(t *testing.T, r *signedRegistry, body string)
		provider string
		want     string
	}{
		{"unsigned", func(t *testing.T, r *signedRegistry, body string) {
			r.unsigned = true
			r.publish(t, "fakebox", body, false, nil)
		}, "fakebox", "is not signed"},
		{"no key known (conch's own registry today)", func(t *testing.T, r *signedRegistry, body string) {
			r.publish(t, "fakebox", body, false, nil)
			t.Setenv("CONCH_PROVIDERS_KEY", "")
		}, "fakebox", "not signed with a key conch-sandbox knows"},
		{"signed by another key", func(t *testing.T, r *signedRegistry, body string) {
			r.publish(t, "fakebox", body, false, nil)
			other, _, _ := integration.NewKey()
			r.mu.Lock()
			sig, _ := integration.Sign(other, r.files["index.json"])
			r.files["index.json.sig"] = []byte(sig)
			r.mu.Unlock()
		}, "fakebox", "signature does not match"},
		{"file changed since the index", func(t *testing.T, r *signedRegistry, body string) {
			r.publish(t, "fakebox", body, false, nil)
			r.mu.Lock()
			r.files["providers/fakebox.toml"] = []byte(body + "\n# changed\n")
			r.mu.Unlock()
		}, "fakebox", "not the one the index lists"},
		{"hosts not the index's", func(t *testing.T, r *signedRegistry, body string) {
			r.publish(t, "fakebox", body, false, func(e *sandbox.RegistryEntry) { e.APIHost = "api.elsewhere.test" })
		}, "fakebox", "not the hosts the registry's index lists"},
		{"key variable not the index's", func(t *testing.T, r *signedRegistry, body string) {
			r.publish(t, "fakebox", body, false, func(e *sandbox.RegistryEntry) { e.KeyEnv = "OTHER_KEY" })
		}, "fakebox", "not the $OTHER_KEY the registry's index lists"},
		{"too new a format", func(t *testing.T, r *signedRegistry, body string) {
			r.publish(t, "fakebox", body, false, func(e *sandbox.RegistryEntry) { e.Format = sandbox.ProviderFormat + 1 })
		}, "fakebox", "needs a newer conch-sandbox"},
		{"no ssh", func(t *testing.T, r *signedRegistry, body string) {
			r.publish(t, "fakebox", body, true, nil)
		}, "fakebox", "has no ssh way in"},
		{"not in the registry", func(t *testing.T, r *signedRegistry, body string) {
			r.publish(t, "fakebox", body, false, nil)
		}, "acme", `the provider registry has none of that name`},
	} {
		t.Run(c.name, func(t *testing.T) {
			isolate(t)
			reg := newSignedRegistry(t)
			api, body := fakeProviderFile(t, "fakebox", tokenKind)
			c.setup(t, reg, body)
			code, out, errs := run(t, "", "create", "--provider", c.provider, "--yes")
			if code != 1 || out != "" || !strings.Contains(errs, c.want) {
				t.Fatalf("%d %q %q", code, out, errs)
			}
			noConchCommand(t, errs)
			if _, err := os.Stat(sandbox.ProviderFilePath(c.provider)); err == nil {
				t.Fatal("installed")
			}
			if inst, _ := sandbox.ReadInstalled(); len(inst) != 0 {
				t.Fatalf("recorded: %+v", inst)
			}
			if api.called() != "" {
				t.Fatalf("the provider was asked:\n%s", api.called())
			}
		})
	}
}

// providers lists what the registry has beside what is here, for the
// tools' pickers: installed false, with the way in the index says.
func TestProvidersListsTheRegistry(t *testing.T) {
	isolate(t)
	reg := newSignedRegistry(t)
	_, tok := fakeProviderFile(t, "tokenbox", tokenKind)
	_, key := fakeProviderFile(t, "keybox", keyKind)
	_, off := fakeProviderFile(t, "offbox", tokenKind)
	reg.publish(t, "tokenbox", tok, false, nil)
	reg.publish(t, "keybox", key, false, nil)
	reg.publish(t, "offbox", off, true, nil)
	fakeProvider(t, "fakebox", tokenKind) // one of the person's own

	code, out, errs := run(t, "", "providers", "--json")
	if code != 0 {
		t.Fatalf("%q", errs)
	}
	got := decode[providersJSON](t, out)
	want := map[string]string{"fakebox": "true token", "keybox": "false key", "tokenbox": "false token"}
	if len(got.Providers) != len(want) || got.RegistryProblem != "" {
		t.Fatalf("%s", out)
	}
	for _, p := range got.Providers {
		if w := want[p.Name]; w != strings.Join([]string{map[bool]string{true: "true", false: "false"}[p.Installed], p.SSH}, " ") {
			t.Errorf("%s: installed %v ssh %q, want %s", p.Name, p.Installed, p.SSH, w)
		}
	}
	// Named, it is installed and listed once.
	if code, _, errs := run(t, "", "provider", "install", "keybox"); code != 0 || !strings.Contains(errs, "installed") {
		t.Fatalf("install: %d %q", code, errs)
	}
	_, out, _ = run(t, "", "providers", "--json")
	n := 0
	for _, p := range decode[providersJSON](t, out).Providers {
		if p.Name == "keybox" {
			n++
			if !p.Installed {
				t.Fatal("keybox still not installed")
			}
		}
	}
	if n != 1 {
		t.Fatalf("keybox listed %d times", n)
	}
	// The text list says so too.
	if _, out, _ := run(t, "", "providers"); !strings.Contains(out, "not installed") {
		t.Fatalf("%q", out)
	}
	// No registry to read: what is here, and why the rest is not.
	reg.mu.Lock()
	reg.unsigned = true
	reg.mu.Unlock()
	reg.write(t)
	code, out, errs = run(t, "", "providers", "--json")
	got = decode[providersJSON](t, out)
	if code != 0 || len(got.Providers) != 2 || !strings.Contains(got.RegistryProblem, "not signed") {
		t.Fatalf("%d %s %q", code, out, errs)
	}
	noConchCommand(t, errs)
}

func TestProviderInstall(t *testing.T) {
	isolate(t)
	reg := newSignedRegistry(t)
	_, body := fakeProviderFile(t, "keybox", keyKind)
	reg.publish(t, "keybox", body, false, nil)
	if code, _, errs := run(t, "", "provider", "install", "keybox"); code != 0 {
		t.Fatalf("%q", errs)
	}
	// Again: there already, and not an error.
	if code, _, errs := run(t, "", "provider", "install", "keybox"); code != 0 || !strings.Contains(errs, "installed already") {
		t.Fatalf("again: %d %q", code, errs)
	}
	// A file of the person's own is theirs.
	fakeProvider(t, "fakebox", tokenKind)
	if code, _, errs := run(t, "", "provider", "install", "fakebox"); code != 1 || !strings.Contains(errs, "a provider file of your own") {
		t.Fatalf("own: %d %q", code, errs)
	}
	for _, args := range [][]string{{"provider"}, {"provider", "install"}, {"provider", "rm", "x"}, {"provider", "install", "../x"}} {
		if code, _, _ := run(t, "", args...); code != 2 {
			t.Errorf("%v: %d", args, code)
		}
	}
}

// key set for a provider not installed fetches it first: the key is for
// its file.
func TestKeySetInstalls(t *testing.T) {
	isolate(t)
	reg := newSignedRegistry(t)
	_, body := fakeProviderFile(t, "keybox", keyKind)
	reg.publish(t, "keybox", body, false, nil)
	if code, _, errs := run(t, "k-1\n", "key", "set", "keybox"); code != 0 {
		t.Fatalf("%q", errs)
	}
	if _, err := os.Stat(sandbox.ProviderFilePath("keybox")); err != nil {
		t.Fatal(err)
	}
}
