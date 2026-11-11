package cloud

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/relay"
)

// Reaching a machine (PROTOCOL §3.2, §3.4): /connect for the relay, the
// room, a single-use ticket and the machine's key; the key checked
// against the one pinned; the relay dialled with the ticket and a Noise
// session made with the machine through it. Inside, conch's protocol
// travels on a /bridge socket and scripts on /exec ones.

// waitPoll is how often a machine on its way up is asked about; tests
// shorten it.
var waitPoll = 2 * time.Second

// noGatewayTries is how often a relay that has no gateway in the room
// yet — the machine is running but its agent has not joined — is tried
// again, waitPoll apart.
var noGatewayTries = 15

// Dial opens a tunnel to the machine id, waking it and waiting for it
// first when it is asleep. say, if set, is told what is being waited on.
func (c *Client) Dial(ctx context.Context, id string, say func(string)) (*relay.Session, error) {
	if say == nil {
		say = func(string) {}
	}
	refused := 0
	for gatewayTries := 0; ; {
		m, err := c.waitRunning(ctx, id, say)
		if err != nil {
			return nil, err
		}
		ci, err := c.Connect(ctx, id)
		if err != nil {
			return nil, err
		}
		if ci.State != StateRunning || ci.ServerKey == "" {
			say(fmt.Sprintf("%s is %s", nameOf(m), ci.State))
			if err := sleepCtx(ctx, waitPoll); err != nil {
				return nil, err
			}
			continue
		}
		if err := checkPin(m, ci.ServerKey); err != nil {
			return nil, err
		}
		pub, err := decodeKey(ci.ServerKey)
		if err != nil {
			return nil, fmt.Errorf("the key Conch Cloud gave for %s: %w", nameOf(m), err)
		}
		s, err := relay.DialTicket(ctx, ci.RelayURL, ci.Room, ci.Ticket, pub)
		switch {
		case err == nil:
			return s, nil
		case errors.Is(err, relay.ErrNoGateway) && gatewayTries < noGatewayTries:
			gatewayTries++
			say(nameOf(m) + " is starting up")
		case errors.Is(err, relay.ErrTicketRefused) && refused == 0:
			refused++ // a ticket that lapsed on the way: one more
		case errors.Is(err, relay.ErrTicketRefused):
			return nil, fmt.Errorf("the relay refused this device's ticket for %s: %w", nameOf(m), err)
		case errors.Is(err, relay.ErrRevoked):
			return nil, ErrSignedOut
		case errors.Is(err, relay.ErrNoGateway):
			return nil, fmt.Errorf("%s is running but its conch has not joined the relay; try again shortly", nameOf(m))
		default:
			return nil, fmt.Errorf("the relay for %s: %w", nameOf(m), err)
		}
		if err := sleepCtx(ctx, waitPoll); err != nil {
			return nil, err
		}
	}
}

// waitRunning returns the machine once it is running, waking it if it
// sleeps, and an error for one that will not come up.
func (c *Client) waitRunning(ctx context.Context, id string, say func(string)) (Machine, error) {
	woke := false
	for {
		m, err := c.Machine(ctx, id)
		if err != nil {
			return m, err
		}
		switch m.State {
		case StateRunning:
			return m, nil
		case StateAsleep, StateSleeping, StateArchived:
			if !woke {
				woke = true
				say("waking " + nameOf(m))
				if _, err := c.Wake(ctx, id); err != nil {
					return m, err
				}
				continue
			}
		case StateError:
			detail := m.StateDetail
			if detail == "" {
				detail = "no reason given"
			}
			return m, fmt.Errorf("%s is in error: %s", nameOf(m), detail)
		case StateDestroying:
			return m, fmt.Errorf("%s is being destroyed", nameOf(m))
		default:
			say(fmt.Sprintf("waiting for %s (%s)", nameOf(m), m.State))
		}
		if err := sleepCtx(ctx, waitPoll); err != nil {
			return m, err
		}
	}
}

func nameOf(m Machine) string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// decodeKey reads a key as base64url, with or without padding (the
// contract says base64url; plain base64 is taken too).
func decodeKey(k string) ([]byte, error) {
	k = strings.TrimRight(strings.TrimSpace(k), "=")
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(k); err == nil {
			if len(b) != 32 {
				return nil, fmt.Errorf("a %d-byte key, want 32", len(b))
			}
			return b, nil
		}
	}
	return nil, errors.New("not base64url")
}

// ErrDropped is a tunnel that ended before its socket did: the relay or
// the machine went away mid-stream.
var ErrDropped = errors.New("the connection to the machine dropped")

