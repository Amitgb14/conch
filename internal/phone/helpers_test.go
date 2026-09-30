package phone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

// TestMain lets this test binary stand in for an agent: with
// PHONE_HELPER=asker it is the program in a pane that asks questions and
// prints what it is sent. A shell script can't be: the server takes a pane
// whose foreground program is a shell for a shell, whatever it was
// launched as.
func TestMain(m *testing.M) {
	if os.Getenv("PHONE_HELPER") == "asker" {
		asker()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// asker asks one question with the cursor on the first choice, prints
// every byte it is sent as hex, and after an Enter asks a second with the
// cursor on the last.
func asker() {
	stty := exec.Command("stty", "raw", "-echo")
	stty.Stdin = os.Stdin
	if err := stty.Run(); err != nil {
		fmt.Print("stty failed: ", err)
		return
	}
	one := make([]byte, 1)
	echo := func(untilEnter bool) {
		for {
			if n, err := os.Stdin.Read(one); err != nil || n == 0 {
				os.Exit(0)
			}
			fmt.Printf("%02x.", one[0])
			if untilEnter && one[0] == '\r' {
				return
			}
		}
	}
	fmt.Print("Bash command\r\n  go test ./...\r\nDo you want to proceed?\r\n❯ 1. Yes\r\n  2. Yes, and do not ask again\r\n  3. No\r\n")
	fmt.Print("first:")
	echo(true)
	fmt.Print("\r\n\r\nDo you want to make this edit to main.go?\r\n  1. Yes\r\n  2. Yes, allow all edits\r\n❯ 3. No\r\n")
	fmt.Print("second:")
	echo(false)
}

// isolate keeps a test away from the developer's conch: its own home and
// conch directory, and no inherited socket or pane.
func isolate(t *testing.T) (home string) {
	t.Helper()
	home, _ = filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	for _, k := range []string{"CONCH_SOCKET", "CONCH_PANE_ID", "CONCH_RELOAD_STATE", "CONCH_MACHINE",
		"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "ZDOTDIR"} {
		t.Setenv(k, "")
	}
	t.Setenv("SHELL", "/bin/sh")
	gh := filepath.Join(home, "fake-gh")
	os.WriteFile(gh, []byte("#!/bin/sh\necho '[]'\n"), 0o755)
	t.Setenv("CONCH_GH", gh)
	return home
}

// startServer runs a real conch server on a socket in a short temporary
// directory, which is also its CONCH_HOME.
func startServer(t *testing.T) (c *client.Client, dir, sock string) {
	t.Helper()
	isolate(t)
	dir, err := os.MkdirTemp("", "ph")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Setenv("CONCH_HOME", dir)
	sock = filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	t.Cleanup(func() { srv.Stop(); time.Sleep(100 * time.Millisecond); os.RemoveAll(dir) })
	for range 100 {
		if c, err = client.Dial(sock, "test"); err == nil {
			t.Cleanup(func() { c.Close() })
			go func() {
				for range c.Events {
				}
			}()
			return c, dir, sock
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(err)
	return nil, "", ""
}

// fixture is a gateway in front of a real server, served on loopback.
type fixture struct {
	t     *testing.T
	c     *client.Client // the test's own connection to the server
	dir   string
	store *Store
	g     *Gateway
	web   *httptest.Server
	clock atomic.Int64 // added to the real time, in nanoseconds

	logMu sync.Mutex
	logs  []string
}

// noNetwork is a push client that never leaves the test.
var noNetwork = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
	return nil, errors.New("no network in tests")
})}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, pushOptions{client: noNetwork})
}

func newFixtureWith(t *testing.T, po pushOptions) *fixture {
	t.Helper()
	c, dir, sock := startServer(t)
	f := &fixture{t: t, c: c, dir: dir, store: OpenStore(dir)}
	f.g = newGateway(f.store, func() (*client.Client, error) { return client.Dial(sock, "conch-web-test") }, f.logf, po)
	f.g.now = f.now
	f.web = httptest.NewServer(f.g.Handler())
	t.Cleanup(func() { f.g.Close(); f.web.Close() })
	return f
}

func (f *fixture) now() time.Time { return time.Now().Add(time.Duration(f.clock.Load())) }

func (f *fixture) logf(format string, args ...any) {
	f.logMu.Lock()
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
	f.logMu.Unlock()
}

