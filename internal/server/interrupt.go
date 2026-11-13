package server

import (
	"time"

	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/proto"
)

// ---- agent.interrupt ----

// interruptGap is the pause after each interrupt key, with nothing else
// typed into the pane: long enough for the agent to read a lone escape as
// the escape key rather than the start of a sequence, short enough that
// a second escape still counts as "again".
var interruptGap = 120 * time.Millisecond

// After an interrupt the agent is looked at this often, for this long or
// until it stops working, rather than at the next tick: a chat's
// "Stopping…" ends when pane.updated says so.
var (
	interruptLookEvery = 100 * time.Millisecond
	interruptLookFor   = 3 * time.Second
)

// interruptAgent sends the agent in a pane its interrupt key — or ctrl+c
// with Hard — and notes the interrupt, so the hook's "working" (no hook
// fires on an interrupt) gives way once the screen stops showing work.
func (s *Server) interruptAgent(p proto.AgentInterruptParams) (proto.AgentInterruptResult, *proto.Error) {
	e, perr := s.get(p.ID)
	if perr != nil {
		return proto.AgentInterruptResult{}, perr
	}
	info := e.info()
	switch {
	case info.State != proto.PaneRunning:
		return proto.AgentInterruptResult{}, proto.Errorf(proto.ErrBadRequest, "pane %s has exited", info.ID)
	case info.Agent == nil:
		return proto.AgentInterruptResult{}, proto.Errorf(proto.ErrBadRequest, "pane %s is not running an agent", info.ID)
	}
	keys := []string{"ctrl+c"}
	if !p.Hard {
		keys = s.manifests[info.Agent.Name].InterruptKeys()
	}
	if err := e.p.SendKeysApart(keys, interruptGap); err != nil {
		return proto.AgentInterruptResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	e.evalMu.Lock()
	s.evaluate(e, func(t *detect.Tracker) {
		t.UserInput()
		t.Interrupted(time.Now())
	})
	state := ""
	if a := e.info().Agent; a != nil {
		state = a.State
	}
	e.evalMu.Unlock()
	if state == proto.AgentWorking {
		go s.watchStop(e)
	}
	return proto.AgentInterruptResult{ID: info.ID, Keys: keys, State: state}, nil
}

// watchStop looks at an interrupted agent every moment until it stops
// working, so its clients hear at once rather than at the next tick.
func (s *Server) watchStop(e *entry) {
	t := time.NewTicker(interruptLookEvery)
	defer t.Stop()
	until := time.After(interruptLookFor)
	for {
		select {
		case <-t.C:
			if !s.alive(e) {
				return
			}
			s.observe(e)
			if a := e.info().Agent; a == nil || a.State != proto.AgentWorking {
				return
			}
		case <-until:
			return
		case <-e.p.Done():
			return
		case <-s.quit:
			return
		}
	}
}
