package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakeRegistry serves an index and files as the registry's Pages site does.
type fakeRegistry struct {
	mu    sync.Mutex
	files map[string][]byte // path → body
	index Index
	raw   []byte // served as index.json instead, when set
	hits  map[string]int
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	r := &fakeRegistry{files: map[string][]byte{}, index: Index{Format: 1}, hits: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		path := strings.TrimPrefix(req.URL.Path, "/")
		r.hits[path]++
		if path == "index.json" {
			if r.raw != nil {
				w.Write(r.raw)
				return
			}
			json.NewEncoder(w).Encode(r.index)
			return
		}
		b, ok := r.files[path]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CONCH_PROVIDERS_URL", srv.URL+"/")
	return r
}

// publish puts a file in the registry, listed as the index lists it.
func (r *fakeRegistry) publish(name, body string) RegistryEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	sum := sha256.Sum256([]byte(body))
	e := RegistryEntry{Name: name, Label: strings.ToUpper(name[:1]) + name[1:], API: "https://api.example.test", APIHost: "api.example.test",
		SSHHost: "sandbox.tensorlake.ai", KeyEnv: "TENSORLAKE_API_KEY", Format: 1, File: "providers/" + name + ".toml",
		SHA256: hex.EncodeToString(sum[:]), Docs: "https://docs." + name + ".test/api"}
	r.files[e.File] = []byte(body)
	for i := range r.index.Providers {
		if r.index.Providers[i].Name == name {
			r.index.Providers[i] = e
			return e
		}
	}
	r.index.Providers = append(r.index.Providers, e)
	return e
}

func registryFile(name string) string {
	return strings.Replace(tensorlakeFile("https://api.example.test"), `name = "tlake"`, `name = "`+name+`"`, 1)
}

func TestRegistryIndex(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	r := newFakeRegistry(t)
	r.publish("zeta", registryFile("zeta"))
	r.publish("acme", registryFile("acme"))
	ctx := context.Background()
	ix, err := FetchIndex(ctx)
	if err != nil || len(ix.Providers) != 2 || ix.Providers[0].Name != "acme" {
		t.Fatalf("index %+v %v", ix, err)
	}
	if got := ix.Search(""); len(got) != 2 {
		t.Fatal("an empty search is every one")
	}
	if got := ix.Search(" ZET "); len(got) != 1 || got[0].Name != "zeta" {
		t.Fatalf("search by label, any case: %+v", got)
	}
	if got := ix.Search("api.example"); len(got) != 2 {
		t.Fatal("search by host")
	}
	if got := ix.Search("nothing"); len(got) != 0 {
		t.Fatal(got)
	}
	if e, ok := ix.ForHost("docs.zeta.test"); !ok || e.Name != "zeta" {
		t.Fatal("found by its docs' host")
	}
	if e, ok := ix.ForHost("API.EXAMPLE.TEST"); !ok || e.Name != "acme" {
		t.Fatal("found by its API's host")
	}
	if _, ok := ix.ForHost(""); ok {
		t.Fatal("no host is no provider")
	}
	if _, ok := ix.Find("nope"); ok {
		t.Fatal("find")
	}

	for _, c := range []struct {
		raw  string
		want string
	}{
		{`{"format": 2, "providers": []}`, "update conch"},
		{`{`, "index"},
		{strings.Repeat(" ", maxIndex+1), "over"},
	} {
		r.mu.Lock()
		r.raw = []byte(c.raw)
		r.mu.Unlock()
		if _, err := FetchIndex(ctx); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%.20q: %v", c.raw, err)
		}
	}
	r.mu.Lock()
	r.raw = []byte(`{"format": 1}`)
	r.mu.Unlock()
	if ix, err := FetchIndex(ctx); err != nil || len(ix.Providers) != 0 {
		t.Fatalf("an empty registry: %+v %v", ix, err)
	}

	// Only https, or http on this computer.
	for _, u := range []string{"http://registry.example.test", "ftp://x", "::"} {
		t.Setenv("CONCH_PROVIDERS_URL", u)
		if _, err := FetchIndex(ctx); err == nil || !strings.Contains(err.Error(), "https://") {
			t.Errorf("%s: %v", u, err)
		}
	}
	t.Setenv("CONCH_PROVIDERS_URL", "http://127.0.0.1:1")
	if _, err := FetchIndex(ctx); err == nil || !strings.Contains(err.Error(), "provider registry") {
		t.Fatalf("unreachable: %v", err)
	}
	t.Setenv("CONCH_PROVIDERS_URL", "")
	if RegistryURL() != registryURL {
		t.Fatal("the default registry")
	}
}

