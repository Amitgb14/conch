// Package relocate moves a whole project to another machine: the
// repository (cloned there from its origin, or a project there that has
// it), every task worktree with its commits and uncommitted work, every
// branch holding commits its origin hasn't (given a worktree here first,
// so it travels the same way), and the agents — each continued there with
// its conversation, then closed here.
//
// It is what the TUI's project move does, made a library so the CLI
// (`conch project move`) and Conch.app, through the CLI, do the same: a
// Plan first, read from both servers and changing nothing, then Run, which
// reports each step as it goes. One worktree failing doesn't stop the
// others; what stayed here, and why, is in the Result.
package relocate

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// Worktree is one checkout that moves, with the agents working in it.
type Worktree struct {
	Branch string                 `json:"branch"`
	Path   string                 `json:"path"`
	Info   proto.WorktreeMoveInfo `json:"info"`
	Agents []proto.PaneInfo       `json:"agents,omitempty"`
	// There is the worktree the project there already has on this branch —
	// an earlier move's. Nothing is copied over it; its agents continue in
	// it.
	There string `json:"there,omitempty"`
}

// Plan is what a move would do, read from both machines; nothing changes
// until Run.
type Plan struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Path      string `json:"path"` // the main checkout, here
	Remote    string `json:"remote,omitempty"`
	From      string `json:"from"` // labels, for what is said
	To        string `json:"to"`

	// Target is the project there that has the same origin; when there
	// is none, CloneTo is where the origin is cloned there first.
	Target     string `json:"target,omitempty"`
	TargetName string `json:"target_name,omitempty"`
	TargetPath string `json:"target_path,omitempty"`
	CloneTo    string `json:"clone_to,omitempty"`

	Worktrees []Worktree `json:"worktrees,omitempty"`
	// Branches have no worktree and hold commits the origin hasn't (local
	// only, gone upstream, or ahead of it): each is given a worktree here,
	// then moves like the others.
	Branches []proto.BranchInfo `json:"branches,omitempty"`
	// MainAgents work in the main checkout: they continue in the project's
	// own checkout there.
	MainAgents []proto.PaneInfo `json:"main_agents,omitempty"`

	// What stays here: worktrees that can't move (and why), the main
	// checkout's uncommitted files, terminals.
	Skipped []string `json:"skipped,omitempty"`
	// Already is the branches the project there has checked out already,
	// with no agent left to follow them: nothing to do.
	Already   []string `json:"already,omitempty"`
	MainDirty int      `json:"main_dirty,omitempty"`
	Terminals int      `json:"terminals,omitempty"`
}

// Agents is every agent the plan continues there.
func (p Plan) Agents() []proto.PaneInfo {
	var out []proto.PaneInfo
	for _, w := range p.Worktrees {
		out = append(out, w.Agents...)
	}
	return append(out, p.MainAgents...)
}

// Empty says whether there is nothing to move.
func (p Plan) Empty() bool {
	return len(p.Worktrees) == 0 && len(p.Branches) == 0 && len(p.MainAgents) == 0
}

// ErrNothing is a project with nothing to move: no task worktree, no
// branch of its own and no agent.
var ErrNothing = errors.New("nothing would move")

