package proto

import "time"

// CapServerHeap is server.heap: the server writes its heap and its
// goroutines to files on its own machine and says how much memory it
// holds. It is how a server that keeps growing is looked into without
// stopping it — two snapshots an hour apart, compared with go tool pprof
// -base, name the code holding what was added.
const CapServerHeap = "server.heap.v1"

// MethodServerHeap answers a ServerHeap (server.heap.v1).
const MethodServerHeap = "server.heap"

// ServerHeap is the server's memory at one moment, read after a garbage
// collection so it counts what is still held rather than what is
// waiting to be freed. Heap and Goroutines are the files written, on the
// server's machine: a heap profile (pprof, gzipped) and the goroutines
// with their stacks (text). They hold names of the server's functions and
// sizes, never what a pane shows.
type ServerHeap struct {
	At          time.Time `json:"at"`
	Heap        string    `json:"heap"`
	Goroutines  string    `json:"goroutines"`
	HeapInuse   uint64    `json:"heap_inuse"`   // bytes in use by live objects' spans
	HeapAlloc   uint64    `json:"heap_alloc"`   // bytes of live objects
	HeapObjects uint64    `json:"heap_objects"` // live objects
	Sys         uint64    `json:"sys"`          // bytes taken from the system, all told
	Goroutine   int       `json:"goroutine"`    // goroutines running
	Panes       int       `json:"panes"`
}
