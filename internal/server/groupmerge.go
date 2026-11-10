package server

import (
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// Merge All When Green on a task across a group's repos. Repos that
// change together are merged together: nothing is merged until every
// pull request of the task is open, green and held back by nothing the
// autopilot would hold it for (mergeHold), and then they merge one after
// another in the order the person gave — the contract before the code
// that serves it, before the code that calls it. Each merge waits for the
// one before it to land; one GitHub refuses stops it there, so nothing
// after it is merged. It is the server's, watched on the autopilot's tick,
// so it carries on with every client closed, and it is kept in
// groups.json, so a reload or a restart carries on too.

// savedMerge is a task's GroupTaskMerge with its projects by folder.
type savedMerge struct {
	Order      []string  `json:"order"`
	Merged     []string  `json:"merged,omitempty"`
	State      string    `json:"state"`
	Why        string    `json:"why,omitempty"`
	RemoveTask bool      `json:"remove_task,omitempty"`
	Armed      time.Time `json:"armed"`
}

func (g *groupBook) mergeInfo(m *savedMerge) *proto.GroupTaskMerge {
	if m == nil {
		return nil
	}
	return &proto.GroupTaskMerge{Order: g.ids(m.Order), Merged: g.ids(m.Merged), State: m.State, Why: m.Why,
		RemoveTask: m.RemoveTask, Armed: m.Armed}
}

func (g *groupBook) taskInfo(t savedTask) proto.GroupTask {
	return proto.GroupTask{Branch: t.Branch, Folder: t.Folder, Projects: g.ids(t.Projects),
		Panes: t.Panes, Each: t.Each, Created: t.Created, Merge: g.mergeInfo(t.Merge)}
}

// findTask is the group's task on branch; g.mu held.
func (g *groupBook) findTask(group, branch string) (*savedTask, *proto.Error) {
	gi := slices.IndexFunc(g.groups, func(sg savedGroup) bool { return sg.Name == group })
	if gi < 0 {
		return nil, proto.Errorf(proto.ErrNotFound, "no group called %s", group)
	}
	ti := slices.IndexFunc(g.groups[gi].Tasks, func(t savedTask) bool { return t.Branch == branch })
	if ti < 0 {
		return nil, proto.Errorf(proto.ErrNotFound, "%s has no task on %s", group, branch)
	}
	return &g.groups[gi].Tasks[ti], nil
}

// isTaskFolder says whether path (or real, the same with its links
// resolved) is a task's folder.
func (g *groupBook) isTaskFolder(path, real string) bool {
	g.mu.Lock()
	var folders []string
	for _, sg := range g.groups {
		for _, t := range sg.Tasks {
			if t.Folder != "" {
				folders = append(folders, t.Folder)
			}
		}
	}
	g.mu.Unlock()
	for _, f := range folders {
		r, err := filepath.EvalSymlinks(f)
		if err != nil {
			r = f
		}
		if f == path || f == real || r == path || r == real {
			return true
		}
	}
	return false
}

func mergeKey(group, branch string) string { return group + "\x00" + branch }

// armMerge arms Merge All When Green on a task, or with Stop disarms it.
// Arming again after it stopped keeps what was merged already.
func (g *groupBook) armMerge(mp proto.GroupTaskMergeParams) (proto.GroupTask, *proto.Error) {
	g.mu.Lock()
	t, perr := g.findTask(mp.Group, mp.Branch)
	if perr != nil {
		g.mu.Unlock()
		return proto.GroupTask{}, perr
	}
	if mp.Stop {
		if t.Merge != nil && t.Merge.State != proto.MergeMerged {
			t.Merge.State, t.Merge.Why = proto.MergeStopped, "stopped by hand"
		}
	} else {
		if t.Merge != nil && t.Merge.State == proto.MergeMerged {
			g.mu.Unlock()
			return proto.GroupTask{}, proto.Errorf(proto.ErrBadRequest, "every pull request of %s is merged already", mp.Branch)
		}
		order := t.Projects
		if len(mp.Order) > 0 {
			order = nil
			for _, id := range mp.Order {
				p, perr := g.s.projects.get(id)
				if perr != nil {
					g.mu.Unlock()
					return proto.GroupTask{}, perr
				}
				if !slices.Contains(t.Projects, p.root) {
					g.mu.Unlock()
					return proto.GroupTask{}, proto.Errorf(proto.ErrBadRequest, "%s is not one of the task's repos", p.info.Name)
				}
				if !slices.Contains(order, p.root) {
					order = append(order, p.root)
				}
			}
			// Every repo the task has, or one would be left unmerged
			// while the task said it was done.
			for _, root := range t.Projects {
				if !slices.Contains(order, root) && g.registered(projectID(root)) {
					g.mu.Unlock()
					return proto.GroupTask{}, proto.Errorf(proto.ErrBadRequest, "the order leaves out %s", filepath.Base(root))
				}
			}
		}
		var merged []string
		if t.Merge != nil {
			merged = t.Merge.Merged
		}
		t.Merge = &savedMerge{Order: order, Merged: merged, State: proto.MergeWaiting, RemoveTask: mp.RemoveTask, Armed: time.Now()}
	}
	info := g.taskInfo(*t)
	g.mu.Unlock()
	g.changed()
	go g.merges() // now, rather than at the next tick
	return info, nil
}

// merges takes the next step of every armed task.
func (g *groupBook) merges() {
	type job struct {
		group, branch string
		order, merged []string
	}
	g.mu.Lock()
	var jobs []job
	for _, sg := range g.groups {
		for _, t := range sg.Tasks {
			m := t.Merge
			if m == nil || (m.State != proto.MergeWaiting && m.State != proto.MergeMerging) || g.merging[mergeKey(sg.Name, t.Branch)] {
				continue
			}
			jobs = append(jobs, job{sg.Name, t.Branch, slices.Clone(m.Order), slices.Clone(m.Merged)})
		}
	}
	g.mu.Unlock()
	for _, j := range jobs {
		g.mergeStep(j.group, j.branch, j.order, j.merged)
	}
}

// mergeStep looks at a task's pull requests in order: what still holds it,
// or the next one to merge, or that every one is merged.
func (g *groupBook) mergeStep(group, branch string, order, merged []string) {
	var (
		next   *project
		nextPR proto.PRInfo
		landed []string // merged since, by hand or by GitHub
		why    string
	)
	// Every repo is looked at, not only up to the first that holds: one
	// further on may have been merged by hand meanwhile. What is said is
	// the first that holds, in the order they merge.
	for _, root := range order {
		if slices.Contains(merged, root) {
			continue
		}
		p, perr := g.s.projects.get(projectID(root))
		if perr != nil {
			if why == "" {
				why = filepath.Base(root) + " is no longer in conch"
			}
			continue
		}
		p.mu.Lock()
		info := p.info
		pr, have := p.prs[branch]
		p.mu.Unlock()
		hold := ""
		switch {
		case have && pr.State == "MERGED":
			landed = append(landed, root)
			continue
		case !have || pr.State != "OPEN":
			hold = fmt.Sprintf("%s has no open pull request on %s", info.Name, branch)
		case pr.Checks == "fail":
			hold = fmt.Sprintf("%s #%d has a failing check", info.Name, pr.Number)
		case pr.Checks != "pass":
			hold = fmt.Sprintf("%s #%d is waiting on its checks", info.Name, pr.Number)
		default:
			if h := g.s.mergeHold(info, branch, pr, false); h != "" {
				hold = info.Name + ": " + h
			}
		}
		if why == "" {
			why = hold
		}
		if hold == "" && next == nil {
			next, nextPR = p, pr
		}
	}

	key := mergeKey(group, branch)
	g.mu.Lock()
	t, perr := g.findTask(group, branch)
	if perr != nil || t.Merge == nil || (t.Merge.State != proto.MergeWaiting && t.Merge.State != proto.MergeMerging) || g.merging[key] {
		g.mu.Unlock()
		return
	}
	m := t.Merge
	changed, start, finish := false, false, false
	for _, root := range landed {
		if !slices.Contains(m.Merged, root) {
			m.Merged = append(m.Merged, root)
			changed = true
		}
	}
	switch {
	case why != "":
		if m.Why != why {
			m.Why = why
			changed = true
			log.Printf("group task %s %s: merge all waits: %s", group, branch, why)
		}
	case next == nil:
		m.State, m.Why = proto.MergeMerged, ""
		changed, finish = true, m.RemoveTask
	default:
		if m.State != proto.MergeMerging || m.Why != "" {
			m.State, m.Why = proto.MergeMerging, ""
			changed = true
		}
		if g.merging == nil {
			g.merging = map[string]bool{}
		}
		g.merging[key] = true
		start = true
	}
	g.mu.Unlock()
	if changed {
		g.changed()
	}
	if start {
		go g.mergeOne(group, branch, next, nextPR)
	}
	if finish {
		go g.tidy(group, branch)
	}
}

// mergeOne merges one repo's pull request and records how it went.
func (g *groupBook) mergeOne(group, branch string, p *project, pr proto.PRInfo) {
	_, perr := g.s.projects.mergePR(proto.BranchPRMergeParams{ProjectID: p.id, Branch: branch, Number: pr.Number, Head: pr.Head})
	g.mu.Lock()
	delete(g.merging, mergeKey(group, branch))
	if t, err := g.findTask(group, branch); err == nil && t.Merge != nil {
		if perr != nil {
			t.Merge.State = proto.MergeStopped
			t.Merge.Why = fmt.Sprintf("GitHub would not merge %s #%d: %s", p.info.Name, pr.Number, perr.Message)
		} else if !slices.Contains(t.Merge.Merged, p.root) {
			t.Merge.Merged = append(t.Merge.Merged, p.root)
		}
	}
	g.mu.Unlock()
	if perr != nil {
		log.Printf("group task %s %s: merge all stopped: %s", group, branch, perr.Message)
	} else {
		log.Printf("group task %s %s: merged %s #%d", group, branch, p.info.Name, pr.Number)
	}
	g.changed()
	if perr == nil {
		g.merges() // the next one, not at the next tick
	}
}

// tidy takes a task away once all of it is merged, as it was asked to:
// each repo's worktree and branch — refused, as any discard is, where
// that would lose work, and then left — and the task folder.
func (g *groupBook) tidy(group, branch string) {
	g.mu.Lock()
	t, perr := g.findTask(group, branch)
	var roots []string
	if perr == nil {
		roots = slices.Clone(t.Projects)
	}
	g.mu.Unlock()
	if perr != nil {
		return
	}
	for _, root := range roots {
		if _, err := g.s.projects.discardBranch(proto.BranchDiscardParams{ProjectID: projectID(root), Branch: branch}); err != nil {
			log.Printf("group task %s %s: kept %s's branch: %s", group, branch, filepath.Base(root), err.Message)
		}
	}
	if err := g.removeTask(proto.GroupTaskRef{Group: group, Branch: branch}); err != nil {
		log.Printf("group task %s %s: %s", group, branch, err.Message)
	}
}
