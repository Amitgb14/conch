package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestMain doubles as a fake `conch server` for EnsureServer: EnsureServer
// re-executes os.Executable() (this test binary) with the argument "server".
func TestMain(m *testing.M) {
	if os.Getenv("A3_FAKE_SERVER") == "1" && len(os.Args) > 1 && os.Args[1] == "server" {
		os.Exit(a3FakeServerMain())
	}
	os.Exit(m.Run())
}

func a3FakeServerMain() int {
	if os.Getenv("A3_FAKE_SERVER_SILENT") == "1" {
		// Binds and accepts, but serves nothing: a server that came up and
		// then stopped answering.
		ln, err := net.Listen("unix", os.Getenv("CONCH_SOCKET"))
		if err != nil {
			return 4
		}
		var held []net.Conn
		go func() {
			for {
				nc, err := ln.Accept()
				if err != nil {
					return
				}
				held = append(held, nc)
			}
		}()
		time.Sleep(3 * time.Second)
		ln.Close()
		return 0
	}
	if os.Getenv("A3_FAKE_SERVER_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "fake server: failing on purpose")
		return 3
	}
	sock := os.Getenv("CONCH_SOCKET")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake server:", err)
		return 4
	}
	fmt.Println("fake server listening on", sock)
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer nc.Close()
				conn := proto.NewConn(nc)
				for {
					msg, err := conn.Read()
					if err != nil {
						return
					}
					if msg.Method == proto.MethodHello {
						a3Reply(conn, msg, proto.HelloResult{Version: proto.Version}, nil)
					}
				}
			}()
		}
	}()
	time.Sleep(3 * time.Second)
	ln.Close()
	os.Remove(sock)
	return 0
}

// a3ShortDir returns a short temp directory: macOS limits unix socket
// paths to ~104 bytes, and t.TempDir() is often too long.
func a3ShortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "a3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func a3Isolate(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
}

// a3Handler answers one non-hello message on the server side. It may write
// any number of messages to conn.
type a3Handler func(conn *proto.Conn, msg proto.Message)

// a3Server is a faithful fake of the conch server's wire behaviour: NDJSON
// proto.Messages, hello answered with a HelloResult, responses carrying the
// request ID and either Result ({} when empty) or Error.
type a3Server struct {
	t      *testing.T
	sock   string
	ln     net.Listener
	caps   []string
	hello  func(proto.HelloParams) (any, *proto.Error)
	handle a3Handler

	mu      sync.Mutex
	helloed []proto.HelloParams
	conns   []*proto.Conn
}

func a3NewServer(t *testing.T, handle a3Handler) *a3Server {
	t.Helper()
	a3Isolate(t)
	s := &a3Server{t: t, sock: filepath.Join(a3ShortDir(t), "s.sock"), caps: []string{"pane.v1", "events.v1"}, handle: handle}
	ln, err := net.Listen("unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	s.ln = ln
	t.Cleanup(func() {
		ln.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, c := range s.conns {
			c.Close()
		}
	})
	go s.serve()
	return s
}

func (s *a3Server) serve() {
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		conn := proto.NewConn(nc)
		s.mu.Lock()
		s.conns = append(s.conns, conn)
		s.mu.Unlock()
		go s.serveConn(conn)
	}
}

func (s *a3Server) serveConn(conn *proto.Conn) {
	defer conn.Close()
	for {
		msg, err := conn.Read()
		if err != nil {
			return
		}
		if msg.Method == proto.MethodHello {
			var hp proto.HelloParams
			_ = json.Unmarshal(msg.Params, &hp)
			s.mu.Lock()
			s.helloed = append(s.helloed, hp)
			s.mu.Unlock()
			var result any = proto.HelloResult{Version: "9.9.9", Protocol: proto.ProtocolVersion, Capabilities: s.caps, PID: 42, Platform: "plan9/mips"}
			var perr *proto.Error
			if s.hello != nil {
				result, perr = s.hello(hp)
			}
			if result == a3Hangup {
				return
			}
			a3Reply(conn, msg, result, perr)
			continue
		}
		if s.handle != nil {
			s.handle(conn, msg)
		}
	}
}

// a3Hangup as a hello result makes the fake server drop the connection.
var a3Hangup = &struct{ hangup bool }{true}

func a3Reply(conn *proto.Conn, msg proto.Message, result any, perr *proto.Error) {
	if msg.ID == "" {
		return
	}
	resp := proto.Message{ID: msg.ID}
	if perr != nil {
		resp.Error = perr
	} else {
		resp.Result = proto.Marshal(result)
		if resp.Result == nil {
			resp.Result = json.RawMessage("{}")
		}
	}
	_ = conn.Write(resp)
}

