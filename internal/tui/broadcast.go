package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// Broadcast types one message into several panes and submits it: the
// running agents and terminals of what is selected in the tree, listed
// like the tree (project or CLI, then Agents and Terminals). An agent gets
// the message as its next prompt; a terminal runs it as a command, so
// terminals start ticked only when a Terminals section (or a shell) was
// selected. Agents waiting for an answer start unticked: the message would
// answer their question.

type broadcastTarget struct {
	machine string
	pane    proto.PaneInfo
	group   string // the project's name or "CLI", with the machine when there are several
	shell   bool   // no agent runs in it: it would run the text
	on      bool
}

func (bt broadcastTarget) waiting() bool {
	return bt.pane.Agent != nil && bt.pane.Agent.State == proto.AgentBlocked
}

// broadcastScope names the selection and lists its running panes; every
// widens it to all machines.
func (m Model) broadcastScope(every bool) (label string, targets []broadcastTarget) {
	r, ok := m.selectedRow()
	if !ok {
		every = true
	}
	// Which sections: a Terminals row or a shell lists terminals only, an
	// Agents row or an agent lists agents only, anything else both.
	wantAgents, wantShells, shellsOn := true, true, false
	var sel *proto.PaneInfo
	if !every {
		switch r.kind {
		case kindAgents:
			wantShells = false
		case kindTerminals:
			wantAgents, shellsOn = false, true
		case kindPane:
			if sel = m.pane(r.machine, r.paneID); sel != nil && sel.Agent == nil {
				wantAgents, shellsOn = false, true
			} else {
				wantShells = false
			}
		}
	}
	known := func(mid string, p proto.PaneInfo) bool {
		return p.ProjectID != "" && m.project(mid, p.ProjectID) != nil
	}
	in := func(mach *machine, p proto.PaneInfo) bool {
		if every {
			return true
		}
		if mach.id != r.machine {
			return false
		}
		k := known(mach.id, p)
		switch r.kind {
		case kindMachine:
			return true
		case kindCLI:
			return !k
		case kindWorkspace:
			return k
		case kindBranch:
			return k && p.ProjectID == r.projectID && p.Branch == r.branch
		case kindPane:
			return sel != nil && k == known(mach.id, *sel) && (!k || p.ProjectID == sel.ProjectID)
		}
		if r.projectID == "" { // a machine's own Agents or Terminals
			return !k
		}
		return k && p.ProjectID == r.projectID
	}
	order := map[string]int{}
	for i, mach := range m.machines {
		order[mach.id] = i
		if mach.c == nil {
			continue
		}
		for _, p := range mach.panes {
			shell := p.Agent == nil
			if p.State != proto.PaneRunning || (shell && !wantShells) || (!shell && !wantAgents) || !in(mach, p) {
				continue
			}
			group := "CLI"
			if known(mach.id, p) {
				group = m.projectName(mach.id, p.ProjectID)
			}
			if len(m.machines) > 1 {
				group = mach.label + " › " + group
			}
			bt := broadcastTarget{machine: mach.id, pane: p, group: group, shell: shell}
			bt.on = (shell && shellsOn) || (!shell && !bt.waiting())
			targets = append(targets, bt)
		}
	}
	// Like the tree: machines in order, projects before CLI, agents before
	// terminals.
	sort.SliceStable(targets, func(i, j int) bool {
		a, b := targets[i], targets[j]
		if order[a.machine] != order[b.machine] {
			return order[a.machine] < order[b.machine]
		}
		ac, bc := strings.HasSuffix(a.group, "CLI"), strings.HasSuffix(b.group, "CLI")
		if ac != bc {
			return bc
		}
		if ga, gb := strings.ToLower(a.group), strings.ToLower(b.group); ga != gb {
			return ga < gb
		}
		return !a.shell && b.shell
	})

	mach := m.machine(r.machine)
	switch {
	case every:
		label = "every machine"
	case r.kind == kindMachine && mach != nil:
		label = mach.label
	case r.kind == kindBranch:
		label = m.projectName(r.machine, r.projectID) + " · " + r.branch
	case r.kind == kindPane:
		label = "CLI"
		if sel != nil && known(r.machine, *sel) {
			label = m.projectName(r.machine, sel.ProjectID)
		}
		if sel != nil && sel.Agent == nil {
			label += " · Terminals"
		} else {
			label += " · Agents"
		}
	case r.kind == kindWorkspace:
		label = "Workspace"
	case r.kind == kindCLI || r.projectID == "":
		label = "CLI"
	default:
		label = m.projectName(r.machine, r.projectID)
	}
	switch {
	case every, r.kind == kindPane:
	case r.kind == kindAgents:
		label += " · Agents"
	case r.kind == kindTerminals:
		label += " · Terminals"
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

// openBroadcast starts a broadcast to the selection.
func (m *Model) openBroadcast() tea.Cmd {
	label, targets := m.broadcastScope(false)
	if len(targets) == 0 {
		m.setFlash("nothing running in "+label+" to broadcast to", true)
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
	sel     int  // index into targets
	scroll  int  // first visible list row
	every   bool
	err     string
}

// broadcastListRows is how many list rows (headings and panes) show.
const broadcastListRows = 12

func newBroadcastDialog(m Model, label string, targets []broadcastTarget) *broadcastDialog {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "a prompt for agents, or a command for terminals"
	in.CharLimit = 4000
	in.Width = m.dialogWidth() - 12
	in.Focus()
	return &broadcastDialog{label: label, in: in, targets: targets}
}

// listRow is a heading (target < 0) or a pane of the recipient list.
type listRow struct {
	heading string
	sub     bool // an Agents / Terminals heading
	target  int
}

func (d *broadcastDialog) rows() []listRow {
	var out []listRow
	group, section := "", ""
	for i, t := range d.targets {
		if t.group != group || i == 0 {
			group, section = t.group, ""
			out = append(out, listRow{heading: t.group, target: -1})
		}
		s := "Agents"
		if t.shell {
			s = "Terminals"
		}
		if s != section {
			section = s
			out = append(out, listRow{heading: s, sub: true, target: -1})
		}
		out = append(out, listRow{target: i})
	}
	return out
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
		d.tickAll(func(broadcastTarget) bool { return true })
	case "t":
		d.tickAll(func(t broadcastTarget) bool { return t.shell })
	case "e":
		d.widen(m)
	}
	d.keepVisible()
	return false, nil
}

// tickAll ticks every target matching which, or unticks them when all
// already are.
func (d *broadcastDialog) tickAll(which func(broadcastTarget) bool) {
	all := true
	for _, t := range d.targets {
		all = all && (!which(t) || t.on)
	}
	for i, t := range d.targets {
		if which(t) {
			d.targets[i].on = !all
		}
	}
	d.err = ""
}

// widen switches between the selection and every machine, keeping ticks.
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
	rows := d.rows()
	at := slices.IndexFunc(rows, func(r listRow) bool { return r.target == d.sel })
	if at < 0 {
		return
	}
	// Keep the pane's headings in view when scrolling up to it.
	top := at
	for top > 0 && rows[top-1].target < 0 {
		top--
	}
	if top < d.scroll {
		d.scroll = top
	}
	if at >= d.scroll+broadcastListRows {
		d.scroll = at - broadcastListRows + 1
	}
}

// review asks for confirmation, naming the recipients and warning about
// terminals and agents that are waiting for an answer.
func (d *broadcastDialog) review(m *Model) tea.Cmd {
	text := strings.TrimSpace(d.in.Value())
	chosen := d.chosen()
	switch {
	case text == "":
		d.err = "type the message first"
		return nil
	case len(chosen) == 0:
		d.err = "tick at least one (space)"
		return nil
	}
	var names []string
	agents, shells, waiting := 0, 0, 0
	for _, t := range chosen {
		names = append(names, t.pane.DisplayName()+" ("+t.group+")")
		switch {
		case t.shell:
			shells++
		case t.waiting():
			agents++
			waiting++
		default:
			agents++
		}
	}
	q := fmt.Sprintf("Send “%s” to %s: %s?", ansi.Truncate(text, 60, "…"), countText(agents, shells), strings.Join(names, ", "))
	if shells == 1 {
		q += " The terminal runs it as a shell command."
	} else if shells > 1 {
		q += " The terminals run it as a shell command."
	}
	if waiting == 1 {
		q += " 1 agent is waiting for an answer: the message will answer its question."
	} else if waiting > 1 {
		q += fmt.Sprintf(" %d agents are waiting for an answer: the message will answer their questions.", waiting)
	}
	back := d
	c := newConfirm(q, func(m *Model) tea.Cmd { return m.sendBroadcast(chosen, text) })
	c.title = " Broadcast "
	m.overlay = &broadcastConfirm{dialog: c, back: back}
	return nil
}

// countText says "2 agents and 1 terminal".
func countText(agents, shells int) string {
	var parts []string
	if agents > 0 {
		parts = append(parts, fmt.Sprintf("%d agent%s", agents, plural(agents)))
	}
	if shells > 0 {
		parts = append(parts, fmt.Sprintf("%d terminal%s", shells, plural(shells)))
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, " and ")
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

func (c *broadcastConfirm) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && !b.contains(msg.X, msg.Y) {
		m.overlay = c.back // back to the message, not lost
	}
	return nil
}

type broadcastDoneMsg struct {
	agents, shells int // sent
	skipped        []string
}

// sendBroadcast sends text to the chosen panes, through each machine's
// server.
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
	var calls []func() broadcastDoneMsg
	many := len(order) > 1
	for _, mid := range order {
		mach := m.machine(mid)
		var ts []broadcastTarget
		for _, t := range byMachine[mid] {
			switch {
			case mach == nil || mach.c == nil:
				skipped = append(skipped, t.pane.DisplayName()+": machine offline")
			case len(mach.c.MissingCapabilities([]string{"agent.broadcast.v1"})) > 0:
				skipped = append(skipped, t.pane.DisplayName()+": "+mach.label+"'s server predates broadcasts")
			case t.shell && len(mach.c.MissingCapabilities([]string{"agent.broadcast.shells.v1"})) > 0:
				skipped = append(skipped, t.pane.DisplayName()+": "+mach.label+"'s server predates broadcasts to terminals")
			default:
				ts = append(ts, t)
			}
		}
		if len(ts) == 0 {
			continue
		}
		c, label := mach.c, mach.label
		ids := make([]string, len(ts))
		byID := map[string]broadcastTarget{}
		shells := false
		for i, t := range ts {
			ids[i], byID[t.pane.ID] = t.pane.ID, t
			shells = shells || t.shell
		}
		params := proto.AgentBroadcastParams{IDs: ids, Text: text, Shells: shells}
		calls = append(calls, func() broadcastDoneMsg {
			var res proto.AgentBroadcastResult
			if err := callCtx(c, proto.MethodAgentBroadcast, params, &res); err != nil {
				var out []string
				for _, id := range ids {
					out = append(out, byID[id].pane.DisplayName()+": "+err.Error())
				}
				return broadcastDoneMsg{skipped: out}
			}
			done := broadcastDoneMsg{}
			for _, r := range res.Results {
				t := byID[r.ID]
				switch {
				case r.Sent && t.shell:
					done.shells++
				case r.Sent:
					done.agents++
				default:
					name := t.pane.DisplayName()
					if many {
						name += " on " + label
					}
					done.skipped = append(done.skipped, name+": "+r.Error)
				}
			}
			return done
		})
	}
	// One message for the whole broadcast: the machines are called at once,
	// and their counts added up, so the last to answer doesn't hide the rest.
	return func() tea.Msg {
		results := make([]broadcastDoneMsg, len(calls))
		var wg sync.WaitGroup
		for i, call := range calls {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = call()
			}()
		}
		wg.Wait()
		done := broadcastDoneMsg{skipped: skipped}
		for _, r := range results {
			done.agents += r.agents
			done.shells += r.shells
			done.skipped = append(done.skipped, r.skipped...)
		}
		return done
	}
}

