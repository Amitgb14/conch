package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// conch server heap against a real server: the memory said, the files
// written and named, and the commands to read them.
func TestServerHeapCommand(t *testing.T) {
	dir := a4Env(t)
	startA4RealServer(t, dir)
	out, _, err := a4Out(t, func() error { return runServer([]string{"heap"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"live in", "goroutines", "panes", "heap profile:  " + dir + "/debug/heap-",
		"goroutines:    " + dir + "/debug/goroutines-", "go tool pprof -top -sample_index=inuse_space ", "-base OLDER.pb.gz"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	ents, _ := os.ReadDir(dir + "/debug")
	if len(ents) != 2 {
		t.Errorf("debug folder holds %d files", len(ents))
	}

	out, _, err = a4Out(t, func() error { return runServer([]string{"heap", "-json"}) })
	if err != nil || !strings.Contains(out, `"heap_alloc"`) || !strings.Contains(out, `"goroutines": "`+dir) {
		t.Fatalf("-json: %v\n%s", err, out)
	}
	if _, _, err := a4Out(t, func() error { return runServer([]string{"heap", "extra"}) }); err == nil || !strings.Contains(err.Error(), "no arguments") {
		t.Errorf("an argument: %v", err)
	}
}

// An older server is told to reload, and nothing is asked of it.
func TestServerHeapOlderServer(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, os.Getenv("CONCH_SOCKET"))
	srv.Hello = func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = []string{"pane.v1"}
		return h
	}
	_, _, err := a4Out(t, func() error { return runServer([]string{"heap"}) })
	if err == nil || !strings.Contains(err.Error(), "predates heap snapshots") || !strings.Contains(err.Error(), "conch server reload") {
		t.Fatalf("err %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, c := range srv.calls {
		if c.Method == proto.MethodServerHeap {
			t.Error("server.heap asked of a server without it")
		}
	}
}

// No server: nothing to look at, and said so.
func TestServerHeapNoServer(t *testing.T) {
	a4Env(t)
	_, _, err := a4Out(t, func() error { return runServer([]string{"heap"}) })
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("err %v", err)
	}
}

func TestHeapWords(t *testing.T) {
	h := proto.ServerHeap{At: time.Now(), Heap: "/h/debug/heap-1.pb.gz", Goroutines: "/h/debug/goroutines-1.txt",
		HeapAlloc: 312 << 20, HeapInuse: 330 << 20, HeapObjects: 1_234_567, Sys: 540 << 20, Goroutine: 812, Panes: 47}
	got := heapWords(h, 10736, "")
	if !strings.HasPrefix(got, "server pid 10736: 312 MB live in 1.2M objects · heap 330 MB · 540 MB from the system · 812 goroutines · 47 panes\n") ||
		strings.Contains(got, "(on ") {
		t.Errorf("%s", got)
	}
	if got := heapWords(h, 1, "busybox"); !strings.Contains(got, "heap profile (on busybox):") || !strings.Contains(got, "goroutines (on busybox):") {
		t.Errorf("another machine's files not said to be there: %s", got)
	}
	if got := heapWords(h, 1, "local"); strings.Contains(got, "(on ") {
		t.Errorf("local named: %s", got)
	}
	for n, want := range map[uint64]string{0: "0 B", 1023: "1023 B", 1024: "1 KB", 1<<20 - 1: "1024 KB", 1 << 20: "1 MB", 1<<30 - 1: "1024 MB", 1 << 30: "1.0 GB", 3 << 30: "3.0 GB"} {
		if got := bytesWords(n); got != want {
			t.Errorf("bytesWords(%d) = %q, want %q", n, got, want)
		}
	}
	for n, want := range map[uint64]string{0: "0", 9999: "9999", 10_000: "10K", 999_999: "1000K", 1_000_000: "1.0M"} {
		if got := countWords(n); got != want {
			t.Errorf("countWords(%d) = %q, want %q", n, got, want)
		}
	}
}