func (s *a3Server) dial(t *testing.T) *Client {
	t.Helper()
	c, err := Dial(s.sock, "a3-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestA3DialHandshake(t *testing.T) {
	s := a3NewServer(t, nil)
	c := s.dial(t)

	if c.Server.Version != "9.9.9" || c.Server.PID != 42 || c.Server.Platform != "plan9/mips" || c.Server.Protocol != proto.ProtocolVersion {
		t.Fatalf("server info not decoded: %+v", c.Server)
	}
	if !reflect.DeepEqual(c.Server.Capabilities, []string{"pane.v1", "events.v1"}) {
		t.Fatalf("capabilities = %v", c.Server.Capabilities)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.helloed) != 1 {
		t.Fatalf("hello count = %d", len(s.helloed))
	}
	hp := s.helloed[0]
	if hp.Client != "a3-test" || hp.Version != proto.Version || hp.Protocol != proto.ProtocolVersion || !reflect.DeepEqual(hp.Capabilities, proto.Capabilities) {
		t.Fatalf("hello params = %+v", hp)
	}
	if c.Err() != nil {
		t.Fatalf("Err on open connection = %v", c.Err())
	}
}

func TestA3DialNoServer(t *testing.T) {
	a3Isolate(t)
	if _, err := Dial(filepath.Join(a3ShortDir(t), "missing.sock"), "x"); err == nil {
		t.Fatal("dial to missing socket succeeded")
	}
}

func TestA3HandshakeRejected(t *testing.T) {
	s := a3NewServer(t, nil)
	s.hello = func(hp proto.HelloParams) (any, *proto.Error) {
		return nil, proto.Errorf(proto.ErrBadRequest, "protocol mismatch: client %d, server %d", hp.Protocol, 99)
	}
	_, err := Dial(s.sock, "x")
	if err == nil {
		t.Fatal("handshake error not reported")
	}
	if !strings.HasPrefix(err.Error(), "handshake: ") {
		t.Fatalf("error not wrapped: %v", err)
	}
	var perr *proto.Error
	if !errors.As(err, &perr) || perr.Code != proto.ErrBadRequest || !strings.Contains(perr.Message, "protocol mismatch") {
		t.Fatalf("want *proto.Error bad_request, got %#v", err)
	}
}

func TestA3HandshakeHangup(t *testing.T) {
	s := a3NewServer(t, nil)
	s.hello = func(proto.HelloParams) (any, *proto.Error) { return a3Hangup, nil }
	_, err := Dial(s.sock, "x")
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("want ErrClosed, got %v", err)
	}
}

func TestA3HandshakeBadResult(t *testing.T) {
	s := a3NewServer(t, nil)
	s.hello = func(proto.HelloParams) (any, *proto.Error) { return []int{1, 2}, nil }
	if _, err := Dial(s.sock, "x"); err == nil || !strings.HasPrefix(err.Error(), "handshake: ") {
		t.Fatalf("undecodable hello result: %v", err)
	}
}

func TestA3MissingCapabilities(t *testing.T) {
	c := &Client{Server: proto.HelloResult{Capabilities: []string{"a", "b"}}}
	if got := c.MissingCapabilities([]string{"a", "c", "b", "d"}); !reflect.DeepEqual(got, []string{"c", "d"}) {
		t.Fatalf("missing = %v", got)
	}
	if got := c.MissingCapabilities(nil); got != nil {
		t.Fatalf("nothing wanted: %v", got)
	}
	if got := c.MissingCapabilities([]string{"a"}); got != nil {
		t.Fatalf("all present: %v", got)
	}
	old := &Client{}
	if got := old.MissingCapabilities([]string{"x", "y"}); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Fatalf("server without capabilities: %v", got)
	}
}

type a3Echo struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

func a3EchoHandler(conn *proto.Conn, msg proto.Message) {
	switch msg.Method {
	case "echo":
		var v a3Echo
		_ = json.Unmarshal(msg.Params, &v)
		a3Reply(conn, msg, v, nil)
	case "empty":
		a3Reply(conn, msg, nil, nil)
	case "raw-empty":
		_ = conn.Write(proto.Message{ID: msg.ID})
	case "fail":
		a3Reply(conn, msg, nil, proto.Errorf(proto.ErrNotFound, "no pane %q", "p9"))
	case "wrong-type":
		a3Reply(conn, msg, "a string", nil)
	case "hang":
		// never answer
	}
}

