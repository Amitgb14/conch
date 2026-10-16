package transfer

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// FS is the files a copy reads and writes: this machine's (Local), or a
// host's that a terminal is logged in to, through sftp. It is what the two
// have in common, with paths always slash-separated and absolute — conch
// runs on macOS and Linux, and sftp speaks POSIX paths.
type FS interface {
	Stat(name string) (fs.FileInfo, error)
	Lstat(name string) (fs.FileInfo, error)
	// ReadDir is a folder's entries as Lstat sees them, sorted by name.
	ReadDir(name string) ([]fs.FileInfo, error)
	// Open is a file to read. It must not wait on a fifo.
	Open(name string) (File, error)
	// Create makes a new file to write, refusing a name already taken.
	Create(name string, perm fs.FileMode) (File, error)
	Mkdir(name string, perm fs.FileMode) error
	Chmod(name string, mode fs.FileMode) error
	// Rename moves oldname to newname, replacing a file already there.
	Rename(oldname, newname string) error
	Remove(name string) error
	RemoveAll(name string) error
	Symlink(target, name string) error
	Readlink(name string) (string, error)
	// Home is where ~ is.
	Home() (string, error)
}

// File is an open file of an FS.
type File interface {
	io.Reader
	io.ReaderAt
	io.Writer
	io.WriterAt
	io.Closer
	Stat() (fs.FileInfo, error)
}

// Local is this machine's files.
var Local FS = localFS{}

type localFS struct{}

func (localFS) Stat(name string) (fs.FileInfo, error)  { return os.Stat(name) }
func (localFS) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }

func (localFS) ReadDir(name string) ([]fs.FileInfo, error) {
	entries, err := os.ReadDir(name)
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue // gone since the folder was read
		}
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

// Open opens without blocking: opening a fifo for reading otherwise waits
// for a writer that may never come.
func (localFS) Open(name string) (File, error) {
	return os.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

func (localFS) Create(name string, perm fs.FileMode) (File, error) {
	return os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
}

func (localFS) Mkdir(name string, perm fs.FileMode) error { return os.Mkdir(name, perm) }
func (localFS) Chmod(name string, mode fs.FileMode) error { return os.Chmod(name, mode) }
func (localFS) Rename(oldname, newname string) error      { return os.Rename(oldname, newname) }
func (localFS) Remove(name string) error                  { return os.Remove(name) }
func (localFS) RemoveAll(name string) error               { return os.RemoveAll(name) }
func (localFS) Symlink(target, name string) error         { return os.Symlink(target, name) }
func (localFS) Readlink(name string) (string, error)      { return os.Readlink(name) }
func (localFS) Home() (string, error)                     { return os.UserHomeDir() }

// CreateTemp makes a new file in dir named pattern with a random part in
// place of its "*", as os.CreateTemp does, and returns it with its path.
func CreateTemp(fsys FS, dir, pattern string) (File, string, error) {
	for range 100 {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		name := pattern + hex.EncodeToString(b)
		if i := lastStar(pattern); i >= 0 {
			name = pattern[:i] + hex.EncodeToString(b) + pattern[i+1:]
		}
		path := filepath.Join(dir, name)
		f, err := fsys.Create(path, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, path, err
	}
	return nil, "", errors.New("no free name for a temporary file in " + dir)
}

func lastStar(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '*' {
			return i
		}
	}
	return -1
}

// walk calls fn for root and everything under it, folders before what is
// in them, in name order, never following a symlink.
func walk(fsys FS, root string, fn func(path string, info fs.FileInfo) error) error {
	info, err := fsys.Lstat(root)
	if err != nil {
		return err
	}
	return walkFrom(fsys, root, info, fn)
}

func walkFrom(fsys FS, path string, info fs.FileInfo, fn func(string, fs.FileInfo) error) error {
	if err := fn(path, info); err != nil || !info.IsDir() {
		return err
	}
	entries, err := fsys.ReadDir(path)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if err := walkFrom(fsys, filepath.Join(path, e.Name()), e, fn); err != nil {
			return err
		}
	}
	return nil
}
