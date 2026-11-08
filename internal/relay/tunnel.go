package relay

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/flynn/noise"
)

// The tunnel: what a phone and a gateway say to each other inside the
// Noise session. Each direction is a byte stream of frames, a frame being
// a four-byte big-endian length and a JSON object; the stream is cut into
// Noise messages however it falls. The gateway's direction is deflated as
// one stream, flushed at every frame, because it carries screens — text
// that compresses about fifteen times — and a phone is often on mobile
// data; it is compressed before it is encrypted, the only order in which
// compression does anything.
//
// The frames are a phone's HTTP calls and WebSockets carried whole:
//
//	phone → gateway  {"t":"req","id":1,"m":"GET","p":"/api/hello","h":{…},"b":"…"}
//	gateway → phone  {"t":"res","id":1,"s":200,"h":{…},"b":"…"}
//	phone → gateway  {"t":"wo","id":2,"p":"/api/socket","h":{…}}   open a socket
//	gateway → phone  {"t":"wo","id":2}                               it is open
//	either way       {"t":"wm","id":2,"d":"…"}                       a message on it
//	either way       {"t":"wc","id":2,"c":1000,"r":"…"}              it is closed
//
// The gateway serves them with its own handler, reached through an
// in-memory listener, so every route, permission and check is the one a
// phone on the network meets.

// Protocol is the Noise protocol, and Prologue binds a session to conch's
// use of it: a handshake made for something else does not complete here.
const (
	Prologue = "conch-relay-v1"
	// TunnelHost is the address the gateway's handler sees the tunnel's
	// calls at, and their Origin: its own, so its same-origin checks hold.
	TunnelHost = "conch-relay"
)

// Suite is Noise_NK_25519_AESGCM_SHA256: every part of it is in a
// browser's WebCrypto.
var Suite = noise.NewCipherSuite(noise.DH25519, noise.CipherAESGCM, noise.HashSHA256)

// Limits on what is carried. A frame is one call or one socket message.
const (
	maxFrame      = 8 << 20
	maxRequest    = 1 << 20
	maxResponse   = 8 << 20
	maxCalls      = 32
	noiseOverhead = 16
	maxPlain      = noise.MaxMsgLen - noiseOverhead
)

// Frame is one message of the tunnel.
type Frame struct {
	T  string            `json:"t"`
	ID int64             `json:"id"`
	M  string            `json:"m,omitempty"`
	P  string            `json:"p,omitempty"`
	H  map[string]string `json:"h,omitempty"`
	B  string            `json:"b,omitempty"`
	S  int               `json:"s,omitempty"`
	D  string            `json:"d,omitempty"`
	C  int               `json:"c,omitempty"`
	R  string            `json:"r,omitempty"`
}

// Hello is each side's handshake payload: the version, and on the
// gateway's side how its direction is compressed.
type Hello struct {
	V int    `json:"v"`
	Z string `json:"z,omitempty"`
}

// HandshakeVersion is the tunnel's version. A phone app speaking another
// is told so rather than half understood.
const HandshakeVersion = 1

// ---- the pieces both ends share ----

// sealer writes a stream of frames as Noise messages on a WebSocket,
// deflating it first when z is set. It is safe for several writers.
type sealer struct {
	mu   sync.Mutex
	c    *websocket.Conn
	cs   *noise.CipherState
	z    *flate.Writer
	zbuf bytes.Buffer
}

func newSealer(c *websocket.Conn, cs *noise.CipherState, deflate bool) *sealer {
	s := &sealer{c: c, cs: cs}
	if deflate {
		s.z, _ = flate.NewWriter(&s.zbuf, flate.BestSpeed)
	}
	return s
}

// errTooBig is a frame over maxFrame: nothing of it was sent, so the
// session goes on and the other end can be told instead.
var errTooBig = errors.New("a frame over the tunnel's limit")

