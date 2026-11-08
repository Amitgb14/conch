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

func (s *sealer) send(ctx context.Context, f Frame) error {
	body, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(body) > maxFrame {
		return fmt.Errorf("a %d-byte frame is over the tunnel's %d", len(body), maxFrame)
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

func (l *memListener) dial(ctx context.Context) (net.Conn, error) {
	a, b := net.Pipe()
	select {
	case l.conns <- b:
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

// serveSession is one phone's session on the gateway: frames in, calls on
// the gateway's handler, answers out. It returns when the phone goes.
func serveSession(ctx context.Context, c *websocket.Conn, recv, send *noise.CipherState, hc *http.Client, logf func(string, ...any)) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := newSealer(c, send, true)
	in := newOpener(ctx, c, recv, false)
	calls := make(chan struct{}, maxCalls)
	var mu sync.Mutex
	sockets := map[int64]*websocket.Conn{}
	defer func() {
		mu.Lock()
		for _, s := range sockets {
			s.CloseNow()
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
				_ = out.send(ctx, call(ctx, hc, f))
			}(f)
		case "wo":
			go func(f Frame) {
				s, err := openSocket(ctx, hc, f)
				if err != nil {
					_ = out.send(ctx, Frame{T: "wc", ID: f.ID, C: int(websocket.StatusInternalError), R: err.Error()})
					return
				}
				mu.Lock()
				if old := sockets[f.ID]; old != nil {
					old.CloseNow()
				}
				sockets[f.ID] = s
				mu.Unlock()
				if out.send(ctx, Frame{T: "wo", ID: f.ID}) != nil {
					s.CloseNow()
					return
				}
				for {
					_, msg, err := s.Read(ctx)
					if err != nil {
						code := int(websocket.CloseStatus(err))
						if code < 0 {
							code = int(websocket.StatusGoingAway)
						}
						mu.Lock()
						if sockets[f.ID] == s {
							delete(sockets, f.ID)
						}
						mu.Unlock()
						_ = out.send(ctx, Frame{T: "wc", ID: f.ID, C: code})
						return
					}
					if out.send(ctx, Frame{T: "wm", ID: f.ID, D: string(msg)}) != nil {
						s.CloseNow()
						return
					}
				}
			}(f)
		case "wm":
			mu.Lock()
			s := sockets[f.ID]
			mu.Unlock()
			if s != nil {
				// Keep the order the phone sent them in: written here, on
				// the reading goroutine.
				_ = s.Write(ctx, websocket.MessageText, []byte(f.D))
			}
		case "wc":
			mu.Lock()
			s := sockets[f.ID]
			delete(sockets, f.ID)
			mu.Unlock()
			if s != nil {
				s.Close(websocket.StatusNormalClosure, "")
			}
		}
	}
}

// call makes one of a phone's calls on the gateway's handler.
func call(ctx context.Context, hc *http.Client, f Frame) Frame {
	fail := func(status int, msg string) Frame {
		b, _ := json.Marshal(map[string]any{"error": map[string]string{"code": "bad_request", "message": msg}})
		return Frame{T: "res", ID: f.ID, S: status, B: string(b), H: map[string]string{"Content-Type": "application/json"}}
	}
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
