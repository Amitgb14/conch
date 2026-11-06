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
		case kindWorkspace:
			w.Selected = "the projects on machine " + pl.machine
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

	// The list under the request: what to ask now and what was asked
	// before, narrowed by what is typed. pick is the highlighted one, -1
	// for none, so enter asks what was typed.
	opts     []askOption
	pick     int
	counts   askCounts
	optLines map[int]int // content line → index into shown(), for clicks
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
	in.Placeholder = "say what you want, or pick one below — e.g. start 3 agents on api to fix the flaky tests"
	in.CharLimit = 2000
	in.Focus()
	b := &askBar{in: in, hist: len(m.brain.history), pick: -1}
	b.world = m.world()
	b.counts = countAgents(b.world)
	here, place := "", false
	if r, ok := m.selectedRow(); ok {
		switch r.kind {
		case kindPane:
			if p := m.pane(r.machine, r.paneID); p != nil && p.Agent != nil {
				here = p.DisplayName()
			}
		case kindProject, kindBranch:
			place = true
		}
	}
	b.opts = askOptions(b.world, here, place, m.brain.history)
	m.overlay = b
	return textinput.Blink
}

func (b *askBar) width(m Model) int { return clamp(110, 40, max(m.width-4, 40)) }

// shown are the options the typed request leaves.
func (b *askBar) shown() []askOption { return matching(b.opts, b.in.Value()) }

// choose acts on an option: one to finish goes in the box, the rest are
// asked as they stand.
func (b *askBar) choose(m *Model, o askOption) tea.Cmd {
	b.pick = -1
	b.in.SetValue(o.stem())
	b.in.CursorEnd()
	if o.fill() {
		return textinput.Blink
	}
	return b.submit(m)
}

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
		b.showPlan(msg.plan)
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
			if opts := b.shown(); b.pick >= 0 && b.pick < len(opts) {
				return false, b.choose(m, opts[b.pick])
			}
			return false, b.submit(m)
		case "down":
			b.pick = min(b.pick+1, len(b.shown())-1)
			return false, nil
		case "up":
			b.pick = max(b.pick-1, -1)
			return false, nil
		case "tab":
			// Finish typing from the highlighted option, or the first.
			if opts := b.shown(); len(opts) > 0 {
				o := opts[max(b.pick, 0)]
				b.pick = -1
				b.in.SetValue(o.stem())
				b.in.CursorEnd()
			}
			return false, nil
		case "ctrl+p":
			b.pick = -1
			if b.hist > 0 {
				b.hist--
				b.in.SetValue(m.brain.history[b.hist])
				b.in.CursorEnd()
			}
			return false, nil
		case "ctrl+n":
			b.pick = -1
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
		// Typing narrows the list, so a highlight would point elsewhere.
		before := b.in.Value()
		var cmd tea.Cmd
		b.in, cmd = b.in.Update(k)
		if b.in.Value() != before {
			b.pick = -1
		}
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
		case "m":
			// The fast path read it one way; the model may read it another.
			if b.plan.Fast {
				return false, b.think(m, strings.TrimSpace(b.in.Value()))
			}
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
	// What needs no model is answered at once, and works with no
	// provider set up at all.
	if !m.cfg.Brain.NoFastPath {
		if plan, ok := brain.FastPlan(m.world(), request); ok {
			b.remember(m, request)
			b.world, b.err = m.world(), ""
			b.showPlan(plan)
			return nil
		}
	}
	return b.think(m, request)
}

