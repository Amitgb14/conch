// Package desktop is the Go half of conch's desktop window: what its page
// reaches over a WebSocket on loopback. The page is the person, as the TUI
// is, so the host passes protocol calls through to whichever machine they
// name rather than keeping a curated API — a TUI feature then needs no
// route here to reach the window. What the host owns is what a page
// cannot: one connection per machine, the pane subscriptions the pages
// share, and the token that keeps other programs out.
//
// The window and `conch desktop -browser` both serve it.
package desktop

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/fleet"
	"github.com/Amitgb14/conch/internal/proto"
)

// refused are methods a page may not call. Stopping or reloading a server
// ends or interrupts every pane on it, which a stray click must not do;
// the window asks for those itself, with a confirmation (phase 5).
var refused = map[string]bool{
	proto.MethodServerStop:   true,
	proto.MethodServerReload: true,
}

// callWait bounds one call a page makes.
const callWait = 30 * time.Second

// Options is how a host reaches conch.
type Options struct {
	// Pool is every machine. The host dials its own connection to each
	// (fleet.Pool.Dial) for calls and events, and never closes the pool.
	Pool *fleet.Pool
	// StatePath is desktop.json; "" keeps nothing.
	StatePath string
	// Conch is the conch the page runs in a pane for what only the CLI
	// does, talking to the person as it goes — adding a machine, which
	// may ask about a host key or a password. "" offers none of it.
	Conch string
	// MachinesEvery is how often the machine list is looked at for a
	// change nobody announced (one added from a pane, one going down);
	// 0 is every five seconds.
	MachinesEvery time.Duration
	Logf          func(format string, args ...any)
}

// Host serves pages.
type Host struct {
	token string
	o     Options
	state *stateFile

	mu       sync.Mutex
	pages    map[*page]struct{}
	machines map[string]*machineLink
	// wanted is how many pages want each pane's frames, by machine:pane;
	// it outlives a machine's connection so a reconnect subscribes again.
	wanted map[string]int
	// last is each wanted pane's newest frame, for a page that subscribes
	// to a pane another page already has: the server sent its frame then.
	last   map[string]json.RawMessage
	closed bool
	wg     sync.WaitGroup
	quit   chan struct{}
	shown  string // the machine list as last sent, to tell a change
}

// machineLink is the host's own connection to one machine.
type machineLink struct {
	id   string
	c    *client.Client
	subs map[string]bool // panes subscribed on this connection, by server ID
}

// New makes a host with a fresh token. Close it when done.
func New(o Options) *Host {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.MachinesEvery <= 0 {
		o.MachinesEvery = 5 * time.Second
	}
	h := &Host{
		token: newToken(), o: o, state: &stateFile{path: o.StatePath},
		pages: map[*page]struct{}{}, machines: map[string]*machineLink{}, wanted: map[string]int{},
		last: map[string]json.RawMessage{}, quit: make(chan struct{}),
	}
	h.wg.Add(1)
	go h.watchMachines()
	return h
}

// watchMachines tells the pages when the machine list changes without
// anyone saying so: a machine added from a pane, one that dropped.
func (h *Host) watchMachines() {
	defer h.wg.Done()
	t := time.NewTicker(h.o.MachinesEvery)
	defer t.Stop()
	for {
		select {
		case <-h.quit:
			return
		case <-t.C:
			if len(h.allPages()) > 0 {
				h.MachinesChanged()
			}
		}
	}
}

// Token is what a page must present. It is new each time a host starts.
func (h *Host) Token() string { return h.token }

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // no randomness: nothing would be safe to serve
	}
	return hex.EncodeToString(b)
}

func (h *Host) authorized(r *http.Request) bool {
	got := r.URL.Query().Get("token")
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) == 1
}

// Handler serves the socket and, given assets, the page — for a browser.
// The token is not served here: a browser is handed it in the URL's
// fragment, which never reaches a server, so another program on this
// computer that finds the port still has nothing to present.
func (h *Host) Handler(assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/socket", h.serveSocket)
	if assets != nil {
		mux.Handle("/", noStore(http.FileServerFS(assets)))
	}
	return mux
}

// noStore keeps a browser from caching the page: it changes with every
// build of conch, and a stale module beside a new one does not load.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// originPatterns are the webview's own schemes. A page in a browser comes
// from the host's listener, which the WebSocket library lets in as the
// same origin; any other page is refused even holding the token.
var originPatterns = []string{"wails", "wails.localhost"}

