package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// Broadcast types one message into several agents: the running agents of
// the group selected in the tree (a machine, project, branch or CLI), or
// of every machine. Shells never receive it — they would run the text —
// and agents waiting for an answer start unticked, since the message would
// answer their question.

type broadcastTarget struct {
	machine string
	pane    proto.PaneInfo
	group   string // project name or "CLI", plus the branch
	on      bool
}

func (bt broadcastTarget) waiting() bool {
	return bt.pane.Agent != nil && bt.pane.Agent.State == proto.AgentBlocked
}

// broadcastScope names the selected group and lists its running agents.
// every widens it to all machines.
func (m Model) broadcastScope(every bool) (label string, targets []broadcastTarget) {
	r, ok := m.selectedRow()
	if !ok {
		every = true
	}
	in := func(mach *machine, p proto.PaneInfo) bool {
		if every {
			return true
		}
		if mach.id != r.machine {
			return false
		}
		known := p.ProjectID != "" && m.project(mach.id, p.ProjectID) != nil
		switch r.kind {
		case kindMachine:
			return true
		case kindCLI:
			return !known
		case kindBranch:
			return known && p.ProjectID == r.projectID && p.Branch == r.branch
		case kindPane:
			if sel := m.pane(r.machine, r.paneID); sel != nil {
				selKnown := sel.ProjectID != "" && m.project(mach.id, sel.ProjectID) != nil
				return known == selKnown && (!known || p.ProjectID == sel.ProjectID)
			}
			return false
		}
		if r.projectID == "" { // a machine's own Agents or Terminals
			return !known
		}
		return known && p.ProjectID == r.projectID
	}
	for _, mach := range m.machines {
		if mach.c == nil {
			continue
		}
		for _, p := range mach.panes {
			if p.Agent == nil || p.State != proto.PaneRunning || !in(mach, p) {
				continue
			}
			group := "CLI"
			if proj := m.project(mach.id, p.ProjectID); proj != nil {
				group = proj.Name
			}
			if p.Branch != "" {
				group += " · " + p.Branch
			}
			if len(m.machines) > 1 {
				group = mach.label + " › " + group
			}
			bt := broadcastTarget{machine: mach.id, pane: p, group: group}
			bt.on = !bt.waiting()
			targets = append(targets, bt)
		}
	}
	sort.SliceStable(targets, func(i, j int) bool { return strings.ToLower(targets[i].group) < strings.ToLower(targets[j].group) })

	mach := m.machine(r.machine)
	switch {
	case every:
		label = "every machine"
	case r.kind == kindMachine && mach != nil:
		label = mach.label
	case r.kind == kindBranch:
		label = m.projectName(r.machine, r.projectID) + " · " + r.branch
	case r.kind == kindCLI || (r.projectID == "" && r.kind != kindPane):
		label = "CLI"
	case r.kind == kindPane:
		if p := m.pane(r.machine, r.paneID); p != nil && p.ProjectID != "" && m.project(r.machine, p.ProjectID) != nil {
			label = m.projectName(r.machine, p.ProjectID)
		} else {
			label = "CLI"
		}
	default:
		label = m.projectName(r.machine, r.projectID)
	}
	if !every && len(m.machines) > 1 && mach != nil && r.kind != kindMachine {
		label = mach.label + " › " + label
	}
	return label, targets
}

func (m Model) projectName(mid, pid string) string {
	if p := m.project(mid, pid); p != nil {
		return p.Name
	}
	return pid
}

// openBroadcast starts a broadcast to the selected group.
func (m *Model) openBroadcast() tea.Cmd {
	label, targets := m.broadcastScope(false)
	if len(targets) == 0 {
		if _, all := m.broadcastScope(true); len(all) > 0 {
			m.setFlash("no agents running in "+label+" · B on a machine or project with agents", true)
		} else {
			m.setFlash("no agents running to broadcast to (shells never get broadcasts)", true)
		}
		return nil
	}
	d := newBroadcastDialog(*m, label, targets)
	m.overlay = d
	return textinput.Blink
}

type broadcastDialog struct {
	label   string
	in      textinput.Model
	targets []broadcastTarget
	list    bool // the recipient list has the keys
	sel     int
	scroll  int
	every   bool
	err     string
}

const broadcastListRows = 10

func newBroadcastDialog(m Model, label string, targets []broadcastTarget) *broadcastDialog {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "e.g. run the tests and fix anything you broke"
	in.CharLimit = 4000
	in.Width = m.dialogWidth() - 12
	in.Focus()
	return &broadcastDialog{label: label, in: in, targets: targets}
}

func (d *broadcastDialog) chosen() []broadcastTarget {
	var out []broadcastTarget
	for _, t := range d.targets {
		if t.on {
			out = append(out, t)
		}
	}
	return out
}

