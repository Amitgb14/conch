package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// overlay is a menu or dialog drawn over the screen that takes all input.
type overlay interface {
	update(m *Model, msg tea.Msg) (closed bool, cmd tea.Cmd)
	render(m Model) box
	mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd
}

// dimmer is an overlay that fades the screen behind it.
type dimmer interface {
	dimBackground() bool
}

// box is rendered overlay content and its top-left screen position.
type box struct {
	lines []string
	x, y  int
}

func (b box) width() int {
	if len(b.lines) == 0 {
		return 0
	}
	return ansi.StringWidth(b.lines[0])
}

func (b box) contains(x, y int) bool {
	return x >= b.x && x < b.x+b.width() && y >= b.y && y < b.y+len(b.lines)
}

// frameLines wraps content lines (already w wide) in a rounded border.
func frameLines(title string, content []string, w int, color lipgloss.Color) []string {
	return frameLinesBar(title, content, w, color, nil)
}

// frameLinesBar is frameLines with something drawn on the right border of
// the content lines mark names — the scrollbar's thumb. The border is
// already a column of its own, so a bar there costs no content.
func frameLinesBar(title string, content []string, w int, color lipgloss.Color, mark map[int]string) []string {
	bs := lipgloss.NewStyle().Foreground(color)
	title = ansi.Truncate(title, max(w-2, 0), "…")
	out := []string{bs.Render("╭─") + styleBold.Render(title) + bs.Render(strings.Repeat("─", max(w-1-ansi.StringWidth(title), 0))+"╮")}
	for i, l := range content {
		right := bs.Render("│")
		if g, ok := mark[i]; ok {
			right = g
		}
		out = append(out, bs.Render("│")+fit(l, w)+right)
	}
	return append(out, bs.Render("╰"+strings.Repeat("─", w)+"╯"))
}

// ---- menu ----

type menuItem struct {
	key   string
	label string
	run   func(m *Model) tea.Cmd
}

type menu struct {
	title string
	items []menuItem
	sel   int
	x, y  int
	// back is the menu this one was opened from, if any: esc goes there
	// rather than closing everything, so a wrong turn costs one key.
	back *menu
	// agentsOf is the machine whose agents this menu lists, for the one
	// that does: a fresh list arriving redraws it, since what it says is
	// installed is the thing that goes stale.
	agentsOf string
}

