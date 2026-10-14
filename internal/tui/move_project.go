package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// Moving a whole project: every task worktree goes, one after another,
// into one project on the other machine, each with its agents, as a single
// worktree would (move.go); then the agents in the main checkout continue
// in that project's own checkout there. Where it goes is settled once, for
// all of them, by the lead — a worktreeMove standing for the project. The
// main checkout's uncommitted changes, terminals and saved conversations no
// agent has open stay here.

// projectMove is one project move in progress.
type projectMove struct {
	lead     *worktreeMove   // the project as a whole: where it goes
	parts    []*worktreeMove // one per task worktree, moved in turn
	next     int             // the part to move next
	main     []proto.PaneInfo
	mainPath string
	skipped  []string // worktrees that can't move, and why: said before it starts
	moved    []string // branches that arrived
	started  []proto.PaneInfo
	failed   []string // what stayed here, and why
}

type (
	projectDescribedMsg struct {
		lead  *worktreeMove
		infos []proto.WorktreeMoveInfo // by part
		errs  []error                  // by part
	}
	projectMainDoneMsg struct {
		job     *projectMove
		started []proto.PaneInfo
		failed  []string
	}
)

// openMoveProject starts moving project projectID: which machine?
func (m *Model) openMoveProject(mid, projectID string) tea.Cmd {
	proj := m.project(mid, projectID)
	switch {
	case proj == nil || !proj.Git:
		m.setFlash("select a git project", true)
		return nil
	case m.clientOf(mid) == nil:
		m.setFlash(m.offlineText(mid), true)
		return nil
	case !m.hasCapability(mid, proto.CapWorktreeMove):
		m.setFlash("the server on "+m.machineLabel(mid)+" predates moving worktrees; reload it", true)
		return nil
	}
	if len(projectWorktrees(proj)) == 0 && !m.hasAgentIn(mid, projectID) {
		m.setFlash(proj.Name+" has no task worktree and no agent running in it; nothing would move", true)
		return nil
	}
	name, path := proj.Name, proj.Path
	return m.openMoveTo(name, func(m *Model, dst, dstLabel, provider string) tea.Cmd {
		job := &projectMove{mainPath: path}
		job.lead = &worktreeMove{src: mid, dst: dst, srcLabel: m.machineLabel(mid), dstLabel: dstLabel,
			projectID: projectID, path: path, branch: name, provider: provider, job: job}
		if proj := m.project(mid, projectID); proj != nil {
			for _, wt := range projectWorktrees(proj) {
				job.parts = append(job.parts, &worktreeMove{src: mid, srcLabel: job.lead.srcLabel, projectID: projectID,
					path: wt.Path, branch: wt.Branch, job: job})
			}
		}
		return m.describeProject(job.lead)
	}, mid)
}

// projectWorktrees is the task worktrees of proj: checked out on a branch
// that isn't the base.
func projectWorktrees(proj *proto.ProjectInfo) []proto.WorktreeInfo {
	var out []proto.WorktreeInfo
	for _, wt := range proj.Worktrees {
		if !wt.Main && !wt.Detached && wt.Branch != "" && wt.Branch != proj.Base {
			out = append(out, wt)
		}
	}
	return out
}

// inMainCheckout says whether agent p works in proj's main checkout rather
// than a worktree: an agent in a worktree that is not moving stays with it.
func inMainCheckout(proj *proto.ProjectInfo, p proto.PaneInfo) bool {
	var in *proto.WorktreeInfo
	for i, wt := range proj.Worktrees {
		if within(p.Cwd, wt.Path) && (in == nil || len(wt.Path) > len(in.Path)) {
			in = &proj.Worktrees[i]
		}
	}
	if in == nil {
		return p.Branch == "" || p.Branch == proj.Base
	}
	return in.Main
}

func (m *Model) hasAgentIn(mid, projectID string) bool {
	if mach := m.machine(mid); mach != nil {
		for _, p := range mach.panes {
			if p.Agent != nil && p.State == proto.PaneRunning && p.ProjectID == projectID {
				return true
			}
		}
	}
	return false
}

