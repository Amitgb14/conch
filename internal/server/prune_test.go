package server_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestBranchPrune: branch.stale lists the local branches nobody has
// committed to for a while, oldest first, with what deleting each loses;
// branch.prune deletes those that lose nothing and refuses the rest, each
// checked again: the base, one checked out in a worktree, one with commits
// neither merged nor pushed (unless forced), and one that is not there.
func TestBranchPrune(t *testing.T) {
	c, proj, repo, _, _ := harvestFixture(t) // main, and feat checked out in a worktree
	id := proj.ID
	old := func(days int) []string {
		d := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
		return []string{"GIT_COMMITTER_DATE=" + d, "GIT_AUTHOR_DATE=" + d}
	}
	commitOn := func(branch, file string, env []string) {
		t.Helper()
		git(t, repo, "switch", "-q", "-c", branch, "main")
		os.WriteFile(filepath.Join(repo, file), []byte(branch+"\n"), 0o644)
		git(t, repo, "add", file)
		gitEnv(t, repo, env, "commit", "-q", "-m", branch)
		git(t, repo, "switch", "-q", "main")
	}
	// merged: old, and main has it. wip: old, a commit nowhere else.
	// pushed: old, its commit on origin. fresh: last week.
	commitOn("merged", "m.txt", old(90))
	git(t, repo, "merge", "-q", "--ff-only", "merged")
	commitOn("wip", "w.txt", old(60))
	commitOn("pushed", "p.txt", old(45))
	git(t, repo, "push", "-q", "-u", "origin", "pushed")
	commitOn("fresh", "f.txt", old(7))
	// feat's worktree, and main itself, are made old too.
	feat := filepath.Join(filepath.Dir(repo), "api.worktrees", "feat")
	if _, err := os.Stat(feat); err != nil {
		t.Fatalf("feat's worktree is not where expected: %v", err)
	}
	gitEnv(t, feat, old(40), "commit", "-q", "--allow-empty", "-m", "feat")

	var stale proto.BranchStale
	if err := call(t, c, proto.MethodBranchStale, proto.BranchStaleParams{ProjectID: id}, &stale); err != nil {
		t.Fatal(err)
	}
	if stale.Days != 30 {
		t.Fatalf("no age given is 30 days: %d", stale.Days)
	}
	var names []string
	got := map[string]proto.StaleBranch{}
	for _, b := range stale.Branches {
		names = append(names, b.Name)
		got[b.Name] = b
	}
	// main fast-forwarded to merged's commit, so both are 90 days old.
	if want := "feat,main,merged,pushed,wip"; strings.Join(sorted(names), ",") != want {
		t.Fatalf("old branches %v, want %s (fresh left out)", names, want)
	}
	for i := 1; i < len(stale.Branches); i++ {
		if stale.Branches[i].Committed.Before(stale.Branches[i-1].Committed) {
			t.Fatalf("not oldest first: %v", names)
		}
	}
	for name, ok := range map[string]func(proto.StaleBranch) bool{
		"merged": func(b proto.StaleBranch) bool { return b.Merged && b.Unmerged == 0 && b.Suggested },
		"main":   func(b proto.StaleBranch) bool { return b.Base && !b.Suggested && b.Worktree == repo },
		"wip":    func(b proto.StaleBranch) bool { return b.Unmerged == 1 && !b.Merged && !b.Suggested },
		"pushed": func(b proto.StaleBranch) bool { return b.Unmerged == 0 && b.Upstream == "origin/pushed" && b.Suggested },
		"feat":   func(b proto.StaleBranch) bool { return b.Worktree == feat && !b.Suggested },
	} {
		if b, listed := got[name]; !listed || !ok(b) {
			t.Errorf("%s: %+v (listed %v)", name, b, listed)
		}
	}
	var week proto.BranchStale
	if err := call(t, c, proto.MethodBranchStale, proto.BranchStaleParams{ProjectID: id, Days: 5}, &week); err != nil {
		t.Fatal(err)
	}
	if len(week.Branches) != len(stale.Branches)+1 || week.Days != 5 {
		t.Fatalf("five days also takes fresh: %+v", week)
	}

	var res proto.BranchPruneResult
	err := call(t, c, proto.MethodBranchPrune, proto.BranchPruneParams{ProjectID: id, Branches: []proto.PruneBranch{
		{Name: "merged"}, {Name: "pushed"}, {Name: "wip"}, {Name: "feat"}, {Name: "main"}, {Name: "nope"}, {Name: "--force"},
	}}, &res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Deleted, ",") != "merged,pushed" {
		t.Fatalf("deleted: %v", res.Deleted)
	}
	why := map[string]string{}
	for _, f := range res.Failed {
		why[f.Path] = f.Error
	}
	for name, want := range map[string]string{
		"wip": "would lose 1 commit", "feat": "checked out in", "main": "base branch", "nope": "no such branch", "--force": "no such branch",
	} {
		if !strings.Contains(why[name], want) {
			t.Errorf("%s kept for %q, want %q", name, why[name], want)
		}
	}
	if err := call(t, c, proto.MethodBranchPrune, proto.BranchPruneParams{ProjectID: id, Branches: []proto.PruneBranch{{Name: "wip", Force: true}}}, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 || res.Deleted[0] != "wip" {
		t.Fatalf("forced: %+v", res)
	}
	left := strings.Join(sorted(strings.Fields(gitOut(t, repo, "branch", "--format=%(refname:short)"))), ",")
	if left != "feat,fresh,main" {
		t.Fatalf("branches left: %s, want feat,fresh,main", left)
	}
	if err := call(t, c, proto.MethodBranchStale, proto.BranchStaleParams{ProjectID: "nope"}, &stale); err == nil {
		t.Fatal("an unknown project should fail")
	}
}

func gitEnv(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func sorted(s []string) []string {
	s = append([]string(nil), s...)
	sort.Strings(s)
	return s
}
