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
	defer func(was bool) { repanic = was }(repanic)
	repanic = false
	recovering(c, proto.Message{ID: "7", Method: proto.MethodFSWriteRange}, func() { panic("makeslice: len out of range") })
	select {
	case m := <-got:
		if m.ID != "7" || m.Error == nil || m.Error.Code != proto.ErrInternal || !strings.Contains(m.Error.Message, "makeslice") {
			t.Fatalf("reply: %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no reply")
	}

	// Nothing panicked: nothing extra is sent.
	recovering(c, proto.Message{ID: "8"}, func() {})

	repanic = true
	defer func() {
		if recover() == nil {
			t.Fatal("under test the panic was swallowed")
		}
	}()
	recovering(c, proto.Message{ID: "9"}, func() { panic("bug") })
}
