package server_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// rawText is everything raw output events for a pane have carried so far.
func rawText(col *a5Collector, id string) string {
	col.mu.Lock()
	defer col.mu.Unlock()
	var b strings.Builder
	for _, m := range col.msgs {
		var o proto.PaneOutput
		if m.Event == proto.EventPaneOutput && json.Unmarshal(m.Data, &o) == nil && o.ID == id {
			b.Write(o.Data)
		}
	}
	return b.String()
}

func waitRaw(t *testing.T, col *a5Collector, id, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := rawText(col, id); strings.Contains(got, want) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("raw output never held %q; got %q", want, rawText(col, id))
	return ""
}

func TestPaneRawReplaysThenStreams(t *testing.T) {
	a5Env(t)
	c, _ := startServer(t)
	col := a5Collect(c)
	ctx := a5Ctx(t)
	if !hasCap(c, proto.CapPaneRaw) {
		t.Fatal("the server does not offer pane.raw.v1")
	}
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		// The query is answered by conch's emulator into the program's
		// input, which `read` takes; it must not reach the raw client.
		Command: []string{"/bin/sh", "-c", `echo first-$((1+1)); read x; printf '\033[6nsecond-%s\n' "$x"; read y; echo third-$((2+2)); sleep 30`},
		Cols:    40, Rows: 6,
	}, &info); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var read proto.PaneReadResult
	for time.Now().Before(deadline) {
		_ = c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &read)
		if strings.Contains(strings.Join(read.Lines, "\n"), "first-2") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	var start proto.PaneRawStart
	if err := c.Call(ctx, proto.MethodPaneRaw, proto.PaneRef{ID: info.ID}, &start); err != nil {
		t.Fatal(err)
	}
	if start.ID != info.ID || start.Cols != 40 || start.Rows != 6 || !strings.Contains(string(start.Replay), "first-2") {
		t.Fatalf("start = %+v (%q)", start, start.Replay)
	}
	if err := c.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "abc\n"}, nil); err != nil {
		t.Fatal(err)
	}
	got := waitRaw(t, col, info.ID, "second-abc")
	if strings.Contains(got, "\x1b[6n") {
		t.Fatalf("the cursor position query reached the raw client: %q", got)
	}
	if strings.Contains(got, "first-2") {
		t.Fatalf("output from before the replay was streamed: %q", got)
	}

	// A second subscription on the same connection is refused.
	a5Code(t, c.Call(ctx, proto.MethodPaneRaw, proto.PaneRef{ID: info.ID}, nil), proto.ErrBadRequest)
	if err := c.Call(ctx, proto.MethodPaneSubscribe, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatalf("frames on a raw subscription: %v, want a no-op", err) // as a second frame subscription is
	}
	time.Sleep(50 * time.Millisecond)
	col.mu.Lock()
	for _, m := range col.msgs {
		if m.Event == proto.EventPaneFrame {
			t.Error("frames were sent to a raw subscription")
			break
		}
	}
	col.mu.Unlock()
	a5Code(t, c.Call(ctx, proto.MethodPaneRaw, proto.PaneRef{ID: "p999"}, nil), proto.ErrNotFound)

	// Unsubscribed, nothing more comes.
	if err := c.Call(ctx, proto.MethodPaneUnsubscribe, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "\n"}, nil); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		_ = c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &read)
		if strings.Contains(strings.Join(read.Lines, "\n"), "third-4") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(rawText(col, info.ID), "third-4") {
		t.Fatal("raw output kept coming after pane.unsubscribe")
	}
}

func TestPaneRawSendsLastOutputBeforeExit(t *testing.T) {
	a5Env(t)
	c, _ := startServer(t)
	col := a5Collect(c)
	ctx := a5Ctx(t)
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", `read x; echo bye-$((5+5))`},
		Cols:    40, Rows: 6,
	}, &info); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodPaneRaw, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "\n"}, nil); err != nil {
		t.Fatal(err)
	}
	waitRaw(t, col, info.ID, "bye-10")
	col.wait(t, "the exit", func(m proto.Message) bool { return m.Event == proto.EventPaneExited })
}

// hasCap reports whether the server said it can do cap.
func hasCap(c *client.Client, cap string) bool {
	for _, x := range c.Server.Capabilities {
		if x == cap {
			return true
		}
	}
	return false
}

func rawPaneSize(t *testing.T, c *client.Client, id string) (int, int) {
	t.Helper()
	var list proto.PaneList
	if err := c.Call(a5Ctx(t), proto.MethodPaneList, nil, &list); err != nil {
		t.Fatal(err)
	}
	for _, p := range list.Panes {
		if p.ID == id {
			return p.Cols, p.Rows
		}
	}
	t.Fatalf("no pane %s", id)
	return 0, 0
}

