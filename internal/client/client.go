// Package client talks to a conch server over its socket.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// Client is a connection to one conch server.
type Client struct {
	conn   *proto.Conn
	Server proto.HelloResult

	// Events receives server pushes in order. It is closed when the
	// connection ends; Err then reports why.
	Events chan proto.Message

	nextID  atomic.Uint64
	mu      sync.Mutex
	pending map[string]chan proto.Message
	err     error

	// outbox serialises notifications so keystrokes keep their order
	// without blocking the caller on a socket write.
	outbox chan proto.Message
	done   chan struct{}

	// Events are handed on by a separate goroutine through this queue, so
	// reading the connection never waits for a client that is slow to take
	// them: replies to calls would queue behind a burst of pane output.
	evMu     sync.Mutex
	evCond   *sync.Cond
	evQueue  []proto.Message
	evFrame  map[string]int // pane ID -> its queued frame, which newer ones replace
	evClosed bool
}

// maxQueuedEvents bounds the queue for a client that stops reading Events
// altogether; the oldest are dropped.
const maxQueuedEvents = 4096

// ErrClosed is returned for calls on a closed connection.
var ErrClosed = errors.New("connection to conch server closed")

// Dial connects to the server socket and performs the hello handshake.
func Dial(sockPath, clientName string) (*Client, error) {
	nc, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return nil, err
	}
	return New(nc, clientName)
}

// New performs the hello handshake over an established stream, such as an
// SSH bridge to a remote server. The client owns rw from then on.
func New(rw io.ReadWriteCloser, clientName string) (*Client, error) {
	c := &Client{
		conn:    proto.NewConn(rw),
		Events:  make(chan proto.Message, 1024),
		pending: map[string]chan proto.Message{},
		outbox:  make(chan proto.Message, 1024),
		done:    make(chan struct{}),
	}
	c.evCond = sync.NewCond(&c.evMu)
	c.evFrame = map[string]int{}
	go c.readLoop()
	go c.writeLoop()
	go c.eventLoop()

	// Generous: through a bridge this includes starting the remote server.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := c.Call(ctx, proto.MethodHello, proto.HelloParams{
		Client:       clientName,
		Version:      proto.Version,
		Protocol:     proto.ProtocolVersion,
		Capabilities: proto.Capabilities,
	}, &c.Server)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("handshake: %w", err)
	}
	return c, nil
}