func (m *Model) receiveBroadcast(msg broadcastDoneMsg) {
	sent := countText(msg.agents, msg.shells)
	switch {
	case len(msg.skipped) == 0:
		m.setFlash("broadcast sent to "+sent, false)
	case msg.agents+msg.shells == 0:
		m.setFlash("broadcast not sent · "+strings.Join(msg.skipped, " · "), true)
	default:
		m.setFlash("broadcast sent to "+sent+" · not sent: "+strings.Join(msg.skipped, " · "), true)
	}
}

func (d *broadcastDialog) render(m Model) box {
	w := m.dialogWidth()
	label := styleMuted.Render("Message")
	if !d.list {
		label = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("Message")
	}
	lines := []string{"", " " + label + "  " + d.in.View(), ""}
	head := fmt.Sprintf("To %d of %d in %s", len(d.chosen()), len(d.targets), d.label)
	lines = append(lines, " "+styleBold.Render(ansi.Truncate(head, w-2, "…")))
	rows := d.rows()
	end := min(d.scroll+broadcastListRows, len(rows))
	if d.scroll > 0 {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("   … %d above", d.scroll)))
	}
	for _, r := range rows[d.scroll:end] {
		switch {
		case r.target < 0 && r.sub:
			lines = append(lines, "   "+styleMuted.Render(r.heading))
			continue
		case r.target < 0:
			lines = append(lines, " "+styleAccent.Render(ansi.Truncate(r.heading, w-2, "…")))
			continue
		}
		t := d.targets[r.target]
		box := "[ ]"
		if t.on {
			box = "[x]"
		}
		var note string
		switch {
		case t.shell && t.pane.Title != "" && t.pane.Title != t.pane.DisplayName():
			note = styleMuted.Render(ansi.Truncate(t.pane.Title, 24, "…"))
		case t.shell:
			note = styleMuted.Render("runs it as a command")
		case t.waiting():
			note = styleWarn.Render("! waiting for an answer")
		default:
			note = styleMuted.Render(t.pane.Agent.State)
		}
		row := fmt.Sprintf("     %s %s", box, t.pane.DisplayName())
		if t.pane.Branch != "" {
			row += "  " + styleMuted.Render(t.pane.Branch)
		}
		line := spread(row, note+" ", w)
		if d.list && r.target == d.sel {
			line = styleSel.Render(spread(ansi.Strip(row), ansi.Strip(note)+" ", w))
		}
		lines = append(lines, line)
	}
	if end < len(rows) {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("   … %d more", len(rows)-end)))
	}
	lines = append(lines, "")
	if d.err != "" {
		lines = append(lines, " "+styleErr.Render(d.err))
	}
	widen := "e every machine"
	if d.every {
		widen = "e just the selection"
	}
	hints := "enter review · tab recipients · esc cancel"
	if d.list {
		hints = "space tick · a all · t terminals · " + widen + " · enter review · tab message"
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
	if msg.Y == b.y+2 {
		d.list = false
		return d.in.Focus()
	}
	// Lines: border, blank, message, blank, heading, [above], list rows.
	first := b.y + 5
	if d.scroll > 0 {
		first++
	}
	rows := d.rows()
	if i := d.scroll + msg.Y - first; msg.Y >= first && i < min(d.scroll+broadcastListRows, len(rows)) && rows[i].target >= 0 {
		t := rows[i].target
		d.list, d.sel = true, t
		d.in.Blur()
		d.targets[t].on = !d.targets[t].on
	}
	return nil
}