func TestA3CallSuccess(t *testing.T) {
	c := a3NewServer(t, a3EchoHandler).dial(t)
	ctx := context.Background()

	var out a3Echo
	if err := c.Call(ctx, "echo", a3Echo{N: 7, Text: "hi"}, &out); err != nil {
		t.Fatal(err)
	}
	if out != (a3Echo{N: 7, Text: "hi"}) {
		t.Fatalf("out = %+v", out)
	}
	if err := c.Call(ctx, "echo", a3Echo{N: 1}, nil); err != nil {
		t.Fatalf("nil out: %v", err)
	}
	var empty a3Echo
	if err := c.Call(ctx, "empty", nil, &empty); err != nil || empty != (a3Echo{}) {
		t.Fatalf("empty result: %+v %v", empty, err)
	}
	keep := a3Echo{N: 5}
	if err := c.Call(ctx, "raw-empty", nil, &keep); err != nil || keep.N != 5 {
		t.Fatalf("response without result must leave out untouched: %+v %v", keep, err)
	}
}

func TestA3CallError(t *testing.T) {
	c := a3NewServer(t, a3EchoHandler).dial(t)
	err := c.Call(context.Background(), "fail", nil, nil)
	var perr *proto.Error
	if !errors.As(err, &perr) || perr.Code != proto.ErrNotFound || err.Error() != `not_found: no pane "p9"` {
		t.Fatalf("got %#v", err)
	}
	// The connection stays usable after an error response.
	if err := c.Call(context.Background(), "echo", a3Echo{}, nil); err != nil {
		t.Fatal(err)
	}

	var out a3Echo
	if err := c.Call(context.Background(), "wrong-type", nil, &out); err == nil {
		t.Fatal("decoding a string into a struct succeeded")
	} else if errors.As(err, &perr) {
		t.Fatalf("decode error reported as protocol error: %v", err)
	}
}

func TestA3CallTimeoutAndLateReply(t *testing.T) {
	var mu sync.Mutex
	var held []proto.Message
	var heldConn *proto.Conn
	c := a3NewServer(t, func(conn *proto.Conn, msg proto.Message) {
		if msg.Method == "slow" {
			mu.Lock()
			held, heldConn = append(held, msg), conn
			mu.Unlock()
			return
		}
		a3EchoHandler(conn, msg)
	}).dial(t)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := c.Call(ctx, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout not honoured promptly")
	}
	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("timed-out call left %d pending entries", n)
	}

	// A reply arriving after the caller gave up is dropped harmlessly.
	mu.Lock()
	for _, m := range held {
		a3Reply(heldConn, m, a3Echo{N: 99}, nil)
	}
	mu.Unlock()
	var out a3Echo
	if err := c.Call(context.Background(), "echo", a3Echo{N: 3}, &out); err != nil || out.N != 3 {
		t.Fatalf("after late reply: %+v %v", out, err)
	}
}

func TestA3CallContextCanceled(t *testing.T) {
	c := a3NewServer(t, a3EchoHandler).dial(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Call(ctx, "hang", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled: %v", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	if err := c.Call(ctx, "hang", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled while waiting: %v", err)
	}
	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("cancelled calls left %d pending entries", n)
	}
}

func TestA3CallContextCanceledWhileOutboxFull(t *testing.T) {
	// A client whose writer never drains: Call blocks sending to the outbox
	// until its context ends.
	c := &Client{
		pending: map[string]chan proto.Message{},
		outbox:  make(chan proto.Message),
		done:    make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "x", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if len(c.pending) != 0 {
		t.Fatalf("pending not cleaned: %v", c.pending)
	}

	// Closing the connection unblocks a Call stuck on the outbox.
	c2 := &Client{
		pending: map[string]chan proto.Message{},
		outbox:  make(chan proto.Message),
		done:    make(chan struct{}),
	}
	errc := make(chan error, 1)
	go func() { errc <- c2.Call(context.Background(), "x", nil, nil) }()
	time.Sleep(20 * time.Millisecond)
	c2.mu.Lock()
	c2.err = ErrClosed
	close(c2.done)
	c2.mu.Unlock()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not return after the connection closed")
	}

	// Notify on a closed client does not block.
	notified := make(chan struct{})
	go func() { c2.Notify("x", nil); close(notified) }()
	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatal("Notify blocked on a closed client")
	}
}

