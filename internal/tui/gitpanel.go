package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// The git panel: a command window in the middle of the right-hand panel,
// opened with b or $ on a branch in the tree, $ in its changes, or Git
// panel from its row menu. A click on a branch shows its changes and leaves
// the window closed: a window over the right-hand side is more than one
// click on a row should do. It is a prompt that runs
// any git command in the branch's worktree, or gives an agent working there
// its next message, over what the last commands printed. The git a
// developer reaches for every day waits behind tab, so the window itself
// stays a prompt and its output.

const (
	// gitPanelKeep is how many lines of output the panel keeps.
	gitPanelKeep = 500
	// gitPanelOutputMax is the most lines of output shown at once.
	gitPanelOutputMax = 16
	gitPanelOutputMin = 4
	gitPanelWidth     = 90
)

// gitAction is one of the panel's actions: git commands to run in the
// worktree, an instruction for the agent, or something conch already does
// (commit, push) run as it is elsewhere.
type gitAction struct {
	key, label string
	cmds       [][]string
	prompt     string
	run        func(m *Model, g *gitPanel) tea.Cmd
	// confirm is asked before the commands run; danger makes it a
	// question enter doesn't answer, for what can't be undone.
	confirm string
	danger  bool
}

type gitHitKind int

const (
	hitAction gitHitKind = iota
	hitTarget
	hitPrompt
)

// gitHit is a clickable span of the last render, in content coordinates.
type gitHit struct {
	kind       gitHitKind
	line       int
	x0, x1     int
	i          int
	actionsKey string
}

type gitPanel struct {
	t  harvestTarget
	in textinput.Model
	// agent is the pane the prompt goes to, or "" for git.
	agent string
	// onActions shows the actions in place of the output, with the
	// keyboard on them; cols is how many a row of them held last time.
	onActions bool
	sel, cols int
	output    []string
	scroll    int // first output line shown
	// running names the command in flight, so a second waits.
	running    string
	inProgress string
	history    []string
	hist       int // len(history) when not browsing it
	// noGit is why git commands can't run here (an older server).
	noGit string
	hits  []gitHit
}

type (
	// gitPanelMsg is a branch.git call's answer, for the panel that asked:
	// one opened since for the same branch has asked for its own. quiet
	// drops it when that panel is gone, as the status read on opening is.
	gitPanelMsg struct {
		panel *gitPanel
		t     harvestTarget
		label string
		quiet bool
		res   proto.BranchGitResult
		err   error
	}
	// gitPromptMsg says whether an instruction reached its agent.
	gitPromptMsg struct {
		panel *gitPanel
		t     harvestTarget
		to    string
		text  string
		err   error
	}
)

// openGitPanel opens the panel for t, empty: the files and how the branch
// stands are on its changes page behind it. The server is only asked
// whether a rebase or merge is left stopped there, which prints nothing.
func (m *Model) openGitPanel(t harvestTarget) tea.Cmd {
	proj := m.project(t.machine, t.projectID)
	switch {
	case proj == nil || !proj.Git || t.branch == "":
		m.setFlash("select a branch of a git project", true)
		return nil
	case m.clientOf(t.machine) == nil:
		m.setFlash(m.offlineText(t.machine), true)
		return nil
	}
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = gitPlaceholder
	in.Focus()
	g := &gitPanel{t: t, in: in}
	if !m.hasCapability(t.machine, proto.CapBranchGit) {
		g.noGit = "The server on " + m.machineLabel(t.machine) + " predates running git from here; reload it. Push, commit and the agent still work."
		g.add(g.noGit)
	}
	m.overlay = g
	cmds := []tea.Cmd{textinput.Blink}
	if g.noGit == "" {
		cmds = append(cmds, g.call(m, "state", nil, true))
	}
	return tea.Batch(cmds...)
}

// branchAgents are the agents running in the branch's checkout.
func (m Model) branchAgents(t harvestTarget) []proto.PaneInfo {
	mach := m.machine(t.machine)
	if mach == nil {
		return nil
	}
	var out []proto.PaneInfo
	for _, p := range mach.panes {
		if p.ProjectID == t.projectID && p.Branch == t.branch && p.Agent != nil && p.State == proto.PaneRunning {
			out = append(out, p)
		}
	}
	return out
}

