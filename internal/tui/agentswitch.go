package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// Switching a running agent's conversation to another agent in the same
// pane (#352): Switch Agent ▸ on an agent's menu lists the agents it could
// go to, a card says what would cross and counts down before asking the
// server, and the pane then says where its conversation came from, with
// Switch back. The server does the switching (agent.switch); never
// mid-turn, so a switch asked for while the agent works waits for the
// turn to end, and the card says so until it is made or called off.

// switchCountdown is how long the card waits before switching: long
// enough to read what crosses and change your mind.
const switchCountdown = 5

// switchMenuItem is Switch Agent ▸ on an agent pane's menu.
func (m Model) switchMenuItem(mid, pane string, x, y int) menuItem {
	if !m.hasCapability(mid, proto.CapAgentSwitch) {
		return menuItem{"", "Switch Agent ▸" + styleMuted.Render("  its server is too old"), func(m *Model) tea.Cmd {
			m.setFlash("the conch server there cannot switch agents yet: reload it with conch server reload", true)
			return nil
		}}
	}
	return menuItem{"", "Switch Agent ▸", func(m *Model) tea.Cmd {
		mu := newSwitchMenu(*m, mid, pane, x, y, m.menuFrom)
		if mu == nil {
			return nil
		}
		m.overlay = mu
		if mach := m.machine(mid); mach != nil {
			return mach.checkAgents() // what is installed is what goes stale
		}
		return nil
	}}
}

// newSwitchMenu lists the agents a pane's conversation can be switched
// to: each installed one but its own, saying how it would cross.
func newSwitchMenu(m Model, mid, pane string, x, y int, back *menu) *menu {
	p, mach := m.pane(mid, pane), m.machine(mid)
	if p == nil || p.Agent == nil || mach == nil {
		return nil
	}
	from := p.Agent.Name
	var items []menuItem
	if sw, ok := mach.switching[pane]; ok {
		to := agentLabel(sw.To)
		items = append(items, menuItem{"x", "Call off the switch to " + to, func(m *Model) tea.Cmd { return m.cancelSwitch(mid, pane, to) }})
	}
	n := 0
	for _, a := range installedFirst(mach.agentList, true) {
		if a.Name == from {
			continue
		}
		a := a
		label := a.Label
		if label == "" {
			label = agentLabel(a.Name)
		}
		mark, style := m.agentMark(proto.PaneInfo{Agent: &proto.AgentStatus{Name: a.Name}})
		name := style.Render(mark) + label
		if a.SwitchTo == proto.SwitchNever {
			why := label + " cannot be given a first message, so a conversation cannot be handed to it"
			items = append(items, menuItem{"", styleMuted.Render(mark + label + "  cannot take a first message"), func(m *Model) tea.Cmd {
				m.setFlash(why, true)
				return nil
			}})
			continue
		}
		how := styleMuted.Render("  handoff document")
		if a.SwitchTo == proto.SwitchNative {
			how = styleOK.Render("  full history")
		}
		key := ""
		if n < 9 {
			n++
			key = fmt.Sprint(n)
		}
		items = append(items, menuItem{key, name + how, func(m *Model) tea.Cmd { return m.previewSwitch(mid, pane, a.Name) }})
	}
	if n == 0 && len(items) == 0 {
		items = append(items, menuItem{"", styleMuted.Render("no other agent is installed here"), nil})
	}
	who := agentLabel(from)
	items = append(items,
		menuItem{"", styleMuted.Render("Switches when this turn ends."), nil},
		menuItem{"", styleMuted.Render(who + "'s own session is kept, so you can switch back."), nil})
	return &menu{title: "Switch " + who + " to", items: items, x: x + 4, y: y + 1, back: back, switchOf: paneKey(mid, pane)}
}

// switchPreviewMsg is what agent.switch said it would do.
type switchPreviewMsg struct {
	machine, pane, to string
	res               proto.AgentSwitchResult
	err               error
}

// switchMadeMsg is the answer to the switch itself: made, or waiting.
type switchMadeMsg struct {
	machine, pane, to string
	res               proto.AgentSwitchResult
	err               error
}

// switchTickMsg counts the card down; gen tells a card's ticks from an
// earlier card's.
type switchTickMsg struct{ gen int }

