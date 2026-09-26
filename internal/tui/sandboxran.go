package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// Bringing back what a sandbox was running. Stopping a sandbox ends every
// process in it — that is what stopping means, and the provider keeps only
// the filesystem — so the agents and terminals there are gone when it
// comes back. Their work isn't: the checkout is as it was, and an agent's
// conversation is saved in the sandbox's own home. So conch writes down
// what was running, and offers to start it again on the way back: each
// agent resumed on its own conversation, each terminal in its own folder.

// ranPane is one thing that was running in a sandbox when it stopped.
type ranPane struct {
	Agent   string   `json:"agent,omitempty"`   // "" for a terminal
	Session string   `json:"session,omitempty"` // the conversation to resume
	Dir     string   `json:"dir,omitempty"`
	Branch  string   `json:"branch,omitempty"`
	Name    string   `json:"name,omitempty"`
	Command []string `json:"command,omitempty"` // a terminal's own command
}

// what it says it is, for a confirmation to count them.
func (r ranPane) what() string {
	if r.Agent != "" {
		return agentLabel(r.Agent)
	}
	return "terminal"
}

// runningIn is what a machine is running, in the shape conch remembers.
func runningIn(mach *machine) []ranPane {
	var ran []ranPane
	for _, p := range mach.panes {
		if p.State != proto.PaneRunning {
			continue
		}
		r := ranPane{Dir: p.Cwd, Branch: p.Branch, Name: p.Name}
		switch {
		case p.Agent != nil:
			r.Agent, r.Session = p.Agent.Name, p.Agent.SessionID
		case mach.agents[p.ID]:
			continue // an agent that has finished; its shell is not worth starting
		default:
			r.Command = p.Command
		}
		ran = append(ran, r)
	}
	return ran
}

// rememberRunning writes down what a sandbox is running, to offer back
// when it starts again. Called before conch stops one, and when a machine
// is lost, which is the last moment its panes are known.
func (m *Model) rememberRunning(mid string) {
	mach := m.machine(mid)
	if mach == nil {
		return
	}
	provider, _, ok := mach.sandbox()
	if !ok {
		return // an ordinary machine keeps running without conch
	}
	if !m.cfg.Sandbox.Of(provider).RestoresRunning() {
		delete(m.sandboxRan, mid) // asked not to keep a note of it
		return
	}
	ran := runningIn(mach)
	if m.sandboxRan == nil {
		m.sandboxRan = map[string][]ranPane{}
	}
	if len(ran) == 0 {
		delete(m.sandboxRan, mid)
		return
	}
	m.sandboxRan[mid] = ran
}

// offerRestore asks whether to start again what the sandbox was running
// when it stopped. Nothing is started without an answer: an agent picks up
// where it left off, which is not always what you want to happen twice.
func (m *Model) offerRestore(mid string) tea.Cmd {
	ran := m.sandboxRan[mid]
	mach := m.machine(mid)
	if len(ran) == 0 || mach == nil {
		return nil
	}
	if provider, _, ok := mach.sandbox(); !ok || !m.cfg.Sandbox.Of(provider).RestoresRunning() {
		delete(m.sandboxRan, mid)
		return nil
	}
	if len(mach.panes) > 0 {
		return nil // something already runs there; leave it be
	}
	m.overlay = newConfirm(fmt.Sprintf("%s was running %s when it stopped. Start them again?%s",
		mach.label, listRan(ran), resumeNote(ran)),
		func(m *Model) tea.Cmd { return m.restoreRunning(mid) })
	return nil
}

