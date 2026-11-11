package cloudagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/Amitgb14/conch/internal/relay"
)

// The relay (PROTOCOL §3): the machine is the gateway of its room, with the
// room secret hello gave it and its own Noise key, and clients reach it
// through the relay with a ticket. Inside the tunnel a client opens
// sockets, and this gateway serves two: /bridge, conch's own protocol to
// the machine's server, one message a line — what `conch machine bridge`
// carries over ssh — and /exec, a script run as the machine's user, what
// `ssh host sh -c …` is elsewhere. They are served only through the
// tunnel: the agent listens on no network address at all.

// maxTunnelMessage is the tunnel's own limit on a frame (PROTOCOL §9).
const maxTunnelMessage = 8 << 20

// startLink joins the relay for cfg's room, replacing a link to another
// room or relay; a hello that names the same one changes nothing.
func (a *Agent) startLink(ctx context.Context, cfg VMConfig) {
	id := cfg.RelayURL + "\x00" + cfg.RoomSecret
	a.mu.Lock()
	if a.link != nil && a.linkKey == id {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	a.stopLink()
	lctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	l := &relay.Link{Relay: cfg.RelayURL, Secret: cfg.RoomSecret, Key: a.key, Handler: a.tunnelHandler(), Logf: a.logf,
		OnStatus: func(relay.Status) { a.kick() }} // clients_attached moves with it
	a.mu.Lock()
	a.link, a.linkKey, a.linkStop, a.linkDone = l, id, cancel, done
	a.mu.Unlock()
	go func() {
		defer close(done)
		if err := l.Run(lctx); err != nil {
			a.logf("cloud: relay %s: %v", cfg.RelayURL, err)
		}
	}()
}

// stopLink leaves the relay and waits until the link has.
func (a *Agent) stopLink() {
	a.mu.Lock()
	stop, done := a.linkStop, a.linkDone
	a.link, a.linkKey, a.linkStop, a.linkDone = nil, "", nil, nil
	a.mu.Unlock()
	if stop != nil {
		stop()
		<-done
	}
}

// TunnelHandler is what a machine serves inside the tunnel — /bridge to
// the conch server at opts.ServerSocket, /exec as opts.User — without the
// rest of an agent. conch's client side (internal/cloud) tests itself
// against it, so the gateway it is checked against is the machine's own
// and not a copy that could drift from it.
func TunnelHandler(opts Options) (http.Handler, error) {
	a, err := New(opts)
	if err != nil {
		return nil, err
	}
	return a.tunnelHandler(), nil
}

// tunnelHandler is what the tunnel's sockets reach.
func (a *Agent) tunnelHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/bridge", a.serveBridge)
	mux.HandleFunc("/exec", a.serveExec)
	return onlyThroughTunnel(mux)
}

