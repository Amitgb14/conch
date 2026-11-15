package desktop

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/fleet"
	"github.com/Amitgb14/conch/internal/proto"
)

// The bridge: what `conch desktop` runs between a desktop app and every
// machine's conch server. It owns the state the app draws — machines,
// projects, panes and their agents — and sends it whole once, then only
// what changed, so three apps in three languages draw the same tree and
// none of them works it out itself. It carries no terminal output: each
// pane's terminal runs the `conch attach` the bridge names for it.

// Machines is where the bridge gets its machines: a fleet.Pool.
type Machines interface {
	Machines() []fleet.Machine
}

// Options configures Serve.
type Options struct {
	Machines Machines
	// Conch is the conch binary the attach commands run: this one.
	Conch string
	// Version is the conch version said in the bridge's hello.
	Version string
	// Every is how often the servers are read for changes (1 s).
	Every time.Duration
	// Wait bounds one read of one machine (5 s), so a machine that stops
	// answering does not hold up the others' changes.
	Wait time.Duration
}

// errNoHello is the answer to anything an app sends before its hello.
var errNoHello = errors.New("say hello first")

// Serve runs the bridge on in and out until in ends (the app went), ctx
// is done, or out cannot be written. An app that closes its end is the
// normal way out and returns nil.
func Serve(ctx context.Context, in io.Reader, out io.Writer, o Options) error {
	if o.Every <= 0 {
		o.Every = time.Second
	}
	if o.Wait <= 0 {
		o.Wait = 5 * time.Second
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	b := &bridge{o: o, w: json.NewEncoder(out), last: map[string]machineRead{}}

	msgs := make(chan Message)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			var m Message
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				_ = b.send(Message{Type: TypeResult, Error: "not a message: " + err.Error()})
				continue
			}
			select {
			case msgs <- m:
			case <-ctx.Done():
				return
			}
		}
		readErr <- sc.Err()
	}()

	// Nothing but hello until hello.
	for b.hello == nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErr:
			return err
		case m := <-msgs:
			if m.Type != TypeHello {
				if err := b.send(Message{Type: TypeResult, ID: m.ID, Error: errNoHello.Error()}); err != nil {
					return err
				}
				continue
			}
			var h AppHello
			if err := json.Unmarshal(m.Data, &h); err != nil {
				if err := b.send(Message{Type: TypeResult, Error: "hello: " + err.Error()}); err != nil {
					return err
				}
				continue
			}
			b.hello = &h
		}
	}
	if err := b.sendData(Message{Type: TypeHello}, BridgeHello{Protocol: Protocol, Conch: o.Version, Capabilities: Capabilities}); err != nil {
		return err
	}

	cur := b.read(ctx)
	if err := b.sendData(Message{Type: TypeSnapshot, Seq: b.next()}, cur); err != nil {
		return err
	}

	tick := time.NewTicker(o.Every)
	defer tick.Stop()
	again := make(chan struct{}, 1) // an intent changed something: look now
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			return err
		case m := <-msgs:
			wg.Add(1)
			go func() {
				defer wg.Done()
				if b.intent(ctx, m) {
					select {
					case again <- struct{}{}:
					default:
					}
				}
			}()
			continue
		case <-tick.C:
		case <-again:
		}
		next := b.read(ctx)
		if p, changed := diff(cur, next); changed {
			if err := b.sendData(Message{Type: TypePatch, Seq: b.next()}, p); err != nil {
				return err
			}
		}
		for _, n := range notices(cur, next) {
			if err := b.sendData(Message{Type: TypeNotify}, n); err != nil {
				return err
			}
		}
		cur = next
	}
}

type bridge struct {
	o     Options
	hello *AppHello

	wmu sync.Mutex
	w   *json.Encoder
	seq int

	// last is each machine's last good read, kept so a machine whose
	// read fails once does not have every pane removed and set again.
	last map[string]machineRead
}

type machineRead struct {
	projects []Project
	panes    []Pane
}

func (b *bridge) next() int { b.seq++; return b.seq }

func (b *bridge) send(m Message) error {
	b.wmu.Lock()
	defer b.wmu.Unlock()
	return b.w.Encode(m)
}

func (b *bridge) sendData(m Message, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.Data = data
	return b.send(m)
}

