package cloud

import (
	"bytes"
	"context"
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

	"github.com/coder/websocket"
	"github.com/flynn/noise"

	"github.com/Amitgb14/conch/internal/cloudagent"
	"github.com/Amitgb14/conch/internal/cloudcontract"
	"github.com/Amitgb14/conch/internal/relay"
)

func TestMain(m *testing.M) {
	// No test reaches a real Keychain: the default is nil in a test
	// binary already; this says it out loud.
	SetKeychain(nil)
	pollUnit, waitPoll, eventsRetry = time.Millisecond, 10*time.Millisecond, 10*time.Millisecond
	os.Exit(m.Run())
}

// isolate keeps a test off the developer's conch: its own CONCH_HOME and
// HOME, and no inherited session.
func isolate(t *testing.T) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("CONCH_HOME", filepath.Join(dir, "conch"))
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv(URLEnv, "")
	return dir
}

// memKeychain is a Keychain in memory.
type memKeychain struct {
	mu    sync.Mutex
	items map[string]string
}

func (k *memKeychain) Get(a string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.items[a]
	if !ok {
		return "", fmt.Errorf("no item")
	}
	return v, nil
}

func (k *memKeychain) Set(a, s string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.items == nil {
		k.items = map[string]string{}
	}
	k.items[a] = s
	return nil
}

func (k *memKeychain) Delete(a string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.items, a)
	return nil
}

// ---- the fake control plane ----

// fake is the control plane's client side on httptest: device flow,
// tokens that rotate (a reused refresh token revokes the device, as the
// real one does), machines, connect with single-use tickets, and a relay
// that checks them. Every request and answer is checked against the
// contract when it is there.
type fake struct {
	t    *testing.T
	srv  *httptest.Server
	spec *cloudcontract.Spec

	mu         sync.Mutex
	forbidden  bool // any request fails the test
	badTickets bool // the relay refuses every ticket
	codes      map[string]*deviceCode
	refresh    map[string]string // current refresh token → device
	prevRefr   map[string]string // the one before, for reuse
	access     map[string]accessTok
	revoked    map[string]bool
	accessTTL  int
	machines   map[string]*Machine
	order      []string
	tickets    map[string]string // ticket → room
	calls      map[string]int
	keys       map[string]string // machine → its server key
	rooms      map[string]string // machine → room
	n          int

	// relay is the relay the fake's connect names, with its ticket check.
	relay      *httptest.Server
	relayInner *relay.Server
}

type deviceCode struct {
	user     string
	state    string // pending, approved, denied
	interval int
	slowDown int // answer slow_down this many more times
	device   string
}

type accessTok struct {
	device  string
	expires time.Time
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{t: t, codes: map[string]*deviceCode{}, refresh: map[string]string{}, prevRefr: map[string]string{},
		access: map[string]accessTok{}, revoked: map[string]bool{}, accessTTL: 3600, machines: map[string]*Machine{},
		tickets: map[string]string{}, calls: map[string]int{}, keys: map[string]string{}, rooms: map[string]string{}}
	var why string
	f.spec, why = cloudcontract.Load(Contract)
	if f.spec == nil {
		t.Logf("contract checks skipped: %s", why)
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	f.relayInner = &relay.Server{}
	f.relay = httptest.NewServer(http.HandlerFunc(f.serveRelay))
	t.Cleanup(f.relay.Close)
	t.Setenv(URLEnv, f.srv.URL)
	return f
}

func (f *fake) URL() string { return f.srv.URL }

func (f *fake) id(prefix string) string {
	f.n++
	return fmt.Sprintf("%s_%016d", prefix, f.n)
}

func (f *fake) count(route string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[route]
}

func (f *fake) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

// addMachine adds a running machine whose gateway uses key.
func (f *fake) addMachine(name, state, key, room string) *Machine {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := &Machine{ID: f.id("m"), Name: name, Size: "standard", Region: "us-west", State: state,
		Sleep: SleepPolicy{IdleSeconds: 600}, CreatedAt: time.Now().UTC().Truncate(time.Second), ServerKey: key}
	f.machines[m.ID] = m
	f.order = append(f.order, m.ID)
	f.keys[m.ID], f.rooms[m.ID] = key, room
	return m
}

func (f *fake) setMachine(id string, fn func(m *Machine)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.machines[id])
}

