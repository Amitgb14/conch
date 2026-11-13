package fleet

import (
	"errors"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/remote"
)

// A machine whose server is older than this conch is reached all the
// same: remote.Connect hands back a working client with the error, and the
// pool keeps it — dropped, it was never closed, and each retry left
// another ssh bridge running while the machine showed as offline.
func TestOutdatedServerIsKeptNotLeaked(t *testing.T) {
	r := newRig(t)
	r.mu.Lock()
	r.alongside["old"] = &remote.OutdatedServerError{Missing: []string{"x.v1"}}
	r.alongside["odd"] = errors.New("connected, but something else went wrong")
	r.mu.Unlock()
	r.set(remote.Machine{ID: "old", Label: "old", Target: startServer(t), Enabled: true},
		remote.Machine{ID: "odd", Label: "odd", Target: startServer(t), Enabled: true})

	old := r.until(t, "old", state(Online))
	if old.Client() == nil || old.Client().Err() != nil {
		t.Fatalf("the outdated machine's client: %+v", old)
	}
	r.until(t, "odd", state(Offline))

	// Retries come and go; nothing is left open but the one in use.
	for range 3 {
		r.clock.add(time.Hour)
		r.pool.Machine("old")
		r.pool.Machine("odd")
		r.until(t, "odd", state(Offline))
	}
	c, err := r.pool.Dial("old")
	if err != nil || c == nil {
		t.Fatalf("dial the outdated machine: %v", err)
	}
	c.Close()

	r.mu.Lock()
	made := append(r.made[:0:0], r.made...)
	r.mu.Unlock()
	open := 0
	for _, c := range made {
		if c.Err() == nil {
			open++
		}
	}
	if open != 1 {
		t.Fatalf("%d of %d clients left open, want only the pool's own for old", open, len(made))
	}
}
