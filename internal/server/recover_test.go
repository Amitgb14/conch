package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// A request run aside that panics answers with an error rather than
// ending the server with every pane in it; under test it panics again, so
// the bug is met rather than hidden.
func TestAsidePanicIsAnError(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := &client{conn: proto.NewConn(a)}
	got := make(chan proto.Message, 1)
	go func() {
		m, err := proto.NewConn(b).Read()
		if err == nil {
			got <- m
		}
	}()
	defer func(was bool) { repanic.Store(was) }(repanic.Load())
	repanic.Store(false)
	recovering(c, proto.Message{ID: "7", Method: proto.MethodFSWriteRange}, func() { panic("makeslice: len out of range") })
	select {
	case m := <-got:
		if m.ID != "7" || m.Error == nil || m.Error.Code != proto.ErrInternal || !strings.Contains(m.Error.Message, "makeslice") {
			t.Fatalf("reply: %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no reply")
	}

	// Nothing panicked, or a notification panicked: nothing is sent. The
	// next reply read is the one for "10".
	recovering(c, proto.Message{ID: "8"}, func() {})
	recovering(c, proto.Message{Method: proto.MethodPaneSendText}, func() { panic("bug") })
	go func() {
		m, err := proto.NewConn(b).Read()
		if err == nil {
			got <- m
		}
	}()
	recovering(c, proto.Message{ID: "10"}, func() { panic("bug") })
	select {
	case m := <-got:
		if m.ID != "10" {
			t.Fatalf("read reply %q, want only the one for 10", m.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no reply")
	}

	repanic.Store(true)
	defer func() {
		if recover() == nil {
			t.Fatal("under test the panic was swallowed")
		}
	}()
	recovering(c, proto.Message{ID: "9"}, func() { panic("bug") })
}
