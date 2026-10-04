package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// `conch mcp` is a stdio MCP server in front of the same socket the CLI
// uses, so an agent driving other agents calls a tool and gets a typed
// result back instead of parsing a screenful of text. The command-line half
// (`conch agent prompt`, pane names, the skill) stays as it is: this is the
// same work through a second door, and both doors reach one server.
//
// Scoping needs no new rules. The server finds the caller from the socket's
// peer pid walked up its parents to a pane's program (internal/server/
// scope.go), so an MCP process an agent starts inside its own pane inherits
// that pane's scope — the panes it started, its project, that project's
// branches and worktrees — and a tool cannot reach past it. Nothing here
// declares who it is, and nothing here can widen it.
//
// Two rules the transport imposes. Stdout *is* the protocol, so nothing but
// a reply is ever written there — notes go to stderr. And requests are
// answered one at a time, in the order they arrive: a `wait` holds the
// session until it ends, as `conch wait` holds a shell, which is the
// behaviour an agent handing work over expects.

// mcpProtocol is the MCP revision conch implements. A client asking for
// another revision is answered with this one, as the spec says to, and
// decides for itself whether to carry on.
const mcpProtocol = "2025-06-18"

// mcpMaxLine bounds one JSON-RPC message; a longer line is a parse error
// rather than memory conch keeps growing.
const mcpMaxLine = 4 << 20

// JSON-RPC error codes, as MCP uses them.
const (
	mcpParseError     = -32700
	mcpInvalidRequest = -32600
	mcpMethodNotFound = -32601
	mcpInvalidParams  = -32602
	mcpInternalError  = -32603
)

// mcpDefaultWait is how long a tool that waits gives up after when the
// caller says nothing. A wait with no limit at all would hold the session
// for as long as the agent sat there.
const mcpDefaultWait = 30 * time.Minute

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *mcpError) Error() string { return e.Message }

// mcpSession is one run of the server: the stream it answers on, and the
// connection to conch it keeps while it does.
type mcpSession struct {
	w   io.Writer
	err io.Writer
	// dial makes the connection to the conch server; tests replace it.
	dial func() (*client.Client, error)

	mu sync.Mutex // guards writes to w, so a reply is never interleaved
	c  *client.Client
}

// runMCP serves MCP on stdin and stdout.
func runMCP(args []string) error {
	if len(args) > 0 {
		return errors.New("usage: conch mcp")
	}
	s := &mcpSession{w: os.Stdout, err: os.Stderr, dial: func() (*client.Client, error) { return connect(false) }}
	defer s.close()
	return s.serve(os.Stdin)
}

func (s *mcpSession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c != nil {
		s.c.Close()
		s.c = nil
	}
}

// serve reads messages until the stream ends. A message conch cannot read
// is answered with a parse error and the session carries on: one bad line
// from a client is not a reason to drop the agent's tools.
func (s *mcpSession) serve(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), mcpMaxLine)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.reply(mcpResponse{ID: nil, Error: &mcpError{mcpParseError, "that is not JSON: " + err.Error()}})
			continue
		}
		s.handle(req)
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			// Nothing can be answered: the id was in the line conch could
			// not hold. Say so on stderr and end, as a client that sends
			// one is not going to get an answer it can match.
			fmt.Fprintf(s.err, "conch mcp: a message over %d bytes\n", mcpMaxLine)
		}
		return err
	}
	return nil
}

// handle answers one message. A request with no id is a notification and
// gets no reply at all, whatever it asks for.
func (s *mcpSession) handle(req mcpRequest) {
	notification := len(req.ID) == 0
	result, err := s.dispatch(req)
	if notification {
		return
	}
	if err != nil {
		var me *mcpError
		if !errors.As(err, &me) {
			me = &mcpError{mcpInternalError, err.Error()}
		}
		s.reply(mcpResponse{ID: req.ID, Error: me})
		return
	}
	s.reply(mcpResponse{ID: req.ID, Result: result})
}

func (s *mcpSession) dispatch(req mcpRequest) (any, error) {
	switch req.Method {
	case "initialize":
		return s.initialize(req.Params)
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools()}, nil
	case "tools/call":
		return s.callTool(req.Params)
	case "notifications/initialized", "notifications/cancelled":
		// Nothing to do: one request is answered at a time, so there is
		// never another in flight to cancel.
		return struct{}{}, nil
	case "resources/list":
		return map[string]any{"resources": []any{}}, nil
	case "prompts/list":
		return map[string]any{"prompts": []any{}}, nil
	}
	return nil, &mcpError{mcpMethodNotFound, "conch mcp has no method " + req.Method}
}

func (s *mcpSession) initialize(json.RawMessage) (any, error) {
	return map[string]any{
		"protocolVersion": mcpProtocol,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "conch", "title": "conch", "version": proto.Version},
		"instructions": "conch is the terminal you are running in. These tools start other coding agents " +
			"in panes of their own, prompt them, wait for them and read their screens. " +
			"Start one helper per job: each is an agent spending the person's usage, and the person sees " +
			"every pane in conch's tree. You reach only what your own pane may reach — the panes you " +
			"started and your project — and a tool that is refused says so.",
	}, nil
}

// mcpContent is the text of a tool's answer, which is what a model reads.
func mcpContent(text string) []map[string]any {
	return []map[string]any{{"type": "text", "text": text}}
}

// toolResult is a tool that worked: a line for the model to read and the
// same facts typed, for a client that would rather have fields.
func toolResult(text string, structured any) any {
	res := map[string]any{"content": mcpContent(text), "isError": false}
	if structured != nil {
		res["structuredContent"] = structured
	}
	return res
}

// toolFailed is a tool that did not work. It is a result, not a JSON-RPC
// error, because the agent that called it is the one who can act on it:
// a refusal it reads is a sentence it can answer, where a transport error
// is something its client swallows.
func toolFailed(text string) any {
	return map[string]any{"content": mcpContent(text), "isError": true}
}

// callTool runs one tool by name.
func (s *mcpSession) callTool(raw json.RawMessage) (any, error) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &p) != nil {
		return nil, &mcpError{mcpInvalidParams, "tools/call takes a name and arguments"}
	}
	if p.Name == "" {
		return nil, &mcpError{mcpInvalidParams, "tools/call needs a tool name"}
	}
	for _, t := range mcpTools() {
		if t.Name != p.Name {
			continue
		}
		c, err := s.conch()
		if err != nil {
			return toolFailed(err.Error()), nil
		}
		out, err := t.run(c, p.Arguments)
		if err != nil {
			return toolFailed(err.Error()), nil
		}
		return out, nil
	}
	return nil, &mcpError{mcpInvalidParams, fmt.Sprintf("conch mcp has no tool %q; call tools/list", p.Name)}
}

// conch is the connection to the conch server, made when a tool first
// wants it and kept for the session. One that has dropped — the server
// reloaded, or stopped — is replaced rather than handed back broken, so a
// long-lived MCP process outlives a server restart.
func (s *mcpSession) conch() (*client.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c != nil {
		if s.c.Err() == nil {
			return s.c, nil
		}
		s.c.Close()
		s.c = nil
	}
	c, err := s.dial()
	if err != nil {
		return nil, fmt.Errorf("the conch server is not answering: %v", err)
	}
	s.c = c
	return c, nil
}

func (s *mcpSession) reply(res mcpResponse) {
	res.JSONRPC = "2.0"
	b, err := json.Marshal(res)
	if err != nil {
		fmt.Fprintln(s.err, "conch mcp: cannot write a reply:", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.w, "%s\n", b)
}
