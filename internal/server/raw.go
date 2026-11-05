package server

import (
	"github.com/Amitgb14/conch/internal/proto"
)

// subscribeRaw gives the client the pane's output as the program wrote it
// (pane.raw.v1): the replay is the answer, and each piece after it is an
// EventPaneOutput, in order, until pane.unsubscribe, the client going, or
// the program ending. A client that falls behind is not slowed down to:
// it is sent a fresh replay with Reset, for a terminal cleared first.
//
// It shares the client's subscriptions with frames, by pane, so one
// pane.unsubscribe ends either; a connection takes a pane one way or the
// other, not both.
func (s *Server) subscribeRaw(c *client, e *entry) (proto.PaneRawStart, *proto.Error) {
	p := e.p
	c.mu.Lock()
	if _, ok := c.subs[p.ID()]; ok {
		c.mu.Unlock()
		return proto.PaneRawStart{}, proto.Errorf(proto.ErrBadRequest, "pane %s is already subscribed on this connection", p.ID())
	}
	sub := &subscription{stop: make(chan struct{}), kick: make(chan struct{}, 1)}
	c.subs[p.ID()] = sub
	c.mu.Unlock()
	s.setWatchers(e, +1)

	replay, cols, rows, raw := p.SubscribeRaw()
	go func() {
		defer func() { p.UnsubscribeRaw(raw) }()
		for {
			select {
			case b := <-raw.C:
				if c.conn.Write(proto.Message{Event: proto.EventPaneOutput, Data: proto.Marshal(proto.PaneOutput{ID: p.ID(), Data: b})}) != nil {
					return
				}
				continue
			case <-sub.stop:
				return
			case <-p.Done():
				// What it printed last is still to be sent; the exit
				// itself is broadcast as pane.exited.
				for {
					select {
					case b := <-raw.C:
						if c.conn.Write(proto.Message{Event: proto.EventPaneOutput, Data: proto.Marshal(proto.PaneOutput{ID: p.ID(), Data: b})}) != nil {
							return
						}
					default:
						return
					}
				}
			default:
			}
			if raw.Lost() {
				// Behind: whatever is still queued was sent before the
				// loss, and is overtaken by a fresh replay.
				p.UnsubscribeRaw(raw)
				var again string
				again, cols, rows, raw = p.SubscribeRaw()
				out := proto.PaneOutput{ID: p.ID(), Data: []byte(again), Reset: true, Cols: cols, Rows: rows}
				if c.conn.Write(proto.Message{Event: proto.EventPaneOutput, Data: proto.Marshal(out)}) != nil {
					return
				}
				continue
			}
			select {
			case b := <-raw.C:
				if c.conn.Write(proto.Message{Event: proto.EventPaneOutput, Data: proto.Marshal(proto.PaneOutput{ID: p.ID(), Data: b})}) != nil {
					return
				}
			case <-sub.stop:
				return
			case <-p.Done():
				// Back to the top, which sends what is left.
			case <-raw.LostC():
				// Behind: back to the top, which starts it again.
			case <-sub.kick:
				// pane.scroll means nothing to a raw view.
			}
		}
	}()
	return proto.PaneRawStart{ID: p.ID(), Cols: cols, Rows: rows, Replay: []byte(replay)}, nil
}