// describeProject asks the source what each worktree's move takes.
func (m *Model) describeProject(lead *worktreeMove) tea.Cmd {
	if lead.provider == "" && !m.hasCapability(lead.dst, proto.CapWorktreeMove) {
		m.setFlash("the server on "+lead.dstLabel+" predates moving worktrees; reload it", true)
		return nil
	}
	c := m.clientOf(lead.src)
	if c == nil {
		m.setFlash(m.offlineText(lead.src), true)
		return nil
	}
	parts := lead.job.parts
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second+time.Duration(len(parts))*10*time.Second)
		defer cancel()
		msg := projectDescribedMsg{lead: lead, infos: make([]proto.WorktreeMoveInfo, len(parts)), errs: make([]error, len(parts))}
		for i, p := range parts {
			msg.errs[i] = c.Call(ctx, proto.MethodWorktreeDescribe, proto.WorktreeRef{ProjectID: p.projectID, Path: p.path}, &msg.infos[i])
		}
		return msg
	}
}

// receiveProjectDescribed keeps the worktrees that can move, says which
// can't, and settles where they go.
func (m *Model) receiveProjectDescribed(msg projectDescribedMsg) tea.Cmd {
	lead, job := msg.lead, msg.lead.job
	var keep []*worktreeMove
	for i, p := range job.parts {
		if msg.errs[i] != nil {
			job.skipped = append(job.skipped, p.branch+" ("+errText(msg.errs[i])+")")
			continue
		}
		p.info = msg.infos[i]
		keep = append(keep, p)
	}
	job.parts = keep
	if proj := m.project(lead.src, lead.projectID); proj != nil {
		lead.info.Remote = proj.Remote
	}
	for _, p := range job.parts {
		if lead.info.Remote == "" {
			lead.info.Remote = p.info.Remote
		}
	}
	return m.planMove(lead)
}

// confirmProjectMove says what moves, where, and what stays.
func (m *Model) confirmProjectMove(lead *worktreeMove) {
	job := lead.job
	inPart := map[string]bool{}
	var agents []proto.PaneInfo
	for _, p := range job.parts {
		p.agents = m.agentsIn(p)
		for _, a := range p.agents {
			inPart[a.ID] = true
		}
		agents = append(agents, p.agents...)
	}
	job.main = nil
	proj := m.project(lead.src, lead.projectID)
	if mach := m.machine(lead.src); mach != nil && proj != nil {
		for _, p := range mach.panes {
			if p.Agent != nil && p.State == proto.PaneRunning && p.ProjectID == lead.projectID && !inPart[p.ID] && inMainCheckout(proj, p) {
				job.main = append(job.main, p)
			}
		}
	}
	agents = append(agents, job.main...)

	remote := remoteName(lead.info.Remote)
	var text []string
	switch {
	case lead.provider != "":
		text = []string{
			fmt.Sprintf("Move the project %s to a new %s sandbox.", lead.branch, providerLabel(lead.provider)),
			"conch creates the sandbox, installs itself there and clones " + remote + " into " + lead.cloneTo +
				" first. The sandbox needs access to the repository, and runs, and costs, until you stop it (m → Stop sandbox).",
		}
		if len(m.cfg.Sandbox.Of(lead.provider).Env) == 0 {
			text = append(text, "Nothing is passed in: an agent there logs in afresh unless env in [sandbox."+lead.provider+"] names its key.")
		}
	case lead.target == "" && lead.cloneTo != "":
		text = []string{
			fmt.Sprintf("Move the project %s to %s.", lead.branch, lead.dstLabel),
			lead.dstLabel + " has no clone of " + remote + ", so it is cloned into " + lead.cloneTo + " there first. It needs access to the repository from there.",
		}
	default:
		text = []string{fmt.Sprintf("Move the project %s to %s · %s.", lead.branch, lead.dstLabel, lead.targetName)}
	}
	if len(job.parts) > 0 {
		var branches []string
		for _, p := range job.parts {
			branches = append(branches, p.branch)
		}
		text = append(text, "Task worktrees, with their commits and all their uncommitted work: "+listSome(branches, 6)+".")
	} else {
		text = append(text, "No task worktree to move.")
	}
	if len(job.skipped) > 0 {
		text = append(text, "Can't move, so they stay here: "+strings.Join(job.skipped, "; ")+".")
	}
	if len(agents) > 0 {
		var names []string
		busy := false
		for _, p := range agents {
			name := p.DisplayName()
			if p.Agent.State == proto.AgentWorking {
				name += " (working)"
				busy = true
			}
			names = append(names, name)
		}
		text = append(text, "Continued there with their conversations, then closed here: "+strings.Join(names, ", ")+".")
		if busy {
			text = append(text, "A working agent is stopped mid-task; what it changes during the move stays here.")
		}
	}
	if len(job.main) > 0 {
		text = append(text, "Agents in the main checkout continue in the project's checkout there; its uncommitted changes stay here.")
	}
	text = append(text, "Terminals, and saved conversations no agent has open, stay here (Sessions → s shares one).")
	if len(job.parts) > 0 {
		text = append(text, "The worktrees here are kept; remove them later with x.")
	}
	lead.cols, lead.rows = m.paneArea()
	m.overlay = &dialog{title: " Move " + ansi.Truncate(lead.branch, 30, "…") + " ", text: text, confirm: true,
		submit: func(m *Model, _ []string) tea.Cmd { return m.runMove(lead) }}
}