// approve approves the newest device code, as a person would.
func (f *fake) approve(state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.codes {
		c.state = state
	}
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	route := r.Method + " " + r.URL.Path
	f.mu.Lock()
	f.calls[route]++
	forbidden := f.forbidden
	f.mu.Unlock()
	if forbidden {
		f.t.Errorf("a request with no account: %s", route)
	}
	if f.spec != nil {
		for _, e := range f.spec.CheckRequest(r.Method, r.URL.Path, r.Header, body) {
			f.t.Errorf("request breaks the contract: %s", e)
		}
	}
	rec := httptest.NewRecorder()
	r.Body = io.NopCloser(bytes.NewReader(body))
	f.handle(rec, r)
	if f.spec != nil {
		for _, e := range f.spec.CheckResponse(r.Method, r.URL.Path, rec.Code, rec.Header(), rec.Body.Bytes()) {
			f.t.Errorf("the fake's answer breaks the contract: %s", e)
		}
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	w.Write(rec.Body.Bytes())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "detail": detail})
}

// device is who a request's bearer token belongs to, or "" after
// answering 401.
func (f *fake) device(w http.ResponseWriter, r *http.Request) string {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	a, ok := f.access[tok]
	if !ok || f.revoked[a.device] || time.Now().After(a.expires) {
		writeProblem(w, 401, "unauthorized", "The access token is not valid.")
		return ""
	}
	return a.device
}

func (f *fake) mint(w http.ResponseWriter, device string) {
	access, refresh := "cca_"+f.id("a"), "ccr_"+f.id("r")
	f.access[access] = accessTok{device: device, expires: time.Now().Add(time.Duration(f.accessTTL) * time.Second)}
	f.refresh[refresh] = device
	writeJSON(w, 200, map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": f.accessTTL, "device_id": device})
}

func (f *fake) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var in map[string]any
	json.NewDecoder(r.Body).Decode(&in)
	str := func(k string) string { s, _ := in[k].(string); return s }
	path := r.URL.Path
	switch {
	case r.Method == "POST" && path == "/v1/auth/device":
		code := "dc_" + f.id("c")
		user := fmt.Sprintf("ABCD-%04d", f.n%10000)
		f.codes[code] = &deviceCode{user: user, state: "pending", interval: 1}
		writeJSON(w, 200, map[string]any{"device_code": code, "user_code": user, "verification_uri": f.srv.URL + "/device",
			"verification_uri_complete": f.srv.URL + "/device?code=" + user, "expires_in": 600, "interval": 1})
	case r.Method == "POST" && path == "/v1/auth/token" && str("grant_type") == "device_code":
		c := f.codes[str("device_code")]
		switch {
		case c == nil:
			writeProblem(w, 400, "expired_token", "Unknown or expired device code; start again.")
		case c.slowDown > 0:
			c.slowDown--
			writeProblem(w, 400, "slow_down", "Polling too fast.")
		case c.state == "denied":
			writeProblem(w, 400, "access_denied", "The sign-in was denied.")
		case c.state == "suspended":
			writeProblem(w, 403, "suspended", "This account is suspended.")
		case c.state != "approved":
			writeProblem(w, 400, "authorization_pending", "Waiting for the person to approve.")
		default:
			delete(f.codes, str("device_code"))
			f.mint(w, f.id("d"))
		}
	case r.Method == "POST" && path == "/v1/auth/token" && str("grant_type") == "refresh_token":
		tok := str("refresh_token")
		dev, ok := f.refresh[tok]
		if !ok || f.revoked[dev] {
			if leaked, reused := f.prevRefr[tok]; reused {
				f.revoked[leaked] = true
			}
			writeProblem(w, 400, "expired_token", "The refresh token is invalid, expired or already used; sign in again.")
			return
		}
		delete(f.refresh, tok)
		f.prevRefr[tok] = dev
		f.mint(w, dev)
	case r.Method == "POST" && path == "/v1/auth/revoke":
		if dev := f.device(w, r); dev != "" {
			f.revoked[dev] = true
			w.WriteHeader(204)
		}
	case r.Method == "GET" && path == "/v1/me":
		if f.device(w, r) != "" {
			writeJSON(w, 200, map[string]any{"id": "u_1", "handle": "ada", "plan": "pro", "allowance": map[string]any{
				"running_seconds": 360000, "running_seconds_used": 7200, "storage_gb": 100, "storage_gb_used": 12.5, "max_machines": 3,
				"period_end": "2026-12-01T00:00:00Z"}})
		}
	case r.Method == "GET" && path == "/v1/devices":
		if dev := f.device(w, r); dev != "" {
			writeJSON(w, 200, map[string]any{"devices": []any{map[string]any{"id": dev, "name": "laptop", "client": "cli",
				"created_at": "2026-11-10T10:00:00Z", "last_seen_at": "2026-11-10T11:00:00Z", "current": true}}})
		}
	case r.Method == "GET" && path == "/v1/machines":
		if f.device(w, r) != "" {
			list := []any{}
			for _, id := range f.order {
				if m := f.machines[id]; m != nil {
					list = append(list, m)
				}
			}
			writeJSON(w, 200, map[string]any{"machines": list})
		}
	case r.Method == "POST" && path == "/v1/machines":
		if f.device(w, r) != "" {
			if len(f.machines) >= 3 {
				writeProblem(w, 402, "plan_limit", "Your plan allows 3 machines.")
				return
			}
			m := &Machine{ID: f.id("m"), Name: str("name"), Size: str("size"), Region: "us-west", State: StateCreating,
				Sleep: SleepPolicy{IdleSeconds: 600}, CreatedAt: time.Now().UTC().Truncate(time.Second)}
			f.machines[m.ID] = m
			f.order = append(f.order, m.ID)
			writeJSON(w, 201, m)
		}
	case strings.HasPrefix(path, "/v1/machines/"):
		if f.device(w, r) == "" {
			return
		}
		rest := strings.TrimPrefix(path, "/v1/machines/")
		id, action, _ := strings.Cut(rest, "/")
		m := f.machines[id]
		if m == nil {
			writeProblem(w, 404, "not_found", "No such machine.")
			return
		}
		switch r.Method + " " + action {
		case "GET ":
			writeJSON(w, 200, m)
		case "DELETE ":
			delete(f.machines, id)
			w.WriteHeader(202)
		case "PATCH ":
			if n := str("name"); n != "" {
				m.Name = n
			}
			writeJSON(w, 200, m)
		case "POST wake":
			if m.State == StateAsleep {
				m.State = StateWaking
			}
			writeJSON(w, 200, m)
		case "POST sleep":
			if in["force"] != true && m.Activity != nil && len(m.Activity.Agents) > 0 {
				writeProblem(w, 409, "agents_working", "An agent is working.")
				return
			}
			m.State = StateAsleep
			writeJSON(w, 200, m)
		case "POST connect":
			if m.State == StateAsleep {
				m.State = StateWaking
			}
			ticket := "cct_" + f.id("t")
			f.tickets[ticket] = f.rooms[id]
			writeJSON(w, 200, map[string]any{"relay_url": "ws" + strings.TrimPrefix(f.relay.URL, "http"), "room": f.rooms[id],
				"ticket": ticket, "server_key": f.keys[id], "expires_at": time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339), "state": m.State})
		default:
			writeProblem(w, 404, "not_found", "No such route in the fake.")
		}
	default:
		writeProblem(w, 404, "not_found", "No such route in the fake.")
	}
}

