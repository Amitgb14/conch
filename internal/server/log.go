package server

import (
	"context"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// logTimeout bounds reading a project's history: a log of a few thousand
// commits takes milliseconds, so a long one is a repository in trouble.
const logTimeout = 20 * time.Second

// log is a project's history for a commit graph, read from its main
// checkout: every branch and tag is reachable from there, worktrees
// included, since they share the repository.
func (pm *projectManager) log(lp proto.LogParams) (proto.LogResult, *proto.Error) {
	p, perr := pm.gitProject(lp.ProjectID)
	if perr != nil {
		return proto.LogResult{}, perr
	}
	limit := lp.Limit
	switch {
	case limit <= 0:
		limit = proto.LogLimit
	case limit > proto.LogLimitMax:
		limit = proto.LogLimitMax
	}
	ctx, cancel := context.WithTimeout(context.Background(), logTimeout)
	defer cancel()
	commits, more, err := gitx.Log(ctx, p.root, limit)
	if err != nil {
		return proto.LogResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	res := proto.LogResult{Commits: make([]proto.LogCommit, 0, len(commits)), More: more}
	for _, c := range commits {
		res.Commits = append(res.Commits, proto.LogCommit{Hash: c.Hash, Parents: c.Parents, Subject: c.Subject,
			Author: c.Author, Time: c.Time, Refs: c.Refs})
	}
	return res, nil
}
