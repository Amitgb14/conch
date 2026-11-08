// Package relay is how a phone reaches conch web without Tailscale: both
// connect out to a relay, which pairs a phone's WebSocket with one the
// gateway opens for it and copies messages between the two. What it
// copies is a Noise session the phone and the gateway made with a key the
// relay never sees (the gateway's, carried in the pairing QR code), so it
// forwards screens and keystrokes it cannot read and could not forge.
//
// A relay knows rooms, not people. A room's id is a hash of a secret only
// its gateway holds, so a phone — which knows the id — can ask for the
// gateway but cannot stand in for it. Nothing is stored: a relay that
// restarts loses its connections and the gateways make them again.
package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// The relay's addresses. A gateway holds GatewayPath open and opens
// AcceptPath once for each phone; a phone opens PhonePath.
const (
	GatewayPath = "/relay/gateway"
	AcceptPath  = "/relay/accept"
	PhonePath   = "/relay/phone"
	HealthPath  = "/relay/health"
	// SecretHeader carries a gateway's room secret. A header, not the
	// query, so it is in no access log along the way.
	SecretHeader = "X-Conch-Relay-Secret"
)

// Close codes a phone is told, so it can say which part is missing
// rather than spin: the relay answered, but the gateway isn't there.
const (
	CodeNoGateway = 4404 // the room's gateway is not connected
	CodeBusy      = 4429 // the room has as many phones as it takes
	CodeReplaced  = 4409 // a newer connection from the same gateway took over
	CodeTimeout   = 4408 // the gateway didn't take the phone in time
)

// Limits. A room is one gateway; phones beyond maxPhones are a mistake or
// an attack. maxMessage is above a Noise message (65535) with room for the
// WebSocket's own framing.
var maxPhones = 16 // a variable so a test can fill a room

const (
	maxRooms    = 10000
	maxMessage  = 1 << 17
	acceptWait  = 10 * time.Second
	pingEvery   = 25 * time.Second
	writeWithin = 10 * time.Second
)

// NewSecret makes a gateway's room secret: 256 random bits.
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// RoomID is the room a secret opens: public, since a phone has to name
// it, and useless without the secret for anything but asking for the
// gateway.
func RoomID(secret string) string {
	sum := sha256.Sum256([]byte("conch-relay-room\x00" + secret))
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

// validRoom is the shape RoomID gives: 22 base64url characters.
func validRoom(id string) bool {
	if len(id) != 22 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// Open is what the relay tells a gateway when a phone arrives: open
// AcceptPath with this id to take it.
type Open struct {
	Open string `json:"open"`
}

// Server is a relay. UI, when set, is served for everything that isn't
// the relay's own — the phone's app, so a phone can open the relay's
// address and pair.
type Server struct {
	UI   http.Handler
	Logf func(format string, args ...any)

	mu    sync.Mutex
	rooms map[string]*room
}

type room struct {
	gw      *websocket.Conn
	gwWrite *sync.Mutex
	// waiting are phones the gateway has been told about, by id.
	waiting map[string]chan *websocket.Conn
	phones  int
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// Close tells every gateway the relay is going, so each reconnects (to
// this relay restarted, or another) rather than wait on a dead one.
func (s *Server) Close() {
	s.mu.Lock()
	rooms := s.rooms
	s.rooms = nil
	s.mu.Unlock()
	for _, rm := range rooms {
		rm.gw.Close(websocket.StatusGoingAway, "the relay is stopping")
	}
}

// ServeHTTP routes the relay's paths, and the app for the rest.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case HealthPath:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	case GatewayPath:
		s.serveGateway(w, r)
	case AcceptPath:
		s.serveAccept(w, r)
	case PhonePath:
		s.servePhone(w, r)
	default:
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/pair" {
			// The app's calls go through the tunnel, never to the relay.
			http.Error(w, "this is a conch relay: the gateway's API is reached through it, not on it", http.StatusNotFound)
			return
		}
		if s.UI == nil {
			http.NotFound(w, r)
			return
		}
		s.UI.ServeHTTP(w, r)
	}
}

// gatewayRoom checks a gateway's secret against the room it names.
func gatewayRoom(r *http.Request) (string, bool) {
	id := r.URL.Query().Get("room")
	secret := r.Header.Get(SecretHeader)
	if !validRoom(id) || secret == "" {
		return "", false
	}
	return id, subtle.ConstantTimeCompare([]byte(RoomID(secret)), []byte(id)) == 1
}