func newRowMenu(m Model, r row, x, y int) *menu {
	act := func(k string) func(m *Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			m.focus = focusSidebar
			next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
			*m = next.(Model)
			return cmd
		}
	}
	enter := func(m *Model) tea.Cmd {
		r, _ := m.selectedRow()
		return m.activate(r)
	}
	var items []menuItem
	title := ""
	switch r.kind {
	case kindPane:
		if p := m.pane(r.machine, r.paneID); p != nil {
			title = p.DisplayName()
		}
		items = []menuItem{
			{"enter", "Open", enter},
			{"r", "Rename", act("r")},
			{"", "Redraw (" + m.cfg.Keys.Prefix + " r)", func(m *Model) tea.Cmd {
				r, _ := m.selectedRow()
				return m.redrawPane(r)
			}},
			{"c", "Start an agent here…", act("c")},
			{"n", "New terminal here", act("n")},
			{"i", "Agent setup (skills, MCP, instructions)", act("i")},
			{"", "Open a link it printed (" + m.cfg.Keys.Prefix + " u)", func(m *Model) tea.Cmd {
				r, _ := m.selectedRow()
				return m.openLinks(r)
			}},
		}
		// A message to the agent in this pane, if there is one: the pane
		// menu is where everything else about that pane already is.
		if p := m.pane(r.machine, r.paneID); p != nil && p.Agent != nil {
			mid, pane := r.machine, r.paneID
			switch {
			case !m.hasCapability(mid, proto.CapAgentPrompt):
				items = append(items, menuItem{"", "Prompt… (its server is too old)", func(m *Model) tea.Cmd {
					m.setFlash("the server there predates prompting an agent; reload it", true)
					return nil
				}})
			default:
				label := "Prompt…"
				if p.Agent.State == proto.AgentBlocked {
					label = "Prompt… (it is waiting for an answer)"
				}
				items = append(items, menuItem{"", label, func(m *Model) tea.Cmd { return m.openPrompt(mid, pane) }})
			}
		}
		items = append(items, m.monitorItems(r.machine, r.paneID)...)
		if target := m.sshTargetOfRow(r); target != "" {
			items = append(items,
				menuItem{"e", "Edit ssh host (save it with a name, folder, options)…", act("e")},
				menuItem{"g", "Move to folder…", func(m *Model) tea.Cmd { m.overlay = newMoveSSHMenu(*m, r, x, y); return nil }},
				menuItem{"K", "Set up login without a password (copy ssh key)", act("K")},
			)
		}
		items = append(items, menuItem{"x", "Close", act("x")})
		if p := m.pane(r.machine, r.paneID); p != nil && p.Agent != nil {
			items = append(items, menuItem{"Y", "Read and copy the conversation", act("Y")})
		}
	case kindSubagent: // no pane of its own: what there is to do is with its agent
		title = "subagent"
		if p := m.pane(r.machine, r.paneID); p != nil {
			title = "subagent of " + p.DisplayName()
		}
		items = []menuItem{{"enter", "Open its agent", enter}}
	case kindBranch:
		title = r.branch
		items = []menuItem{
			{"enter", "View changes", enter},
			{"b", "Git panel (commands, ask its agent)…", act("b")},
			{"c", "Start an agent on this branch…", act("c")},
			{"n", "Open terminal on this branch", act("n")},
			{"t", "New task in project", act("t")},
			{"y", "Copy branch name", act("y")},
			{"i", "Agent setup of this checkout", act("i")},
			{"R", "Refresh git status and PRs", act("R")},
		}
		if pr := m.branchPR(r.machine, r.projectID, r.branch); pr != nil {
			items = append([]menuItem{items[0], {"o", fmt.Sprintf("Open pull request #%d", pr.Number), act("o")}}, items[1:]...)
		}
		items = append(items, compareMenuItem(m, r)...)
		items = append(items, harvestMenuItems(m, r)...)
		if proj := m.project(r.machine, r.projectID); proj != nil {
			worktree := false
			for _, wt := range proj.Worktrees {
				if wt.Branch == r.branch && !wt.Main {
					worktree = true
				}
			}
			if checkedOut(proj, r.branch) && r.branch != proj.Base && m.hasCapability(r.machine, proto.CapWorktreeMove) {
				items = append(items, menuItem{"T", "Move to another machine…", act("T")})
			}
			switch {
			case worktree:
				items = append(items, menuItem{"x", "Remove worktree", act("x")})
			case r.branch != proj.Base:
				items = append(items, menuItem{"x", "Delete branch…", act("x")})
			}
		}
	case kindProject:
		if proj := m.project(r.machine, r.projectID); proj != nil {
			title = proj.Name
		}
		taskLabel := "New task (branch + worktree + agent)"
		if proj := m.project(r.machine, r.projectID); proj != nil && !proj.Git {
			taskLabel = "New task (an agent with a prompt, here)"
		}
		items = []menuItem{
			{"t", taskLabel, act("t")},
			{"c", "Start an agent in project…", act("c")},
			{"n", "Open terminal in project", act("n")},
			{"i", "Agent setup (skills, MCP, instructions)", act("i")},
			{"B", "Broadcast to its agents and terminals…", act("B")},
			{"F", "Local files for new worktrees…", act("F")},
			{"W", "Clean up worktrees…", act("W")},
			{"R", "Refresh git status", act("R")},
			{"", "Show all branches", func(m *Model) tea.Cmd {
				m.showAll[scoped(r.machine, r.projectID)] = true
				m.expanded[r.id] = true
				m.expanded[sectionID(r.machine, r.projectID, "branches")] = true
				return tea.Batch(m.rebuild(), m.saveState())
			}},
			{"x", "Remove from sidebar", act("x")},
		}
	case kindMachine:
		mach := m.machine(r.machine)
		if mach == nil {
			break
		}
		title = mach.label
		mid := mach.id
		_, _, isSandbox := mach.sandbox()
		switch {
		case isSandbox && mach.busy != "":
			return &menu{title: title, x: x, y: y, items: []menuItem{{"", mach.busy + "…", func(*Model) tea.Cmd { return nil }}}}
		case isSandbox && mach.sandboxState != "":
			items = append(items, menuItem{"s", "Start sandbox", func(m *Model) tea.Cmd { return m.sandboxOp(mid, "start") }})
		case mach.state == stateAttention && mid != localMachine:
			items = append(items, menuItem{"i", "Install / upgrade conch there", func(m *Model) tea.Cmd { return m.reconnect(mid, true) }})
		case mach.state == stateOffline && mid == localMachine:
			items = append(items, menuItem{"s", "Start the server", func(m *Model) tea.Cmd { return m.reconnect(mid, true) }})
		}
		if mach.state == stateOnline {
			items = append(items,
				menuItem{"c", "Start or install an agent…", act("c")},
				menuItem{"t", "New task in the home directory…", act("t")},
				menuItem{"a", "Add project…", act("a")},
				menuItem{"n", "New terminal", act("n")},
			)
			if mid == localMachine {
				items = append(items, menuItem{"H", "SSH to a host…", act("H")})
			}
		}
		items = append(items, menuItem{"R", "Reconnect", act("R")})
		if mid != localMachine {
			items = append(items, menuItem{"r", "Rename…", act("r")})
		}
		if mach.state == stateOnline && m.canReload(mid) {
			items = append(items, menuItem{"", "Reload server onto the installed build (keeps panes)", func(m *Model) tea.Cmd {
				return m.reloadServer(mid)
			}})
		}
		if mach.state == stateOnline {
			items = append(items, menuItem{"", "Restart server (stops its panes)…", func(m *Model) tea.Cmd {
				m.overlay = newConfirm(fmt.Sprintf("Restart the conch server on %s? Every pane there stops.", mach.label),
					func(m *Model) tea.Cmd { return m.restartServer(mid) })
				return nil
			}})
		}
		if isSandbox {
			if mach.state == stateOnline {
				items = append(items, menuItem{"o", "Open a port in the browser…", func(m *Model) tea.Cmd { return m.openSandboxPort(mid) }})
				items = append(items, menuItem{"S", "Stop sandbox…", func(m *Model) tea.Cmd { m.confirmStopSandbox(mid); return nil }})
			}
			items = append(items, menuItem{"K", "Keep a snapshot…", func(m *Model) tea.Cmd { return m.openSnapshotDialog(mid) }})
			items = append(items, menuItem{"$", "What it has cost…", func(m *Model) tea.Cmd { return m.openSandboxUsage(mid) }})
			items = append(items, menuItem{"D", "Delete sandbox…", func(m *Model) tea.Cmd { m.confirmDeleteSandbox(mid); return nil }})
		}
		items = append(items, menuItem{"M", "Add machine…", act("M")})
		switch {
		case isSandbox:
			items = append(items, menuItem{"x", "Remove machine (the sandbox keeps running)", act("x")})
		case mid != localMachine:
			items = append(items, menuItem{"x", "Remove machine", act("x")})
		}
	case kindSavedSSH:
		target := savedSSHTarget(r.id)
		title = sshDisplay(target, m.sshInfo[target])
		items = []menuItem{
			{"enter", "Connect", enter},
			{"e", "Edit host, name, folder, ssh options…", act("e")},
			{"g", "Move to folder…", func(m *Model) tea.Cmd { m.overlay = newMoveSSHMenu(*m, r, x, y); return nil }},
			{"K", "Set up login without a password (copy ssh key)", act("K")},
			{"N", "New folder…", act("N")},
			{"x", "Forget (remove from the tree)", act("x")},
		}
	case kindFolder:
		title = r.label
		if r.section == kindSSH && r.machine == localMachine && r.projectID == "" {
			items = append(items, menuItem{"a", "Add a host to this folder…", act("a")}, menuItem{"H", "SSH to a host…", act("H")})
		}
		items = append(items,
			menuItem{"N", "New folder…", act("N")},
			menuItem{"x", "Remove folder (what is in it stays, nothing closes)", act("x")},
		)
	case kindWorkspace:
		title = "Workspace"
		items = []menuItem{
			{"a", "Add project…", act("a")},
			{"t", "New task (branch + worktree + agent)…", act("t")},
			{"B", "Broadcast to its projects' agents and terminals…", act("B")},
		}
	default:
		items = []menuItem{
			{"a", "Add project…", act("a")},
			{"c", "Start an agent…", act("c")},
			{"n", "New terminal", act("n")},
			{"M", "Add machine…", act("M")},
		}
		if r.machine == localMachine && r.projectID == "" {
			// The local CLI group and its sections: where ssh sessions go.
			items = append(items[:3], append([]menuItem{{"H", "SSH to a host…", act("H")}}, items[3:]...)...)
			if r.kind == kindSSH {
				items = append(items, menuItem{"N", "New folder (eng, staging, prod…)…", act("N")})
			}
		}
	}
	return &menu{title: title, items: withActions(m, r, x, y, items), x: x, y: y}
}

