package tui

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/brain"
	"github.com/Amitgb14/conch/internal/proto"
)

// brainState is what the TUI keeps from the brain: agent summaries and the
// command bar's history.
type brainState struct {
	summaries map[string]*paneSummary // by paneKey
	inflight  int
	history   []string
}

type paneSummary struct {
	brain.Summary
	at      time.Time
	screen  uint64 // hash of the screen it summarised
	pending bool
	err     string
}

// maxSummaries in flight at once; each is a model request.
const maxSummaries = 2

type summaryMsg struct {
	key     string
	summary brain.Summary
	screen  uint64
	err     error
}

func newBrainState() *brainState {
	return &brainState{summaries: map[string]*paneSummary{}}
}

func (m Model) provider() (brain.Provider, error) {
	p, err := brain.New(m.cfg.Brain)
	if err != nil {
		return nil, err
	}
	return p, p.Check()
}

// summaryText is the one-line summary of a pane for display: what it needs
// from the user when anything, else what it's doing.
func (m Model) summaryText(mid, id string) string {
	if m.brain == nil {
		return ""
	}
	s := m.brain.summaries[paneKey(mid, id)]
	if s == nil || (s.Doing == "" && s.Needs == "") {
		return ""
	}
	if s.Needs != "" {
		return "needs: " + s.Needs
	}
	return s.Doing
}

// observeAgent summarises an agent on its own when automatic summaries are
// on and it just finished or started waiting for the user.
func (m *Model) observeAgent(mach *machine, old, info proto.PaneInfo) tea.Cmd {
	if !m.cfg.Brain.Summaries || info.Agent == nil {
		return nil
	}
	was := ""
	if old.Agent != nil {
		was = old.Agent.State
	}
	now := info.Agent.State
	switch {
	case now == was:
		return nil
	case now == proto.AgentBlocked, was == proto.AgentWorking && (now == proto.AgentDone || now == proto.AgentIdle):
		return m.summarize(mach.id, info, false)
	}
	return nil
}

// summarize asks the brain about a pane. A manual request runs even when
// the screen hasn't changed since the last summary.
func (m *Model) summarize(mid string, p proto.PaneInfo, manual bool) tea.Cmd {
	if m.brain == nil {
		m.brain = newBrainState()
	}
	key := paneKey(mid, p.ID)
	cur := m.brain.summaries[key]
	if cur != nil && cur.pending {
		return nil
	}
	if !manual && m.brain.inflight >= maxSummaries {
		return nil
	}
	c := m.clientOf(mid)
	if c == nil {
		return nil
	}
	provider, err := m.provider()
	if err != nil {
		if manual {
			m.setFlash(err.Error(), true)
		}
		return nil
	}
	if cur == nil {
		cur = &paneSummary{}
		m.brain.summaries[key] = cur
	}
	cur.pending = true
	m.brain.inflight++
	view := brain.AgentView{Title: p.Title, Branch: p.Branch, Cwd: p.Cwd}
	if p.Agent != nil {
		view.Agent, view.State = p.Agent.Name, p.Agent.State
	}
	last := cur.screen
	id := p.ID
	return func() tea.Msg {
		var screen proto.PaneReadResult
		if err := callCtx(c, proto.MethodPaneRead, proto.PaneRef{ID: id}, &screen); err != nil {
			return summaryMsg{key: key, err: err}
		}
		h := fnv.New64a()
		for _, l := range screen.Lines {
			h.Write([]byte(l))
		}
		sum := h.Sum64()
		if !manual && sum == last {
			return summaryMsg{key: key, screen: sum} // nothing new to say
		}
		view.Screen = screen.Lines
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		s, err := brain.Summarize(ctx, provider, view)
		return summaryMsg{key: key, summary: s, screen: sum, err: err}
	}
}

func (m *Model) receiveSummary(msg summaryMsg) {
	if m.brain == nil {
		return
	}
	m.brain.inflight = max(m.brain.inflight-1, 0)
	s := m.brain.summaries[msg.key]
	if s == nil {
		return
	}
	s.pending = false
	switch {
	case msg.err != nil:
		s.err = msg.err.Error()
		m.setFlash("summary: "+s.err, true)
	case msg.summary.Doing != "" || msg.summary.Needs != "":
		s.Summary, s.screen, s.at, s.err = msg.summary, msg.screen, time.Now(), ""
	}
}

