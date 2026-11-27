package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Events is what the client tells whoever draws the agent: the updates it
// streams, the permissions it asks for, and lines conch could not read.
type Events interface {
	// Update is one session/update.
	Update(sessionID string, u Update)
	// Permission asks the person, and returns what they chose. ctx ends
	// when the turn is cancelled, and the answer is then "cancelled"
	// whatever Permission returns.
	Permission(ctx context.Context, p PermissionParams) PermissionOutcome
	// Problem is something wrong with what the agent sent, worth saying
	// but not worth stopping for.
	Problem(msg string)
}

// Options start an agent.
type Options struct {
	// Argv is the command that speaks ACP on its stdin and stdout.
	Argv []string
	Env  []string // the whole environment; nil is this process's
	// Dir is the folder the agent works in, and the one its file and
	// terminal requests are kept to.
	Dir    string
	Events Events
	// InitTimeout bounds initialize; 0 is a minute (an agent written in
	// node can take a while to load).
	InitTimeout time.Duration
}

// Client is a running agent, spoken to over ACP.
type Client struct {
	conn   *conn
	cmd    *exec.Cmd
	stdin  io.Closer
	ws     *workspace
	ev     Events
	stderr *tail
	// Init is what the agent answered initialize with.
	Init InitializeResult

	mu        sync.Mutex
	cancelled bool                 // the current turn was cancelled
	asking    []context.CancelFunc // permission requests waiting on the person
	stop      chan struct{}
	stopOnce  sync.Once
	// exited is closed once the agent's process has ended and what it
	// wrote to stderr has all been copied — which Wait does, after the
	// agent's stdout has often been seen to close.
	exited chan struct{}
}

// ClientCaps is what conch offers an agent: files and terminals, inside
// the folder it was started in.
var ClientCaps = ClientCapabilities{FS: FSCapabilities{ReadTextFile: true, WriteTextFile: true}, Terminal: true}

// tail keeps the end of what the agent writes to stderr, for saying why
// it stopped.
type tail struct {
	mu sync.Mutex
	b  []byte
}

const tailMax = 4 << 10

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > tailMax {
		t.b = append([]byte(nil), t.b[len(t.b)-tailMax:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.b))
}

// Start runs the agent and initializes it.
func Start(ctx context.Context, o Options) (*Client, error) {
	if len(o.Argv) == 0 {
		return nil, errors.New("no command to run the agent with")
	}
	ws, err := newWorkspace(o.Dir)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(o.Argv[0], o.Argv[1:]...)
	cmd.Dir = ws.root
	cmd.Env = o.Env
	// Its own process group: it is not on the terminal — conch is — so a
	// key the person presses is not a signal to it, and closing ends
	// whatever it started too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Pipes of conch's own rather than StdinPipe/StdoutPipe: Wait closes
	// those as the agent exits, and what it wrote last — often why it
	// exited — would be lost with them.
	inR, stdin, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdout, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		stdin.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout = inR, outW
	c := &Client{cmd: cmd, stdin: stdin, ws: ws, ev: o.Events, stderr: &tail{}, stop: make(chan struct{}), exited: make(chan struct{})}
	cmd.Stderr = c.stderr
	err = cmd.Start()
	inR.Close()
	outW.Close()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start %s: %w", o.Argv[0], err)
	}
	go func() {
		_ = cmd.Wait()
		close(c.exited)
	}()
	c.conn = newConn(stdout, stdin, c.handle, o.Events.Problem)

	timeout := o.InitTimeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	ictx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err = c.conn.Call(ictx, MethodInitialize, InitializeParams{
		ProtocolVersion: ProtocolVersion, ClientCapabilities: ClientCaps,
		ClientInfo: &Info{Name: "conch", Title: "conch"},
	}, &c.Init)
	if err != nil {
		// Why before Close: closing ends the agent, which may still be
		// writing the reason on its way out.
		err = c.why("initialize", err)
		c.Close()
		return nil, err
	}
	if c.Init.ProtocolVersion != 0 && c.Init.ProtocolVersion != ProtocolVersion {
		c.Close()
		return nil, fmt.Errorf("the agent speaks ACP version %d; conch speaks %d", c.Init.ProtocolVersion, ProtocolVersion)
	}
	return c, nil
}

// why is err with what the agent last wrote to stderr, when it has gone.
// lastWordsWait bounds how long an error waits for an agent that closed
// its output to finish saying why: one that lingers is not waited for.
const lastWordsWait = 2 * time.Second

func (c *Client) why(what string, err error) error {
	if errors.Is(err, ErrClosed) {
		select {
		case <-c.exited:
		case <-time.After(lastWordsWait):
		}
		return &ExitError{What: what, Stderr: lastLines(c.stderr.String(), 6)}
	}
	return fmt.Errorf("%s: %w", what, err)
}

