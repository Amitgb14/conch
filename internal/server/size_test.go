package server_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// paneSize is a pane's size as pane.list reports it.
func paneSize(t *testing.T, c *client.Client, id string) [2]int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var list proto.PaneList
	if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil {
		t.Fatal(err)
	}
	for _, p := range list.Panes {
		if p.ID == id {
			return [2]int{p.Cols, p.Rows}
		}
	}
	t.Fatalf("no pane %s", id)
	return [2]int{}
}

func callOK(t *testing.T, c *client.Client, method string, params any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Call(ctx, method, params, nil); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

// Two clients showing one pane: the size is that of the one last typed
// into, not of whichever redrew last. Before anyone types, the last resize
// wins, as it always did.
func TestPaneTakesTheSizeOfTheClientTypedInto(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	tui, dir := startServer(t)
	window, err := client.Dial(filepath.Join(dir, "s.sock"), "window")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { window.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var info proto.PaneInfo
	if err := tui.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/cat"}, Cols: 80, Rows: 24}, &info); err != nil {
		t.Fatal(err)
	}
	id := info.ID
	resize := func(c *client.Client, cols, rows int) {
		callOK(t, c, proto.MethodPaneResize, proto.PaneResizeParams{ID: id, Cols: cols, Rows: rows})
	}
	want := func(cols, rows int, why string) {
		t.Helper()
		if got := paneSize(t, tui, id); got != [2]int{cols, rows} {
			t.Fatalf("%s: %dx%d, want %dx%d", why, got[0], got[1], cols, rows)
		}
	}

	resize(tui, 200, 50)
	resize(window, 100, 30)
	want(100, 30, "nobody has typed: the last resize wins")

	callOK(t, tui, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: id, Text: "x"})
	want(200, 50, "typed into the TUI: its size")

	resize(window, 90, 28) // the window redraws; it does not have the pane's attention
	want(200, 50, "a resize from the other client waits")

	callOK(t, window, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: id, Keys: []string{"enter"}})
	want(90, 28, "typed into the window: the size it asked for")

	resize(window, 95, 29) // its own resizes apply while it has the pane
	want(95, 29, "the window's own resize")

	// A pointer moving over the TUI is not typing.
	callOK(t, tui, proto.MethodPaneSendMouse, proto.PaneSendMouseParams{ID: id, X: 1, Y: 1, Button: "none", Action: proto.MouseMotion})
	want(95, 29, "a mouse motion does not take the pane")

	// The window goes: its claim goes with it, and the last resize wins again.
	window.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resize(tui, 120, 40)
		if paneSize(t, tui, id) == [2]int{120, 40} {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a client that left still holds the pane's size")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A client that typed but never asked for a size changes nothing.
	callOK(t, tui, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: id, Text: "y"})
	want(120, 40, "typed, with its own size already applied")
}

func TestResizeRefusesNonsense(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	c, _ := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/cat"}, Cols: 80, Rows: 24}, &info); err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{0, 10}, {10, 0}, {-1, 5}} {
		if err := c.Call(ctx, proto.MethodPaneResize, proto.PaneResizeParams{ID: info.ID, Cols: size[0], Rows: size[1]}, nil); err == nil {
			t.Errorf("%v accepted", size)
		}
	}
	if got := paneSize(t, c, info.ID); got != [2]int{80, 24} {
		t.Fatalf("size %v after refused resizes", got)
	}
}