func TestRegistryFileAndInstall(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	r := newFakeRegistry(t)
	ctx := context.Background()
	e := r.publish("acme", registryFile("acme"))

	data, err := FetchProviderFile(ctx, e)
	if err != nil || string(data) != registryFile("acme") {
		t.Fatalf("fetch: %v", err)
	}
	// The file has to be the one the index lists.
	bad := e
	bad.SHA256 = strings.Repeat("0", 64)
	if _, err := FetchProviderFile(ctx, bad); err == nil || !strings.Contains(err.Error(), "hash differs") {
		t.Fatalf("hash: %v", err)
	}
	newer := e
	newer.Format = ProviderFormat + 1
	if _, err := FetchProviderFile(ctx, newer); err == nil || !strings.Contains(err.Error(), "update conch") {
		t.Fatalf("too new: %v", err)
	}
	for _, file := range []string{"../etc/passwd", "providers/../../x", "index.json"} {
		odd := e
		odd.File = file
		if _, err := FetchProviderFile(ctx, odd); err == nil {
			t.Errorf("%s was fetched", file)
		}
	}
	gone := e
	gone.File = "providers/gone.toml"
	if _, err := FetchProviderFile(ctx, gone); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing: %v", err)
	}

	// Nothing installed yet.
	if m, err := ReadInstalled(); err != nil || len(m) != 0 {
		t.Fatalf("installed %v %v", m, err)
	}
	f, err := InstallProviderFile("acme", data)
	if err != nil || f.Name != "acme" {
		t.Fatalf("install: %v", err)
	}
	if !Known("acme") || !IsCustom("acme") {
		t.Fatal("an installed provider is a provider")
	}
	m, _ := ReadInstalled()
	in := m["acme"]
	if in.SHA256 != e.SHA256 || in.APIHost != "api.example.test" || in.SSHHost != "sandbox.tensorlake.ai" || in.Installed == "" {
		t.Fatalf("record %+v", in)
	}
	if st, _ := os.Stat(ProviderFilePath("acme")); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	if edited, err := EditedSinceInstall("acme", in); err != nil || edited {
		t.Fatal("as it came")
	}
	os.WriteFile(ProviderFilePath("acme"), append(data, '\n'), 0o600)
	if edited, _ := EditedSinceInstall("acme", in); !edited {
		t.Fatal("an edit is seen")
	}
	os.Remove(ProviderFilePath("acme"))
	if edited, _ := EditedSinceInstall("acme", in); edited {
		t.Fatal("a removed file is not an edit")
	}
	if err := ForgetInstalled("acme"); err != nil {
		t.Fatal(err)
	}
	if err := ForgetInstalled("acme"); err != nil {
		t.Fatal("forgetting twice")
	}
	if m, _ := ReadInstalled(); len(m) != 0 {
		t.Fatal(m)
	}

	// Refused: a file conch cannot run, a name it has built in, a file
	// that names another provider.
	for _, c := range []struct{ name, body, want string }{
		{"acme", "name = \"acme\"\n", "cannot be used here"},
		{"daytona", registryFile("daytona"), "built into conch"},
		{"other", registryFile("acme"), `not "other"`},
		{"acme", "not toml = = =", "acme"},
	} {
		if _, err := InstallProviderFile(c.name, []byte(c.body)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.want, err)
		}
	}
	if _, err := os.Stat(ProviderFilePath("acme")); !os.IsNotExist(err) {
		t.Fatal("a refused file was kept")
	}

	// A record that cannot be read is said, not taken as empty.
	os.WriteFile(installedPath(), []byte("{"), 0o600)
	if _, err := ReadInstalled(); err == nil {
		t.Fatal("a broken record")
	}
	if _, err := InstallProviderFile("acme", data); err == nil {
		t.Fatal("installed over a broken record")
	}
	// The record is not a provider file.
	os.Remove(installedPath())
	InstallProviderFile("acme", data)
	if good, bad := CustomProviders(); len(good) != 1 || len(bad) != 0 {
		t.Fatalf("providers %v %v", good, bad)
	}
}
