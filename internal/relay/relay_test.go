package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// marker is in every answer the test gateway gives, so a relay that could
// read the traffic would see it.
const marker = "PLAINTEXT-MARKER-7f3a"

// testGateway is a gateway's handler, as the tunnel meets it: an echo of
// what a call carried, a cookie set, and a socket that echoes.
func testGateway(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		http.SetCookie(w, &http.Cookie{Name: "conch_device", Value: "tok", HttpOnly: true, Secure: true})
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Not-Passed", "1")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"method": r.Method, "host": r.Host, "origin": r.Header.Get("Origin"), "cookie": r.Header.Get("Cookie"),
			"csrf": r.Header.Get("X-CSRF-Token"), "evil": r.Header.Get("X-Evil"), "body": string(body), "marker": marker,
		})
	})
	mux.HandleFunc("/api/big", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("row of a screen "+marker+"\n", 40000))
	})
	mux.HandleFunc("/api/socket", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil) // same origin: the tunnel's own
		if err != nil {
			t.Errorf("socket refused: %v", err)
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(maxFrame)
		for {
			_, m, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if string(m) == "bye" {
				c.Close(websocket.StatusNormalClosure, "")
				return
			}
			if err := c.Write(r.Context(), websocket.MessageText, append([]byte("echo:"), m...)); err != nil {
				return
			}
		}
	})
	return mux
}

type fixture struct {
	srv    *Server
	relay  *httptest.Server
	link   *Link
	cancel context.CancelFunc
	logs   *logs
}

type logs struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logs) logf(f string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b.WriteString(strings.TrimSpace(sprintf(f, a...)) + "\n")
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	srv := &Server{UI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "the app") })}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	lg := &logs{}
	l := &Link{Relay: ts.URL, Secret: secret, Key: key, Handler: testGateway(t), Logf: lg.logf}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = l.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "the gateway to connect", func() bool { return l.Status().Connected })
	return &fixture{srv: srv, relay: ts, link: l, cancel: cancel, logs: lg}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *fixture) dial(t *testing.T) *Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := Dial(ctx, f.relay.URL, f.link.Room(), f.link.Key.Public)
	if err != nil {
		t.Fatalf("dial: %v\n%s", err, f.logs)
	}
	t.Cleanup(s.Close)
	return s
}

func ctx10(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestRoomID(t *testing.T) {
	a, _ := NewSecret()
	b, _ := NewSecret()
	if again := RoomID(a); RoomID(a) != again || RoomID(a) == RoomID(b) {
		t.Fatal("a room is its secret's, and only its")
	}
	if !validRoom(RoomID(a)) || strings.Contains(RoomID(a), a) {
		t.Fatalf("room %q: wrong shape, or carries its secret", RoomID(a))
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 21), strings.Repeat("a", 23), strings.Repeat("/", 22)} {
		if validRoom(bad) {
			t.Errorf("%q taken as a room", bad)
		}
	}
}

func TestKeyFromPrivate(t *testing.T) {
	k, _ := NewKey()
	back, err := KeyFromPrivate(EncodeKey(k.Private))
	if err != nil || !bytes.Equal(back.Public, k.Public) {
		t.Fatalf("a key kept and read back isn't the same key: %v", err)
	}
	for _, bad := range []string{"", "!!", EncodeKey([]byte("short"))} {
		if _, err := KeyFromPrivate(bad); err == nil {
			t.Errorf("%q read as a key", bad)
		}
	}
	if Fingerprint(k.Public) != Fingerprint(back.Public) || len(strings.Fields(Fingerprint(k.Public))) != 4 {
		t.Fatalf("fingerprint %q", Fingerprint(k.Public))
	}
	other, _ := NewKey()
	if Fingerprint(k.Public) == Fingerprint(other.Public) {
		t.Fatal("two keys, one fingerprint")
	}
}

