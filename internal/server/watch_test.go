package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// a7Watch is a watcher on its own, with what it announces collected. It has
// no server, no socket and no clients: only real files under t.TempDir.
type a7Watch struct {
	*worktreeWatcher
	// Not named mu: that would shadow the watcher's own mutex, which the
	// tests take to read its roots and directories.
	seenMu   sync.Mutex
	events   []proto.WorktreeChanged
	firstAt  time.Time // when the first event was announced
	requests []string
	quit     chan struct{}
}

func a7New(t *testing.T) *a7Watch {
	t.Helper()
	w := &a7Watch{quit: make(chan struct{})}
	w.worktreeWatcher = newWorktreeWatcher(
		func(c proto.WorktreeChanged) {
			w.seenMu.Lock()
			defer w.seenMu.Unlock()
			if w.firstAt.IsZero() {
				w.firstAt = time.Now()
			}
			w.events = append(w.events, c)
		},
		func(id string) {
			w.seenMu.Lock()
			defer w.seenMu.Unlock()
			w.requests = append(w.requests, id)
		})
	if w.worktreeWatcher == nil {
		t.Skip("this machine gives no file watches")
	}
	go w.run(w.quit)
	t.Cleanup(func() { close(w.quit) })
	return w
}

func (w *a7Watch) seen() []proto.WorktreeChanged {
	w.seenMu.Lock()
	defer w.seenMu.Unlock()
	return slices.Clone(w.events)
}

func (w *a7Watch) forget() {
	w.seenMu.Lock()
	defer w.seenMu.Unlock()
	w.events, w.requests, w.firstAt = nil, nil, time.Time{}
}

// waitFor polls until want reports true, so no test turns on a fixed sleep.
func (w *a7Watch) waitFor(t *testing.T, what string, want func([]proto.WorktreeChanged) bool) []proto.WorktreeChanged {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := w.seen(); want(got) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; saw %+v", what, w.seen())
	return nil
}