func (s *Server) serveGateway(w http.ResponseWriter, r *http.Request) {
	id, ok := gatewayRoom(r)
	if !ok {
		http.Error(w, "not this room's gateway", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	if s.rooms == nil {
		s.rooms = map[string]*room{}
	}
	if _, there := s.rooms[id]; !there && len(s.rooms) >= maxRooms {
		s.mu.Unlock()
		http.Error(w, "this relay is full", http.StatusServiceUnavailable)
		return
	}
	s.mu.Unlock()
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(4096)
	rm := &room{gw: c, gwWrite: &sync.Mutex{}, waiting: map[string]chan *websocket.Conn{}}
	s.mu.Lock()
	old := s.rooms[id]
	s.rooms[id] = rm
	s.mu.Unlock()
	if old != nil {
		// The same gateway again — restarted, or its network changed
		// before the relay noticed the old connection was gone.
		old.gw.Close(CodeReplaced, "replaced by a newer connection")
	}
	s.logf("relay: gateway connected to %s", short(id))

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, pcancel := context.WithTimeout(ctx, writeWithin)
				err := c.Ping(pctx)
				pcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	// The gateway sends nothing; reading is how its going is noticed.
	for {
		if _, _, err := c.Read(ctx); err != nil {
			break
		}
	}
	s.mu.Lock()
	if s.rooms[id] == rm {
		delete(s.rooms, id)
	}
	s.mu.Unlock()
	c.CloseNow()
	s.logf("relay: gateway left %s", short(id))
}

func (s *Server) servePhone(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("room")
	if !validRoom(id) {
		http.Error(w, "no such room", http.StatusBadRequest)
		return
	}
	// Same-origin only (the default): a phone's page is the relay's own.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(maxMessage)
	phoneID, err := NewSecret()
	if err != nil {
		c.Close(websocket.StatusInternalError, "")
		return
	}
	ch := make(chan *websocket.Conn, 1)
	s.mu.Lock()
	rm := s.rooms[id]
	switch {
	case rm == nil:
		s.mu.Unlock()
		c.Close(CodeNoGateway, "the computer isn't connected to the relay")
		return
	case rm.phones >= maxPhones:
		s.mu.Unlock()
		c.Close(CodeBusy, "too many phones on this computer at once")
		return
	}
	rm.phones++
	rm.waiting[phoneID] = ch
	gw, gwWrite := rm.gw, rm.gwWrite
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(rm.waiting, phoneID)
		rm.phones--
		s.mu.Unlock()
	}()

	b, _ := json.Marshal(Open{Open: phoneID})
	gwWrite.Lock()
	wctx, wcancel := context.WithTimeout(r.Context(), writeWithin)
	err = gw.Write(wctx, websocket.MessageText, b)
	wcancel()
	gwWrite.Unlock()
	if err != nil {
		c.Close(CodeNoGateway, "the computer isn't connected to the relay")
		return
	}
	var other *websocket.Conn
	select {
	case other = <-ch:
	case <-time.After(acceptWait):
		c.Close(CodeTimeout, "the computer didn't answer")
		return
	case <-r.Context().Done():
		return
	}
	splice(r.Context(), c, other)
}

func (s *Server) serveAccept(w http.ResponseWriter, r *http.Request) {
	id, ok := gatewayRoom(r)
	if !ok {
		http.Error(w, "not this room's gateway", http.StatusForbidden)
		return
	}
	phoneID := r.URL.Query().Get("phone")
	s.mu.Lock()
	var ch chan *websocket.Conn
	if rm := s.rooms[id]; rm != nil {
		ch = rm.waiting[phoneID]
		delete(rm.waiting, phoneID)
	}
	s.mu.Unlock()
	if ch == nil {
		http.Error(w, "no phone waiting by that id", http.StatusNotFound)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(maxMessage)
	// The phone's handler does the copying; this one waits for it, since
	// returning would end the connection.
	done := make(chan struct{})
	go func() {
		<-r.Context().Done()
		close(done)
	}()
	ch <- c
	<-done
}

// splice copies messages both ways until either side goes, then closes
// both. Messages are copied whole and as they came: binary stays binary.
func splice(ctx context.Context, a, b *websocket.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	pipe := func(from, to *websocket.Conn) {
		defer wg.Done()
		defer cancel()
		for {
			typ, r, err := from.Reader(ctx)
			if err != nil {
				return
			}
			w, err := to.Writer(ctx, typ)
			if err != nil {
				return
			}
			if spliceTap != nil {
				r = io.TeeReader(r, tapWriter{})
			}
			if _, err := io.Copy(w, r); err != nil {
				return
			}
			if err := w.Close(); err != nil {
				return
			}
		}
	}
	go pipe(a, b)
	go pipe(b, a)
	wg.Wait()
	a.Close(websocket.StatusNormalClosure, "")
	b.Close(websocket.StatusNormalClosure, "")
}

// spliceTap, in a test, sees every byte the relay copies: what a relay
// run by somebody else could see.
var spliceTap func([]byte)

type tapWriter struct{}

func (tapWriter) Write(b []byte) (int, error) {
	spliceTap(append([]byte(nil), b...))
	return len(b), nil
}

// short is a room id as a log line says it: enough to tell rooms apart,
// not enough to open one.
func short(id string) string {
	if len(id) > 6 {
		return id[:6] + "…"
	}
	return id
}
