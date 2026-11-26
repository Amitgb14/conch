package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// Another agent at a plan limit (#352 phase 3): the server offers one when
// an agent is out of plan on every account it has (agent.switch.offered),
// and this card puts the offer to the person — Continue in Codex, Wait for
// the reset, Choose… — with what would cross, read by the same preview
// Switch Agent ▸ uses. With [agents] switch_at_limit the server switches
// by itself after a few seconds, and the card counts those down with
// Cancel; either way the switch is said, never silent, and the pane's
// banner then offers Switch back.

// limitCard is an offer of another agent at a plan limit.
type limitCard struct {
	machine string
	offer   proto.AgentSwitchOffer
	// preview is what would cross, once agent.switch has said; nil until.
	preview *proto.AgentSwitchResult
	gen     int
	buttons []cardButton
}

// limitPreviewMsg is the preview read for a limit card.
type limitPreviewMsg struct {
	machine, pane string
	res           proto.AgentSwitchResult
	err           error
}

// limitTickMsg redraws a counting card each second.
type limitTickMsg struct{ gen int }

// switchOffered takes agent.switch.offered.
func (m *Model) switchOffered(mach *machine, o proto.AgentSwitchOffer) tea.Cmd {
	c := m.limitCardFor(mach.id, o.Pane)
	if o.Gone {
		if c != nil {
			m.overlay = nil
		}
		return nil
	}
	p := m.pane(mach.id, o.Pane)
	name := o.Pane
	if p != nil {
		name = p.DisplayName()
	}
	if o.To == "" {
		m.setFlash(name+": "+agentLabel(o.From)+" is out of plan on every account; "+o.Why, true)
		return nil
	}
	if m.overlay != nil && c == nil {
		// Something else is open: said, not put over it. The pane's menu
		// still has Switch Agent ▸.
		what := "m → Switch Agent ▸ to continue in " + agentLabel(o.To)
		if o.Auto {
			what = "continuing in " + agentLabel(o.To) + " in a few seconds"
		}
		m.setFlash(name+": "+agentLabel(o.From)+" is out of plan on every account · "+what, true)
		return nil
	}
	switchGen++
	m.overlay = &limitCard{machine: mach.id, offer: o, gen: switchGen}
	cmds := []tea.Cmd{m.limitPreview(mach.id, o.Pane, o.To)}
	if o.Auto {
		cmds = append(cmds, limitTick(switchGen))
	}
	return tea.Batch(cmds...)
}

func limitTick(gen int) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return limitTickMsg{gen: gen} })
}

// limitPreview asks what the switch would carry, for the card.
func (m *Model) limitPreview(mid, pane, to string) tea.Cmd {
	c := m.clientOf(mid)
	if c == nil {
		return nil
	}
	return func() tea.Msg {
		var res proto.AgentSwitchResult
		err := callCtxFor(c, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: pane, To: to, Preview: true}, &res, time.Minute)
		return limitPreviewMsg{machine: mid, pane: pane, res: res, err: err}
	}
}

func (m *Model) limitPreviewed(msg limitPreviewMsg) {
	if c := m.limitCardFor(msg.machine, msg.pane); c != nil && msg.err == nil {
		res := msg.res
		c.preview = &res
	}
}

func (m Model) limitCardFor(mid, pane string) *limitCard {
	c, ok := m.overlay.(*limitCard)
	if !ok || c.machine != mid || c.offer.Pane != pane {
		return nil
	}
	return c
}

func (m Model) limitCardUp() bool {
	_, ok := m.overlay.(*limitCard)
	return ok
}

// tick redraws the countdown until the server's moment.
func (c *limitCard) tick(gen int) tea.Cmd {
	if gen != c.gen || !c.offer.Auto {
		return nil
	}
	return limitTick(c.gen)
}

