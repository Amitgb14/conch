package sandbox

import (
	"context"
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

// fakeTensorlake is Tensorlake's sandbox API, as far as conch uses it.
type fakeTensorlake struct {
	t  *testing.T
	mu sync.Mutex

	boxes   map[string]map[string]any
	states  map[string][]string // queued statuses, one per look
	created []map[string]any
	calls   []string
	auth    []string
	fail    map[string]int // "METHOD /path" → status, once
	failMsg string
	pages   []string // answers for GET /sandboxes, in order
	nextID  int
}

func newFakeTensorlake(t *testing.T) (*fakeTensorlake, *Tensorlake) {
	t.Helper()
	f := &fakeTensorlake{t: t, boxes: map[string]map[string]any{}, states: map[string][]string{}, fail: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("TENSORLAKE_API_KEY", "tl_secret")
	t.Setenv("TENSORLAKE_API_URL", srv.URL)
	t.Setenv("TENSORLAKE_SSH_HOST", "")
	old := pollEvery
	pollEvery = time.Millisecond
	t.Cleanup(func() { pollEvery = old })
	return f, NewTensorlake(config.ProviderCfg{})
}

func (f *fakeTensorlake) add(id, name string, states ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.boxes[id] = map[string]any{"id": id, "name": name, "namespace": "default", "status": states[0],
		"created_at": int64(1793000000000), "resources": map[string]any{"cpus": 2.0, "memory_mb": 4096, "disk_mb": 10240}}
	f.states[id] = states[1:]
}

func (f *fakeTensorlake) status(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b := f.boxes[id]; b != nil {
		return b["status"].(string)
	}
	return "gone"
}

func (f *fakeTensorlake) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeTensorlake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := r.Method + " " + r.URL.Path
	f.calls = append(f.calls, key)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	if st, ok := f.fail[key]; ok {
		delete(f.fail, key)
		w.WriteHeader(st)
		io.WriteString(w, f.failMsg)
		return
	}
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case key == "POST /sandboxes":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.created = append(f.created, body)
		f.nextID++
		id := fmt.Sprintf("sbx%d", f.nextID)
		f.boxes[id] = map[string]any{"id": id, "name": body["name"], "status": "pending",
			"created_at": int64(1793000000000), "resources": map[string]any{"cpus": 1.0, "memory_mb": 2048, "disk_mb": 10240}}
		f.states[id] = []string{"running"}
		w.WriteHeader(http.StatusAccepted)
		reply(map[string]any{"sandbox_id": id, "name": body["name"], "state": "pending"})
	case key == "GET /sandboxes":
		if len(f.pages) > 0 {
			page := f.pages[0]
			f.pages = f.pages[1:]
			io.WriteString(w, page)
			return
		}
		var list []any
		for _, b := range f.boxes {
			list = append(list, b)
		}
		reply(map[string]any{"sandboxes": list, "next_cursor": nil})
	case len(parts) >= 2 && parts[0] == "sandboxes":
		b := f.boxes[parts[1]]
		if b == nil {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, "sandbox not found")
			return
		}
		switch {
		case r.Method == http.MethodGet && len(parts) == 2:
			if q := f.states[parts[1]]; len(q) > 0 {
				b["status"], f.states[parts[1]] = q[0], q[1:]
			}
			reply(b)
		case r.Method == http.MethodDelete:
			delete(f.boxes, parts[1])
		case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "suspend":
			b["status"], f.states[parts[1]] = "suspending", []string{"suspended"}
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "resume":
			b["status"], f.states[parts[1]] = "pending", []string{"running"}
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestTensorlakeNeedsAKey(t *testing.T) {
	t.Setenv("TENSORLAKE_API_KEY", "")
	p := NewTensorlake(config.ProviderCfg{})
	if err := p.Check(); !errors.Is(err, ErrNotConfigured) || !strings.Contains(err.Error(), "$TENSORLAKE_API_KEY") {
		t.Fatalf("check: %v", err)
	}
	if _, err := p.List(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("list without a key: %v", err)
	}
	// A key from Settings, under a variable of the person's choosing.
	t.Setenv("MY_TL", "k2")
	if err := NewTensorlake(config.ProviderCfg{APIKeyEnv: "MY_TL"}).Check(); err != nil {
		t.Fatal(err)
	}
	if err := NewTensorlake(config.ProviderCfg{APIKey: " k3 "}).Check(); err != nil {
		t.Fatal(err)
	}
	if p, err := Open("tensorlake", config.SandboxCfg{}); err != nil || p.Name() != "tensorlake" || !Known("tensorlake") {
		t.Fatalf("open: %v %v", p, err)
	}
	if ProviderLabel("tensorlake") != "Tensorlake" {
		t.Fatal(ProviderLabel("tensorlake"))
	}
}

func TestTensorlakeCreateAndList(t *testing.T) {
	f, p := newFakeTensorlake(t)
	s, err := p.Create(context.Background(), Spec{Name: "Fix Login!", CPU: 2, Memory: 4, Disk: 20, Snapshot: "ubuntu-dev"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "sbx1" || s.State != StateStarted {
		t.Fatalf("created %+v", s)
	}
	body := f.created[0]
	name, _ := body["name"].(string)
	if !strings.HasPrefix(name, "conch-fix-login-") || len(name) != len("conch-fix-login-")+6 {
		t.Fatalf("name %q", name)
	}
	if body["wait"] != false || body["timeout_secs"] != 0.0 || body["image"] != "ubuntu-dev" {
		t.Fatalf("body %v", body)
	}
	res := body["resources"].(map[string]any)
	if res["cpus"] != 2.0 || res["memory_mb"] != 4096.0 || res["disk_mb"] != 20480.0 {
		t.Fatalf("resources %v", res)
	}
	if f.auth[0] != "Bearer tl_secret" {
		t.Fatalf("auth %q", f.auth[0])
	}
	// Nothing asked for: Tensorlake's defaults, and a name all the same,
	// since only a named sandbox can be suspended.
	if _, err := p.Create(context.Background(), Spec{AutoStop: 30}); err != nil {
		t.Fatal(err)
	}
	b2 := f.created[1]
	if _, ok := b2["resources"]; ok || b2["image"] != nil || b2["timeout_secs"] != 1800.0 {
		t.Fatalf("defaults %v", b2)
	}
	if n, _ := b2["name"].(string); !strings.HasPrefix(n, "conch-") || len(n) != len("conch-")+6 {
		t.Fatalf("unnamed %q", n)
	}
	// One somebody made by hand is not conch's.
	f.add("mine", "my-dev", "running")
	f.add("anon", "", "running")
	list, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list %+v", list)
	}
	for _, s := range list {
		if !strings.HasPrefix(s.Name, "conch-") || s.Memory != 2 || s.Disk != 10 || s.Created.IsZero() {
			t.Fatalf("listed %+v", s)
		}
	}
}

func TestTensorlakeRefusesWhatItCannotDo(t *testing.T) {
	f, p := newFakeTensorlake(t)
	if _, err := p.Create(context.Background(), Spec{Env: map[string]string{"ANTHROPIC_API_KEY": "x"}}); err == nil ||
		!strings.Contains(err.Error(), "Tensorlake secrets") {
		t.Fatalf("env: %v", err)
	}
	for _, d := range []int{9, 101} {
		if _, err := p.Create(context.Background(), Spec{Disk: d}); err == nil || !strings.Contains(err.Error(), "10 to 100 GiB") {
			t.Fatalf("disk %d: %v", d, err)
		}
	}
	for _, d := range []int{10, 100} { // the edges are allowed
		if _, err := p.Create(context.Background(), Spec{Disk: d}); err != nil {
			t.Fatalf("disk %d: %v", d, err)
		}
	}
	if len(f.created) != 2 {
		t.Fatalf("refused creates reached the API: %d", len(f.created))
	}
}

func TestTensorlakeStartStopDelete(t *testing.T) {
	f, p := newFakeTensorlake(t)
	ctx := context.Background()
	f.add("a", "conch-a-000000", "running")
	if err := p.Stop(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if f.status("a") != "suspended" {
		t.Fatalf("stop left it %s", f.status("a"))
	}
	if err := p.Stop(ctx, "a"); err != nil { // already stopped: nothing asked
		t.Fatal(err)
	}
	s, err := p.Start(ctx, "a")
	if err != nil || s.State != StateStarted {
		t.Fatalf("start: %+v %v", s, err)
	}
	if _, err := p.Start(ctx, "a"); err != nil { // already running
		t.Fatal(err)
	}
	n := 0
	for _, c := range f.called() {
		if strings.HasSuffix(c, "/suspend") || strings.HasSuffix(c, "/resume") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("suspend/resume asked %d times: %v", n, f.called())
	}
	// A resume that failed is an error with Tensorlake's reason, and asking
	// again is how it is retried.
	f.add("b", "conch-b-000000", "resume_failed")
	f.with(func() { f.boxes["b"]["error_details"] = "snapshot missing" })
	got, err := p.Get(ctx, "b")
	if err != nil || got.State != StateError || got.Reason != "snapshot missing" {
		t.Fatalf("failed resume: %+v %v", got, err)
	}
	if s, err := p.Start(ctx, "b"); err != nil || s.State != StateStarted {
		t.Fatalf("retry: %+v %v", s, err)
	}
	if err := p.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("gone: %v", err)
	}
	if _, err := p.Get(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get gone: %v", err)
	}
	for _, fn := range []func() error{
		func() error { return p.Delete(ctx, "") },
		func() error { _, err := p.Get(ctx, ""); return err },
		func() error { _, err := p.SSHAccess(ctx, ""); return err },
	} {
		if err := fn(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("empty id: %v", err)
		}
	}
	// A sandbox that ends while conch waits on it says so.
	f.add("c", "conch-c-000000", "pending", "terminated")
	f.with(func() { f.boxes["c"]["termination_reason"] = "ImageNotFound" })
	if _, err := p.Start(ctx, "c"); err == nil || !strings.Contains(err.Error(), "ImageNotFound") {
		t.Fatalf("terminated: %v", err)
	}
}

func (f *fakeTensorlake) with(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func TestTensorlakeSSHAccess(t *testing.T) {
	f, p := newFakeTensorlake(t)
	f.add("sbx-9", "conch-x-000000", "running")
	a, err := p.SSHAccess(context.Background(), "sbx-9")
	if err != nil {
		t.Fatal(err)
	}
	if a.Target() != "sbx-9@sandbox.tensorlake.ai" || !a.PlainUser {
		t.Fatalf("access %+v", a)
	}
	t.Setenv("TENSORLAKE_SSH_HOST", "ssh.example.test")
	if a, _ := NewTensorlake(config.ProviderCfg{}).SSHAccess(context.Background(), "sbx-9"); a.Host != "ssh.example.test" {
		t.Fatalf("host %q", a.Host)
	}
	var _ AccountKeyed = p
	var _ Noted = p
	if !strings.Contains(p.Note(), "tl sbx ssh keys add") {
		t.Fatal(p.Note())
	}
}

func TestTensorlakeListPagesAndErrors(t *testing.T) {
	f, p := newFakeTensorlake(t)
	f.pages = []string{
		`{"sandboxes":[{"id":"1","name":"conch-a-1","status":"running","created_at":1}],"next_cursor":"c2"}`,
		`{"sandboxes":[{"id":"2","name":"conch-b-2","status":"suspended","created_at":1}],"next_cursor":null}`,
	}
	list, err := p.List(context.Background())
	if err != nil || len(list) != 2 || list[1].State != StateStopped {
		t.Fatalf("pages: %+v %v", list, err)
	}
	// A cursor that never moves ends the list rather than looping.
	f.pages = []string{
		`{"sandboxes":[{"id":"1","name":"conch-a-1","status":"running"}],"next_cursor":"same"}`,
		`{"sandboxes":[{"id":"1","name":"conch-a-1","status":"running"}],"next_cursor":"same"}`,
	}
	if list, err := p.List(context.Background()); err != nil || len(list) != 2 {
		t.Fatalf("stuck cursor: %+v %v", list, err)
	}
	f.pages = []string{`{not json`}
	if _, err := p.List(context.Background()); err == nil {
		t.Fatal("bad JSON listed")
	}
	// A refused key says where the key comes from; Tensorlake's own text
	// is kept.
	f.with(func() { f.fail["GET /sandboxes"], f.failMsg = http.StatusUnauthorized, "invalid credentials" })
	if _, err := p.List(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "invalid credentials") || !strings.Contains(err.Error(), "$TENSORLAKE_API_KEY") {
		t.Fatalf("401: %v", err)
	}
	f.with(func() { f.fail["POST /sandboxes"], f.failMsg = http.StatusConflict, "name exists" })
	if _, err := p.Create(context.Background(), Spec{}); err == nil || !strings.Contains(err.Error(), "409 name exists") {
		t.Fatalf("409: %v", err)
	}
	// A 404 on create is a missing image, not a missing sandbox.
	f.with(func() { f.fail["POST /sandboxes"], f.failMsg = http.StatusNotFound, "snapshot not found" })
	if _, err := p.Create(context.Background(), Spec{}); err == nil || errors.Is(err, ErrNotFound) ||
		!strings.Contains(err.Error(), "snapshot not found") {
		t.Fatalf("404 on create: %v", err)
	}
	// A cancelled wait ends with the context.
	ctx, cancel := context.WithCancel(context.Background())
	f.add("slow", "conch-slow-0", "pending", "pending", "pending", "pending", "pending", "pending")
	cancel()
	if _, err := p.Start(ctx, "slow"); err == nil {
		t.Fatal("a cancelled start returned")
	}
}

func TestTensorlakeNames(t *testing.T) {
	long := strings.Repeat("a", 60)
	for in, prefix := range map[string]string{
		"":          "conch-",
		"!!!":       "conch-",
		"Hull":      "conch-hull-",
		"fix/login": "conch-fix-login-",
		long:        "conch-" + strings.Repeat("a", 32) + "-",
	} {
		got := tensorlakeName(in)
		if !strings.HasPrefix(got, prefix) || len(got) != len(prefix)+6 {
			t.Fatalf("%q → %q", in, got)
		}
	}
	if a, b := tensorlakeName("x"), tensorlakeName("x"); a == b {
		t.Fatal("two names alike")
	}
}
