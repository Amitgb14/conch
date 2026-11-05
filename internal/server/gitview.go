package server

import (
	"context"
	"log"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// The commit graph's side panel (project.commit.v1) and reviewing a
// branch's pull request (pr.review.v1), for Conch.app's git view.

// commitDetail answers project.commit: a commit, a range, or one file's
// diff in either, read from the project's main checkout, which shares the
// repository with every worktree.
func (pm *projectManager) commitDetail(cp proto.CommitParams) (proto.CommitDetail, *proto.Error) {
	p, perr := pm.gitProject(cp.ProjectID)
	if perr != nil {
		return proto.CommitDetail{}, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), logTimeout)
	defer cancel()
	if cp.File != "" {
		diff, err := gitx.CommitFileDiff(ctx, p.root, cp.From, cp.Hash, cp.File)
		if err != nil {
			return proto.CommitDetail{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		return proto.CommitDetail{Hash: cp.Hash, Diff: diff}, nil
	}
	if cp.From != "" {
		c, err := gitx.CompareCommits(ctx, p.root, cp.From, cp.Hash)
		if err != nil {
			return proto.CommitDetail{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		return proto.CommitDetail{Hash: cp.Hash, Files: fileStats(c.Files), Commits: c.Commits}, nil
	}
	d, err := gitx.ShowCommit(ctx, p.root, cp.Hash)
	if err != nil {
		return proto.CommitDetail{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	return proto.CommitDetail{Hash: d.Hash, Parents: d.Parents, Author: d.Author, Email: d.Email, Time: d.Time,
		Subject: d.Subject, Body: d.Body, Files: fileStats(d.Files), Commits: 1}, nil
}

func fileStats(fs []gitx.FileStat) []proto.FileChange {
	out := make([]proto.FileChange, 0, len(fs))
	for _, f := range fs {
		out = append(out, proto.FileChange{Path: f.Path, OrigPath: f.OrigPath, Code: f.Code, Added: f.Added, Deleted: f.Deleted, Binary: f.Binary})
	}
	return out
}

// reviewBranch submits a review of the branch's open pull request — the
// one conch last read for it — through gh.
func (pm *projectManager) reviewBranch(rp proto.BranchReviewParams) (proto.BranchReviewResult, *proto.Error) {
	p, perr := pm.gitProject(rp.ProjectID)
	if perr != nil {
		return proto.BranchReviewResult{}, perr
	}
	p.mu.Lock()
	pr, known := p.prs[rp.Branch]
	p.mu.Unlock()
	if !known || pr.State != "OPEN" {
		return proto.BranchReviewResult{}, proto.Errorf(proto.ErrBadRequest, "%s has no open pull request to review", rp.Branch)
	}
	comments := make([]ghx.LineComment, 0, len(rp.Comments))
	for _, c := range rp.Comments {
		comments = append(comments, ghx.LineComment{Path: c.File, Line: c.Line, Body: c.Note})
	}
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	err := ghx.Review(ctx, p.root, pr.Number, rp.Event, rp.Body, comments)
	pm.requestPRs(p)
	if err != nil {
		return proto.BranchReviewResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	log.Printf("project %s: reviewed pull request #%d (%s, %s)", p.id, pr.Number, rp.Branch, rp.Event)
	return proto.BranchReviewResult{PR: pr.Number, URL: pr.URL}, nil
}