func TestA3NotifyOrder(t *testing.T) {
	got := make(chan proto.Message, 300)
	c := a3NewServer(t, func(conn *proto.Conn, msg proto.Message) {
		if msg.Method == "key" {
			got <- msg
			return
		}
		a3EchoHandler(conn, msg)
	}).dial(t)

	const n = 200
	for i := 0; i < n; i++ {
		c.Notify("key", a3Echo{N: i})
	}
	for i := 0; i < n; i++ {
		select {
		case msg := <-got:
			if msg.ID != "" {
				t.Fatalf("notification carries an ID: %q", msg.ID)
			}
			var v a3Echo
			if err := json.Unmarshal(msg.Params, &v); err != nil || v.N != i {
				t.Fatalf("notification %d out of order: %s %v", i, msg.Params, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d notifications arrived", i)
		}
	}
	c.Notify("key", nil)
	select {
	case msg := <-got:
		if msg.Params != nil {
			t.Fatalf("nil params encoded as %s", msg.Params)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nil-param notification lost")
	}
}

func TestA3EventsDelivery(t *testing.T) {
	c := a3NewServer(t, func(conn *proto.Conn, msg proto.Message) {
		if msg.Method == "burst" {
			for i := 0; i < 50; i++ {
				_ = conn.Write(proto.Message{Event: proto.EventPaneUpdated, Data: proto.Marshal(proto.PaneInfo{ID: fmt.Sprint(i)})})
			}
			// An event that happens to carry an ID is still an event.
			_ = conn.Write(proto.Message{ID: msg.ID, Event: proto.EventPaneClosed, Data: proto.Marshal(proto.PaneRef{ID: "last"})})
			a3Reply(conn, msg, a3Echo{N: 1}, nil)
			return
		}
		a3EchoHandler(conn, msg)
	}).dial(t)

	var out a3Echo
	if err := c.Call(context.Background(), "burst", nil, &out); err != nil || out.N != 1 {
		t.Fatalf("call interleaved with events: %+v %v", out, err)
	}
	for i := 0; i < 50; i++ {
		ev := <-c.Events
		var p proto.PaneInfo
		if ev.Event != proto.EventPaneUpdated || json.Unmarshal(ev.Data, &p) != nil || p.ID != fmt.Sprint(i) {
			t.Fatalf("event %d: %+v", i, ev)
		}
	}
	ev := <-c.Events
	if ev.Event != proto.EventPaneClosed {
		t.Fatalf("event with ID: %+v", ev)
	}
}

func TestA3ServerCloseEndsConnection(t *testing.T) {
	started := make(chan struct{})
	c := a3NewServer(t, func(conn *proto.Conn, msg proto.Message) {
		if msg.Method == "die" {
			close(started)
			conn.Close()
		}
	}).dial(t)

	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "die", nil, nil) }()
	<-started
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("pending call: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending call not released when the server hung up")
	}
	a3WaitClosed(t, c.Events)
	if !errors.Is(c.Err(), ErrClosed) {
		t.Fatalf("Err = %v", c.Err())
	}
	if err := c.Call(context.Background(), "echo", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after close: %v", err)
	}
	c.Notify("key", nil) // must not block or panic
}

func a3WaitClosed(t *testing.T, ch <-chan proto.Message) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("Events not closed")
		}
	}
}

func TestA3ClientClose(t *testing.T) {
	c := a3NewServer(t, a3EchoHandler).dial(t)
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "hang", nil, nil) }()
	time.Sleep(30 * time.Millisecond)

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil { // idempotent
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("pending call after Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not release the pending call")
	}
	a3WaitClosed(t, c.Events)
	if !errors.Is(c.Err(), ErrClosed) {
		t.Fatalf("Err = %v", c.Err())
	}
}

func TestA3CloseWithUndrainedEvents(t *testing.T) {
	flooded := make(chan struct{})
	c := a3NewServer(t, func(conn *proto.Conn, msg proto.Message) {
		if msg.Method == "flood" {
			for i := 0; i < 1100; i++ {
				if conn.Write(proto.Message{Event: "tick"}) != nil {
					break
				}
			}
			close(flooded)
		}
	}).dial(t)
	c.Notify("flood", nil)
	<-flooded
	time.Sleep(50 * time.Millisecond) // let the reader block on the full Events buffer
	c.Close()
	a3WaitClosed(t, c.Events)
}

