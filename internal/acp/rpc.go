// Package acp is conch's side of the Agent Client Protocol
// (agentclientprotocol.com): JSON-RPC 2.0 over an agent's stdin and
// stdout, one message a line. conch is the *client* — the agent asks it
// to read and write files, run commands and ask the person for
// permission, and conch asks the agent to start, load and prompt
// sessions.
//
// It is the third way conch runs an agent, beside the supported nine and
// the manifests it only runs: an agent that speaks ACP has no terminal
// interface of its own, so `conch acp` (cmd/conch/acp.go, Run here) runs
// in the pane as the client, draws the conversation and reports the
// agent's state over OSC 7501 from the protocol itself — no screen rules.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ProtocolVersion is the ACP version conch speaks.
const ProtocolVersion = 1

// maxLine bounds one message. An agent replaying a long session sends
// large updates, so this is generous; a line past it is dropped whole
// and the connection carries on, rather than the pane growing without
// bound or dying on one message.
const maxLine = 32 << 20

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

// Error is a JSON-RPC error, sent or received.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if len(e.Data) > 0 && string(e.Data) != "null" {
		return fmt.Sprintf("%s (%d): %s", e.Message, e.Code, e.Data)
	}
	return fmt.Sprintf("%s (%d)", e.Message, e.Code)
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Handler answers what the agent asks or tells: a request (notify false)
// gets the result or error back; a notification's answer is dropped.
type Handler func(ctx context.Context, method string, params json.RawMessage, notify bool) (any, *Error)

// ErrClosed is a call made on, or cut short by, a connection that ended.
var ErrClosed = errors.New("the agent's connection closed")

// conn is one JSON-RPC connection over a reader and a writer.
type conn struct {
	w       io.Writer
	wmu     sync.Mutex
	handler Handler
	// Problem is told about a line that was not a message conch could
	// read: malformed JSON, a line too long. The connection carries on.
	problem func(string)

	mu      sync.Mutex
	next    int64
	pending map[string]chan message
	closed  bool
	done    chan struct{}
	err     error
}

func newConn(r io.Reader, w io.Writer, h Handler, problem func(string)) *conn {
	if problem == nil {
		problem = func(string) {}
	}
	c := &conn{w: w, handler: h, problem: problem, pending: map[string]chan message{}, done: make(chan struct{})}
	go c.read(r)
	return c
}

// Done is closed when the agent's output ends.
func (c *conn) Done() <-chan struct{} { return c.done }

// Err is why the connection ended, once Done is closed.
func (c *conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *conn) read(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	var err error
	for {
		var line []byte
		line, err = readLine(br)
		if errors.Is(err, errLineTooLong) {
			c.problem(fmt.Sprintf("a message over %d MB was dropped", maxLine>>20))
			continue
		}
		if len(bytes.TrimSpace(line)) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			break
		}
	}
	if err == io.EOF {
		err = ErrClosed
	}
	c.mu.Lock()
	c.closed, c.err = true, err
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.done)
}

var errLineTooLong = errors.New("line too long")

// readLine reads one line, discarding the rest of one past maxLine.
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(buf)+len(chunk) > maxLine {
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = br.ReadSlice('\n')
			}
			if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
				return nil, err
			}
			return nil, errLineTooLong
		}
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return buf, err
	}
}

func (c *conn) dispatch(line []byte) {
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		c.problem("a line that is not JSON-RPC: " + clip(string(line), 120))
		c.reply(json.RawMessage("null"), nil, &Error{Code: codeParse, Message: "parse error"})
		return
	}
	switch {
	case m.Method != "":
		notify := len(m.ID) == 0 || string(m.ID) == "null"
		if notify {
			// In order, here: updates are a stream, and the answer to the
			// prompt that follows them must not overtake them.
			_, _ = c.handler(context.Background(), m.Method, m.Params, true)
			return
		}
		// A request may wait on the person (a permission), so it must
		// not hold up the stream behind it.
		go func() {
			res, rerr := c.handler(context.Background(), m.Method, m.Params, false)
			c.reply(m.ID, res, rerr)
		}()
	case len(m.ID) > 0:
		c.mu.Lock()
		ch := c.pending[string(m.ID)]
		delete(c.pending, string(m.ID))
		c.mu.Unlock()
		if ch == nil {
			c.problem("an answer to nothing conch asked: " + clip(string(line), 120))
			return
		}
		ch <- m
	default:
		c.problem("a message that is neither a request nor an answer: " + clip(string(line), 120))
		c.reply(json.RawMessage("null"), nil, &Error{Code: codeInvalidRequest, Message: "invalid request"})
	}
}

func (c *conn) write(m message) error {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(append(b, '\n'))
	return err
}

func (c *conn) reply(id json.RawMessage, res any, rerr *Error) {
	m := message{ID: id}
	if rerr != nil {
		m.Error = rerr
	} else {
		b, err := json.Marshal(res)
		if err != nil {
			m.Error = &Error{Code: codeInternal, Message: err.Error()}
		} else {
			m.Result = b
		}
	}
	_ = c.write(m)
}

// Call sends a request and waits for its answer, decoding the result into
// out (which may be nil).
func (c *conn) Call(ctx context.Context, method string, params, out any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.next++
	id := json.RawMessage(fmt.Sprint(c.next))
	ch := make(chan message, 1)
	c.pending[string(id)] = ch
	c.mu.Unlock()
	if err := c.write(message{ID: id, Method: method, Params: p}); err != nil {
		c.forget(id)
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if m.Error != nil {
			return m.Error
		}
		if out != nil && len(m.Result) > 0 && string(m.Result) != "null" {
			if err := json.Unmarshal(m.Result, out); err != nil {
				return fmt.Errorf("%s: the agent's answer: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	}
}

func (c *conn) forget(id json.RawMessage) {
	c.mu.Lock()
	delete(c.pending, string(id))
	c.mu.Unlock()
}

// Notify sends a notification.
func (c *conn) Notify(method string, params any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(message{Method: method, Params: p})
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