// ExitError is the agent having exited while conch waited on it, with the
// end of what it wrote to stderr — usually why.
type ExitError struct {
	What   string // the call it was answering
	Stderr string
}

func (e *ExitError) Error() string {
	if e.Stderr != "" {
		return e.What + ": the agent exited: " + e.Stderr
	}
	return e.What + ": the agent exited"
}

func (e *ExitError) Unwrap() error { return ErrClosed }

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Stderr is the end of what the agent wrote to stderr.
func (c *Client) Stderr() string { return c.stderr.String() }

// Done is closed when the agent's output ends — it exited, or closed it.
func (c *Client) Done() <-chan struct{} { return c.conn.Done() }

// Close ends the agent and every terminal it left running.
func (c *Client) Close() {
	c.stopOnce.Do(func() {
		close(c.stop)
		c.stdin.Close()
		c.ws.closeAll()
		if c.cmd.Process != nil {
			_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGTERM)
			go func() {
				select {
				case <-c.conn.Done():
				case <-time.After(3 * time.Second):
				}
				_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
			}()
		}
	})
}

// NewSession starts a conversation in the workspace.
func (c *Client) NewSession(ctx context.Context) (string, error) {
	var r NewSessionResult
	if err := c.conn.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: c.ws.root, MCPServers: []any{}}, &r); err != nil {
		return "", c.why("session/new", err)
	}
	if r.SessionID == "" {
		return "", errors.New("session/new: the agent named no session")
	}
	return r.SessionID, nil
}

// ErrNoLoad is an agent asked to reopen a session that cannot.
var ErrNoLoad = errors.New("the agent cannot reopen a saved session (it does not offer loadSession)")

// LoadSession reopens a saved conversation; the agent replays it as
// updates before answering.
func (c *Client) LoadSession(ctx context.Context, id string) error {
	if !c.Init.AgentCapabilities.LoadSession {
		return ErrNoLoad
	}
	if err := c.conn.Call(ctx, MethodSessionLoad, LoadSessionParams{SessionID: id, Cwd: c.ws.root, MCPServers: []any{}}, nil); err != nil {
		return c.why("session/load", err)
	}
	return nil
}

// ErrNoList is an agent asked for its sessions that cannot say.
var ErrNoList = errors.New("the agent does not list its sessions (no sessionCapabilities.list)")

// maxListPages bounds how far a list is followed: an agent whose cursor
// never ends must not hold the Sessions view.
const maxListPages = 20

// ListSessions is the agent's sessions in the workspace.
func (c *Client) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	if !c.Init.CanList() {
		return nil, ErrNoList
	}
	var out []SessionInfo
	cursor := ""
	for range maxListPages {
		var r ListSessionsResult
		if err := c.conn.Call(ctx, MethodSessionList, ListSessionsParams{Cwd: c.ws.root, Cursor: cursor}, &r); err != nil {
			return out, c.why("session/list", err)
		}
		out = append(out, r.Sessions...)
		if r.NextCursor == "" || r.NextCursor == cursor {
			break
		}
		cursor = r.NextCursor
	}
	return out, nil
}

// Prompt sends one turn and waits for the agent to end it.
func (c *Client) Prompt(ctx context.Context, sessionID, text string) (string, error) {
	c.mu.Lock()
	c.cancelled = false
	c.mu.Unlock()
	var r PromptResult
	err := c.conn.Call(ctx, MethodPrompt, PromptParams{SessionID: sessionID, Prompt: []ContentBlock{{Type: "text", Text: text}}}, &r)
	if err != nil {
		return "", c.why("session/prompt", err)
	}
	return r.StopReason, nil
}

// Cancel asks the agent to stop the current turn. Permission requests
// still waiting are answered "cancelled", as are any it asks afterwards
// in this turn — the protocol's rule, and the only safe answer to a
// question nobody is there to read.
func (c *Client) Cancel(sessionID string) error {
	c.mu.Lock()
	c.cancelled = true
	asking := c.asking
	c.asking = nil
	c.mu.Unlock()
	for _, cancel := range asking {
		cancel()
	}
	return c.conn.Notify(MethodCancel, CancelParams{SessionID: sessionID})
}

func decode[T any](params json.RawMessage) (T, *Error) {
	var v T
	if err := json.Unmarshal(params, &v); err != nil {
		return v, &Error{Code: codeInvalidParams, Message: err.Error()}
	}
	return v, nil
}

