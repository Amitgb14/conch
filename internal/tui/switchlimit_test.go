package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func outOfPlan(auto bool) proto.AgentSwitchOffer {
	o := proto.AgentSwitchOffer{Pane: "p1", From: "claude", To: "codex", Crossed: proto.SwitchNative,
		Accounts: []proto.AccountRank{{Account: "work", Reported: true, Room: 0}, {Account: "", Reported: true, Room: 3}},
		ResetsAt: time.Now().Add(2*time.Hour + 10*time.Minute + 30*time.Second)}
	if auto {
		o.Auto, o.At, o.Setting = true, time.Now().Add(5*time.Second), "codex"
	}
	return o
}

// offerTo hands the model an agent.switch.offered event.
func offerTo(t *testing.T, m *Model, o proto.AgentSwitchOffer) tea.Cmd {
	t.Helper()
	return m.handleEvent(m.machines[0], eventMsg(t, proto.EventAgentSwitchOffered, o))
}

// Out of plan on every account: the card offers Codex, saying how full
// each account is, what crosses (read by the preview), that Claude's
// session stays, and how to have it done unasked. Wait calls the offer
// off on the server.
func TestLimitCardOffers(t *testing.T) {
	m, peer := switchFixture(t, true)
	peer.setResult(proto.MethodAgentSwitch, proto.AgentSwitchResult{From: "claude", To: "codex", Crossed: proto.SwitchNative, Preview: true,
		Crossing: &proto.SwitchCrossing{Messages: 42, Commands: 18, Notes: 6, Fits: true, Plan: true}})
	cmd := offerTo(t, m, outOfPlan(false))
	c, ok := m.overlay.(*limitCard)
	if !ok || cmd == nil {
		t.Fatalf("no card: %T", m.overlay)
	}
	for _, msg := range runCmds(t, m, cmd) {
		next, _ := m.Update(msg)
		*m = next.(Model)
	}
	peer.waitMethod(t, proto.MethodAgentSwitch, `"preview":true`)
	if c.preview == nil {
		t.Fatal("the preview was not kept")
	}
	text := cardText(*m)
	for _, want := range []string{"Claude Code is out of plan on every account", "work 100% · default 97% · resets in 2h 10m",
		"Continue in", "Codex?", "whole conversation", "Carries 42 messages · 18 commands · 6 notes · the plan",
		"Claude Code's session stays as it is", "Switch back is offered", "Continue in Codex", "Wait for the reset", "Choose…",
		`[agents] switch_at_limit = "codex"`} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q on the card:\n%s", want, text)
		}
	}
	if bar := ansi.Strip(m.statusBar()); !strings.Contains(bar, "OUT OF PLAN") || !strings.Contains(bar, "continue in codex") || !strings.Contains(bar, "wait") {
		t.Fatalf("status bar: %q", bar)
	}
	// w: waits for the reset, which the server is told.
	cmd = a1Key(t, m, a2Key("w"))
	if m.overlay != nil || cmd == nil {
		t.Fatal("w left the card up")
	}
	cmd()
	peer.waitMethod(t, proto.MethodAgentSwitchCancel, `"id":"p1"`)
}

