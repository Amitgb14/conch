package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cloneOf clones root as another machine's copy of the project would be.
func cloneOf(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "other")
	if err := Clone(ctx, root, dir); err != nil {
		t.Fatal(err)
	}
	configure(t, dir)
	return resolve(dir)
}

func TestRemoteURL(t *testing.T) {
	root := newRepo(t)
	if got := RemoteURL(ctx, root); got != "" {
		t.Fatalf("no remote: %q", got)
	}
	// A single remote not called origin still counts.
	git(t, root, "remote", "add", "upstream", "git@example.com:a/b.git")
	if got := RemoteURL(ctx, root); got != "git@example.com:a/b.git" {
		t.Fatalf("only remote: %q", got)
	}
	// With several, origin wins; without origin none is guessed.
	git(t, root, "remote", "add", "fork", "git@example.com:me/b.git")
	if got := RemoteURL(ctx, root); got != "" {
		t.Fatalf("several, no origin: %q", got)
	}
	git(t, root, "remote", "add", "origin", "https://example.com/a/b.git")
	if got := RemoteURL(ctx, root); got != "https://example.com/a/b.git" {
		t.Fatalf("origin: %q", got)
	}
	if got := RemoteURL(ctx, t.TempDir()); got != "" {
		t.Fatalf("not a repository: %q", got)
	}
}

func TestHistoryAndHaveCommits(t *testing.T) {
	root := newRepo(t)
	other := cloneOf(t, root)
	commit(t, root, "a.txt", "a\n", "a")
	commit(t, root, "b.txt", "b\n", "b")

	hist, err := History(ctx, root, "main", 1000)
	if err != nil || len(hist) != 3 {
		t.Fatalf("history: %v %v", hist, err)
	}
	if h, _ := History(ctx, root, "main", 2); len(h) != 2 || h[0] != hist[0] {
		t.Fatalf("limit: %v", h)
	}
	if _, err := History(ctx, root, "nope", 10); err == nil {
		t.Fatal("unknown rev: no error")
	}

	// The clone has only the first commit; junk and short names are ignored.
	have, err := HaveCommits(ctx, other, append([]string{"zzz", "HEAD", strings.ToUpper(hist[0])}, hist...))
	if err != nil || len(have) != 1 || have[0] != hist[2] {
		t.Fatalf("have: %v %v", have, err)
	}
	if have, err := HaveCommits(ctx, root, hist); err != nil || strings.Join(have, ",") != strings.Join(hist, ",") {
		t.Fatalf("all, in order: %v %v", have, err)
	}
	if have, err := HaveCommits(ctx, other, nil); err != nil || have != nil {
		t.Fatalf("nothing asked: %v %v", have, err)
	}
	// A tree's hash is not a commit.
	tree := git(t, root, "rev-parse", "HEAD^{tree}")
	if have, _ := HaveCommits(ctx, root, []string{tree}); len(have) != 0 {
		t.Fatalf("a tree counted as a commit: %v", have)
	}
}

func TestBundleRoundTrip(t *testing.T) {
	root, wt := taskRepo(t)
	other := cloneOf(t, root)
	commit(t, wt, "f1.txt", "one\n", "feat one")
	commit(t, wt, "f2.txt", "two\n", "feat two")
	head := git(t, wt, "rev-parse", "HEAD")
	base := git(t, root, "rev-parse", "main")
	file := filepath.Join(t.TempDir(), "b.bundle")

	// Only what the clone lacks: the two feat commits.
	if err := CreateBundle(ctx, root, file, "feat", base); err != nil {
		t.Fatal(err)
	}
	got, err := FetchBundle(ctx, other, file, "feat", "refs/conch/incoming/feat")
	if err != nil || got != head {
		t.Fatalf("fetch: %s %v", got, err)
	}
	if n := git(t, other, "rev-list", "--count", base+".."+got); n != "2" {
		t.Fatalf("commits: %s", n)
	}

	// Into a repository missing the prerequisite: verify fails. Its first
	// commit must differ: two repos made in the same second by newRepo share
	// theirs, prerequisite and all.
	stranger := filepath.Join(t.TempDir(), "stranger")
	os.MkdirAll(stranger, 0o755)
	stranger = resolve(stranger)
	git(t, stranger, "init", "-q", "-b", "main")
	configure(t, stranger)
	commit(t, stranger, "OTHER.md", "unrelated\n", "unrelated")
	if _, err := FetchBundle(ctx, stranger, file, "feat", "refs/conch/incoming/feat"); err == nil {
		t.Fatal("bundle without its prerequisite applied")
	}
	// A whole-history bundle works anywhere.
	full := filepath.Join(t.TempDir(), "full.bundle")
	if err := CreateBundle(ctx, root, full, "feat", ""); err != nil {
		t.Fatal(err)
	}
	if got, err := FetchBundle(ctx, stranger, full, "feat", "refs/conch/incoming/feat"); err != nil || got != head {
		t.Fatalf("full bundle: %s %v", got, err)
	}
	// Nothing new: said so instead of git's refusal.
	if err := CreateBundle(ctx, root, filepath.Join(t.TempDir(), "e"), "feat", head); !errors.Is(err, ErrEmptyBundle) {
		t.Fatalf("empty: %v", err)
	}
	if err := CreateBundle(ctx, root, filepath.Join(t.TempDir(), "e"), "nope", base); err == nil {
		t.Fatal("unknown branch: no error")
	}
	if _, err := FetchBundle(ctx, other, filepath.Join(t.TempDir(), "missing"), "feat", "refs/x"); err == nil {
		t.Fatal("missing bundle: no error")
	}

	// Branch bookkeeping.
	if !IsAncestor(ctx, other, base, head) || IsAncestor(ctx, other, head, base) {
		t.Fatal("ancestry")
	}
	if err := SetBranch(ctx, other, "feat", head); err != nil || git(t, other, "rev-parse", "feat") != head {
		t.Fatalf("set branch: %v", err)
	}
	DeleteRef(ctx, other, "refs/conch/incoming/feat")
	if _, err := revParse(ctx, other, "--verify", "refs/conch/incoming/feat"); err == nil {
		t.Fatal("ref not deleted")
	}
}

