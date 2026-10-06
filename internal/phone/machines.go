package phone

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// The gateway reaches every machine in the catalog, not only this one, by
// keeping a client per machine as the TUI does. Three things make that
// different from the TUI's version:
//
//   - A phone asking for the agent list must not wait on ssh. Reaching a
//     machine takes seconds at best, and a minute when conch has to be
//     installed there, so a machine is connected to in the background and
//     the list says what state it is in. The phone draws a machine that is
//     `connecting` and fills it in when it answers.
//   - A machine that cannot be reached must not take the others with it.
//     Every list is built from the machines that answered, and the machine
//     itself carries why it did not, so nothing is silently missing.
//   - A pane ID is the server's own, so `p3` exists on every machine. The
//     phone is given `machine:pane` and sends back what it was given.

// machineRetry is how long before a machine that would not answer is tried
// again. Tests shorten it.
var machineRetry = 30 * time.Second

// The two ways out of this package, behind a lock because a test replaces
// them while a gateway is running and its connecting goroutines read them:
// machineConnect is the one thing here that would otherwise run ssh, and
// savedMachines is the catalog on disk.
var (
	hooksMu        sync.RWMutex
	machineConnect = func(ctx context.Context, m remote.Machine) (*client.Client, error) {
		tr, err := remote.TransportFor(ctx, m.Label, m.Target, false)
		if err != nil {
			return nil, err
		}
		return remote.Connect(ctx, tr, remote.Options{})
	}
	savedMachines = remote.Machines
)

// connectTo and catalog read those hooks under the lock.
func connectTo(ctx context.Context, m remote.Machine) (*client.Client, error) {
	hooksMu.RLock()
	connect := machineConnect
	hooksMu.RUnlock()
	return connect(ctx, m)
}

func catalog() ([]remote.Machine, error) {
	hooksMu.RLock()
	saved := savedMachines
	hooksMu.RUnlock()
	return saved()
}

// setMachineHooks replaces them and returns what was there, for tests.
func setMachineHooks(connect func(context.Context, remote.Machine) (*client.Client, error),
	saved func() ([]remote.Machine, error)) (func(context.Context, remote.Machine) (*client.Client, error), func() ([]remote.Machine, error)) {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	oldConnect, oldSaved := machineConnect, savedMachines
	if connect != nil {
		machineConnect = connect
	}
	if saved != nil {
		savedMachines = saved
	}
	return oldConnect, oldSaved
}

// machineConn is one machine and what the gateway knows about it.
type machineConn struct {
	id, label string
	c         *client.Client
	err       error     // why it is not reachable
	tried     time.Time // when it was last attempted
	busy      bool      // an attempt is running
}

// stateLocked is what the phone is told, which is the only place these
// words are decided. The caller holds machMu.
func (mc *machineConn) stateLocked() string {
	switch {
	case mc.c != nil && mc.c.Err() == nil:
		return MachineOnline
	case mc.busy:
		return MachineConnecting
	}
	return MachineOffline
}

// machineState is a machine as it was when it was looked at: a snapshot,
// not the live machineConn, because reaching a machine happens in another
// goroutine and a pointer handed out here would be read while it is
// written. The client is safe to carry — it is not replaced once set,
// only closed, and a closed one reports its own error.
type machineState struct {
	id, label string
	c         *client.Client
	state     string
	detail    string
}

// client is the connection while it is good.
func (m machineState) client() *client.Client {
	if m.c != nil && m.c.Err() == nil {
		return m.c
	}
	return nil
}

// snapLocked takes the snapshot. The caller holds machMu.
func (mc *machineConn) snapLocked() machineState {
	m := machineState{id: mc.id, label: mc.label, c: mc.c, state: mc.stateLocked()}
	if m.state == MachineOffline && mc.err != nil {
		m.detail = firstLine(mc.err.Error())
	}
	return m
}