// previewSwitch asks what switching would carry, for the card.
func (m *Model) previewSwitch(mid, pane, to string) tea.Cmd {
	c := m.clientOf(mid)
	if c == nil {
		m.setFlash(m.offlineText(mid), true)
		return nil
	}
	m.setFlash("reading the conversation…", false)
	return func() tea.Msg {
		var res proto.AgentSwitchResult
		err := callCtxFor(c, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: pane, To: to, Preview: true}, &res, time.Minute)
		return switchPreviewMsg{machine: mid, pane: pane, to: to, res: res, err: err}
	}
}

// switchCard is the card a switch is decided on.
type switchCard struct {
	machine, pane, to string
	preview           proto.AgentSwitchResult
	left              int // seconds before it asks by itself
	gen               int
	state             switchState
	waits             []string
	err               string
	// buttons are where the last render put each button: the content
	// line and the columns, for clicks.
	buttons []cardButton
}

type cardButton struct {
	line, x0, x1 int
	act          func(m *Model) tea.Cmd
}

type switchState int

const (
	switchCounting switchState = iota
	switchAsking               // asked, no answer yet
	switchWaiting              // the server waits for the turn to end
	switchFailed
)

var switchGen int

func (m *Model) openSwitchCard(msg switchPreviewMsg) tea.Cmd {
	if msg.err != nil {
		m.setFlash("can't switch: "+errText(msg.err), true)
		return nil
	}
	m.flash = ""
	switchGen++
	m.overlay = &switchCard{machine: msg.machine, pane: msg.pane, to: msg.to, preview: msg.res, left: switchCountdown, gen: switchGen}
	return switchTick(switchGen)
}

func switchTick(gen int) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return switchTickMsg{gen: gen} })
}

// switchNow asks the server to switch. Without now: a turn under way is
// waited for, which the answer says.
func (c *switchCard) switchNow(m *Model) tea.Cmd {
	if c.state != switchCounting {
		return nil
	}
	cl := m.clientOf(c.machine)
	if cl == nil {
		m.setFlash(m.offlineText(c.machine), true)
		return nil
	}
	c.state = switchAsking
	mid, pane, to := c.machine, c.pane, c.to
	return func() tea.Msg {
		var res proto.AgentSwitchResult
		err := callCtxFor(cl, proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: pane, To: to}, &res, 3*time.Minute)
		return switchMadeMsg{machine: mid, pane: pane, to: to, res: res, err: err}
	}
}

// cancel closes the card, calling off a switch the server holds.
func (c *switchCard) cancel(m *Model) tea.Cmd {
	m.overlay = nil
	if c.state == switchWaiting || c.state == switchAsking {
		return m.cancelSwitch(c.machine, c.pane, agentLabel(c.to))
	}
	return nil
}

// choose goes back to the list of agents.
func (c *switchCard) choose(m *Model) tea.Cmd {
	if c.state == switchWaiting || c.state == switchAsking {
		return nil // called off first, or the server holds two
	}
	m.overlay = nil
	mu := newSwitchMenu(*m, c.machine, c.pane, max(m.width/2-25, 0), max(m.height/3, 0), nil)
	if mu == nil {
		return nil
	}
	m.overlay = mu
	return nil
}

func (m *Model) cancelSwitch(mid, pane, to string) tea.Cmd {
	cl := m.clientOf(mid)
	if cl == nil {
		return nil
	}
	if mach := m.machine(mid); mach != nil {
		delete(mach.switching, pane)
	}
	return func() tea.Msg {
		if err := callCtx(cl, proto.MethodAgentSwitchCancel, proto.AgentSwitchCancelParams{ID: pane}, nil); err != nil {
			return errMsg{err}
		}
		return flashMsg("the switch to " + to + " was called off")
	}
}

func (c *switchCard) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q":
			return true, c.cancel(m)
		case "enter", " ":
			if c.state == switchFailed {
				m.overlay = nil
				return true, nil
			}
			return true, c.switchNow(m)
		case "c":
			return true, c.choose(m)
		}
		return true, nil // the card holds the keys while it is up
	}
	return false, nil
}

func (c *switchCard) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if !b.contains(msg.X, msg.Y) {
		return nil // the card stays: a switch is not put aside by a stray click
	}
	line, x := msg.Y-b.y-1, msg.X-b.x-1
	for _, bt := range c.buttons {
		if line == bt.line && x >= bt.x0 && x < bt.x1 {
			return bt.act(m)
		}
	}
	return nil
}

