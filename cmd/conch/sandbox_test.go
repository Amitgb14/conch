package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	usage   string // the analytics reply; "" is a sensible default
	mu      sync.Mutex
	boxes   map[string]string // id → state
	created []map[string]any
	bodies  map[string][]byte
	calls   []string
	// createState is the state a new sandbox reports ("" is started).
	createState string
}

func newA4Daytona(t *testing.T) *a4Daytona {
	t.Helper()
	d := &a4Daytona{boxes: map[string]string{}, bodies: map[string][]byte{}}
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	t.Setenv("DAYTONA_API_KEY", "k-test")
	t.Setenv("DAYTONA_API_URL", srv.URL)
	t.Setenv("DAYTONA_ANALYTICS_URL", srv.URL)
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

// body is what a call was sent, for a test to check.
func (d *a4Daytona) body(key string) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]byte(nil), d.bodies[key]...)
}

func (d *a4Daytona) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, "/usage") && strings.Contains(r.URL.Path, "/organization/") {
		d.calls = append(d.calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if d.usage != "" {
			fmt.Fprint(w, d.usage)
			return
		}
		fmt.Fprint(w, `[{"startAt":"2026-09-26T10:00:00Z","endAt":"2026-09-26T10:02:00Z","cpu":1,"ramGB":1,"diskGB":3,"price":0.002},`+
			`{"startAt":"2026-09-26T10:02:00Z","endAt":"2026-09-26T10:30:00Z","cpu":0,"ramGB":0,"diskGB":3,"price":0.001}]`)
		return
	}
	if parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/"); len(parts) == 5 && parts[4] == "signed-preview-url" {
		d.calls = append(d.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if d.boxes[parts[1]] == "" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"statusCode":404,"message":"not found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"url":"https://%s-signed.proxy.daytona.work","token":"signed"}`, parts[3])
		return
	}
	d.calls = append(d.calls, r.Method+" "+r.URL.Path)
	if r.Body != nil {
		if b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20)); len(b) > 0 {
			d.bodies[r.Method+" "+r.URL.Path] = b
			r.Body = io.NopCloser(bytes.NewReader(b)) // handlers read it again
		}
	}
	if r.Header.Get("Authorization") != "Bearer k-test" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	box := func(id string) map[string]any {
		return map[string]any{"id": id, "organizationId": "org-1", "state": d.boxes[id],
			"cpu": 1, "memory": 1, "disk": 3}
	}
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/snapshots":
		fmt.Fprint(w, `{"items":[{"name":"live-2026-09-20-1000","state":"active","cpu":1,"memory":1,"disk":3,"createdAt":"2026-09-20T10:00:00Z"},`+
			`{"name":"older","state":"active","createdAt":"2026-09-18T10:00:00Z"}]}`)
	case len(strings.Split(strings.Trim(r.URL.Path, "/"), "/")) == 2 && strings.HasPrefix(r.URL.Path, "/snapshots/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
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
	for _, want := range []string{"Creating a Daytona sandbox…", "sandbox sbx1-0123456789 started", "probing fix-login…", "copying conch to fix-login"} {
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
	out, errOut := a4Capture(t, "", func() { err = runSandbox(nil) })
	if err != nil {
		t.Fatal(err)
	}
	// Both kinds of row nobody could otherwise account for say what to do.
	for _, want := range []string{"1 with no machine here", "conch sandbox rm ID deletes one",
		"1 gone: deleted outside conch"} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("stderr lacks %q:\n%s", want, errOut)
		}
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
	out, errOut = a4Capture(t, "", func() { err = runSandbox([]string{"rm", "-y", "gone"}) })
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

func TestA4MachineAddRefusesASandboxTarget(t *testing.T) {
	a4Env(t)
	if err := runMachine([]string{"add", "daytona:sb1"}); err == nil || !strings.Contains(err.Error(), "conch sandbox create") {
		t.Fatalf("err %v", err)
	}
}

// A sandbox destroyed in Daytona's own interface: conch lists it as gone
// rather than stuck at "destroying", says what clears it, and rm clears it
// even while Daytona is still deleting.
func TestA4SandboxDestroyedElsewhere(t *testing.T) {
	a4Env(t)
	d := newA4Daytona(t)
	d.set("sb-going", "destroying")
	if _, err := remote.SaveMachine(remote.Machine{Label: "box", Target: "daytona:sb-going"}); err != nil {
		t.Fatal(err)
	}

	var err error
	out, errOut := a4Capture(t, "", func() { err = runSandbox([]string{"ls"}) })
	if err != nil {
		t.Fatal(err)
	}
	row := strings.Join(strings.Fields(strings.Split(strings.TrimSpace(out), "\n")[1]), " ")
	if row != "box box sb-going gone -" {
		t.Fatalf("ls row %q in\n%s", row, out)
	}
	if !strings.Contains(errOut, "1 gone: deleted outside conch") || !strings.Contains(errOut, "conch sandbox rm ID") {
		t.Fatalf("ls said nothing about it: %q", errOut)
	}

	// Starting or stopping it says it is gone, rather than waiting.
	for _, args := range [][]string{{"start", "box"}, {"stop", "-y", "box"}} {
		if _, _ = a4Capture(t, "", func() { err = runSandbox(args) }); err == nil {
			t.Fatalf("%v on a deleted sandbox", args)
		}
	}

	// rm clears what is left here, and the machine is gone from the catalog.
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"rm", "-y", "box"}) })
	if err != nil || !strings.Contains(out, "deleted box") {
		t.Fatalf("rm: %q %v", out, err)
	}
	ms, err := remote.Machines()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.Target == "daytona:sb-going" {
			t.Fatalf("the machine is still in the catalog: %+v", m)
		}
	}
}

