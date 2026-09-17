package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// networkTimeout bounds git and gh calls that reach a remote.
const networkTimeout = 2 * time.Minute

// gitProject returns a project that is a git repository.
func (pm *projectManager) gitProject(id string) (*project, *proto.Error) {
	p, perr := pm.get(id)
	if perr != nil {
		return nil, perr
	}
	if !p.snapshot().Git {
		return nil, proto.Errorf(proto.ErrBadRequest, "%s is not a git repository", p.root)
	}
	return p, nil
}

// checkout finds where branch is checked out, asking git rather than the
// last refresh: a worktree made or removed a moment ago must count.
func checkout(ctx context.Context, p *project, branch string) (gitx.Worktree, bool, *proto.Error) {
	wts, err := gitx.Worktrees(ctx, p.root)
	if err != nil {
		return gitx.Worktree{}, false, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	for _, wt := range wts {
		if wt.Branch == branch && !wt.Prunable {
			return wt, true, nil
		}
	}
	return gitx.Worktree{}, false, nil
}

// notBase refuses branch methods aimed at the project's base branch.
func notBase(p *project, branch string) (base string, perr *proto.Error) {
	base = p.snapshot().Base
	switch {
	case branch == "":
		return "", proto.Errorf(proto.ErrBadRequest, "no branch given")
	case branch == base || "origin/"+branch == base:
		return "", proto.Errorf(proto.ErrBadRequest, "%s is the base branch", branch)
	}
	return base, nil
}

func (pm *projectManager) commitBranch(cp proto.BranchCommitParams) (proto.CommitResult, *proto.Error) {
	p, perr := pm.gitProject(cp.ProjectID)
	if perr != nil {
		return proto.CommitResult{}, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	wt, ok, perr := checkout(ctx, p, cp.Branch)
	switch {
	case perr != nil:
		return proto.CommitResult{}, perr
	case !ok:
		return proto.CommitResult{}, proto.Errorf(proto.ErrBadRequest, "%s is not checked out anywhere, so it has nothing uncommitted", cp.Branch)
	}
	hash, err := gitx.CommitChanges(ctx, wt.Path, cp.Message, cp.Files)
	pm.request(p)
	if err != nil {
		return proto.CommitResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	log.Printf("project %s: committed %s on %s", p.id, short(hash), cp.Branch)
	return proto.CommitResult{Hash: hash}, nil
}

func (pm *projectManager) pushBranch(br proto.BranchRef) *proto.Error {
	p, perr := pm.gitProject(br.ProjectID)
	if perr != nil {
		return perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	err := gitx.Push(ctx, p.root, br.Branch)
	pm.request(p)
	if err != nil {
		return proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	pm.requestPRs(p)
	log.Printf("project %s: pushed %s", p.id, br.Branch)
	return nil
}

// openPR pushes the branch and opens a pull request into the base.
func (pm *projectManager) openPR(pp proto.BranchPRParams) (proto.BranchPRResult, *proto.Error) {
	p, perr := pm.gitProject(pp.ProjectID)
	if perr != nil {
		return proto.BranchPRResult{}, perr
	}
	base, perr := notBase(p, pp.Branch)
	if perr != nil {
		return proto.BranchPRResult{}, perr
	}
	p.mu.Lock()
	pr, known := p.prs[pp.Branch]
	p.mu.Unlock()
	if known && pr.State == "OPEN" {
		return proto.BranchPRResult{}, proto.Errorf(proto.ErrBadRequest, "pull request #%d is already open for %s: %s", pr.Number, pp.Branch, pr.URL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	if err := gitx.Push(ctx, p.root, pp.Branch); err != nil {
		pm.request(p)
		return proto.BranchPRResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	// gh wants the base as a branch on GitHub, not a local remote-tracking name.
	url, err := ghx.Create(ctx, p.root, strings.TrimPrefix(base, "origin/"), pp.Branch, pp.Title, pp.Body, pp.Draft)
	pm.request(p)
	pm.requestPRs(p)
	if err != nil {
		return proto.BranchPRResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	log.Printf("project %s: opened %s", p.id, url)
	return proto.BranchPRResult{URL: url}, nil
}

// mergeBranch merges a branch into the base where the base is checked out.
func (pm *projectManager) mergeBranch(mp proto.BranchMergeParams) (proto.CommitResult, *proto.Error) {
	p, perr := pm.gitProject(mp.ProjectID)
	if perr != nil {
		return proto.CommitResult{}, perr
	}
	base, perr := notBase(p, mp.Branch)
	if perr != nil {
		return proto.CommitResult{}, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	into, ok, perr := checkout(ctx, p, base)
	switch {
	case perr != nil:
		return proto.CommitResult{}, perr
	case !ok:
		return proto.CommitResult{}, proto.Errorf(proto.ErrBadRequest, "%s isn't checked out anywhere; check it out to merge into it", base)
	}
	wt, ok, perr := checkout(ctx, p, mp.Branch)
	if perr != nil {
		return proto.CommitResult{}, perr
	}
	if ok {
		files, err := gitx.StatusFiles(ctx, wt.Path)
		if err != nil {
			return proto.CommitResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		if len(files) > 0 {
			return proto.CommitResult{}, proto.Errorf(proto.ErrBadRequest,
				"%s has %s; commit or discard them first, a merge only takes commits", mp.Branch, plural(len(files), "uncommitted file"))
		}
	}
	hash, err := gitx.Merge(ctx, into.Path, mp.Branch, mp.Squash, mp.Message)
	pm.request(p)
	if err != nil {
		return proto.CommitResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	log.Printf("project %s: merged %s into %s (%s)", p.id, mp.Branch, base, short(hash))
	return proto.CommitResult{Hash: hash, Into: base}, nil
}

// discardBranch removes a branch's linked worktree and deletes the branch.
// Unless forced it refuses to lose uncommitted files or commits that are in
// neither the base nor an upstream.
func (pm *projectManager) discardBranch(dp proto.BranchDiscardParams) (proto.BranchDiscardResult, *proto.Error) {
	var res proto.BranchDiscardResult
	p, perr := pm.gitProject(dp.ProjectID)
	if perr != nil {
		return res, perr
	}
	base, perr := notBase(p, dp.Branch)
	if perr != nil {
		return res, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	wt, checkedOut, perr := checkout(ctx, p, dp.Branch)
	if perr != nil {
		return res, perr
	}
	if checkedOut {
		switch {
		case wt.Main:
			return res, proto.Errorf(proto.ErrBadRequest, "%s is checked out in the main checkout; switch it to another branch first", dp.Branch)
		case pm.s.panesIn(wt.Path):
			return res, proto.Errorf(proto.ErrBadRequest, "panes are still running in %s; close them first", wt.Path)
		}
		res.Worktree = wt.Path
		files, err := gitx.StatusFiles(ctx, wt.Path)
		if err != nil {
			return res, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		for _, f := range files {
			res.Uncommitted = append(res.Uncommitted, f.Path)
		}
	}
	res.Unmerged = gitx.Unmerged(ctx, p.root, dp.Branch, base)
	if res.Unmerged > 0 && p.mergedPR(ctx, dp.Branch) {
		res.Unmerged = 0
	}
	if dp.DryRun {
		return res, nil
	}
	if !dp.Force && (len(res.Uncommitted) > 0 || res.Unmerged > 0) {
		return res, proto.Errorf(proto.ErrBadRequest, "discarding %s would lose %s", dp.Branch, lossText(res))
	}
	if checkedOut {
		remove := gitx.RemoveWorktree
		if dp.Force {
			remove = gitx.ForceRemoveWorktree
		}
		if err := remove(ctx, p.root, wt.Path); err != nil {
			pm.request(p)
			return res, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
	}
	err := gitx.DeleteBranch(ctx, p.root, dp.Branch)
	pm.request(p)
	if err != nil {
		if checkedOut {
			return res, proto.Errorf(proto.ErrBadRequest, "removed the worktree, but not the branch: %v", err)
		}
		return res, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	log.Printf("project %s: discarded %s", p.id, dp.Branch)
	res.Done = true
	return res, nil
}

// mergedPR reports whether the branch's pull request was merged with the
// branch's current commit, so deleting the branch loses nothing.
func (p *project) mergedPR(ctx context.Context, branch string) bool {
	p.mu.Lock()
	pr, ok := p.prs[branch]
	p.mu.Unlock()
	if !ok || pr.State != "MERGED" || pr.Head == "" {
		return false
	}
	head, err := gitx.BranchHead(ctx, p.root, branch)
	return err == nil && head == pr.Head
}

// lossText describes what discarding a branch loses.
func lossText(r proto.BranchDiscardResult) string {
	var parts []string
	if n := len(r.Uncommitted); n > 0 {
		parts = append(parts, plural(n, "uncommitted file"))
	}
	if r.Unmerged > 0 {
		parts = append(parts, plural(r.Unmerged, "commit")+" not merged or pushed")
	}
	return strings.Join(parts, " and ")
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func short(hash string) string {
	return hash[:min(len(hash), 7)]
}
