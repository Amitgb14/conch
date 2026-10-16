package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sandbox"
)

// Moving a worktree to another machine: its commits, uncommitted work and
// local files go to a project there (cloned first if it has none), then
// each agent working in it continues there with its conversation and is
// closed here. The worktree here is kept.
//
// It is one choice and one confirmation: the machine — or a new sandbox —
// is picked, and conch settles the rest itself. The project there is the
// one cloned from the same origin; a machine without one gets a clone in
// the same place under its home as here; a new sandbox is created,
// connected and cloned into first. Only a project with no origin, which
// conch cannot match, still asks which project gets it.

const cloneTimeout = 10 * time.Minute

// worktreeMove is one move in progress. The pointer travels in its messages;
// only the command running a step touches it meanwhile.
type worktreeMove struct {
	src, dst           string // machine IDs
	srcLabel, dstLabel string
	projectID, path    string // on src
	branch             string
	target             string // project on dst
	targetName         string
	// cloneTo is where to clone the origin on dst before moving, when no
	// project there has it.
	cloneTo string
	// provider is the sandbox provider making dst, while there is no dst
	// yet; empty once the sandbox is a machine.
	provider string
	// job is the whole project this worktree moves with, if it does.
	job        *projectMove
	info       proto.WorktreeMoveInfo
	agents     []proto.PaneInfo // on src, to continue on dst
	cols, rows int

	mv *client.Move
}

type (
	moveDescribedMsg struct {
		mv  *worktreeMove
		err error
	}
	moveClonedMsg struct {
		mv      *worktreeMove
		project proto.ProjectInfo
		err     error
	}
	moveStepMsg struct {
		mv  *worktreeMove
		err error
	}
	// moveSandboxFailedMsg is a sandbox made for a move that could not be
	// made or set up.
	moveSandboxFailedMsg struct {
		mv  *worktreeMove
		err error
	}
	moveDoneMsg struct {
		mv      *worktreeMove
		result  proto.WorktreeUnpackResult
		started []proto.PaneInfo // agents now on dst
		closed  []string         // their panes on src, closed
		failed  []string         // agents that stayed, and why
	}
)

// openMoveAgent moves the worktree an agent's pane works in, with every
// agent in it: the agent is what somebody sees, the worktree is what moves.
func (m *Model) openMoveAgent(mid, paneID string) tea.Cmd {
	t, ok := m.paneMoveTarget(mid, paneID)
	if !ok {
		m.setFlash("this agent isn't in a worktree of a git project; only a branch's worktree moves", true)
		return nil
	}
	return m.openMoveWorktree(t)
}

// paneMoveTarget is the branch whose worktree pane paneID is in.
func (m *Model) paneMoveTarget(mid, paneID string) (harvestTarget, bool) {
	p := m.pane(mid, paneID)
	if p == nil || p.ProjectID == "" {
		return harvestTarget{}, false
	}
	proj := m.project(mid, p.ProjectID)
	if proj == nil || !proj.Git {
		return harvestTarget{}, false
	}
	t := harvestTarget{machine: mid, projectID: p.ProjectID, branch: p.Branch}
	// The deepest worktree holding its folder: a worktree may sit inside
	// the main checkout (.worktrees/), which holds it too.
	best := -1
	for _, wt := range proj.Worktrees {
		if within(p.Cwd, wt.Path) && len(wt.Path) > best {
			t.branch, best = wt.Branch, len(wt.Path)
		}
	}
	return t, t.branch != ""
}

// canMoveBranch, canMoveAgent and canMoveProject say whether T has
// something to move on that row, for its menus and the status bar.
func (m *Model) canMoveBranch(mid, projectID, branch string) bool {
	proj := m.project(mid, projectID)
	return proj != nil && proj.Git && branch != "" && branch != proj.Base && checkedOut(proj, branch) &&
		m.hasCapability(mid, proto.CapWorktreeMove)
}

func (m *Model) canMoveAgent(mid, paneID string) bool {
	if p := m.pane(mid, paneID); p == nil || p.Agent == nil {
		return false
	}
	t, ok := m.paneMoveTarget(mid, paneID)
	return ok && m.canMoveBranch(mid, t.projectID, t.branch)
}

func (m *Model) canMoveProject(mid, projectID string) bool {
	proj := m.project(mid, projectID)
	return proj != nil && proj.Git && m.hasCapability(mid, proto.CapWorktreeMove) &&
		(len(projectWorktrees(proj)) > 0 || m.hasAgentIn(mid, projectID))
}

