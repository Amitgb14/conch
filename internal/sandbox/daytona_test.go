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

// fakeDaytona is Daytona's API in miniature. Each sandbox walks through
// the states queued for it, one per GET, and stays on the last.
type fakeDaytona struct {
	t  *testing.T
	mu sync.Mutex

	boxes   map[string]*fakeBox
	created []map[string]any // create bodies
	calls   []string         // "METHOD path"
	pages   []string         // raw list replies, served in turn
	fail    map[string][]int // "METHOD path" → statuses to answer first
	failMsg string
	headers map[string]string // added to failure replies
	access  string            // raw ssh-access reply
	nextID  int
}

type fakeBox struct {
	sandbox daytonaSandbox
	states  []State
}

func newFakeDaytona(t *testing.T) (*fakeDaytona, *Daytona) {
	t.Helper()
	f := &fakeDaytona{t: t, boxes: map[string]*fakeBox{}, fail: map[string][]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("DAYTONA_API_KEY", "k-secret")
	t.Setenv("DAYTONA_API_URL", srv.URL+"/api/")
	t.Setenv("DAYTONA_TARGET", "")
	old := pollEvery
	pollEvery = time.Millisecond
	t.Cleanup(func() { pollEvery = old })
	return f, NewDaytona(config.DaytonaCfg{})
}

// add puts a sandbox in place that will walk through states.
func (f *fakeDaytona) add(id string, states ...State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.boxes[id] = &fakeBox{sandbox: daytonaSandbox{ID: id, Name: id, State: states[0]}, states: states[1:]}
}

// then queues more states for a sandbox, as an action would cause.
func (f *fakeDaytona) then(id string, states ...State) {
	f.boxes[id].states = append(f.boxes[id].states, states...)
}

func (f *fakeDaytona) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeDaytona) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := r.Header.Get("Authorization"); got != "Bearer k-secret" {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"statusCode":401,"message":"Unauthorized"}`)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api")
	key := r.Method + " " + path
	f.calls = append(f.calls, key)
	if st := f.fail[key]; len(st) > 0 {
		f.fail[key] = st[1:]
		for k, v := range f.headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(st[0])
		fmt.Fprint(w, f.failMsg)
		return
	}
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case key == "POST /sandbox":
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("create body: %v", err)
		}
		f.created = append(f.created, body)
		f.nextID++
		id := fmt.Sprintf("sb%d", f.nextID)
		box := &fakeBox{sandbox: daytonaSandbox{ID: id, State: StateCreating, CreatedAt: "2026-09-25T10:00:00Z"},
			states: []State{"pulling_snapshot", StateStarting, StateStarted}}
		if name, _ := body["name"].(string); name == "bad" {
			box.states = []State{StateBuildFailed}
			box.sandbox.ErrorReason = "image not found"
		}
		f.boxes[id] = box
		reply(box.sandbox)
	case key == "GET /sandbox":
		if len(f.pages) == 0 {
			reply(map[string]any{"items": []any{}, "nextCursor": nil})
			return
		}
		page := f.pages[0]
		f.pages = f.pages[1:]
		fmt.Fprint(w, page)
	case len(parts) >= 2 && parts[0] == "sandbox":
		box := f.boxes[parts[1]]
		if box == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"statusCode":404,"message":"Sandbox with ID or name %s not found"}`, parts[1])
			return
		}
		switch {
		case r.Method == http.MethodGet && len(parts) == 2:
			if len(box.states) > 0 {
				box.sandbox.State, box.states = box.states[0], box.states[1:]
			}
			reply(box.sandbox)
		case r.Method == http.MethodDelete && len(parts) == 2:
			delete(f.boxes, parts[1])
		case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "ssh-access":
			fmt.Fprint(w, f.access)
		case r.Method == http.MethodPost && len(parts) == 3 && (parts[2] == "start" || parts[2] == "stop"):
		default:
			f.t.Errorf("unexpected %s", key)
			w.WriteHeader(http.StatusNotFound)
		}
	default:
		f.t.Errorf("unexpected %s", key)
		w.WriteHeader(http.StatusNotFound)
	}
}

