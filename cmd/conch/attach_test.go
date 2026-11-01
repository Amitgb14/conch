package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

// attachServer is a fake server for one attached pane: it records what is
// sent and lets the test push events down the connection that subscribed.
type attachServer struct {
	*a4Server
	mu   sync.Mutex
	conn *proto.Conn
}

func startAttachServer(t *testing.T) (*attachServer, *client.Client) {
	t.Helper()
	a4Env(t)
	s := &attachServer{a4Server: startA4Server(t, config.SocketPath())}
	s.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodPaneSubscribe {
			s.mu.Lock()
			s.conn = conn
			s.mu.Unlock()
		}
		return nil, nil
	})
	c, err := connect(false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return s, c
}

// push sends an event to the subscribed connection, once there is one.
func (s *attachServer) push(t *testing.T, event string, v any) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		conn := s.conn
		s.mu.Unlock()
		if conn != nil {
			if err := conn.Write(proto.Message{Event: event, Data: proto.Marshal(v)}); err != nil {
				t.Fatal(err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("nothing subscribed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// sent is every call of a method so far, decoded.
func attachSent[T any](s *attachServer, method string) []T {
	s.a4Server.mu.Lock()
	defer s.a4Server.mu.Unlock()
	var out []T
	for _, c := range s.calls {
		if c.Method == method {
			var v T
			json.Unmarshal(c.Params, &v) //nolint:errcheck
			out = append(out, v)
		}
	}
	return out
}

// waitFor polls until ok, or fails the test.
func attachWait(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// syncBuffer is an io.Writer the loop writes to while the test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// The whole round: the terminal is taken and given back, the pane is
// sized to it and subscribed, a frame is drawn, typing reaches the pane,
// a resize follows the window, and the pane ending ends the attach.
func TestAttachShowsAndTypesIntoAPane(t *testing.T) {
	srv, c := startAttachServer(t)
	inR, inW := io.Pipe()
	defer inW.Close()
	out := &syncBuffer{}
	cols, rows := 80, 24
	var sizeMu sync.Mutex
	size := func() (int, int, bool) {
		sizeMu.Lock()
		defer sizeMu.Unlock()
		return cols, rows, true
	}
	winch := make(chan os.Signal, 1)
	type result struct {
		msg string
		err error
	}
	done := make(chan result, 1)
	go func() {
		msg, err := attachLoop(c, "p3", inR, out, size, winch)
		done <- result{msg, err}
	}()

	attachWait(t, "subscribe", func() bool { return len(attachSent[proto.PaneRef](srv, proto.MethodPaneSubscribe)) == 1 })
	if r := attachSent[proto.PaneResizeParams](srv, proto.MethodPaneResize); len(r) != 1 || r[0] != (proto.PaneResizeParams{ID: "p3", Cols: 80, Rows: 24}) {
		t.Fatalf("resize %+v", r)
	}
	srv.push(t, proto.EventPaneFrame, proto.Frame{ID: "p3", Cols: 80, Rows: 24, Lines: []string{"hello", "\x1b[1mworld\x1b[0m"}})
	srv.push(t, proto.EventPaneFrame, proto.Frame{ID: "p9", Lines: []string{"someone else's"}})
	attachWait(t, "the frame", func() bool { return strings.Contains(out.String(), "hello\x1b[0m\x1b[K\r\n\x1b[1mworld") })
	if !strings.HasPrefix(out.String(), attachEnter) {
		t.Fatalf("terminal not taken: %q", out.String())
	}

	io.WriteString(inW, "ls\r") //nolint:errcheck
	attachWait(t, "typing", func() bool {
		s := attachSent[proto.PaneSendTextParams](srv, proto.MethodPaneSendText)
		return len(s) == 1 && s[0].Text == "ls\r"
	})

	sizeMu.Lock()
	cols, rows = 100, 30
	sizeMu.Unlock()
	winch <- os.Interrupt
	attachWait(t, "the resize", func() bool { return len(attachSent[proto.PaneResizeParams](srv, proto.MethodPaneResize)) == 2 })

	srv.push(t, proto.EventPaneExited, proto.PaneInfo{ID: "p9"}) // not ours
	srv.push(t, proto.EventPaneExited, proto.PaneInfo{ID: "p3", ExitCode: 2})
	select {
	case r := <-done:
		if r.err != nil || r.msg != "pane p3 ended (exit 2)" {
			t.Fatalf("ended with %q, %v", r.msg, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attach did not end with its pane")
	}
	if !strings.HasSuffix(out.String(), attachLeave) {
		t.Fatalf("terminal not given back: %q", out.String()[max(len(out.String())-60, 0):])
	}
	if strings.Contains(out.String(), "someone else's") {
		t.Fatal("drew another pane's frame")
	}
}

// Closing the window (the input ending) detaches, and the pane carries on.
func TestAttachEndsWithItsInput(t *testing.T) {
	srv, c := startAttachServer(t)
	done := make(chan string, 1)
	go func() {
		msg, _ := attachLoop(c, "p3", strings.NewReader(""), io.Discard, func() (int, int, bool) { return 0, 0, false }, nil)
		done <- msg
	}()
	select {
	case msg := <-done:
		if msg != "" {
			t.Fatalf("message %q", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not end")
	}
	if len(attachSent[proto.PaneResizeParams](srv, proto.MethodPaneResize)) != 0 {
		t.Fatal("resized to a size it could not read")
	}
	if len(attachSent[proto.PaneRef](srv, proto.MethodPaneClose)) != 0 {
		t.Fatal("closed the pane")
	}
}

func TestAttachInput(t *testing.T) {
	srv, c := startAttachServer(t)
	s := &attachSession{c: c, id: "p3", out: io.Discard}
	type step struct {
		in    string
		final bool
		held  string
	}
	for _, st := range []step{
		{in: "abc"},
		{in: "\x1b[A"},                   // up, by name
		{in: "\x1bOB"},                   // down in application mode, by name
		{in: "\x1b[1;5C"},                // ctrl+right
		{in: "\x1b[5~"},                  // pgup
		{in: "\x1b[3;2~"},                // shift+delete
		{in: "x\x1b", held: "\x1b"},      // a lone esc waits for more
		{in: "\x1b", final: true},        // and goes when nothing came
		{in: "\x1b[1;", held: "\x1b[1;"}, // half a sequence waits
		{in: "\x1b[1;3D"},                // alt+left once it is whole
		{in: "\x1bx"},                    // alt+x as typed
		{in: "\x1b[200~line1\nline2\x1b[201~"},
		{in: "\x1b[200~par", held: "\x1b[200~par"},
		{in: "\x1b[Z"}, // shift+tab, as typed
		{in: "é😀"},
	} {
		if held := string(s.input([]byte(st.in), st.final)); held != st.held {
			t.Fatalf("%q held %q, want %q", st.in, held, st.held)
		}
	}
	var texts []string
	for _, p := range attachSent[proto.PaneSendTextParams](srv, proto.MethodPaneSendText) {
		t := p.Text
		if p.Paste {
			t = "paste:" + t
		}
		texts = append(texts, t)
	}
	if got, want := strings.Join(texts, "|"), "abc|x|\x1b|\x1bx|paste:line1\nline2|\x1b[Z|é😀"; got != want {
		t.Fatalf("text %q\nwant %q", got, want)
	}
	var keys []string
	for _, p := range attachSent[proto.PaneSendKeysParams](srv, proto.MethodPaneSendKeys) {
		keys = append(keys, p.Keys...)
	}
	if got, want := strings.Join(keys, " "), "up down ctrl+right pgup shift+delete alt+left"; got != want {
		t.Fatalf("keys %q, want %q", got, want)
	}
}

// The mouse follows the TUI's rule (AGENTS.md): the wheel scrolls conch's
// history on the main screen and goes to the program on the alternate
// screen; presses go only to a program that asked.
func TestAttachMouse(t *testing.T) {
	srv, c := startAttachServer(t)
	s := &attachSession{c: c, id: "p3", out: io.Discard}

	// Main screen, nothing asked: the wheel scrolls the history, up to
	// what there is and back down to 0; a click goes nowhere.
	s.frame = &proto.Frame{ID: "p3", History: 4}
	s.input([]byte("\x1b[<64;5;5M\x1b[<64;5;5M\x1b[<65;5;5M\x1b[<65;5;5M\x1b[<65;5;5M"), false)
	s.input([]byte("\x1b[<0;10;3M\x1b[<0;10;3m"), false)
	var offsets []int
	for _, p := range attachSent[proto.PaneScrollParams](srv, proto.MethodPaneScroll) {
		offsets = append(offsets, p.Offset)
	}
	if got := fmt.Sprint(offsets); got != "[3 4 1 0]" {
		t.Fatalf("scroll offsets %v", offsets)
	}
	if n := len(attachSent[proto.PaneSendMouseParams](srv, proto.MethodPaneSendMouse)); n != 0 {
		t.Fatalf("%d mouse events sent to a program that did not ask", n)
	}

	// Typing while scrolled back returns to the live screen first.
	s.input([]byte("\x1b[<64;5;5M"), false)
	s.input([]byte("q"), false)
	if sc := attachSent[proto.PaneScrollParams](srv, proto.MethodPaneScroll); sc[len(sc)-1].Offset != 0 || s.offset != 0 {
		t.Fatalf("typing left the view at %d", s.offset)
	}

	// The alternate screen, asked: the wheel and presses go as events,
	// 0-based, with their modifiers.
	s.frame = &proto.Frame{ID: "p3", AltScreen: true, Mouse: true}
	s.input([]byte("\x1b[<64;7;2M\x1b[<16;10;3M\x1b[<32;11;3M\x1b[<0;11;3m\x1b[<2;1;1M"), false)
	got := attachSent[proto.PaneSendMouseParams](srv, proto.MethodPaneSendMouse)
	want := []proto.PaneSendMouseParams{
		{ID: "p3", X: 6, Y: 1, Button: "wheel_up", Action: proto.MouseWheel},
		{ID: "p3", X: 9, Y: 2, Button: "left", Action: proto.MousePress, Ctrl: true},
		{ID: "p3", X: 10, Y: 2, Button: "left", Action: proto.MouseMotion},
		{ID: "p3", X: 10, Y: 2, Button: "left", Action: proto.MouseRelease},
		{ID: "p3", X: 0, Y: 0, Button: "right", Action: proto.MousePress},
	}
	if len(got) != len(want) {
		t.Fatalf("mouse %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d: %+v, want %+v", i, got[i], want[i])
		}
	}

	// The alternate screen, not asked: the wheel moves by arrows.
	s.frame = &proto.Frame{ID: "p3", AltScreen: true}
	s.input([]byte("\x1b[<65;1;1M"), false)
	keys := attachSent[proto.PaneSendKeysParams](srv, proto.MethodPaneSendKeys)
	if last := keys[len(keys)-1].Keys; strings.Join(last, " ") != "down down down" {
		t.Fatalf("wheel on a full-screen program: %v", last)
	}

	// Malformed reports are dropped, not typed.
	before := len(attachSent[proto.PaneSendTextParams](srv, proto.MethodPaneSendText))
	s.input([]byte("\x1b[<0;0;1M\x1b[<0;1M\x1b[<0;1;1;1M"), false)
	if after := len(attachSent[proto.PaneSendTextParams](srv, proto.MethodPaneSendText)); after != before {
		t.Fatal("a malformed mouse report was typed into the pane")
	}
}

func TestRenderAttachFrame(t *testing.T) {
	got := renderAttachFrame(&proto.Frame{Lines: []string{"a", "", "\x1b[31mc"}})
	want := "\x1b[?2026h\x1b[Ha\x1b[0m\x1b[K\r\n\x1b[0m\x1b[K\r\n\x1b[31mc\x1b[0m\x1b[K\x1b[0m\x1b[J\x1b[?2026l"
	if got != want {
		t.Fatalf("%q\nwant %q", got, want)
	}
	if got := renderAttachFrame(&proto.Frame{}); got != "\x1b[?2026h\x1b[H\x1b[0m\x1b[J\x1b[?2026l" {
		t.Fatalf("empty frame %q", got)
	}
}

func TestAttachNeedsOnePaneAndATerminal(t *testing.T) {
	a4Env(t)
	for _, args := range [][]string{nil, {"a", "b"}, {"-x"}} {
		if err := runAttach(args); err == nil || err.Error() != attachUsage {
			t.Errorf("%q: %v", args, err)
		}
	}
	// go test's stdin is not a terminal.
	if err := runAttach([]string{"p3"}); err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("no terminal: %v", err)
	}
}

// Against a real server, so what the frames carry and where is the
// server's own: a pane's output reaches this terminal, typing reaches the
// pane, and the pane ending ends the attach with its exit code.
func TestAttachRealServer(t *testing.T) {
	dir := a4Env(t)
	srv := server.New(config.SocketPath(), dir)
	go srv.Run()
	t.Cleanup(func() {
		srv.Stop()
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(config.SocketPath()); err != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	for i := 0; ; i++ {
		if c, err := client.Dial(config.SocketPath(), "test"); err == nil {
			c.Close()
			break
		}
		if i == 200 {
			t.Fatal("server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var err error
	out, _ := a4Capture(t, "", func() {
		err = runNew([]string{"-cwd", dir, "--", "/bin/sh", "-c", "echo ready-$((6*7)); read x; echo got-$x-$((1+1)); sleep 1; exit 3"})
	})
	id := strings.TrimSpace(out)
	if err != nil || id == "" {
		t.Fatalf("new: %q %v", out, err)
	}
	c, err := connect(false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	inR, inW := io.Pipe()
	defer inW.Close()
	screen := &syncBuffer{}
	done := make(chan string, 1)
	go func() {
		msg, err := attachLoop(c, id, inR, screen, func() (int, int, bool) { return 60, 10, true }, nil)
		if err != nil {
			msg = err.Error()
		}
		done <- msg
	}()
	// Markers only the program's output has, never the typed line.
	attachWait(t, "the pane's output", func() bool { return strings.Contains(screen.String(), "ready-42") })
	io.WriteString(inW, "abc\r") //nolint:errcheck
	attachWait(t, "the answer to typing", func() bool { return strings.Contains(screen.String(), "got-abc-2") })
	select {
	case msg := <-done:
		if msg != "pane "+id+" ended (exit 3)" {
			t.Fatalf("ended with %q", msg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("attach did not end with its pane")
	}
}