// carryOn switches now: the card becomes the switch card, asking.
func (c *limitCard) carryOn(m *Model) tea.Cmd {
	pre := proto.AgentSwitchResult{From: c.offer.From, To: c.offer.To, Crossed: c.offer.Crossed}
	if c.preview != nil {
		pre = *c.preview
	}
	switchGen++
	sc := &switchCard{machine: c.machine, pane: c.offer.Pane, to: c.offer.To, preview: pre, gen: switchGen}
	m.overlay = sc
	return sc.switchNow(m)
}

// wait puts the offer aside: the server holds the agent where it is, and
// offers nothing again until it has worked. For a switch the server
// would make by itself, that calls it off.
func (c *limitCard) wait(m *Model) tea.Cmd {
	m.overlay = nil
	cl := m.clientOf(c.machine)
	if cl == nil {
		return nil
	}
	pane, from, auto := c.offer.Pane, agentLabel(c.offer.From), c.offer.Auto
	return func() tea.Msg {
		if err := callCtx(cl, proto.MethodAgentSwitchCancel, proto.AgentSwitchCancelParams{ID: pane}, nil); err != nil {
			return errMsg{err}
		}
		if auto {
			return flashMsg("the switch was called off: " + from + " waits for its plan to reset")
		}
		return flashMsg(from + " waits for its plan to reset")
	}
}

// choose opens the list of agents, as Switch Agent ▸ does.
func (c *limitCard) choose(m *Model) tea.Cmd {
	m.overlay = nil
	if mu := newSwitchMenu(*m, c.machine, c.offer.Pane, max(m.width/2-25, 0), max(m.height/3, 0), nil); mu != nil {
		m.overlay = mu
	}
	return nil
}

func (c *limitCard) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter", " ":
			return true, c.carryOn(m)
		case "w", "esc", "q":
			return true, c.wait(m)
		case "c":
			return true, c.choose(m)
		}
		return true, nil // the card holds the keys while it is up
	}
	return false, nil
}

func (c *limitCard) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || !b.contains(msg.X, msg.Y) {
		return nil
	}
	line, x := msg.Y-b.y-1, msg.X-b.x-1
	for _, bt := range c.buttons {
		if line == bt.line && x >= bt.x0 && x < bt.x1 {
			return bt.act(m)
		}
	}
	return nil
}

func (c *limitCard) dimBackground() bool { return false }

func (c *limitCard) render(m Model) box {
	mr := m.mainRect()
	w := clamp(mr.w-4, 10, 100)
	w = min(w, max(m.width-2, 1))
	lines, buttons := c.lines(m, w, time.Now())
	c.buttons = buttons
	b := box{lines: frameLines(" ⚠ Out of plan ", lines, w, colorWarn)}
	b.x = clamp(mr.x+(mr.w-b.width())/2, 0, max(m.width-b.width(), 0))
	b.y = clamp(m.height-statusHeight-len(b.lines)-1, 0, max(m.height-len(b.lines), 0))
	return b
}

// offerFullness says how full each of the agent's accounts is and when
// the first turns over: "work 100% · personal 97% · resets in 2h 10m".
func offerFullness(o proto.AgentSwitchOffer, now time.Time) string {
	var parts []string
	for _, r := range o.Accounts {
		name := r.Account
		if name == "" {
			name = "default"
		}
		if r.Reported {
			parts = append(parts, fmt.Sprintf("%s %d%%", name, int(100-r.Room+0.5)))
		}
	}
	if !o.ResetsAt.IsZero() && o.ResetsAt.After(now) {
		parts = append(parts, "resets "+resetsIn(o.ResetsAt.Sub(now)))
	}
	return strings.Join(parts, " · ")
}

