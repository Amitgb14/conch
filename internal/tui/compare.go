package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// Comparing the attempts at one task: what each changed, what its agent is
// doing and what it spent, so one can be kept and the rest thrown away
// through the same harvest steps as any other branch.

// attemptRow is one attempt in the compare view.
type attemptRow struct {
	branch string // the full branch name
	name   string // its last component: the agent that tried it
	// changes is what the branch holds, once read.
	changes *proto.Changes
	err     string
}

// compareMsg carries one attempt's changes.
type compareMsg struct {
	machine, projectID, branch string
	data                       proto.Changes
	err                        error
}

// attemptsIn lists the branches attempting task base, newest commit first,
// as the project last reported them.
func attemptsIn(proj *proto.ProjectInfo, base string) []attemptRow {
	if proj == nil || base == "" {
		return nil
	}
	var out []attemptRow
	for _, b := range proj.Branches {
		if gitx.AttemptBase(b.Name) != base {
			continue
		}
		out = append(out, attemptRow{branch: b.Name, name: strings.TrimPrefix(b.Name, base+"/")})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// compareView lists the attempts at one task.
type compareView struct {
	machine, projectID string
	base               string // the task's branch name, without the attempt
	rows               []attemptRow
	sel                int
	err                string
	// command is what t last ran in every attempt, and runs where it ran.
	command string
	runs    map[string]testRun
}

// openCompare shows the attempts at the task a branch belongs to.
func (m *Model) openCompare(t harvestTarget) tea.Cmd {
	proj := m.project(t.machine, t.projectID)
	if proj == nil {
		m.setFlash("select a branch of a git project", true)
		return nil
	}
	base := gitx.AttemptBase(t.branch)
	rows := attemptsIn(proj, base)
	if len(rows) < 2 {
		m.setFlash(t.branch+" is not one of several attempts at a task; start some with Attempts in the task dialog", true)
		return nil
	}
	v := &compareView{machine: t.machine, projectID: t.projectID, base: base, rows: rows}
	for i, r := range rows {
		if r.branch == t.branch {
			v.sel = i
		}
	}
	m.overlay = v
	return v.reload(m)
}

// reload reads what each attempt changed.
func (v *compareView) reload(m *Model) tea.Cmd {
	c := m.clientOf(v.machine)
	var cmds []tea.Cmd
	for _, r := range v.rows {
		mid, pid, branch := v.machine, v.projectID, r.branch
		cmds = append(cmds, func() tea.Msg {
			msg := compareMsg{machine: mid, projectID: pid, branch: branch}
			if c == nil {
				msg.err = errString(m.offlineText(mid))
				return msg
			}
			var out proto.Changes
			msg.err = callCtx(c, proto.MethodProjectChanges, proto.ChangesParams{ProjectID: pid, Branch: branch}, &out)
			msg.data = out
			return msg
		})
	}
	return tea.Batch(cmds...)
}

func (v *compareView) receive(msg compareMsg) {
	if msg.machine != v.machine || msg.projectID != v.projectID {
		return
	}
	for i := range v.rows {
		if v.rows[i].branch != msg.branch {
			continue
		}
		if msg.err != nil {
			v.rows[i].err = msg.err.Error()
			return
		}
		data := msg.data
		v.rows[i].changes, v.rows[i].err = &data, ""
	}
}

// agentOf is the pane working on an attempt, if any.
func (v *compareView) agentOf(m Model, branch string) *proto.PaneInfo {
	mach := m.machine(v.machine)
	if mach == nil {
		return nil
	}
	for i, p := range mach.panes {
		if p.ProjectID == v.projectID && p.Branch == branch && p.Agent != nil {
			return &mach.panes[i]
		}
	}
	return nil
}

func (v *compareView) target(i int) harvestTarget {
	return harvestTarget{machine: v.machine, projectID: v.projectID, branch: v.rows[i].branch}
}

func (v *compareView) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch k.String() {
	case "esc", "q":
		m.overlay = nil
		return true, nil
	case "up", "k":
		v.sel = max(v.sel-1, 0)
	case "down", "j":
		v.sel = min(v.sel+1, len(v.rows)-1)
	case "R":
		return false, v.reload(m)
	case "enter":
		// Look at one attempt's diff: the changes view, as everywhere else.
		t := v.target(v.sel)
		m.overlay = nil
		m.cursor = branchNodeID(t.machine, t.projectID, t.branch)
		return true, m.rebuild()
	case "M":
		m.overlay = nil
		return true, m.openMerge(v.target(v.sel))
	case "D":
		m.overlay = nil
		return true, m.discardBranch(v.target(v.sel))
	case "x":
		return false, v.discardOthers(m)
	case "t":
		return false, v.openTestCommand(m)
	case "o":
		// The terminal a run went to, to read its output.
		if run, ok := v.runs[v.rows[v.sel].branch]; ok && run.pane != "" {
			if p := m.pane(v.machine, run.pane); p != nil {
				m.overlay = nil
				m.revealPane(v.machine, *p)
				m.focus = focusMain
				return true, m.rebuild()
			}
		}
		v.err = "nothing has run here yet — t runs a command in every attempt"
	}
	return false, nil
}

// discardOthers keeps the selected attempt and throws the rest away, after
// the usual confirmation for each one that would lose work.
func (v *compareView) discardOthers(m *Model) tea.Cmd {
	keep := v.rows[v.sel]
	var others []harvestTarget
	for i, r := range v.rows {
		if r.branch != keep.branch && v.agentOf(*m, r.branch) == nil {
			others = append(others, v.target(i))
		}
	}
	if len(others) == 0 {
		v.err = "nothing to discard: the other attempts still have agents running"
		return nil
	}
	m.overlay = nil
	// One plan at a time, so each says what it loses; the rest follow.
	return m.discardBranch(others[0])
}

func (v *compareView) render(m Model) box {
	w := m.dialogWidth()
	lines := []string{"", " " + styleBold.Render(ansi.Truncate("Attempts at "+v.base, w-2, "…"))}
	for i, r := range v.rows {
		state, cost := "", ""
		if p := v.agentOf(m, r.branch); p != nil {
			_, label, style := m.paneGlyph(*p)
			state = style.Render(label)
			cost = m.costChip(paneUsage(*p))
		}
		what := styleMuted.Render("reading…")
		switch {
		case r.err != "":
			what = styleErr.Render(ansi.Truncate(r.err, max(w/2, 20), "…"))
		case r.changes != nil:
			what = compareSummary(*r.changes)
		}
		if run, ok := v.runs[r.branch]; ok {
			if text, style := run.result(m, v.machine); text != "" {
				what += styleMuted.Render(" · ") + style(text)
			}
		}
		left := "   " + r.name
		if i == v.sel {
			left = " ▸ " + r.name
		}
		line := spread(left+"  "+what, joinRight(cost, state)+" ", w)
		if i == v.sel {
			line = styleSel.Render(spread(ansi.Strip(left+"  "+what), ansi.Strip(joinRight(cost, state))+" ", w))
		}
		lines = append(lines, line)
	}
	lines = append(lines, "")
	if v.err != "" {
		lines = append(lines, " "+styleErr.Render(ansi.Truncate(v.err, w-2, "…")), "")
	}
	hints := "enter its changes · t run a command in each · M merge into the base · D discard it · x keep it, discard the rest · R reload · esc close"
	if v.command != "" {
		hints = "enter its changes · t rerun " + ansi.Truncate(v.command, 24, "…") + " · o its terminal · M merge · D discard · x keep it, discard the rest · R reload · esc close"
	}
	for _, l := range wrap(hints, w-2) {
		lines = append(lines, " "+styleMuted.Render(l))
	}
	b := box{lines: frameLines(" Compare attempts ", lines, w, colorAccent)}
	b.x = max((m.width-b.width())/2, 0)
	b.y = max((m.height-len(b.lines))/4, 0)
	return b
}

// compareSummary is what one attempt changed, in a line.
func compareSummary(ch proto.Changes) string {
	added, deleted := 0, 0
	for _, f := range ch.Files {
		added += f.Added
		deleted += f.Deleted
	}
	parts := []string{styleMuted.Render(counted(len(ch.Files), "file"))}
	if stat := diffStat(added, deleted); stat != "" {
		parts = append(parts, stat)
	}
	if n := len(ch.Commits); n > 0 {
		parts = append(parts, styleMuted.Render(counted(n, "commit")))
	}
	if len(ch.Files) == 0 && len(ch.Commits) == 0 {
		return styleWarn.Render("nothing yet")
	}
	return strings.Join(parts, styleMuted.Render(" · "))
}

func (v *compareView) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if !b.contains(msg.X, msg.Y) {
		m.overlay = nil
		return nil
	}
	// Lines: border, blank, heading, then one per attempt.
	if i := msg.Y - b.y - 3; i >= 0 && i < len(v.rows) {
		if i == v.sel { // a second click opens it, as in the tree
			return func() tea.Msg { return tea.KeyMsg{Type: tea.KeyEnter} }
		}
		v.sel = i
	}
	return nil
}

