package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// taskRepo makes a repo on main with a linked worktree for branch feat.
func taskRepo(t *testing.T) (root, wt string) {
	t.Helper()
	root = newRepo(t)
	wt = WorktreeDir(root, "feat")
	if err := AddWorktree(ctx, root, wt, "feat", "main"); err != nil {
		t.Fatal(err)
	}
	return root, resolve(wt)
}

func TestCommitAll(t *testing.T) {
	_, wt := taskRepo(t)
	if _, err := CommitChanges(ctx, wt, "nothing", nil); !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("clean checkout: %v", err)
	}
	if _, err := CommitChanges(ctx, wt, "  \n", nil); err == nil || !strings.Contains(err.Error(), "message") {
		t.Fatalf("empty message: %v", err)
	}

	write(t, wt, "README.md", "hello\nmore\n") // modified
	write(t, wt, "new/file.txt", "new\n")      // untracked in a new folder
	hash, err := CommitChanges(ctx, wt, "-starts with a dash", nil)
	if err != nil {
		t.Fatal(err)
	}
	if hash != git(t, wt, "rev-parse", "HEAD") || len(hash) < 40 {
		t.Fatalf("hash %q", hash)
	}
	if s := git(t, wt, "log", "-1", "--format=%s"); s != "-starts with a dash" {
		t.Fatalf("subject %q", s)
	}
	if st := git(t, wt, "status", "--porcelain"); st != "" {
		t.Fatalf("left behind: %q", st)
	}
}

func TestCommitSelectedFiles(t *testing.T) {
	_, wt := taskRepo(t)
	commit(t, wt, "gone.txt", "bye\n", "add gone")
	commit(t, wt, "old.txt", "moving\n", "add old")

	write(t, wt, "README.md", "hello\nedited\n")
	write(t, wt, "keep.txt", "not this one\n")
	write(t, wt, "staged.txt", "staged but not picked\n")
	git(t, wt, "add", "staged.txt")
	os.Remove(filepath.Join(wt, "gone.txt"))
	git(t, wt, "mv", "old.txt", "moved.txt")

	if _, err := CommitChanges(ctx, wt, "pick", []string{"README.md", "gone.txt", "old.txt", "moved.txt"}); err != nil {
		t.Fatal(err)
	}
	got := git(t, wt, "show", "--name-status", "--format=", "HEAD")
	for _, want := range []string{"M\tREADME.md", "D\tgone.txt", "moved.txt"} {
		if !strings.Contains(got, want) {
			t.Fatalf("commit lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "keep.txt") || strings.Contains(got, "staged.txt") {
		t.Fatalf("committed unpicked files:\n%s", got)
	}
	st := git(t, wt, "status", "--porcelain")
	if !strings.Contains(st, "?? keep.txt") || !strings.Contains(st, "A  staged.txt") {
		t.Fatalf("unpicked files not left as they were: %q", st)
	}
	// Picking only files that have no changes commits nothing.
	if _, err := CommitChanges(ctx, wt, "none", []string{"README.md"}); !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("unchanged pick: %v", err)
	}
}

func TestCommitRefusesConflicts(t *testing.T) {
	root := newRepo(t)
	git(t, root, "checkout", "-q", "-b", "other")
	commit(t, root, "README.md", "other\n", "other")
	git(t, root, "checkout", "-q", "main")
	commit(t, root, "README.md", "main\n", "main")
	cmd := command(ctx, root, "merge", "other")
	_ = cmd.Run() // conflicts
	if _, err := CommitChanges(ctx, root, "resolve", nil); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("committed over a conflict: %v", err)
	}
}

