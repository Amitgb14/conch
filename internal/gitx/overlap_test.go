package gitx

import (
	"errors"
	"slices"
	"testing"
)

func TestTouchedFiles(t *testing.T) {
	root, wt := taskRepo(t)
	if got, err := TouchedFiles(ctx, wt, "main"); err != nil || len(got) != 0 {
		t.Fatalf("fresh checkout: %v %v", got, err)
	}
	commit(t, wt, "a.go", "a\n", "a")                        // committed on the branch
	write(t, wt, "README.md", "hello\nchanged\n")            // uncommitted
	write(t, wt, "new dir/b c.go", "b\n")                    // untracked, with a space
	write(t, wt, ".gitignore", "ignored.log\n")              // untracked too
	write(t, wt, "ignored.log", "noise\n")                   // ignored: not touched
	commit(t, root, "main-only.txt", "m\n", "main moves on") // the base moving is not the branch's work
	got, err := TouchedFiles(ctx, wt, "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".gitignore", "README.md", "a.go", "new dir/b c.go"}; !slices.Equal(got, want) {
		t.Fatalf("touched %q, want %q", got, want)
	}
	// No base, or one that doesn't exist: what is uncommitted, at least.
	for _, base := range []string{"", "nope"} {
		got, err := TouchedFiles(ctx, wt, base)
		if err != nil || !slices.Contains(got, "README.md") || slices.Contains(got, "a.go") {
			t.Fatalf("base %q: %v %v", base, got, err)
		}
	}
	if _, err := TouchedFiles(ctx, t.TempDir(), "main"); err == nil {
		t.Fatal("a folder that isn't a checkout")
	}
}

func TestMergeConflicts(t *testing.T) {
	root, wt := taskRepo(t)
	commit(t, wt, "README.md", "branch\n", "branch edits")
	commit(t, wt, "other.txt", "x\n", "branch adds")
	// Nothing on main yet: clean.
	files, err := MergeConflicts(ctx, root, "main", "feat")
	if errors.Is(err, ErrNoMergeTree) {
		t.Skip("git here is older than 2.38")
	}
	if err != nil || len(files) != 0 {
		t.Fatalf("clean: %v %v", files, err)
	}
	// Main changes the same line: it would conflict, in that file only.
	commit(t, root, "README.md", "main\n", "main edits")
	files, err = MergeConflicts(ctx, root, "main", "feat")
	if err != nil || !slices.Equal(files, []string{"README.md"}) {
		t.Fatalf("conflict: %v %v", files, err)
	}
	// Nothing was touched trying.
	if out := git(t, root, "status", "--porcelain"); out != "" {
		t.Fatalf("the checkout changed: %q", out)
	}
	if _, err := MergeConflicts(ctx, root, "main", "no-such-branch"); err == nil {
		t.Fatal("an unknown branch merged")
	}
}
