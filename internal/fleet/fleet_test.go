package fleet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/server"
)

// startServer runs a conch server on a short socket path (macOS caps them
// near 104 bytes) and returns the path.
func startServer(t *testing.T) string {
	t.Helper()
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	dir, err := os.MkdirTemp("", "fl")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Setenv("HOME", dir)
	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	t.Cleanup(func() { srv.Stop(); time.Sleep(50 * time.Millisecond); os.RemoveAll(dir) })
	for range 100 {
		if c, err := client.Dial(sock, "probe"); err == nil {
			c.Close()
			return sock
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return ""
}

// clock is a time a test moves by hand.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// rig is a pool over one real local server and a catalog the test sets,
// whose machines are reached by dialling the socket each one names in its
// Target — or failing with the error the test gives it.
type rig struct {
	pool    *Pool
	local   *client.Client
	clock   *clock
	mu      sync.Mutex
	saved   []remote.Machine
	failing map[string]error
	block   chan struct{} // when set, connecting waits for it
	tries   atomic.Int32
	changed atomic.Int32
}

// release opens the gate set in block, once.
func (r *rig) release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.block != nil {
		close(r.block)
		r.block = nil
	}
}

func newRig(t *testing.T) *rig {
	t.Helper()
	sock := startServer(t)
	local, err := client.Dial(sock, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })
	r := &rig{local: local, clock: &clock{t: time.Unix(1_000_000, 0)}, failing: map[string]error{}}
	r.pool = New(Options{
		Local:     func() (*client.Client, error) { return local, nil },
		DialLocal: func() (*client.Client, error) { return client.Dial(sock, "own") },
		Connect: func(ctx context.Context, m remote.Machine) (*client.Client, error) {
			r.tries.Add(1)
			r.mu.Lock()
			fail, block := r.failing[m.ID], r.block
			r.mu.Unlock()
			if block != nil {
				select {
				case <-block:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if fail != nil {
				return nil, fail
			}
			return client.Dial(m.Target, "fleet-test")
		},
		Catalog: func() ([]remote.Machine, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.saved == nil {
				return nil, errors.New("machines.json is nonsense")
			}
			return append([]remote.Machine(nil), r.saved...), nil
		},
		Now:     r.clock.now,
		Changed: func() { r.changed.Add(1) },
	})
	t.Cleanup(r.pool.Close)
	// Cleanups run last first: the gate opens before Close waits on an
	// attempt held at it, so a test that fails holding it does not hang.
	t.Cleanup(r.release)
	return r
}

// set is the catalog; set() with nothing is an empty one, not a failing one.
func (r *rig) set(saved ...remote.Machine) {
	r.mu.Lock()
	r.saved = append([]remote.Machine{}, saved...)
	r.mu.Unlock()
}

// until polls the pool until ok holds for machine id.
func (r *rig) until(t *testing.T, id string, ok func(Machine) bool) Machine {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if m, found := r.pool.Machine(id); found && ok(m) {
			return m
		}
		if time.Now().After(deadline) {
			m, _ := r.pool.Machine(id)
			t.Fatalf("%s never got there: %+v", id, m)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func state(s string) func(Machine) bool { return func(m Machine) bool { return m.State == s } }

func TestLocalFirstAndOthersByLabel(t *testing.T) {
	r := newRig(t)
	a, b := startServer(t), startServer(t)
	r.set(
		remote.Machine{ID: "zed", Label: "zed box", Target: a, Enabled: true},
		remote.Machine{ID: "off", Label: "a disabled one", Target: a},
		remote.Machine{ID: "alpha", Label: "alpha box", Target: b, Enabled: true},
		remote.Machine{ID: "alpha", Label: "alpha again", Target: b, Enabled: true}, // a duplicate ID
		remote.Machine{ID: Local, Label: "a second local", Target: b, Enabled: true},
	)
	r.until(t, "zed", state(Online))
	r.until(t, "alpha", state(Online))
	var ids []string
	for _, m := range r.pool.Machines() {
		ids = append(ids, m.ID+"="+m.Label)
	}
	if got := strings.Join(ids, ","); got != "local=this computer,alpha=alpha box,zed=zed box" {
		t.Fatalf("machines %s", got)
	}
	if m, _ := r.pool.Machine(Local); m.Client() != r.local || m.State != Online {
		t.Fatalf("local %+v", m)
	}
	if r.changed.Load() < 2 {
		t.Fatalf("Changed told %d times for two machines coming up", r.changed.Load())
	}
}

func TestUnreadableCatalogLeavesThisComputer(t *testing.T) {
	r := newRig(t) // saved is nil: the catalog fails
	ms := r.pool.Machines()
	if len(ms) != 1 || ms[0].ID != Local || ms[0].State != Online {
		t.Fatalf("machines %+v", ms)
	}
}

func TestLocalDown(t *testing.T) {
	p := New(Options{Catalog: func() ([]remote.Machine, error) { return nil, nil }})
	defer p.Close()
	ms := p.Machines()
	if len(ms) != 1 || ms[0].State != Offline || ms[0].Client() != nil || ms[0].Detail == "" {
		t.Fatalf("machines %+v", ms)
	}
}

func TestFailureCarriesOneLineAndRetriesLater(t *testing.T) {
	r := newRig(t)
	r.failing["box"] = errors.New("ssh: connect to host box port 22: Connection refused\nmore detail ssh gives")
	r.set(remote.Machine{ID: "box", Label: "box", Target: startServer(t), Enabled: true})
	m := r.until(t, "box", state(Offline))
	if m.Detail != "ssh: connect to host box port 22: Connection refused" || m.Client() != nil {
		t.Fatalf("offline %+v", m)
	}
	// Looking again before the retry is due does not try again.
	tries := r.tries.Load()
	r.pool.Machines()
	r.clock.add(29 * time.Second)
	r.pool.Machines()
	if r.tries.Load() != tries {
		t.Fatalf("tried again before the retry was due")
	}
	// Once it is due, and the machine answers, it comes up.
	r.mu.Lock()
	delete(r.failing, "box")
	r.mu.Unlock()
	r.clock.add(2 * time.Second)
	r.until(t, "box", state(Online))
}

func TestConnectingWhileAnAttemptRuns(t *testing.T) {
	r := newRig(t)
	r.block = make(chan struct{})
	r.set(remote.Machine{ID: "box", Label: "box", Target: startServer(t), Enabled: true})
	r.until(t, "box", state(Connecting))
	// Connecting is said before the attempt's goroutine runs; wait for it.
	for deadline := time.Now().Add(5 * time.Second); r.tries.Load() == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no attempt was made")
		}
	}
	r.pool.Machines()
	r.pool.Machines()
	time.Sleep(20 * time.Millisecond)
	if n := r.tries.Load(); n != 1 {
		t.Fatalf("%d attempts while one was running", n)
	}
	r.release()
	r.until(t, "box", state(Online))
}

func TestGoneFromCatalogClosesItsClient(t *testing.T) {
	r := newRig(t)
	r.set(remote.Machine{ID: "box", Label: "box", Target: startServer(t), Enabled: true})
	c := r.until(t, "box", state(Online)).Client()
	r.set()
	if _, ok := r.pool.Machine("box"); ok {
		t.Fatal("a machine gone from the catalog is still listed")
	}
	deadline := time.Now().Add(5 * time.Second)
	for c.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("its client was left open")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCloseWhileConnectingLeavesNothingOpen(t *testing.T) {
	r := newRig(t)
	r.block = make(chan struct{})
	r.set(remote.Machine{ID: "box", Label: "box", Target: startServer(t), Enabled: true})
	r.until(t, "box", state(Connecting))
	done := make(chan struct{})
	go func() { r.pool.Close(); close(done) }()
	time.Sleep(20 * time.Millisecond)
	r.release() // the attempt finishes after Close began
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not wait for the attempt and return")
	}
	if r.local.Err() != nil {
		t.Fatal("Close closed this computer's client, which is the caller's")
	}
	// A closed pool starts nothing new.
	tries := r.tries.Load()
	r.clock.add(time.Hour)
	r.pool.Machines()
	time.Sleep(50 * time.Millisecond)
	if r.tries.Load() != tries {
		t.Fatal("a closed pool reached for a machine")
	}
}

func TestDial(t *testing.T) {
	r := newRig(t)
	r.set(remote.Machine{ID: "box", Label: "box", Target: startServer(t), Enabled: true})
	own, err := r.pool.Dial(Local)
	if err != nil || own == r.local {
		t.Fatalf("local: %v (shared client handed out: %v)", err, own == r.local)
	}
	own.Close()
	if _, err := r.pool.Dial("nobody"); err == nil {
		t.Fatal("dialled a machine that is not in the catalog")
	}
	r.mu.Lock()
	r.block = make(chan struct{})
	r.mu.Unlock()
	r.pool.Machines()
	if _, err := r.pool.Dial("box"); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("a machine still connecting: %v", err)
	}
	r.release()
	shared := r.until(t, "box", state(Online)).Client()
	c, err := r.pool.Dial("box")
	if err != nil || c == shared {
		t.Fatalf("box: %v (shared client handed out: %v)", err, c == shared)
	}
	c.Close()
}

func TestNoteDropsADeadClient(t *testing.T) {
	r := newRig(t)
	r.set(remote.Machine{ID: "box", Label: "box", Target: startServer(t), Enabled: true})
	c := r.until(t, "box", state(Online)).Client()
	c.Close()
	r.pool.Note("box", errors.New("list failed\nwith more"))
	r.pool.Note("nobody", errors.New("ignored")) // no such machine: nothing to do
	r.mu.Lock()
	r.failing["box"] = errors.New("still down")
	r.mu.Unlock()
	r.clock.add(time.Hour)
	m := r.until(t, "box", state(Offline))
	if m.Client() != nil {
		t.Fatalf("a closed client is still handed out: %+v", m)
	}
}

func TestPaneIDs(t *testing.T) {
	if got := ComposePaneID("box", "p3"); got != "box:p3" {
		t.Fatal(got)
	}
	for ref, want := range map[string][2]string{
		"p3":       {Local, "p3"},
		"local:p3": {Local, "p3"},
		"box-2:p9": {"box-2", "p9"},
	} {
		m, p, err := SplitPaneID(ref)
		if err != nil || m != want[0] || p != want[1] {
			t.Errorf("%q: %s %s %v", ref, m, p, err)
		}
	}
	for _, ref := range []string{"", ":p3", "Box:p3", "box:", "box:x", "a:b:p3", "box;rm:p3", "box:p3 "} {
		if _, _, err := SplitPaneID(ref); err == nil {
			t.Errorf("%q: accepted", ref)
		}
	}
	if MachineOf("box:p3") != "box" || MachineOf("p3") != Local {
		t.Fatal("MachineOf")
	}
	for id, ok := range map[string]bool{"box": true, "box-2": true, "": false, "Box": false, "a:b": false, "a b": false, "é": false} {
		if IDOK(id) != ok {
			t.Errorf("IDOK(%q) = %v", id, !ok)
		}
	}
	if FirstLine("  one\ntwo") != "one" || FirstLine("") != "" || FirstLine("x") != "x" {
		t.Fatal("FirstLine")
	}
}
