package server_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// failGit puts a git first on PATH that fails the subcommand named and
// runs the real one for everything else, the server's own git included.
func failGit(t *testing.T, sub string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	bin := t.TempDir()
	// gitx runs git -c core.quotepath=off -C DIR SUBCOMMAND …
	script := "#!/bin/sh\nif [ \"$5\" = " + sub + " ]; then echo 'fatal: broken' >&2; exit 128; fi\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestUnknownLossIsNeverSafe: when git cannot count what deleting a
// branch would lose, the count is unknown, not 0 — so neither the old
// branches nor the worktrees list suggests it, and neither prune nor
// cleanup deletes it, forced or not. Before, a failed count read as
// "nothing to lose" and the branch went with --force.
func TestUnknownLossIsNeverSafe(t *testing.T) {
	c, proj, repo, _, _ := harvestFixture(t)
	id := proj.ID
	old := time.Now().Add(-60 * 24 * time.Hour).Format(time.RFC3339)
	// pushed: old, its commit on origin, so it loses nothing.
	git(t, repo, "switch", "-q", "-c", "pushed", "main")
	os.WriteFile(filepath.Join(repo, "p.txt"), []byte("p\n"), 0o644)
	git(t, repo, "add", "p.txt")
	gitEnv(t, repo, []string{"GIT_COMMITTER_DATE=" + old, "GIT_AUTHOR_DATE=" + old}, "commit", "-q", "-m", "pushed")
	git(t, repo, "push", "-q", "-u", "origin", "pushed")
	git(t, repo, "switch", "-q", "main")
	// lost: a worktree whose folder is gone, its branch pushed too.
	lost := filepath.Join(filepath.Dir(repo), "api.worktrees", "lost")
	git(t, repo, "worktree", "add", "-q", "-b", "lost", lost)
	os.WriteFile(filepath.Join(lost, "l.txt"), []byte("l\n"), 0o644)
	git(t, lost, "add", "l.txt")
	git(t, lost, "commit", "-q", "-m", "lost work")
	git(t, lost, "push", "-q", "-u", "origin", "lost")
	os.RemoveAll(lost)

	staleBranch := func() proto.StaleBranch {
		t.Helper()
		var stale proto.BranchStale
		if err := call(t, c, proto.MethodBranchStale, proto.BranchStaleParams{ProjectID: id}, &stale); err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(stale.Branches, func(b proto.StaleBranch) bool { return b.Name == "pushed" })
		if i < 0 {
			t.Fatalf("pushed not listed: %+v", stale.Branches)
		}
		return stale.Branches[i]
	}
	staleWorktree := func() proto.StaleWorktree {
		t.Helper()
		var stale proto.WorktreeStale
		if err := call(t, c, proto.MethodWorktreeStale, proto.ProjectRef{ID: id}, &stale); err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(stale.Worktrees, func(w proto.StaleWorktree) bool { return w.Branch == "lost" })
		if i < 0 {
			t.Fatalf("lost not listed: %+v", stale.Worktrees)
		}
		return stale.Worktrees[i]
	}
	// While git can count, both are suggested: the fixture is sound.
	if b := staleBranch(); !b.Suggested || b.Unknown != "" {
		t.Fatalf("counted: %+v", b)
	}
	if w := staleWorktree(); !w.Suggested {
		t.Fatalf("counted: %+v", w)
	}

	failGit(t, "rev-list")
	if b := staleBranch(); b.Suggested || b.Unknown == "" || b.Unmerged != 0 {
		t.Fatalf("uncounted branch: %+v", b)
	}
	if w := staleWorktree(); w.Suggested || !slices.ContainsFunc(w.Reasons, func(r string) bool { return strings.Contains(r, "can't count") }) {
		t.Fatalf("uncounted worktree: %+v", w)
	}

	var pruned proto.BranchPruneResult
	if err := call(t, c, proto.MethodBranchPrune, proto.BranchPruneParams{ProjectID: id, Branches: []proto.PruneBranch{
		{Name: "pushed", Force: true}, {Name: ""}, {Name: "-D"},
	}}, &pruned); err != nil {
		t.Fatal(err)
	}
	if len(pruned.Deleted) != 0 || len(pruned.Failed) != 3 {
		t.Fatalf("pruned with an unknown count: %+v", pruned)
	}
	for _, f := range pruned.Failed {
		want := "no such branch"
		if f.Path == "pushed" {
			want = "can't tell what deleting pushed would lose"
		}
		if !strings.Contains(f.Error, want) {
			t.Errorf("%q kept for %q, want %q", f.Path, f.Error, want)
		}
	}

	var cleaned proto.WorktreeCleanupResult
	if err := call(t, c, proto.MethodWorktreeCleanup, proto.WorktreeCleanupParams{ProjectID: id, Remove: []proto.CleanupWorktree{{Path: lost, Force: true}}}, &cleaned); err != nil {
		t.Fatal(err)
	}
	if len(cleaned.Failed) != 1 || !strings.Contains(cleaned.Failed[0].Error, "kept branch lost") {
		t.Fatalf("cleanup with an unknown count: %+v", cleaned)
	}
	left := strings.Fields(gitOut(t, repo, "branch", "--format=%(refname:short)"))
	if !slices.Contains(left, "pushed") || !slices.Contains(left, "lost") {
		t.Fatalf("a branch went with its count unknown: %v", left)
	}
}