// moveProviders is the sandbox providers set up well enough to make a
// sandbox for a move: one without a key is left out, not offered and
// refused.
func moveProviders() []string {
	var out []string
	for _, name := range sandbox.Providers {
		if p, err := openSandboxProvider(name); err == nil && p != nil && p.Check() == nil {
			out = append(out, name)
		}
	}
	return out
}

// openMoveWorktree starts moving branch t's worktree: which machine?
func (m *Model) openMoveWorktree(t harvestTarget) tea.Cmd {
	proj := m.project(t.machine, t.projectID)
	switch {
	case proj == nil || !proj.Git || t.branch == "":
		m.setFlash("select a branch of a git project", true)
		return nil
	case m.clientOf(t.machine) == nil:
		m.setFlash(m.offlineText(t.machine), true)
		return nil
	case !m.hasCapability(t.machine, proto.CapWorktreeMove):
		m.setFlash("the server on "+m.machineLabel(t.machine)+" predates moving worktrees; reload it", true)
		return nil
	case t.branch == proj.Base:
		m.setFlash(t.branch+" is the base branch; move a task branch", true)
		return nil
	}
	path := ""
	for _, wt := range proj.Worktrees {
		if wt.Branch == t.branch {
			path = wt.Path
		}
	}
	if path == "" {
		m.setFlash(t.branch+" isn't checked out; open a terminal on it (n) to make its worktree first", true)
		return nil
	}
	return m.openMoveTo(t.branch, func(m *Model, dst, dstLabel, provider string) tea.Cmd {
		mv := &worktreeMove{src: t.machine, dst: dst, srcLabel: m.machineLabel(t.machine), dstLabel: dstLabel,
			projectID: t.projectID, path: path, branch: t.branch, provider: provider}
		return m.describeMove(mv)
	}, t.machine)
}

// openMoveTo asks where what moves goes: every other online machine, then
// a new sandbox from each provider set up. pick is given the machine, or
// the provider when it is a sandbox still to be made.
func (m *Model) openMoveTo(what string, pick func(m *Model, dst, dstLabel, provider string) tea.Cmd, src string) tea.Cmd {
	var items []menuItem
	for _, mach := range m.machines {
		if mach.id == src || mach.c == nil || mach.state != stateOnline {
			continue
		}
		key := ""
		if len(items) < 9 {
			key = fmt.Sprint(len(items) + 1)
		}
		id, label := mach.id, mach.label
		items = append(items, menuItem{key, label, func(m *Model) tea.Cmd { return pick(m, id, label, "") }})
	}
	for _, name := range moveProviders() {
		name := name
		items = append(items, menuItem{"", "New " + providerLabel(name) + " sandbox", func(m *Model) tea.Cmd {
			return pick(m, "", "a new "+providerLabel(name)+" sandbox", name)
		}})
	}
	if len(items) == 0 {
		m.setFlash("no other machine is online · add one, or a sandbox, with M", true)
		return nil
	}
	m.overlay = &menu{title: "Move " + ansi.Truncate(what, 30, "…") + " to which machine?", items: items,
		x: max(m.width/2-25, 0), y: max(m.height/3, 0)}
	return nil
}

// describeMove asks the source what the move takes.
func (m *Model) describeMove(mv *worktreeMove) tea.Cmd {
	// A sandbox not yet made runs the conch this one installs.
	if mv.provider == "" && !m.hasCapability(mv.dst, proto.CapWorktreeMove) {
		m.setFlash("the server on "+mv.dstLabel+" predates moving worktrees; reload it", true)
		return nil
	}
	c := m.clientOf(mv.src)
	if c == nil {
		m.setFlash(m.offlineText(mv.src), true)
		return nil
	}
	ref := proto.WorktreeRef{ProjectID: mv.projectID, Path: mv.path}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := c.Call(ctx, proto.MethodWorktreeDescribe, ref, &mv.info)
		return moveDescribedMsg{mv: mv, err: err}
	}
}

// receiveMoveDescribed settles where on the other machine it goes, and
// asks only when conch cannot: see the top of this file.
func (m *Model) receiveMoveDescribed(msg moveDescribedMsg) tea.Cmd {
	mv := msg.mv
	if msg.err != nil {
		m.moveFailed(mv, "can't move "+mv.branch, errText(msg.err))
		return nil
	}
	return m.planMove(mv)
}

