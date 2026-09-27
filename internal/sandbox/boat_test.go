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

// fakeBoat is boat.dev's API, as far as conch uses it.
type fakeBoat struct {
	t  *testing.T
	mu sync.Mutex

	boxes   map[string]*boatSandbox
	states  map[string][]string // queued states, one per look
	created []map[string]any
	bodies  map[string][]byte
	headers map[string]http.Header
	calls   []string
	fail    map[string][]int
	failMsg string
	usage   string
	snaps   string
	pages   []string // pre-baked answers for GET /sandboxes, in order
	nextID  int
}

func newFakeBoat(t *testing.T) (*fakeBoat, *Boat) {
	t.Helper()
	f := &fakeBoat{t: t, boxes: map[string]*boatSandbox{}, states: map[string][]string{},
		bodies: map[string][]byte{}, headers: map[string]http.Header{}, fail: map[string][]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("BOAT_API_KEY", "boat_secret")
	t.Setenv("BOAT_API_URL", srv.URL)
	old := pollEvery
	pollEvery = time.Millisecond
	t.Cleanup(func() { pollEvery = old })
	return f, NewBoat(config.ProviderCfg{})
}

// add puts a sandbox in place that walks through states as it is looked at.
func (f *fakeBoat) add(id string, states ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.boxes[id] = &boatSandbox{ID: id, Name: id, State: states[0], Type: "default", VCPU: 4, MemGB: 8,
		IP: "203.0.113.5", Subdomain: "frazil-pneuma-rallye", CreatedAt: "2026-09-26T10:00:00Z"}
	f.states[id] = states[1:]
}

func (f *fakeBoat) state(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.boxes[id]; s != nil {
		return s.State
	}
	return "gone"
}

func (f *fakeBoat) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// The fake is read by the server's own goroutines, so a test changes it
// only through these.

func (f *fakeBoat) with(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// refuse makes the next call to key fail with status, answering msg.
func (f *fakeBoat) refuse(key, msg string, status ...int) {
	f.with(func() { f.failMsg, f.fail[key] = msg, status })
}

func (f *fakeBoat) allow() {
	f.with(func() { f.failMsg, f.fail = "", map[string][]int{} })
}

func (f *fakeBoat) body(key string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.bodies[key]...)
}

func (f *fakeBoat) header(key, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.headers[key].Get(name)
}

func (f *fakeBoat) creations() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.created...)
}

// edit changes a sandbox as boat would have it.
func (f *fakeBoat) edit(id string, fn func(*boatSandbox)) {
	f.with(func() {
		if s := f.boxes[id]; s != nil {
			fn(s)
		}
	})
}

