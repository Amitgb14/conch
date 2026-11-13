//go:build !darwin || !cgo

package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/Amitgb14/conch/internal/gitx"
)

// The watch backend for everywhere but macOS: fsnotify, which is inotify on
// Linux and kqueue on the BSDs. Both are told about one directory at a time,
// so a tree is walked and each directory added, and directories created
// later are added as their parent reports them.
//
// kqueue opens a descriptor for every watched path — the directory and each
// file in it — which is why this backend is budgeted. inotify keeps a single
// descriptor for the lot and only counts watches.

// kqueueWatches reports whether a watch costs a file descriptor per path.
var kqueueWatches = runtime.GOOS != "linux" && runtime.GOOS != "windows"

// skipRel reports whether the directory at path, under root, is one to
// leave alone. It lives here, not in watch.go, because only this backend
// walks a tree: FSEvents takes it whole.
func skipRel(root, path string, ignored []string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	return rel != "." && skipPath(filepath.ToSlash(rel), ignored)
}

type fsnotifyBackend struct {
	fsw  *fsnotify.Watcher
	out  chan string
	stop chan struct{}
	// lost says events were thrown away because the reader was behind;
	// the watcher turns it into "these worktrees changed, paths unknown".
	lost  atomic.Bool
	drops dropLog

	closeOnce sync.Once

	mu      sync.Mutex
	ignored map[string][]string // root → directories not to watch
	dirs    map[string]string   // watched directory → its root
	// charge asks the watcher for room for a directory made after its
	// root was walked (see addLater); nil allows it, as tests that make
	// a backend alone want.
	charge func(root string, dirs, fds int) bool
	// ignores is whether git ignores a directory, rel to root; a field so
	// tests can answer without git.
	ignores func(root, rel string) bool
}

func newBackend() (watchBackend, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	b := &fsnotifyBackend{
		fsw: fsw, out: make(chan string, 256), stop: make(chan struct{}),
		ignored: map[string][]string{}, dirs: map[string]string{},
		ignores: gitIgnoresDir,
	}
	go b.pump()
	return b, nil
}

func (b *fsnotifyBackend) budgeted() bool { return kqueueWatches }

// cost is what watching root would take: a directory each, and on kqueue a
// descriptor for every file in them too.
func (b *fsnotifyBackend) cost(root string, ignored []string) (dirs, fds int) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if skipRel(root, path, ignored) {
			return filepath.SkipDir
		}
		dirs++
		fds++
		if kqueueWatches {
			if es, err := os.ReadDir(path); err == nil {
				for _, e := range es {
					if !e.IsDir() {
						fds++
					}
				}
			}
		}
		return nil
	})
	return dirs, fds
}

// watch watches root bar ignored. Called again for a root already watched
// (the resync), it also lets go of what is no longer wanted: a directory
// gone, or one git has come to ignore — a node_modules made since the last
// walk — so what is watched is what the walk counted.
func (b *fsnotifyBackend) watch(root string, ignored []string) error {
	b.mu.Lock()
	b.ignored[root] = ignored
	var stale []string
	for d, r := range b.dirs {
		if r != root {
			continue
		}
		if st, err := os.Lstat(d); err != nil || !st.IsDir() || skipRel(root, d, ignored) {
			stale = append(stale, d)
		}
	}
	for _, d := range stale {
		delete(b.dirs, d)
	}
	b.mu.Unlock()
	for _, d := range stale {
		_ = b.fsw.Remove(d)
	}
	b.addTree(root, root)
	return nil
}

// setCharge is how the watcher hands over its budget (watch.go).
func (b *fsnotifyBackend) setCharge(charge func(root string, dirs, fds int) bool) {
	b.mu.Lock()
	b.charge = charge
	b.mu.Unlock()
}

// gitIgnoresDir asks git, briefly: a folder whose answer does not come is
// watched, as it would have been.
func gitIgnoresDir(root, rel string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ignored, err := gitx.IgnoresDir(ctx, root, rel)
	return err == nil && ignored
}

