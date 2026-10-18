package gitx

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ontoRepo is taskRepo with a bare origin both branches are pushed to.
func ontoRepo(t *testing.T) (root, wt, origin string) {
	t.Helper()
	root, wt = taskRepo(t)
	origin = filepath.Join(t.TempDir(), "origin.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	git(t, root, "remote", "add", "origin", origin)
	git(t, root, "push", "-q", "-u", "origin", "main")
	commit(t, wt, "feat.txt", "feat\n", "feat")
	if err := Push(ctx, wt, "feat"); err != nil {
		t.Fatal(err)
	}
	return root, wt, origin
}

// pushMain puts a commit on main as somebody else would: on the remote,
// not in this checkout.
func pushMain(t *testing.T, origin, name, content string) {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	git(t, filepath.Dir(other), "clone", "-q", "-b", "main", origin, other)
	configure(t, other)
	commit(t, other, name, content, "main changes "+name)
	git(t, other, "push", "-q", "origin", "main")
}

func TestFreshBaseAndRebaseOnto(t *testing.T) {
	root, wt, origin := ontoRepo(t)
	pushMain(t, origin, "other.txt", "other\n")
	base := FreshBase(ctx, wt, "feat", "main")
	if base != "origin/main" || !resolves(ctx, root, "origin/main") {
		t.Fatalf("base %q", base)
	}
	if IsAncestor(ctx, wt, base, "HEAD") {
		t.Fatal("on top of a base it has not taken")
	}
	moved, err := RebaseOnto(ctx, wt, "feat", base)
	if err != nil || !moved || !IsAncestor(ctx, wt, base, "HEAD") {
		t.Fatalf("rebase: %v %v", moved, err)
	}
	if moved, err := RebaseOnto(ctx, wt, "feat", base); err != nil || moved {
		t.Fatalf("again: %v %v", moved, err)
	}
	// "origin/main" given is the same as "main".
	if got := FreshBase(ctx, wt, "feat", "origin/main"); got != "origin/main" {
		t.Fatalf("named with its remote: %q", got)
	}
	if got := FreshBase(ctx, wt, "feat", ""); got != "" {
		t.Fatalf("no base: %q", got)
	}
}

func TestFreshBaseWithoutARemote(t *testing.T) {
	_, wt := taskRepo(t)
	if got := FreshBase(ctx, wt, "feat", "main"); got != "main" {
		t.Fatalf("no remote: %q", got)
	}
	if got := FreshBase(ctx, wt, "feat", "nothing"); got != "nothing" {
		t.Fatalf("no remote, no branch: %q", got)
	}
}

// A conflict is undone and named; uncommitted work stops it before it
// starts; a branch not checked out there is refused.
func TestRebaseOntoRefusals(t *testing.T) {
	_, wt, origin := ontoRepo(t)
	pushMain(t, origin, "feat.txt", "theirs\n")
	base := FreshBase(ctx, wt, "feat", "main")
	head := git(t, wt, "rev-parse", "HEAD")
	_, err := RebaseOnto(ctx, wt, "feat", base)
	var conflict *OntoConflictError
	if !errors.As(err, &conflict) || !reflect.DeepEqual(conflict.Files, []string{"feat.txt"}) || !strings.Contains(err.Error(), "undone") {
		t.Fatalf("conflict: %v", err)
	}
	if git(t, wt, "rev-parse", "HEAD") != head || InProgress(ctx, wt) != "" {
		t.Fatal("the conflict was not undone")
	}
	if got := BaseChanges(ctx, wt, "feat", base, conflict.Files, 5); !strings.Contains(got, "main changes feat.txt") {
		t.Fatalf("base changes: %q", got)
	}

	write(t, wt, "feat.txt", "dirty\n")
	if _, err := RebaseOnto(ctx, wt, "feat", base); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("dirty: %v", err)
	}
	git(t, wt, "checkout", "-q", "--", "feat.txt")
	if _, err := RebaseOnto(ctx, wt, "main", base); err == nil || !strings.Contains(err.Error(), "not checked out") {
		t.Fatalf("other branch: %v", err)
	}
}

// What a branch deleted is what its pull request deletes.
func TestDeleted(t *testing.T) {
	_, wt := taskRepo(t)
	if got, err := Deleted(ctx, wt, "main"); err != nil || len(got) != 0 {
		t.Fatalf("nothing: %v %v", got, err)
	}
	git(t, wt, "rm", "-q", "README.md")
	git(t, wt, "commit", "-q", "-m", "drop it")
	if got, _ := Deleted(ctx, wt, "main"); !reflect.DeepEqual(got, []string{"README.md"}) {
		t.Fatalf("deleted %v", got)
	}
	if _, err := Deleted(ctx, wt, "no-such-base"); err == nil {
		t.Fatal("no base")
	}
}

// A rewritten branch replaces the pushed one only while the remote still
// has what this checkout last saw there.
func TestPushOver(t *testing.T) {
	_, wt, origin := ontoRepo(t)
	pushed := PushedHead(ctx, wt, "feat")
	if pushed == "" || pushed != git(t, origin, "rev-parse", "feat") {
		t.Fatalf("pushed head %q", pushed)
	}
	git(t, wt, "commit", "-q", "--amend", "-m", "feat, reworded")
	if err := Push(ctx, wt, "feat"); err == nil {
		t.Fatal("a plain push replaced a rewritten branch")
	}
	if err := PushOver(ctx, wt, "feat", "0000000000000000000000000000000000000001"); !errors.As(err, new(*PushRejectedError)) {
		t.Fatalf("a stale lease: %v", err)
	}
	if err := PushOver(ctx, wt, "feat", pushed); err != nil {
		t.Fatal(err)
	}
	if git(t, origin, "rev-parse", "feat") != git(t, wt, "rev-parse", "HEAD") {
		t.Fatal("not replaced")
	}
	_, fresh := taskRepo(t)
	if got := PushedHead(ctx, fresh, "feat"); got != "" {
		t.Fatalf("never pushed: %q", got)
	}
}

func TestDiffSizeAndChanged(t *testing.T) {
	_, wt := taskRepo(t)
	if n, err := DiffSize(ctx, wt, "main"); err != nil || n != 0 {
		t.Fatalf("nothing: %d %v", n, err)
	}
	commit(t, wt, "a.go", "one\ntwo\nthree\n", "three lines")
	write(t, wt, "README.md", "changed\n")
	git(t, wt, "commit", "-qam", "one changed")
	write(t, wt, "bin.dat", "\x00\x01\x02")
	git(t, wt, "add", "bin.dat")
	git(t, wt, "commit", "-qm", "binary")
	if n, err := DiffSize(ctx, wt, "main"); err != nil || n != 5 { // 3 added, 1 added and 1 removed, binary not counted
		t.Fatalf("size %d %v", n, err)
	}
	if got, _ := Changed(ctx, wt, "main"); !reflect.DeepEqual(got, []string{"README.md", "a.go", "bin.dat"}) {
		t.Fatalf("changed %v", got)
	}
	if _, err := DiffSize(ctx, wt, "nothing"); err == nil {
		t.Fatal("no base")
	}
	if _, err := Changed(ctx, wt, "nothing"); err == nil {
		t.Fatal("no base")
	}
}
