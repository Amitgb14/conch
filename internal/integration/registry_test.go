package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
)

// fakeRegistry serves an index of files, signed with priv unless it is
// "", from memory the test may change.
type fakeRegistry struct {
	mu    sync.Mutex
	srv   *httptest.Server
	files map[string][]byte // by path
	priv  string
	index Index
}

func newFakeRegistry(t *testing.T, priv string) *fakeRegistry {
	t.Helper()
	fr := &fakeRegistry{files: map[string][]byte{}, priv: priv, index: Index{Format: 2}}
	fr.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fr.mu.Lock()
		defer fr.mu.Unlock()
		data, ok := fr.files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(fr.srv.Close)
	return fr
}

// publish puts a file in the registry at version v, and the index again.
func (fr *fakeRegistry) publish(t *testing.T, src string, v int, hosts Hosts, revoked ...Revocation) Entry {
	t.Helper()
	f, problems, err := Parse([]byte(src))
	if err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	sum := sha256.Sum256([]byte(src))
	e := Entry{Kind: f.Kind, Name: f.Name, Label: f.Label, Version: v, Format: 3, File: "integrations/" + f.Name + ".toml",
		SHA256: hex.EncodeToString(sum[:]), Hosts: hosts, Operations: f.Operations(), Tier: "community", Revoked: revoked}
	fr.mu.Lock()
	defer fr.mu.Unlock()
	fr.files[e.File] = []byte(src)
	var kept []Entry
	for _, x := range fr.index.Entries {
		if x.Name != e.Name {
			kept = append(kept, x)
		}
	}
	fr.index.Entries = append(kept, e)
	fr.writeIndexLocked(t)
	return e
}

func (fr *fakeRegistry) writeIndexLocked(t *testing.T) {
	data, _ := json.Marshal(fr.index)
	fr.files["index.json"] = data
	delete(fr.files, "index.json.sig")
	if fr.priv != "" {
		sig, err := Sign(fr.priv, data)
		if err != nil {
			t.Fatal(err)
		}
		fr.files["index.json.sig"] = []byte(sig)
	}
}

var acmeHosts = Hosts{API: "api.acme.dev", Follow: []string{"logs.acme.dev"}}

