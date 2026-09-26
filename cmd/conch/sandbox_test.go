package main

import (
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

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// a4Daytona is Daytona's API as the sandbox commands use it: sandboxes
// change state at once, and ssh access names a fake gateway.
type a4Daytona struct {
	mu      sync.Mutex
	boxes   map[string]string // id → state
	created []map[string]any
	calls   []string
	// createState is the state a new sandbox reports ("" is started).
	createState string
}

func newA4Daytona(t *testing.T) *a4Daytona {
	t.Helper()
	d := &a4Daytona{boxes: map[string]string{}}
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	t.Setenv("DAYTONA_API_KEY", "k-test")
	t.Setenv("DAYTONA_API_URL", srv.URL)
	return d
}

func (d *a4Daytona) state(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.boxes[id]
}

func (d *a4Daytona) set(id, state string) {
	d.mu.Lock()
	d.boxes[id] = state
	d.mu.Unlock()
}

func (d *a4Daytona) called() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Join(d.calls, "\n")
}

func (d *a4Daytona) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer k-test" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	box := func(id string) map[string]any {
		return map[string]any{"id": id, "state": d.boxes[id], "cpu": 1, "memory": 1, "disk": 3}
	}
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/sandbox":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		d.created = append(d.created, body)
		id := fmt.Sprintf("sbx%d-0123456789", len(d.created))
		d.boxes[id] = "started"
		if d.createState != "" {
			d.boxes[id] = d.createState
		}
		reply(box(id))
	case r.Method == http.MethodGet && r.URL.Path == "/sandbox":
		items := []any{}
		for id := range d.boxes {
			items = append(items, box(id))
		}
		reply(map[string]any{"items": items, "nextCursor": nil})
	case len(parts) >= 2 && parts[0] == "sandbox":
		id := parts[1]
		if _, ok := d.boxes[id]; !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Sandbox not found"}`)
			return
		}
		switch {
		case r.Method == http.MethodGet:
			reply(box(id))
		case r.Method == http.MethodDelete:
			delete(d.boxes, id)
		case parts[len(parts)-1] == "start":
			d.boxes[id] = "started"
		case parts[len(parts)-1] == "stop":
			d.boxes[id] = "stopped"
		case parts[len(parts)-1] == "ssh-access":
			reply(map[string]any{"token": "tok-secret", "sshCommand": "ssh tok-secret@gw.test", "expiresAt": time.Now().Add(time.Hour).Format(time.RFC3339)})
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// a4SandboxMachine sets up the fake ssh gateway: a new sandbox has no
// conch until it is installed, then reports this build, and its bridge
// reaches a fake server.
func a4SandboxMachine(t *testing.T) (*a4SSH, *a4Server) {
	t.Helper()
	f := newA4SSH(t)
	platform, s, m := a4OtherPlatform()
	f.setProbe(t, s+"\n"+m+"\n")
	f.setProbeAfterInstall(t, a4CurrentProbe())
	srv := startA4Server(t, "")
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.PID, h.Hostname, h.Platform = 777, "sandbox-host", platform
		return h
	})
	t.Setenv("A4_BRIDGE_SOCK", srv.sock)
	bin := filepath.Join(t.TempDir(), "conch-remote")
	os.WriteFile(bin, []byte("remote build"), 0o755)
	t.Setenv("CONCH_REMOTE_BINARY", bin)
	return f, srv
}

