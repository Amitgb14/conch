package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A snapshot writes a heap profile and the goroutines, the person's alone,
// and says how much is held.
func TestHeapSnapshot(t *testing.T) {
	home := t.TempDir()
	s := New(filepath.Join(home, "s.sock"), home)
	s.mu.Lock()
	s.panes["p1"], s.panes["p2"] = &entry{}, &entry{}
	s.mu.Unlock()
	dir := filepath.Join(home, "debug")
	now := time.Date(2026, 11, 16, 21, 5, 0, 0, time.UTC)

	h, err := s.heapSnapshot(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if h.Panes != 2 || h.HeapAlloc == 0 || h.HeapInuse < h.HeapAlloc/2 || h.HeapObjects == 0 || h.Sys < h.HeapInuse || h.Goroutine < 1 || !h.At.Equal(now) {
		t.Errorf("numbers: %+v", h)
	}
	if filepath.Dir(h.Heap) != dir || filepath.Base(h.Heap) != "heap-20261116-210500.000.pb.gz" ||
		filepath.Base(h.Goroutines) != "goroutines-20261116-210500.000.txt" {
		t.Errorf("paths: %s %s", h.Heap, h.Goroutines)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("folder: %v %v", fi.Mode(), err)
	}
	for _, f := range []string{h.Heap, h.Goroutines} {
		if fi, err := os.Stat(f); err != nil || fi.Mode().Perm() != 0o600 || fi.Size() == 0 {
			t.Errorf("%s: %v %v", f, fi, err)
		}
	}
	// The heap profile is pprof's: gzip, holding a protobuf naming the
	// functions that allocated.
	raw, _ := os.ReadFile(h.Heap)
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("heap profile is not gzip: %v", err)
	}
	pb, err := io.ReadAll(zr)
	if err != nil || len(pb) == 0 || !bytes.Contains(pb, []byte("inuse_space")) {
		t.Errorf("heap profile: %d bytes, %v", len(pb), err)
	}
	g, _ := os.ReadFile(h.Goroutines)
	if !strings.HasPrefix(string(g), "goroutine profile: total ") || !strings.Contains(string(g), "heapSnapshot") {
		t.Errorf("goroutines: %.200s", g)
	}

	// The same instant again does not write over the first.
	if _, err := s.heapSnapshot(dir, now); err == nil {
		t.Error("a second snapshot in the same instant replaced the first")
	}
	if got, _ := os.ReadFile(h.Heap); !bytes.Equal(got, raw) {
		t.Error("the first heap profile changed")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 2 {
		t.Errorf("a failed snapshot left files: %d", len(ents))
	}
}

// A folder that cannot be made is said, not a panic.
func TestHeapSnapshotUnwritable(t *testing.T) {
	home := t.TempDir()
	s := New(filepath.Join(home, "s.sock"), home)
	blocker := filepath.Join(home, "debug")
	if err := os.WriteFile(blocker, []byte("a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.heapSnapshot(blocker, time.Now()); err == nil || !strings.Contains(err.Error(), "debug folder") {
		t.Errorf("err %v", err)
	}
}

// Only the newest ten of each kind stay; anything else in the folder is
// not conch's to remove.
func TestPruneSnapshots(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 13; i++ {
		for _, f := range []string{"heap-202611%02d-000000.000.pb.gz", "goroutines-202611%02d-000000.000.txt"} {
			if err := os.WriteFile(filepath.Join(dir, strings.Replace(f, "%02d", twoDigits(i+1), 1)), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o600)
	os.Mkdir(filepath.Join(dir, "heap-folder"), 0o700)
	pruneSnapshots(dir, "heap-", 10)
	pruneSnapshots(dir, "goroutines-", 10)
	var heaps, gors []string
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		switch {
		case e.IsDir():
		case strings.HasPrefix(e.Name(), "heap-"):
			heaps = append(heaps, e.Name())
		case strings.HasPrefix(e.Name(), "goroutines-"):
			gors = append(gors, e.Name())
		}
	}
	if len(heaps) != 10 || heaps[0] != "heap-20261104-000000.000.pb.gz" || len(gors) != 10 || gors[0] != "goroutines-20261104-000000.000.txt" {
		t.Errorf("kept %v %v", heaps, gors)
	}
	for _, keep := range []string{"notes.txt", "heap-folder"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s removed", keep)
		}
	}
	pruneSnapshots(filepath.Join(dir, "missing"), "heap-", 10) // no folder: nothing to do
	pruneSnapshots(dir, "heap-", 0)
	if ents, _ := os.ReadDir(dir); len(ents) != 12 { // 10 goroutines, notes, the folder
		t.Errorf("keep 0 left %d", len(ents))
	}
}

func twoDigits(n int) string { return string(rune('0'+n/10)) + string(rune('0'+n%10)) }
