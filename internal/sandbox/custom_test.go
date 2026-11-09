package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
)

// tensorlakeFile describes Tensorlake as an adapted provider would, at api.
func tensorlakeFile(api string) string {
	return `name = "tlake"
label = "Tlake"
api = "` + api + `"
key_env = "TENSORLAKE_API_KEY"
adapted_by = "claude"

[create]
call = "POST /sandboxes"
body = { name = "{name}", wait = false, image = "{snapshot}", resources = { cpus = "{cpu}", memory_mb = "{memory_mb}", disk_mb = "{disk_mb}" } }
id = "sandbox_id"

[get]
call = "GET /sandboxes/{id}"
state = "status"
name = "name"
reason = "error_details"
cpu = "resources.cpus"
memory_mb = "resources.memory_mb"
disk_mb = "resources.disk_mb"
created = "created_at"

[list]
call = "GET /sandboxes?limit=100"
items = "sandboxes"
id = "id"
next = "next_cursor"
cursor_param = "cursor"
mine = "conch-"

[start]
call = "POST /sandboxes/{id}/resume"
[stop]
call = "POST /sandboxes/{id}/suspend"
[delete]
call = "DELETE /sandboxes/{id}"

[states]
pending = "starting"
running = "started"
suspending = "stopping"
suspended = "stopped"
terminated = "gone"
resume_failed = "error"

[ssh]
host = "sandbox.tensorlake.ai"
user = "{id}"
key = "account"
`
}