// conch sandbox url prints a link to a port inside a sandbox.
func TestA4SandboxURL(t *testing.T) {
	a4Env(t)
	d := newA4Daytona(t)
	d.set("sb-live", "started")
	if _, err := remote.SaveMachine(remote.Machine{Label: "live", Target: "daytona:sb-live"}); err != nil {
		t.Fatal(err)
	}

	var err error
	out, _ := a4Capture(t, "", func() { err = runSandbox([]string{"url", "live", "3000"}) })
	if err != nil || strings.TrimSpace(out) != "https://3000-signed.proxy.daytona.work" {
		t.Fatalf("url: %q %v", out, err)
	}
	if !strings.Contains(d.called(), "expiresInSeconds=3600") {
		t.Fatalf("asked for: %s", d.called())
	}
	// -open hands the link to the desktop rather than only printing it —
	// and says once that anyone with the link can reach that port. Nothing
	// opens here: the test replaces the handing over.
	var opened string
	oldOpen := openInBrowser
	openInBrowser = func(url string) error { opened = url; return nil }
	t.Cleanup(func() { openInBrowser = oldOpen })
	out, errOut := a4Capture(t, "", func() { err = runSandbox([]string{"url", "-open", "live", "3000"}) })
	if err != nil || opened != "https://3000-signed.proxy.daytona.work" {
		t.Fatalf("-open: opened %q (%q) %v", opened, out, err)
	}
	if !strings.Contains(errOut, "the link works for anyone until it expires") {
		t.Fatalf("-open said: %q", errOut)
	}

	// A time of its own, in the shape a Go duration takes.
	if _, _ = a4Capture(t, "", func() { err = runSandbox([]string{"url", "-expires", "10m", "live", "3000"}) }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.called(), "expiresInSeconds=600") {
		t.Fatalf("10m asked for: %s", d.called())
	}
	// What it refuses: a port that isn't one, too few arguments, an
	// unknown sandbox.
	for _, args := range [][]string{
		{"url", "live", "nope"},
		{"url", "live", "0"},
		{"url", "live"},
		{"url"},
	} {
		if _, _, err := a4Out(t, func() error { return runSandbox(args) }); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
}

// conch sandbox usage prints what a sandbox has cost, period by period.
func TestA4SandboxUsage(t *testing.T) {
	a4Env(t)
	d := newA4Daytona(t)
	d.set("sb-live", "started")
	if _, err := remote.SaveMachine(remote.Machine{Label: "live", Target: "daytona:sb-live"}); err != nil {
		t.Fatal(err)
	}

	var err error
	out, errOut := a4Capture(t, "", func() { err = runSandbox([]string{"usage", "live"}) })
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(out), "\n")
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if len(rows) != 4 || flat(rows[0]) != "FROM FOR COST WHAT" {
		t.Fatalf("usage:\n%s", out)
	}
	if !strings.Contains(flat(rows[1]), "running, 1 vCPU, 1 GiB, 3 GiB disk") ||
		!strings.Contains(flat(rows[2]), "stopped, 3 GiB disk") {
		t.Fatalf("periods:\n%s", out)
	}
	if !strings.Contains(flat(rows[3]), "$0.003000 total since") {
		t.Fatalf("total:\n%s", out)
	}
	if errOut != "" {
		t.Fatalf("said on stderr: %q", errOut)
	}

	// Nothing reported yet says so, and is not an error.
	d.usage = "[]"
	out, errOut = a4Capture(t, "", func() { err = runSandbox([]string{"usage", "live"}) })
	if err != nil || out != "" || !strings.Contains(errOut, "nothing to report") {
		t.Fatalf("nothing yet: %q %q %v", out, errOut, err)
	}
	// And what it refuses.
	for _, args := range [][]string{{"usage"}, {"usage", "live", "extra"}} {
		if _, _, err := a4Out(t, func() error { return runSandbox(args) }); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
}

// a4Boat is boat.dev's API as the sandbox commands use it: its sandboxes
// have an sshd of their own, so a key is authorized and there is no token.
type a4Boat struct {
	mu      sync.Mutex
	boxes   map[string]string // id → state
	created []map[string]any
	keys    []string
	calls   []string
}

func newA4Boat(t *testing.T) *a4Boat {
	t.Helper()
	b := &a4Boat{boxes: map[string]string{}}
	srv := httptest.NewServer(b)
	t.Cleanup(srv.Close)
	t.Setenv("BOAT_API_KEY", "boat_test")
	t.Setenv("BOAT_API_URL", srv.URL)
	// A key of conch's own, so nothing has to make one.
	ssh := filepath.Join(os.Getenv("HOME"), ".ssh")
	os.MkdirAll(ssh, 0o700)
	os.WriteFile(filepath.Join(ssh, "id_ed25519.pub"),
		[]byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample conch@test\n"), 0o644)
	return b
}

func (b *a4Boat) called() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.calls, "\n")
}