// withActions puts somebody's own commands on a row's menu, above the
// item that closes or removes something: a menu's last line is where the
// hand goes by habit, and that belongs to the destructive one.
func withActions(m Model, r row, x, y int, items []menuItem) []menuItem {
	add := m.actionsMenuItem(r, x, y)
	if len(add) == 0 {
		return items
	}
	at := len(items)
	for i, it := range items {
		if it.key == "x" {
			at = i
			break
		}
	}
	return append(items[:at:at], append(add, items[at:]...)...)
}

func (mu *menu) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch k.String() {
	case "esc", "left", "h":
		if mu.back != nil {
			mu.back.sel = 0
			m.overlay = mu.back
			return true, nil
		}
		m.overlay = nil
		return true, nil
	case "q", "m":
		m.overlay = nil
		return true, nil
	case "up", "k", "shift+tab":
		mu.sel = (mu.sel + len(mu.items) - 1) % len(mu.items)
	case "down", "j", "tab":
		mu.sel = (mu.sel + 1) % len(mu.items)
	case "enter":
		return true, mu.run(m, mu.sel)
	default:
		for i, it := range mu.items {
			if it.key != "" && it.key != "enter" && it.key == k.String() {
				return true, mu.run(m, i)
			}
		}
	}
	return false, nil
}

func (mu *menu) run(m *Model, i int) tea.Cmd {
	m.overlay = nil
	return mu.items[i].run(m)
}

func (mu *menu) render(m Model) box {
	w := ansi.StringWidth(mu.title) + 4 // the title is framed as " title " inside the corners
	for _, it := range mu.items {
		w = max(w, ansi.StringWidth(it.label)+10)
	}
	// Never wider than the terminal. A menu was as wide as its longest
	// label, and the view's last truncation then took the right border and
	// the end of that label off the side of the screen; an item that does
	// not fit is cut with an ellipsis instead, so it reads as abbreviated
	// rather than as broken.
	w = min(w, max(m.width-2, 1))
	var lines []string
	for i, it := range mu.items {
		key := it.key
		if key == "enter" {
			key = "⏎"
		}
		label := it.label
		if room := max(w-5, 1); ansi.StringWidth(label) > room {
			label = ansi.Truncate(label, room, "…")
		}
		line := " " + padRight(styleMuted.Render(key), 3) + " " + label
		if i == mu.sel {
			line = styleSel.Render(padRight(" "+padRight(key, 3)+" "+ansi.Strip(label), w))
		}
		lines = append(lines, line)
	}
	b := box{lines: frameLines(" "+mu.title+" ", lines, w, colorAccent), x: mu.x, y: mu.y}
	b.x = clamp(b.x, 0, max(m.width-b.width(), 0))
	b.y = clamp(b.y, 0, max(m.height-len(b.lines), 0))
	return b
}

func (mu *menu) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	i := msg.Y - b.y - 1
	inItem := b.contains(msg.X, msg.Y) && i >= 0 && i < len(mu.items)
	switch {
	case msg.Action == tea.MouseActionMotion && inItem:
		mu.sel = i
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && inItem:
		return mu.run(m, i)
	case msg.Action == tea.MouseActionPress && !b.contains(msg.X, msg.Y):
		m.overlay = nil
	}
	return nil
}

// newAgentMenu lists the agents a machine knows: installed ones start at
// the selected place, missing ones install.
func newAgentMenu(m Model, mach *machine) *menu {
	var items []menuItem
	list := mach.agentList
	if len(list) == 0 { // an older server: offer what conch can launch by name
		for _, name := range []string{"claude", "codex", "gemini", "opencode"} {
			list = append(list, proto.AgentAvailability{Name: name, Installed: true})
		}
	}
	// What can be started comes first, and what would have to be
	// installed after it: with a dozen agents the numbers are worth more
	// on the ones somebody is actually choosing between.
	list = append(append([]proto.AgentAvailability{}, installedFirst(list, true)...), installedFirst(list, false)...)
	sel, digits := -1, 0
	// Digits are handed out as the rows that act are added, not by an
	// agent's place in the list: a row that only explains itself would
	// otherwise take a number and leave a hole in the count. Ten of them,
	// with 0 for the tenth, as the tab keys do.
	next := func() string {
		if digits >= 10 {
			return ""
		}
		digits++
		if digits == 10 {
			return "0"
		}
		return fmt.Sprint(digits)
	}
	for _, a := range list {
		a := a
		label := a.Label
		if label == "" {
			label = agentLabel(a.Name)
		}
		if a.Installed {
			detail := ""
			if a.Version != "" {
				detail = "  " + styleMuted.Render(a.Version)
			}
			// An agent conch only runs says so where it is chosen: it
			// starts and is watched like any other, and its saved
			// conversations are not read, which is worth knowing before
			// rather than when the Sessions view is empty.
			if a.Tier == proto.TierRunsHere {
				detail += styleMuted.Render("  runs here · no sessions")
			}
			if a.Name == m.defaultAgent() {
				detail += styleMuted.Render("  default")
				sel = len(items)
			} else if sel < 0 {
				sel = len(items)
			}
			items = append(items, menuItem{next(), "Start " + label + detail, func(m *Model) tea.Cmd { return m.openAgent(a.Name) }})
		} else if a.NoInstaller {
			// A manifest that named no install script. Offering to
			// install it would run nothing and say it worked, which is
			// what it used to do.
			items = append(items, menuItem{"", label + styleMuted.Render("  not installed · conch has no installer for it"),
				func(m *Model) tea.Cmd {
					m.setFlash("install "+label+" yourself and conch will find it on your PATH", false)
					return nil
				}})
		} else {
			items = append(items, menuItem{next(), "Install " + label + styleMuted.Render("  not installed"), func(m *Model) tea.Cmd { return m.installAgent(mach.id, a.Name) }})
		}
	}
	items = append(items, menuItem{"n", "Open a terminal instead", func(m *Model) tea.Cmd { return m.openAgent("") }})
	return &menu{title: "Start an agent · " + m.placeLabel(), items: items, sel: max(sel, 0),
		agentsOf: mach.id, x: max(m.width/2-25, 0), y: max(m.height/3, 0)}
}