// think asks the model for a plan.
func (b *askBar) think(m *Model, request string) tea.Cmd {
	provider, err := m.provider()
	if err != nil {
		b.phase, b.err = askInput, err.Error()
		return textinput.Blink
	}
	b.remember(m, request)
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

func (b *askBar) remember(m *Model, request string) {
	if h := m.brain.history; len(h) == 0 || h[len(h)-1] != request {
		m.brain.history = append(h, request)
		if len(m.brain.history) > 50 {
			m.brain.history = m.brain.history[1:]
		}
	}
	b.hist = len(m.brain.history)
}

// showPlan lists a plan's actions, checked against the world it was made for.
func (b *askBar) showPlan(plan brain.Plan) {
	b.plan, b.phase, b.sel, b.actions = plan, askPlan, 0, nil
	for _, a := range plan.Actions {
		pa := plannedAction{Action: a}
		if err := b.world.Validate(&pa.Action); err != nil {
			pa.invalid = err.Error()
		} else {
			pa.on = true
		}
		pa.desc = b.world.Describe(pa.Action)
		b.actions = append(b.actions, pa)
	}
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
		b.in.Width = w - 6
		b.optLines = nil
		lines = append(lines, "", "  "+styleAccent.Render("✦ ")+b.in.View(), " "+styleMuted.Render(strings.Repeat("─", max(w-2, 0))))
		switch {
		case b.phase == askThinking:
			spin := spinner[m.spin%len(spinner)]
			lines = append(lines, "", styleMuted.Render(fmt.Sprintf("  %s thinking with %s… %ds · esc cancel", spin, m.cfg.Brain.Provider, int(time.Since(b.started).Seconds()))))
		default:
			if b.err != "" {
				for _, l := range wrap(b.err, w-4) {
					lines = append(lines, "  "+styleErr.Render(l))
				}
			}
			lines = append(lines, b.inputLines(m, w, len(lines))...)
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
		if b.phase == askPlan && b.plan.Fast {
			hint += " · m ask the model"
			lines = append(lines, "", styleMuted.Render(" answered without the model"))
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

// inputLines are what is under the request while it is typed: where it
// applies, the agents, then what to ask — fitted to the screen, the list
// giving way first. above is how many lines are drawn before them.
func (b *askBar) inputLines(m Model, w, above int) []string {
	label := func(k, v string) string {
		return ansi.Truncate("  "+styleMuted.Render(fmt.Sprintf("%-9s", k))+v, w, "…")
	}
	ctx := m.world().Selected
	if ctx == "" {
		ctx = "all machines"
	}
	fast := "requests conch knows are answered at once"
	if m.cfg.Brain.NoFastPath {
		fast = "every request goes to the model"
	}
	head := []string{"", label("Context", ctx), label("Agents", b.counts.String()),
		label("Model", m.cfg.Brain.Provider+styleMuted.Render(" · "+fast))}

	opts := b.shown()
	var list []string
	at := map[int]int{} // index into list → option
	group := ""
	for i, o := range opts {
		g := "Suggested"
		if o.recent {
			g = "Recent"
		}
		if g != group {
			group = g
			list = append(list, "", "  "+styleBold.Render(g))
		}
		at[len(list)] = i
		list = append(list, b.optionLine(o, i == b.pick, w))
	}
	foot := []string{"", styleMuted.Render(ansi.Truncate("  enter ask · ↑↓ choose · tab fill in · ctrl+p/n history · esc close", w, "…")),
		styleMuted.Render("  nothing runs until you confirm")}
	if typed := strings.TrimSpace(b.in.Value()); typed != "" && b.pick < 0 {
		// Before enter is pressed, say whether it costs a model call.
		how := m.cfg.Brain.Provider + " plans it"
		if _, ok := brain.FastPlan(b.world, typed); ok && !m.cfg.Brain.NoFastPath {
			how = "conch answers it at once, no model"
		}
		foot = append([]string{"", "  " + styleAccent.Render("↵ ") + styleMuted.Render(ansi.Truncate("enter asks this · "+how, w-5, "…"))}, foot[1:]...)
	}

	room := func() int { return max(m.height-6, 5) - above - len(head) - len(foot) }
	// Short of room, the model and the counts go before the options do.
	for room() < len(list) && len(head) > 2 {
		head = head[:len(head)-1]
	}
	if room() < len(list) {
		list = list[:max(room(), 0)]
		// A heading with nothing under it is cut with what it headed.
		for len(list) > 0 {
			if _, ok := at[len(list)-1]; ok {
				break
			}
			list = list[:len(list)-1]
		}
	}
	b.optLines = map[int]int{}
	for k, v := range at {
		if k < len(list) {
			b.optLines[above+len(head)+k] = v
		}
	}
	out := append(head, list...)
	return append(out, foot...)
}

// optionLine is one option: the request on the left, what it does on the
// right, the whole line lit when it is the one enter would take.
func (b *askBar) optionLine(o askOption, lit bool, w int) string {
	mark := "  "
	if lit {
		mark = "▸ "
	}
	note := o.note
	room := w - 4 - ansi.StringWidth(mark)
	if ansi.StringWidth(note)+12 > room {
		note = ""
	}
	text := ansi.Truncate(o.text, max(room-ansi.StringWidth(note)-2, 1), "…")
	gap := max(room-ansi.StringWidth(text)-ansi.StringWidth(note), 1)
	if lit {
		return styleSel.Render(fit("  "+mark+text+strings.Repeat(" ", gap)+note, w))
	}
	return "  " + mark + text + strings.Repeat(" ", gap) + styleMuted.Render(note)
}

func (b *askBar) mouse(m *Model, msg tea.MouseMsg, bx box) tea.Cmd {
	if !bx.contains(msg.X, msg.Y) && msg.Action == tea.MouseActionPress && b.phase != askRunning && b.phase != askThinking {
		m.overlay = nil
		return nil
	}
	// A click on an option is enter on it.
	if b.phase == askInput && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if i, ok := b.optLines[msg.Y-bx.y-1]; ok {
			if opts := b.shown(); i < len(opts) {
				return b.choose(m, opts[i])
			}
		}
	}
	return nil
}

func (b *askBar) dimBackground() bool { return true }

// thinking reports whether the spinner must keep ticking.
func (b *askBar) thinking() bool { return b.phase == askThinking || b.phase == askRunning }
