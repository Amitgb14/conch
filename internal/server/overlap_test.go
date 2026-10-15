package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

func TestSharedFiles(t *testing.T) {
	for _, tc := range []struct{ a, b, want []string }{
		{nil, []string{"x"}, nil},
		{[]string{"a", "c", "e"}, []string{"b", "c", "d", "e"}, []string{"c", "e"}},
		{[]string{"a"}, []string{"b"}, nil},
		{[]string{"1", "2", "3", "4", "5", "6", "7"}, []string{"1", "2", "3", "4", "5", "6", "7"}, []string{"1", "2", "3", "4", "5"}},
	} {
		if got := sharedFiles(tc.a, tc.b); !slices.Equal(got, tc.want) {
			t.Errorf("%v ∩ %v = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestNoteOverlaps(t *testing.T) {
	a5IsolateEnv(t)
	root := filepath.Join(t.TempDir(), "api")
	a5GitRepo(t, root)
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(root, "shared.go"), []byte("one\n"), 0o644)
	run(root, "add", ".")
	run(root, "commit", "-q", "-m", "shared")
	ctx := context.Background()
	wtA, wtB, wtC := root+"-a", root+"-b", root+"-c"
	for b, dir := range map[string]string{"a": wtA, "b": wtB, "c": wtC} {
		if err := gitx.AddWorktree(ctx, root, dir, b, "main"); err != nil {
			t.Fatal(err)
		}
	}
	// a and b both change shared.go; c changes something else.
	os.WriteFile(filepath.Join(wtA, "shared.go"), []byte("a\n"), 0o644)
	os.WriteFile(filepath.Join(wtB, "shared.go"), []byte("b\n"), 0o644)
	os.WriteFile(filepath.Join(wtC, "other.go"), []byte("c\n"), 0o644)
	// b commits its edit, and main changes the same line: b would conflict.
	run(wtB, "commit", "-qam", "b")
	os.WriteFile(filepath.Join(root, "shared.go"), []byte("main\n"), 0o644)
	run(root, "commit", "-qam", "main")

	info := proto.ProjectInfo{Base: "main", Worktrees: []proto.WorktreeInfo{
		{Path: root, Branch: "main", Main: true},
		{Path: wtA, Branch: "a"}, {Path: wtB, Branch: "b"}, {Path: wtC, Branch: "c"},
		{Path: root + "-gone", Branch: "gone"}, // a folder that isn't there: no hint, no error
	}}
	noteOverlaps(ctx, root, &info)
	byBranch := map[string]proto.WorktreeInfo{}
	for _, w := range info.Worktrees {
		byBranch[w.Branch] = w
	}
	if o := byBranch["a"].Overlaps; len(o) != 1 || o[0].Branch != "b" || !slices.Equal(o[0].Files, []string{"shared.go"}) {
		t.Fatalf("a overlaps %+v", o)
	}
	if o := byBranch["b"].Overlaps; len(o) != 1 || o[0].Branch != "a" {
		t.Fatalf("b overlaps %+v", o)
	}
	if o := byBranch["c"].Overlaps; len(o) != 0 {
		t.Fatalf("c overlaps %+v", o)
	}
	if byBranch["main"].Overlaps != nil || byBranch["gone"].Overlaps != nil {
		t.Fatal("the main checkout or a missing one was given overlaps")
	}
	if _, err := gitx.MergeConflicts(ctx, root, "main", "b"); err == gitx.ErrNoMergeTree {
		t.Skip("git here is older than 2.38")
	}
	if c := byBranch["b"].Conflicts; !slices.Equal(c, []string{"shared.go"}) {
		t.Fatalf("b conflicts %v", c)
	}
	if c := byBranch["a"].Conflicts; len(c) != 0 {
		t.Fatalf("a has nothing committed, so nothing to conflict: %v", c)
	}
}
