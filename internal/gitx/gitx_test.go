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

var ctx = context.Background()

func TestMain(m *testing.M) {
	// Keep the developer's own git config out of the tests.
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Exit(m.Run())
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	write(t, dir, name, content)
	git(t, dir, "add", "--", name)
	git(t, dir, "commit", "-q", "-m", msg)
}

func configure(t *testing.T, dir string) {
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "commit.gpgsign", "false")
}

// newRepo makes a repo with one commit on main and returns its resolved root.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dir = resolve(dir)
	git(t, dir, "init", "-q", "-b", "main")
	configure(t, dir)
	commit(t, dir, "README.md", "hello\n", "initial")
	return dir
}

func TestDiscover(t *testing.T) {
	root := newRepo(t)
	write(t, root, "sub/dir/x.txt", "x\n")
	wt := filepath.Join(filepath.Dir(root), "linked")
	git(t, root, "worktree", "add", "-q", "-b", "feat", wt)

	for _, dir := range []string{root, filepath.Join(root, "sub", "dir"), wt} {
		r, err := Discover(ctx, dir)
		if err != nil {
			t.Fatalf("Discover(%s): %v", dir, err)
		}
		if r.Root != root {
			t.Errorf("Discover(%s).Root = %q, want %q", dir, r.Root, root)
		}
		if r.CommonDir != filepath.Join(root, ".git") {
			t.Errorf("Discover(%s).CommonDir = %q", dir, r.CommonDir)
		}
	}

	if _, err := Discover(ctx, t.TempDir()); !errors.Is(err, ErrNotRepo) {
		t.Errorf("Discover(non-repo) err = %v, want ErrNotRepo", err)
	}
}

func TestDefaultBase(t *testing.T) {
	root := newRepo(t)
	git(t, root, "checkout", "-q", "-b", "feat")
	if got := DefaultBase(ctx, root); got != "main" {
		t.Errorf("DefaultBase = %q, want main", got)
	}
	git(t, root, "checkout", "-q", "main")

	clone := filepath.Join(t.TempDir(), "clone")
	git(t, root, "clone", "-q", "-b", "main", root, clone)
	git(t, clone, "checkout", "-q", "-b", "other")
	git(t, clone, "branch", "-q", "-D", "main")
	if got := DefaultBase(ctx, clone); got != "origin/main" {
		t.Errorf("DefaultBase(clone without local main) = %q, want origin/main", got)
	}
}

