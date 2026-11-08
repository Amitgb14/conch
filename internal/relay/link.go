package relay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/flynn/noise"
)

// NewKey makes a gateway's static key, the one a phone learns from the
// pairing QR code.
func NewKey() (noise.DHKey, error) {
	return noise.DH25519.GenerateKeypair(rand.Reader)
}

// EncodeKey and DecodePublic are a key as it is written down: base64url.
func EncodeKey(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// KeyFromPrivate is a static key from its private half, as kept.
func KeyFromPrivate(private string) (noise.DHKey, error) {
	priv, err := base64.RawURLEncoding.DecodeString(private)
	if err != nil || len(priv) != 32 {
		return noise.DHKey{}, errors.New("not a relay key")
	}
	// X25519's public half from its private half: the base point times
	// the scalar, which is what a DH with the base point gives.
	pub, err := noise.DH25519.DH(priv, basePoint[:])
	if err != nil {
		return noise.DHKey{}, err
	}
	return noise.DHKey{Private: priv, Public: pub}, nil
}

var basePoint = [32]byte{9}

// Fingerprint is a key as four words, for a person to compare on the
// phone and on the computer: the same words, the same key.
func Fingerprint(pub []byte) string {
	sum := hashBytes(pub)
	var w []string
	for i := 0; i < 4; i++ {
		n := int(sum[2*i])<<8 | int(sum[2*i+1])
		w = append(w, wordList[n%len(wordList)])
	}
	return strings.Join(w, " ")
}

// HTTPURL is the relay's address as a page is opened at: https for wss.
func HTTPURL(relay string) (string, error) {
	u, err := url.Parse(strings.TrimRight(relay, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not a relay's address", relay)
	}
	switch u.Scheme {
	case "wss", "https":
		u.Scheme = "https"
	case "ws", "http":
		u.Scheme = "http"
	default:
		return "", fmt.Errorf("%q: a relay is wss:// (or https://)", relay)
	}
	u.Path, u.RawQuery, u.Fragment = strings.TrimRight(u.Path, "/"), "", ""
	return u.String(), nil
}

// wsURL is the relay's address for a WebSocket on path.
func wsURL(relay, path string, q url.Values) (string, error) {
	h, err := HTTPURL(relay)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(h)
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path += path
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Status is how a gateway's link to its relay is, for `conch web` to say
// and the settings to show.
type Status struct {
	Connected bool      `json:"connected"`
	Error     string    `json:"error,omitempty"`
	Since     time.Time `json:"since"`
	Phones    int       `json:"phones"`
}

// Link is a gateway's connection to a relay: it keeps its room open and
// serves every phone that arrives with Handler — the gateway's own.
type Link struct {
	Relay   string // wss://relay.example.com
	Secret  string
	Key     noise.DHKey
	Handler http.Handler
	Logf    func(format string, args ...any)
	// OnStatus, when set, is told every change of Status.
	OnStatus func(Status)

	mu     sync.Mutex
	status Status
}

func (l *Link) logf(format string, args ...any) {
	if l.Logf != nil {
		l.Logf(format, args...)
	}
}

// Status is the link as it is now.
func (l *Link) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status
}

func (l *Link) setStatus(fn func(*Status)) {
	l.mu.Lock()
	fn(&l.status)
	s := l.status
	l.mu.Unlock()
	if l.OnStatus != nil {
		l.OnStatus(s)
	}
}

// Room is the room this link holds.
func (l *Link) Room() string { return RoomID(l.Secret) }

// Run keeps the link up until ctx ends, reconnecting with a backoff, and
// serves phones as they come.
func (l *Link) Run(ctx context.Context) error {
	if _, err := HTTPURL(l.Relay); err != nil {
		return err
	}
	ln := newMemListener()
	srv := &http.Server{Handler: l.Handler, ReadHeaderTimeout: 10 * time.Second,
		ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
			return context.WithValue(ctx, throughKey{}, true)
		}}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		ln.Close()
	}()
	wait := time.Second
	for {
		up := time.Now()
		err := l.hold(ctx, ln)
		if ctx.Err() != nil {
			return nil
		}
		l.setStatus(func(s *Status) {
			s.Connected, s.Since = false, time.Now()
			if err != nil {
				s.Error = err.Error()
			}
		})
		if errors.Is(err, errReplaced) {
			// Another conch web holds the room now. Taking it back would
			// have the two take it from each other for ever, so this one
			// waits until the room is free — that one stopped — and only
			// then connects again.
			l.logf("relay: %v; waiting for it to stop", err)
			if !l.waitFree(ctx) {
				return nil
			}
			wait = time.Second
			continue
		}
		if time.Since(up) > time.Minute {
			wait = time.Second
		}
		l.logf("relay: not connected to %s (%v); trying again in %s", l.Relay, err, wait)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		wait = min(wait*2, 30*time.Second)
	}
}

func (l *Link) header() http.Header {
	h := http.Header{}
	h.Set(SecretHeader, l.Secret)
	return h
}

// errReplaced is the relay saying another connection took this room.
var errReplaced = errors.New("another conch web took this computer's room")

// replacedPoll is how often a replaced link asks whether its room is free.
var replacedPoll = 15 * time.Second // a variable so a test can wait less

