package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// openSandboxProvider opens a provider from config.toml; tests replace it.
var openSandboxProvider = remote.OpenProvider

// setUpSandboxFn installs conch in a new sandbox and connects; tests
// replace it.
var setUpSandboxFn = remote.SetUpSandbox

// sandboxWait bounds creating, starting or stopping a sandbox.
const sandboxWait = 10 * time.Minute

// sandbox reports the provider and ID of a machine that is a sandbox.
func (mach *machine) sandbox() (provider, id string, ok bool) {
	return remote.ParseSandboxTarget(mach.target)
}

// sandboxDoneMsg ends a start, stop or delete of a machine's sandbox.
type sandboxDoneMsg struct {
	machine string
	op      string // start, stop or delete
	err     error
}

var sandboxDoing = map[string]string{"start": "starting", "stop": "stopping", "delete": "deleting"}

// sandboxOp runs op on the machine's sandbox, showing it as busy meanwhile.
func (m *Model) sandboxOp(mid, op string) tea.Cmd {
	mach := m.machine(mid)
	if mach == nil || mach.busy != "" {
		return nil
	}
	provider, id, ok := mach.sandbox()
	if !ok {
		return nil
	}
	mach.busy = sandboxDoing[op]
	m.setFlash(mach.busy+" "+mach.label+"…", false)
	run := func() tea.Msg {
		p, err := openSandboxProvider(provider)
		if err == nil {
			err = p.Check()
		}
		if err != nil {
			return sandboxDoneMsg{machine: mid, op: op, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), sandboxWait)
		defer cancel()
		switch op {
		case "start":
			_, err = p.Start(ctx, id)
		case "stop":
			err = p.Stop(ctx, id)
		case "delete":
			if err = p.Delete(ctx, id); errors.Is(err, sandbox.ErrNotFound) {
				err = nil // already gone: forgetting it is all that's left
			}
			if err == nil {
				err = remote.RemoveMachine(mid)
			}
		}
		return sandboxDoneMsg{machine: mid, op: op, err: err}
	}
	return tea.Batch(run, m.rebuild())
}

// sandboxDone applies the end of a sandbox operation.
func (m *Model) sandboxDone(msg sandboxDoneMsg) tea.Cmd {
	mach := m.machine(msg.machine)
	if mach == nil {
		return nil
	}
	mach.busy = ""
	if msg.err != nil {
		m.setFlash(fmt.Sprintf("%s %s failed: %v", msg.op, mach.label, msg.err), true)
		return m.rebuild()
	}
	switch msg.op {
	case "start":
		m.setFlash("started "+mach.label, false)
		return m.reconnect(mach.id, true)
	case "stop":
		mach.close()
		mach.state, mach.err, mach.sandboxState = stateAttention, "sandbox stopped", sandbox.StateStopped
		m.setFlash("stopped "+mach.label+" · its files are kept", false)
		return m.rebuild()
	case "delete":
		m.setFlash("deleted "+mach.label, false)
		return m.syncCatalog()
	}
	return nil
}

// confirmStopSandbox asks before stopping a sandbox: whatever runs there
// ends.
func (m *Model) confirmStopSandbox(mid string) {
	mach := m.machine(mid)
	if mach == nil {
		return
	}
	text := []string{fmt.Sprintf("Stop %s? Its agents and terminals end; its files stay, and it stops costing for anything but disk.", mach.label)}
	if n := workingAgents(mach.panes); n > 0 {
		text = append(text, fmt.Sprintf("%d agent%s there %s still working.", n, plural(n), map[bool]string{true: "is", false: "are"}[n == 1]))
	}
	d := newConfirm("", func(m *Model) tea.Cmd { return m.sandboxOp(mid, "stop") })
	d.text = text
	m.overlay = d
}

// confirmDeleteSandbox asks before deleting a sandbox, listing the work
// conch last saw there that exists nowhere else.
func (m *Model) confirmDeleteSandbox(mid string) {
	mach := m.machine(mid)
	if mach == nil {
		return
	}
	_, id, _ := mach.sandbox()
	text := []string{fmt.Sprintf("Delete %s (sandbox %s) and everything in it? This can't be undone.", mach.label, id)}
	lost := remote.UnsavedWork(mach.projects)
	switch {
	case len(lost) > 0:
		text = append(text, "Not saved anywhere else:")
		for _, l := range lost {
			text = append(text, "  "+l)
		}
	case mach.state != stateOnline:
		text = append(text, "conch can't see into it now, so it can't tell whether work there is pushed.")
	}
	d := newConfirm("", func(m *Model) tea.Cmd { return m.sandboxOp(mid, "delete") })
	d.text = text
	m.overlay = d
}

func workingAgents(panes []proto.PaneInfo) int {
	n := 0
	for _, p := range panes {
		if p.Agent != nil && p.Agent.State == proto.AgentWorking {
			n++
		}
	}
	return n
}

