package server

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestBroadcast(t *testing.T) {
	s, _, work := shareFixture(t)

	for _, p := range []proto.AgentBroadcastParams{
		{IDs: []string{"p1"}},
		{IDs: []string{"p1"}, Text: " \n "},
		{Text: "hello"},
	} {
		if _, perr := s.broadcastMessage(p); perr == nil || perr.Code != proto.ErrBadRequest {
			t.Errorf("%+v: %v", p, perr)
		}
	}

	claude := agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	codex := agentPane(t, s, "p2", "codex", work, "stty -echo; exec cat")
	shell := agentPane(t, s, "p3", "", work, "stty -echo; exec cat")
	gone := agentPane(t, s, "p4", "claude", work, "stty -echo; exec cat")
	gone.p.Close()
	a5WaitFor(t, "p4 exits", func() bool { return gone.info().State == proto.PaneExited })

	res, perr := s.broadcastMessage(proto.AgentBroadcastParams{IDs: []string{"p1", "p2", "p3", "p4", "p404", "p1"}, Text: "  run the tests  "})
	if perr != nil {
		t.Fatal(perr)
	}
	var got []string
	for _, r := range res.Results {
		got = append(got, r.ID+"="+map[bool]string{true: "sent", false: r.Error}[r.Sent])
	}
	if strings.Join(got, ",") != "p1=sent,p2=sent,p3=not running an agent,p4=exited,p404=no such pane" {
		t.Fatalf("results: %v", got)
	}
	for _, e := range []*entry{claude, codex} {
		a5WaitFor(t, "the message in "+e.info().ID, func() bool {
			return strings.Contains(strings.Join(e.p.PlainLines(), "\n"), "run the tests")
		})
	}
	// The shell never received it.
	if strings.Contains(strings.Join(shell.p.PlainLines(), "\n"), "run the tests") {
		t.Fatal("a shell got the broadcast")
	}
	// Once per pane, trimmed, submitted with Enter: cat echoes one line.
	a5WaitFor(t, "one submitted line", func() bool {
		return strings.Count(strings.Join(claude.p.PlainLines(), "\n"), "run the tests") == 1
	})
	for _, l := range claude.p.PlainLines() {
		if strings.Contains(l, "run the tests") && strings.TrimRight(l, " ") != "run the tests" {
			t.Fatalf("line %q", l)
		}
	}
}