// planMove settles the project there from mv.info.Remote, then confirms.
func (m *Model) planMove(mv *worktreeMove) tea.Cmd {
	if mv.provider != "" {
		if mv.info.Remote == "" {
			m.moveFailed(mv, "can't move "+mv.branch+" to a new sandbox",
				"this project has no origin, so there is nothing to clone there; push it somewhere first, or move it to a machine that has it")
			return nil
		}
		mv.cloneTo = m.cloneFolder(mv)
		m.confirmMove(mv)
		return nil
	}
	mach := m.machine(mv.dst)
	if mach == nil {
		return nil
	}
	if p, ok := m.sameOrigin(mv, mach); ok {
		mv.target, mv.targetName = p.ID, p.Name
		m.confirmMove(mv)
		return nil
	}
	if mv.info.Remote != "" {
		folder := m.cloneFolder(mv)
		taken := false
		for _, p := range mach.projects {
			taken = taken || m.tildify(mv.dst, p.Path) == folder || p.Path == folder
		}
		if !taken {
			mv.cloneTo = folder
			m.confirmMove(mv)
			return nil
		}
	}
	return m.chooseMoveProject(mv, mach)
}

// sameOrigin is the project on mach cloned from the same origin as the one
// moving: the one in the same place under the home folder, if several are.
func (m *Model) sameOrigin(mv *worktreeMove, mach *machine) (proto.ProjectInfo, bool) {
	if mv.info.Remote == "" {
		return proto.ProjectInfo{}, false
	}
	var found []proto.ProjectInfo
	for _, p := range mach.projects {
		if p.Git && sameRemote(p.Remote, mv.info.Remote) {
			found = append(found, p)
		}
	}
	if len(found) == 0 {
		return proto.ProjectInfo{}, false
	}
	want := m.cloneFolder(mv)
	for _, p := range found {
		if m.tildify(mv.dst, p.Path) == want {
			return p, true
		}
	}
	return found[0], true
}

// cloneFolder is where a clone goes on the other machine: where the
// project is here, relative to the home folder.
func (m *Model) cloneFolder(mv *worktreeMove) string {
	where := filepath.Base(mv.path)
	if proj := m.project(mv.src, mv.projectID); proj != nil {
		where = m.tildify(mv.src, proj.Path)
	}
	if !strings.HasPrefix(where, "~") {
		where = "~/" + filepath.Base(where)
	}
	return where
}

// chooseMoveProject asks which project on the other machine gets it: those
// cloned from the same origin first, or a new clone.
func (m *Model) chooseMoveProject(mv *worktreeMove, mach *machine) tea.Cmd {
	type choice struct {
		p    proto.ProjectInfo
		same bool
	}
	var choices []choice
	for _, p := range mach.projects {
		if p.Git {
			choices = append(choices, choice{p, mv.info.Remote != "" && sameRemote(p.Remote, mv.info.Remote)})
		}
	}
	sort.SliceStable(choices, func(i, j int) bool { return choices[i].same && !choices[j].same })
	var items []menuItem
	for _, c := range choices {
		key := ""
		if len(items) < 9 {
			key = fmt.Sprint(len(items) + 1)
		}
		label := c.p.Name + styleMuted.Render("  "+m.tildify(mv.dst, c.p.Path))
		if c.same {
			label += styleMuted.Render(" · same origin")
		}
		p := c.p
		items = append(items, menuItem{key, label, func(m *Model) tea.Cmd {
			mv.target, mv.targetName = p.ID, p.Name
			m.confirmMove(mv)
			return nil
		}})
	}
	if mv.info.Remote != "" {
		items = append(items, menuItem{"c", "Clone " + remoteName(mv.info.Remote) + " there…", func(m *Model) tea.Cmd {
			m.openMoveClone(mv)
			return nil
		}})
	}
	if len(items) == 0 {
		m.setFlash(mv.dstLabel+" has no git project, and this one has no origin to clone it from · add it there first", true)
		return nil
	}
	m.overlay = &menu{title: "Move " + ansi.Truncate(mv.branch, 24, "…") + " into which project on " + mv.dstLabel + "?", items: items,
		x: max(m.width/2-30, 0), y: max(m.height/3, 0)}
	return nil
}