// compareMenuItem offers the compare view for a branch that is one of
// several attempts at a task.
func compareMenuItem(m Model, r row) []menuItem {
	proj := m.project(r.machine, r.projectID)
	base := gitx.AttemptBase(r.branch)
	if proj == nil || len(attemptsIn(proj, base)) < 2 {
		return nil
	}
	t := harvestTarget{machine: r.machine, projectID: r.projectID, branch: r.branch}
	return []menuItem{{"A", fmt.Sprintf("Compare the %s at %s…", counted(len(attemptsIn(proj, base)), "attempt"), base),
		func(m *Model) tea.Cmd { return m.openCompare(t) }}}
}

// ---- running a command in every attempt ----

// testRun is one attempt's run of the compare view's command.
type testRun struct {
	pane string // the pane it runs in
	err  string // why it couldn't start
}

// result is what the run came to, from the pane's own state.
func (t testRun) result(m Model, mid string) (text string, style func(...string) string) {
	switch {
	case t.err != "":
		return t.err, styleErr.Render
	case t.pane == "":
		return "", styleMuted.Render
	}
	p := m.pane(mid, t.pane)
	switch {
	case p == nil:
		return "closed", styleMuted.Render
	case p.State != proto.PaneExited:
		return "running…", styleWork.Render
	case p.ExitCode == 0:
		return "passed", styleOK.Render
	}
	return fmt.Sprintf("failed (exit %d)", p.ExitCode), styleErr.Render
}