// machines returns every machine to show, local first, with the clients of
// those that are up. A machine that is not connected is being connected to
// in the background, so asking again shortly says more.
func (g *Gateway) machines() []machineState {
	g.machMu.Lock()
	defer g.machMu.Unlock()
	if g.conns == nil {
		g.conns = map[string]*machineConn{}
	}
	local := g.conns[LocalMachine]
	if local == nil {
		local = &machineConn{id: LocalMachine, label: "this computer"}
		g.conns[LocalMachine] = local
	}
	// Local is this process's own socket: dialling it is cheap and
	// immediate, so it is done here rather than in the background.
	if c, aerr := g.server(); aerr == nil {
		local.c, local.err = c, nil
	} else {
		local.c, local.err = nil, fmt.Errorf("%s", aerr.Message)
	}
	out := []machineState{}

	saved, err := catalog()
	if err != nil {
		// The catalog cannot be read: say so against this computer rather
		// than inventing machines.
		g.logf("phone: machines: %v", err)
		return append(out, local.snapLocked())
	}
	seen := map[string]bool{LocalMachine: true}
	for _, m := range saved {
		if !m.Enabled || m.ID == LocalMachine || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		mc := g.conns[m.ID]
		if mc == nil {
			mc = &machineConn{id: m.ID, label: m.Label}
			g.conns[m.ID] = mc
		}
		mc.label = m.Label
		g.ensureLocked(mc, m)
		out = append(out, mc.snapLocked())
	}
	// Machines no longer in the catalog take their connections with them.
	for id, mc := range g.conns {
		if !seen[id] {
			if mc.c != nil {
				mc.c.Close()
			}
			delete(g.conns, id)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].label < out[j].label })
	// This computer is always first, and always listed.
	return append([]machineState{local.snapLocked()}, out...)
}

// machinesFor is machines() as one device may see it: this computer, and
// the machines that device has been given. Every list the phone is shown
// is built from this rather than from machines(), so a machine nobody
// granted is not merely un-actionable but invisible — its name, its label
// and whether it is up are the person's business, not the device's.
func (g *Gateway) machinesFor(dev Device) []machineState {
	all := g.machines()
	out := make([]machineState, 0, len(all))
	for _, mc := range all {
		if dev.Reaches(mc.id) {
			out = append(out, mc)
		}
	}
	return out
}

// reaches refuses a machine a device was not given, and says how it would
// be. The refusal names the machine the phone asked for — it sent the
// name, so nothing is disclosed — and the command, because a device that
// cannot reach a machine looks exactly like one whose machine is off.
func (g *Gateway) reaches(dev Device, machine string) *APIError {
	if dev.Reaches(machine) {
		return nil
	}
	return apiErr(CodeForbidden, fmt.Sprintf(
		"this device reaches this computer only; `conch web permission %s %s -machine %s` adds %s to it",
		dev.ID, dev.Permission, machine, machine))
}

// ensureLocked starts reaching a machine that is not up, unless one is
// already being started or it failed too recently to be worth retrying.
// The caller holds machMu.
func (g *Gateway) ensureLocked(mc *machineConn, m remote.Machine) {
	if (mc.c != nil && mc.c.Err() == nil) || mc.busy {
		return
	}
	if mc.c != nil { // it dropped: forget it before trying again
		mc.c.Close()
		mc.c = nil
	}
	if !mc.tried.IsZero() && g.now().Sub(mc.tried) < machineRetry {
		return
	}
	mc.busy, mc.tried = true, g.now()
	g.machWG.Add(1)
	go func() {
		defer g.machWG.Done()
		changed := false
		defer func() {
			if changed {
				g.tellMachines()
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), machineConnectWait)
		defer cancel()
		c, err := connectTo(ctx, m)
		g.machMu.Lock()
		defer g.machMu.Unlock()
		mc.busy = false
		switch {
		case err != nil:
			mc.c, mc.err = nil, err
			g.logf("phone: %s: %v", m.ID, err)
		case g.closed:
			c.Close() // nobody left to serve
		default:
			mc.c, mc.err = c, nil
			// Nothing here follows a machine's events outside a socket;
			// keep the queue empty so the client does not block.
			go func() {
				for range c.Events {
				}
			}()
			changed = true
		}
	}()
}

// tellMachines sends every open socket the machine list, so a phone sees a
// machine come up without asking. Sent from the connecting goroutine, so
// it must not hold machMu while it writes.
func (g *Gateway) tellMachines() {
	g.mu.Lock()
	socks := make([]*socket, 0, len(g.sockets))
	for s := range g.sockets {
		socks = append(socks, s)
	}
	g.mu.Unlock()
	if len(socks) == 0 {
		return
	}
	list := machineList(g.machines(), nil)
	for _, s := range socks {
		s.send(ServerMessage{Type: MsgMachines, Machines: &list})
	}
}

