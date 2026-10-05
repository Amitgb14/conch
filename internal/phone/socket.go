package phone

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// Limits on one socket: how many panes it may draw at once, and how many
// keys one message may carry.
const (
	maxOpenFrames = 4
	maxKeys       = 64
	writeTimeout  = 10 * time.Second
)

// socket is one open app. It has its own connection to the conch server,
// so the panes it draws are its own subscriptions and end with it.
type socket struct {
	g         *Gateway
	conn      *websocket.Conn
	devID     string
	tokenHash string
	ctx       context.Context
	cancel    context.CancelFunc

	mu       sync.Mutex
	watching bool
	// clients is this socket's own connection per machine: its events are
	// what the phone follows, so it holds them rather than sharing the
	// gateway's. A machine that comes up later is added by pumpAll.
	clients map[string]*client.Client
	pumping map[string]bool
	// Every map below is keyed by the composite pane id (machine:pane),
	// because p3 exists on every machine.
	known map[string]bool // panes whose agent the phone has been told of
	// For panes.watch: every pane, terminals too.
	watchingPanes bool
	knownPanes    map[string]bool
	open          map[string]bool   // panes it is drawing
	sizes         map[string][2]int // each open pane's columns and rows, from its frames
	// projects is each machine's project names: ids are a server's own.
	projects map[string]map[string]string

	byeOnce sync.Once
}

// serveSocket opens the WebSocket: view to open, and each message checked
// for its own permission as it arrives.
func (g *Gateway) serveSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		g.failStatus(w, r, http.StatusMethodNotAllowed, apiErr(CodeBadRequest, r.Method+" isn't supported here"))
		return
	}
	dev, token, aerr := g.authenticate(r, PermView)
	if aerr != nil {
		g.fail(w, r, aerr)
		return
	}
	// A page from another site is refused: a socket carries the cookie
	// but no CSRF header, so the Origin is what is checked — here, and by
	// Accept, which is told the public address is the gateway's own.
	if !g.sameOrigin(r) {
		g.fail(w, r, apiErr(CodeForbidden, "the socket has to come from the gateway's own page"))
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		g.logf("GET %s refused", r.URL.Path)
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(maxBody)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &socket{g: g, conn: conn, devID: dev.ID, tokenHash: hashSecret(token), ctx: ctx, cancel: cancel,
		clients: map[string]*client.Client{}, pumping: map[string]bool{}, known: map[string]bool{},
		knownPanes: map[string]bool{}, open: map[string]bool{}, sizes: map[string][2]int{},
		projects: map[string]map[string]string{}}
	g.logf("socket %s open", dev.ID)

	g.mu.Lock()
	closed := g.closed
	if !closed {
		g.sockets[s] = struct{}{}
	}
	g.mu.Unlock()
	if closed {
		s.bye(ByeShuttingDown)
		return
	}
	defer func() {
		g.mu.Lock()
		delete(g.sockets, s)
		g.mu.Unlock()
		g.logf("socket %s closed", dev.ID)
	}()

	// This computer must answer: without it there is nothing to show. The
	// others are joined as they come up, and a phone sees them arrive.
	c, err := g.dial()
	if err != nil {
		s.bye(ByeServerGone)
		return
	}
	defer s.closeClients()
	s.join(LocalMachine, c)
	go s.follow()
	s.read()
}

// send writes one message. A phone that has stopped taking them loses the
// socket rather than holding the gateway up.
func (s *socket) send(m ServerMessage) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, writeTimeout)
	defer cancel()
	if err := s.conn.Write(ctx, websocket.MessageText, b); err != nil {
		s.cancel()
		return
	}
	s.g.logf("socket %s > %s %dB", s.devID, m.Type, len(b))
}

func (s *socket) fail(id string, e *APIError) {
	s.send(ServerMessage{Type: MsgError, ID: id, Error: e})
}

// bye says why the socket is closing and closes it.
func (s *socket) bye(reason string) {
	s.byeOnce.Do(func() {
		s.send(ServerMessage{Type: MsgBye, Reason: reason})
		// The close frame goes out at once; cancelling first would cut the
		// connection before the phone had read why.
		s.conn.Close(websocket.StatusNormalClosure, reason)
		s.cancel()
	})
}

