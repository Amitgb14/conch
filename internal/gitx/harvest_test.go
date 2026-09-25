package gitx

import (
	"context"
	"errors"
	"fmt"
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

	// A push the remote is ahead of says so in its own words, as the error
	// a rebase can mend rather than git's page of hints.
	other := git(t, root, "commit-tree", "main^{tree}", "-p", "main", "-m", "someone else's")
	git(t, root, "push", "-q", "--force", "origin", other+":refs/heads/feat")
	err := Push(ctx, root, "feat")
	var rej *PushRejectedError
	if !errors.As(err, &rej) || rej.Remote != "origin" || rej.Branch != "feat" {
		t.Fatalf("non-fast-forward: %v", err)
	}
	if !strings.Contains(err.Error(), "commits this checkout does not have") {
		t.Fatalf("rejection reads %q", err)
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

func TestMergedAndPrune(t *testing.T) {
	root, wt := taskRepo(t)
	if !Merged(ctx, root, "feat", "main") {
		t.Fatal("a branch with no commits of its own is merged")
	}
	if Merged(ctx, root, "missing", "main") {
		t.Fatal("a missing branch is not merged")
	}
	commit(t, wt, "a.txt", "a\n", "one")
	if Merged(ctx, root, "feat", "main") || Merged(ctx, root, "feat", "") {
		t.Fatal("a new commit is not merged")
	}
	// Squash-merged into main: its changes are there under another commit.
	squashed := git(t, root, "commit-tree", "feat^{tree}", "-p", "main", "-m", "squash")
	git(t, root, "update-ref", "refs/heads/main", squashed)
	git(t, root, "reset", "-q", "--hard")
	if !Merged(ctx, root, "feat", "main") {
		t.Fatal("a squash-merged branch is merged")
	}

	// A worktree whose folder was deleted is pruned.
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	wts, _ := Worktrees(ctx, root)
	if len(wts) != 2 || !wts[1].Prunable {
		t.Fatalf("before prune: %+v", wts)
	}
	if err := PruneWorktrees(ctx, root); err != nil {
		t.Fatal(err)
	}
	if wts, _ := Worktrees(ctx, root); len(wts) != 1 {
		t.Fatalf("after prune: %+v", wts)
	}
	if err := PruneWorktrees(ctx, t.TempDir()); err == nil {
		t.Fatal("pruned outside a repository")
	}
}

func TestUnreachable(t *testing.T) {
	root := newRepo(t)
	dir := filepath.Join(t.TempDir(), "detached")
	git(t, root, "worktree", "add", "-q", "--detach", dir, "main")
	if n := Unreachable(ctx, dir); n != 0 {
		t.Fatalf("at main: %d", n)
	}
	commit(t, dir, "x.txt", "x\n", "one")
	commit(t, dir, "y.txt", "y\n", "two")
	if n := Unreachable(ctx, dir); n != 2 {
		t.Fatalf("two commits on no branch: %d", n)
	}
	git(t, dir, "branch", "keep")
	if n := Unreachable(ctx, dir); n != 0 {
		t.Fatalf("kept by a branch: %d", n)
	}
	if n := Unreachable(ctx, t.TempDir()); n != 0 {
		t.Fatalf("not a repository: %d", n)
	}
}

// hunks splits a file's diff into its header and its hunks.
func hunks(t *testing.T, diff string) (header string, out []string) {
	t.Helper()
	lines := strings.SplitAfter(diff, "\n")
	cur := -1
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "@@"):
			out = append(out, l)
			cur++
		case cur < 0:
			header += l
		default:
			out[cur] += l
		}
	}
	if len(out) == 0 {
		t.Fatalf("no hunks in:\n%s", diff)
	}
	return header, out
}

