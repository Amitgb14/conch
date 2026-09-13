package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

type machineState int

const (
	stateConnecting machineState = iota
	stateOnline
	stateOffline
	stateAttention // needs the user: install, upgrade or an ssh prompt
)

func (s machineState) String() string {
	switch s {
	case stateConnecting:
		return "connecting"
	case stateOnline:
		return "online"
	case stateOffline:
		return "offline"
	}
	return "needs attention"
}

const (
	retryMin = 2 * time.Second
	retryMax = time.Minute
)

// machine is one conch server the TUI shows: this computer or a remote one.
// While disconnected it keeps the last panes and projects it saw, shown
// dimmed.
type machine struct {
	id     string
	label  string
	target string // ssh target; "" for this computer

	c       *client.Client
	server  proto.HelloResult
	state   machineState
	err     string // why it is offline or needs attention
	warning string // online but degraded, e.g. an outdated server

	panes    []proto.PaneInfo
	projects []proto.ProjectInfo
	agents   map[string]bool   // panes that have run an agent
	sizes    map[string][2]int // last size sent per pane

	// Which agents are installed there; nil until known (or when the server
	// can't tell).
	available map[string]proto.AgentAvailability
	agentList []proto.AgentAvailability // the same, in the server's order
	// installers maps panes running an agent installer to the agent.
	installers map[string]string
	// limits are the plan limits agents there last reported, by agent.
	limits map[string]proto.PlanLimits

	// gen increases with every connection attempt; messages from older
	// attempts are ignored.
	gen      int
	failures int
}

type (
	machineEventMsg struct {
		machine string
		gen     int
		msg     proto.Message
	}
	machineClosedMsg struct {
		machine string
		gen     int
		err     error
	}
	machineRetryMsg struct {
		machine string
		gen     int
	}
	machineConnectedMsg struct {
		machine string
		gen     int
		c       *client.Client
		err     error
	}
	machineAddedMsg struct{ m remote.Machine }
	panesMsg        struct {
		machine string
		gen     int
		panes   []proto.PaneInfo
	}
	projectsMsg struct {
		machine  string
		gen      int
		projects []proto.ProjectInfo
	}
	limitsMsg struct {
		machine string
		gen     int
		limits  []proto.PlanLimits
	}
	agentStatusMsg struct {
		machine string
		gen     int
		agents  []proto.AgentAvailability
	}
)

func newMachine(id, label, target string) *machine {
	return &machine{id: id, label: label, target: target, agents: map[string]bool{}, sizes: map[string][2]int{},
		installers: map[string]string{}}
}

// attach adopts a connected client.
func (mach *machine) attach(c *client.Client) {
	mach.gen++
	mach.c, mach.server = c, c.Server
	mach.state, mach.err, mach.warning, mach.failures = stateOnline, "", "", 0
	mach.sizes = map[string][2]int{} // a new connection may see new sizes
}

// listen starts receiving events and loads the machine's state.
func (mach *machine) listen() []tea.Cmd {
	c, id, gen := mach.c, mach.id, mach.gen
	return []tea.Cmd{
		mach.waitEvent(),
		mach.checkAgents(),
		func() tea.Msg {
			if len(c.MissingCapabilities([]string{"agent.limits.v1"})) > 0 {
				return nil
			}
			var res proto.AgentLimitsResult
			if err := callCtx(c, proto.MethodAgentLimits, nil, &res); err != nil {
				return nil
			}
			return limitsMsg{machine: id, gen: gen, limits: res.Limits}
		},
		func() tea.Msg {
			var list proto.PaneList
			if err := callCtx(c, proto.MethodPaneList, nil, &list); err != nil {
				return nil // the connection's closing reports the problem
			}
			return panesMsg{machine: id, gen: gen, panes: list.Panes}
		},
		func() tea.Msg {
			var list proto.ProjectList
			if err := callCtx(c, proto.MethodProjectList, nil, &list); err != nil {
				return nil
			}
			return projectsMsg{machine: id, gen: gen, projects: list.Projects}
		},
	}
}

// checkAgents asks which agents are installed there. Servers from before
// agent installs can't say; the machine then just tries to launch them.
func (mach *machine) checkAgents() tea.Cmd {
	c, id, gen := mach.c, mach.id, mach.gen
	if c == nil || len(c.MissingCapabilities([]string{"agent.install.v1"})) > 0 {
		return nil
	}
	return func() tea.Msg {
		var res proto.AgentStatusResult
		if err := callCtx(c, proto.MethodAgentStatus, nil, &res); err != nil {
			return nil
		}
		return agentStatusMsg{machine: id, gen: gen, agents: res.Agents}
	}
}