// installedFirst picks the agents that are installed, or the ones that
// are not, keeping the order the machine gave them.
func installedFirst(list []proto.AgentAvailability, installed bool) []proto.AgentAvailability {
	var out []proto.AgentAvailability
	for _, a := range list {
		if a.Installed == installed {
			out = append(out, a)
		}
	}
	return out
}

// placeLabel names where a new pane for the selected row would start.
func (m Model) placeLabel() string {
	pl := m.contextPlace()
	where := pl.machine
	if mach := m.machine(pl.machine); mach != nil {
		where = mach.label
	}
	if proj := m.project(pl.machine, pl.projectID); proj != nil {
		where = proj.Name + " on " + where
		if pl.branch != "" && pl.branch != proj.Base {
			where = pl.branch + " · " + where
		}
	}
	return where
}

// ---- dialog ----

type field struct {
	label string
	in    textinput.Model
	// check makes the field a checkbox with the text beside it; its value
	// is "on" or "".
	check *bool
	text  string
}

// addCheck appends a checkbox field.
func (d *dialog) addCheck(label, text string, on bool) {
	w := 0
	for _, f := range d.fields {
		w = max(w, ansi.StringWidth(f.label))
	}
	w = max(w, len(label))
	for i := range d.fields {
		d.fields[i].label = padRight(strings.TrimRight(d.fields[i].label, " "), w)
	}
	v := on
	d.fields = append(d.fields, field{label: padRight(label, w), in: textinput.New(), check: &v, text: text})
}

type dialog struct {
	title   string
	text    []string
	fields  []field
	focus   int
	confirm bool // yes/no question without fields
	notice  bool // text to read, closed with enter or esc
	yesOnly bool // a confirm that enter doesn't accept, for what can't be undone
	onNo    bool // which button the keyboard is on; Yes to begin with
	// buttons is where a confirm's Yes and No sit, from the last render:
	// the content line and each one's columns, for clicks.
	buttons struct{ line, yes0, yes1, no0, no1 int }
	submit  func(m *Model, values []string) tea.Cmd
	// decline runs when a confirm is answered No — n or its button — as
	// opposed to put aside with esc. Most confirms have none.
	decline func(m *Model) tea.Cmd
	// onChange runs after a field was edited, e.g. to update placeholders.
	onChange func(d *dialog)
	// back is what opened this dialog, if it should come back afterwards:
	// a settings screen a field was reached from, say, so changing three
	// things in a row costs three keys rather than nine.
	back overlay
}

// dialogWidth is how wide a dialog's content is drawn. 72 where there
// is room, with a margin either side, and never wider than the terminal:
// the floor of 30 is a floor only while the frame still fits inside the
// screen. It used to be unconditional, so every dialog was 32 columns
// wide — frame included — in a terminal narrower than that, and spilled
// over whatever was behind it.
func (m Model) dialogWidth() int {
	w := min(72, max(m.width-4, 1)) // the usual: a margin of 2 each side
	if w < 30 {
		w = min(30, max(m.width-2, 1)) // cramped: give up the margin, keep the frame
	}
	return w
}

func newDialog(m Model, title string, text []string, labels []string, values []string) *dialog {
	d := &dialog{title: title, text: text}
	labelW := 0
	for _, l := range labels {
		labelW = max(labelW, len(l))
	}
	for i, l := range labels {
		in := textinput.New()
		in.Prompt = ""
		in.Width = max(m.dialogWidth()-labelW-6, 1)
		if i < len(values) {
			in.SetValue(values[i])
		}
		d.fields = append(d.fields, field{label: padRight(l, labelW), in: in})
	}
	if len(d.fields) > 0 {
		d.fields[0].in.Focus()
	}
	return d
}

func (d *dialog) focusCmd() tea.Cmd { return textinput.Blink }

// newNotice shows text too long for the status bar, such as why something
// failed.
func newNotice(title string, text []string) *dialog {
	return &dialog{title: title, text: text, notice: true}
}

// declined is what answering No does: d.decline, when there is one.
func (d *dialog) declined(m *Model) tea.Cmd {
	if d.decline == nil {
		return nil
	}
	return d.decline(m)
}

func newConfirm(question string, yes func(m *Model) tea.Cmd) *dialog {
	return &dialog{title: " Confirm ", text: []string{question}, confirm: true,
		submit: func(m *Model, _ []string) tea.Cmd { return yes(m) }}
}

func newRenameDialog(m Model, mid string, p proto.PaneInfo) *dialog {
	id := p.ID
	d := newDialog(m, " Rename ", []string{"Leave empty to use the agent's task title."}, []string{"Name"}, []string{p.Name})
	if !p.CustomName {
		d.fields[0].in.SetValue("")
		d.fields[0].in.Placeholder = p.DisplayName()
	}
	d.submit = func(m *Model, v []string) tea.Cmd {
		return m.callOn(mid, proto.MethodPaneRename, proto.PaneRenameParams{ID: id, Name: v[0]}, nil, nil)
	}
	return d
}

func newAddProjectDialog(m Model, mid string) *dialog {
	start := "~"
	where := ""
	if mid == localMachine {
		start = m.tildify(mid, cwdOrHome())
	} else if mach := m.machine(mid); mach != nil {
		where = " on " + mach.label
	}
	d := newDialog(m, " Add project"+where+" ", []string{"A git repository (any folder inside it) or a plain folder. ~ is the home directory there."},
		[]string{"Path"}, []string{start})
	d.fields[0].in.CursorEnd()
	d.submit = func(m *Model, v []string) tea.Cmd {
		var info proto.ProjectInfo
		return m.callOn(mid, proto.MethodProjectAdd, proto.ProjectAddParams{Path: strings.TrimSpace(v[0])}, &info,
			m.addedProject(mid, &info, "added "))
	}
	return d
}

