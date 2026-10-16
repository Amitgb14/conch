package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// A task can wait for other work to finish (conch task -after): stacked
// work, where the second change needs the first. It waits for a branch —
// one that exists, or the one a task still on the schedule will start —
// and then starts from that branch's commits.
//
// When it is unsure it asks rather than starts: a branch that is gone
// without being merged, uncommitted work that the new branch would not
// see, or a task it waited for that never started all leave it waiting
// and saying why, since starting on the wrong base is work thrown away.

// resolveAfter settles what an item being added waits for: a task still on
// the schedule (by its ID, or by the branch it will start), a branch of the
// project, or a pane working on one. Called with the book unlocked.
func (s *Server) resolveAfter(it *proto.ScheduledItem) *proto.Error {
	ref := strings.TrimSpace(it.After)
	it.After, it.AfterBranch, it.AfterHead, it.AfterSeen = ref, "", "", false
	if ref == "" {
		return nil
	}
	b := s.schedules
	b.mu.Lock()
	var dep *proto.ScheduledItem
	for i := range b.items {
		o := &b.items[i]
		if o.Task != nil && (o.ID == ref || o.Task.Branch == ref) {
			c := *o
			dep = &c
			break
		}
	}
	b.mu.Unlock()
	if dep != nil {
		switch {
		case dep.Task.ProjectID != it.Task.ProjectID:
			return proto.Errorf(proto.ErrBadRequest, "%s is a task of another project", ref)
		case dep.EveryMinutes > 0:
			return proto.Errorf(proto.ErrBadRequest, "%s repeats, so there is no one run to wait for", ref)
		}
		it.After = dep.ID
		return nil
	}
	p, perr := s.projects.get(it.Task.ProjectID)
	if perr != nil {
		return perr
	}
	branch := ref
	if proto.IsPaneID(ref) {
		// A pane: the branch its agent is working on.
		e, perr := s.get(ref)
		if perr != nil {
			return perr
		}
		info := e.info()
		if info.ProjectID != it.Task.ProjectID || info.Branch == "" {
			return proto.Errorf(proto.ErrBadRequest, "pane %s is not on a branch of %s", ref, p.snapshot().Name)
		}
		branch = info.Branch
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	head, err := gitx.BranchHead(ctx, p.root, branch)
	if err != nil || head == "" {
		if strings.HasPrefix(ref, "s") && len(ref) == 9 && isHex(ref[1:]) {
			return proto.Errorf(proto.ErrNotFound, "nothing on the schedule is %s; if it has started, wait for its branch instead", ref)
		}
		return proto.Errorf(proto.ErrNotFound, "%s is neither a branch of %s nor a task on its schedule", ref, p.snapshot().Name)
	}
	// A branch that exists already: its agent, if any, is past starting,
	// so one found idle has finished.
	it.AfterBranch, it.AfterHead, it.AfterSeen = branch, head, true
	return nil
}

// afterStartGrace is how long a new agent that has not yet begun work is
// taken to be starting rather than finished: a moment after it starts,
// before its first message has reached it, it looks just like one that is
// done. Its turns say which, but they start again from zero at a reload,
// so they are trusted only for a pane this young.
const afterStartGrace = 2 * time.Minute

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return s != ""
}

// afterReady says whether the work an item waits for has finished. When it
// has, base is the branch to start from ("" for the task's own Base or the
// project's); when not, waiting says why, for the list. It notes on it what
// it learns: the branch's head, and whether its agent has been seen working.
func (s *Server) afterReady(it *proto.ScheduledItem) (base, waiting string) {
	if it.AfterBranch == "" {
		return "", s.afterItemWaiting(it)
	}
	p, perr := s.projects.get(it.Task.ProjectID)
	if perr != nil {
		return "", perr.Message
	}
	br := it.AfterBranch
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	head, err := gitx.BranchHead(ctx, p.root, br)
	if err != nil || head == "" {
		// Gone: merged and cleaned away, or thrown out.
		if it.AfterHead != "" && gitx.IsAncestor(ctx, p.root, it.AfterHead, p.snapshot().Base) {
			return "", ""
		}
		return "", fmt.Sprintf("branch %s is gone and its work is not on %s; was it discarded? conch schedule rm %s takes this away",
			br, p.snapshot().Base, it.ID)
	}
	it.AfterHead = head
	wt, checkedOut := p.branchWorktree(br)
	ba := s.branchAgents(p.id, br, wt.Path, time.Now())
	switch {
	case ba.busy:
		it.AfterSeen = true
		return "", "the agent on " + br + " is still at work"
	case ba.starting, ba.agents > 0 && !it.AfterSeen && !ba.done:
		return "", "the agent on " + br + " has not started working yet"
	}
	if it.Task.Base != "" {
		return "", "" // only the order was asked for, not the base
	}
	if checkedOut && wt.Status != nil && !wt.Status.Clean() {
		return "", br + " has uncommitted changes, which a branch made from it would not have; commit them, and this starts"
	}
	return br, ""
}

// afterItemWaiting is why an item waiting for a task that has not started
// yet is still waiting. The task it names starting gives it its branch.
func (s *Server) afterItemWaiting(it *proto.ScheduledItem) string {
	b := s.schedules
	b.mu.Lock()
	defer b.mu.Unlock()
	seen := map[string]bool{it.ID: true}
	for id := it.After; ; {
		var dep *proto.ScheduledItem
		for i := range b.items {
			if b.items[i].ID == id {
				dep = &b.items[i]
				break
			}
		}
		switch {
		case dep == nil && id == it.After:
			return fmt.Sprintf("the task it waits for (%s) is no longer on the schedule — taken off, or it failed to start; conch schedule rm %s, then add it again without -after", it.After, it.ID)
		case dep == nil, dep.AfterBranch != "" || dep.After == "":
			return "waiting for " + it.After + " to start, then finish"
		case seen[dep.After]:
			return "it waits on itself, through " + it.After + "; conch schedule rm " + it.ID + " takes it away"
		}
		seen[id] = true
		id = dep.After
	}
}

// branchWork is what the agents on a branch are doing.
type branchWork struct {
	busy     bool // at work, asking, or launched and not seen yet
	starting bool // new, and not yet begun (afterStartGrace)
	done     bool // finished, and nobody has looked yet
	agents   int
}

// branchAgents looks at the agents working on a branch of a project.
// Reviewers are left out: they read the work rather than make it.
func (s *Server) branchAgents(projectID, branch, dir string, now time.Time) branchWork {
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.panes))
	for _, e := range s.panes {
		entries = append(entries, e)
	}
	s.mu.Unlock()
	var w branchWork
	for _, e := range entries {
		info := e.info()
		if info.State != proto.PaneRunning || info.ProjectID != projectID || strings.HasPrefix(info.Name, "review · ") {
			continue
		}
		if info.Branch != branch && (dir == "" || !within(info.Cwd, dir)) {
			continue
		}
		if info.Agent == nil {
			if e.launched != "" { // started as an agent, not seen yet
				w.busy = true
				w.agents++
			}
			continue
		}
		w.agents++
		switch info.Agent.State {
		case proto.AgentWorking, proto.AgentBlocked:
			w.busy = true
		case proto.AgentDone:
			w.done = true
		default:
			if info.Agent.Turn == 0 && now.Sub(info.Created) < afterStartGrace {
				w.starting = true
			}
		}
	}
	return w
}
