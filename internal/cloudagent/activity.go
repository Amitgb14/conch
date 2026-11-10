package cloudagent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/proto"
)

// Activity (PROTOCOL §5): the control plane decides when the machine
// sleeps from what the VM says its agents are doing, so the VM says it
// whenever it changes and at least every minute — a machine that stops
// reporting for five is treated as idle. The agents' states are conch's
// own (internal/detect, as the server reports them); clients are the
// tunnel's sessions; and processes_busy is a terminal with something other
// than a shell in front — a build, a test run, a dev server somebody
// started — which an idle agent does not make idle.

// Variables so a test waits less.
var (
	heartbeat   = 30 * time.Second // well inside the contract's 60 s
	minReport   = time.Second      // the contract's most: one a second
	busyEvery   = 5 * time.Second  // a terminal's foreground is polled
	watchEvery  = 5 * time.Second  // boot.json and the clock
	sleptAfter  = 20 * time.Second // a tick this late means the machine was paused
	reportRetry = 5 * time.Second
)

// foreground is the program in front of a pane's terminal; a variable so
// a test can say.
var foreground = detect.TerminalForeground

// shells are programs whose being in front means nothing is running.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true,
	"mksh": true, "tcsh": true, "csh": true, "nu": true, "elvish": true, "xonsh": true, "login": true}

func isShell(p detect.Process) bool {
	for _, n := range p.Names() {
		if shells[strings.TrimPrefix(filepath.Base(n), "-")] {
			return true
		}
	}
	return false
}

// event takes one of the server's events.
func (a *Agent) event(ctx context.Context, c *client.Client, msg proto.Message) {
	switch msg.Event {
	case proto.EventPaneCreated, proto.EventPaneUpdated:
		var p proto.PaneInfo
		if json.Unmarshal(msg.Data, &p) != nil || p.ID == "" {
			return
		}
		a.mu.Lock()
		was := a.panes[p.ID]
		a.panes[p.ID] = p
		_, known := a.projects[p.ProjectID]
		a.mu.Unlock()
		if p.ProjectID != "" && !known {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if names, err := projectNames(cctx, c); err == nil {
				a.mu.Lock()
				a.projects = names
				a.mu.Unlock()
			}
			cancel()
		}
		if agentState(p) == "waiting" && agentState(was) != "waiting" {
			a.pushWaiting(ctx, p)
		}
	case proto.EventPaneExited, proto.EventPaneClosed:
		var ref struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(msg.Data, &ref) != nil {
			return
		}
		a.mu.Lock()
		delete(a.panes, ref.ID)
		a.mu.Unlock()
	default:
		return
	}
	a.kick()
}

// agentState is the contract's state for the agent in p, "" for no agent.
func agentState(p proto.PaneInfo) string {
	if p.State != proto.PaneRunning || p.Agent == nil {
		return ""
	}
	return stateFor(p.Agent.State)
}

// activity is the report as things are now.
func (a *Agent) activity() VMActivity {
	a.mu.Lock()
	defer a.mu.Unlock()
	act := VMActivity{Agents: []AgentActivity{}, ProcessesBusy: a.busy}
	for _, p := range a.panes {
		if st := agentState(p); st != "" {
			act.Agents = append(act.Agents, AgentActivity{Agent: p.Agent.Name, State: st, Project: a.projects[p.ProjectID]})
		}
	}
	slices.SortFunc(act.Agents, func(x, y AgentActivity) int {
		return strings.Compare(x.Project+"\x00"+x.Agent+"\x00"+x.State, y.Project+"\x00"+y.Agent+"\x00"+y.State)
	})
	if a.link != nil {
		act.ClientsAttached = a.link.Status().Phones
	}
	return act
}

// busyNow says whether any terminal that is not an agent's has something
// other than a shell in front.
func (a *Agent) busyNow() bool {
	a.mu.Lock()
	var pids []int
	for _, p := range a.panes {
		if p.State == proto.PaneRunning && p.Agent == nil && p.PID > 0 {
			pids = append(pids, p.PID)
		}
	}
	a.mu.Unlock()
	for _, pid := range pids {
		fg, err := foreground(pid)
		if err == nil && !isShell(fg) {
			return true
		}
	}
	return false
}

