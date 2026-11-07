package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// A rebase before pushing asks git where the branch is checked out, not
// the last snapshot: a snapshot taken while an earlier rebase was under way
// saw the worktree detached, and the next push refused with "feat is not
// checked out here" (TestHarvestPushRebase, on CI, now and then).
func TestPushRebaseTrustsGitOverTheSnapshot(t *testing.T) {
	a5IsolateEnv(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	s, dir := a5Server(t)
	pm := s.projects
	repo := filepath.Join(dir, "repo")
	a5GitRepo(t, repo)
	origin := filepath.Join(dir, "origin.git")
	run := func(d string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", d}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(dir, "init", "-q", "--bare", origin)
	run(repo, "remote", "add", "origin", origin)
	run(repo, "push", "-q", "origin", "main")
	p, err := pm.add(repo, true)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(dir, "feat")
	run(repo, "worktree", "add", "-q", "-b", "feat", wt)
	run(wt, "push", "-q", "-u", "origin", "feat")

	// The snapshot says feat is checked out nowhere, as one taken mid-rebase
	// did; git knows better.
	p.mu.Lock()
	p.info = p.load()
	for i := range p.info.Worktrees {
		if p.info.Worktrees[i].Branch == "feat" {
			p.info.Worktrees[i].Branch = ""
		}
	}
	p.mu.Unlock()
	if _, ok := p.branchWorktree("feat"); ok {
		t.Fatal("the stale snapshot still has feat")
	}
	if _, perr := pm.pushBranch(proto.BranchPushParams{ProjectID: p.id, Branch: "feat", Rebase: true}); perr != nil {
		t.Fatalf("push -rebase with a stale snapshot: %v", perr)
	}

	// A branch really checked out nowhere is still refused, and said so.
	run(repo, "branch", "loose", "main")
	_, perr := pm.pushBranch(proto.BranchPushParams{ProjectID: p.id, Branch: "loose", Rebase: true})
	if perr == nil || !strings.Contains(perr.Message, "loose is not checked out here") {
		t.Fatalf("a branch checked out nowhere: %v", perr)
	}
}

// A read of git that began before the project changed is not applied: it
// dropped a worktree added meanwhile, and of two reads that overlapped, the
// older finishing last undid the newer.
func TestRefreshOlderThanTheSnapshotIsDropped(t *testing.T) {
	a5IsolateEnv(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	s, dir := a5Server(t)
	pm := s.projects
	repo := filepath.Join(dir, "repo")
	a5GitRepo(t, repo)
	p, err := pm.add(repo, true)
	if err != nil {
		t.Fatal(err)
	}
	has := func(branch string) bool { _, ok := p.branchWorktree(branch); return ok }

	// A read begins; a worktree is added before it is applied.
	p.mu.Lock()
	start := p.gen
	p.mu.Unlock()
	stale := p.load()
	if _, _, perr := pm.addWorktree(p, "feat", ""); perr != nil {
		t.Fatal(perr)
	}
	if !has("feat") {
		t.Fatal("the worktree just added isn't known")
	}
	if pm.apply(p, stale, "", start) {
		t.Fatal("a read from before the worktree was added was applied")
	}
	p.mu.Lock()
	pending := p.pending
	p.mu.Unlock()
	if !has("feat") || !pending {
		t.Fatalf("after the stale read: feat known %v, another read asked for %v", has("feat"), pending)
	}

	// Two reads overlap: the newer applies, the older is dropped after it.
	p.mu.Lock()
	first := p.gen
	p.mu.Unlock()
	older := stale // what a slow read begun earlier would say: no feat
	newer := p.load()
	if !pm.apply(p, newer, "", first) || !has("feat") {
		t.Fatal("the newer read was not applied")
	}
	if pm.apply(p, older, "", first) || !has("feat") {
		t.Fatal("the older read, finishing last, undid the newer")
	}

	// And an ordinary refresh, with nothing changed meanwhile, applies.
	if !pm.refresh(p, "") || !has("feat") {
		t.Fatal("a refresh with nothing in between was not applied")
	}
}
