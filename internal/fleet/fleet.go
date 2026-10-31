// Package fleet keeps a client per machine — this computer and every
// enabled machine in the catalog — for a program that shows them all at
// once: the phone gateway.
//
// Three things make it different from simply dialling each one:
//
//   - Nobody waits on ssh. Reaching a machine takes seconds at best, and a
//     minute when conch has to be installed there, so a machine is
//     connected to in the background and its state says how far it got.
//     Asking again shortly says more; Changed says when it does.
//   - A machine that cannot be reached does not take the others with it.
//     It carries why, so nothing is silently missing.
//   - A pane ID is its server's own, so `p3` exists on every machine.
//     Outside a machine a pane is `machine:pane` (ComposePaneID).
package fleet

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// Local is this computer's machine ID.
const Local = "local"

// A machine's state.
const (
	Online     = "online"
	Connecting = "connecting"
	Offline    = "offline"
)

// Machine is a machine as it was when it was looked at: a snapshot, not
// the live connection, because reaching a machine happens in another
// goroutine and a pointer handed out here would be read while it is
// written. The client is safe to carry — it is not replaced once set,
// only closed, and a closed one reports its own error.
type Machine struct {
	ID, Label string
	State     string
	Detail    string // why it is offline, one line
	c         *client.Client
}

// Client is the connection while it is good, else nil.
func (m Machine) Client() *client.Client {
	if m.c != nil && m.c.Err() == nil {
		return m.c
	}
	return nil
}

// Options says how a pool reaches things. Everything but Local has a
// default; the functions are read at each use, so a test may change what
// they return while the pool runs.
type Options struct {
	// Local is this computer's shared client, cheap to ask for: the pool
	// asks at every look rather than keeping its own.
	Local func() (*client.Client, error)
	// DialLocal is a fresh connection of this computer's, for Dial.
	DialLocal func() (*client.Client, error)
	// LocalLabel is what this computer is called; "this computer".
	LocalLabel string
	// Connect reaches a machine of the catalog: over ssh, by default.
	Connect func(context.Context, remote.Machine) (*client.Client, error)
	// Catalog is the machines there are: remote.Machines, by default.
	Catalog func() ([]remote.Machine, error)
	// Retry is how long before a machine that would not answer is tried
	// again (30 s); ConnectWait bounds one attempt (45 s).
	Retry, ConnectWait func() time.Duration
	Now                func() time.Time
	// Changed is told when a machine comes up. It is called with no lock
	// held, so it may look at the pool.
	Changed func()
	// Logf gets one line for each machine that fails, and nothing else.
	Logf func(format string, args ...any)
}

// Pool is the clients of every machine.
type Pool struct {
	o Options

	mu     sync.Mutex
	conns  map[string]*conn
	closed bool
	wg     sync.WaitGroup
}

// conn is one machine and what the pool knows about it.
type conn struct {
	id, label string
	c         *client.Client
	err       error     // why it is not reachable
	tried     time.Time // when it was last attempted
	busy      bool      // an attempt is running
}