func TestWorkPatchesRoundTrip(t *testing.T) {
	root, wt := taskRepo(t)
	write(t, wt, "README.md", "hello\nstaged\n")
	git(t, wt, "add", "README.md")
	write(t, wt, "README.md", "hello\nstaged\nunstaged\n")
	write(t, wt, "bin.dat", "\x00\x01\x02")
	git(t, wt, "add", "bin.dat")

	staged, unstaged, err := WorkPatches(ctx, wt)
	if err != nil || len(staged) == 0 || len(unstaged) == 0 || !strings.Contains(string(staged), "GIT binary patch") {
		t.Fatalf("patches: %v\n%s\n%s", err, staged, unstaged)
	}

	// Applied to a fresh checkout of the same commit, the staged part is
	// staged and the rest isn't.
	other := cloneOf(t, root)
	if err := ApplyPatch(ctx, other, staged, true); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPatch(ctx, other, unstaged, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(other, "README.md")); string(b) != "hello\nstaged\nunstaged\n" {
		t.Fatalf("content: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(other, "bin.dat")); string(b) != "\x00\x01\x02" {
		t.Fatalf("binary: %q", b)
	}
	if got := git(t, other, "diff", "--cached", "--name-only"); got != "README.md\nbin.dat" {
		t.Fatalf("staged: %q", got)
	}
	if got := git(t, other, "diff", "--name-only"); got != "README.md" {
		t.Fatalf("unstaged: %q", got)
	}

	// Nothing to apply; a patch that doesn't fit.
	if err := ApplyPatch(ctx, other, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPatch(ctx, other, staged, true); err == nil {
		t.Fatal("applied the same patch twice")
	}
	if s, u, err := WorkPatches(ctx, root); err != nil || len(s) != 0 || len(u) != 0 {
		t.Fatalf("clean checkout: %q %q %v", s, u, err)
	}
}

func TestOtherFiles(t *testing.T) {
	_, wt := taskRepo(t)
	write(t, wt, ".gitignore", "*.log\n")
	write(t, wt, "notes/todo.md", "x")
	write(t, wt, "debug.log", "x")
	files, err := OtherFiles(ctx, wt)
	if err != nil || strings.Join(files, ",") != ".gitignore,notes/todo.md" {
		t.Fatalf("files: %v %v", files, err)
	}
	if _, err := OtherFiles(ctx, t.TempDir()); err == nil {
		t.Fatal("not a repository: no error")
	}
}

func TestClone(t *testing.T) {
	root := newRepo(t)
	dir := filepath.Join(t.TempDir(), "deep", "er", "copy")
	if err := Clone(ctx, root, dir); err != nil {
		t.Fatal(err)
	}
	if got := RemoteURL(ctx, dir); got != root {
		t.Fatalf("origin: %q", got)
	}
	// Into a folder that isn't empty, or from nowhere: git's reason.
	write(t, dir, "x", "x")
	if err := Clone(ctx, root, dir); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("not empty: %v", err)
	}
	if err := Clone(ctx, filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "c")); err == nil ||
		!strings.Contains(err.Error(), "git clone") {
		t.Fatalf("no source: %v", err)
	}
}
