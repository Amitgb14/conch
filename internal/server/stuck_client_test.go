package server_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// A client that connects, says hello and never reads again — a TUI
// suspended with ctrl+z, an ssh bridge whose link stalled — must not hold
// up anyone else. Its socket buffer fills in a few KB of events; writes
// to it used to block every broadcast, and with them every other
// client's calls: a healthy client's pane.create timed out by the 21st
// pane.
func TestAStuckClientHoldsUpNobody(t *testing.T) {
	c, dir := startServer(t)
	stuck, err := net.Dial("unix", filepath.Join(dir, "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer stuck.Close()
	if err := proto.NewConn(stuck).Write(proto.Message{ID: "1", Method: proto.MethodHello, Params: proto.Marshal(proto.HelloParams{
		Client: "stuck", Version: proto.Version, Protocol: proto.ProtocolVersion, Capabilities: proto.Capabilities,
	})}); err != nil {
		t.Fatal(err)
	}
	// It never reads: not even the hello's answer.

	// Each pane prints and exits, so each is broadcast as added, updated
	// and exited: plenty to fill a socket nobody drains.
	for i := 0; i < 60; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
			Command: []string{"/bin/sh", "-c", "printf 'pane output to broadcast about\\n'"}, Cols: 80, Rows: 24}, nil)
		cancel()
		if err != nil {
			t.Fatalf("pane %d, beside a client that never reads: %v", i+1, err)
		}
	}
	// And the healthy client still hears about them.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var pl proto.PaneList
	if err := c.Call(ctx, proto.MethodPaneList, nil, &pl); err != nil {
		t.Fatalf("pane.list: %v", err)
	}
}