func (f *fixture) logged() string {
	f.logMu.Lock()
	defer f.logMu.Unlock()
	return strings.Join(f.logs, "\n")
}

// forgetAttempts clears the pairing rate limit, so a test may pair as
// many phones as it needs.
func (f *fixture) forgetAttempts() {
	f.g.mu.Lock()
	f.g.attempts = map[string][]time.Time{}
	f.g.mu.Unlock()
}

func (f *fixture) call(method string, params, out any) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.c.Call(ctx, method, params, out); err != nil {
		f.t.Fatalf("%s: %v", method, err)
	}
}

// asker starts the fake agent that asks questions (see asker above).
func (f *fixture) asker() string {
	f.t.Helper()
	exe, err := os.Executable()
	if err != nil {
		f.t.Fatal(err)
	}
	return f.start("claude", []string{exe}, []string{"PHONE_HELPER=asker"})
}

// pane starts a pane running script, as agent when that is named, and
// waits for the server to see the agent in it.
func (f *fixture) pane(agent, script string) string {
	f.t.Helper()
	return f.start(agent, []string{"/bin/sh", "-c", script}, nil)
}

func (f *fixture) start(agent string, command, env []string) string {
	f.t.Helper()
	var info proto.PaneInfo
	f.call(proto.MethodPaneCreate, proto.PaneCreateParams{Agent: agent,
		Command: command, Env: env, Cwd: f.dir, Cols: 100, Rows: 24}, &info)
	if agent != "" {
		waitFor(f.t, agent+" detected in "+info.ID, func() bool {
			var list proto.PaneList
			f.call(proto.MethodPaneList, nil, &list)
			for _, p := range list.Panes {
				if p.ID == info.ID && p.Agent != nil {
					return true
				}
			}
			return false
		})
	}
	return info.ID
}

// screen is a pane's visible screen as text.
func (f *fixture) screen(id string) string {
	f.t.Helper()
	var res proto.PaneReadResult
	f.call(proto.MethodPaneRead, proto.PaneRef{ID: id}, &res)
	return strings.Join(res.Lines, "\n")
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// fakePhone is a paired device: what a browser would hold and send.
type fakePhone struct {
	t     *testing.T
	base  string
	id    string
	token string
	csrf  string
}

// pairRaw posts a code to /pair as a phone would.
func (f *fixture) pairRaw(code, name string) *http.Response {
	f.t.Helper()
	body, _ := json.Marshal(PairRequest{Code: code, DeviceName: name})
	res, err := http.Post(f.web.URL+"/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

// pair issues a code for permission, as `conch web pair` does, and
// exchanges it.
func (f *fixture) pair(permission string) *fakePhone {
	f.t.Helper()
	f.forgetAttempts()
	code, err := f.store.NewCode(permission, f.now())
	if err != nil {
		f.t.Fatal(err)
	}
	res := f.pairRaw(code, permission+" phone")
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		f.t.Fatalf("pair: %d %s", res.StatusCode, b)
	}
	var pr PairResponse
	if err := json.NewDecoder(res.Body).Decode(&pr); err != nil || pr.Permission != permission {
		f.t.Fatalf("pair response %+v: %v", pr, err)
	}
	p := &fakePhone{t: f.t, base: f.web.URL, id: pr.DeviceID}
	for _, ck := range res.Cookies() {
		if ck.Name == CookieName {
			p.token = ck.Value
		}
	}
	if p.token == "" {
		f.t.Fatal("pairing set no cookie")
	}
	var h Hello
	if status := p.get("/api/hello", &h); status != 200 || h.DeviceID != pr.DeviceID {
		f.t.Fatalf("hello: %d %+v", status, h)
	}
	p.csrf = h.CSRFToken
	return p
}

// do makes a request with the phone's cookie and CSRF token, and returns
// the status and the body.
func (p *fakePhone) do(method, path string, body any) (int, []byte) {
	p.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, p.base+path, rd)
	if err != nil {
		p.t.Fatal(err)
	}
	if p.token != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: p.token})
	}
	if p.csrf != "" {
		req.Header.Set(CSRFHeader, p.csrf)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func (p *fakePhone) get(path string, out any) int {
	p.t.Helper()
	status, b := p.do("GET", path, nil)
	if status == 200 && out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			p.t.Fatalf("GET %s: %v in %s", path, err, b)
		}
	}
	return status
}

