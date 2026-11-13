package server

import (
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// pipeClient is a client with an outbox, its other end returned for the
// test to read from — or not.
func pipeClient(t *testing.T) (*client, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	c := &client{conn: proto.NewConn(a), subs: map[string]*subscription{}}
	c.startOutbox(a)
	t.Cleanup(func() { c.close(); b.Close() })
	return c, b
}

func shortLimits(t *testing.T, max int, wait time.Duration) {
	t.Helper()
	oldMax, oldWait := outboxMax, writeWait
	outboxMax, writeWait = max, wait
	t.Cleanup(func() { outboxMax, writeWait = oldMax, oldWait })
}

// Sending to a client that has stopped reading returns at once, however
// much is sent; it is cut off once too much waits, and after that sends
// say it is gone.
func TestOutboxNeverWaitsOnAStuckClient(t *testing.T) {
	shortLimits(t, 64<<10, time.Hour)
	c, _ := pipeClient(t) // nobody reads the other end
	big := proto.Message{Event: "e", Data: json.RawMessage(`"` + strings.Repeat("x", 8<<10) + `"`)}
	start := time.Now()
	sent := 0
	for i := 0; i < 100; i++ {
		if c.send(big) {
			sent++
		}
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("sending to a stuck client took %s", d)
	}
	if sent == 0 || sent == 100 {
		t.Fatalf("sent %d of 100: want some queued, then a cut", sent)
	}
	if c.send(proto.Message{Event: "after"}) {
		t.Fatal("a client cut off still takes messages")
	}
}

// One write that does not finish in time cuts the client off, so a
// stream waiting on it (sendWait) is let go rather than held for good.
func TestOutboxWriteDeadline(t *testing.T) {
	shortLimits(t, 32<<20, 100*time.Millisecond)
	c, _ := pipeClient(t)
	done := make(chan bool, 1)
	go func() { done <- c.sendWait(proto.Message{Event: "frame"}) }()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("a write nobody read reported written")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sendWait held for good by a client that never reads")
	}
}

// A reply far bigger than one deadline's worth over a slow link — a whole
// file over an ssh bridge — still arrives while the client keeps reading:
// the deadline is for a write making no progress, not for the message.
func TestOutboxSlowReaderGetsABigReply(t *testing.T) {
	shortLimits(t, 32<<20, 200*time.Millisecond)
	c, other := pipeClient(t)
	body := strings.Repeat("x", 1<<20)
	go c.send(proto.Message{ID: "1", Result: json.RawMessage(`"` + body + `"`)})
	got := make([]byte, 0, len(body)+64)
	buf := make([]byte, 16<<10)
	start := time.Now()
	for !strings.HasSuffix(string(got), "\n") {
		other.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := other.Read(buf)
		if err != nil {
			t.Fatalf("after %d bytes in %s: %v", len(got), time.Since(start), err)
		}
		got = append(got, buf[:n]...)
		time.Sleep(20 * time.Millisecond) // a slow link: 16 KB every 20ms
	}
	if time.Since(start) < 4*200*time.Millisecond {
		t.Fatalf("read in %s: not slower than one deadline, so nothing was shown", time.Since(start))
	}
	var m proto.Message
	if err := json.Unmarshal(got, &m); err != nil || m.ID != "1" || len(m.Result) != len(body)+2 {
		t.Fatalf("the reply: %v (%d bytes)", err, len(m.Result))
	}
	if !c.send(proto.Message{Event: "after"}) {
		t.Fatal("cut off while reading")
	}
}

// A client that reads gets everything, in the order it was sent, and
// sendWait returns once its message is through; flush waits for what was
// queued before it.
func TestOutboxOrderAndFlush(t *testing.T) {
	c, other := pipeClient(t)
	rc := proto.NewConn(other)
	var mu sync.Mutex
	var got []string
	go func() {
		for {
			m, err := rc.Read()
			if err != nil {
				return
			}
			mu.Lock()
			got = append(got, m.Event)
			mu.Unlock()
		}
	}()
	for i := 0; i < 50; i++ {
		c.send(proto.Message{Event: "e" + string(rune('A'+i%26))})
	}
	if !c.sendWait(proto.Message{Event: "last"}) {
		t.Fatal("sendWait to a reader failed")
	}
	c.send(proto.Message{Event: "after"})
	c.flush(5 * time.Second)
	// Written is read on the other end; the reader records it a moment
	// later.
	a5WaitFor(t, "every message read", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= 52 })
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 52 || got[0] != "eA" || got[50] != "last" || got[51] != "after" {
		t.Fatalf("got %d: %v", len(got), got)
	}
}

// Flushing a client that is stuck gives up after its time rather than
// holding a stop or a reload for good.
func TestOutboxFlushGivesUp(t *testing.T) {
	c, _ := pipeClient(t)
	c.send(proto.Message{Event: "never read"})
	start := time.Now()
	c.flush(100 * time.Millisecond)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("flush took %s", d)
	}
}

// A client made without an outbox — as many tests make one — writes in
// place, as before.
func TestClientWithoutOutboxWritesInPlace(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	c := &client{conn: proto.NewConn(a)}
	go func() { proto.NewConn(b).Read() }()
	if !c.send(proto.Message{Event: "x"}) {
		t.Fatal("send")
	}
	c.flush(time.Second) // nothing to wait for
	c.close()
	if c.send(proto.Message{Event: "y"}) {
		t.Fatal("sent on a closed connection")
	}
}
