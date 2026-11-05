package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestParsePs(t *testing.T) {
	got := parsePs([]byte("  1     0\n 42     1\nbad line here\n 43 x\n\n 44    42\n"))
	want := map[int]int{1: 0, 42: 1, 44: 42}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestParseLsof(t *testing.T) {
	out := "p100\nf5\nn*:4317\nf6\nn[::1]:4317\nf7\nn127.0.0.1:9000\np200\nf3\nn*:5173\np300\nn*:nope\nn\nnnoport\np0\nn*:80\n"
	got := parseLsof([]byte(out))
	if !slices.Equal(got[100], []int{4317, 9000}) || !slices.Equal(got[200], []int{5173}) || len(got[300]) != 0 || len(got[0]) != 0 {
		t.Fatalf("got %v", got)
	}
	if len(parseLsof(nil)) != 0 {
		t.Fatal("nothing listening should be an empty answer")
	}
}

func TestParseProcNetTCP(t *testing.T) {
	b := []byte(`  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:10DD 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 31337 1 0000000000000000 100 0 0 10 0
   1: 0100007F:1F90 0100007F:D3A2 01 00000000:00000000 00:00000000 00000000  1000        0 31338 1 0000000000000000 20 4 30 10 -1
   2: short
`)
	got := parseProcNetTCP(b)
	if len(got) != 1 || got["31337"] != 4317 {
		t.Fatalf("got %v, want only the listening socket, port 4317", got)
	}
}

func TestProcListening(t *testing.T) {
	proc := t.TempDir()
	os.MkdirAll(filepath.Join(proc, "net"), 0o755)
	os.WriteFile(filepath.Join(proc, "net", "tcp"), []byte("hdr\n 0: 00000000:10DD 00000000:0000 0A 0:0 0:0 0 1000 0 777 1\n"), 0o644)
	os.MkdirAll(filepath.Join(proc, "55", "fd"), 0o755)
	os.Symlink("socket:[777]", filepath.Join(proc, "55", "fd", "3"))
	os.Symlink("/dev/null", filepath.Join(proc, "55", "fd", "0"))
	os.MkdirAll(filepath.Join(proc, "self"), 0o755) // not a pid
	got, err := procListening(proc)
	if err != nil || !slices.Equal(got[55], []int{4317}) {
		t.Fatalf("got %v, %v", got, err)
	}
	// No listening sockets at all: an empty answer, not an error.
	if got, err := procListening(t.TempDir()); err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestPortsUnder(t *testing.T) {
	children := map[int][]int{10: {11, 12}, 12: {13}, 13: {10}} // a loop must not hang
	ports := map[int][]int{11: {9000}, 13: {4317, 9000}, 99: {1}}
	if got := portsUnder(10, children, ports); !slices.Equal(got, []int{4317, 9000}) {
		t.Fatalf("got %v", got)
	}
	if got := portsUnder(99, nil, ports); !slices.Equal(got, []int{1}) {
		t.Fatalf("got %v", got)
	}
	if got := portsUnder(7, children, ports); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestListeningRealLookup(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot listen on localhost here:", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	byPid, err := listeningByPid(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if byPid == nil {
		t.Skip("no way to look on this platform")
	}
	if !slices.Contains(byPid[os.Getpid()], port) {
		t.Fatalf("port %d of this process not found: %v", port, byPid[os.Getpid()])
	}
}

func TestLookListeningTellsClients(t *testing.T) {
	a5IsolateEnv(t)
	s, _ := a5Server(t)
	ev := a5Listen(t, s)
	var info proto.PaneInfo
	res, perr := s.dispatch(&client{subs: map[string]*subscription{}}, proto.Message{Method: proto.MethodPaneCreate,
		Params: proto.Marshal(proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", "sleep 30"}, Cols: 40, Rows: 5})})
	if perr != nil {
		t.Fatal(perr)
	}
	info = res.(proto.PaneInfo)
	e := a5Entry(t, s, info.ID)

	child := info.PID + 100000
	listen := map[int][]int{child: {5173, 4317}}
	oldParents, oldListening := processParents, listeningByPid
	t.Cleanup(func() { processParents, listeningByPid = oldParents, oldListening })
	processParents = func(context.Context) (map[int]int, error) { return map[int]int{child: info.PID, info.PID: 1}, nil }
	listeningByPid = func(context.Context) (map[int][]int, error) { return listen, nil }

	updates := func() []proto.PaneInfo {
		ev.mu.Lock()
		defer ev.mu.Unlock()
		var out []proto.PaneInfo
		for _, m := range ev.msgs {
			var p proto.PaneInfo
			if m.Event == proto.EventPaneUpdated && json.Unmarshal(m.Data, &p) == nil && p.ID == info.ID {
				out = append(out, p)
			}
		}
		return out
	}
	// The pane's first look, run before the ports are found, as it can be
	// under load: it must not swallow the news of them.
	s.reshown(e)
	s.lookListening(context.Background())
	if got := e.info().Listening; !slices.Equal(got, []int{4317, 5173}) {
		t.Fatalf("listening %v, want [4317 5173]", got)
	}
	a5WaitFor(t, "pane.updated with the ports", func() bool {
		u := updates()
		return len(u) > 0 && slices.Equal(u[len(u)-1].Listening, []int{4317, 5173})
	})
	n := len(updates())

	// Nothing changed: nothing new said about the ports. (The pane's own
	// looks at its program may still send an update, with the same ports.)
	s.lookListening(context.Background())
	time.Sleep(50 * time.Millisecond)
	for _, u := range updates()[n:] {
		if !slices.Equal(u.Listening, []int{4317, 5173}) {
			t.Fatalf("ports changed without a change: %v", u.Listening)
		}
	}
	n = len(updates())

	// The server stops listening: said, as no ports.
	listen = map[int][]int{}
	s.lookListening(context.Background())
	a5WaitFor(t, "pane.updated without ports", func() bool {
		u := updates()
		return len(u) > n && len(u[len(u)-1].Listening) == 0
	})

	// A failed look changes nothing.
	listen = map[int][]int{child: {1234}}
	listeningByPid = func(context.Context) (map[int][]int, error) { return nil, errors.New("lsof broke") }
	s.lookListening(context.Background())
	if got := e.info().Listening; len(got) != 0 {
		t.Fatalf("a failed look set %v", got)
	}
}

func TestLookListeningWithNobodyConnected(t *testing.T) {
	a5IsolateEnv(t)
	s, _ := a5Server(t)
	called := false
	oldParents := processParents
	t.Cleanup(func() { processParents = oldParents })
	processParents = func(context.Context) (map[int]int, error) { called = true; return nil, nil }
	s.lookListening(context.Background())
	if called {
		t.Fatal("looked with no client to tell")
	}
}
