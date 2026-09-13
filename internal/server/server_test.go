package server_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amitghadge/conch/internal/client"
	"github.com/amitghadge/conch/internal/proto"
	"github.com/amitghadge/conch/internal/server"
)

func TestServerLifecycle(t *testing.T) {
	// Keep the socket path short: unix socket paths are capped near 104 bytes.
	dir, err := os.MkdirTemp("", "conch")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")

	srv := server.New(sock, dir)
	ran := make(chan error, 1)
	go func() { ran <- srv.Run() }()

	var c *client.Client
	for i := 0; i < 100; i++ {
		if c, err = client.Dial(sock, "test"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Server.Protocol != proto.ProtocolVersion {
		t.Fatalf("protocol %d", c.Server.Protocol)
	}

	if err := server.New(sock, dir).Run(); err != server.ErrAlreadyRunning {
		t.Fatalf("second server: got %v, want ErrAlreadyRunning", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var info proto.PaneInfo
	err = c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", "echo ready-$CONCH_PANE_ID; read x; echo got-$x; exit 7"},
		Cols:    40, Rows: 5,
	}, &info)
	if err != nil {
		t.Fatal(err)
	}

	c.Notify(proto.MethodPaneSubscribe, proto.PaneRef{ID: info.ID})
	waitEvent(t, c, func(m proto.Message) bool {
		var f proto.Frame
		return m.Event == proto.EventPaneFrame && json.Unmarshal(m.Data, &f) == nil &&
			strings.Contains(strings.Join(f.Lines, "\n"), "ready-"+info.ID)
	})

	if err := c.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: "abc"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: info.ID, Keys: []string{"enter"}}, nil); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, c, func(m proto.Message) bool {
		var p proto.PaneInfo
		return m.Event == proto.EventPaneExited && json.Unmarshal(m.Data, &p) == nil && p.ExitCode == 7
	})

	var read proto.PaneReadResult
	if err := c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &read); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(read.Lines, "\n"), "got-abc") {
		t.Fatalf("screen missing got-abc: %q", read.Lines)
	}

	var perr *proto.Error
	err = c.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: "nope", Text: "x"}, nil)
	if !asProtoErr(err, &perr) || perr.Code != proto.ErrNotFound {
		t.Fatalf("unknown pane: got %v", err)
	}

	if err := c.Call(ctx, proto.MethodPaneClose, proto.PaneRef{ID: info.ID}, nil); err != nil {
		t.Fatal(err)
	}
	var list proto.PaneList
	if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil || len(list.Panes) != 0 {
		t.Fatalf("list after close: %v %v", list.Panes, err)
	}

	if err := c.Call(ctx, proto.MethodServerStop, nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ran:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket not removed: %v", err)
	}
}

func waitEvent(t *testing.T, c *client.Client, match func(proto.Message) bool) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-c.Events:
			if !ok {
				t.Fatalf("connection closed: %v", c.Err())
			}
			if match(m) {
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for event")
		}
	}
}

func asProtoErr(err error, target **proto.Error) bool {
	pe, ok := err.(*proto.Error)
	if ok {
		*target = pe
	}
	return ok
}