// dropped is why a socket ended early.
func dropped(s *relay.Session, err error) error {
	if se := s.Err(); se != nil {
		if errors.Is(se, relay.ErrRevoked) {
			return ErrSignedOut
		}
		return fmt.Errorf("%w: %v", ErrDropped, se)
	}
	return fmt.Errorf("%w: %v", ErrDropped, err)
}

// Bridge carries conch's protocol between in/out and the machine's
// server: a line of in is a message to it, and each message back a line
// of out — what `conch bridge` does over ssh. It returns nil when in
// ends, and an error when the machine's end does first.
func Bridge(ctx context.Context, s *relay.Session, in io.Reader, out io.Writer) error {
	k, err := s.Socket(ctx, "/bridge", nil)
	if err != nil {
		return dropped(s, err)
	}
	inDone := make(chan error, 1)
	go func() {
		br := bufio.NewReaderSize(in, 64<<10)
		for {
			line, err := br.ReadBytes('\n')
			if msg := bytes.TrimRight(line, "\r\n"); len(bytes.TrimSpace(msg)) > 0 {
				if serr := k.Send(ctx, string(msg)); serr != nil {
					inDone <- dropped(s, serr)
					return
				}
			}
			if err != nil {
				if err == io.EOF {
					err = nil
				}
				inDone <- err
				return
			}
		}
	}()
	outDone := make(chan error, 1)
	go func() {
		for {
			msg, err := k.Recv(ctx)
			if err != nil {
				outDone <- err
				return
			}
			if _, err := io.WriteString(out, msg+"\n"); err != nil {
				outDone <- err
				return
			}
		}
	}()
	select {
	case err := <-inDone:
		k.Close()
		return err
	case err := <-outDone:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return dropped(s, err)
	}
}

// ExecOptions is one script for Exec.
type ExecOptions struct {
	Script string
	// TTY runs it on a terminal of its own, Cols×Rows, which Resize
	// changes.
	TTY        bool
	Cols, Rows int
	Resize     <-chan [2]int
	Stdin      io.Reader // nil: none
	Stdout     io.Writer
	Stderr     io.Writer
}

type execMsg struct {
	Out  string `json:"out,omitempty"`
	Err  string `json:"err,omitempty"`
	Exit *int   `json:"exit,omitempty"`
}

// Exec runs a script on the machine as its user's login shell would —
// what `ssh host sh -c …` is elsewhere — and returns its exit status.
func Exec(ctx context.Context, s *relay.Session, o ExecOptions) (int, error) {
	k, err := s.Socket(ctx, "/exec", nil)
	if err != nil {
		return 255, dropped(s, err)
	}
	defer k.Close()
	start, _ := json.Marshal(map[string]any{"script": o.Script, "tty": o.TTY})
	if err := k.Send(ctx, string(start)); err != nil {
		return 255, dropped(s, err)
	}
	if o.TTY && o.Cols > 0 && o.Rows > 0 {
		r, _ := json.Marshal(map[string]any{"resize": []int{o.Cols, o.Rows}})
		_ = k.Send(ctx, string(r))
	}
	var sendMu sync.Mutex
	send := func(v any) error {
		b, _ := json.Marshal(v)
		sendMu.Lock()
		defer sendMu.Unlock()
		return k.Send(ctx, string(b))
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		if o.Stdin != nil {
			buf := make([]byte, 32<<10)
			for {
				n, err := o.Stdin.Read(buf)
				if n > 0 {
					if send(map[string]any{"in": base64.StdEncoding.EncodeToString(buf[:n])}) != nil {
						return
					}
				}
				if err != nil {
					break
				}
			}
		}
		_ = send(map[string]any{"in": "", "eof": true})
	}()
	if o.Resize != nil {
		go func() {
			for {
				select {
				case <-stop:
					return
				case sz := <-o.Resize:
					_ = send(map[string]any{"resize": []int{sz[0], sz[1]}})
				}
			}
		}()
	}
	stdout, stderr := o.Stdout, o.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	for {
		msg, err := k.Recv(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return 255, ctx.Err()
			}
			return 255, dropped(s, err)
		}
		var m execMsg
		if json.Unmarshal([]byte(msg), &m) != nil {
			continue
		}
		if m.Out != "" {
			if b, err := base64.StdEncoding.DecodeString(m.Out); err == nil {
				stdout.Write(b)
			}
		}
		if m.Err != "" {
			if b, err := base64.StdEncoding.DecodeString(m.Err); err == nil {
				stderr.Write(b)
			}
		}
		if m.Exit != nil {
			return *m.Exit, nil
		}
	}
}