func (s *sealer) send(ctx context.Context, f Frame) error {
	body, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(body) > maxFrame {
		return fmt.Errorf("%w: %d bytes, over %d", errTooBig, len(body), maxFrame)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(body)))
	out := append(head[:], body...)
	if s.z != nil {
		s.zbuf.Reset()
		if _, err := s.z.Write(out); err != nil {
			return err
		}
		if err := s.z.Flush(); err != nil {
			return err
		}
		out = s.zbuf.Bytes()
	}
	for len(out) > 0 {
		n := min(len(out), maxPlain)
		msg, err := s.cs.Encrypt(nil, nil, out[:n])
		if err != nil {
			return err
		}
		if err := s.c.Write(ctx, websocket.MessageBinary, msg); err != nil {
			return err
		}
		out = out[n:]
	}
	return nil
}

// opener reads Noise messages off a WebSocket and gives back the frames
// in them, inflating first when the other side deflates. A goroutine
// decrypts into a pipe, so the inflater reads a stream that waits for
// more rather than one that ends: flate cannot carry on after an EOF.
type opener struct {
	r io.Reader
}

func newOpener(ctx context.Context, c *websocket.Conn, cs *noise.CipherState, inflate bool) *opener {
	pr, pw := io.Pipe()
	go func() {
		for {
			typ, msg, err := c.Read(ctx)
			if err != nil {
				pw.CloseWithError(err)
				return
			}
			if typ != websocket.MessageBinary {
				pw.CloseWithError(errors.New("a text message inside the tunnel"))
				return
			}
			plain, err := cs.Decrypt(nil, nil, msg)
			if err != nil {
				pw.CloseWithError(errors.New("a message that doesn't decrypt: not from the other end of this session"))
				return
			}
			if _, err := pw.Write(plain); err != nil {
				return
			}
		}
	}()
	var r io.Reader = pr
	if inflate {
		r = flate.NewReader(pr)
	}
	return &opener{r: r}
}