// onlyThroughTunnel refuses anything that did not come through the relay's
// session, which only the link's in-memory connection can say.
func onlyThroughTunnel(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !relay.Through(r) {
			http.Error(w, "only through the tunnel", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// serveBridge carries one client's connection to the conch server: each
// message from the client is a line to the server, each line from the
// server a message back.
func (a *Agent) serveBridge(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(maxTunnelMessage)
	ctx := r.Context()
	nc, err := net.DialTimeout("unix", a.opts.ServerSocket, 5*time.Second)
	if err != nil {
		ws.Close(websocket.StatusInternalError, "the machine's conch server is not answering")
		return
	}
	defer nc.Close()
	go func() {
		defer ws.Close(websocket.StatusNormalClosure, "")
		br := bufio.NewReaderSize(nc, 64<<10)
		for {
			line, err := br.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				if werr := ws.Write(ctx, websocket.MessageText, bytes.TrimRight(line, "\r\n")); werr != nil {
					nc.Close()
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		_, msg, err := ws.Read(ctx)
		if err != nil {
			return
		}
		msg = bytes.TrimRight(msg, "\r\n")
		if bytes.ContainsAny(msg, "\r\n") {
			ws.Close(websocket.StatusPolicyViolation, "one message is one line of conch's protocol")
			return
		}
		if _, err := nc.Write(append(msg, '\n')); err != nil {
			return
		}
	}
}

// execStart is /exec's first message, and execIn the ones after it.
type execStart struct {
	Script string `json:"script"`
	TTY    bool   `json:"tty"`
}

type execIn struct {
	In     *string `json:"in"`
	EOF    bool    `json:"eof"`
	Resize []int   `json:"resize"`
}

type execOut struct {
	Out  string `json:"out,omitempty"`
	Err  string `json:"err,omitempty"`
	Exit *int   `json:"exit,omitempty"`
}

// serveExec runs one script in the machine user's login shell, as ssh
// would, its stdin, stdout and stderr carried as base64 messages and its
// exit status last; with tty, on a terminal of its own that the client
// may resize. A client that goes away takes the script with it.
func (a *Agent) serveExec(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(maxTunnelMessage)
	ctx := r.Context()
	_, first, err := ws.Read(ctx)
	if err != nil {
		return
	}
	var start execStart
	if json.Unmarshal(first, &start) != nil || start.Script == "" {
		ws.Close(websocket.StatusPolicyViolation, `the first message is {"script":…}`)
		return
	}
	u := a.opts.User
	cmd := exec.Command(u.Shell, "-l", "-c", start.Script)
	cmd.Dir = u.Home
	cmd.Env = u.env()
	attrs := u.credential()
	if attrs == nil {
		attrs = &syscall.SysProcAttr{}
	}
	var wmu sync.Mutex
	send := func(o execOut) error {
		b, _ := json.Marshal(o)
		wmu.Lock()
		defer wmu.Unlock()
		return ws.Write(ctx, websocket.MessageText, b)
	}
	pump := func(r io.Reader, out bool, wg *sync.WaitGroup) {
		defer wg.Done()
		buf := make([]byte, 32<<10)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				enc := base64.StdEncoding.EncodeToString(buf[:n])
				o := execOut{Err: enc}
				if out {
					o = execOut{Out: enc}
				}
				if send(o) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}

	var stdin io.WriteCloser
	var term *os.File
	var outputs sync.WaitGroup
	if start.TTY {
		term, err = pty.StartWithAttrs(cmd, &pty.Winsize{Cols: 80, Rows: 24}, withSession(attrs, true))
		if err != nil {
			a.execFailed(ws, err)
			return
		}
		defer term.Close()
		stdin = term
		outputs.Add(1)
		go pump(term, true, &outputs)
	} else {
		cmd.SysProcAttr = withSession(attrs, false)
		stdin, err = cmd.StdinPipe()
		if err != nil {
			a.execFailed(ws, err)
			return
		}
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()
		if err := cmd.Start(); err != nil {
			a.execFailed(ws, err)
			return
		}
		outputs.Add(2)
		go pump(stdout, true, &outputs)
		go pump(stderr, false, &outputs)
	}
	exited := make(chan struct{})
	go func() {
		// What the client sends, until it ends stdin or goes.
		for {
			_, msg, err := ws.Read(ctx)
			if err != nil {
				select {
				case <-exited:
				default:
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP) // the client went: so does the script
				}
				return
			}
			var in execIn
			if json.Unmarshal(msg, &in) != nil {
				continue
			}
			if in.In != nil && *in.In != "" {
				if b, err := base64.StdEncoding.DecodeString(*in.In); err == nil {
					_, _ = stdin.Write(b)
				}
			}
			if in.EOF {
				if term != nil {
					_, _ = term.Write([]byte{4}) // ^D: a terminal's end of input
				} else {
					stdin.Close()
				}
			}
			if len(in.Resize) == 2 && term != nil && in.Resize[0] > 0 && in.Resize[1] > 0 {
				_ = pty.Setsize(term, &pty.Winsize{Cols: uint16(in.Resize[0]), Rows: uint16(in.Resize[1])})
			}
		}
	}()
	if term == nil {
		outputs.Wait() // everything it printed, before its status
	}
	err = cmd.Wait()
	close(exited)
	if term != nil {
		// A terminal's output ends when the last program on it closes it;
		// what is left is read, then the terminal closed under the reader.
		done := make(chan struct{})
		go func() { outputs.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			term.Close()
			<-done
		}
	}
	code := exitCode(cmd, err)
	_ = send(execOut{Exit: &code})
	ws.Close(websocket.StatusNormalClosure, "")
}

// withSession is attrs with a session of the script's own, so the whole
// of it can be ended together, and for a terminal that terminal as its
// controlling one.
func withSession(attrs *syscall.SysProcAttr, tty bool) *syscall.SysProcAttr {
	a := *attrs
	a.Setsid = true
	a.Setctty = tty
	return &a
}

// exitCode is the script's status as a shell reports one: 128 and the
// signal for one that was killed.
func exitCode(cmd *exec.Cmd, err error) int {
	if cmd.ProcessState == nil {
		return 255
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	if code := cmd.ProcessState.ExitCode(); code >= 0 {
		return code
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return 1
	}
	return 255
}

func (a *Agent) execFailed(ws *websocket.Conn, err error) {
	a.logf("cloud: exec: %v", err)
	ws.Close(websocket.StatusInternalError, "the script could not be started: "+err.Error())
}