func (h *Host) serveSocket(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: originPatterns})
	if err != nil {
		return
	}
	ws.SetReadLimit(maxMessage)
	p := newPage(h, ws)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		ws.Close(websocket.StatusGoingAway, "closing")
		return
	}
	h.pages[p] = struct{}{}
	h.mu.Unlock()
	defer h.dropPage(p)
	p.run(r.Context())
}

// Close ends every page and drops the host's connections.
func (h *Host) Close() {
	h.mu.Lock()
	if !h.closed {
		close(h.quit)
	}
	h.closed = true
	pages := make([]*page, 0, len(h.pages))
	for p := range h.pages {
		pages = append(pages, p)
	}
	links := h.machines
	h.machines = map[string]*machineLink{}
	h.mu.Unlock()
	for _, p := range pages {
		p.close("the host is closing")
	}
	for _, l := range links {
		l.c.Close()
	}
	h.wg.Wait()
}

// MachinesChanged tells every page the machine list, when it is not what
// they were last told; give it to the pool's Changed so a machine coming
// up shows without asking.
func (h *Host) MachinesChanged() { h.tellMachines(false) }

// tellMachines sends the machine list; force sends it even unchanged, as
// after a connection dropped, when the page must look again regardless.
func (h *Host) tellMachines(force bool) {
	list := h.machineList()
	key, _ := json.Marshal(list)
	h.mu.Lock()
	same := string(key) == h.shown
	h.shown = string(key)
	h.mu.Unlock()
	if same && !force {
		return
	}
	for _, p := range h.allPages() {
		p.send(out{T: "machines", Machines: list})
	}
}

func (h *Host) allPages() []*page {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*page, 0, len(h.pages))
	for p := range h.pages {
		out = append(out, p)
	}
	return out
}

