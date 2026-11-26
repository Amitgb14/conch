package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// switchFixture is a1Fixture with a server that can switch agents (or,
// old, one that cannot) and an agent list saying how each is reached.
func switchFixture(t *testing.T, canSwitch bool) (*Model, *a1Peer) {
	t.Helper()
	a2Isolate(t)
	m, _ := a1Fixture(t, false)
	caps := []string{"agent.install.v1"}
	if canSwitch {
		caps = append(caps, proto.CapAgentSwitch)
	}
	var c *client.Client
	c, peer := a1FakeClient(t, caps...)
	mach := m.machines[0]
	mach.c, mach.server = c, c.Server
	mach.agentList = []proto.AgentAvailability{
		{Name: "claude", Label: "Claude Code", Installed: true, SwitchTo: proto.SwitchNative},
		{Name: "codex", Label: "Codex", Installed: true, SwitchTo: proto.SwitchNative},
		{Name: "gemini", Label: "Gemini CLI", Installed: true, SwitchTo: proto.SwitchHandoff},
		{Name: "aider", Label: "Aider", Installed: true, SwitchTo: proto.SwitchNever},
		{Name: "kilo", Label: "Kilo Code", Installed: false, SwitchTo: proto.SwitchHandoff},
	}
	return m, peer
}

// menuLabels is a menu's items as they read.
func switchLabels(mu *menu) []string {
	var out []string
	for _, it := range mu.items {
		out = append(out, ansi.Strip(it.label))
	}
	return out
}

// clickItem clicks a menu's item whose label holds want, through the
// model's own mouse path.
func clickItem(t *testing.T, m *Model, want string) tea.Cmd {
	t.Helper()
	mu, ok := m.overlay.(*menu)
	if !ok {
		t.Fatalf("no menu up: %T", m.overlay)
	}
	b := mu.render(*m)
	for i, it := range mu.items {
		if strings.Contains(ansi.Strip(it.label), want) {
			return a1Mouse(t, m, b.x+3, b.y+1+i, tea.MouseButtonLeft, tea.MouseActionPress)
		}
	}
	t.Fatalf("no %q in %q", want, switchLabels(mu))
	return nil
}

// openPaneMenu opens p1's row menu as m does.
func openPaneMenu(t *testing.T, m *Model) *menu {
	t.Helper()
	a1At(t, m, paneNodeID(localMachine, "p1"))
	a1Key(t, m, a2Key("m"))
	mu, ok := m.overlay.(*menu)
	if !ok {
		t.Fatalf("m opened %T", m.overlay)
	}
	return mu
}

// Switch Agent ▸ on an agent's menu lists every installed agent but its
// own, saying how each would get the conversation, and greys out one
// that cannot be given a first message. Clicked, as Amit does.
func TestSwitchAgentMenu(t *testing.T) {
	m, peer := switchFixture(t, true)
	mu := openPaneMenu(t, m)
	if !strings.Contains(strings.Join(switchLabels(mu), "\n"), "Switch Agent ▸") {
		t.Fatalf("no Switch Agent on %q", switchLabels(mu))
	}
	clickItem(t, m, "Switch Agent")
	sm, ok := m.overlay.(*menu)
	if !ok || sm.switchOf != paneKey(localMachine, "p1") || sm.back != mu {
		t.Fatalf("the switch menu: %T %+v", m.overlay, sm)
	}
	got := strings.Join(switchLabels(sm), "\n")
	for _, want := range []string{"Codex  full history", "Gemini CLI  handoff document", "Aider  cannot take a first message",
		"Switches when this turn ends.", "Claude Code's own session is kept, so you can switch back."} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	for _, not := range []string{"Claude Code  ", "Kilo"} {
		if strings.Contains(got, not) {
			t.Errorf("%q offered in\n%s", not, got)
		}
	}
	// The greyed one only says why.
	clickItem(t, m, "Aider")
	if m.overlay != nil || !strings.Contains(m.flash, "cannot be given a first message") {
		t.Fatalf("aider: overlay %T flash %q", m.overlay, m.flash)
	}
	// esc goes back to the row's menu, one step.
	m.overlay = sm
	a1Key(t, m, a2Key("esc"))
	if m.overlay != mu {
		t.Fatalf("esc went to %T", m.overlay)
	}

	// Codex, by its number: the preview is asked for, changing nothing.
	m.overlay = sm
	peer.setResult(proto.MethodAgentSwitch, proto.AgentSwitchResult{From: "claude", To: "codex", Crossed: proto.SwitchNative, Preview: true,
		Crossing: &proto.SwitchCrossing{Messages: 42, Commands: 18, Notes: 6, Fits: true, Bytes: 48 << 10, Plan: true,
			Left: []string{"Claude Code's thinking and reasoning"}}})
	cmd := a1Key(t, m, a2Key("1"))
	if cmd == nil {
		t.Fatal("1 asked nothing")
	}
	msg := cmd()
	p := peer.waitMethod(t, proto.MethodAgentSwitch, `"preview":true`)
	if !strings.Contains(string(p.Params), `"to":"codex"`) || !strings.Contains(string(p.Params), `"id":"p1"`) {
		t.Fatalf("asked %s", p.Params)
	}
	next, _ := m.Update(msg)
	*m = next.(Model)
	if c, ok := m.overlay.(*switchCard); !ok || c.to != "codex" || c.left != switchCountdown {
		t.Fatalf("no card: %T", m.overlay)
	}
}

