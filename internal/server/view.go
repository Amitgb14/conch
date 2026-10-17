package server

import (
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// What a TUI is showing is the TUI's, not the server's: the server owns
// the panes and knows nothing of tabs, splits or which one has the
// keyboard. But an agent in a pane can only reach the server, so for it
// to see the screen — or ask for something to be put in front of the
// person — the server carries both ways. A TUI publishes its layout
// (view.state), anything may read the last one (view.get), and a request
// to show panes together (view.show) is passed to every client as an
// event for the TUIs to act on.
//
// It is a request and not an order on purpose. A client showing another
// machine, or a phone with one pane on screen, does what it can and the
// person's own arrangement is never taken apart underneath them.

// viewBook keeps the last view each client published, newest first when
// asked. A client that goes away takes its view with it: a layout nobody
// is looking at is not an answer to "what is on screen".
type viewBook struct {
	mu    sync.Mutex
	views map[*client]proto.ViewState
}

func newViewBook() *viewBook { return &viewBook{views: map[*client]proto.ViewState{}} }

// put records what c is showing. An empty state (no tabs) is kept as
// such: a TUI with nothing open is a true answer.
func (b *viewBook) put(c *client, v proto.ViewState, now time.Time) {
	v.At = now
	b.mu.Lock()
	defer b.mu.Unlock()
	b.views[c] = v
}

// forget drops a client's view when it disconnects.
func (b *viewBook) forget(c *client) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.views, c)
}

// latest is the most recently published view, and whether there is one.
// With a terminal and a phone both connected the newest wins, which is
// the screen somebody was last looking at.
func (b *viewBook) latest() (proto.ViewState, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var best proto.ViewState
	found := false
	for _, v := range b.views {
		if !found || v.At.After(best.At) {
			best, found = v, true
		}
	}
	return best, found
}

// viewState records a client's layout.
func (s *Server) viewState(c *client, v proto.ViewState) {
	s.views.put(c, v, time.Now())
}

// viewGet answers with the newest layout a client published. Nothing
// published means no TUI is connected, which is a different answer from
// an empty one, so Tabs is nil and nothing pretends otherwise.
func (s *Server) viewGet() proto.ViewState {
	v, _ := s.views.latest()
	return v
}

// viewShow passes a request to show panes together to every client. The
// panes are checked here so an agent asking for one that does not exist
// is told, rather than finding out from a TUI that quietly did nothing.
func (s *Server) viewShow(p proto.ViewShowParams) (proto.ViewShowResult, *proto.Error) {
	if len(p.Panes) == 0 {
		return proto.ViewShowResult{}, proto.Errorf(proto.ErrBadRequest, "name at least one pane to show")
	}
	seen := map[string]bool{}
	panes := make([]string, 0, len(p.Panes))
	for _, id := range p.Panes {
		if seen[id] {
			continue // asking twice for the same pane is one split, not two
		}
		if _, err := s.get(id); err != nil {
			return proto.ViewShowResult{}, err
		}
		seen[id] = true
		panes = append(panes, id)
	}
	p.Panes = panes
	s.broadcast(proto.EventViewShow, p)
	return proto.ViewShowResult{Panes: panes}, nil
}