// resetsIn is a wait as the card says it: "in 2h 10m", "in 6m".
func resetsIn(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "in a minute"
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	}
	return fmt.Sprintf("in %dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func (c *limitCard) lines(m Model, w int, now time.Time) ([]string, []cardButton) {
	o := c.offer
	from, to := agentLabel(o.From), agentLabel(o.To)
	mark, style := m.agentMark(proto.PaneInfo{Agent: &proto.AgentStatus{Name: o.To}})
	target := style.Render(mark) + styleOK.Render(to)
	lines := []string{spread(styleWarn.Render("⚠ "+from+" is out of plan on every account"), styleMuted.Render(offerFullness(o, now)), w)}
	// Prose wraps, three lines at most.
	say := func(s string) {
		wrapped := strings.Split(ansi.Wordwrap(s, max(w, 1), " "), "\n")
		if len(wrapped) > 3 {
			wrapped = append(wrapped[:2], ansi.Truncate(strings.Join(wrapped[2:], " "), w, "…"))
		}
		for _, l := range wrapped {
			lines = append(lines, fit(l, w))
		}
	}
	how := "It takes the whole conversation and your last message, and carries on."
	if o.Crossed == proto.SwitchHandoff {
		how = "It is handed the conversation as a document for its first message."
	}
	if o.Auto {
		left := int(o.At.Sub(now).Round(time.Second).Seconds())
		when := fmt.Sprintf(" in %ds", max(left, 0))
		if left <= 0 {
			when = " now"
		}
		say("Continuing in " + target + when + ". " + how)
	} else {
		say("Continue in " + target + "? " + how)
	}
	if pre := c.preview; pre != nil && pre.Crossing != nil {
		cr := pre.Crossing
		carries := []string{countOf(cr.Messages, "message"), countOf(cr.Commands, "command"), countOf(cr.Notes, "note")}
		if cr.Plan {
			carries = append(carries, "the plan")
		}
		s := styleMuted.Render("Carries ") + strings.Join(carries, " · ")
		if !cr.Fits || cr.Shortened > 0 {
			s += styleMuted.Render(fmt.Sprintf(" · %s shortened to fit", countOf(cr.Shortened, "step")))
		}
		lines = append(lines, fit(s, w))
	}
	say(styleMuted.Render(from + "'s session stays as it is. When " + from + " has room again, Switch back is offered."))
	if o.Why != "" {
		say(styleMuted.Render("Note: " + o.Why + "."))
	}

	type btn struct {
		label string
		on    bool
		act   func(m *Model) tea.Cmd
	}
	bs := []btn{{"Continue in " + to, true, c.carryOn}, {"Wait for the reset", false, c.wait}, {"Choose…", false, c.choose}}
	if o.Auto {
		bs = []btn{{"Continue Now", true, c.carryOn}, {"Cancel", false, c.wait}, {"Choose…", false, c.choose}}
	}
	var row strings.Builder
	var buttons []cardButton
	x := 0
	for i, b := range bs {
		if i > 0 {
			row.WriteString("  ")
			x += 2
		}
		label := " " + b.label + " "
		st := styleSelDim
		if b.on {
			st = styleSel
		}
		row.WriteString(st.Render(label))
		buttons = append(buttons, cardButton{line: len(lines) + 1, x0: x, x1: x + ansi.StringWidth(label), act: b.act})
		x += ansi.StringWidth(label)
	}
	lines = append(lines, "", fit(row.String(), w))
	if !o.Auto && o.Setting == "" {
		lines = append(lines, fit(styleMuted.Render("To do this without asking: ")+`[agents] switch_at_limit = "`+o.To+`"`, w))
	}
	return lines, buttons
}

// limitSwitched says a switch the server made at a plan limit, closing
// the card that counted down to it.
func (m *Model) limitSwitched(mid string, res proto.AgentSwitchResult) bool {
	if res.Reason != "limit" {
		return false
	}
	if c := m.limitCardFor(mid, res.Pane.ID); c != nil {
		m.overlay = nil
	}
	if res.Error != "" {
		m.setFlash(res.Pane.DisplayName()+": the switch to "+agentLabel(res.To)+" at the plan limit failed: "+res.Error, true)
		return true
	}
	m.setFlash(res.Pane.DisplayName()+": "+agentLabel(res.From)+" was out of plan on every account; "+switchedText(res)+" · Switch back is above it", false)
	return true
}