// A server that cannot switch keeps the item, saying why, so it is
// found where it will be once the server is reloaded.
func TestSwitchAgentOnAnOlderServer(t *testing.T) {
	m, peer := switchFixture(t, false)
	mu := openPaneMenu(t, m)
	got := strings.Join(switchLabels(mu), "\n")
	if !strings.Contains(got, "Switch Agent ▸  its server is too old") {
		t.Fatalf("no disabled item in\n%s", got)
	}
	clickItem(t, m, "Switch Agent")
	if m.overlay != nil || !strings.Contains(m.flash, "conch server reload") {
		t.Fatalf("overlay %T flash %q", m.overlay, m.flash)
	}
	for _, meth := range peer.methods() {
		if meth == proto.MethodAgentSwitch {
			t.Fatal("asked an old server to switch")
		}
	}
	// A shell has none at all.
	a1At(t, m, paneNodeID(localMachine, "p2"))
	a1Key(t, m, a2Key("m"))
	if sm, ok := m.overlay.(*menu); ok && strings.Contains(strings.Join(switchLabels(sm), "\n"), "Switch Agent") {
		t.Fatal("a shell offers Switch Agent")
	}
}

func switchCardOn(t *testing.T, m *Model, preview proto.AgentSwitchResult) *switchCard {
	t.Helper()
	m.Update(nil)
	cmd := m.openSwitchCard(switchPreviewMsg{machine: localMachine, pane: "p1", to: preview.To, res: preview})
	if cmd == nil {
		t.Fatal("no countdown")
	}
	c, ok := m.overlay.(*switchCard)
	if !ok {
		t.Fatalf("no card: %T", m.overlay)
	}
	return c
}

func cardText(m Model) string {
	b := m.overlay.render(m)
	return ansi.Strip(strings.Join(b.lines, "\n"))
}