func TestA4SandboxCreate(t *testing.T) {
	a4Env(t)
	d := newA4Daytona(t)
	f, _ := a4SandboxMachine(t)
	t.Setenv("MY_TOKEN", "oauth-value")
	os.WriteFile(filepath.Join(os.Getenv("CONCH_HOME"), "config.toml"),
		[]byte("[sandbox.daytona]\nsnapshot = \"daytona-medium\"\nenv = [\"MY_TOKEN\"]\n"), 0o600)

	var err error
	out, errOut := a4Capture(t, "", func() {
		err = runSandbox([]string{"create", "-label", "fix-login", "-cpu", "2"})
	})
	if err != nil {
		t.Fatalf("create: %v\n%s", err, errOut)
	}
	if out != "added fix-login (fix-login): daytona sandbox sbx1-0123456789, server pid 777 on sandbox-host\n" {
		t.Fatalf("out %q", out)
	}
	for _, want := range []string{"Creating a Daytona sandbox…", "sandbox sbx1-0123456789 started", "Checking fix-login…", "copying conch to fix-login", "installed /home/dev/.local/bin/conch"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	if strings.Contains(out+errOut, "tok-secret") {
		t.Fatalf("the ssh token was printed:\n%s%s", out, errOut)
	}
	body := d.created[0]
	if body["snapshot"] != "daytona-medium" || body["cpu"] != 2.0 || body["autoStopInterval"] != 0.0 {
		t.Fatalf("create body %v", body)
	}
	if env, _ := body["env"].(map[string]any); env["MY_TOKEN"] != "oauth-value" {
		t.Fatalf("env %v", body["env"])
	}
	ms, _ := remote.Machines()
	if len(ms) != 1 || ms[0].ID != "fix-login" || ms[0].Target != "daytona:sbx1-0123456789" {
		t.Fatalf("saved %+v", ms)
	}
	if argv := f.argv(t); !strings.Contains(argv, "tok-secret@gw.test") || !strings.Contains(argv, "StrictHostKeyChecking=accept-new") {
		t.Fatalf("ssh argv:\n%s", argv)
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "installed.bin")); string(b) != "remote build" {
		t.Fatalf("installed %q", b)
	}

	// No label: named after the sandbox.
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"create"}) })
	if err != nil || !strings.HasPrefix(out, "added sandbox-sbx2-012 (sandbox-sbx2-012)") {
		t.Fatalf("default label: %q %v", out, err)
	}
}

func TestA4SandboxCreateRefusesBeforeSpending(t *testing.T) {
	a4Env(t)
	var err error
	// No key: nothing is created.
	a4Capture(t, "", func() { err = runSandbox([]string{"create"}) })
	if err == nil || !strings.Contains(err.Error(), "$DAYTONA_API_KEY") {
		t.Fatalf("no key: %v", err)
	}
	d := newA4Daytona(t)
	t.Setenv("UNSET_ONE", "")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"create", "-env", "UNSET_ONE"}, "$UNSET_ONE isn't set here"},
		{[]string{"create", "-env", "A=B"}, "give a variable name"},
		{[]string{"create", "-env", ""}, "give a variable name"},
		{[]string{"create", "-cpu", "-1"}, "can't be negative"},
		{[]string{"create", "extra"}, "usage: conch sandbox create"},
		{[]string{"create", "-bogus"}, "flag provided but not defined"},
	} {
		a4Capture(t, "", func() { err = runSandbox(c.args) })
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}
	if d.called() != "" {
		t.Fatalf("Daytona was called:\n%s", d.called())
	}
	if err := runSandbox([]string{"frobnicate"}); err == nil || !strings.Contains(err.Error(), `unknown sandbox subcommand "frobnicate"`) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestA4SandboxCreateFailsHalfWay(t *testing.T) {
	a4Env(t)
	d := newA4Daytona(t)
	a4SandboxMachine(t)
	t.Setenv("A4_PROBE_FAIL", "tok-secret@gw.test: Permission denied (publickey).")

	// Asked, and the default deletes it.
	var err error
	_, errOut := a4Capture(t, "\n", func() { err = runSandbox([]string{"create"}) })
	if err == nil || strings.Contains(err.Error(), "tok-secret") || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(errOut, "Setting up sandbox sbx1-0123456789 failed. Delete it? [Y/n]") || !strings.Contains(errOut, "deleted sandbox sbx1-0123456789") {
		t.Fatalf("stderr:\n%s", errOut)
	}
	if d.state("sbx1-0123456789") != "" {
		t.Fatal("the failed sandbox is still there")
	}
	if ms, _ := remote.Machines(); len(ms) != 0 {
		t.Fatalf("saved %+v", ms)
	}

	// Kept when the user says no.
	_, errOut = a4Capture(t, "n\n", func() { err = runSandbox([]string{"create"}) })
	if err == nil || !strings.Contains(errOut, "kept it; delete it with: conch sandbox rm sbx2-0123456789") || d.state("sbx2-0123456789") != "started" {
		t.Fatalf("kept: %v\n%s", err, errOut)
	}

	// -yes deletes without asking.
	_, errOut = a4Capture(t, "", func() { err = runSandbox([]string{"create", "-yes"}) })
	if err == nil || strings.Contains(errOut, "[Y/n]") || d.state("sbx3-0123456789") != "" {
		t.Fatalf("-yes: %v\n%s", err, errOut)
	}
}

