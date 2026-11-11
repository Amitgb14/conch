// Package cloudtest is a fake Conch Cloud control plane for conch's
// tests: the client side of the contract on httptest — the device flow,
// tokens that rotate (a reused refresh token revokes the device, as the
// real one does), machines, connect with single-use tickets — and a relay
// that checks those tickets in front of conch's own. Every request and
// answer is checked against the contract when a conch-cloud checkout is
// beside this one (internal/cloudcontract). Tests import it; nothing
// conch ships does.
package cloudtest

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

	"github.com/coder/websocket"
	"github.com/flynn/noise"

	"github.com/Amitgb14/conch/internal/cloudagent"
	"github.com/Amitgb14/conch/internal/cloudcontract"
	"github.com/Amitgb14/conch/internal/relay"
)

// Contract is the version the fake checks against.
const Contract = "1.4.0"

// URLEnv is internal/cloud's, set to the fake's address.
const URLEnv = "CONCH_CLOUD_URL"

// Machine is a machine as the fake keeps and answers it.
type Machine struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Size        string         `json:"size"`
	Region      string         `json:"region"`
	State       string         `json:"state"`
	StateDetail string         `json:"state_detail,omitempty"`
	Sleep       map[string]int `json:"sleep"`
	ServerKey   string         `json:"server_key,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Activity    *Activity      `json:"activity,omitempty"`
}

// Activity is a machine's last activity report.
type Activity struct {
	Agents          []AgentActivity `json:"agents"`
	ClientsAttached int             `json:"clients_attached"`
	ReportedAt      time.Time       `json:"reported_at"`
}

// AgentActivity is one agent in it.
type AgentActivity struct {
	Agent string `json:"agent"`
	State string `json:"state"`
}

// Fake is the control plane's client side on httptest: device flow,
// tokens that rotate (a reused refresh token revokes the device, as the
// real one does), machines, connect with single-use tickets, and a relay
// that checks them. Every request and answer is checked against the
// contract when it is there.
type Fake struct {
	t    *testing.T
	Srv  *httptest.Server
	Spec *cloudcontract.Spec

	Mu         sync.Mutex // held while changing the fields below
	Forbidden  bool       // any request fails the test
	BadTickets bool       // the relay refuses every ticket
	Codes      map[string]*DeviceCode
	refreshTok map[string]string // current refresh token → device
	prevRefr   map[string]string // the one before, for reuse
	Access     map[string]AccessTok
	Revoked    map[string]bool
	AccessTTL  int
	Machines   map[string]*Machine
	order      []string
	tickets    map[string]string // ticket → room
	calls      map[string]int
	Keys       map[string]string // machine → its server key
	Rooms      map[string]string // machine → room
	n          int

	// relay is the relay the fake's connect names, with its ticket check.
	Relay      *httptest.Server
	relayInner *relay.Server
}

type DeviceCode struct {
	User     string
	State    string // pending, approved, denied, suspended
	Interval int
	SlowDown int // answer slow_down this many more times
}

type AccessTok struct {
	device  string
	expires time.Time
}

func New(t *testing.T) *Fake {
	t.Helper()
	f := &Fake{t: t, Codes: map[string]*DeviceCode{}, refreshTok: map[string]string{}, prevRefr: map[string]string{},
		Access: map[string]AccessTok{}, Revoked: map[string]bool{}, AccessTTL: 3600, Machines: map[string]*Machine{},
		tickets: map[string]string{}, calls: map[string]int{}, Keys: map[string]string{}, Rooms: map[string]string{}}
	var why string
	f.Spec, why = cloudcontract.Load(Contract)
	if f.Spec == nil {
		t.Logf("contract checks skipped: %s", why)
	}
	f.Srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Srv.Close)
	f.relayInner = &relay.Server{}
	f.Relay = httptest.NewServer(http.HandlerFunc(f.serveRelay))
	t.Cleanup(f.Relay.Close)
	t.Setenv(URLEnv, f.Srv.URL)
	return f
}

func (f *Fake) URL() string { return f.Srv.URL }

func (f *Fake) id(prefix string) string {
	f.n++
	return fmt.Sprintf("%s_%016d", prefix, f.n)
}

func (f *Fake) Count(route string) int {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return f.calls[route]
}

func (f *Fake) Total() int {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

// addMachine adds a running machine whose gateway uses key.
func (f *Fake) AddMachine(name, state, key, room string) *Machine {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	m := &Machine{ID: f.id("m"), Name: name, Size: "standard", Region: "us-west", State: state,
		Sleep: map[string]int{"idle_seconds": 600}, CreatedAt: time.Now().UTC().Truncate(time.Second), ServerKey: key}
	f.Machines[m.ID] = m
	f.order = append(f.order, m.ID)
	f.Keys[m.ID], f.Rooms[m.ID] = key, room
	return m
}

func (f *Fake) SetMachine(id string, fn func(m *Machine)) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	fn(f.Machines[id])
}

// approve approves the newest device code, as a person would.
func (f *Fake) Approve(state string) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, c := range f.Codes {
		c.State = state
	}
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	route := r.Method + " " + r.URL.Path
	f.Mu.Lock()
	f.calls[route]++
	forbidden := f.Forbidden
	f.Mu.Unlock()
	if forbidden {
		f.t.Errorf("a request with no account: %s", route)
	}
	if f.Spec != nil {
		for _, e := range f.Spec.CheckRequest(r.Method, r.URL.Path, r.Header, body) {
			f.t.Errorf("request breaks the contract: %s", e)
		}
	}
	rec := httptest.NewRecorder()
	r.Body = io.NopCloser(bytes.NewReader(body))
	f.handle(rec, r)
	if f.Spec != nil {
		for _, e := range f.Spec.CheckResponse(r.Method, r.URL.Path, rec.Code, rec.Header(), rec.Body.Bytes()) {
			f.t.Errorf("the fake's answer breaks the contract: %s", e)
		}
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	w.Write(rec.Body.Bytes())
}

// WriteJSON answers v as JSON.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// WriteProblem answers a problem+json.
func WriteProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "detail": detail})
}

// device is who a request's bearer token belongs to, or "" after
// answering 401.
func (f *Fake) device(w http.ResponseWriter, r *http.Request) string {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	a, ok := f.Access[tok]
	if !ok || f.Revoked[a.device] || time.Now().After(a.expires) {
		WriteProblem(w, 401, "unauthorized", "The access token is not valid.")
		return ""
	}
	return a.device
}

func (f *Fake) mint(w http.ResponseWriter, device string) {
	access, refresh := "cca_"+f.id("a"), "ccr_"+f.id("r")
	f.Access[access] = AccessTok{device: device, expires: time.Now().Add(time.Duration(f.AccessTTL) * time.Second)}
	f.refreshTok[refresh] = device
	WriteJSON(w, 200, map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": f.AccessTTL, "device_id": device})
}

func (f *Fake) handle(w http.ResponseWriter, r *http.Request) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	var in map[string]any
	json.NewDecoder(r.Body).Decode(&in)
	str := func(k string) string { s, _ := in[k].(string); return s }
	path := r.URL.Path
	switch {
	case r.Method == "POST" && path == "/v1/auth/device":
		code := "dc_" + f.id("c")
		user := fmt.Sprintf("ABCD-%04d", f.n%10000)
		f.Codes[code] = &DeviceCode{User: user, State: "pending", Interval: 1}
		WriteJSON(w, 200, map[string]any{"device_code": code, "user_code": user, "verification_uri": f.Srv.URL + "/device",
			"verification_uri_complete": f.Srv.URL + "/device?code=" + user, "expires_in": 600, "interval": 1})
	case r.Method == "POST" && path == "/v1/auth/token" && str("grant_type") == "device_code":
		c := f.Codes[str("device_code")]
		switch {
		case c == nil:
			WriteProblem(w, 400, "expired_token", "Unknown or expired device code; start again.")
		case c.SlowDown > 0:
			c.SlowDown--
			WriteProblem(w, 400, "slow_down", "Polling too fast.")
		case c.State == "denied":
			WriteProblem(w, 400, "access_denied", "The sign-in was denied.")
		case c.State == "suspended":
			WriteProblem(w, 403, "suspended", "This account is suspended.")
		case c.State != "approved":
			WriteProblem(w, 400, "authorization_pending", "Waiting for the person to approve.")
		default:
			delete(f.Codes, str("device_code"))
			f.mint(w, f.id("d"))
		}
	case r.Method == "POST" && path == "/v1/auth/token" && str("grant_type") == "refresh_token":
		tok := str("refresh_token")
		dev, ok := f.refreshTok[tok]
		if !ok || f.Revoked[dev] {
			if leaked, reused := f.prevRefr[tok]; reused {
				f.Revoked[leaked] = true
			}
			WriteProblem(w, 400, "expired_token", "The refresh token is invalid, expired or already used; sign in again.")
			return
		}
		delete(f.refreshTok, tok)
		f.prevRefr[tok] = dev
		f.mint(w, dev)
	case r.Method == "POST" && path == "/v1/auth/revoke":
		if dev := f.device(w, r); dev != "" {
			f.Revoked[dev] = true
			w.WriteHeader(204)
		}
	case r.Method == "GET" && path == "/v1/me":
		if f.device(w, r) != "" {
			WriteJSON(w, 200, map[string]any{"id": "u_1", "handle": "ada", "plan": "pro", "allowance": map[string]any{
				"running_seconds": 360000, "running_seconds_used": 7200, "storage_gb": 100, "storage_gb_used": 12.5, "max_machines": 3,
				"period_end": "2026-12-01T00:00:00Z"}})
		}
	case r.Method == "GET" && path == "/v1/devices":
		if dev := f.device(w, r); dev != "" {
			WriteJSON(w, 200, map[string]any{"devices": []any{map[string]any{"id": dev, "name": "laptop", "client": "cli",
				"created_at": "2026-11-10T10:00:00Z", "last_seen_at": "2026-11-10T11:00:00Z", "current": true}}})
		}
	case r.Method == "GET" && path == "/v1/sizes":
		if f.device(w, r) != "" {
			size := func(id, name string, vcpu, mem, disk int, plans ...string) map[string]any {
				return map[string]any{"id": id, "name": name, "vcpu": vcpu, "memory_gb": mem, "disk_gb": disk, "plans": plans}
			}
			WriteJSON(w, 200, map[string]any{
				"sizes": []any{size("small", "Small", 2, 4, 30, "free", "pro"), size("standard", "Standard", 4, 8, 60, "free", "pro"),
					size("large", "Large", 8, 16, 120, "pro"), size("xl", "XL", 16, 32, 240, "pro")},
				"regions": []any{map[string]any{"id": "us-west", "name": "US West"}}})
		}
	case r.Method == "GET" && path == "/v1/machines":
		if f.device(w, r) != "" {
			list := []any{}
			for _, id := range f.order {
				if m := f.Machines[id]; m != nil {
					list = append(list, m)
				}
			}
			WriteJSON(w, 200, map[string]any{"machines": list})
		}
	case r.Method == "POST" && path == "/v1/machines":
		if f.device(w, r) != "" {
			if len(f.Machines) >= 3 {
				WriteProblem(w, 402, "plan_limit", "Your plan allows 3 machines.")
				return
			}
			m := &Machine{ID: f.id("m"), Name: str("name"), Size: str("size"), Region: "us-west", State: "creating",
				Sleep: map[string]int{"idle_seconds": 600}, CreatedAt: time.Now().UTC().Truncate(time.Second)}
			f.Machines[m.ID] = m
			f.order = append(f.order, m.ID)
			WriteJSON(w, 201, m)
		}
	case strings.HasPrefix(path, "/v1/machines/"):
		if f.device(w, r) == "" {
			return
		}
		rest := strings.TrimPrefix(path, "/v1/machines/")
		id, action, _ := strings.Cut(rest, "/")
		m := f.Machines[id]
		if m == nil {
			WriteProblem(w, 404, "not_found", "No such machine.")
			return
		}
		switch r.Method + " " + action {
		case "GET ":
			WriteJSON(w, 200, m)
		case "DELETE ":
			delete(f.Machines, id)
			w.WriteHeader(202)
		case "PATCH ":
			if n := str("name"); n != "" {
				m.Name = n
			}
			WriteJSON(w, 200, m)
		case "POST wake":
			if m.State == "asleep" {
				m.State = "waking"
			}
			WriteJSON(w, 200, m)
		case "POST sleep":
			if in["force"] != true && m.Activity != nil && len(m.Activity.Agents) > 0 {
				WriteProblem(w, 409, "agents_working", "An agent is working.")
				return
			}
			m.State = "asleep"
			WriteJSON(w, 200, m)
		case "POST connect":
			if m.State == "asleep" {
				m.State = "waking"
			}
			ticket := "cct_" + f.id("t")
			f.tickets[ticket] = f.Rooms[id]
			WriteJSON(w, 200, map[string]any{"relay_url": "ws" + strings.TrimPrefix(f.Relay.URL, "http"), "room": f.Rooms[id],
				"ticket": ticket, "server_key": f.Keys[id], "expires_at": time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339), "state": m.State})
		default:
			WriteProblem(w, 404, "not_found", "No such route in the fake.")
		}
	default:
		WriteProblem(w, 404, "not_found", "No such route in the fake.")
	}
}

// serveRelay is conch's relay with the cloud relay's ticket check in
// front of the client's door (PROTOCOL §3.3): a ticket the fake handed
// out, once, for this room — anything else is closed with 4401.
func (f *Fake) serveRelay(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == relay.PhonePath {
		t := r.Header.Get(relay.TicketHeader)
		if t == "" {
			t = r.URL.Query().Get("ticket")
		}
		f.Mu.Lock()
		room, ok := f.tickets[t]
		delete(f.tickets, t)
		ok = ok && !f.BadTickets
		f.Mu.Unlock()
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
type Gateway struct {
	Key    string // its public key, base64url
	Room   string
	DH     noise.DHKey
	Secret string
	cancel context.CancelFunc
	done   chan struct{}
}

func (f *Fake) StartGateway(t *testing.T, serverSocket string) *Gateway {
	t.Helper()
	key, err := relay.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := relay.NewSecret()
	return f.JoinGateway(t, serverSocket, key, secret)
}

// joinGateway is a gateway with this key in this room: the same machine
// coming back.
func (f *Fake) JoinGateway(t *testing.T, serverSocket string, key noise.DHKey, secret string) *Gateway {
	t.Helper()
	h, err := cloudagent.TunnelHandler(cloudagent.Options{ServerSocket: serverSocket, Logf: t.Logf, BootPath: "/nonexistent", StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &relay.Link{Relay: "ws" + strings.TrimPrefix(f.Relay.URL, "http"), Secret: secret, Key: key, Handler: h, Logf: func(string, ...any) {}}
	g := &Gateway{Key: relay.EncodeKey(key.Public), Room: relay.RoomID(secret), cancel: cancel, done: make(chan struct{}), DH: key, Secret: secret}
	go func() { defer close(g.done); l.Run(ctx) }()
	t.Cleanup(g.Stop)
	// Wait until it has joined the room.
	deadline := time.Now().Add(10 * time.Second)
	for !l.Status().Connected {
		if time.Now().After(deadline) {
			t.Fatal("the gateway never joined the relay")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return g
}

func (g *Gateway) Stop() {
	g.cancel()
	<-g.done
}
