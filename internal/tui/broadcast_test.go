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

// broadcastFixture: api has p1 claude (feat), p5 codex (main, waiting for
// an answer), p2 zsh and p6 an exited agent; CLI has p4 codex and p3 bash.
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
		// Groups list agents (ticked unless waiting) and terminals (unticked).
		{machineID(localMachine), "local", "p1,p5(off),p2(off),p4,p3(off)"},
		{projectNodeID(localMachine, "r1"), "api", "p1,p5(off),p2(off)"},
		{sectionID(localMachine, "r1", "branches"), "api", "p1,p5(off),p2(off)"},
		{cliID(localMachine), "CLI", "p4,p3(off)"},
		{branchNodeID(localMachine, "r1", "feat"), "api · feat", "p1"},
		{branchNodeID(localMachine, "r1", "main"), "api · main", "p5(off)"},
		// A section lists only its kind; terminals chosen on purpose start ticked.
		{sectionID(localMachine, "r1", "agents"), "api · Agents", "p1,p5(off)"},
		{sectionID(localMachine, "r1", "terminals"), "api · Terminals", "p2"},
		{machineID(localMachine) + "/agents", "CLI · Agents", "p4"},
		{looseTerminalsID(localMachine), "CLI · Terminals", "p3"},
		// A pane selects its section.
		{paneNodeID(localMachine, "p1"), "api · Agents", "p1,p5(off)"},
		{paneNodeID(localMachine, "p2"), "api · Terminals", "p2"},
		{paneNodeID(localMachine, "p3"), "CLI · Terminals", "p3"},
		{paneNodeID(localMachine, "p4"), "CLI · Agents", "p4"},
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
	m.cursor = paneNodeID(localMachine, "p2")
	if label, targets := m.broadcastScope(true); label != "every machine" || targetIDs(targets) != "p1,p5(off),p2(off),p4,p3(off)" {
		t.Errorf("every: %q %s", label, targetIDs(targets))
	}
	m.cursor = ""
	if label, _ := m.broadcastScope(false); label != "every machine" {
		t.Errorf("no selection: %q", label)
	}

	// Several machines: groups and labels name them; an offline one adds nothing.
	box := newMachine("box", "devbox", "dev@box")
	box.c = m.machines[0].c
	box.panes = []proto.PaneInfo{{ID: "p1", Name: "gemini", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "gemini"}}}
	off := newMachine("off", "offline", "x")
	off.panes = []proto.PaneInfo{{ID: "p9", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "claude"}}}
	m.machines = append(m.machines, box, off)
	m.cursor = sectionID(localMachine, "r1", "terminals")
	if label, targets := m.broadcastScope(false); label != "local › api · Terminals" || targets[0].group != "local › api" {
		t.Errorf("multi-machine: %q %+v", label, targets)
	}
	_, targets := m.broadcastScope(true)
	if got := targetIDs(targets); got != "p1,p5(off),p2(off),p4,p3(off),p1" || targets[5].machine != "box" || targets[5].group != "devbox › CLI" {
		t.Errorf("every machine: %s %+v", got, targets[5])
	}
}

