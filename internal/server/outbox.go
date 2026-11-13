package server

import (
	"log"
	"net"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// Writing to a client is done by a goroutine of its own, from a queue.
//
// A client that stops reading — a suspended TUI, an ssh bridge whose
// connection stalled, a phone gateway that hung — fills its socket's
// buffer in a few KB, and every write to it then blocks. Writes used to be
// made where the message arose: a broadcast wrote to every client in turn,
// so one stuck client held up every event to every other; and an agent's
// state is broadcast with its evaluation lock held, so typing into that
// pane from a healthy client then waited too, on a connection whose serve
// loop was the one stuck — and the TUI froze with it. Now a message is
// only queued where it arises, which never waits; the client's writer
// writes it with a deadline, and a client that cannot keep up is cut off.
// It reconnects as it would after a server restart.

// Variables so tests can make a stuck client's cut-off quick.
var (
	// outboxMax is how much may wait for a client before it is cut off.
	// Frames and raw output wait for their own write (sendWait), so what
	// piles up is events and replies — a few KB each — and only from a
	// client that has stopped reading.
	outboxMax = 32 << 20
	// writeWait is how long one write may take. A client reading at all
	// drains a frame in milliseconds, even over a slow ssh link.
	writeWait = 10 * time.Second
)

// replyWait is how long the server waits for what it has queued for a
// client to be written before it stops or reloads.
const replyWait = 2 * time.Second

// outbox is a client's messages waiting to be written, in order.
type outbox struct {
	nc net.Conn
	// max and wait are outboxMax and writeWait as they were when it
	// started: read once, so its writer never reads the globals.
	max  int
	wait time.Duration
	mu   sync.Mutex
	cond *sync.Cond
	q    []proto.Message
	size int
	// queued and written count messages, so flush can wait for those
	// queued before it.
	queued, written uint64
	closed          bool
}

// startOutbox gives c a queue and the goroutine that writes it to nc.
func (c *client) startOutbox(nc net.Conn) {
	o := &outbox{nc: nc, max: outboxMax, wait: writeWait}
	o.cond = sync.NewCond(&o.mu)
	c.out = o
	go c.writeLoop()
}

// msgSize is about what a message takes on the wire.
func msgSize(m proto.Message) int {
	return 64 + len(m.Params) + len(m.Result) + len(m.Data) + len(m.Method) + len(m.Event)
}

// send queues m for c, or — for a client with no outbox, as tests make —
// writes it at once. It reports false when c is gone.
func (c *client) send(m proto.Message) bool {
	_, ok := c.enqueue(m)
	return ok
}

// sendWait is send for a stream — a pane's frames, its raw output — that
// should go no faster than the client takes it: it returns once m is
// written. Only the stream waits; nothing else queued for c, or for any
// other client, does. It reports false when c is gone, so the stream can
// stop.
func (c *client) sendWait(m proto.Message) bool {
	n, ok := c.enqueue(m)
	if !ok || c.out == nil {
		return ok
	}
	o := c.out
	o.mu.Lock()
	defer o.mu.Unlock()
	for o.written < n && !o.closed {
		o.cond.Wait()
	}
	return !o.closed
}

// enqueue queues m and returns its place in c's queue.
func (c *client) enqueue(m proto.Message) (uint64, bool) {
	o := c.out
	if o == nil {
		return 0, c.conn.Write(m) == nil
	}
	n := msgSize(m)
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return 0, false
	}
	if o.size+n > o.max {
		o.mu.Unlock()
		c.cut("it fell more than %d MB behind", o.max>>20)
		return 0, false
	}
	o.q = append(o.q, m)
	o.size += n
	o.queued++
	at := o.queued
	o.cond.Signal()
	o.mu.Unlock()
	return at, true
}

// writeLoop writes c's queue in order until c is closed or a write fails.
func (c *client) writeLoop() {
	o := c.out
	for {
		o.mu.Lock()
		for len(o.q) == 0 && !o.closed {
			o.cond.Wait()
		}
		if o.closed {
			o.mu.Unlock()
			return
		}
		m := o.q[0]
		o.q[0] = proto.Message{} // let it go once written
		o.q = o.q[1:]
		o.size -= msgSize(m)
		o.mu.Unlock()

		_ = o.nc.SetWriteDeadline(time.Now().Add(o.wait))
		err := c.conn.Write(m)

		o.mu.Lock()
		o.written++
		o.cond.Broadcast()
		o.mu.Unlock()
		if err != nil {
			c.cut("a write failed: %v", err)
			return
		}
	}
}

// cut drops c's queue and closes its connection; its serve loop then
// ends and cleans up after it.
func (c *client) cut(why string, args ...any) {
	o := c.out
	o.mu.Lock()
	already := o.closed
	o.closed = true
	o.q, o.size = nil, 0
	o.cond.Broadcast()
	o.mu.Unlock()
	if already {
		return
	}
	if why != "" {
		log.Printf("client cut off: "+why, args...)
	}
	_ = o.nc.Close()
}

// flush waits, at most d, until everything queued for c so far is
// written: the answer to server.stop or server.reload, say, before the
// server goes.
func (c *client) flush(d time.Duration) {
	o := c.out
	if o == nil {
		return
	}
	timer := time.AfterFunc(d, func() {
		o.mu.Lock()
		o.cond.Broadcast()
		o.mu.Unlock()
	})
	defer timer.Stop()
	deadline := time.Now().Add(d)
	o.mu.Lock()
	defer o.mu.Unlock()
	target := o.queued
	for o.written < target && !o.closed && time.Now().Before(deadline) {
		o.cond.Wait()
	}
}

// close stops c's writer; what is still queued is not written.
func (c *client) close() {
	if c.out == nil {
		_ = c.conn.Close()
		return
	}
	c.cut("")
}