func TestA4SandboxCreateThatNeverStarts(t *testing.T) {
	a4Env(t)
	d := newA4Daytona(t)
	d.createState = "build_failed"
	var err error
	_, errOut := a4Capture(t, "", func() { err = runSandbox([]string{"create", "-yes"}) })
	if err == nil || !strings.Contains(err.Error(), "is build_failed") {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(errOut, "deleted sandbox sbx1-0123456789") || d.state("sbx1-0123456789") != "" {
		t.Fatalf("not cleaned up:\n%s", errOut)
	}
}

func TestA4SandboxListStartStopRemove(t *testing.T) {
	a4Env(t)
	d := newA4Daytona(t)
	d.set("sb-live", "started")
	d.set("sb-orphan", "stopped")
	for _, m := range []remote.Machine{
		{Label: "live", Target: "daytona:sb-live"},
		{Label: "gone", Target: "daytona:sb-deleted"},
		{Label: "gpu", Target: "dev@gpu.lab"},
	} {
		if _, err := remote.SaveMachine(m); err != nil {
			t.Fatal(err)
		}
	}

	var err error
	out, _ := a4Capture(t, "", func() { err = runSandbox(nil) })
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(out), "\n")
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if len(rows) != 4 || flat(rows[0]) != "ID LABEL SANDBOX STATE SIZE" ||
		flat(rows[1]) != "live live sb-live started 1 vCPU, 1 GiB, 3 GiB disk" ||
		flat(rows[2]) != "gone gone sb-deleted gone -" ||
		flat(rows[3]) != "- - sb-orphan stopped 1 vCPU, 1 GiB, 3 GiB disk" {
		t.Fatalf("ls:\n%s", out)
	}

	// Stop asks first; no leaves it running.
	a4Capture(t, "n\n", func() { err = runSandbox([]string{"stop", "live"}) })
	if err != nil || d.state("sb-live") != "started" {
		t.Fatalf("declined stop: %v %s", err, d.state("sb-live"))
	}
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"stop", "-y", "live"}) })
	if err != nil || out != "live stopped\n" || d.state("sb-live") != "stopped" {
		t.Fatalf("stop: %q %v", out, err)
	}

	// A stopped sandbox can't be reached, and -m says what to run.
	machineFlag = "live"
	_, err = connectMachine("live")
	machineFlag = ""
	if err == nil || !strings.Contains(err.Error(), "sandbox live is stopped; run: conch sandbox start live") {
		t.Fatalf("-m stopped: %v", err)
	}
	err = machineUpgrade(remote.FindMachine("live"))
	if err == nil || !strings.Contains(err.Error(), "conch sandbox start live") {
		t.Fatalf("upgrade stopped: %v", err)
	}

	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"start", "daytona:sb-live"}) })
	if err != nil || out != "live started\n" || d.state("sb-live") != "started" {
		t.Fatalf("start by target: %q %v", out, err)
	}
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"start", "sb-orphan"}) })
	if err != nil || out != "sb-orphan started\n" {
		t.Fatalf("start by sandbox id: %q %v", out, err)
	}

	// rm of one Daytona has lost still forgets it.
	out, errOut := a4Capture(t, "", func() { err = runSandbox([]string{"rm", "-y", "gone"}) })
	if err != nil || out != "deleted gone\n" || !strings.Contains(errOut, "already gone from Daytona") {
		t.Fatalf("rm gone: %q %q %v", out, errOut, err)
	}
	// rm asks, and no keeps everything.
	d.set("sb-live", "stopped")
	_, errOut = a4Capture(t, "\n", func() { err = runSandbox([]string{"rm", "live"}) })
	if err != nil || d.state("sb-live") != "stopped" || !strings.Contains(errOut, "live is stopped, so conch can't check it") {
		t.Fatalf("declined rm: %v\n%s", err, errOut)
	}
	out, _ = a4Capture(t, "y\n", func() { err = runSandbox([]string{"rm", "live"}) })
	if err != nil || out != "deleted live\n" || d.state("sb-live") != "" {
		t.Fatalf("rm: %q %v", out, err)
	}
	ms, _ := remote.Machines()
	if len(ms) != 1 || ms[0].ID != "gpu" {
		t.Fatalf("machines left %+v", ms)
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"start"}, "usage: conch sandbox start ID"},
		{[]string{"stop"}, "usage: conch sandbox stop"},
		{[]string{"rm", "a", "b"}, "usage: conch sandbox rm"},
		{[]string{"start", "gpu"}, "gpu is not a sandbox"},
		{[]string{"rm", "-y", "dev@elsewhere"}, `no sandbox "dev@elsewhere"`},
		{[]string{"start", "nope"}, "Sandbox not found"},
	} {
		a4Capture(t, "", func() { err = runSandbox(c.args) })
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.args, err, c.want)
		}
	}
}