func (b *a4Boat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer boat_test" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	box := func(id string) map[string]any {
		return map[string]any{"id": id, "name": id, "state": b.boxes[id], "vcpu": 4, "memoryGB": 8,
			"ip": "198.51.100.7", "subdomain": "frazil-pneuma-rallye"}
	}
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
		b.created = append(b.created, body)
		id := fmt.Sprintf("bx_23456%d", len(b.created))
		b.boxes[id] = "ready"
		reply(map[string]any{"ok": true, "sandbox": box(id)})
	case r.Method == http.MethodGet && r.URL.Path == "/sandboxes":
		var list []any
		for id := range b.boxes {
			list = append(list, box(id))
		}
		reply(map[string]any{"ok": true, "sandboxes": list})
	case len(parts) >= 2 && parts[0] == "sandboxes":
		id := parts[1]
		if _, ok := b.boxes[id]; !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"ok":false,"error":{"code":"not_found","message":"sandbox not found"}}`)
			return
		}
		switch {
		case r.Method == http.MethodGet && len(parts) == 2:
			reply(map[string]any{"ok": true, "sandbox": box(id)})
		case r.Method == http.MethodDelete:
			delete(b.boxes, id)
		case len(parts) == 3 && parts[2] == "sshkey":
			key, _ := body["key"].(string)
			b.keys = append(b.keys, key)
			reply(map[string]any{"ok": true, "machineIp": "198.51.100.7"})
		case len(parts) == 3 && parts[2] == "stop":
			b.boxes[id] = "archived"
			reply(map[string]any{"ok": true, "sandbox": box(id)})
		case len(parts) == 3 && parts[2] == "resume":
			b.boxes[id] = "ready"
			reply(map[string]any{"ok": true, "sandbox": box(id)})
		case len(parts) == 3 && parts[2] == "host":
			reply(map[string]any{"ok": true, "url": fmt.Sprintf("https://frazil-pneuma-rallye-%v.on.boat.dev", body["port"])})
		case len(parts) == 3 && parts[2] == "usage":
			fmt.Fprint(w, `{"ok":true,"seconds":3600,"dollars":0.25,"running":true}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// -provider names whose sandboxes a command means; a command that names a