// customHome makes a conch home with the file in it, and returns the path.
func customHome(t *testing.T, name, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CONCH_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "providers"), 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, "providers", name+".toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCustomProviderRunsTheLifecycle(t *testing.T) {
	f, builtin := newFakeTensorlake(t)
	customHome(t, "tlake", tensorlakeFile(builtin.base))
	if !Known("tlake") || !IsCustom("tlake") || Builtin("tlake") || ProviderLabel("tlake") != "Tlake" {
		t.Fatal("the file's provider is not known")
	}
	if got := Names(); got[len(got)-1] != "tlake" || got[0] != "daytona" {
		t.Fatalf("names %v", got)
	}
	p, err := Open("tlake", config.SandboxCfg{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := p.Create(ctx, Spec{Name: "hull", CPU: 2, Memory: 4})
	if err != nil || s.State != StateStarted || !strings.HasPrefix(s.Name, "conch-hull-") {
		t.Fatalf("create: %+v %v", s, err)
	}
	body := f.created[0]
	res := body["resources"].(map[string]any)
	// Placeholders alone take their value's type, and empty ones are left
	// out: no disk asked for, no image named.
	if body["wait"] != false || res["cpus"] != 2.0 || res["memory_mb"] != 4096.0 || res["disk_mb"] != nil || body["image"] != nil {
		t.Fatalf("body %v", body)
	}
	if f.auth[0] != "Bearer tl_secret" {
		t.Fatalf("auth %q", f.auth[0])
	}
	f.add("mine", "my-dev", "running")
	list, err := p.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != s.ID || list[0].Memory != 2 || list[0].Created.IsZero() {
		t.Fatalf("list %+v %v", list, err)
	}
	if err := p.Stop(ctx, s.ID); err != nil || f.status(s.ID) != "suspended" {
		t.Fatalf("stop: %v %s", err, f.status(s.ID))
	}
	if s2, err := p.Start(ctx, s.ID); err != nil || s2.State != StateStarted {
		t.Fatalf("start: %+v %v", s2, err)
	}
	a, err := p.SSHAccess(ctx, s.ID)
	if err != nil || a.Target() != s.ID+"@sandbox.tensorlake.ai" || !a.PlainUser {
		t.Fatalf("access %+v %v", a, err)
	}
	if ak, ok := p.(AccountKeyed); !ok || !ak.AccountKey() {
		t.Fatal("an account key is not said")
	}
	if !strings.Contains(p.(Noted).Note(), "adapted from its API docs by claude") {
		t.Fatal(p.(Noted).Note())
	}
	all, mine, err := p.(*Custom).Count(ctx)
	if err != nil || all != 2 || mine != 1 {
		t.Fatalf("count %d %d %v", all, mine, err)
	}
	if err := p.Delete(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted twice: %v", err)
	}
	if _, err := p.Get(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// Variables are refused when the file gives no way to pass them.
	if _, err := p.Create(ctx, Spec{Env: map[string]string{"A": "1"}}); err == nil || !strings.Contains(err.Error(), "{env}") {
		t.Fatalf("env: %v", err)
	}
	// A refused key names the variable; Tensorlake's words are kept.
	f.with(func() { f.fail["GET /sandboxes"], f.failMsg = http.StatusUnauthorized, "bad key" })
	if _, err := p.List(ctx); err == nil || !strings.Contains(err.Error(), "401 bad key") || !strings.Contains(err.Error(), "$TENSORLAKE_API_KEY") {
		t.Fatalf("401: %v", err)
	}
	// Without a key it is not ready, and says where the key goes.
	t.Setenv("TENSORLAKE_API_KEY", "")
	p2, _ := Open("tlake", config.SandboxCfg{})
	if err := p2.Check(); !errors.Is(err, ErrNotConfigured) || !strings.Contains(err.Error(), "Tlake") {
		t.Fatalf("check: %v", err)
	}
}

func TestProviderFileProblems(t *testing.T) {
	good := tensorlakeFile("https://api.example.test")
	path := customHome(t, "tlake", good)
	f, problems, err := ReadProviderFile(path)
	if err != nil || len(problems) != 0 {
		t.Fatalf("a good file: %v %v", problems, err)
	}
	if d := f.Describe(); len(d) != 8 || !strings.Contains(d[7].Call, "registered with the account") ||
		!strings.Contains(d[0].Call, `"{name}"`) || !strings.Contains(d[6].Call, "running→started") {
		t.Fatalf("describe %+v", d)
	}
	for _, c := range []struct{ change, want string }{
		{`name = "daytona"`, "built in"},
		{`name = "Bad Name"`, "lowercase"},
		{`api = "http://api.example.test"`, "https://"},
		{`key_env = "lower"`, "key_env"},
		{"\nauth_value = \"Token\"", "never sends {key}"},
		{"\nbogus = 1", "unknown key bogus"},
		{`call = "FETCH /sandboxes"`, "METHOD /path"},
		{`call = "GET /sandboxes/x"`, "names no {id}"},
		{`body = { name = "{name}", x = "{nope}" }`, "unknown placeholder {nope}"},
		{`body = { label = "{label}" }`, "never sends {name}"},
		{`running = "up"`, "one of starting"},
		{`host = ""`, "no ssh cannot be used"},
		{`key = "maybe"`, "account, authorize or none"},
		{`key = "authorize"`, "[ssh.authorize] call"},
		{`next = ""`, "go together"},
	} {
		body := good
		key := strings.SplitN(strings.TrimSpace(c.change), " ", 2)[0]
		switch {
		case strings.HasPrefix(c.change, "\n"):
			// a key added at the top level
			body = strings.Replace(good, "\n[create]", c.change+"\n\n[create]", 1)
		case key == "call" && strings.Contains(c.change, "FETCH"):
			body = strings.Replace(good, `call = "POST /sandboxes"`, c.change, 1)
		case key == "call":
			body = strings.Replace(good, `call = "GET /sandboxes/{id}"`, c.change, 1)
		case key == "body":
			body = strings.Replace(good, good[strings.Index(good, "body = {"):strings.Index(good, "\nid = ")], c.change, 1)
		default:
			for _, line := range strings.Split(good, "\n") {
				if strings.HasPrefix(line, key+" = ") {
					body = strings.Replace(good, line, strings.TrimSpace(c.change), 1)
					break
				}
			}
		}
		if body == good {
			t.Fatalf("%q changed nothing", c.change)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, problems, err := ReadProviderFile(path)
		if err != nil {
			t.Fatalf("%q: %v", c.change, err)
		}
		if !strings.Contains(strings.Join(problems, "\n"), c.want) {
			t.Errorf("%q: want %q in %q", c.change, c.want, problems)
		}
	}
	// Not TOML at all, an empty file, a huge one, and none.
	for body, want := range map[string]string{"name = ": "tlake.toml", "": "name", strings.Repeat("#", 300<<10): "over 256 KB"} {
		os.WriteFile(path, []byte(body), 0o600)
		_, problems, err := ReadProviderFile(path)
		got := strings.Join(problems, "\n")
		if err != nil {
			got = err.Error()
		}
		if !strings.Contains(got, want) {
			t.Errorf("%q…: want %q in %q", body[:min(len(body), 10)], want, got)
		}
	}
	if _, _, err := ReadProviderFile(filepath.Join(t.TempDir(), "none.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestCustomProvidersSkipsBadFiles(t *testing.T) {
	path := customHome(t, "tlake", tensorlakeFile("https://api.example.test"))
	dir := filepath.Dir(path)
	os.WriteFile(filepath.Join(dir, "broken.toml"), []byte("name = "), 0o600)
	os.WriteFile(filepath.Join(dir, "other.toml"), []byte(tensorlakeFile("https://api.example.test")), 0o600) // names tlake
	os.WriteFile(filepath.Join(dir, "daytona.toml"), []byte(strings.Replace(tensorlakeFile("https://a.test"), `"tlake"`, `"daytona"`, 1)), 0o600)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600)
	good, bad := CustomProviders()
	if len(good) != 1 || good[0] != "tlake" {
		t.Fatalf("good %v", good)
	}
	if len(bad) != 3 || !strings.Contains(bad["other"], `names "tlake"`) || !strings.Contains(bad["daytona"], "built in") {
		t.Fatalf("bad %v", bad)
	}
	// A built-in's name is the built-in, whatever a file says.
	if p, err := Open("daytona", config.SandboxCfg{}); err != nil || p.Name() != "daytona" {
		t.Fatal(err)
	}
	for _, name := range []string{"broken", "nope", "../etc/passwd", ""} {
		if _, err := Open(name, config.SandboxCfg{}); err == nil {
			t.Fatalf("opened %q", name)
		}
		if Known(name) {
			t.Fatalf("knows %q", name)
		}
	}
	// No folder at all: the built-ins alone.
	t.Setenv("CONCH_HOME", t.TempDir())
	if got := Names(); len(got) != len(builtin) {
		t.Fatalf("names %v", got)
	}
}

// A provider whose sshd takes a key through the API, and one whose gateway
// hands out a credential per connection.
func TestCustomProviderKeysAndAccess(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.String()+" "+string(b)+" "+r.Header.Get("X-Api-Key"))
		mu.Unlock()
		switch {
		case r.URL.Path == "/boxes/b1/token":
			json.NewEncoder(w).Encode(map[string]any{"ssh": map[string]any{"user": "tok123", "host": "gw.example.test"}})
		case r.URL.Path == "/boxes/b1":
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"state": "UP", "label": "conch-x"}})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ACME_KEY", "k1")
	file := `name = "acme"
api = "` + srv.URL + `"
key_env = "ACME_KEY"
auth_header = "X-Api-Key"
auth_value = "{key}"
[create]
call = "POST /boxes"
body = { label = "{name}" }
id = "id"
[get]
call = "GET /boxes/{id}"
state = "data.state"
name = "data.label"
[list]
call = "GET /boxes"
items = "data"
id = "id"
mine = "conch-"
[start]
call = "POST /boxes/{id}/up"
[stop]
call = "POST /boxes/{id}/down"
[delete]
call = "DELETE /boxes/{id}"
[states]
UP = "started"
[ssh]
host = "{id}.ssh.example.test"
user = "root"
port = 2222
key = "authorize"
[ssh.authorize]
call = "PUT /boxes/{id}/keys"
body = { key = "{public_key}" }
`
	customHome(t, "acme", file)
	p, err := Open("acme", config.SandboxCfg{})
	if err != nil {
		t.Fatal(err)
	}
	c := p.(*Custom)
	if !c.AuthorizesKey() || c.AccountKey() {
		t.Fatal("key mode")
	}
	ctx := context.Background()
	if err := c.AuthorizeKey(ctx, "b1", " ssh-ed25519 AAAA me \n"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got[0], `PUT /boxes/b1/keys {"key":"ssh-ed25519 AAAA me"} k1`) {
		t.Fatalf("authorize %q", got[0])
	}
	a, err := c.SSHAccess(ctx, "b1")
	if err != nil || a.Target() != "ssh://root@b1.ssh.example.test:2222" || !a.PlainUser {
		t.Fatalf("access %+v %v", a, err)
	}
	// The per-connection kind: a credential out of a reply, kept secret.
	f2 := strings.Replace(file, `host = "{id}.ssh.example.test"
user = "root"
port = 2222
key = "authorize"`, `host = "{reply.ssh.host}"
user = "{reply.ssh.user}"
key = "none"
[ssh.access]
call = "POST /boxes/{id}/token"`, 1)
	customHome(t, "acme", f2)
	t.Setenv("ACME_KEY", "k1")
	p, err = Open("acme", config.SandboxCfg{})
	if err != nil {
		t.Fatal(err)
	}
	a, err = p.SSHAccess(ctx, "b1")
	if err != nil || a.Target() != "tok123@gw.example.test" || a.PlainUser {
		t.Fatalf("access by token %+v %v", a, err)
	}
	if err := p.(*Custom).AuthorizeKey(ctx, "b1", "x"); err != nil { // nothing to hand over
		t.Fatal(err)
	}
}

func TestPick(t *testing.T) {
	var v any
	json.Unmarshal([]byte(`{"a":{"b":[{"c":"x"},{"c":2}]},"n":null}`), &v)
	for path, want := range map[string]any{"a.b.0.c": "x", "a.b.1.c": 2.0, "a.b.2.c": nil, "a.x": nil, "n": nil, "": nil, "a.b.-1": nil, "a.b.z": nil} {
		if got := Pick(v, path); got != want {
			t.Errorf("%q: %v, want %v", path, got, want)
		}
	}
}

// The guide's own example is a good file: the agent is told to copy it.
func TestProviderFileGuideExampleIsGood(t *testing.T) {
	start := strings.Index(ProviderFileGuide, "```toml\n")
	end := strings.Index(ProviderFileGuide[start+8:], "```")
	if start < 0 || end < 0 {
		t.Fatal("no example in the guide")
	}
	path := customHome(t, "acme", ProviderFileGuide[start+8:start+8+end])
	if _, problems, err := ReadProviderFile(path); err != nil || len(problems) > 0 {
		t.Fatalf("the guide's example: %v %v", problems, err)
	}
}