func TestPush(t *testing.T) {
	root, wt := taskRepo(t)
	if err := Push(ctx, root, "feat"); err == nil || !strings.Contains(err.Error(), "no remote") {
		t.Fatalf("no remote: %v", err)
	}
	if err := Push(ctx, root, "missing"); err == nil || !strings.Contains(err.Error(), "no branch") {
		t.Fatalf("missing branch: %v", err)
	}

	origin := filepath.Join(t.TempDir(), "origin.git")
	git(t, root, "init", "-q", "--bare", origin)
	git(t, root, "remote", "add", "origin", origin)
	commit(t, wt, "a.txt", "a\n", "first")
	if err := Push(ctx, root, "feat"); err != nil {
		t.Fatal(err)
	}
	if up := git(t, root, "rev-parse", "--abbrev-ref", "feat@{upstream}"); up != "origin/feat" {
		t.Fatalf("upstream %q", up)
	}
	commit(t, wt, "b.txt", "b\n", "second")
	if err := Push(ctx, root, "feat"); err != nil {
		t.Fatal(err)
	}
	if a, b := git(t, origin, "rev-parse", "feat"), git(t, wt, "rev-parse", "HEAD"); a != b {
		t.Fatalf("remote at %s, branch at %s", a, b)
	}

	// A branch tracking another name (a task started from origin/main) goes
	// to its own name, never onto what it tracks.
	git(t, root, "push", "-q", "origin", "main")
	git(t, root, "fetch", "-q", "origin")
	mainBefore := git(t, origin, "rev-parse", "main")
	git(t, root, "branch", "--track", "task", "origin/main")
	commitOn(t, root, "task", "t.txt")
	if err := Push(ctx, root, "task"); err != nil {
		t.Fatal(err)
	}
	if git(t, origin, "rev-parse", "main") != mainBefore {
		t.Fatal("pushed a task branch onto main")
	}
	if git(t, origin, "rev-parse", "task") != git(t, root, "rev-parse", "task") {
		t.Fatal("task not pushed under its own name")
	}
	if up := git(t, root, "rev-parse", "--abbrev-ref", "task@{upstream}"); up != "origin/task" {
		t.Fatalf("task upstream %q", up)
	}

	// A branch tracking a local branch (remote ".") goes to origin.
	git(t, root, "branch", "--track", "local", "main")
	if err := Push(ctx, root, "local"); err != nil {
		t.Fatal(err)
	}
	git(t, origin, "rev-parse", "--verify", "local")

	// A rejected push returns git's reason.
	other := git(t, root, "commit-tree", "main^{tree}", "-p", "main", "-m", "someone else's")
	git(t, root, "push", "-q", "--force", "origin", other+":refs/heads/feat")
	if err := Push(ctx, root, "feat"); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("non-fast-forward: %v", err)
	}

	// Several remotes and no origin: refuse to guess.
	git(t, root, "remote", "rename", "origin", "one")
	git(t, root, "remote", "add", "two", origin)
	git(t, root, "branch", "fresh", "main")
	if err := Push(ctx, root, "fresh"); err == nil || !strings.Contains(err.Error(), "several") {
		t.Fatalf("ambiguous remote: %v", err)
	}
}

// commitOn adds a commit to branch without checking it out.
func commitOn(t *testing.T, dir, branch, file string) {
	t.Helper()
	blob := git(t, dir, "rev-parse", branch+"^{tree}")
	c := git(t, dir, "commit-tree", blob, "-p", branch, "-m", "on "+branch+": "+file)
	git(t, dir, "update-ref", "refs/heads/"+branch, c)
}

