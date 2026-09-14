package tui

import (
	"fmt"
	"strings"

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
	bs := lipgloss.NewStyle().Foreground(color)
	title = ansi.Truncate(title, max(w-2, 0), "…")
	out := []string{bs.Render("╭─") + styleBold.Render(title) + bs.Render(strings.Repeat("─", max(w-1-ansi.StringWidth(title), 0))+"╮")}
	for _, l := range content {
		out = append(out, bs.Render("│")+fit(l, w)+bs.Render("│"))
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
			{"c", "Start an agent here…", act("c")},
			{"n", "New terminal here", act("n")},
			{"i", "Agent setup (skills, MCP, instructions)", act("i")},
			{"x", "Close", act("x")},
		}
	case kindBranch:
		title = r.branch
		items = []menuItem{
			{"enter", "View changes", enter},
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
		if proj := m.project(r.machine, r.projectID); proj != nil {
			for _, wt := range proj.Worktrees {
				if wt.Branch == r.branch && !wt.Main {
					items = append(items, menuItem{"x", "Remove worktree", act("x")})
				}
			}
		}
	case kindProject:
		if proj := m.project(r.machine, r.projectID); proj != nil {
			title = proj.Name
		}
		items = []menuItem{
			{"t", "New task (branch + worktree + agent)", act("t")},
			{"c", "Start an agent in project…", act("c")},
			{"n", "Open terminal in project", act("n")},
			{"i", "Agent setup (skills, MCP, instructions)", act("i")},
			{"B", "Broadcast to its agents…", act("B")},
			{"F", "Local files for new worktrees…", act("F")},
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
		switch {
		case mach.state == stateAttention && mid != localMachine:
			items = append(items, menuItem{"i", "Install / upgrade conch there", func(m *Model) tea.Cmd { return m.reconnect(mid, true) }})
		case mach.state == stateOffline && mid == localMachine:
			items = append(items, menuItem{"s", "Start the server", func(m *Model) tea.Cmd { return m.reconnect(mid, true) }})
		}
		if mach.state == stateOnline {
			items = append(items,
				menuItem{"c", "Start or install an agent…", act("c")},
				menuItem{"a", "Add project…", act("a")},
				menuItem{"n", "New terminal", act("n")},
			)
		}
		items = append(items, menuItem{"R", "Reconnect", act("R")})
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
		items = append(items, menuItem{"M", "Add machine…", act("M")})
		if mid != localMachine {
			items = append(items, menuItem{"x", "Remove machine", act("x")})
		}
	default:
		items = []menuItem{
			{"a", "Add project…", act("a")},
			{"c", "Start an agent…", act("c")},
			{"n", "New terminal", act("n")},
			{"M", "Add machine…", act("M")},
		}
	}
	return &menu{title: title, items: items, x: x, y: y}
}

func (mu *menu) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch k.String() {
	case "esc", "q", "m":
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
	w := ansi.StringWidth(mu.title) + 2
	for _, it := range mu.items {
		w = max(w, ansi.StringWidth(it.label)+10)
	}
	var lines []string
	for i, it := range mu.items {
		key := it.key
		if key == "enter" {
			key = "⏎"
		}
		line := " " + padRight(styleMuted.Render(key), 3) + " " + it.label
		if i == mu.sel {
			line = styleSel.Render(padRight(" "+padRight(key, 3)+" "+ansi.Strip(it.label), w))
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
	sel := -1
	for i, a := range list {
		a := a
		label := a.Label
		if label == "" {
			label = agentLabel(a.Name)
		}
		key := ""
		if i < 9 {
			key = fmt.Sprint(i + 1)
		}
		if a.Installed {
			detail := ""
			if a.Version != "" {
				detail = "  " + styleMuted.Render(a.Version)
			}
			if a.Name == m.defaultAgent() {
				detail += styleMuted.Render("  default")
				sel = len(items)
			} else if sel < 0 {
				sel = len(items)
			}
			items = append(items, menuItem{key, "Start " + label + detail, func(m *Model) tea.Cmd { return m.openAgent(a.Name) }})
		} else {
			items = append(items, menuItem{key, "Install " + label + styleMuted.Render("  not installed"), func(m *Model) tea.Cmd { return m.installAgent(mach.id, a.Name) }})
		}
	}
	items = append(items, menuItem{"n", "Open a terminal instead", func(m *Model) tea.Cmd { return m.openAgent("") }})
	return &menu{title: "Start an agent · " + m.placeLabel(), items: items, sel: max(sel, 0),
		x: max(m.width/2-25, 0), y: max(m.height/3, 0)}
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
}

type dialog struct {
	title   string
	text    []string
	fields  []field
	focus   int
	confirm bool // yes/no question without fields
	submit  func(m *Model, values []string) tea.Cmd
	// onChange runs after a field was edited, e.g. to update placeholders.
	onChange func(d *dialog)
}

func (m Model) dialogWidth() int { return clamp(72, 30, max(m.width-4, 30)) }

func newDialog(m Model, title string, text []string, labels []string, values []string) *dialog {
	d := &dialog{title: title, text: text}
	labelW := 0
	for _, l := range labels {
		labelW = max(labelW, len(l))
	}
	for i, l := range labels {
		in := textinput.New()
		in.Prompt = ""
		in.Width = m.dialogWidth() - labelW - 6
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
			func() tea.Msg { return flashMsg("added " + info.Name) })
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
	text = append(text, "If ssh needs a password or a host key answer, run `conch machine add TARGET` in a terminal instead.")
	d := newDialog(m, " Add machine ", text, []string{"SSH target", "Label"}, nil)
	d.fields[0].in.Placeholder = "user@host, host alias or ssh://user@host:port"
	d.fields[1].in.Placeholder = "defaults to the host name"
	d.submit = func(m *Model, v []string) tea.Cmd {
		target := strings.TrimSpace(v[0])
		if target == "" {
			return func() tea.Msg { return errMsg{errString("an ssh target is required")} }
		}
		m.setFlash("adding "+target+" (installing conch there if needed)…", false)
		return addMachine(target, strings.TrimSpace(v[1]))
	}
	return d
}

func newTaskDialog(m Model, mid string, proj proto.ProjectInfo) *dialog {
	d := newDialog(m, " New task · "+proj.Name+" ",
		[]string{"Creates a branch and worktree, then starts the agent with the prompt."},
		[]string{"Prompt", "Branch", "Base", "Agent"}, nil)
	d.fields[1].in.Placeholder = "derived from the prompt"
	d.fields[2].in.Placeholder = proj.Base
	d.fields[3].in.Placeholder = m.defaultAgent() + " (default · " + strings.Join(knownAgents(&m), ", ") + ")"
	defaultAgent := m.defaultAgent()
	d.onChange = func(d *dialog) {
		if p := strings.TrimSpace(d.fields[0].in.Value()); p != "" {
			d.fields[1].in.Placeholder = gitx.BranchFromPrompt(p)
		}
	}
	id := proj.ID
	d.submit = func(m *Model, v []string) tea.Cmd {
		if strings.TrimSpace(v[0]) == "" {
			return func() tea.Msg { return errMsg{errString("a task needs a prompt")} }
		}
		agent := strings.ToLower(strings.TrimSpace(v[3]))
		if agent == "" {
			agent = defaultAgent
		}
		if mach := m.machine(mid); mach != nil && mach.missingAgent(agent) {
			return func() tea.Msg { return askInstallMsg{machine: mid, agent: agent} }
		}
		cols, rows := m.paneArea()
		params := proto.TaskCreateParams{ProjectID: id, Prompt: v[0], Branch: strings.TrimSpace(v[1]),
			Base: strings.TrimSpace(v[2]), Agent: agent, Cols: cols, Rows: rows}
		var info proto.PaneInfo
		return m.callOn(mid, proto.MethodTaskCreate, params, &info, func() tea.Msg { return createdMsg{machine: mid, info: info} })
	}
	return d
}

type errString string

func (e errString) Error() string { return string(e) }

func (d *dialog) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, isKey := msg.(tea.KeyMsg)
	if d.confirm {
		if isKey {
			switch k.String() {
			case "y", "Y", "enter":
				m.overlay = nil
				return true, d.submit(m, nil)
			case "n", "N", "esc", "q":
				m.overlay = nil
				return true, nil
			}
		}
		return false, nil
	}
	if isKey {
		switch k.String() {
		case "esc":
			m.overlay = nil
			return true, nil
		case "tab", "down":
			return false, d.setFocus(d.focus + 1)
		case "shift+tab", "up":
			return false, d.setFocus(d.focus - 1)
		case "enter":
			values := make([]string, len(d.fields))
			for i, f := range d.fields {
				values[i] = f.in.Value()
			}
			m.overlay = nil
			return true, d.submit(m, values)
		}
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
		lines = append(lines, " "+label+"  "+f.in.View())
	}
	lines = append(lines, "")
	hint := "enter confirm · tab next field · esc cancel"
	if d.confirm {
		hint = "y yes · n no"
	}
	lines = append(lines, " "+styleMuted.Render(hint))
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
	// Fields sit right after the text block and one blank line.
	first := b.y + 1 + 1 + len(d.textLines(*m)) + 1
	if i := msg.Y - first; i >= 0 && i < len(d.fields) {
		return d.setFocus(i)
	}
	return nil
}

func (d *dialog) textLines(m Model) []string {
	var out []string
	for _, t := range d.text {
		out = append(out, wrap(t, m.dialogWidth()-2)...)
	}
	return out
}

// ---- help ----

type help struct{}

func newHelp() help { return help{} }

var helpText = []string{
	"Tree",
	"  ↑↓ jk  move            ←→ hl  fold / unfold     space  toggle",
	"  enter  open pane, view branch changes           tab  focus main",
	"  /      filter          esc  clear filter        m  menu (or right-click)",
	"  !      next agent waiting for you",
	"  B      broadcast: one message to every running agent of the selected machine, project, branch or CLI",
	"",
	"Create",
	"  t  new task: branch + worktree + an agent with a prompt",
	"  c  start an agent here: pick Claude, Codex, Gemini or OpenCode (click or 1-9)",
	"  n  terminal here       a  add or create a project",
	"  M  add machine (ssh)   R  reconnect a machine    A  start or install any agent",
	"  r  rename              x  close / remove        R  refresh git and PRs",
	"  o  open a branch's pull request                 y  copy name / path",
	"  i  agent setup: instructions, skills, MCP servers, and what a worktree lacks",
	"  F  local files (.env, local agent settings) copied into new worktrees",
	"",
	"Splits and tabs (ctrl+b, then)",
	"  % or v split right   \" or - split down   x close split (ends its pane)   = equalize",
	"  ←→↑↓ focus   o next split   ; last split   { } swap   ctrl/alt+arrows resize (repeats)",
	"  c new tab   n / p next / previous   0-9 go to tab   l last tab   w every tab, grouped",
	"  the tab bar lists the tabs of the project, CLI, Agents or Terminals selected in the tree",
	"  & close tab   , rename tab   z zoom   ! next waiting agent   : ask   d detach   ? this help",
	"  S type into every split of the tab at once (again to stop; synced borders turn amber)",
	"  in the tree: v open in a split right · s below · O in a new tab",
	"  mouse: click a split to focus it · drag borders to resize · click tabs, + and ×",
	"",
	"Pane and changes",
	"  ctrl+b then any other key → back to the tree    ctrl+b z  zoom",
	"  ctrl+b [  scroll history (↑↓ pgup pgdn g) · wheel scrolls too",
	"  changes: ↑↓ file · enter diff · esc back · y copy path / diff",
	"  y in the tree copies a branch name or directory",
	"  Sessions (under a project): enter resume · / search titles and conversations · s share with another agent",
	"    d delete · a agent filter · I resume all interrupted · x dismiss",
	"",
	"Mouse",
	"  click select · double-click open · right-click menu · wheel scroll",
	"  drag in a pane to copy · double-click copies a word",
	"  drag the sidebar edge to resize · shift-drag for terminal selection",
	"",
	"Brain",
	"  :  ask conch (ctrl+b : in a pane, or click ✦ Ask): \"start 2 agents on api to fix the flaky tests\",",
	"     \"what is waiting for me?\" — it proposes actions; nothing runs until you confirm",
	"  S  summarise the selected agent now (automatic summaries: Settings → Brain)",
	"",
	"Usage",
	"  title bar: ctx = context in use / window size · out = tokens generated · $ = reported cost",
	"  status bar: Claude 5h 42% · 7d 18% = plan limit windows (click for reset times)",
	"",
	"  ,  settings (or click ⚙): theme, prompt, notifications, agents, brain",
	"  q  detach (agents keep running)",
}

func (help) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		m.overlay = nil
		return true, nil
	}
	return false, nil
}

func (help) render(m Model) box {
	w := 0
	for _, l := range helpText {
		w = max(w, ansi.StringWidth(l)+2)
	}
	w = min(w, max(m.width-4, 20))
	lines := make([]string, 0, len(helpText))
	for _, l := range helpText {
		if l != "" && !strings.HasPrefix(l, " ") {
			lines = append(lines, " "+styleBold.Render(l))
		} else {
			lines = append(lines, " "+l)
		}
	}
	b := box{lines: frameLines(" Keys · any key closes ", lines, w, colorAccent)}
	b.x = max((m.width-b.width())/2, 0)
	b.y = max((m.height-len(b.lines))/3, 0)
	return b
}

func (help) mouse(m *Model, msg tea.MouseMsg, _ box) tea.Cmd {
	if msg.Action == tea.MouseActionPress {
		m.overlay = nil
	}
	return nil
}

// wrap breaks text into lines of at most w cells at spaces.
func wrap(text string, w int) []string {
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
