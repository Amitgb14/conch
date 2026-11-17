package server

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// heapKeep is how many snapshots of each kind the debug folder keeps: a
// profile is a few hundred KB, and a server asked every few minutes for
// a day would otherwise fill a disk nobody looks at.
const heapKeep = 10

// heapSnapshot writes the server's heap profile and its goroutines into
// dir, and says how much memory it holds (server.heap.v1). It collects
// garbage first, so the numbers and the profile are what is still held:
// a server that keeps growing is holding something, and what is merely
// waiting to be freed says nothing about what.
//
// The files are the person's alone (0600, in a 0700 folder): they name
// conch's functions and sizes, and a goroutine's stack its arguments'
// addresses, never what a pane shows — but they are this machine's.
func (s *Server) heapSnapshot(dir string, now time.Time) (proto.ServerHeap, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return proto.ServerHeap{}, fmt.Errorf("the debug folder: %w", err)
	}
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	stamp := now.Format("20060102-150405.000")
	h := proto.ServerHeap{
		At:          now,
		Heap:        filepath.Join(dir, "heap-"+stamp+".pb.gz"),
		Goroutines:  filepath.Join(dir, "goroutines-"+stamp+".txt"),
		HeapInuse:   ms.HeapInuse,
		HeapAlloc:   ms.HeapAlloc,
		HeapObjects: ms.HeapObjects,
		Sys:         ms.Sys,
		Goroutine:   runtime.NumGoroutine(),
	}
	s.mu.Lock()
	h.Panes = len(s.panes)
	s.mu.Unlock()

	if err := writeProfile(h.Heap, "heap", 0); err != nil {
		return proto.ServerHeap{}, err
	}
	if err := writeProfile(h.Goroutines, "goroutine", 1); err != nil {
		os.Remove(h.Heap)
		return proto.ServerHeap{}, err
	}
	pruneSnapshots(dir, "heap-", heapKeep)
	pruneSnapshots(dir, "goroutines-", heapKeep)
	return h, nil
}

// writeProfile writes the named runtime profile to path, created new:
// two snapshots in the same millisecond must not write over each other.
func writeProfile(path, name string, debug int) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("the %s profile: %w", name, err)
	}
	werr := pprof.Lookup(name).WriteTo(f, debug)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(path)
		return fmt.Errorf("the %s profile: %w", name, werr)
	}
	return nil
}

// pruneSnapshots keeps the newest keep files in dir whose names start
// with prefix — the time in the name sorts them — and removes the rest.
// What else is in the folder is left alone.
func pruneSnapshots(dir, prefix string, keep int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	for len(names) > keep {
		os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}
