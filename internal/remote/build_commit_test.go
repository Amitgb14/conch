package remote

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/buildinfo"
)

// A conch built at one commit, its source tree since moved to another:
// what it builds for a machine is its own commit, from the same
// repository, and the checkout is left alone.

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// twoCommits makes a repository whose deep/x.go says "first" at the first
// commit and "second" at the second, which is checked out.
func twoCommits(t *testing.T) (src, first, second string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	src = a4FakeSource(t)
	gitIn(t, src, "init", "-q")
	x := filepath.Join(src, "internal", "deep", "x.go")
	os.WriteFile(x, []byte("package deep // first\n"), 0o600)
	gitIn(t, src, "add", "-A")
	gitIn(t, src, "commit", "-q", "-m", "first")
	first = gitIn(t, src, "rev-parse", "HEAD")
	os.WriteFile(x, []byte("package deep // second\n"), 0o600)
	gitIn(t, src, "commit", "-q", "-am", "second")
	second = gitIn(t, src, "rev-parse", "HEAD")
	return src, first, second
}

func withCommit(t *testing.T, c string) {
	t.Helper()
	old := buildinfo.Commit
	buildinfo.Commit = c
	t.Cleanup(func() { buildinfo.Commit = old })
}

func TestAtCommit(t *testing.T) {
	src, first, second := twoCommits(t)
	ctx := context.Background()
	if head, ok := atCommit(ctx, src, first); !ok || head != second {
		t.Fatalf("moved on: %q %v", head, ok)
	}
	if _, ok := atCommit(ctx, src, second); ok {
		t.Fatal("the same commit is built as the tree is, uncommitted changes and all")
	}
	if _, ok := atCommit(ctx, src, strings.Repeat("e", 40)); ok {
		t.Fatal("a commit the repository lacks can't be built")
	}
	if _, ok := atCommit(ctx, src, ""); ok {
		t.Fatal("no commit known: the tree as it is")
	}
	if _, ok := atCommit(ctx, "", first); ok {
		t.Fatal("no tree")
	}
	if _, ok := atCommit(ctx, t.TempDir(), first); ok {
		t.Fatal("not a git checkout")
	}
}

func TestExportCommitLeavesTheCheckoutAlone(t *testing.T) {
	src, first, second := twoCommits(t)
	dir, err := exportCommit(context.Background(), src, first)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	b, _ := os.ReadFile(filepath.Join(dir, "internal", "deep", "x.go"))
	if !strings.Contains(string(b), "first") {
		t.Fatalf("exported %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, ".src.tar")); !os.IsNotExist(err) {
		t.Fatal("the archive was left in the tree")
	}
	if gitIn(t, src, "rev-parse", "HEAD") != second || gitIn(t, src, "status", "--porcelain") != "" ||
		strings.Count(gitIn(t, src, "worktree", "list"), "\n") != 0 {
		t.Fatal("the checkout was changed")
	}
	if _, err := exportCommit(context.Background(), src, strings.Repeat("e", 40)); err == nil {
		t.Fatal("a missing commit exported")
	}
}

// fakeGo puts a go on PATH whose build writes the tree it was run in and
// what x.go says there into the -o file.
func fakeGo(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
out=""
while [ $# -gt 0 ]; do [ "$1" = "-o" ] && { shift; out="$1"; }; shift; done
{ pwd; cat internal/deep/x.go; } > "$out"
`
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestA4CrossBuildBuildsThisConchsCommit(t *testing.T) {
	a4Env(t)
	src, first, second := twoCommits(t)
	a4NoSource(t)
	t.Setenv("CONCH_SOURCE", src)
	fakeGo(t)
	withCommit(t, first)

	var steps []string
	say := func(s string) { steps = append(steps, s) }
	out, err := crossBuild(context.Background(), "linux/amd64", say)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if !strings.Contains(string(b), "first") || strings.Contains(string(b), src+"\n") {
		t.Fatalf("built from the tree as it is, not this conch's commit:\n%s", b)
	}
	if len(steps) != 1 || !strings.Contains(steps[0], "from commit "+first[:12]) || !strings.Contains(steps[0], "is on "+second[:12]) {
		t.Fatalf("steps %q", steps)
	}
	// The exported tree is gone afterwards.
	if dir := strings.SplitN(string(b), "\n", 2)[0]; dir != "" {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("export left behind at %s", dir)
		}
	}

	// Again: a hit on the commit, whatever the tree does meanwhile.
	steps = nil
	os.WriteFile(filepath.Join(src, "internal", "deep", "y.go"), []byte("package deep\n"), 0o600)
	if got, err := crossBuild(context.Background(), "linux/amd64", say); err != nil || got != out || len(steps) != 0 {
		t.Fatalf("second time: %q %v %q", got, err, steps)
	}

	// This conch at the tree's own commit: the tree as it is, as before.
	withCommit(t, second)
	steps = nil
	if _, err := crossBuild(context.Background(), "linux/amd64", say); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(out)
	if !strings.Contains(string(b), "second") || len(steps) != 1 || !strings.Contains(steps[0], "from "+src) {
		t.Fatalf("same commit: %q\n%s", steps, b)
	}
}
