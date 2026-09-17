package server_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestWorktreeCleanup(t *testing.T) {
	c, proj, repo, feat, _ := harvestFixture(t)
	id := proj.ID
	wts := filepath.Join(filepath.Dir(repo), "api.worktrees")
	add := func(name string, args ...string) string {
		t.Helper()
		dir := filepath.Join(wts, name)
		git(t, repo, append([]string{"worktree", "add", "-q"}, append(args, dir)...)...)
		return dir
	}
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n"), 0o644)
	git(t, repo, "add", ".gitignore")
	git(t, repo, "commit", "-q", "-m", "ignore .env")

	// done: squash-merged into main, with a copied .env that must not block it.
	done := add("done", "-b", "done")
	os.WriteFile(filepath.Join(done, "d.txt"), []byte("d\n"), 0o644)
	git(t, done, "add", "d.txt")
	git(t, done, "commit", "-q", "-m", "done work")
	os.WriteFile(filepath.Join(done, ".env"), []byte("SECRET=1\n"), 0o644)
	git(t, repo, "merge", "-q", "--squash", "done")
	git(t, repo, "commit", "-q", "-m", "land done")
	// wip: uncommitted work. ahead: a commit nowhere else.
	wip := add("wip", "-b", "wip")
	os.WriteFile(filepath.Join(wip, "w.txt"), []byte("w\n"), 0o644)
	ahead := add("ahead", "-b", "ahead")
	os.WriteFile(filepath.Join(ahead, "a2.txt"), []byte("a\n"), 0o644)
	git(t, ahead, "add", "a2.txt")
	git(t, ahead, "commit", "-q", "-m", "unpushed")
	// gone: its folder was deleted. lost: the same, with a commit.
	gone := add("gone", "-b", "gone")
	os.RemoveAll(gone)
	lost := add("lost", "-b", "lost")
	os.WriteFile(filepath.Join(lost, "l.txt"), []byte("l\n"), 0o644)
	git(t, lost, "add", "l.txt")
	git(t, lost, "commit", "-q", "-m", "lost work")
	os.RemoveAll(lost)
	// detached at main; locked; busy with a pane (feat, from the fixture).
	detached := add("detached", "--detach")
	locked := add("locked", "-b", "locked")
	git(t, repo, "worktree", "lock", locked)
	var pane proto.PaneInfo
	if err := call(t, c, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", "sleep 30"}, Cwd: feat}, &pane); err != nil {
		t.Fatal(err)
	}
	waitProject(t, c, func(p proto.ProjectInfo) bool { return p.ID == id && len(p.Branches) >= 8 })

	var stale proto.WorktreeStale
	if err := call(t, c, proto.MethodWorktreeStale, proto.ProjectRef{ID: id}, &stale); err != nil {
		t.Fatal(err)
	}
	got := map[string]proto.StaleWorktree{}
	for _, w := range stale.Worktrees {
		got[filepath.Base(w.Path)] = w
		if w.Path == repo {
			t.Fatal("the main checkout is listed")
		}
	}
	type want struct {
		suggested bool
		reasons   string
		check     func(proto.StaleWorktree) bool
	}
	for name, w := range map[string]want{
		"done":     {true, "merged into main", func(s proto.StaleWorktree) bool { return s.Merged && s.Branch == "done" }},
		"wip":      {false, "no commits of its own", func(s proto.StaleWorktree) bool { return s.Uncommitted == 1 }},
		"ahead":    {false, "", func(s proto.StaleWorktree) bool { return s.Unmerged == 1 && !s.Merged }},
		"gone":     {true, "folder gone, no commits of its own", func(s proto.StaleWorktree) bool { return s.Missing }},
		"lost":     {false, "folder gone", func(s proto.StaleWorktree) bool { return s.Missing && s.Unmerged == 1 }},
		"detached": {true, "detached", func(s proto.StaleWorktree) bool { return s.Branch == "" }},
		"locked":   {false, "no commits of its own", func(s proto.StaleWorktree) bool { return s.Locked }},
		"feat":     {false, "no commits of its own", func(s proto.StaleWorktree) bool { return s.Panes }},
	} {
		s, ok := got[name]
		if !ok {
			t.Fatalf("%s not listed: %+v", name, stale.Worktrees)
		}
		if s.Suggested != w.suggested || strings.Join(s.Reasons, ", ") != w.reasons || !w.check(s) {
			t.Fatalf("%s: %+v", name, s)
		}
	}
	if len(stale.Worktrees) != 8 {
		t.Fatalf("listed %d worktrees", len(stale.Worktrees))
	}

	var res proto.WorktreeCleanupResult
	remove := []proto.CleanupWorktree{{Path: done}, {Path: wip}, {Path: ahead, Force: true}, {Path: gone}, {Path: lost},
		{Path: detached}, {Path: locked}, {Path: feat}, {Path: repo}, {Path: filepath.Join(wts, "nope")}}
	if err := call(t, c, proto.MethodWorktreeCleanup, proto.WorktreeCleanupParams{ProjectID: id, Remove: remove}, &res); err != nil {
		t.Fatal(err)
	}
	var removed []string
	for _, r := range res.Removed {
		removed = append(removed, filepath.Base(r))
	}
	if strings.Join(removed, ",") != "done,ahead,detached,gone" {
		t.Fatalf("removed %v", removed)
	}
	failed := map[string]string{}
	for _, f := range res.Failed {
		failed[filepath.Base(f.Path)] = f.Error
	}
	for name, want := range map[string]string{
		"wip": "would lose 1 uncommitted file", "feat": "panes are still running", "locked": "locked",
		"api": "main checkout", "nope": "not a worktree", "lost": "pruned, but kept branch lost: it has 1 commit not merged or pushed",
	} {
		if !strings.Contains(failed[name], want) {
			t.Fatalf("%s failed with %q, want %q (all: %v)", name, failed[name], want, failed)
		}
	}
	if len(res.Failed) != 6 {
		t.Fatalf("failures %v", failed)
	}
	for _, dir := range []string{done, ahead, detached} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("%s still there", dir)
		}
	}
	branches := gitOut(t, repo, "branch", "--format=%(refname:short)")
	for _, b := range []string{"done", "ahead", "gone"} {
		if strings.Contains("\n"+branches+"\n", "\n"+b+"\n") {
			t.Fatalf("branch %s kept:\n%s", b, branches)
		}
	}
	for _, b := range []string{"wip", "lost", "locked", "feat", "main"} {
		if !strings.Contains("\n"+branches+"\n", "\n"+b+"\n") {
			t.Fatalf("branch %s deleted:\n%s", b, branches)
		}
	}
	if list := gitOut(t, repo, "worktree", "list", "--porcelain"); strings.Contains(list, "/gone") || strings.Contains(list, "/lost") {
		t.Fatalf("missing worktrees not pruned:\n%s", list)
	}
}