// NewPlan reads what moving projectID from from to to would do. into, when
// given, is where to clone it there instead of the folder it has here
// (relative to the home folder).
func NewPlan(ctx context.Context, from, to *client.Client, projectID, fromLabel, toLabel, into string) (Plan, error) {
	for _, c := range []*client.Client{from, to} {
		if missing := c.MissingCapabilities([]string{proto.CapWorktreeMove}); len(missing) > 0 {
			who := fromLabel
			if c == to {
				who = toLabel
			}
			return Plan{}, fmt.Errorf("the server on %s predates moving a project; reload or upgrade it", who)
		}
	}
	var projects proto.ProjectList
	if err := from.Call(ctx, proto.MethodProjectList, nil, &projects); err != nil {
		return Plan{}, err
	}
	var proj *proto.ProjectInfo
	for i := range projects.Projects {
		if p := projects.Projects[i]; p.ID == projectID || p.Name == projectID {
			proj = &projects.Projects[i]
		}
	}
	switch {
	case proj == nil:
		return Plan{}, fmt.Errorf("no project %q on %s", projectID, fromLabel)
	case !proj.Git:
		return Plan{}, fmt.Errorf("%s is not a git repository: only git projects move", proj.Name)
	}
	plan := Plan{ProjectID: proj.ID, Name: proj.Name, Path: proj.Path, Remote: proj.Remote, From: fromLabel, To: toLabel}

	// The worktrees, and what each takes.
	for _, wt := range ProjectWorktrees(proj) {
		var info proto.WorktreeMoveInfo
		if err := from.Call(ctx, proto.MethodWorktreeDescribe, proto.WorktreeRef{ProjectID: proj.ID, Path: wt.Path}, &info); err != nil {
			plan.Skipped = append(plan.Skipped, wt.Branch+" ("+ErrText(err)+")")
			continue
		}
		if plan.Remote == "" {
			plan.Remote = info.Remote
		}
		plan.Worktrees = append(plan.Worktrees, Worktree{Branch: wt.Branch, Path: wt.Path, Info: info})
	}
	plan.Branches = BranchesToMove(proj)
	for _, wt := range proj.Worktrees {
		if wt.Main && wt.Status != nil {
			plan.MainDirty = wt.Status.Files
		}
	}

	// Who works where.
	var panes proto.PaneList
	if err := from.Call(ctx, proto.MethodPaneList, nil, &panes); err != nil {
		return Plan{}, err
	}
	for _, p := range panes.Panes {
		if p.ProjectID != proj.ID || p.State != proto.PaneRunning {
			continue
		}
		if p.Agent == nil {
			plan.Terminals++
			continue
		}
		placed := false
		for i := range plan.Worktrees {
			if within(p.Cwd, plan.Worktrees[i].Path) {
				plan.Worktrees[i].Agents = append(plan.Worktrees[i].Agents, p)
				placed = true
				break
			}
		}
		if !placed && InMainCheckout(proj, p) {
			plan.MainAgents = append(plan.MainAgents, p)
		}
	}
	if plan.Empty() {
		return plan, fmt.Errorf("%s has no task worktree, no branch of its own and no agent running: %w", proj.Name, ErrNothing)
	}

	// Where it goes.
	if err := plan.settle(ctx, from, to, into); err != nil {
		return plan, err
	}
	if plan.Empty() {
		return plan, fmt.Errorf("everything of %s is on %s already: %w", proj.Name, toLabel, ErrNothing)
	}
	return plan, nil
}

// settle finds the project there with the same origin, or where to clone
// it: the folder it has here, relative to the home folder.
func (p *Plan) settle(ctx context.Context, from, to *client.Client, into string) error {
	var there proto.ProjectList
	if err := to.Call(ctx, proto.MethodProjectList, nil, &there); err != nil {
		return err
	}
	want := into
	if want == "" {
		want = CloneFolder(p.Path, home(from))
	}
	if into == "" && p.Remote != "" {
		var found []proto.ProjectInfo
		for _, q := range there.Projects {
			if q.Git && SameRemote(q.Remote, p.Remote) {
				found = append(found, q)
			}
		}
		if len(found) > 0 {
			pick := found[0]
			for _, q := range found {
				if Tildify(q.Path, home(to)) == want {
					pick = q
				}
			}
			p.Target, p.TargetName, p.TargetPath = pick.ID, pick.Name, pick.Path
			p.joinWorktrees(&pick)
			return nil
		}
	}
	if p.Remote == "" {
		return fmt.Errorf("%s has no origin, so there is nothing to clone on %s; push it somewhere first", p.Name, p.To)
	}
	for _, q := range there.Projects {
		if Tildify(q.Path, home(to)) == want || q.Path == want {
			return fmt.Errorf("%s on %s already holds another project (%s); move into another folder", want, p.To, q.Name)
		}
	}
	p.CloneTo = want
	return nil
}

