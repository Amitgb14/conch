package phone

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// pushed is one request a fake push service received.
type pushed struct {
	path   string
	header http.Header
	msg    PushMessage
	raw    map[string]any
}

// pushService is a push service on loopback, over TLS as the real ones
// are. It decrypts what it gets as a browser would, with the key a
// subscription hands out.
type pushService struct {
	t    *testing.T
	srv  *httptest.Server
	ua   *ecdh.PrivateKey
	auth []byte

	mu   sync.Mutex
	got  []pushed
	gone map[string]bool // paths answered 410
}

func newPushService(t *testing.T) *pushService {
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	ps := &pushService{t: t, ua: ua, auth: make([]byte, 16), gone: map[string]bool{"/gone": true}}
	rand.Read(ps.auth)
	ps.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		plain, err := ps.decrypt(body)
		if err != nil {
			t.Errorf("push to %s: %v", r.URL.Path, err)
			w.WriteHeader(400)
			return
		}
		p := pushed{path: r.URL.Path, header: r.Header}
		json.Unmarshal(plain, &p.msg)
		json.Unmarshal(plain, &p.raw)
		ps.mu.Lock()
		ps.got = append(ps.got, p)
		gone := ps.gone[r.URL.Path]
		ps.mu.Unlock()
		if gone {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(ps.srv.Close)
	return ps
}

// decrypt is RFC 8291 from the browser's side.
func (ps *pushService) decrypt(body []byte) ([]byte, error) {
	if len(body) < 21 || binary.BigEndian.Uint32(body[16:20]) != pushRecordSize {
		return nil, io.ErrUnexpectedEOF
	}
	salt, idlen := body[:16], int(body[20])
	asPub, err := ecdh.P256().NewPublicKey(body[21 : 21+idlen])
	if err != nil {
		return nil, err
	}
	shared, err := ps.ua.ECDH(asPub)
	if err != nil {
		return nil, err
	}
	ikm, _ := hkdf.Key(sha256.New, shared, ps.auth, "WebPush: info\x00"+string(ps.ua.PublicKey().Bytes())+string(asPub.Bytes()), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		return nil, err
	}
	if len(plain) == 0 || plain[len(plain)-1] != 2 {
		return nil, io.ErrUnexpectedEOF
	}
	return plain[:len(plain)-1], nil
}

func (ps *pushService) keys() PushKeys {
	return PushKeys{P256dh: base64.RawURLEncoding.EncodeToString(ps.ua.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(ps.auth)}
}

func (ps *pushService) all() []pushed {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return append([]pushed{}, ps.got...)
}

// waitFor waits up to 5 s — the plan's bound — for pushes to every path
// in paths about pane, and returns them by path.
func (ps *pushService) waitFor(typ, pane string, paths ...string) map[string]pushed {
	ps.t.Helper()
	found := map[string]pushed{}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range ps.all() {
			if p.msg.Type == typ && p.msg.Pane == pane {
				found[p.path] = p
			}
		}
		if len(found) >= len(paths) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, path := range paths {
		if _, ok := found[path]; !ok {
			ps.t.Fatalf("no %s push for %s to %s within 5s; got %+v", typ, pane, path, ps.all())
		}
	}
	return found
}

// A push goes out on the move into waiting, and into done for those who
// asked; not for an agent that was already waiting when the gateway
// started, not twice for one wait, and not to a device since revoked.
func TestPushOnTransitions(t *testing.T) {
	ps := newPushService(t)
	c, dir, sock := startServer(t)
	f := &fixture{t: t, c: c, dir: dir, store: OpenStore(dir)}

	// An agent already waiting before the gateway starts.
	before := f.pane("claude", "stty -echo; exec cat")
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: before, Agent: "claude", Event: "PermissionRequest"}, nil)
	asker := f.pane("claude", "stty -echo; exec cat")
	worker := f.pane("claude", "stty -echo; exec cat")

	ready := make(chan struct{}, 4)
	f.g = newGateway(f.store, func() (*client.Client, error) { return client.Dial(sock, "conch-web-test") }, f.logf, pushOptions{
		allowed:  func(e string) bool { return strings.HasPrefix(e, ps.srv.URL+"/") },
		client:   ps.srv.Client(),
		watching: func() { ready <- struct{}{} },
	})
	f.web = httptest.NewServer(f.g.Handler())
	t.Cleanup(func() { f.g.Close(); f.web.Close() })
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the push watcher never started")
	}

	subscribe := func(p *fakePhone, path string, on ...string) {
		t.Helper()
		status, b := p.do("POST", "/api/push/subscribe", PushSubscription{Endpoint: ps.srv.URL + path, Keys: ps.keys(), On: on})
		if status != 204 {
			t.Fatalf("subscribe %s: %d %s", path, status, b)
		}
	}
	both, waitingOnly, gone := f.pair(PermView), f.pair(PermView), f.pair(PermFull)
	subscribe(both, "/both", "waiting", "done")
	subscribe(waitingOnly, "/waiting")
	subscribe(gone, "/gone")

	// Into waiting: everyone.
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: asker, Agent: "claude", Event: "Notification",
		NotificationType: "permission_prompt", Message: "Claude needs your permission to run rm -rf build"}, nil)
	got := ps.waitFor("waiting", asker, "/both", "/waiting", "/gone")
	p := got["/both"]
	if p.msg != (PushMessage{Type: "waiting", Pane: asker, Name: "sh", URL: "/agent/" + asker}) {
		t.Fatalf("message %+v", p.msg)
	}
	// Nothing from the question travels through the push service.
	if len(p.raw) != 4 {
		t.Fatalf("payload fields %v", p.raw)
	}
	for k, want := range map[string]string{"Content-Encoding": "aes128gcm", "TTL": "86400", "Urgency": "high", "Topic": "conch-" + asker} {
		if p.header.Get(k) != want {
			t.Errorf("%s: %q, want %q", k, p.header.Get(k), want)
		}
	}
	checkVAPID(t, f.store, ps.srv.URL, p.header.Get("Authorization"))

	// The push service said /gone is gone: its subscription is dropped.
	waitFor(t, "the gone subscription dropped", func() bool {
		devs, _ := f.store.Devices()
		for _, d := range devs {
			if d.ID == gone.id {
				return len(d.Push) == 0
			}
		}
		return false
	})

	// Into done: only who asked for it. The agent works and stops with
	// nobody looking at it.
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: worker, Agent: "claude", Event: "UserPromptSubmit"}, nil)
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: worker, Agent: "claude", Event: "Stop"}, nil)
	done := ps.waitFor("done", worker, "/both")
	if done["/both"].header.Get("Urgency") != "normal" {
		t.Errorf("done urgency %q", done["/both"].header.Get("Urgency"))
	}

	// Still waiting, asked again: no second push. Then the one device that
	// wants done is revoked, and the next done reaches nobody.
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: asker, Agent: "claude", Event: "PermissionRequest"}, nil)
	if ok, err := f.store.Revoke(both.id); !ok || err != nil {
		t.Fatal(ok, err)
	}
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: worker, Agent: "claude", Event: "UserPromptSubmit"}, nil)
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: worker, Agent: "claude", Event: "Stop"}, nil)
	// A last waiting push, to /waiting, marks the end: everything before
	// it has been sent by then (one sender, in order).
	last := f.pane("claude", "stty -echo; exec cat")
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: last, Agent: "claude", Event: "PermissionRequest"}, nil)
	ps.waitFor("waiting", last, "/waiting")

	count := map[string]int{}
	for _, p := range ps.all() {
		count[p.msg.Type+" "+p.msg.Pane+" "+p.path]++
		if p.msg.Pane == before {
			t.Errorf("a push for %s, which was waiting before the gateway started: %+v", before, p.msg)
		}
	}
	want := map[string]int{
		"waiting " + asker + " /both": 1, "waiting " + asker + " /waiting": 1, "waiting " + asker + " /gone": 1,
		"done " + worker + " /both":     1,
		"waiting " + last + " /waiting": 1,
	}
	if len(count) != len(want) {
		t.Fatalf("pushes %v, want %v", count, want)
	}
	for k, n := range want {
		if count[k] != n {
			t.Errorf("%s: %d pushes, want %d (all: %v)", k, count[k], n, count)
		}
	}
	// The log names devices and kinds, never an endpoint.
	if log := f.logged(); strings.Contains(log, ps.srv.URL) || !strings.Contains(log, "push waiting "+asker+": sent to 2 of 3") {
		t.Fatalf("log:\n%s", log)
	}
}

