package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// a6Identity gives git an author without touching the developer's config.
func a6Identity(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "A6")
	t.Setenv("GIT_AUTHOR_EMAIL", "a6@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "A6")
	t.Setenv("GIT_COMMITTER_EMAIL", "a6@example.com")
}

func TestA6InitRepo(t *testing.T) {
	a6Identity(t)
	dir := resolve(t.TempDir())
	if err := InitRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if b := git(t, dir, "branch", "--show-current"); b != "main" {
		t.Fatalf("branch %q", b)
	}
	if s := git(t, dir, "log", "--format=%s"); s != "Initial commit" {
		t.Fatalf("log %q", s)
	}
	// Worktrees can be made straight away.
	wt := filepath.Join(t.TempDir(), "wt")
	if err := AddWorktree(ctx, dir, wt, "feat", "main"); err != nil {
		t.Fatal(err)
	}
	// Re-initialising an existing repo is harmless.
	if err := InitRepo(ctx, dir); err != nil {
		t.Fatalf("reinit: %v", err)
	}
	// A missing directory is an error.
	if err := InitRepo(ctx, filepath.Join(dir, "missing", "deeper")); err == nil {
		t.Fatal("init in a missing dir succeeded")
	}
}

func TestA6InitRepoWithoutIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_AUTHOR_NAME", "")
	t.Setenv("GIT_AUTHOR_EMAIL", "")
	t.Setenv("GIT_COMMITTER_NAME", "")
	t.Setenv("GIT_COMMITTER_EMAIL", "")
	t.Setenv("EMAIL", "")
	dir := resolve(t.TempDir())
	// user.useConfigOnly makes git refuse to guess an identity.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.useConfigOnly")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	if err := InitRepo(ctx, dir); err != nil {
		t.Fatalf("no identity should not be an error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	if resolves(ctx, dir, "HEAD") {
		t.Fatal("commit made without an identity")
	}
}

func TestA6UntrackedFilesGlobs(t *testing.T) {
	root := newRepo(t)
	write(t, root, ".gitignore", ".env\n*.local.json\nsecrets/\n")
	git(t, root, "add", ".gitignore")
	git(t, root, "commit", "-q", "-m", "ignore")

	write(t, root, ".env", "A=1\n")
	write(t, root, "sub/.env", "B=1\n")
	write(t, root, "config/app.local.json", "{}\n")
	write(t, root, "config/deep/x.local.json", "{}\n")
	write(t, root, "secrets/key", "k\n")
	write(t, root, "notes.txt", "untracked, not ignored\n")
	write(t, root, "sub/todo.txt", "t\n")
	write(t, root, "with space.txt", "s\n")

	sorted := func(s []string) []string { sort.Strings(s); return s }
	cases := []struct {
		name     string
		patterns []string
		ignored  bool
		want     []string
	}{
		{"empty patterns", nil, true, nil},
		{"literal name", []string{".env"}, true, []string{".env"}},
		{"star does not cross slashes", []string{"*.local.json"}, true, nil},
		{"dir glob", []string{"config/*.local.json"}, true, []string{"config/app.local.json"}},
		{"double star", []string{"**/.env"}, true, []string{".env", "sub/.env"}},
		{"double star deep", []string{"config/**/*.local.json"}, true, []string{"config/app.local.json", "config/deep/x.local.json"}},
		{"directory", []string{"secrets"}, true, []string{"secrets/key"}},
		{"several with blanks", []string{"  ", ".env ", "secrets/*"}, true, []string{".env", "secrets/key"}},
		{"not ignored", []string{"*.txt", "sub/*"}, false, []string{"notes.txt", "sub/todo.txt", "with space.txt"}},
		{"ignored excludes plain untracked", []string{"*.txt"}, true, nil},
		{"untracked excludes ignored", []string{".env"}, false, nil},
		{"tracked files never listed", []string{"README.md", ".gitignore"}, false, nil},
		{"no match", []string{"nothing-*"}, false, nil},
		{"space in name", []string{"with space.txt"}, false, []string{"with space.txt"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := UntrackedFiles(ctx, root, c.patterns, c.ignored)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(sorted(got), sorted(c.want)) {
				t.Fatalf("UntrackedFiles(%q, %v) = %q, want %q", c.patterns, c.ignored, got, c.want)
			}
		})
	}
}

