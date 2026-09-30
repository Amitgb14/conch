package phone

import (
	"context"
	"encoding/json"
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
	c         *client.Client
	devID     string
	tokenHash string
	ctx       context.Context
	cancel    context.CancelFunc

	mu       sync.Mutex
	watching bool
	known    map[string]bool // panes whose agent the phone has been told of
	// For panes.watch: every pane, terminals too.
	watchingPanes bool
	knownPanes    map[string]bool
	open          map[string]bool // panes it is drawing
	projects      map[string]string

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
		known: map[string]bool{}, knownPanes: map[string]bool{}, open: map[string]bool{}, projects: map[string]string{}}
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

	c, err := g.dial()
	if err != nil {
		s.bye(ByeServerGone)
		return
	}
	defer c.Close()
	s.c = c
	go s.pump()
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

func (s *socket) call(method string, params, out any) error {
	ctx, cancel := context.WithTimeout(s.ctx, callTimeout)
	defer cancel()
	return s.c.Call(ctx, method, params, out)
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
		agents, err := agentList(ctx, s.c)
		cancel()
		if err != nil {
			s.fail(m.ID, fromServer(err))
			return
		}
		s.mu.Lock()
		for _, a := range agents {
			s.known[a.Pane] = true
		}
		s.mu.Unlock()
		s.send(ServerMessage{Type: MsgAgents, ID: m.ID, Agents: &agents})

	case MsgPanesWatch:
		s.mu.Lock()
		s.watchingPanes = true
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(s.ctx, callTimeout)
		panes, err := paneList(ctx, s.c)
		cancel()
		if err != nil {
			s.fail(m.ID, fromServer(err))
			return
		}
		s.mu.Lock()
		for _, p := range panes {
			s.knownPanes[p.Pane] = true
		}
		s.mu.Unlock()
		s.send(ServerMessage{Type: MsgPanes, ID: m.ID, Panes: &panes})

	case MsgScroll:
		if aerr := paneID(m.Pane); aerr != nil {
			s.fail(m.ID, aerr)
			return
		}
		s.mu.Lock()
		open := s.open[m.Pane]
		s.mu.Unlock()
		if !open || m.Offset < 0 {
			s.fail(m.ID, apiErr(CodeBadRequest, "scroll takes a pane this socket has open and an offset of 0 or more"))
			return
		}
		// This socket's own connection: only its view of the pane moves.
		if err := s.call(proto.MethodPaneScroll, proto.PaneScrollParams{ID: m.Pane, Offset: m.Offset}, nil); err != nil {
			s.fail(m.ID, fromServer(err))
		}

	case MsgText:
		if aerr := paneID(m.Pane); aerr != nil {
			s.fail(m.ID, aerr)
			return
		}
		if m.Text == "" {
			s.fail(m.ID, apiErr(CodeBadRequest, "text needs something to type"))
			return
		}
		// Across lines it is a paste, so a program that asked for bracketed
		// paste doesn't take each line's end for Enter.
		params := proto.PaneSendTextParams{ID: m.Pane, Text: m.Text, Paste: strings.ContainsAny(m.Text, "\r\n")}
		if err := s.call(proto.MethodPaneSendText, params, nil); err != nil {
			s.fail(m.ID, fromServer(err))
		}

	case MsgFrameOpen:
		if aerr := paneID(m.Pane); aerr != nil {
			s.fail(m.ID, aerr)
			return
		}
		s.mu.Lock()
		already, full := s.open[m.Pane], len(s.open) >= maxOpenFrames
		if !already && !full {
			s.open[m.Pane] = true // before asking: the first frame may beat the answer
		}
		s.mu.Unlock()
		if already {
			return
		}
		if full {
			s.fail(m.ID, apiErr(CodeBadRequest, "too many panes open on this socket; close one first"))
			return
		}
		if err := s.call(proto.MethodPaneSubscribe, proto.PaneRef{ID: m.Pane}, nil); err != nil {
			s.mu.Lock()
			delete(s.open, m.Pane)
			s.mu.Unlock()
			s.fail(m.ID, fromServer(err))
		}

	case MsgFrameClose:
		if aerr := paneID(m.Pane); aerr != nil {
			s.fail(m.ID, aerr)
			return
		}
		s.mu.Lock()
		was := s.open[m.Pane]
		delete(s.open, m.Pane)
		s.mu.Unlock()
		if was {
			_ = s.call(proto.MethodPaneUnsubscribe, proto.PaneRef{ID: m.Pane}, nil)
		}

	case MsgKeys:
		if aerr := paneID(m.Pane); aerr != nil {
			s.fail(m.ID, aerr)
			return
		}
		if len(m.Keys) == 0 || len(m.Keys) > maxKeys {
			s.fail(m.ID, apiErr(CodeBadRequest, "keys takes between 1 and 64 key names"))
			return
		}
		if err := s.call(proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: m.Pane, Keys: m.Keys}, nil); err != nil {
			s.fail(m.ID, fromServer(err))
		}
	}
}

