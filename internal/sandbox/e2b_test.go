package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// fakeE2B is E2B's platform API, as far as conch uses it.
type fakeE2B struct {
	t  *testing.T
	mu sync.Mutex

	boxes   map[string]*e2bSandbox
	created []map[string]any  // create bodies
	bodies  map[string][]byte // "METHOD path" → the last body sent
	calls   []string          // "METHOD path?query"
	fail    map[string][]int  // "METHOD path" → statuses to answer first
	failMsg string
	nextID  int
}

func newFakeE2B(t *testing.T) (*fakeE2B, *E2B) {
	t.Helper()
	f := &fakeE2B{t: t, boxes: map[string]*e2bSandbox{}, bodies: map[string][]byte{}, fail: map[string][]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("E2B_API_KEY", "e2b-secret")
	t.Setenv("E2B_API_URL", srv.URL)
	return f, NewE2B(config.ProviderCfg{})
}

func (f *fakeE2B) add(id, state string) *e2bSandbox {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &e2bSandbox{SandboxID: id, TemplateID: "base", State: state, CPUCount: 2, MemoryMB: 4096,
		DiskSizeMB: 10240, Metadata: map[string]string{Label: "1"}, StartedAt: "2026-09-26T10:00:00Z",
		EnvdAccessToken: "tok-" + id, Domain: "e2b.app"}
	f.boxes[id] = s
	return s
}

func (f *fakeE2B) state(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.boxes[id]; s != nil {
		return s.State
	}
	return "gone"
}

func (f *fakeE2B) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeE2B) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := r.Header.Get("X-API-Key"); got != "e2b-secret" {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Invalid API key"}`)
		return
	}
	key := r.Method + " " + r.URL.Path
	call := key
	if r.URL.RawQuery != "" {
		call += "?" + r.URL.RawQuery
	}
	f.calls = append(f.calls, call)
	body := make([]byte, 0)
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, maxBody))
		f.bodies[key] = body
	}
	if st := f.fail[key]; len(st) > 0 {
		f.fail[key] = st[1:]
		w.WriteHeader(st[0])
		fmt.Fprint(w, f.failMsg)
		return
	}
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v2/sandboxes":
		var in map[string]any
		_ = json.Unmarshal(body, &in)
		f.created = append(f.created, in)
		f.nextID++
		id := fmt.Sprintf("sb%d", f.nextID)
		s := f.addLocked(id, "running")
		if md, ok := in["metadata"].(map[string]any); ok {
			s.Metadata = map[string]string{}
			for k, v := range md {
				s.Metadata[k] = fmt.Sprint(v)
			}
		}
		w.WriteHeader(http.StatusCreated)
		reply(s)
	case r.Method == http.MethodGet && r.URL.Path == "/v2/sandboxes":
		var list []e2bSandbox
		want := r.URL.Query().Get("metadata")
		for _, s := range f.boxes {
			if want != "" && s.Metadata[Label] != "1" {
				continue
			}
			list = append(list, *s)
		}
		reply(list)
	case len(parts) >= 2 && parts[0] == "sandboxes":
		id := parts[1]
		s := f.boxes[id]
		if s == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"message":"sandbox %s not found"}`, id)
			return
		}
		switch {
		case r.Method == http.MethodGet && len(parts) == 2:
			reply(s)
		case r.Method == http.MethodDelete && len(parts) == 2:
			delete(f.boxes, id)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "pause":
			s.State = "paused"
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "timeout":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	case len(parts) >= 3 && parts[0] == "v2" && parts[1] == "sandboxes":
		id := parts[2]
		s := f.boxes[id]
		if s == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"message":"sandbox %s not found"}`, id)
			return
		}
		if len(parts) == 4 && parts[3] == "connect" && r.Method == http.MethodPost {
			s.State = "running"
			w.WriteHeader(http.StatusCreated)
			reply(s)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"no such route"}`)
	}
}

// addLocked is add for a caller already holding the lock.
func (f *fakeE2B) addLocked(id, state string) *e2bSandbox {
	s := &e2bSandbox{SandboxID: id, TemplateID: "base", State: state, CPUCount: 2, MemoryMB: 4096,
		DiskSizeMB: 10240, Metadata: map[string]string{Label: "1"}, StartedAt: "2026-09-26T10:00:00Z",
		EnvdAccessToken: "tok-" + id, Domain: "e2b.app"}
	f.boxes[id] = s
	return s
}

func TestE2BNeedsAKey(t *testing.T) {
	t.Setenv("E2B_API_KEY", "")
	t.Setenv("E2B_API_URL", "")
	e := NewE2B(config.ProviderCfg{})
	err := e.Check()
	if !errors.Is(err, ErrNotConfigured) || !strings.Contains(err.Error(), "$E2B_API_KEY") ||
		!strings.Contains(err.Error(), "Settings → Sandboxes") {
		t.Fatalf("no key: %v", err)
	}
	if e.base != DefaultE2BURL || e.Name() != "e2b" {
		t.Fatalf("defaults: %q %q", e.base, e.Name())
	}
	// A key kept in the settings is used, and wins over the environment.
	t.Setenv("E2B_API_KEY", "from-env")
	if got := NewE2B(config.ProviderCfg{APIKey: " from-settings "}); got.key != "from-settings" || !got.fromSettings {
		t.Fatalf("settings key: %q", got.key)
	}
	if got := NewE2B(config.ProviderCfg{}); got.key != "from-env" {
		t.Fatalf("environment key: %q", got.key)
	}
	// A variable of its own, and an endpoint of its own.
	t.Setenv("MY_E2B", "named")
	got := NewE2B(config.ProviderCfg{APIKeyEnv: "MY_E2B", APIURL: "https://e2b.example/"})
	if got.key != "named" || got.base != "https://e2b.example" {
		t.Fatalf("named variable: %q %q", got.key, got.base)
	}
	if err := NewE2B(config.ProviderCfg{APIKeyEnv: "NOT_SET"}).Check(); !strings.Contains(err.Error(), "$NOT_SET") {
		t.Fatalf("names the variable it looked in: %v", err)
	}
	// Nothing is asked of the API without a key.
	if _, err := NewE2B(config.ProviderCfg{APIKeyEnv: "NOT_SET"}).Get(ctxFor(t), "sb1"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("called without a key: %v", err)
	}
}

func TestE2BCreateAndList(t *testing.T) {
	f, e := newFakeE2B(t)
	ctx := ctxFor(t)

	s, err := e.Create(ctx, Spec{Env: map[string]string{"TOKEN": "x"}, Labels: map[string]string{"project": "api"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "sb1" || s.State != StateStarted || s.CPU != 2 || s.Memory != 4 || s.Disk != 10 {
		t.Fatalf("created %+v", s)
	}
	body := f.created[0]
	if body["templateID"] != defaultE2BTemplate {
		t.Fatalf("template %v", body["templateID"])
	}
	if body["timeout"] != float64(e2bTTL.Seconds()) {
		t.Fatalf("timeout %v, want %v", body["timeout"], e2bTTL.Seconds())
	}
	// Ending is conch's to decide: a sandbox whose clock runs out pauses,
	// keeping its files and memory, rather than being killed.
	if body["autoPause"] != true || body["autoPauseMemory"] != true {
		t.Fatalf("auto-pause: %v", body)
	}
	labels, _ := body["metadata"].(map[string]any)
	if labels[Label] != "1" || labels["project"] != "api" {
		t.Fatalf("metadata %v: conch's own label must be there", labels)
	}
	if env, _ := body["envVars"].(map[string]any); env["TOKEN"] != "x" {
		t.Fatalf("env %v", body["envVars"])
	}

	// A template and an idle timeout of its own.
	if _, err := e.Create(ctx, Spec{Snapshot: "my-template", AutoStop: 30}); err != nil {
		t.Fatal(err)
	}
	if body := f.created[1]; body["templateID"] != "my-template" || body["timeout"] != float64(1800) {
		t.Fatalf("second create: %v", body)
	}
	// A template named in the settings is used when the spec names none.
	f2, _ := newFakeE2B(t)
	_ = f2
	withTmpl := NewE2B(config.ProviderCfg{Snapshot: "settings-template"})
	if got := firstSet("", withTmpl.tmpl, defaultE2BTemplate); got != "settings-template" {
		t.Fatalf("settings template: %q", got)
	}

	// The list asks for conch's own, running and paused, and reads both.
	f.add("theirs", "running")
	f.boxes["theirs"].Metadata = map[string]string{"someone": "else"}
	f.add("mine-paused", "paused")
	list, err := e.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]State{}
	for _, s := range list {
		states[s.ID] = s.State
	}
	if _, theirs := states["theirs"]; theirs {
		t.Fatalf("listed somebody else's: %v", states)
	}
	if states["mine-paused"] != StateStopped || states["sb1"] != StateStarted {
		t.Fatalf("states %v", states)
	}
	var query string
	for _, c := range f.called() {
		if strings.HasPrefix(c, "GET /v2/sandboxes?") {
			query = c
		}
	}
	if !strings.Contains(query, "metadata=conch%3D1") || !strings.Contains(query, "state=running&state=paused") {
		t.Fatalf("list query %q", query)
	}
}

func TestE2BStartStopDeleteAndRefresh(t *testing.T) {
	f, e := newFakeE2B(t)
	ctx := ctxFor(t)
	f.add("sb-run", "running")
	f.add("sb-paused", "paused")

	// Starting one that is running asks nothing of the API beyond the look.
	before := len(f.called())
	if s, err := e.Start(ctx, "sb-run"); err != nil || s.State != StateStarted {
		t.Fatalf("start a running one: %+v %v", s, err)
	}
	if got := f.called()[before:]; len(got) != 1 || got[0] != "GET /sandboxes/sb-run" {
		t.Fatalf("start a running one asked %v", got)
	}
	// A paused one is resumed through connect, which also sets its clock.
	if s, err := e.Start(ctx, "sb-paused"); err != nil || s.State != StateStarted {
		t.Fatalf("start a paused one: %+v %v", s, err)
	}
	if f.state("sb-paused") != "running" {
		t.Fatalf("still %s", f.state("sb-paused"))
	}

	// Stop pauses, keeping the files and the memory.
	if err := e.Stop(ctx, "sb-run"); err != nil || f.state("sb-run") != "paused" {
		t.Fatalf("stop: %v %s", err, f.state("sb-run"))
	}
	// Pausing one that is already paused is the state asked for.
	f.fail["POST /sandboxes/sb-run/pause"] = []int{http.StatusConflict}
	f.failMsg = `{"message":"sandbox is already paused"}`
	if err := e.Stop(ctx, "sb-run"); err != nil {
		t.Fatalf("pause an already paused one: %v", err)
	}

	// Refresh pushes the end back, in seconds, from now.
	if err := e.Refresh(ctx, "sb-run", 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal(f.bodies["POST /sandboxes/sb-run/timeout"], &body)
	if body["timeout"] != float64(1200) {
		t.Fatalf("refresh sent %v", body)
	}
	if err := e.Refresh(ctx, "sb-run", 0); err != nil { // nothing asked for is conch's own hour
		t.Fatal(err)
	}
	_ = json.Unmarshal(f.bodies["POST /sandboxes/sb-run/timeout"], &body)
	if body["timeout"] != e2bTTL.Seconds() {
		t.Fatalf("refresh with no time given: %v", body)
	}

	// Delete, and deleting one that is already gone.
	if err := e.Delete(ctx, "sb-run"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if f.state("sb-run") != "gone" {
		t.Fatal("it is still there")
	}
	if err := e.Delete(ctx, "sb-run"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete an unknown one: %v", err)
	}
	if err := e.Delete(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete nothing: %v", err)
	}
}

func TestE2BErrorsAndAccess(t *testing.T) {
	f, e := newFakeE2B(t)
	ctx := ctxFor(t)
	f.add("sb1", "running")

	// A sandbox conch doesn't know about reads as not found, wherever asked.
	for _, err := range []error{
		func() error { _, err := e.Get(ctx, "nope"); return err }(),
		func() error { _, err := e.Start(ctx, "nope"); return err }(),
		e.Stop(ctx, "nope"),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown sandbox: %v", err)
		}
	}
	if _, err := e.Get(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no id: %v", err)
	}

	// A bad key says where to look, without ever quoting the key.
	bad := NewE2B(config.ProviderCfg{APIKey: "wrong", APIURL: e.base})
	_, err := bad.List(ctx)
	if err == nil || !strings.Contains(err.Error(), "check the key in $E2B_API_KEY") {
		t.Fatalf("bad key: %v", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatalf("the key is in the error: %v", err)
	}

	// E2B gives no ssh, and says so rather than half-working.
	if _, err := e.SSHAccess(ctx, "sb1"); err == nil || !strings.Contains(err.Error(), "no ssh") {
		t.Fatalf("ssh access: %v", err)
	}
	// What conch uses instead: envd's address and a fresh token.
	a, err := e.envdAccess(ctx, "sb1")
	if err != nil || a.User != "tok-sb1" || a.Host != "e2b.app" {
		t.Fatalf("envd access: %+v %v", a, err)
	}
	if _, err := e.envdAccess(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no id: %v", err)
	}
	f.boxes["sb1"].EnvdAccessToken = ""
	if _, err := e.envdAccess(ctx, "sb1"); err == nil || !strings.Contains(err.Error(), "no access token") {
		t.Fatalf("a sandbox with no token: %v", err)
	}
}