// joinWorktrees settles what the project there has already — a branch an
// earlier move took, or that somebody checked out there: moving it again
// would fail (git checks a branch out once), and copying over it would
// lose whatever happened to it there since.
func (p *Plan) joinWorktrees(there *proto.ProjectInfo) {
	at := map[string]string{}
	for _, wt := range there.Worktrees {
		if !wt.Main && wt.Branch != "" {
			at[wt.Branch] = wt.Path
		}
	}
	var keep []Worktree
	for _, w := range p.Worktrees {
		if path, ok := at[w.Branch]; ok {
			if len(w.Agents) == 0 {
				p.Already = append(p.Already, w.Branch)
				continue
			}
			w.There = path
		}
		keep = append(keep, w)
	}
	p.Worktrees = keep
	var branches []proto.BranchInfo
	for _, b := range p.Branches {
		if _, ok := at[b.Name]; ok {
			p.Already = append(p.Already, b.Name)
			continue
		}
		branches = append(branches, b)
	}
	p.Branches = branches
}

// Event is a step of a run, said as it starts and as it ends.
type Event struct {
	Kind   string `json:"kind"`  // clone, branch, worktree, agents, done
	Name   string `json:"name"`  // the branch, or what is cloned
	State  string `json:"state"` // start, ok, fail
	Detail string `json:"detail,omitempty"`
	Step   int    `json:"step"`
	Steps  int    `json:"steps"`
}

// Result is what a run did: the branches that arrived, the agents that
// continue there, and what stayed here, with why.
type Result struct {
	Target     string           `json:"target"`
	TargetPath string           `json:"target_path"`
	Moved      []string         `json:"moved,omitempty"`
	Started    []proto.PaneInfo `json:"started,omitempty"`
	Failed     []string         `json:"failed,omitempty"`
}

// Options are the size new panes there start at.
type Options struct {
	Cols, Rows int
}

// Steps is how many steps a run of p reports.
func (p Plan) Steps() int {
	n := len(p.Worktrees) + len(p.Branches)
	if p.Target == "" {
		n++
	}
	if len(p.MainAgents) > 0 {
		n++
	}
	return n
}

// Run moves p from from to to, saying each step to emit (which may be nil).
// An error is returned only when nothing could move at all — the clone
// failed; a worktree that fails is in Result.Failed and the rest go on.
func Run(ctx context.Context, p Plan, from, to *client.Client, opt Options, emit func(Event)) (Result, error) {
	step, steps := 0, p.Steps()
	say := func(kind, name, state, detail string) {
		if emit != nil {
			emit(Event{Kind: kind, Name: name, State: state, Detail: detail, Step: step, Steps: steps})
		}
	}
	res := Result{Target: p.Target, TargetPath: p.TargetPath}

	if p.Target == "" {
		step++
		say("clone", p.Remote, "start", "into "+p.CloneTo)
		cctx, cancel := context.WithTimeout(ctx, 11*time.Minute) // the server's own limit is 10
		var info proto.ProjectInfo
		err := to.Call(cctx, proto.MethodProjectClone, proto.ProjectCloneParams{URL: p.Remote, Path: p.CloneTo}, &info)
		cancel()
		if err != nil {
			say("clone", p.Remote, "fail", ErrText(err))
			return res, fmt.Errorf("clone %s on %s: %s", RemoteName(p.Remote), p.To, ErrText(err))
		}
		res.Target, res.TargetPath = info.ID, info.Path
		say("clone", p.Remote, "ok", info.Path)
	}

	worktrees := append([]Worktree(nil), p.Worktrees...)
	// A branch without a worktree is given one here, so it travels as the
	// others do: its commits, as many as the other side lacks.
	for _, b := range p.Branches {
		step++
		say("branch", b.Name, "start", "")
		var wt proto.WorktreeResult
		if err := from.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: p.ProjectID, Branch: b.Name}, &wt); err != nil {
			res.Failed = append(res.Failed, b.Name+": "+ErrText(err))
			say("branch", b.Name, "fail", ErrText(err))
			continue
		}
		var info proto.WorktreeMoveInfo
		if err := from.Call(ctx, proto.MethodWorktreeDescribe, proto.WorktreeRef{ProjectID: p.ProjectID, Path: wt.Path}, &info); err != nil {
			res.Failed = append(res.Failed, b.Name+": "+ErrText(err))
			say("branch", b.Name, "fail", ErrText(err))
			continue
		}
		if path, err := moveOne(ctx, from, to, p.ProjectID, res.Target, wt.Path, info); err != nil {
			res.Failed = append(res.Failed, b.Name+": "+ErrText(err))
			say("branch", b.Name, "fail", ErrText(err))
		} else {
			res.Moved = append(res.Moved, b.Name)
			say("branch", b.Name, "ok", path)
		}
	}

	for _, w := range worktrees {
		step++
		say("worktree", w.Branch, "start", "")
		path, err := w.There, error(nil)
		if path == "" {
			path, err = moveOne(ctx, from, to, p.ProjectID, res.Target, w.Path, w.Info)
		}
		if err != nil {
			res.Failed = append(res.Failed, w.Branch+": "+ErrText(err))
			say("worktree", w.Branch, "fail", ErrText(err))
			continue
		}
		if w.There == "" {
			res.Moved = append(res.Moved, w.Branch)
		}
		started, _, failed := ContinueAgents(from, to, w.Agents, w.Path, path, p.From, opt.Cols, opt.Rows)
		res.Started = append(res.Started, started...)
		res.Failed = append(res.Failed, failed...)
		detail := path
		if len(started) > 0 {
			detail = path + " · " + Count(len(started), "agent continues", "agents continue") + " there"
		}
		say("worktree", w.Branch, "ok", detail)
	}

	if len(p.MainAgents) > 0 {
		step++
		say("agents", p.Name, "start", "")
		dir := res.TargetPath
		if dir == "" {
			dir = targetPath(ctx, to, res.Target)
		}
		if dir == "" {
			res.Failed = append(res.Failed, "the agents in the main checkout: the project on "+p.To+" went away")
			say("agents", p.Name, "fail", "the project there went away")
		} else {
			started, _, failed := ContinueAgents(from, to, p.MainAgents, p.Path, dir, p.From, opt.Cols, opt.Rows)
			res.Started = append(res.Started, started...)
			res.Failed = append(res.Failed, failed...)
			state := "ok"
			if len(failed) > 0 {
				state = "fail"
			}
			say("agents", p.Name, state, fmt.Sprintf("%d of %d continue in %s", len(started), len(p.MainAgents), dir))
		}
	}
	if res.TargetPath == "" {
		res.TargetPath = targetPath(ctx, to, res.Target)
	}
	return res, nil
}

