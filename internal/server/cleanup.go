package server

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// cleanupTimeout bounds reading or removing every worktree of a project.
const cleanupTimeout = 2 * time.Minute

// staleWorktrees lists a project's linked worktrees with what removing each
// would lose and why it looks finished.
func (pm *projectManager) staleWorktrees(id string) (proto.WorktreeStale, *proto.Error) {
	out := proto.WorktreeStale{Worktrees: []proto.StaleWorktree{}}
	p, perr := pm.gitProject(id)
	if perr != nil {
		return out, perr
	}
	info := p.snapshot()
	branches := map[string]proto.BranchInfo{}
	for _, b := range info.Branches {
		branches[b.Name] = b
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	info.Base = p.baseBranch(ctx)
	wts, err := gitx.Worktrees(ctx, p.root)
	if err != nil {
		return out, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	for _, wt := range wts {
		if wt.Main {
			continue
		}
		sw := proto.StaleWorktree{Path: wt.Path, Branch: wt.Branch, Base: wt.Branch != "" && wt.Branch == info.Base,
			Missing: wt.Prunable, Locked: wt.Locked}
		if b, known := branches[wt.Branch]; known {
			sw.Gone, sw.Committed, sw.PR = b.Gone, b.Committed, b.PR
		}
		if !sw.Missing {
			// Mid-rebase, git has detached HEAD; the rebase still names
			// the branch, and the worktree is anything but finished.
			op, rebasing := gitx.Operation(ctx, wt.Path)
			sw.Operation = op
			if wt.Branch == "" && rebasing != "" {
				sw.Branch = rebasing
			}
		}
		readable := true
		if !sw.Missing {
			sw.Panes = pm.s.panesIn(wt.Path)
			files, commits, err := p.losses(ctx, info.Base, wt.Branch, wt.Path)
			if err != nil {
				readable = false
				sw.Reasons = append(sw.Reasons, "can't read its status")
			}
			sw.Uncommitted, sw.Unmerged = len(files), commits
		} else if wt.Branch != "" && !sw.Base {
			_, sw.Unmerged, _ = p.losses(ctx, info.Base, wt.Branch, "")
		}

		finished := false
		note := func(reason string) {
			sw.Reasons = append(sw.Reasons, reason)
			finished = true
		}
		if sw.Missing {
			note("folder gone")
		}
		switch {
		case sw.Operation != "":
		case wt.Branch == "":
			note("detached")
		case sw.Base:
		case gitx.Ahead(ctx, p.root, wt.Branch, info.Base) == 0:
			sw.Merged = true
			note("no commits of its own")
		case gitx.Merged(ctx, p.root, wt.Branch, info.Base):
			sw.Merged = true
			note("merged into " + info.Base)
		}
		if sw.PR != nil && (sw.PR.State == "MERGED" || sw.PR.State == "CLOSED") {
			note("pull request " + strings.ToLower(sw.PR.State))
		}
		if sw.Gone {
			note("upstream deleted")
		}
		sw.Suggested = finished && readable && sw.Operation == "" && !sw.Base && !sw.Locked && !sw.Panes && sw.Uncommitted == 0 && sw.Unmerged == 0
		out.Worktrees = append(out.Worktrees, sw)
	}
	return out, nil
}

// cleanupWorktrees removes the listed worktrees one by one, each checked
// again first, and deletes their branches (never the base).
func (pm *projectManager) cleanupWorktrees(cp proto.WorktreeCleanupParams) (proto.WorktreeCleanupResult, *proto.Error) {
	var res proto.WorktreeCleanupResult
	p, perr := pm.gitProject(cp.ProjectID)
	if perr != nil {
		return res, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	base := p.baseBranch(ctx)
	wts, err := gitx.Worktrees(ctx, p.root)
	if err != nil {
		return res, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	byPath := map[string]gitx.Worktree{}
	for _, wt := range wts {
		byPath[wt.Path] = wt
	}
	fail := func(path, format string, args ...any) {
		res.Failed = append(res.Failed, proto.CleanupFailure{Path: path, Error: fmt.Sprintf(format, args...)})
	}
	// Folders already gone are pruned together at the end; their branches
	// go after that, since git counts them as checked out until then.
	type missing struct {
		path, branch string
		force        bool
	}
	var pruned []missing
	for _, item := range cp.Remove {
		wt, ok := byPath[realPath(item.Path)]
		switch {
		case !ok:
			fail(item.Path, "not a worktree of %s", p.root)
			continue
		case wt.Main:
			fail(item.Path, "the main checkout is never removed")
			continue
		case wt.Locked:
			fail(item.Path, "locked; unlock it with git worktree unlock")
			continue
		case wt.Prunable:
			pruned = append(pruned, missing{wt.Path, wt.Branch, item.Force})
			continue
		case pm.s.panesIn(wt.Path):
			fail(item.Path, "panes are still running in it")
			continue
		}
		if op := gitx.InProgress(ctx, wt.Path); op != "" && !item.Force {
			fail(item.Path, "a %s is in progress in it; finish or abort it first", op)
			continue
		}
		branch := wt.Branch
		if branch == base {
			branch = "" // the base branch stays
		}
		// With no branch to delete, commits count only when no branch has them.
		files, commits, err := p.losses(ctx, base, branch, wt.Path)
		if err != nil {
			fail(item.Path, "%v", err)
			continue
		}
		if loss := lossText(proto.BranchDiscardResult{Uncommitted: files, Unmerged: commits}); loss != "" && !item.Force {
			fail(item.Path, "would lose %s", loss)
			continue
		}
		remove := gitx.RemoveWorktree
		if item.Force {
			remove = gitx.ForceRemoveWorktree
		}
		if err := remove(ctx, p.root, wt.Path); err != nil {
			fail(item.Path, "%v", err)
			continue
		}
		if branch != "" {
			if err := gitx.DeleteBranch(ctx, p.root, branch); err != nil {
				fail(item.Path, "removed the worktree, but not branch %s: %v", branch, err)
				continue
			}
		}
		res.Removed = append(res.Removed, wt.Path)
		pm.s.closeDoneReviewers(p.id, wt.Branch, wt.Path)
	}
	if len(pruned) > 0 {
		if err := gitx.PruneWorktrees(ctx, p.root); err != nil {
			for _, m := range pruned {
				fail(m.path, "%v", err)
			}
			pruned = nil
		}
	}
	for _, m := range pruned {
		if m.branch != "" && m.branch != base {
			if _, commits, _ := p.losses(ctx, base, m.branch, ""); commits > 0 && !m.force {
				fail(m.path, "pruned, but kept branch %s: it has %s", m.branch, plural(commits, "commit")+" not merged or pushed")
				continue
			}
			if err := gitx.DeleteBranch(ctx, p.root, m.branch); err != nil {
				fail(m.path, "pruned, but not branch %s: %v", m.branch, err)
				continue
			}
		}
		res.Removed = append(res.Removed, m.path)
	}
	pm.request(p)
	if len(res.Removed) > 0 {
		log.Printf("project %s: cleaned up %d worktrees", p.id, len(res.Removed))
	}
	return res, nil
}

// realPath resolves symlinks when the path exists, as git's worktree list
// does, so /tmp and /private/tmp name the same worktree.
func realPath(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return filepath.Clean(path)
}

// staleDays is how old a branch's last commit is before branch.stale lists
// it, when the caller names no age.
const staleDays = 30

// staleBranches lists a project's local branches with no commit in the
// last days, oldest first, with what deleting each would lose. A branch
// checked out somewhere is listed with where, since it goes with its
// worktree rather than on its own.
func (pm *projectManager) staleBranches(sp proto.BranchStaleParams) (proto.BranchStale, *proto.Error) {
	days := sp.Days
	if days <= 0 {
		days = staleDays
	}
	out := proto.BranchStale{Days: days, Branches: []proto.StaleBranch{}}
	p, perr := pm.gitProject(sp.ProjectID)
	if perr != nil {
		return out, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	base := p.baseBranch(ctx)
	branches, err := gitx.Branches(ctx, p.root, base)
	if err != nil {
		return out, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	checkedOut, perr := checkedOutBranches(ctx, p.root)
	if perr != nil {
		return out, perr
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	for i := len(branches) - 1; i >= 0; i-- { // Branches is newest first
		b := branches[i]
		if b.Committed.IsZero() || !b.Committed.Before(cutoff) {
			continue
		}
		sb := proto.StaleBranch{Name: b.Name, Committed: b.Committed, Subject: b.Subject, Upstream: b.Upstream, Gone: b.Gone,
			Base: b.Name == base, Worktree: checkedOut[b.Name]}
		if !sb.Base {
			sb.Merged = gitx.Merged(ctx, p.root, b.Name, base)
			_, sb.Unmerged, _ = p.losses(ctx, base, b.Name, "")
		}
		sb.Suggested = !sb.Base && sb.Worktree == "" && sb.Unmerged == 0
		out.Branches = append(out.Branches, sb)
	}
	return out, nil
}

// checkedOutBranches is where each branch is checked out, the main checkout
// among them.
func checkedOutBranches(ctx context.Context, root string) (map[string]string, *proto.Error) {
	wts, err := gitx.Worktrees(ctx, root)
	if err != nil {
		return nil, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	at := map[string]string{}
	for _, wt := range wts {
		if wt.Branch != "" {
			at[wt.Branch] = wt.Path
		} else if wt.Detached && !wt.Prunable {
			// A branch being rebased is as checked out as any other.
			if b := gitx.RebasingBranch(ctx, wt.Path); b != "" {
				at[b] = wt.Path
			}
		}
	}
	return at, nil
}

// pruneBranches deletes the named local branches, each checked again first:
// never the base, never one checked out, and never one whose commits would
// be lost unless that one is forced.
func (pm *projectManager) pruneBranches(bp proto.BranchPruneParams) (proto.BranchPruneResult, *proto.Error) {
	var res proto.BranchPruneResult
	p, perr := pm.gitProject(bp.ProjectID)
	if perr != nil {
		return res, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	base := p.baseBranch(ctx)
	checkedOut, perr := checkedOutBranches(ctx, p.root)
	if perr != nil {
		return res, perr
	}
	fail := func(name, format string, args ...any) {
		res.Failed = append(res.Failed, proto.CleanupFailure{Path: name, Error: fmt.Sprintf(format, args...)})
	}
	for _, b := range bp.Branches {
		if _, err := gitx.BranchHead(ctx, p.root, b.Name); err != nil || b.Name == "" || strings.HasPrefix(b.Name, "-") {
			fail(b.Name, "no such branch")
			continue
		}
		if b.Name == base {
			fail(b.Name, "the base branch is never deleted")
			continue
		}
		if at := checkedOut[b.Name]; at != "" {
			fail(b.Name, "checked out in %s; remove that worktree instead", at)
			continue
		}
		if _, commits, _ := p.losses(ctx, base, b.Name, ""); commits > 0 && !b.Force {
			fail(b.Name, "would lose %s not merged or pushed", plural(commits, "commit"))
			continue
		}
		if err := gitx.DeleteBranch(ctx, p.root, b.Name); err != nil {
			fail(b.Name, "%v", err)
			continue
		}
		res.Deleted = append(res.Deleted, b.Name)
	}
	pm.request(p)
	if len(res.Deleted) > 0 {
		log.Printf("project %s: deleted %d old branches", p.id, len(res.Deleted))
	}
	return res, nil
}
