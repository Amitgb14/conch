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