// rm shows what would be lost from a running sandbox before asking.
func TestA4SandboxRemoveWarnsAboutUnpushedWork(t *testing.T) {
	a4Env(t)
	d := newA4Daytona(t)
	_, srv := a4SandboxMachine(t)
	d.set("sb1", "started")
	remote.SaveMachine(remote.Machine{Label: "box", Target: "daytona:sb1"})
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		if msg.Method != proto.MethodProjectList {
			return nil, nil
		}
		return proto.ProjectList{Projects: []proto.ProjectInfo{{Name: "api",
			Worktrees: []proto.WorktreeInfo{{Path: "/w/feat", Branch: "feat", Status: &proto.GitStatus{Files: 3}}},
			Branches:  []proto.BranchInfo{{Name: "feat", BaseAhead: 2, Worktree: "/w/feat"}, {Name: "main"}}}}}, nil
	})
	f := filepath.Join(t.TempDir(), "probe")
	os.WriteFile(f, []byte(a4CurrentProbe()), 0o600)
	t.Setenv("A4_PROBE", f) // conch is already there

	var err error
	_, errOut := a4Capture(t, "n\n", func() { err = runSandbox([]string{"rm", "box"}) })
	if err != nil || !strings.Contains(errOut, "api feat: 2 commits on no remote, 3 files uncommitted") || d.state("sb1") != "started" {
		t.Fatalf("%v\n%s", err, errOut)
	}
}

func TestA4DescribeUnsaved(t *testing.T) {
	got := describeUnsaved([]proto.ProjectInfo{{Name: "api",
		Worktrees: []proto.WorktreeInfo{{Path: "/w/a", Status: &proto.GitStatus{Files: 1}}, {Path: "/w/clean", Status: &proto.GitStatus{}}},
		Branches: []proto.BranchInfo{
			{Name: "a", Upstream: "origin/a", Ahead: 1, Worktree: "/w/a"},
			{Name: "gone", Upstream: "origin/gone", Gone: true, BaseAhead: 4},
			{Name: "pushed", Upstream: "origin/pushed"},
			{Name: "clean", Worktree: "/w/clean"},
			{Name: "nostatus", Worktree: "/w/unknown"},
		}}})
	want := []string{"api a: 1 commit not pushed, 1 file uncommitted", "api gone: 4 commits on no remote"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	if describeUnsaved(nil) != nil {
		t.Fatal("nothing to lose")
	}
}

func TestA4MachineAddRefusesASandboxTarget(t *testing.T) {
	a4Env(t)
	if err := runMachine([]string{"add", "daytona:sb1"}); err == nil || !strings.Contains(err.Error(), "conch sandbox create") {
		t.Fatalf("err %v", err)
	}
}
