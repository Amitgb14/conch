package gitx

import (
	"context"
	"errors"
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