func newAddMachineDialog(m Model) *dialog {
	text := []string{"conch reaches the machine with your ssh setup and installs itself to ~/.local/bin/conch there if needed."}
	if hosts := remote.SSHHosts(); len(hosts) > 0 {
		if len(hosts) > 12 {
			hosts = append(hosts[:12], "…")
		}
		text = append(text, "Hosts in ~/.ssh/config: "+strings.Join(hosts, ", "))
	}
	text = append(text, "Password: only if ssh asks for one. It is used for this add and never saved; a new host's key is accepted.")
	d := newDialog(m, " Add machine ", text, []string{"SSH target", "Label", "Password"}, nil)
	d.fields[0].in.Placeholder = "user@host, host alias or ssh://user@host:port"
	d.fields[1].in.Placeholder = "defaults to the host name"
	d.fields[2].in.Placeholder = "leave empty for key login"
	d.fields[2].in.EchoMode = textinput.EchoPassword
	d.fields[2].in.EchoCharacter = '•'
	d.addCheck("Key login", "with a password, add my ssh key there so reconnects need no password", true)
	d.submit = func(m *Model, v []string) tea.Cmd {
		target := strings.TrimSpace(v[0])
		if target == "" {
			return func() tea.Msg { return errMsg{errString("an ssh target is required")} }
		}
		m.setFlash("adding "+target+" (installing conch there if needed)…", false)
		return m.fromHere(addMachineFn(target, strings.TrimSpace(v[1]), v[2], v[3] == "on"))
	}
	return d
}

func newTaskDialog(m Model, mid string, proj proto.ProjectInfo) *dialog {
	intro := "Creates a branch and worktree, then starts the agent with the prompt."
	d := newDialog(m, " New task · "+proj.Name+" ", []string{intro},
		[]string{"Prompt", "Branch", "Base", "Agent", "Attempts"}, nil)
	d.fields[1].in.Placeholder = "derived from the prompt"
	d.fields[2].in.Placeholder = proj.Base
	d.fields[3].in.Placeholder = m.defaultAgent() + " (default · " + strings.Join(knownAgents(&m), ", ") + ", or several: claude,codex)"
	d.fields[4].in.Placeholder = "1 (each attempt gets its own branch)"
	defaultAgent := m.defaultAgent()
	// The warnings follow the Agent and Attempts fields: each agent has its
	// own plan, and every attempt spends one.
	warn := func(d *dialog) {
		agents := splitAgents(d.fields[3].in.Value())
		if len(agents) == 0 {
			agents = []string{defaultAgent}
		}
		d.text = []string{intro}
		n, err := attemptsField(d.fields[4].in.Value())
		switch {
		case err != nil:
			d.text = append(d.text, styleErr.Render("⚠ "+err.Error()))
		case max(n, len(agents)) > 1:
			plan := attemptPlan(proj.Name, agents, n, strings.TrimSpace(d.fields[1].in.Value()),
				strings.TrimSpace(d.fields[0].in.Value()), proj.Branches)
			var names []string
			for _, at := range plan {
				names = append(names, at.branch)
			}
			d.text = append(d.text, fmt.Sprintf("%s of the same prompt, one per branch: %s.",
				counted(len(plan), "attempt"), listSome(names, 4)))
		}
		for _, w := range m.limitWarnings(mid, agents, time.Now()) {
			d.text = append(d.text, styleWarn.Render("⚠")+" "+w)
		}
	}
	warn(d)
	d.onChange = func(d *dialog) {
		if p := strings.TrimSpace(d.fields[0].in.Value()); p != "" {
			d.fields[1].in.Placeholder = gitx.BranchFromPrompt(proj.Name, p)
		}
		warn(d)
	}
	id, name, branches := proj.ID, proj.Name, proj.Branches
	d.submit = func(m *Model, v []string) tea.Cmd {
		if strings.TrimSpace(v[0]) == "" {
			return func() tea.Msg { return errMsg{errString("a task needs a prompt")} }
		}
		n, err := attemptsField(v[4])
		if err != nil {
			return func() tea.Msg { return errMsg{errString(err.Error())} }
		}
		agents := splitAgents(v[3])
		if len(agents) == 0 {
			agents = []string{defaultAgent}
		}
		if mach := m.machine(mid); mach != nil {
			for _, agent := range agents {
				if mach.missingAgent(agent) {
					return func() tea.Msg { return askInstallMsg{machine: mid, agent: agent} }
				}
			}
		}
		cols, rows := m.paneArea()
		plan := attemptPlan(name, agents, n, strings.TrimSpace(v[1]), strings.TrimSpace(v[0]), branches)
		if len(plan) == 1 {
			params := proto.TaskCreateParams{ProjectID: id, Prompt: v[0], Branch: plan[0].branch,
				Base: strings.TrimSpace(v[2]), Agent: plan[0].agent, Cols: cols, Rows: rows}
			var info proto.PaneInfo
			return m.callOn(mid, proto.MethodTaskCreate, params, &info, func() tea.Msg { return createdMsg{machine: mid, info: info} })
		}
		m.setFlash("starting "+counted(len(plan), "attempt")+"…", false)
		return m.startAttempts(mid, id, v[0], strings.TrimSpace(v[2]), plan, cols, rows)
	}
	return d
}

type errString string

func (e errString) Error() string { return string(e) }

func (d *dialog) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, isKey := msg.(tea.KeyMsg)
	if d.notice {
		switch {
		case !isKey:
		case k.String() == "enter" || k.String() == "esc" || k.String() == "q" || k.String() == " ":
			m.overlay = nil
		}
		return true, nil
	}
	if d.confirm {
		if isKey {
			switch k.String() {
			case "y", "Y":
				m.overlay = d.back
				return true, d.submit(m, nil)
			case "n", "N":
				m.overlay = d.back
				return true, d.declined(m)
			case "esc", "q":
				m.overlay = d.back
				return true, nil
			case "left", "right", "tab", "shift+tab", "h", "l":
				d.onNo = !d.onNo // two buttons: any of these moves between them
				return false, nil
			case "enter", " ":
				if d.onNo {
					m.overlay = d.back
					return true, d.declined(m)
				}
				// What can't be undone takes y or the button, never a
				// stray enter — but space on the button is deliberate.
				if d.yesOnly && k.String() == "enter" {
					return false, nil
				}
				m.overlay = d.back
				return true, d.submit(m, nil)
			}
		}
		return false, nil
	}
	if isKey {
		switch k.String() {
		case "esc":
			m.overlay = d.back // nil unless something is waiting behind it
			return true, nil
		case "tab", "down":
			return false, d.setFocus(d.focus + 1)
		case "shift+tab", "up":
			return false, d.setFocus(d.focus - 1)
		case "enter":
			values := make([]string, len(d.fields))
			for i, f := range d.fields {
				values[i] = f.in.Value()
				if f.check != nil {
					values[i] = map[bool]string{true: "on"}[*f.check]
				}
			}
			m.overlay = d.back
			return true, d.submit(m, values)
		case " ", "x":
			if c := d.fields[d.focus].check; c != nil {
				*c = !*c
				return false, nil
			}
		}
	}
	if d.fields[d.focus].check != nil {
		return false, nil // a checkbox takes no text
	}
	var cmd tea.Cmd
	d.fields[d.focus].in, cmd = d.fields[d.focus].in.Update(msg)
	if d.onChange != nil {
		d.onChange(d)
	}
	return false, cmd
}

