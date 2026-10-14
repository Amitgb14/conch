package server

import (
	"context"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sessions"
)

// maxReply bounds an answer sent back: a review or a report, not a book.
const maxReply = 64 << 10

// agentReply reads the last thing a pane's agent said from its saved
// conversation: the whole answer, which its screen shows only the end of,
// and without the interface drawn around it.
func (s *Server) agentReply(ref proto.PaneRef) (proto.AgentReply, *proto.Error) {
	e, perr := s.get(ref.ID)
	if perr != nil {
		return proto.AgentReply{}, perr
	}
	info := e.info()
	if info.Agent == nil {
		return proto.AgentReply{}, proto.Errorf(proto.ErrBadRequest, "no agent is running in %s", ref.ID)
	}
	if info.Agent.SessionID == "" {
		return proto.AgentReply{}, proto.Errorf(proto.ErrNotFound,
			"%s has not said which conversation it is in yet, so there is nothing saved to read", info.Agent.Name)
	}
	env := sessions.CurrentEnv()
	for _, f := range sessions.List(env, []string{info.Cwd}, 0) {
		if f.Agent != info.Agent.Name || f.ID != info.Agent.SessionID {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		turns, err := sessions.Transcript(ctx, env, f)
		if err != nil {
			return proto.AgentReply{}, proto.Errorf(proto.ErrBadRequest, "%v", err)
		}
		return lastReply(info.Agent.Name, turns)
	}
	return proto.AgentReply{}, proto.Errorf(proto.ErrNotFound, "no saved %s conversation %s in %s",
		sessions.AgentName(info.Agent.Name), info.Agent.SessionID, info.Cwd)
}

// lastReply is the newest assistant turn with something in it.
func lastReply(agent string, turns []sessions.Turn) (proto.AgentReply, *proto.Error) {
	for i := len(turns) - 1; i >= 0; i-- {
		t := turns[i]
		if t.Role != "assistant" || strings.TrimSpace(t.Text) == "" {
			continue
		}
		text := strings.TrimSpace(t.Text)
		if len(text) > maxReply {
			text = strings.ToValidUTF8(text[:maxReply], "") + "\n\n… (cut)"
		}
		return proto.AgentReply{Agent: agent, Text: text, Time: t.Time}, nil
	}
	return proto.AgentReply{}, proto.Errorf(proto.ErrNotFound, "%s has not answered anything yet", sessions.AgentName(agent))
}