// pump passes on what the server says, for as long as the socket lives.
func (s *socket) pump() {
	for {
		select {
		case msg, ok := <-s.c.Events:
			if !ok {
				s.bye(ByeServerGone)
				return
			}
			if paired, _ := s.allowed(PermView); !paired {
				s.bye(ByeRevoked)
				return
			}
			s.event(msg)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *socket) event(msg proto.Message) {
	switch msg.Event {
	case proto.EventPaneFrame:
		var f proto.Frame
		if json.Unmarshal(msg.Data, &f) != nil {
			return
		}
		s.mu.Lock()
		open := s.open[f.ID]
		s.mu.Unlock()
		if !open {
			return
		}
		if f.Lines == nil {
			f.Lines = []string{}
		}
		s.send(ServerMessage{Type: MsgFrame, Frame: &Frame{Pane: f.ID, Cols: f.Cols, Rows: f.Rows, Lines: f.Lines,
			Offset: f.Offset, History: f.History, AltScreen: f.AltScreen}})

	case proto.EventPaneUpdated, proto.EventPaneCreated:
		var p proto.PaneInfo
		if json.Unmarshal(msg.Data, &p) != nil {
			return
		}
		s.mu.Lock()
		watching, watchingPanes := s.watching, s.watchingPanes
		s.mu.Unlock()
		if watchingPanes {
			s.paneChanged(p)
		}
		if !watching {
			return
		}
		if !isAgent(p) {
			s.gone(p.ID)
			return
		}
		ctx, cancel := context.WithTimeout(s.ctx, callTimeout)
		a := agentOf(ctx, s.c, p, s.projectNames(ctx, p.ProjectID))
		cancel()
		s.mu.Lock()
		s.known[p.ID] = true
		s.mu.Unlock()
		s.send(ServerMessage{Type: MsgAgent, Agent: &a})

	case proto.EventPaneExited, proto.EventPaneClosed:
		var ref proto.PaneRef
		if json.Unmarshal(msg.Data, &ref) != nil {
			return
		}
		s.mu.Lock()
		delete(s.open, ref.ID)
		wasPane := s.knownPanes[ref.ID]
		delete(s.knownPanes, ref.ID)
		s.mu.Unlock()
		s.gone(ref.ID)
		if wasPane {
			s.send(ServerMessage{Type: MsgPaneGone, Pane: ref.ID})
		}
	}
}

// paneChanged tells a phone watching every pane about one that appeared
// or changed, or that it has gone when it no longer runs.
func (s *socket) paneChanged(p proto.PaneInfo) {
	if !isRunning(p) {
		s.mu.Lock()
		was := s.knownPanes[p.ID]
		delete(s.knownPanes, p.ID)
		s.mu.Unlock()
		if was {
			s.send(ServerMessage{Type: MsgPaneGone, Pane: p.ID})
		}
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, callTimeout)
	pn := paneOf(ctx, s.c, p, s.projectNames(ctx, p.ProjectID))
	cancel()
	s.mu.Lock()
	s.knownPanes[p.ID] = true
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
func (s *socket) projectNames(ctx context.Context, id string) map[string]string {
	s.mu.Lock()
	_, have := s.projects[id]
	names := s.projects
	s.mu.Unlock()
	if id == "" || have {
		return names
	}
	fresh, err := projectNames(ctx, s.c)
	if err != nil {
		return names
	}
	s.mu.Lock()
	s.projects = fresh
	s.mu.Unlock()
	return fresh
}