func (c *Client) readLoop() {
	defer c.closeEvents()
	for {
		msg, err := c.conn.Read()
		if err != nil {
			c.fail(err)
			return
		}
		if msg.Event != "" {
			c.queueEvent(msg)
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[msg.ID]
		delete(c.pending, msg.ID)
		c.mu.Unlock()
		if ok {
			ch <- msg
		}
	}
}

// queueEvent adds an event for the event loop to hand on. A pane frame is
// the whole screen, so a newer one replaces the frame still queued for that
// pane instead of piling up behind it.
func (c *Client) queueEvent(msg proto.Message) {
	c.evMu.Lock()
	defer c.evMu.Unlock()
	if c.evClosed {
		return
	}
	if msg.Event == proto.EventPaneFrame {
		if id := frameID(msg); id != "" {
			if i, ok := c.evFrame[id]; ok {
				c.evQueue[i] = msg
				c.evCond.Signal()
				return
			}
			c.evFrame[id] = len(c.evQueue)
		}
	}
	c.evQueue = append(c.evQueue, msg)
	for len(c.evQueue) > maxQueuedEvents {
		c.dropFirstLocked()
	}
	c.evCond.Signal()
}

// dropFirstLocked removes the oldest queued event. The caller holds evMu.
func (c *Client) dropFirstLocked() {
	first := c.evQueue[0]
	c.evQueue = c.evQueue[1:]
	if first.Event == proto.EventPaneFrame {
		if id := frameID(first); id != "" {
			delete(c.evFrame, id)
		}
	}
	for id := range c.evFrame {
		c.evFrame[id]--
	}
}

// frameID is the pane a frame event belongs to.
func frameID(msg proto.Message) string {
	var f struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(msg.Data, &f) != nil {
		return ""
	}
	return f.ID
}

// eventLoop hands queued events to Events in order.
func (c *Client) eventLoop() {
	defer close(c.Events)
	for {
		c.evMu.Lock()
		for len(c.evQueue) == 0 && !c.evClosed {
			c.evCond.Wait()
		}
		if len(c.evQueue) == 0 {
			c.evMu.Unlock()
			return // closed and drained
		}
		msg := c.evQueue[0]
		c.dropFirstLocked()
		c.evMu.Unlock()
		select {
		case c.Events <- msg:
		case <-c.done:
			return
		}
	}
}

// closeEvents ends the event loop once what is queued has been handed on.
func (c *Client) closeEvents() {
	c.evMu.Lock()
	c.evClosed = true
	c.evMu.Unlock()
	c.evCond.Broadcast()
}

func (c *Client) writeLoop() {
	for {
		select {
		case msg := <-c.outbox:
			if err := c.conn.Write(msg); err != nil {
				c.fail(err)
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		err = ErrClosed
	}
	c.err = err
	close(c.done)
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.conn.Close()
}

// Err returns why the connection ended, or nil while it is open.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// MissingCapabilities lists the capabilities in want that the server did
// not advertise.
func (c *Client) MissingCapabilities(want []string) []string {
	have := map[string]bool{}
	for _, cap := range c.Server.Capabilities {
		have[cap] = true
	}
	var missing []string
	for _, w := range want {
		if !have[w] {
			missing = append(missing, w)
		}
	}
	return missing
}

// Call sends a request and decodes the result into out (which may be nil).
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	ch := make(chan proto.Message, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.pending[id] = ch
	c.mu.Unlock()

	select {
	case c.outbox <- proto.Message{ID: id, Method: method, Params: proto.Marshal(params)}:
	case <-c.done:
		return c.Err()
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return c.Err()
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	}
}

func (c *Client) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// Notify sends a request without waiting for a reply. Notifications are
// delivered in the order Notify is called.
func (c *Client) Notify(method string, params any) {
	select {
	case c.outbox <- proto.Message{Method: method, Params: proto.Marshal(params)}:
	case <-c.done:
	}
}

// Close ends the connection.
func (c *Client) Close() error {
	c.fail(ErrClosed)
	return nil
}

// startWait is how long a server started here is given to answer.
var startWait = 5 * time.Second

// probeWait bounds one look at a socket: a healthy server answers hello in
// milliseconds, so this only ever runs out for one that has stopped serving.
var probeWait = 5 * time.Second

// answers reports whether a server on sockPath replies to a hello. Connecting
// is not enough: a server that closed its accept loop — one wedged partway
// through a reload — still lets the kernel complete connections, and a client
// that took that for a running server waited out its whole handshake and gave
// up with no way back.
func answers(sockPath string, wait time.Duration) bool {
	nc, err := net.DialTimeout("unix", sockPath, wait)
	if err != nil {
		return false
	}
	defer nc.Close()
	_ = nc.SetDeadline(time.Now().Add(wait))
	conn := proto.NewConn(nc)
	if err := conn.Write(proto.Message{ID: "probe", Method: proto.MethodHello, Params: mustJSON(proto.HelloParams{
		Client: "conch-probe", Version: proto.Version, Protocol: proto.ProtocolVersion,
	})}); err != nil {
		return false
	}
	for {
		msg, err := conn.Read()
		if err != nil {
			return false
		}
		if msg.ID == "probe" {
			return true // an error reply still means a server is serving
		}
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// EnsureServer makes sure a server is answering on sockPath, starting a
// detached `conch server` (logging to logPath) if needed. A socket left
// behind by a server that stopped serving is removed first, so a new server
// can bind it instead of the user being stuck with a conch that won't start.
func EnsureServer(sockPath, logPath string) error {
	if answers(sockPath, probeWait) {
		return nil
	}
	if _, err := os.Stat(sockPath); err == nil {
		// Taking a socket away from a server that is merely busy would
		// leave it running and unreachable, panes and all, so give a
		// second chance before deciding nothing is behind it.
		time.Sleep(200 * time.Millisecond)
		if answers(sockPath, probeWait) {
			return nil
		}
		_ = os.Remove(sockPath)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()

	cmd := exec.Command(exe, "server")
	cmd.Env = config.MergeEnv(os.Environ(), "CONCH_SOCKET="+sockPath)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach from this terminal
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.After(startWait)
	for {
		if answers(sockPath, time.Second) {
			return nil
		}
		select {
		case err := <-exited:
			return fmt.Errorf("server exited during startup (%v); see %s", err, logPath)
		case <-deadline:
			return fmt.Errorf("server did not start within %s; see %s", startWait, logPath)
		case <-time.After(50 * time.Millisecond):
		}
	}
}
