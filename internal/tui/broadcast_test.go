package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func targetIDs(ts []broadcastTarget) string {
	var ids []string
	for _, t := range ts {
		id := t.pane.ID
		if !t.on {
			id += "(off)"
		}
		ids = append(ids, id)
	}
	return strings.Join(ids, ",")
}

// broadcastFixture: p1 claude on api·feat, p2 zsh in api, p3 bash (CLI),
// p4 codex (CLI), plus p5 codex on api·main waiting for an answer and p6 an
// exited agent.
func broadcastFixture(t *testing.T) (*Model, *a1Peer) {
	t.Helper()
	m, peer := a1Fixture(t, true)
	mach := m.machines[0]
	mach.panes = append(mach.panes,
		proto.PaneInfo{ID: "p5", Name: "codex", State: proto.PaneRunning, ProjectID: "r1", Branch: "main", Agent: &proto.AgentStatus{Name: "codex", State: proto.AgentBlocked}},
		proto.PaneInfo{ID: "p6", Name: "old", State: proto.PaneExited, ProjectID: "r1", Agent: &proto.AgentStatus{Name: "claude"}},
	)
	m.rebuild()
	return m, peer
}

func TestBroadcastScope(t *testing.T) {
	m, _ := broadcastFixture(t)
	for _, c := range []struct {
		row, label, want string
	}{
		{machineID(localMachine), "local", "p1,p5(off),p4"},
		{projectNodeID(localMachine, "r1"), "api", "p1,p5(off)"},
		{sectionID(localMachine, "r1", "agents"), "api", "p1,p5(off)"},
		{sectionID(localMachine, "r1", "terminals"), "api", "p1,p5(off)"},
		{branchNodeID(localMachine, "r1", "feat"), "api · feat", "p1"},
		{branchNodeID(localMachine, "r1", "main"), "api · main", "p5(off)"},
		{cliID(localMachine), "CLI", "p4"},
		{machineID(localMachine) + "/agents", "CLI", "p4"},
		{paneNodeID(localMachine, "p1"), "api", "p1,p5(off)"},
		{paneNodeID(localMachine, "p2"), "api", "p1,p5(off)"}, // a shell selects its group, never itself
		{paneNodeID(localMachine, "p3"), "CLI", "p4"},
	} {
		if indexOfRow(m.rows, c.row) < 0 {
			t.Fatalf("no row %s", c.row)
		}
		m.cursor = c.row
		label, targets := m.broadcastScope(false)
		if label != c.label || targetIDs(targets) != c.want {
			t.Errorf("%s: %q %s, want %q %s", c.row, label, targetIDs(targets), c.label, c.want)
		}
	}
	if label, targets := m.broadcastScope(true); label != "every machine" || targetIDs(targets) != "p1,p5(off),p4" {
		t.Errorf("every: %q %s", label, targetIDs(targets))
	}
	m.cursor = ""
	if label, _ := m.broadcastScope(false); label != "every machine" {
		t.Errorf("no selection: %q", label)
	}

	// A second machine: labels name machines; an offline one contributes nothing.
	box := newMachine("box", "devbox", "dev@box")
	box.c = m.machines[0].c
	box.panes = []proto.PaneInfo{{ID: "p1", Name: "gemini", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "gemini"}}}
	off := newMachine("off", "offline", "x")
	off.panes = []proto.PaneInfo{{ID: "p9", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "claude"}}}
	m.machines = append(m.machines, box, off)
	m.cursor = projectNodeID(localMachine, "r1")
	if label, targets := m.broadcastScope(false); label != "local › api" || !strings.HasPrefix(targets[0].group, "local › api") {
		t.Errorf("multi-machine label %q groups %+v", label, targets)
	}
	_, targets := m.broadcastScope(true)
	machines := map[string]int{}
	for _, t := range targets {
		machines[t.machine]++
	}
	if machines["box"] != 1 || machines["off"] != 0 || machines[localMachine] != 3 {
		t.Errorf("every machine: %v", machines)
	}
}

func TestOpenBroadcast(t *testing.T) {
	m, _ := broadcastFixture(t)

	// Nothing running in the selection, but elsewhere.
	m.machines[0].panes = m.machines[0].panes[:4] // drop p5, p6
	m.machines[0].panes[0].Agent = nil            // p1 no longer an agent
	a1At(t, m, projectNodeID(localMachine, "r1"))
	if m.openBroadcast(); m.overlay != nil || !strings.Contains(m.flash, "no agents running in api") {
		t.Fatalf("empty group: %q", m.flash)
	}
	// Nothing anywhere.
	m.machines[0].panes[3].Agent = nil
	if m.openBroadcast(); m.overlay != nil || !strings.Contains(m.flash, "shells never get broadcasts") {
		t.Fatalf("none: %q", m.flash)
	}

	// B in the tree and the project menu open it.
	m, _ = broadcastFixture(t)
	a1At(t, m, projectNodeID(localMachine, "r1"))
	a1Key(t, m, runes("B"))
	if _, ok := m.overlay.(*broadcastDialog); !ok {
		t.Fatalf("B: %T", m.overlay)
	}
	m.overlay = nil
	r, _ := m.selectedRow()
	mu := newRowMenu(*m, r, 0, 0)
	for i, it := range mu.items {
		if it.key == "B" {
			mu.run(m, i)
		}
	}
	if _, ok := m.overlay.(*broadcastDialog); !ok {
		t.Fatalf("menu: %T", m.overlay)
	}
}