// startProjectParts begins the worktrees once the project there is settled.
func (m *Model) startProjectParts(lead *worktreeMove) tea.Cmd {
	job := lead.job
	for _, p := range job.parts {
		p.dst, p.dstLabel, p.target, p.targetName = lead.dst, lead.dstLabel, lead.target, lead.targetName
		p.cols, p.rows = lead.cols, lead.rows
	}
	job.next = 0
	return m.runPart(job)
}

// nextPart goes on after a worktree arrived or failed: a failure stays
// here and is said at the end, rather than stopping the rest.
func (m *Model) nextPart(mv *worktreeMove, failure string) tea.Cmd {
	if failure != "" {
		mv.job.failed = append(mv.job.failed, failure)
	}
	return m.runPart(mv.job)
}

// runPart moves the next worktree, or, after the last, the agents in the
// main checkout.
func (m *Model) runPart(job *projectMove) tea.Cmd {
	for job.next < len(job.parts) {
		p := job.parts[job.next]
		job.next++
		if cmd := m.startMove(p); cmd != nil {
			return cmd
		}
		job.failed = append(job.failed, p.branch+": "+m.flash)
	}
	lead := job.lead
	from, to := m.clientOf(lead.src), m.clientOf(lead.dst)
	dir := ""
	if proj := m.project(lead.dst, lead.target); proj != nil {
		dir = proj.Path
	}
	if len(job.main) == 0 || from == nil || to == nil || dir == "" {
		failed := []string(nil)
		if len(job.main) > 0 {
			failed = append(failed, "the agents in the main checkout: "+lead.dstLabel+" or "+lead.srcLabel+" went away")
		}
		return func() tea.Msg { return projectMainDoneMsg{job: job, failed: failed} }
	}
	m.setFlash("moving "+lead.branch+" to "+lead.dstLabel+" · continuing the agents in its main checkout…", false)
	return func() tea.Msg {
		started, _, failed := continueAgents(from, to, job.main, job.mainPath, dir, lead.srcLabel, lead.cols, lead.rows)
		return projectMainDoneMsg{job: job, started: started, failed: failed}
	}
}

// add counts a worktree that arrived.
func (job *projectMove) add(branch string, done moveDoneMsg) {
	job.moved = append(job.moved, branch)
	job.started = append(job.started, done.started...)
	job.failed = append(job.failed, done.failed...)
}

func (m *Model) receiveProjectMainDone(msg projectMainDoneMsg) tea.Cmd {
	job := msg.job
	lead := job.lead
	job.started = append(job.started, msg.started...)
	job.failed = append(job.failed, msg.failed...)
	if mach := m.machine(lead.dst); mach != nil {
		for _, p := range job.started {
			if mach.paneIndex(p.ID) < 0 {
				mach.panes = append(mach.panes, p)
			}
		}
	}
	note := "moved " + lead.branch + " to " + lead.dstLabel
	if n := len(job.moved); n > 0 {
		note += " · " + counted(n, "worktree")
	}
	if n := len(job.started); n > 0 {
		verb := "continue"
		if n == 1 {
			verb = "continues"
		}
		note += " · " + counted(n, "agent") + " " + verb + " there"
	}
	if len(job.failed) > 0 {
		note += " · stayed here: " + strings.Join(job.failed, "; ")
	}
	if len(job.started) > 0 {
		m.revealPane(lead.dst, job.started[0])
		m.arrived()
		m.focus = focusMain
	}
	m.setFlash(note, len(job.failed) > 0)
	if len(job.failed) > 0 {
		text := []string{"Moved " + lead.branch + " to " + lead.dstLabel + ", but these stayed here:"}
		for _, f := range job.failed {
			text = append(text, "  "+f)
		}
		m.overlay = newNotice(" Moved "+ansi.Truncate(lead.branch, 30, "…")+" ", text)
	}
	return tea.Batch(m.rebuild(), m.saveState())
}