// Machine is what a page is told about one machine.
type Machine struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	State        string   `json:"state"`
	Detail       string   `json:"detail,omitempty"`
	Version      string   `json:"version,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

func (h *Host) machineList() []Machine {
	ms := h.o.Pool.Machines()
	out := make([]Machine, 0, len(ms))
	for _, m := range ms {
		pm := Machine{ID: m.ID, Label: m.Label, State: m.State, Detail: m.Detail}
		if c := m.Client(); c != nil {
			pm.Version, pm.Capabilities = c.Server.Version, c.Server.Capabilities
		}
		out = append(out, pm)
	}
	return out
}

// link is the host's connection to a machine, dialled the first time it
// is needed and again after it drops.
func (h *Host) link(machine string) (*machineLink, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, errors.New("the host is closing")
	}
	if l := h.machines[machine]; l != nil && l.c.Err() == nil {
		h.mu.Unlock()
		return l, nil
	}
	h.mu.Unlock()

	// Dialling may take a while (ssh); it is done without the lock, and
	// two pages asking at once may both dial — the second is closed.
	c, err := h.o.Pool.Dial(machine)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		c.Close()
		return nil, errors.New("the host is closing")
	}
	if l := h.machines[machine]; l != nil && l.c.Err() == nil {
		h.mu.Unlock()
		c.Close()
		return l, nil
	}
	l := &machineLink{id: machine, c: c, subs: map[string]bool{}}
	h.machines[machine] = l
	// What pages wanted from this machine before it dropped, they still
	// want: subscribe again on the new connection.
	var again []string
	for ref, n := range h.wanted {
		if m, pane, err := fleet.SplitPaneID(ref); err == nil && m == machine && n > 0 {
			l.subs[pane] = true
			again = append(again, pane)
		}
	}
	h.wg.Add(1)
	h.mu.Unlock()
	go h.follow(l)
	for _, pane := range again {
		c.Notify(proto.MethodPaneSubscribe, proto.PaneRef{ID: pane})
	}
	return l, nil
}

// follow hands a machine's events to the pages until its connection ends.
func (h *Host) follow(l *machineLink) {
	defer h.wg.Done()
	for msg := range l.c.Events {
		if msg.Event == proto.EventPaneFrame {
			var f struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(msg.Data, &f) != nil {
				continue
			}
			ref := fleet.ComposePaneID(l.id, f.ID)
			h.mu.Lock()
			if h.wanted[ref] > 0 {
				h.last[ref] = msg.Data
			}
			h.mu.Unlock()
			for _, p := range h.allPages() {
				p.frame(ref, msg.Data)
			}
			continue
		}
		ev := out{T: "event", Machine: l.id, Event: msg.Event, Data: msg.Data}
		for _, p := range h.allPages() {
			p.send(ev)
		}
	}
	h.mu.Lock()
	if h.machines[l.id] == l {
		delete(h.machines, l.id)
	}
	closed := h.closed
	h.mu.Unlock()
	if !closed {
		h.o.Logf("desktop: %s: connection ended: %v", l.id, l.c.Err())
		h.o.Pool.Note(l.id, fmt.Errorf("connection ended: %w", l.c.Err()))
		h.tellMachines(true)
	}
}

// call runs one method on a machine for a page.
func (h *Host) call(ctx context.Context, machine, method string, params json.RawMessage) (json.RawMessage, *Error) {
	if refused[method] {
		return nil, &Error{Code: "refused", Message: method + " is not available to the page"}
	}
	if method == proto.MethodPaneSubscribe || method == proto.MethodPaneUnsubscribe {
		return nil, &Error{Code: "refused", Message: "subscribe with a subscribe message, so pages can share a pane"}
	}
	if machine == "" {
		machine = fleet.Local
	}
	if !fleet.IDOK(machine) {
		return nil, &Error{Code: "bad_request", Message: fmt.Sprintf("%q does not name a machine", machine)}
	}
	l, err := h.link(machine)
	if err != nil {
		return nil, &Error{Code: "unavailable", Message: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, callWait)
	defer cancel()
	var res json.RawMessage
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	if err := l.c.Call(ctx, method, params, &res); err != nil {
		var perr *proto.Error
		if errors.As(err, &perr) {
			return nil, &Error{Code: perr.Code, Message: perr.Message}
		}
		return nil, &Error{Code: "unavailable", Message: err.Error()}
	}
	return res, nil
}

// subscribe adds a page's interest in a pane, subscribing on the machine
// the first time any page wants it, and returns the newest frame there
// already is (nil for none yet).
func (h *Host) subscribe(ref string) (json.RawMessage, *Error) {
	machine, pane, err := fleet.SplitPaneID(ref)
	if err != nil {
		return nil, &Error{Code: "bad_request", Message: err.Error()}
	}
	l, err := h.link(machine)
	if err != nil {
		return nil, &Error{Code: "unavailable", Message: err.Error()}
	}
	h.mu.Lock()
	h.wanted[ref]++
	first := !l.subs[pane]
	l.subs[pane] = true
	last := h.last[ref]
	h.mu.Unlock()
	if first {
		l.c.Notify(proto.MethodPaneSubscribe, proto.PaneRef{ID: pane})
	}
	return last, nil
}

// unsubscribe drops it, and on the machine once no page wants it.
func (h *Host) unsubscribe(ref string) {
	machine, pane, err := fleet.SplitPaneID(ref)
	if err != nil {
		return
	}
	h.mu.Lock()
	if h.wanted[ref] > 0 {
		h.wanted[ref]--
	}
	last := h.wanted[ref] == 0
	if last {
		delete(h.wanted, ref)
		delete(h.last, ref)
	}
	l := h.machines[machine]
	if last && l != nil {
		delete(l.subs, pane)
	}
	h.mu.Unlock()
	if last && l != nil {
		l.c.Notify(proto.MethodPaneUnsubscribe, proto.PaneRef{ID: pane})
	}
}

func (h *Host) dropPage(p *page) {
	h.mu.Lock()
	delete(h.pages, p)
	h.mu.Unlock()
	for _, ref := range p.subscribed() {
		h.unsubscribe(ref)
	}
}

// SharedClient is one connection to this computer's server, dialled again
// when it drops: what the pool asks for at every look.
func SharedClient(dial func() (*client.Client, error)) func() (*client.Client, error) {
	var (
		mu sync.Mutex
		c  *client.Client
	)
	return func() (*client.Client, error) {
		mu.Lock()
		defer mu.Unlock()
		if c != nil && c.Err() == nil {
			return c, nil
		}
		nc, err := dial()
		if err != nil {
			return nil, err
		}
		c = nc
		go func() { // nothing follows this one's events
			for range nc.Events {
			}
		}()
		return c, nil
	}
}
