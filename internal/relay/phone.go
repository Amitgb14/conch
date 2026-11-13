package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/flynn/noise"
)

// Session is a phone's end of the tunnel, in Go: what the phone's app does
// in tunnel.mjs. conch uses it to check a relay works end to end, and the
// tests to drive a gateway through one.
type Session struct {
	c   *websocket.Conn
	out *sealer
	ctx context.Context
	end context.CancelFunc

	mu      sync.Mutex
	next    int64
	calls   map[int64]chan Frame
	sockets map[int64]*Socket
	err     error
}

// ErrNoGateway is the relay saying the computer isn't connected to it.
var ErrNoGateway = errors.New("the computer isn't connected to the relay")

// ErrTicketRefused and ErrRevoked are a cloud relay closing a client's
// socket: its ticket was not good (used, expired, another room's), or
// the device it was given to has been signed out.
var (
	ErrTicketRefused = errors.New("the relay refused the ticket")
	ErrRevoked       = errors.New("this device was signed out of the account")
)

// closedErr names the close codes a client can act on.
func closedErr(err error) error {
	switch websocket.CloseStatus(err) {
	case CodeNoGateway:
		return ErrNoGateway
	case CodeBadTicket:
		return ErrTicketRefused
	case CodeRevoked:
		return ErrRevoked
	}
	return err
}

// Dial opens a session with the gateway in room, whose key is gwPub (from
// the pairing link): the relay first, then the handshake through it.
func Dial(ctx context.Context, relay, room string, gwPub []byte) (*Session, error) {
	return DialTicket(ctx, relay, room, "", gwPub)
}

// TicketHeader carries a ticket to a relay that admits a room's clients
// only with one (Conch Cloud's, PROTOCOL §3.2): single use, short-lived,
// handed out by the control plane. A relay of conch's own ignores it.
const TicketHeader = "X-Conch-Relay-Ticket"

// DialTicket is Dial with a ticket for the relay, when it has one.
func DialTicket(ctx context.Context, relay, room, ticket string, gwPub []byte) (*Session, error) {
	u, err := wsURL(relay, PhonePath, url.Values{"room": {room}})
	if err != nil {
		return nil, err
	}
	var opts *websocket.DialOptions
	if ticket != "" {
		opts = &websocket.DialOptions{HTTPHeader: http.Header{TicketHeader: {ticket}}}
	}
	c, res, err := websocket.Dial(ctx, u, opts)
	if err != nil {
		if res != nil && res.StatusCode == http.StatusUnauthorized {
			return nil, ErrTicketRefused
		}
		return nil, err
	}
	c.SetReadLimit(maxMessage)
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: Suite, Pattern: noise.HandshakeNK, Initiator: true, Prologue: []byte(Prologue), PeerStatic: gwPub,
	})
	if err != nil {
		c.CloseNow()
		return nil, err
	}
	hello, _ := json.Marshal(Hello{V: HandshakeVersion})
	msg, _, _, err := hs.WriteMessage(nil, hello)
	if err != nil {
		c.CloseNow()
		return nil, err
	}
	if err := c.Write(ctx, websocket.MessageBinary, msg); err != nil {
		c.CloseNow()
		return nil, err
	}
	_, reply, err := c.Read(ctx)
	if err != nil {
		c.CloseNow()
		return nil, closedErr(err)
	}
	payload, send, recv, err := hs.ReadMessage(nil, reply)
	if err != nil {
		c.CloseNow()
		return nil, errors.New("the answer isn't from the computer this phone paired with")
	}
	var hi Hello
	if json.Unmarshal(payload, &hi) != nil || hi.V != HandshakeVersion {
		c.CloseNow()
		return nil, fmt.Errorf("the computer speaks tunnel version %d, this %d", hi.V, HandshakeVersion)
	}
	sctx, end := context.WithCancel(context.Background())
	s := &Session{c: c, out: newSealer(c, send, false), ctx: sctx, end: end,
		calls: map[int64]chan Frame{}, sockets: map[int64]*Socket{}}
	in := newOpener(sctx, c, recv, hi.Z == "deflate-raw")
	go s.read(in)
	return s, nil
}

