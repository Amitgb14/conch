package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func bundleGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Every branch and tag travels, the branch asked for is checked out with
// its files, and there is no remote.
func TestBundleAllAndInitFromBundle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	bundleGit(t, dir, "init", "-q", "-b", "main", src)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("a\n"), 0o644)
	bundleGit(t, src, "add", "a.txt")
	bundleGit(t, src, "commit", "-q", "-m", "one")
	bundleGit(t, src, "tag", "v1")
	bundleGit(t, src, "checkout", "-q", "-b", "dev")
	os.WriteFile(filepath.Join(src, "b.txt"), []byte("b\n"), 0o644)
	bundleGit(t, src, "add", "b.txt")
	bundleGit(t, src, "commit", "-q", "-m", "two")

	file := filepath.Join(dir, "all.bundle")
	if err := BundleAll(ctx, src, file); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "deep", "dst") // its parent is made too
	if err := InitFromBundle(ctx, file, dst, "dev"); err != nil {
		t.Fatal(err)
	}
	if got := bundleGit(t, dst, "branch", "--show-current"); got != "dev" {
		t.Fatalf("checked out %q", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "b.txt")); err != nil {
		t.Fatal("dev's file is not in the checkout")
	}
	if st := bundleGit(t, dst, "status", "--porcelain"); st != "" {
		t.Fatalf("not clean:\n%s", st)
	}
	if got := bundleGit(t, dst, "for-each-ref", "--format=%(refname)"); got != "refs/heads/dev\nrefs/heads/main\nrefs/tags/v1" {
		t.Fatalf("refs:\n%s", got)
	}
	if got := bundleGit(t, dst, "remote"); got != "" {
		t.Fatalf("remote %q", got)
	}

	// A branch the bundle lacks, or none named: its first branch.
	for _, b := range []string{"gone", ""} {
		d := filepath.Join(dir, "x"+b)
		if err := InitFromBundle(ctx, file, d, b); err != nil {
			t.Fatal(err)
		}
		if got := bundleGit(t, d, "branch", "--show-current"); got != "dev" {
			t.Fatalf("%q: checked out %q", b, got)
		}
	}

	// No commits: said, and no bundle made.
	empty := filepath.Join(dir, "empty")
	bundleGit(t, dir, "init", "-q", empty)
	if err := BundleAll(ctx, empty, filepath.Join(dir, "e.bundle")); !errors.Is(err, ErrNoCommits) {
		t.Fatalf("empty: %v", err)
	}
	// Not a bundle.
	bad := filepath.Join(dir, "bad.bundle")
	os.WriteFile(bad, []byte("nope"), 0o644)
	if err := InitFromBundle(ctx, bad, filepath.Join(dir, "y"), "main"); err == nil {
		t.Fatal("not a bundle, and no error")
	}
}