// openMoveClone asks where to clone the project on the other machine: by
// default where it is here, relative to the home folder.
func (m *Model) openMoveClone(mv *worktreeMove) {
	d := newDialog(*m, " Clone on "+mv.dstLabel+" ", []string{"Clone " + mv.info.Remote + " into this folder on " + mv.dstLabel +
		". It needs access to the repository from there."}, []string{"Folder"}, []string{m.cloneFolder(mv)})
	d.submit = func(m *Model, v []string) tea.Cmd {
		folder := strings.TrimSpace(v[0])
		if folder == "" {
			m.setFlash("a folder is needed", true)
			return nil
		}
		mv.cloneTo = folder
		m.confirmMove(mv)
		return nil
	}
	m.overlay = d
}

// cloneForMove clones the origin into mv.cloneTo on the other machine.
func (m *Model) cloneForMove(mv *worktreeMove) tea.Cmd {
	c := m.clientOf(mv.dst)
	if c == nil {
		m.setFlash(m.offlineText(mv.dst), true)
		return nil
	}
	m.setFlash("cloning "+remoteName(mv.info.Remote)+" on "+mv.dstLabel+"…", false)
	params := proto.ProjectCloneParams{URL: mv.info.Remote, Path: mv.cloneTo}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cloneTimeout)
		defer cancel()
		var p proto.ProjectInfo
		err := c.Call(ctx, proto.MethodProjectClone, params, &p)
		return moveClonedMsg{mv: mv, project: p, err: err}
	}
}

func (m *Model) receiveMoveCloned(msg moveClonedMsg) tea.Cmd {
	mv := msg.mv
	if msg.err != nil {
		more := []string{"Nothing changed on " + mv.srcLabel + "."}
		if mach := m.machine(mv.dst); mach != nil {
			if _, _, ok := mach.sandbox(); ok {
				// Made for this move, and now running for nothing.
				more = append(more, mv.dstLabel+" is still running, and costs until stopped (m → Stop sandbox, or Delete).")
			}
		}
		m.moveFailed(mv, "clone on "+mv.dstLabel+" failed", errText(msg.err), more...)
		return nil
	}
	if mach := m.machine(mv.dst); mach != nil && m.project(mv.dst, msg.project.ID) == nil {
		mach.projects = append(mach.projects, msg.project)
	}
	mv.target, mv.targetName = msg.project.ID, msg.project.Name
	return tea.Batch(m.startMove(mv), m.rebuild())
}

// createMoveSandbox makes the sandbox a move goes to, from the provider's
// settings in config.toml, and adds it as a machine; the move carries on
// once it is connected (resumeMove).
func (m *Model) createMoveSandbox(mv *worktreeMove) tea.Cmd {
	cfg := m.cfg.Sandbox.Of(mv.provider)
	env, err := sandbox.EnvFrom(cfg.Env)
	if err != nil {
		m.moveFailed(mv, "can't make a sandbox for "+mv.branch, err.Error())
		return nil
	}
	spec := sandbox.Spec{Snapshot: cfg.Snapshot, Env: env, AutoStop: cfg.AutoStop}
	m.setFlash("creating a "+providerLabel(mv.provider)+" sandbox for "+mv.branch+" (a minute or two)…", false)
	ch := make(chan string, 16)
	job := createSandboxWith(mv.provider, spec, "", ch)
	return m.fromHere(tea.Batch(func() tea.Msg {
		switch msg := job().(type) {
		case machineAddedMsg:
			msg.move = mv
			return msg
		case errMsg:
			return moveSandboxFailedMsg{mv: mv, err: msg.err}
		default:
			return msg
		}
	}, nextStep(ch)))
}

func (m *Model) receiveMoveSandboxFailed(msg moveSandboxFailedMsg) {
	m.setWorking("")
	m.moveFailed(msg.mv, "making "+msg.mv.dstLabel+" for "+msg.mv.branch+" failed", errText(msg.err),
		"Nothing changed on "+msg.mv.srcLabel+".")
}

// awaitMove holds a move for the sandbox just made for it until it is
// connected.
func (m *Model) awaitMove(mach *machine, mv *worktreeMove) {
	mv.dst, mv.dstLabel, mv.provider = mach.id, mach.label, ""
	if m.pendingMoves == nil {
		m.pendingMoves = map[string]*worktreeMove{}
	}
	m.pendingMoves[mach.id] = mv
	m.setFlash(mach.label+" is up · connecting to move "+mv.branch+" there…", false)
}