// agentPane is the pane the prompt is aimed at, if it still runs an agent
// on this branch; otherwise the prompt is git's again.
func (g *gitPanel) agentPane(m Model) (proto.PaneInfo, bool) {
	if g.agent == "" {
		return proto.PaneInfo{}, false
	}
	for _, p := range m.branchAgents(g.t) {
		if p.ID == g.agent {
			return p, true
		}
	}
	g.agent = ""
	return proto.PaneInfo{}, false
}

// actions are the buttons for where the prompt is aimed.
func (g *gitPanel) actions(m Model) []gitAction {
	proj := m.project(g.t.machine, g.t.projectID)
	base := ""
	if proj != nil {
		base = proj.Base
	}
	onBase := base == "" || g.t.branch == base
	if p, ok := g.agentPane(m); ok {
		name := p.DisplayName()
		var out []gitAction
		if g.inProgress != "" {
			out = append(out, gitAction{key: "x", label: "Resolve the " + g.inProgress, prompt: resolvePrompt(g.inProgress)})
		}
		out = append(out,
			gitAction{key: "r", label: "Review changes", prompt: "Review the uncommitted changes in this worktree and point out bugs or anything missing. Don't change files yet."},
			gitAction{key: "c", label: "Commit its work", prompt: "Commit your work in this worktree with a clear commit message."},
			gitAction{key: "t", label: "Write tests", prompt: "Add tests for the changes on this branch, and run them."},
		)
		if !onBase {
			out = append(out, gitAction{key: "b", label: "Rebase on " + base, prompt: "Rebase this branch onto " + base + " and resolve any conflicts, keeping both sides' intent."})
		}
		return append(out, gitAction{key: "o", label: "Open " + name, run: func(m *Model, g *gitPanel) tea.Cmd {
			m.overlay = nil
			return m.openPaneRow(g.t.machine, p.ID)
		}})
	}

	var out []gitAction
	if op := g.inProgress; op != "" {
		out = append(out,
			gitAction{key: "c", label: "Continue " + op, cmds: [][]string{{op, "--continue"}}},
			gitAction{key: "a", label: "Abort " + op, cmds: [][]string{{op, "--abort"}},
				confirm: "Abort the " + op + " on " + g.t.branch + "? The branch goes back to how it was before it started."},
		)
		if agents := m.branchAgents(g.t); len(agents) > 0 {
			p := agents[0]
			out = append(out, gitAction{key: "x", label: "Ask " + p.DisplayName() + " to resolve", run: func(m *Model, g *gitPanel) tea.Cmd {
				return g.promptAgent(m, p, resolvePrompt(op))
			}})
		}
	}
	t := g.t
	out = append(out,
		gitAction{key: "s", label: "Status", cmds: [][]string{{"status", "--short", "--branch"}}},
		gitAction{key: "l", label: "Log", cmds: [][]string{{"log", "--oneline", "--graph", "--decorate", "-n", "30"}}},
		gitAction{key: "d", label: "Diff stat", cmds: [][]string{{"diff", "--stat", "HEAD"}}},
		gitAction{key: "f", label: "Fetch", cmds: [][]string{{"fetch", "--prune"}}},
		gitAction{key: "u", label: "Pull", cmds: [][]string{{"pull", "--rebase", "--autostash"}}},
		gitAction{key: "p", label: "Push", run: func(m *Model, g *gitPanel) tea.Cmd {
			g.add("pushing " + t.branch + "…")
			return m.pushBranch(t)
		}},
		gitAction{key: "C", label: "Commit…", run: func(m *Model, g *gitPanel) tea.Cmd {
			cmd := m.openCommit(t, commitSelection{})
			if d, ok := m.overlay.(*dialog); ok {
				d.back = g // back here once it is committed, or cancelled
			} else if m.overlay == nil {
				m.overlay = g // it said why not in the status bar
			}
			return cmd
		}},
		gitAction{key: "A", label: "Amend", cmds: [][]string{{"add", "--all"}, {"commit", "--amend", "--no-edit"}},
			confirm: "Put every uncommitted change into the last commit on " + t.branch + "? If it was pushed, the next push has to be forced."},
		gitAction{key: "z", label: "Stash", cmds: [][]string{{"stash", "push", "--include-untracked"}}},
		gitAction{key: "Z", label: "Pop stash", cmds: [][]string{{"stash", "pop"}}},
		gitAction{key: "S", label: "Stashes", cmds: [][]string{{"stash", "list"}}},
		gitAction{key: "B", label: "Branches", cmds: [][]string{{"branch", "-vv"}}},
	)
	if !onBase {
		out = append(out,
			gitAction{key: "r", label: "Rebase on " + base, cmds: [][]string{{"rebase", base}}},
			gitAction{key: "m", label: "Merge " + base + " in", cmds: [][]string{{"merge", "--no-edit", base}}},
		)
	}
	return append(out,
		gitAction{key: "U", label: "Undo last commit", cmds: [][]string{{"reset", "--soft", "HEAD~1"}},
			confirm: "Undo the last commit on " + t.branch + "? Its changes stay in the worktree, staged."},
		gitAction{key: "X", label: "Discard changes…", cmds: [][]string{{"reset", "--hard", "HEAD"}, {"clean", "-fd"}},
			confirm: "Throw away every uncommitted change on " + t.branch + ", untracked files included? This can't be undone.", danger: true},
	)
}

