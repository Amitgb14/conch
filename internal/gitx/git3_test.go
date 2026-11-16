package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Git 3.0 makes a new repository SHA-256 with reftable refs on a branch
// called main (Documentation/BreakingChanges). Existing repositories keep
// what they have, so conch meets both kinds for years, side by side.
// These tests make the new kind with today's git and run conch against it.

// git3 says whether this git can make a Git 3 repository: reftable came
// in 2.45.
func git3(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	out, err := exec.Command("git", "init", "-q", "--object-format=sha256", "--ref-format=reftable", dir).CombinedOutput()
	if err != nil {
		t.Skipf("this git cannot make a SHA-256 reftable repository: %v\n%s", err, out)
	}
}

// newGit3Repo is newRepo the way git 3 makes one.
func newGit3Repo(t *testing.T) string {
	t.Helper()
	git3(t)
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dir = resolve(dir)
	git(t, dir, "init", "-q", "--object-format=sha256", "--ref-format=reftable", "-b", "main")
	configure(t, dir)
	commit(t, dir, "README.md", "hello\n", "initial")
	return dir
}

// A reftable repository keeps no refs/heads files: every ref lives in
// reftable/, HEAD included, so a stamp that read only the files backend
// never moved there and a branch on screen went stale.
func TestMetaStampReftable(t *testing.T) {
	root := newGit3Repo(t)
	common := filepath.Join(root, ".git")
	s1 := MetaStamp(common)
	if s := MetaStamp(common); s != s1 {
		t.Fatal("unstable stamp")
	}
	git(t, root, "branch", "other")
	s2 := MetaStamp(common)
	if s2 == s1 {
		t.Error("stamp unchanged after a branch was made")
	}
	git(t, root, "update-ref", "refs/heads/other", "HEAD") // same value: still a write
	git(t, root, "branch", "-D", "other")
	s3 := MetaStamp(common)
	if s3 == s2 {
		t.Error("stamp unchanged after a branch was deleted")
	}
	git(t, root, "checkout", "-q", "-b", "side")
	if s := MetaStamp(common); s == s3 {
		t.Error("stamp unchanged after checkout")
	}

	// A linked worktree keeps its own HEAD in its own reftable.
	wt := filepath.Join(filepath.Dir(root), "linked")
	git(t, root, "worktree", "add", "-q", "-b", "feat", wt)
	s4 := MetaStamp(common)
	configure(t, wt)
	commit(t, wt, "f.txt", "f\n", "on feat")
	git(t, wt, "checkout", "-q", "--detach")
	if s := MetaStamp(common); s == s4 {
		t.Error("stamp unchanged after a linked worktree moved HEAD")
	}
}

// What the graph, the branch list and the changes view read works the
// same on 64-character hashes and reftable refs.
func TestGit3Repository(t *testing.T) {
	root := newGit3Repo(t)
	head := git(t, root, "rev-parse", "HEAD")
	if len(head) != 64 {
		t.Fatalf("not a SHA-256 repository: %q", head)
	}
	r, err := Discover(ctx, root)
	if err != nil || r.Root != root {
		t.Fatalf("Discover = %+v, %v", r, err)
	}
	if b := DefaultBase(ctx, root); b != "main" {
		t.Errorf("DefaultBase = %q", b)
	}
	wt := filepath.Join(filepath.Dir(root), "wt")
	if err := AddWorktree(ctx, root, wt, "feat", "main"); err != nil {
		t.Fatal(err)
	}
	configure(t, wt)
	commit(t, wt, "a.txt", "a\n", "add a")
	write(t, wt, "a.txt", "a\nb\n")

	commits, more, err := Log(ctx, root, 10)
	if err != nil || more || len(commits) != 2 {
		t.Fatalf("Log = %d commits, more %v, %v", len(commits), more, err)
	}
	for _, c := range commits {
		if len(c.Hash) != 64 {
			t.Errorf("hash %q", c.Hash)
		}
		for _, p := range c.Parents {
			if len(p) != 64 {
				t.Errorf("parent %q", p)
			}
		}
	}
	if commits[0].Parents[0] != head {
		t.Errorf("feat's parent %q, want %q", commits[0].Parents[0], head)
	}

	bs, err := Branches(ctx, root, "main")
	if err != nil {
		t.Fatal(err)
	}
	var feat *Branch
	for i := range bs {
		if bs[i].Name == "feat" {
			feat = &bs[i]
		}
	}
	if feat == nil || feat.BaseAhead != 1 {
		t.Errorf("Branches = %+v", bs)
	}
	wts, err := Worktrees(ctx, root)
	if err != nil || len(wts) != 2 {
		t.Fatalf("Worktrees = %+v, %v", wts, err)
	}
	ch, err := WorktreeChanges(ctx, wt, "main")
	if err != nil || len(ch.Files) == 0 {
		t.Fatalf("WorktreeChanges = %+v, %v", ch, err)
	}
	d, err := ShowCommit(ctx, root, "feat")
	if err != nil || len(d.Hash) != 64 {
		t.Fatalf("ShowCommit = %+v, %v", d, err)
	}
	if Ahead(ctx, root, "feat", "main") != 1 || !Merged(ctx, root, "main", "feat") {
		t.Error("ahead/merged wrong on SHA-256")
	}
	if c := CommitOf(ctx, root, "feat"); len(c) != 64 {
		t.Errorf("CommitOf = %q", c)
	}
	if f := Format(ctx, root); f.Objects != "sha256" || f.Refs != "reftable" {
		t.Errorf("Format = %+v", f)
	}
	if f := Format(ctx, newRepo(t)); f.Objects != "sha1" || f.Refs != "files" {
		t.Errorf("Format of a git 2 repository = %+v", f)
	}
	if f := Format(ctx, t.TempDir()); f != (RepoFormat{}) {
		t.Errorf("Format outside a repository = %+v", f)
	}
	if !isHash(head) {
		t.Error("isHash refuses a SHA-256 hash")
	}
	if got, _ := HaveCommits(ctx, root, []string{head}); len(got) != 1 {
		t.Errorf("HaveCommits = %v", got)
	}
}

