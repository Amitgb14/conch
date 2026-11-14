package server

import (
	"context"
	"os"
	"strings"
	"sync"
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
	env := s.sessionEnv()
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

// agentChat reads a pane's agent's conversation, with the tools it used,
// for a chat drawn beside its terminal: what changed since the version the
// chat has. An agent that has not said which conversation it is in yet has
// an empty chat rather than an error: a chat is opened beside an agent just
// started, and fills in as it works.
func (s *Server) agentChat(p proto.AgentChatParams) (proto.AgentChat, *proto.Error) {
	e, perr := s.get(p.ID)
	if perr != nil {
		return proto.AgentChat{}, perr
	}
	info := e.info()
	if info.Agent == nil {
		return proto.AgentChat{}, proto.Errorf(proto.ErrBadRequest, "no agent is running in %s", p.ID)
	}
	out := proto.AgentChat{Agent: info.Agent.Name, Session: info.Agent.SessionID, Turns: []proto.ChatTurn{}, Reset: true}
	if out.Session == "" {
		return out, nil
	}
	env := s.sessionEnv()
	f, ok := chatSession(env, info.Agent.Name, info.Agent.SessionID, info.Cwd)
	if !ok {
		// Named but not on disk yet: an agent writes its transcript after
		// its first answer.
		return out, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	up, err := sessions.ChatSince(ctx, env, f, p.Epoch, p.Ver)
	if err != nil {
		out.Note = err.Error()
		return out, nil
	}
	out.Epoch, out.Ver, out.Reset = up.Epoch, up.Ver, up.Reset
	for _, t := range up.Turns {
		out.Turns = append(out.Turns, proto.ChatTurn{Seq: t.Seq, Role: t.Role, Text: t.Text, Detail: t.Detail, Time: t.Time,
			Input: t.Input, InputKind: t.InputKind, Output: t.Output, Error: t.Error, Done: t.Done})
	}
	return out, nil
}

// chatFiles remembers where each conversation is saved, so a chat polled
// every second does not list the folder's sessions every time.
var chatFiles sync.Map // agent \x00 session \x00 cwd → sessions.Session

func chatSession(env sessions.Env, agent, id, cwd string) (sessions.Session, bool) {
	k := agent + "\x00" + id + "\x00" + cwd
	if v, ok := chatFiles.Load(k); ok {
		f := v.(sessions.Session)
		if _, err := os.Stat(f.Path); err == nil || f.Path == "" {
			return f, true
		}
		chatFiles.Delete(k)
	}
	for _, f := range sessions.List(env, []string{cwd}, 0) {
		if f.Agent == agent && f.ID == id {
			chatFiles.Store(k, f)
			return f, true
		}
	}
	return sessions.Session{}, false
}
