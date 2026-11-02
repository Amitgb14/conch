package config

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path all at once, as far as anyone
// reading it can tell: into a temporary file of its own beside it, then
// renamed over it. The temporary file's name is unique to the writer,
// which is the point: with one fixed name (path + ".tmp"), two writers at
// once — two TUIs, or a TUI and Conch.app, saving ui.json in the same
// moment — truncated and wrote the same temporary file, and the shorter
// write left the longer one's tail behind it. The rename then published
// both: a ui.json that did not parse, which read as an empty one and lost
// every workspace, tab and fold. Seen live (2026-11-02).
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}