// post makes a POST and decodes either the result or the error.
func (p *fakePhone) post(path string, body, out any) (int, *APIError) {
	p.t.Helper()
	status, b := p.do("POST", path, body)
	return status, decodeResult(p.t, status, b, out)
}

func decodeResult(t *testing.T, status int, b []byte, out any) *APIError {
	t.Helper()
	if status >= 400 {
		var eb ErrorBody
		if err := json.Unmarshal(b, &eb); err != nil || eb.Error == nil || eb.Error.Code == "" || eb.Error.Message == "" {
			t.Fatalf("status %d without the error shape: %s", status, b)
		}
		return eb.Error
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%v in %s", err, b)
		}
	}
	return nil
}

// agents is the list as the phone gets it.
func (p *fakePhone) agents() []Agent {
	p.t.Helper()
	var list AgentList
	if status := p.get("/api/agents", &list); status != 200 {
		p.t.Fatalf("agents: %d", status)
	}
	return list.Agents
}

// waiting waits for pane's agent to be waiting on a question that
// satisfies ok, and returns it.
func (p *fakePhone) waiting(pane string, ok func(*Question) bool) *Question {
	p.t.Helper()
	var q *Question
	waitFor(p.t, pane+" waiting on its question", func() bool {
		for _, a := range p.agents() {
			if a.Pane == pane && a.State == StateWaiting && a.Question != nil && ok(a.Question) {
				q = a.Question
				return true
			}
		}
		return false
	})
	return q
}

// fakeSocket is the phone's end of the WebSocket.
type fakeSocket struct {
	t    *testing.T
	conn *websocket.Conn
	msgs chan ServerMessage // closed when the socket is
}

// dialSocket opens the socket with token as the cookie ("" for none).
func dialSocket(base, token string) (*websocket.Conn, *http.Response, error) {
	h := http.Header{}
	if token != "" {
		h.Set("Cookie", CookieName+"="+token)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+SocketPath, &websocket.DialOptions{HTTPHeader: h})
}

func (p *fakePhone) socket() *fakeSocket {
	p.t.Helper()
	conn, _, err := dialSocket(p.base, p.token)
	if err != nil {
		p.t.Fatalf("socket: %v", err)
	}
	conn.SetReadLimit(1 << 20)
	s := &fakeSocket{t: p.t, conn: conn, msgs: make(chan ServerMessage, 256)}
	p.t.Cleanup(func() { conn.CloseNow() })
	go func() {
		defer close(s.msgs)
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var m ServerMessage
			if json.Unmarshal(data, &m) == nil {
				s.msgs <- m
			}
		}
	}()
	return s
}

func (s *fakeSocket) send(m ClientMessage) {
	s.t.Helper()
	b, _ := json.Marshal(m)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.conn.Write(ctx, websocket.MessageText, b); err != nil {
		s.t.Fatalf("socket write: %v", err)
	}
}

// next reads messages until one satisfies ok; the rest are dropped. It
// fails when the socket closes first.
func (s *fakeSocket) next(what string, ok func(ServerMessage) bool) ServerMessage {
	s.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m, open := <-s.msgs:
			if !open {
				s.t.Fatalf("socket closed waiting for %s", what)
			}
			if ok(m) {
				return m
			}
		case <-timeout:
			s.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// until reads every message up to the pong answering a ping sent now:
// what the gateway said about everything sent before.
func (s *fakeSocket) until(pingID string) []ServerMessage {
	s.t.Helper()
	s.send(ClientMessage{Type: MsgPing, ID: pingID})
	var seen []ServerMessage
	s.next("pong "+pingID, func(m ServerMessage) bool {
		if m.Type == MsgPong && m.ID == pingID {
			return true
		}
		seen = append(seen, m)
		return false
	})
	return seen
}

// closed waits for the socket to end and returns what arrived before.
func (s *fakeSocket) closed() []ServerMessage {
	s.t.Helper()
	var seen []ServerMessage
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m, open := <-s.msgs:
			if !open {
				return seen
			}
			seen = append(seen, m)
		case <-timeout:
			s.t.Fatalf("the socket stayed open; it had sent %+v", seen)
		}
	}
}
