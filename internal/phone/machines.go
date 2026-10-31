package phone

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/fleet"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// The gateway reaches every machine in the catalog, not only this one,
// through a fleet.Pool (internal/fleet): a client per machine, reached in
// the background so a phone asking for the agent list never waits on ssh,
// and a machine that cannot be reached carrying why rather than taking the
// others with it. A pane is given to the phone as `machine:pane`, and the
// phone sends back what it was given.

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
		tr, err := remote.TransportFor(ctx, m.Label, m.Target, false, m.Opts...)
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

// machineState is a machine as the gateway last saw it.
type machineState = fleet.Machine

// newPool is the gateway's pool, reaching this computer through the
// gateway's own client and everything else through the hooks above.
func (g *Gateway) newPool() *fleet.Pool {
	return fleet.New(fleet.Options{
		Local: func() (*client.Client, error) {
			c, aerr := g.server()
			if aerr != nil {
				return nil, fmt.Errorf("%s", aerr.Message)
			}
			return c, nil
		},
		DialLocal:   func() (*client.Client, error) { return g.dial() },
		Connect:     connectTo,
		Catalog:     catalog,
		Retry:       func() time.Duration { return machineRetry },
		ConnectWait: func() time.Duration { return machineConnectWait },
		Now:         func() time.Time { return g.now() },
		Changed:     g.tellMachines,
		Logf:        func(f string, a ...any) { g.logf("phone: "+f, a...) },
	})
}

// machines returns every machine to show, local first, with the clients of
// those that are up. A machine that is not connected is being connected to
// in the background, so asking again shortly says more.
func (g *Gateway) machines() []machineState { return g.pool.Machines() }