// listRan says what was running, in words: "2 Claude Code agents and a terminal".
func listRan(ran []ranPane) string {
	counts := map[string]int{}
	var order []string
	for _, r := range ran {
		what := r.what()
		if counts[what] == 0 {
			order = append(order, what)
		}
		counts[what]++
	}
	var parts []string
	for _, what := range order {
		parts = append(parts, counted(counts[what], what))
	}
	switch len(parts) {
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}

// resumeNote says which agents come back where they left off, and which
// start afresh because nothing saved their conversation.
func resumeNote(ran []ranPane) string {
	resumed, fresh := 0, 0
	for _, r := range ran {
		switch {
		case r.Agent != "" && r.Session != "":
			resumed++
		case r.Agent != "":
			fresh++
		}
	}
	switch {
	case resumed > 0 && fresh > 0:
		return fmt.Sprintf(" %s carry on where they left off; %s start afresh, having saved no conversation.",
			counted(resumed, "agent"), counted(fresh, "agent"))
	case resumed > 0:
		return " The agents carry on where they left off."
	case fresh > 0:
		return " The agents start afresh: nothing saved their conversation."
	}
	return ""
}

// restoreDoneMsg is what came of starting things again.
type restoreDoneMsg struct {
	machine string
	started int
	first   proto.PaneInfo // to show, so "it started" is something you can see
	failed  []string       // one line each, in the words the server used
}

// restoreRunning starts again what was written down, and forgets it: a
// second start is a fresh sandbox as far as this is concerned. Each is
// started in turn rather than all at once, so the reasons come back in
// order and a sandbox is not asked for four panes in the same instant.
func (m *Model) restoreRunning(mid string) tea.Cmd {
	ran := m.sandboxRan[mid]
	if len(ran) == 0 {
		return nil
	}
	delete(m.sandboxRan, mid)
	cols, rows := m.paneArea()
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24 // before the first layout, rather than a pane of nothing
	}
	mach := m.machine(mid)
	if mach == nil || mach.c == nil {
		m.setFlash(m.offlineText(mid), true)
		return nil
	}
	c := mach.c
	m.setFlash("starting "+listRan(ran)+" again in "+mach.label+"…", false)
	return tea.Batch(m.saveState(), func() tea.Msg {
		done := restoreDoneMsg{machine: mid}
		for _, r := range ran {
			info, err := restoreOne(c, r, cols, rows)
			if err != nil {
				done.failed = append(done.failed, r.what()+" in "+r.Dir+": "+errText(err))
				continue
			}
			if done.started == 0 {
				done.first = info
			}
			done.started++
		}
		return done
	})
}

// restoreOne starts one of them: an agent on the conversation it was on,
// or, when that conversation has gone, the same agent afresh in the same
// folder — losing the thread is no reason to lose the agent as well.
func restoreOne(c *client.Client, r ranPane, cols, rows int) (proto.PaneInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var info proto.PaneInfo
	if r.Agent != "" && r.Session != "" {
		ref := proto.SessionRef{Agent: r.Agent, ID: r.Session, Dir: r.Dir, Cols: cols, Rows: rows}
		err := c.Call(ctx, proto.MethodSessionResume, ref, &info)
		if err == nil {
			return info, nil
		}
		var perr *proto.Error
		if !errors.As(err, &perr) || perr.Code != proto.ErrNotFound {
			return info, err
		}
	}
	params := proto.PaneCreateParams{Name: r.Name, Agent: r.Agent, Command: r.Command,
		Cwd: r.Dir, Cols: cols, Rows: rows}
	err := c.Call(ctx, proto.MethodPaneCreate, params, &info)
	return info, err
}

// receiveRestore says what started and what didn't. A sandbox that comes
// back with nothing showing is the thing to avoid: either a pane is on
// screen, or there is a reason on screen.
func (m *Model) receiveRestore(msg restoreDoneMsg) tea.Cmd {
	label := m.machineLabel(msg.machine)
	switch {
	case msg.started == 0 && len(msg.failed) == 0:
		return nil
	case msg.started == 0:
		m.showError(errString("nothing could be started again in " + label + ": " + strings.Join(msg.failed, "; ")))
		return nil
	case len(msg.failed) > 0:
		m.setFlash(fmt.Sprintf("started %s in %s · %s", counted(msg.started, "of them"), label, strings.Join(msg.failed, "; ")), true)
	default:
		m.setFlash("started "+counted(msg.started, "of them")+" again in "+label, false)
	}
	if msg.first.ID != "" {
		// Show the first, so it is plain that something is running.
		return func() tea.Msg { return createdMsg{machine: msg.machine, info: msg.first} }
	}
	return m.rebuild()
}