// summarizeSelected is S: summarise the selected agent now.
func (m *Model) summarizeSelected() tea.Cmd {
	r, _ := m.selectedRow()
	if r.kind != kindPane {
		m.setFlash("select an agent to summarise", true)
		return nil
	}
	p := m.pane(r.machine, r.paneID)
	if p == nil || p.Agent == nil {
		m.setFlash("no agent is running in this pane", true)
		return nil
	}
	cmd := m.summarize(r.machine, *p, true)
	if cmd != nil {
		m.setFlash("summarising "+p.DisplayName()+"…", false)
	}
	return cmd
}

// world describes every machine for the planner.
func (m Model) world() brain.World {
	w := brain.World{DefaultAgent: m.defaultAgent()}
	for _, mach := range m.machines {
		sums := map[string]string{}
		for _, p := range mach.panes {
			if t := m.summaryText(mach.id, p.ID); t != "" {
				sums[p.ID] = t
			}
		}
		online := mach.state == stateOnline && mach.c != nil
		w.Machines = append(w.Machines, brain.MachineFrom(mach.id, mach.label, online, mach.agentList, mach.projects, mach.panes, sums))
	}
	r, ok := m.selectedRow()
	if ok {
		pl := m.contextPlace()
		switch r.kind {
		case kindPane:
			w.Selected = fmt.Sprintf("pane %s on machine %s", r.paneID, pl.machine)
		case kindBranch:
			w.Selected = fmt.Sprintf("branch %s of project %s on machine %s", r.branch, pl.projectID, pl.machine)
		case kindMachine:
			w.Selected = "machine " + pl.machine
		default:
			if pl.projectID != "" {
				w.Selected = fmt.Sprintf("project %s on machine %s", pl.projectID, pl.machine)
			}
		}
	}
	return w
}

// ---- command bar ----

type askPhase int

const (
	askInput askPhase = iota
	askThinking
	askPlan
	askRunning
	askDone
)

// askBar is the command bar: a request in plain words becomes a plan of
// conch actions, shown for confirmation before anything runs.
type askBar struct {
	phase  askPhase
	in     textinput.Model
	hist   int // position while browsing history; len(history) = new
	cancel context.CancelFunc
	err    string

	world   brain.World
	plan    brain.Plan
	actions []plannedAction
	sel     int
	started time.Time
}

type plannedAction struct {
	brain.Action
	desc    string
	invalid string // why it can't run
	on      bool
	result  string // after running: "" pending, else ✓/✗ text
	failed  bool
}

type (
	planMsg struct {
		bar  *askBar
		plan brain.Plan
		err  error
	}
	actionDoneMsg struct {
		bar *askBar
		i   int
		res brain.Result
		err error
	}
)

func (m *Model) openAsk() tea.Cmd {
	if m.brain == nil {
		m.brain = newBrainState()
	}
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "e.g. start 3 agents on api to fix the failing tests · what is waiting for me?"
	in.CharLimit = 2000
	in.Focus()
	b := &askBar{in: in, hist: len(m.brain.history)}
	m.overlay = b
	return textinput.Blink
}

func (b *askBar) width(m Model) int { return clamp(100, 40, max(m.width-4, 40)) }

