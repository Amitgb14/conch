package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/fleet"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/server"
)

// rig is a host over one real conch server, served by httptest.
type rig struct {
	h    *Host
	srv  *httptest.Server
	dir  string
	sock string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Setenv("HOME", dir)
	t.Setenv("CONCH_HOME", dir)
	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	t.Cleanup(func() { srv.Stop(); time.Sleep(50 * time.Millisecond); os.RemoveAll(dir) })
	var local *client.Client
	for range 100 {
		if local, err = client.Dial(sock, "pool"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })
	pool := fleet.New(fleet.Options{
		Local:     func() (*client.Client, error) { return local, nil },
		DialLocal: func() (*client.Client, error) { return client.Dial(sock, "desktop") },
		Catalog:   func() ([]remote.Machine, error) { return nil, nil },
	})
	t.Cleanup(pool.Close)
	h := New(Options{Pool: pool, StatePath: filepath.Join(dir, "desktop.json")})
	hs := httptest.NewServer(h.Handler(nil))
	t.Cleanup(func() { h.Close(); hs.Close() })
	return &rig{h: h, srv: hs, dir: dir, sock: sock}
}

// pageConn is a test page.
type pageConn struct {
	t    *testing.T
	ws   *websocket.Conn
	n    int
	seen []out // read while looking for something else
}

func (r *rig) open(t *testing.T) *pageConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(r.srv.URL, "http")+"/socket?token="+r.h.Token(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ws.SetReadLimit(maxMessage)
	t.Cleanup(func() { ws.CloseNow() })
	p := &pageConn{t: t, ws: ws}
	if m := p.until(func(m out) bool { return m.T == "hello" }); len(m.Machines) == 0 || m.Machines[0].ID != fleet.Local || m.Machines[0].State != fleet.Online {
		t.Fatalf("hello %+v", m)
	}
	return p
}

func (p *pageConn) write(m in) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, p.ws, m); err != nil {
		p.t.Fatal(err)
	}
}

// until is the first message, already read or still to come, that
// satisfies ok. What it passes over is kept for the next look: the server
// may send an event before the result of the call that caused it.
func (p *pageConn) until(ok func(out) bool) out {
	p.t.Helper()
	for i, m := range p.seen {
		if ok(m) {
			p.seen = append(p.seen[:i], p.seen[i+1:]...)
			return m
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		var m out
		if err := wsjson.Read(ctx, p.ws, &m); err != nil {
			p.t.Fatalf("reading: %v", err)
		}
		if ok(m) {
			return m
		}
		if m.T != "frame" { // frames are superseded; keeping them all is waste
			p.seen = append(p.seen, m)
		}
	}
}

// call makes a call and waits for its result.
func (p *pageConn) call(machine, method string, params any) out {
	p.t.Helper()
	p.n++
	id := "c" + string(rune('0'+p.n%10)) + strings.Repeat("x", p.n/10)
	raw, _ := json.Marshal(params)
	p.write(in{T: "call", ID: id, Machine: machine, Method: method, Params: raw})
	return p.until(func(m out) bool { return m.T == "result" && m.ID == id })
}

func (p *pageConn) frameWith(ref, text string) {
	p.t.Helper()
	p.until(func(m out) bool {
		var f proto.Frame
		return m.T == "frame" && m.Pane == ref && json.Unmarshal(m.Frame, &f) == nil &&
			strings.Contains(strings.Join(f.Lines, "\n"), text)
	})
}

func TestTokenAndOrigin(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	base := "ws" + strings.TrimPrefix(r.srv.URL, "http") + "/socket"
	for _, tok := range []string{"", "nope", r.h.Token()[:63], r.h.Token() + "0"} {
		if _, resp, err := websocket.Dial(ctx, base+"?token="+tok, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q let in: %v", tok, err)
		}
	}
	if _, _, err := websocket.Dial(ctx, base+"?token="+r.h.Token(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {"https://elsewhere.example"}}}); err == nil {
		t.Error("a page from another site was let in")
	}
	ws, _, err := websocket.Dial(ctx, base+"?token="+r.h.Token(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {"wails://wails"}}})
	if err != nil {
		t.Fatalf("the webview: %v", err)
	}
	ws.CloseNow()
	// Two hosts never share a token.
	if New(Options{Pool: fleet.New(fleet.Options{})}).Token() == r.h.Token() {
		t.Fatal("a token was reused")
	}
}

