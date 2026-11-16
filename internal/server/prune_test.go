package server_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// TestBranchPruneMerged: branch.stale with Merged lists the branches the
// base already has, whatever their age — merged by git, squashed in, or
// merged on GitHub with the branch's commit — and nothing else: not the
// base, not a branch merely pushed, not one with a commit since. One
// checked out is listed with where, and is not suggested.
func TestBranchPruneMerged(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "gh.log")
	gh := filepath.Join(dir, "gh")
	// viapr's pull request is merged on GitHub with the commit in .head.
	os.WriteFile(gh, []byte(`#!/bin/sh
if [ "$1 $2" = "pr list" ]; then
  head=$(cat "`+log+`.head" 2>/dev/null)
  if [ -n "$head" ]; then
    printf '[{"number":7,"title":"t","state":"MERGED","url":"https://github.com/o/r/pull/7","headRefName":"viapr","headRefOid":"%s","updatedAt":"2026-09-12T10:00:00Z"}]' "$head"
  else
    echo '[]'
  fi
  exit 0
fi
exit 0
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	c, proj, repo, wt, _ := harvestFixture(t) // feat checked out in wt
	id := proj.ID
	commitOn := func(branch, file string) string {
		t.Helper()
		git(t, repo, "switch", "-q", "-c", branch, "main")
		os.WriteFile(filepath.Join(repo, file), []byte(branch+"\n"), 0o644)
		git(t, repo, "add", file)
		git(t, repo, "commit", "-q", "-m", branch)
		head := gitOut(t, repo, "rev-parse", "HEAD")
		git(t, repo, "switch", "-q", "main")
		return head
	}
	// merged: today, merged into main by git.
	commitOn("merged", "m.txt")
	git(t, repo, "merge", "-q", "--no-ff", "-m", "merge merged", "merged")
	// squashed: its change came into main as a commit of its own.
	commitOn("squashed", "s.txt")
	git(t, repo, "merge", "-q", "--squash", "squashed")
	git(t, repo, "commit", "-q", "-m", "squash squashed")
	// viapr: merged on GitHub; main here never got it.
	os.WriteFile(log+".head", []byte(commitOn("viapr", "v.txt")), 0o644)
	// pushed: on origin, merged nowhere. later: merged, then a commit more.
	commitOn("pushed", "p.txt")
	git(t, repo, "push", "-q", "origin", "pushed")
	commitOn("later", "l.txt")
	git(t, repo, "merge", "-q", "--no-ff", "-m", "merge later", "later")
	git(t, repo, "switch", "-q", "later")
	os.WriteFile(filepath.Join(repo, "l2.txt"), []byte("more\n"), 0o644)
	git(t, repo, "add", "l2.txt")
	git(t, repo, "commit", "-q", "-m", "after the merge")
	git(t, repo, "switch", "-q", "main")
	// feat, checked out in its worktree, merged too.
	git(t, repo, "merge", "-q", "--no-ff", "-m", "merge feat", "feat")

	// Wait for the server to have read viapr's pull request.
	deadline := time.Now().Add(15 * time.Second)
	for {
		var list proto.ProjectList
		_ = call(t, c, proto.MethodProjectRefresh, proto.ProjectRef{ID: id}, nil)
		if err := call(t, c, proto.MethodProjectList, nil, &list); err == nil && slices.ContainsFunc(list.Projects, func(p proto.ProjectInfo) bool {
			return p.ID == id && prState("viapr", "MERGED")(p)
		}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("viapr's pull request was never read")
		}
		time.Sleep(200 * time.Millisecond)
	}

	var got proto.BranchStale
	if err := call(t, c, proto.MethodBranchStale, proto.BranchStaleParams{ProjectID: id, Merged: true}, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Merged {
		t.Fatal("the reply does not say it is the merged list")
	}
	by := map[string]proto.StaleBranch{}
	var names []string
	for _, b := range got.Branches {
		by[b.Name] = b
		names = append(names, b.Name)
	}
	if want := "feat,merged,squashed,viapr"; strings.Join(sorted(names), ",") != want {
		t.Fatalf("merged branches %v, want %s", names, want)
	}
	for _, n := range []string{"merged", "squashed", "viapr"} {
		if b := by[n]; !b.Merged || !b.Suggested || b.Unmerged != 0 {
			t.Errorf("%s: %+v", n, b)
		}
	}
	if b := by["feat"]; b.Worktree != wt || b.Suggested {
		t.Errorf("feat: %+v", b)
	}
	// The stale list says merged for a pull request's merge too.
	var stale proto.BranchStale
	if err := call(t, c, proto.MethodBranchStale, proto.BranchStaleParams{ProjectID: id, Days: 1}, &stale); err != nil || stale.Merged {
		t.Fatalf("%+v %v", stale, err)
	}
	var res proto.BranchPruneResult
	if err := call(t, c, proto.MethodBranchPrune, proto.BranchPruneParams{ProjectID: id, Branches: []proto.PruneBranch{
		{Name: "merged"}, {Name: "squashed"}, {Name: "viapr"},
	}}, &res); err != nil || strings.Join(res.Deleted, ",") != "merged,squashed,viapr" {
		t.Fatalf("%+v %v", res, err)
	}
	left := strings.Join(sorted(strings.Fields(gitOut(t, repo, "branch", "--format=%(refname:short)"))), ",")
	if left != "feat,later,main,pushed" {
		t.Fatalf("branches left: %s", left)
	}
}
