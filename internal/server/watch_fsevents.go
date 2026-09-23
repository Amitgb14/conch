//go:build darwin && cgo

package server

import (
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsevents"
)

// The watch backend for macOS: FSEvents, which watches a whole tree from one
// stream. fsnotify's kqueue backend would do instead, but it opens a file
// descriptor for every watched path — the directory and each file in it —
// and a worktree of any size then holds thousands. Descriptors are handed
// out lowest first and the pane read loop waits on its pty with select,
// whose fd_set holds 1024, so that once left a new pane's pty past the set
// and panicked the server. FSEvents costs one stream per worktree instead.

// fseventsLatency is how long the kernel gathers changes before handing
// them over. It is well under the debounce that follows, so it adds no
// noticeable delay and saves a wake-up per file.
const fseventsLatency = 50 * time.Millisecond

type fseventsBackend struct {
	out  chan string
	stop chan struct{}

	closeOnce sync.Once
	pumps     sync.WaitGroup // one per started stream

	mu      sync.Mutex
	streams map[string]*fsevents.EventStream
}

func newBackend() (watchBackend, error) {
	return &fseventsBackend{
		out: make(chan string, 256), stop: make(chan struct{}),
		streams: map[string]*fsevents.EventStream{},
	}, nil
}

// budgeted is false: a stream watches a whole tree, so there is nothing to
// ration. cost says so too.
func (b *fseventsBackend) budgeted() bool                                { return false }
func (b *fseventsBackend) cost(root string, ignored []string) (int, int) { return 0, 0 }
func (b *fseventsBackend) paths() <-chan string                          { return b.out }
func (b *fseventsBackend) errs() <-chan error                            { return nil }

func (b *fseventsBackend) watch(root string, ignored []string) error {
	b.mu.Lock()
	if _, have := b.streams[root]; have {
		b.mu.Unlock()
		return nil // already watched whole; FSEvents needs no re-walking
	}
	b.mu.Unlock()

	dev, err := fsevents.DeviceForPath(root)
	if err != nil {
		return err
	}
	es := &fsevents.EventStream{
		Paths:   []string{root},
		Latency: fseventsLatency,
		Device:  dev,
		// FileEvents reports the file, not just its folder. WatchRoot tells
		// us when the worktree itself is moved or removed.
		Flags: fsevents.FileEvents | fsevents.WatchRoot,
	}
	if err := es.Start(); err != nil {
		return err
	}
	b.mu.Lock()
	b.streams[root] = es
	b.mu.Unlock()
	b.pumps.Add(1)
	go b.pump(root, es)
	return nil
}

func (b *fseventsBackend) unwatch(root string) {
	b.mu.Lock()
	es := b.streams[root]
	delete(b.streams, root)
	b.mu.Unlock()
	if es != nil {
		es.Stop() // closes its Events channel, which ends the pump
	}
}

// close stops every stream and waits for their pumps, so that no
// CoreFoundation thread of ours is left running. It can be called twice: a
// reload closes the watches before exec'ing, and shutdown closes them again.
func (b *fseventsBackend) close() {
	b.closeOnce.Do(func() {
		close(b.stop)
		b.mu.Lock()
		streams := make([]*fsevents.EventStream, 0, len(b.streams))
		for root, es := range b.streams {
			streams = append(streams, es)
			delete(b.streams, root)
		}
		b.mu.Unlock()
		for _, es := range streams {
			es.Stop()
		}
		done := make(chan struct{})
		go func() { b.pumps.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second): // never hold up a shutdown
		}
	})
}

// pump forwards one stream's paths. It reads es.Events until the stream
// closes it and never before: FSEvents delivers batches from a CoreFoundation
// callback, and a callback whose send has nobody to receive it blocks inside
// cgo, on a thread locked to it, for good. A thread stuck in a cgo callback
// also stops syscall.Exec — runtime_BeforeExec waits on darwin for pending
// preemption signals — so abandoning this channel left a server that could
// never reload. Once stopped it keeps draining, dropping what it reads.
func (b *fseventsBackend) pump(root string, es *fsevents.EventStream) {
	defer b.pumps.Done()
	stopped := false
	for batch := range es.Events {
		if !stopped {
			select {
			case <-b.stop:
				stopped = true
			default:
			}
		}
		if stopped {
			continue // drained and dropped: the stream is on its way out
		}
		for _, ev := range batch {
			select {
			case b.out <- fseventsPath(root, ev.Path):
			default: // the reader is behind; the next batch will do
				log.Printf("worktree watch: dropped an event for %s", ev.Path)
			}
		}
	}
}

// fseventsPath makes a stream's path absolute. FSEvents reports paths
// relative to the device root, so the leading separator is missing, and it
// resolves symlinks — /var comes back as /private/var — so a path that no
// longer sits under the worktree we asked about is mapped back onto it.
func fseventsPath(root, p string) string {
	if !strings.HasPrefix(p, string(filepath.Separator)) {
		p = string(filepath.Separator) + p
	}
	p = filepath.Clean(p)
	if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
		return p
	}
	// The stream was opened on root, so anything it reports belongs to it:
	// if the names differ it is because one side is resolved and the other
	// is not. Keep the tail under the root we know.
	if real, err := filepath.EvalSymlinks(root); err == nil && real != root {
		if p == real || strings.HasPrefix(p, real+string(filepath.Separator)) {
			return filepath.Join(root, strings.TrimPrefix(p, real))
		}
	}
	return p
}