// resumeMove carries on a move held for mid, once that machine's projects
// are known: what it clones into and what it is cloned beside.
func (m *Model) resumeMove(mid string) tea.Cmd {
	mv := m.pendingMoves[mid]
	if mv == nil || m.clientOf(mid) == nil {
		return nil
	}
	delete(m.pendingMoves, mid)
	if !m.hasCapability(mid, proto.CapWorktreeMove) {
		m.moveFailed(mv, "can't move "+mv.branch+" to "+mv.dstLabel, "the conch installed there predates moving worktrees")
		return nil
	}
	if mach := m.machine(mid); mach != nil {
		if p, ok := m.sameOrigin(mv, mach); ok {
			mv.target, mv.targetName, mv.cloneTo = p.ID, p.Name, ""
		}
	}
	return m.runMove(mv)
}

// runMove does what was confirmed, from wherever it has to begin.
func (m *Model) runMove(mv *worktreeMove) tea.Cmd {
	switch {
	case mv.provider != "":
		return m.createMoveSandbox(mv)
	case mv.target == "" && mv.cloneTo != "":
		return m.cloneForMove(mv)
	default:
		return m.startMove(mv)
	}
}

// confirmMove says what moves and what happens to the agents here.
func (m *Model) confirmMove(mv *worktreeMove) {
	if mv.job != nil {
		m.confirmProjectMove(mv)
		return
	}
	mv.agents = m.agentsIn(mv)
	i := mv.info
	var text []string
	switch {
	case mv.provider != "":
		text = []string{
			fmt.Sprintf("Move %s to a new %s sandbox: its commits and all its uncommitted work.", mv.branch, providerLabel(mv.provider)),
			"conch creates the sandbox, installs itself there and clones " + remoteName(i.Remote) + " into " + mv.cloneTo +
				" first. The sandbox needs access to the repository, and runs, and costs, until you stop it (m → Stop sandbox).",
		}
		if len(m.cfg.Sandbox.Of(mv.provider).Env) == 0 {
			text = append(text, "Nothing is passed in: an agent there logs in afresh unless env in [sandbox."+mv.provider+"] names its key.")
		}
	case mv.target == "" && mv.cloneTo != "":
		text = []string{
			fmt.Sprintf("Move %s to %s: its commits and all its uncommitted work.", mv.branch, mv.dstLabel),
			mv.dstLabel + " has no clone of " + remoteName(i.Remote) + ", so it is cloned into " + mv.cloneTo +
				" there first. It needs access to the repository from there.",
		}
	default:
		text = []string{fmt.Sprintf("Move %s to %s · %s: its commits and all its uncommitted work.", mv.branch, mv.dstLabel, mv.targetName)}
	}
	var work []string
	if i.Staged > 0 {
		work = append(work, fmt.Sprintf("%d staged", i.Staged))
	}
	if i.Unstaged > 0 {
		work = append(work, fmt.Sprintf("%d changed", i.Unstaged))
	}
	if i.Other > 0 {
		work = append(work, fmt.Sprintf("%d untracked", i.Other))
	}
	if len(work) > 0 {
		text = append(text, "Uncommitted: "+strings.Join(work, ", ")+" file(s).")
	} else {
		text = append(text, "No uncommitted changes.")
	}
	if len(i.Local) > 0 {
		text = append(text, "Local files too: "+listSome(i.Local, 3)+".")
	}
	if len(mv.agents) > 0 {
		var names []string
		busy := false
		for _, p := range mv.agents {
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
	text = append(text, "The worktree here is kept; remove it later with x.")
	mv.cols, mv.rows = m.paneArea()
	m.overlay = &dialog{title: " Move " + ansi.Truncate(mv.branch, 30, "…") + " ", text: text, confirm: true,
		submit: func(m *Model, _ []string) tea.Cmd { return m.runMove(mv) }}
}

// agentsIn is the agents running in mv's worktree.
func (m *Model) agentsIn(mv *worktreeMove) []proto.PaneInfo {
	var out []proto.PaneInfo
	if mach := m.machine(mv.src); mach != nil {
		for _, p := range mach.panes {
			if p.Agent != nil && p.State == proto.PaneRunning && p.ProjectID == mv.projectID && (within(p.Cwd, mv.path) || p.Branch == mv.branch) {
				out = append(out, p)
			}
		}
	}
	return out
}

func (m *Model) startMove(mv *worktreeMove) tea.Cmd {
	from, to := m.clientOf(mv.src), m.clientOf(mv.dst)
	switch {
	case from == nil:
		m.setFlash(m.offlineText(mv.src), true)
		return nil
	case to == nil:
		m.setFlash(m.offlineText(mv.dst), true)
		return nil
	}
	if mv.job != nil && mv.job.lead == mv {
		return m.startProjectParts(mv)
	}
	mv.mv = client.NewMove(from, to, mv.projectID, mv.path, mv.target, mv.info.History)
	m.setFlash(mv.progress(), false)
	return mv.step()
}

func (mv *worktreeMove) progress() string {
	return "moving " + mv.branch + " to " + mv.dstLabel + " · " + mv.mv.Progress()
}

func (mv *worktreeMove) step() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), mv.mv.Timeout())
		defer cancel()
		done, err := mv.mv.Step(ctx)
		if err == nil && done {
			return mv.continueAgents()
		}
		return moveStepMsg{mv: mv, err: err}
	}
}