func (b *askBar) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case planMsg:
		if msg.bar != b || b.phase != askThinking {
			return false, nil
		}
		if msg.err != nil {
			b.phase, b.err = askInput, msg.err.Error()
			return false, textinput.Blink
		}
		b.plan, b.phase, b.sel, b.actions = msg.plan, askPlan, 0, nil
		for _, a := range msg.plan.Actions {
			pa := plannedAction{Action: a}
			if err := b.world.Validate(&pa.Action); err != nil {
				pa.invalid = err.Error()
			} else {
				pa.on = true
			}
			pa.desc = b.world.Describe(pa.Action)
			b.actions = append(b.actions, pa)
		}
		return false, nil
	case actionDoneMsg:
		if msg.bar != b {
			return false, nil
		}
		pa := &b.actions[msg.i]
		var cmds []tea.Cmd
		switch {
		case msg.err != nil:
			pa.result, pa.failed = msg.err.Error(), true
		case msg.res.Pane != nil:
			pa.result = "started " + msg.res.Pane.DisplayName()
			mid, info := msg.res.Machine, *msg.res.Pane
			if mach := m.machine(mid); mach != nil && mach.paneIndex(info.ID) < 0 {
				mach.panes = append(mach.panes, info)
			}
			m.revealPane(mid, info)
			cmds = append(cmds, m.rebuild())
		case pa.Type == brain.ActFocus:
			pa.result = "shown"
			if p := m.pane(pa.Machine, pa.Pane); p != nil {
				m.revealPane(pa.Machine, *p)
				cmds = append(cmds, m.rebuild())
			}
		default:
			pa.result = "done"
		}
		cmds = append(cmds, b.runNext(m))
		return false, tea.Batch(cmds...)
	case tea.KeyMsg:
		return b.key(m, msg)
	}
	if b.phase == askInput {
		var cmd tea.Cmd
		b.in, cmd = b.in.Update(msg)
		return false, cmd
	}
	return false, nil
}

func (b *askBar) key(m *Model, k tea.KeyMsg) (bool, tea.Cmd) {
	switch b.phase {
	case askInput:
		switch k.String() {
		case "esc":
			m.overlay = nil
			return true, nil
		case "enter":
			return false, b.submit(m)
		case "up":
			if b.hist > 0 {
				b.hist--
				b.in.SetValue(m.brain.history[b.hist])
				b.in.CursorEnd()
			}
			return false, nil
		case "down":
			if b.hist < len(m.brain.history) {
				b.hist++
				if b.hist == len(m.brain.history) {
					b.in.SetValue("")
				} else {
					b.in.SetValue(m.brain.history[b.hist])
				}
				b.in.CursorEnd()
			}
			return false, nil
		}
		var cmd tea.Cmd
		b.in, cmd = b.in.Update(k)
		return false, cmd
	case askThinking:
		if k.String() == "esc" {
			if b.cancel != nil {
				b.cancel()
			}
			b.phase = askInput
			return false, textinput.Blink
		}
	case askPlan:
		switch k.String() {
		case "esc", "q":
			m.overlay = nil
			return true, nil
		case "e":
			b.phase = askInput
			return false, textinput.Blink
		case "up", "k":
			b.sel = max(b.sel-1, 0)
		case "down", "j":
			b.sel = min(b.sel+1, max(len(b.actions)-1, 0))
		case " ", "x":
			if b.sel < len(b.actions) && b.actions[b.sel].invalid == "" {
				b.actions[b.sel].on = !b.actions[b.sel].on
			}
		case "enter", "y":
			if b.selectedCount() == 0 {
				m.overlay = nil
				return true, nil
			}
			b.phase = askRunning
			return false, b.runNext(m)
		}
	case askRunning:
		// Running actions can't be interrupted halfway; keys wait.
	case askDone:
		m.overlay = nil
		return true, nil
	}
	return false, nil
}

func (b *askBar) selectedCount() int {
	n := 0
	for _, a := range b.actions {
		if a.on && a.invalid == "" {
			n++
		}
	}
	return n
}

func (b *askBar) submit(m *Model) tea.Cmd {
	request := strings.TrimSpace(b.in.Value())
	if request == "" {
		return nil
	}
	provider, err := m.provider()
	if err != nil {
		b.err = err.Error()
		return nil
	}
	if h := m.brain.history; len(h) == 0 || h[len(h)-1] != request {
		m.brain.history = append(h, request)
		if len(m.brain.history) > 50 {
			m.brain.history = m.brain.history[1:]
		}
	}
	b.hist = len(m.brain.history)
	b.world, b.err, b.phase, b.started = m.world(), "", askThinking, time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	b.cancel = cancel
	w := b.world
	return tea.Batch(m.startTicking(), func() tea.Msg {
		defer cancel()
		plan, err := brain.MakePlan(ctx, provider, w, request)
		return planMsg{bar: b, plan: plan, err: err}
	})
}

