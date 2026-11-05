package gitx

import (
	"context"
	"strings"
	"testing"
)

func TestShowCommit(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	root := git(t, dir, "rev-parse", "HEAD")
	commit(t, dir, "a.go", "package a\n\nfunc A() {}\n", "Add a\n\nBecause b needs it.\nTwo lines.")
	git(t, dir, "mv", "README.md", "README.txt")
	write(t, dir, "a.go", "package a\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "Rename and trim")
	head := git(t, dir, "rev-parse", "HEAD")

	d, err := ShowCommit(ctx, dir, head)
	if err != nil {
		t.Fatal(err)
	}
	if d.Hash != head || d.Subject != "Rename and trim" || d.Author != "Tester" || d.Email != "t@example.com" || len(d.Parents) != 1 || d.Time.IsZero() {
		t.Fatalf("%+v", d)
	}
	byPath := map[string]FileStat{}
	for _, f := range d.Files {
		byPath[f.Path] = f
	}
	if f := byPath["README.txt"]; f.Code != "R" || f.OrigPath != "README.md" {
		t.Fatalf("rename %+v in %+v", f, d.Files)
	}
	if f := byPath["a.go"]; f.Code != "M" || f.Deleted != 2 {
		t.Fatalf("modify %+v", f)
	}

	d, _ = ShowCommit(ctx, dir, "HEAD~1")
	if d.Body != "Because b needs it.\nTwo lines." || len(d.Files) != 1 || d.Files[0].Code != "A" || d.Files[0].Added != 3 {
		t.Fatalf("with a body: %+v", d)
	}
	// The root commit: against nothing.
	d, err = ShowCommit(ctx, dir, root)
	if err != nil || len(d.Parents) != 0 || len(d.Files) != 1 || d.Files[0].Path != "README.md" || d.Files[0].Code != "A" {
		t.Fatalf("root %+v %v", d, err)
	}

	for _, bad := range []string{"", "--all", "a..b", "x;rm", "nope"} {
		if _, err := ShowCommit(ctx, dir, bad); err == nil {
			t.Errorf("ShowCommit(%q) answered", bad)
		}
	}
}

func TestCompareAndFileDiff(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	from := git(t, dir, "rev-parse", "HEAD")
	commit(t, dir, "a.go", "one\n", "a")
	commit(t, dir, "a.go", "one\ntwo\n", "a again")
	commit(t, dir, "b.go", "b\n", "b")
	to := git(t, dir, "rev-parse", "HEAD")

	c, err := CompareCommits(ctx, dir, from, to)
	if err != nil || c.Commits != 3 || len(c.Files) != 2 {
		t.Fatalf("%+v %v", c, err)
	}
	if c.Files[0].Path != "a.go" || c.Files[0].Added != 2 || c.Files[0].Code != "A" {
		t.Fatalf("files %+v", c.Files)
	}
	if _, err := CompareCommits(ctx, dir, "-x", to); err == nil {
		t.Fatal("an option taken as a commit")
	}

	diff, err := CommitFileDiff(ctx, dir, "", to+"~1", "a.go")
	if err != nil || !strings.Contains(diff, "+two") || strings.Contains(diff, "+one") {
		t.Fatalf("one commit's diff of a.go: %q %v", diff, err)
	}
	diff, err = CommitFileDiff(ctx, dir, from, to, "a.go")
	if err != nil || !strings.Contains(diff, "+one") || !strings.Contains(diff, "+two") {
		t.Fatalf("the range's diff: %q %v", diff, err)
	}
	if _, err := CommitFileDiff(ctx, dir, "", to, "--help"); err == nil {
		t.Fatal("an option taken as a path")
	}
	// The root commit's own diff, against nothing.
	if diff, err := CommitFileDiff(ctx, dir, "", from, "README.md"); err != nil || !strings.Contains(diff, "+hello") {
		t.Fatalf("root %q %v", diff, err)
	}
}
