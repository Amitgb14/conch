package server

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Amitgb14/conch/internal/agentsetup"
	"github.com/Amitgb14/conch/internal/proto"
)

// agent.skill writes into the server's own home — so `conch -m` installs
// it on that machine — and from an agent's pane is refused, the person's
// setup being theirs.
func TestAgentSkillMethod(t *testing.T) {
	home := a5IsolateEnv(t)
	s, _ := a5Server(t)
	run := func(c *client, p proto.AgentSkillParams) (proto.AgentSkillResult, *proto.Error) {
		res, perr := s.dispatch(c, proto.Message{Method: proto.MethodAgentSkill, Params: proto.Marshal(p)})
		if perr != nil {
			return proto.AgentSkillResult{}, perr
		}
		return res.(proto.AgentSkillResult), nil
	}
	claude := filepath.Join(home, ".claude", "skills", "conch", "SKILL.md")

	res, perr := run(&client{}, proto.AgentSkillParams{})
	if perr != nil || res.Applied || len(res.Changes) != 3 || res.Changes[0].Action != proto.SyncCreate || res.Changes[0].Path != claude {
		t.Fatalf("plan: %+v %v", res, perr)
	}
	if _, err := os.Stat(claude); err == nil {
		t.Fatal("a plan wrote the skill")
	}
	if res, perr = run(&client{}, proto.AgentSkillParams{Apply: true}); perr != nil || !res.Applied {
		t.Fatalf("apply: %+v %v", res, perr)
	}
	if b, _ := os.ReadFile(claude); !bytes.Equal(b, agentsetup.Skill) {
		t.Fatal("not written")
	}
	if _, perr := run(&client{}, proto.AgentSkillParams{Agents: []string{"aider"}}); perr == nil || perr.Code != proto.ErrBadRequest {
		t.Fatalf("unknown agent: %v", perr)
	}

	work := t.TempDir()
	agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	if _, perr := run(&client{pane: "p1"}, proto.AgentSkillParams{Remove: true, Apply: true}); perr == nil || perr.Code != proto.ErrOutOfScope {
		t.Fatalf("from an agent: %v", perr)
	}
	if _, err := os.Stat(claude); err != nil {
		t.Fatal("removed from an agent's pane")
	}
}
