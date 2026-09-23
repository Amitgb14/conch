//go:build darwin && cgo

package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsevents"
)

// TestFSEventsPath: FSEvents reports paths relative to the device root, so
// without a leading separator, and resolves symlinks on the way — /var comes
// back as /private/var. Both have to be undone, or nothing matches the
// worktree the stream was opened on and every event is dropped.
func TestFSEventsPath(t *testing.T) {
	for _, tc := range []struct {
		name, root, in, want string
	}{
		{"the missing leading separator", "/src/api", "src/api/main.go", "/src/api/main.go"},
		{"already absolute", "/src/api", "/src/api/main.go", "/src/api/main.go"},
		{"the worktree itself", "/src/api", "src/api", "/src/api"},
		{"a path it cleans", "/src/api", "src/api/./sub//main.go", "/src/api/sub/main.go"},
		{"outside any root is left alone", "/src/api", "other/place.go", "/other/place.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fseventsPath(tc.root, tc.in); got != tc.want {
				t.Fatalf("fseventsPath(%q, %q) = %q, want %q", tc.root, tc.in, got, tc.want)
			}
		})
	}
}

// TestFSEventsPathThroughASymlink is the case that matters on macOS: the
// temporary directories every test uses are reached through /var, which is a
// link to /private/var, and the stream reports the resolved side.
func TestFSEventsPathThroughASymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == link {
		t.Skip("nothing was resolved, so there is no case to test")
	}

	// A stream opened on the link reports the resolved path; it has to come
	// back under the link, which is the root the watcher knows.
	got := fseventsPath(link, resolved[1:]+"/src/main.go")
	want := filepath.Join(link, "src", "main.go")
	if got != want {
		t.Fatalf("fseventsPath(%q, resolved…) = %q, want %q", link, got, want)
	}
	// The root itself maps back too.
	if got := fseventsPath(link, resolved[1:]); got != link {
		t.Fatalf("the root came back as %q, want %q", got, link)
	}
	// A path already under the link is left as it is.
	under := filepath.Join(link, "a.go")
	if got := fseventsPath(link, under); got != under {
		t.Fatalf("a path under the root became %q", got)
	}
}

// FSEvents hands batches over from a CoreFoundation callback, and a send
// with nobody receiving blocks that callback inside cgo for good — which
// also stops the process ever exec'ing itself again, so a reload hangs. The
// pump must therefore read the stream's channel until the stream closes it,
// stopped or not.
func TestFSEventsPumpDrainsAfterStop(t *testing.T) {
	events := make(chan []fsevents.Event)
	b := &fseventsBackend{out: make(chan string, 1), stop: make(chan struct{}), streams: map[string]*fsevents.EventStream{}}
	es := &fsevents.EventStream{Events: events}
	done := make(chan struct{})
	b.pumps.Add(1) // as watch() does before starting one
	go func() { b.pump("/w", es); close(done) }()

	// Stopped: the batch is still taken, not left blocking the callback.
	close(b.stop)
	for i := 0; i < 3; i++ {
		select {
		case events <- []fsevents.Event{{Path: "w/a.go"}}:
		case <-time.After(5 * time.Second):
			t.Fatalf("batch %d was not taken: a callback would be stuck in cgo", i+1)
		}
	}
	// Nothing is forwarded once stopped.
	select {
	case p := <-b.out:
		t.Fatalf("forwarded %q after stopping", p)
	default:
	}
	// The pump ends only when the stream closes its channel, as Stop does.
	select {
	case <-done:
		t.Fatal("the pump left the channel before the stream closed it")
	default:
	}
	close(events)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the pump did not end with the stream")
	}
}

// A reader that has stopped listening must not block the callback either:
// batches keep being taken and their paths dropped.
func TestFSEventsPumpDropsWhenTheReaderIsBehind(t *testing.T) {
	events := make(chan []fsevents.Event)
	b := &fseventsBackend{out: make(chan string), stop: make(chan struct{}), streams: map[string]*fsevents.EventStream{}}
	es := &fsevents.EventStream{Events: events}
	done := make(chan struct{})
	b.pumps.Add(1) // as watch() does before starting one
	go func() { b.pump("/w", es); close(done) }()
	t.Cleanup(func() { close(events); <-done })

	for i := 0; i < 3; i++ { // nobody reads b.out at all
		select {
		case events <- []fsevents.Event{{Path: "w/a.go"}, {Path: "w/b.go"}}:
		case <-time.After(5 * time.Second):
			t.Fatalf("batch %d was not taken with no reader on the other side", i+1)
		}
	}
}
