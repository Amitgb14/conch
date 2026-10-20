package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// What the first run against GitHub found (R59 in
// docs/testing/end-to-end.md): each test here failed before its fix.

// A check runs in a terminal nobody may be looking at, so git in it must
// not page: `git grep` there sat at less's (END) until somebody pressed q.
func TestAutopilotCheckDoesNotPage(t *testing.T) {
	t.Setenv("GIT_PAGER", "less")
	t.Setenv("PAGER", "less")
	s, p, _, _, _, _ := shipFixture(t)
	cmd := `test "$GIT_PAGER" = cat && test "$PAGER" = cat`
	s.autopilot.set(proto.AutopilotPolicy{Checks: map[string]string{p.id: cmd}})
	key := branchKey(p.id, "api/model")
	s.autopilotStartCheck(p.id, "api/model", cmd)
	a5WaitFor(t, "the check's verdict", func() bool {
		s.autopilot.mu.Lock()
		defer s.autopilot.mu.Unlock()
		return s.autopilot.saved.Passed[key] != "" || s.autopilot.saved.Failed[key] != ""
	})
	s.autopilot.mu.Lock()
	failed := s.autopilot.saved.Failed[key]
	s.autopilot.mu.Unlock()
	if failed != "" {
		t.Fatal("the check ran with a pager")
	}
}