// runNext runs the next selected action that hasn't run yet.
func (b *askBar) runNext(m *Model) tea.Cmd {
	for i := range b.actions {
		pa := &b.actions[i]
		if !pa.on || pa.invalid != "" || pa.result != "" {
			continue
		}
		c := m.clientOf(pa.Machine)
		if c == nil {
			pa.result, pa.failed = m.offlineText(pa.Machine), true
			continue
		}
		cols, rows := m.paneArea()
		a := pa.Action
		pa.result = "…"
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			res, err := brain.Execute(ctx, c, a, cols, rows)
			return actionDoneMsg{bar: b, i: i, res: res, err: err}
		}
	}
	b.phase = askDone
	return nil
}

func (b *askBar) render(m Model) box {
	w := b.width(m)
	var lines []string
	switch b.phase {
	case askInput, askThinking:
		b.in.Width = w - 4
		lines = append(lines, " "+styleAccent.Render("✦ ")+b.in.View())
		switch {
		case b.phase == askThinking:
			spin := spinner[m.spin%len(spinner)]
			lines = append(lines, "", styleMuted.Render(fmt.Sprintf(" %s thinking with %s… %ds · esc cancel", spin, m.cfg.Brain.Provider, int(time.Since(b.started).Seconds()))))
		case b.err != "":
			lines = append(lines, "")
			for _, l := range wrap(b.err, w-3) {
				lines = append(lines, " "+styleErr.Render(l))
			}
		default:
			ctx := m.world().Selected
			if ctx == "" {
				ctx = "all machines"
			}
			lines = append(lines, "", styleMuted.Render(ansi.Truncate(" context: "+ctx, w-1, "…")),
				styleMuted.Render(" enter ask · ↑↓ history · esc close · nothing runs until you confirm"))
		}
	default:
		lines = append(lines, " "+styleMuted.Render("✦ "+ansi.Truncate(b.in.Value(), w-4, "…")), "")
		if b.plan.Reply != "" {
			for _, l := range wrap(b.plan.Reply, w-3) {
				lines = append(lines, " "+l)
			}
			lines = append(lines, "")
		}
		if len(b.actions) == 0 {
			lines = append(lines, styleMuted.Render(" no actions"))
		}
		for i, pa := range b.actions {
			box := styleMuted.Render("□")
			if pa.on {
				box = styleOK.Render("■")
			}
			text := pa.desc
			switch {
			case pa.invalid != "":
				box = styleErr.Render("✗")
				text += styleErr.Render(" — " + pa.invalid)
			case pa.result == "…":
				box = styleWork.Render(spinner[m.spin%len(spinner)])
			case pa.failed:
				box = styleErr.Render("✗")
				text += styleErr.Render(" — " + pa.result)
			case pa.result != "":
				box = styleOK.Render("✓")
				text += styleMuted.Render(" — " + pa.result)
			}
			wrapped := wrap(text, w-6)
			for j, l := range wrapped {
				prefix := "    "
				if j == 0 {
					prefix = " " + box + "  "
				}
				line := prefix + l
				if i == b.sel && b.phase == askPlan {
					line = styleSel.Render(fit(ansi.Strip(line), w))
				}
				lines = append(lines, line)
			}
		}
		hint := " space toggle · enter run selected · e edit request · esc cancel"
		switch b.phase {
		case askRunning:
			hint = " running…"
		case askDone:
			hint = " done · any key closes"
		}
		if b.phase == askPlan && len(b.actions) == 0 {
			hint = " e ask something else · esc close"
		}
		lines = append(lines, "", styleMuted.Render(hint))
	}
	maxLines := max(m.height-6, 5)
	if len(lines) > maxLines {
		lines = append(lines[:maxLines-1], styleMuted.Render(" …"))
	}
	bx := box{lines: frameLines(" ✦ Ask conch ", lines, w, colorAccent)}
	bx.x = max((m.width-bx.width())/2, 0)
	bx.y = max((m.height-len(bx.lines))/2, 0)
	return bx
}

func (b *askBar) mouse(m *Model, msg tea.MouseMsg, bx box) tea.Cmd {
	if !bx.contains(msg.X, msg.Y) && msg.Action == tea.MouseActionPress && b.phase != askRunning && b.phase != askThinking {
		m.overlay = nil
	}
	return nil
}

func (b *askBar) dimBackground() bool { return true }

// thinking reports whether the spinner must keep ticking.
func (b *askBar) thinking() bool { return b.phase == askThinking || b.phase == askRunning }
