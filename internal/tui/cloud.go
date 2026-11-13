package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Amitgb14/conch/internal/cloud"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// Conch Cloud in the tree. A cloud machine is a machine (its target is
// conch:<id>, remote.TransportFor reaches it), so all this adds is its
// lifecycle: the control plane's word on each one's state, asked every
// cloudEvery while this conch is signed in — never otherwise; with no
// cloud.json the tick only looks for the file — a chip saying it, and
// Wake and Sleep. The machines themselves come into machines.json from
// the account (remote.SyncCloudMachines), and the catalog watch adds them.

const cloudEvery = 15 * time.Second

// openCloud is the signed-in client; tests replace it.
var openCloud = cloud.Open

type cloudTickMsg struct{}

type cloudMachinesMsg struct {
	machines []cloud.Machine
	err      error
}

type cloudOpDoneMsg struct {
	machine string
	op      string // wake or sleep
	err     error
}

func cloudTick() tea.Cmd {
	return tea.Tick(cloudEvery, func(time.Time) tea.Msg { return cloudTickMsg{} })
}

// askCloud asks the control plane for the account's machines — when
// there is an account.
func askCloud() tea.Cmd {
	if !cloud.SignedIn() {
		return nil
	}
	return func() tea.Msg {
		c, err := openCloud()
		if err != nil {
			return cloudMachinesMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ms, err := c.Machines(ctx)
		if err == nil {
			err = remote.SyncCloudMachines(ms)
		}
		return cloudMachinesMsg{machines: ms, err: err}
	}
}

// cloudMachines takes in the account's machines: each one's state, and a
// machine that has woken (by Wake here, conch cloud exec elsewhere, the
// web UI) is connected to.
func (m *Model) cloudMachines(msg cloudMachinesMsg) tea.Cmd {
	if msg.err != nil {
		if errors.Is(msg.err, cloud.ErrSignedOut) || errors.Is(msg.err, cloud.ErrNotSignedIn) {
			m.cloudState = nil
			_ = remote.ForgetCloudMachines()
			return m.syncCatalog()
		}
		return nil // the network's trouble: the next tick asks again
	}
	states := map[string]cloud.Machine{}
	for _, cm := range msg.machines {
		states[cm.ID] = cm
	}
	m.cloudState = states
	var cmds []tea.Cmd
	for _, mach := range m.machines {
		id, ok := mach.cloudID()
		if !ok {
			continue
		}
		cm, ok := states[id]
		if !ok {
			continue
		}
		if mach.state == stateAttention && mach.sandboxState != "" && cm.State == cloud.StateRunning && mach.busy == "" {
			cmds = append(cmds, m.reconnect(mach.id, false))
		}
	}
	return tea.Batch(append(cmds, m.syncCatalog(), m.rebuild())...)
}

// cloudID is the machine's Conch Cloud id, for a cloud machine.
func (mach *machine) cloudID() (string, bool) { return remote.ParseCloudTarget(mach.target) }

// cloudStateOf is what the control plane last said of a cloud machine's
// state, or what connecting to it found, or "".
func (m Model) cloudStateOf(mach *machine) cloud.Machine {
	id, _ := mach.cloudID()
	if cm, ok := m.cloudState[id]; ok {
		return cm
	}
	return cloud.Machine{ID: id, State: string(mach.sandboxState)}
}

// cloudRow is a cloud machine's row: its state as the control plane has
// it, beside the connection's, and a cloud mark.
func (m Model) cloudRow(mach *machine, badge string) (glyph string, glyphStyle lipgloss.Style, label string, labelStyle lipgloss.Style, right string, ok bool) {
	cm := m.cloudStateOf(mach)
	chip := func(s lipgloss.Style, text string) string { return s.Render("☁ " + text) }
	switch {
	case mach.busy != "":
		return spinner[m.spin%len(spinner)], styleWork, mach.label, styleMuted, chip(styleMuted, mach.busy), true
	case mach.state == stateOnline:
		return "●", styleOK, mach.label, styleBold, joinRight(styleMuted.Render("☁"), badge), true
	}
	switch cm.State {
	case cloud.StateAsleep, cloud.StateSleeping, cloud.StateArchived:
		return "■", styleMuted, mach.label, styleMuted, chip(styleMuted, cm.State), true
	case cloud.StateWaking, cloud.StateCreating:
		return spinner[m.spin%len(spinner)], styleWork, mach.label, styleMuted, chip(styleWork, cm.State), true
	case cloud.StateError, cloud.StateDestroying:
		return "!", styleErr, mach.label, styleMuted, chip(styleErr, cm.State), true
	}
	return "", glyphStyle, "", labelStyle, "", false
}

// cloudOp wakes or sleeps a cloud machine.
func (m *Model) cloudOp(mid, op string, force bool) tea.Cmd {
	mach := m.machine(mid)
	if mach == nil || mach.busy != "" {
		return nil
	}
	id, ok := mach.cloudID()
	if !ok {
		return nil
	}
	mach.busy = map[string]string{"wake": "waking", "sleep": "going to sleep"}[op]
	m.setFlash(mach.busy+" "+mach.label+"…", false)
	run := func() tea.Msg {
		c, err := openCloud()
		if err != nil {
			return cloudOpDoneMsg{machine: mid, op: op, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if op == "wake" {
			_, err = c.Wake(ctx, id)
		} else {
			_, err = c.Sleep(ctx, id, force)
		}
		return cloudOpDoneMsg{machine: mid, op: op, err: err}
	}
	return tea.Batch(run, m.rebuild())
}

func (m *Model) cloudOpDone(msg cloudOpDoneMsg) tea.Cmd {
	mach := m.machine(msg.machine)
	if mach == nil {
		return nil
	}
	mach.busy = ""
	switch {
	case errors.Is(msg.err, cloud.ErrAgentsWorking):
		mid := mach.id
		m.overlay = newConfirm(fmt.Sprintf("Agents are working on %s. Put it to sleep anyway? They stop where they are.", mach.label),
			func(m *Model) tea.Cmd { return m.cloudOp(mid, "sleep", true) })
		return m.rebuild()
	case msg.err != nil:
		m.setFlash(fmt.Sprintf("%s %s failed: %v", msg.op, mach.label, msg.err), true)
		return m.rebuild()
	}
	if msg.op == "wake" {
		// Connecting waits while it wakes (conch cloud exec does).
		mach.sandboxState = ""
		m.setFlash("waking "+mach.label+"; it connects once it is up", false)
		return tea.Batch(m.reconnect(mach.id, false), askCloud())
	}
	mach.close()
	mach.state, mach.err, mach.sandboxState = stateAttention, mach.label+" is asleep", sandbox.State(cloud.StateAsleep)
	m.setFlash(mach.label+" is going to sleep · its disk is kept", false)
	return tea.Batch(m.rebuild(), askCloud())
}