func (f *fakeBoat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := r.Header.Get("Authorization"); got != "Bearer boat_secret" {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"ok":false,"error":{"code":"unauthorized","message":"Invalid token"}}`)
		return
	}
	key := r.Method + " " + r.URL.Path
	f.calls = append(f.calls, key)
	f.headers[key] = r.Header.Clone()
	if r.Body != nil {
		if b, _ := io.ReadAll(io.LimitReader(r.Body, maxBody)); len(b) > 0 {
			f.bodies[key] = b
		}
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
	case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
		var in map[string]any
		_ = json.Unmarshal(f.bodies[key], &in)
		f.created = append(f.created, in)
		f.nextID++
		id := fmt.Sprintf("bx_2345678%d", f.nextID)
		f.boxes[id] = &boatSandbox{ID: id, Name: id, State: "provisioning", Type: "default", VCPU: 4, MemGB: 8,
			IP: "203.0.113.5", CreatedAt: "2026-09-26T10:00:00Z"}
		f.states[id] = []string{"ready"}
		w.WriteHeader(http.StatusAccepted)
		reply(map[string]any{"ok": true, "type": "sandbox.created", "sandbox": f.boxes[id]})
	case r.Method == http.MethodGet && r.URL.Path == "/sandboxes":
		if len(f.pages) > 0 {
			page := f.pages[0]
			f.pages = f.pages[1:]
			fmt.Fprint(w, page)
			return
		}
		var list []boatSandbox
		for _, s := range f.boxes {
			list = append(list, *s)
		}
		reply(map[string]any{"ok": true, "type": "sandbox.list", "sandboxes": list,
			"pageInfo": map[string]any{"hasMore": false, "nextCursor": ""}})
	case r.Method == http.MethodGet && r.URL.Path == "/named-snapshots":
		if f.snaps == "" {
			f.snaps = `{"ok":true,"snapshots":[{"name":"web-stack","state":"ready","type":"default","createdAt":"2026-09-20T10:00:00Z"}]}`
		}
		fmt.Fprint(w, f.snaps)
	case r.Method == http.MethodPost && r.URL.Path == "/named-snapshots":
		var in map[string]any
		_ = json.Unmarshal(f.bodies[key], &in)
		id, _ := in["sandboxId"].(string)
		if f.boxes[id] == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"ok":false,"error":{"code":"not_found","message":"sandbox %s not found"}}`, id)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case len(parts) == 2 && parts[0] == "named-snapshots" && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	case len(parts) >= 2 && parts[0] == "sandboxes":
		id := parts[1]
		s := f.boxes[id]
		if s == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"ok":false,"error":{"code":"not_found","message":"sandbox %s not found"}}`, id)
			return
		}
		switch {
		case r.Method == http.MethodGet && len(parts) == 2:
			// Each look moves it on, as a machine coming up does.
			if next := f.states[id]; len(next) > 0 {
				s.State, f.states[id] = next[0], next[1:]
			}
			reply(map[string]any{"ok": true, "sandbox": s})
		case r.Method == http.MethodDelete && len(parts) == 2:
			delete(f.boxes, id)
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPost && len(parts) == 3:
			switch parts[2] {
			case "stop":
				s.State = "archiving"
				f.states[id] = []string{"archived"}
				w.WriteHeader(http.StatusAccepted)
				reply(map[string]any{"ok": true, "type": "sandbox.stopping", "sandbox": s})
			case "resume":
				s.State = "provisioning"
				s.IP = "203.0.113.9" // a resume lands on another machine
				f.states[id] = []string{"ready"}
				w.WriteHeader(http.StatusAccepted)
				reply(map[string]any{"ok": true, "sandbox": s})
			case "sshkey":
				reply(map[string]any{"ok": true, "machineIp": s.IP})
			case "host":
				var in map[string]any
				_ = json.Unmarshal(f.bodies[key], &in)
				reply(map[string]any{"ok": true, "url": fmt.Sprintf("https://%s-%v.on.boat.dev", s.Subdomain, in["port"])})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		case r.Method == http.MethodGet && len(parts) == 3 && parts[2] == "usage":
			if f.usage == "" {
				f.usage = `{"ok":true,"type":"sandbox.usage","seconds":4980,"dollars":0.0498,"running":false}`
			}
			fmt.Fprint(w, f.usage)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"ok":false,"error":{"code":"not_found","message":"no such route"}}`)
	}
}

func TestBoatNeedsAKey(t *testing.T) {
	t.Setenv("BOAT_API_KEY", "")
	t.Setenv("BOAT_API_URL", "")
	b := NewBoat(config.ProviderCfg{})
	err := b.Check()
	if !errors.Is(err, ErrNotConfigured) || !strings.Contains(err.Error(), "$BOAT_API_KEY") {
		t.Fatalf("no key: %v", err)
	}
	if b.base != DefaultBoatURL || b.Name() != "boat" {
		t.Fatalf("defaults: %q %q", b.base, b.Name())
	}
	t.Setenv("BOAT_API_KEY", "from-env")
	if got := NewBoat(config.ProviderCfg{APIKey: " from-settings "}); got.key != "from-settings" || !got.fromSettings {
		t.Fatalf("the settings key: %q", got.key)
	}
	if got := NewBoat(config.ProviderCfg{}); got.key != "from-env" {
		t.Fatalf("the environment key: %q", got.key)
	}
	// Nothing is asked of the API without one.
	if _, err := NewBoat(config.ProviderCfg{APIKeyEnv: "NOT_SET"}).Get(ctxFor(t), "bx_2345678a"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("called without a key: %v", err)
	}
	// What its refusals read as.
	for _, c := range []struct {
		status int
		want   string
	}{{401, "check the key in $BOAT_API_KEY"}, {402, "needs a plan"}, {404, "boat:"}} {
		e := &BoatError{Status: c.status, keyEnv: "BOAT_API_KEY", Message: "no"}
		if !strings.Contains(e.Error(), c.want) {
			t.Fatalf("%d reads %q", c.status, e.Error())
		}
	}
}