// moveOne moves the worktree at path into project target there, returning
// where it is there.
func moveOne(ctx context.Context, from, to *client.Client, projectID, target, path string, info proto.WorktreeMoveInfo) (string, error) {
	mv := client.NewMove(from, to, projectID, path, target, info.History)
	mctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	r, err := mv.Run(mctx)
	return r.Path, err
}

func targetPath(ctx context.Context, to *client.Client, target string) string {
	var list proto.ProjectList
	if to.Call(ctx, proto.MethodProjectList, nil, &list) == nil {
		for _, q := range list.Projects {
			if q.ID == target {
				return q.Path
			}
		}
	}
	return ""
}

func home(c *client.Client) string {
	if c == nil {
		return ""
	}
	return c.Server.Home
}

// ProjectWorktrees is the task worktrees of proj: checked out on a branch
// that isn't the base.
func ProjectWorktrees(proj *proto.ProjectInfo) []proto.WorktreeInfo {
	var out []proto.WorktreeInfo
	for _, wt := range proj.Worktrees {
		if !wt.Main && !wt.Detached && wt.Branch != "" && wt.Branch != proj.Base {
			out = append(out, wt)
		}
	}
	return out
}

// BranchesToMove is proj's branches with no worktree that hold commits the
// origin hasn't: local only, gone upstream, or ahead of it. The base
// branch stays: the clone there has its own.
func BranchesToMove(proj *proto.ProjectInfo) []proto.BranchInfo {
	var out []proto.BranchInfo
	for _, b := range proj.Branches {
		if b.Worktree != "" || b.Name == proj.Base || CheckedOut(proj, b.Name) {
			continue
		}
		if b.Upstream == "" || b.Gone || b.Ahead > 0 {
			out = append(out, b)
		}
	}
	return out
}