// tick counts down, and asks when it gets to nothing.
func (c *switchCard) tick(m *Model, gen int) tea.Cmd {
	if gen != c.gen || c.state != switchCounting {
		return nil
	}
	c.left--
	if c.left <= 0 {
		return c.switchNow(m)
	}
	return switchTick(c.gen)
}

func (c *switchCard) render(m Model) box {
	mr := m.mainRect()
	w := clamp(mr.w-4, 10, 100)
	w = min(w, max(m.width-2, 1))
	lines, buttons := c.lines(m, w)
	c.buttons = buttons
	color := colorAccent
	if c.state == switchFailed {
		color = colorWarn
	}
	b := box{lines: frameLines(" ⇄ Switch agent ", lines, w, color)}
	b.x = clamp(mr.x+(mr.w-b.width())/2, 0, max(m.width-b.width(), 0))
	b.y = clamp(m.height-statusHeight-len(b.lines)-1, 0, max(m.height-len(b.lines), 0))
	return b
}

// lines is the card's content at width w, and where its buttons are.
func (c *switchCard) lines(m Model, w int) ([]string, []cardButton) {
	p := m.pane(c.machine, c.pane)
	from := agentLabel(c.preview.From)
	to := agentLabel(c.to)
	mark, style := m.agentMark(proto.PaneInfo{Agent: &proto.AgentStatus{Name: c.to}})
	target := style.Render(mark) + to
	where, turn := "", "the turn has ended"
	if p != nil {
		where = p.DisplayName()
		if p.Agent != nil && (p.Agent.State == proto.AgentWorking || p.Agent.State == proto.AgentBlocked) {
			turn = "waits for the turn to end"
		}
	}
	var head string
	switch c.state {
	case switchCounting:
		head = styleBold.Render("Switching to ") + target + styleBold.Render(fmt.Sprintf(" in %ds", c.left))
	case switchAsking:
		head = styleBold.Render("Switching to ") + target + styleBold.Render("…")
	case switchWaiting:
		head = styleBold.Render("Switching to ") + target + styleBold.Render(" when the turn ends")
	case switchFailed:
		head = styleErr.Render("Could not switch to ") + target
	}
	right := where
	if c.state == switchCounting || c.state == switchAsking {
		right = strings.TrimPrefix(where+" · "+turn, " · ")
	}
	lines := []string{spread(head, styleMuted.Render(right), w)}
	// A value wraps under itself, three lines at most: a reason is
	// worth reading whole, and a card taller than that hides the pane.
	kv := func(k, v string) {
		room := max(w-9, 1)
		wrapped := strings.Split(ansi.Wordwrap(v, room, " "), "\n")
		if len(wrapped) > 3 {
			wrapped = append(wrapped[:2], ansi.Truncate(strings.Join(wrapped[2:], " "), room, "…"))
		}
		for i, part := range wrapped {
			label := ""
			if i == 0 {
				label = k
			}
			lines = append(lines, fit(styleMuted.Render(padRight(label, 9))+ansi.Truncate(part, room, "…"), w))
		}
	}
	switch c.state {
	case switchFailed:
		kv("Why", c.err)
		kv("Keeps", from+" running as it was")
	case switchWaiting:
		kv("Waits", strings.Join(c.waits, " and "))
		kv("Keeps", from+"'s session as it is, so you can switch back")
	default:
		cr := c.preview.Crossing
		if cr == nil {
			cr = &proto.SwitchCrossing{}
		}
		carries := []string{countOf(cr.Messages, "message"), countOf(cr.Commands, "command") + " with their output", countOf(cr.Notes, "note") + " for reads and edits"}
		if cr.Plan {
			carries = append(carries, "the plan")
		}
		kv("Carries", strings.Join(carries, " · "))
		fits := "all " + humanBytes(cr.Bytes) + ", nothing shortened"
		if cr.Bytes == 0 {
			fits = "all of it, nothing shortened"
		}
		if !cr.Fits || cr.Shortened > 0 {
			fits = fmt.Sprintf("%s, %s shortened to fit %s's context", humanBytes(cr.Bytes), countOf(cr.Shortened, "step"), to)
		}
		kv("Fits", fits)
		if len(cr.Left) > 0 {
			kv("Leaves", strings.Join(cr.Left, "; "))
		}
		if c.preview.Crossed == proto.SwitchHandoff {
			how := "the handoff document, as " + to + "'s first message"
			if c.preview.Why != "" {
				how += ": " + c.preview.Why
			}
			kv("How", how)
		} else {
			kv("How", "as "+to+"'s own session, resumed with the history")
		}
		kv("Keeps", from+"'s session as it is, so Switch back works at any time")
	}
	// The buttons, each one remembered for a click.
	type btn struct {
		label string
		on    bool
		act   func(m *Model) tea.Cmd
	}
	var bs []btn
	switch c.state {
	case switchCounting:
		bs = []btn{{"Switch Now", true, c.switchNow}, {"Choose…", false, c.choose}, {"Cancel", false, func(m *Model) tea.Cmd { return c.cancel(m) }}}
	case switchAsking, switchWaiting:
		bs = []btn{{"Cancel", false, func(m *Model) tea.Cmd { return c.cancel(m) }}}
	case switchFailed:
		bs = []btn{{"Close", true, func(m *Model) tea.Cmd { m.overlay = nil; return nil }}, {"Choose…", false, c.choose}}
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
	return lines, buttons
}

func (c *switchCard) dimBackground() bool { return false }

// humanBytes is a size as a person reads one.
func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

func (m Model) switchCardUp() bool {
	_, ok := m.overlay.(*switchCard)
	return ok
}

// switchCardFor is the card for a pane, when one is up.
func (m Model) switchCardFor(mid, pane string) *switchCard {
	c, ok := m.overlay.(*switchCard)
	if !ok || c.machine != mid || c.pane != pane {
		return nil
	}
	return c
}

// switchMade takes the server's answer to a switch asked for on the card.
func (m *Model) switchMade(msg switchMadeMsg) tea.Cmd {
	c := m.switchCardFor(msg.machine, msg.pane)
	switch {
	case msg.err != nil:
		if c != nil {
			c.state, c.err = switchFailed, errText(msg.err)
			return nil
		}
		m.setFlash("can't switch to "+agentLabel(msg.to)+": "+errText(msg.err), true)
	case msg.res.Pending:
		if mach := m.machine(msg.machine); mach != nil {
			mach.noteSwitching(msg.pane, proto.AgentSwitching{Pane: msg.pane, From: msg.res.From, To: msg.to, Waits: msg.res.Waits})
		}
		if c != nil {
			c.state, c.waits = switchWaiting, msg.res.Waits
		}
	default:
		if c != nil {
			m.overlay = nil
		}
		m.setFlash(switchedText(msg.res), false)
	}
	return nil
}

// switchedText says what a switch did.
func switchedText(res proto.AgentSwitchResult) string {
	s := "switched to " + agentLabel(res.To)
	if res.Crossed == proto.SwitchHandoff {
		s += " with the handoff document"
		if res.Why != "" {
			s += " (" + res.Why + ")"
		}
		return s
	}
	return s + ", with the whole conversation"
}

// switchEvent takes agent.switching and agent.switched.
func (m *Model) switchEvent(mach *machine, msg proto.Message) tea.Cmd {
	switch msg.Event {
	case proto.EventAgentSwitching:
		var sw proto.AgentSwitching
		if !decodeInto(msg, &sw) {
			return nil
		}
		mach.noteSwitching(sw.Pane, sw)
		if c := m.switchCardFor(mach.id, sw.Pane); c != nil && c.state == switchAsking {
			c.state, c.waits = switchWaiting, sw.Waits
		}
		// The server's own switch at a plan limit, waiting on background
		// commands: the card counting down to it says so.
		if lc := m.limitCardFor(mach.id, sw.Pane); lc != nil {
			m.overlay = &switchCard{machine: mach.id, pane: sw.Pane, to: sw.To, state: switchWaiting, waits: sw.Waits,
				preview: proto.AgentSwitchResult{From: sw.From, To: sw.To}}
		}
	case proto.EventAgentSwitched:
		var res proto.AgentSwitchResult
		if !decodeInto(msg, &res) {
			return nil
		}
		_, waited := mach.switching[res.Pane.ID]
		delete(mach.switching, res.Pane.ID)
		if !waited && m.limitSwitched(mach.id, res) {
			return nil // made by the server at a plan limit, and said so
		}
		c := m.switchCardFor(mach.id, res.Pane.ID)
		switch {
		case res.Error != "" && c != nil:
			c.state, c.err = switchFailed, res.Error
		case res.Error != "":
			m.setFlash(res.Pane.DisplayName()+": the switch to "+agentLabel(res.To)+" failed: "+res.Error, true)
		case c != nil && c.state == switchWaiting:
			m.overlay = nil
			m.setFlash(switchedText(res), false)
		case waited:
			m.setFlash(res.Pane.DisplayName()+": "+switchedText(res), false)
		}
	}
	return nil
}

// ---- after a switch: where it came from, and the way back ----

// switchBanner is the line over a switched pane — where its
// conversation came from, what crossed, Switch back and × — and where
// those two are on it, or ok false when there is none to show.
func (m Model) switchBanner(l *leaf, w int) (line string, back, gone [2]int, ok bool) {
	if l == nil || l.view.Kind != kindPane || w < 12 {
		return "", back, gone, false
	}
	p := m.pane(l.view.Machine, l.view.PaneID)
	if p == nil || p.SwitchedFrom == nil || p.SwitchedFrom.Back || p.Agent == nil || p.State != proto.PaneRunning {
		return "", back, gone, false
	}
	if m.bannerGone[bannerKey(l.view.Machine, *p)] {
		return "", back, gone, false
	}
	sf := p.SwitchedFrom
	mark, style := m.agentMark(proto.PaneInfo{Agent: &proto.AgentStatus{Name: sf.Agent}})
	text := "⇄ Continued from " + style.Render(mark) + styleOK.Render(agentLabel(sf.Agent))
	if cr := sf.Crossing; cr != nil {
		text += styleOK.Render(" · " + countOf(cr.Messages, "message") + " · " + countOf(cr.Commands, "command") + " · " + countOf(cr.Notes, "note"))
	}
	const sb, x = "Switch back", "×"
	tail := styleLink.Render(sb) + "  " + styleMuted.Render(x) + " "
	tw := ansi.StringWidth(sb) + 2 + ansi.StringWidth(x) + 1
	room := w - tw - 2
	if room < 4 {
		return "", back, gone, false
	}
	head := " " + styleOK.Render(ansi.Truncate(text, room, "…"))
	line = head + strings.Repeat(" ", max(w-ansi.StringWidth(head)-tw, 0)) + tail
	at := w - tw
	back = [2]int{at, at + ansi.StringWidth(sb)}
	gone = [2]int{at + ansi.StringWidth(sb) + 2, at + ansi.StringWidth(sb) + 3}
	return line, back, gone, true
}

// bannerKey names one switch of one pane, so dismissing it leaves the
// next switch's banner to show.
func bannerKey(mid string, p proto.PaneInfo) string {
	at := ""
	if p.SwitchedFrom != nil {
		at = p.SwitchedFrom.At.UTC().Format(time.RFC3339Nano)
	}
	return paneKey(mid, p.ID) + "|" + at
}

// leafInner is the part of a leaf its content is drawn in: inside its
// border, below the switch banner when it has one.
func (m Model) leafInner(l *leaf, r rect) rect {
	in := m.inner(r)
	if in.h < 4 {
		return in // too short to give a row away
	}
	if _, _, _, ok := m.switchBanner(l, in.w); ok {
		return rect{in.x, in.y + 1, in.w, in.h - 1}
	}
	return in
}

// clickBanner acts on a click on a leaf's banner row; false when the
// click was elsewhere.
func (m *Model) clickBanner(x, y int) (tea.Cmd, bool) {
	rects, _ := m.leafRects()
	id := m.leafAt(rects, x, y)
	l := m.tab().leaf(id)
	if l == nil {
		return nil, false
	}
	in := m.inner(rects[id])
	if y != in.y || m.leafInner(l, rects[id]) == in {
		return nil, false
	}
	_, back, gone, _ := m.switchBanner(l, in.w)
	cx := x - in.x
	p := m.pane(l.view.Machine, l.view.PaneID)
	switch {
	case cx >= back[0] && cx < back[1]:
		return m.previewSwitch(l.view.Machine, l.view.PaneID, p.SwitchedFrom.Agent), true
	case cx >= gone[0] && cx < gone[1]:
		m.dismissBanner(l.view.Machine, *p)
		return nil, true
	}
	return nil, true // on the banner, not on what is under it
}

func (m *Model) dismissBanner(mid string, p proto.PaneInfo) {
	if m.bannerGone == nil {
		m.bannerGone = map[string]bool{}
	}
	m.bannerGone[bannerKey(mid, p)] = true
}

// noteSwitching remembers a switch the server holds for a pane, for the
// menu's Call off.
func (mach *machine) noteSwitching(pane string, sw proto.AgentSwitching) {
	if mach.switching == nil {
		mach.switching = map[string]proto.AgentSwitching{}
	}
	mach.switching[pane] = sw
}