func TestBroadcastDialog(t *testing.T) {
	m, peer := broadcastFixture(t)
	m.machines[0].c, peer = a1FakeClient(t, "agent.broadcast.v1")
	a1At(t, m, machineID(localMachine))
	m.openBroadcast()
	d := m.overlay.(*broadcastDialog)
	key := func(k tea.KeyMsg) tea.Cmd {
		t.Helper()
		_, cmd := m.overlay.update(m, k)
		return cmd
	}

	// Enter without a message, then without recipients: errors, no confirm.
	key(a2Key("enter"))
	if d.err != "type the message first" || m.overlay != d {
		t.Fatalf("empty message: %q", d.err)
	}
	for _, r := range "run the tests" {
		if r == ' ' {
			key(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
		} else {
			key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
	if d.in.Value() != "run the tests" || d.err != "" {
		t.Fatalf("typed %q (err %q)", d.in.Value(), d.err)
	}
	// Letters that are list keys are text while typing.
	key(runes("a"))
	key(tea.KeyMsg{Type: tea.KeyBackspace})

	key(a2Key("tab"))
	if !d.list {
		t.Fatal("tab should move to the list")
	}
	key(runes("a")) // tick all (p5 was off)
	if targetIDs(d.targets) != "p1,p5,p4" {
		t.Fatalf("a: %s", targetIDs(d.targets))
	}
	key(runes("a")) // none
	if targetIDs(d.targets) != "p1(off),p5(off),p4(off)" {
		t.Fatalf("a again: %s", targetIDs(d.targets))
	}
	key(a2Key("enter"))
	if d.err != "tick at least one agent (space)" {
		t.Fatalf("no recipients: %q", d.err)
	}
	key(runes(" ")) // p1 on
	key(a2Key("down"))
	key(a2Key("down"))
	key(runes("x")) // p4 on
	key(a2Key("down"))
	if d.sel != 2 || targetIDs(d.targets) != "p1,p5(off),p4" {
		t.Fatalf("ticks: sel %d %s", d.sel, targetIDs(d.targets))
	}
	// Up past the first row returns to the message.
	key(a2Key("up"))
	key(a2Key("up"))
	key(a2Key("up"))
	if d.list {
		t.Fatal("up from the first row should return to the message")
	}
	key(a2Key("down"))
	if !d.list {
		t.Fatal("down from the message should enter the list")
	}

	// e widens to every machine and back, keeping ticks.
	key(runes("e"))
	if !d.every || d.label != "every machine" || targetIDs(d.targets) != "p1,p5(off),p4" {
		t.Fatalf("widen: %q %s", d.label, targetIDs(d.targets))
	}
	key(runes("e"))
	if d.every || d.label != "local" {
		t.Fatalf("narrow: %q", d.label)
	}

	// Render fits and names the state of each agent.
	for _, w := range []int{40, 80, 160} {
		m.width = w
		b := d.render(*m)
		for i, l := range b.lines {
			if ansi.StringWidth(l) > max(w, 34) {
				t.Fatalf("width %d: line %d is %d wide", w, i, ansi.StringWidth(l))
			}
		}
	}
	m.width = 160
	out := ansi.Strip(strings.Join(d.render(*m).lines, "\n"))
	for _, want := range []string{"To 2 of 3 agents in local", "[x] codex", "[ ] codex", "! waiting for an answer", "space tick"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}

	// Review: tick the waiting agent too; the confirmation warns.
	d.sel = 1
	key(runes(" "))
	key(a2Key("enter"))
	c, ok := m.overlay.(*broadcastConfirm)
	if !ok {
		t.Fatalf("confirm: %T", m.overlay)
	}
	q := strings.Join(c.text, " ")
	if !strings.Contains(q, "Send “run the tests” to 3 agents") || !strings.Contains(q, "1 of them is waiting for an answer") {
		t.Fatalf("question: %q", q)
	}
	// n goes back with the message kept; enter again, then yes sends.
	key(runes("n"))
	if m.overlay != d || d.in.Value() != "run the tests" {
		t.Fatal("n should return to the dialog")
	}
	key(a2Key("enter"))
	cmd := key(runes("y"))
	if m.overlay != nil || cmd == nil {
		t.Fatal("yes should send")
	}
	a2Run(cmd)
	msg := peer.waitMethod(t, proto.MethodAgentBroadcast, `"text":"run the tests"`)
	for _, id := range []string{"p1", "p4", "p5"} {
		if !strings.Contains(string(msg.Params), `"`+id+`"`) {
			t.Fatalf("params %s lack %s", msg.Params, id)
		}
	}

	// esc cancels.
	m.openBroadcast()
	key(a2Key("esc"))
	if m.overlay != nil {
		t.Fatal("esc")
	}
}

func TestBroadcastDialogMouseAndScroll(t *testing.T) {
	m, _ := broadcastFixture(t)
	mach := m.machines[0]
	for i := 0; i < 14; i++ {
		mach.panes = append(mach.panes, proto.PaneInfo{ID: "q" + itoa(i), Name: "agent" + itoa(i), State: proto.PaneRunning, ProjectID: "r1",
			Agent: &proto.AgentStatus{Name: "claude"}})
	}
	a1At(t, m, projectNodeID(localMachine, "r1"))
	m.openBroadcast()
	d := m.overlay.(*broadcastDialog)
	if len(d.targets) != 16 {
		t.Fatalf("targets %d", len(d.targets))
	}
	b := d.render(*m)
	if !strings.Contains(ansi.Strip(strings.Join(b.lines, "\n")), "… 6 more") {
		t.Fatal("no more marker")
	}
	d.list = true
	for i := 0; i < 15; i++ {
		d.update(m, a2Key("down"))
	}
	if d.scroll != 6 || d.sel != 15 {
		t.Fatalf("scroll %d sel %d", d.scroll, d.sel)
	}
	if !strings.Contains(ansi.Strip(strings.Join(d.render(*m).lines, "\n")), "… 6 above") {
		t.Fatal("no above marker")
	}

	d.scroll, d.sel = 0, 0
	b = d.render(*m)
	press := func(x, y int) tea.Cmd {
		return d.mouse(m, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, b)
	}
	before := d.targets[1].on
	press(b.x+3, b.y+6) // the second recipient
	if d.sel != 1 || d.targets[1].on == before || !d.list {
		t.Fatalf("click row: sel %d on %v", d.sel, d.targets[1].on)
	}
	press(b.x+3, b.y+2) // the message
	if d.list {
		t.Fatal("click message")
	}
	press(0, 0) // outside: nothing lost
	if m.overlay != d {
		t.Fatal("outside click closed the dialog")
	}
	d.mouse(m, tea.MouseMsg{X: b.x + 3, Y: b.y + 6, Button: tea.MouseButtonWheelDown}, b) // ignored

	// Outside the confirmation: back to the dialog.
	d.in.SetValue("hi")
	d.review(m)
	c := m.overlay.(*broadcastConfirm)
	cb := c.render(*m)
	c.mouse(m, tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, cb)
	if m.overlay != d {
		t.Fatalf("outside confirm: %T", m.overlay)
	}
}

func TestSendBroadcast(t *testing.T) {
	m, _ := broadcastFixture(t)
	local := m.machines[0]
	old := newMachine("old", "oldbox", "x")
	old.c = a2Client("pane.v1")
	gone := newMachine("gone", "gonebox", "y")
	m.machines = append(m.machines, old, gone)
	targets := []broadcastTarget{
		{machine: "old", pane: proto.PaneInfo{ID: "p1", Name: "claude"}},
		{machine: "gone", pane: proto.PaneInfo{ID: "p2", Name: "codex"}},
	}

	// Nothing reachable: one message naming why.
	msgs := a2Run(m.sendBroadcast(targets, "hi"))
	if len(msgs) != 1 {
		t.Fatalf("msgs %v", msgs)
	}
	done := msgs[0].(broadcastDoneMsg)
	if done.sent != 0 || len(done.skipped) != 2 || !strings.Contains(done.skipped[0], "oldbox's server predates broadcasts") ||
		!strings.Contains(done.skipped[1], "machine offline") {
		t.Fatalf("skipped: %+v", done)
	}

	// A server error names every pane on that machine.
	c, peer := a1FakeClient(t, "agent.broadcast.v1")
	local.c = c
	peer.setError(proto.MethodAgentBroadcast, "boom")
	msgs = a2Run(m.sendBroadcast([]broadcastTarget{{machine: localMachine, pane: proto.PaneInfo{ID: "p1", Name: "claude"}}}, "hi"))
	if d := msgs[0].(broadcastDoneMsg); len(d.skipped) != 1 || !strings.Contains(d.skipped[0], "claude: a1: boom") {
		t.Fatalf("error: %+v", d)
	}
}

func TestReceiveBroadcast(t *testing.T) {
	m, _ := broadcastFixture(t)
	next, _ := m.update(broadcastDoneMsg{sent: 1})
	*m = next.(Model)
	if m.flash != "broadcast sent to 1 agent" || m.flashIsErr {
		t.Fatalf("one: %q", m.flash)
	}
	m.receiveBroadcast(broadcastDoneMsg{sent: 3, skipped: []string{"zsh: not running an agent"}})
	if m.flash != "broadcast sent to 3 agents · not sent: zsh: not running an agent" || !m.flashIsErr {
		t.Fatalf("partial: %q", m.flash)
	}
	m.receiveBroadcast(broadcastDoneMsg{skipped: []string{"codex: exited", "claude: exited"}})
	if m.flash != "broadcast not sent · codex: exited · claude: exited" {
		t.Fatalf("none: %q", m.flash)
	}
}
