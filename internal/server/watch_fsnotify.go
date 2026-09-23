//go:build !darwin || !cgo

package server

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
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

type fsnotifyBackend struct {
	fsw  *fsnotify.Watcher
	out  chan string
	stop chan struct{}

	mu      sync.Mutex
	ignored map[string][]string // root → directories not to watch
	dirs    map[string]string   // watched directory → its root
}

func newBackend() (watchBackend, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	b := &fsnotifyBackend{
		fsw: fsw, out: make(chan string, 256), stop: make(chan struct{}),
		ignored: map[string][]string{}, dirs: map[string]string{},
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

func (b *fsnotifyBackend) watch(root string, ignored []string) error {
	b.mu.Lock()
	b.ignored[root] = ignored
	b.mu.Unlock()
	b.addTree(root, root)
	return nil
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

func (b *fsnotifyBackend) paths() <-chan string { return b.out }
func (b *fsnotifyBackend) errs() <-chan error   { return b.fsw.Errors }

func (b *fsnotifyBackend) close() {
	close(b.stop)
	_ = b.fsw.Close()
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
					b.addTree(root, ev.Name)
				}
			}
			if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
				b.dropTree(ev.Name)
			}
			select {
			case b.out <- ev.Name:
			case <-b.stop:
				return
			default: // the reader is behind; the next event will do
				log.Printf("worktree watch: dropped an event for %s", ev.Name)
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