// resolvePrompt asks an agent to finish what git stopped on.
func resolvePrompt(op string) string {
	return "A git " + op + " stopped on conflicts in this worktree. Resolve them keeping both sides' intent, " +
		"git add the files, then run git " + op + " --continue. Tell me if a conflict needs my decision."
}

// run starts action i.
func (g *gitPanel) run(m *Model, i int) tea.Cmd {
	acts := g.actions(*m)
	if i < 0 || i >= len(acts) {
		return nil
	}
	a := acts[i]
	switch {
	case a.run != nil:
		return a.run(m, g)
	case a.prompt != "":
		p, ok := g.agentPane(*m)
		if !ok {
			return nil
		}
		return g.promptAgent(m, p, a.prompt)
	case a.confirm != "":
		d := newConfirm(a.confirm, func(m *Model) tea.Cmd {
			m.overlay = g
			return g.runGit(m, a.label, a.cmds)
		})
		d.yesOnly = a.danger
		d.back = g
		m.overlay = d
		return nil
	}
	return g.runGit(m, a.label, a.cmds)
}

const gitPlaceholder = "a git command: status, log -5, commit -m '…'"

// runGit sends cmds to the worktree. One runs at a time: a second press
// while a pull is still going would only race it.
func (g *gitPanel) runGit(m *Model, label string, cmds [][]string) tea.Cmd {
	return g.call(m, label, cmds, false)
}

func (g *gitPanel) call(m *Model, label string, cmds [][]string, quiet bool) tea.Cmd {
	if g.noGit != "" {
		g.add(g.noGit)
		return nil
	}
	// A quiet call only reads the worktree's state: it neither waits for
	// the person's command nor makes theirs wait.
	if g.running != "" && !quiet {
		g.add("still running " + g.running + "…")
		return nil
	}
	c := m.clientOf(g.t.machine)
	if c == nil {
		g.add(m.offlineText(g.t.machine))
		return nil
	}
	if !quiet {
		g.running = label
	}
	t := g.t
	params := proto.BranchGitParams{ProjectID: t.projectID, Branch: t.branch, Commands: cmds}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), harvestTimeout)
		defer cancel()
		var res proto.BranchGitResult
		err := c.Call(ctx, proto.MethodBranchGit, params, &res)
		return gitPanelMsg{panel: g, t: t, label: label, quiet: quiet, res: res, err: err}
	}
}