// InMainCheckout says whether agent p works in proj's main checkout rather
// than a worktree: an agent in a worktree that is not moving stays with it.
func InMainCheckout(proj *proto.ProjectInfo, p proto.PaneInfo) bool {
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

// CheckedOut says whether branch has a worktree in proj.
func CheckedOut(proj *proto.ProjectInfo, branch string) bool {
	for _, wt := range proj.Worktrees {
		if wt.Branch == branch {
			return true
		}
	}
	return false
}

// CloneFolder is where a clone goes on the other machine: where the
// project is here, relative to the home folder (~/workspace/daos), or
// ~/name when it is outside it.
func CloneFolder(path, home string) string {
	where := Tildify(path, home)
	if !strings.HasPrefix(where, "~") {
		where = "~/" + filepath.Base(path)
	}
	return where
}

// Tildify writes path with ~ for home, when it is inside it.
func Tildify(path, home string) string {
	if home == "" {
		return path
	}
	homes := []string{home}
	if real, err := filepath.EvalSymlinks(home); err == nil && real != home { // the server records real paths
		homes = append(homes, real)
	}
	for _, h := range homes {
		if path == h {
			return "~"
		}
		if strings.HasPrefix(path, h+"/") {
			return "~" + path[len(h):]
		}
	}
	return path
}

func within(path, dir string) bool {
	return path != "" && (path == dir || strings.HasPrefix(path, dir+"/"))
}

// ContinueAgents starts each agent's conversation in dir on to — the same
// agent, or one afresh when it saved none — and closes its pane on from
// once that worked. here is the folder the agents worked in on from.
func ContinueAgents(from, to *client.Client, agents []proto.PaneInfo, here, dir, fromLabel string, cols, rows int) (started []proto.PaneInfo, closed, failed []string) {
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
			info, err = HandOver(ctx, from, to, s, dir, fromLabel, p.Agent.Name, cols, rows)
		} else {
			err = to.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: p.Agent.Name, Cwd: dir,
				Cols: cols, Rows: rows}, &info)
		}
		if err != nil {
			failed = append(failed, name+": "+ErrText(err))
			continue
		}
		started = append(started, info)
		if err := from.Call(ctx, proto.MethodPaneClose, proto.PaneRef{ID: p.ID}, nil); err != nil {
			failed = append(failed, name+" started there but didn't close here: "+ErrText(err))
			continue
		}
		closed = append(closed, p.ID)
	}
	return started, closed, failed
}

// HandOver gives session s to agent in dir on to.
func HandOver(ctx context.Context, from, to *client.Client, s proto.SessionInfo, dir, fromLabel, agent string, cols, rows int) (proto.PaneInfo, error) {
	var exp proto.SessionExport
	if err := from.Call(ctx, proto.MethodSessionExport, proto.SessionRef{Agent: s.Agent, ID: s.ID, Dir: s.Dir}, &exp); err != nil {
		return proto.PaneInfo{}, err
	}
	var res proto.SessionShareResult
	err := to.Call(ctx, proto.MethodSessionShare, proto.SessionShareParams{Agent: s.Agent, ID: s.ID, Dir: dir, To: agent,
		Doc: exp.Doc, Name: exp.Name, From: fromLabel, Cols: cols, Rows: rows}, &res)
	return res.Pane, err
}

// SameRemote says whether two remote URLs name the same repository, however
// they are written: git@host:a/b.git, ssh://git@host/a/b, https://host/a/b.
func SameRemote(a, b string) bool {
	na, nb := NormalRemote(a), NormalRemote(b)
	return na != "" && strings.EqualFold(na, nb) // the big hosts ignore case in paths
}

// NormalRemote is a remote URL as host/path, without .git.
func NormalRemote(u string) string {
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

// RemoteName is a remote URL's repository, for labels: owner/name.
func RemoteName(u string) string {
	n := NormalRemote(u)
	parts := strings.Split(n, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return n
}

// Count is n said with the noun that fits it: 1 agent continues, 2 agents
// continue — what a person reads, rather than agent(s).
func Count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// ErrText is an error for a status line: a server's own message without
// its code.
func ErrText(err error) string {
	var perr *proto.Error
	if errors.As(err, &perr) { // keep what wrapped it: "pack the worktree: …"
		return strings.Replace(err.Error(), perr.Error(), perr.Message, 1)
	}
	return err.Error()
}
