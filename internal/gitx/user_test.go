package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckUserCommand(t *testing.T) {
	for _, bad := range [][]string{nil, {}, {""}, {"  "}, {"-C", "/tmp", "status"}, {"-c", "core.pager=sh", "log"}, {"--git-dir=/x", "log"}, {"--exec-path=/x"}} {
		if err := CheckUserCommand(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	for _, good := range [][]string{{"status"}, {"log", "-n", "3"}, {"commit", "-m", "-starts with a dash"}} {
		if err := CheckUserCommand(good); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
}

func TestRunUserOutputAndExitCode(t *testing.T) {
	_, wt := taskRepo(t)
	write(t, wt, "README.md", "hello\nchanged\n")
	out, code, trunc, err := RunUser(ctx, wt, []string{"status", "--short", "--branch"}, 1<<16)
	if err != nil || code != 0 || trunc {
		t.Fatalf("status: code %d trunc %v err %v", code, trunc, err)
	}
	if !strings.Contains(out, "## feat") || !strings.Contains(out, " M README.md") {
		t.Fatalf("status output %q", out)
	}

	// A failing command is a result, with git's own words for it.
	out, code, _, err = RunUser(ctx, wt, []string{"checkout", "no-such-branch"}, 1<<16)
	if err != nil || code == 0 || !strings.Contains(out, "no-such-branch") {
		t.Fatalf("bad checkout: code %d err %v out %q", code, err, out)
	}

	// Not a git command at all.
	if _, code, _, err = RunUser(ctx, wt, []string{"definitely-not-a-command"}, 1<<16); err != nil || code == 0 {
		t.Fatalf("unknown command: code %d err %v", code, err)
	}

	// Refused before git runs.
	if _, _, _, err = RunUser(ctx, wt, []string{"-C", "/", "status"}, 1<<16); err == nil {
		t.Fatal("a global option was run")
	}
}

func TestRunUserMissingDirectory(t *testing.T) {
	_, code, _, err := RunUser(ctx, "/no/such/dir/for/conch", []string{"status"}, 1<<16)
	if err != nil || code == 0 {
		t.Fatalf("missing dir: code %d err %v", code, err)
	}
}

func TestRunUserTruncates(t *testing.T) {
	_, wt := taskRepo(t)
	for i := 0; i < 20; i++ {
		commit(t, wt, "f.txt", strings.Repeat("x", i+1)+"\n", "commit number "+strings.Repeat("y", 30))
	}
	out, code, trunc, err := RunUser(ctx, wt, []string{"log"}, 100)
	if err != nil || code != 0 || !trunc || len(out) != 100 {
		t.Fatalf("log: code %d trunc %v len %d err %v", code, trunc, len(out), err)
	}
	// Exactly at the limit is not truncated.
	full, _, _, _ := RunUser(ctx, wt, []string{"log", "-1", "--format=%s"}, 1<<16)
	out, _, trunc, _ = RunUser(ctx, wt, []string{"log", "-1", "--format=%s"}, len(full))
	if trunc || out != full {
		t.Fatalf("at the limit: trunc %v %q vs %q", trunc, out, full)
	}
	if _, _, trunc, _ = RunUser(ctx, wt, []string{"log", "-1", "--format=%s"}, 0); !trunc {
		t.Fatal("a zero limit kept everything")
	}
}

// Nothing waits for an editor: a commit with no message stops, amending
// keeps the message, and a merge takes git's own.
func TestRunUserNeverWaitsForAnEditor(t *testing.T) {
	root, wt := taskRepo(t)
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	write(t, wt, "a.txt", "a\n")
	git(t, wt, "add", "a.txt")
	if _, code, _, err := RunUser(c, wt, []string{"commit"}, 1<<16); err != nil || code == 0 {
		t.Fatalf("commit without a message: code %d err %v", code, err)
	}
	git(t, wt, "commit", "-q", "-m", "add a")
	write(t, wt, "b.txt", "b\n")
	git(t, wt, "add", "b.txt")
	if out, code, _, err := RunUser(c, wt, []string{"commit", "--amend"}, 1<<16); err != nil || code != 0 {
		t.Fatalf("amend: code %d err %v %s", code, err, out)
	}
	if s := git(t, wt, "log", "-1", "--format=%s"); s != "add a" {
		t.Fatalf("amended subject %q", s)
	}
	commit(t, root, "m.txt", "m\n", "on main")
	if out, code, _, err := RunUser(c, wt, []string{"merge", "main"}, 1<<16); err != nil || code != 0 {
		t.Fatalf("merge: code %d err %v %s", code, err, out)
	}
	if s := git(t, wt, "log", "-1", "--format=%s"); !strings.Contains(s, "Merge branch 'main'") {
		t.Fatalf("merge subject %q", s)
	}
}

func TestRunUserCancelled(t *testing.T) {
	_, wt := taskRepo(t)
	c, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, _, err := RunUser(c, wt, []string{"status"}, 1<<16); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestInProgress(t *testing.T) {
	root, wt := taskRepo(t)
	if got := InProgress(ctx, wt); got != "" {
		t.Fatalf("clean: %q", got)
	}
	if got := InProgress(ctx, "/no/such/dir/for/conch"); got != "" {
		t.Fatalf("missing dir: %q", got)
	}

	// The same line changed on both sides conflicts.
	commit(t, root, "README.md", "hello\nfrom main\n", "main edit")
	commit(t, wt, "README.md", "hello\nfrom feat\n", "feat edit")

	if _, code, _, _ := RunUser(ctx, wt, []string{"merge", "main"}, 1<<16); code == 0 {
		t.Fatal("merge didn't conflict")
	}
	if got := InProgress(ctx, wt); got != "merge" {
		t.Fatalf("merging: %q", got)
	}
	// Only the worktree with the conflict is mid-merge.
	if got := InProgress(ctx, root); got != "" {
		t.Fatalf("main checkout: %q", got)
	}
	git(t, wt, "merge", "--abort")

	if _, code, _, _ := RunUser(ctx, wt, []string{"rebase", "main"}, 1<<16); code == 0 {
		t.Fatal("rebase didn't conflict")
	}
	if got := InProgress(ctx, wt); got != "rebase" {
		t.Fatalf("rebasing: %q", got)
	}
	// The worktree is detached while it rebases; the rebase still names it.
	if got := RebasingBranch(ctx, wt); got != "feat" {
		t.Fatalf("rebasing branch: %q", got)
	}
	if got := RebasingBranch(ctx, root); got != "" {
		t.Fatalf("main checkout rebasing %q", got)
	}
	git(t, wt, "rebase", "--abort")

	main := git(t, root, "rev-parse", "HEAD")
	if _, code, _, _ := RunUser(ctx, wt, []string{"cherry-pick", main}, 1<<16); code == 0 {
		t.Fatal("cherry-pick didn't conflict")
	}
	if got := InProgress(ctx, wt); got != "cherry-pick" {
		t.Fatalf("cherry-picking: %q", got)
	}
	git(t, wt, "cherry-pick", "--abort")

	feat := git(t, wt, "rev-parse", "HEAD")
	commit(t, wt, "README.md", "hello\nfrom feat again\n", "feat again")
	if _, code, _, _ := RunUser(ctx, wt, []string{"revert", "--no-edit", feat}, 1<<16); code == 0 {
		t.Fatal("revert didn't conflict")
	}
	if got := InProgress(ctx, wt); got != "revert" {
		t.Fatalf("reverting: %q", got)
	}
	git(t, wt, "revert", "--abort")
	if got := InProgress(ctx, wt); got != "" {
		t.Fatalf("after aborting: %q", got)
	}
	if got := RebasingBranch(ctx, wt); got != "" {
		t.Fatalf("rebasing after aborting: %q", got)
	}
	if got := RebasingBranch(ctx, "/no/such/dir/for/conch"); got != "" {
		t.Fatalf("missing dir: %q", got)
	}
}

// stuckRebase leaves the checkout at dir mid-rebase onto onto, stopped on
// a conflict, with the extra arguments given to git rebase.
func stuckRebase(t *testing.T, dir, onto string, args ...string) {
	t.Helper()
	argv := append(append([]string{"rebase"}, args...), onto)
	if _, code, _, _ := RunUser(ctx, dir, argv, 1<<16); code == 0 {
		t.Fatalf("git %v didn't conflict", argv)
	}
}

func TestOperation(t *testing.T) {
	t.Run("linked worktree, each backend", func(t *testing.T) {
		for _, args := range [][]string{nil, {"-i"}, {"--apply"}} {
			root, wt := taskRepo(t)
			commit(t, root, "README.md", "hello\nfrom main\n", "main edit")
			commit(t, wt, "README.md", "hello\nfrom feat\n", "feat edit")
			if len(args) > 0 && args[0] == "-i" {
				t.Setenv("GIT_SEQUENCE_EDITOR", "true") // take the todo list as it is
			}
			stuckRebase(t, wt, "main", args...)
			if op, b := Operation(ctx, wt); op != "rebase" || b != "feat" {
				t.Fatalf("rebase %v: %q %q", args, op, b)
			}
			// The main checkout is not the one rebasing.
			if op, b := Operation(ctx, root); op != "" || b != "" {
				t.Fatalf("main checkout during rebase %v: %q %q", args, op, b)
			}
			git(t, wt, "rebase", "--abort")
			if op, b := Operation(ctx, wt); op != "" || b != "" {
				t.Fatalf("after aborting %v: %q %q", args, op, b)
			}
		}
	})

	t.Run("main checkout", func(t *testing.T) {
		root := newRepo(t)
		git(t, root, "checkout", "-q", "-b", "side")
		commit(t, root, "README.md", "hello\nfrom side\n", "side edit")
		git(t, root, "checkout", "-q", "main")
		commit(t, root, "README.md", "hello\nfrom main\n", "main edit")
		stuckRebase(t, root, "side")
		if op, b := Operation(ctx, root); op != "rebase" || b != "main" {
			t.Fatalf("main checkout rebasing: %q %q", op, b)
		}
	})

	t.Run("detached HEAD rebased", func(t *testing.T) {
		root, wt := taskRepo(t)
		commit(t, root, "README.md", "hello\nfrom main\n", "main edit")
		commit(t, wt, "README.md", "hello\nfrom feat\n", "feat edit")
		git(t, wt, "checkout", "-q", "--detach")
		stuckRebase(t, wt, "main")
		// git writes "detached HEAD" where a branch would be: no branch.
		if op, b := Operation(ctx, wt); op != "rebase" || b != "" {
			t.Fatalf("detached rebase: %q %q", op, b)
		}
	})

	t.Run("head-name missing or empty", func(t *testing.T) {
		root, wt := taskRepo(t)
		commit(t, root, "README.md", "hello\nfrom main\n", "main edit")
		commit(t, wt, "README.md", "hello\nfrom feat\n", "feat edit")
		stuckRebase(t, wt, "main")
		gd := git(t, wt, "rev-parse", "--absolute-git-dir")
		name := filepath.Join(gd, "rebase-merge", "head-name")
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if op, b := Operation(ctx, wt); op != "rebase" || b != "" {
			t.Fatalf("empty head-name: %q %q", op, b)
		}
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
		if op, b := Operation(ctx, wt); op != "rebase" || b != "" {
			t.Fatalf("no head-name: %q %q", op, b)
		}
	})

	t.Run("am is not a rebase", func(t *testing.T) {
		root, wt := taskRepo(t)
		commit(t, root, "README.md", "hello\nfrom main\n", "main edit")
		patch := filepath.Join(t.TempDir(), "p.patch")
		write(t, filepath.Dir(patch), "p.patch", git(t, root, "format-patch", "-1", "--stdout")+"\n")
		commit(t, wt, "README.md", "hello\nfrom feat\n", "feat edit")
		if _, code, _, _ := RunUser(ctx, wt, []string{"am", patch}, 1<<16); code == 0 {
			t.Fatal("am didn't conflict")
		}
		if op, b := Operation(ctx, wt); op != "am" || b != "" {
			t.Fatalf("am: %q %q", op, b)
		}
	})

	t.Run("not a repository", func(t *testing.T) {
		if op, b := Operation(ctx, t.TempDir()); op != "" || b != "" {
			t.Fatalf("plain folder: %q %q", op, b)
		}
	})
}

func TestOperationWord(t *testing.T) {
	for op, want := range map[string]string{
		"rebase": "rebasing", "merge": "merging", "cherry-pick": "cherry-picking",
		"revert": "reverting", "am": "applying patches", "bisect": "bisect", "": "",
	} {
		if got := OperationWord(op); got != want {
			t.Errorf("%q: %q, want %q", op, got, want)
		}
	}
}
