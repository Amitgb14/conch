package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// Watching worktrees for edits.
//
// The project ticker re-reads git when its metadata changes, which catches
// commits, branch moves and staging. What it cannot see is an agent writing
// a file: that leaves git's metadata alone, and rewriting a line in place
// leaves the file's own +/− counts alone too. So the worktrees themselves are
// watched, and a change is announced as proto.EventWorktreeChanged within a
// debounce — a diff on screen re-reads itself in about a tenth of a second
// instead of waiting for the next poll.

const (
	// watchDebounce coalesces a burst of writes — an agent saving a dozen
	// files, or a formatter rewriting them — into one event per worktree.
	watchDebounce = 150 * time.Millisecond
	// watchMaxDirs caps the directories watched across every project, so a
	// repository with a huge unignored tree cannot exhaust the inotify
	// budget of the machine. Past it the worktree falls back to polling.
	watchMaxDirs = 4096
	// watchMaxFDs is the descriptor budget where a watch costs descriptors
	// (see kqueueWatches). It is kept far below select's 1024-entry fd_set,
	// because the pane read loop waits on its pty with select: descriptors
	// are handed out lowest first, so a watcher that holds thousands leaves
	// a new pane's pty numbered past the set. That crashed a server once.
	watchMaxFDs = 256
	// watchMinGap is the least time between two announcements about the same
	// worktree. Only directories are skipped as ignored — a file git ignores
	// inside a watched folder, say a build's log or object file, still fires —
	// so without a floor a build could announce several times a second, and
	// every client showing that branch would re-read git each time. The first
	// change of a burst still goes out on the next debounce.
	watchMinGap = 750 * time.Millisecond
	// watchMaxPaths is how many paths one event carries; past it it says
	// More and the reader re-reads what it is showing regardless.
	watchMaxPaths = 64
	// watchResyncEvery is how often a watched worktree is walked again, to
	// pick up directories created while no watch was on their parent.
	watchResyncEvery = 30 * time.Second
	watchGitTimeout  = 10 * time.Second
)

// watchBackend is how a machine watches a tree. They differ enough to
// matter: inotify and kqueue are told about one directory at a time and
// charge for each, while FSEvents takes a whole tree for one stream.
type watchBackend interface {
	// cost is what watching root would take in directories and in file
	// descriptors; both are zero for a backend that charges neither.
	cost(root string, ignored []string) (dirs, fds int)
	// watch starts watching root and everything under it bar ignored.
	watch(root string, ignored []string) error
	unwatch(root string)
	// paths yields absolute paths that changed.
	paths() <-chan string
	errs() <-chan error
	close()
	// budgeted reports whether a watch costs descriptors, so the caller
	// keeps to watchMaxFDs and leaves the low ones for panes' ptys.
	budgeted() bool
}

// skipRel reports whether the directory at path, under root, is one to
// leave alone.
func skipRel(root, path string, ignored []string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	return rel != "." && skipPath(filepath.ToSlash(rel), ignored)
}

// worktreeWatcher watches the worktrees of every project for edits. A nil
// watcher is one the machine would not give us, and does nothing: clients
// keep polling, which is slower but correct.
type worktreeWatcher struct {
	be watchBackend
	// announce is what a debounced change is reported to, and request asks
	// for the project's git state to be re-read. Both are fields so tests
	// can watch a temporary folder without a whole server.
	announce func(proto.WorktreeChanged)
	request  func(projectID string)
	maxDirs  int // watchMaxDirs, lowered in tests to reach the cap
	maxFDs   int // watchMaxFDs, likewise

	mu     sync.Mutex
	roots  map[string]*watchRoot // worktree path → what is watched there
	dirty  map[string]*watchEdits
	nDirs  int
	nFDs   int  // descriptors held, where a watch costs one
	capped bool // the budget ran out; said once
}

type watchRoot struct {
	projectID string
	ignored   []string // directories git ignores, relative to the worktree
	walked    time.Time
	announced time.Time // last event about this worktree
	watched   bool      // the whole tree is watched, so events are complete
	dirs, fds int       // what it holds, to give back when it is dropped
}