// The card says what crosses, counts down from five and then asks; the
// server waits for the turn, which the card says until it is made.
func TestSwitchCardCountsDownAndWaits(t *testing.T) {
	m, peer := switchFixture(t, true)
	pre := proto.AgentSwitchResult{From: "claude", To: "codex", Crossed: proto.SwitchNative, Preview: true,
		Crossing: &proto.SwitchCrossing{Messages: 42, Commands: 18, Notes: 6, Fits: true, Bytes: 48 << 10, Plan: true,
			Left: []string{"Claude Code's thinking and reasoning"}}}
	c := switchCardOn(t, m, pre)
	text := cardText(*m)
	for _, want := range []string{"Switching to", "Codex in 5s", "42 messages · 18 commands with their output · 6 notes for reads and edits · the plan",
		"all 48 KB, nothing shortened", "Claude Code's thinking and reasoning", "as Codex's own session",
		"Claude Code's session as it is, so Switch back works", "Switch Now", "Choose…", "Cancel"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q on the card:\n%s", want, text)
		}
	}
	// The status bar says the card's keys.
	if bar := ansi.Strip(m.statusBar()); !strings.Contains(bar, "SWITCH") || !strings.Contains(bar, "switch now") || !strings.Contains(bar, "cancel") {
		t.Fatalf("status bar: %q", bar)
	}
	// A tick from an earlier card does nothing.
	if cmd := c.tick(m, c.gen-1); cmd != nil || c.left != switchCountdown {
		t.Fatal("an old card's tick counted")
	}
	for i := switchCountdown - 1; i > 0; i-- {
		next, cmd := m.Update(switchTickMsg{gen: c.gen})
		*m = next.(Model)
		if c.left != i || cmd == nil {
			t.Fatalf("after a tick: %d left", c.left)
		}
	}
	peer.setResult(proto.MethodAgentSwitch, proto.AgentSwitchResult{From: "claude", To: "codex", Pending: true, Waits: []string{"the turn to end"}})
	next, cmd := m.Update(switchTickMsg{gen: c.gen})
	*m = next.(Model)
	if cmd == nil || c.state != switchAsking {
		t.Fatalf("at nought it did not ask: %v", c.state)
	}
	next, _ = m.Update(cmd())
	*m = next.(Model)
	p := peer.waitFor(t, "the switch", func(msg proto.Message) bool {
		return msg.Method == proto.MethodAgentSwitch && !strings.Contains(string(msg.Params), "preview")
	})
	if strings.Contains(string(p.Params), `"now"`) {
		t.Fatalf("the card asked to switch mid-turn: %s", p.Params)
	}
	if c.state != switchWaiting || !strings.Contains(cardText(*m), "the turn to end") {
		t.Fatalf("waiting: %v\n%s", c.state, cardText(*m))
	}
	if _, ok := m.machines[0].switching["p1"]; !ok {
		t.Fatal("the waiting switch is not remembered for the menu")
	}
	// Choose… is not offered while the server holds one.
	if c.choose(m); m.overlay != c {
		t.Fatal("choose while waiting")
	}

	// The turn ends and the server switches: the card goes, saying so.
	ev := eventMsg(t, proto.EventAgentSwitched, proto.AgentSwitchResult{Pane: proto.PaneInfo{ID: "p1", Name: "claude"}, From: "claude", To: "codex",
		Crossed: proto.SwitchNative})
	m.handleEvent(m.machines[0], ev)
	if m.overlay != nil || !strings.Contains(m.flash, "switched to Codex") {
		t.Fatalf("after the switch: %T %q", m.overlay, m.flash)
	}
	if _, ok := m.machines[0].switching["p1"]; ok {
		t.Fatal("still waiting after the switch")
	}
}