func (o *opener) next() (Frame, error) {
	var head [4]byte
	if _, err := io.ReadFull(o.r, head[:]); err != nil {
		return Frame{}, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n > maxFrame {
		return Frame{}, fmt.Errorf("a %d-byte frame is over the tunnel's %d", n, maxFrame)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(o.r, body); err != nil {
		return Frame{}, err
	}
	var f Frame
	if err := json.Unmarshal(body, &f); err != nil {
		return Frame{}, fmt.Errorf("a frame that isn't JSON: %w", err)
	}
	return f, nil
}

// ---- the gateway's end ----

// memListener hands the tunnel's connections to an http.Server: the
// gateway's own handler serves calls that came through the relay.
type memListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newMemListener() *memListener {
	return &memListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *memListener) Addr() net.Addr { return memAddr{} }

// dial connects to the listener; the handler sees the connection as
// coming from remote.
func (l *memListener) dial(ctx context.Context, remote net.Addr) (net.Conn, error) {
	a, b := net.Pipe()
	select {
	case l.conns <- addrConn{Conn: b, remote: remote}:
		return a, nil
	case <-l.done:
		a.Close()
		b.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		a.Close()
		b.Close()
		return nil, ctx.Err()
	}
}

// addrConn is a pipe's end with the address the handler is to see.
type addrConn struct {
	net.Conn
	remote net.Addr
}

func (c addrConn) RemoteAddr() net.Addr {
	if c.remote == nil {
		return c.Conn.RemoteAddr()
	}
	return c.remote
}

// relayAddr is a phone session's address, as the gateway's handler sees it.
type relayAddr string

func (relayAddr) Network() string  { return "relay" }
func (a relayAddr) String() string { return string(a) }

type memAddr struct{}

func (memAddr) Network() string { return "relay" }
func (memAddr) String() string  { return TunnelHost }

// passIn is what a phone's call may carry to the gateway, and passOut what
// comes back: the device's cookie and the app's own headers, nothing a
// relay or a browser added.
var (
	passIn  = []string{"Content-Type", "X-CSRF-Token", "Cookie"}
	passOut = []string{"Content-Type", "Set-Cookie", "Retry-After"}
)

// sockQueue is how many of a phone's messages may wait for a socket on
// the gateway that is not reading; past it the socket is closed rather
// than hold up the session, which every call and socket shares.
const sockQueue = 256

// tunnelSocket is a phone's WebSocket on the gateway, and what waits to
// be written to it.
type tunnelSocket struct {
	c    *websocket.Conn
	in   chan string   // never closed: a message for a socket just closed is dropped
	done chan struct{} // closed by close
	once sync.Once
}

// close closes the socket without waiting for the other end to answer,
// which a socket that isn't reading never does: it may be called with
// the session's lock held.
func (t *tunnelSocket) close(code websocket.StatusCode) {
	t.once.Do(func() {
		close(t.done)
		go t.c.Close(code, "")
	})
}

// serveSession is one phone's session on the gateway: frames in, calls on
// the gateway's handler, answers out. It returns when the phone goes.
func serveSession(ctx context.Context, c *websocket.Conn, recv, send *noise.CipherState, hc *http.Client, logf func(string, ...any)) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := newSealer(c, send, true)
	in := newOpener(ctx, c, recv, false)
	calls := make(chan struct{}, maxCalls)
	var mu sync.Mutex
	sockets := map[int64]*tunnelSocket{}
	// opening are sockets asked for and not yet open; a close that arrives
	// meanwhile is kept in cancelled, and the socket closed when it opens.
	opening := map[int64]bool{}
	cancelled := map[int64]bool{}
	defer func() {
		mu.Lock()
		for _, s := range sockets {
			s.close(websocket.StatusGoingAway)
		}
		mu.Unlock()
	}()
	for {
		f, err := in.next()
		if err != nil {
			if ctx.Err() == nil && websocket.CloseStatus(err) == -1 && !errors.Is(err, io.EOF) {
				logf("relay: a phone's session ended: %v", err)
			}
			return
		}
		switch f.T {
		case "req":
			select {
			case calls <- struct{}{}:
			default:
				_ = out.send(ctx, Frame{T: "res", ID: f.ID, S: http.StatusTooManyRequests, B: `{"error":{"code":"rate_limited","message":"too many calls at once"}}`,
					H: map[string]string{"Content-Type": "application/json"}})
				continue
			}
			go func(f Frame) {
				defer func() { <-calls }()
				if err := out.send(ctx, call(ctx, hc, f)); errors.Is(err, errTooBig) {
					// Said, or the phone waits on this call for ever.
					_ = out.send(ctx, failFrame(f.ID, http.StatusBadGateway, "the gateway's answer is too large for the tunnel"))
				}
			}(f)
		case "wo":
			mu.Lock()
			opening[f.ID] = true
			mu.Unlock()
			go func(f Frame) {
				c, err := openSocket(ctx, hc, f)
				mu.Lock()
				delete(opening, f.ID)
				gone := cancelled[f.ID]
				delete(cancelled, f.ID)
				if err != nil || gone {
					mu.Unlock()
					if err != nil && !gone {
						_ = out.send(ctx, Frame{T: "wc", ID: f.ID, C: int(websocket.StatusInternalError), R: err.Error()})
					}
					if c != nil {
						c.Close(websocket.StatusNormalClosure, "")
					}
					return
				}
				s := &tunnelSocket{c: c, in: make(chan string, sockQueue), done: make(chan struct{})}
				if old := sockets[f.ID]; old != nil {
					old.close(websocket.StatusNormalClosure)
				}
				sockets[f.ID] = s
				mu.Unlock()
				// What the phone sends is written here, in the order it
				// came, and not on the session's reading goroutine: a
				// socket slow to read would hold up every call.
				go func() {
					for {
						select {
						case m := <-s.in:
							if s.c.Write(ctx, websocket.MessageText, []byte(m)) != nil {
								return
							}
						case <-s.done:
							return
						}
					}
				}()
				if out.send(ctx, Frame{T: "wo", ID: f.ID}) != nil {
					s.close(websocket.StatusGoingAway)
					return
				}
				for {
					_, msg, err := s.c.Read(ctx)
					code := 0
					switch {
					case err != nil:
						code = int(websocket.CloseStatus(err))
						if code < 0 {
							code = int(websocket.StatusGoingAway)
						}
					default:
						err = out.send(ctx, Frame{T: "wm", ID: f.ID, D: string(msg)})
						if errors.Is(err, errTooBig) {
							// The phone is told the socket is gone, rather
							// than left with one that has gone quiet.
							code = int(websocket.StatusMessageTooBig)
						} else if err != nil {
							s.close(websocket.StatusGoingAway)
							return
						}
					}
					if code == 0 {
						continue
					}
					// Whoever takes the socket out of the map tells the
					// phone: if the phone closed it, or it was given up
					// for falling behind, that has been said already.
					mu.Lock()
					mine := sockets[f.ID] == s
					if mine {
						delete(sockets, f.ID)
					}
					mu.Unlock()
					s.close(websocket.StatusCode(code))
					if mine {
						_ = out.send(ctx, Frame{T: "wc", ID: f.ID, C: code})
					}
					return
				}
			}(f)
		case "wm":
			mu.Lock()
			s := sockets[f.ID]
			if s != nil {
				select {
				case s.in <- f.D:
				default:
					// The gateway's socket isn't reading: give it up.
					delete(sockets, f.ID)
					s.close(websocket.StatusTryAgainLater)
					go func(id int64) {
						_ = out.send(ctx, Frame{T: "wc", ID: id, C: int(websocket.StatusTryAgainLater), R: "the gateway fell behind"})
					}(f.ID)
				}
			}
			mu.Unlock()
		case "wc":
			mu.Lock()
			s := sockets[f.ID]
			delete(sockets, f.ID)
			if s == nil && opening[f.ID] {
				cancelled[f.ID] = true
			}
			mu.Unlock()
			if s != nil {
				s.close(websocket.StatusNormalClosure)
			}
		}
	}
}

// failFrame is an answer the tunnel gives itself, in the gateway's shape.
func failFrame(id int64, status int, msg string) Frame {
	b, _ := json.Marshal(map[string]any{"error": map[string]string{"code": "bad_request", "message": msg}})
	return Frame{T: "res", ID: id, S: status, B: string(b), H: map[string]string{"Content-Type": "application/json"}}
}

// call makes one of a phone's calls on the gateway's handler.
func call(ctx context.Context, hc *http.Client, f Frame) Frame {
	fail := func(status int, msg string) Frame { return failFrame(f.ID, status, msg) }
	if !strings.HasPrefix(f.P, "/") || strings.HasPrefix(f.P, "//") {
		return fail(http.StatusBadRequest, "a path on the gateway is needed")
	}
	if len(f.B) > maxRequest {
		return fail(http.StatusRequestEntityTooLarge, "the request is too large")
	}
	var body io.Reader
	if f.B != "" {
		body = strings.NewReader(f.B)
	}
	req, err := http.NewRequestWithContext(ctx, f.M, "http://"+TunnelHost+f.P, body)
	if err != nil {
		return fail(http.StatusBadRequest, "the request can't be made: "+err.Error())
	}
	for _, k := range passIn {
		if v := f.H[k]; v != "" {
			req.Header.Set(k, v)
		}
	}
	// The gateway's own origin: the page is the gateway's app, served by
	// the relay, and the session is what makes it the gateway's.
	req.Header.Set("Origin", "https://"+TunnelHost)
	res, err := hc.Do(req)
	if err != nil {
		return fail(http.StatusBadGateway, "the gateway didn't answer: "+err.Error())
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil || len(b) > maxResponse {
		return fail(http.StatusBadGateway, "the gateway's answer couldn't be read")
	}
	h := map[string]string{}
	for _, k := range passOut {
		if vs := res.Header.Values(k); len(vs) > 0 {
			h[k] = strings.Join(vs, "\n")
		}
	}
	return Frame{T: "res", ID: f.ID, S: res.StatusCode, H: h, B: string(b)}
}

// openSocket opens one of a phone's WebSockets on the gateway's handler.
func openSocket(ctx context.Context, hc *http.Client, f Frame) (*websocket.Conn, error) {
	if !strings.HasPrefix(f.P, "/") || strings.HasPrefix(f.P, "//") {
		return nil, errors.New("a path on the gateway is needed")
	}
	h := http.Header{}
	if v := f.H["Cookie"]; v != "" {
		h.Set("Cookie", v)
	}
	h.Set("Origin", "https://"+TunnelHost)
	s, _, err := websocket.Dial(ctx, "ws://"+TunnelHost+f.P, &websocket.DialOptions{HTTPClient: hc, HTTPHeader: h})
	if err != nil {
		return nil, err
	}
	s.SetReadLimit(maxFrame)
	return s, nil
}
