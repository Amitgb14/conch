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
		sw.Suggested = finished && readable && !sw.Base && !sw.Locked && !sw.Panes && sw.Uncommitted == 0 && sw.Unmerged == 0
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
