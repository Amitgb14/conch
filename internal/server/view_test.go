package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// view.show names panes the server has, refuses one it does not, drops a
// pane asked for twice — one pane twice is one split — and reaches every
// client as an event, since the layout belongs to whoever is drawing it.
func TestViewShowChecksItsPanesAndIsBroadcast(t *testing.T) {
	c, dir := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	events := a5Collect(c)

	var perr error
	if perr = c.Call(ctx, proto.MethodViewShow, proto.ViewShowParams{}, nil); perr == nil {
		t.Fatal("showing nothing was allowed")
	}
	if perr = c.Call(ctx, proto.MethodViewShow, proto.ViewShowParams{Panes: []string{"p404"}}, nil); perr == nil {
		t.Fatal("a pane that is not there was passed on")
	}

	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Name: "one", Command: []string{"/bin/sh", "-c", "read x"}, Cwd: dir, Cols: 20, Rows: 5, NoProject: true,
	}, &info); err != nil {
		t.Fatal(err)
	}
	var res proto.ViewShowResult
	if err := c.Call(ctx, proto.MethodViewShow,
		proto.ViewShowParams{Panes: []string{info.ID, info.ID}, Name: "those two", Why: "claude"}, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Panes) != 1 || res.Panes[0] != info.ID {
		t.Fatalf("asked twice for one pane: %v", res.Panes)
	}
	events.wait(t, "view.show", func(m proto.Message) bool { return m.Event == proto.EventViewShow })

	// The layout a client publishes is what view.get answers with, and
	// nothing is invented before one does.
	var got proto.ViewState
	if err := c.Call(ctx, proto.MethodViewGet, nil, &got); err != nil || len(got.Tabs) != 0 {
		t.Fatalf("before any TUI: %+v %v", got, err)
	}
	state := proto.ViewState{Client: "tui", Focus: info.ID,
		Tabs: []proto.ViewTab{{Name: "api", Panes: []string{info.ID}, Active: true}}}
	c.Notify(proto.MethodViewState, state)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := c.Call(ctx, proto.MethodViewGet, nil, &got); err != nil {
			t.Fatal(err)
		}
		if got.Focus == info.ID && len(got.Tabs) == 1 && got.Tabs[0].Name == "api" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("view.get never answered with what was published: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.At.IsZero() {
		t.Fatal("the server did not stamp when it was published")
	}
}