// promptAgent gives the agent in p text as its next message.
func (g *gitPanel) promptAgent(m *Model, p proto.PaneInfo, text string) tea.Cmd {
	c := m.clientOf(g.t.machine)
	if c == nil {
		g.add(m.offlineText(g.t.machine))
		return nil
	}
	name := p.DisplayName()
	g.add("→ " + name + ": " + text)
	t := g.t
	switch {
	case m.hasCapability(t.machine, proto.CapAgentPrompt):
		return func() tea.Msg {
			var res proto.AgentPromptResult
			err := callCtx(c, proto.MethodAgentPrompt, proto.AgentPromptParams{ID: p.ID, Text: text}, &res)
			return gitPromptMsg{panel: g, t: t, to: name, text: text, err: err}
		}
	case m.hasCapability(t.machine, "agent.broadcast.v1"):
		// An older server: the same message, without its refusal while
		// the agent waits on a question.
		return func() tea.Msg {
			var res proto.AgentBroadcastResult
			err := callCtx(c, proto.MethodAgentBroadcast, proto.AgentBroadcastParams{IDs: []string{p.ID}, Text: text}, &res)
			return gitPromptMsg{panel: g, t: t, to: name, text: text, err: err}
		}
	}
	g.add("the server on " + m.machineLabel(t.machine) + " predates sending agents messages; reload it")
	return nil
}

// submit runs what was typed: git's arguments, or a message for the agent.
func (g *gitPanel) submit(m *Model) tea.Cmd {
	text := strings.TrimSpace(g.in.Value())
	if text == "" {
		return nil
	}
	if n := len(g.history); n == 0 || g.history[n-1] != text {
		g.history = append(g.history, text)
	}
	g.hist = len(g.history)
	g.in.SetValue("")
	if p, ok := g.agentPane(*m); ok {
		return g.promptAgent(m, p, text)
	}
	args, _, ok := shellWords(text)
	if !ok {
		g.add("a quote is left open, or a backslash ends the line")
		return nil
	}
	if len(args) > 0 && args[0] == "git" {
		args = args[1:]
	}
	if len(args) == 0 {
		g.add("type a git command after git: status, log -5, …")
		return nil
	}
	return g.runGit(m, args[0], [][]string{args})
}

// add appends text to the output and shows it from its first line, so the
// start of a long log is what is seen.
func (g *gitPanel) add(text string) {
	first := len(g.output)
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		l = strings.TrimRight(l, "\r")
		if i := strings.LastIndex(l, "\r"); i >= 0 {
			l = l[i+1:] // progress redrawn in place: its last state
		}
		g.output = append(g.output, strings.ReplaceAll(ansi.Strip(l), "\t", "    "))
	}
	if drop := len(g.output) - gitPanelKeep; drop > 0 {
		g.output = g.output[drop:]
		first = max(first-drop, 0)
	}
	g.scroll = first
}

// receiveGitPanel shows a branch.git answer in the panel that asked, or in
// the status bar once it was closed, and refreshes the branch's views.
func (m *Model) receiveGitPanel(msg gitPanelMsg) tea.Cmd {
	g := msg.panel
	open := g != nil && m.gitPanelOpen(g)
	if g != nil && !msg.quiet {
		g.running = ""
	}
	switch {
	case !open && msg.quiet:
		return nil
	case msg.quiet:
		if msg.err == nil && g.running == "" { // a command since knows better
			g.inProgress = msg.res.InProgress
		}
		return nil
	case msg.err != nil && open:
		g.add(errText(msg.err))
	case msg.err != nil:
		m.setFlash(msg.label+": "+errText(msg.err), true)
	case open:
		g.inProgress = msg.res.InProgress
		out := msg.res.Output
		if msg.res.Truncated {
			out += "… (the rest was cut off)"
		}
		if strings.TrimSpace(out) != "" {
			g.add(out)
		}
		if msg.res.ExitCode != 0 {
			g.add(fmt.Sprintf("exit %d", msg.res.ExitCode))
		}
	case msg.res.ExitCode != 0:
		m.setFlash(fmt.Sprintf("git %s on %s failed (exit %d)", msg.label, msg.t.branch, msg.res.ExitCode), true)
	default:
		m.setFlash("git "+msg.label+" done on "+msg.t.branch, false)
	}
	return m.pollBranch(msg.t)
}

func (m *Model) receiveGitPrompt(msg gitPromptMsg) {
	text := "sent to " + msg.to
	if msg.err != nil {
		text = "not sent to " + msg.to + ": " + errText(msg.err)
	}
	if g := msg.panel; g != nil && m.gitPanelOpen(g) {
		g.add(text)
		return
	}
	m.setFlash(text, msg.err != nil)
}