// Cancel calls off what the server holds; esc on a card still counting
// asks nothing at all.
func TestSwitchCardCancel(t *testing.T) {
	m, peer := switchFixture(t, true)
	pre := proto.AgentSwitchResult{From: "claude", To: "codex", Crossed: proto.SwitchNative, Preview: true, Crossing: &proto.SwitchCrossing{Messages: 2, Fits: true}}
	switchCardOn(t, m, pre)
	if cmd := a1Key(t, m, a2Key("esc")); cmd != nil || m.overlay != nil {
		t.Fatal("esc on a counting card")
	}

	c := switchCardOn(t, m, pre)
	peer.setResult(proto.MethodAgentSwitch, proto.AgentSwitchResult{From: "claude", To: "codex", Pending: true, Waits: []string{"the turn to end"}})
	// Switch Now, clicked.
	b := c.render(*m)
	bt := c.buttons[0]
	cmd := a1Mouse(t, m, b.x+1+bt.x0, b.y+1+bt.line, tea.MouseButtonLeft, tea.MouseActionPress)
	if cmd == nil {
		t.Fatal("a click on Switch Now asked nothing")
	}
	next, _ := m.Update(cmd())
	*m = next.(Model)
	if c.state != switchWaiting {
		t.Fatalf("state %v", c.state)
	}
	b = c.render(*m)
	if len(c.buttons) != 1 {
		t.Fatalf("buttons while waiting: %+v", c.buttons)
	}
	bt = c.buttons[0]
	cmd = a1Mouse(t, m, b.x+1+bt.x0, b.y+1+bt.line, tea.MouseButtonLeft, tea.MouseActionPress)
	if m.overlay != nil || cmd == nil {
		t.Fatal("Cancel left the card up")
	}
	cmd()
	peer.waitMethod(t, proto.MethodAgentSwitchCancel, `"id":"p1"`)
	// A click beside the card leaves it be.
	c = switchCardOn(t, m, pre)
	a1Mouse(t, m, 0, 0, tea.MouseButtonLeft, tea.MouseActionPress)
	if m.overlay != c {
		t.Fatal("a stray click put the card away")
	}
	// Choose… goes back to the list.
	a1Key(t, m, a2Key("c"))
	if sm, ok := m.overlay.(*menu); !ok || sm.switchOf == "" {
		t.Fatalf("c opened %T", m.overlay)
	}
}

// A switch that fails says why on the card — answered at once, or later
// when a waiting one's moment came — or in the flash with no card up.
func TestSwitchThatFails(t *testing.T) {
	m, peer := switchFixture(t, true)
	pre := proto.AgentSwitchResult{From: "claude", To: "gemini", Crossed: proto.SwitchHandoff, Preview: true,
		Why: "conch does not write Gemini CLI's sessions", Crossing: &proto.SwitchCrossing{Messages: 3, Fits: true}}
	c := switchCardOn(t, m, pre)
	if text := cardText(*m); !strings.Contains(text, "the handoff document, as Gemini CLI's first message: conch does not write") ||
		!strings.Contains(text, "sessions") || !strings.Contains(text, "all of it, nothing shortened") {
		t.Fatalf("the handoff's why:\n%s", text)
	}
	peer.setError(proto.MethodAgentSwitch, "conch has not seen Claude Code's conversation in p1 yet")
	cmd := c.switchNow(m)
	next, _ := m.Update(cmd())
	*m = next.(Model)
	if c.state != switchFailed || !strings.Contains(cardText(*m), "conch has not seen") || !strings.Contains(cardText(*m), "Could not switch") {
		t.Fatalf("failed:\n%s", cardText(*m))
	}
	if bar := ansi.Strip(m.statusBar()); !strings.Contains(bar, "close") {
		t.Fatalf("status bar: %q", bar)
	}
	a1Key(t, m, a2Key("enter"))
	if m.overlay != nil {
		t.Fatal("enter left a failed card up")
	}

	// A waiting one fails when its moment comes: on the card…
	c = switchCardOn(t, m, pre)
	c.state = switchWaiting
	fail := eventMsg(t, proto.EventAgentSwitched, proto.AgentSwitchResult{Pane: proto.PaneInfo{ID: "p1", Name: "claude"}, To: "gemini",
		Error: "the agent is no longer running there"})
	m.handleEvent(m.machines[0], fail)
	if c.state != switchFailed || !strings.Contains(cardText(*m), "no longer running") {
		t.Fatalf("on the card:\n%s", cardText(*m))
	}
	// …or with none, in the flash.
	m.overlay, m.flash = nil, ""
	m.handleEvent(m.machines[0], fail)
	if !strings.Contains(m.flash, "failed: the agent is no longer running there") {
		t.Fatalf("flash %q", m.flash)
	}
	// And a preview refused never opens a card.
	m.openSwitchCard(switchPreviewMsg{machine: localMachine, pane: "p1", to: "codex", err: errors.New("nothing has been said in p1 yet")})
	if m.overlay != nil || !strings.Contains(m.flash, "nothing has been said") {
		t.Fatalf("refused preview: %T %q", m.overlay, m.flash)
	}
}

