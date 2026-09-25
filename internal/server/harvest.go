package server

import (
	"context"
	"errors"
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

// maxPatch caps a commit's patch, which travels over the protocol.
const maxPatch = 4 << 20

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

// baseBranch is the project's base. A project added moments ago has not
// been refreshed yet, so git is asked when the snapshot has no base.
func (p *project) baseBranch(ctx context.Context) string {
	if base := p.snapshot().Base; base != "" {
		return base
	}
	return gitx.DefaultBase(ctx, p.root)
}

// notBase refuses branch methods aimed at the project's base branch.
func notBase(ctx context.Context, p *project, branch string) (base string, perr *proto.Error) {
	base = p.baseBranch(ctx)
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
	var hash string
	var err error
	if cp.Patch != "" {
		if len(cp.Patch) > maxPatch {
			return proto.CommitResult{}, proto.Errorf(proto.ErrBadRequest, "the patch is too large (%d bytes)", len(cp.Patch))
		}
		hash, err = gitx.CommitPatchChanges(ctx, wt.Path, cp.Message, cp.Patch, cp.Files)
	} else {
		hash, err = gitx.CommitChanges(ctx, wt.Path, cp.Message, cp.Files)
	}
	pm.request(p)
	if err != nil {
		return proto.CommitResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	log.Printf("project %s: committed %s on %s", p.id, short(hash), cp.Branch)
	return proto.CommitResult{Hash: hash}, nil
}

func (pm *projectManager) pushBranch(pp proto.BranchPushParams) (proto.BranchPushResult, *proto.Error) {
	p, perr := pm.gitProject(pp.ProjectID)
	if perr != nil {
		return proto.BranchPushResult{}, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	var took int
	var err error
	if pp.Rebase {
		// A rebase needs the branch checked out; pushing alone does not.
		wt, ok := p.branchWorktree(pp.Branch)
		if !ok {
			pm.request(p)
			return proto.BranchPushResult{}, proto.Errorf(proto.ErrBadRequest,
				"%s is not checked out here, so its commits cannot be rebased onto the remote's", pp.Branch)
		}
		took, err = gitx.PushRebase(ctx, wt.Path, pp.Branch)
	} else {
		err = gitx.Push(ctx, p.root, pp.Branch)
	}
	pm.request(p)
	if err != nil {
		var rej *gitx.PushRejectedError
		var conflict *gitx.RebaseConflictError
		switch {
		case errors.As(err, &rej):
			return proto.BranchPushResult{}, proto.Errorf(proto.ErrPushRejected, "%v", err)
		case errors.As(err, &conflict):
			return proto.BranchPushResult{}, proto.Errorf(proto.ErrRebaseConflict, "%v", err)
		}
		return proto.BranchPushResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	pm.requestPRs(p)
	if took > 0 {
		log.Printf("project %s: pushed %s after taking %d commit(s) from the remote", p.id, pp.Branch, took)
	} else {
		log.Printf("project %s: pushed %s", p.id, pp.Branch)
	}
	return proto.BranchPushResult{Took: took}, nil
}

// openPR pushes the branch and opens a pull request into the base.
func (pm *projectManager) openPR(pp proto.BranchPRParams) (proto.BranchPRResult, *proto.Error) {
	p, perr := pm.gitProject(pp.ProjectID)
	if perr != nil {
		return proto.BranchPRResult{}, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	base, perr := notBase(ctx, p, pp.Branch)
	if perr != nil {
		return proto.BranchPRResult{}, perr
	}
	p.mu.Lock()
	pr, known := p.prs[pp.Branch]
	p.mu.Unlock()
	if known && pr.State == "OPEN" {
		return proto.BranchPRResult{}, proto.Errorf(proto.ErrBadRequest, "pull request #%d is already open for %s: %s", pr.Number, pp.Branch, pr.URL)
	}
	if err := gitx.Push(ctx, p.root, pp.Branch); err != nil {
		pm.request(p)
		var rej *gitx.PushRejectedError
		if errors.As(err, &rej) {
			return proto.BranchPRResult{}, proto.Errorf(proto.ErrPushRejected,
				"%v, so there is nothing to open a pull request from yet; push it first", err)
		}
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
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	base, perr := notBase(ctx, p, mp.Branch)
	if perr != nil {
		return proto.CommitResult{}, perr
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	base, perr := notBase(ctx, p, dp.Branch)
	if perr != nil {
		return res, perr
	}
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
	}
	var err error
	if res.Uncommitted, res.Unmerged, err = p.losses(ctx, base, dp.Branch, res.Worktree); err != nil {
		return res, proto.Errorf(proto.ErrBadRequest, "%v", err)
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
	err = gitx.DeleteBranch(ctx, p.root, dp.Branch)
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

// losses reads what removing the checkout at dir ("" for none) and deleting
// branch ("" when detached) would lose: uncommitted files, and commits kept
// nowhere else.
func (p *project) losses(ctx context.Context, base, branch, dir string) (uncommitted []string, commits int, err error) {
	if dir != "" {
		files, err := gitx.StatusFiles(ctx, dir)
		if err != nil {
			return nil, 0, err
		}
		for _, f := range files {
			uncommitted = append(uncommitted, f.Path)
		}
	}
	switch {
	case branch != "":
		commits = gitx.Unmerged(ctx, p.root, branch, base)
		if commits > 0 && p.mergedPR(ctx, branch) {
			commits = 0
		}
	case dir != "":
		commits = gitx.Unreachable(ctx, dir)
	}
	return uncommitted, commits, nil
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

// resolve says which project and checkout path is in. The worktrees are read
// from git, not from the last refresh, so a checkout made a moment ago (a
// task's, say) is already known.
func (pm *projectManager) resolve(path string) (proto.ProjectPlace, *proto.Error) {
	p, err := pm.add(path, true)
	if err != nil {
		return proto.ProjectPlace{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	info := p.snapshot()
	place := proto.ProjectPlace{ProjectID: p.id, Root: p.root, Git: info.Git, Base: info.Base}
	if !info.Git {
		return place, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	if place.Base == "" {
		place.Base = gitx.DefaultBase(ctx, p.root)
	}
	wts, err := gitx.Worktrees(ctx, p.root)
	if err != nil {
		return place, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	dir := realPath(path)
	for _, wt := range wts {
		if within(dir, wt.Path) && len(wt.Path) > len(place.Worktree) {
			place.Worktree, place.Branch = wt.Path, wt.Branch
		}
	}
	return place, nil
}