// machineConnectWait bounds one attempt at a machine. Long enough for ssh
// and a server starting there, short enough that a machine which is simply
// off does not sit as "connecting" for ever.
var machineConnectWait = 45 * time.Second

// machineList is what the phone is told about every machine, with the
// agents each one contributed.
func machineList(snaps []machineState, counts map[string]int) []Machine {
	out := make([]Machine, 0, len(snaps))
	for _, mc := range snaps {
		out = append(out, Machine{ID: mc.id, Label: mc.label, State: mc.state,
			Agents: counts[mc.id], Detail: mc.detail})
	}
	return out
}

// firstLine keeps a message to one line: ssh's refusals run to several, and
// the phone shows this beside a machine's name.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// composePaneID is the ID the phone is given: the machine it is on and the pane
// its own server calls it.
func composePaneID(machine, pane string) string { return machine + ":" + pane }

// splitPaneID reads one back. A bare pane ID means this computer, so an app
// built before machines keeps working. The machine is validated here
// because it goes into a map lookup, not into a shell.
func splitPaneID(ref string) (machine, pane string, err *APIError) {
	machine, pane = LocalMachine, ref
	if i := strings.IndexByte(ref, ':'); i >= 0 {
		machine, pane = ref[:i], ref[i+1:]
	}
	switch {
	case machine == "" || !machineIDOK(machine):
		return "", "", apiErr(CodeBadRequest, fmt.Sprintf("%q does not name a machine", ref))
	case !proto.IsPaneID(pane):
		return "", "", apiErr(CodeBadRequest, fmt.Sprintf("%q is not a pane", ref))
	}
	return machine, pane, nil
}

// machineOf is the machine a `machine:pane` names, for deciding what a
// device may be told rather than what it asked for: a reference with no
// machine in it is this computer's, as it is everywhere else.
func machineOf(ref string) string {
	if i := strings.IndexByte(ref, ':'); i >= 0 {
		return ref[:i]
	}
	return LocalMachine
}

// machineIDOK is the shape the catalog gives an ID (remote.slug): lower
// case letters, digits and dashes, and never a colon.
func machineIDOK(id string) bool {
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return id != ""
}

// paneOn resolves an ID the phone sent to the machine's client, the pane ID
// that machine's own server knows, and the canonical `machine:pane` — a
// bare `p3` is accepted and canonicalised here, so everything downstream
// keys panes one way. A machine the gateway does not reach is not_found, as
// an unknown pane is; one that is merely not up yet says so as itself,
// since trying again shortly is the thing to do.
func (g *Gateway) paneOn(dev Device, ref string) (c *client.Client, pane, id string, aerr *APIError) {
	machine, pane, aerr := splitPaneID(ref)
	if aerr != nil {
		return nil, "", "", aerr
	}
	if aerr := g.reaches(dev, machine); aerr != nil {
		return nil, "", "", aerr
	}
	id = composePaneID(machine, pane)
	for _, mc := range g.machines() {
		if mc.id != machine {
			continue
		}
		if c := mc.client(); c != nil {
			return c, pane, id, nil
		}
		if mc.state == MachineConnecting {
			return nil, "", "", apiErr(CodeServerUnavailable, fmt.Sprintf("%s is being reached; try again in a moment", mc.label))
		}
		detail := ""
		if mc.detail != "" {
			detail = ": " + mc.detail
		}
		return nil, "", "", apiErr(CodeServerUnavailable, fmt.Sprintf("%s is not answering%s", mc.label, detail))
	}
	return nil, "", "", apiErr(CodeNotFound, fmt.Sprintf("no machine %q", machine))
}

// closeMachines drops every remote connection and waits for the attempts
// in flight, so a closed gateway leaves no ssh behind.
func (g *Gateway) closeMachines() {
	g.machMu.Lock()
	for id, mc := range g.conns {
		if id != LocalMachine && mc.c != nil {
			mc.c.Close()
		}
		delete(g.conns, id)
	}
	g.machMu.Unlock()
	g.machWG.Wait()
}

