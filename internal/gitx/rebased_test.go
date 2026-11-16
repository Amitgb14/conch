package gitx

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// rebasedRepo is a feat branch, pushed, rebased onto a main that changed
// the same line, with the conflict resolved by hand — what an agent asked
// to resolve leaves behind. It returns the tips before and after.
func rebasedRepo(t *testing.T) (root, wt, origin, before, after string) {
	t.Helper()
	root, wt, origin = ontoRepo(t)
	commit(t, wt, "shared.txt", "feat's line\n", "feat edits shared")
	commit(t, wt, "own.txt", "own\n", "feat's own file")
	if err := Push(ctx, wt, "feat"); err != nil {
		t.Fatal(err)
	}
	pushMain(t, origin, "shared.txt", "main's line\n")
	git(t, wt, "fetch", "-q", "origin")
	before = git(t, wt, "rev-parse", "HEAD")
	cmd := exec.Command("git", "-C", wt, "rebase", "origin/main")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected a conflict:\n%s", out)
	}
	write(t, wt, "shared.txt", "main's line\nfeat's line\n")
	git(t, wt, "add", "shared.txt")
	cmd = exec.Command("git", "-C", wt, "rebase", "--continue")
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("continue: %v\n%s", err, out)
	}
	return root, wt, origin, before, git(t, wt, "rev-parse", "HEAD")
}

func TestLastRebase(t *testing.T) {
	_, wt, _, before, after := rebasedRepo(t)
	r, err := LastRebase(ctx, wt, "feat")
	if err != nil || r == nil {
		t.Fatalf("%+v %v", r, err)
	}
	if r.Before != before || r.After != after || r.Onto != git(t, wt, "rev-parse", "origin/main") {
		t.Errorf("tips: %+v (before %s after %s)", r, before, after)
	}
	if r.At.IsZero() {
		t.Error("no time")
	}
	// A commit after the rebase: it is no longer the newest thing done.
	commit(t, wt, "later.txt", "later\n", "later")
	if r, err := LastRebase(ctx, wt, "feat"); err != nil || r != nil {
		t.Errorf("after a commit: %+v %v", r, err)
	}
}

func TestLastRebaseNone(t *testing.T) {
	_, wt := taskRepo(t)
	if r, err := LastRebase(ctx, wt, "feat"); err != nil || r != nil {
		t.Errorf("never rebased: %+v %v", r, err)
	}
	commit(t, wt, "a.txt", "a\n", "a")
	if r, err := LastRebase(ctx, wt, "feat"); err != nil || r != nil {
		t.Errorf("committed: %+v %v", r, err)
	}
	if r, err := LastRebase(ctx, wt, "nope"); err != nil || r != nil {
		t.Errorf("no such branch: %+v %v", r, err)
	}
}

// Each commit before against its rewrite: the one whose conflict was
// resolved changed, the other came across as it was.
func TestRewritten(t *testing.T) {
	_, wt, _, before, after := rebasedRepo(t)
	r, _ := LastRebase(ctx, wt, "feat")
	cs, err := Rewritten(ctx, wt, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 3 {
		t.Fatalf("%+v", cs)
	}
	byS := map[string]Rewrite{}
	for _, c := range cs {
		byS[c.Subject] = c
	}
	if c := byS["feat edits shared"]; c.Change != RewriteChanged || c.Old == "" || c.New == "" || c.Old == c.New {
		t.Errorf("resolved: %+v", c)
	}
	if c := byS["feat's own file"]; c.Change != RewriteSame || !strings.HasPrefix(after, c.New) || !strings.HasPrefix(before, c.Old) {
		t.Errorf("unchanged: %+v", c)
	}
	if c := byS["feat"]; c.Change != RewriteSame {
		t.Errorf("first: %+v", c)
	}
	// Oldest first, as the branch has them.
	if cs[2].Subject != "feat's own file" {
		t.Errorf("order: %+v", cs)
	}
	patch, err := RewriteDiff(ctx, wt, r, 0)
	if err != nil || !strings.Contains(patch, "feat edits shared") || !strings.Contains(patch, "main's line") {
		t.Errorf("diff %v:\n%s", err, patch)
	}
	if short, _ := RewriteDiff(ctx, wt, r, 40); len(short) > 40+len(cutNote) {
		t.Errorf("cut: %d", len(short))
	}
}

// Undo puts the branch back where it was, keeping the rewrite in the
// reflog; refused once the branch moved on or with changes not committed.
func TestUndoRebase(t *testing.T) {
	_, wt, _, before, after := rebasedRepo(t)
	write(t, wt, "own.txt", "edited\n")
	if err := UndoRebase(ctx, wt, "feat", before, after); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("dirty: %v", err)
	}
	git(t, wt, "checkout", "--", "own.txt")
	if err := UndoRebase(ctx, wt, "feat", before, "0123456789012345678901234567890123456789"); err == nil {
		t.Fatal("moved on")
	}
	if err := UndoRebase(ctx, wt, "feat", before, after); err != nil {
		t.Fatal(err)
	}
	if got := git(t, wt, "rev-parse", "HEAD"); got != before {
		t.Fatalf("head %s, want %s", got, before)
	}
	if r, _ := LastRebase(ctx, wt, "feat"); r != nil {
		t.Errorf("still says rebased: %+v", r)
	}
}

// The rebased branch replaces the pushed one, which the remote still has.
func TestRebasedPushOver(t *testing.T) {
	_, wt, origin, before, after := rebasedRepo(t)
	if got := PushedHead(ctx, wt, "feat"); got != before {
		t.Fatalf("pushed %s", got)
	}
	if err := PushOver(ctx, wt, "feat", before); err != nil {
		t.Fatal(err)
	}
	if got := git(t, origin, "rev-parse", "feat"); got != after {
		t.Errorf("origin has %s", got)
	}
}
