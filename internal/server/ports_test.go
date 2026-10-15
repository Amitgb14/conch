package server

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// stubPorts makes every block free and every worktree exist, except those
// the test says otherwise about.
func stubPorts(t *testing.T, busy map[int]bool, gone map[string]bool) {
	t.Helper()
	free, exists := portsFree, portExists
	portsFree = func(base int) bool { return !busy[base] }
	portExists = func(path string) bool { return !gone[path] }
	t.Cleanup(func() { portsFree, portExists = free, exists })
}

func TestPortBookGivesEachWorktreeItsOwnBlock(t *testing.T) {
	stubPorts(t, nil, nil)
	b := loadPortBook(t.TempDir())
	a, c := b.assign("/w/a"), b.assign("/w/c")
	if a != portFirst || c != portFirst+proto.PortBlock {
		t.Fatalf("blocks %d and %d, want %d and %d", a, c, portFirst, portFirst+proto.PortBlock)
	}
	if again := b.assign("/w/a"); again != a {
		t.Fatalf("second ask gave %d, want the same %d", again, a)
	}
	if b.of("/w/a") != a || b.of("/w/none") != 0 {
		t.Fatal("of disagrees with assign")
	}
	if b.assign("") != 0 {
		t.Fatal("an empty path was given ports")
	}
}

func TestPortBookSkipsBlocksInUse(t *testing.T) {
	stubPorts(t, map[int]bool{portFirst: true, portFirst + 10: true}, nil)
	b := loadPortBook(t.TempDir())
	if got := b.assign("/w/a"); got != portFirst+20 {
		t.Fatalf("got %d, want the first free block %d", got, portFirst+20)
	}
}

func TestPortBookTakesBackBlocksOfWorktreesGone(t *testing.T) {
	gone := map[string]bool{}
	stubPorts(t, nil, gone)
	b := loadPortBook(t.TempDir())
	first := b.assign("/w/old")
	gone["/w/old"] = true
	if got := b.assign("/w/new"); got != first {
		t.Fatalf("got %d, want the removed worktree's %d back", got, first)
	}
	if b.of("/w/old") != 0 {
		t.Fatal("a removed worktree kept its block")
	}
}

func TestPortBookRelease(t *testing.T) {
	stubPorts(t, nil, nil)
	b := loadPortBook(t.TempDir())
	first := b.assign("/w/a")
	b.release("/w/a")
	b.release("/w/never") // nothing to do
	if b.of("/w/a") != 0 {
		t.Fatal("release kept the block")
	}
	if got := b.assign("/w/b"); got != first {
		t.Fatalf("got %d, want the released %d", got, first)
	}
	var nilBook *portBook // a server built without one
	if nilBook.of("x") != 0 || nilBook.assign("x") != 0 {
		t.Fatal("a nil book gave ports")
	}
	nilBook.release("x")
}

func TestPortBookRunsOut(t *testing.T) {
	busy := map[int]bool{}
	for base := portFirst; base <= portLast; base += proto.PortBlock {
		busy[base] = true
	}
	stubPorts(t, busy, nil)
	b := loadPortBook(t.TempDir())
	if got := b.assign("/w/a"); got != 0 {
		t.Fatalf("got %d with every block in use", got)
	}
	busy[portLast] = false // the very last block
	if got := b.assign("/w/a"); got != portLast {
		t.Fatalf("got %d, want the last block %d", got, portLast)
	}
}

func TestPortBookPersists(t *testing.T) {
	stubPorts(t, nil, nil)
	dir := t.TempDir()
	b := loadPortBook(dir)
	a := b.assign("/w/a")
	again := loadPortBook(dir)
	if again.of("/w/a") != a {
		t.Fatalf("after loading, /w/a holds %d, want %d", again.of("/w/a"), a)
	}
	if got := again.assign("/w/b"); got == a {
		t.Fatal("a loaded book handed out a block already held")
	}
}

func TestPortBookLoadsBadFiles(t *testing.T) {
	stubPorts(t, nil, nil)
	for name, body := range map[string]string{
		"truncated": `{"worktrees": {"/w/a": 41`,
		"not json":  "ports!",
		"empty":     "",
		// Off the grid, outside the range, negative: dropped, not trusted.
		"bad bases": `{"worktrees": {"/w/a": 41005, "/w/b": 80, "/w/c": -10, "/w/d": 41020}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "ports.json"), []byte(body), 0o600)
			b := loadPortBook(dir)
			var held []string
			for p := range b.bases {
				held = append(held, p)
			}
			if name == "bad bases" {
				if !slices.Equal(held, []string{"/w/d"}) || b.of("/w/d") != 41020 {
					t.Fatalf("kept %v", b.bases)
				}
				return
			}
			if len(held) != 0 {
				t.Fatalf("kept %v from a bad file", b.bases)
			}
			if b.assign("/w/x") != portFirst {
				t.Fatal("a book loaded from a bad file cannot assign")
			}
		})
	}
}

func TestPortEnv(t *testing.T) {
	if portEnv(0) != nil {
		t.Fatal("no block should set nothing")
	}
	got := portEnv(41010)
	if !slices.Equal(got, []string{"CONCH_PORT=41010", "CONCH_PORT_COUNT=10"}) {
		t.Fatalf("env %v", got)
	}
}

func TestPortsFreeSeesAListener(t *testing.T) {
	// The real check, against a port this test holds.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot listen here")
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if portsFree(port) {
		t.Fatalf("port %d is in use but was called free", port)
	}
}
