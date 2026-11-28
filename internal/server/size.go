package server

import "sync"

// paneSizes decides a pane's size when more than one client shows it. The
// TUI sizes every pane it draws to the space it has, a phone to its
// screen; with two of them showing one pane
// at once they would trade sizes on every redraw. So each client's size
// is kept, and the one used is that of the client the person last typed
// into — tmux's window-size latest. Until anyone has, the last resize
// wins, as it always did.
type paneSizes struct {
	mu   sync.Mutex
	want map[string]map[*client][2]int // pane → client → the size it asked for
	by   map[string]*client            // pane → the client that last sent it input
	// when is the order clients asked in (pane → client → a count), so a
	// pane let go of goes back to the one that asked most recently.
	when map[string]map[*client]uint64
	n    uint64
}

// handBack is a pane let go of by the client that held it, and the size
// of the one it goes back to.
type handBack struct {
	pane string
	size [2]int
}

// ask records c's size for a pane and says whether to apply it now.
func (z *paneSizes) ask(c *client, pane string, cols, rows int) bool {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.want == nil {
		z.want, z.by = map[string]map[*client][2]int{}, map[string]*client{}
	}
	if z.want[pane] == nil {
		z.want[pane] = map[*client][2]int{}
	}
	z.want[pane][c] = [2]int{cols, rows}
	if z.when == nil {
		z.when = map[string]map[*client]uint64{}
	}
	if z.when[pane] == nil {
		z.when[pane] = map[*client]uint64{}
	}
	z.n++
	z.when[pane][c] = z.n
	by := z.by[pane]
	return by == nil || by == c
}

// input notes that the person typed into a pane through c, and returns
// c's size for it to apply, if c ever asked for one.
func (z *paneSizes) input(c *client, pane string) ([2]int, bool) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.by == nil {
		z.want, z.by = map[string]map[*client][2]int{}, map[string]*client{}
	}
	z.by[pane] = c
	size, ok := z.want[pane][c]
	return size, ok
}

// forget drops a client that has gone: its sizes, and its claim on any
// pane, which goes back to the client that asked most recently — the
// laptop after the phone is put away, rather than the phone's size kept
// until somebody types at the laptop again.
func (z *paneSizes) forget(c *client) []handBack {
	z.mu.Lock()
	defer z.mu.Unlock()
	var back []handBack
	for pane := range z.want {
		if hb, ok := z.releaseLocked(c, pane); ok {
			back = append(back, hb)
		}
	}
	for pane, by := range z.by {
		if by == c {
			delete(z.by, pane)
		}
	}
	return back
}

// release drops c's size for one pane it has stopped showing, and says
// what the pane goes back to if c held it.
func (z *paneSizes) release(c *client, pane string) (handBack, bool) {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.releaseLocked(c, pane)
}

func (z *paneSizes) releaseLocked(c *client, pane string) (handBack, bool) {
	ws := z.want[pane]
	if _, asked := ws[c]; !asked && z.by[pane] != c {
		return handBack{}, false
	}
	held := z.by[pane] == c || z.by[pane] == nil
	delete(ws, c)
	delete(z.when[pane], c)
	if z.by[pane] == c {
		delete(z.by, pane)
	}
	if len(ws) == 0 {
		delete(z.want, pane)
		delete(z.when, pane)
		return handBack{}, false
	}
	if !held {
		return handBack{}, false // another client has it: nothing moves
	}
	var next *client
	var at uint64
	for other, n := range z.when[pane] {
		if next == nil || n > at {
			next, at = other, n
		}
	}
	if next == nil {
		return handBack{}, false
	}
	z.by[pane] = next
	return handBack{pane: pane, size: ws[next]}, true
}

// drop forgets a pane that has closed.
func (z *paneSizes) drop(pane string) {
	z.mu.Lock()
	defer z.mu.Unlock()
	delete(z.want, pane)
	delete(z.by, pane)
	delete(z.when, pane)
}
