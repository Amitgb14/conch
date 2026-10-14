package server

import (
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sessions"
)

func TestAgentReplyReadsTheConversation(t *testing.T) {
	s, _, work := shareFixture(t)
	e := agentPane(t, s, "p1", "claude", work, "sleep 30")
	t.Cleanup(func() { e.p.Close() })

	// Before the agent says which conversation it is in, there is none.
	if _, perr := s.agentReply(proto.PaneRef{ID: "p1"}); perr == nil || !strings.Contains(perr.Message, "has not said which conversation") {
		t.Fatalf("no session yet: %v", perr)
	}
	s.report(e, proto.AgentReportParams{ID: "p1", Agent: "claude", Event: "PostToolUse", SessionID: "c1"})
	got, perr := s.agentReply(proto.PaneRef{ID: "p1"})
	if perr != nil {
		t.Fatal(perr)
	}
	if got.Agent != "claude" || got.Text != "The cross compiler is missing." {
		t.Fatalf("reply %+v", got)
	}

	// A session it names that isn't on disk says so.
	s.report(e, proto.AgentReportParams{ID: "p1", Agent: "claude", Event: "PostToolUse", SessionID: "gone"})
	if _, perr := s.agentReply(proto.PaneRef{ID: "p1"}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatalf("missing session: %v", perr)
	}
	// No such pane; a pane with no agent.
	if _, perr := s.agentReply(proto.PaneRef{ID: "nope"}); perr == nil {
		t.Fatal("an unknown pane replied")
	}
	plain := agentPane(t, s, "p2", "", work, "sleep 30")
	t.Cleanup(func() { plain.p.Close() })
	if _, perr := s.agentReply(proto.PaneRef{ID: "p2"}); perr == nil || !strings.Contains(perr.Message, "no agent") {
		t.Fatalf("a shell replied: %v", perr)
	}
}

func TestLastReply(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	turns := []sessions.Turn{
		{Role: "user", Text: "review it"},
		{Role: "assistant", Text: "First look.", Time: at},
		{Role: "assistant", Text: "  Final review.  ", Time: at.Add(time.Minute)},
		{Role: "assistant", Text: "   "}, // a tool-only turn
		{Role: "user", Text: "thanks"},
	}
	got, perr := lastReply("codex", turns)
	if perr != nil || got.Text != "Final review." || !got.Time.Equal(at.Add(time.Minute)) {
		t.Fatalf("got %+v %v", got, perr)
	}
	if _, perr := lastReply("codex", turns[:1]); perr == nil || !strings.Contains(perr.Message, "has not answered") {
		t.Fatalf("no answer: %v", perr)
	}
	if _, perr := lastReply("codex", nil); perr == nil {
		t.Fatal("no turns answered")
	}
	huge := strings.Repeat("é", maxReply)
	got, _ = lastReply("codex", []sessions.Turn{{Role: "assistant", Text: huge}})
	if len(got.Text) > maxReply+20 || !strings.HasSuffix(got.Text, "… (cut)") || !strings.HasPrefix(huge, strings.TrimSuffix(got.Text, "\n\n… (cut)")) {
		t.Fatalf("cut to %d bytes", len(got.Text))
	}
}
