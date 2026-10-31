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
// pane, which goes back to the last resize winning.
func (z *paneSizes) forget(c *client) {
	z.mu.Lock()
	defer z.mu.Unlock()
	for pane, ws := range z.want {
		delete(ws, c)
		if len(ws) == 0 {
			delete(z.want, pane)
		}
	}
	for pane, by := range z.by {
		if by == c {
			delete(z.by, pane)
		}
	}
}

// drop forgets a pane that has closed.
func (z *paneSizes) drop(pane string) {
	z.mu.Lock()
	defer z.mu.Unlock()
	delete(z.want, pane)
	delete(z.by, pane)
}
