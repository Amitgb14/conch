package server

import (
	"context"
	"log"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// A branch rebased — an agent asked to resolve its conflict with the base
// does this — and not yet pushed: read back for the person to review
// before it goes up over the branch it rewrote, or to put back.

// rebasedDiffMax caps the range-diff handed to a client: a rebase that
// rewrote a great deal is better read in a terminal than in a panel.
const rebasedDiffMax = 256 << 10

func (pm *projectManager) branchRebased(rp proto.BranchRebasedParams) (proto.BranchRebased, *proto.Error) {
	out := proto.BranchRebased{Rebases: []proto.BranchRebase{}}
	p, perr := pm.gitProject(rp.ProjectID)
	if perr != nil {
		return out, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wts, err := gitx.Worktrees(ctx, p.root)
	if err != nil {
		return out, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	base := p.baseBranch(ctx)
	seen := map[string]bool{}
	for _, wt := range wts {
		if wt.Branch == "" || wt.Prunable || wt.Branch == base || seen[wt.Branch] || (rp.Branch != "" && wt.Branch != rp.Branch) {
			continue
		}
		seen[wt.Branch] = true
		r, err := gitx.LastRebase(ctx, wt.Path, wt.Branch)
		if err != nil || r == nil {
			continue
		}
		remote := gitx.PushedHead(ctx, wt.Path, wt.Branch)
		if remote == r.After {
			continue // pushed already
		}
		b := proto.BranchRebase{Branch: r.Branch, Before: r.Before, After: r.After, Onto: r.Onto, At: r.At, Remote: remote}
		if cs, err := gitx.Rewritten(ctx, wt.Path, r); err == nil {
			for _, c := range cs {
				b.Commits = append(b.Commits, proto.RebasedCommit{Old: c.Old, New: c.New, Subject: c.Subject, Change: c.Change})
			}
		} else {
			log.Printf("project %s: reading the rebase of %s: %v", p.id, r.Branch, err)
		}
		if rp.Diff && rp.Branch != "" {
			if d, err := gitx.RewriteDiff(ctx, wt.Path, r, rebasedDiffMax); err == nil {
				b.Diff = d
			} else {
				return out, proto.Errorf(proto.ErrBadRequest, "%v", err)
			}
		}
		out.Rebases = append(out.Rebases, b)
	}
	return out, nil
}

func (pm *projectManager) undoRebase(up proto.BranchRebaseUndoParams) (struct{}, *proto.Error) {
	p, perr := pm.gitProject(up.ProjectID)
	if perr != nil {
		return struct{}{}, perr
	}
	if up.Before == "" || up.After == "" {
		return struct{}{}, proto.Errorf(proto.ErrBadRequest, "say where %s was before the rebase and after it", up.Branch)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wt, ok, perr := checkout(ctx, p, up.Branch)
	if perr != nil {
		return struct{}{}, perr
	}
	if !ok {
		return struct{}{}, proto.Errorf(proto.ErrBadRequest, "%s is not checked out here, so its rebase cannot be undone", up.Branch)
	}
	err := gitx.UndoRebase(ctx, wt.Path, up.Branch, up.Before, up.After)
	pm.request(p)
	if err != nil {
		return struct{}{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	log.Printf("project %s: put %s back where it was before its rebase", p.id, up.Branch)
	return struct{}{}, nil
}