func TestBoatCreateAndList(t *testing.T) {
	f, b := newFakeBoat(t)
	ctx := ctxFor(t)

	s, err := b.Create(ctx, Spec{Env: map[string]string{"TOKEN": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	// Boat answers before the machine is up; conch waits for it.
	if s.State != StateStarted || s.CPU != 4 || s.Memory != 8 {
		t.Fatalf("created %+v", s)
	}
	body := f.creations()[0]
	if body["ttlSeconds"] != float64(boatTTL.Seconds()) {
		t.Fatalf("ttl %v", body["ttlSeconds"])
	}
	if env, _ := body["env"].(map[string]any); env["TOKEN"] != "x" {
		t.Fatalf("env %v", body["env"])
	}
	if _, named := body["type"]; named {
		t.Fatalf("a size nobody asked for: %v", body)
	}
	// A size is a type; anything else is a snapshot to deploy from.
	if _, err := b.Create(ctx, Spec{Snapshot: "large", AutoStop: 120}); err != nil {
		t.Fatal(err)
	}
	if body := f.creations()[1]; body["type"] != "large" || body["ttlSeconds"] != float64(7200) {
		t.Fatalf("a size: %v", body)
	}
	if _, err := b.Create(ctx, Spec{Snapshot: "web-stack"}); err != nil {
		t.Fatal(err)
	}
	if body := f.creations()[2]; body["from"] != "web-stack" || body["type"] != nil {
		t.Fatalf("a snapshot: %v", body)
	}

	// The list reads boat's states as conch's.
	f.add("bx_2345678z", "archived")
	list, err := b.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]State{}
	for _, s := range list {
		states[s.ID] = s.State
	}
	if states["bx_2345678z"] != StateStopped || len(states) != 4 {
		t.Fatalf("states %v", states)
	}
}

func TestBoatStartStopDelete(t *testing.T) {
	f, b := newFakeBoat(t)
	ctx := ctxFor(t)
	f.add("bx_23456781", "ready")
	f.add("bx_23456782", "archived")

	// Starting one that is up asks nothing more of the API.
	before := len(f.called())
	if s, err := b.Start(ctx, "bx_23456781"); err != nil || s.State != StateStarted {
		t.Fatalf("start a running one: %+v %v", s, err)
	}
	if got := f.called()[before:]; len(got) != 1 {
		t.Fatalf("start a running one asked %v", got)
	}
	// A stopped one is resumed, and waits until it is up again.
	s, err := b.Start(ctx, "bx_23456782")
	if err != nil || s.State != StateStarted {
		t.Fatalf("resume: %+v %v", s, err)
	}
	if !strings.Contains(strings.Join(f.called(), "\n"), "POST /sandboxes/bx_23456782/resume") {
		t.Fatalf("no resume: %v", f.called())
	}
	// A resume lands on another machine, so where to ssh is read again.
	a, err := b.SSHAccess(ctx, "bx_23456782")
	if err != nil || a.Host != "203.0.113.9" || a.User != boatUser || a.Port != 0 {
		t.Fatalf("after a resume: %+v %v", a, err)
	}

	// Stop waits for it to be archived.
	if err := b.Stop(ctx, "bx_23456781"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if f.state("bx_23456781") != "archived" {
		t.Fatalf("still %s", f.state("bx_23456781"))
	}
	if err := b.Stop(ctx, "bx_23456781"); err != nil { // already stopped
		t.Fatalf("stop again: %v", err)
	}

	// Delete repeats the id in the header boat asks for.
	if err := b.Delete(ctx, "bx_23456781"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := f.header("DELETE /sandboxes/bx_23456781", "X-Ascii-Confirm-Delete"); got != "bx_23456781" {
		t.Fatalf("confirmation header %q", got)
	}
	if err := b.Delete(ctx, "bx_23456781"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete an unknown one: %v", err)
	}
	if err := b.Delete(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete nothing: %v", err)
	}
}

func TestBoatSSHAccessAndKey(t *testing.T) {
	f, b := newFakeBoat(t)
	ctx := ctxFor(t)
	f.add("bx_23456781", "ready")

	// A key of your own is authorized; nothing secret comes back.
	if err := b.AuthorizeKey(ctx, "bx_23456781", " ssh-ed25519 AAAAC3Nz conch@mac \n"); err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	_ = json.Unmarshal(f.body("POST /sandboxes/bx_23456781/sshkey"), &sent)
	if sent["key"] != "ssh-ed25519 AAAAC3Nz conch@mac" {
		t.Fatalf("the key sent: %v", sent)
	}
	if err := b.AuthorizeKey(ctx, "bx_23456781", "  "); err == nil || !strings.Contains(err.Error(), "no public key") {
		t.Fatalf("no key: %v", err)
	}
	if err := b.AuthorizeKey(ctx, "", "ssh-ed25519 x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no sandbox: %v", err)
	}

	// The address is the machine's own, as user.
	a, err := b.SSHAccess(ctx, "bx_23456781")
	if err != nil || a.User != boatUser || a.Host != "203.0.113.5" || a.Port != 0 {
		t.Fatalf("access %+v %v", a, err)
	}
	// A machine with no public IPv4 is reached through a forwarder.
	f.edit("bx_23456781", func(s *boatSandbox) { s.SSHEndpoint = "203.0.113.10:22001" })
	a, err = b.SSHAccess(ctx, "bx_23456781")
	if err != nil || a.Host != "203.0.113.10" || a.Port != 22001 {
		t.Fatalf("through a forwarder: %+v %v", a, err)
	}
	if got := a.Target(); got != "ssh://user@203.0.113.10:22001" {
		t.Fatalf("target %q", got)
	}
	// An endpoint conch cannot read, and a machine with no address yet.
	f.edit("bx_23456781", func(s *boatSandbox) { s.SSHEndpoint = "nonsense" })
	if _, err := b.SSHAccess(ctx, "bx_23456781"); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("a bad endpoint: %v", err)
	}
	f.edit("bx_23456781", func(s *boatSandbox) { s.SSHEndpoint, s.IP = "", "" })
	if _, err := b.SSHAccess(ctx, "bx_23456781"); err == nil || !strings.Contains(err.Error(), "no address yet") {
		t.Fatalf("no address: %v", err)
	}
	if _, err := b.SSHAccess(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no id: %v", err)
	}
}

func TestBoatPreviewUsageAndSnapshots(t *testing.T) {
	f, b := newFakeBoat(t)
	ctx := ctxFor(t)
	f.add("bx_23456781", "ready")

	// A port is hosted and the link comes back.
	got, err := b.PreviewURL(ctx, "bx_23456781", 3000, time.Hour)
	if err != nil || got != "https://frazil-pneuma-rallye-3000.on.boat.dev" {
		t.Fatalf("link %q %v", got, err)
	}
	if _, err := b.PreviewURL(ctx, "bx_23456781", 0, time.Hour); err == nil || !strings.Contains(err.Error(), "1 to 65535") {
		t.Fatalf("a port that isn't one: %v", err)
	}

	// What it has cost, as one stretch: boat gives a total, not periods.
	u, err := b.Usage(ctx, "bx_23456781", time.Now().Add(-24*time.Hour), time.Now())
	if err != nil || !u.Known || fmt.Sprintf("%.4f", u.Cost) != "0.0498" {
		t.Fatalf("usage %+v %v", u, err)
	}
	if len(u.Periods) != 1 || u.Periods[0].To.Sub(u.Periods[0].From) != 4980*time.Second {
		t.Fatalf("periods %+v", u.Periods)
	}
	f.with(func() { f.usage = `{"ok":true,"seconds":0,"dollars":0,"running":false}` })
	if u, err := b.Usage(ctx, "bx_23456781", time.Now().Add(-time.Hour), time.Now()); err != nil || u.Known {
		t.Fatalf("nothing to report: %+v %v", u, err)
	}

	// Snapshots: keep one, list them, forget one.
	if err := b.Snapshot(ctx, "bx_23456781", " web-stack "); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal(f.body("POST /named-snapshots"), &body)
	if body["name"] != "web-stack" || body["sandboxId"] != "bx_23456781" {
		t.Fatalf("snapshot body %v", body)
	}
	if err := b.Snapshot(ctx, "bx_23456781", " "); err == nil || !strings.Contains(err.Error(), "needs a name") {
		t.Fatalf("no name: %v", err)
	}
	list, err := b.Snapshots(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "web-stack" || list[0].State != "ready" {
		t.Fatalf("snapshots %+v %v", list, err)
	}
	if err := b.ForgetSnapshot(ctx, "web-stack"); err != nil {
		t.Fatal(err)
	}
	if err := b.ForgetSnapshot(ctx, " "); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no name: %v", err)
	}
}

// The pages of a long list are followed, and a list that never ends stops.
func TestBoatListPages(t *testing.T) {
	f, b := newFakeBoat(t)
	page := func(id, next string, more bool) string {
		return fmt.Sprintf(`{"ok":true,"sandboxes":[{"id":%q,"state":"ready"}],"pageInfo":{"hasMore":%v,"nextCursor":%q}}`, id, more, next)
	}
	f.with(func() { f.pages = []string{page("bx_1", "c2", true), page("bx_2", "", false)} })
	list, err := b.List(ctxFor(t))
	if err != nil || len(list) != 2 || list[1].ID != "bx_2" {
		t.Fatalf("two pages: %+v %v", list, err)
	}
	if got := f.called()[1]; !strings.Contains(got, "/sandboxes") {
		t.Fatalf("second page not asked for: %v", f.called())
	}
	// A cursor that repeats itself is the end, not a loop.
	f.with(func() { f.pages = []string{page("bx_1", "c2", true), page("bx_2", "c2", true)} })
	if list, err := b.List(ctxFor(t)); err != nil || len(list) != 2 {
		t.Fatalf("a repeating cursor: %+v %v", list, err)
	}
	// A page that always says there is another gives up, with what it has.
	f.with(func() {
		for i := 0; i < maxPages+1; i++ {
			f.pages = append(f.pages, page(fmt.Sprintf("bx_%d", i), fmt.Sprintf("c%d", i), true))
		}
	})
	list, err = b.List(ctxFor(t))
	if err == nil || !strings.Contains(err.Error(), "more than") || len(list) != maxPages {
		t.Fatalf("no end: %d %v", len(list), err)
	}
}

func TestBoatGoesWrong(t *testing.T) {
	f, b := newFakeBoat(t)
	ctx := ctxFor(t)

	// A create boat refuses says why, in boat's own words.
	f.refuse("POST /sandboxes", `{"ok":false,"error":{"code":"quota_exceeded","message":"Too many sandboxes"}}`, http.StatusPaymentRequired)
	_, err := b.Create(ctx, Spec{})
	if err == nil || !strings.Contains(err.Error(), "Too many sandboxes") || !strings.Contains(err.Error(), "needs a plan") {
		t.Fatalf("a refused create: %v", err)
	}
	// One that answers but names no sandbox.
	f.allow()
	f.with(func() { f.pages, f.snaps = nil, `{"ok":true,"snapshots":[]}` })
	// A sandbox that comes up broken is a failure, with the reason it gave.
	f.add("bx_23456781", "provisioning", "error")
	f.edit("bx_23456781", func(s *boatSandbox) { s.Error = "no capacity" })
	if _, err := b.Start(ctx, "bx_23456781"); err == nil || !strings.Contains(err.Error(), "no capacity") {
		t.Fatalf("a broken sandbox: %v", err)
	}
	var ferr *FailedError
	if _, err := b.wait(ctx, Sandbox{ID: "x", State: StateError, Reason: "nope"}, StateStarted); !errors.As(err, &ferr) {
		t.Fatalf("wait on a broken one: %v", err)
	}
	// Waiting for one that never gets there ends with the context, saying
	// what it was still doing.
	dead, cancel := contextWithDeadline(t)
	defer cancel()
	f.add("bx_23456782", "provisioning")
	if _, err := b.wait(dead, Sandbox{ID: "bx_23456782", State: StateStarting}, StateStarted); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a sandbox that never comes up: %v", err)
	}
	// Whichever call the deadline lands in, it says what it was waiting for.
	done, stop := context.WithCancel(context.Background())
	stop()
	if _, err := b.wait(done, Sandbox{ID: "bx_23456782", State: StateStarting}, StateStarted); err == nil ||
		!strings.Contains(err.Error(), "is still starting") {
		t.Fatalf("a wait that was cancelled: %v", err)
	}

	// Anything asked of an unknown sandbox is not-found, not a crash.
	for name, call := range map[string]func() error{
		"get":      func() error { _, err := b.Get(ctx, "bx_nope"); return err },
		"start":    func() error { _, err := b.Start(ctx, "bx_nope"); return err },
		"stop":     func() error { return b.Stop(ctx, "bx_nope") },
		"ssh":      func() error { _, err := b.SSHAccess(ctx, "bx_nope"); return err },
		"key":      func() error { return b.AuthorizeKey(ctx, "bx_nope", "ssh-ed25519 x") },
		"preview":  func() error { _, err := b.PreviewURL(ctx, "bx_nope", 3000, 0); return err },
		"usage":    func() error { _, err := b.Usage(ctx, "bx_nope", time.Now().Add(-time.Hour), time.Now()); return err },
		"snapshot": func() error { return b.Snapshot(ctx, "bx_nope", "keep") },
		"forget":   func() error { return b.ForgetSnapshot(ctx, "gone") },
	} {
		if name == "forget" {
			f.refuse("DELETE /named-snapshots/gone", "", http.StatusNotFound)
		}
		if err := call(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s of an unknown sandbox: %v", name, err)
		}
	}

	// A delete that fails for another reason says so, and one that fails
	// because it has already gone is not-found.
	f.add("bx_23456783", "ready")
	f.refuse("DELETE /sandboxes/bx_23456783", `{"ok":false,"error":{"code":"conflict","message":"still archiving"}}`, http.StatusConflict)
	if err := b.Delete(ctx, "bx_23456783"); err == nil || !strings.Contains(err.Error(), "still archiving") {
		t.Fatalf("a refused delete: %v", err)
	}

	// A reply that isn't JSON at all is passed on as it came.
	f.refuse("GET /sandboxes", "<html>bad gateway</html>", http.StatusBadGateway)
	if _, err := b.List(ctx); err == nil || !strings.Contains(err.Error(), "bad gateway") {
		t.Fatalf("an answer that isn't JSON: %v", err)
	}
	// One too big to read is refused rather than held in memory.
	f.refuse("GET /sandboxes", strings.Repeat("x", maxBody+1), http.StatusOK)
	if _, err := b.List(ctx); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("an oversized answer: %v", err)
	}
	// A key boat doesn't know.
	f.allow()
	bad := NewBoat(config.ProviderCfg{APIKey: "wrong", APIURL: b.base})
	if _, err := bad.List(ctx); err == nil || !strings.Contains(err.Error(), "check the key in $BOAT_API_KEY") {
		t.Fatalf("a key boat doesn't know: %v", err)
	}
}

func contextWithDeadline(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 30*time.Millisecond)
}

// Boat's machines come in sizes, so a number of CPUs is refused rather
// than quietly dropped.
func TestBoatSizesNotNumbers(t *testing.T) {
	f, b := newFakeBoat(t)
	for _, spec := range []Spec{{CPU: 4}, {Memory: 8}, {Disk: 20}} {
		_, err := b.Create(ctxFor(t), spec)
		if err == nil || !strings.Contains(err.Error(), "come in sizes") || !strings.Contains(err.Error(), "xlarge") {
			t.Fatalf("%+v: %v", spec, err)
		}
	}
	if got := f.creations(); len(got) != 0 {
		t.Fatalf("boat was asked anyway: %v", got)
	}
}