func TestMerge(t *testing.T) {
	for _, squash := range []bool{false, true} {
		root, wt := taskRepo(t)
		if _, err := Merge(ctx, root, "feat", squash, ""); err == nil || !strings.Contains(err.Error(), "nothing") {
			t.Fatalf("squash=%v nothing to merge: %v", squash, err)
		}
		commit(t, wt, "a.txt", "a\n", "add a")
		commit(t, wt, "b.txt", "b\n", "add b")

		write(t, root, "README.md", "dirty\n")
		if _, err := Merge(ctx, root, "feat", squash, ""); err == nil || !strings.Contains(err.Error(), "uncommitted") {
			t.Fatalf("squash=%v dirty base: %v", squash, err)
		}
		git(t, root, "checkout", "--", "README.md")
		write(t, root, "scratch.txt", "untracked is fine\n")

		hash, err := Merge(ctx, root, "feat", squash, "Land feat")
		if err != nil {
			t.Fatalf("squash=%v: %v", squash, err)
		}
		if hash != git(t, root, "rev-parse", "main") {
			t.Fatalf("hash %s is not main", hash)
		}
		parents := strings.Fields(git(t, root, "log", "-1", "--format=%P"))
		if want := map[bool]int{false: 2, true: 1}[squash]; len(parents) != want {
			t.Fatalf("squash=%v: %d parents", squash, len(parents))
		}
		if s := git(t, root, "log", "-1", "--format=%s"); s != "Land feat" {
			t.Fatalf("squash=%v subject %q", squash, s)
		}
		for _, f := range []string{"a.txt", "b.txt", "scratch.txt"} {
			if _, err := os.Stat(filepath.Join(root, f)); err != nil {
				t.Fatalf("squash=%v: %v", squash, err)
			}
		}
		// Merged either way, deleting the branch loses nothing.
		if n := Unmerged(ctx, root, "feat", "main"); n != 0 {
			t.Fatalf("squash=%v: %d unmerged after merging", squash, n)
		}
	}
}

func TestMergeDefaultMessage(t *testing.T) {
	root, wt := taskRepo(t)
	commit(t, wt, "a.txt", "a\n", "add a")
	if _, err := Merge(ctx, root, "feat", true, ""); err != nil {
		t.Fatal(err)
	}
	if body := git(t, root, "log", "-1", "--format=%B"); !strings.Contains(body, "add a") {
		t.Fatalf("squash message lacks the commits: %q", body)
	}
}

