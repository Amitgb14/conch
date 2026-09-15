package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func a2MenuLabels(mu *menu) string {
	var out []string
	for _, it := range mu.items {
		out = append(out, it.key+" "+ansi.Strip(it.label))
	}
	return strings.Join(out, " | ")
}

func TestA2MenuNavigationAndShortcuts(t *testing.T) {
	m := a2Model()
	var ran []string
	item := func(key, label string) menuItem {
		return menuItem{key, label, func(m *Model) tea.Cmd { ran = append(ran, label); return nil }}
	}
	mu := &menu{title: "T", items: []menuItem{item("enter", "Open"), item("r", "Rename"), item("", "Hidden"), item("x", "Close")}}
	m.overlay = mu

	for _, step := range []struct {
		key  string
		want int
	}{{"down", 1}, {"j", 2}, {"tab", 3}, {"down", 0}, {"up", 3}, {"k", 2}, {"shift+tab", 1}} {
		if closed, _ := mu.update(m, a2Key(step.key)); closed || mu.sel != step.want {
			t.Fatalf("after %q: sel %d closed %v, want sel %d", step.key, mu.sel, closed, step.want)
		}
	}
	// Non-key messages and unknown keys change nothing.
	if closed, _ := mu.update(m, tickMsg{}); closed {
		t.Fatal("a tick closed the menu")
	}
	if closed, _ := mu.update(m, a2Key("z")); closed || len(ran) != 0 {
		t.Fatal("an unbound key ran something")
	}
	// "enter" is not a shortcut for the item keyed "enter"; it runs the selection.
	if closed, _ := mu.update(m, a2Key("x")); !closed || m.overlay != nil || ran[0] != "Close" {
		t.Fatalf("shortcut x: closed %v overlay %v ran %v", closed, m.overlay, ran)
	}
	m.overlay = mu
	mu.sel = 1
	if closed, _ := mu.update(m, a2Key("enter")); !closed || ran[1] != "Rename" {
		t.Fatalf("enter ran %v", ran)
	}
	for _, k := range []string{"esc", "q", "m"} {
		m.overlay = mu
		if closed, _ := mu.update(m, a2Key(k)); !closed || m.overlay != nil {
			t.Fatalf("%s should close the menu", k)
		}
	}
	if len(ran) != 2 {
		t.Fatalf("closing ran items: %v", ran)
	}

	// Rendering: the selected row is highlighted, the box stays on screen.
	mu.x, mu.y, mu.sel = 1000, 1000, 3
	b := mu.render(*m)
	a2CheckBox(t, b, *m)
	if b.x+b.width() != m.width || b.y+len(b.lines) != m.height {
		t.Fatalf("menu not clamped to the corner: %d,%d", b.x, b.y)
	}
	out := a2Plain(b.lines)
	for _, want := range []string{" T ", "⏎", "Rename", "Close"} {
		if !strings.Contains(out, want) {
			t.Fatalf("menu lacks %q:\n%s", want, out)
		}
	}

	// Mouse: hover selects, click runs, a press outside closes.
	m.overlay = mu
	mu.mouse(m, tea.MouseMsg{X: b.x + 2, Y: b.y + 2, Action: tea.MouseActionMotion}, b)
	if mu.sel != 1 {
		t.Fatalf("hover selected %d", mu.sel)
	}
	mu.mouse(m, tea.MouseMsg{X: b.x + 2, Y: b.y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if m.overlay != nil || ran[len(ran)-1] != "Open" {
		t.Fatalf("click ran %v", ran)
	}
	m.overlay = mu
	mu.mouse(m, tea.MouseMsg{X: b.x + 2, Y: b.y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b) // the border
	if m.overlay == nil {
		t.Fatal("clicking the title closed the menu")
	}
	mu.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if m.overlay != nil {
		t.Fatal("a click outside should close the menu")
	}
}

func TestA2BoxContains(t *testing.T) {
	b := box{lines: []string{"abcd", "efgh"}, x: 2, y: 3}
	for _, c := range []struct {
		x, y int
		want bool
	}{{2, 3, true}, {5, 4, true}, {6, 4, false}, {1, 3, false}, {2, 5, false}, {2, 2, false}} {
		if got := b.contains(c.x, c.y); got != c.want {
			t.Errorf("contains(%d,%d) = %v", c.x, c.y, got)
		}
	}
	if (box{}).width() != 0 || (box{}).contains(0, 0) {
		t.Fatal("an empty box has no area")
	}
}

func TestA2RowMenus(t *testing.T) {
	m := a2Model()
	m.machines = append(m.machines,
		&machine{id: "box", label: "box", target: "box", state: stateAttention},
		&machine{id: "gpu", label: "gpu", target: "gpu", state: stateOnline, c: a2Client("server.reload.v1")},
	)

	mu := newRowMenu(*m, row{kind: kindPane, machine: localMachine, paneID: "p1"}, 3, 4)
	if mu.title != "claude" || mu.x != 3 || mu.y != 4 || !strings.HasPrefix(a2MenuLabels(mu), "enter Open | r Rename") {
		t.Fatalf("pane menu %q: %s", mu.title, a2MenuLabels(mu))
	}

	mu = newRowMenu(*m, row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"}, 0, 0)
	got := a2MenuLabels(mu)
	if mu.title != "feat" || !strings.HasPrefix(got, "enter View changes | o Open pull request #7 | c ") || !strings.HasSuffix(got, "x Remove worktree") {
		t.Fatalf("branch menu: %s", got)
	}
	if got := a2MenuLabels(newRowMenu(*m, row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "main"}, 0, 0)); strings.Contains(got, "Remove worktree") || strings.Contains(got, "pull request") {
		t.Fatalf("the main checkout without a PR: %s", got)
	}

	mu = newRowMenu(*m, row{id: "project:r1", kind: kindProject, machine: localMachine, projectID: "r1"}, 0, 0)
	if mu.title != "api" || !strings.Contains(a2MenuLabels(mu), " Show all branches") {
		t.Fatalf("project menu: %s", a2MenuLabels(mu))
	}
	for i, it := range mu.items {
		if it.label == "Show all branches" {
			mu.run(m, i)
		}
	}
	if !m.showAll[scoped(localMachine, "r1")] || !m.expanded["project:r1"] || !m.expanded[sectionID(localMachine, "r1", "branches")] {
		t.Fatalf("show all branches: %v %v", m.showAll, m.expanded)
	}

	// Machines: what is offered follows the machine's state.
	m.machines[0].state = stateOffline
	if got := a2MenuLabels(newRowMenu(*m, row{kind: kindMachine, machine: localMachine}, 0, 0)); got != "s Start the server | R Reconnect | M Add machine…" {
		t.Fatalf("offline local machine: %s", got)
	}
	if got := a2MenuLabels(newRowMenu(*m, row{kind: kindMachine, machine: "box"}, 0, 0)); got != "i Install / upgrade conch there | R Reconnect | r Rename… | M Add machine… | x Remove machine" {
		t.Fatalf("remote needing attention: %s", got)
	}
	mu = newRowMenu(*m, row{kind: kindMachine, machine: "gpu"}, 0, 0)
	got = a2MenuLabels(mu)
	for _, want := range []string{"c Start or install an agent…", "a Add project…", " Reload server onto the installed build (keeps panes)", " Restart server (stops its panes)…", "x Remove machine"} {
		if !strings.Contains(got, want) {
			t.Fatalf("online remote lacks %q: %s", want, got)
		}
	}
	// Restart asks first; no answers "no".
	m.machines[0].state = stateOnline
	mu = newRowMenu(*m, row{kind: kindMachine, machine: localMachine}, 0, 0)
	for i, it := range mu.items {
		if strings.HasPrefix(it.label, "Restart server") {
			mu.run(m, i)
		}
	}
	d, ok := m.overlay.(*dialog)
	if !ok || !d.confirm || !strings.Contains(d.text[0], "Restart the conch server on local?") {
		t.Fatalf("restart confirm: %#v", m.overlay)
	}
	if closed, cmd := d.update(m, a2Key("n")); !closed || cmd != nil || m.overlay != nil {
		t.Fatal("n should cancel the restart")
	}
	// Yes with no connection does nothing.
	m.overlay = d
	if closed, cmd := d.update(m, a2Key("y")); !closed || cmd != nil {
		t.Fatal("restarting an unconnected server should do nothing")
	}

	if newRowMenu(*m, row{kind: kindMachine, machine: "nope"}, 0, 0).items != nil {
		t.Fatal("an unknown machine has no menu items")
	}
	if got := a2MenuLabels(newRowMenu(*m, row{kind: kindAgents}, 0, 0)); got != "a Add project… | c Start an agent… | n New terminal | M Add machine…" {
		t.Fatalf("default menu: %s", got)
	}
}

func TestA2AgentMenuWithoutAgentList(t *testing.T) {
	m := a2Model()
	m.cfg.Agents.Default = "gemini"
	mu := newAgentMenu(*m, m.machines[0])
	if got := a2MenuLabels(mu); got != "1 Start Claude Code | 2 Start Codex | 3 Start Gemini CLI  default | 4 Start OpenCode | n Open a terminal instead" || mu.sel != 2 {
		t.Fatalf("older server picker (sel %d): %s", mu.sel, got)
	}
	// Starting an agent without a connection reports the machine offline.
	if msg := a2ErrText(a2Run(mu.items[0].run(m))); msg != "local is online" {
		t.Fatalf("start offline: %q", msg)
	}
	missing := &machine{id: "box", label: "box", state: stateOnline, agentList: []proto.AgentAvailability{{Name: "codex", Label: "Codex"}}}
	m.machines = append(m.machines, missing)
	mu = newAgentMenu(*m, missing)
	if msg := a2ErrText(a2Run(mu.items[0].run(m))); msg != "box is online" {
		t.Fatalf("install offline: %q", msg)
	}
}

func TestA2PlaceLabel(t *testing.T) {
	m := a2Model()
	m.rows = []row{{id: "b", kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"},
		{id: "p", kind: kindProject, machine: localMachine, projectID: "r1"},
		{id: "x", kind: kindMachine, machine: "ghost"}}
	for cursor, want := range map[string]string{"b": "feat · api on local", "p": "api on local", "x": "ghost"} {
		m.cursor = cursor
		if got := m.placeLabel(); got != want {
			t.Errorf("cursor %s: %q, want %q", cursor, got, want)
		}
	}
}

func TestA2DialogEditing(t *testing.T) {
	m := a2Model()
	var got []string
	d := newDialog(*m, " Two ", []string{"Some explanatory text that is long enough to wrap across more than one line of the dialog box when it is drawn."},
		[]string{"Name", "Longer label"}, []string{"alpha"})
	d.submit = func(m *Model, v []string) tea.Cmd { got = v; return nil }
	changes := 0
	d.onChange = func(*dialog) { changes++ }
	m.overlay = d
	if d.focusCmd() == nil || d.fields[0].label != "Name        " || d.fields[0].in.Value() != "alpha" || !d.fields[0].in.Focused() {
		t.Fatalf("fields: %q %q", d.fields[0].label, d.fields[0].in.Value())
	}
	a2Type(m, d, "!")
	if changes != 1 || d.fields[0].in.Value() != "alpha!" {
		t.Fatalf("typing: %q after %d changes", d.fields[0].in.Value(), changes)
	}
	for _, step := range []struct {
		key  string
		want int
	}{{"tab", 1}, {"down", 0}, {"shift+tab", 1}, {"up", 0}, {"up", 1}} {
		d.update(m, a2Key(step.key))
		if d.focus != step.want || !d.fields[step.want].in.Focused() || d.fields[1-step.want].in.Focused() {
			t.Fatalf("after %s focus is %d", step.key, d.focus)
		}
	}
	a2Type(m, d, "beta")
	b := d.render(*m)
	a2CheckBox(t, b, *m)
	out := a2Plain(b.lines)
	if !strings.Contains(out, "enter confirm · tab next field · esc cancel") || !strings.Contains(out, "alpha!") {
		t.Fatalf("dialog render:\n%s", out)
	}

	// Clicks focus a field; a click outside cancels.
	first := b.y + 2 + len(d.textLines(*m)) + 1
	if d.mouse(m, tea.MouseMsg{X: b.x + 3, Y: first, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b); d.focus != 0 {
		t.Fatalf("click on the first field focused %d", d.focus)
	}
	d.mouse(m, tea.MouseMsg{X: b.x + 3, Y: first + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonRight}, b)
	d.mouse(m, tea.MouseMsg{X: b.x + 3, Y: first + 1, Action: tea.MouseActionMotion}, b)
	d.mouse(m, tea.MouseMsg{X: b.x + 3, Y: b.y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if d.focus != 0 || m.overlay == nil {
		t.Fatal("only left clicks on fields move focus")
	}

	if closed, _ := d.update(m, a2Key("enter")); !closed || m.overlay != nil || len(got) != 2 || got[0] != "alpha!" || got[1] != "beta" {
		t.Fatalf("submit: closed %v values %q", closed, got)
	}
	m.overlay = d
	got = nil
	if closed, _ := d.update(m, a2Key("esc")); !closed || m.overlay != nil || got != nil {
		t.Fatal("esc should cancel without submitting")
	}
	m.overlay = d
	d.mouse(m, tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if m.overlay != nil {
		t.Fatal("a click outside should cancel")
	}

	// A narrow screen narrows the dialog but never below 30.
	if w := (Model{width: 50}).dialogWidth(); w != 46 {
		t.Fatalf("dialog width at 50 columns: %d", w)
	}
	if w := (Model{width: 10}).dialogWidth(); w != 30 {
		t.Fatalf("dialog width at 10 columns: %d", w)
	}
	if (&dialog{}).setFocus(3) != nil {
		t.Fatal("focusing in a dialog without fields")
	}
}

func TestA2Confirm(t *testing.T) {
	m := a2Model()
	yes := 0
	for _, c := range []struct {
		key    string
		closed bool
		yes    int
	}{{"y", true, 1}, {"Y", true, 2}, {"enter", true, 3}, {"n", true, 3}, {"N", true, 3}, {"esc", true, 3}, {"q", true, 3}, {"z", false, 3}} {
		d := newConfirm("Really?", func(*Model) tea.Cmd { yes++; return nil })
		m.overlay = d
		closed, _ := d.update(m, a2Key(c.key))
		if closed != c.closed || yes != c.yes || (closed && m.overlay != nil) {
			t.Fatalf("%q: closed %v yes %d", c.key, closed, yes)
		}
	}
	d := newConfirm("Really?", func(*Model) tea.Cmd { return nil })
	if closed, _ := d.update(m, tickMsg{}); closed {
		t.Fatal("a tick answered the question")
	}
	b := d.render(*m)
	a2CheckBox(t, b, *m)
	if out := a2Plain(b.lines); !strings.Contains(out, "y yes · n no") || !strings.Contains(out, "Really?") || !strings.Contains(out, "Confirm") {
		t.Fatalf("confirm render:\n%s", out)
	}
}

func TestA2RenameDialog(t *testing.T) {
	m := a2Model()
	d := newRenameDialog(*m, localMachine, proto.PaneInfo{ID: "p1", Name: "claude", Title: "Fix login", Agent: &proto.AgentStatus{Name: "claude"}})
	if d.fields[0].in.Value() != "" || d.fields[0].in.Placeholder != "Fix login" {
		t.Fatalf("an unrenamed pane: %q placeholder %q", d.fields[0].in.Value(), d.fields[0].in.Placeholder)
	}
	d = newRenameDialog(*m, localMachine, proto.PaneInfo{ID: "p1", Name: "mine", CustomName: true})
	if d.fields[0].in.Value() != "mine" {
		t.Fatalf("a renamed pane keeps its name: %q", d.fields[0].in.Value())
	}
	m.overlay = d
	_, cmd := d.update(m, a2Key("enter"))
	if msg := a2ErrText(a2Run(cmd)); msg != "local is online" {
		t.Fatalf("rename without a connection: %q", msg)
	}
	if msg := a2ErrText(a2Run(newRenameDialog(*m, "gone", proto.PaneInfo{ID: "x"}).submit(m, []string{"n"}))); msg != "unknown machine gone" {
		t.Fatalf("rename on an unknown machine: %q", msg)
	}
}

func TestA2AddProjectDialog(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	d := newAddProjectDialog(*m, localMachine)
	if d.title != " Add project " || d.fields[0].in.Value() == "" {
		t.Fatalf("local add project: %q %q", d.title, d.fields[0].in.Value())
	}
	m.machines = append(m.machines, &machine{id: "box", label: "devbox", state: stateOffline})
	d = newAddProjectDialog(*m, "box")
	if d.title != " Add project on devbox " || d.fields[0].in.Value() != "~" {
		t.Fatalf("remote add project: %q %q", d.title, d.fields[0].in.Value())
	}
	if msg := a2ErrText(a2Run(d.submit(m, []string{"  ~/src  "}))); msg != "devbox is offline" {
		t.Fatalf("add offline: %q", msg)
	}
}

func TestA2AddMachineDialog(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	d := newAddMachineDialog(*m)
	if len(d.fields) != 4 || !strings.Contains(strings.Join(d.text, " "), "never saved") || d.fields[3].check == nil || !*d.fields[3].check {
		t.Fatalf("add machine dialog: %d fields %+v", len(d.fields), d.text)
	}
	if strings.Contains(strings.Join(d.text, " "), "Hosts in ~/.ssh/config") {
		t.Fatal("no ssh config in an empty home")
	}
	type call struct {
		target, label, password string
		key                     bool
	}
	var got []call
	old := addMachineFn
	addMachineFn = func(target, label, password string, key bool) tea.Cmd {
		got = append(got, call{target, label, password, key})
		return func() tea.Msg { return nil }
	}
	t.Cleanup(func() { addMachineFn = old })

	if msg := a2ErrText(a2Run(d.submit(m, []string{"   ", "x", "", "on"}))); msg != "an ssh target is required" || len(got) != 0 {
		t.Fatalf("empty target: %q %v", msg, got)
	}

	// Typed through the dialog: the password is masked when drawn.
	m.overlay = d
	a2Type(m, d, "dev@10.0.0.115")
	d.update(m, a2Key("tab"))
	d.update(m, a2Key("tab"))
	a2Type(m, d, "s3cret pw")
	out := ansi.Strip(strings.Join(d.render(*m).lines, "\n"))
	if strings.Contains(out, "s3cret") || !strings.Contains(out, "•••") || !strings.Contains(out, "[x] with a password, add my ssh key") {
		t.Fatalf("render:\n%s", out)
	}
	// Space on the checkbox toggles it and types nothing.
	d.update(m, a2Key("tab"))
	d.update(m, a2Key(" "))
	if *d.fields[3].check || !strings.Contains(ansi.Strip(strings.Join(d.render(*m).lines, "\n")), "[ ] with a password") {
		t.Fatal("space didn't untick")
	}
	d.update(m, a2Key("x"))
	if !*d.fields[3].check {
		t.Fatal("x didn't tick")
	}
	d.update(m, a2Key("q")) // no text goes into a checkbox
	d.update(m, a2Key("enter"))
	if len(got) != 1 || got[0] != (call{"dev@10.0.0.115", "", "s3cret pw", true}) || !strings.Contains(m.flash, "adding dev@10.0.0.115") {
		t.Fatalf("submit: %+v flash %q", got, m.flash)
	}
	// Unticked, the value says so; a click on the focused checkbox toggles it.
	d2 := newAddMachineDialog(*m)
	a2Run(d2.submit(m, []string{"host", "box", "", ""}))
	if got[1] != (call{"host", "box", "", false}) {
		t.Fatalf("no password: %+v", got[1])
	}
	d2.focus = 3
	b := d2.render(*m)
	first := b.y + 1 + 1 + len(d2.textLines(*m)) + 1
	d2.mouse(m, tea.MouseMsg{X: b.x + 5, Y: first + 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, b)
	if *d2.fields[3].check {
		t.Fatal("click on the checkbox didn't toggle")
	}
}

func TestA2TaskDialog(t *testing.T) {
	m := a2Model()
	proj := m.machines[0].projects[0]
	d := newTaskDialog(*m, localMachine, proj)
	if len(d.fields) != 4 || d.fields[2].in.Placeholder != "main" || !strings.HasPrefix(d.fields[3].in.Placeholder, "claude (default · ") {
		t.Fatalf("task dialog placeholders: %q %q", d.fields[2].in.Placeholder, d.fields[3].in.Placeholder)
	}
	m.overlay = d
	a2Type(m, d, "Fix the login bug")
	if ph := d.fields[1].in.Placeholder; ph == "derived from the prompt" || ph == "" {
		t.Fatalf("branch placeholder should follow the prompt: %q", ph)
	}

	if msg := a2ErrText(a2Run(d.submit(m, []string{"  ", "", "", ""}))); msg != "a task needs a prompt" {
		t.Fatalf("empty prompt: %q", msg)
	}
	m.machines[0].available = map[string]proto.AgentAvailability{"claude": {Name: "claude", Installed: true}}
	msgs := a2Run(d.submit(m, []string{"do it", "", "", " Codex "}))
	if ask, ok := msgs[0].(askInstallMsg); !ok || ask.agent != "codex" || ask.machine != localMachine {
		t.Fatalf("a missing agent asks to install: %#v", msgs)
	}
	if msg := a2ErrText(a2Run(d.submit(m, []string{"do it", "", "", ""}))); msg != "local is online" {
		t.Fatalf("task without a connection: %q", msg)
	}
	_, cmd := d.update(m, a2Key("enter"))
	if msg := a2ErrText(a2Run(cmd)); msg != "local is online" || m.overlay != nil {
		t.Fatalf("enter submits: %q", msg)
	}
}

func TestA2Help(t *testing.T) {
	m := a2Model()
	h := newHelp()
	m.overlay = h
	b := h.render(*m)
	a2CheckBox(t, b, *m)
	out := a2Plain(b.lines)
	if !strings.Contains(out, "Keys · any key closes") || !strings.Contains(out, "Splits and tabs") {
		t.Fatalf("help:\n%s", out)
	}
	narrow := Model{width: 40, height: 20}
	if nb := h.render(narrow); nb.width() != 38 {
		t.Fatalf("narrow help is %d wide", nb.width())
	}
	if closed, _ := h.update(m, tickMsg{}); closed || m.overlay == nil {
		t.Fatal("a tick closed help")
	}
	h.mouse(m, tea.MouseMsg{Action: tea.MouseActionMotion}, b)
	if m.overlay == nil {
		t.Fatal("moving the mouse closed help")
	}
	h.mouse(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, b)
	if m.overlay != nil {
		t.Fatal("a click should close help")
	}
	m.overlay = h
	if closed, _ := h.update(m, a2Key("x")); !closed || m.overlay != nil {
		t.Fatal("any key closes help")
	}
}

func TestA2Wrap(t *testing.T) {
	if got := strings.Join(wrap("one two three four", 9), "|"); got != "one two|three|four" {
		t.Fatalf("wrap: %q", got)
	}
	if wrap("   ", 5) != nil {
		t.Fatal("blank text has no lines")
	}
	if got := strings.Join(wrap("supercalifragilistic word", 5), "|"); got != "supercalifragilistic|word" {
		t.Fatalf("long words stay whole: %q", got)
	}
}