func TestA3GarbageFromServer(t *testing.T) {
	a3Isolate(t)
	cli, srv := net.Pipe()
	defer srv.Close()
	go func() {
		sc := proto.NewConn(srv)
		msg, err := sc.Read()
		if err != nil {
			return
		}
		a3Reply(sc, msg, proto.HelloResult{Version: "1"}, nil)
		_, _ = io.WriteString(srv, "this is not json\n")
	}()
	pc, err := New(cli, "x")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	a3WaitClosed(t, pc.Events)
	if err := pc.Err(); err == nil || errors.Is(err, ErrClosed) || !strings.Contains(err.Error(), "decode message") {
		t.Fatalf("Err = %v, want the decode error", err)
	}
}

// a3FailWriter lets the handshake through, then fails every write.
type a3FailWriter struct {
	net.Conn
	mu     sync.Mutex
	broken bool
}

var errA3Write = errors.New("a3: write refused")

func (w *a3FailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	broken := w.broken
	w.mu.Unlock()
	if broken {
		return 0, errA3Write
	}
	return w.Conn.Write(p)
}

func TestA3WriteFailure(t *testing.T) {
	cli, srv := net.Pipe()
	defer srv.Close()
	go func() {
		sc := proto.NewConn(srv)
		for {
			msg, err := sc.Read()
			if err != nil {
				return
			}
			a3Reply(sc, msg, proto.HelloResult{}, nil)
		}
	}()
	fw := &a3FailWriter{Conn: cli}
	c, err := New(fw, "x")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fw.mu.Lock()
	fw.broken = true
	fw.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Call(ctx, "echo", nil, nil); !errors.Is(err, errA3Write) {
		t.Fatalf("call over a broken writer: %v", err)
	}
	if !errors.Is(c.Err(), errA3Write) {
		t.Fatalf("Err = %v", c.Err())
	}
}

func TestA3ConcurrentCalls(t *testing.T) {
	c := a3NewServer(t, func(conn *proto.Conn, msg proto.Message) {
		// Answer out of order to exercise ID matching.
		go func() {
			time.Sleep(time.Duration(len(msg.ID)%3) * time.Millisecond)
			a3EchoHandler(conn, msg)
		}()
	}).dial(t)

	var wg sync.WaitGroup
	errs := make(chan error, 1000)
	for g := 0; g < 25; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				want := a3Echo{N: g*1000 + i, Text: fmt.Sprintf("g%d-%d", g, i)}
				var out a3Echo
				if err := c.Call(context.Background(), "echo", want, &out); err != nil {
					errs <- err
					return
				}
				if out != want {
					errs <- fmt.Errorf("got %+v, want %+v", out, want)
					return
				}
				c.Notify("noop", nil)
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestA3EnsureServerAlreadyRunning(t *testing.T) {
	s := a3NewServer(t, nil)
	logPath := filepath.Join(a3ShortDir(t), "never", "server.log")
	if err := EnsureServer(s.sock, logPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("a running server must not create the log: %v", err)
	}
}

func TestA3EnsureServerStarts(t *testing.T) {
	a3Isolate(t)
	t.Setenv("A3_FAKE_SERVER", "1")
	dir := a3ShortDir(t)
	sock := filepath.Join(dir, "s.sock")
	logPath := filepath.Join(dir, "logs", "server.log")
	if err := EnsureServer(sock, logPath); err != nil {
		t.Fatal(err)
	}
	nc, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("server not listening after EnsureServer: %v", err)
	}
	nc.Close()
	info, err := os.Stat(filepath.Dir(logPath))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("log dir: %v %v", info, err)
	}
	// The child's stdout goes to the log.
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, _ := os.ReadFile(logPath)
		if strings.Contains(string(b), "fake server listening on "+sock) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("log = %q", b)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestA3EnsureServerExitsDuringStartup(t *testing.T) {
	a3Isolate(t)
	t.Setenv("A3_FAKE_SERVER", "1")
	t.Setenv("A3_FAKE_SERVER_FAIL", "1")
	dir := a3ShortDir(t)
	logPath := filepath.Join(dir, "server.log")
	err := EnsureServer(filepath.Join(dir, "s.sock"), logPath)
	if err == nil || !strings.Contains(err.Error(), "server exited during startup") || !strings.Contains(err.Error(), logPath) {
		t.Fatalf("got %v", err)
	}
	b, _ := os.ReadFile(logPath)
	if !strings.Contains(string(b), "failing on purpose") {
		t.Fatalf("stderr not logged: %q", b)
	}
}