// A bundle carries its repository's hash. The repository made to take it
// has to be made with the same one, whatever git would make by default —
// SHA-1 now, SHA-256 under git 3 — or the fetch is refused.
func TestInitFromBundleKeepsObjectFormat(t *testing.T) {
	git3(t)
	for _, c := range []struct{ src, dflt string }{
		{"sha256", "sha1"}, // a git 3 repository moved to a machine with git 2
		{"sha1", "sha256"}, // an old repository moved to a machine with git 3
	} {
		t.Run(c.src+"-into-"+c.dflt, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "src")
			bundleGit(t, dir, "init", "-q", "--object-format="+c.src, "-b", "main", src)
			os.WriteFile(filepath.Join(src, "a.txt"), []byte("a\n"), 0o644)
			bundleGit(t, src, "add", "a.txt")
			bundleGit(t, src, "commit", "-q", "-m", "one")
			file := filepath.Join(dir, "all.bundle")
			if err := BundleAll(ctx, src, file); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_DEFAULT_HASH", c.dflt)
			dst := filepath.Join(dir, "dst")
			if err := InitFromBundle(ctx, file, dst, "main"); err != nil {
				t.Fatal(err)
			}
			if got := bundleGit(t, dst, "rev-parse", "--show-object-format"); got != c.src {
				t.Errorf("made a %s repository from a %s bundle", got, c.src)
			}
			if a, b := bundleGit(t, src, "rev-parse", "HEAD"), bundleGit(t, dst, "rev-parse", "HEAD"); a != b {
				t.Errorf("HEAD %s, want %s", b, a)
			}
		})
	}
}

func TestBundleObjectFormat(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]struct{ body, want string }{
		"v2":        {"# v2 git bundle\nabc refs/heads/main\n\nPACK", "sha1"},
		"v3 sha256": {"# v3 git bundle\n@object-format=sha256\n@filter=blob:none\nabc refs/heads/main\n\nPACK", "sha256"},
		"v3 no cap": {"# v3 git bundle\nabc refs/heads/main\n\nPACK", "sha1"},
		"empty":     {"", "sha1"},
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		os.WriteFile(p, []byte(c.body), 0o644)
		if got := bundleObjectFormat(p); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	if got := bundleObjectFormat(filepath.Join(dir, "missing")); got != "sha1" {
		t.Errorf("missing: %q", got)
	}
}

// A git older than 2.45 does not know --show-ref-format, and rev-parse
// prints an option it does not know back as if it were an argument,
// exiting 0 (Apple's git 2.39 does): that is no refs backend.
func TestRefsAnswer(t *testing.T) {
	for in, want := range map[string]string{
		"reftable\n":          "reftable",
		"files\n":             "files",
		"--show-ref-format\n": "",
		"":                    "",
		"  \n":                "",
	} {
		if got := refsAnswer(in); got != want {
			t.Errorf("refsAnswer(%q) = %q, want %q", in, got, want)
		}
	}
}
