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
)

// Moving a worktree to another machine: its commits, uncommitted work and
// local files go to a project there (cloned first if it has none), then
// each agent working in it continues there with its conversation and is
// closed here. The worktree here is kept.

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
	info               proto.WorktreeMoveInfo
	agents             []proto.PaneInfo // on src, to continue on dst
	cols, rows         int

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
	moveDoneMsg struct {
		mv      *worktreeMove
		result  proto.WorktreeUnpackResult
		started []proto.PaneInfo // agents now on dst
		closed  []string         // their panes on src, closed
		failed  []string         // agents that stayed, and why
	}
)

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
	var items []menuItem
	for _, mach := range m.machines {
		if mach.id == t.machine || mach.c == nil || mach.state != stateOnline {
			continue
		}
		key := ""
		if len(items) < 9 {
			key = fmt.Sprint(len(items) + 1)
		}
		mv := &worktreeMove{src: t.machine, dst: mach.id, srcLabel: m.machineLabel(t.machine), dstLabel: mach.label,
			projectID: t.projectID, path: path, branch: t.branch}
		items = append(items, menuItem{key, mach.label, func(m *Model) tea.Cmd { return m.describeMove(mv) }})
	}
	if len(items) == 0 {
		m.setFlash("no other machine is online · add one with M", true)
		return nil
	}
	m.overlay = &menu{title: "Move " + ansi.Truncate(t.branch, 30, "…") + " to which machine?", items: items,
		x: max(m.width/2-25, 0), y: max(m.height/3, 0)}
	return nil
}

// describeMove asks the source what the move takes.
func (m *Model) describeMove(mv *worktreeMove) tea.Cmd {
	if !m.hasCapability(mv.dst, proto.CapWorktreeMove) {
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

// receiveMoveDescribed asks which project on the other machine gets it:
// those cloned from the same origin first, or a new clone.
func (m *Model) receiveMoveDescribed(msg moveDescribedMsg) tea.Cmd {
	mv := msg.mv
	if msg.err != nil {
		m.moveFailed(mv, "can't move "+mv.branch, errText(msg.err))
		return nil
	}
	mach := m.machine(mv.dst)
	if mach == nil {
		return nil
	}
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
	where := filepath.Base(mv.path)
	if proj := m.project(mv.src, mv.projectID); proj != nil {
		where = m.tildify(mv.src, proj.Path)
	}
	if !strings.HasPrefix(where, "~") {
		where = "~/" + filepath.Base(where)
	}
	d := newDialog(*m, " Clone on "+mv.dstLabel+" ", []string{"Clone " + mv.info.Remote + " into this folder on " + mv.dstLabel +
		". It needs access to the repository from there."}, []string{"Folder"}, []string{where})
	d.submit = func(m *Model, v []string) tea.Cmd {
		folder := strings.TrimSpace(v[0])
		if folder == "" {
			m.setFlash("a folder is needed", true)
			return nil
		}
		c := m.clientOf(mv.dst)
		if c == nil {
			m.setFlash(m.offlineText(mv.dst), true)
			return nil
		}
		m.setFlash("cloning "+remoteName(mv.info.Remote)+" on "+mv.dstLabel+"…", false)
		params := proto.ProjectCloneParams{URL: mv.info.Remote, Path: folder}
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), cloneTimeout)
			defer cancel()
			var p proto.ProjectInfo
			err := c.Call(ctx, proto.MethodProjectClone, params, &p)
			return moveClonedMsg{mv: mv, project: p, err: err}
		}
	}
	m.overlay = d
}

func (m *Model) receiveMoveCloned(msg moveClonedMsg) {
	mv := msg.mv
	if msg.err != nil {
		m.moveFailed(mv, "clone on "+mv.dstLabel+" failed", errText(msg.err))
		return
	}
	if mach := m.machine(mv.dst); mach != nil && m.project(mv.dst, msg.project.ID) == nil {
		mach.projects = append(mach.projects, msg.project)
	}
	mv.target, mv.targetName = msg.project.ID, msg.project.Name
	m.confirmMove(mv)
}

// confirmMove says what moves and what happens to the agents here.
func (m *Model) confirmMove(mv *worktreeMove) {
	mv.agents = nil
	if mach := m.machine(mv.src); mach != nil {
		for _, p := range mach.panes {
			if p.Agent != nil && p.State == proto.PaneRunning && p.ProjectID == mv.projectID && (within(p.Cwd, mv.path) || p.Branch == mv.branch) {
				mv.agents = append(mv.agents, p)
			}
		}
	}
	i := mv.info
	text := []string{fmt.Sprintf("Move %s to %s · %s: its commits and all its uncommitted work.", mv.branch, mv.dstLabel, mv.targetName)}
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
		submit: func(m *Model, _ []string) tea.Cmd { return m.startMove(mv) }}
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
	if len(mv.agents) == 0 {
		return done
	}
	from, to := mv.mv.From, mv.mv.To
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Which saved conversation is open in which pane.
	sessions := map[string]proto.SessionInfo{}
	dirs := map[string]bool{mv.path: true}
	for _, p := range mv.agents {
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
	for _, p := range mv.agents {
		name := p.DisplayName()
		var info proto.PaneInfo
		var err error
		if s, ok := sessions[p.ID]; ok && handoff {
			info, err = handOver(ctx, from, to, s, mv, p.Agent.Name)
		} else {
			err = to.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: p.Agent.Name, Cwd: done.result.Path,
				Cols: mv.cols, Rows: mv.rows}, &info)
		}
		if err != nil {
			done.failed = append(done.failed, name+": "+errText(err))
			continue
		}
		done.started = append(done.started, info)
		if err := from.Call(ctx, proto.MethodPaneClose, proto.PaneRef{ID: p.ID}, nil); err != nil {
			done.failed = append(done.failed, name+" started there but didn't close here: "+errText(err))
			continue
		}
		done.closed = append(done.closed, p.ID)
	}
	return done
}

// handOver gives session s to agent in the moved worktree.
func handOver(ctx context.Context, from, to *client.Client, s proto.SessionInfo, mv *worktreeMove, agent string) (proto.PaneInfo, error) {
	var exp proto.SessionExport
	if err := from.Call(ctx, proto.MethodSessionExport, proto.SessionRef{Agent: s.Agent, ID: s.ID, Dir: s.Dir}, &exp); err != nil {
		return proto.PaneInfo{}, err
	}
	var res proto.SessionShareResult
	err := to.Call(ctx, proto.MethodSessionShare, proto.SessionShareParams{Agent: s.Agent, ID: s.ID, Dir: mv.mv.Result().Path, To: agent,
		Doc: exp.Doc, Name: exp.Name, From: mv.srcLabel, Cols: mv.cols, Rows: mv.rows}, &res)
	return res.Pane, err
}

func (m *Model) receiveMoveDone(msg moveDoneMsg) tea.Cmd {
	mv := msg.mv
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