// missingAgent reports whether agent is known not to be installed.
func (mach *machine) missingAgent(agent string) bool {
	if mach.available == nil {
		return false
	}
	return !mach.available[agent].Installed
}

func (mach *machine) waitEvent() tea.Cmd {
	c, id, gen := mach.c, mach.id, mach.gen
	if c == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-c.Events
		if !ok {
			return machineClosedMsg{machine: id, gen: gen, err: c.Err()}
		}
		return machineEventMsg{machine: id, gen: gen, msg: msg}
	}
}

// connect tries to reach the machine's server. With install set (the user
// asked), a remote machine gets conch installed or upgraded and this
// computer's server is started if it isn't running; background attempts do
// neither.
func (mach *machine) connect(install bool) tea.Cmd {
	mach.gen++
	mach.state = stateConnecting
	id, gen, target := mach.id, mach.gen, mach.target
	return func() tea.Msg {
		if target == "" {
			sock := config.SocketPath()
			if install {
				if err := client.EnsureServer(sock, config.ServerLogPath()); err != nil {
					return machineConnectedMsg{machine: id, gen: gen, err: err}
				}
			}
			c, err := client.Dial(sock, "conch-tui")
			return machineConnectedMsg{machine: id, gen: gen, c: c, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		c, err := remote.Connect(ctx, target, remote.Options{Install: install})
		return machineConnectedMsg{machine: id, gen: gen, c: c, err: err}
	}
}

// connected applies the outcome of a connection attempt.
func (mach *machine) connected(msg machineConnectedMsg) tea.Cmd {
	var outdated *remote.OutdatedServerError
	var needs *remote.InstallError
	switch {
	case msg.err == nil || (errors.As(msg.err, &outdated) && msg.c != nil):
		mach.attach(msg.c)
		if outdated != nil {
			mach.warning = "server is from an older build · m → restart server"
		}
		return tea.Batch(mach.listen()...)
	case errors.As(msg.err, &needs):
		mach.state, mach.err = stateAttention, needs.Reason
		return nil // installing is the user's call
	}
	mach.state, mach.err = stateOffline, msg.err.Error()
	mach.failures++
	return mach.scheduleRetry()
}

// lost marks a dropped connection. Panes and projects stay as last seen.
func (mach *machine) lost(err error) {
	mach.c = nil
	mach.gen++
	mach.state = stateOffline
	mach.err = "connection lost"
	if err != nil && !errors.Is(err, client.ErrClosed) {
		mach.err += ": " + err.Error()
	}
}

// scheduleRetry reconnects after a backoff that grows with failures.
func (mach *machine) scheduleRetry() tea.Cmd {
	delay := retryMin << min(mach.failures, 5)
	delay = min(delay, retryMax)
	id, gen := mach.id, mach.gen
	return tea.Tick(delay, func(time.Time) tea.Msg { return machineRetryMsg{machine: id, gen: gen} })
}

func (mach *machine) close() {
	mach.gen++
	if mach.c != nil {
		mach.c.Close()
		mach.c = nil
	}
}

func (mach *machine) setPanes(panes []proto.PaneInfo) {
	mach.panes = panes
	for _, p := range panes {
		if p.Agent != nil {
			mach.agents[p.ID] = true
		}
	}
}

func (mach *machine) pane(id string) *proto.PaneInfo {
	if i := mach.paneIndex(id); i >= 0 {
		return &mach.panes[i]
	}
	return nil
}

func (mach *machine) paneIndex(id string) int {
	for i, p := range mach.panes {
		if p.ID == id {
			return i
		}
	}
	return -1
}

// addMachine connects to target, installing conch there if needed (the
// user asked to add it), and saves it.
func addMachine(target, label string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		c, err := remote.Connect(ctx, target, remote.Options{Install: true})
		var outdated *remote.OutdatedServerError
		if err != nil && !errors.As(err, &outdated) {
			return errMsg{err}
		}
		c.Close()
		m, err := remote.SaveMachine(remote.Machine{Label: label, Target: target})
		if err != nil {
			return errMsg{err}
		}
		return machineAddedMsg{m: m}
	}
}