// Continue in Codex, clicked: the switch is asked for (never -now: it is
// the turn's end anyway) and the card becomes the switch's.
func TestLimitCardContinueClicked(t *testing.T) {
	m, peer := switchFixture(t, true)
	offerTo(t, m, outOfPlan(false))
	c := m.overlay.(*limitCard)
	b := c.render(*m)
	bt := c.buttons[0]
	cmd := a1Mouse(t, m, b.x+1+bt.x0, b.y+1+bt.line, tea.MouseButtonLeft, tea.MouseActionPress)
	if cmd == nil {
		t.Fatal("a click on Continue asked nothing")
	}
	sc, ok := m.overlay.(*switchCard)
	if !ok || sc.state != switchAsking || sc.to != "codex" {
		t.Fatalf("after continue: %T", m.overlay)
	}
	cmd()
	p := peer.waitFor(t, "the switch", func(msg proto.Message) bool {
		return msg.Method == proto.MethodAgentSwitch && !strings.Contains(string(msg.Params), "preview")
	})
	if strings.Contains(string(p.Params), `"now"`) || !strings.Contains(string(p.Params), `"to":"codex"`) {
		t.Fatalf("asked %s", p.Params)
	}

	// Choose…, by its key, opens the agents; a stray click leaves the
	// card; Wait, clicked, puts it away.
	m.overlay = nil
	offerTo(t, m, outOfPlan(false))
	a1Mouse(t, m, 0, 0, tea.MouseButtonLeft, tea.MouseActionPress)
	if _, ok := m.overlay.(*limitCard); !ok {
		t.Fatal("a stray click put the card away")
	}
	a1Key(t, m, a2Key("c"))
	if mu, ok := m.overlay.(*menu); !ok || mu.switchOf == "" {
		t.Fatalf("c opened %T", m.overlay)
	}
	m.overlay = nil
	offerTo(t, m, outOfPlan(false))
	c = m.overlay.(*limitCard)
	b = c.render(*m)
	bt = c.buttons[1]
	if cmd := a1Mouse(t, m, b.x+1+bt.x0, b.y+1+bt.line, tea.MouseButtonLeft, tea.MouseActionPress); cmd == nil || m.overlay != nil {
		t.Fatal("Wait clicked did nothing")
	}
}

// switch_at_limit set: the card counts down to the server's own switch
// with Cancel; the switch, when the server makes it, is said, and the
// card goes. Cancel calls it off.
func TestLimitCardCountsDownToTheServersSwitch(t *testing.T) {
	m, peer := switchFixture(t, true)
	cmd := offerTo(t, m, outOfPlan(true))
	c := m.overlay.(*limitCard)
	if cmd == nil {
		t.Fatal("nothing to count with")
	}
	text := cardText(*m)
	for _, want := range []string{"Continuing in", "Codex in 5s", "Continue Now", "Cancel"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q on the card:\n%s", want, text)
		}
	}
	if strings.Contains(text, "switch_at_limit") {
		t.Errorf("tells somebody who set it how to set it:\n%s", text)
	}
	if cmd := c.tick(c.gen - 1); cmd != nil {
		t.Fatal("an old card's tick went on")
	}
	if cmd := c.tick(c.gen); cmd == nil {
		t.Fatal("the countdown stopped")
	}
	// The server switches.
	m.handleEvent(m.machines[0], eventMsg(t, proto.EventAgentSwitched, proto.AgentSwitchResult{Pane: proto.PaneInfo{ID: "p1", Name: "api"},
		From: "claude", To: "codex", Crossed: proto.SwitchNative, Reason: "limit"}))
	if m.overlay != nil || !strings.Contains(m.flash, "out of plan on every account") || !strings.Contains(m.flash, "switched to Codex") ||
		!strings.Contains(m.flash, "Switch back") {
		t.Fatalf("after: %T %q", m.overlay, m.flash)
	}

	// Cancel, by esc, calls the server's switch off.
	offerTo(t, m, outOfPlan(true))
	cmd = a1Key(t, m, a2Key("esc"))
	if m.overlay != nil || cmd == nil {
		t.Fatal("esc left it")
	}
	if msg := cmd(); !strings.Contains(string(msg.(flashMsg)), "called off") {
		t.Fatalf("said %v", msg)
	}
	peer.waitMethod(t, proto.MethodAgentSwitchCancel, `"id":"p1"`)

	// The server's switch waiting on background commands: the card says so.
	offerTo(t, m, outOfPlan(true))
	m.handleEvent(m.machines[0], eventMsg(t, proto.EventAgentSwitching, proto.AgentSwitching{Pane: "p1", From: "claude", To: "codex",
		Waits: []string{"1 background command(s), up to 10m0s: npm run dev"}}))
	if sc, ok := m.overlay.(*switchCard); !ok || sc.state != switchWaiting || !strings.Contains(cardText(*m), "npm run dev") {
		t.Fatalf("waiting: %T", m.overlay)
	}
	// And a failure of the server's switch is said with no card up.
	m.overlay = nil
	delete(m.machines[0].switching, "p1")
	m.handleEvent(m.machines[0], eventMsg(t, proto.EventAgentSwitched, proto.AgentSwitchResult{Pane: proto.PaneInfo{ID: "p1", Name: "api"},
		From: "claude", To: "codex", Reason: "limit", Error: "Codex is not installed here"}))
	if !strings.Contains(m.flash, "failed: Codex is not installed here") {
		t.Fatalf("flash %q", m.flash)
	}
}