func (d *dialog) setFocus(i int) tea.Cmd {
	if len(d.fields) == 0 {
		return nil
	}
	d.fields[d.focus].in.Blur()
	d.focus = (i + len(d.fields)) % len(d.fields)
	return d.fields[d.focus].in.Focus()
}

func (d *dialog) render(m Model) box {
	w := m.dialogWidth()
	lines := []string{""}
	for _, t := range d.text {
		for _, l := range wrap(t, w-2) {
			lines = append(lines, " "+l)
		}
	}
	if len(d.fields) > 0 {
		lines = append(lines, "")
	}
	for i, f := range d.fields {
		label := styleMuted.Render(f.label)
		if i == d.focus {
			label = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(f.label)
		}
		if f.check != nil {
			box := "[ ] "
			if *f.check {
				box = "[x] "
			}
			if i == d.focus {
				box = styleAccent.Render(box)
			}
			for j, l := range wrap(f.text, max(w-ansi.StringWidth(f.label)-8, 10)) {
				if j == 0 {
					lines = append(lines, " "+label+"  "+box+l)
				} else {
					lines = append(lines, " "+strings.Repeat(" ", ansi.StringWidth(f.label))+"      "+l)
				}
			}
			continue
		}
		lines = append(lines, " "+label+"  "+f.in.View())
	}
	lines = append(lines, "")
	if d.confirm {
		// Buttons to click, named with their keys: y (or enter) and n (or esc).
		yes, no := " y  Yes ", " n  No "
		d.buttons.line = len(lines)
		d.buttons.yes0, d.buttons.yes1 = 1, 1+ansi.StringWidth(yes)
		d.buttons.no0 = d.buttons.yes1 + 3
		d.buttons.no1 = d.buttons.no0 + ansi.StringWidth(no)
		yesStyle, noStyle := styleSel, styleSelDim
		if d.onNo {
			yesStyle, noStyle = styleSelDim, styleSel
		}
		lines = append(lines, " "+yesStyle.Render(yes)+"   "+noStyle.Render(no))
		switch {
		case d.yesOnly:
			// What can't be undone takes y or the button, never a stray enter.
			lines = append(lines, " "+styleMuted.Render("y or the button confirms; enter does not"))
		default:
			lines = append(lines, " "+styleMuted.Render("y / n · ← → move · enter takes the one shown · esc cancels"))
		}
	} else if d.notice {
		lines = append(lines, " "+styleMuted.Render("enter or esc closes"))
	} else {
		lines = append(lines, " "+styleMuted.Render("enter confirm · tab next field · esc cancel"))
	}
	b := box{lines: frameLines(d.title, lines, w, colorAccent)}
	b.x = max((m.width-b.width())/2, 0)
	b.y = max((m.height-len(b.lines))/3, 0)
	return b
}

func (d *dialog) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if !b.contains(msg.X, msg.Y) {
		m.overlay = nil
		return nil
	}
	if d.notice {
		return nil
	}
	if d.confirm {
		// Content starts one line and one column inside the border.
		if msg.Y != b.y+1+d.buttons.line {
			return nil
		}
		switch x := msg.X - b.x - 1; {
		case x >= d.buttons.yes0 && x < d.buttons.yes1:
			m.overlay = d.back
			return d.submit(m, nil)
		case x >= d.buttons.no0 && x < d.buttons.no1:
			m.overlay = d.back
			return d.declined(m)
		}
		return nil
	}
	// Fields sit right after the text block and one blank line.
	first := b.y + 1 + 1 + len(d.textLines(*m)) + 1
	if i := msg.Y - first; i >= 0 && i < len(d.fields) {
		if c := d.fields[i].check; c != nil && i == d.focus {
			*c = !*c
			return nil
		}
		return d.setFocus(i)
	}
	return nil
}

func (d *dialog) textLines(m Model) []string {
	var out []string
	for _, t := range d.text {
		out = append(out, wrap(t, max(m.dialogWidth()-2, 1))...)
	}
	return out
}

// ---- help ----

// help is the keys overlay. It is taller than most screens and wider than
// some, so it scrolls both ways: top and left are where it is scrolled to.
type help struct {
	top, left int
}

func newHelp() *help { return &help{} }