// machinesFor is machines() as one device may see it: this computer, and
// the machines that device has been given. Every list the phone is shown
// is built from this rather than from machines(), so a machine nobody
// granted is not merely un-actionable but invisible — its name, its label
// and whether it is up are the person's business, not the device's.
func (g *Gateway) machinesFor(dev Device) []machineState {
	all := g.machines()
	out := make([]machineState, 0, len(all))
	for _, mc := range all {
		if dev.Reaches(mc.ID) {
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

// tellMachines sends every open socket the machine list, so a phone sees a
// machine come up without asking. Sent from the connecting goroutine, so
// it must not hold the pool's lock while it writes.
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

// phoneNeeds is what the gateway asks of a server for the phone to work
// fully. A machine is reached with its own conch, which may be older than
// this one — a machine that has not been upgraded since, or one somebody
// else keeps — so this is asked of each machine and not of this computer
// once. Only what the phone actually uses belongs here: everything else
// the app does is older than the protocol's capability list.
var phoneNeeds = []string{proto.CapAgentPrompt, proto.CapTaskName, proto.CapReviewComments}

// behindWords says what an older conch there costs the person, in the
// order it will bite them. A capability nobody has a word for is named as
// itself rather than left silent.
func behindWords(missing []string) string {
	var parts []string
	for _, m := range missing {
		switch m {
		case proto.CapAgentPrompt:
			parts = append(parts, "replies")
		case proto.CapTaskName:
			parts = append(parts, "naming a task")
		case proto.CapReviewComments:
			parts = append(parts, "review comments")
		default:
			parts = append(parts, m)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " and ") + " need a newer conch there"
}

// machineList is what the phone is told about every machine, with the
// agents each one contributed and what its own conch cannot do.
func machineList(snaps []machineState, counts map[string]int) []Machine {
	out := make([]Machine, 0, len(snaps))
	for _, mc := range snaps {
		m := Machine{ID: mc.ID, Label: mc.Label, State: mc.State,
			Agents: counts[mc.ID], Detail: mc.Detail}
		if c := mc.Client(); c != nil {
			m.Missing = c.MissingCapabilities(phoneNeeds)
			m.Behind = behindWords(m.Missing)
		}
		out = append(out, m)
	}
	return out
}

// upgradeHere is what to run to make a machine's conch new enough: a
// reload for this computer, an upgrade for anywhere else. Said in the
// refusal, because "reload the server" is the wrong advice for a machine
// the person is not sitting at.
func upgradeHere(machine, label string) string {
	if machine == LocalMachine {
		return "reload it with `conch server reload`"
	}
	return "upgrade it with `conch machine upgrade " + machine + "`"
}

// lacks reports what a machine's conch cannot do, for a refusal that names
// the machine and how to mend it rather than failing when it is tried.
func (g *Gateway) lacks(machine string, c interface {
	MissingCapabilities([]string) []string
}, want ...string) *APIError {
	missing := c.MissingCapabilities(want)
	if len(missing) == 0 {
		return nil
	}
	label := machine
	for _, mc := range g.machines() {
		if mc.ID == machine {
			label = mc.Label
		}
	}
	return apiErr(CodeServerUnavailable, "the conch on "+label+" is older than this one: "+
		behindWords(missing)+", so "+upgradeHere(machine, label))
}

// splitPaneID reads back an ID the phone was given (fleet.SplitPaneID), as
// the error the phone is shown.
func splitPaneID(ref string) (machine, pane string, aerr *APIError) {
	machine, pane, err := fleet.SplitPaneID(ref)
	if err != nil {
		return "", "", apiErr(CodeBadRequest, err.Error())
	}
	return machine, pane, nil
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
	id = fleet.ComposePaneID(machine, pane)
	for _, mc := range g.machines() {
		if mc.ID != machine {
			continue
		}
		if c := mc.Client(); c != nil {
			return c, pane, id, nil
		}
		if mc.State == fleet.Connecting {
			return nil, "", "", apiErr(CodeServerUnavailable, fmt.Sprintf("%s is being reached; try again in a moment", mc.Label))
		}
		detail := ""
		if mc.Detail != "" {
			detail = ": " + mc.Detail
		}
		return nil, "", "", apiErr(CodeServerUnavailable, fmt.Sprintf("%s is not answering%s", mc.Label, detail))
	}
	return nil, "", "", apiErr(CodeNotFound, fmt.Sprintf("no machine %q", machine))
}

// machineUp reports whether the gateway itself has a machine, which is
// what a socket needs before it can have one of its own.
func (g *Gateway) machineUp(machine string) bool {
	for _, mc := range g.machines() {
		if mc.ID == machine {
			return mc.Client() != nil
		}
	}
	return false
}

// machineWhy is why a machine cannot be used, in the words the phone
// shows: being reached, not answering with ssh's own reason, or gone from
// the catalog. A phone that is told "no connection yet" and nothing more
// cannot tell a machine that is coming from one that never will.
func (g *Gateway) machineWhy(machine string) string {
	for _, mc := range g.machines() {
		if mc.ID != machine {
			continue
		}
		switch {
		case mc.State == fleet.Connecting:
			return mc.Label + " is being reached; try again in a moment"
		case mc.Detail != "":
			return mc.Label + " is not answering: " + mc.Detail
		case mc.Client() == nil:
			return mc.Label + " is not answering"
		}
		return mc.Label + " has not answered this phone yet; try again in a moment"
	}
	return "no machine " + machine
}

// machineJoinWait is how long a message that needs another machine waits
// for this socket's own connection to it, and machineJoinPoll how often
// it looks. Long enough for ssh on a connection the gateway already has
// open (its master is up, so this is a channel on it), short enough that
// a phone is never left holding a tap.
var (
	machineJoinWait = 6 * time.Second
	machineJoinPoll = 100 * time.Millisecond
)

// closeMachines drops every remote connection and waits for the attempts
// in flight, so a closed gateway leaves no ssh behind.
func (g *Gateway) closeMachines() { g.pool.Close() }

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
		c := mc.Client()
		if c == nil {
			continue
		}
		list, err := agentList(ctx, mc.ID, c)
		if err != nil {
			// One machine's list failing is that machine's problem; the
			// others still answer, and it carries the reason.
			g.noteMachine(mc.ID, err)
			continue
		}
		counts[mc.ID] = len(list)
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
		c := mc.Client()
		if c == nil {
			continue
		}
		list, err := paneList(ctx, mc.ID, c)
		if err != nil {
			g.noteMachine(mc.ID, err)
			continue
		}
		for _, pn := range list {
			if pn.Kind == KindAgent {
				counts[mc.ID]++
			}
		}
		panes = append(panes, list...)
	}
	return panes, machineList(conns, counts)
}

// noteMachine records why a machine's list could not be read.
func (g *Gateway) noteMachine(id string, err error) { g.pool.Note(id, err) }

// dialMachine gives a socket its own connection to a machine. A socket
// follows events, so it cannot share the gateway's client: that one's
// events are drained and thrown away.
func (g *Gateway) dialMachine(id string) (*client.Client, error) { return g.pool.Dial(id) }

// machineFor is the client of a machine a request names, "" being this
// computer: what the routes that *make* something on a machine use, where
// paneOn is for the ones that act on a pane that exists.
func (g *Gateway) machineFor(dev Device, id string) (*client.Client, string, *APIError) {
	if id == "" {
		id = LocalMachine
	}
	if !fleet.IDOK(id) {
		return nil, "", apiErr(CodeBadRequest, fmt.Sprintf("%q does not name a machine", id))
	}
	if aerr := g.reaches(dev, id); aerr != nil {
		return nil, "", aerr
	}
	for _, mc := range g.machines() {
		if mc.ID != id {
			continue
		}
		if c := mc.Client(); c != nil {
			return c, id, nil
		}
		if mc.State == fleet.Connecting {
			return nil, "", apiErr(CodeServerUnavailable, fmt.Sprintf("%s is being reached; try again in a moment", mc.Label))
		}
		return nil, "", apiErr(CodeServerUnavailable, fmt.Sprintf("%s is not answering", mc.Label))
	}
	return nil, "", apiErr(CodeNotFound, fmt.Sprintf("no machine %q", id))
}
