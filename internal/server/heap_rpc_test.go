package server_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// server.heap over the socket: announced, and answered with files under
// the server's own folder.
func TestServerHeapMethod(t *testing.T) {
	c, dir := startServer(t)
	if len(c.MissingCapabilities([]string{proto.CapServerHeap})) != 0 {
		t.Fatal("server.heap.v1 not announced")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var h proto.ServerHeap
	if err := c.Call(ctx, proto.MethodServerHeap, nil, &h); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h.Heap, dir+"/debug/heap-") || !strings.HasPrefix(h.Goroutines, dir+"/debug/goroutines-") || h.HeapAlloc == 0 {
		t.Fatalf("%+v", h)
	}
	for _, f := range []string{h.Heap, h.Goroutines} {
		if _, err := os.Stat(f); err != nil {
			t.Error(err)
		}
	}
}
