package server

import (
	"context"
	"log"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// An agent asked to resolve a branch's conflict with the base works for a
// while, and all git shows meanwhile is a branch stuck mid-rebase — or,
// before it has started, one that will conflict. So who was asked is
// kept on the branch's worktree (WorktreeInfo.Resolve) for as long as the
// conflict is there, and a client can say the agent is at it.
//
// It is kept in memory: a reload forgets it, and a client then goes by an
// agent working in a worktree stopped mid-rebase, which is nearly always
// the same thing.

// askGrace is how long an ask outlives a conflict git no longer shows,
// while the branch has not moved: the agent has not started the rebase
// yet (the autopilot undoes its own before asking), or merge-tree could
// not say there was a conflict (git before 2.38).
const askGrace = 2 * time.Minute

// resolveAsk is an ask and where the branch was when it was made.
type resolveAsk struct {
	proto.ResolveAsk
	head string
}

// stillConflicts is whether a worktree shows a conflict with the base:
// stopped mid-rebase or merge, or one merging it would meet.
func stillConflicts(w proto.WorktreeInfo) bool {
	return w.Rebasing != "" || (w.Status != nil && w.Status.Conflicts > 0) || len(w.Conflicts) > 0
}

// applyAsks puts each ask on its branch's worktree in info and, when info
// is a fresh read of git (fresh), forgets those whose conflict is over:
// an older read can predate the very rebase the agent was asked about.
// The caller holds p.mu, and info's Worktrees are its own to change.
func (p *project) applyAsks(info *proto.ProjectInfo, now time.Time, fresh bool) {
	seen := map[string]bool{}
	for i, w := range info.Worktrees {
		info.Worktrees[i].Resolve = nil
		name := w.Branch
		if name == "" {
			name = w.Rebasing
		}
		a, ok := p.asks[name]
		if !ok || name == "" {
			continue
		}
		seen[name] = true
		if fresh && !stillConflicts(w) && (w.Head != a.head || now.Sub(a.At) > askGrace) {
			delete(p.asks, name) // resolved, given up on, or never needed
			continue
		}
		r := a.ResolveAsk
		info.Worktrees[i].Resolve = &r
	}
	for name := range p.asks {
		if fresh && !seen[name] {
			delete(p.asks, name) // its worktree is gone
		}
	}
}

// noteResolveAsk records ask (nil: nobody) for branch and says so to the
// clients at once rather than at the next refresh.
func (pm *projectManager) noteResolveAsk(p *project, branch string, ask *resolveAsk) {
	p.mu.Lock()
	if ask == nil {
		delete(p.asks, branch)
	} else {
		if p.asks == nil {
			p.asks = map[string]resolveAsk{}
		}
		p.asks[branch] = *ask
	}
	info := p.info
	info.Worktrees = append([]proto.WorktreeInfo(nil), info.Worktrees...)
	p.applyAsks(&info, time.Now(), false)
	p.info = info
	p.gen++ // a read of git begun before this is older than it
	p.mu.Unlock()
	if pm.s.projectRegistered(p) {
		pm.s.broadcast(proto.EventProjectUpdated, info)
	}
}

// newAsk is pane asked to resolve branch, checked out at dir, by by.
func newAsk(ctx context.Context, p *project, branch, dir, pane, by string) *resolveAsk {
	// The files a stopped rebase is waiting on; else those merging it
	// would conflict in, from the last refresh.
	files := gitx.ConflictFiles(ctx, dir)
	for _, w := range p.snapshot().Worktrees {
		if w.Path == dir && len(files) == 0 {
			files = w.Conflicts
		}
	}
	return &resolveAsk{
		ResolveAsk: proto.ResolveAsk{Pane: pane, By: by, At: time.Now(), Files: files},
		head:       gitx.CommitOf(ctx, dir, "HEAD"),
	}
}

func (s *Server) branchResolving(rp proto.BranchResolvingParams) (struct{}, *proto.Error) {
	p, perr := s.projects.gitProject(rp.ProjectID)
	if perr != nil {
		return struct{}{}, perr
	}
	if rp.Branch == "" {
		return struct{}{}, proto.Errorf(proto.ErrBadRequest, "say which branch")
	}
	if rp.PaneID == "" {
		s.projects.noteResolveAsk(p, rp.Branch, nil)
		return struct{}{}, nil
	}
	if _, perr := s.get(rp.PaneID); perr != nil {
		return struct{}{}, perr
	}
	// Mid-rebase git names no branch: the worktree rewriting it is the one.
	dir := ""
	for _, w := range p.snapshot().Worktrees {
		if w.Branch == rp.Branch || (w.Branch == "" && w.Rebasing == rp.Branch) {
			dir = w.Path
		}
	}
	if dir == "" {
		return struct{}{}, proto.Errorf(proto.ErrBadRequest, "%s is not checked out here", rp.Branch)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	s.projects.noteResolveAsk(p, rp.Branch, newAsk(ctx, p, rp.Branch, dir, rp.PaneID, ""))
	log.Printf("project %s: pane %s is resolving %s's conflict", p.id, rp.PaneID, rp.Branch)
	return struct{}{}, nil
}
