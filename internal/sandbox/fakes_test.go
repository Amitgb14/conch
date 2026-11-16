package sandbox

// The fakes of the providers conch once had built in — boat.dev, Daytona,
// Tensorlake — kept to run their provider files (testdata/registry) and
// adapted ones against, as those providers' own tests once did. Each
// constructor returns the fake and its API's address.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// boatSandbox is boat.dev's sandbox, as far as conch reads it.
type boatSandbox struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	Error string `json:"error"`
	Type  string `json:"type"`
	VCPU  int    `json:"vcpu"`
	MemGB int    `json:"memoryGB"`
	IP    string `json:"ip"`
	// SSHEndpoint is host:port, set only when the machine has no public
	// IPv4 of its own. When it is there it is what to connect to.
	SSHEndpoint string `json:"sshEndpoint"`
	Subdomain   string `json:"subdomain"`
	CreatedAt   string `json:"createdAt"`
	SetupStatus string `json:"setupStatus"`
	SetupError  string `json:"setupError"`
}

// daytonaSandbox is the API's sandbox, as far as conch reads it.
type daytonaSandbox struct {
	ID string `json:"id"`
	// OrganizationID is which account it belongs to, which the analytics
	// host wants in its paths.
	OrganizationID string            `json:"organizationId"`
	Name           string            `json:"name"`
	State          State             `json:"state"`
	ErrorReason    string            `json:"errorReason"`
	Target         string            `json:"target"`
	CPU            int               `json:"cpu"`
	Memory         int               `json:"memory"`
	Disk           int               `json:"disk"`
	Labels         map[string]string `json:"labels"`
	CreatedAt      string            `json:"createdAt"`
	// DesiredState is where Daytona is taking it: "destroyed" for a
	// sandbox deleted moments ago that still reports its old state.
	DesiredState State `json:"desiredState"`
}

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

func newFakeBoat(t *testing.T) (*fakeBoat, string) {
	t.Helper()
	f := &fakeBoat{t: t, boxes: map[string]*boatSandbox{}, states: map[string][]string{},
		bodies: map[string][]byte{}, headers: map[string]http.Header{}, fail: map[string][]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("BOAT_API_KEY", "boat_secret")
	old, oldWait := pollEvery, waitUnit
	pollEvery, waitUnit = time.Millisecond, time.Millisecond/5
	t.Cleanup(func() { pollEvery, waitUnit = old, oldWait })
	return f, srv.URL
}

func (f *fakeBoat) state(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.boxes[id]; s != nil {
		return s.State
	}
	return "gone"
}

// The fake is read by the server's own goroutines, so a test changes it
// only through these.

func (f *fakeBoat) with(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
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

// fakeDaytona is Daytona's API in miniature. Each sandbox walks through
// the states queued for it, one per GET, and stays on the last.
type fakeDaytona struct {
	t  *testing.T
	mu sync.Mutex

	boxes   map[string]*fakeBox
	created []map[string]any  // create bodies
	calls   []string          // "METHOD path"
	pages   []string          // raw list replies, served in turn
	queries map[string]string // "METHOD path" → the last query string
	bodies  map[string][]byte // "METHOD path" → the last body sent
	usage   string            // the analytics host's reply
	fail    map[string][]int  // "METHOD path" → statuses to answer first
	failMsg string
	headers map[string]string // added to failure replies
	access  string            // raw ssh-access reply
	nextID  int
	// slow holds the first reply to a call back this long, as a busy
	// machine or network can; and served says how many have been answered.
	slow   map[string]time.Duration
	served map[string]int
}

type fakeBox struct {
	sandbox daytonaSandbox
	states  []State
}

func newFakeDaytona(t *testing.T) (*fakeDaytona, string) {
	t.Helper()
	f := &fakeDaytona{t: t, boxes: map[string]*fakeBox{}, fail: map[string][]int{}, queries: map[string]string{}, bodies: map[string][]byte{},
		slow: map[string]time.Duration{}, served: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("DAYTONA_API_KEY", "k-secret")
	old := pollEvery
	pollEvery = time.Millisecond
	t.Cleanup(func() { pollEvery = old })
	return f, srv.URL + "/api"
}

func (f *fakeDaytona) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api")
	f.mu.Lock()
	wait := f.slow[key]
	delete(f.slow, key)
	f.served[key]++
	f.mu.Unlock()
	time.Sleep(wait)
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := r.Header.Get("Authorization"); got != "Bearer k-secret" {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"statusCode":401,"message":"Unauthorized"}`)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api")
	f.calls = append(f.calls, key)
	f.queries[key] = r.URL.RawQuery
	if r.Body != nil {
		if b, err := io.ReadAll(io.LimitReader(r.Body, maxBody)); err == nil && len(b) > 0 {
			f.bodies[key] = b
			r.Body = io.NopCloser(bytes.NewReader(b)) // the handlers still read it
		}
	}
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
	case strings.HasSuffix(path, "/usage") && strings.HasPrefix(path, "/organization/"):
		fmt.Fprint(w, firstSet(f.usage, "[]"))
	case key == "GET /snapshots":
		reply(map[string]any{"items": []map[string]any{
			{"name": "older", "state": "active", "cpu": 1, "memory": 1, "disk": 3, "createdAt": "2026-09-20T10:00:00Z"},
			{"name": "newest", "state": "building", "createdAt": "2026-09-25T10:00:00Z"},
		}})
	case len(parts) == 2 && parts[0] == "snapshots" && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 3 && parts[0] == "sandbox" && parts[2] == "snapshot" && r.Method == http.MethodPost:
		if f.boxes[parts[1]] == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"statusCode":404,"message":"not found"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
	case len(parts) == 5 && parts[0] == "sandbox" && parts[2] == "ports" && parts[4] == "signed-preview-url":
		if f.boxes[parts[1]] == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"statusCode":404,"message":"Sandbox %s not found"}`, parts[1])
			return
		}
		reply(map[string]string{"url": "https://" + parts[3] + "-token.proxy.daytona.work", "token": "token"})
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

func newFakeTensorlake(t *testing.T) (*fakeTensorlake, string) {
	t.Helper()
	f := &fakeTensorlake{t: t, boxes: map[string]map[string]any{}, states: map[string][]string{}, fail: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("TENSORLAKE_API_KEY", "tl_secret")
	old := pollEvery
	pollEvery = time.Millisecond
	t.Cleanup(func() { pollEvery = old })
	return f, srv.URL
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

func (f *fakeTensorlake) with(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}