func checkVAPID(t *testing.T, store *Store, origin, auth string) {
	t.Helper()
	key, err := store.VAPIDKey()
	if err != nil {
		t.Fatal(err)
	}
	rest, _ := strings.CutPrefix(auth, "vapid t=")
	jwt, k, _ := strings.Cut(rest, ", k=")
	if k != base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()) {
		t.Fatalf("k=%q is not the gateway's key", k)
	}
	parts := strings.Split(jwt, ".")
	var claims map[string]any
	json.Unmarshal(mustB64(t, parts[1]), &claims)
	if claims["aud"] != origin {
		t.Fatalf("aud %v, want %s", claims["aud"], origin)
	}
	pub, _ := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), key.PublicKey().Bytes())
	sig := mustB64(t, parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("the push is not signed with the gateway's key")
	}
}

// Where the push service can't be reached, or refuses, the gateway says
// so in its log and carries on; it never follows a redirect elsewhere.
func TestPushFailuresAndRedirects(t *testing.T) {
	var hits sync.Map
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Store("elsewhere", true)
	}))
	defer elsewhere.Close()
	svc := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, elsewhere.URL+"/x", http.StatusTemporaryRedirect)
		case "/busy":
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	defer svc.Close()
	key, _ := ecdh.P256().GenerateKey(rand.Reader)
	ps := newPushService(t)
	msg := PushMessage{Type: "waiting", Pane: "p1", Name: "x", URL: "/agent/p1"}
	for path, want := range map[string]string{"/redirect": "answered 307", "/busy": "answered 429"} {
		hc := svc.Client()
		hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		err := sendPush(t.Context(), hc, key, PushSubscription{Endpoint: svc.URL + path, Keys: ps.keys()}, msg, time.Now())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", path, err)
		}
	}
	if _, ok := hits.Load("elsewhere"); ok {
		t.Fatal("a redirect was followed")
	}
	// The gateway's own client is the one that refuses redirects.
	g := New(OpenStore(t.TempDir()), nil, nil)
	defer g.Close()
	if g.pushClient.CheckRedirect == nil || g.pushClient.Timeout == 0 {
		t.Fatal("the default push client follows redirects or never gives up")
	}
	if g.pushAllowed(svc.URL+"/x") || !g.pushAllowed("https://fcm.googleapis.com/fcm/send/x") {
		t.Fatal("the default gateway's push services")
	}
	if err := sendPush(t.Context(), svc.Client(), key, PushSubscription{Endpoint: svc.URL, Keys: PushKeys{}}, msg, time.Now()); err == nil {
		t.Fatal("sent with no keys")
	}
}

// A full queue loses the newest message, not the watcher.
func TestPushQueueFull(t *testing.T) {
	var logged []string
	g := &Gateway{pushQueue: make(chan pushJob, 1), logf: func(f string, a ...any) { logged = append(logged, f) }}
	g.queuePush(PushMessage{Type: "waiting", Pane: "p1"})
	g.queuePush(PushMessage{Type: "waiting", Pane: "p2"})
	if len(g.pushQueue) != 1 || len(logged) != 1 || !strings.Contains(logged[0], "dropped") {
		t.Fatalf("queue %d, log %v", len(g.pushQueue), logged)
	}
}