func TestReplyTakesNoSize(t *testing.T) {
	a5Env(t)
	c1, dir := startServer(t)
	col := a5Collect(c1)
	ctx := a5Ctx(t)
	c2, err := client.Dial(dir+"/s.sock", "test2")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	a5Collect(c2)
	var info proto.PaneInfo
	if err := c1.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", "cat >/dev/null"}, Cols: 40, Rows: 6}, &info); err != nil {
		t.Fatal(err)
	}
	// c1 asks for 50×10 and types: the pane is c1's.
	_ = c1.Call(ctx, proto.MethodPaneResize, proto.PaneResizeParams{ID: info.ID, Cols: 50, Rows: 10}, nil)
	_ = c1.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "x"}, nil)
	// c2 asks for 70×12: kept, not applied.
	_ = c2.Call(ctx, proto.MethodPaneResize, proto.PaneResizeParams{ID: info.ID, Cols: 70, Rows: 12}, nil)
	if cols, rows := rawPaneSize(t, c1, info.ID); cols != 50 || rows != 10 {
		t.Fatalf("size %dx%d, want c1's 50x10", cols, rows)
	}
	// A reply through c2 — its terminal answering — leaves it c1's.
	if err := c2.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "\x1b[I", Reply: true}, nil); err != nil {
		t.Fatal(err)
	}
	if cols, rows := rawPaneSize(t, c1, info.ID); cols != 50 || rows != 10 {
		t.Fatalf("a reply took the size: %dx%d", cols, rows)
	}
	// Typing through c2 takes it, and everybody hears the new size.
	col.reset()
	if err := c2.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "y"}, nil); err != nil {
		t.Fatal(err)
	}
	if cols, rows := rawPaneSize(t, c1, info.ID); cols != 70 || rows != 12 {
		t.Fatalf("typing did not take the size: %dx%d", cols, rows)
	}
	col.wait(t, "pane.updated with the new size", func(m proto.Message) bool {
		var p proto.PaneInfo
		return m.Event == proto.EventPaneUpdated && json.Unmarshal(m.Data, &p) == nil && p.ID == info.ID && p.Cols == 70 && p.Rows == 12
	})
}

func TestResizeIsBroadcast(t *testing.T) {
	a5Env(t)
	c, _ := startServer(t)
	col := a5Collect(c)
	ctx := a5Ctx(t)
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", "sleep 30"}, Cols: 40, Rows: 6}, &info); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodPaneResize, proto.PaneResizeParams{ID: info.ID, Cols: 61, Rows: 9}, nil); err != nil {
		t.Fatal(err)
	}
	col.wait(t, "pane.updated with 61x9", func(m proto.Message) bool {
		var p proto.PaneInfo
		return m.Event == proto.EventPaneUpdated && json.Unmarshal(m.Data, &p) == nil && p.ID == info.ID && p.Cols == 61 && p.Rows == 9
	})
}

// When the pane changes size, a raw client is sent a fresh replay at the
// new size, marked Reset: its terminal reflowed the screen when it changed
// width, and a program repainting by counting rows left leftovers there.
func TestPaneRawResetsOnResize(t *testing.T) {
	a5Env(t)
	c, _ := startServer(t)
	col := a5Collect(c)
	ctx := a5Ctx(t)
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", `echo mark-$((3+4)); sleep 30`},
		Cols:    40, Rows: 6,
	}, &info); err != nil {
		t.Fatal(err)
	}
	var read proto.PaneReadResult
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		_ = c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &read)
		if strings.Contains(strings.Join(read.Lines, "\n"), "mark-7") {
			break
		}
	}
	var start proto.PaneRawStart
	if err := c.Call(ctx, proto.MethodPaneRaw, proto.PaneRef{ID: info.ID}, &start); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodPaneResize, proto.PaneResizeParams{ID: info.ID, Cols: 52, Rows: 9}, nil); err != nil {
		t.Fatal(err)
	}
	var reset *proto.PaneOutput
	for deadline := time.Now().Add(10 * time.Second); reset == nil && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		col.mu.Lock()
		for _, m := range col.msgs {
			var o proto.PaneOutput
			if m.Event == proto.EventPaneOutput && json.Unmarshal(m.Data, &o) == nil && o.ID == info.ID && o.Reset {
				reset = &o
			}
		}
		col.mu.Unlock()
	}
	if reset == nil {
		t.Fatal("no fresh replay after the pane changed size")
	}
	if reset.Cols != 52 || reset.Rows != 9 || !strings.Contains(string(reset.Data), "mark-7") {
		t.Fatalf("reset for %dx%d holding %q", reset.Cols, reset.Rows, reset.Data)
	}
}