func ctxFor(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestNewDaytonaReadsConfigThenEnvironment(t *testing.T) {
	t.Setenv("DAYTONA_API_KEY", "  from-env \n")
	t.Setenv("DAYTONA_API_URL", "https://env.example/api/")
	t.Setenv("DAYTONA_TARGET", "eu")
	d := NewDaytona(config.DaytonaCfg{})
	if d.key != "from-env" || d.base != "https://env.example/api" || d.target != "eu" {
		t.Fatalf("from env: key %q base %q target %q", d.key, d.base, d.target)
	}
	t.Setenv("MY_KEY", "mine")
	d = NewDaytona(config.DaytonaCfg{APIKeyEnv: "MY_KEY", APIURL: "https://cfg.example", Target: "us", Snapshot: "daytona-large"})
	if d.key != "mine" || d.base != "https://cfg.example" || d.target != "us" || d.snap != "daytona-large" {
		t.Fatalf("config wins: %+v", d)
	}
	t.Setenv("DAYTONA_API_URL", "")
	if d := NewDaytona(config.DaytonaCfg{}); d.base != DefaultDaytonaURL {
		t.Fatalf("default base %q", d.base)
	}
	if d.Name() != "daytona" {
		t.Fatalf("name %q", d.Name())
	}
}

func TestDaytonaWithoutAKeyIsNotConfigured(t *testing.T) {
	t.Setenv("DAYTONA_API_KEY", "")
	t.Setenv("DAYTONA_API_URL", "http://127.0.0.1:1") // never reached
	d := NewDaytona(config.DaytonaCfg{})
	err := d.Check()
	if !errors.Is(err, ErrNotConfigured) || !strings.Contains(err.Error(), "$DAYTONA_API_KEY") {
		t.Fatalf("check: %v", err)
	}
	if _, err := d.Get(context.Background(), "x"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("get without a key: %v", err)
	}
	t.Setenv("OTHER", "")
	if err := NewDaytona(config.DaytonaCfg{APIKeyEnv: "OTHER"}).Check(); !strings.Contains(err.Error(), "$OTHER") {
		t.Fatalf("names the configured variable: %v", err)
	}
}

func TestDaytonaCreateWaitsUntilStarted(t *testing.T) {
	f, d := newFakeDaytona(t)
	d.target, d.snap = "eu", "daytona-small"
	s, err := d.Create(ctxFor(t), Spec{Name: "fix-login", CPU: 2, Memory: 4, Env: map[string]string{"A": "1"},
		Labels: map[string]string{"project": "api", Label: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "sb1" || s.State != StateStarted {
		t.Fatalf("sandbox %+v", s)
	}
	body := f.created[0]
	for k, want := range map[string]any{"name": "fix-login", "snapshot": "daytona-small", "target": "eu",
		"cpu": 2.0, "memory": 4.0, "autoStopInterval": 0.0} {
		if body[k] != want {
			t.Errorf("%s = %v, want %v", k, body[k], want)
		}
	}
	if _, ok := body["disk"]; ok {
		t.Errorf("zero disk was sent: %v", body)
	}
	labels, _ := body["labels"].(map[string]any)
	if labels[Label] != "1" || labels["project"] != "api" {
		t.Errorf("labels %v: conch's own label must win", labels)
	}
	if env, _ := body["env"].(map[string]any); env["A"] != "1" {
		t.Errorf("env %v", body["env"])
	}
	if gets := strings.Count(strings.Join(f.called(), "\n"), "GET /sandbox/sb1"); gets != 3 {
		t.Errorf("polled %d times, want 3: %v", gets, f.called())
	}
}

func TestDaytonaCreateSendsTheMinimum(t *testing.T) {
	f, d := newFakeDaytona(t)
	if _, err := d.Create(ctxFor(t), Spec{AutoStop: -5}); err != nil {
		t.Fatal(err)
	}
	body := f.created[0]
	for _, k := range []string{"name", "snapshot", "target", "cpu", "memory", "disk", "env"} {
		if _, ok := body[k]; ok {
			t.Errorf("%s sent for an empty spec: %v", k, body)
		}
	}
	if body["autoStopInterval"] != 0.0 {
		t.Errorf("a negative auto-stop must become never (0): %v", body["autoStopInterval"])
	}
	f2, d2 := newFakeDaytona(t)
	if _, err := d2.Create(ctxFor(t), Spec{AutoStop: 90}); err != nil {
		t.Fatal(err)
	}
	if f2.created[0]["autoStopInterval"] != 90.0 {
		t.Errorf("auto-stop %v", f2.created[0]["autoStopInterval"])
	}
}

func TestDaytonaCreateThatFailsToBuild(t *testing.T) {
	_, d := newFakeDaytona(t)
	s, err := d.Create(ctxFor(t), Spec{Name: "bad"})
	var failed *FailedError
	if !errors.As(err, &failed) || failed.State != StateBuildFailed || !strings.Contains(err.Error(), "image not found") {
		t.Fatalf("err %v", err)
	}
	if s.ID != "sb1" {
		t.Fatalf("the failed sandbox is still returned, to be deleted: %+v", s)
	}
}

func TestDaytonaCreateRefused(t *testing.T) {
	f, d := newFakeDaytona(t)
	f.fail["POST /sandbox"] = []int{400}
	f.failMsg = `{"statusCode":400,"message":["cpu must not be greater than 4","memory must be an integer"]}`
	_, err := d.Create(ctxFor(t), Spec{CPU: 9})
	if err == nil || !strings.Contains(err.Error(), "cpu must not be greater than 4; memory must be an integer") {
		t.Fatalf("err %v", err)
	}
}

func TestDaytonaCreateWithoutAnID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"state":"creating"}`)
	}))
	defer srv.Close()
	t.Setenv("DAYTONA_API_KEY", "k")
	d := NewDaytona(config.DaytonaCfg{APIURL: srv.URL})
	if _, err := d.Create(ctxFor(t), Spec{}); err == nil || !strings.Contains(err.Error(), "no sandbox id") {
		t.Fatalf("err %v", err)
	}
}

func TestDaytonaWaitEndsWithItsContext(t *testing.T) {
	f, d := newFakeDaytona(t)
	f.add("stuck", StateCreating)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := d.Start(ctx, "stuck")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "it is creating") {
		t.Fatalf("err %v", err)
	}
}

func TestDaytonaWaitRidesOutBriefTrouble(t *testing.T) {
	f, d := newFakeDaytona(t)
	f.add("sb", StateStarting, StateStarted)
	f.fail["GET /sandbox/sb"] = []int{503, 429, 502}
	s, err := d.wait(ctxFor(t), Sandbox{ID: "sb", State: StateStarting}, StateStarted)
	if err != nil || s.State != StateStarted {
		t.Fatalf("state %v err %v", s.State, err)
	}
}

func TestDaytonaWaitGivesUpOnLastingTrouble(t *testing.T) {
	f, d := newFakeDaytona(t)
	f.add("sb", StateStarting)
	f.fail["GET /sandbox/sb"] = []int{500, 500, 500, 500, 500, 500, 500}
	f.failMsg = "upstream down"
	_, err := d.wait(ctxFor(t), Sandbox{ID: "sb", State: StateStarting}, StateStarted)
	var api *APIError
	if !errors.As(err, &api) || api.Status != 500 || !strings.Contains(err.Error(), "upstream down") {
		t.Fatalf("err %v", err)
	}
	f.fail["GET /sandbox/sb"] = []int{403}
	f.failMsg = `{"message":"Forbidden"}`
	if _, err := d.wait(ctxFor(t), Sandbox{ID: "sb", State: StateStarting}, StateStarted); !errors.As(err, &api) || api.Status != 403 {
		t.Fatalf("a refusal isn't retried: %v", err)
	}
}

func TestDaytonaGetUnknownSandbox(t *testing.T) {
	_, d := newFakeDaytona(t)
	_, err := d.Get(ctxFor(t), "nope")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err %v", err)
	}
	if _, err := d.Get(ctxFor(t), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty id: %v", err)
	}
	for _, err := range []error{d.Delete(ctxFor(t), ""), func() error { _, err := d.SSHAccess(ctxFor(t), ""); return err }()} {
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("empty id: %v", err)
		}
	}
}

func TestDaytonaEscapesTheID(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		fmt.Fprint(w, `{"id":"x","state":"started","token":"t"}`)
	}))
	defer srv.Close()
	t.Setenv("DAYTONA_API_KEY", "k")
	d := NewDaytona(config.DaytonaCfg{APIURL: srv.URL})
	_, _ = d.Get(ctxFor(t), "a/b")
	_ = d.Delete(ctxFor(t), "../x")
	_, _ = d.SSHAccess(ctxFor(t), "a?b")
	want := []string{"/sandbox/a%2Fb", "/sandbox/..%2Fx", "/sandbox/a%3Fb/ssh-access"}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Fatalf("paths %v, want %v", paths, want)
	}
}

func TestDaytonaRefusedKeySaysWhereItCameFrom(t *testing.T) {
	_, d := newFakeDaytona(t)
	d.key = "wrong-key"
	_, err := d.Get(ctxFor(t), "sb")
	if err == nil || !strings.Contains(err.Error(), "$DAYTONA_API_KEY") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(err.Error(), "wrong-key") {
		t.Fatalf("the key leaked into the error: %v", err)
	}
}

func TestDaytonaListPagesThroughConchsSandboxes(t *testing.T) {
	f, d := newFakeDaytona(t)
	f.pages = []string{
		// Seen for real: straight after a delete, a sandbox still says
		// started, with only its desired state giving it away.
		`{"items":[{"id":"a","state":"started","labels":{"conch":"1"}},{"id":"deleted","state":"started","desiredState":"destroyed"}],"nextCursor":"c1"}`,
		`{"items":[{"id":"b","state":"stopped","createdAt":"2026-09-25T10:00:00Z"}],"nextCursor":null}`,
	}
	got, err := d.List(ctxFor(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "a" || got[1].State != StateStopped || got[1].Created.IsZero() {
		t.Fatalf("got %+v", got)
	}
}

func TestDaytonaListQuery(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		switch r.URL.Query().Get("cursor") {
		case "":
			fmt.Fprint(w, `{"items":[{"id":"a"}],"nextCursor":"c1"}`)
		default:
			fmt.Fprint(w, `{"items":[],"nextCursor":""}`)
		}
	}))
	defer srv.Close()
	t.Setenv("DAYTONA_API_KEY", "k")
	d := NewDaytona(config.DaytonaCfg{APIURL: srv.URL})
	if _, err := d.List(ctxFor(t)); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 {
		t.Fatalf("queries %v", queries)
	}
	for _, q := range queries {
		if !strings.Contains(q, "labels=%7B%22conch%22%3A%221%22%7D") || !strings.Contains(q, "limit=200") {
			t.Fatalf("query %q must filter by conch's label", q)
		}
	}
	if !strings.Contains(queries[1], "cursor=c1") {
		t.Fatalf("second page without the cursor: %q", queries[1])
	}
}

func TestDaytonaListStopsOnARepeatedOrEndlessCursor(t *testing.T) {
	f, d := newFakeDaytona(t)
	f.pages = []string{`{"items":[{"id":"a"}],"nextCursor":"same"}`, `{"items":[{"id":"b"}],"nextCursor":"same"}`}
	got, err := d.List(ctxFor(t))
	if err != nil || len(got) != 2 {
		t.Fatalf("repeated cursor: %v %v", got, err)
	}

	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		fmt.Fprintf(w, `{"items":[],"nextCursor":"c%d"}`, n)
	}))
	defer srv.Close()
	d = NewDaytona(config.DaytonaCfg{APIURL: srv.URL})
	if _, err := d.List(ctxFor(t)); err == nil || n != maxPages {
		t.Fatalf("endless cursor: %d pages, err %v", n, err)
	}
}

func TestDaytonaListFailure(t *testing.T) {
	f, d := newFakeDaytona(t)
	f.fail["GET /sandbox"] = []int{500}
	if _, err := d.List(ctxFor(t)); err == nil || !strings.Contains(err.Error(), "list sandboxes") {
		t.Fatalf("err %v", err)
	}
	f.pages = []string{`{"items":[{"id":`}
	if _, err := d.List(ctxFor(t)); err == nil || !strings.Contains(err.Error(), "reading reply") {
		t.Fatalf("truncated reply: %v", err)
	}
}

func TestDaytonaStart(t *testing.T) {
	f, d := newFakeDaytona(t)
	f.add("up", StateStarted)
	if s, err := d.Start(ctxFor(t), "up"); err != nil || s.State != StateStarted {
		t.Fatalf("already started: %v %v", s, err)
	}
	f.add("down", StateStopped)
	f.then("down", StateStopped, StateStarting, StateStarted)
	if s, err := d.Start(ctxFor(t), "down"); err != nil || s.State != StateStarted {
		t.Fatalf("stopped: %v %v", s, err)
	}
	f.add("going", StateStopping, StateStopping, StateStopped, StateStarting, StateStarted)
	if _, err := d.Start(ctxFor(t), "going"); err != nil {
		t.Fatalf("stopping: %v", err)
	}
	f.add("coming", StateCreating, "pulling_snapshot", StateStarted)
	if _, err := d.Start(ctxFor(t), "coming"); err != nil {
		t.Fatalf("coming up: %v", err)
	}
	var starts []string
	for _, c := range f.called() {
		if strings.HasSuffix(c, "/start") {
			starts = append(starts, c)
		}
	}
	if strings.Join(starts, ",") != "POST /sandbox/down/start,POST /sandbox/going/start" {
		t.Fatalf("start sent for %v; not for one started or coming up", starts)
	}
	if _, err := d.Start(ctxFor(t), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	f.add("broken", StateStopped)
	f.fail["POST /sandbox/broken/start"] = []int{400}
	f.failMsg = `{"message":"Sandbox is in an invalid state"}`
	if _, err := d.Start(ctxFor(t), "broken"); err == nil || !strings.Contains(err.Error(), "start sandbox: daytona: Sandbox is in an invalid state (400)") {
		t.Fatalf("refused: %v", err)
	}
}

func TestDaytonaStop(t *testing.T) {
	f, d := newFakeDaytona(t)
	f.add("down", StateStopped)
	f.add("cold", StateArchived)
	f.add("up", StateStarted, StateStarted, StateStopping, StateStopped)
	f.add("going", StateStopping, StateStopping, StateStopped)
	for _, id := range []string{"down", "cold", "up", "going"} {
		if err := d.Stop(ctxFor(t), id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	var stops []string
	for _, c := range f.called() {
		if strings.HasSuffix(c, "/stop") {
			stops = append(stops, c)
		}
	}
	if strings.Join(stops, ",") != "POST /sandbox/up/stop" {
		t.Fatalf("stop sent for %v", stops)
	}
	if err := d.Stop(ctxFor(t), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	f.add("err", StateError)
	f.boxes["err"].sandbox.ErrorReason = "runner lost"
	if err := d.Stop(ctxFor(t), "err"); err == nil || !strings.Contains(err.Error(), "runner lost") {
		t.Fatalf("errored sandbox: %v", err)
	}
}

func TestDaytonaDelete(t *testing.T) {
	f, d := newFakeDaytona(t)
	f.add("sb", StateStarted)
	if err := d.Delete(ctxFor(t), "sb"); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(ctxFor(t), "sb"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestDaytonaSSHAccess(t *testing.T) {
	f, d := newFakeDaytona(t)
	f.add("sb", StateStarted)
	f.access = `{"token":"tok123","expiresAt":"2026-09-25T11:00:00Z","sshCommand":"ssh -p 2222 tok123@gw.example"}`
	a, err := d.SSHAccess(ctxFor(t), "sb")
	if err != nil {
		t.Fatal(err)
	}
	if a.User != "tok123" || a.Host != "gw.example" || a.Port != 2222 || a.Expires.IsZero() {
		t.Fatalf("access %+v", a)
	}
	if a.Target() != "ssh://tok123@gw.example:2222" {
		t.Fatalf("target %q", a.Target())
	}

	f.access = `{"token":"tok9"}`
	a, err = d.SSHAccess(ctxFor(t), "sb")
	if err != nil || a.Host != daytonaSSHHost || a.Port != 0 || a.Target() != "tok9@"+daytonaSSHHost {
		t.Fatalf("no command falls back to Daytona's gateway: %+v %v", a, err)
	}

	f.access = `{"token":""}`
	if _, err := d.SSHAccess(ctxFor(t), "sb"); err == nil || !strings.Contains(err.Error(), "no token") {
		t.Fatalf("empty token: %v", err)
	}
	f.access = ``
	if _, err := d.SSHAccess(ctxFor(t), "sb"); err == nil || !strings.Contains(err.Error(), "empty reply") {
		t.Fatalf("empty reply: %v", err)
	}
	if _, err := d.SSHAccess(ctxFor(t), "gone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestParseSSHCommand(t *testing.T) {
	for _, c := range []struct {
		cmd, host string
		port      int
		ok        bool
	}{
		{"ssh tok@ssh.app.daytona.io", "ssh.app.daytona.io", 0, true},
		{"ssh -p 2222 tok@localhost", "localhost", 2222, true},
		{"ssh tok@h -p 22", "h", 22, true},
		{"", "", 0, false},
		{"ssh", "", 0, false},
		{"scp tok@h", "", 0, false},
		{"ssh -p x tok@h", "", 0, false},
		{"ssh -p 70000 tok@h", "", 0, false},
		{"ssh other@h", "", 0, false},
		{"ssh tok@a/b", "", 0, false},
		{"ssh tok@", "", 0, false},
	} {
		host, port, ok := parseSSHCommand(c.cmd, "tok")
		if host != c.host || port != c.port || ok != c.ok {
			t.Errorf("%q: %q %d %v, want %q %d %v", c.cmd, host, port, ok, c.host, c.port, c.ok)
		}
	}
	if (Access{User: "t", Host: "h", Port: 22}).Target() != "t@h" {
		t.Error("port 22 needs no ssh:// form")
	}
}

func TestDaytonaErrorMessages(t *testing.T) {
	for _, c := range []struct{ body, want string }{
		{`{"message":"Sandbox not found"}`, "Sandbox not found"},
		{`{"message":["a","b"]}`, "a; b"},
		{`{"error":"Bad Request"}`, "Bad Request"},
		{`{"message":[]}`, ""},
		{`plain text`, "plain text"},
		{`<html><body>502</body></html>`, ""},
		{strings.Repeat("x", 300), ""},
		{``, ""},
	} {
		if got := errorMessage([]byte(c.body)); got != c.want {
			t.Errorf("%q: %q, want %q", c.body, got, c.want)
		}
	}
	e := &APIError{Status: 502, keyEnv: "K"}
	if e.Error() != "daytona: Bad Gateway (502)" || !e.temporary() {
		t.Errorf("empty message: %q", e.Error())
	}
	if (&APIError{Status: 404}).temporary() || !(&APIError{Status: 429}).temporary() {
		t.Error("temporary")
	}
}

func TestDaytonaRetryAfter(t *testing.T) {
	h := http.Header{}
	if retryAfter(h) != 0 {
		t.Fatal("none")
	}
	h.Set("Retry-After-sandbox-create", "3")
	h.Set("Retry-After", "bogus")
	if got := retryAfter(h); got != 3*time.Second {
		t.Fatalf("got %v", got)
	}
	h.Set("Retry-After", "3600")
	if got := retryAfter(h); got != time.Minute {
		t.Fatalf("capped: %v", got)
	}
	h = http.Header{"Retry-After": {"-4"}}
	if retryAfter(h) != 0 {
		t.Fatal("negative")
	}
}

func TestDaytonaRateLimitIsHonoured(t *testing.T) {
	f, d := newFakeDaytona(t)
	f.add("sb", StateStarting, StateStarted)
	f.fail["GET /sandbox/sb"] = []int{429}
	f.headers = map[string]string{"Retry-After-lifecycle": "1"}
	start := time.Now()
	if _, err := d.wait(ctxFor(t), Sandbox{ID: "sb", State: StateStarting}, StateStarted); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < time.Second {
		t.Fatalf("waited %v, not the second Daytona asked for", time.Since(start))
	}
}

func TestDaytonaOversizedReply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, io.LimitReader(neverEnding('x'), maxBody+10))
	}))
	defer srv.Close()
	t.Setenv("DAYTONA_API_KEY", "k")
	d := NewDaytona(config.DaytonaCfg{APIURL: srv.URL})
	if _, err := d.Get(ctxFor(t), "sb"); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("err %v", err)
	}
}

type neverEnding byte

func (b neverEnding) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(b)
	}
	return len(p), nil
}

func TestDaytonaUnreachable(t *testing.T) {
	t.Setenv("DAYTONA_API_KEY", "k")
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // nothing listens there now
	d := NewDaytona(config.DaytonaCfg{APIURL: srv.URL})
	if _, err := d.Get(ctxFor(t), "sb"); err == nil {
		t.Fatal("expected an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Get(ctx, "sb"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}