// watchEdits are the paths one worktree changed since the last flush.
type watchEdits struct {
	paths map[string]bool
	more  bool
}

func newWorktreeWatcher(announce func(proto.WorktreeChanged), request func(string)) *worktreeWatcher {
	be, err := newBackend()
	if err != nil {
		// No watches to be had (an old kernel, or the per-user limit is
		// already spent). Polling still works, so this is not fatal.
		log.Printf("worktree watch unavailable: %v", err)
		return nil
	}
	return &worktreeWatcher{be: be, announce: announce, request: request,
		maxDirs: watchMaxDirs, maxFDs: watchMaxFDs,
		roots: map[string]*watchRoot{}, dirty: map[string]*watchEdits{}}
}

// run reads events until quit is closed.
func (w *worktreeWatcher) run(quit <-chan struct{}) {
	if w == nil {
		return
	}
	t := time.NewTicker(watchDebounce)
	defer t.Stop()
	defer w.be.close()
	for {
		select {
		case path, ok := <-w.be.paths():
			if !ok {
				return
			}
			w.note(path)
		case err, ok := <-w.be.errs():
			if !ok {
				return
			}
			log.Printf("worktree watch: %v", err)
		case <-t.C:
			w.flush()
		case <-quit:
			return
		}
	}
}

// note records one event, and starts watching a directory that was created.
func (w *worktreeWatcher) note(path string) {
	root := w.rootOf(path)
	if root == "" {
		return
	}
	w.mu.Lock()
	r := w.roots[root]
	w.mu.Unlock()
	if r == nil {
		return
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || skipPath(rel, r.ignored) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	e := w.dirty[root]
	if e == nil {
		e = &watchEdits{paths: map[string]bool{}}
		w.dirty[root] = e
	}
	if len(e.paths) >= watchMaxPaths && !e.paths[rel] {
		e.more = true
		return
	}
	e.paths[rel] = true
}

// rootOf is the watched worktree a path belongs to, longest first so a
// worktree inside another is matched before its parent.
func (w *worktreeWatcher) rootOf(path string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	best := ""
	for root := range w.roots {
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			if len(root) > len(best) {
				best = root
			}
		}
	}
	return best
}

// flush announces what changed since the last tick, one event per worktree.
// A worktree still inside its quiet stretch keeps its edits for the next one.
func (w *worktreeWatcher) flush() {
	now := time.Now()
	w.mu.Lock()
	var changed []proto.WorktreeChanged
	for root, e := range w.dirty {
		r := w.roots[root]
		if r == nil {
			delete(w.dirty, root) // the worktree went away while the edits waited
			continue
		}
		if now.Sub(r.announced) < watchMinGap {
			continue // these join whatever else arrives before the next one
		}
		r.announced = now
		delete(w.dirty, root)
		paths := make([]string, 0, len(e.paths))
		for p := range e.paths {
			paths = append(paths, p)
		}
		slices.Sort(paths) // a map's order would make every event different
		changed = append(changed, proto.WorktreeChanged{
			ProjectID: r.projectID, Worktree: root, Paths: paths, More: e.more})
	}
	w.mu.Unlock()

	seen := map[string]bool{}
	for _, c := range changed {
		w.announce(c)
		if !seen[c.ProjectID] { // one git re-read however many worktrees moved
			seen[c.ProjectID] = true
			w.request(c.ProjectID)
		}
	}
}

// syncProject makes the watched worktrees of a project exactly worktrees.
func (w *worktreeWatcher) syncProject(projectID string, worktrees []string) {
	if w == nil {
		return
	}
	want := map[string]bool{}
	for _, p := range worktrees {
		if p != "" {
			want[p] = true
		}
	}
	w.mu.Lock()
	var gone, held, stale []string
	for root, r := range w.roots {
		switch {
		case r.projectID != projectID:
		case !want[root]:
			gone = append(gone, root)
		case time.Since(r.walked) > watchResyncEvery:
			stale = append(stale, root)
			delete(want, root)
		default:
			held = append(held, root)
			delete(want, root)
		}
	}
	w.mu.Unlock()

	for _, root := range gone {
		w.dropRoot(root)
	}
	// A worktree deleted under us leaves no event behind — nothing watches
	// its parent — so the ones being kept are checked rather than assumed.
	for _, root := range held {
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			w.dropRoot(root)
		}
	}
	for _, root := range stale {
		w.walkRoot(projectID, root) // again, for directories created unseen
	}
	for root := range want {
		w.walkRoot(projectID, root)
	}
}