// everyAgent is every reachable machine's agents in one list, waiting
// first and longest wait first across all of them: the agent that has
// waited longest is first wherever it runs. A machine that is not up
// contributes none and does not fail the list — it says for itself what
// is wrong (machineList).
func (g *Gateway) everyAgent(ctx context.Context, dev Device) ([]Agent, []Machine) {
	conns := g.machinesFor(dev)
	agents := []Agent{}
	counts := map[string]int{}
	for _, mc := range conns {
		c := mc.client()
		if c == nil {
			continue
		}
		list, err := agentList(ctx, mc.id, c)
		if err != nil {
			// One machine's list failing is that machine's problem; the
			// others still answer, and it carries the reason.
			g.noteMachine(mc.id, err)
			continue
		}
		counts[mc.id] = len(list)
		agents = append(agents, list...)
	}
	sortAgents(agents)
	return agents, machineList(conns, counts)
}

// everyPane is everyAgent for panes, agents before terminals as one
// machine's list has them, machine by machine.
func (g *Gateway) everyPane(ctx context.Context, dev Device) ([]Pane, []Machine) {
	conns := g.machinesFor(dev)
	panes := []Pane{}
	counts := map[string]int{}
	for _, mc := range conns {
		c := mc.client()
		if c == nil {
			continue
		}
		list, err := paneList(ctx, mc.id, c)
		if err != nil {
			g.noteMachine(mc.id, err)
			continue
		}
		for _, pn := range list {
			if pn.Kind == KindAgent {
				counts[mc.id]++
			}
		}
		panes = append(panes, list...)
	}
	return panes, machineList(conns, counts)
}

// noteMachine records why a machine's list could not be read, and drops a
// connection that has gone so the next look reaches for it again.
func (g *Gateway) noteMachine(id string, err error) {
	g.machMu.Lock()
	defer g.machMu.Unlock()
	mc := g.conns[id]
	if mc == nil {
		return
	}
	mc.err = err
	if mc.c != nil && mc.c.Err() != nil {
		mc.c.Close()
		mc.c = nil
	}
	g.logf("phone: %s: %v", id, err)
}

// dialMachine gives a socket its own connection to a machine. A socket
// follows events, so it cannot share the gateway's client: that one's
// events are drained and thrown away. Local is this process's socket;
// anything else rides the connection the gateway already has, which is why
// it must be up first (machines).
func (g *Gateway) dialMachine(id string) (*client.Client, error) {
	if id == LocalMachine {
		return g.dial()
	}
	up := false
	for _, mc := range g.machines() {
		if mc.id == id && mc.client() != nil {
			up = true
		}
	}
	if !up {
		return nil, fmt.Errorf("%s is not connected", id)
	}
	saved, err := catalog()
	if err != nil {
		return nil, err
	}
	for _, m := range saved {
		if m.ID != id {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), machineConnectWait)
		defer cancel()
		return connectTo(ctx, m)
	}
	return nil, fmt.Errorf("no machine %q", id)
}

// machineFor is the client of a machine a request names, "" being this
// computer: what the routes that *make* something on a machine use, where
// paneOn is for the ones that act on a pane that exists.
func (g *Gateway) machineFor(dev Device, id string) (*client.Client, string, *APIError) {
	if id == "" {
		id = LocalMachine
	}
	if !machineIDOK(id) {
		return nil, "", apiErr(CodeBadRequest, fmt.Sprintf("%q does not name a machine", id))
	}
	if aerr := g.reaches(dev, id); aerr != nil {
		return nil, "", aerr
	}
	for _, mc := range g.machines() {
		if mc.id != id {
			continue
		}
		if c := mc.client(); c != nil {
			return c, id, nil
		}
		if mc.state == MachineConnecting {
			return nil, "", apiErr(CodeServerUnavailable, fmt.Sprintf("%s is being reached; try again in a moment", mc.label))
		}
		return nil, "", apiErr(CodeServerUnavailable, fmt.Sprintf("%s is not answering", mc.label))
	}
	return nil, "", apiErr(CodeNotFound, fmt.Sprintf("no machine %q", id))
}
