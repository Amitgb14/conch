package server_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/server"
)

// a8Server starts a server on sock and waits for it to answer.
func a8Server(t *testing.T, sock, dir string) *server.Server {
	t.Helper()
	srv := server.New(sock, dir)
	go srv.Run()
	for i := 0; i < 100; i++ {
		if c, err := client.Dial(sock, "test"); err == nil {
			c.Close()
			return srv
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server on %s never answered", sock)
	return nil
}

func a8Dir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "a8") // short: macOS caps socket paths
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// A server that stops takes its own socket file with it.
func TestA8StopRemovesItsOwnSocket(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	dir := a8Dir(t)
	sock := filepath.Join(dir, "s.sock")
	srv := a8Server(t, sock, dir)
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("no socket while running: %v", err)
	}
	srv.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(sock); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the socket file outlived its server")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The one that bit a real session: a server stopping after another has taken
// over the same path must not delete the new server's socket.
func TestA8StopKeepsAnotherServersSocket(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	dir := a8Dir(t)
	sock := filepath.Join(dir, "s.sock")

	old := a8Server(t, sock, dir)
	// What recovery does when a server stops answering: the path it left is
	// removed so a new server can bind one of its own there.
	if err := os.Remove(sock); err != nil {
		t.Fatal(err)
	}
	newer := a8Server(t, sock, a8Dir(t))
	t.Cleanup(func() { newer.Stop() })

	old.Stop()
	time.Sleep(300 * time.Millisecond) // let the old one finish shutting down

	st, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("the newer server's socket was deleted: %v", err)
	}
	if st.Mode()&os.ModeSocket == 0 {
		t.Fatalf("%s is no longer a socket: %v", sock, st.Mode())
	}
	c, err := client.Dial(sock, "test")
	if err != nil {
		t.Fatalf("the newer server is unreachable after the old one stopped: %v", err)
	}
	c.Close()
}

// Shutting down when the socket file is already gone is quiet.
func TestA8StopWithoutItsSocket(t *testing.T) {
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	dir := a8Dir(t)
	sock := filepath.Join(dir, "s.sock")
	srv := a8Server(t, sock, dir)
	if err := os.Remove(sock); err != nil {
		t.Fatal(err)
	}
	srv.Stop()
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket came back: %v", err)
	}
}

// A reload on a long-running server once hung in syscall.Exec itself —
// runtime_BeforeExec on darwin waits for pending preemption signals — so the
// server neither came back nor kept serving, and conch could not be started
// again until the socket was cleared by hand. It did not reproduce in a
// harness with projects, FSEvents watches and panes; see row 9.34 in
// docs/testing/end-to-end.md.
func TestA8ReloadAlwaysComesBack(t *testing.T) {
	t.Skip("bug: a reload wedged in syscall.Exec (runtime_BeforeExec) on a real long-running server; not reproduced here")
}