// addLater watches a directory made inside a watched one since its root
// was walked — mkdir, a package unpacked, a build's output folder — but
// only what git does not ignore, and only as far as the budget goes. The
// walk that counted the root never saw it: added unasked, one npm install
// put hundreds of folders and thousands of files under watch on kqueue,
// past the budget and past select's descriptor limit, which once crashed
// the server. What does not fit is left to the next resync, which counts
// it, and the reader is told something changed meanwhile.
func (b *fsnotifyBackend) addLater(root, dir string) {
	rel, err := filepath.Rel(root, dir)
	if err != nil || b.ignores(root, filepath.ToSlash(rel)) {
		return
	}
	b.mu.Lock()
	ignored, charge := b.ignored[root], b.charge
	b.mu.Unlock()
	if skipRel(root, dir, ignored) {
		return
	}
	// cost walks dir alone, so the ignored list (relative to root) is
	// applied by addTree as it adds; what it skips was over-counted, which
	// errs on the side of the budget.
	dirs, fds := b.cost(dir, nil)
	if charge != nil && !charge(root, dirs, fds) {
		b.lost.Store(true)
		return
	}
	b.addTree(root, dir)
}

func (b *fsnotifyBackend) unwatch(root string) {
	b.mu.Lock()
	delete(b.ignored, root)
	var gone []string
	for d, r := range b.dirs {
		if r == root {
			gone = append(gone, d)
		}
	}
	for _, d := range gone {
		delete(b.dirs, d)
	}
	b.mu.Unlock()
	for _, d := range gone {
		_ = b.fsw.Remove(d)
	}
}

func (b *fsnotifyBackend) missed() bool {
	b.drops.report(watchLogf)
	return b.lost.Swap(false)
}

// noteDropped is a path thrown away: counted, and logged with the rest of
// its burst when the watcher next asks.
func (b *fsnotifyBackend) noteDropped(path string) {
	b.lost.Store(true)
	b.drops.add(path)
}

// loseOne says an event was dropped, without needing a flood to make
// one happen: a test cannot reliably outrun a channel of 256.
func (b *fsnotifyBackend) loseOne() { b.lost.Store(true) }

func (b *fsnotifyBackend) paths() <-chan string { return b.out }
func (b *fsnotifyBackend) errs() <-chan error   { return b.fsw.Errors }

// close can be called twice: a reload closes the watches before exec'ing,
// and the run loop closes them again when the server stops.
func (b *fsnotifyBackend) close() {
	b.closeOnce.Do(func() {
		close(b.stop)
		_ = b.fsw.Close()
	})
}

// addTree watches dir and its unignored descendants, all belonging to root.
func (b *fsnotifyBackend) addTree(root, dir string) {
	b.mu.Lock()
	ignored := b.ignored[root]
	b.mu.Unlock()
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if skipRel(root, path, ignored) {
			return filepath.SkipDir
		}
		b.mu.Lock()
		_, have := b.dirs[path]
		b.mu.Unlock()
		if have {
			return nil
		}
		if err := b.fsw.Add(path); err != nil {
			return filepath.SkipDir // out of watches, or unreadable
		}
		b.mu.Lock()
		b.dirs[path] = root
		b.mu.Unlock()
		return nil
	})
}

// dropTree stops watching dir and anything under it.
func (b *fsnotifyBackend) dropTree(dir string) {
	prefix := dir + string(filepath.Separator)
	b.mu.Lock()
	var gone []string
	for d := range b.dirs {
		if d == dir || strings.HasPrefix(d, prefix) {
			gone = append(gone, d)
		}
	}
	for _, d := range gone {
		delete(b.dirs, d)
	}
	b.mu.Unlock()
	for _, d := range gone {
		_ = b.fsw.Remove(d)
	}
}

// pump turns the watcher's events into paths, keeping the watched set in
// step as directories come and go.
func (b *fsnotifyBackend) pump() {
	for {
		select {
		case ev, ok := <-b.fsw.Events:
			if !ok {
				return
			}
			if ev.Op == fsnotify.Chmod {
				continue // permissions and times alone change no content
			}
			b.mu.Lock()
			root, watched := b.dirs[filepath.Dir(ev.Name)]
			b.mu.Unlock()
			if !watched {
				continue
			}
			// A new directory may already hold files written before this
			// watch, so it is walked rather than only added.
			if ev.Has(fsnotify.Create) {
				if st, err := os.Lstat(ev.Name); err == nil && st.IsDir() {
					b.addLater(root, ev.Name)
				}
			}
			if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
				b.dropTree(ev.Name)
			}
			select {
			case b.out <- ev.Name:
			case <-b.stop:
				return
			default:
				// The reader is behind. The path goes rather than the
				// loop blocking; the fact that something changed does not
				// (missed, and noteAll in watch.go).
				b.noteDropped(ev.Name)
			}
		case <-b.stop:
			return
		}
	}
}

// watchedDirs is every directory this backend has a watch on. Tests use it
// to check that ignored trees cost nothing.
func (b *fsnotifyBackend) watchedDirs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.dirs))
	for d := range b.dirs {
		out = append(out, d)
	}
	return out
}
