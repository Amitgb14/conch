package server

import (
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/proto"
)

// ---- agent.prompt ----

// promptAgent submits text to one agent: what an agent driving another uses
// instead of pane.send_text, which types anything anywhere. An agent waiting
// on a question is refused: the message's Enter could answer it.
//
// The result names the turn that answers the message. An idle or done agent
// still reports that state for a moment after the message goes in — done
// even becomes idle once it is typed into — so a client waits for the turn,
// not the state.
func (s *Server) promptAgent(p proto.AgentPromptParams) (proto.AgentPromptResult, *proto.Error) {
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return proto.AgentPromptResult{}, proto.Errorf(proto.ErrBadRequest, "agent.prompt needs a message")
	}
	e, perr := s.get(p.ID)
	if perr != nil {
		return proto.AgentPromptResult{}, perr
	}

	// Held across the check and the paste so no evaluation moves the state
	// in between; the evaluation first reads the screen as it is now, not
	// as it was at the last tick.
	e.evalMu.Lock()
	s.evaluate(e, func(*detect.Tracker) {})
	info := e.info()
	res, perr := promptable(info)
	if perr == nil {
		if err := e.p.SendText(text, true); err != nil {
			perr = proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
	}
	e.evalMu.Unlock()
	if perr != nil {
		return proto.AgentPromptResult{}, perr
	}

	// Agents take an Enter inside a paste as a newline, so it goes on its
	// own. A question that came up meanwhile keeps it: the message stays
	// in the agent's input rather than answer something nobody read.
	time.Sleep(submitDelay)
	s.observe(e)
	if now := e.info(); now.Agent != nil && now.Agent.State == proto.AgentBlocked {
		return proto.AgentPromptResult{}, blockedErr(now, "the message was typed but not sent")
	}
	if err := e.p.SendKeys([]string{"enter"}); err != nil {
		return proto.AgentPromptResult{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
	}
	s.userInput(e)
	return res, nil
}

// promptable checks that the pane runs an agent free to take a message,
// and names the turn that will answer it.
func promptable(info proto.PaneInfo) (proto.AgentPromptResult, *proto.Error) {
	a := info.Agent
	switch {
	case info.State != proto.PaneRunning:
		return proto.AgentPromptResult{}, proto.Errorf(proto.ErrBadRequest, "pane %s has exited", info.ID)
	case a == nil:
		return proto.AgentPromptResult{}, proto.Errorf(proto.ErrBadRequest, "pane %s is not running an agent", info.ID)
	case a.State == proto.AgentBlocked:
		return proto.AgentPromptResult{}, blockedErr(info, "nothing was typed")
	}
	turn := a.Turn + 1
	if a.State == proto.AgentWorking {
		turn = a.Turn // it reads the message when this work ends
	}
	return proto.AgentPromptResult{ID: info.ID, Agent: a.Name, Turn: turn}, nil
}

func blockedErr(info proto.PaneInfo, what string) *proto.Error {
	asked := ""
	if m := info.Agent.Message; m != "" {
		asked = ": " + m
	}
	return proto.Errorf(proto.ErrAgentBlocked, "%s in %s is waiting for an answer%s; %s", info.Agent.Name, info.ID, asked, what)
}
