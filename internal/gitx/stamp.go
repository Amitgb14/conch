package gitx

import (
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
)

// MetaStamp is a cheap fingerprint of a repository's git metadata, computed
// from file mtimes and sizes without running git. It changes when refs, HEAD,
// the index or worktrees change, so callers can skip refreshes otherwise.
// It reads both ref backends: the files one (refs/heads, packed-refs) and
// reftable, git 3's default, where every ref — HEAD too — is in
// reftable/ and the files above never move.
func MetaStamp(commonDir string) string {
	h := fnv.New64a()
	add := func(path string) {
		info, err := os.Stat(path)
		if err != nil {
			return
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", path, info.Size(), info.ModTime().UnixNano())
	}
	// Every reftable write adds a table and rewrites tables.list with its
	// name, so the list's text moves even when its size and mtime might not.
	list := func(dir string) {
		if b, err := os.ReadFile(filepath.Join(dir, "reftable", "tables.list")); err == nil {
			fmt.Fprintf(h, "%s\x00%s\x00", dir, b)
		}
	}
	for _, name := range []string{"HEAD", "index", "packed-refs", "FETCH_HEAD"} {
		add(filepath.Join(commonDir, name))
	}
	list(commonDir)
	// A deleted branch only shows as a missing path, which the hash of
	// present paths captures.
	filepath.WalkDir(filepath.Join(commonDir, "refs", "heads"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			add(p)
		}
		return nil
	})
	wts, _ := filepath.Glob(filepath.Join(commonDir, "worktrees", "*"))
	for _, wt := range wts {
		add(filepath.Join(wt, "HEAD"))
		add(filepath.Join(wt, "index"))
		list(wt)
	}
	return fmt.Sprintf("%016x", h.Sum64())
}