func TestRegistryInstallUpdateRemove(t *testing.T) {
	isolate(t)
	priv, pub, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	fr := newFakeRegistry(t, priv)
	fr.publish(t, good, 1, acmeHosts)
	r := Registry{Name: "test", URL: fr.srv.URL, Key: pub}
	ctx := context.Background()
	ix, err := FetchIndex(ctx, r)
	if err != nil || !ix.Signed || len(ix.Entries) != 1 {
		t.Fatalf("index %+v %v", ix, err)
	}
	e, _ := ix.Find("acme-ci")
	if e.Registry != "test" || len(ix.Search("ci")) != 1 || len(ix.Search("nothing")) != 0 {
		t.Fatalf("entry %+v", e)
	}
	data, f, err := FetchFile(ctx, r, e)
	if err != nil || f.Name != "acme-ci" {
		t.Fatal(err)
	}
	if err := Install(e, data); err != nil {
		t.Fatal(err)
	}
	all, bad := All()
	if len(bad) > 0 || !hasFile(all, "acme-ci") {
		t.Fatalf("after install: %v %v", names(all), bad)
	}
	recs, _ := Records()
	if rec := recs["acme-ci"]; rec.Version != 1 || rec.Registry != "test" || rec.Hosts.API != "api.acme.dev" {
		t.Fatalf("record %+v", rec)
	}

	// Version 2 reaches a new host: the person is asked again.
	v2 := strings.Replace(good, `["logs.acme.dev"]`, `["logs.acme.dev", "cdn.acme.dev"]`, 1)
	e2 := fr.publish(t, v2, 2, Hosts{API: "api.acme.dev", Follow: []string{"logs.acme.dev", "cdn.acme.dev"}})
	if !HostsChanged(recs["acme-ci"], e2) {
		t.Fatal("a new host was not noticed")
	}
	if HostsChanged(recs["acme-ci"], Entry{Hosts: acmeHosts}) {
		t.Fatal("the same hosts read as changed")
	}
	data2, _, err := FetchFile(ctx, r, e2)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(e2, data2); err != nil {
		t.Fatal(err)
	}

	// Edited here: an update leaves it alone.
	path := filepath.Join(Dir(), "acme-ci.toml")
	os.WriteFile(path, append(data2, []byte("\n# mine\n")...), 0o600)
	if edited, _ := Edited("acme-ci"); !edited {
		t.Fatal("an edit not noticed")
	}
	if err := Install(e2, data2); err == nil || !strings.Contains(err.Error(), "edited here") {
		t.Fatalf("installed over an edit: %v", err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "# mine") {
		t.Fatal("the edit is gone")
	}

	if err := Remove("acme-ci"); err != nil {
		t.Fatal(err)
	}
	if err := Remove("acme-ci"); err == nil {
		t.Fatal("removed twice")
	}
	if err := Remove("../x"); err == nil {
		t.Fatal("a path for a name")
	}
	recs, _ = Records()
	if _, ok := recs["acme-ci"]; ok {
		t.Fatal("record kept after remove")
	}
}

func TestInstallOverAFileOfYourOwn(t *testing.T) {
	home := isolate(t)
	os.MkdirAll(filepath.Join(home, "integrations"), 0o755)
	os.WriteFile(filepath.Join(home, "integrations", "acme-ci.toml"), []byte(good), 0o600)
	if err := Install(Entry{Name: "acme-ci", Version: 1}, []byte(good)); err == nil || !strings.Contains(err.Error(), "of your own") {
		t.Fatalf("got %v", err)
	}
}

func TestRegistrySignatures(t *testing.T) {
	isolate(t)
	priv, pub, _ := NewKey()
	_, other, _ := NewKey()
	ctx := context.Background()

	signed := newFakeRegistry(t, priv)
	signed.publish(t, good, 1, acmeHosts)
	if _, err := FetchIndex(ctx, Registry{Name: "r", URL: signed.srv.URL, Key: other}); err == nil || !strings.Contains(err.Error(), "signature does not match") {
		t.Fatalf("another key: %v", err)
	}
	if _, err := FetchIndex(ctx, Registry{Name: "r", URL: signed.srv.URL, Key: "not base64!"}); err == nil {
		t.Fatal("a broken key accepted")
	}
	// The index changed after it was signed.
	signed.mu.Lock()
	signed.files["index.json"] = append(signed.files["index.json"], ' ')
	signed.mu.Unlock()
	if _, err := FetchIndex(ctx, Registry{Name: "r", URL: signed.srv.URL, Key: pub}); err == nil {
		t.Fatal("a changed index accepted")
	}

	unsigned := newFakeRegistry(t, "")
	unsigned.publish(t, good, 1, acmeHosts)
	if _, err := FetchIndex(ctx, Registry{Name: "r", URL: unsigned.srv.URL, Key: pub}); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Fatalf("unsigned with a key known: %v", err)
	}
	// With no key known — conch's own registry until its CI signs — it is
	// read, and said to be unsigned.
	ix, err := FetchIndex(ctx, Registry{Name: "r", URL: unsigned.srv.URL})
	if err != nil || ix.Signed {
		t.Fatalf("unsigned, no key: %+v %v", ix, err)
	}
	if _, err := Sign("short", nil); err == nil {
		t.Fatal("a bad private key signed")
	}
}