// agent.switching from another client is remembered, so the menu offers
// to call it off.
func TestSwitchWaitingElsewhereIsOffered(t *testing.T) {
	m, peer := switchFixture(t, true)
	m.handleEvent(m.machines[0], eventMsg(t, proto.EventAgentSwitching, proto.AgentSwitching{Pane: "p1", From: "claude", To: "codex", Waits: []string{"the turn to end"}}))
	sm := newSwitchMenu(*m, localMachine, "p1", 10, 5, nil)
	if sm == nil || !strings.HasPrefix(switchLabels(sm)[0], "Call off the switch to Codex") {
		t.Fatalf("menu: %q", switchLabels(sm))
	}
	m.overlay = sm
	cmd := a1Key(t, m, a2Key("x"))
	if cmd == nil {
		t.Fatal("x asked nothing")
	}
	cmd()
	peer.waitMethod(t, proto.MethodAgentSwitchCancel, `"id":"p1"`)
	if _, ok := m.machines[0].switching["p1"]; ok {
		t.Fatal("still remembered as waiting")
	}
}

// switchedPane makes p1 a Codex that was switched from Claude Code.
func switchedPane(m *Model, back bool) {
	p := &m.machines[0].panes[0]
	p.Agent = &proto.AgentStatus{Name: "codex", State: proto.AgentIdle}
	p.SwitchedFrom = &proto.SwitchOrigin{Agent: "claude", Session: "s1", At: time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC), Back: back,
		Crossing: &proto.SwitchCrossing{Messages: 42, Commands: 18, Notes: 6}}
}

