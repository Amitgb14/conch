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
}

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
	go c.readLoop()
	go c.writeLoop()

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
	defer close(c.Events)
	for {
		msg, err := c.conn.Read()
		if err != nil {
			c.fail(err)
			return
		}
		if msg.Event != "" {
			select {
			case c.Events <- msg:
			case <-c.done:
				return
			}
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

// EnsureServer makes sure a server is listening on sockPath, starting a
// detached `conch server` (logging to logPath) if needed.
func EnsureServer(sockPath, logPath string) error {
	if nc, err := net.Dial("unix", sockPath); err == nil {
		nc.Close()
		return nil
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

	deadline := time.After(5 * time.Second)
	for {
		if nc, err := net.Dial("unix", sockPath); err == nil {
			nc.Close()
			return nil
		}
		select {
		case err := <-exited:
			return fmt.Errorf("server exited during startup (%v); see %s", err, logPath)
		case <-deadline:
			return fmt.Errorf("server did not start within 5s; see %s", logPath)
		case <-time.After(50 * time.Millisecond):
		}
	}
}
