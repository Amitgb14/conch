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

	"github.com/charmbracelet/x/ansi"

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
	// panes is what pane.list answers (p3 running unless a test says
	// otherwise), and caller the pane pane.caller names.
	panes  []proto.PaneInfo
	caller string
}

func startAttachServer(t *testing.T) (*attachServer, *client.Client) {
	t.Helper()
	a4Env(t)
	s := &attachServer{a4Server: startA4Server(t, config.SocketPath()),
		panes: []proto.PaneInfo{{ID: "p3", State: proto.PaneRunning}}}
	s.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch msg.Method {
		case proto.MethodPaneSubscribe:
			s.conn = conn
		case proto.MethodPaneList:
			return proto.PaneList{Panes: s.panes}, nil
		case proto.MethodPaneCaller:
			return proto.CallerInfo{Pane: s.caller}, nil
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
		msg, err := attachLoop(c, "p3", inR, out, size, winch, nil)
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
		msg, _ := attachLoop(c, "p3", strings.NewReader(""), io.Discard, func() (int, int, bool) { return 0, 0, false }, nil, nil)
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
		srv.StopAndWait()
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
		msg, err := attachLoop(c, id, inR, screen, func() (int, int, bool) { return 60, 10, true }, nil, nil)
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

// A pane already over when attached — exited (its last frame comes, no
// exit event), or gone — is said at once rather than shown for ever.
func TestAttachEndedPane(t *testing.T) {
	for _, tc := range []struct {
		panes []proto.PaneInfo
		want  string
	}{
		{[]proto.PaneInfo{{ID: "p3", State: proto.PaneExited, ExitCode: 1}}, "pane p3 ended (exit 1)"},
		{[]proto.PaneInfo{{ID: "p4", State: proto.PaneRunning}}, "pane p3 was closed"},
		{nil, "pane p3 was closed"},
	} {
		srv, c := startAttachServer(t)
		srv.mu.Lock()
		srv.panes = tc.panes
		srv.mu.Unlock()
		inR, inW := io.Pipe()
		out := &syncBuffer{}
		done := make(chan string, 1)
		go func() {
			msg, _ := attachLoop(c, "p3", inR, out, func() (int, int, bool) { return 80, 24, true }, nil, nil)
			done <- msg
		}()
		select {
		case msg := <-done:
			if msg != tc.want {
				t.Errorf("%+v: %q, want %q", tc.panes, msg, tc.want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%+v: attach kept showing an ended pane", tc.panes)
		}
		inW.Close()
		if !strings.HasSuffix(out.String(), attachLeave) {
			t.Errorf("%+v: terminal not given back", tc.panes)
		}
	}
}

// Killed or hung up, attach ends and gives the terminal back.
func TestAttachQuitsOnSignal(t *testing.T) {
	_, c := startAttachServer(t)
	inR, inW := io.Pipe()
	defer inW.Close()
	out := &syncBuffer{}
	quit := make(chan os.Signal, 1)
	done := make(chan string, 1)
	go func() {
		msg, _ := attachLoop(c, "p3", inR, out, func() (int, int, bool) { return 80, 24, true }, nil, quit)
		done <- msg
	}()
	quit <- os.Interrupt
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("did not end on the signal")
	}
	if !strings.HasSuffix(out.String(), attachLeave) {
		t.Fatalf("terminal not given back: %q", out.String())
	}
}

// Attaching the pane it runs in is refused, as the server says which pane
// that is; another pane, or a server that names none, is not.
func TestAttachRefusesItsOwnPane(t *testing.T) {
	srv, c := startAttachServer(t)
	if err := attachRefusal(c, "p3"); err != nil {
		t.Fatalf("no caller: %v", err)
	}
	srv.mu.Lock()
	srv.caller = "p3"
	srv.mu.Unlock()
	if err := attachRefusal(c, "p3"); err == nil || !strings.Contains(err.Error(), "runs in") {
		t.Fatalf("own pane: %v", err)
	}
	if err := attachRefusal(c, "p4"); err != nil {
		t.Fatalf("another pane: %v", err)
	}
}

// Shift with page up and down, home and end scroll the history on the main
// screen, a page at a time, kept to what there is; a full-screen program,
// or a view with no frame yet, gets them as keys.
func TestAttachScrollKeys(t *testing.T) {
	srv, c := startAttachServer(t)
	s := &attachSession{c: c, id: "p3", out: io.Discard}
	s.input([]byte("\x1b[5;2~"), false) // no frame yet: the pane's
	s.frame = &proto.Frame{ID: "p3", Rows: 10, History: 25}
	for _, k := range []string{
		"\x1b[5;2~", "\x1b[5;2~", "\x1b[5;2~", // up 9, 18, then 25 (all there is)
		"\x1b[6;2~", // down to 16
		"\x1b[1;2F", // shift+end: live
		"\x1b[1;2H", // shift+home: the top
		"\x1b[6;2~", // 16
		"\x1b[6;2~", // 7
		"\x1b[6;2~", // 0, not below
		"\x1b[6;2~", // nothing to send
	} {
		s.input([]byte(k), false)
	}
	var offsets []int
	for _, p := range attachSent[proto.PaneScrollParams](srv, proto.MethodPaneScroll) {
		offsets = append(offsets, p.Offset)
	}
	if got := fmt.Sprint(offsets); got != "[9 18 25 16 0 25 16 7 0]" {
		t.Fatalf("scroll offsets %v", offsets)
	}
	s.frame = &proto.Frame{ID: "p3", Rows: 10, AltScreen: true}
	s.input([]byte("\x1b[6;2~\x1b[5~"), false) // a full-screen program's, and a plain pgup always is
	var keys []string
	for _, p := range attachSent[proto.PaneSendKeysParams](srv, proto.MethodPaneSendKeys) {
		keys = append(keys, p.Keys...)
	}
	if got := strings.Join(keys, " "); got != "shift+pgup shift+pgdown pgup" {
		t.Fatalf("keys %q", got)
	}
}

// A paste starting with the app's token is a search for this view: the
// match brought to the middle when it is out of sight, every match on
// screen marked, and the marks gone when the search ends. Without the
// token — or with none given — it is the person's paste.
func TestAttachControlPaste(t *testing.T) {
	srv, c := startAttachServer(t)
	out := &syncBuffer{}
	s := &attachSession{c: c, id: "p3", out: out, control: "tok"}
	s.frame = &proto.Frame{ID: "p3", Cols: 20, Rows: 4, History: 100,
		Lines: []string{"one \x1b[1mError\x1b[0m two", "fine", "error again", ""}}
	paste := func(body string) { s.input([]byte("\x1b[200~"+body+"\x1b[201~"), false) }
	ctl := func(v attachControl) string {
		b, _ := json.Marshal(v)
		return attachControlMark + "tok " + string(b)
	}

	// Line 40 of 100 is out of sight from the live screen: brought to the
	// second row of four (100 - (40 - 2)).
	paste(ctl(attachControl{Find: "error", Line: 40, History: 100}))
	sc := attachSent[proto.PaneScrollParams](srv, proto.MethodPaneScroll)
	if len(sc) != 1 || sc[0].Offset != 62 || s.offset != 62 {
		t.Fatalf("scrolls %+v, offset %d", sc, s.offset)
	}
	drawn := out.String()
	if !strings.Contains(drawn, attachMatch+"Error\x1b[0m") || !strings.Contains(drawn, attachMatch+"error\x1b[0m") {
		t.Fatalf("matches not marked (smart case): %q", drawn)
	}
	// Already in view (offset 62 shows lines 38–41): no scroll.
	paste(ctl(attachControl{Find: "error", Line: 41, History: 100}))
	if n := len(attachSent[proto.PaneScrollParams](srv, proto.MethodPaneScroll)); n != 1 {
		t.Fatalf("scrolled for a match in view (%d scrolls)", n)
	}
	// A match on the live screen goes back to it.
	paste(ctl(attachControl{Find: "error", Line: 101, History: 100}))
	if s.offset != 0 {
		t.Fatalf("offset %d for a match on the screen", s.offset)
	}
	// A frame drawn later is marked too, until the search ends.
	s.event(proto.Message{Event: proto.EventPaneFrame, Data: proto.Marshal(proto.Frame{ID: "p3", Cols: 20, Rows: 4, History: 100, Lines: []string{"new error"}})})
	if !strings.HasSuffix(out.String(), "new "+attachMatch+"error\x1b[0m"+strings.Repeat(" ", 0)+"\x1b[0m\x1b[K\x1b[0m\x1b[J\x1b[?2026l") {
		t.Fatalf("a new frame is not marked: %q", out.String()[max(len(out.String())-80, 0):])
	}
	paste(ctl(attachControl{Clear: true}))
	if s.query != "" || strings.Contains(out.String()[strings.LastIndex(out.String(), "\x1b[?2026h"):], attachMatch) {
		t.Fatal("the marks outlived the search")
	}
	if s.offset != 0 {
		t.Fatal("ending the search moved the view")
	}

	// Garbled after the token: dropped, not pasted. Another token, no
	// token, or a view given none: the person's paste.
	paste(attachControlMark + "tok {nope")
	paste(attachControlMark + "other " + `{"find":"x"}`)
	paste("plain")
	s.control = ""
	paste(ctl(attachControl{Find: "x"}))
	var texts []string
	for _, p := range attachSent[proto.PaneSendTextParams](srv, proto.MethodPaneSendText) {
		texts = append(texts, p.Text)
	}
	if len(texts) != 3 || texts[1] != "plain" || !strings.Contains(texts[0], "other") || s.query != "" {
		t.Fatalf("pasted %q, query %q", texts, s.query)
	}
}

func TestMarkMatches(t *testing.T) {
	for _, c := range []struct {
		line  string
		spans [][2]int
		w     int
		want  string
	}{
		{"abc", nil, 3, "abc"},
		{"abc", [][2]int{{0, 1}}, 0, "abc"},                                 // no width: nothing
		{"abcdef", [][2]int{{1, 3}}, 6, "a" + attachMatch + "bc\x1b[0mdef"}, // the middle
		{"ab", [][2]int{{1, 9}}, 4, "a" + attachMatch + "b  \x1b[0m"},       // to the edge, padded
		{"\x1b[31mred\x1b[0m", [][2]int{{0, 3}}, 3, attachMatch + "red\x1b[0m"},
		{"x", [][2]int{{5, 7}}, 3, "x"}, // past the width
	} {
		if got := markMatches(c.line, c.spans, c.w); got != c.want {
			t.Errorf("markMatches(%q, %v, %d) = %q, want %q", c.line, c.spans, c.w, got, c.want)
		}
	}
}

// -hold keeps an ended pane's last screen, with why it ended on its bottom
// line: the wheel still scrolls it, a key ends it, and the pane closed ends
// it too. Without it an ended pane ends attach at once (TestAttachEndedPane).
func TestAttachHold(t *testing.T) {
	run := func(t *testing.T, panes []proto.PaneInfo) (*attachServer, *io.PipeWriter, *syncBuffer, chan string) {
		srv, c := startAttachServer(t)
		srv.mu.Lock()
		srv.panes = panes
		srv.mu.Unlock()
		inR, inW := io.Pipe()
		out := &syncBuffer{}
		done := make(chan string, 1)
		go func() {
			msg, _ := attachLoopHold(c, "p3", inR, out, func() (int, int, bool) { return 60, 3, true }, nil, nil, true)
			done <- msg
		}()
		t.Cleanup(func() { inW.Close() })
		return srv, inW, out, done
	}
	frame := proto.Frame{ID: "p3", Cols: 60, Rows: 3, History: 5, Lines: []string{"build ok", "", "$"}}

	t.Run("ended before", func(t *testing.T) {
		srv, inW, out, done := run(t, []proto.PaneInfo{{ID: "p3", State: proto.PaneExited, ExitCode: 1}})
		srv.push(t, proto.EventPaneFrame, frame)
		attachWait(t, "the note", func() bool { return strings.Contains(out.String(), "pane p3 ended (exit 1) · any key closes") })
		if !strings.Contains(out.String(), "build ok") {
			t.Fatal("the last screen was not drawn")
		}
		inW.Write([]byte("\x1b[<64;1;1M")) // the wheel: scrolls, does not end it
		attachWait(t, "a scroll", func() bool { return len(attachSent[proto.PaneScrollParams](srv, proto.MethodPaneScroll)) == 1 })
		select {
		case msg := <-done:
			t.Fatalf("the wheel ended it: %q", msg)
		case <-time.After(100 * time.Millisecond):
		}
		inW.Write([]byte("q"))
		select {
		case msg := <-done:
			if msg != "pane p3 ended (exit 1)" {
				t.Fatalf("ended with %q", msg)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a key did not end it")
		}
		if n := len(attachSent[proto.PaneSendTextParams](srv, proto.MethodPaneSendText)); n != 0 {
			t.Fatalf("the key that closed it was typed into the pane (%d)", n)
		}
	})

	t.Run("ends while shown", func(t *testing.T) {
		srv, _, out, done := run(t, []proto.PaneInfo{{ID: "p3", State: proto.PaneRunning}})
		srv.push(t, proto.EventPaneFrame, frame)
		srv.push(t, proto.EventPaneExited, proto.PaneInfo{ID: "p3", State: proto.PaneExited})
		attachWait(t, "the note", func() bool { return strings.Contains(out.String(), "pane p3 ended (exit 0)") })
		select {
		case msg := <-done:
			t.Fatalf("ended at once: %q", msg)
		case <-time.After(100 * time.Millisecond):
		}
		srv.push(t, proto.EventPaneClosed, proto.PaneInfo{ID: "p3"})
		select {
		case msg := <-done:
			if msg != "pane p3 was closed" {
				t.Fatalf("closed: %q", msg)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("closing the pane did not end it")
		}
	})
}

// The note never runs past the pane: cut at a narrow one.
func TestAttachHoldNoteFits(t *testing.T) {
	out := &syncBuffer{}
	s := &attachSession{out: out, over: "pane p3 ended (exit 1)",
		frame: &proto.Frame{ID: "p3", Cols: 12, Rows: 2, Lines: []string{"a", "b"}}}
	s.draw()
	lines := strings.Split(out.String(), "\r\n")
	last := ansi.Strip(lines[len(lines)-1])
	if w := ansi.StringWidth(strings.TrimRight(last, "\x1b[0mKJ?26l")); w > 12 || !strings.HasPrefix(last, " pane p3") {
		t.Fatalf("note %q (%d wide)", last, w)
	}
}

func TestAttachHoldFlag(t *testing.T) {
	for _, args := range [][]string{{"-hold"}, {"-hold", "-x"}, {"-hold", "p1", "p2"}, {"-x", "p1"}} {
		if err := runAttach(args); err == nil || err.Error() != attachUsage {
			t.Errorf("%q: %v", args, err)
		}
	}
}
