package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// Every test starts with a CONCH_HOME and HOME of its own, so none reads
// the developer's keys, provider files or ~/.ssh, and no provider's key
// from the developer's environment is sent to a fake.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "conch-sandbox-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("CONCH_HOME", filepath.Join(home, "conch"))
	for _, k := range []string{"CONCH_SOCKET", "CONCH_PANE_ID", "CONCH_PROVIDERS_URL", "FAKEBOX_API_KEY",
		"DAYTONA_API_KEY", "BOAT_API_KEY", "TENSORLAKE_API_KEY", "INSTABOX_API_KEY"} {
		os.Unsetenv(k)
	}
	os.Setenv("CONCH_SSH_KEYGEN", "/bin/false")
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// isolate gives a test a fresh home and conch home, whose ~/.ssh/config
// already includes the aliases — somebody who said yes once.
func isolate(t *testing.T) string {
	t.Helper()
	home := bare(t)
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(includeLine()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// bare is isolate with no ~/.ssh at all.
func bare(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CONCH_HOME", filepath.Join(home, "conch"))
	t.Setenv("FAKEBOX_API_KEY", "")
	t.Setenv("CONCH_SSH_KEYGEN", "/bin/false")
	sandbox.PollFaster(t, 10*time.Millisecond)
	return home
}

// userKey puts a key of the person's own in ~/.ssh, so nothing runs
// ssh-keygen.
func userKey(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".ssh")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "id_ed25519"), []byte("private"), 0o600)
	os.WriteFile(filepath.Join(dir, "id_ed25519.pub"), []byte("ssh-ed25519 AAAAmine me@here\n"), 0o600)
	return filepath.Join(dir, "id_ed25519")
}

// fakeAPI is a provider's API: sandboxes that change state at once.
type fakeAPI struct {
	mu      sync.Mutex
	boxes   map[string]map[string]any
	calls   []string
	bodies  map[string]string
	n       int
	keys    []string // public keys handed over
	failOn  map[string]int
	created string // the state a new box reports
	tokens  int    // tokens handed out, so each is new
	token   string // what the next token is, when set
}

func (f *fakeAPI) called() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, "\n")
}

func (f *fakeAPI) has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.boxes[id]
	return ok
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	call := r.Method + " " + r.URL.Path
	f.calls = append(f.calls, call)
	b, _ := json.Marshal(body)
	f.bodies[call] = string(b)
	if r.Header.Get("Authorization") != "Bearer k-test" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	kind := r.Method + " " + parts[0]
	if len(parts) > 2 {
		kind += " " + parts[2]
	}
	if code := f.failOn[kind]; code != 0 {
		w.WriteHeader(code)
		fmt.Fprint(w, `{"message":"no"}`)
		return
	}
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	if r.Method == http.MethodPost && r.URL.Path == "/boxes" {
		f.n++
		id := fmt.Sprintf("box%d", f.n)
		state := "running"
		if f.created != "" {
			state = f.created
		}
		f.boxes[id] = map[string]any{"id": id, "name": body["name"], "state": state, "cpu": 2, "mem": 4, "disk": 10, "region": body["region"]}
		reply(f.boxes[id])
		return
	}
	if len(parts) < 2 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	box, ok := f.boxes[parts[1]]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"not found"}`)
		return
	}
	switch {
	case r.Method == http.MethodGet:
		reply(box)
	case r.Method == http.MethodDelete:
		delete(f.boxes, parts[1])
	case parts[len(parts)-1] == "start":
		box["state"] = "running"
	case parts[len(parts)-1] == "stop":
		box["state"] = "stopped"
	case parts[len(parts)-1] == "ssh":
		f.tokens++
		tok := fmt.Sprintf("tok-secret-%s-%d", parts[1], f.tokens)
		if f.token != "" {
			tok = f.token
		}
		reply(map[string]any{"token": tok, "expires": "2026-11-20T12:00:00Z"})
	case parts[len(parts)-1] == "keys":
		f.keys = append(f.keys, fmt.Sprint(body["key"]))
	}
}

// providerKind is which way in a fake provider file says.
type providerKind int

const (
	tokenKind providerKind = iota // a token from [ssh.access], like Daytona
	keyKind                       // a key handed over, like Hetzner
	labelKind                     // a token, and the name sent as {label}, as Daytona's file does
)

// fakeProvider installs a provider file NAME.toml for a fake API, and its key.
func fakeProvider(t *testing.T, name string, kind providerKind) *fakeAPI {
	t.Helper()
	f := &fakeAPI{boxes: map[string]map[string]any{}, bodies: map[string]string{}, failOn: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("FAKEBOX_API_KEY", "k-test")
	nameField, allMine := `name = "{name}"`, ""
	if kind == labelKind {
		nameField, allMine = `name = "{label}"`, "all_mine = true"
	}
	ssh := ""
	switch kind {
	case tokenKind, labelKind:
		ssh = `
[ssh]
host = "gw.test"
user = "{reply.token}"
key = "none"
expires = "{reply.expires}"
[ssh.access]
call = "POST /boxes/{id}/ssh"
`
	case keyKind:
		ssh = `
[ssh]
host = "10.0.0.9"
user = "root"
port = 2222
key = "authorize"
[ssh.authorize]
call = "POST /boxes/{id}/keys"
body = { key = "{public_key}" }
`
	}
	file := fmt.Sprintf(`format = 4
name = %q
label = "Fake Box"
api = %q
key_env = "FAKEBOX_API_KEY"

[create]
call = "POST /boxes"
body = { %s, region = "{region}", size = "{size}", image = "{image}", labels = { conch = "1" } }
id = "id"

[get]
call = "GET /boxes/{id}"
state = "state"
name = "name"
cpu = "cpu"
memory_gib = "mem"
disk_gib = "disk"

[list]
call = "GET /boxes"
items = "items"
id = "id"
mine = "conch-"
%s

[start]
call = "POST /boxes/{id}/start"
[stop]
call = "POST /boxes/{id}/stop"
[delete]
call = "DELETE /boxes/{id}"

[states]
running = "started"
pending = "starting"
stopped = "stopped"
failed = "error"

[size]
names = ["small", "large"]
default = "small"

[[setting]]
key = "region"
label = "Region"
choices = ["us", "eu"]

[[setting]]
key = "image"
label = "Image"
choices_call = "GET /images"
items = "items"
value = "name"
free = true
%s`, name, srv.URL, nameField, allMine, ssh)
	if err := os.MkdirAll(sandbox.ProvidersDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sandbox.ProviderFilePath(name), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, problems, err := sandbox.ReadProviderFile(sandbox.ProviderFilePath(name)); err != nil || len(problems) > 0 {
		t.Fatalf("the fake's file: %v %v", err, problems)
	}
	return f
}

// run runs conch-sandbox with stdin, returning its status, stdout and stderr.
func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	a := &app{stdin: strings.NewReader(stdin), stdout: &out, stderr: &errb, wait: 30 * time.Second,
		readSecret: func() (string, error) { return "", fmt.Errorf("not a terminal") }}
	code := a.run(context.Background(), args)
	return code, out.String(), errb.String()
}

// decode reads the one JSON object a command printed.
func decode[T any](t *testing.T, out string) T {
	t.Helper()
	var v T
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("more than one line on stdout:\n%s", out)
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return v
}