// reportLoop sends activity on change and on the heartbeat, and carries
// out what the control plane answers with.
func (a *Agent) reportLoop(ctx context.Context) error {
	beat := time.NewTicker(heartbeat)
	defer beat.Stop()
	poll := time.NewTicker(busyEvery)
	defer poll.Stop()
	var last *VMActivity
	var sent time.Time
	retry := (<-chan time.Time)(nil)
	for {
		force := false
		select {
		case <-ctx.Done():
			return nil
		case <-a.changed:
		case <-beat.C:
			force = true
		case <-retry:
			force = true
			retry = nil
		case <-poll.C:
			busy := a.busyNow()
			a.mu.Lock()
			moved := busy != a.busy
			a.busy = busy
			a.mu.Unlock()
			if !moved {
				continue
			}
		}
		act := a.activity()
		if !force && last != nil && sameActivity(*last, act) {
			continue
		}
		if wait := minReport - time.Since(sent); wait > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(wait):
			}
			act = a.activity()
		}
		sent = time.Now()
		cmds, err := a.api.Activity(ctx, act)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, ErrUnauthorized) {
				if !a.newToken() {
					return err
				}
				// A new inject while this process kept running: its
				// processes are where they were.
				if err := a.hello(ctx, WokeMemory); err != nil {
					return err
				}
				continue
			}
			a.logf("cloud: activity report: %v", err)
			retry = time.After(reportRetry)
			continue
		}
		last = &act
		for _, c := range cmds {
			a.command(ctx, c)
		}
	}
}

func sameActivity(x, y VMActivity) bool {
	return x.ClientsAttached == y.ClientsAttached && x.ProcessesBusy == y.ProcessesBusy && slices.Equal(x.Agents, y.Agents)
}

// command carries out one of the control plane's commands; one this agent
// does not know is ignored, as the contract says.
func (a *Agent) command(ctx context.Context, c VMCommand) {
	switch c.Type {
	case CommandPauseAgents:
		n := a.pauseAgents(ctx)
		a.logf("cloud: paused %d working agent(s): %s", n, c.Reason)
	default:
		a.logf("cloud: ignoring command %q", c.Type)
	}
}

// pauseAgents sends Esc to every agent that is working — what a person
// would press to stop one — and says how many it reached.
func (a *Agent) pauseAgents(ctx context.Context) int {
	c := a.server()
	if c == nil {
		return 0
	}
	a.mu.Lock()
	var ids []string
	for _, p := range a.panes {
		if agentState(p) == "working" {
			ids = append(ids, p.ID)
		}
	}
	a.mu.Unlock()
	slices.Sort(ids)
	n := 0
	for _, id := range ids {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := c.Call(cctx, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: id, Keys: []string{"esc"}}, nil)
		cancel()
		if err != nil {
			a.logf("cloud: pause %s: %v", id, err)
			continue
		}
		n++
	}
	return n
}

// watchLoop notices what nobody tells the agent: the machine having been
// paused and resumed with its memory (PROTOCOL §3.1 asks for a hello after
// every wake, and nothing else says one happened), and a new boot.json.
func (a *Agent) watchLoop(ctx context.Context) error {
	t := time.NewTicker(watchEvery)
	defer t.Stop()
	prev := time.Now()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			now := time.Now()
			if slept(prev, now, watchEvery) {
				a.logf("cloud: the machine was paused for %s; saying hello again", now.Round(0).Sub(prev.Round(0)).Round(time.Second))
				if err := a.hello(ctx, WokeMemory); err != nil {
					return err
				}
			} else if a.newToken() {
				if err := a.hello(ctx, WokeMemory); err != nil {
					return err
				}
			}
			prev = time.Now()
		}
	}
}

// slept says whether a tick expected every interval came so late — by
// the wall clock, which goes on while a machine is paused, or by the
// monotonic one — that the machine must have been paused in between.
func slept(prev, now time.Time, interval time.Duration) bool {
	wall := now.Round(0).Sub(prev.Round(0))
	mono := now.Sub(prev)
	return wall-interval > sleptAfter || mono-interval > sleptAfter
}
