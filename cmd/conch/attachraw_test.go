package main

import (
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

func TestRawInputSequence(t *testing.T) {
	for _, tc := range []struct {
		in    string
		final bool
		n     int
		reply bool
	}{
		{"\x1b[A", false, -1, false},            // up
		{"\x1b[1;5A", false, -1, false},         // ctrl+up
		{"\x1b[13;2~", false, -1, false},        // shift+F3, legacy
		{"\x1b[1;2R", false, -1, false},         // shift+F3, xterm: a key, not a position report
		{"\x1b[I", false, -1, true},             // focus in
		{"\x1b[O", false, -1, true},             // focus out
		{"\x1b[?62;22c", false, -1, true},       // DA1 reply
		{"\x1b[>1;10;0c", false, -1, true},      // DA2 reply
		{"\x1b[?2026;2$y", false, -1, true},     // DECRPM
		{"\x1b[?1u", false, -1, true},           // kitty key flags
		{"\x1b[97;5u", false, -1, false},        // ctrl+a, kitty: a key
		{"\x1b[48;24;80;0;0t", false, -1, true}, // in-band resize
		{"\x1b[<35;10;5M", false, -1, true},     // pointer moving, no button
		{"\x1b[<32;10;5M", false, -1, false},    // dragging with the left button
		{"\x1b[<0;10;5M", false, -1, false},     // a click
		{"\x1b[<64;10;5M", false, -1, false},    // the wheel
		{"\x1b]11;rgb:0000/0000/0000\x07", false, -1, true},
		{"\x1b]11;rgb:0000/0000/0000\x1b\\", false, -1, true},
		{"\x1bP>|ghostty 1.4.0\x1b\\", false, -1, true}, // XTVERSION
		{"\x1b_Gi=1;OK\x1b\\", false, -1, true},         // kitty graphics
		{"\x1bP", false, 2, false},                      // alt+P, not a DCS
		{"\x1b]", false, 2, false},                      // alt+]
		{"\x1bb", false, 2, false},                      // alt+b
		{"\x1b[", false, 0, false},                      // more to come
		{"\x1b[", true, 2, false},                       // nothing came: alt+[
		{"\x1b", false, 0, false},
		{"\x1b", true, 1, false}, // esc on its own
	} {
		if tc.n < 0 {
			tc.n = len(tc.in) // the whole of it
		}
		n, reply := rawInputSequence([]byte(tc.in), tc.final)
		if n != tc.n || reply != tc.reply {
			t.Errorf("rawInputSequence(%q, %v) = %d, %v; want %d, %v", tc.in, tc.final, n, reply, tc.n, tc.reply)
		}
	}
}

// startRawServer is the fake server with a pane.raw that answers with a
// replay and keeps the connection for pushing output.
func startRawServer(t *testing.T, raw bool) (*attachServer, *client.Client) {
	t.Helper()
	srv, c := startAttachServer(t)
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		switch msg.Method {
		case proto.MethodPaneRaw:
			srv.conn = conn
			return proto.PaneRawStart{ID: "p3", Cols: 80, Rows: 24, Replay: []byte("REPLAY-SCREEN")}, nil
		case proto.MethodPaneSubscribe:
			srv.conn = conn
		case proto.MethodPaneList:
			return proto.PaneList{Panes: srv.panes}, nil
		}
		return nil, nil
	})
	if !raw {
		var kept []string
		for _, x := range c.Server.Capabilities {
			if x != proto.CapPaneRaw {
				kept = append(kept, x)
			}
		}
		c.Server.Capabilities = kept
	}
	return srv, c
}

type rawRun struct {
	srv  *attachServer
	in   *io.PipeWriter
	out  *syncBuffer
	done chan string
	ok   chan bool
}

func runRaw(t *testing.T, srv *attachServer, c *client.Client, hold bool) *rawRun {
	t.Helper()
	t.Setenv(attachControlEnv, "tok")
	old := rawSettle
	rawSettle = 50 * time.Millisecond // this fake terminal is never the pane's size
	t.Cleanup(func() { rawSettle = old })
	inR, inW := io.Pipe()
	t.Cleanup(func() { inW.Close() })
	r := &rawRun{srv: srv, in: inW, out: &syncBuffer{}, done: make(chan string, 1), ok: make(chan bool, 1)}
	go func() {
		msg, ok, err := attachRawLoop(c, "p3", inR, r.out, func() (int, int, bool) { return 100, 30, true }, nil, nil, hold)
		if err != nil {
			msg = err.Error()
		}
		r.ok <- ok
		r.done <- msg
	}()
	return r
}