// newAddMenu asks what kind of machine to add.
func newAddMenu() *menu {
	return &menu{title: "Add a machine", items: []menuItem{
		{"s", "Over ssh…", func(m *Model) tea.Cmd {
			d := newAddMachineDialog(*m)
			m.overlay = d
			return d.focusCmd()
		}},
		{"d", "New Daytona sandbox…", func(m *Model) tea.Cmd {
			d := newSandboxDialog(*m)
			m.overlay = d
			return d.focusCmd()
		}},
	}}
}

func newSandboxDialog(m Model) *dialog {
	text := []string{"Creates a sandbox with Daytona, installs conch there and adds it as a machine. It runs, and costs, until you stop it (m → Stop sandbox)."}
	if p, err := openSandboxProvider("daytona"); err != nil {
		text = append(text, err.Error())
	} else if err := p.Check(); err != nil {
		text = append(text, "Needs a Daytona API key: "+strings.TrimPrefix(err.Error(), sandbox.ErrNotConfigured.Error()+": ")+".")
	}
	d := newDialog(m, " New Daytona sandbox ", text, []string{"Label", "Snapshot", "vCPUs", "Memory GiB", "Disk GiB", "Pass in"}, nil)
	d.fields[0].in.Placeholder = "defaults to sandbox-<id>"
	d.fields[1].in.Placeholder = firstNonEmpty(m.cfg.Sandbox.Daytona.Snapshot, "Daytona's default")
	d.fields[2].in.Placeholder = "the snapshot's"
	d.fields[3].in.Placeholder = "the snapshot's"
	d.fields[4].in.Placeholder = "the snapshot's"
	d.fields[5].in.Placeholder = "names of your environment variables, e.g. CLAUDE_CODE_OAUTH_TOKEN"
	cfg := m.cfg.Sandbox.Daytona
	d.submit = func(m *Model, v []string) tea.Cmd {
		var sizes [3]int
		for i, name := range []string{"vCPUs", "Memory", "Disk"} {
			s := strings.TrimSpace(v[2+i])
			if s == "" {
				continue
			}
			n, err := strconv.Atoi(s)
			if err != nil || n < 0 {
				return func() tea.Msg { return errMsg{fmt.Errorf("%s: give a whole number", name)} }
			}
			sizes[i] = n
		}
		names := append([]string{}, cfg.Env...)
		names = append(names, strings.FieldsFunc(v[5], func(r rune) bool { return r == ',' || r == ' ' })...)
		env, err := sandbox.EnvFrom(names)
		if err != nil {
			return func() tea.Msg { return errMsg{err} }
		}
		spec := sandbox.Spec{Snapshot: strings.TrimSpace(v[1]), CPU: sizes[0], Memory: sizes[1], Disk: sizes[2], Env: env, AutoStop: cfg.AutoStop}
		m.setFlash("creating a Daytona sandbox (a minute or two)…", false)
		return createSandbox("daytona", spec, strings.TrimSpace(v[0]))
	}
	return d
}

// createSandbox makes a sandbox, sets conch up in it and saves it as a
// machine. A sandbox that can't be set up is deleted rather than left to
// cost: nobody is there to ask.
func createSandbox(provider string, spec sandbox.Spec, label string) tea.Cmd {
	return func() tea.Msg {
		p, err := openSandboxProvider(provider)
		if err == nil {
			err = p.Check()
		}
		if err != nil {
			return errMsg{err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), sandboxWait)
		defer cancel()
		s, err := p.Create(ctx, spec)
		if err == nil {
			if label == "" {
				label = remote.DefaultSandboxLabel(s.ID)
			}
			m := remote.Machine{Label: label, Target: remote.SandboxTarget(provider, s.ID)}
			c, setUpErr := setUpSandboxFn(ctx, m, func(string) {})
			if setUpErr == nil {
				c.Close()
				saved, err := remote.SaveMachine(m)
				if err != nil {
					return errMsg{err}
				}
				return machineAddedMsg{m: saved, note: provider + " sandbox " + s.ID + " · runs until stopped (m → Stop sandbox)"}
			}
			err = setUpErr
		}
		if s.ID == "" {
			return errMsg{fmt.Errorf("create sandbox: %w", err)}
		}
		dctx, dcancel := context.WithTimeout(context.Background(), time.Minute)
		defer dcancel()
		if derr := p.Delete(dctx, s.ID); derr != nil && !errors.Is(derr, sandbox.ErrNotFound) {
			return errMsg{fmt.Errorf("setting up sandbox %s failed: %v; deleting it failed too (%v): conch sandbox rm %s", s.ID, err, derr, s.ID)}
		}
		return errMsg{fmt.Errorf("setting up sandbox %s failed, so it was deleted: %w", s.ID, err)}
	}
}