// sandbox takes it from that sandbox's own machine instead.
func TestA4SandboxProviderFlag(t *testing.T) {
	a4Env(t)
	b := newA4Boat(t)
	f, _ := a4SandboxMachine(t)

	var err error
	out, errOut := a4Capture(t, "", func() {
		err = runSandbox([]string{"-provider", "boat", "create", "-label", "hull"})
	})
	if err != nil {
		t.Fatalf("create: %v\n%s", err, errOut)
	}
	if out != "added hull (hull): boat sandbox bx_234561, server pid 777 on sandbox-host\n" {
		t.Fatalf("out %q", out)
	}
	if !strings.Contains(errOut, "Creating a boat.dev sandbox…") {
		t.Fatalf("stderr:\n%s", errOut)
	}
	// conch's own key opened it, and ssh went to the machine as user.
	if len(b.keys) != 1 || !strings.HasPrefix(b.keys[0], "ssh-ed25519 ") {
		t.Fatalf("authorized %q", b.keys)
	}
	if argv := f.argv(t); !strings.Contains(argv, "user@198.51.100.7") {
		t.Fatalf("ssh argv:\n%s", argv)
	}
	ms, _ := remote.Machines()
	if len(ms) != 1 || ms[0].Target != "boat:bx_234561" {
		t.Fatalf("saved %+v", ms)
	}

	// The next command starts clean: without the flag it means Daytona
	// again, and asks boat nothing.
	before := b.called()
	if err := runSandbox([]string{"create"}); err == nil || !strings.Contains(err.Error(), "$DAYTONA_API_KEY") {
		t.Fatalf("without the flag: %v", err)
	}
	if b.called() != before {
		t.Fatalf("boat was asked anyway:\n%s", b.called())
	}
	// A named sandbox takes its provider from its own machine.
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"usage", "hull"}) })
	// boat charges for machine time only, so the line says what was
	// running and nothing about disk.
	if err != nil || !strings.Contains(out, "$0.250000") || !strings.Contains(out, "running, 4 vCPU, 8 GiB") ||
		strings.Contains(out, "GiB disk") {
		t.Fatalf("usage: %q %v", out, err)
	}
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"url", "hull", "3000"}) })
	if err != nil || !strings.Contains(out, "https://frazil-pneuma-rallye-3000.on.boat.dev") {
		t.Fatalf("url: %q %v", out, err)
	}
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"-provider", "boat", "ls"}) })
	// boat says nothing about disk, so the size column doesn't invent it.
	if err != nil || !strings.Contains(out, "bx_234561") || !strings.Contains(out, "hull") ||
		!strings.Contains(out, "4 vCPU, 8 GiB") || strings.Contains(out, "GiB disk") {
		t.Fatalf("ls: %q %v", out, err)
	}
	// Stopping and starting it again go to boat, not Daytona.
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"stop", "-y", "hull"}) })
	if err != nil || !strings.Contains(out, "stopped") {
		t.Fatalf("stop: %q %v", out, err)
	}
	if !strings.Contains(b.called(), "POST /sandboxes/bx_234561/stop") {
		t.Fatalf("boat was not told to stop:\n%s", b.called())
	}

	// A provider conch doesn't know, and a flag with nothing after it.
	for _, c := range []struct{ args, want []string }{
		{[]string{"-provider", "fly", "ls"}, []string{`unknown sandbox provider "fly"`, "daytona, boat"}},
		{[]string{"-provider"}, []string{"-provider needs a name"}},
		{[]string{"--provider=nope", "ls"}, []string{`unknown sandbox provider "nope"`}},
	} {
		err := runSandbox(c.args)
		for _, want := range c.want {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%v: %v, want %q", c.args, err, want)
			}
		}
	}
	// The long and short spellings both work, anywhere in the arguments.
	for _, args := range [][]string{{"--provider", "boat", "ls"}, {"-provider=boat", "ls"}, {"ls", "-provider", "boat"}} {
		if _, _ = a4Capture(t, "", func() { err = runSandbox(args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

// conch sandbox snapshot keeps a sandbox to make others from, and
// snapshots lists what is kept or forgets one.
func TestA4SandboxSnapshots(t *testing.T) {
	a4Env(t)
	d := newA4Daytona(t)
	d.set("sb-live", "stopped")
	if _, err := remote.SaveMachine(remote.Machine{Label: "live", Target: "daytona:sb-live"}); err != nil {
		t.Fatal(err)
	}
	var err error

	// A name of its own, and the advice that it takes a while.
	out, errOut := a4Capture(t, "", func() { err = runSandbox([]string{"snapshot", "-name", "base", "live"}) })
	if err != nil || strings.TrimSpace(out) != "keeping live as base" {
		t.Fatalf("snapshot: %q %v", out, err)
	}
	if !strings.Contains(errOut, "conch sandbox create -snapshot base") {
		t.Fatalf("it said: %q", errOut)
	}
	var body map[string]any
	_ = json.Unmarshal(d.body("POST /sandbox/sb-live/snapshot"), &body)
	if body["name"] != "base" || body["includeMemory"] != false {
		t.Fatalf("snapshot body %v", body)
	}
	// No name: the label and the date, so two are never the same.
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"snapshot", "live"}) })
	if err != nil || !strings.HasPrefix(strings.TrimSpace(out), "keeping live as live-") {
		t.Fatalf("default name: %q %v", out, err)
	}

	// The list, newest first, with what each one is.
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"snapshots"}) })
	rows := strings.Split(strings.TrimSpace(out), "\n")
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if err != nil || len(rows) != 3 || flat(rows[0]) != "NAME STATE SIZE KEPT" ||
		!strings.HasPrefix(flat(rows[1]), "live-2026-09-20-1000 active 1 vCPU, 1 GiB, 3 GiB disk") ||
		!strings.HasPrefix(flat(rows[2]), "older active") {
		t.Fatalf("snapshots:\n%s\n%v", out, err)
	}

	// Forgetting one asks first; no leaves it alone.
	a4Capture(t, "n\n", func() { err = runSandbox([]string{"snapshots", "-rm", "older"}) })
	if err != nil || strings.Contains(d.called(), "DELETE /snapshots/older") {
		t.Fatalf("declined: %v\n%s", err, d.called())
	}
	out, _ = a4Capture(t, "y\n", func() { err = runSandbox([]string{"snapshots", "-rm", "older"}) })
	if err != nil || !strings.Contains(out, "forgot older") || !strings.Contains(d.called(), "DELETE /snapshots/older") {
		t.Fatalf("forgot: %q %v", out, err)
	}
	// -y forgets without asking.
	out, _ = a4Capture(t, "", func() { err = runSandbox([]string{"snapshots", "-rm", "base", "-y"}) })
	if err != nil || !strings.Contains(out, "forgot base") {
		t.Fatalf("-y: %q %v", out, err)
	}

	// What it refuses: a sandbox it doesn't know, no sandbox at all, and a
	// provider that cannot keep snapshots.
	for _, c := range []struct{ args, want []string }{
		{[]string{"snapshot"}, []string{"usage: conch sandbox snapshot"}},
		{[]string{"snapshot", "nope"}, []string{"Sandbox not found"}}, // a name conch has no machine for goes to the provider
		{[]string{"snapshot", "-bogus", "live"}, []string{"flag provided but not defined"}},
	} {
		err = runSandbox(c.args)
		for _, want := range c.want {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%v: %v, want %q", c.args, err, want)
			}
		}
	}
}