func TestAttachRawWithoutTheCapability(t *testing.T) {
	srv, c := startRawServer(t, false)
	r := runRaw(t, srv, c, false)
	if ok := <-r.ok; ok {
		t.Fatal("ran raw against a server without pane.raw.v1")
	}
	if len(attachSent[proto.PaneRef](srv, proto.MethodPaneRaw)) != 0 {
		t.Fatal("asked for raw output anyway")
	}
}

func TestAttachRawRound(t *testing.T) {
	srv, c := startRawServer(t, true)
	r := runRaw(t, srv, c, false)

	attachWait(t, "pane.raw", func() bool { return len(attachSent[proto.PaneRef](srv, proto.MethodPaneRaw)) == 1 })
	// Run by the app (a control token), it waits to be told the room.
	if rs := attachSent[proto.PaneResizeParams](srv, proto.MethodPaneResize); len(rs) != 0 {
		t.Fatalf("resize %+v before the app said how much room it has", rs)
	}
	attachWait(t, "the replay", func() bool { return strings.Contains(r.out.String(), rawClear+"REPLAY-SCREEN") })

	srv.push(t, proto.EventPaneOutput, proto.PaneOutput{ID: "p3", Data: []byte("live\x1b[31m!")})
	srv.push(t, proto.EventPaneOutput, proto.PaneOutput{ID: "p9", Data: []byte("someone else's")})
	attachWait(t, "live output", func() bool { return strings.HasSuffix(r.out.String(), "live\x1b[31m!") })
	srv.push(t, proto.EventPaneOutput, proto.PaneOutput{ID: "p3", Data: []byte("AGAIN"), Reset: true, Cols: 80, Rows: 24})
	attachWait(t, "a reset", func() bool { return strings.HasSuffix(r.out.String(), rawClear+"AGAIN") })
	if strings.Contains(r.out.String(), "someone else's") {
		t.Fatal("another pane's output was written")
	}

	texts := func() []proto.PaneSendTextParams {
		return attachSent[proto.PaneSendTextParams](srv, proto.MethodPaneSendText)
	}
	// Typing, as typing — escapes and all, since this terminal encoded them.
	io.WriteString(r.in, "ls\r\x1b[A") //nolint:errcheck
	attachWait(t, "typing", func() bool { return len(texts()) == 1 })
	if s := texts()[0]; s.Text != "ls\r\x1b[A" || s.Reply || s.Paste {
		t.Fatalf("typed %+v", s)
	}
	// The terminal answering, and the pointer moving: replies.
	io.WriteString(r.in, "\x1b[I\x1b[<35;3;4M") //nolint:errcheck
	attachWait(t, "a reply", func() bool { return len(texts()) == 2 })
	if s := texts()[1]; s.Text != "\x1b[I\x1b[<35;3;4M" || !s.Reply {
		t.Fatalf("reply %+v", s)
	}
	// Both at once go in order, each as what it is.
	io.WriteString(r.in, "a\x1b[Ob") //nolint:errcheck
	attachWait(t, "three more", func() bool { return len(texts()) == 5 })
	if got := texts()[2:]; got[0].Text != "a" || got[0].Reply || got[1].Text != "\x1b[O" || !got[1].Reply || got[2].Text != "b" || got[2].Reply {
		t.Fatalf("mixed %+v", got)
	}

	// A control paste from the app: the room it has, then a claim.
	io.WriteString(r.in, attachControlMark+`tok {"cols":120,"rows":40}`) //nolint:errcheck
	attachWait(t, "the size asked for", func() bool { return len(attachSent[proto.PaneResizeParams](srv, proto.MethodPaneResize)) == 1 })
	if rs := attachSent[proto.PaneResizeParams](srv, proto.MethodPaneResize); rs[0] != (proto.PaneResizeParams{ID: "p3", Cols: 120, Rows: 40}) {
		t.Fatalf("resize %+v", rs[0])
	}
	// The same room again (the app says it until the pane takes it): no
	// second request.
	io.WriteString(r.in, attachControlMark+`tok {"cols":120,"rows":40}`) //nolint:errcheck
	time.Sleep(30 * time.Millisecond)
	if rs := attachSent[proto.PaneResizeParams](srv, proto.MethodPaneResize); len(rs) != 1 {
		t.Fatalf("the same room asked for again: %+v", rs)
	}
	io.WriteString(r.in, "\x1b[200~"+attachControlMark+`tok {"claim":true}`+"\x1b[201~") //nolint:errcheck
	attachWait(t, "the claim", func() bool { return len(texts()) == 6 })
	if s := texts()[5]; s.Text != "" || s.Reply {
		t.Fatalf("claim %+v, want an empty typed text", s)
	}
	// Somebody else's token, or none: typed, as it came.
	io.WriteString(r.in, attachControlMark+`other {"claim":true}`) //nolint:errcheck
	attachWait(t, "a paste that is not ours", func() bool { return len(texts()) >= 7 })
	var rest strings.Builder
	for _, s := range texts()[6:] {
		rest.WriteString(s.Text)
	}
	if rest.String() != attachControlMark+`other {"claim":true}` {
		t.Fatalf("not ours went as %q", rest.String())
	}

	srv.push(t, proto.EventPaneExited, proto.PaneInfo{ID: "p3", ExitCode: 4})
	select {
	case msg := <-r.done:
		if msg != "pane p3 ended (exit 4)" {
			t.Fatalf("ended with %q", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not end with the pane")
	}
}

func TestAttachRawControlInPieces(t *testing.T) {
	srv, c := startRawServer(t, true)
	r := runRaw(t, srv, c, false)
	attachWait(t, "pane.raw", func() bool { return len(attachSent[proto.PaneRef](srv, proto.MethodPaneRaw)) == 1 })
	body := attachControlMark + `tok {"cols":90,"rows":20}`
	io.WriteString(r.in, body[:7]) //nolint:errcheck
	time.Sleep(5 * time.Millisecond)
	io.WriteString(r.in, body[7:]) //nolint:errcheck
	attachWait(t, "the size", func() bool { return len(attachSent[proto.PaneResizeParams](srv, proto.MethodPaneResize)) == 1 })
	if got := attachSent[proto.PaneSendTextParams](srv, proto.MethodPaneSendText); len(got) != 0 {
		t.Fatalf("a control in pieces reached the pane: %+v", got)
	}
	// Garbled but ours: swallowed, not typed.
	io.WriteString(r.in, "\x1b[200~"+attachControlMark+"tok {nope\x1b[201~x") //nolint:errcheck
	attachWait(t, "the x after it", func() bool { return len(attachSent[proto.PaneSendTextParams](srv, proto.MethodPaneSendText)) == 1 })
	if got := attachSent[proto.PaneSendTextParams](srv, proto.MethodPaneSendText); got[0].Text != "x" {
		t.Fatalf("got %+v", got)
	}
}

func TestAttachRawHoldsAnEndedPane(t *testing.T) {
	srv, c := startRawServer(t, true)
	r := runRaw(t, srv, c, true)
	attachWait(t, "the replay", func() bool { return strings.Contains(r.out.String(), "REPLAY-SCREEN") })
	srv.push(t, proto.EventPaneExited, proto.PaneInfo{ID: "p3", ExitCode: 1})
	attachWait(t, "the note", func() bool { return strings.Contains(r.out.String(), "pane p3 ended (exit 1) · any key closes") })
	srv.push(t, proto.EventPaneOutput, proto.PaneOutput{ID: "p3", Data: []byte("AFTER")})
	io.WriteString(r.in, "q") //nolint:errcheck
	select {
	case msg := <-r.done:
		if msg != "pane p3 ended (exit 1)" {
			t.Fatalf("ended with %q", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a key did not close the held view")
	}
	if strings.Contains(r.out.String(), "AFTER") {
		t.Fatal("output after the end was written over the note")
	}
}

func TestAttachRawAlreadyEnded(t *testing.T) {
	srv, c := startRawServer(t, true)
	srv.mu.Lock()
	srv.panes = []proto.PaneInfo{{ID: "p3", State: proto.PaneExited, ExitCode: 9}}
	srv.mu.Unlock()
	r := runRaw(t, srv, c, false)
	select {
	case msg := <-r.done:
		if msg != "pane p3 ended (exit 9)" {
			t.Fatalf("ended with %q", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not say the pane had ended")
	}
}

// Against a real server: the program's own bytes arrive, queries out, and
// typing reaches it.
func TestAttachRawRealServer(t *testing.T) {
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
		err = runNew([]string{"-cwd", dir, "--", "/bin/sh", "-c", `echo ready-$((6*7)); read x; printf '\033[cgot-%s-%s\n' "$x" $((1+1)); read y; exit 3`})
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
		msg, ok, err := attachRawLoop(c, id, inR, screen, func() (int, int, bool) { return 60, 10, true }, nil, nil, false)
		if !ok {
			msg = "not raw"
		}
		if err != nil {
			msg = err.Error()
		}
		done <- msg
	}()
	attachWait(t, "the pane's output", func() bool { return strings.Contains(screen.String(), "ready-42") })
	io.WriteString(inW, "abc\r") //nolint:errcheck
	attachWait(t, "the answer to typing", func() bool { return strings.Contains(screen.String(), "got-abc-2") })
	if strings.Contains(screen.String(), "\x1b[c") {
		t.Fatalf("a query conch answers reached this terminal: %q", screen.String())
	}
	io.WriteString(inW, "\r") //nolint:errcheck
	select {
	case msg := <-done:
		if msg != "pane "+id+" ended (exit 3)" {
			t.Fatalf("ended with %q", msg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("attach did not end with its pane")
	}
}

func TestAttachRawViewAsksForNothing(t *testing.T) {
	srv, c := startRawServer(t, true)
	t.Setenv(attachControlEnv, "tok")
	inR, inW := io.Pipe()
	defer inW.Close()
	out := &syncBuffer{}
	done := make(chan string, 1)
	go func() {
		msg, _, _ := attachRawLoopAs(c, "p3", inR, out, func() (int, int, bool) { return 30, 8, true }, nil, nil, false, true)
		done <- msg
	}()
	attachWait(t, "the replay", func() bool { return strings.Contains(out.String(), "REPLAY-SCREEN") })
	io.WriteString(inW, attachControlMark+`tok {"cols":20,"rows":5,"claim":true}`) //nolint:errcheck
	io.WriteString(inW, "y")                                                       //nolint:errcheck
	attachWait(t, "typing", func() bool { return len(attachSent[proto.PaneSendTextParams](srv, proto.MethodPaneSendText)) == 1 })
	if r := attachSent[proto.PaneResizeParams](srv, proto.MethodPaneResize); len(r) != 0 {
		t.Fatalf("a view asked for a size: %+v", r)
	}
	if s := attachSent[proto.PaneSendTextParams](srv, proto.MethodPaneSendText); s[0].Text != "y" {
		t.Fatalf("a view claimed the pane: %+v", s)
	}
	srv.push(t, proto.EventPaneClosed, proto.PaneInfo{ID: "p3"})
	if msg := <-done; msg != "pane p3 was closed" {
		t.Fatalf("ended with %q", msg)
	}
}

func TestAttachViewNeedsRaw(t *testing.T) {
	a4Env(t)
	if err := runAttach([]string{"-view", "p3"}); err == nil || !strings.Contains(err.Error(), "-view goes with -raw") {
		t.Fatalf("got %v", err)
	}
}

// Run in a terminal of somebody's own (no control token), it sizes the
// pane to that terminal, and follows it.
func TestAttachRawOnItsOwnTakesItsTerminalsSize(t *testing.T) {
	srv, c := startRawServer(t, true)
	t.Setenv(attachControlEnv, "")
	inR, inW := io.Pipe()
	defer inW.Close()
	out := &syncBuffer{}
	winch := make(chan os.Signal, 1)
	cols := 100
	var mu sync.Mutex
	go attachRawLoop(c, "p3", inR, out, func() (int, int, bool) { mu.Lock(); defer mu.Unlock(); return cols, 30, true }, winch, nil, false) //nolint:errcheck
	attachWait(t, "the first resize", func() bool { return len(attachSent[proto.PaneResizeParams](srv, proto.MethodPaneResize)) == 1 })
	if rs := attachSent[proto.PaneResizeParams](srv, proto.MethodPaneResize); rs[0] != (proto.PaneResizeParams{ID: "p3", Cols: 100, Rows: 30}) {
		t.Fatalf("resize %+v", rs[0])
	}
	mu.Lock()
	cols = 90
	mu.Unlock()
	winch <- os.Interrupt
	attachWait(t, "the next", func() bool { return len(attachSent[proto.PaneResizeParams](srv, proto.MethodPaneResize)) == 2 })
	if rs := attachSent[proto.PaneResizeParams](srv, proto.MethodPaneResize); rs[1].Cols != 90 {
		t.Fatalf("resize %+v", rs[1])
	}
}

// Run by the app, the replay waits for the terminal to be the pane's size:
// written into a smaller one it scrolled away and the pane showed blank.
func TestAttachRawReplayWaitsForThePanesSize(t *testing.T) {
	srv, c := startRawServer(t, true) // the replay is for 80x24
	t.Setenv(attachControlEnv, "tok")
	inR, inW := io.Pipe()
	defer inW.Close()
	out := &syncBuffer{}
	winch := make(chan os.Signal, 1)
	var mu sync.Mutex
	cols, rows := 73, 19 // the size the view was made at
	size := func() (int, int, bool) { mu.Lock(); defer mu.Unlock(); return cols, rows, true }
	go attachRawLoop(c, "p3", inR, out, size, winch, nil, false) //nolint:errcheck
	attachWait(t, "pane.raw", func() bool { return len(attachSent[proto.PaneRef](srv, proto.MethodPaneRaw)) == 1 })
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(out.String(), "REPLAY-SCREEN") {
		t.Fatal("the replay was written into a terminal of another size")
	}
	mu.Lock()
	cols, rows = 80, 24 // the app sized it to the pane
	mu.Unlock()
	winch <- os.Interrupt
	attachWait(t, "the replay", func() bool { return strings.Contains(out.String(), "REPLAY-SCREEN") })
}

// A terminal that never comes to the pane's size still gets the replay.
func TestAttachRawReplayDoesNotWaitForever(t *testing.T) {
	srv, c := startRawServer(t, true)
	r := runRaw(t, srv, c, false) // 100x30 against an 80x24 replay
	attachWait(t, "the replay", func() bool { return strings.Contains(r.out.String(), "REPLAY-SCREEN") })
}

// rawHeld starts a raw attach run by the app whose terminal is 80x24,
// the size of the first replay, so that one is drawn at once; the test
// changes the size and signals winch as the app's terminal would.
type rawHeld struct {
	srv   *attachServer
	in    *io.PipeWriter
	out   *syncBuffer
	winch chan os.Signal
	mu    sync.Mutex
	cols  int
	rows  int
	done  chan string
}

func startRawHeld(t *testing.T, wait time.Duration, view bool) *rawHeld {
	t.Helper()
	srv, c := startRawServer(t, true)
	t.Setenv(attachControlEnv, "tok")
	old := rawResettle
	rawResettle = wait
	t.Cleanup(func() { rawResettle = old })
	inR, inW := io.Pipe()
	t.Cleanup(func() { inW.Close() })
	h := &rawHeld{srv: srv, in: inW, out: &syncBuffer{}, winch: make(chan os.Signal, 1), cols: 80, rows: 24, done: make(chan string, 1)}
	size := func() (int, int, bool) { h.mu.Lock(); defer h.mu.Unlock(); return h.cols, h.rows, true }
	go func() {
		msg, _, _ := attachRawLoopAs(c, "p3", inR, h.out, size, h.winch, nil, false, view)
		h.done <- msg
	}()
	attachWait(t, "the replay", func() bool { return strings.Contains(h.out.String(), "REPLAY-SCREEN") })
	return h
}

func (h *rawHeld) output(t *testing.T, o proto.PaneOutput) {
	t.Helper()
	o.ID = "p3"
	h.srv.push(t, proto.EventPaneOutput, o)
}

func (h *rawHeld) resize(cols, rows int) {
	h.mu.Lock()
	h.cols, h.rows = cols, rows
	h.mu.Unlock()
	h.winch <- os.Interrupt
}

// A fresh replay mid-stream — the pane changed size — is held until the
// window follows the pane, as the first one waits: drawn into the old
// size, the window would reflow it in turn. What arrives meanwhile is
// kept, drawn after it and in order, and typing still goes through.
func TestAttachRawResetIsHeldForThePanesNewSize(t *testing.T) {
	h := startRawHeld(t, time.Minute, false) // only the size releases it
	h.output(t, proto.PaneOutput{Data: []byte("WIDER-SCREEN"), Reset: true, Cols: 120, Rows: 30})
	for i := range 3 {
		h.output(t, proto.PaneOutput{Data: []byte("after-" + string(rune('a'+i)))})
	}
	// Typing during the hold: the loop is still reading.
	h.in.Write([]byte("x")) //nolint:errcheck
	attachWait(t, "the typing", func() bool { return len(attachSent[proto.PaneSendTextParams](h.srv, proto.MethodPaneSendText)) == 1 })
	if got := h.out.String(); strings.Contains(got, "WIDER-SCREEN") || strings.Contains(got, "after-") {
		t.Fatalf("drawn before the window was the pane's new size: %q", got)
	}

	h.resize(120, 30)
	attachWait(t, "the held replay", func() bool { return strings.Contains(h.out.String(), "after-c") })
	got := h.out.String()
	at := strings.Index(got, "WIDER-SCREEN")
	if strings.LastIndex(got, rawClear) > at {
		t.Fatalf("the screen was not cleared before the fresh replay: %q", got)
	}
	if !(at < strings.Index(got, "after-a") && strings.Index(got, "after-a") < strings.Index(got, "after-b") && strings.Index(got, "after-b") < strings.Index(got, "after-c")) {
		t.Fatalf("out of order: %q", got)
	}

	// Once released, output goes straight through again.
	h.output(t, proto.PaneOutput{Data: []byte("LIVE")})
	attachWait(t, "live output", func() bool { return strings.Contains(h.out.String(), "LIVE") })
}

// A replay arriving while one is held replaces it; and a window that never
// gets to the size is drawn anyway when the wait is over.
func TestAttachRawHeldReplayGivesUp(t *testing.T) {
	h := startRawHeld(t, time.Second, false)
	h.output(t, proto.PaneOutput{Data: []byte("FIRST-REPLAY"), Reset: true, Cols: 300, Rows: 90})
	h.output(t, proto.PaneOutput{Data: []byte("tail-of-first")})
	h.output(t, proto.PaneOutput{Data: []byte("NEVER-FITS"), Reset: true, Cols: 310, Rows: 95})
	attachWait(t, "the replay for a size never reached", func() bool { return strings.Contains(h.out.String(), "NEVER-FITS") })
	if got := h.out.String(); strings.Contains(got, "FIRST-REPLAY") || strings.Contains(got, "tail-of-first") {
		t.Fatalf("a replayed-over replay was drawn: %q", got)
	}
}

// The pane ending while a replay is held draws it first: what the program
// printed last comes before why the view ended.
func TestAttachRawHeldReplayDrawnAtExit(t *testing.T) {
	h := startRawHeld(t, time.Minute, false)
	h.output(t, proto.PaneOutput{Data: []byte("LAST-WORDS"), Reset: true, Cols: 120, Rows: 30})
	h.srv.push(t, proto.EventPaneExited, proto.PaneInfo{ID: "p3", ExitCode: 3})
	select {
	case why := <-h.done:
		if !strings.Contains(why, "exit 3") {
			t.Fatalf("ended with %q", why)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the view did not end")
	}
	if !strings.Contains(h.out.String(), "LAST-WORDS") {
		t.Fatalf("the held replay was lost at exit: %q", h.out.String())
	}
}

// ⌘K's preview (-view) keeps the size it was made at, so it draws a fresh
// replay at once rather than waiting for a size it will never be.
func TestAttachRawViewDoesNotHold(t *testing.T) {
	h := startRawHeld(t, time.Minute, true)
	h.output(t, proto.PaneOutput{Data: []byte("PREVIEW-REPLAY"), Reset: true, Cols: 120, Rows: 30})
	attachWait(t, "the preview's replay", func() bool { return strings.Contains(h.out.String(), "PREVIEW-REPLAY") })
}