func TestA3EnsureServerLogErrors(t *testing.T) {
	a3Isolate(t)
	dir := a3ShortDir(t)
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureServer(filepath.Join(dir, "s.sock"), filepath.Join(file, "sub", "server.log")); err == nil {
		t.Fatal("log dir under a file: no error")
	}
	logDir := filepath.Join(dir, "isdir")
	if err := os.Mkdir(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := EnsureServer(filepath.Join(dir, "s.sock"), logDir); err == nil {
		t.Fatal("log path is a directory: no error")
	}
}

// A client that doesn't take its events used to stop reading the
// connection once Events filled, so replies waited behind pane output —
// with busy agents a diff could take seconds or never load. Events are
// queued on their own now; a reply arrives however much is unread.
func TestRepliesArriveWhileEventsPileUp(t *testing.T) {
	const flood = 3000 // well past the 1024-slot Events buffer
	c := a3NewServer(t, func(conn *proto.Conn, msg proto.Message) {
		if msg.Method == "flood" {
			for i := 0; i < flood; i++ {
				_ = conn.Write(proto.Message{Event: proto.EventPaneUpdated, Data: proto.Marshal(proto.PaneInfo{ID: fmt.Sprint(i)})})
			}
		}
		a3Reply(conn, msg, a3Echo{N: 7}, nil)
	}).dial(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out a3Echo
	if err := c.Call(ctx, "flood", nil, &out); err != nil || out.N != 7 {
		t.Fatalf("call behind %d unread events: %+v %v", flood, out, err)
	}
	// Nothing was read yet, and another call still gets through.
	if err := c.Call(ctx, "again", nil, &out); err != nil {
		t.Fatalf("second call: %v", err)
	}
	// The events are all there, in order, once the client reads them.
	for i := 0; i < flood; i++ {
		select {
		case ev := <-c.Events:
			var p proto.PaneInfo
			if json.Unmarshal(ev.Data, &p) != nil || p.ID != fmt.Sprint(i) {
				t.Fatalf("event %d: %s", i, ev.Data)
			}
		case <-ctx.Done():
			t.Fatalf("only %d of %d events arrived", i, flood)
		}
	}
}

// Pane frames are whole screens, so a newer frame replaces the one still
// queued for the same pane; other events and other panes keep their place.
func TestQueuedFramesCoalesce(t *testing.T) {
	c := &Client{done: make(chan struct{})}
	c.evCond = sync.NewCond(&c.evMu)
	c.evFrame = map[string]int{}
	frame := func(id, text string) proto.Message {
		return proto.Message{Event: proto.EventPaneFrame, Data: proto.Marshal(proto.Frame{ID: id, Lines: []string{text}})}
	}
	c.queueEvent(frame("p1", "one"))
	c.queueEvent(proto.Message{Event: proto.EventPaneUpdated, Data: proto.Marshal(proto.PaneInfo{ID: "p1"})})
	c.queueEvent(frame("p2", "other"))
	c.queueEvent(frame("p1", "two"))
	c.queueEvent(frame("p1", "three"))

	var got []string
	for _, m := range c.evQueue {
		if m.Event == proto.EventPaneFrame {
			var f proto.Frame
			_ = json.Unmarshal(m.Data, &f)
			got = append(got, f.ID+":"+f.Lines[0])
		} else {
			got = append(got, m.Event)
		}
	}
	if strings.Join(got, " ") != "p1:three "+proto.EventPaneUpdated+" p2:other" {
		t.Fatalf("queue: %v", got)
	}

	// A frame that can't be read isn't merged with anything.
	c.queueEvent(proto.Message{Event: proto.EventPaneFrame, Data: []byte("{bad")})
	c.queueEvent(proto.Message{Event: proto.EventPaneFrame, Data: []byte("{bad")})
	if len(c.evQueue) != 5 {
		t.Fatalf("unreadable frames were merged: %d queued", len(c.evQueue))
	}
	if frameID(proto.Message{Data: []byte(`{"id":"p9"}`)}) != "p9" || frameID(proto.Message{Data: []byte("x")}) != "" {
		t.Fatal("frameID")
	}

	// After the queued frame is handed on, the next one queues afresh.
	c.evMu.Lock()
	c.dropFirstLocked()
	c.evMu.Unlock()
	c.queueEvent(frame("p1", "four"))
	if n := len(c.evQueue); n != 5 || c.evFrame["p1"] != n-1 {
		t.Fatalf("after hand-on: %d queued, p1 at %d", n, c.evFrame["p1"])
	}
	if i := c.evFrame["p2"]; i != 1 {
		t.Fatalf("p2's index not shifted: %d", i)
	}
}

// A client that stops reading entirely keeps the newest events, not memory
// without bound; nothing is queued once the connection has closed.
func TestEventQueueIsBounded(t *testing.T) {
	c := &Client{done: make(chan struct{})}
	c.evCond = sync.NewCond(&c.evMu)
	c.evFrame = map[string]int{}
	for i := 0; i < maxQueuedEvents+100; i++ {
		c.queueEvent(proto.Message{Event: proto.EventPaneUpdated, Data: proto.Marshal(proto.PaneInfo{ID: fmt.Sprint(i)})})
	}
	if len(c.evQueue) != maxQueuedEvents {
		t.Fatalf("queued %d, want %d", len(c.evQueue), maxQueuedEvents)
	}
	var first, last proto.PaneInfo
	_ = json.Unmarshal(c.evQueue[0].Data, &first)
	_ = json.Unmarshal(c.evQueue[len(c.evQueue)-1].Data, &last)
	if first.ID != "100" || last.ID != fmt.Sprint(maxQueuedEvents+99) {
		t.Fatalf("kept %s..%s, want the newest", first.ID, last.ID)
	}

	// Frames dropped off the front forget their place.
	c.evQueue, c.evFrame = nil, map[string]int{}
	c.queueEvent(proto.Message{Event: proto.EventPaneFrame, Data: proto.Marshal(proto.Frame{ID: "p1"})})
	for i := 0; i < maxQueuedEvents; i++ {
		c.queueEvent(proto.Message{Event: proto.EventPaneUpdated})
	}
	if _, ok := c.evFrame["p1"]; ok {
		t.Fatal("a dropped frame is still indexed")
	}

	c.closeEvents()
	before := len(c.evQueue)
	c.queueEvent(proto.Message{Event: proto.EventPaneUpdated})
	if len(c.evQueue) != before {
		t.Fatal("queued after close")
	}
}

// a3Wedged is a socket that accepts connections and answers nothing: what a
// server wedged partway through a reload leaves behind.
func a3Wedged(t *testing.T) string {
	t.Helper()
	sock := filepath.Join(a3ShortDir(t), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		var held []net.Conn
		defer func() {
			for _, nc := range held {
				nc.Close()
			}
		}()
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, nc) // kept open, never answered
		}
	}()
	return sock
}