// After a switch the pane says where its conversation came from, with
// Switch back and ×, on a row of its own: the pane is a row shorter so
// nothing of it is hidden, and clicks below land where they did.
func TestSwitchBanner(t *testing.T) {
	m, peer := switchFixture(t, true)
	switchedPane(m, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.frames[paneKey(localMachine, "p1")] = &proto.Frame{ID: "p1", Lines: []string{"› continue"}}
	rects, _ := m.leafRects()
	l := m.tab().focused()
	in := m.inner(rects[l.id])
	if body := m.leafInner(l, rects[l.id]); body.y != in.y+1 || body.h != in.h-1 {
		t.Fatalf("the pane is not a row down: %+v in %+v", body, in)
	}
	if _, rows := m.paneArea(); rows != in.h-1 {
		t.Fatalf("the pane is sized to %d rows in %d", rows, in.h)
	}
	screen := strings.Split(ansi.Strip(m.View()), "\n")
	line := screen[in.y]
	if !strings.Contains(line, "⇄ Continued from") || !strings.Contains(line, "Claude Code · 42 messages · 18 commands · 6 notes") ||
		!strings.Contains(line, "Switch back") {
		t.Fatalf("banner row: %q", line)
	}
	if !strings.Contains(screen[in.y+1], "› continue") {
		t.Fatalf("the pane's first row is not under the banner: %q", screen[in.y+1])
	}
	for _, s := range screen {
		if ansi.StringWidth(s) > m.width {
			t.Fatalf("a line is %d wide in %d: %q", ansi.StringWidth(s), m.width, s)
		}
	}

	// Switch back, clicked: the way there is the same card.
	_, back, gone, _ := m.switchBanner(l, in.w)
	peer.setResult(proto.MethodAgentSwitch, proto.AgentSwitchResult{From: "codex", To: "claude", Preview: true, Crossed: proto.SwitchNative,
		Crossing: &proto.SwitchCrossing{Fits: true}})
	cmd := a1Mouse(t, m, in.x+back[0]+1, in.y, tea.MouseButtonLeft, tea.MouseActionPress)
	if cmd == nil {
		t.Fatal("Switch back did nothing")
	}
	cmd()
	if p := peer.waitMethod(t, proto.MethodAgentSwitch, `"preview":true`); !strings.Contains(string(p.Params), `"to":"claude"`) {
		t.Fatalf("switched back to %s", p.Params)
	}
	// Elsewhere on the banner is not the pane's.
	if cmd := a1Mouse(t, m, in.x+2, in.y, tea.MouseButtonLeft, tea.MouseActionPress); cmd != nil {
		t.Fatal("a click on the banner's words did something")
	}
	// × puts it away, and the pane has its row back.
	a1Mouse(t, m, in.x+gone[0], in.y, tea.MouseButtonLeft, tea.MouseActionPress)
	if _, _, _, ok := m.switchBanner(l, in.w); ok {
		t.Fatal("× left the banner")
	}
	if body := m.leafInner(l, rects[l.id]); body != in {
		t.Fatalf("after ×: %+v in %+v", body, in)
	}
	// A later switch of the same pane has a banner of its own.
	m.machines[0].panes[0].SwitchedFrom.At = m.machines[0].panes[0].SwitchedFrom.At.Add(time.Hour)
	if _, _, _, ok := m.switchBanner(l, in.w); !ok {
		t.Fatal("the next switch's banner was put away too")
	}
}

// No banner once it is back where it started, nor on a pane too small to
// give a row away, nor from an older server's origin with no counts.
func TestSwitchBannerWhenNot(t *testing.T) {
	m, _ := switchFixture(t, true)
	switchedPane(m, true)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	l := m.tab().focused()
	if _, _, _, ok := m.switchBanner(l, 80); ok {
		t.Fatal("a banner after switching back")
	}
	switchedPane(m, false)
	m.machines[0].panes[0].SwitchedFrom.Crossing = nil
	line, _, _, ok := m.switchBanner(l, 80)
	if !ok || strings.Contains(ansi.Strip(line), "messages") || !strings.Contains(ansi.Strip(line), "Continued from") {
		t.Fatalf("an older origin: %q", ansi.Strip(line))
	}
	if _, _, _, ok := m.switchBanner(l, 11); ok {
		t.Fatal("a banner in 11 columns")
	}
	if got := m.leafInner(l, rect{0, 0, 20, 5}); got != m.inner(rect{0, 0, 20, 5}) {
		t.Fatalf("a 3-row pane gave its row away: %+v", got)
	}
	m.machines[0].panes[0].State = proto.PaneExited
	if _, _, _, ok := m.switchBanner(l, 80); ok {
		t.Fatal("a banner on an exited pane")
	}
}

// Every size: the card, the menu and the banner never draw past the edge.
func TestSwitchUIFitsEverySize(t *testing.T) {
	for _, sz := range [][2]int{{1, 1}, {20, 5}, {38, 12}, {60, 20}, {120, 40}, {300, 80}} {
		m, _ := switchFixture(t, true)
		switchedPane(m, false)
		next, _ := m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		*m = next.(Model)
		a1Open(t, m, paneNodeID(localMachine, "p1"))
		m.frames[paneKey(localMachine, "p1")] = &proto.Frame{ID: "p1", Lines: []string{"› continue"}}
		check := func(what string) {
			t.Helper()
			for _, s := range strings.Split(m.View(), "\n") {
				if ansi.StringWidth(s) > sz[0] {
					t.Fatalf("%dx%d %s: a line %d wide: %q", sz[0], sz[1], what, ansi.StringWidth(s), ansi.Strip(s))
				}
			}
		}
		check("banner")
		m.overlay = newSwitchMenu(*m, localMachine, "p1", 3, 3, nil)
		check("menu")
		m.openSwitchCard(switchPreviewMsg{machine: localMachine, pane: "p1", to: "claude", res: proto.AgentSwitchResult{From: "codex", To: "claude",
			Crossed: proto.SwitchHandoff, Why: strings.Repeat("a long reason ", 20),
			Crossing: &proto.SwitchCrossing{Messages: 9999, Commands: 999, Notes: 99, Shortened: 12, Bytes: 9 << 20, Left: []string{strings.Repeat("x", 300)}}}})
		check("card")
		m.overlay.(*switchCard).state = switchFailed
		m.overlay.(*switchCard).err = strings.Repeat("why ", 100)
		check("failed card")
	}
}