func TestA6UntrackedFilesAllBlankPatterns(t *testing.T) {
	root := newRepo(t)
	write(t, root, "stray.txt", "x\n")
	got, err := UntrackedFiles(ctx, root, []string{"", "   "}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Without the guard in UntrackedFiles, nothing follows "--" and git
	// lists every untracked file instead of none.
	if len(got) != 0 {
		t.Fatalf("only-blank patterns listed %q, want nothing", got)
	}
}

func TestA6UntrackedFilesErrors(t *testing.T) {
	notRepo := resolve(t.TempDir())
	if _, err := UntrackedFiles(ctx, notRepo, []string{".env"}, true); err == nil || !strings.Contains(err.Error(), "git ls-files") {
		t.Fatalf("not a repo: %v", err)
	}
	root := newRepo(t)
	c, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := UntrackedFiles(c, root, []string{".env"}, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

func TestA6LiteralPathspecsElsewhere(t *testing.T) {
	// Outside UntrackedFiles, file names are literal: a file named like a
	// glob diffs only itself.
	root := newRepo(t)
	commit(t, root, "a.txt", "a\n", "a")
	commit(t, root, "[a].txt", "b\n", "glob-named")
	write(t, root, "a.txt", "changed\n")
	write(t, root, "[a].txt", "changed too\n")
	d, err := Diff(ctx, root, "[a].txt", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "changed too") || strings.Contains(d, "+changed\n") {
		t.Fatalf("diff of glob-named file: %s", d)
	}
}

func TestA6AddWorktreeErrors(t *testing.T) {
	root := newRepo(t)

	// Parent path is a regular file.
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o600)
	if err := AddWorktree(ctx, root, filepath.Join(file, "wt"), "b1", "main"); err == nil || !strings.Contains(err.Error(), "create worktree parent") {
		t.Fatalf("parent is a file: %v", err)
	}

	// Branch already checked out in the main worktree.
	if err := AddWorktree(ctx, root, filepath.Join(t.TempDir(), "wt"), "main", ""); err == nil {
		t.Fatal("checked out main twice")
	}

	// Unknown base.
	if err := AddWorktree(ctx, root, filepath.Join(t.TempDir(), "wt"), "b2", "no-such-base"); err == nil || !strings.Contains(err.Error(), "git worktree add") {
		t.Fatalf("bad base: %v", err)
	}
	if localBranchExists(ctx, root, "b2") {
		t.Fatal("failed add left a branch behind")
	}

	// Target path exists and is not empty.
	busy := t.TempDir()
	write(t, busy, "x", "x")
	if err := AddWorktree(ctx, root, busy, "b3", "main"); err == nil {
		t.Fatal("worktree added over a non-empty dir")
	}

	// Empty base starts from HEAD.
	git(t, root, "checkout", "-q", "-b", "side")
	commit(t, root, "side.txt", "s\n", "side")
	wt := filepath.Join(t.TempDir(), "from-head")
	if err := AddWorktree(ctx, root, wt, "b4", ""); err != nil {
		t.Fatal(err)
	}
	if got, want := git(t, wt, "rev-parse", "HEAD"), git(t, root, "rev-parse", "side"); got != want {
		t.Fatalf("b4 at %s, want side %s", got, want)
	}

	// Not a repository.
	if err := AddWorktree(ctx, resolve(t.TempDir()), filepath.Join(t.TempDir(), "wt"), "x", ""); err == nil {
		t.Fatal("add in a non-repo succeeded")
	}
}

func TestA6RemoveWorktreeErrors(t *testing.T) {
	root := newRepo(t)
	if err := RemoveWorktree(ctx, root, filepath.Join(t.TempDir(), "never")); err == nil || !strings.Contains(err.Error(), "git worktree remove") {
		t.Fatalf("remove unknown: %v", err)
	}
	if err := RemoveWorktree(ctx, root, root); err == nil {
		t.Fatal("removed the main worktree")
	}
	// A locked worktree is refused without force.
	wt := filepath.Join(t.TempDir(), "locked")
	if err := AddWorktree(ctx, root, wt, "l", "main"); err != nil {
		t.Fatal(err)
	}
	git(t, root, "worktree", "lock", wt)
	if err := RemoveWorktree(ctx, root, wt); err == nil {
		t.Fatal("removed a locked worktree")
	}
}

func TestA6WorktreesPrunableAndErrors(t *testing.T) {
	root := newRepo(t)
	wt := filepath.Join(filepath.Dir(root), "gone")
	git(t, root, "worktree", "add", "-q", "-b", "gone", wt)
	os.RemoveAll(wt)
	wts, err := Worktrees(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 || !wts[1].Prunable || wts[1].Branch != "gone" || wts[1].Main {
		t.Fatalf("prunable: %+v", wts)
	}
	if _, err := Worktrees(ctx, resolve(t.TempDir())); err == nil {
		t.Fatal("Worktrees in a non-repo succeeded")
	}
	// Listing from inside a linked worktree still puts main first.
	wt2 := filepath.Join(filepath.Dir(root), "linked")
	git(t, root, "worktree", "add", "-q", "-b", "linked", wt2)
	wts, _ = Worktrees(ctx, resolve(wt2))
	if len(wts) < 2 || wts[0].Path != root || !wts[0].Main {
		t.Fatalf("from linked: %+v", wts)
	}
}

func TestA6GitErrorAndMissingBinary(t *testing.T) {
	e := &gitError{args: []string{"status"}, code: 128}
	if e.Error() != "git status: exit status 128" {
		t.Fatalf("empty stderr: %s", e.Error())
	}
	e.stderr = "fatal: boom"
	if e.Error() != "git status: fatal: boom" {
		t.Fatalf("stderr: %s", e.Error())
	}

	root := newRepo(t)
	t.Setenv("PATH", t.TempDir())
	_, err := run(ctx, root, "status")
	var ge *gitError
	if err == nil || errors.As(err, &ge) || !strings.HasPrefix(err.Error(), "git status:") {
		t.Fatalf("missing git: %v", err)
	}
	if _, err := Discover(ctx, root); err == nil || errors.Is(err, ErrNotRepo) {
		t.Fatalf("Discover without git: %v", err)
	}
	if got := DefaultBase(ctx, root); got != "HEAD" {
		t.Fatalf("DefaultBase without git: %q", got)
	}
	if _, err := UntrackedFiles(ctx, root, []string{"x"}, false); err == nil {
		t.Fatal("UntrackedFiles without git succeeded")
	}
	if got := headOrEmptyTree(ctx, root); got != "4b825dc642cb6eb9a060e54bf8d69288fbee4904" {
		t.Fatalf("empty tree fallback: %s", got)
	}
}

func TestA6RunCanceledAndOKCodes(t *testing.T) {
	root := newRepo(t)
	// diff --exit-code exits 1 on changes; listed as ok, it succeeds.
	write(t, root, "README.md", "changed\n")
	out, code, err := runCodes(ctx, root, []int{1}, "diff", "--exit-code", "--stat")
	if err != nil || code != 1 || !strings.Contains(string(out), "README.md") {
		t.Fatalf("ok codes: %q %d %v", out, code, err)
	}
	if _, code, err := runCodes(ctx, root, nil, "diff", "--exit-code"); err == nil || code != 1 {
		t.Fatalf("not ok: %d %v", code, err)
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := run(c, root, "status"); err == nil {
		t.Fatal("canceled run succeeded")
	}
}

func TestA6DiscoverEdges(t *testing.T) {
	root := newRepo(t)
	// Inside the .git dir there is no work tree.
	if _, err := Discover(ctx, filepath.Join(root, ".git")); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("inside .git: %v", err)
	}
	// A missing dir is an error but not ErrNotRepo-specific.
	if _, err := Discover(ctx, filepath.Join(root, "nope")); err == nil {
		t.Fatal("missing dir discovered")
	}
	// A linked worktree resolves to the main root.
	wt := filepath.Join(filepath.Dir(root), "wt")
	git(t, root, "worktree", "add", "-q", "-b", "x", wt)
	r, err := Discover(ctx, filepath.Join(wt))
	if err != nil || r.Root != root || r.CommonDir != filepath.Join(root, ".git") {
		t.Fatalf("linked: %+v %v", r, err)
	}
	// A subdirectory resolves to the root.
	os.MkdirAll(filepath.Join(root, "a", "b"), 0o755)
	if r, err := Discover(ctx, filepath.Join(root, "a", "b")); err != nil || r.Root != root {
		t.Fatalf("subdir: %+v %v", r, err)
	}
	if resolve("/definitely/not/here/../x") != "/definitely/not/x" {
		t.Fatal("resolve should clean missing paths")
	}
}

func TestA6DefaultBaseFallbacks(t *testing.T) {
	dir := resolve(t.TempDir())
	git(t, dir, "init", "-q", "-b", "work")
	configure(t, dir)
	commit(t, dir, "f", "x", "c")
	if got := DefaultBase(ctx, dir); got != "work" {
		t.Fatalf("current branch fallback: %q", got)
	}
	git(t, dir, "branch", "trunk")
	if got := DefaultBase(ctx, dir); got != "trunk" {
		t.Fatalf("trunk: %q", got)
	}
	git(t, dir, "branch", "master")
	if got := DefaultBase(ctx, dir); got != "master" {
		t.Fatalf("master before trunk: %q", got)
	}

	d2 := resolve(t.TempDir())
	git(t, d2, "init", "-q", "-b", "x")
	configure(t, d2)
	commit(t, d2, "f", "x", "c")
	git(t, d2, "checkout", "-q", "--detach")
	git(t, d2, "branch", "-q", "-D", "x")
	if got := DefaultBase(ctx, d2); got != "HEAD" {
		t.Fatalf("detached: %q", got)
	}
	if resolves(ctx, d2, "") {
		t.Fatal("empty rev resolves")
	}
	if resolves(ctx, d2, "--all") {
		t.Fatal("option-like rev resolves")
	}
}

func TestA6StatusRenamesAndCounts(t *testing.T) {
	root := newRepo(t)
	commit(t, root, "old.txt", "1\n2\n3\n", "old")
	git(t, root, "mv", "old.txt", "new.txt")
	write(t, root, "noeol.txt", "a\nb")
	write(t, root, "bin.dat", "x\x00y")
	write(t, root, "empty.txt", "")

	ch, err := WorktreeChanges(ctx, root, "main")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]FileChange{}
	for _, f := range ch.Files {
		byPath[f.Path] = f
	}
	if f := byPath["new.txt"]; f.Code != "R" || f.OrigPath != "old.txt" || !f.Staged || f.Unstaged {
		t.Fatalf("rename: %+v", f)
	}
	if f := byPath["noeol.txt"]; f.Code != "?" || f.Added != 2 {
		t.Fatalf("no trailing newline: %+v", f)
	}
	if f := byPath["bin.dat"]; !f.Binary || f.Added != 0 {
		t.Fatalf("binary untracked: %+v", f)
	}
	if f := byPath["empty.txt"]; f.Added != 0 || f.Binary {
		t.Fatalf("empty: %+v", f)
	}
	for i := 1; i < len(ch.Files); i++ {
		if ch.Files[i-1].Path > ch.Files[i].Path {
			t.Fatalf("not sorted: %+v", ch.Files)
		}
	}
	if len(ch.Commits) != 0 {
		t.Fatalf("no commits beyond main: %+v", ch.Commits)
	}

	// An unresolvable base skips commits.
	ch, err = WorktreeChanges(ctx, root, "nope")
	if err != nil || ch.Commits != nil || ch.Base != "nope" {
		t.Fatalf("bad base: %+v %v", ch, err)
	}
	if _, err := WorktreeChanges(ctx, resolve(t.TempDir()), "main"); err == nil {
		t.Fatal("changes in a non-repo")
	}
	if _, err := WorktreeStatus(ctx, resolve(t.TempDir())); err == nil {
		t.Fatal("status in a non-repo")
	}
}

func TestA6BranchChangesRenameAndMissing(t *testing.T) {
	root := newRepo(t)
	commit(t, root, "doc.md", strings.Repeat("line\n", 20), "doc")
	git(t, root, "checkout", "-q", "-b", "feat")
	git(t, root, "mv", "doc.md", "guide.md")
	git(t, root, "commit", "-q", "-m", "rename")
	git(t, root, "checkout", "-q", "main")

	ch, err := BranchChanges(ctx, root, "feat", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Files) != 1 || ch.Files[0].Code != "R" || ch.Files[0].OrigPath != "doc.md" || ch.Files[0].Path != "guide.md" {
		t.Fatalf("rename: %+v", ch.Files)
	}
	if len(ch.Commits) != 1 || ch.Commits[0].Subject != "rename" || ch.Commits[0].Author != "Tester" || ch.Commits[0].Time.IsZero() {
		t.Fatalf("commits: %+v", ch.Commits)
	}
	for _, c := range [][2]string{{"nope", "main"}, {"feat", "nope"}, {"feat", ""}} {
		ch, err := BranchChanges(ctx, root, c[0], c[1])
		if err != nil || ch.Files != nil || ch.Commits != nil {
			t.Fatalf("%v: %+v %v", c, ch, err)
		}
	}
}

func TestA6CountLines(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, content string
		n             int
		bin           bool
	}{
		{"one", "x", 1, false},
		{"two", "x\ny\n", 2, false},
		{"blank", "\n\n\n", 3, false},
		{"nul-late", strings.Repeat("a", 9000) + "\x00", 1, false},
		{"nul-early", "a\x00b\n", 0, true},
	}
	for _, c := range cases {
		p := filepath.Join(dir, c.name)
		os.WriteFile(p, []byte(c.content), 0o644)
		if n, bin := countLines(p); n != c.n || bin != c.bin {
			t.Errorf("%s: %d %v, want %d %v", c.name, n, bin, c.n, c.bin)
		}
	}
	if n, bin := countLines(filepath.Join(dir, "missing")); n != 0 || bin {
		t.Fatal("missing file")
	}
	if n, _ := countLines(dir); n != 0 {
		t.Fatal("directory")
	}
	big := filepath.Join(dir, "big")
	os.WriteFile(big, []byte(strings.Repeat("l\n", maxDiff)), 0o644)
	if n, _ := countLines(big); n != 0 {
		t.Fatalf("big file counted %d", n)
	}
	link := filepath.Join(dir, "link")
	os.Symlink(filepath.Join(dir, "two"), link)
	if n, _ := countLines(link); n != 2 {
		t.Fatalf("symlink to file: %d", n)
	}
	if runtimeCanChmod() {
		unreadable := filepath.Join(dir, "unreadable")
		os.WriteFile(unreadable, []byte("x\n"), 0o000)
		if n, _ := countLines(unreadable); n != 0 {
			t.Fatalf("unreadable: %d", n)
		}
	}
}

func runtimeCanChmod() bool { return os.Geteuid() != 0 }

func TestA6ParseHelpers(t *testing.T) {
	for in, want := range map[string][3]int{
		"":                    {0, 0, 0},
		"[gone]":              {1, 0, 0},
		"[ahead 3]":           {0, 3, 0},
		"[behind 4]":          {0, 0, 4},
		"[ahead 2, behind 1]": {0, 2, 1},
		"[ahead x]":           {0, 0, 0},
	} {
		gone, a, b := parseTrack(in)
		g := 0
		if gone {
			g = 1
		}
		if [3]int{g, a, b} != want {
			t.Errorf("parseTrack(%q) = %v %d %d", in, gone, a, b)
		}
	}
	if a, b := parsePair(" 5 7\n"); a != 5 || b != 7 {
		t.Fatalf("parsePair: %d %d", a, b)
	}
	if a, b := parsePair("junk"); a != 0 || b != 0 {
		t.Fatalf("parsePair junk: %d %d", a, b)
	}
	if splitNUL(nil) != nil || splitNUL([]byte("\x00")) != nil {
		t.Fatal("splitNUL empty")
	}
	if got := splitNUL([]byte("a\x00\x00b\x00")); !reflect.DeepEqual(got, []string{"a", "", "b"}) {
		t.Fatalf("splitNUL: %q", got)
	}
	if a, b := revListCounts(ctx, resolve(t.TempDir()), "main", "x"); a != 0 || b != 0 {
		t.Fatal("revListCounts in a non-repo")
	}
}

func TestA6BranchesErrorsAndGone(t *testing.T) {
	if _, err := Branches(ctx, resolve(t.TempDir()), ""); err == nil {
		t.Fatal("Branches in a non-repo")
	}
	root := newRepo(t)
	git(t, root, "branch", "feat")
	git(t, root, "config", "branch.feat.remote", "origin")
	git(t, root, "config", "branch.feat.merge", "refs/heads/feat")
	git(t, root, "remote", "add", "origin", "/nonexistent")
	bs, err := Branches(ctx, root, "no-such-base")
	if err != nil {
		t.Fatal(err)
	}
	var feat *Branch
	for i := range bs {
		if bs[i].Name == "feat" {
			feat = &bs[i]
		}
	}
	if feat == nil || !feat.Gone || feat.Upstream != "origin/feat" || feat.BaseAhead != 0 {
		t.Fatalf("gone upstream: %+v", bs)
	}
}

func TestAttemptBranch(t *testing.T) {
	base := BranchFromPrompt("conch", "Fix the flaky login test")
	if base != "conch/fix-flaky-login-test" {
		t.Fatalf("base %q", base)
	}
	used := map[string]bool{}
	taken := func(s string) bool { return used[s] }
	for _, c := range []struct {
		agent string
		n     int
		want  string
	}{
		{"claude", 1, "conch/fix-flaky-login-test/claude"},
		{"codex", 1, "conch/fix-flaky-login-test/codex"},
		{"claude", 2, "conch/fix-flaky-login-test/claude-2"},
		{"Gemini CLI", 1, "conch/fix-flaky-login-test/gemini-cli"},
	} {
		got := AttemptBranch(base, c.agent, c.n, taken)
		if got != c.want {
			t.Fatalf("attempt %s#%d = %q, want %q", c.agent, c.n, got, c.want)
		}
		used[got] = true
	}
	// A name already in use gets the next free number, not a clash.
	if got := AttemptBranch(base, "claude", 1, taken); got != "conch/fix-flaky-login-test/claude-3" {
		t.Fatalf("already taken: %q", got)
	}
	// Odd inputs still produce a usable branch.
	for _, c := range []struct{ base, agent, want string }{
		{"", "claude", "conch/task/claude"},
		{"feat/", "claude", "feat/claude"},
		{"feat", "", "feat/attempt"},
		{"feat", "!!!", "feat/attempt"},
	} {
		if got := AttemptBranch(c.base, c.agent, 1, nil); got != c.want {
			t.Errorf("AttemptBranch(%q, %q) = %q, want %q", c.base, c.agent, got, c.want)
		}
	}
	for _, c := range []struct{ branch, want string }{
		{"conch/fix-flaky-login-test/claude", "conch/fix-flaky-login-test"},
		{"conch/fix-flaky-login-test", "conch"},
		{"main", ""},
		{"/odd", ""},
		{"", ""},
	} {
		if got := AttemptBase(c.branch); got != c.want {
			t.Errorf("AttemptBase(%q) = %q, want %q", c.branch, got, c.want)
		}
	}
}

// TestDiffChangesWhileCountsDoNot is the premise of the TUI's live diff: a
// line rewritten in place leaves a file's +/− counts exactly as they were,
// so only re-reading the diff itself notices what an agent wrote.
func TestDiffChangesWhileCountsDoNot(t *testing.T) {
	root := newRepo(t)
	commit(t, root, "a.go", "one\ntwo\nthree\n", "add a.go")

	count := func() (int, int) {
		t.Helper()
		ch, err := WorktreeChanges(ctx, root, "main")
		if err != nil {
			t.Fatal(err)
		}
		if len(ch.Files) != 1 || ch.Files[0].Path != "a.go" {
			t.Fatalf("files %+v", ch.Files)
		}
		return ch.Files[0].Added, ch.Files[0].Deleted
	}
	diff := func() string {
		t.Helper()
		d, err := Diff(ctx, root, "a.go", "")
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	write(t, root, "a.go", "one\nTWO\nthree\n")
	added, deleted := count()
	first := diff()
	if !strings.Contains(first, "+TWO") {
		t.Fatalf("first diff:\n%s", first)
	}

	// The agent rewrites that same line differently.
	write(t, root, "a.go", "one\ntwotwo\nthree\n")
	if a, d := count(); a != added || d != deleted {
		t.Fatalf("counts moved from %d/%d to %d/%d", added, deleted, a, d)
	}
	second := diff()
	if second == first || !strings.Contains(second, "+twotwo") {
		t.Fatalf("second diff:\n%s", second)
	}
	// Which is exactly what freshLines in the TUI keys off.
	if strings.Contains(second, "+TWO\n") {
		t.Fatalf("the old line survived:\n%s", second)
	}

	// Undoing the edit empties the diff, which the view shows as such.
	write(t, root, "a.go", "one\ntwo\nthree\n")
	if d := diff(); strings.TrimSpace(d) != "" {
		t.Fatalf("diff after undo:\n%s", d)
	}
}

func TestIgnoredDirs(t *testing.T) {
	root := newRepo(t)
	commit(t, root, ".gitignore", "node_modules/\nbuild/\n*.log\nempty/\n", "ignore rules")
	write(t, root, "node_modules/pkg/index.js", "x\n")
	write(t, root, "build/out/a.o", "x\n")
	write(t, root, "debug.log", "x\n")
	write(t, root, "src/main.go", "package main\n")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	dirs, err := IgnoredDirs(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(dirs)
	if want := []string{"build", "node_modules"}; !reflect.DeepEqual(dirs, want) {
		t.Fatalf("IgnoredDirs = %v, want %v", dirs, want)
	}

	// A repo with nothing ignored, and one with no .gitignore at all.
	clean := newRepo(t)
	if dirs, err := IgnoredDirs(ctx, clean); err != nil || len(dirs) != 0 {
		t.Fatalf("clean repo: %v %v", dirs, err)
	}
	// Outside a repo it fails rather than guessing.
	if _, err := IgnoredDirs(ctx, t.TempDir()); err == nil {
		t.Fatal("IgnoredDirs outside a repo")
	}
	// A cancelled context stops it.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := IgnoredDirs(cancelled, root); err == nil {
		t.Fatal("IgnoredDirs with a cancelled context")
	}
}

func TestIgnoredPaths(t *testing.T) {
	root := newRepo(t)
	commit(t, root, ".gitignore", "node_modules/\n*.log\nempty/\n", "ignore rules")
	write(t, root, "node_modules/pkg/index.js", "x\n")
	write(t, root, "debug.log", "x\n")
	write(t, root, "src/trace.log", "x\n")
	write(t, root, "src/main.go", "package main\n")

	paths, err := IgnoredPaths(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	// A folder comes once with its slash; files come as they are, however deep.
	if want := []string{"debug.log", "node_modules/", "src/trace.log"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("IgnoredPaths = %v, want %v", paths, want)
	}
	if paths, err := IgnoredPaths(ctx, newRepo(t)); err != nil || len(paths) != 0 {
		t.Fatalf("clean repo: %v %v", paths, err)
	}
	if _, err := IgnoredPaths(ctx, t.TempDir()); err == nil {
		t.Fatal("IgnoredPaths outside a repo")
	}
}