func TestMergeConflictLeavesBaseAlone(t *testing.T) {
	for _, squash := range []bool{false, true} {
		root, wt := taskRepo(t)
		commit(t, wt, "README.md", "from feat\n", "feat edit")
		commit(t, wt, "other.txt", "fine\n", "feat other")
		commit(t, root, "README.md", "from main\n", "main edit")
		before := git(t, root, "rev-parse", "HEAD")
		write(t, root, "untracked.txt", "keep me\n")

		_, err := Merge(ctx, root, "feat", squash, "")
		var ce *ConflictError
		if !errors.As(err, &ce) || strings.Join(ce.Files, ",") != "README.md" || ce.Into != "main" {
			t.Fatalf("squash=%v conflict: %v", squash, err)
		}
		if !strings.Contains(err.Error(), "nothing was changed") {
			t.Fatalf("message %q", err)
		}
		if after := git(t, root, "rev-parse", "HEAD"); after != before {
			t.Fatalf("squash=%v HEAD moved", squash)
		}
		if st := git(t, root, "status", "--porcelain"); st != "?? untracked.txt" {
			t.Fatalf("squash=%v checkout not restored: %q", squash, st)
		}
		if _, err := os.Stat(filepath.Join(root, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
			t.Fatalf("squash=%v merge still in progress", squash)
		}
	}
}

func TestMergeHookRefusalIsUndone(t *testing.T) {
	root, wt := taskRepo(t)
	commit(t, wt, "a.txt", "a\n", "add a")
	hook := filepath.Join(root, ".git", "hooks", "pre-commit")
	os.MkdirAll(filepath.Dir(hook), 0o755)
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho no commits today >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := git(t, root, "rev-parse", "HEAD")
	if _, err := Merge(ctx, root, "feat", true, ""); err == nil || !strings.Contains(err.Error(), "no commits today") {
		t.Fatalf("hook refusal: %v", err)
	}
	if git(t, root, "rev-parse", "HEAD") != before || git(t, root, "status", "--porcelain") != "" {
		t.Fatal("a refused squash left changes behind")
	}
}

func TestMergeCancelled(t *testing.T) {
	root, wt := taskRepo(t)
	commit(t, wt, "a.txt", "a\n", "add a")
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Merge(cctx, root, "feat", false, ""); err == nil {
		t.Fatal("merged with a cancelled context")
	}
	if git(t, root, "status", "--porcelain") != "" {
		t.Fatal("cancelled merge left changes")
	}
}

func TestUnmergedAndDelete(t *testing.T) {
	root, wt := taskRepo(t)
	if n := Unmerged(ctx, root, "feat", "main"); n != 0 {
		t.Fatalf("fresh branch: %d", n)
	}
	if n := Unmerged(ctx, root, "missing", "main"); n != 0 {
		t.Fatalf("missing branch: %d", n)
	}
	commit(t, wt, "a.txt", "a\n", "one")
	commit(t, wt, "b.txt", "b\n", "two")
	if n := Unmerged(ctx, root, "feat", "main"); n != 2 {
		t.Fatalf("two new commits: %d", n)
	}
	if n := Unmerged(ctx, root, "feat", "no-such-base"); n != 3 {
		t.Fatalf("without a base every commit counts: %d", n)
	}

	// Pushed commits are safe on the remote.
	origin := filepath.Join(t.TempDir(), "origin.git")
	git(t, root, "init", "-q", "--bare", origin)
	git(t, root, "remote", "add", "origin", origin)
	if err := Push(ctx, root, "feat"); err != nil {
		t.Fatal(err)
	}
	git(t, root, "fetch", "-q", "origin")
	commit(t, wt, "c.txt", "c\n", "three")
	if n := Unmerged(ctx, root, "feat", "main"); n != 1 {
		t.Fatalf("one unpushed: %d", n)
	}

	// Merged into the base's upstream (say on GitHub) but not yet pulled.
	squashed := git(t, root, "commit-tree", "feat^{tree}", "-p", "main", "-m", "squashed feat")
	git(t, root, "push", "-q", "origin", squashed+":refs/heads/main")
	git(t, root, "fetch", "-q", "origin")
	git(t, root, "branch", "--set-upstream-to=origin/main", "main")
	git(t, root, "config", "branch.feat.merge", "refs/heads/none")
	if n := Unmerged(ctx, root, "feat", "main"); n != 0 {
		t.Fatalf("contained in the base's upstream: %d", n)
	}
	git(t, root, "branch", "--unset-upstream", "main")
	git(t, root, "config", "branch.feat.merge", "refs/heads/feat")

	// Removing a dirty worktree needs force; then the branch can go.
	write(t, wt, "dirty.txt", "x\n")
	if err := RemoveWorktree(ctx, root, wt); err == nil {
		t.Fatal("removed a dirty worktree without force")
	}
	if err := ForceRemoveWorktree(ctx, root, wt); err != nil {
		t.Fatal(err)
	}
	if err := DeleteBranch(ctx, root, "feat"); err != nil {
		t.Fatal(err)
	}
	if localBranchExists(ctx, root, "feat") {
		t.Fatal("branch still there")
	}
	if err := DeleteBranch(ctx, root, "feat"); err == nil {
		t.Fatal("deleted a missing branch")
	}
}

func TestStatusFiles(t *testing.T) {
	_, wt := taskRepo(t)
	if files, err := StatusFiles(ctx, wt); err != nil || len(files) != 0 {
		t.Fatalf("clean: %v %v", files, err)
	}
	write(t, wt, "x.txt", "x\n")
	if files, err := StatusFiles(ctx, wt); err != nil || len(files) != 1 || files[0].Code != "?" {
		t.Fatalf("untracked: %v %v", files, err)
	}
	if _, err := StatusFiles(ctx, t.TempDir()); err == nil {
		t.Fatal("status outside a repository")
	}
}