// dropProject stops watching everything a project owned.
func (w *worktreeWatcher) dropProject(projectID string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	var gone []string
	for root, r := range w.roots {
		if r.projectID == projectID {
			gone = append(gone, root)
		}
	}
	w.mu.Unlock()
	for _, root := range gone {
		w.dropRoot(root)
	}
}

// walkRoot watches a worktree and every directory under it that git does not
// ignore. A worktree that no longer exists is dropped instead.
func (w *worktreeWatcher) walkRoot(projectID, root string) {
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		w.dropRoot(root)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), watchGitTimeout)
	ignored, err := gitx.IgnoredDirs(ctx, root)
	cancel()
	if err != nil {
		// Not a checkout, or git could not say. Watching its whole tree
		// could mean node_modules, so it is left to polling.
		w.dropRoot(root)
		return
	}
	// What the tree would cost, before anything is watched: a worktree is
	// watched whole or not at all. Half of one is worse than none, because
	// the reader would back off its polling and then miss the other half.
	dirs, fds := w.be.cost(root, ignored)
	w.mu.Lock()
	r := w.roots[root]
	if r == nil {
		r = &watchRoot{projectID: projectID}
		w.roots[root] = r
	}
	r.projectID, r.ignored, r.walked = projectID, ignored, time.Now()
	held := 0
	if r.watched {
		held = r.dirs // re-walking a root keeps what it already has
	}
	budgeted := w.be.budgeted()
	room := w.nDirs-held+dirs <= w.maxDirs && (!budgeted || w.nFDs-r.fds+fds <= w.maxFDs)
	w.mu.Unlock()

	if !room {
		w.noteCapped(root, dirs, fds)
		w.dropRoot(root)
		return
	}
	if err := w.be.watch(root, ignored); err != nil {
		log.Printf("worktree watch: %s: %v", root, err)
		w.dropRoot(root)
		return
	}
	w.mu.Lock()
	if r = w.roots[root]; r != nil {
		r.watched, r.dirs, r.fds = true, dirs, fds
		w.nDirs += dirs - held
		w.nFDs += fds - r.fds
	}
	w.mu.Unlock()
}

// noteCapped says once that a worktree is too big for the budget, so its
// changes come from polling instead.
func (w *worktreeWatcher) noteCapped(root string, dirs, fds int) {
	w.mu.Lock()
	first := !w.capped
	w.capped = true
	w.mu.Unlock()
	if first {
		what := fmt.Sprintf("%d directories", dirs)
		if w.be.budgeted() {
			what = fmt.Sprintf("%d directories and %d files", dirs, fds-dirs)
		}
		log.Printf("worktree watch: %s needs %s, past the budget; it falls back to polling", root, what)
	}
}

// watching reports whether a worktree's edits are announced rather than
// left to the reader's polling.
func (w *worktreeWatcher) watching(root string) bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	r := w.roots[root]
	return r != nil && r.watched
}

// dropRoot forgets a worktree entirely.
func (w *worktreeWatcher) dropRoot(root string) {
	w.mu.Lock()
	r, known := w.roots[root]
	if r != nil {
		w.nDirs -= r.dirs
		w.nFDs -= r.fds
		w.nDirs, w.nFDs = max(w.nDirs, 0), max(w.nFDs, 0)
	}
	delete(w.roots, root)
	delete(w.dirty, root)
	w.mu.Unlock()
	if known {
		w.be.unwatch(root)
	}
}

// skipPath reports whether a path relative to a worktree is one to leave
// alone: git's own metadata, or a directory git ignores.
func skipPath(rel string, ignored []string) bool {
	if rel == "" || rel == "." {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".git" { // the project ticker reads git's metadata itself
			return true
		}
	}
	for _, ig := range ignored {
		if rel == ig || strings.HasPrefix(rel, ig+"/") {
			return true
		}
	}
	return false
}