func TestFetchFileRefusals(t *testing.T) {
	isolate(t)
	fr := newFakeRegistry(t, "")
	r := Registry{Name: "r", URL: fr.srv.URL}
	ctx := context.Background()
	e := fr.publish(t, good, 1, acmeHosts)
	for name, tc := range map[string]struct {
		change func(*Entry)
		want   string
	}{
		"hash":         {func(e *Entry) { e.SHA256 = strings.Repeat("0", 64) }, "its hash differs"},
		"hosts":        {func(e *Entry) { e.Hosts = Hosts{API: "api.acme.dev"} }, "logs.acme.dev, which the index does not show"},
		"kind":         {func(e *Entry) { e.Kind = "tickets" }, `kind "tickets"`},
		"sandbox":      {func(e *Entry) { e.Kind = KindSandbox }, "conch sandbox provider install"},
		"newer format": {func(e *Entry) { e.Format = 9 }, "format 9"},
		"outside":      {func(e *Entry) { e.File = "../secrets" }, "lists acme-ci's file at"},
		"another name": {func(e *Entry) { e.Name = "other-ci" }, "not what the index lists"},
		"missing file": {func(e *Entry) { e.File = "integrations/gone.toml" }, "404"},
	} {
		x := e
		tc.change(&x)
		if _, _, err := FetchFile(ctx, r, x); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := strings.Replace(good, `format = 3`, `format = 2`, 1)
	e = fr.publish(t, good, 1, acmeHosts)
	fr.mu.Lock()
	fr.files[e.File] = []byte(bad)
	fr.mu.Unlock()
	sum := sha256.Sum256([]byte(bad))
	e.SHA256 = hex.EncodeToString(sum[:])
	if _, _, err := FetchFile(ctx, r, e); err == nil || !strings.Contains(err.Error(), "cannot be used here") {
		t.Errorf("a file with problems: %v", err)
	}
}

func TestRevokedVersionIsLeftOut(t *testing.T) {
	isolate(t)
	fr := newFakeRegistry(t, "")
	r := Registry{Name: "r", URL: fr.srv.URL}
	ctx := context.Background()
	e := fr.publish(t, good, 1, acmeHosts)
	data, _, _ := FetchFile(ctx, r, e)
	e.Registry = "r"
	if err := Install(e, data); err != nil {
		t.Fatal(err)
	}
	fr.publish(t, good, 1, acmeHosts, Revocation{Version: 1, Reason: "sent logs to the wrong host"})
	ix, _ := FetchIndex(ctx, r)
	newly, err := MarkRevoked(ix)
	if err != nil || len(newly) != 1 || newly[0] != "acme-ci" {
		t.Fatalf("revoked %v %v", newly, err)
	}
	all, bad := All()
	if hasFile(all, "acme-ci") || !strings.Contains(strings.Join(bad, "\n"), "revoked: sent logs to the wrong host") {
		t.Fatalf("a revoked file used: %v %v", names(all), bad)
	}
	if newly, _ := MarkRevoked(ix); len(newly) != 0 {
		t.Fatal("revoked twice")
	}
	// The registry takes the revocation back.
	fr.publish(t, good, 1, acmeHosts)
	ix, _ = FetchIndex(ctx, r)
	MarkRevoked(ix)
	if all, _ := All(); !hasFile(all, "acme-ci") {
		t.Fatal("not back after the revocation went")
	}
	// Another registry's index says nothing about this one's files.
	other := &Index{Registry: "other", Entries: []Entry{{Name: "acme-ci", Revoked: []Revocation{{Version: 1, Reason: "x"}}}}}
	if newly, _ := MarkRevoked(other); len(newly) != 0 {
		t.Fatal("revoked by another registry")
	}
}

func TestIndexFormats(t *testing.T) {
	// Today's sandbox index reads as entries of kind sandbox.
	ix, err := ParseIndex([]byte(`{"format": 1, "providers": [
	  {"name": "acme", "label": "Acme", "api_host": "api.acme.dev", "ssh_host": "ssh.acme.dev", "format": 2, "file": "providers/acme.toml", "sha256": "ab", "checked": {"date": "2026-11-01", "conch": "0.2.0"}},
	  {"name": "../bad", "file": "x"}
	]}`))
	if err != nil || len(ix.Entries) != 1 {
		t.Fatalf("format 1: %+v %v", ix, err)
	}
	if e := ix.Entries[0]; e.Kind != KindSandbox || e.Hosts.SSH != "ssh.acme.dev" || e.Tier != "verified" {
		t.Fatalf("entry %+v", e)
	}
	if _, err := ParseIndex([]byte(`{"format": 3}`)); err == nil || !strings.Contains(err.Error(), "update conch") {
		t.Fatalf("a newer index: %v", err)
	}
	if _, err := ParseIndex([]byte(`{"format": 2, "entries": [`)); err == nil {
		t.Fatal("a truncated index")
	}
	ix, err = ParseIndex([]byte(`{"format": 2, "entries": [{"kind": "notify", "name": "slack", "version": 3}]}`))
	if err != nil || ix.Entries[0].Kind != "notify" {
		t.Fatalf("a kind this conch does not read is still listed: %+v %v", ix, err)
	}
}

func TestRegistries(t *testing.T) {
	t.Setenv("CONCH_PROVIDERS_URL", "")
	rs := Registries(config.IntegrationsCfg{Registries: []config.RegistryCfg{{Name: "acme", URL: "https://acme.dev/reg/", Key: "k"}, {Name: "empty"}}})
	if len(rs) != 2 || rs[0].URL != registryURL || rs[1].URL != "https://acme.dev/reg" || rs[1].Key != "k" {
		t.Fatalf("registries %+v", rs)
	}
	t.Setenv("CONCH_PROVIDERS_URL", "http://127.0.0.1:1/")
	t.Setenv("CONCH_PROVIDERS_KEY", "pub")
	if rs := Registries(config.IntegrationsCfg{}); rs[0].URL != "http://127.0.0.1:1" || rs[0].Key != "pub" {
		t.Fatalf("overridden %+v", rs)
	}
	if _, err := FetchIndex(context.Background(), Registry{Name: "x", URL: "http://example.com"}); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("plain http: %v", err)
	}
}

func hasFile(files []*File, name string) bool {
	for _, f := range files {
		if f.Name == name {
			return true
		}
	}
	return false
}

func names(files []*File) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Name)
	}
	return out
}
