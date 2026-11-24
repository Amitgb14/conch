package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// gitSpy stands in for git on PATH: it writes down what it was asked and
// hands it on, except `rebase --abort` while refusing is set.
type gitSpy struct {
	t      *testing.T
	log    string
	refuse string
}

func spyGit(t *testing.T) *gitSpy {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	g := &gitSpy{t: t, log: filepath.Join(dir, "log"), refuse: filepath.Join(dir, "refuse")}
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> '%s'
case "$*" in
*"rebase --abort"*)
	if [ -e '%s' ]; then echo "fatal: Unable to create index.lock: File exists." >&2; exit 128; fi ;;
esac
exec '%s' "$@"
`, g.log, g.refuse, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return g
}

// ran counts the commands run that have what in them.
func (g *gitSpy) ran(what string) int {
	b, _ := os.ReadFile(g.log)
	return strings.Count(string(b), what)
}

func (g *gitSpy) rebases() int { return g.ran("rebase --quiet") }
func (g *gitSpy) fetches() int { return g.ran("fetch --quiet -- origin +refs/heads/main") }

// A conflict with nobody to give it to was rebased and undone at every
// tick: 33,093 times in one real worktree.
func TestAutopilotConflictWithNoAgentIsTriedOnce(t *testing.T) {
	notes := apNotes(t)
	s, p, e, wt, _ := resolveFixture(t, "model.go", "package api // theirs\n")
	key := branchKey(p.id, "api/model")
	s.close(e.p.ID())
	a5WaitFor(t, "the agent gone", func() bool { return s.branchAgent(p.id, "api/model") == nil })
	g := spyGit(t)
	for range 10 {
		s.autopilotResolve(key)
	}
	if r, f := g.rebases(), g.fetches(); r != 1 || f != 1 {
		t.Fatalf("10 ticks: %d rebases and %d fetches, want one of each", r, f)
	}
	if n := notes.count("there is no agent free to resolve it"); n != 1 {
		t.Fatalf("said %d times", n)
	}
	if at, ok := apConflicted(s, key); !ok || at != "" {
		t.Fatalf("no longer waiting for an agent: %q %v", at, ok)
	}

	// A new commit is another rebase to try, at once.
	if err := os.WriteFile(filepath.Join(wt, "more.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	apGit(t, wt, "add", "more.txt")
	apGit(t, wt, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "more")
	s.projects.refreshNow(p)
	s.autopilotResolve(key)
	if r := g.rebases(); r != 2 {
		t.Fatalf("after a new commit: %d rebases, want 2", r)
	}
}

// An agent that cannot be reached is asked again without the worktree
// being rebased again for it, and gets the conflict once it can be.
func TestAutopilotConflictIsHandedOverWithoutAnotherRebase(t *testing.T) {
	s, p, _, _, _ := resolveFixture(t, "model.go", "package api // theirs\n")
	key := branchKey(p.id, "api/model")
	asks, deaf := 0, true
	var told string
	s.autopilot.prompt = func(pp proto.AgentPromptParams) (proto.AgentPromptResult, *proto.Error) {
		asks++
		if deaf {
			return proto.AgentPromptResult{}, proto.Errorf(proto.ErrBadRequest, "not listening")
		}
		told = pp.Text
		return proto.AgentPromptResult{}, nil
	}
	g := spyGit(t)
	for range 5 {
		s.autopilotResolve(key)
	}
	if r := g.rebases(); r != 1 || asks != 5 {
		t.Fatalf("%d rebases and %d asks, want 1 and 5", r, asks)
	}
	deaf = false
	s.autopilotResolve(key)
	if g.rebases() != 1 || !strings.Contains(told, "model.go") {
		t.Fatalf("%d rebases, told %q", g.rebases(), told)
	}
}

// A rebase git refuses for another reason is not run again at every tick
// either.
func TestAutopilotFailedRebaseWaits(t *testing.T) {
	notes := apNotes(t)
	s, p, _, _, _ := resolveFixture(t, "other.txt", "theirs\n")
	key := branchKey(p.id, "api/model")
	hook := filepath.Join(p.root, ".git", "hooks", "pre-rebase")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho not today >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := spyGit(t)
	for range 10 {
		s.autopilotResolve(key)
	}
	if r := g.rebases(); r != 1 {
		t.Fatalf("%d rebases, want 1", r)
	}
	if n := notes.count("rebasing it failed"); n != 1 {
		t.Fatalf("said %d times", n)
	}
	if n := g.ran("rebase --abort"); n != 0 {
		t.Fatalf("aborted a rebase that never started: %d", n)
	}
}

// A refusal before anything is touched costs nothing and passes soon.
func TestAutopilotDirtyWorktreeIsLookedAtAgainAtOnce(t *testing.T) {
	s, p, _, wt, _ := resolveFixture(t, "other.txt", "theirs\n")
	key := branchKey(p.id, "api/model")
	head := apGit(t, wt, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(wt, "model.go"), []byte("package api // not committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.autopilotResolve(key)
	if apGit(t, wt, "rev-parse", "HEAD") != head {
		t.Fatal("rebased over uncommitted work")
	}
	apGit(t, wt, "checkout", "--", "model.go")
	s.autopilotResolve(key)
	if apGit(t, wt, "rev-parse", "HEAD") == head {
		t.Fatal("not tried again once it was clean")
	}
}

// An abort that fails was swallowed: the agent was told the rebase was
// undone, and the next tick told the person to finish conch's own.
func TestAutopilotSaysARebaseItCouldNotUndo(t *testing.T) {
	notes := apNotes(t)
	s, p, _, wt, _ := resolveFixture(t, "model.go", "package api // theirs\n")
	key := branchKey(p.id, "api/model")
	asks := 0
	s.autopilot.prompt = func(proto.AgentPromptParams) (proto.AgentPromptResult, *proto.Error) {
		asks++
		return proto.AgentPromptResult{}, nil
	}
	g := spyGit(t)
	if err := os.WriteFile(g.refuse, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		s.autopilotResolve(key)
	}
	if asks != 0 {
		t.Fatal("the agent was told of a rebase undone that was not")
	}
	if notes.count("could not be undone") != 1 || notes.count("run `git rebase --abort`") != 1 {
		t.Fatalf("notes:\n%s", notes.b.String())
	}
	if n := notes.count("already under way there"); n != 0 {
		t.Fatal("conch's own rebase was put on the person")
	}
	if g.rebases() != 1 || gitx.InProgress(t.Context(), wt) != "rebase" {
		t.Fatalf("%d rebases", g.rebases())
	}
}

// Somebody's own rebase is left alone, and not fetched for at every tick.
func TestAutopilotLeavesAnothersRebase(t *testing.T) {
	s, p, _, wt, _ := resolveFixture(t, "model.go", "package api // theirs\n")
	key := branchKey(p.id, "api/model")
	apGit(t, wt, "fetch", "-q", "origin")
	if exec.Command("git", "-C", wt, "rebase", "origin/main").Run() == nil {
		t.Fatal("no conflict to stop on")
	}
	g := spyGit(t)
	for range 5 {
		s.autopilotResolve(key)
	}
	if g.rebases()+g.fetches()+g.ran("rebase --abort") != 0 {
		t.Fatalf("touched it: %d rebases, %d fetches", g.rebases(), g.fetches())
	}
	if gitx.InProgress(t.Context(), wt) != "rebase" {
		t.Fatal("their rebase is gone")
	}
}

// waited makes a branch's last stopped rebase older than resolveWait.
func waited(s *Server, key string) {
	s.autopilot.mu.Lock()
	defer s.autopilot.mu.Unlock()
	r := s.autopilot.stopped[key]
	r.at = time.Now().Add(-resolveWait - time.Second)
	s.autopilot.stopped[key] = r
}

// After the wait the base is looked at again, and rebased onto only when
// it moved.
func TestAutopilotConflictFollowsTheBase(t *testing.T) {
	s, p, e, _, origin := resolveFixture(t, "model.go", "package api // theirs\n")
	key := branchKey(p.id, "api/model")
	s.close(e.p.ID())
	a5WaitFor(t, "the agent gone", func() bool { return s.branchAgent(p.id, "api/model") == nil })
	g := spyGit(t)
	s.autopilotResolve(key)

	waited(s, key)
	s.autopilotResolve(key)
	s.autopilotResolve(key) // and waits again from there
	if r, f := g.rebases(), g.fetches(); r != 1 || f != 2 {
		t.Fatalf("base unmoved: %d rebases and %d fetches, want 1 and 2", r, f)
	}

	other := filepath.Join(t.TempDir(), "other")
	apGit(t, filepath.Dir(other), "clone", "-q", "-b", "main", origin, other)
	if err := os.WriteFile(filepath.Join(other, "elsewhere.txt"), []byte("moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	apGit(t, other, "add", ".")
	apGit(t, other, "-c", "user.name=o", "-c", "user.email=o@example.com", "commit", "-qm", "main moves")
	apGit(t, other, "push", "-q", "origin", "main")
	waited(s, key)
	s.autopilotResolve(key)
	if r := g.rebases(); r != 2 {
		t.Fatalf("base moved: %d rebases, want 2", r)
	}
}

// What was learnt is not an excuse to give a dirty worktree to the agent.
func TestAutopilotKnownConflictStillWaitsForACleanWorktree(t *testing.T) {
	s, p, _, wt, _ := resolveFixture(t, "model.go", "package api // theirs\n")
	key := branchKey(p.id, "api/model")
	asks := 0
	s.autopilot.prompt = func(proto.AgentPromptParams) (proto.AgentPromptResult, *proto.Error) {
		asks++
		return proto.AgentPromptResult{}, proto.Errorf(proto.ErrBadRequest, "not listening")
	}
	s.autopilotResolve(key)
	if err := os.WriteFile(filepath.Join(wt, "model.go"), []byte("package api // not committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.autopilotResolve(key)
	if asks != 1 {
		t.Fatalf("asked %d times, want once, before it was dirty", asks)
	}
}

func TestAutopilotFailedRebaseIsTriedAgainAfterTheWait(t *testing.T) {
	s, p, _, _, _ := resolveFixture(t, "other.txt", "theirs\n")
	key := branchKey(p.id, "api/model")
	hook := filepath.Join(p.root, ".git", "hooks", "pre-rebase")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := spyGit(t)
	s.autopilotResolve(key)
	waited(s, key)
	s.autopilotResolve(key)
	if r := g.rebases(); r != 2 {
		t.Fatalf("%d rebases, want 2", r)
	}
}

// What stopped is forgotten with the conflict, and with the project.
func TestAutopilotForgetsAStoppedRebase(t *testing.T) {
	s, p, _, _, _ := resolveFixture(t, "model.go", "package api // theirs\n")
	key := branchKey(p.id, "api/model")
	kept := func() bool {
		s.autopilot.mu.Lock()
		defer s.autopilot.mu.Unlock()
		_, ok := s.autopilot.stopped[key]
		return ok
	}
	s.rebaseStopped(key, stoppedRebase{head: "h"})
	apPR(s, p, "api/model", proto.PRInfo{Number: 8, Checks: "pass"})
	if kept() {
		t.Fatal("kept after the conflict cleared")
	}
	s.rebaseStopped(key, stoppedRebase{head: "h"})
	s.autopilot.forget(p.id)
	if kept() {
		t.Fatal("kept for a removed project")
	}
}

// Nor is a dirty one, with nobody to ask, fetched for at every tick.
func TestAutopilotKnownConflictInADirtyWorktreeWaits(t *testing.T) {
	s, p, e, wt, _ := resolveFixture(t, "model.go", "package api // theirs\n")
	key := branchKey(p.id, "api/model")
	s.close(e.p.ID())
	a5WaitFor(t, "the agent gone", func() bool { return s.branchAgent(p.id, "api/model") == nil })
	s.autopilotResolve(key)
	if err := os.WriteFile(filepath.Join(wt, "model.go"), []byte("package api // not committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waited(s, key)
	g := spyGit(t)
	for range 5 {
		s.autopilotResolve(key)
	}
	if f := g.fetches(); f != 1 {
		t.Fatalf("%d fetches, want 1", f)
	}
}
