package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// failingAborts puts a git on PATH whose `rebase --abort` fails n times.
func failingAborts(t *testing.T, n int) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
*"rebase --abort"*)
	echo x >> '%s'
	if [ $(wc -l < '%s') -le %d ]; then echo "fatal: Unable to create index.lock: File exists." >&2; exit 128; fi ;;
esac
exec '%s' "$@"
`, count, count, n, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func conflicting(t *testing.T) (wt, base, head string) {
	t.Helper()
	_, wt, origin := ontoRepo(t)
	pushMain(t, origin, "feat.txt", "theirs\n")
	return wt, FreshBase(ctx, wt, "feat", "main"), git(t, wt, "rev-parse", "HEAD")
}

// An abort that loses to another git's lock is tried again.
func TestRebaseAbortOutlastsALock(t *testing.T) {
	for _, fails := range []int{1, abortTries - 1} {
		t.Run(fmt.Sprint(fails), func(t *testing.T) {
			wt, base, head := conflicting(t)
			failingAborts(t, fails)
			_, err := RebaseOnto(ctx, wt, "feat", base)
			var conflict *OntoConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("err %v", err)
			}
			if InProgress(ctx, wt) != "" || git(t, wt, "rev-parse", "HEAD") != head {
				t.Fatal("not undone")
			}
		})
	}
}

// One that never works is never called undone.
func TestRebaseThatCouldNotBeUndone(t *testing.T) {
	wt, base, _ := conflicting(t)
	failingAborts(t, abortTries)
	_, err := RebaseOnto(ctx, wt, "feat", base)
	var left *RebaseLeftError
	var conflict *OntoConflictError
	if !errors.As(err, &left) || errors.As(err, &conflict) || left.Dir != wt {
		t.Fatalf("err %v", err)
	}
	if InProgress(ctx, wt) != "rebase" {
		t.Fatal("said left, and is not")
	}
}

// The same for the rebase onto a branch's own remote, when pushing.
func TestPushRebaseThatCouldNotBeUndone(t *testing.T) {
	_, wt, origin := ontoRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	git(t, filepath.Dir(other), "clone", "-q", "-b", "feat", origin, other)
	configure(t, other)
	commit(t, other, "feat.txt", "theirs\n", "theirs")
	git(t, other, "push", "-q", "origin", "feat")
	commit(t, wt, "feat.txt", "ours\n", "ours")
	failingAborts(t, abortTries)
	_, err := PushRebase(ctx, wt, "feat")
	var left *RebaseLeftError
	var conflict *RebaseConflictError
	if !errors.As(err, &left) || errors.As(err, &conflict) {
		t.Fatalf("err %v", err)
	}
}

// A cancelled rebase is undone all the same, and where none began there is
// nothing to abort.
func TestAbortRebase(t *testing.T) {
	wt, base, head := conflicting(t)
	if err := abortRebase(ctx, wt); err != nil {
		t.Fatalf("nothing under way: %v", err)
	}
	if exec.Command("git", "-C", wt, "rebase", base).Run() == nil {
		t.Fatal("no conflict to stop on")
	}
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if err := abortRebase(context.WithoutCancel(gone), wt); err != nil || InProgress(ctx, wt) != "" || git(t, wt, "rev-parse", "HEAD") != head {
		t.Fatalf("not undone: %v", err)
	}
}
