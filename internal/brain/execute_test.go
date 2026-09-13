package brain_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/brain"
	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

func TestExecuteSendAndClose(t *testing.T) {
	dir, err := os.MkdirTemp("", "cb") // short: unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	srv := server.New(filepath.Join(dir, "s.sock"), dir)
	go srv.Run()
	t.Cleanup(func() { srv.Stop(); time.Sleep(100 * time.Millisecond); os.RemoveAll(dir) })
	var c *client.Client
	for i := 0; i < 100; i++ {
		if c, err = client.Dial(filepath.Join(dir, "s.sock"), "test"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/cat"}, Cwd: dir}, &info); err != nil {
		t.Fatal(err)
	}
	var list proto.PaneList
	c.Call(ctx, proto.MethodPaneList, nil, &list)
	w := brain.World{Machines: []brain.Machine{brain.MachineFrom("local", "local", true, nil, nil, list.Panes, nil)}}

	send := brain.Action{Type: brain.ActSend, Machine: "local", Pane: info.ID, Text: "hello brain"}
	if err := w.Validate(&send); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.Execute(ctx, c, send, 80, 24); err != nil {
		t.Fatal(err)
	}
	for {
		var screen proto.PaneReadResult
		c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &screen)
		if strings.Count(strings.Join(screen.Lines, "\n"), "hello brain") >= 2 { // typed, then echoed by cat
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("text never arrived: %q", screen.Lines)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := brain.Execute(ctx, c, brain.Action{Type: brain.ActClose, Machine: "local", Pane: info.ID}, 0, 0); err != nil {
		t.Fatal(err)
	}
	c.Call(ctx, proto.MethodPaneList, nil, &list)
	for _, p := range list.Panes {
		if p.ID == info.ID {
			t.Fatal("pane still open")
		}
	}
}
