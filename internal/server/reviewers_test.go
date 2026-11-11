package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// agentOn starts the stand-in agent named name in branch's worktree,
// making the worktree first if there is none, and waits for conch to see
// the agent in it.
func agentOn(t *testing.T, s *Server, p *project, branch, name string) *entry {
	t.Helper()
	wt, ok := p.branchWorktree(branch)
	path := wt.Path
	if !ok {
		var perr *proto.Error
		if path, _, perr = s.projects.addWorktree(p, branch, ""); perr != nil {
			t.Fatal(perr)
		}
	}
	info, perr := s.create(proto.PaneCreateParams{Agent: "claude", Name: name, Cwd: path})
	if perr != nil {
		t.Fatal(perr)
	}
	e := a5Entry(t, s, info.ID)
	a5WaitFor(t, name+" seen as an agent on "+branch, func() bool {
		s.projects.refresh(p, "")
		s.observe(e)
		i := e.info()
		return i.Agent != nil && i.Branch == branch
	})
	return e
}

// finish has the agent in e take a turn and end it.
func finish(s *Server, e *entry) {
	hook(s, e, "UserPromptSubmit")
	hook(s, e, "Stop")
}

func running(s *Server, e *entry) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.panes[e.info().ID] == e
}

func TestReviewerDone(t *testing.T) {
	agent := func(state string, failed bool) *proto.AgentStatus {
		return &proto.AgentStatus{Name: "codex", State: state, Failed: failed}
	}
	rv := proto.ReviewerPrefix + "feat"
	for _, c := range []struct {
		p    proto.PaneInfo
		want bool
	}{
		{proto.PaneInfo{Name: rv, State: proto.PaneRunning, Agent: agent(proto.AgentDone, false)}, true},
		{proto.PaneInfo{Name: rv, State: proto.PaneRunning, Agent: agent(proto.AgentIdle, false)}, true},
		{proto.PaneInfo{Name: rv, State: proto.PaneRunning, Agent: agent(proto.AgentWorking, false)}, false},
		{proto.PaneInfo{Name: rv, State: proto.PaneRunning, Agent: agent(proto.AgentBlocked, false)}, false},
		{proto.PaneInfo{Name: rv, State: proto.PaneRunning, Agent: agent(proto.AgentDone, true)}, false},
		{proto.PaneInfo{Name: rv, State: proto.PaneRunning}, false},
		{proto.PaneInfo{Name: rv, State: proto.PaneExited, Agent: agent(proto.AgentDone, false)}, false},
		{proto.PaneInfo{Name: "feat", State: proto.PaneRunning, Agent: agent(proto.AgentDone, false)}, false},
		{proto.PaneInfo{Name: "my review · notes", State: proto.PaneRunning, Agent: agent(proto.AgentDone, false)}, false},
	} {
		if got := proto.ReviewerDone(c.p); got != c.want {
			t.Errorf("%q %s %+v: %v, want %v", c.p.Name, c.p.State, c.p.Agent, got, c.want)
		}
	}
}

// Removing a worktree closes the finished reviewer working in it — it no
// longer refuses the removal — and leaves one still reading, which does.
func TestRemoveWorktreeClosesItsDoneReviewer(t *testing.T) {
	s, p := afterFixture(t)
	rv := agentOn(t, s, p, "feat", proto.ReviewerPrefix+"feat")
	finish(s, rv)
	other := agentOn(t, s, p, "other", proto.ReviewerPrefix+"other")
	finish(s, other)
	wt, _ := p.branchWorktree("feat")

	if s.panesIn(wt.Path) {
		t.Fatal("a finished reviewer counted as a pane at work in the worktree")
	}
	if perr := s.projects.removeWorktree(proto.WorktreeRemoveParams{ProjectID: p.id, Path: wt.Path}); perr != nil {
		t.Fatal(perr)
	}
	if running(s, rv) {
		t.Fatal("the reviewer outlived its worktree")
	}
	if !running(s, other) {
		t.Fatal("another branch's reviewer was closed")
	}

	// Still reading: the worktree stays, and so does it.
	busy := agentOn(t, s, p, "busy", proto.ReviewerPrefix+"busy")
	hook(s, busy, "UserPromptSubmit")
	bwt, _ := p.branchWorktree("busy")
	perr := s.projects.removeWorktree(proto.WorktreeRemoveParams{ProjectID: p.id, Path: bwt.Path})
	if perr == nil || !strings.Contains(perr.Message, "running") || !running(s, busy) {
		t.Fatalf("a reviewer at work: %v, running %v", perr, running(s, busy))
	}
}

// The branch's own agent is never closed with it, finished or not: it is
// what refuses the removal, as before.
func TestRemoveWorktreeKeepsTheWriter(t *testing.T) {
	s, p := afterFixture(t)
	writer := agentOn(t, s, p, "feat", "claude")
	finish(s, writer)
	rv := agentOn(t, s, p, "feat", proto.ReviewerPrefix+"feat")
	finish(s, rv)
	wt, _ := p.branchWorktree("feat")
	if perr := s.projects.removeWorktree(proto.WorktreeRemoveParams{ProjectID: p.id, Path: wt.Path}); perr == nil {
		t.Fatal("removed a worktree its writer is in")
	}
	if !running(s, writer) || !running(s, rv) {
		t.Fatal("a refused removal closed panes")
	}
}

