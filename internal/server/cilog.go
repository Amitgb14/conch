package server

import (
	"context"
	"errors"
	"time"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/proto"
)

// maxCILog is how much of a failed run's log is sent back: the end, where
// the error is. A log of a big test suite runs to megabytes.
const maxCILog = 64 << 10

// ciLog reads what a branch's failed CI run printed, through gh on this
// machine — the one with the checkout and the login — so the client can
// hand it to the branch's agent.
func (pm *projectManager) ciLog(lp proto.BranchCILogParams) (proto.BranchCILog, *proto.Error) {
	p, perr := pm.gitProject(lp.ProjectID)
	if perr != nil {
		return proto.BranchCILog{}, perr
	}
	if lp.Branch == "" {
		return proto.BranchCILog{}, proto.Errorf(proto.ErrBadRequest, "no branch given")
	}
	p.mu.Lock()
	head := p.prs[lp.Branch].Head
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	run, err := ghx.FailedLog(ctx, p.root, lp.Branch, head)
	switch {
	case errors.Is(err, ghx.ErrNoFailedRun):
		return proto.BranchCILog{}, proto.Errorf(proto.ErrNotFound,
			"no failed GitHub Actions run on %s: the failing check is another service's, or it was re-run — open the pull request to see it", lp.Branch)
	case err != nil:
		return proto.BranchCILog{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	log := run.Log
	if len(log) > maxCILog {
		log = log[len(log)-maxCILog:]
	}
	return proto.BranchCILog{Workflow: run.Workflow, URL: run.URL, Log: log}, nil
}