// answers is the difference between a server that is there and one that only
// looks like it: connecting proves nothing.
func TestA3Answers(t *testing.T) {
	a3Isolate(t)
	dir := a3ShortDir(t)
	if answers(filepath.Join(dir, "nothing.sock"), 200*time.Millisecond) {
		t.Error("a socket that isn't there should not answer")
	}
	plain := filepath.Join(dir, "a-file")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if answers(plain, 200*time.Millisecond) {
		t.Error("a plain file should not answer")
	}
	start := time.Now()
	if answers(a3Wedged(t), 200*time.Millisecond) {
		t.Error("a socket nobody serves should not answer")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("waited %v on a silent socket, want about the probe", took)
	}
	if s := a3NewServer(t, nil); !answers(s.sock, 5*time.Second) {
		t.Error("a running server should answer")
	}
	// A server that refuses the handshake is still a server.
	s := a3NewServer(t, nil)
	s.hello = func(proto.HelloParams) (any, *proto.Error) {
		return nil, &proto.Error{Code: "too_old", Message: "no"}
	}
	if !answers(s.sock, 5*time.Second) {
		t.Error("a refusal still means a server is serving")
	}
}

// The recovery that matters: a socket left by a wedged server is cleared out
// of the way and a new server takes its place.
func TestA3EnsureServerReplacesAWedgedSocket(t *testing.T) {
	a3Isolate(t)
	t.Setenv("A3_FAKE_SERVER", "1")
	probeWait = 300 * time.Millisecond
	t.Cleanup(func() { probeWait = 5 * time.Second })

	sock := a3Wedged(t)
	before, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(a3ShortDir(t), "logs", "server.log")
	if err := EnsureServer(sock, logPath); err != nil {
		t.Fatalf("EnsureServer on a wedged socket: %v", err)
	}
	after, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("no socket after recovery: %v", err)
	}
	if os.SameFile(before, after) {
		t.Error("the wedged socket was reused instead of replaced")
	}
	c, err := Dial(sock, "test")
	if err != nil {
		t.Fatalf("the new server does not answer: %v", err)
	}
	c.Close()
}