func TestOpenBroadcast(t *testing.T) {
	m, _ := broadcastFixture(t)
	m.machines[0].panes = []proto.PaneInfo{{ID: "p3", Name: "bash", State: proto.PaneExited}}
	m.rebuild()
	a1At(t, m, machineID(localMachine))
	if m.openBroadcast(); m.overlay != nil || m.flash != "nothing running in local to broadcast to" {
		t.Fatalf("empty: %q", m.flash)
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
	m, _ := broadcastFixture(t)
	c, peer := a1FakeClient(t, "agent.broadcast.v1", "agent.broadcast.shells.v1")
	m.machines[0].c = c
	a1At(t, m, machineID(localMachine))
	m.openBroadcast()
	d := m.overlay.(*broadcastDialog)
	key := func(k tea.KeyMsg) tea.Cmd {
		t.Helper()
		_, cmd := m.overlay.update(m, k)
		return cmd
	}
	typeText := func(s string) {
		for _, r := range s {
			if r == ' ' {
				key(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
			} else {
				key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
		}
	}

	// The list is nested like the tree.
	var rows []string
	for _, r := range d.rows() {
		if r.target < 0 {
			rows = append(rows, r.heading)
		} else {
			rows = append(rows, d.targets[r.target].pane.ID)
		}
	}
	if got := strings.Join(rows, " "); got != "api Agents p1 p5 Terminals p2 CLI Agents p4 Terminals p3" {
		t.Fatalf("rows: %s", got)
	}

	key(a2Key("enter"))
	if d.err != "type the message first" || m.overlay != d {
		t.Fatalf("empty message: %q", d.err)
	}
	typeText("git pull")
	key(runes("t")) // list keys are text while typing
	key(tea.KeyMsg{Type: tea.KeyBackspace})
	if d.in.Value() != "git pull" || d.err != "" {
		t.Fatalf("typed %q (err %q)", d.in.Value(), d.err)
	}

	key(a2Key("tab"))
	key(runes("t")) // tick every terminal
	if targetIDs(d.targets) != "p1,p5(off),p2,p4,p3" {
		t.Fatalf("t: %s", targetIDs(d.targets))
	}
	key(runes("t")) // and untick them
	if targetIDs(d.targets) != "p1,p5(off),p2(off),p4,p3(off)" {
		t.Fatalf("t again: %s", targetIDs(d.targets))
	}
	key(runes("a"))
	key(runes("a"))
	if targetIDs(d.targets) != "p1(off),p5(off),p2(off),p4(off),p3(off)" {
		t.Fatalf("a twice: %s", targetIDs(d.targets))
	}
	key(a2Key("enter"))
	if d.err != "tick at least one (space)" {
		t.Fatalf("none ticked: %q", d.err)
	}
	// Tick p2 and p3 (the terminals) with space and x.
	key(a2Key("down"))
	key(a2Key("down"))
	key(runes(" "))
	key(a2Key("down"))
	key(a2Key("down"))
	key(runes("x"))
	key(a2Key("down")) // stays on the last
	if d.sel != 4 || targetIDs(d.targets) != "p1(off),p5(off),p2,p4(off),p3" {
		t.Fatalf("ticks: sel %d %s", d.sel, targetIDs(d.targets))
	}
	for i := 0; i < 5; i++ {
		key(a2Key("up"))
	}
	if d.list {
		t.Fatal("up past the first pane should return to the message")
	}
	key(a2Key("down"))

	// e widens and narrows, keeping ticks.
	key(runes("e"))
	if !d.every || d.label != "every machine" || targetIDs(d.targets) != "p1(off),p5(off),p2,p4(off),p3" {
		t.Fatalf("widen: %q %s", d.label, targetIDs(d.targets))
	}
	key(runes("e"))
	if d.every || d.label != "local" {
		t.Fatalf("narrow: %q", d.label)
	}

	// Render: headings, notes, width.
	m.width = 160
	out := ansi.Strip(strings.Join(d.render(*m).lines, "\n"))
	for _, want := range []string{"To 2 of 5 in local", " api ", "   Agents", "   Terminals", "[x] zsh", "runs it as a command",
		"[ ] codex  main", "! waiting for an answer", " CLI ", "t terminals"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
	for _, w := range []int{40, 80, 160} {
		m.width = w
		for i, l := range d.render(*m).lines {
			if ansi.StringWidth(l) > max(w, 34) {
				t.Fatalf("width %d: line %d is %d wide", w, i, ansi.StringWidth(l))
			}
		}
	}
	m.width = 160

	// Review: add an agent and the waiting agent; the confirmation says
	// what terminals do and warns about the question.
	d.sel = 0
	key(runes(" "))
	d.sel = 1
	key(runes(" "))
	key(a2Key("enter"))
	cf, ok := m.overlay.(*broadcastConfirm)
	if !ok {
		t.Fatalf("confirm: %T", m.overlay)
	}
	q := strings.Join(cf.text, " ")
	for _, want := range []string{"Send “git pull” to 2 agents and 2 terminals", "The terminals run it as a shell command.",
		"1 agent is waiting for an answer: the message will answer its question."} {
		if !strings.Contains(q, want) {
			t.Fatalf("question lacks %q: %q", want, q)
		}
	}
	key(runes("n"))
	if m.overlay != d || d.in.Value() != "git pull" {
		t.Fatal("n should return to the dialog")
	}
	key(a2Key("enter"))
	cmd := key(runes("y"))
	if m.overlay != nil || cmd == nil {
		t.Fatal("yes should send")
	}
	a2Run(cmd)
	msg := peer.waitMethod(t, proto.MethodAgentBroadcast, `"shells":true`)
	for _, id := range []string{"p1", "p5", "p2", "p3"} {
		if !strings.Contains(string(msg.Params), `"`+id+`"`) {
			t.Fatalf("params %s lack %s", msg.Params, id)
		}
	}
	if strings.Contains(string(msg.Params), `"p4"`) {
		t.Fatalf("unticked p4 sent: %s", msg.Params)
	}

	// Agents only: shells stays off the wire.
	a1At(t, m, sectionID(localMachine, "r1", "agents"))
	m.openBroadcast()
	typeText("hi")
	key(a2Key("enter"))
	a2Run(key(runes("y")))
	msg = peer.waitMethod(t, proto.MethodAgentBroadcast, `"text":"hi"`)
	if strings.Contains(string(msg.Params), "shells") {
		t.Fatalf("agents-only broadcast allowed shells: %s", msg.Params)
	}

	// esc cancels.
	m.openBroadcast()
	key(a2Key("esc"))
	if m.overlay != nil {
		t.Fatal("esc")
	}
}

func TestBroadcastConfirmWording(t *testing.T) {
	m, _ := broadcastFixture(t)
	a1At(t, m, sectionID(localMachine, "r1", "terminals"))
	m.openBroadcast()
	d := m.overlay.(*broadcastDialog)
	d.in.SetValue("make test")
	d.review(m)
	q := strings.Join(m.overlay.(*broadcastConfirm).text, " ")
	if !strings.Contains(q, "to 1 terminal: zsh (api)?") || !strings.Contains(q, "The terminal runs it as a shell command.") || strings.Contains(q, "waiting") {
		t.Fatalf("one terminal: %q", q)
	}
	a1At(t, m, projectNodeID(localMachine, "r1"))
	m.openBroadcast()
	d = m.overlay.(*broadcastDialog)
	d.in.SetValue("go")
	for i := range d.targets {
		d.targets[i].on = !d.targets[i].shell
	}
	d.review(m)
	q = strings.Join(m.overlay.(*broadcastConfirm).text, " ")
	if !strings.Contains(q, "to 2 agents:") || strings.Contains(q, "terminal") {
		t.Fatalf("agents only: %q", q)
	}
	if countText(0, 0) != "nothing" || countText(1, 0) != "1 agent" || countText(3, 1) != "3 agents and 1 terminal" {
		t.Fatal("countText")
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
	if len(d.targets) != 17 || len(d.rows()) != 20 {
		t.Fatalf("targets %d rows %d", len(d.targets), len(d.rows()))
	}
	if !strings.Contains(ansi.Strip(strings.Join(d.render(*m).lines, "\n")), "… 8 more") {
		t.Fatal("no more marker")
	}
	d.list = true
	for i := 0; i < 20; i++ {
		d.update(m, a2Key("down"))
	}
	if d.sel != 16 || d.scroll != 8 {
		t.Fatalf("scroll %d sel %d", d.scroll, d.sel)
	}
	if !strings.Contains(ansi.Strip(strings.Join(d.render(*m).lines, "\n")), "… 8 above") {
		t.Fatal("no above marker")
	}
	// Back at the first pane its headings are in view again.
	d.sel = 0
	d.keepVisible()
	if d.scroll != 0 {
		t.Fatalf("headings hidden: scroll %d", d.scroll)
	}

	b := d.render(*m)
	press := func(x, y int) {
		d.mouse(m, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, b)
	}
	// Lines 5 and 6 are the "api" and "Agents" headings; 7 is the first pane.
	before := d.targets[0].on
	press(b.x+3, b.y+5)
	press(b.x+3, b.y+6)
	if d.targets[0].on != before {
		t.Fatal("a heading click toggled a pane")
	}
	d.list = false
	press(b.x+3, b.y+7)
	if d.sel != 0 || d.targets[0].on == before || !d.list {
		t.Fatalf("click pane: sel %d on %v", d.sel, d.targets[0].on)
	}
	press(b.x+3, b.y+2)
	if d.list {
		t.Fatal("click message")
	}
	press(0, 0)
	if m.overlay != d {
		t.Fatal("outside click closed the dialog")
	}
	d.mouse(m, tea.MouseMsg{X: b.x + 3, Y: b.y + 7, Button: tea.MouseButtonWheelDown}, b)

	d.in.SetValue("hi")
	d.review(m)
	c := m.overlay.(*broadcastConfirm)
	c.mouse(m, tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, c.render(*m))
	if m.overlay != d {
		t.Fatalf("outside confirm: %T", m.overlay)
	}

	// Its buttons: No goes back to the message, Yes sends.
	click := func(label string) {
		t.Helper()
		m.overlay = c
		b := c.render(*m)
		for i, l := range b.lines {
			if col := strings.Index(ansi.Strip(l), label); col >= 0 && strings.Contains(ansi.Strip(l), "Yes") {
				c.mouse(m, tea.MouseMsg{X: b.x + ansi.StringWidth(ansi.Strip(l)[:col]), Y: b.y + i, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, b)
				return
			}
		}
		t.Fatalf("no %q button", label)
	}
	click("No")
	if m.overlay != d {
		t.Fatalf("No: %T", m.overlay)
	}
	click("Yes")
	if m.overlay != nil {
		t.Fatalf("Yes should send and close: %T", m.overlay)
	}
}

func TestSendBroadcast(t *testing.T) {
	m, _ := broadcastFixture(t)
	old := newMachine("old", "oldbox", "x")
	old.c = a2Client("pane.v1")
	agentsOnly := newMachine("mid", "midbox", "z")
	agentsOnly.c = a2Client("agent.broadcast.v1")
	gone := newMachine("gone", "gonebox", "y")
	m.machines = append(m.machines, old, agentsOnly, gone)
	msgs := a2Run(m.sendBroadcast([]broadcastTarget{
		{machine: "old", pane: proto.PaneInfo{ID: "p1", Name: "claude"}},
		{machine: "mid", pane: proto.PaneInfo{ID: "p2", Name: "zsh"}, shell: true},
		{machine: "gone", pane: proto.PaneInfo{ID: "p3", Name: "codex"}},
	}, "hi"))
	if len(msgs) != 1 {
		t.Fatalf("msgs %v", msgs)
	}
	done := msgs[0].(broadcastDoneMsg)
	want := []string{"claude: oldbox's server predates broadcasts", "zsh: midbox's server predates broadcasts to terminals", "codex: machine offline"}
	if done.agents+done.shells != 0 || strings.Join(done.skipped, "|") != strings.Join(want, "|") {
		t.Fatalf("skipped: %+v", done)
	}

	// A server error names every pane sent to it.
	c, peer := a1FakeClient(t, "agent.broadcast.v1", "agent.broadcast.shells.v1")
	m.machines[0].c = c
	peer.setError(proto.MethodAgentBroadcast, "boom")
	msgs = a2Run(m.sendBroadcast([]broadcastTarget{{machine: localMachine, pane: proto.PaneInfo{ID: "p1", Name: "claude"}}}, "hi"))
	if d := msgs[0].(broadcastDoneMsg); len(d.skipped) != 1 || d.skipped[0] != "claude: a1: boom" {
		t.Fatalf("error: %+v", d)
	}
}

// Found end to end: with terminals on two machines the status said "sent to
// 1 terminal", each machine's answer replacing the other's.
func TestSendBroadcastAddsUpMachines(t *testing.T) {
	m, _ := broadcastFixture(t)
	caps := []string{"agent.broadcast.v1", "agent.broadcast.shells.v1"}
	lc, lpeer := a1FakeClient(t, caps...)
	lpeer.setResult(proto.MethodAgentBroadcast, proto.AgentBroadcastResult{Results: []proto.BroadcastOutcome{{ID: "p1", Sent: true}}})
	m.machines[0].c = lc
	box := newMachine("box", "devbox", "x")
	bc, bpeer := a1FakeClient(t, caps...)
	bpeer.setResult(proto.MethodAgentBroadcast, proto.AgentBroadcastResult{Results: []proto.BroadcastOutcome{
		{ID: "p3", Sent: true}, {ID: "p4", Sent: true}, {ID: "p5", Error: "exited"}}})
	box.c = bc
	gone := newMachine("gone", "gonebox", "y")
	m.machines = append(m.machines, box, gone)

	msgs := a2Run(m.sendBroadcast([]broadcastTarget{
		{machine: localMachine, pane: proto.PaneInfo{ID: "p1", Name: "e2e-local"}, shell: true},
		{machine: "box", pane: proto.PaneInfo{ID: "p3", Name: "e2e-remote"}, shell: true},
		{machine: "box", pane: proto.PaneInfo{ID: "p4", Name: "claude"}},
		{machine: "box", pane: proto.PaneInfo{ID: "p5", Name: "codex"}},
		{machine: "gone", pane: proto.PaneInfo{ID: "p9", Name: "zsh"}, shell: true},
	}, "echo hi"))
	if len(msgs) != 1 {
		t.Fatalf("one message for the broadcast, got %v", msgs)
	}
	done := msgs[0].(broadcastDoneMsg)
	if done.shells != 2 || done.agents != 1 || strings.Join(done.skipped, "|") != "zsh: machine offline|codex on devbox: exited" {
		t.Fatalf("merged: %+v", done)
	}
	m.receiveBroadcast(done)
	if m.flash != "broadcast sent to 1 agent and 2 terminals · not sent: zsh: machine offline · codex on devbox: exited" {
		t.Fatalf("flash: %q", m.flash)
	}

	// Nothing to call at all still answers.
	if msgs := a2Run(m.sendBroadcast(nil, "hi")); len(msgs) != 1 || msgs[0].(broadcastDoneMsg).agents != 0 {
		t.Fatalf("empty: %v", msgs)
	}
}

func TestReceiveBroadcast(t *testing.T) {
	m, _ := broadcastFixture(t)
	next, _ := m.update(broadcastDoneMsg{agents: 1})
	*m = next.(Model)
	if m.flash != "broadcast sent to 1 agent" || m.flashIsErr {
		t.Fatalf("one: %q", m.flash)
	}
	m.receiveBroadcast(broadcastDoneMsg{agents: 2, shells: 3})
	if m.flash != "broadcast sent to 2 agents and 3 terminals" {
		t.Fatalf("mixed: %q", m.flash)
	}
	m.receiveBroadcast(broadcastDoneMsg{shells: 1, skipped: []string{"zsh: exited"}})
	if m.flash != "broadcast sent to 1 terminal · not sent: zsh: exited" || !m.flashIsErr {
		t.Fatalf("partial: %q", m.flash)
	}
	m.receiveBroadcast(broadcastDoneMsg{skipped: []string{"codex: exited", "claude: exited"}})
	if m.flash != "broadcast not sent · codex: exited · claude: exited" {
		t.Fatalf("none: %q", m.flash)
	}
}