func TestCalls(t *testing.T) {
	r := newRig(t)
	p := r.open(t)

	res := p.call("", proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/cat"}, Cols: 30, Rows: 4, NoProject: true})
	var info proto.PaneInfo
	if res.Error != nil || json.Unmarshal(res.Result, &info) != nil || !proto.IsPaneID(info.ID) {
		t.Fatalf("create: %+v", res)
	}
	res = p.call(fleet.Local, proto.MethodPaneList, nil)
	if res.Error != nil || !strings.Contains(string(res.Result), `"`+info.ID+`"`) {
		t.Fatalf("list: %+v", res)
	}
	// The server's own error comes through as itself.
	if res = p.call("", proto.MethodPaneClose, proto.PaneRef{ID: "p999"}); res.Error == nil || res.Error.Code != proto.ErrNotFound {
		t.Fatalf("close of no pane: %+v", res)
	}
	for _, method := range []string{proto.MethodServerStop, proto.MethodServerReload, proto.MethodPaneSubscribe, proto.MethodPaneUnsubscribe} {
		if res = p.call("", method, proto.PaneRef{ID: info.ID}); res.Error == nil || res.Error.Code != "refused" {
			t.Errorf("%s: %+v", method, res)
		}
	}
	if res = p.call("Box:1", proto.MethodPaneList, nil); res.Error == nil || res.Error.Code != "bad_request" {
		t.Errorf("a bad machine: %+v", res)
	}
	if res = p.call("box", proto.MethodPaneList, nil); res.Error == nil || res.Error.Code != "unavailable" {
		t.Errorf("a machine not in the catalog: %+v", res)
	}
	p.write(in{T: "nonsense", ID: "z"})
	if m := p.until(func(m out) bool { return m.T == "result" && m.ID == "z" }); m.Error == nil {
		t.Error("an unknown message was taken")
	}
	// Events reach the page, named with their machine.
	p.call("", proto.MethodPaneClose, proto.PaneRef{ID: info.ID})
	p.until(func(m out) bool {
		return m.T == "event" && m.Machine == fleet.Local && m.Event == proto.EventPaneClosed
	})
}

func TestTwoPagesShareAPane(t *testing.T) {
	r := newRig(t)
	a, b := r.open(t), r.open(t)
	var info proto.PaneInfo
	json.Unmarshal(a.call("", proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/cat"}, Cols: 30, Rows: 4, NoProject: true}).Result, &info)
	ref := fleet.ComposePaneID(fleet.Local, info.ID)

	a.write(in{T: "subscribe", Pane: info.ID}) // bare: this computer's
	a.until(func(m out) bool { return m.T == "frame" && m.Pane == ref })
	// The second page gets the frame there already is, without waiting
	// for the screen to change.
	b.write(in{T: "subscribe", Pane: ref})
	b.until(func(m out) bool { return m.T == "frame" && m.Pane == ref })

	a.call("", proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "both-see"})
	a.frameWith(ref, "both-see")
	b.frameWith(ref, "both-see")

	// One leaving leaves the other subscribed.
	a.write(in{T: "unsubscribe", Pane: ref})
	a.write(in{T: "unsubscribe", Pane: ref}) // twice: nothing more to drop
	a.call("", proto.MethodPaneList, nil)    // the unsubscribes are read by now
	b.call("", proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "-still"})
	b.frameWith(ref, "-still")
	if n := r.wanted(ref); n != 1 {
		t.Fatalf("wanted %d after one page left", n)
	}
	// A page that goes takes its subscriptions with it.
	b.ws.CloseNow()
	deadline := time.Now().Add(5 * time.Second)
	for r.wanted(ref) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("a closed page's subscription was kept")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.h.mu.Lock()
	_, cached := r.h.last[ref]
	r.h.mu.Unlock()
	if cached {
		t.Fatal("a frame nobody wants was kept")
	}
}

func (r *rig) wanted(ref string) int {
	r.h.mu.Lock()
	defer r.h.mu.Unlock()
	return r.h.wanted[ref]
}

// A machine's connection dropping is not the end of a page's panes: the
// next use dials again and subscribes to what was wanted.
func TestReconnectSubscribesAgain(t *testing.T) {
	r := newRig(t)
	p := r.open(t)
	var info proto.PaneInfo
	json.Unmarshal(p.call("", proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/cat"}, Cols: 30, Rows: 4, NoProject: true}).Result, &info)
	ref := fleet.ComposePaneID(fleet.Local, info.ID)
	p.write(in{T: "subscribe", Pane: ref})
	p.until(func(m out) bool { return m.T == "frame" && m.Pane == ref })

	r.h.mu.Lock()
	old := r.h.machines[fleet.Local]
	r.h.mu.Unlock()
	old.c.Close()
	p.until(func(m out) bool { return m.T == "machines" }) // the page is told
	res := p.call("", proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "after-drop"})
	if res.Error != nil {
		t.Fatalf("a call after the drop: %+v", res.Error)
	}
	p.frameWith(ref, "after-drop")
}

func TestState(t *testing.T) {
	r := newRig(t)
	p := r.open(t)
	p.write(in{T: "state", ID: "s1", State: json.RawMessage(`{"tabs":[1,2]}`)})
	if m := p.until(func(m out) bool { return m.T == "result" && m.ID == "s1" }); m.Error != nil {
		t.Fatal(m.Error)
	}
	for id, bad := range map[string]string{"s2": `[1]`, "s3": `"x"`, "s4": `{"a":"` + strings.Repeat("x", maxState) + `"}`} {
		p.write(in{T: "state", ID: id, State: json.RawMessage(bad)})
		if m := p.until(func(m out) bool { return m.T == "result" && m.ID == id }); m.Error == nil {
			t.Errorf("%s: saved %.20s", id, bad)
		}
	}
	// A new page is given what was saved.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(r.srv.URL, "http")+"/socket?token="+r.h.Token(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	var hello out
	if err := wsjson.Read(ctx, ws, &hello); err != nil || string(hello.State) != `{"tabs":[1,2]}` {
		t.Fatalf("hello state %s %v", hello.State, err)
	}
}

func TestStateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desktop.json")
	s := &stateFile{path: path}
	logs := []string{}
	logf := func(f string, a ...any) { logs = append(logs, f) }
	if got := string(s.load(logf)); got != "{}" || len(logs) != 0 {
		t.Fatalf("missing: %s %v", got, logs)
	}
	os.WriteFile(path, []byte(`{"tabs":`), 0o600) // truncated
	if got := string(s.load(logf)); got != "{}" {
		t.Fatalf("truncated: %s", got)
	}
	if b, err := os.ReadFile(path + ".bad"); err != nil || string(b) != `{"tabs":` {
		t.Fatalf("the bad file was not set aside: %q %v", b, err)
	}
	if e := s.save(json.RawMessage(` {"v":1} `)); e != nil {
		t.Fatal(e)
	}
	if got := string(s.load(logf)); got != ` {"v":1} ` {
		t.Fatalf("saved %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 { // desktop.json and .bad, no temp files
		t.Fatalf("left behind %v", entries)
	}
	// A directory where the file's folder should be: refused, not crashed.
	blocked := &stateFile{path: filepath.Join(path, "sub", "desktop.json")}
	if e := blocked.save(json.RawMessage(`{}`)); e == nil {
		t.Fatal("saved under a file")
	}
	// No path keeps nothing and says {}.
	none := &stateFile{}
	if none.save(json.RawMessage(`{}`)) != nil || string(none.load(logf)) != "{}" {
		t.Fatal("no path")
	}
}

// A page that is slow is sent the newest frame of each pane, not every
// frame; queued messages go first.
func TestFramesCoalesce(t *testing.T) {
	p := newPage(nil, nil)
	p.subs["local:p1"], p.subs["local:p2"] = 1, 1
	p.frame("local:p1", json.RawMessage(`1`))
	p.frame("local:p2", json.RawMessage(`a`))
	p.frame("local:p1", json.RawMessage(`2`))
	p.frame("local:p9", json.RawMessage(`x`)) // not subscribed
	p.queue = append(p.queue, out{T: "event"})
	var got []string
	for {
		m, ok := p.next()
		if !ok {
			break
		}
		got = append(got, m.T+":"+m.Pane+":"+string(m.Frame))
	}
	if strings.Join(got, " ") != "event:: frame:local:p1:2 frame:local:p2:a" {
		t.Fatalf("sent %v", got)
	}
}

// A page that stops reading is closed rather than queued for without end.
func TestOverflowEndsThePage(t *testing.T) {
	r := newRig(t)
	pc := r.open(t)
	var p *page
	for _, x := range r.h.allPages() {
		p = x
	}
	p.mu.Lock()
	p.queue = make([]out, queueMax)
	p.mu.Unlock()
	p.send(out{T: "event"})
	p.mu.Lock()
	ended := p.ended
	p.mu.Unlock()
	if !ended {
		t.Fatal("an overflowing page was kept")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := pc.ws.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("closed with %v", err)
			}
			break
		}
	}
}