func (s *Session) read(in *opener) {
	for {
		f, err := in.next()
		if err != nil {
			s.close(closedErr(err))
			return
		}
		s.mu.Lock()
		switch f.T {
		case "res":
			if ch := s.calls[f.ID]; ch != nil {
				delete(s.calls, f.ID)
				ch <- f
			}
		case "wo", "wm", "wc":
			if k := s.sockets[f.ID]; k != nil {
				k.take(f)
				if f.T == "wc" {
					delete(s.sockets, f.ID)
				}
			}
		}
		s.mu.Unlock()
	}
}

func (s *Session) close(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	for id, ch := range s.calls {
		close(ch)
		delete(s.calls, id)
	}
	for id, k := range s.sockets {
		k.take(Frame{T: "wc", ID: id, C: int(websocket.StatusGoingAway)})
		delete(s.sockets, id)
	}
	s.mu.Unlock()
	s.end()
}

// Err is why the session ended, or nil while it lasts.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Done is closed when the session ends.
func (s *Session) Done() <-chan struct{} { return s.ctx.Done() }

// Close ends the session.
func (s *Session) Close() {
	s.c.Close(websocket.StatusNormalClosure, "")
	s.close(errors.New("closed"))
}

// Do makes one call on the gateway and waits for its answer.
func (s *Session) Do(ctx context.Context, method, path string, h map[string]string, body string) (Frame, error) {
	ch := make(chan Frame, 1)
	s.mu.Lock()
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return Frame{}, err
	}
	s.next++
	id := s.next
	s.calls[id] = ch
	s.mu.Unlock()
	if err := s.out.send(ctx, Frame{T: "req", ID: id, M: method, P: path, H: h, B: body}); err != nil {
		return Frame{}, err
	}
	select {
	case f, ok := <-ch:
		if !ok {
			return Frame{}, errors.New("the session ended")
		}
		return f, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.calls, id)
		s.mu.Unlock()
		return Frame{}, ctx.Err()
	}
}

// Socket is a WebSocket on the gateway, carried by the session.
type Socket struct {
	s      *Session
	id     int64
	opened chan struct{}
	msgs   chan string
	done   chan struct{}
	once   sync.Once
	code   int
	reason string
}

func (k *Socket) take(f Frame) {
	switch f.T {
	case "wo":
		select {
		case <-k.opened:
		default:
			close(k.opened)
		}
	case "wm":
		select {
		case k.msgs <- f.D:
		case <-k.done:
		}
	case "wc":
		k.once.Do(func() {
			k.code, k.reason = f.C, f.R
			close(k.done)
		})
	}
}

// Socket opens a WebSocket at path on the gateway.
func (s *Session) Socket(ctx context.Context, path string, h map[string]string) (*Socket, error) {
	s.mu.Lock()
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return nil, err
	}
	s.next++
	k := &Socket{s: s, id: s.next, opened: make(chan struct{}), msgs: make(chan string, 256), done: make(chan struct{})}
	s.sockets[k.id] = k
	s.mu.Unlock()
	if err := s.out.send(ctx, Frame{T: "wo", ID: k.id, P: path, H: h}); err != nil {
		return nil, err
	}
	select {
	case <-k.opened:
		return k, nil
	case <-k.done:
		return nil, fmt.Errorf("the socket was refused: %s (%d)", k.reason, k.code)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Send writes a message on the socket.
func (k *Socket) Send(ctx context.Context, text string) error {
	return k.s.out.send(ctx, Frame{T: "wm", ID: k.id, D: text})
}

// Recv waits for the socket's next message. Messages that came before the
// socket closed are still given, in order: a script's exit status is its
// last message, sent just before the close.
func (k *Socket) Recv(ctx context.Context) (string, error) {
	select {
	case m := <-k.msgs:
		return m, nil
	default:
	}
	select {
	case m := <-k.msgs:
		return m, nil
	case <-k.done:
		select {
		case m := <-k.msgs:
			return m, nil
		default:
		}
		return "", fmt.Errorf("the socket closed (%d)", k.code)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Close closes the socket.
func (k *Socket) Close() {
	ctx, cancel := context.WithTimeout(k.s.ctx, time.Second)
	defer cancel()
	_ = k.s.out.send(ctx, Frame{T: "wc", ID: k.id, C: int(websocket.StatusNormalClosure)})
}