var helpText = []string{
	"Tree",
	"  ↑↓ jk  move            ←→ hl  fold / unfold     space  toggle",
	"  enter  open pane, view branch changes           tab  focus main",
	"  /      filter (/ ! keeps only the agents waiting for you, on every machine)",
	"  esc    clear filter    m  menu (or right-click)",
	"  !      next agent waiting for you",
	"  Q      review queue: what needs a decision, across every machine",
	"         / filters it · v checks a row · o its output · x dismisses it (and it stays dismissed)",
	"  B      broadcast: one message to the agents and terminals of the selection (terminals run it as a command)",
	"  f      files: browse the checkout of the selected project, or of a branch's worktree",
	"",
	"Create",
	"  t  new task: branch + worktree + an agent with a prompt (Attempts: try it several times);",
	"     on a machine, or a folder that isn't a git repository, the agent works right there",
	"  c  start an agent here: pick Claude, Codex, Gemini or OpenCode (click or 1-9)",
	"  n  terminal here       a  add or create a project",
	"  N  new folder in Agents, Terminals or SSH: a group of your own · drag rows in, x removes it",
	"  M  add machine: over ssh, or a new sandbox · sandboxes group under Sandboxes → provider",
	"  R  reconnect a machine    A  start or install any agent",
	"     m on a sandbox: start, stop or delete it (a sandbox runs, and costs, until stopped)",
	"  H  ssh from this computer to a host (listed under CLI → SSH; nothing installed there)",
	"     asks whether to save the host (default no): saved hosts stay under SSH to reconnect; x forgets one",
	"     extra ssh options (-o KexAlgorithms=… -p 2222) go in its SSH options field, and stay with a saved host",
	"     on a saved host or ssh session: e edit host, name, folder, options · K login without a password (copies your key)",
	"     a saved host goes in an SSH folder (N) as a host: its sessions follow it · a on an SSH folder adds a host to it",
	"  P  pair a phone with this computer: a QR code for conch web, to type into terminals too (v / r / f: less);",
	"     a click on its code copies the code, anywhere else in it the link (c, y)",
	"  r  rename (pane, machine) x  close / remove (a branch: worktree, then the branch)",
	"                            R  refresh git and PRs",
	"  o  open a branch's pull request                 y  copy name / path",
	"  b  git window for a branch ($ does too, here or in its changes; a click shows the changes):",
	"     any git command; tab shows the everyday ones (status, log, fetch, pull, push, commit,",
	"     stash, rebase, undo, …);",
	"     ctrl+t sends to an agent on the branch instead; a rebase that stops offers continue,",
	"     abort, or handing the conflict to the agent",
	"  m  menu for the row: on an agent's pane it holds Prompt… — its next message, refused while it waits",
	"     on an answer of its own, and it says when the work it started ends",
	"     Actions… holds your own commands from config.toml ([[actions]]), run in that checkout",
	"     with CONCH_MACHINE, CONCH_PROJECT, CONCH_BRANCH, CONCH_WORKTREE and CONCH_PANE set",
	"  i  agent setup: instructions, skills, MCP servers, and what a worktree lacks",
	"     in it: s gives the other agents this one's setup (it says what it would write first), u undoes that,",
	"     S installs or removes conch's own skill for the agent whose tab is open (Settings → Agents does every agent)",
	"  F  local files (.env, local agent settings) copied into new worktrees",
	"  W  clean up a project's worktrees: the finished ones (merged, folder gone) come ticked",
	"",
	"Splits and tabs (ctrl+b, then)",
	"  % or v split right   \" or - split down   x close split (ends its pane)   = equalize",
	"  ←→↑↓ focus   o next split   ; last split   q split numbers (then a digit)   { } swap",
	"  ctrl/alt+arrows resize (repeats)   space next layout   alt+1-5 even-h, even-v, main-h, main-v, tiled",
	"  u  open a link the pane printed: put back together across the lines and the box it was",
	"     drawn in, opened in the browser and copied — a login URL an agent asks you to visit",
	"  c new tab (a terminal in it)   C this split's branch changes in a tab   n / p next / previous",
	"  0-9 go to tab   l last tab",
	"  < > . move tab   w every tab, grouped",
	"  the tab bar lists the tabs of the Workspace, project, CLI, Agents, Terminals or SSH selected in the tree",
	"  a tab marked ! (amber) has an agent waiting for your answer",
	"  & close tab   , rename tab   z zoom   ! next waiting agent   : ask   d detach   ? this help",
	"  S type into every split of the tab at once (again to stop; synced borders turn amber)",
	"  N new workspace (own tabs and tree; + beside conch)   ( ) previous / next workspace   $ rename   X close workspace",
	"  each shows only its own: projects added in it (a), panes started in it, its machines (M), saved ssh hosts and folders",
	"  what belongs to no workspace is workspace 1's; closing one asks whether its agents and terminals move there or end   x takes out of this one if another has it",
	"  in the tree: v open in a split right · s below · O in a new tab (beside what the tab already shows)",
	"  mouse: click a split to focus it · drag borders to resize · drag a split's title onto another to swap them, onto a tab to move it there, onto + for a tab of its own · drag the bar on a pane's right edge to scroll its history · click tabs and × · drag a tab to reorder · + new tab, terminal, agent or ssh",
	"  mouse: click a link an agent printed to open it (alt+click inside an agent's own interface, which is owed its clicks)",
	"",
	"Pane and changes",
	"  ctrl+b then any other key → back to the tree    ctrl+b z  zoom",
	"  ctrl+b r  draw the pane again (stale text after a resize)",
	"  ctrl+b b  back to where the split was before the last jump (again returns)",
	"  ctrl+b [  scroll history (↑↓ pgup pgdn g) · wheel scrolls too",
	"    / search up · ? search down · n next · N previous (lower case matches either case)",
	"  ctrl+b M  alert when the pane goes quiet after output (a build finishing) · ctrl+b A  alert on output",
	"    a watched pane shows ~ (quiet) or # (output) in the tree until you look at it",
	"  changes: ↑↓ file · enter diff · esc back · y copy path / diff",
	"    space or a click in the ✓ column marks a file · c commit (the marked files, else all)",
	"    P push (the remote ahead? it offers to take its commits first) · p open a pull request",
	"    in a diff: space marks the hunk under ▸ · n / N next, previous hunk · c commits the marked hunks",
	"    e opens the file in $EDITOR at that hunk, in a pane of its own (on the worktree's machine)",
	"    s side by side or unified · a wide terminal starts side by side",
	"    the file list washes the row of a file being written and marks it ▌; an open diff re-reads as the",
	"    agent writes: ▌ marks what just changed, F follows it, R re-reads now",
	"    M merge into the base (undone if it conflicts) · D discard the branch and its worktree",
	"    A compare the attempts at this task (t runs a command in each, o shows its terminal)",
	"    T move the branch's worktree to another machine; its agents continue there (also T in the tree)",
	"  y in the tree copies a branch name or directory",
	"  Sessions (under a project): enter resume · / search titles and conversations · s share with another agent, here or on another machine",
	"    d delete · a agent filter · I resume all interrupted · x dismiss",
	"  Files (under a project, or f): enter open a folder or preview a file · h fold / up · / find in what is loaded",
	"    a type the path into the agent in this checkout · y copy path · d its diff · e open in $EDITOR",
	"    . hide dotfiles (shown: in a checkout they are the work) · i ignored files · w next checkout",
	"    r read again · icons: Settings → Theme",
	"",
	"Mouse",
	"  click select · double-click open · right-click menu · wheel scroll",
	"  drag in a pane or a page to copy · past the edge or with the wheel it scrolls · double-click copies a word",
	"  Y on an agent opens its conversation: scroll (g top · G bottom), drag over a part, release to copy",
	"  drag the sidebar edge to resize · shift-drag for terminal selection",
	"  drop files (a screenshot) on a remote pane: uploaded there, and their paths there pasted",
	"",
	"Brain",
	"  :  ask conch (ctrl+b : in a pane, or click ✦ Ask): \"start 2 agents on api to fix the flaky tests\",",
	"     \"what is waiting for me?\", \"hand the auth thread to Codex\", \"tell every agent in api to run the tests\"",
	"     — it proposes actions; nothing runs until you confirm",
	"  S  summarise the selected agent now (automatic summaries: Settings → Brain)",
	"",
	"Usage",
	"  title bar: ctx = context in use / window size · out = tokens generated · $ = reported cost",
	"  Sessions: what each saved conversation cost, or the tokens it generated",
	"  tree: an agent started by another is listed under it, folding away with it; elsewhere — another",
	"    section, another machine — it says ↳ for the pane that started it, and that pane counts",
	"    them (⑂2, amber with ! when one of them is waiting); Q, ! and notifications name it too",
	"  tree: each agent's cost, and the total per project and machine — what conch has seen,",
	"    Settings → Theme → Tree hides all of it",
	"    not the whole plan window. Agents that report no cost (Codex, Gemini) show tokens",
	"  a task warns before it starts when that agent's plan window is nearly used; it never refuses",
	"  status bar: Claude 5h 42% · 7d 18% = plan limit windows (click for the lot: resets, every agent, every machine)",
	"  status bar: ⚑ 1 waiting · 1 done = agents asking you something · agents that finished unseen",
	"    (done turns idle once you look); the tree's badges say ⚑1 ✓1 the same way; click goes to the next",
	"  status bar: ⬆ version = updates; click, tick machines with space, u updates",
	"  status bar: 🌐 = pair a phone (as P): its QR code, and conch web started from there if it isn't running;",
	"     Settings → Web sets the address phones open and the port, starts or stops conch web,",
	"     and Devices… lists paired phones: v / r / f what one may do, x twice revokes it",
	"  status bar: ▁▃▆ (bottom right) = memory and CPU conch uses on this computer; click again to close",
	"    conch update list shows every release · conch update rollback goes back one",
	"",
	"  this list: ↑↓ jk pgup pgdn g G or the wheel scroll it · ←→ hl (shift+wheel) pan long lines",
	"",
	"  ,  settings (or click ⚙): theme, prompt, notifications, agents, brain, sandboxes",
	"     Settings → Report a problem… (or ⊙ on the bar) is this conch's own facts, for an issue",
	"     Sandboxes lists the providers; enter opens one's own page, esc goes back to the list",
	"  q  detach (agents keep running)",
}