// allowed checks the device is still paired and may do what needs need.
// It is asked for every message in and every event out: a device revoked
// a moment ago gets nothing more.
func (s *socket) allowed(need string) (paired, ok bool) {
	dev, found := s.g.deviceByHash(s.tokenHash)
	if !found {
		return false, false
	}
	return true, allows(dev.Permission, need)
}

// read takes the phone's messages until the socket ends.
func (s *socket) read() {
	for {
		typ, data, err := s.conn.Read(s.ctx)
		if err != nil {
			return
		}
		var m ClientMessage
		if typ != websocket.MessageText || json.Unmarshal(data, &m) != nil {
			s.fail("", apiErr(CodeBadRequest, "a message is a JSON object with a type"))
			continue
		}
		s.g.logf("socket %s < %s %dB", s.devID, logType(m.Type), len(data))
		need, known := socketNeeds[m.Type]
		if !known {
			need = PermView
		}
		paired, ok := s.allowed(need)
		switch {
		case !paired:
			s.bye(ByeRevoked)
			return
		case !known:
			s.fail(m.ID, apiErr(CodeBadRequest, "unknown message type"))
		case !ok:
			s.fail(m.ID, apiErr(CodeForbidden, "this device's permission is below "+need))
		default:
			s.handle(m)
		}
	}
}

// logType is a message type fit for the log: one the contract has, or
// "unknown" — what a phone puts there is not ours to write down.
func logType(t string) string {
	if _, ok := socketNeeds[t]; ok {
		return t
	}
	return "unknown"
}

// call is one call on a machine's connection.
func (s *socket) call(c *client.Client, method string, params, out any) error {
	ctx, cancel := context.WithTimeout(s.ctx, callTimeout)
	defer cancel()
	return c.Call(ctx, method, params, out)
}