func TestCommitPatchChanges(t *testing.T) {
	_, wt := taskRepo(t)
	lines := ""
	for i := 1; i <= 30; i++ {
		lines += fmt.Sprintf("line %d\n", i)
	}
	commit(t, wt, "f.txt", lines, "add f")
	edited := strings.Replace(lines, "line 1\n", "FIRST\n", 1)
	edited = strings.Replace(edited, "line 30\n", "LAST\n", 1)
	write(t, wt, "f.txt", edited)
	write(t, wt, "other.txt", "other\n") // untracked, must stay out

	diff, err := Diff(ctx, wt, "f.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	header, hs := hunks(t, diff)
	if len(hs) != 2 {
		t.Fatalf("want two hunks, got %d:\n%s", len(hs), diff)
	}

	if _, err := CommitPatchChanges(ctx, wt, "", header+hs[0], nil); err == nil {
		t.Fatal("no message")
	}
	if _, err := CommitPatchChanges(ctx, wt, "m", "  ", nil); err == nil {
		t.Fatal("empty patch")
	}

	// Only the first hunk is committed; the rest stays in the worktree.
	hash, err := CommitPatchChanges(ctx, wt, "First line only", header+hs[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if hash != git(t, wt, "rev-parse", "HEAD") {
		t.Fatalf("hash %q", hash)
	}
	committed := git(t, wt, "show", "HEAD:f.txt")
	if !strings.Contains(committed, "FIRST") || strings.Contains(committed, "LAST") {
		t.Fatalf("committed the wrong hunk:\n%s", committed)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "f.txt")); string(got) != edited {
		t.Fatal("the worktree file changed")
	}
	if st := git(t, wt, "status", "--porcelain"); !strings.Contains(st, "M f.txt") || !strings.Contains(st, "?? other.txt") {
		t.Fatalf("status after a partial commit: %q", st)
	}

	// The same patch no longer applies: the line it changed is committed.
	if _, err := CommitPatchChanges(ctx, wt, "again", header+hs[0], nil); !errors.Is(err, ErrPatchStale) {
		t.Fatalf("stale patch: %v", err)
	}
	if git(t, wt, "log", "-1", "--format=%s") != "First line only" {
		t.Fatal("a stale patch committed something")
	}

	// A patch plus whole files, and what is already staged, go together.
	write(t, wt, "staged.txt", "staged\n")
	git(t, wt, "add", "staged.txt")
	diff, _ = Diff(ctx, wt, "f.txt", "")
	header, hs = hunks(t, diff)
	if _, err := CommitPatchChanges(ctx, wt, "The rest", header+hs[len(hs)-1], []string{"other.txt"}); err != nil {
		t.Fatal(err)
	}
	names := git(t, wt, "show", "--name-only", "--format=", "HEAD")
	for _, want := range []string{"f.txt", "other.txt", "staged.txt"} {
		if !strings.Contains(names, want) {
			t.Fatalf("commit lacks %s:\n%s", want, names)
		}
	}
	if st := git(t, wt, "status", "--porcelain"); st != "" {
		t.Fatalf("left behind: %q", st)
	}
}

func TestCommitPatchFailures(t *testing.T) {
	_, wt := taskRepo(t)
	commit(t, wt, "f.txt", "one\n", "add f")
	write(t, wt, "f.txt", "two\n")
	diff, _ := Diff(ctx, wt, "f.txt", "")

	// A patch that applies but leaves nothing to commit (already committed).
	git(t, wt, "commit", "-q", "-am", "same change")
	if _, err := CommitPatchChanges(ctx, wt, "m", diff, nil); !errors.Is(err, ErrPatchStale) {
		t.Fatalf("applied to a file that moved on: %v", err)
	}
	// Nonsense that git apply refuses outright.
	if _, err := CommitPatchChanges(ctx, wt, "m", "not a patch at all\n", nil); err == nil || errors.Is(err, ErrPatchStale) {
		t.Fatalf("garbage patch: %v", err)
	}
	// A hook that refuses leaves HEAD alone.
	hook := filepath.Join(wt, "..", "repo", ".git", "hooks", "pre-commit")
	if root, err := Discover(ctx, wt); err == nil {
		hook = filepath.Join(root.CommonDir, "hooks", "pre-commit")
	}
	os.MkdirAll(filepath.Dir(hook), 0o755)
	os.WriteFile(hook, []byte("#!/bin/sh\necho refused >&2\nexit 1\n"), 0o755)
	write(t, wt, "f.txt", "three\n")
	diff, _ = Diff(ctx, wt, "f.txt", "")
	before := git(t, wt, "rev-parse", "HEAD")
	if _, err := CommitPatchChanges(ctx, wt, "m", diff, nil); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("hook: %v", err)
	}
	if git(t, wt, "rev-parse", "HEAD") != before {
		t.Fatal("a refused commit moved HEAD")
	}
}

func TestAhead(t *testing.T) {
	root, wt := taskRepo(t)
	if n := Ahead(ctx, root, "feat", "main"); n != 0 {
		t.Fatalf("a fresh branch: %d", n)
	}
	commit(t, wt, "a.txt", "a\n", "one")
	commit(t, wt, "b.txt", "b\n", "two")
	if n := Ahead(ctx, root, "feat", "main"); n != 2 {
		t.Fatalf("two commits: %d", n)
	}
	if n := Ahead(ctx, root, "missing", "main"); n != 0 {
		t.Fatalf("missing branch: %d", n)
	}
	if n := Ahead(ctx, root, "feat", "no-such-base"); n != 0 {
		t.Fatalf("missing base: %d", n)
	}
}

// pushed sets up a feat branch with an upstream on a bare origin, and
// returns the repository, its worktree and origin's path.
func pushed(t *testing.T) (root, wt, origin string) {
	t.Helper()
	root, wt = taskRepo(t)
	origin = filepath.Join(t.TempDir(), "origin.git")
	git(t, root, "init", "-q", "--bare", origin)
	git(t, root, "remote", "add", "origin", origin)
	commit(t, wt, "a.txt", "a\n", "first")
	if err := Push(ctx, root, "feat"); err != nil {
		t.Fatal(err)
	}
	return root, wt, origin
}

// aheadOnOrigin puts a commit on origin's feat that this checkout lacks, as
// another machine or another person pushing would.
func aheadOnOrigin(t *testing.T, root, origin, file, text string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "theirs")
	git(t, root, "clone", "-q", "-b", "feat", origin, clone)
	write(t, clone, file, text)
	git(t, clone, "add", "-A")
	git(t, clone, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-q", "-m", "theirs")
	git(t, clone, "push", "-q", "origin", "feat")
}

// PushRebase is what mends a rejected push: take what the remote has, put
// this branch's own commits on top, push.
func TestPushRebase(t *testing.T) {
	root, wt, origin := pushed(t)
	aheadOnOrigin(t, root, origin, "theirs.txt", "theirs\n")
	commit(t, wt, "mine.txt", "mine\n", "mine")
	mine := git(t, wt, "rev-parse", "HEAD")

	if err := Push(ctx, root, "feat"); err == nil {
		t.Fatal("the push should have been rejected")
	}
	took, err := PushRebase(ctx, wt, "feat")
	if err != nil || took != 1 {
		t.Fatalf("rebase and push: took %d, %v", took, err)
	}
	if got := git(t, origin, "rev-parse", "feat"); got != git(t, wt, "rev-parse", "HEAD") {
		t.Fatalf("origin at %s, branch at %s", got, git(t, wt, "rev-parse", "HEAD"))
	}
	// Both sides' work is there, and mine sits on top of theirs.
	for _, f := range []string{"theirs.txt", "mine.txt"} {
		if _, err := os.Stat(filepath.Join(wt, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if git(t, wt, "rev-parse", "HEAD") == mine {
		t.Fatal("the branch was not rebased")
	}
	if subject := git(t, wt, "log", "-1", "--format=%s"); subject != "mine" {
		t.Fatalf("mine is not on top: %q", subject)
	}

	// With nothing to take it is an ordinary push.
	commit(t, wt, "more.txt", "more\n", "more")
	if took, err := PushRebase(ctx, wt, "feat"); err != nil || took != 0 {
		t.Fatalf("nothing to take: took %d, %v", took, err)
	}
	if git(t, origin, "rev-parse", "feat") != git(t, wt, "rev-parse", "HEAD") {
		t.Fatal("the push did not happen")
	}
}

// A rebase that conflicts is undone: the branch stays as it was, nothing is
// pushed, and no rebase is left half finished.
func TestPushRebaseConflict(t *testing.T) {
	root, wt, origin := pushed(t)
	aheadOnOrigin(t, root, origin, "a.txt", "theirs\n")
	write(t, wt, "a.txt", "mine\n")
	git(t, wt, "add", "-A")
	git(t, wt, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-q", "-m", "mine")
	head, remote := git(t, wt, "rev-parse", "HEAD"), git(t, origin, "rev-parse", "feat")

	took, err := PushRebase(ctx, wt, "feat")
	var conflict *RebaseConflictError
	if !errors.As(err, &conflict) || took != 0 {
		t.Fatalf("conflicting rebase: took %d, %v", took, err)
	}
	if len(conflict.Files) != 1 || conflict.Files[0] != "a.txt" || conflict.Remote != "origin" {
		t.Fatalf("conflict names %+v", conflict)
	}
	if !strings.Contains(err.Error(), "nothing was changed or pushed") {
		t.Fatalf("reads %q", err)
	}
	if git(t, wt, "rev-parse", "HEAD") != head {
		t.Fatal("the branch moved")
	}
	if git(t, origin, "rev-parse", "feat") != remote {
		t.Fatal("something was pushed")
	}
	if b := git(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); b != "feat" {
		t.Fatalf("left on %q", b) // a rebase stopped part way leaves a detached HEAD
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "a.txt")); string(got) != "mine\n" {
		t.Fatalf("the file reads %q", got)
	}
	// The worktree is usable again straight away.
	if _, err := StatusFiles(ctx, wt); err != nil {
		t.Fatal(err)
	}
}

// What PushRebase refuses to do, so no uncommitted work is ever moved.
func TestPushRebaseRefusals(t *testing.T) {
	root, wt, origin := pushed(t)
	aheadOnOrigin(t, root, origin, "theirs.txt", "theirs\n")

	// Uncommitted changes to a tracked file stop it; an untracked file
	// does not, since a rebase leaves those alone.
	write(t, wt, "a.txt", "half written\n")
	if _, err := PushRebase(ctx, wt, "feat"); err == nil || !strings.Contains(err.Error(), "commit or stash") {
		t.Fatalf("dirty worktree: %v", err)
	}
	git(t, wt, "checkout", "--", "a.txt")
	write(t, wt, "scratch.txt", "untracked\n")
	if _, err := PushRebase(ctx, wt, "feat"); err != nil {
		t.Fatalf("untracked file: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "scratch.txt")); string(got) != "untracked\n" {
		t.Fatalf("the untracked file reads %q", got)
	}

	// A directory where the branch is not checked out says which is.
	if _, err := PushRebase(ctx, root, "feat"); err == nil || !strings.Contains(err.Error(), "not checked out") {
		t.Fatalf("wrong worktree: %v", err)
	}
}

// A push refused for another reason — a hook, no access — is not mistaken
// for one the remote is merely ahead of: taking its commits would not mend
// it, so it is reported as it is.
func TestPushRefusedByAHook(t *testing.T) {
	root, wt, origin := pushed(t)
	hook := filepath.Join(origin, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho no thanks >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	commit(t, wt, "b.txt", "b\n", "second")
	err := Push(ctx, root, "feat")
	var rej *PushRejectedError
	if err == nil || errors.As(err, &rej) {
		t.Fatalf("a hook's refusal: %v", err)
	}
	if !strings.Contains(err.Error(), "no thanks") {
		t.Fatalf("the hook's reason is lost: %v", err)
	}
}