// New makes a pool. Close it when done.
func New(o Options) *Pool {
	if o.Local == nil {
		o.Local = func() (*client.Client, error) { return nil, errors.New("no local server") }
	}
	if o.DialLocal == nil {
		o.DialLocal = o.Local
	}
	if o.LocalLabel == "" {
		o.LocalLabel = "this computer"
	}
	if o.Connect == nil {
		o.Connect = func(ctx context.Context, m remote.Machine) (*client.Client, error) {
			tr, err := remote.TransportFor(ctx, m.Label, m.Target, false)
			if err != nil {
				return nil, err
			}
			return remote.Connect(ctx, tr, remote.Options{})
		}
	}
	if o.Catalog == nil {
		o.Catalog = remote.Machines
	}
	if o.Retry == nil {
		o.Retry = func() time.Duration { return 30 * time.Second }
	}
	if o.ConnectWait == nil {
		o.ConnectWait = func() time.Duration { return 45 * time.Second }
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Changed == nil {
		o.Changed = func() {}
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	return &Pool{o: o, conns: map[string]*conn{}}
}

// state is the one place a machine's state is decided. The caller holds mu.
func (mc *conn) state() string {
	switch {
	case mc.c != nil && mc.c.Err() == nil:
		return Online
	case mc.busy:
		return Connecting
	}
	return Offline
}

// snap takes the snapshot. The caller holds mu.
func (mc *conn) snap() Machine {
	m := Machine{ID: mc.id, Label: mc.label, c: mc.c, State: mc.state()}
	if m.State == Offline && mc.err != nil {
		m.Detail = FirstLine(mc.err.Error())
	}
	return m
}

// Machines returns every machine, this computer first and the rest by
// label, starting to reach any that is not up. A machine gone from the
// catalog takes its connection with it.
func (p *Pool) Machines() []Machine {
	p.mu.Lock()
	defer p.mu.Unlock()
	local := p.conns[Local]
	if local == nil {
		local = &conn{id: Local, label: p.o.LocalLabel}
		p.conns[Local] = local
	}
	// Local is this process's own socket: asking for it is cheap and
	// immediate, so it is done here rather than in the background.
	local.c, local.err = p.o.Local()
	if local.err != nil {
		local.c = nil
	}

	saved, err := p.o.Catalog()
	if err != nil {
		// The catalog cannot be read: say so against this computer rather
		// than inventing machines.
		p.o.Logf("machines: %v", err)
		return []Machine{local.snap()}
	}
	out := []Machine{}
	seen := map[string]bool{Local: true}
	for _, m := range saved {
		if !m.Enabled || m.ID == Local || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		mc := p.conns[m.ID]
		if mc == nil {
			mc = &conn{id: m.ID, label: m.Label}
			p.conns[m.ID] = mc
		}
		mc.label = m.Label
		p.ensure(mc, m)
		out = append(out, mc.snap())
	}
	for id, mc := range p.conns {
		if !seen[id] {
			if mc.c != nil {
				mc.c.Close()
			}
			delete(p.conns, id)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return append([]Machine{local.snap()}, out...)
}

// Machine is one machine of Machines, and whether there is one.
func (p *Pool) Machine(id string) (Machine, bool) {
	for _, m := range p.Machines() {
		if m.ID == id {
			return m, true
		}
	}
	return Machine{}, false
}

// ensure starts reaching a machine that is not up, unless an attempt is
// running or the last failed too recently to be worth repeating. The
// caller holds mu.
func (p *Pool) ensure(mc *conn, m remote.Machine) {
	if (mc.c != nil && mc.c.Err() == nil) || mc.busy || p.closed {
		return
	}
	if mc.c != nil { // it dropped: forget it before trying again
		mc.c.Close()
		mc.c = nil
	}
	if !mc.tried.IsZero() && p.o.Now().Sub(mc.tried) < p.o.Retry() {
		return
	}
	mc.busy, mc.tried = true, p.o.Now()
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		changed := false
		defer func() {
			if changed {
				p.o.Changed()
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), p.o.ConnectWait())
		defer cancel()
		c, err := p.o.Connect(ctx, m)
		p.mu.Lock()
		defer p.mu.Unlock()
		mc.busy = false
		switch {
		case err != nil:
			mc.c, mc.err = nil, err
			p.o.Logf("%s: %v", m.ID, err)
		case p.closed:
			c.Close() // nobody left to serve
		default:
			mc.c, mc.err = c, nil
			// The pool's client is for calls; whoever follows a machine's
			// events dials its own (Dial). Keep the queue empty so this
			// one does not block.
			go func() {
				for range c.Events {
				}
			}()
			changed = true
		}
	}()
}

// Note records why a machine could not be used, and drops a connection
// that has gone so the next look reaches for it again.
func (p *Pool) Note(id string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	mc := p.conns[id]
	if mc == nil {
		return
	}
	mc.err = err
	if mc.c != nil && mc.c.Err() != nil {
		mc.c.Close()
		mc.c = nil
	}
	p.o.Logf("%s: %v", id, err)
}

// Dial is a connection of the caller's own to a machine, for following its
// events: the pool's client is shared, and its events are thrown away. A
// machine other than this computer must be up first, so this rides the
// connection already made rather than starting another from nothing.
func (p *Pool) Dial(id string) (*client.Client, error) {
	if id == Local {
		return p.o.DialLocal()
	}
	if m, ok := p.Machine(id); !ok || m.Client() == nil {
		return nil, fmt.Errorf("%s is not connected", id)
	}
	saved, err := p.o.Catalog()
	if err != nil {
		return nil, err
	}
	for _, m := range saved {
		if m.ID == id {
			ctx, cancel := context.WithTimeout(context.Background(), p.o.ConnectWait())
			defer cancel()
			return p.o.Connect(ctx, m)
		}
	}
	return nil, fmt.Errorf("no machine %q", id)
}

// Close drops every remote connection and waits for the attempts in
// flight, so a closed pool leaves no ssh behind. This computer's client
// is the caller's, and is left alone.
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	for id, mc := range p.conns {
		if id != Local && mc.c != nil {
			mc.c.Close()
		}
		delete(p.conns, id)
	}
	p.mu.Unlock()
	p.wg.Wait()
}

// ComposePaneID is a pane's ID outside its machine.
func ComposePaneID(machine, pane string) string { return machine + ":" + pane }

// SplitPaneID reads one back. A bare pane ID means this computer, so a
// caller from before machines keeps working. The machine is checked here
// because it goes into a map lookup, not into a shell.
func SplitPaneID(ref string) (machine, pane string, err error) {
	machine, pane = Local, ref
	if i := strings.IndexByte(ref, ':'); i >= 0 {
		machine, pane = ref[:i], ref[i+1:]
	}
	switch {
	case !IDOK(machine):
		return "", "", fmt.Errorf("%q does not name a machine", ref)
	case !proto.IsPaneID(pane):
		return "", "", fmt.Errorf("%q is not a pane", ref)
	}
	return machine, pane, nil
}

// MachineOf is the machine a `machine:pane` names; a bare pane is this
// computer's.
func MachineOf(ref string) string {
	if i := strings.IndexByte(ref, ':'); i >= 0 {
		return ref[:i]
	}
	return Local
}

// IDOK is the shape the catalog gives an ID (remote.slug): lower case
// letters, digits and dashes, and never a colon.
func IDOK(id string) bool {
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return id != ""
}

// FirstLine keeps a message to one line: ssh's refusals run to several,
// and this is shown beside a machine's name.
func FirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
