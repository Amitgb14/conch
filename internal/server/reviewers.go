package server

import (
	"log"

	"github.com/Amitgb14/conch/internal/proto"
)

// A reviewer ([verify] reviewer, started by a TUI) reads a branch and
// reports; once its report is there it has nothing more to do, and left
// running it was one more agent per branch reviewed, for ever. So the
// server closes a finished one when the branch it read is done with:
// merged, discarded, or its worktree removed. One still reading, asking
// or failed is left — somebody has to look at it — and the TUI closes
// one itself once it hands the report over.

// closeDoneReviewers closes the finished reviewers of projectID's branch:
// on it by name, or started inside dir. It says how many it closed.
func (s *Server) closeDoneReviewers(projectID, branch, dir string) int {
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.panes))
	for _, e := range s.panes {
		entries = append(entries, e)
	}
	s.mu.Unlock()
	n := 0
	for _, e := range entries {
		info := e.info()
		if !proto.ReviewerDone(info) || info.ProjectID != projectID {
			continue
		}
		if !(branch != "" && info.Branch == branch) && !(dir != "" && within(e.dir, dir)) {
			continue
		}
		if s.close(info.ID) == nil {
			log.Printf("pane %s: closed %s, its review done with", info.ID, info.Name)
			n++
		}
	}
	return n
}

// idleReviewer is a pane that does not keep a worktree in use: a finished
// reviewer, closed with the worktree rather than refusing it.
func idleReviewer(e *entry) bool { return proto.ReviewerDone(e.info()) }