func (d *broadcastDialog) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		d.in, cmd = d.in.Update(msg)
		return false, cmd
	}
	switch k.String() {
	case "esc":
		m.overlay = nil
		return true, nil
	case "tab", "shift+tab":
		d.list = !d.list
		if d.list {
			d.in.Blur()
			return false, nil
		}
		return false, d.in.Focus()
	case "enter":
		return false, d.review(m)
	}
	if !d.list {
		if k.String() == "down" {
			d.list = true
			d.in.Blur()
			return false, nil
		}
		var cmd tea.Cmd
		d.in, cmd = d.in.Update(msg)
		d.err = ""
		return false, cmd
	}
	switch k.String() {
	case "up", "k":
		if d.sel == 0 {
			d.list = false
			return false, d.in.Focus()
		}
		d.sel--
	case "down", "j":
		d.sel = min(d.sel+1, len(d.targets)-1)
	case " ", "x":
		if d.sel < len(d.targets) {
			d.targets[d.sel].on = !d.targets[d.sel].on
			d.err = ""
		}
	case "a":
		all := len(d.chosen()) < len(d.targets)
		for i := range d.targets {
			d.targets[i].on = all
		}
		d.err = ""
	case "e":
		d.widen(m)
	}
	d.keepVisible()
	return false, nil
}

// widen switches between the selected group and every machine, keeping
// the ticks of agents in both.
func (d *broadcastDialog) widen(m *Model) {
	d.every = !d.every
	label, targets := m.broadcastScope(d.every)
	was := map[string]bool{}
	for _, t := range d.targets {
		was[scoped(t.machine, t.pane.ID)] = t.on
	}
	for i, t := range targets {
		if on, ok := was[scoped(t.machine, t.pane.ID)]; ok {
			targets[i].on = on
		}
	}
	d.label, d.targets, d.sel, d.scroll = label, targets, 0, 0
}

func (d *broadcastDialog) keepVisible() {
	d.sel = clamp(d.sel, 0, max(len(d.targets)-1, 0))
	if d.sel < d.scroll {
		d.scroll = d.sel
	}
	if d.sel >= d.scroll+broadcastListRows {
		d.scroll = d.sel - broadcastListRows + 1
	}
}

// review asks for confirmation, naming the recipients and warning about
// agents that are waiting for an answer.
func (d *broadcastDialog) review(m *Model) tea.Cmd {
	text := strings.TrimSpace(d.in.Value())
	chosen := d.chosen()
	switch {
	case text == "":
		d.err = "type the message first"
		return nil
	case len(chosen) == 0:
		d.err = "tick at least one agent (space)"
		return nil
	}
	var names []string
	waiting := 0
	for _, t := range chosen {
		names = append(names, t.pane.DisplayName()+" ("+t.group+")")
		if t.waiting() {
			waiting++
		}
	}
	q := fmt.Sprintf("Send “%s” to %d agent%s: %s?", ansi.Truncate(text, 60, "…"), len(chosen), plural(len(chosen)), strings.Join(names, ", "))
	if waiting > 0 {
		q += fmt.Sprintf(" %d of them %s waiting for an answer: the message will answer %s.", waiting, map[bool]string{true: "is", false: "are"}[waiting == 1],
			map[bool]string{true: "its question", false: "their questions"}[waiting == 1])
	}
	back := d
	c := newConfirm(q, func(m *Model) tea.Cmd { return m.sendBroadcast(chosen, text) })
	c.title = " Broadcast "
	// n or esc returns to the message instead of dropping it.
	m.overlay = &broadcastConfirm{dialog: c, back: back}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// broadcastConfirm is the yes/no step; declining goes back to editing.
type broadcastConfirm struct {
	*dialog
	back *broadcastDialog
}

func (c *broadcastConfirm) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "n", "N", "esc", "q":
			m.overlay = c.back
			if !c.back.list {
				return false, c.back.in.Focus()
			}
			return false, nil
		}
	}
	return c.dialog.update(m, msg)
}

type broadcastDoneMsg struct {
	sent    int
	skipped []string
}