// gitPanelOpen says whether g is on screen, or waiting behind a dialog it
// opened (a confirm, the commit message).
func (m Model) gitPanelOpen(g *gitPanel) bool {
	if o, ok := m.overlay.(*gitPanel); ok {
		return o == g
	}
	d, ok := m.overlay.(*dialog)
	return ok && d.back == overlay(g)
}

// gitPanelFor is the open panel for t, if there is one.
func (m Model) gitPanelFor(t harvestTarget) (*gitPanel, bool) {
	g, ok := m.overlay.(*gitPanel)
	if !ok {
		if d, isDialog := m.overlay.(*dialog); isDialog {
			g, ok = d.back.(*gitPanel)
		}
	}
	return g, ok && g.t == t
}

// pollBranch re-reads the changes views showing t's branch.
func (m *Model) pollBranch(t harvestTarget) tea.Cmd {
	var cmds []tea.Cmd
	for _, tab := range m.openTabs() {
		for _, l := range tab.root.leaves() {
			cv := l.changes
			if cv != nil && cv.machine == t.machine && cv.projectID == t.projectID && cv.branch == t.branch {
				cmds = append(cmds, cv.poll(m))
			}
		}
	}
	return tea.Batch(cmds...)
}

func (g *gitPanel) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, isKey := msg.(tea.KeyMsg)
	if !isKey {
		var cmd tea.Cmd
		g.in, cmd = g.in.Update(msg) // the cursor's blink
		return false, cmd
	}
	switch k.String() {
	case "tab", "shift+tab":
		return false, g.showActions(!g.onActions)
	case "ctrl+t":
		g.nextTarget(*m)
		return false, nil
	case "pgup":
		g.scroll = max(g.scroll-gitPanelOutputMax/2, 0)
		return false, nil
	case "pgdown":
		g.scroll = min(g.scroll+gitPanelOutputMax/2, max(len(g.output)-1, 0))
		return false, nil
	}
	if g.onActions {
		acts := g.actions(*m)
		cols := max(g.cols, 1)
		switch k.String() {
		case "esc":
			return false, g.showActions(false) // back to the prompt; esc there closes
		case "left", "h":
			g.sel = (g.sel + len(acts) - 1) % len(acts)
		case "right", "l":
			g.sel = (g.sel + 1) % len(acts)
		case "up", "k":
			if g.sel >= cols {
				g.sel -= cols
			}
		case "down", "j":
			if g.sel+cols < len(acts) {
				g.sel += cols
			}
		case "enter", " ":
			i := g.sel
			g.showActions(false)
			return false, g.run(m, i)
		default:
			for i, a := range acts {
				if a.key == k.String() {
					g.sel = i
					g.showActions(false)
					return false, g.run(m, i)
				}
			}
		}
		return false, nil
	}
	switch k.String() {
	case "esc":
		m.overlay = nil
		return true, nil
	case "enter":
		return false, g.submit(m)
	case "up":
		if g.hist > 0 {
			g.hist--
			g.in.SetValue(g.history[g.hist])
			g.in.CursorEnd()
		}
		return false, nil
	case "down":
		if g.hist < len(g.history) {
			g.hist++
			v := ""
			if g.hist < len(g.history) {
				v = g.history[g.hist]
			}
			g.in.SetValue(v)
			g.in.CursorEnd()
		}
		return false, nil
	}
	var cmd tea.Cmd
	g.in, cmd = g.in.Update(msg)
	return false, cmd
}

// showActions puts the actions in place of the output, or the prompt back.
func (g *gitPanel) showActions(on bool) tea.Cmd {
	g.onActions = on
	if on {
		g.in.Blur()
		return nil
	}
	return g.in.Focus()
}

// nextTarget aims the prompt at the next agent on the branch, then at git
// again.
func (g *gitPanel) nextTarget(m Model) {
	g.setTarget(g.nextTargetID(m))
}

func (g *gitPanel) nextTargetID(m Model) string {
	ids := []string{""}
	for _, p := range m.branchAgents(g.t) {
		ids = append(ids, p.ID)
	}
	for i, id := range ids {
		if id == g.agent {
			return ids[(i+1)%len(ids)]
		}
	}
	return ""
}