func (s *socket) handle(m ClientMessage) {
	switch m.Type {
	case MsgPing:
		s.send(ServerMessage{Type: MsgPong, ID: m.ID})

	case MsgAgentsWatch:
		// Watching before listing: an update that lands between the two is
		// sent rather than lost.
		s.mu.Lock()
		s.watching = true
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(s.ctx, callTimeout)
		agents, machines := s.g.everyAgent(ctx)
		cancel()
		s.mu.Lock()
		for _, a := range agents {
			s.known[a.Pane] = true
		}
		s.mu.Unlock()
		s.send(ServerMessage{Type: MsgMachines, Machines: &machines})
		s.send(ServerMessage{Type: MsgAgents, ID: m.ID, Agents: &agents})

	case MsgPanesWatch:
		s.mu.Lock()
		s.watchingPanes = true
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(s.ctx, callTimeout)
		panes, machines := s.g.everyPane(ctx)
		cancel()
		s.send(ServerMessage{Type: MsgMachines, Machines: &machines})
		s.mu.Lock()
		for _, p := range panes {
			s.knownPanes[p.Pane] = true
		}
		s.mu.Unlock()
		s.send(ServerMessage{Type: MsgPanes, ID: m.ID, Panes: &panes})

	case MsgWheel:
		c, pane, id, aerr := s.paneOn(m.Pane)
		if aerr != nil {
			s.fail(m.ID, aerr)
			return
		}
		button := map[string]string{"up": "wheel_up", "down": "wheel_down"}[m.Direction]
		if button == "" {
			s.fail(m.ID, apiErr(CodeBadRequest, "direction is up or down"))
			return
		}
		n := m.Count
		if n == 0 {
			n = 1
		}
		if n < 0 || n > 10 {
			s.fail(m.ID, apiErr(CodeBadRequest, "count is between 1 and 10"))
			return
		}
		// Over the upper middle of the pane, where an agent's conversation
		// is, rather than its prompt at the bottom.
		s.mu.Lock()
		size := s.sizes[id]
		s.mu.Unlock()
		x, y := max(size[0]/2, 0), max(size[1]/3, 0)
		for range n {
			if err := s.call(c, proto.MethodPaneSendMouse, proto.PaneSendMouseParams{ID: pane, X: x, Y: y, Button: button, Action: proto.MouseWheel}, nil); err != nil {
				s.fail(m.ID, fromServer(err))
				return
			}
		}

	case MsgScroll:
		c, pane, id, aerr := s.paneOn(m.Pane)
		if aerr != nil {
			s.fail(m.ID, aerr)
			return
		}
		s.mu.Lock()
		open := s.open[id]
		s.mu.Unlock()
		if !open || m.Offset < 0 {
			s.fail(m.ID, apiErr(CodeBadRequest, "scroll takes a pane this socket has open and an offset of 0 or more"))
			return
		}
		// This socket's own connection: only its view of the pane moves.
		if err := s.call(c, proto.MethodPaneScroll, proto.PaneScrollParams{ID: pane, Offset: m.Offset}, nil); err != nil {
			s.fail(m.ID, fromServer(err))
		}

	case MsgResize:
		c, pane, id, aerr := s.paneOn(m.Pane)
		if aerr != nil {
			s.fail(m.ID, aerr)
			return
		}
		s.mu.Lock()
		open := s.open[id]
		s.mu.Unlock()
		if !open {
			s.fail(m.ID, apiErr(CodeBadRequest, "resize takes a pane this socket has open"))
			return
		}
		if m.Cols < MinPaneCols || m.Cols > MaxPaneCols || m.Rows < MinPaneRows || m.Rows > MaxPaneRows {
			s.fail(m.ID, apiErr(CodeBadRequest, fmt.Sprintf("resize takes %d–%d columns and %d–%d rows",
				MinPaneCols, MaxPaneCols, MinPaneRows, MaxPaneRows)))
			return
		}
		// The pane itself, not this socket's view of it: a terminal has one
		// size and everybody watching sees it. A laptop whose TUI is also
		// showing the pane will set it back to its own, which is the same
		// rule the TUI has always had — the last one to look wins.
		if err := s.call(c, proto.MethodPaneResize, proto.PaneResizeParams{ID: pane, Cols: m.Cols, Rows: m.Rows}, nil); err != nil {
			s.fail(m.ID, fromServer(err))
		}

	case MsgText:
		c, pane, _, aerr := s.paneOn(m.Pane)
		if aerr != nil {
			s.fail(m.ID, aerr)
			return
		}
		if m.Text == "" {
			s.fail(m.ID, apiErr(CodeBadRequest, "text needs something to type"))
			return
		}
		// Across lines it is a paste, so a program that asked for bracketed
		// paste doesn't take each line's end for Enter.
		params := proto.PaneSendTextParams{ID: pane, Text: m.Text, Paste: strings.ContainsAny(m.Text, "\r\n")}
		if err := s.call(c, proto.MethodPaneSendText, params, nil); err != nil {
			s.fail(m.ID, fromServer(err))
		}

	case MsgFrameOpen:
		c, pane, id, aerr := s.paneOn(m.Pane)
		if aerr != nil {
			s.fail(m.ID, aerr)
			return
		}
		s.mu.Lock()
		already, full := s.open[id], len(s.open) >= maxOpenFrames
		if !already && !full {
			s.open[id] = true // before asking: the first frame may beat the answer
		}
		s.mu.Unlock()
		if already {
			return
		}
		if full {
			s.fail(m.ID, apiErr(CodeBadRequest, "too many panes open on this socket; close one first"))
			return
		}
		if err := s.call(c, proto.MethodPaneSubscribe, proto.PaneRef{ID: pane}, nil); err != nil {
			s.mu.Lock()
			delete(s.open, id)
			s.mu.Unlock()
			s.fail(m.ID, fromServer(err))
		}

	case MsgFrameClose:
		c, pane, id, aerr := s.paneOn(m.Pane)
		if aerr != nil {
			s.fail(m.ID, aerr)
			return
		}
		s.mu.Lock()
		was := s.open[id]
		delete(s.open, id)
		s.mu.Unlock()
		if was {
			_ = s.call(c, proto.MethodPaneUnsubscribe, proto.PaneRef{ID: pane}, nil)
		}

	case MsgKeys:
		c, pane, _, aerr := s.paneOn(m.Pane)
		if aerr != nil {
			s.fail(m.ID, aerr)
			return
		}
		if len(m.Keys) == 0 || len(m.Keys) > maxKeys {
			s.fail(m.ID, apiErr(CodeBadRequest, "keys takes between 1 and 64 key names"))
			return
		}
		if err := s.call(c, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: pane, Keys: m.Keys}, nil); err != nil {
			s.fail(m.ID, fromServer(err))
		}
	}
}