func TestWorktrees(t *testing.T) {
	root := newRepo(t)
	feat := filepath.Join(filepath.Dir(root), "wt feat")
	det := filepath.Join(filepath.Dir(root), "wt-detached")
	git(t, root, "worktree", "add", "-q", "-b", "feat/x", feat)
	git(t, root, "worktree", "add", "-q", "--detach", det)
	git(t, root, "worktree", "lock", det)
	head := git(t, root, "rev-parse", "HEAD")

	wts, err := Worktrees(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 3 {
		t.Fatalf("got %d worktrees: %+v", len(wts), wts)
	}
	want := []Worktree{
		{Path: root, Branch: "main", Head: head, Main: true},
		{Path: feat, Branch: "feat/x", Head: head},
		{Path: det, Head: head, Detached: true, Locked: true},
	}
	for i := range want {
		if wts[i] != want[i] {
			t.Errorf("worktree %d = %+v, want %+v", i, wts[i], want[i])
		}
	}
}

func TestBranches(t *testing.T) {
	root := newRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	git(t, root, "clone", "-q", "--bare", root, origin)
	git(t, root, "remote", "add", "origin", origin)
	git(t, root, "fetch", "-q", "origin")

	git(t, root, "checkout", "-q", "-b", "feat")
	commit(t, root, "a.txt", "a\n", "feat 1")
	commit(t, root, "b.txt", "b\n", "feat 2")
	git(t, root, "push", "-q", "-u", "origin", "feat")
	commit(t, root, "c.txt", "c\n", "feat 3 unpushed")

	git(t, root, "checkout", "-q", "-b", "gone", "main")
	git(t, root, "push", "-q", "-u", "origin", "gone")
	git(t, root, "push", "-q", "origin", "--delete", "gone")
	git(t, root, "fetch", "-q", "--prune", "origin")

	git(t, root, "checkout", "-q", "main")
	commit(t, root, "m.txt", "m\n", "main advance")

	branches, err := Branches(ctx, root, "main")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Branch{}
	for _, b := range branches {
		by[b.Name] = b
	}
	if len(branches) != 3 {
		t.Fatalf("branches = %+v", branches)
	}
	if f := by["feat"]; f.Upstream != "origin/feat" || f.Ahead != 1 || f.Behind != 0 ||
		f.BaseAhead != 3 || f.BaseBehind != 1 || f.Subject != "feat 3 unpushed" || f.Gone {
		t.Errorf("feat = %+v", f)
	}
	if g := by["gone"]; !g.Gone || g.Upstream != "origin/gone" || g.BaseBehind != 1 {
		t.Errorf("gone = %+v", g)
	}
	if m := by["main"]; m.BaseAhead != 0 || m.BaseBehind != 0 || m.Upstream != "" {
		t.Errorf("main = %+v", m)
	}
	for i := 1; i < len(branches); i++ {
		if branches[i].Committed.After(branches[i-1].Committed) {
			t.Errorf("not sorted by commit date: %+v", branches)
		}
	}

	// An unknown base leaves counts at zero.
	branches, err = Branches(ctx, root, "nope")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range branches {
		if b.BaseAhead != 0 || b.BaseBehind != 0 {
			t.Errorf("unknown base: %+v", b)
		}
	}

	// Fallback path used on git < 2.41.
	if a, b := revListCounts(ctx, root, "main", "refs/heads/feat"); a != 3 || b != 1 {
		t.Errorf("revListCounts = %d, %d", a, b)
	}
}

func TestWorktreeStatus(t *testing.T) {
	root := newRepo(t)
	commit(t, root, "old name.txt", "1\n2\n3\n", "add")
	commit(t, root, "mod.txt", "a\nb\n", "add mod")

	s, err := WorktreeStatus(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Clean() {
		t.Errorf("fresh commit not clean: %+v", s)
	}

	git(t, root, "mv", "old name.txt", "new name.txt") // staged rename
	write(t, root, "mod.txt", "a\nB\nc\n")             // unstaged: +2 -1
	write(t, root, "staged.txt", "s\n")
	git(t, root, "add", "staged.txt") // staged add: +1
	write(t, root, "untracked file.txt", "u\n")

	s, err = WorktreeStatus(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	want := Status{Staged: 2, Unstaged: 1, Untracked: 1, Files: 4, Added: 3, Deleted: 1}
	if s != want {
		t.Errorf("status = %+v, want %+v", s, want)
	}

	ch, err := WorktreeChanges(ctx, root, "main")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range ch.Files {
		paths = append(paths, f.Code+" "+f.Path)
	}
	if got := strings.Join(paths, ","); got != "M mod.txt,R new name.txt,A staged.txt,? untracked file.txt" {
		t.Errorf("files = %s", got)
	}
	if f := ch.Files[1]; f.OrigPath != "old name.txt" || !f.Staged || f.Unstaged {
		t.Errorf("rename = %+v", f)
	}
	if f := ch.Files[0]; f.Added != 2 || f.Deleted != 1 || f.Staged || !f.Unstaged {
		t.Errorf("mod = %+v", f)
	}
	if f := ch.Files[3]; f.Added != 1 {
		t.Errorf("untracked = %+v", f)
	}

	// A new folder lists its files, not just "web/".
	write(t, root, "web/index.html", "<p>\n")
	write(t, root, "web/css/style.css", "p{}\n")
	ch, err = WorktreeChanges(ctx, root, "main")
	if err != nil {
		t.Fatal(err)
	}
	paths = nil
	for _, f := range ch.Files {
		if strings.HasPrefix(f.Path, "web") {
			paths = append(paths, f.Path)
		}
	}
	if got := strings.Join(paths, ","); got != "web/css/style.css,web/index.html" {
		t.Errorf("files in a new folder = %q", got)
	}
	if d, err := Diff(ctx, root, "web/index.html", ""); err != nil || !strings.Contains(d, "+<p>") {
		t.Errorf("diff of a file in a new folder = %q, %v", d, err)
	}
}

func TestWorktreeStatusConflict(t *testing.T) {
	root := newRepo(t)
	git(t, root, "checkout", "-q", "-b", "feat")
	commit(t, root, "README.md", "feat\n", "feat")
	git(t, root, "checkout", "-q", "main")
	commit(t, root, "README.md", "main\n", "main")
	if err := exec.Command("git", "-C", root, "merge", "feat").Run(); err == nil {
		t.Fatal("merge unexpectedly succeeded")
	}
	s, err := WorktreeStatus(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if s.Conflicts != 1 || s.Files != 1 {
		t.Errorf("status = %+v", s)
	}
	ch, err := WorktreeChanges(ctx, root, "main")
	if err != nil || len(ch.Files) != 1 || ch.Files[0].Code != "U" {
		t.Errorf("changes = %+v, %v", ch, err)
	}
}

func TestFreshRepo(t *testing.T) {
	dir := resolve(t.TempDir())
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "a\n")
	write(t, dir, "b.txt", "b\nb\n")
	git(t, dir, "add", "b.txt")

	s, err := WorktreeStatus(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if s != (Status{Staged: 1, Untracked: 1, Files: 2, Added: 2}) {
		t.Errorf("status = %+v", s)
	}
	if _, err := WorktreeChanges(ctx, dir, "main"); err != nil {
		t.Errorf("WorktreeChanges: %v", err)
	}
	if d, err := Diff(ctx, dir, "b.txt", ""); err != nil || !strings.Contains(d, "+b") {
		t.Errorf("Diff = %q, %v", d, err)
	}
	if got := DefaultBase(ctx, dir); got != "main" {
		t.Errorf("DefaultBase = %q", got)
	}
}

func TestChanges(t *testing.T) {
	root := newRepo(t)
	commit(t, root, "keep.txt", "1\n2\n3\n", "base")
	git(t, root, "checkout", "-q", "-b", "feat")
	commit(t, root, "new file.txt", "x\ny\n", "feat one")
	write(t, root, "keep.txt", "1\nTWO\n3\n4\n")
	git(t, root, "commit", "-q", "-am", "feat two")
	git(t, root, "checkout", "-q", "main")
	commit(t, root, "main.txt", "m\n", "main moves")

	ch, err := BranchChanges(ctx, root, "feat", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Files) != 2 {
		t.Fatalf("files = %+v", ch.Files)
	}
	if f := ch.Files[0]; f.Path != "keep.txt" || f.Code != "M" || f.Added != 2 || f.Deleted != 1 {
		t.Errorf("keep = %+v", f)
	}
	if f := ch.Files[1]; f.Path != "new file.txt" || f.Code != "A" || f.Added != 2 {
		t.Errorf("new = %+v", f)
	}
	if len(ch.Commits) != 2 || ch.Commits[0].Subject != "feat two" || ch.Commits[1].Subject != "feat one" ||
		ch.Commits[0].Author != "Tester" || len(ch.Commits[0].Hash) != 40 || ch.Commits[0].Time.IsZero() {
		t.Errorf("commits = %+v", ch.Commits)
	}

	// Same branch checked out in a worktree with an uncommitted edit.
	wt := filepath.Join(filepath.Dir(root), "feat-wt")
	git(t, root, "worktree", "add", "-q", wt, "feat")
	write(t, wt, "keep.txt", "1\nTWO\n3\n")
	wc, err := WorktreeChanges(ctx, wt, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(wc.Files) != 1 || wc.Files[0].Deleted != 1 || len(wc.Commits) != 2 {
		t.Errorf("worktree changes = %+v", wc)
	}

	d, err := Diff(ctx, root, "new file.txt", "main...feat")
	if err != nil || !strings.Contains(d, "+x") || strings.Contains(d, "main.txt") {
		t.Errorf("rev diff = %q, %v", d, err)
	}
}

func TestDiff(t *testing.T) {
	root := newRepo(t)
	write(t, root, "README.md", "hello\nworld\n")
	d, err := Diff(ctx, root, "README.md", "")
	if err != nil || !strings.Contains(d, "+world") || strings.Contains(d, "\x1b[") {
		t.Errorf("tracked diff = %q, %v", d, err)
	}

	write(t, root, "sp ace.txt", "brand new\n")
	d, err = Diff(ctx, root, "sp ace.txt", "")
	if err != nil || !strings.Contains(d, "+brand new") {
		t.Errorf("untracked diff = %q, %v", d, err)
	}

	write(t, root, "big.txt", strings.Repeat("line of text\n", 200000))
	d, err = Diff(ctx, root, "big.txt", "")
	if err != nil || !strings.HasSuffix(d, truncatedNote) || len(d) > maxDiff+len(truncatedNote) {
		t.Errorf("big diff len %d, err %v", len(d), err)
	}
}

func TestAddRemoveWorktree(t *testing.T) {
	root := newRepo(t)
	git(t, root, "branch", "existing")

	newPath := WorktreeDir(root, "conch/new-thing")
	if err := AddWorktree(ctx, root, newPath, "conch/new-thing", "main"); err != nil {
		t.Fatal(err)
	}
	if b := git(t, newPath, "branch", "--show-current"); b != "conch/new-thing" {
		t.Errorf("new worktree on %q", b)
	}
	exPath := WorktreeDir(root, "existing")
	if err := AddWorktree(ctx, root, exPath, "existing", "main"); err != nil {
		t.Fatal(err)
	}
	if b := git(t, exPath, "branch", "--show-current"); b != "existing" {
		t.Errorf("existing worktree on %q", b)
	}

	write(t, exPath, "dirty.txt", "x\n")
	if err := RemoveWorktree(ctx, root, exPath); err == nil {
		t.Error("removed a dirty worktree")
	} else if !strings.Contains(err.Error(), "untracked") && !strings.Contains(err.Error(), "modified") {
		t.Errorf("error lacks git's reason: %v", err)
	}
	if err := RemoveWorktree(ctx, root, newPath); err != nil {
		t.Errorf("remove clean worktree: %v", err)
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Errorf("worktree dir still exists: %v", err)
	}

	if err := ValidBranchName(ctx, root, "feat/ok"); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
	for _, bad := range []string{"bad..name", "has space", "-lead", "end.lock"} {
		if ValidBranchName(ctx, root, bad) == nil {
			t.Errorf("invalid name %q accepted", bad)
		}
	}
}

func TestNames(t *testing.T) {
	slugs := []struct{ in, want string }{
		{"feat/Login-Fix", "feat-login-fix"},
		{"  Hello,  World! ", "hello-world"},
		{"..dots..", "dots"},
		{"a_b.c", "a_b.c"},
		{"", "task"},
		{"!!!", "task"},
		{"émoji 🚀 ok", "moji-ok"},
		{strings.Repeat("ab", 40), strings.Repeat("ab", 30)},
		{strings.Repeat("a", 59) + "-b", strings.Repeat("a", 59)},
	}
	for _, c := range slugs {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	prompts := []struct{ project, in, want string }{
		{"conch", "Fix the flaky login tests!", "conch/fix-flaky-login-tests"},
		{"conch", "Could you please add a README to the repo and CI", "conch/add-readme-repo-ci"},
		{"conch", "Don't break /api/v2 routes in server.go now please ok", "conch/dont-break-api-v2-routes"},
		{"conch", "", "conch/task"},
		// The branch belongs to the project it is made in.
		{"OneNutri", "Fix the flaky login tests!", "onenutri/fix-flaky-login-tests"},
		{"onenutri-web", "Add a health check", "onenutri-web/add-health-check"},
		{"My App!", "Ship it", "my-app/ship-it"},
		{"  ", "Ship it", "conch/ship-it"}, // no project name to go on
		{"", "", "conch/task"},
	}
	for _, c := range prompts {
		if got := BranchFromPrompt(c.project, c.in); got != c.want {
			t.Errorf("BranchFromPrompt(%q, %q) = %q, want %q", c.project, c.in, got, c.want)
		}
	}

	for _, c := range []struct{ project, want string }{
		{"conch", "conch/"}, {"OneNutri", "onenutri/"}, {"", "conch/"}, {" ", "conch/"},
		{"task", "task/"}, {"api/v2", "api-v2/"},
	} {
		if got := BranchPrefix(c.project); got != c.want {
			t.Errorf("BranchPrefix(%q) = %q, want %q", c.project, got, c.want)
		}
	}

	if got, want := WorktreeDir("/src/conch", "conch/fix-it"), "/src/conch.worktrees/conch-fix-it"; got != want {
		t.Errorf("WorktreeDir = %q, want %q", got, want)
	}
}

func TestMetaStamp(t *testing.T) {
	root := newRepo(t)
	common := filepath.Join(root, ".git")
	s1 := MetaStamp(common)
	if s2 := MetaStamp(common); s1 != s2 {
		t.Fatalf("unstable stamp: %s vs %s", s1, s2)
	}
	write(t, root, "README.md", "edited but not committed\n")
	if s := MetaStamp(common); s != s1 {
		t.Errorf("worktree edit changed stamp")
	}

	commit(t, root, "README.md", "changed\n", "second")
	s2 := MetaStamp(common)
	if s2 == s1 {
		t.Error("stamp unchanged after commit")
	}
	git(t, root, "branch", "other")
	s3 := MetaStamp(common)
	git(t, root, "checkout", "-q", "other")
	if s := MetaStamp(common); s == s3 {
		t.Error("stamp unchanged after checkout")
	}
	if MetaStamp(filepath.Join(t.TempDir(), "missing")) == "" {
		t.Error("empty stamp for missing dir")
	}
}
