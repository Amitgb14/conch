package sandbox

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
)

// keyHeaderFile is tensorlakeFile sending the key in a header of its own,
// which Go keeps across a redirect to another host as it does not keep
// Authorization.
func keyHeaderFile(api string) string {
	return strings.Replace(tensorlakeFile(api), `key_env = "TENSORLAKE_API_KEY"`,
		"key_env = \"TENSORLAKE_API_KEY\"\nauth_header = \"X-API-Key\"\nauth_value = \"{key}\"", 1)
}

// A redirect away from the API's host and port is not followed with the
// key; one on the same host still is.
func TestCustomRedirectKeepsTheKeyHome(t *testing.T) {
	var mu sync.Mutex
	var leaked []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		leaked = append(leaked, r.Header.Get("X-API-Key")+"|"+r.Header.Get("Authorization"))
		mu.Unlock()
		fmt.Fprint(w, `{"id":"x","status":"running","name":"conch-x"}`)
	}))
	defer other.Close()
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "k-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/sandboxes/away":
			http.Redirect(w, r, other.URL+"/sandboxes/away", http.StatusTemporaryRedirect)
		case "/sandboxes/near":
			http.Redirect(w, r, api.URL+"/sandboxes/here", http.StatusTemporaryRedirect)
		case "/sandboxes/loop":
			http.Redirect(w, r, "/sandboxes/loop", http.StatusFound)
		case "/sandboxes/here":
			fmt.Fprint(w, `{"id":"here","status":"running","name":"conch-here"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	t.Setenv("TENSORLAKE_API_KEY", "k-secret")
	customHome(t, "tlake", keyHeaderFile(api.URL))
	p, err := Open("tlake", config.SandboxCfg{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.Get(ctxFor(t), "away")
	if err == nil || !strings.Contains(err.Error(), "redirected to") || !strings.Contains(err.Error(), "not followed with the key") {
		t.Fatalf("a redirect to another host: %v", err)
	}
	if strings.Contains(err.Error(), "k-secret") {
		t.Fatalf("the error carries the key: %v", err)
	}
	mu.Lock()
	if len(leaked) != 0 {
		t.Fatalf("the other host was reached: %q", leaked)
	}
	mu.Unlock()

	s, err := p.Get(ctxFor(t), "near")
	if err != nil || s.Name != "conch-here" {
		t.Fatalf("a redirect on the same host: %+v %v", s, err)
	}
	if _, err := p.Get(ctxFor(t), "loop"); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Fatalf("a redirect loop: %v", err)
	}
}

// The variables the built-ins read are still read for their files:
// DAYTONA_API_URL is how a self-hosted Daytona was reached, and without it
// the key went to the cloud. config.toml still wins over them.
func TestFormerBuiltinsReadTheirVariables(t *testing.T) {
	daytona, err := os.ReadFile(filepath.Join("testdata", "registry", "daytona.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	hits := map[string]int{}
	serve := func(name string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			hits[name+" "+r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")]++
			mu.Unlock()
			fmt.Fprint(w, `{"id":"x","state":"started","name":"conch-x"}`)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	self, cfgd := serve("self"), serve("config")
	customHome(t, "daytona", string(daytona))
	t.Setenv("DAYTONA_API_KEY", "k")
	t.Setenv("DAYTONA_API_URL", self.URL+"/api")
	t.Setenv("DAYTONA_TARGET", "eu")

	p, err := Open("daytona", config.SandboxCfg{})
	if err != nil {
		t.Fatal(err)
	}
	if c := p.(*Custom); c.region != "eu" {
		t.Fatalf("DAYTONA_TARGET: region %q", c.region)
	}
	if _, err := p.Get(ctxFor(t), "x"); err != nil {
		t.Fatal(err)
	}
	if hits["self GET /api/sandbox/x Bearer k"] != 1 {
		t.Fatalf("DAYTONA_API_URL was not where the call went: %v", hits)
	}

	// config.toml wins, as it did: the variable only fills what it leaves.
	p, err = Open("daytona", config.SandboxCfg{"daytona": {APIURL: cfgd.URL + "/api", Target: "us"}})
	if err != nil {
		t.Fatal(err)
	}
	if c := p.(*Custom); c.region != "us" {
		t.Fatalf("config's target over DAYTONA_TARGET: %q", c.region)
	}
	if _, err := p.Get(ctxFor(t), "x"); err != nil || hits["config GET /api/sandbox/x Bearer k"] != 1 {
		t.Fatalf("config's api_url over DAYTONA_API_URL: %v %v", err, hits)
	}
	p, _ = Open("daytona", config.SandboxCfg{"daytona": {Region: "us"}})
	if c := p.(*Custom); c.region != "us" {
		t.Fatalf("config's region over DAYTONA_TARGET: %q", c.region)
	}

	// Set to nothing, or blank, is not set: the file's own address.
	t.Setenv("DAYTONA_API_URL", "  ")
	t.Setenv("DAYTONA_TARGET", "")
	p, _ = Open("daytona", config.SandboxCfg{})
	if c := p.(*Custom); c.File().API != "https://app.daytona.io/api" || c.region != "" {
		t.Fatalf("blank variables: %q %q", c.File().API, c.region)
	}

	// Tensorlake's address and its ssh gateway, boat.dev's address.
	tl, _ := os.ReadFile(filepath.Join("testdata", "registry", "tensorlake.toml"))
	customHome(t, "tensorlake", string(tl))
	t.Setenv("TENSORLAKE_API_URL", "http://127.0.0.1:1/tl")
	t.Setenv("TENSORLAKE_SSH_HOST", "gw.self.test")
	p, err = Open("tensorlake", config.SandboxCfg{})
	if err != nil {
		t.Fatal(err)
	}
	if f := p.(*Custom).File(); f.API != "http://127.0.0.1:1/tl" || f.SSH.Host != "gw.self.test" {
		t.Fatalf("tensorlake: %q %q", f.API, f.SSH.Host)
	}
	boat, _ := os.ReadFile(filepath.Join("testdata", "registry", "boat.toml"))
	customHome(t, "boat", string(boat))
	t.Setenv("BOAT_API_URL", "http://127.0.0.1:1/boat")
	p, err = Open("boat", config.SandboxCfg{})
	if err != nil {
		t.Fatal(err)
	}
	if f := p.(*Custom).File(); f.API != "http://127.0.0.1:1/boat" {
		t.Fatalf("boat: %q", f.API)
	}

	// A provider conch never had reads none of them.
	customHome(t, "tlake", tensorlakeFile("https://api.example.test"))
	p, err = Open("tlake", config.SandboxCfg{})
	if err != nil {
		t.Fatal(err)
	}
	if f := p.(*Custom).File(); f.API != "https://api.example.test" || f.SSH.Host != "sandbox.tensorlake.ai" {
		t.Fatalf("another provider took a former one's variables: %q %q", f.API, f.SSH.Host)
	}
}

// Two former built-ins opened at once are both adopted, each once, and
// both kept in the record: a read-modify-write each without a lock lost
// one, and update no longer knew it came from the registry.
func TestFormerBuiltinsAdoptedAtOnce(t *testing.T) {
	for round := 0; round < 5; round++ {
		t.Setenv("CONCH_HOME", t.TempDir())
		r := newFakeRegistry(t)
		names := []string{"daytona", "tensorlake", "boat"}
		for _, n := range names {
			data, err := os.ReadFile(filepath.Join("testdata", "registry", n+".toml"))
			if err != nil {
				t.Fatal(err)
			}
			r.publish(n, string(data))
		}
		var wg sync.WaitGroup
		errs := make(chan error, 4*len(names))
		for i := 0; i < 4; i++ {
			for _, n := range names {
				wg.Add(1)
				go func(n string) {
					defer wg.Done()
					if _, err := Open(n, config.SandboxCfg{}); err != nil {
						errs <- fmt.Errorf("%s: %w", n, err)
					}
				}(n)
			}
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		inst, err := ReadInstalled()
		if err != nil {
			t.Fatal(err)
		}
		r.mu.Lock()
		for _, n := range names {
			if _, ok := inst[n]; !ok {
				t.Errorf("round %d: %s's record was lost: %v", round, n, inst)
			}
			if got := r.hits["providers/"+n+".toml"]; got != 1 {
				t.Errorf("round %d: %s's file fetched %d times", round, n, got)
			}
		}
		r.mu.Unlock()
	}
}

// A file in the registry is installed unasked only when it sends the key
// from the same variable to the same host the built-in did, and only when
// it is the file the index lists.
func TestAdoptRefusesAnotherHostOrKey(t *testing.T) {
	daytona, err := os.ReadFile(filepath.Join("testdata", "registry", "daytona.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, body, want string
	}{
		{"another host", strings.Replace(string(daytona), "https://app.daytona.io/api", "https://evil.example.test/api", 1), "evil.example.test"},
		{"a subdomain", strings.Replace(string(daytona), "https://app.daytona.io/api", "https://app.daytona.io.evil.test/api", 1), "app.daytona.io.evil.test"},
		{"another key", strings.Replace(string(daytona), `key_env = "DAYTONA_API_KEY"`, `key_env = "OPENAI_API_KEY"`, 1), "$OPENAI_API_KEY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CONCH_HOME", t.TempDir())
			r := newFakeRegistry(t)
			r.publish("daytona", tc.body)
			_, err := Open("daytona", config.SandboxCfg{})
			if err == nil || !strings.Contains(err.Error(), "not installed unasked") || !strings.Contains(err.Error(), tc.want) ||
				!strings.Contains(err.Error(), "conch sandbox provider install daytona") {
				t.Fatalf("opened: %v", err)
			}
			if !fileMissing("daytona") {
				t.Fatal("the file was installed")
			}
			if inst, _ := ReadInstalled(); len(inst) != 0 {
				t.Fatalf("recorded: %v", inst)
			}
		})
	}
	t.Run("hash mismatch", func(t *testing.T) {
		t.Setenv("CONCH_HOME", t.TempDir())
		r := newFakeRegistry(t)
		e := r.publish("daytona", string(daytona))
		r.mu.Lock()
		r.files[e.File] = append([]byte("# changed after the index\n"), daytona...)
		r.mu.Unlock()
		_, err := Open("daytona", config.SandboxCfg{})
		if err == nil || !strings.Contains(err.Error(), "hash differs") {
			t.Fatalf("opened: %v", err)
		}
		if !fileMissing("daytona") {
			t.Fatal("the file was installed")
		}
	})
	// The same file as it should be is installed: what the refusals refuse
	// is the difference, not the test's registry.
	t.Run("as the built-in did", func(t *testing.T) {
		t.Setenv("CONCH_HOME", t.TempDir())
		newFakeRegistry(t).publish("daytona", string(daytona))
		if _, err := Open("daytona", config.SandboxCfg{}); err != nil || fileMissing("daytona") {
			t.Fatalf("opened: %v", err)
		}
	})
}

// No test reads the developer's own keys or addresses for a provider.
func TestNoProviderVariablesInTests(t *testing.T) {
	for _, k := range testUnset {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("$%s is %q in a test", k, v)
		}
	}
}

// Installs at once each keep their record: without the lock most of them
// were written over by another's.
func TestInstallsAtOnceKeepEveryRecord(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	const n = 24
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if _, err := InstallProviderFile(name, []byte(registryFile(name))); err != nil {
				errs <- err
			}
		}(fmt.Sprintf("p%02d", i))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	inst, err := ReadInstalled()
	if err != nil || len(inst) != n {
		t.Fatalf("%d of %d records kept: %v", len(inst), n, err)
	}
	// And forgetting half at once while capabilities are kept for the rest.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("p%02d", i)
			if i%2 == 0 {
				_ = ForgetInstalled(name)
			} else {
				_ = KeepCapabilities(name, nil)
			}
		}(i)
	}
	wg.Wait()
	if inst, _ = ReadInstalled(); len(inst) != n/2 {
		t.Fatalf("%d records after forgetting half of %d", len(inst), n)
	}
}