// A healthy server is never disturbed, however often it is looked at.
func TestA3EnsureServerLeavesAHealthyOneAlone(t *testing.T) {
	s := a3NewServer(t, nil)
	before, err := os.Stat(s.sock)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := EnsureServer(s.sock, filepath.Join(a3ShortDir(t), "never.log")); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.Stat(s.sock)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("a running server's socket was replaced: %v", err)
	}
}

// A server that hangs up as it is asked, and one that talks before it
// answers: neither should be mistaken for silence.
func TestA3AnswersHangupAndChatter(t *testing.T) {
	a3Isolate(t)
	// Accepts, then closes at once — a server on its way down.
	sock := filepath.Join(a3ShortDir(t), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			nc.Close()
		}
	}()
	if answers(sock, time.Second) {
		t.Error("a server that hangs up is not answering")
	}

	// Events can arrive before the reply; the probe reads past them.
	chatty := filepath.Join(a3ShortDir(t), "c.sock")
	cln, err := net.Listen("unix", chatty)
	if err != nil {
		t.Fatal(err)
	}
	defer cln.Close()
	go func() {
		for {
			nc, err := cln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer nc.Close()
				conn := proto.NewConn(nc)
				msg, err := conn.Read()
				if err != nil {
					return
				}
				_ = conn.Write(proto.Message{Event: "pane.frame"})
				_ = conn.Write(proto.Message{Event: "agent.state"})
				a3Reply(conn, msg, proto.HelloResult{Version: proto.Version}, nil)
			}()
		}
	}()
	if !answers(chatty, 5*time.Second) {
		t.Error("events before the reply should not hide the answer")
	}
}

// A server that starts, binds and then serves nothing is not "running":
// EnsureServer gives up instead of handing back a connection that hangs.
func TestA3EnsureServerStartedButSilent(t *testing.T) {
	a3Isolate(t)
	t.Setenv("A3_FAKE_SERVER", "1")
	t.Setenv("A3_FAKE_SERVER_SILENT", "1")
	probeWait, startWait = 200*time.Millisecond, time.Second
	t.Cleanup(func() { probeWait, startWait = 5*time.Second, 5*time.Second })
	dir := a3ShortDir(t)
	err := EnsureServer(filepath.Join(dir, "s.sock"), filepath.Join(dir, "server.log"))
	if err == nil || !strings.Contains(err.Error(), "did not start within") {
		t.Fatalf("a silent server should not pass for a running one: %v", err)
	}
}

// A live server that misses one probe keeps its socket: only one that
// answers nothing at all is replaced.
func TestA3EnsureServerSecondChance(t *testing.T) {
	a3Isolate(t)
	sock := filepath.Join(a3ShortDir(t), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var seen atomic.Int32
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer nc.Close()
				conn := proto.NewConn(nc)
				msg, err := conn.Read()
				if err != nil {
					return
				}
				if seen.Add(1) == 1 {
					time.Sleep(2 * time.Second) // busy: misses the first probe
					return
				}
				a3Reply(conn, msg, proto.HelloResult{Version: proto.Version}, nil)
			}()
		}
	}()
	before, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	probeWait = 300 * time.Millisecond
	t.Cleanup(func() { probeWait = 5 * time.Second })
	// If a server is started at all the test fails: this one exits at once.
	t.Setenv("A3_FAKE_SERVER", "1")
	t.Setenv("A3_FAKE_SERVER_FAIL", "1")
	if err := EnsureServer(sock, filepath.Join(a3ShortDir(t), "server.log")); err != nil {
		t.Fatalf("a server that answered the second probe: %v", err)
	}
	after, err := os.Stat(sock)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("a busy server's socket was replaced: %v", err)
	}
	if n := seen.Load(); n < 2 {
		t.Fatalf("probed %d times, want a second chance", n)
	}
}

// A socket that cannot be removed (a read-only directory) fails clearly
// instead of hanging or looking like success.
func TestA3EnsureServerCannotClearSocket(t *testing.T) {
	a3Isolate(t)
	t.Setenv("A3_FAKE_SERVER", "1")
	dir := a3ShortDir(t)
	sockDir := filepath.Join(dir, "sock")
	if err := os.Mkdir(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(sockDir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			_ = nc // accepted, never answered
		}
	}()
	if err := os.Chmod(sockDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sockDir, 0o700) })
	probeWait, startWait = 200*time.Millisecond, time.Second
	t.Cleanup(func() { probeWait, startWait = 5*time.Second, 5*time.Second })
	err = EnsureServer(sock, filepath.Join(dir, "server.log"))
	if err == nil {
		t.Fatal("want an error when the socket cannot be cleared")
	}
}