// pump passes on what one machine's server says, for as long as the socket
// lives. This computer going away ends the socket — there is nothing left
// to show; another machine going away leaves the rest of them, and the
// machine list says what happened.
func (s *socket) pump(machine string, c *client.Client) {
	defer func() {
		s.mu.Lock()
		delete(s.clients, machine)
		delete(s.pumping, machine)
		s.mu.Unlock()
		c.Close()
		if machine != LocalMachine {
			s.tellMachines()
		}
	}()
	for {
		select {
		case msg, ok := <-c.Events:
			if !ok {
				if machine == LocalMachine {
					s.bye(ByeServerGone)
				}
				return
			}
			if paired, _ := s.allowed(PermView); !paired {
				s.bye(ByeRevoked)
				return
			}
			s.event(machine, msg)
		case <-s.ctx.Done():
			return
		}
	}
}

// tellMachines sends this socket the machine list again, after one has
// come or gone.
func (s *socket) tellMachines() {
	ctx, cancel := context.WithTimeout(s.ctx, callTimeout)
	defer cancel()
	_, machines := s.g.everyAgent(ctx)
	s.send(ServerMessage{Type: MsgMachines, Machines: &machines})
}

func (s *socket) event(machine string, msg proto.Message) {
	switch msg.Event {
	case proto.EventPaneFrame:
		var f proto.Frame
		if json.Unmarshal(msg.Data, &f) != nil {
			return
		}
		ref := composePaneID(machine, f.ID)
		s.mu.Lock()
		open := s.open[ref]
		if open {
			s.sizes[ref] = [2]int{f.Cols, f.Rows}
		}
		s.mu.Unlock()
		if !open {
			return
		}
		if f.Lines == nil {
			f.Lines = []string{}
		}
		s.send(ServerMessage{Type: MsgFrame, Frame: &Frame{Pane: ref, Cols: f.Cols, Rows: f.Rows, Lines: f.Lines,
			Offset: f.Offset, History: f.History, AltScreen: f.AltScreen, Mouse: f.Mouse}})

	case proto.EventPaneUpdated, proto.EventPaneCreated:
		var p proto.PaneInfo
		if json.Unmarshal(msg.Data, &p) != nil {
			return
		}
		s.mu.Lock()
		watching, watchingPanes := s.watching, s.watchingPanes
		s.mu.Unlock()
		if watchingPanes {
			s.paneChanged(machine, p)
		}
		if !watching {
			return
		}
		if !isAgent(p) {
			s.gone(composePaneID(machine, p.ID))
			return
		}
		c := s.clientOn(machine)
		if c == nil {
			return // it went while this event was in flight
		}
		ctx, cancel := context.WithTimeout(s.ctx, callTimeout)
		a := agentOf(ctx, machine, c, p, s.projectNames(ctx, machine, c, p.ProjectID))
		cancel()
		s.mu.Lock()
		s.known[a.Pane] = true
		s.mu.Unlock()
		s.send(ServerMessage{Type: MsgAgent, Agent: &a})

	case proto.EventPaneExited, proto.EventPaneClosed:
		var ref proto.PaneRef
		if json.Unmarshal(msg.Data, &ref) != nil {
			return
		}
		id := composePaneID(machine, ref.ID)
		s.mu.Lock()
		delete(s.open, id)
		wasPane := s.knownPanes[id]
		delete(s.knownPanes, id)
		s.mu.Unlock()
		s.gone(id)
		if wasPane {
			s.send(ServerMessage{Type: MsgPaneGone, Pane: id})
		}
	}
}

// paneChanged tells a phone watching every pane about one that appeared
// or changed, or that it has gone when it no longer runs.
func (s *socket) paneChanged(machine string, p proto.PaneInfo) {
	id := composePaneID(machine, p.ID)
	if !isRunning(p) {
		s.mu.Lock()
		was := s.knownPanes[id]
		delete(s.knownPanes, id)
		s.mu.Unlock()
		if was {
			s.send(ServerMessage{Type: MsgPaneGone, Pane: id})
		}
		return
	}
	c := s.clientOn(machine)
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, callTimeout)
	pn := paneOf(ctx, machine, c, p, s.projectNames(ctx, machine, c, p.ProjectID))
	cancel()
	s.mu.Lock()
	s.knownPanes[id] = true
	s.mu.Unlock()
	s.send(ServerMessage{Type: MsgPaneChanged, Info: &pn})
}