// serveRelay is conch's relay with the cloud relay's ticket check in
// front of the client's door (PROTOCOL §3.3): a ticket the fake handed
// out, once, for this room — anything else is closed with 4401.
func (f *fake) serveRelay(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == relay.PhonePath {
		t := r.Header.Get(relay.TicketHeader)
		if t == "" {
			t = r.URL.Query().Get("ticket")
		}
		f.mu.Lock()
		room, ok := f.tickets[t]
		delete(f.tickets, t)
		ok = ok && !f.badTickets
		f.mu.Unlock()
		if !ok || room != r.URL.Query().Get("room") {
			c, err := websocket.Accept(w, r, nil)
			if err == nil {
				c.Close(relay.CodeBadTicket, "bad ticket")
			}
			return
		}
	}
	f.relayInner.ServeHTTP(w, r)
}

// gateway is a machine's side: cloudagent's tunnel handler behind a link
// to the fake's relay, with the conch server socket given.
type gateway struct {
	key    string // its public key, base64url
	room   string
	dh     noise.DHKey
	secret string
	cancel context.CancelFunc
	done   chan struct{}
}

func (f *fake) startGateway(t *testing.T, serverSocket string) *gateway {
	t.Helper()
	key, err := relay.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := relay.NewSecret()
	return f.joinGateway(t, serverSocket, key, secret)
}

// joinGateway is a gateway with this key in this room: the same machine
// coming back.
func (f *fake) joinGateway(t *testing.T, serverSocket string, key noise.DHKey, secret string) *gateway {
	t.Helper()
	h, err := cloudagent.TunnelHandler(cloudagent.Options{ServerSocket: serverSocket, Logf: t.Logf, BootPath: "/nonexistent", StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &relay.Link{Relay: "ws" + strings.TrimPrefix(f.relay.URL, "http"), Secret: secret, Key: key, Handler: h, Logf: func(string, ...any) {}}
	g := &gateway{key: relay.EncodeKey(key.Public), room: relay.RoomID(secret), cancel: cancel, done: make(chan struct{}), dh: key, secret: secret}
	go func() { defer close(g.done); l.Run(ctx) }()
	t.Cleanup(g.stop)
	// Wait until it has joined the room.
	deadline := time.Now().Add(10 * time.Second)
	for l.Status().Connected == false {
		if time.Now().After(deadline) {
			t.Fatal("the gateway never joined the relay")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return g
}

func (g *gateway) stop() {
	g.cancel()
	<-g.done
}
