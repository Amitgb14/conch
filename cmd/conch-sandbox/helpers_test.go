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
	// Nothing here reaches a real ssh, herdr or hc, or reads a real
	// tool's context: a test that wants one installs a stand-in.
	t.Setenv("CONCH_SSH", "/nonexistent/ssh")
	t.Setenv("HERDR_BIN_PATH", "/nonexistent/herdr")
	t.Setenv("HC_BIN_PATH", "/nonexistent/hc")
	t.Setenv("PATH", "/usr/bin:/bin")
	for _, k := range []string{"HERDR_PLUGIN_CONTEXT_JSON", "ORCA_WORKSPACE_NAME", "ORCA_REPO_URL", "ORCA_REPO_BRANCH",
		"ORCA_REPO_REF", "ORCA_VM_INSTANCE_ID", "GH_TOKEN"} {
		t.Setenv(k, "")
	}
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
	if r.Method == http.MethodGet && r.URL.Path == "/boxes" {
		items := []any{}
		for _, b := range f.boxes {
			items = append(items, b)
		}
		reply(map[string]any{"items": items})
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
	noSSHKind                     // no [ssh] at all, which no conch will open
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
	if _, problems, err := sandbox.ReadProviderFile(sandbox.ProviderFilePath(name)); err != nil || (len(problems) > 0) != (kind == noSSHKind) {
		t.Fatalf("the fake's file: %v %v", err, problems)
	}
	return f
}

// run runs conch-sandbox with stdin, returning its status, stdout and stderr.
func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	a := &app{stdin: strings.NewReader(stdin), stdout: &out, stderr: &errb, wait: 30 * time.Second, retry: time.Millisecond,
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

// sshStandIn is ssh into a sandbox, here: it runs the script it is given
// with sh, in a home of its own, with a git that only writes down what it
// was asked. FAIL_TIMES connections fail first as ssh's do (255).
type sshStandIn struct {
	home, gitLog, sshLog, dir string
}

func fakeSSH(t *testing.T) *sshStandIn {
	t.Helper()
	dir := t.TempDir()
	f := &sshStandIn{home: filepath.Join(dir, "home"), gitLog: filepath.Join(dir, "git.log"), sshLog: filepath.Join(dir, "ssh.log"), dir: dir}
	os.MkdirAll(f.home, 0o755)
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "git"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "`+f.gitLog+`"
[ -n "$FAKE_GIT_FAIL" ] && { echo "fatal: repository not found" >&2; exit 128; }
case " $* " in
*" clone "*) for a in "$@"; do last=$a; done; mkdir -p "$last/.git" ;;
esac
exit 0
`), 0o755)
	os.WriteFile(filepath.Join(dir, "ssh"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "`+f.sshLog+`"
n=$(cat "`+dir+`/fails" 2>/dev/null || echo 0)
if [ "$n" -gt 0 ]; then echo $((n-1)) > "`+dir+`/fails"; echo "ssh: connect to host gw.test port 22: Connection refused $FAKE_SSH_SAYS" >&2; exit 255; fi
[ -n "$FAKE_SSH_DOWN" ] && { echo "ssh: connect to host $FAKE_SSH_SAYS port 22: Operation timed out" >&2; exit 255; }
[ -n "$FAKE_SSH_DENIED" ] && { echo "$FAKE_SSH_SAYS: Permission denied (publickey)." >&2; exit 255; }
HOME="`+f.home+`" PATH="`+bin+`:/usr/bin:/bin" exec sh -s
`), 0o755)
	t.Setenv("CONCH_SSH", filepath.Join(dir, "ssh"))
	return f
}

// failFirst makes the next n connections fail.
func (f *sshStandIn) failFirst(n int) {
	os.WriteFile(filepath.Join(f.dir, "fails"), []byte(fmt.Sprint(n)), 0o600)
}

func (f *sshStandIn) gitCalls() string { b, _ := os.ReadFile(f.gitLog); return string(b) }
func (f *sshStandIn) sshCalls() string { b, _ := os.ReadFile(f.sshLog); return string(b) }

// herdrStandIn is herdr's command line: machine list, add and remove over
// a JSON file, and every call written down.
type herdrStandIn struct{ state, log string }

func fakeHerdr(t *testing.T) *herdrStandIn {
	t.Helper()
	dir := t.TempDir()
	f := &herdrStandIn{state: filepath.Join(dir, "machines.json"), log: filepath.Join(dir, "herdr.log")}
	os.WriteFile(f.state, []byte("[]"), 0o600)
	bin := filepath.Join(dir, "herdr")
	os.WriteFile(bin, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "`+f.log+`"
case "$1 $2" in
"machine list") cat "`+f.state+`" ;;
"machine add") printf '[{"id":"m1","label":"%s","target":"%s","enabled":true}]\n' "$5" "$3" > "`+f.state+`" ;;
"machine remove") echo '[]' > "`+f.state+`" ;;
"notification show") [ -n "$HERDR_NOTIFY_FAIL" ] && { echo "no server" >&2; exit 1; } ;;
esac
exit 0
`), 0o755)
	t.Setenv("HERDR_BIN_PATH", bin)
	return f
}

func (f *herdrStandIn) set(t *testing.T, json string) {
	t.Helper()
	if err := os.WriteFile(f.state, []byte(json), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *herdrStandIn) calls() string { b, _ := os.ReadFile(f.log); return string(b) }

// fakeHC puts the herdr app's hc on PATH, answering machine ls --json.
func fakeHC(t *testing.T, json string) {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "machines.json"), []byte(json), 0o600)
	os.WriteFile(filepath.Join(dir, "hc"), []byte("#!/bin/sh\n[ \"$*\" = \"machine ls --json\" ] || exit 2\ncat '"+dir+"/machines.json'\n"), 0o755)
	t.Setenv("HC_BIN_PATH", filepath.Join(dir, "hc"))
}