// openTestCommand asks what to run in every attempt's worktree.
func (v *compareView) openTestCommand(m *Model) tea.Cmd {
	back := v
	d := newDialog(*m, " Run in every attempt ",
		[]string{"Runs the command in each attempt's worktree, in its own terminal, and shows what it came to. " +
			"The terminals stay open so you can read the output."},
		[]string{"Command"}, []string{v.command})
	d.fields[0].in.Placeholder = "go test ./... · npm test · make check"
	d.submit = func(m *Model, values []string) tea.Cmd {
		cmd := strings.TrimSpace(values[0])
		m.overlay = back
		if cmd == "" {
			back.err = "nothing to run"
			return nil
		}
		back.command, back.err = cmd, ""
		return back.runCommand(m, cmd)
	}
	m.overlay = d
	return d.focusCmd()
}

// runCommand starts the command in each attempt's worktree.
func (v *compareView) runCommand(m *Model, cmd string) tea.Cmd {
	proj := m.project(v.machine, v.projectID)
	if proj == nil {
		v.err = "the project is gone"
		return nil
	}
	if v.runs == nil {
		v.runs = map[string]testRun{}
	}
	c := m.clientOf(v.machine)
	if c == nil {
		v.err = m.offlineText(v.machine)
		return nil
	}
	mid := v.machine
	var cmds []tea.Cmd
	for _, r := range v.rows {
		dir := ""
		for _, wt := range proj.Worktrees {
			if wt.Branch == r.branch {
				dir = wt.Path
			}
		}
		branch := r.branch
		if dir == "" {
			v.runs[branch] = testRun{err: "no worktree"}
			continue
		}
		// A previous run's terminal is left alone; this one gets its own.
		v.runs[branch] = testRun{}
		params := proto.PaneCreateParams{Name: "test · " + r.name, Command: []string{"/bin/sh", "-lc", cmd},
			Cwd: dir, Cols: 120, Rows: 40}
		cmds = append(cmds, func() tea.Msg {
			var info proto.PaneInfo
			if err := callCtx(c, proto.MethodPaneCreate, params, &info); err != nil {
				return testStartedMsg{machine: mid, branch: branch, err: err.Error()}
			}
			return testStartedMsg{machine: mid, branch: branch, info: info}
		})
	}
	return tea.Batch(cmds...)
}

// testStartedMsg carries the terminal one attempt's run started in.
type testStartedMsg struct {
	machine, branch string
	info            proto.PaneInfo
	err             string
}

// receiveTestStarted records the run and lists its terminal in the tree.
func (m *Model) receiveTestStarted(msg testStartedMsg) tea.Cmd {
	v, ok := m.overlay.(*compareView)
	if ok && v.runs != nil {
		if msg.err != "" {
			v.runs[msg.branch] = testRun{err: msg.err}
		} else {
			v.runs[msg.branch] = testRun{pane: msg.info.ID}
		}
	}
	if msg.err != "" {
		return nil
	}
	if mach := m.machine(msg.machine); mach != nil && mach.paneIndex(msg.info.ID) < 0 {
		mach.panes = append(mach.panes, msg.info)
	}
	return m.rebuild()
}