// sendBroadcast sends text to each machine's chosen agents.
func (m *Model) sendBroadcast(targets []broadcastTarget, text string) tea.Cmd {
	byMachine := map[string][]broadcastTarget{}
	var order []string
	for _, t := range targets {
		if _, ok := byMachine[t.machine]; !ok {
			order = append(order, t.machine)
		}
		byMachine[t.machine] = append(byMachine[t.machine], t)
	}
	var skipped []string
	var calls []tea.Cmd
	for _, mid := range order {
		ts := byMachine[mid]
		mach := m.machine(mid)
		switch {
		case mach == nil || mach.c == nil:
			for _, t := range ts {
				skipped = append(skipped, t.pane.DisplayName()+": machine offline")
			}
			continue
		case len(mach.c.MissingCapabilities([]string{"agent.broadcast.v1"})) > 0:
			for _, t := range ts {
				skipped = append(skipped, t.pane.DisplayName()+": "+mach.label+"'s server predates broadcasts")
			}
			continue
		}
		c, label := mach.c, mach.label
		ids := make([]string, len(ts))
		names := map[string]string{}
		for i, t := range ts {
			ids[i] = t.pane.ID
			names[t.pane.ID] = t.pane.DisplayName()
		}
		calls = append(calls, func() tea.Msg {
			var res proto.AgentBroadcastResult
			if err := callCtx(c, proto.MethodAgentBroadcast, proto.AgentBroadcastParams{IDs: ids, Text: text}, &res); err != nil {
				var out []string
				for _, id := range ids {
					out = append(out, names[id]+": "+err.Error())
				}
				return broadcastDoneMsg{skipped: out}
			}
			done := broadcastDoneMsg{}
			for _, r := range res.Results {
				if r.Sent {
					done.sent++
				} else {
					name := names[r.ID]
					if len(order) > 1 {
						name += " on " + label
					}
					done.skipped = append(done.skipped, name+": "+r.Error)
				}
			}
			return done
		})
	}
	if len(skipped) > 0 || len(calls) == 0 {
		pre := broadcastDoneMsg{skipped: skipped}
		calls = append(calls, func() tea.Msg { return pre })
	}
	return tea.Batch(calls...)
}

func (m *Model) receiveBroadcast(msg broadcastDoneMsg) {
	switch {
	case len(msg.skipped) == 0:
		m.setFlash(fmt.Sprintf("broadcast sent to %d agent%s", msg.sent, plural(msg.sent)), false)
	case msg.sent == 0:
		m.setFlash("broadcast not sent · "+strings.Join(msg.skipped, " · "), true)
	default:
		m.setFlash(fmt.Sprintf("broadcast sent to %d agent%s · not sent: %s", msg.sent, plural(msg.sent), strings.Join(msg.skipped, " · ")), true)
	}
}

func (d *broadcastDialog) render(m Model) box {
	w := m.dialogWidth()
	chosen := len(d.chosen())
	label := styleMuted.Render("Message")
	if !d.list {
		label = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("Message")
	}
	lines := []string{"", " " + label + "  " + d.in.View(), ""}
	head := fmt.Sprintf("To %d of %d agents in %s", chosen, len(d.targets), d.label)
	lines = append(lines, " "+styleBold.Render(ansi.Truncate(head, w-2, "…")))
	end := min(d.scroll+broadcastListRows, len(d.targets))
	if d.scroll > 0 {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("   … %d above", d.scroll)))
	}
	for i := d.scroll; i < end; i++ {
		t := d.targets[i]
		box := "[ ]"
		if t.on {
			box = "[x]"
		}
		state := ""
		if t.pane.Agent != nil {
			state = t.pane.Agent.State
		}
		note := styleMuted.Render(state)
		if t.waiting() {
			note = styleWarn.Render("! waiting for an answer")
		}
		row := fmt.Sprintf(" %s %s  %s", box, t.pane.DisplayName(), styleMuted.Render(t.group))
		line := spread(row, note+" ", w)
		if d.list && i == d.sel {
			line = styleSel.Render(spread(ansi.Strip(row), ansi.Strip(note)+" ", w))
		}
		lines = append(lines, line)
	}
	if end < len(d.targets) {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("   … %d more", len(d.targets)-end)))
	}
	lines = append(lines, "")
	if d.err != "" {
		lines = append(lines, " "+styleErr.Render(d.err))
	}
	widen := "e every machine"
	if d.every {
		widen = "e just " + "the selection"
	}
	hints := "enter review · tab recipients · esc cancel"
	if d.list {
		hints = "space tick · a all · " + widen + " · enter review · tab message"
	}
	for _, l := range wrap(hints, w-2) {
		lines = append(lines, " "+styleMuted.Render(l))
	}
	b := box{lines: frameLines(" Broadcast ", lines, w, colorAccent)}
	b.x = max((m.width-b.width())/2, 0)
	b.y = max((m.height-len(b.lines))/4, 0)
	return b
}

func (d *broadcastDialog) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || !b.contains(msg.X, msg.Y) {
		return nil // an outside click doesn't drop the message
	}
	// Rows: border, blank, message, blank, heading, [above], targets.
	first := b.y + 5
	if d.scroll > 0 {
		first++
	}
	if msg.Y == b.y+2 {
		d.list = false
		return d.in.Focus()
	}
	if i := d.scroll + msg.Y - first; msg.Y >= first && i < min(d.scroll+broadcastListRows, len(d.targets)) {
		d.list, d.sel = true, i
		d.in.Blur()
		d.targets[i].on = !d.targets[i].on
	}
	return nil
}

func (c *broadcastConfirm) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && !b.contains(msg.X, msg.Y) {
		m.overlay = c.back // back to the message, not lost
	}
	return nil
}