// read asks every machine that is online for its projects and panes.
func (b *bridge) read(ctx context.Context) State {
	s := State{Machines: []Machine{}, Projects: []Project{}, Panes: []Pane{}}
	for _, m := range b.o.Machines.Machines() {
		s.Machines = append(s.Machines, Machine{ID: m.ID, Label: m.Label, State: m.State, Detail: m.Detail})
		c := m.Client()
		if c == nil {
			continue // its panes are not knowable now, so not shown
		}
		r, err := readMachine(ctx, b.o.Wait, m.ID, c)
		if err != nil {
			r = b.last[m.ID]
		} else {
			b.last[m.ID] = r
		}
		s.Projects = append(s.Projects, r.projects...)
		s.Panes = append(s.Panes, r.panes...)
	}
	return s
}

func readMachine(ctx context.Context, wait time.Duration, machine string, c *client.Client) (machineRead, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	var pl proto.ProjectList
	if err := c.Call(ctx, proto.MethodProjectList, nil, &pl); err != nil {
		return machineRead{}, err
	}
	var panes proto.PaneList
	if err := c.Call(ctx, proto.MethodPaneList, nil, &panes); err != nil {
		return machineRead{}, err
	}
	var r machineRead
	for _, p := range pl.Projects {
		r.projects = append(r.projects, Project{Key: fleet.ComposePaneID(machine, p.ID), Machine: machine, ID: p.ID, Name: p.Name, Path: p.Path})
	}
	for _, p := range panes.Panes {
		r.panes = append(r.panes, paneOf(machine, p))
	}
	return r, nil
}

// paneOf is a server's pane in the app's words.
func paneOf(machine string, p proto.PaneInfo) Pane {
	out := Pane{
		Key: fleet.ComposePaneID(machine, p.ID), Machine: machine, ID: p.ID,
		Name: p.Name, Title: p.Title, Branch: p.Branch, State: p.State, ExitCode: p.ExitCode,
	}
	if p.ProjectID != "" {
		out.Project = fleet.ComposePaneID(machine, p.ProjectID)
	}
	if p.Agent != nil {
		out.Agent = &Agent{Name: p.Agent.Name, State: agentState(p.Agent.State), Since: p.Agent.Since.UTC(), Failed: p.Agent.Failed}
	}
	return out
}

func agentState(s string) string {
	switch s {
	case proto.AgentBlocked:
		return AgentWaiting
	case proto.AgentWorking:
		return AgentWorking
	case proto.AgentDone:
		return AgentDone
	}
	return AgentIdle
}

// diff is the patch that takes a to b, and whether there is one.
func diff(a, b State) (Patch, bool) {
	var p Patch
	p.Machines = changes(a.Machines, b.Machines, func(m Machine) string { return m.ID })
	p.Projects = changes(a.Projects, b.Projects, func(x Project) string { return x.Key })
	p.Panes = changes(a.Panes, b.Panes, func(x Pane) string { return x.Key })
	return p, p.Machines != nil || p.Projects != nil || p.Panes != nil
}

func changes[T any](a, b []T, key func(T) string) *Changes[T] {
	was := make(map[string]T, len(a))
	for _, x := range a {
		was[key(x)] = x
	}
	var c Changes[T]
	seen := make(map[string]bool, len(b))
	for _, x := range b {
		k := key(x)
		seen[k] = true
		if old, ok := was[k]; !ok || !reflect.DeepEqual(old, x) {
			c.Set = append(c.Set, x)
		}
	}
	for _, x := range a {
		if k := key(x); !seen[k] {
			c.Remove = append(c.Remove, k)
		}
	}
	if len(c.Set) == 0 && len(c.Remove) == 0 {
		return nil
	}
	sort.Strings(c.Remove)
	return &c
}

// notices are what the person should hear about between two states: an
// agent that has started waiting, finished, or failed. An agent seen for
// the first time says nothing — it was not a change anybody missed.
func notices(a, b State) []Notify {
	was := make(map[string]*Agent, len(a.Panes))
	for _, p := range a.Panes {
		was[p.Key] = p.Agent
	}
	var out []Notify
	for _, p := range b.Panes {
		old, ok := was[p.Key]
		if !ok || old == nil || p.Agent == nil {
			continue
		}
		name := p.Name
		if name == "" {
			name = p.Agent.Name
		}
		switch {
		case p.Agent.Failed && !old.Failed:
			out = append(out, Notify{Kind: "failed", Pane: p.Key, Title: name + " failed", Body: p.Title})
		case p.Agent.State == AgentWaiting && old.State != AgentWaiting:
			out = append(out, Notify{Kind: AgentWaiting, Pane: p.Key, Title: name + " is waiting for you", Body: p.Title})
		case p.Agent.State == AgentDone && old.State != AgentDone:
			out = append(out, Notify{Kind: AgentDone, Pane: p.Key, Title: name + " is done", Body: p.Title})
		}
	}
	return out
}

