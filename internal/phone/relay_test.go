package phone

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/relay"
)

// relayed puts the fixture's gateway on a relay, as `conch web -relay`
// does, and opens a phone's session through it.
func (f *fixture) relayed() (*relay.Session, *relay.Link) {
	f.t.Helper()
	ts := httptest.NewServer(&relay.Server{UI: AppHandler()})
	f.t.Cleanup(ts.Close)
	secret, key, err := f.store.Relay()
	if err != nil {
		f.t.Fatal(err)
	}
	l := &relay.Link{Relay: ts.URL, Secret: secret, Key: key, Handler: f.g.Handler(), Logf: f.logf}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = l.Run(ctx); close(done) }()
	f.t.Cleanup(func() { cancel(); <-done })
	waitFor(f.t, "the gateway on the relay", func() bool { return l.Status().Connected })
	dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dcancel()
	s, err := relay.Dial(dctx, ts.URL, relay.RoomID(secret), key.Public)
	if err != nil {
		f.t.Fatalf("dial: %v\n%s", err, f.logged())
	}
	f.t.Cleanup(s.Close)
	return s, l
}

func tctx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// A phone pairs through the relay and then works as one on the network
// does: hello, a reply, and the socket's agents and frames.
func TestPhoneThroughTheRelay(t *testing.T) {
	isolate(t)
	f := newFixture(t)
	pane := f.pane("claude", "stty -echo; echo relayed-marker; exec cat")
	s, l := f.relayed()

	code, err := f.store.NewCode(PermFull, f.now())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(PairRequest{Code: code, DeviceName: "relay phone"})
	res, err := s.Do(tctx(t), "POST", "/pair", map[string]string{"Content-Type": "application/json"}, string(b))
	if err != nil || res.S != 200 {
		t.Fatalf("pair: %v %d %s\n%s", err, res.S, res.B, f.logged())
	}
	var token string
	for _, line := range strings.Split(res.H["Set-Cookie"], "\n") {
		if v, ok := strings.CutPrefix(line, CookieName+"="); ok {
			token, _, _ = strings.Cut(v, ";")
		}
	}
	if token == "" {
		t.Fatalf("no device cookie came back: %v", res.H)
	}
	ck := map[string]string{"Cookie": CookieName + "=" + token}

	res, err = s.Do(tctx(t), "GET", "/api/hello", ck, "")
	if err != nil || res.S != 200 {
		t.Fatalf("hello: %v %d %s", err, res.S, res.B)
	}
	var h Hello
	if err := json.Unmarshal([]byte(res.B), &h); err != nil || h.Permission != PermFull {
		t.Fatalf("hello %+v: %v", h, err)
	}
	// A change needs the CSRF token, through the relay as anywhere.
	body := `{"pane":"` + pane + `","text":"hi"}`
	if res, _ := s.Do(tctx(t), "POST", "/api/reply", ck, body); res.S != http.StatusForbidden {
		t.Fatalf("a reply without the CSRF token: %d", res.S)
	}
	withCSRF := map[string]string{"Cookie": ck["Cookie"], CSRFHeader: h.CSRFToken, "Content-Type": "application/json"}
	if res, _ := s.Do(tctx(t), "POST", "/api/reply", withCSRF, body); res.S/100 != 2 {
		t.Fatalf("reply: %d %s", res.S, res.B)
	}
	// Without the cookie, nothing.
	if res, _ := s.Do(tctx(t), "GET", "/api/hello", nil, ""); res.S != http.StatusUnauthorized {
		t.Fatalf("hello without a device: %d", res.S)
	}

	k, err := s.Socket(tctx(t), SocketPath, ck)
	if err != nil {
		t.Fatalf("socket: %v\n%s", err, f.logged())
	}
	send := func(m ClientMessage) {
		b, _ := json.Marshal(m)
		if err := k.Send(tctx(t), string(b)); err != nil {
			t.Fatal(err)
		}
	}
	next := func(what string, ok func(ServerMessage) bool) ServerMessage {
		t.Helper()
		for {
			raw, err := k.Recv(tctx(t))
			if err != nil {
				t.Fatalf("waiting for %s: %v", what, err)
			}
			var m ServerMessage
			if json.Unmarshal([]byte(raw), &m) == nil && ok(m) {
				return m
			}
		}
	}
	send(ClientMessage{Type: MsgAgentsWatch, ID: "w"})
	next("the agents", func(m ServerMessage) bool { return m.Type == MsgAgents && m.Agents != nil && len(*m.Agents) == 1 })
	send(ClientMessage{Type: MsgFrameOpen, ID: "f", Pane: pane})
	next("its screen", func(m ServerMessage) bool {
		return m.Type == MsgFrame && strings.Contains(strings.Join(m.Frame.Lines, "\n"), "relayed-marker")
	})
	if l.Status().Phones != 1 {
		t.Errorf("the link counts %d phones", l.Status().Phones)
	}

	// A device revoked on the computer is out, relay or not.
	if _, err := f.store.Revoke(h.DeviceID); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.Do(tctx(t), "GET", "/api/hello", ck, ""); res.S != http.StatusUnauthorized {
		t.Fatalf("a revoked device: %d", res.S)
	}
}

// The relay serves the phone's app with the gateway's own headers, so a
// phone can open its address and pair; it answers no call itself.
func TestRelayServesTheApp(t *testing.T) {
	ts := httptest.NewServer(&relay.Server{UI: AppHandler()})
	defer ts.Close()
	for path, want := range map[string]int{"/": 200, "/tunnel.mjs": 200, "/app.mjs": 200, "/sw.js": 200, "/api/hello": 404, "/pair": 404} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, res.StatusCode, want)
		}
		if want == 200 && !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Errorf("%s: no content policy", path)
		}
	}
}

func TestStoreRelayKeys(t *testing.T) {
	st := OpenStore(t.TempDir())
	if _, ok := st.RelayState(); ok {
		t.Fatal("a relay state before any")
	}
	s1, k1, err := st.Relay()
	if err != nil {
		t.Fatal(err)
	}
	s2, k2, _ := st.Relay()
	if s1 != s2 || string(k1.Public) != string(k2.Public) {
		t.Fatal("the relay key changed between reads")
	}
	if err := st.ResetRelay(); err != nil {
		t.Fatal(err)
	}
	s3, k3, _ := st.Relay()
	if s3 == s1 || string(k3.Public) == string(k1.Public) {
		t.Fatal("reset kept the old key")
	}
	link := RelayPairLink("https://relay.example.com/", "123-456", relay.RoomID(s3), k3.Public)
	if !strings.HasPrefix(link, "https://relay.example.com/#code=123-456&room=") || !strings.Contains(link, "&key="+relay.EncodeKey(k3.Public)) {
		t.Fatalf("link %q", link)
	}
	if strings.Contains(link, s3) {
		t.Fatal("the link carries the room's secret")
	}
	// A state from a conch web that isn't running is not said.
	if err := st.SetRelayState(&RelayState{URL: "wss://x", PID: 1 << 30, Connected: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.RelayState(); ok {
		t.Fatal("a dead conch web's relay state")
	}
}