func TestWorktreeCleanupKeepsTheBaseBranch(t *testing.T) {
	c, proj, repo, _, _ := harvestFixture(t)
	git(t, repo, "checkout", "-q", "-b", "other")
	base := filepath.Join(filepath.Dir(repo), "api.worktrees", "main")
	git(t, repo, "worktree", "add", "-q", base, "main")

	var stale proto.WorktreeStale
	if err := call(t, c, proto.MethodWorktreeStale, proto.ProjectRef{ID: proj.ID}, &stale); err != nil {
		t.Fatal(err)
	}
	for _, w := range stale.Worktrees {
		if w.Path == base && (!w.Base || w.Suggested || len(w.Reasons) != 0) {
			t.Fatalf("base worktree: %+v", w)
		}
	}
	var res proto.WorktreeCleanupResult
	if err := call(t, c, proto.MethodWorktreeCleanup, proto.WorktreeCleanupParams{ProjectID: proj.ID,
		Remove: []proto.CleanupWorktree{{Path: base}}}, &res); err != nil || len(res.Removed) != 1 {
		t.Fatalf("cleanup: %+v %v", res, err)
	}
	if out, _ := exec.Command("git", "-C", repo, "rev-parse", "--verify", "main").Output(); len(out) == 0 {
		t.Fatal("the base branch was deleted")
	}

	// A plain folder and an unknown project are refused.
	wantErr(t, call(t, c, proto.MethodWorktreeStale, proto.ProjectRef{ID: "rnope"}, nil), "no project")
	wantErr(t, call(t, c, proto.MethodWorktreeCleanup, proto.WorktreeCleanupParams{ProjectID: "rnope"}, nil), "no project")
	// Nothing to remove is fine.
	if err := call(t, c, proto.MethodWorktreeCleanup, proto.WorktreeCleanupParams{ProjectID: proj.ID}, &res); err != nil {
		t.Fatal(err)
	}
}