// waitFree waits until the relay says the room has no gateway, or says
// something else — down, or an older relay that doesn't know the
// question — which the ordinary reconnect handles. It reports false when
// ctx ended first.
func (l *Link) waitFree(ctx context.Context) bool {
	u, err := wsURL(l.Relay, GatewayPath, url.Values{"room": {l.Room()}, "probe": {"1"}})
	if err != nil {
		return true
	}
	u = "http" + strings.TrimPrefix(u, "ws")
	for {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(replacedPoll):
		}
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		req, _ := http.NewRequestWithContext(rctx, http.MethodGet, u, nil)
		req.Header = l.header()
		res, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			return ctx.Err() == nil
		}
		res.Body.Close()
		if res.StatusCode != http.StatusConflict {
			return true
		}
	}
}

// throughKey marks a request that came through the relay.
type throughKey struct{}

// Through reports whether r reached the gateway's handler through a
// relay's tunnel, rather than over the network. Nothing a client sends
// can make it true: it is set on the in-memory connection itself.
func Through(r *http.Request) bool {
	v, _ := r.Context().Value(throughKey{}).(bool)
	return v
}

// sessions numbers a gateway's phone sessions, for their addresses.
var sessions atomic.Int64

// sessionClient is how one phone's session reaches the gateway's handler:
// through the in-memory listener, from an address of its own
// ("relay-7:0"), so what the gateway counts by address — pairing
// attempts — is counted for each phone rather than for every phone that
// came through the relay at once.
func sessionClient(ln *memListener) *http.Client {
	addr := relayAddr(fmt.Sprintf("relay-%d:0", sessions.Add(1)))
	return &http.Client{
		Transport: &http.Transport{
			DialContext:         func(ctx context.Context, _, _ string) (net.Conn, error) { return ln.dial(ctx, addr) },
			DisableCompression:  true,
			MaxIdleConnsPerHost: maxCalls,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// hold is one connection to the relay, for as long as it lasts.
func (l *Link) hold(ctx context.Context, ln *memListener) error {
	u, err := wsURL(l.Relay, GatewayPath, url.Values{"room": {l.Room()}})
	if err != nil {
		return err
	}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	c, res, err := websocket.Dial(dctx, u, &websocket.DialOptions{HTTPHeader: l.header()})
	cancel()
	if err != nil {
		if res != nil && res.StatusCode == http.StatusForbidden {
			return errors.New("the relay refused this computer's room secret")
		}
		return err
	}
	defer c.CloseNow()
	c.SetReadLimit(4096)

	ctx, stop := context.WithCancel(ctx)
	defer stop()
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
					stop()
					return
				}
			}
		}
	}()
	for {
		_, msg, err := c.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == CodeReplaced {
				return errReplaced
			}
			return err
		}
		var o Open
		if json.Unmarshal(msg, &o) != nil {
			continue
		}
		if o.Ready {
			// The room is this gateway's now: a phone asking finds it.
			l.setStatus(func(s *Status) { s.Connected, s.Error, s.Since = true, "", time.Now() })
			l.logf("relay: connected to %s", l.Relay)
			continue
		}
		if o.Open == "" {
			continue
		}
		go l.accept(ctx, ln, o.Open)
	}
}

// accept takes one phone: its own connection to the relay, the Noise
// handshake, and then the session.
func (l *Link) accept(ctx context.Context, ln *memListener, phone string) {
	u, err := wsURL(l.Relay, AcceptPath, url.Values{"room": {l.Room()}, "phone": {phone}})
	if err != nil {
		return
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	c, _, err := websocket.Dial(dctx, u, &websocket.DialOptions{HTTPHeader: l.header()})
	cancel()
	if err != nil {
		l.logf("relay: couldn't take a phone: %v", err)
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(maxMessage)
	recv, send, err := respond(ctx, c, l.Key)
	if err != nil {
		l.logf("relay: a phone's handshake failed: %v", err)
		c.Close(websocket.StatusPolicyViolation, "handshake failed")
		return
	}
	l.setStatus(func(s *Status) { s.Phones++ })
	defer l.setStatus(func(s *Status) { s.Phones-- })
	hc := sessionClient(ln)
	defer hc.CloseIdleConnections()
	serveSession(ctx, c, recv, send, hc, l.logf)
}

// respond is the responder's half of Noise NK: the phone knows this key
// already, from the QR code; nothing about the phone is known yet — its
// device token, inside the session, says who it is.
func respond(ctx context.Context, c *websocket.Conn, key noise.DHKey) (recv, send *noise.CipherState, err error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: Suite, Pattern: noise.HandshakeNK, Prologue: []byte(Prologue), StaticKeypair: key,
	})
	if err != nil {
		return nil, nil, err
	}
	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	typ, msg, err := c.Read(hctx)
	if err != nil {
		return nil, nil, err
	}
	if typ != websocket.MessageBinary {
		return nil, nil, errors.New("the phone's first message isn't a handshake")
	}
	payload, _, _, err := hs.ReadMessage(nil, msg)
	if err != nil {
		return nil, nil, errors.New("the phone has a different key for this computer: pair it again")
	}
	var hi Hello
	if json.Unmarshal(payload, &hi) != nil || hi.V != HandshakeVersion {
		return nil, nil, fmt.Errorf("the phone speaks tunnel version %d, this computer %d", hi.V, HandshakeVersion)
	}
	reply, _ := json.Marshal(Hello{V: HandshakeVersion, Z: "deflate-raw"})
	out, cs1, cs2, err := hs.WriteMessage(nil, reply)
	if err != nil {
		return nil, nil, err
	}
	if err := c.Write(hctx, websocket.MessageBinary, out); err != nil {
		return nil, nil, err
	}
	// cs1 is the initiator's sending direction: the phone's.
	return cs1, cs2, nil
}