// gone tells the phone an agent it knew of has left.
func (s *socket) gone(pane string) {
	s.mu.Lock()
	was := s.known[pane]
	delete(s.known, pane)
	s.mu.Unlock()
	if was {
		s.send(ServerMessage{Type: MsgAgentGone, Pane: pane})
	}
}

// projectNames is the projects' names, asked for again when one turns up
// that the socket hasn't heard of.
func (s *socket) projectNames(ctx context.Context, machine string, c *client.Client, id string) map[string]string {
	s.mu.Lock()
	names := s.projects[machine]
	_, have := names[id]
	s.mu.Unlock()
	if id == "" || have {
		return names
	}
	fresh, err := projectNames(ctx, c)
	if err != nil {
		return names
	}
	s.mu.Lock()
	s.projects[machine] = fresh
	s.mu.Unlock()
	return fresh
}

// join adds a machine's connection to this socket and starts following its
// events. The caller owns nothing afterwards: closeClients closes them all.
func (s *socket) join(machine string, c *client.Client) {
	s.mu.Lock()
	if s.clients[machine] != nil || s.pumping[machine] {
		s.mu.Unlock()
		c.Close()
		return
	}
	s.clients[machine] = c
	s.pumping[machine] = true
	s.mu.Unlock()
	go s.pump(machine, c)
}

// follow joins the machines that are up and keeps looking for ones that
// come later, so a phone left open sees a machine arrive. Local is already
// joined; a machine that drops is dropped from the socket and joined again
// when the gateway has it back.
func (s *socket) follow() {
	t := time.NewTicker(machineJoinEvery)
	defer t.Stop()
	for {
		for _, mc := range s.g.machines() {
			if mc.id == LocalMachine {
				continue
			}
			s.mu.Lock()
			have := s.clients[mc.id] != nil || s.pumping[mc.id]
			s.mu.Unlock()
			if have || mc.client() == nil {
				continue
			}
			// Its own connection, so this socket's events are its own: the
			// gateway's client is shared by every request.
			c, err := s.g.dialMachine(mc.id)
			if err != nil {
				continue
			}
			s.join(mc.id, c)
			// The phone hears about it from this socket, which is the only
			// one that knows it has joined: the gateway's own broadcast
			// may have happened before this socket was open.
			s.tellMachines()
		}
		select {
		case <-t.C:
		case <-s.ctx.Done():
			return
		}
	}
}

// machineJoinEvery is how often a socket looks for a machine that has come
// up since it opened. Tests shorten it.
var machineJoinEvery = 2 * time.Second

// closeClients ends every connection this socket holds.
func (s *socket) closeClients() {
	s.mu.Lock()
	cs := make([]*client.Client, 0, len(s.clients))
	for _, c := range s.clients {
		cs = append(cs, c)
	}
	s.clients = map[string]*client.Client{}
	s.mu.Unlock()
	for _, c := range cs {
		c.Close()
	}
}

// clientOn is the connection to a machine, or nothing when this socket has
// none — a machine that is still being reached, or has gone.
func (s *socket) clientOn(machine string) *client.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.clients[machine]
	if c != nil && c.Err() != nil {
		return nil
	}
	return c
}

// paneOn resolves an id the phone sent to the machine's client, the pane id
// that machine's server knows, and the canonical machine:pane — which is
// what every map here is keyed by, so a bare `p3` and `local:p3` are the
// same pane and not two.
func (s *socket) paneOn(ref string) (c *client.Client, pane, id string, aerr *APIError) {
	machine, pane, aerr := splitPaneID(ref)
	if aerr != nil {
		return nil, "", "", aerr
	}
	c = s.clientOn(machine)
	if c == nil {
		return nil, "", "", apiErr(CodeServerUnavailable, "this phone has no connection to "+machine+" yet")
	}
	return c, pane, composePaneID(machine, pane), nil
}