func (m *Model) receiveMoveStep(msg moveStepMsg) tea.Cmd {
	if msg.err != nil && msg.mv.job != nil {
		return m.nextPart(msg.mv, msg.mv.branch+": "+errText(msg.err))
	}
	if msg.err != nil {
		mv := msg.mv
		m.moveFailed(mv, "moving "+mv.branch+" to "+mv.dstLabel+" failed", errText(msg.err), "Nothing changed on "+mv.srcLabel+".")
		return nil
	}
	m.setFlash(msg.mv.progress(), false)
	return msg.mv.step()
}

// continueAgents runs once the worktree is there: each agent's conversation
// is handed to the same agent in the new worktree, or the agent starts
// afresh when it saved none; its pane here closes once that worked. It runs
// inside a command.
func (mv *worktreeMove) continueAgents() tea.Msg {
	done := moveDoneMsg{mv: mv, result: mv.mv.Result()}
	done.started, done.closed, done.failed = continueAgents(mv.mv.From, mv.mv.To, mv.agents, mv.path, done.result.Path,
		mv.srcLabel, mv.cols, mv.rows)
	return done
}

// continueAgents starts each agent's conversation in dir on to — the same
// agent, or one afresh when it saved none — and closes its pane on from
// once that worked. here is the folder the agents worked in on from.
func continueAgents(from, to *client.Client, agents []proto.PaneInfo, here, dir, srcLabel string, cols, rows int) (started []proto.PaneInfo, closed, failed []string) {
	if len(agents) == 0 {
		return nil, nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Which saved conversation is open in which pane.
	sessions := map[string]proto.SessionInfo{}
	dirs := map[string]bool{here: true}
	for _, p := range agents {
		if p.Cwd != "" {
			dirs[p.Cwd] = true
		}
	}
	for dir := range dirs {
		var list proto.SessionList
		if from.Call(ctx, proto.MethodSessionList, proto.SessionListParams{Dir: dir}, &list) == nil {
			for _, s := range list.Sessions {
				if s.PaneID != "" && s.ID != "" {
					sessions[s.PaneID] = s
				}
			}
		}
	}
	handoff := len(from.MissingCapabilities([]string{proto.CapSessionHandoff})) == 0 &&
		len(to.MissingCapabilities([]string{proto.CapSessionHandoff})) == 0
	for _, p := range agents {
		name := p.DisplayName()
		var info proto.PaneInfo
		var err error
		if s, ok := sessions[p.ID]; ok && handoff {
			info, err = handOver(ctx, from, to, s, dir, srcLabel, p.Agent.Name, cols, rows)
		} else {
			err = to.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: p.Agent.Name, Cwd: dir,
				Cols: cols, Rows: rows}, &info)
		}
		if err != nil {
			failed = append(failed, name+": "+errText(err))
			continue
		}
		started = append(started, info)
		if err := from.Call(ctx, proto.MethodPaneClose, proto.PaneRef{ID: p.ID}, nil); err != nil {
			failed = append(failed, name+" started there but didn't close here: "+errText(err))
			continue
		}
		closed = append(closed, p.ID)
	}
	return started, closed, failed
}