// A review thread that comes with the read that finds a pull request
// mergeable holds it: the threads are read before the merge is decided.
// #1 merged with one open, read a moment after the merge went.
func TestAutopilotReadsThreadsBeforeMerging(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	apGit(t, p.root, "branch", "feat")
	a5WaitFor(t, "the branch", func() bool {
		s.projects.refresh(p, "")
		for _, b := range p.snapshot().Branches {
			if b.Name == "feat" {
				return true
			}
		}
		return false
	})
	dir := t.TempDir()
	checks, threads, calls := filepath.Join(dir, "checks"), filepath.Join(dir, "threads"), filepath.Join(dir, "calls")
	os.WriteFile(checks, []byte("IN_PROGRESS"), 0o644)
	os.WriteFile(threads, []byte(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]},"reviews":{"nodes":[]}}}}}`), 0o644)
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
echo "$*" >> `+calls+`
case "$1 $2" in
"pr list") c=$(cat `+checks+`); s=COMPLETED; [ "$c" = IN_PROGRESS ] && s=IN_PROGRESS && c=""
  echo '[{"number":5,"title":"Feat","state":"OPEN","url":"https://github.com/o/r/pull/5","headRefName":"feat","headRefOid":"abc","mergeable":"MERGEABLE","updatedAt":"'$(cat `+checks+`.at 2>/dev/null || echo 2026-10-01T10:00:00Z)'","statusCheckRollup":[{"__typename":"CheckRun","status":"'$s'","conclusion":"'$c'"}]}]' ;;
"api graphql") cat `+threads+` ;;
"repo view") echo '{"mergeCommitAllowed":true}' ;;
"pr merge") ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	s.autopilot.set(proto.AutopilotPolicy{AutoMerge: true})
	s.projects.fetchPRs(p) // seen once, pending

	// Green, and a thread opened, in the same read.
	os.WriteFile(checks, []byte("SUCCESS"), 0o644)
	os.WriteFile(checks+".at", []byte("2026-10-01T11:00:00Z"), 0o644)
	os.WriteFile(threads, []byte(prThreads(false, false)), 0o644)
	s.projects.fetchPRs(p)
	s.autopilotMerges()
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(readCalls(calls), "pr merge") {
		t.Fatal("merged with a review thread open")
	}
	st := s.autopilotState()
	if len(st.Branches) != 1 || !strings.Contains(st.Branches[0].Held, "unresolved on #5") {
		t.Fatalf("held: %+v", st.Branches)
	}
}

// An agent that finishes straight after a rebase may be read on no
// branch — the worktree as last read, mid-rebase — and its finish still
// counts: the branch is read again. A resolution sat unpushed for it.
func TestAutopilotFinishReadMidRebase(t *testing.T) {
	s, p, e, wt, _, ghLog := shipFixture(t)
	p.mu.Lock()
	for i := range p.info.Worktrees {
		if p.info.Worktrees[i].Path == wt {
			p.info.Worktrees[i].Branch = "" // as read with the rebase under way
		}
	}
	p.mu.Unlock()
	info := e.info()
	if info.Branch != "" {
		t.Fatalf("on %q, not on no branch", info.Branch)
	}
	s.autopilotFinished(info)
	a5WaitFor(t, "the pull request", func() bool { return strings.Contains(readCalls(ghLog), "pr create") })
}

// An agent that finishes while its branch is shipping — a test it was
// asked for while its pull request was opening — is shipped again once
// the pull requests are read, rather than dropped: #3 stayed a draft with
// its test in it.
func TestAutopilotFinishWhileShipping(t *testing.T) {
	s, p, _, wt, origin, _ := shipFixture(t)
	dir := t.TempDir()
	release, ghLog := filepath.Join(dir, "release"), filepath.Join(dir, "gh.log")
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
case "$1 $2" in
"pr create") echo "$*" >> `+ghLog+`; while [ ! -f `+release+` ]; do sleep 0.02; done; echo https://github.com/o/r/pull/8 ;;
"pr ready") echo "$*" >> `+ghLog+` ;;
"pr list") echo '[]' ;;
"api graphql") echo '{}' ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	s.autopilot.set(proto.AutopilotPolicy{AutoPR: true, RequireTests: true})
	key := branchKey(p.id, "api/model")
	shipped := make(chan struct{})
	go func() { s.autopilotShip(p.id, "api/model"); close(shipped) }()
	a5WaitFor(t, "the pull request opening", func() bool { return strings.Contains(readCalls(ghLog), "pr create") })

	// The test lands, and the agent finishes, while it opens.
	os.WriteFile(filepath.Join(wt, "model_test.go"), []byte("package api\n"), 0o644)
	apGit(t, wt, "add", ".")
	apGit(t, wt, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "Test the model")
	s.autopilotShip(p.id, "api/model")
	os.WriteFile(release, nil, 0o644)
	<-shipped
	if strings.Contains(readCalls(ghLog), "pr ready") {
		t.Fatal("marked ready before the pull request was read")
	}

	// The next read knows the draft it opened; the finish ships now.
	apPR(s, p, "api/model", proto.PRInfo{Number: 8, Draft: true, Checks: "pending"})
	a5WaitFor(t, "the draft marked ready", func() bool { return strings.Contains(readCalls(ghLog), "pr ready 8") })
	a5WaitFor(t, "the test pushed", func() bool {
		return apGit(t, origin, "rev-parse", "api/model") == apGit(t, wt, "rev-parse", "HEAD")
	})
	if n := strings.Count(readCalls(ghLog), "pr create"); n != 1 {
		t.Fatalf("opened %d pull requests", n)
	}
	s.autopilot.mu.Lock()
	left := s.autopilot.reship[key]
	s.autopilot.mu.Unlock()
	if left {
		t.Fatal("still waiting to ship again")
	}
}

// A conflict after the last one cleared is said, even in the same words:
// the second conflict of #2 went to its agent with nothing said.
func TestAutopilotSecondConflictIsSaid(t *testing.T) {
	notes := apNotes(t)
	s, p, e, _, _ := resolveFixture(t, "model.go", "package api // theirs\n")
	key := branchKey(p.id, "api/model")
	asked := "conflicts with origin/main in model.go: asked"
	s.autopilotResolve(key)
	a5WaitFor(t, "the first ask said", func() bool { return notes.count(asked) == 1 })
	a5WaitFor(t, "the request submitted", func() bool { st, _ := agentState(e); return st == proto.AgentIdle })

	// Cleared on GitHub, then conflicting again.
	apPR(s, p, "api/model", proto.PRInfo{Number: 8, Checks: "pass"})
	apPR(s, p, "api/model", proto.PRInfo{Number: 8, Checks: "pass", Conflicting: true})
	s.autopilotResolve(key)
	a5WaitFor(t, "the second ask said", func() bool { return notes.count(asked) == 2 })
}

// A draft GitHub will not mark ready is said once, as that, after the
// push it followed — not as a branch that was not pushed.
func TestAutopilotReadyRefusedIsSaidOnce(t *testing.T) {
	notes := apNotes(t)
	s, p, _, _, _, _ := shipFixture(t)
	gh := filepath.Join(t.TempDir(), "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
case "$1 $2" in
"pr ready") echo 'not yours' >&2; exit 1 ;;
"pr list") echo '[]' ;;
"api graphql") echo '{}' ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	s.autopilot.mu.Lock()
	s.autopilot.saved.Drafted[branchKey(p.id, "api/model")] = true
	s.autopilot.mu.Unlock()
	p.mu.Lock()
	p.prs = map[string]proto.PRInfo{"api/model": {Number: 8, State: "OPEN", Draft: true}}
	p.mu.Unlock()
	s.autopilotShip(p.id, "api/model")
	s.autopilotShip(p.id, "api/model")
	if notes.count("api/model is pushed, but #8 could not be marked ready") != 1 || notes.count("not pushed") != 0 {
		t.Fatalf("notes:\n%s", notes.b.String())
	}
}