// handle answers what the agent asks. A method conch does not offer is
// "method not found", which is what the protocol says an agent asking for
// a capability it was not given gets.
func (c *Client) handle(ctx context.Context, method string, params json.RawMessage, notify bool) (any, *Error) {
	switch method {
	case MethodUpdate:
		p, rerr := decode[UpdateParams](params)
		if rerr != nil {
			c.ev.Problem("a session/update conch could not read: " + rerr.Message)
			return nil, rerr
		}
		c.ev.Update(p.SessionID, p.Update)
		return nil, nil
	case MethodPermission:
		p, rerr := decode[PermissionParams](params)
		if rerr != nil {
			return nil, rerr
		}
		return PermissionResult{Outcome: c.permission(p)}, nil
	case MethodReadFile:
		p, rerr := decode[ReadFileParams](params)
		if rerr != nil {
			return nil, rerr
		}
		return c.ws.readFile(p)
	case MethodWriteFile:
		p, rerr := decode[WriteFileParams](params)
		if rerr != nil {
			return nil, rerr
		}
		if rerr := c.ws.writeFile(p); rerr != nil {
			return nil, rerr
		}
		return struct{}{}, nil
	case MethodTermCreate:
		p, rerr := decode[TermCreateParams](params)
		if rerr != nil {
			return nil, rerr
		}
		return c.ws.createTerminal(p)
	case MethodTermOutput:
		p, rerr := decode[TermParams](params)
		if rerr != nil {
			return nil, rerr
		}
		return c.ws.output(p.TerminalID)
	case MethodTermWait:
		p, rerr := decode[TermParams](params)
		if rerr != nil {
			return nil, rerr
		}
		return c.ws.wait(p.TerminalID, c.stop)
	case MethodTermKill:
		p, rerr := decode[TermParams](params)
		if rerr != nil {
			return nil, rerr
		}
		if rerr := c.ws.killTerminal(p.TerminalID); rerr != nil {
			return nil, rerr
		}
		return struct{}{}, nil
	case MethodTermRelease:
		p, rerr := decode[TermParams](params)
		if rerr != nil {
			return nil, rerr
		}
		if rerr := c.ws.release(p.TerminalID); rerr != nil {
			return nil, rerr
		}
		return struct{}{}, nil
	}
	if notify {
		return nil, nil // a notification conch does not know is ignored
	}
	return nil, &Error{Code: codeMethodNotFound, Message: "method not found: " + method}
}

var cancelledOutcome = PermissionOutcome{Outcome: "cancelled"}

func (c *Client) permission(p PermissionParams) PermissionOutcome {
	c.mu.Lock()
	if c.cancelled {
		c.mu.Unlock()
		return cancelledOutcome
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.asking = append(c.asking, cancel)
	c.mu.Unlock()
	defer cancel()
	out := c.ev.Permission(ctx, p)
	if ctx.Err() != nil {
		return cancelledOutcome
	}
	if out.Outcome == "selected" {
		ok := false
		for _, o := range p.Options {
			ok = ok || o.OptionID == out.OptionID
		}
		if !ok {
			return cancelledOutcome
		}
	}
	return out
}

// quiet is the Events of an agent started only to be asked something:
// nothing is drawn, and a permission it asks for is refused.
type quiet struct{}

func (quiet) Update(string, Update) {}
func (quiet) Permission(context.Context, PermissionParams) PermissionOutcome {
	return cancelledOutcome
}
func (quiet) Problem(string) {}

// noted is quiet, keeping the first line conch could not read — what an
// agent that is not speaking ACP prints instead.
type noted struct {
	quiet
	mu    sync.Mutex
	first string
}

func (n *noted) Problem(msg string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.first == "" {
		n.first = msg
	}
}

// ProbeError is a probe that never got as far as an answer to initialize:
// Noise is the first line it wrote that was not ACP, if any.
type ProbeError struct {
	Err   error
	Noise string
}

func (e *ProbeError) Error() string {
	if e.Noise != "" {
		return e.Err.Error() + " (" + e.Noise + ")"
	}
	return e.Err.Error()
}

func (e *ProbeError) Unwrap() error { return e.Err }

// Probe starts the agent, initializes it and, when it lists them, asks for
// its sessions in o.Dir — what the Sessions view shows for an ACP agent.
// The agent is ended before Probe returns. o.Events is ignored.
func Probe(ctx context.Context, o Options, list bool) (InitializeResult, []SessionInfo, error) {
	n := &noted{}
	o.Events = n
	c, err := Start(ctx, o)
	if err != nil {
		n.mu.Lock()
		defer n.mu.Unlock()
		return InitializeResult{}, nil, &ProbeError{Err: err, Noise: n.first}
	}
	defer c.Close()
	if !list || !c.Init.CanList() {
		return c.Init, nil, nil
	}
	s, err := c.ListSessions(ctx)
	return c.Init, s, err
}