func (g *gitPanel) setTarget(id string) {
	if g.agent == id {
		return
	}
	g.agent, g.sel = id, 0
	g.in.Placeholder = gitPlaceholder
	if id != "" {
		g.in.Placeholder = "a message for the agent"
	}
}

// area is where the panel sits: the right-hand panel, so the tree stays in
// view and a click there still lands. A screen too narrow for that gets
// the whole width.
func (g *gitPanel) area(m Model) rect {
	a := m.mainRect()
	if a.w < 40 {
		return rect{0, 0, m.width, max(m.height-statusHeight, 0)}
	}
	return a
}

// width is the panel's content width in its area: most of a wide one, all
// of a narrow one.
func (g *gitPanel) width(m Model) int {
	a := g.area(m)
	w := min(gitPanelWidth, a.w-8)
	if w < 40 {
		w = a.w - 2
	}
	return max(w, 1)
}

func (g *gitPanel) render(m Model) box {
	w := g.width(m)
	g.hits = g.hits[:0]
	// Only what the commands did: where the branch stands is on the
	// branch's own page already. A rebase or merge left stopped is the
	// one thing to say above the output.
	var lines []string
	if op := g.inProgress; op != "" {
		lines = append(lines, " "+styleWarn.Render(op+" stopped")+styleMuted.Render(" · tab to continue, abort or hand it to the agent"), "")
	}

	// The output, or the actions in its place: a few lines to begin with,
	// growing with what is printed to as much as the screen has room for.
	room := max(g.area(m).h-len(lines)-5, 0)
	outH := clamp(len(g.output), gitPanelOutputMin, gitPanelOutputMax)
	outH = clamp(outH, 0, room)
	var body []string
	if g.onActions {
		body = g.actionLines(m, w, len(lines), max(room, 1))
	} else {
		body = g.outputLines(outH)
	}
	lines = append(lines, body...)
	for i := len(body); i < outH; i++ {
		lines = append(lines, "")
	}
	lines = append(lines, "")

	// The prompt: whom it goes to, then what is typed.
	label, nextName := "git", ""
	if p, ok := g.agentPane(m); ok {
		label = p.DisplayName()
	}
	if id := g.nextTargetID(m); id != g.agent {
		nextName = "git"
		if p := m.pane(g.t.machine, id); p != nil {
			nextName = ansi.Truncate(p.DisplayName(), 16, "…") // the hint keeps room for esc
		}
	}
	label = ansi.Truncate(label, 16, "…") + " ❯ "
	lw := ansi.StringWidth(label)
	g.in.Width = max(w-lw-3, 1)
	labelStyle := styleAccent
	if g.onActions {
		labelStyle = styleMuted
	}
	g.hits = append(g.hits,
		gitHit{kind: hitTarget, line: len(lines), x0: 0, x1: 1 + lw},
		gitHit{kind: hitPrompt, line: len(lines), x0: 1 + lw, x1: w})
	lines = append(lines, " "+labelStyle.Render(label)+g.in.View())

	var hint []string
	if g.running != "" {
		hint = append(hint, styleWarn.Render("running "+g.running+"…"))
	}
	switch {
	case g.onActions:
		hint = append(hint, "a key or enter runs", "arrows move", "tab or esc: back")
	default:
		hint = append(hint, "enter run", "tab actions", "↑↓ history")
		if len(g.output) > outH {
			hint = append(hint, "pgup/pgdn")
		}
	}
	if nextName != "" && !g.onActions {
		hint = append(hint, "ctrl+t → "+nextName)
	}
	if !g.onActions {
		hint = append(hint, "esc close")
	}
	lines = append(lines, " "+styleMuted.Render(fitHint(hint, w-1)))

	for i, l := range lines {
		lines[i] = fit(l, w)
	}
	b := box{lines: frameLines(" ⎇ "+g.t.branch+" ", lines, w, colorAccent)}
	a := g.area(m)
	b.x = clamp(a.x+(a.w-b.width())/2, 0, max(m.width-b.width(), 0))
	b.y = clamp(a.y+(a.h-len(b.lines))/2, 0, max(m.height-1-len(b.lines), 0))
	return b
}