func TestCanonical(t *testing.T) {
	for ref, want := range map[string]string{"p3": "local:p3", "local:p3": "local:p3", "box:p3": "box:p3", "bad ref": "bad ref"} {
		if got := canonical(ref); got != want {
			t.Errorf("%q: %q", ref, got)
		}
	}
}

// SharedClient keeps one connection while it is good and dials again
// once it is not.
func TestSharedClient(t *testing.T) {
	r := newRig(t)
	dials := 0
	get := SharedClient(func() (*client.Client, error) {
		dials++
		return client.Dial(r.sock, "shared")
	})
	a, err := get()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := get(); b != a || dials != 1 {
		t.Fatalf("a good connection was not kept (%d dials)", dials)
	}
	a.Close()
	b, err := get()
	if err != nil || b == a || dials != 2 {
		t.Fatalf("a closed connection was not replaced: %v (%d dials)", err, dials)
	}
	b.Close()
	failing := SharedClient(func() (*client.Client, error) { return nil, os.ErrNotExist })
	if c, err := failing(); c != nil || err == nil {
		t.Fatal("a failed dial gave a client")
	}
}

// A machine added behind the window's back — from a pane running conch
// machine add — reaches the page without anyone announcing it; the page
// is not told the same list over and over.
func TestMachinesWatched(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	var mu sync.Mutex
	catalog := []remote.Machine{}
	pool := fleet.New(fleet.Options{
		Local: func() (*client.Client, error) { return nil, errors.New("no local server here") },
		Catalog: func() ([]remote.Machine, error) {
			mu.Lock()
			defer mu.Unlock()
			return append([]remote.Machine(nil), catalog...), nil
		},
		Connect: func(context.Context, remote.Machine) (*client.Client, error) { return nil, errors.New("unreachable") },
	})
	defer pool.Close()
	h := New(Options{Pool: pool, Conch: "/Applications/conch.app/Contents/Resources/conch", MachinesEvery: 30 * time.Millisecond})
	srv := httptest.NewServer(h.Handler(nil))
	defer func() { h.Close(); srv.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/socket?token="+h.Token(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	var hello out
	if err := wsjson.Read(ctx, ws, &hello); err != nil || hello.Conch != "/Applications/conch.app/Contents/Resources/conch" {
		t.Fatalf("hello %+v %v", hello, err)
	}

	mu.Lock()
	catalog = append(catalog, remote.Machine{ID: "box", Label: "box", Target: "me@box", Enabled: true})
	mu.Unlock()
	machines := 0
	for {
		var m out
		if err := wsjson.Read(ctx, ws, &m); err != nil {
			t.Fatalf("never told about box: %v", err)
		}
		if m.T != "machines" {
			continue
		}
		machines++
		found := false
		for _, x := range m.Machines {
			found = found || x.ID == "box"
		}
		if found {
			break
		}
	}
	// Nothing changes now: no more lists, though the host keeps looking.
	// (box goes from connecting to offline once, which is a change.)
	quiet, qcancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer qcancel()
	extra := 0
	for {
		var m out
		if wsjson.Read(quiet, ws, &m) != nil {
			break
		}
		if m.T == "machines" {
			extra++
		}
	}
	if extra > 2 {
		t.Fatalf("told the same list %d times", extra)
	}
}