// quiet fails if anything is announced in the next stretch. It is only used
// where an event would be wrong, never to prove one arrives.
func (w *a7Watch) quiet(t *testing.T, what string) {
	t.Helper()
	deadline := time.Now().Add(600 * time.Millisecond) // several debounces
	for time.Now().Before(deadline) {
		if got := w.seen(); len(got) > 0 {
			t.Fatalf("%s announced %+v", what, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// a7Git runs git in dir, ignoring the developer's own git configuration.
func a7Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func a7Write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// a7Repo is a checkout that ignores node_modules and *.log.
func a7Repo(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "conchwatch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	repo := filepath.Join(dir, "repo")
	a5GitRepo(t, repo)
	a7Write(t, repo, ".gitignore", "node_modules/\n*.log\n")
	a7Write(t, repo, "src/main.go", "package main\n")
	a7Write(t, repo, "node_modules/pkg/index.js", "x\n")
	a7Git(t, repo, "add", "-A")
	a7Git(t, repo, "commit", "-q", "-m", "files")
	// EvalSymlinks: on macOS the temp dir is under /private, and the paths
	// the watcher reports must match what it was given.
	real, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestWatchAnnouncesAnEdit(t *testing.T) {
	w := a7New(t)
	repo := a7Repo(t)
	w.syncProject("r1", []string{repo})

	a7Write(t, repo, "src/main.go", "package main // edited\n")
	got := w.waitFor(t, "the edit", func(cs []proto.WorktreeChanged) bool { return len(cs) > 0 })
	c := got[0]
	if c.ProjectID != "r1" || c.Worktree != repo {
		t.Fatalf("event %+v", c)
	}
	if !slices.Contains(c.Paths, "src/main.go") || c.More {
		t.Fatalf("paths %v", c.Paths)
	}
	// The project's git state is re-read as well, once.
	w.seenMu.Lock()
	reqs := slices.Clone(w.requests)
	w.seenMu.Unlock()
	if len(reqs) == 0 || reqs[0] != "r1" {
		t.Fatalf("requests %v", reqs)
	}
}

func TestWatchSkipsIgnoredAndGit(t *testing.T) {
	w := a7New(t)
	repo := a7Repo(t)
	w.syncProject("r1", []string{repo})

	// git's own metadata: the project ticker reads that itself.
	a7Write(t, repo, ".git/conch-probe", "x\n")
	// A directory git ignores whole, and an ignored file beside real ones.
	a7Write(t, repo, "node_modules/pkg/index.js", "changed\n")
	a7Write(t, repo, "node_modules/other/new.js", "new\n")
	w.quiet(t, "ignored files")

	// node_modules costs no watches at all, however deep it goes. Only a
	// backend that is told about each directory keeps such a list.
	if fb, ok := w.be.(interface{ watchedDirs() []string }); ok {
		for _, d := range fb.watchedDirs() {
			if filepath.Base(d) == "node_modules" || filepath.Base(d) == ".git" {
				t.Errorf("watching %s", d)
			}
		}
	}

	// A real edit still lands, so the skipping did not break the watch.
	a7Write(t, repo, "src/main.go", "package main // yes\n")
	w.waitFor(t, "a real edit", func(cs []proto.WorktreeChanged) bool { return len(cs) > 0 })
}

func TestWatchFollowsNewDirectories(t *testing.T) {
	w := a7New(t)
	repo := a7Repo(t)
	w.syncProject("r1", []string{repo})

	// A directory created with a file already in it: the watch goes on
	// after the fact, so the tree is walked rather than only added.
	a7Write(t, repo, "src/deep/nested/new.go", "package nested\n")
	w.waitFor(t, "the new tree", func(cs []proto.WorktreeChanged) bool { return len(cs) > 0 })
	w.forget()

	// Editing inside it is now heard.
	a7Write(t, repo, "src/deep/nested/new.go", "package nested // again\n")
	got := w.waitFor(t, "an edit in the new tree", func(cs []proto.WorktreeChanged) bool {
		for _, c := range cs {
			if slices.Contains(c.Paths, "src/deep/nested/new.go") {
				return true
			}
		}
		return false
	})
	if got[0].Worktree != repo {
		t.Fatalf("worktree %q", got[0].Worktree)
	}
}

// TestWatchCapsThePaths drives the accumulation directly: whether a burst
// lands inside one debounce window depends on how fast the machine writes,
// and a test that turns on that is flaky. What matters is the rule — past
// watchMaxPaths an event says More instead of growing without limit.
func TestWatchCapsThePaths(t *testing.T) {
	w := a7New(t)
	root := t.TempDir()
	w.mu.Lock()
	w.roots[root] = &watchRoot{projectID: "r1", watched: true}
	w.mu.Unlock()

	for i := 0; i < watchMaxPaths+40; i++ {
		w.note(filepath.Join(root, fmt.Sprintf("f%03d.go", i)))
	}
	w.mu.Lock()
	e := w.dirty[root]
	w.mu.Unlock()
	if e == nil {
		t.Fatal("nothing accumulated")
	}
	if len(e.paths) != watchMaxPaths {
		t.Fatalf("kept %d paths, want %d", len(e.paths), watchMaxPaths)
	}
	if !e.more {
		t.Fatal("the extra paths were dropped without saying More")
	}
	// A path already counted does not push the total further.
	w.note(filepath.Join(root, "f000.go"))
	w.mu.Lock()
	n := len(w.dirty[root].paths)
	w.mu.Unlock()
	if n != watchMaxPaths {
		t.Fatalf("a repeat grew the list to %d", n)
	}

	// What is announced is sorted, so the same change is the same event.
	got := w.waitFor(t, "the capped event", func(cs []proto.WorktreeChanged) bool { return len(cs) > 0 })
	c := got[0]
	if !c.More || len(c.Paths) != watchMaxPaths {
		t.Fatalf("event: More=%v, %d paths", c.More, len(c.Paths))
	}
	if !slices.IsSorted(c.Paths) {
		t.Fatalf("paths are not in a stable order: %v", c.Paths)
	}
}

// TestWatchCoalesces is the timing-tolerant half: a burst of writes must not
// become an event each.
func TestWatchCoalesces(t *testing.T) {
	w := a7New(t)
	repo := a7Repo(t)
	w.syncProject("r1", []string{repo})

	files := 60
	for i := 0; i < files; i++ {
		a7Write(t, repo, filepath.Join("src", fmt.Sprintf("f%03d.go", i)), "x\n")
	}
	got := w.waitFor(t, "the burst", func(cs []proto.WorktreeChanged) bool { return len(cs) > 0 })
	time.Sleep(2 * watchMinGap) // let any straggler flush arrive
	got = w.seen()
	if len(got) >= files/4 {
		t.Fatalf("%d events for %d files: they are not being coalesced", len(got), files)
	}
	seen := map[string]bool{}
	for _, c := range got {
		for _, p := range c.Paths {
			seen[p] = true
		}
	}
	if len(seen) == 0 {
		t.Fatal("the burst named no paths")
	}
}

func TestWatchSyncAndDrop(t *testing.T) {
	w := a7New(t)
	repo := a7Repo(t)
	other := a7Repo(t)
	w.syncProject("r1", []string{repo, other})
	w.mu.Lock()
	roots := len(w.roots)
	w.mu.Unlock()
	if roots != 2 {
		t.Fatalf("%d roots", roots)
	}

	// A worktree that is gone from the project stops being watched.
	w.syncProject("r1", []string{repo})
	w.mu.Lock()
	_, still := w.roots[other]
	w.mu.Unlock()
	if still {
		t.Fatal("a removed worktree is still watched")
	}
	a7Write(t, other, "src/main.go", "package main // unwatched\n")
	w.quiet(t, "a dropped worktree")

	// The project going away drops the rest, and its watched directories.
	w.dropProject("r1")
	w.mu.Lock()
	roots, n, fds := len(w.roots), w.nDirs, w.nFDs
	w.mu.Unlock()
	if roots != 0 || n != 0 || fds != 0 {
		t.Fatalf("after dropProject: %d roots, %d dirs, %d descriptors", roots, n, fds)
	}
	a7Write(t, repo, "src/main.go", "package main // also unwatched\n")
	w.quiet(t, "a dropped project")
}

func TestWatchDeletedAndNonRepoRoots(t *testing.T) {
	w := a7New(t)
	repo := a7Repo(t)

	// A plain folder is not watched: nothing says what it would ignore.
	plain, err := os.MkdirTemp("", "conchplain")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(plain)
	w.syncProject("r2", []string{plain})
	w.mu.Lock()
	_, watched := w.roots[plain]
	w.mu.Unlock()
	if watched {
		t.Fatal("watched a folder that is not a checkout")
	}

	// Nor is one that does not exist, or a file.
	w.syncProject("r3", []string{filepath.Join(repo, "gone"), filepath.Join(repo, "src", "main.go"), ""})
	w.mu.Lock()
	roots := len(w.roots)
	w.mu.Unlock()
	if roots != 0 {
		t.Fatalf("%d roots for paths that are not worktrees", roots)
	}

	// A worktree removed under us drops itself on the next sync.
	w.syncProject("r1", []string{repo})
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	w.syncProject("r1", []string{repo})
	w.mu.Lock()
	_, still := w.roots[repo]
	w.mu.Unlock()
	if still {
		t.Fatal("a deleted worktree is still watched")
	}
}

func TestWatchNilDoesNothing(t *testing.T) {
	var w *worktreeWatcher
	// Every entry point has to be safe on a machine that gives no watches.
	w.run(make(chan struct{}))
	w.syncProject("r1", []string{"/nope"})
	w.dropProject("r1")
}

func TestSkipPath(t *testing.T) {
	ignored := []string{"node_modules", "web/build"}
	for _, tc := range []struct {
		rel  string
		skip bool
	}{
		{"", false},
		{".", false},
		{"src/main.go", false},
		{".git", true},
		{".git/index", true},
		{"sub/.git/x", true},
		{"node_modules", true},
		{"node_modules/pkg/a.js", true},
		{"node_modules_real/a.js", false}, // a prefix is not a parent
		{"web/build", true},
		{"web/build/out.js", true},
		{"web/builder.js", false},
		{"web/src/a.ts", false},
		{".gitignore", false}, // a file, not the folder
	} {
		if got := skipPath(tc.rel, ignored); got != tc.skip {
			t.Errorf("skipPath(%q) = %v, want %v", tc.rel, got, tc.skip)
		}
	}
}

func TestCapabilityFollowsTheWatcher(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "s.sock"), t.TempDir())
	if s.watcher == nil {
		t.Skip("this machine gives no file watches")
	}
	if !slices.Contains(s.capabilities(), proto.CapWorktreeWatch) {
		t.Fatal("a watching server does not say so")
	}
	// A machine that gave no watches must not claim it: clients would back
	// off their polling and hear nothing.
	s.watcher = nil
	caps := s.capabilities()
	if slices.Contains(caps, proto.CapWorktreeWatch) {
		t.Fatal("a server with no watcher claims to watch")
	}
	if len(caps) != len(proto.Capabilities)-1 {
		t.Fatalf("%d capabilities, want %d", len(caps), len(proto.Capabilities)-1)
	}
	// The shared list is not the one that was edited.
	if !slices.Contains(proto.Capabilities, proto.CapWorktreeWatch) {
		t.Fatal("the package's capability list was modified")
	}
}

func TestWatchHoldsToAMinimumGap(t *testing.T) {
	w := a7New(t)
	repo := a7Repo(t)
	w.syncProject("r1", []string{repo})

	// A file git ignores inside a watched folder still fires — only whole
	// ignored directories are skipped — so a build writing them must not
	// announce on every debounce.
	start := time.Now()
	deadline := start.Add(3 * time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		a7Write(t, repo, "src/build.log", "line\n")
		time.Sleep(20 * time.Millisecond)
	}
	elapsed := time.Since(start)
	got := w.seen()
	if len(got) == 0 {
		t.Fatal("a storm of writes announced nothing at all")
	}
	// Without the floor this would be one event per debounce.
	if most := int(elapsed/watchMinGap) + 2; len(got) > most {
		t.Fatalf("%d events in %s; the floor of %s allows about %d", len(got), elapsed, watchMinGap, most)
	}
	// The first one still went out promptly rather than waiting out the gap.
	w.seenMu.Lock()
	first := w.firstAt
	w.seenMu.Unlock()
	if first.IsZero() || first.Sub(start) > watchMinGap {
		t.Fatalf("the first event waited %s", first.Sub(start))
	}
	// Edits held back during a quiet stretch are not lost.
	w.forget()
	a7Write(t, repo, "src/main.go", "package main // held\n")
	w.waitFor(t, "an edit made during the quiet stretch", func(cs []proto.WorktreeChanged) bool {
		for _, c := range cs {
			if slices.Contains(c.Paths, "src/main.go") {
				return true
			}
		}
		return false
	})
}

// TestWatchIsAllOrNothingWithinBudget: a worktree too big for the budget is
// left to polling entirely. Watching half of one would be worse than none —
// the reader backs its polling off when a worktree is watched, and would
// then never hear about the half that is not.
func TestWatchIsAllOrNothingWithinBudget(t *testing.T) {
	w := a7New(t)
	repo := a7Repo(t)
	a7Write(t, repo, "a/b/c/deep.go", "package c\n")
	if dirs, _ := w.be.cost(repo, nil); dirs == 0 {
		t.Skip("this backend watches a whole tree at once, so it has no budget")
	}

	// Two directories' worth of budget, for a tree that wants more.
	w.mu.Lock()
	w.maxDirs = 2
	w.mu.Unlock()
	w.syncProject("r1", []string{repo})

	if w.watching(repo) {
		t.Fatal("a worktree past the budget reports itself watched")
	}
	w.mu.Lock()
	roots, n, capped := len(w.roots), w.nDirs, w.capped
	w.mu.Unlock()
	if roots != 0 || n != 0 {
		t.Fatalf("it held on to %d roots, dir count %d", roots, n)
	}
	if !capped {
		t.Fatal("the budget ran out without saying so")
	}
	a7Write(t, repo, "src/main.go", "package main // unwatched\n")
	w.quiet(t, "a worktree past the budget")

	// With the budget back it is watched whole, and says so.
	w.mu.Lock()
	w.maxDirs, w.maxFDs, w.capped = watchMaxDirs, watchMaxFDs, false
	w.mu.Unlock()
	w.syncProject("r1", []string{repo})
	if !w.watching(repo) {
		t.Fatal("a worktree inside the budget is not watched")
	}
	w.mu.Lock()
	n = w.nDirs
	w.mu.Unlock()
	if n < 4 { // repo, src, a, a/b, a/b/c
		t.Fatalf("only %d directories watched", n)
	}
	a7Write(t, repo, "a/b/c/deep.go", "package c // edited\n")
	w.waitFor(t, "an edit once it fits", func(cs []proto.WorktreeChanged) bool {
		for _, c := range cs {
			if slices.Contains(c.Paths, "a/b/c/deep.go") {
				return true
			}
		}
		return false
	})

	// Dropping it gives the budget back rather than leaking it.
	w.dropProject("r1")
	w.mu.Lock()
	n, fds := w.nDirs, w.nFDs
	w.mu.Unlock()
	if n != 0 || fds != 0 {
		t.Fatalf("after dropping: %d dirs, %d descriptors still charged", n, fds)
	}
	if w.watching(repo) {
		t.Fatal("a dropped worktree still reports itself watched")
	}
}

// TestWatchDescriptorBudget is the macOS half: there a watch costs a
// descriptor per path, and holding thousands once left a new pane's pty
// numbered past select's fd_set and crashed the server.
func TestWatchDescriptorBudget(t *testing.T) {
	w0 := a7New(t)
	if !w0.be.budgeted() {
		t.Skip("this backend charges no descriptor per path")
	}
	w := a7New(t)
	repo := a7Repo(t)
	for i := 0; i < 40; i++ { // plenty of files, few directories
		a7Write(t, repo, filepath.Join("src", fmt.Sprintf("f%02d.go", i)), "x\n")
	}
	dirs, fds := w.be.cost(repo, nil)
	if fds <= dirs {
		t.Fatalf("files cost nothing: %d dirs, %d descriptors", dirs, fds)
	}

	// A budget that the directories fit but the files do not.
	w.mu.Lock()
	w.maxFDs = dirs + 1
	w.mu.Unlock()
	w.syncProject("r1", []string{repo})
	if w.watching(repo) {
		t.Fatalf("watched a tree needing %d descriptors on a budget of %d", fds, dirs+1)
	}

	w.mu.Lock()
	w.maxFDs, w.capped = watchMaxFDs, false
	w.mu.Unlock()
	w.syncProject("r1", []string{repo})
	if !w.watching(repo) {
		t.Fatal("not watched with the budget restored")
	}
	w.mu.Lock()
	held := w.nFDs
	w.mu.Unlock()
	if held < dirs {
		t.Fatalf("charged %d descriptors for %d directories", held, dirs)
	}
	if held > watchMaxFDs {
		t.Fatalf("held %d descriptors, past the budget of %d", held, watchMaxFDs)
	}
}

// TestWatchBudgetStaysUnderSelectsLimit: the whole point of the budget is
// that a pane's pty keeps a descriptor select can wait on.
func TestWatchBudgetStaysUnderSelectsLimit(t *testing.T) {
	if watchMaxFDs >= 1024 {
		t.Fatalf("watchMaxFDs is %d: a watcher could push a pane's pty past select's fd_set", watchMaxFDs)
	}
}

// TestWatchWholeTreeBackendHasNoBudget is the other half: where a backend
// takes a whole tree for one stream — FSEvents on macOS — a repository of
// any size is watched, rather than falling back to polling because it would
// not fit a descriptor budget.
func TestWatchWholeTreeBackendHasNoBudget(t *testing.T) {
	w := a7New(t)
	repo := a7Repo(t)
	// Only a backend that takes a whole tree for nothing is free of the
	// budget. Linux's inotify charges no descriptors — budgeted() is false
	// — but still a directory each, so the budget applies to it too.
	if dirs, fds := w.be.cost(repo, nil); dirs != 0 || fds != 0 {
		t.Skip("this backend charges per directory or descriptor, so the budget applies")
	}
	// Deep and wide enough that a per-path backend would have refused it.
	for i := 0; i < 30; i++ {
		a7Write(t, repo, filepath.Join("src", fmt.Sprintf("d%02d", i), "f.go"), "package f\n")
	}
	w.mu.Lock()
	w.maxDirs, w.maxFDs = 2, 2 // budgets a per-path backend could never meet
	w.mu.Unlock()
	w.syncProject("r1", []string{repo})
	if !w.watching(repo) {
		t.Fatal("a whole-tree backend refused a tree for want of budget")
	}
	if dirs, fds := w.be.cost(repo, nil); dirs != 0 || fds != 0 {
		t.Fatalf("it charged %d dirs and %d descriptors", dirs, fds)
	}
	a7Write(t, repo, "src/d15/f.go", "package f // edited\n")
	w.waitFor(t, "an edit deep in the tree", func(cs []proto.WorktreeChanged) bool {
		for _, c := range cs {
			if slices.Contains(c.Paths, "src/d15/f.go") {
				return true
			}
		}
		return false
	})
}

// TestWatchBudgetAccumulates: the totals have to grow as worktrees are
// watched and shrink as they go, or the budget is not a budget at all. It
// once read what it had just written and so always added nothing, which
// would have let a kqueue watcher hold descriptors without limit again.
func TestWatchBudgetAccumulates(t *testing.T) {
	w := a7New(t)
	if !w.be.budgeted() {
		t.Skip("this backend charges nothing to ration")
	}
	one, two := a7Repo(t), a7Repo(t)

	w.syncProject("r1", []string{one})
	w.mu.Lock()
	afterOne, dirsOne := w.nFDs, w.nDirs
	w.mu.Unlock()
	if afterOne <= 0 || dirsOne <= 0 {
		t.Fatalf("watching one worktree charged %d dirs, %d descriptors", dirsOne, afterOne)
	}

	// A second worktree adds to the total rather than replacing it.
	w.syncProject("r2", []string{two})
	w.mu.Lock()
	afterTwo, dirsTwo := w.nFDs, w.nDirs
	w.mu.Unlock()
	if afterTwo <= afterOne || dirsTwo <= dirsOne {
		t.Fatalf("a second worktree took the totals from %d/%d to %d/%d",
			dirsOne, afterOne, dirsTwo, afterTwo)
	}

	// Walking the first again keeps the total where it was, rather than
	// charging it twice.
	w.mu.Lock()
	w.roots[one].walked = time.Now().Add(-2 * watchResyncEvery)
	w.mu.Unlock()
	w.syncProject("r1", []string{one})
	w.mu.Lock()
	afterRewalk := w.nFDs
	w.mu.Unlock()
	if afterRewalk != afterTwo {
		t.Fatalf("re-walking changed the total from %d to %d", afterTwo, afterRewalk)
	}

	// Dropping one gives its share back, and dropping both clears it.
	w.dropProject("r2")
	w.mu.Lock()
	afterDrop := w.nFDs
	w.mu.Unlock()
	if afterDrop != afterOne {
		t.Fatalf("dropping the second left %d descriptors, want %d", afterDrop, afterOne)
	}
	w.dropProject("r1")
	w.mu.Lock()
	end, endDirs := w.nFDs, w.nDirs
	w.mu.Unlock()
	if end != 0 || endDirs != 0 {
		t.Fatalf("after dropping both: %d dirs, %d descriptors", endDirs, end)
	}
}