// handOver gives session s to agent in dir on to.
func handOver(ctx context.Context, from, to *client.Client, s proto.SessionInfo, dir, srcLabel, agent string, cols, rows int) (proto.PaneInfo, error) {
	var exp proto.SessionExport
	if err := from.Call(ctx, proto.MethodSessionExport, proto.SessionRef{Agent: s.Agent, ID: s.ID, Dir: s.Dir}, &exp); err != nil {
		return proto.PaneInfo{}, err
	}
	var res proto.SessionShareResult
	err := to.Call(ctx, proto.MethodSessionShare, proto.SessionShareParams{Agent: s.Agent, ID: s.ID, Dir: dir, To: agent,
		Doc: exp.Doc, Name: exp.Name, From: srcLabel, Cols: cols, Rows: rows}, &res)
	return res.Pane, err
}

func (m *Model) receiveMoveDone(msg moveDoneMsg) tea.Cmd {
	mv := msg.mv
	if mv.job != nil {
		mv.job.add(mv.branch, msg)
		return m.nextPart(mv, "")
	}
	if mach := m.machine(mv.dst); mach != nil {
		for _, p := range msg.started {
			if mach.paneIndex(p.ID) < 0 {
				mach.panes = append(mach.panes, p)
			}
		}
	}
	note := "moved " + mv.branch + " to " + mv.dstLabel
	if n := len(msg.started); n > 0 {
		verb := "continue"
		if n == 1 {
			verb = "continues"
		}
		note += " · " + counted(n, "agent") + " " + verb + " there"
	}
	if len(msg.failed) > 0 {
		note += " · stayed here: " + strings.Join(msg.failed, "; ")
	}
	if len(msg.started) > 0 {
		m.revealPane(mv.dst, msg.started[0])
		m.arrived()
		m.focus = focusMain
	}
	m.setFlash(note, len(msg.failed) > 0)
	if len(msg.failed) > 0 {
		text := []string{"Moved " + mv.branch + " to " + mv.dstLabel + ", but these agents stayed here:"}
		for _, f := range msg.failed {
			text = append(text, "  "+f)
		}
		m.overlay = newNotice(" Moved "+ansi.Truncate(mv.branch, 30, "…")+" ", text)
	}
	return tea.Batch(m.rebuild(), m.saveState())
}

// moveFailed says why, in full: the status bar has room for a few words.
func (m *Model) moveFailed(mv *worktreeMove, what, why string, more ...string) {
	m.setFlash(what+": "+why, true)
	m.overlay = newNotice(" "+ansi.Truncate(mv.branch, 30, "…")+" ", append([]string{what + ":", why}, more...))
}

// sameRemote says whether two remote URLs name the same repository, however
// they are written: git@host:a/b.git, ssh://git@host/a/b, https://host/a/b.
// Only the ordering of a list depends on it.
func sameRemote(a, b string) bool {
	na, nb := normalRemote(a), normalRemote(b)
	return na != "" && strings.EqualFold(na, nb) // the big hosts ignore case in paths
}

func normalRemote(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if !strings.Contains(u, "://") {
		// scp-like: [user@]host:path, unless it is a local path.
		if i := strings.Index(u, ":"); i > 0 && !strings.HasPrefix(u, "/") && !strings.Contains(u[:i], "/") {
			u = "ssh://" + u[:i] + "/" + strings.TrimPrefix(u[i+1:], "/")
		}
	}
	if pu, err := url.Parse(u); err == nil && pu.Host != "" {
		u = strings.ToLower(pu.Hostname()) + "/" + strings.TrimPrefix(pu.Path, "/")
	}
	u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	return strings.TrimSuffix(u, "/")
}

// remoteName is a remote URL's repository, for labels: owner/name.
func remoteName(u string) string {
	n := normalRemote(u)
	parts := strings.Split(n, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return n
}

// checkedOut says whether branch has a worktree in proj.
func checkedOut(proj *proto.ProjectInfo, branch string) bool {
	for _, wt := range proj.Worktrees {
		if wt.Branch == branch {
			return true
		}
	}
	return false
}

func within(path, dir string) bool {
	return path != "" && (path == dir || strings.HasPrefix(path, dir+"/"))
}

// errText is an error for a status line: a server's own message without its
// code.
func errText(err error) string {
	var perr *proto.Error
	if errors.As(err, &perr) { // keep what wrapped it: "pack the worktree: …"
		return strings.Replace(err.Error(), perr.Error(), perr.Message, 1)
	}
	return err.Error()
}