func (h *help) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	_, rows := h.view(*m)
	switch k.String() {
	case "up", "k":
		h.scroll(*m, -1, 0)
	case "down", "j":
		h.scroll(*m, 1, 0)
	case "pgup", "b":
		h.scroll(*m, -rows, 0)
	case "pgdown", " ", "f":
		h.scroll(*m, rows, 0)
	case "home", "g":
		h.scroll(*m, -len(helpText), 0)
	case "end", "G":
		h.scroll(*m, len(helpText), 0)
	case "left", "h":
		h.scroll(*m, 0, -helpStep)
	case "right", "l":
		h.scroll(*m, 0, helpStep)
	default:
		m.overlay = nil
		return true, nil
	}
	return false, nil
}

// helpStep is how far ← and → move the help sideways.
const helpStep = 8

// view is the help's inner width and how many of its lines fit on the
// screen: the frame takes a line above and below.
func (help) view(m Model) (w, rows int) {
	for _, l := range helpText {
		w = max(w, ansi.StringWidth(l)+2)
	}
	return min(w, max(m.width-4, 20)), max(m.height-2, 1)
}

// scroll moves the help by dy lines and dx columns, kept to its text, so a
// help taller or wider than the screen can still be read to the end.
func (h *help) scroll(m Model, dy, dx int) {
	w, rows := h.view(m)
	widest := 0
	for _, l := range helpText {
		widest = max(widest, ansi.StringWidth(l)+1)
	}
	h.top = clamp(h.top+dy, 0, max(len(helpText)-rows, 0))
	h.left = clamp(h.left+dx, 0, max(widest-w, 0))
}

func (h *help) render(m Model) box {
	w, rows := h.view(m)
	at := *h
	at.scroll(m, 0, 0) // a resize can leave the offsets past the end
	h = &at
	shown := helpText[h.top:min(h.top+rows, len(helpText))]
	lines := make([]string, 0, len(shown))
	for _, l := range shown {
		cut := ansi.TruncateLeft(l, h.left, "")
		if l != "" && !strings.HasPrefix(l, " ") {
			lines = append(lines, " "+styleBold.Render(cut))
		} else {
			lines = append(lines, " "+cut)
		}
	}
	title := " Keys · ↑↓ ←→ scroll · other keys close "
	b := box{lines: frameLinesBar(title, lines, w, colorAccent, helpThumb(h.top, rows, len(helpText)))}
	b.x = max((m.width-b.width())/2, 0)
	b.y = max((m.height-len(b.lines))/3, 0)
	return b
}

// helpThumb marks the right border with where the shown rows sit in a text
// of total lines, when there is more of it than fits.
func helpThumb(top, rows, total int) map[int]string {
	if total <= rows || rows < 3 {
		return nil
	}
	size := max(rows*rows/total, 1)
	from := clamp(top*(rows-size)/(total-rows), 0, rows-size)
	mark := map[int]string{}
	for i := from; i < from+size; i++ {
		mark[i] = styleThumb.Render(" ")
	}
	return mark
}

func (h *help) mouse(m *Model, msg tea.MouseMsg, _ box) tea.Cmd {
	switch {
	case msg.Button == tea.MouseButtonWheelLeft || msg.Shift && msg.Button == tea.MouseButtonWheelUp:
		h.scroll(*m, 0, -helpStep)
	case msg.Button == tea.MouseButtonWheelRight || msg.Shift && msg.Button == tea.MouseButtonWheelDown:
		h.scroll(*m, 0, helpStep)
	case msg.Button == tea.MouseButtonWheelUp:
		h.scroll(*m, -m.wheelStep(), 0)
	case msg.Button == tea.MouseButtonWheelDown:
		h.scroll(*m, m.wheelStep(), 0)
	case msg.Action == tea.MouseActionPress:
		m.overlay = nil
	}
	return nil
}

// wrap breaks text into lines of at most w cells at spaces. A line that
// already fits is kept as it is, blanks and indentation included, so a
// notice can lay out a list of commands.
func wrap(text string, w int) []string {
	if text == "" {
		return []string{""} // an empty line is a blank line, not nothing
	}
	if strings.TrimSpace(text) != "" && ansi.StringWidth(text) <= w {
		return []string{text}
	}
	var lines []string
	cur := ""
	for _, word := range strings.Fields(text) {
		switch {
		case cur == "":
			cur = word
		case ansi.StringWidth(cur)+1+ansi.StringWidth(word) <= w:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
