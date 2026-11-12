//go:build !darwin || !cgo

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (w *a7Watch) fsn(t *testing.T) *fsnotifyBackend {
	t.Helper()
	b, ok := w.be.(*fsnotifyBackend)
	if !ok {
		t.Skip("not the fsnotify backend")
	}
	return b
}

// watchedUnder is how many watched directories are dir or below it.
func watchedUnder(b *fsnotifyBackend, dir string) int {
	n := 0
	for _, d := range b.watchedDirs() {
		if d == dir || strings.HasPrefix(d, dir+string(filepath.Separator)) {
			n++
		}
	}
	return n
}

// waitWatched waits until dir is watched: the pump has taken the events
// made before it.
func waitWatched(t *testing.T, b *fsnotifyBackend, dir string) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if watchedUnder(b, dir) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never watched", dir)
}

// A folder git ignores, made after the worktree was walked — npm install's
// node_modules, a build's output — is not watched: on kqueue each of its
// files would cost a descriptor, and one install went past the budget.
func TestWatchLeavesLaterIgnoredFoldersAlone(t *testing.T) {
	w := a7New(t)
	b := w.fsn(t)
	repo := a7Repo(t) // ignores node_modules
	if err := os.RemoveAll(filepath.Join(repo, "node_modules")); err != nil {
		t.Fatal(err)
	}
	w.syncProject("r1", []string{repo})
	if !w.watching(repo) {
		t.Fatal("not watched")
	}
	for i := 0; i < 20; i++ {
		a7Write(t, repo, fmt.Sprintf("node_modules/pkg%02d/lib/index.js", i), "x\n")
	}
	a7Write(t, repo, "src/later/new.go", "package later\n") // after: its watch says the pump got this far
	waitWatched(t, b, filepath.Join(repo, "src", "later"))
	if n := watchedUnder(b, filepath.Join(repo, "node_modules")); n != 0 {
		t.Fatalf("%d directories of node_modules watched", n)
	}
}

// A folder git does not ignore, made later, is watched and charged — and
// only as far as the budget goes.
func TestWatchChargesLaterFolders(t *testing.T) {
	w := a7New(t)
	b := w.fsn(t)
	repo := a7Repo(t)
	w.syncProject("r1", []string{repo})
	w.mu.Lock()
	start := w.nDirs
	w.maxDirs = start + 3
	w.mu.Unlock()

	a7Write(t, repo, "small/one.go", "x\n") // 1 directory: fits
	waitWatched(t, b, filepath.Join(repo, "small"))
	w.mu.Lock()
	if w.nDirs != start+1 {
		t.Errorf("charged %d for one directory", w.nDirs-start)
	}
	w.mu.Unlock()

	// 10 directories in one: does not fit what is left.
	big := filepath.Join(repo, "big")
	for i := 0; i < 10; i++ {
		a7Write(t, repo, fmt.Sprintf("big/d%d/f.go", i), "x\n")
	}
	a7Write(t, repo, "small/later/f.go", "x\n") // fits, and says the pump is past big
	waitWatched(t, b, filepath.Join(repo, "small", "later"))
	w.mu.Lock()
	held, max := w.nDirs, w.maxDirs
	w.mu.Unlock()
	if held > max {
		t.Fatalf("holds %d directories on a budget of %d", held, max)
	}
	if n := len(b.watchedDirs()); n > max {
		t.Fatalf("%d directories watched on a budget of %d", n, max)
	}
	if n := watchedUnder(b, big); n != 0 {
		t.Fatalf("%d directories of a tree past the budget watched", n)
	}
}

// A resync lets go of what git has come to ignore, and of what is gone,
// so what is watched is what it counted.
func TestWatchResyncLetsGo(t *testing.T) {
	w := a7New(t)
	b := w.fsn(t)
	repo := a7Repo(t)
	for i := 0; i < 5; i++ {
		a7Write(t, repo, fmt.Sprintf("gen/d%d/f.go", i), "x\n")
	}
	a7Write(t, repo, "old/f.go", "x\n")
	w.syncProject("r1", []string{repo})
	if watchedUnder(b, filepath.Join(repo, "gen")) != 6 {
		t.Fatalf("gen not watched: %v", b.watchedDirs())
	}
	a7Write(t, repo, ".gitignore", "node_modules/\n*.log\ngen/\n")
	if err := os.RemoveAll(filepath.Join(repo, "old")); err != nil {
		t.Fatal(err)
	}
	w.walkRoot("r1", repo)
	if n := watchedUnder(b, filepath.Join(repo, "gen")); n != 0 {
		t.Fatalf("%d directories git now ignores still watched", n)
	}
	if n := watchedUnder(b, filepath.Join(repo, "old")); n != 0 {
		t.Fatal("a directory gone is still watched")
	}
	w.mu.Lock()
	held := w.nDirs
	w.mu.Unlock()
	if n := len(b.watchedDirs()); n != held {
		t.Fatalf("%d watched, %d charged", n, held)
	}
}