// Discarding a branch closes its finished reviewer, and so does a branch
// merged (by name: the worktree may be gone, or never have been).
func TestDiscardAndMergeCloseTheReviewer(t *testing.T) {
	s, p := afterFixture(t)
	rv := agentOn(t, s, p, "feat", proto.ReviewerPrefix+"feat")
	finish(s, rv)
	res, perr := s.projects.discardBranch(proto.BranchDiscardParams{ProjectID: p.id, Branch: "feat", Force: true})
	if perr != nil || !res.Done {
		t.Fatalf("discard: %+v %v", res, perr)
	}
	if running(s, rv) {
		t.Fatal("the reviewer outlived its discarded branch")
	}

	rv2 := agentOn(t, s, p, "next", proto.ReviewerPrefix+"next")
	finish(s, rv2)
	if n := s.closeDoneReviewers("someone-else", "next", ""); n != 0 || !running(s, rv2) {
		t.Fatal("closed another project's reviewer")
	}
	if n := s.closeDoneReviewers(p.id, "next", ""); n != 1 || running(s, rv2) {
		t.Fatalf("merged: closed %d", n)
	}
	if n := s.closeDoneReviewers(p.id, "next", ""); n != 0 {
		t.Fatalf("closed again: %d", n)
	}
}

// A branch merged on GitHub by anybody — gh, the web page — closes its
// finished reviewer the next time conch reads the pull requests; a branch
// whose pull request is still open, or was closed unmerged, keeps its.
func TestMergedOnGitHubClosesTheReviewer(t *testing.T) {
	s, p := afterFixture(t)
	merged := agentOn(t, s, p, "feat", proto.ReviewerPrefix+"feat")
	finish(s, merged)
	open := agentOn(t, s, p, "wip", proto.ReviewerPrefix+"wip")
	finish(s, open)
	closed := agentOn(t, s, p, "gone", proto.ReviewerPrefix+"gone")
	finish(s, closed)

	gh := filepath.Join(t.TempDir(), "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
case "$1 $2" in
"pr list") cat <<'JSON'
[{"number":7,"title":"Feat","state":"MERGED","isDraft":false,"url":"https://x/7","headRefName":"feat","reviewDecision":"","updatedAt":"2026-11-07T10:00:00Z","statusCheckRollup":[]},
 {"number":8,"title":"Wip","state":"OPEN","isDraft":false,"url":"https://x/8","headRefName":"wip","reviewDecision":"","updatedAt":"2026-11-07T10:00:00Z","statusCheckRollup":[]},
 {"number":9,"title":"Gone","state":"CLOSED","isDraft":false,"url":"https://x/9","headRefName":"gone","reviewDecision":"","updatedAt":"2026-11-07T10:00:00Z","statusCheckRollup":[]}]
JSON
;;
*) echo '{}' ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	s.projects.fetchPRs(p)
	if running(s, merged) {
		t.Fatal("the merged branch's reviewer is still running")
	}
	if !running(s, open) || !running(s, closed) {
		t.Fatalf("closed a reviewer of a branch not merged: open %v, closed %v", running(s, open), running(s, closed))
	}
	// Read again: nothing more goes.
	s.projects.fetchPRs(p)
	if !running(s, open) || !running(s, closed) {
		t.Fatal("a second read closed more")
	}
}

// A branch merged and then committed to again is work again: its finished
// reviewer has a report nobody has read, and stays. One still at the
// commit that was merged is done with, and its reviewer goes.
func TestMergedThenMovedOnKeepsTheReviewer(t *testing.T) {
	s, p := afterFixture(t)
	done := agentOn(t, s, p, "feat", proto.ReviewerPrefix+"feat")
	finish(s, done)
	again := agentOn(t, s, p, "again", proto.ReviewerPrefix+"again")
	finish(s, again)

	ctx := context.Background()
	featHead, err := gitx.BranchHead(ctx, p.root, "feat")
	if err != nil {
		t.Fatal(err)
	}
	// "again" was merged at the commit it was on, and has one more since.
	mergedAt, err := gitx.BranchHead(ctx, p.root, "again")
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := p.branchWorktree("again")
	os.WriteFile(filepath.Join(wt.Path, "more.txt"), []byte("more\n"), 0o644)
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "more"}} {
		if out, err := exec.Command("git", append([]string{"-C", wt.Path}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	gh := filepath.Join(t.TempDir(), "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
case "$1 $2" in
"pr list") cat <<'JSON'
[{"number":7,"title":"Feat","state":"MERGED","isDraft":false,"url":"https://x/7","headRefName":"feat","headRefOid":"`+featHead+`","reviewDecision":"","updatedAt":"2026-11-07T10:00:00Z","statusCheckRollup":[]},
 {"number":8,"title":"Again","state":"MERGED","isDraft":false,"url":"https://x/8","headRefName":"again","headRefOid":"`+mergedAt+`","reviewDecision":"","updatedAt":"2026-11-07T10:00:00Z","statusCheckRollup":[]}]
JSON
;;
*) echo '{}' ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	s.projects.fetchPRs(p)
	if running(s, done) {
		t.Fatal("the reviewer of a branch still at its merge is running")
	}
	if !running(s, again) {
		t.Fatal("closed the reviewer of a branch committed to since its merge")
	}

	// The moves the rule rests on.
	if movedSinceMerge(ctx, p.root, "feat", featHead) || !movedSinceMerge(ctx, p.root, "again", mergedAt) {
		t.Fatal("moved since merge")
	}
	if movedSinceMerge(ctx, p.root, "again", "") || movedSinceMerge(ctx, p.root, "deleted-here", mergedAt) {
		t.Fatal("an unknown head, or a branch gone here, is as done as it was")
	}
}