// intent does what an app asked and answers it. It says whether the
// state may have changed, so the caller looks again at once.
func (b *bridge) intent(ctx context.Context, m Message) bool {
	reply := func(data any, err error) {
		r := Message{Type: TypeResult, ID: m.ID}
		if err != nil {
			r.Error = err.Error()
			_ = b.send(r)
			return
		}
		r.OK = true
		if data != nil {
			_ = b.sendData(r, data)
			return
		}
		_ = b.send(r)
	}
	if m.Type != TypeIntent {
		reply(nil, fmt.Errorf("the bridge takes intents, not %q", m.Type))
		return false
	}
	switch m.Intent {
	case IntentPaneAttach:
		var ref PaneRef
		if err := json.Unmarshal(m.Data, &ref); err != nil {
			reply(nil, err)
			return false
		}
		a, err := b.attach(ref.Pane)
		reply(a, err)
		return false
	case IntentPaneNew:
		var req PaneNew
		if err := json.Unmarshal(m.Data, &req); err != nil {
			reply(nil, err)
			return false
		}
		machine := req.Machine
		if machine == "" {
			machine = fleet.Local
		}
		c, err := b.client(machine)
		if err != nil {
			reply(nil, err)
			return false
		}
		ctx, cancel := context.WithTimeout(ctx, b.o.Wait)
		defer cancel()
		var info proto.PaneInfo
		err = c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
			Name: req.Name, Agent: req.Agent, Prompt: req.Prompt, Command: req.Command, Cwd: req.Cwd,
			NoProject: req.Cwd == "",
		}, &info)
		if err != nil {
			reply(nil, err)
			return false
		}
		reply(PaneRef{Pane: fleet.ComposePaneID(machine, info.ID)}, nil)
		return true
	case IntentPaneClose:
		var ref PaneRef
		if err := json.Unmarshal(m.Data, &ref); err != nil {
			reply(nil, err)
			return false
		}
		machine, id, err := fleet.SplitPaneID(ref.Pane)
		if err != nil {
			reply(nil, err)
			return false
		}
		c, err := b.client(machine)
		if err != nil {
			reply(nil, err)
			return false
		}
		ctx, cancel := context.WithTimeout(ctx, b.o.Wait)
		defer cancel()
		err = c.Call(ctx, proto.MethodPaneClose, proto.PaneRef{ID: id}, nil)
		reply(nil, err)
		return err == nil
	}
	reply(nil, fmt.Errorf("unknown intent %q", m.Intent))
	return false
}

// client is a machine's connection while it is online.
func (b *bridge) client(machine string) (*client.Client, error) {
	for _, m := range b.o.Machines.Machines() {
		if m.ID != machine {
			continue
		}
		if c := m.Client(); c != nil {
			return c, nil
		}
		if m.Detail != "" {
			return nil, fmt.Errorf("%s is %s: %s", m.Label, m.State, m.Detail)
		}
		return nil, fmt.Errorf("%s is %s", m.Label, m.State)
	}
	return nil, fmt.Errorf("no machine %q", machine)
}

// attach is the command that shows a pane in the app's terminal: this
// conch's attach, through -m for another machine — the same command
// Conch.app runs — behind whatever the app said to wrap commands in.
func (b *bridge) attach(key string) (Attach, error) {
	machine, id, err := fleet.SplitPaneID(key)
	if err != nil {
		return Attach{}, err
	}
	argv := append([]string{}, b.hello.Wrap...)
	argv = append(argv, b.o.Conch)
	if machine != fleet.Local {
		argv = append(argv, "-m", machine)
	}
	argv = append(argv, "attach")
	if b.hello.Raw {
		argv = append(argv, "-raw")
	}
	return Attach{Argv: append(argv, id), Raw: b.hello.Raw}, nil
}