// An offer withdrawn takes its card away; one with nobody to carry on is
// said, not carded; one arriving over another overlay is said, leaving
// that overlay.
func TestLimitOfferElsewise(t *testing.T) {
	m, _ := switchFixture(t, true)
	offerTo(t, m, outOfPlan(false))
	offerTo(t, m, proto.AgentSwitchOffer{Pane: "p2", From: "claude", To: "codex", Gone: true})
	if m.overlay == nil {
		t.Fatal("another pane's withdrawal took the card")
	}
	offerTo(t, m, proto.AgentSwitchOffer{Pane: "p1", From: "claude", To: "codex", Gone: true})
	if m.overlay != nil {
		t.Fatal("a withdrawn offer left its card")
	}
	offerTo(t, m, proto.AgentSwitchOffer{Pane: "p1", From: "claude", Why: `switch_at_limit names codex, but Codex is out of plan too`})
	if m.overlay != nil || !strings.Contains(m.flash, "Codex is out of plan too") {
		t.Fatalf("nobody to carry on: %T %q", m.overlay, m.flash)
	}
	mu := &menu{title: "x"}
	m.overlay = mu
	offerTo(t, m, outOfPlan(false))
	if m.overlay != mu || !strings.Contains(m.flash, "Switch Agent") {
		t.Fatalf("over a menu: %T %q", m.overlay, m.flash)
	}
	offerTo(t, m, outOfPlan(true))
	if m.overlay != mu || !strings.Contains(m.flash, "continuing in Codex") {
		t.Fatalf("over a menu, unasked: %T %q", m.overlay, m.flash)
	}
	// A switch made by hand is not taken for the server's.
	if m.limitSwitched(localMachine, proto.AgentSwitchResult{Pane: proto.PaneInfo{ID: "p1"}, To: "codex"}) {
		t.Fatal("a hand switch said as the limit's")
	}
}

// The card at every size: no line wider than the screen.
func TestLimitCardFitsEverySize(t *testing.T) {
	for _, sz := range [][2]int{{1, 1}, {20, 5}, {38, 12}, {60, 20}, {120, 40}, {300, 80}} {
		for _, auto := range []bool{false, true} {
			m, _ := switchFixture(t, true)
			next, _ := m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
			*m = next.(Model)
			o := outOfPlan(auto)
			o.Why = strings.Repeat("a long reason ", 30)
			for i := 0; i < 8; i++ {
				o.Accounts = append(o.Accounts, proto.AccountRank{Account: strings.Repeat("n", 20), Reported: true})
			}
			offerTo(t, m, o)
			m.overlay.(*limitCard).preview = &proto.AgentSwitchResult{Crossing: &proto.SwitchCrossing{Messages: 9999, Commands: 999, Notes: 99, Shortened: 12}}
			for _, s := range strings.Split(m.View(), "\n") {
				if ansi.StringWidth(s) > sz[0] {
					t.Fatalf("%dx%d auto %v: a line %d wide: %q", sz[0], sz[1], auto, ansi.StringWidth(s), ansi.Strip(s))
				}
			}
		}
	}
}

func TestResetsIn(t *testing.T) {
	for d, want := range map[time.Duration]string{30 * time.Second: "in a minute", 6 * time.Minute: "in 6m", 26*time.Hour + 5*time.Minute: "in 26h 5m"} {
		if got := resetsIn(d); got != want {
			t.Errorf("%v: %q", d, got)
		}
	}
	if got := offerFullness(proto.AgentSwitchOffer{Accounts: []proto.AccountRank{{Account: "x"}}}, time.Now()); got != "" {
		t.Errorf("nothing reported: %q", got)
	}
}