// fitHint joins the hints, leaving out the least needed ones — history,
// scrolling, what enter does — until they fit in w, so the last one (how
// to close) stays in sight.
func fitHint(hint []string, w int) string {
	for _, drop := range []string{"↑↓ history", "pgup/pgdn", "enter run", "arrows move"} {
		if ansi.StringWidth(strings.Join(hint, " · ")) <= w {
			break
		}
		for i, h := range hint {
			if h == drop {
				hint = append(hint[:i:i], hint[i+1:]...)
				break
			}
		}
	}
	return strings.Join(hint, " · ")
}

// outputLines are the h lines of output from the scroll position.
func (g *gitPanel) outputLines(h int) []string {
	if h <= 0 {
		return nil
	}
	if len(g.output) == 0 {
		return []string{" " + styleMuted.Render("Any git command runs in this worktree · tab for the everyday ones")}
	}
	g.scroll = clamp(g.scroll, 0, max(len(g.output)-h, 0))
	var out []string
	for _, l := range g.output[g.scroll:min(g.scroll+h, len(g.output))] {
		if strings.HasPrefix(l, "$ git ") || strings.HasPrefix(l, "→ ") {
			l = styleAccent.Render(l)
		}
		out = append(out, " "+l)
	}
	return out
}

// actionLines lays the actions out in columns, recording where each is
// for clicks; top is the line they start on. On a screen too short for
// every row, the rows shown follow the selection.
func (g *gitPanel) actionLines(m Model, w, top, room int) []string {
	acts := g.actions(m)
	cellW := 0
	for _, a := range acts {
		cellW = max(cellW, ansi.StringWidth(a.label)+5)
	}
	g.cols = max((w-1)/cellW, 1)
	g.sel = clamp(g.sel, 0, len(acts)-1)
	var out []string
	for i, a := range acts {
		row, col := i/g.cols, i%g.cols
		if col == 0 {
			out = append(out, " ")
		}
		cell := padRight(" "+a.key+"  "+a.label, cellW-1)
		switch {
		case i == g.sel:
			cell = styleSel.Render(cell)
		case a.danger:
			cell = " " + styleMuted.Render(a.key) + "  " + styleErr.Render(padRight(a.label, cellW-5))
		default:
			cell = " " + styleMuted.Render(a.key) + "  " + padRight(a.label, cellW-5)
		}
		out[row] += cell + " "
	}
	first := clamp(g.sel/g.cols-room+1, 0, max(len(out)-room, 0))
	out = out[first:min(first+room, len(out))]
	for i, a := range acts {
		row, col := i/g.cols-first, i%g.cols
		if row < 0 || row >= len(out) {
			continue
		}
		x0 := 1 + col*cellW
		g.hits = append(g.hits, gitHit{kind: hitAction, line: top + row, x0: x0, x1: x0 + cellW - 1, i: i, actionsKey: a.key})
	}
	return out
}

func (g *gitPanel) mouse(m *Model, msg tea.MouseMsg, b box) tea.Cmd {
	inside := b.contains(msg.X, msg.Y)
	switch {
	case msg.Button == tea.MouseButtonWheelUp && inside:
		g.scroll = max(g.scroll-3, 0)
		return nil
	case msg.Button == tea.MouseButtonWheelDown && inside:
		g.scroll = min(g.scroll+3, max(len(g.output)-1, 0))
		return nil
	case msg.Action != tea.MouseActionPress:
		return nil
	case !inside:
		// A click elsewhere closes the panel and still does what it
		// would have: another branch shows its changes, a pane takes
		// focus. Moving the window to that branch is b on it.
		m.overlay = nil
		return func() tea.Msg { return msg }
	case msg.Button != tea.MouseButtonLeft:
		return nil
	}
	line, col := msg.Y-b.y-1, msg.X-b.x-1
	for _, h := range g.hits {
		if h.line != line || col < h.x0 || col >= h.x1 {
			continue
		}
		switch h.kind {
		case hitTarget:
			g.nextTarget(*m)
		case hitAction:
			g.showActions(false)
			return g.run(m, h.i)
		case hitPrompt:
			return g.showActions(false)
		}
		return nil
	}
	return nil
}
