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
	// With shells allowed, the terminal gets it too.
	res, _ = s.broadcastMessage(proto.AgentBroadcastParams{IDs: []string{"p3", "p4"}, Text: "echo from-broadcast", Shells: true})
	if len(res.Results) != 2 || !res.Results[0].Sent || res.Results[1].Error != "exited" {
		t.Fatalf("shells: %+v", res.Results)
	}
	a5WaitFor(t, "the command in the terminal", func() bool {
		return strings.Contains(strings.Join(shell.p.PlainLines(), "\n"), "echo from-broadcast")
	})

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

// An agent waiting on a question is not typed into by a broadcast: its
// Enter would pick whatever the menu has highlighted. The agents beside it
// still get the message.
func TestBroadcastSkipsAnAgentThatIsAsking(t *testing.T) {
	s, _, work := shareFixture(t)
	asking := agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	free := agentPane(t, s, "p2", "claude", work, "stty -echo; exec cat")
	s.report(asking, proto.AgentReportParams{ID: "p1", Agent: "claude", Event: "Notification",
		NotificationType: "permission_prompt", Message: "Claude needs your permission to use Bash"})

	res, perr := s.broadcastMessage(proto.AgentBroadcastParams{IDs: []string{"p1", "p2"}, Text: "run the tests"})
	if perr != nil {
		t.Fatal(perr)
	}
	if len(res.Results) != 2 || res.Results[0].Sent ||
		res.Results[0].Error != "claude in p1 is waiting for an answer: Claude needs your permission to use Bash; nothing was typed" ||
		!res.Results[1].Sent {
		t.Fatalf("results: %+v", res.Results)
	}
	a5WaitFor(t, "the message in p2", func() bool { return screenCount(free, "run the tests") == 1 })
	// cat prints a line when Enter reaches it, so anything typed into p1
	// before the marker would show on or above the marker's line.
	asking.p.SendText("marker-line", false)
	asking.p.SendKeys([]string{"enter"})
	a5WaitFor(t, "the marker", func() bool { return screenCount(asking, "marker-line") == 1 })
	if n := screenCount(asking, "run the tests"); n != 0 {
		t.Fatalf("typed %d times into an agent that was asking", n)
	}
	if st, _ := agentState(asking); st != proto.AgentBlocked {
		t.Fatalf("p1 is %q: the broadcast answered it", st)
	}
}

// A question that comes up between the paste and the Enter keeps the
// Enter back: the message waits in the input, unsent, and says so.
func TestBroadcastHoldsTheEnterWhenAQuestionComesUp(t *testing.T) {
	s, _, work := shareFixture(t)
	e := agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	if perr := s.pasteUnlessAsked(e, "run the tests", false); perr != nil {
		t.Fatal(perr)
	}
	s.report(e, proto.AgentReportParams{ID: "p1", Agent: "claude", Event: "Notification",
		NotificationType: "permission_prompt", Message: "Claude needs your permission to use Bash"})
	perr := s.enterUnlessAsked(e)
	if perr == nil || perr.Code != proto.ErrAgentBlocked || !strings.Contains(perr.Message, "the message was typed but not sent") {
		t.Fatalf("enter: %v", perr)
	}
	// With echo off, cat shows only what Enter submitted: nothing yet.
	e.p.SendText("marker-line", false)
	e.p.SendKeys([]string{"enter"})
	a5WaitFor(t, "the marker", func() bool { return screenCount(e, "marker-line") == 1 })
	for _, l := range e.p.PlainLines() {
		if strings.Contains(l, "run the tests") && !strings.Contains(l, "marker-line") {
			t.Fatalf("submitted on its own: %q", l)
		}
	}
}

// A pane the caller took for an agent at the last tick may be a shell by
// the time the message is typed: looked at again, it is refused unless
// shells were asked for, since a shell would run the message.
func TestPasteOnlyIntoAnAgentUnlessShells(t *testing.T) {
	s, _, work := shareFixture(t)
	e := agentPane(t, s, "p1", "", work, "stty -echo; exec cat")
	perr := s.pasteUnlessAsked(e, "rm -rf build", false)
	if perr == nil || !strings.Contains(perr.Message, "not running an agent any more; nothing was typed") {
		t.Fatalf("into a shell: %v", perr)
	}
	if perr := s.pasteUnlessAsked(e, "echo shells-asked-for", true); perr != nil {
		t.Fatalf("shells asked for: %v", perr)
	}
}