func TestHTTPURL(t *testing.T) {
	for in, want := range map[string]string{
		"wss://relay.example.com":       "https://relay.example.com",
		"wss://relay.example.com/":      "https://relay.example.com",
		"https://relay.example.com/x/":  "https://relay.example.com/x",
		"ws://127.0.0.1:8080":           "http://127.0.0.1:8080",
		"wss://r.example.com/?a=1#frag": "https://r.example.com",
	} {
		if got, err := HTTPURL(in); err != nil || got != want {
			t.Errorf("HTTPURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "relay.example.com", "ftp://x", "wss://"} {
		if _, err := HTTPURL(bad); err == nil {
			t.Errorf("%q taken as a relay", bad)
		}
	}
}

func TestCallsThroughTheRelay(t *testing.T) {
	f := newFixture(t)
	s := f.dial(t)
	res, err := s.Do(ctx10(t), "POST", "/api/echo",
		map[string]string{"Cookie": "conch_device=tok", "X-CSRF-Token": "c", "X-Evil": "1", "Content-Type": "application/json"}, `{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.S != 200 {
		t.Fatalf("status %d: %s", res.S, res.B)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(res.B), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"method": "POST", "host": TunnelHost, "origin": "https://" + TunnelHost,
		"cookie": "conch_device=tok", "csrf": "c", "evil": "", "body": `{"a":1}`}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: the gateway saw %q, want %q", k, got[k], v)
		}
	}
	if !strings.Contains(res.H["Set-Cookie"], "conch_device=tok") {
		t.Errorf("the device cookie didn't come back: %v", res.H)
	}
	if _, ok := res.H["X-Not-Passed"]; ok {
		t.Error("a header the app doesn't need came back")
	}
	if f.link.Status().Phones != 1 {
		t.Errorf("phones = %d, want 1", f.link.Status().Phones)
	}
}

func TestBadPaths(t *testing.T) {
	f := newFixture(t)
	s := f.dial(t)
	for _, p := range []string{"", "api/echo", "//evil.example.com/x"} {
		res, err := s.Do(ctx10(t), "GET", p, nil, "")
		if err != nil || res.S != http.StatusBadRequest {
			t.Errorf("%q: %d %v, want 400", p, res.S, err)
		}
	}
	res, _ := s.Do(ctx10(t), "POST", "/api/echo", nil, strings.Repeat("x", maxRequest+1))
	if res.S != http.StatusRequestEntityTooLarge {
		t.Errorf("an overlong body: %d", res.S)
	}
}

// The relay copies everything, and must understand none of it: no answer,
// no cookie, no message on a socket is there in the clear.
func TestTheRelaySeesNoPlaintext(t *testing.T) {
	var mu sync.Mutex
	var seen bytes.Buffer
	spliceTap = func(b []byte) { mu.Lock(); seen.Write(b); mu.Unlock() }
	t.Cleanup(func() { spliceTap = nil })
	f := newFixture(t)
	s := f.dial(t)
	if res, err := s.Do(ctx10(t), "GET", "/api/echo", map[string]string{"Cookie": "conch_device=" + marker}, ""); err != nil || !strings.Contains(res.B, marker) {
		t.Fatalf("%v %q", err, res.B)
	}
	k, err := s.Socket(ctx10(t), "/api/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = k.Send(ctx10(t), marker)
	if m, err := k.Recv(ctx10(t)); err != nil || m != "echo:"+marker {
		t.Fatalf("%q %v", m, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen.Len() == 0 {
		t.Fatal("the tap saw nothing: the test isn't looking")
	}
	for _, s := range []string{marker, "conch_device", "/api/echo", "echo:"} {
		if bytes.Contains(seen.Bytes(), []byte(s)) {
			t.Errorf("the relay saw %q in the clear", s)
		}
	}
}

func TestSocketThroughTheRelay(t *testing.T) {
	f := newFixture(t)
	s := f.dial(t)
	k, err := s.Socket(ctx10(t), "/api/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	// In order, and a message far over a Noise message's 64 KB.
	big := strings.Repeat("abcdefgh", 200_000)
	for i := 0; i < 100; i++ {
		_ = k.Send(ctx10(t), sprintf("m%d", i))
	}
	_ = k.Send(ctx10(t), big)
	for i := 0; i < 100; i++ {
		m, err := k.Recv(ctx10(t))
		if err != nil || m != sprintf("echo:m%d", i) {
			t.Fatalf("message %d: %q %v", i, m, err)
		}
	}
	if m, err := k.Recv(ctx10(t)); err != nil || m != "echo:"+big {
		t.Fatalf("the big message: %d bytes, %v", len(m), err)
	}
	_ = k.Send(ctx10(t), "bye")
	if _, err := k.Recv(ctx10(t)); err == nil || k.code != int(websocket.StatusNormalClosure) {
		t.Fatalf("the gateway closed the socket; the phone heard %v (%d)", err, k.code)
	}
	if _, err := s.Socket(ctx10(t), "//elsewhere", nil); err == nil {
		t.Error("a socket opened somewhere other than the gateway")
	}
}

func TestBigAnswer(t *testing.T) {
	f := newFixture(t)
	s := f.dial(t)
	res, err := s.Do(ctx10(t), "GET", "/api/big", nil, "")
	if err != nil || res.S != 200 || len(res.B) != 40000*len("row of a screen "+marker+"\n") {
		t.Fatalf("%v %d %d", err, res.S, len(res.B))
	}
}

func TestSeveralPhonesAtOnce(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		s := f.dial(t)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				res, err := s.Do(context.Background(), "GET", "/api/echo", nil, "")
				if err != nil || res.S != 200 {
					t.Errorf("%v %d", err, res.S)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestNoGateway(t *testing.T) {
	ts := httptest.NewServer(&Server{})
	defer ts.Close()
	secret, _ := NewSecret()
	key, _ := NewKey()
	_, err := Dial(ctx10(t), ts.URL, RoomID(secret), key.Public)
	if !errors.Is(err, ErrNoGateway) {
		t.Fatalf("got %v, want ErrNoGateway", err)
	}
	if _, err := Dial(ctx10(t), ts.URL, "not-a-room", key.Public); err == nil {
		t.Fatal("a phone got into a room with no shape")
	}
}

// A phone holding another computer's key — or a relay answering in the
// gateway's place — doesn't get a session.
func TestWrongKey(t *testing.T) {
	f := newFixture(t)
	other, _ := NewKey()
	if _, err := Dial(ctx10(t), f.relay.URL, f.link.Room(), other.Public); err == nil {
		t.Fatal("a session with the wrong key")
	}
	waitFor(t, "the gateway to say why", func() bool { return strings.Contains(f.logs.String(), "pair it again") })
	// And the gateway still serves the right one.
	f.dial(t)
}

func TestWrongSecret(t *testing.T) {
	ts := httptest.NewServer(&Server{})
	defer ts.Close()
	secret, _ := NewSecret()
	other, _ := NewSecret()
	for _, h := range []string{other, ""} {
		req, _ := http.NewRequest("GET", ts.URL+GatewayPath+"?room="+RoomID(secret), nil)
		req.Header.Set(SecretHeader, h)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("secret %q: %d, want 403", h, res.StatusCode)
		}
	}
}

// The same gateway twice: the newer connection takes the room, the older
// says why it lost it.
func TestGatewayReplaced(t *testing.T) {
	f := newFixture(t)
	second := &Link{Relay: f.relay.URL, Secret: f.link.Secret, Key: f.link.Key, Handler: testGateway(t)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = second.Run(ctx) }()
	waitFor(t, "the second to connect", func() bool { return second.Status().Connected })
	waitFor(t, "the first to hear", func() bool {
		return strings.Contains(f.logs.String(), "another conch web took this computer's room")
	})
	cancel()
	waitFor(t, "the first to come back", func() bool { return f.link.Status().Connected })
}

func TestRoomFull(t *testing.T) {
	old := maxPhones
	maxPhones = 2
	t.Cleanup(func() { maxPhones = old })
	f := newFixture(t)
	f.dial(t)
	f.dial(t)
	_, err := Dial(ctx10(t), f.relay.URL, f.link.Room(), f.link.Key.Public)
	if websocket.CloseStatus(err) != CodeBusy {
		t.Fatalf("a third phone: %v", err)
	}
}

func TestRelayServesTheAppAndNoAPI(t *testing.T) {
	f := newFixture(t)
	for path, want := range map[string]int{"/": 200, "/agent/p1": 200, "/api/hello": 404, "/pair": 404, HealthPath: 200} {
		res, err := http.Get(f.relay.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, res.StatusCode, want)
		}
	}
	// A phone's socket from somebody else's page is refused.
	req, _ := http.NewRequest("GET", f.relay.URL+PhonePath+"?room="+f.link.Room(), nil)
	h := http.Header{"Origin": {"https://evil.example.com"}}
	_, res, err := websocket.Dial(ctx10(t), strings.Replace(req.URL.String(), "http", "ws", 1), &websocket.DialOptions{HTTPHeader: h})
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("a cross-origin phone socket: %v", err)
	}
}

// When the relay goes, the gateway says so and keeps trying.
func TestLinkNoticesTheRelayGoing(t *testing.T) {
	f := newFixture(t)
	f.srv.Close()
	f.relay.Close()
	waitFor(t, "the link to notice", func() bool { return !f.link.Status().Connected })
	if f.link.Status().Error == "" {
		t.Error("no reason given")
	}
}

func TestRespondRefusesNonsense(t *testing.T) {
	// The handshake on a connection that sends something else.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		key, _ := NewKey()
		_, _, err = respond(r.Context(), c, key)
		if err == nil {
			t.Error("a handshake from nonsense")
		}
		c.CloseNow()
	}))
	defer ts.Close()
	for _, m := range []struct {
		typ websocket.MessageType
		b   []byte
	}{{websocket.MessageText, []byte("hi")}, {websocket.MessageBinary, []byte("not noise")}} {
		c, _, err := websocket.Dial(ctx10(t), "ws"+strings.TrimPrefix(ts.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Write(ctx10(t), m.typ, m.b)
		_, _, _ = c.Read(ctx10(t))
		c.CloseNow()
	}
}

var sprintf = fmt.Sprintf
