package server

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// hook reports a Claude hook event for e, as `conch report` would.
func hook(s *Server, e *entry, event string) {
	s.report(e, proto.AgentReportParams{ID: e.info().ID, Agent: "claude", Event: event})
}

func agentState(e *entry) (string, int) {
	a := e.info().Agent
	if a == nil {
		return "", 0
	}
	return a.State, a.Turn
}

func screenCount(e *entry, text string) int {
	return strings.Count(strings.Join(e.p.PlainLines(), "\n"), text)
}

func TestPromptAgentRefuses(t *testing.T) {
	s, _, work := shareFixture(t)
	agentPane(t, s, "p1", "", work, "stty -echo; exec cat")
	gone := agentPane(t, s, "p2", "claude", work, "stty -echo; exec cat")
	gone.p.Close()
	a5WaitFor(t, "p2 exits", func() bool { return gone.info().State == proto.PaneExited })

	for _, c := range []struct {
		params    proto.AgentPromptParams
		code, msg string
	}{
		{proto.AgentPromptParams{ID: "p1"}, proto.ErrBadRequest, "needs a message"},
		{proto.AgentPromptParams{ID: "p1", Text: " \n "}, proto.ErrBadRequest, "needs a message"},
		{proto.AgentPromptParams{ID: "p404", Text: "hi"}, proto.ErrNotFound, `no pane "p404"`},
		{proto.AgentPromptParams{ID: "p1", Text: "hi"}, proto.ErrBadRequest, "p1 is not running an agent"},
		{proto.AgentPromptParams{ID: "p2", Text: "hi"}, proto.ErrBadRequest, "p2 has exited"},
	} {
		if _, perr := s.promptAgent(c.params); perr == nil || perr.Code != c.code || !strings.Contains(perr.Message, c.msg) {
			t.Errorf("%+v: %v, want %s %q", c.params, perr, c.code, c.msg)
		}
	}
}

// The guard itself: a message typed onto a question could answer it.
func TestPromptAgentBlockedTypesNothing(t *testing.T) {
	s, _, work := shareFixture(t)
	e := agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	s.report(e, proto.AgentReportParams{ID: "p1", Agent: "claude", Event: "Notification",
		NotificationType: "permission_prompt", Message: "Claude needs your permission to use Bash"})
	if st, _ := agentState(e); st != proto.AgentBlocked {
		t.Fatalf("state %q", st)
	}

	_, perr := s.promptAgent(proto.AgentPromptParams{ID: "p1", Text: "run the tests"})
	if perr == nil || perr.Code != proto.ErrAgentBlocked ||
		perr.Message != "claude in p1 is waiting for an answer: Claude needs your permission to use Bash; nothing was typed" {
		t.Fatalf("blocked: %v", perr)
	}
	// Refusing is not an answer: the agent still waits.
	if st, _ := agentState(e); st != proto.AgentBlocked {
		t.Fatalf("state after refusal %q", st)
	}
	// cat prints a line when Enter reaches it, so anything typed before
	// the marker would show on or above the marker's line.
	e.p.SendText("marker-line", false)
	e.p.SendKeys([]string{"enter"})
	a5WaitFor(t, "the marker", func() bool { return screenCount(e, "marker-line") == 1 })
	if n := screenCount(e, "run the tests"); n != 0 {
		t.Fatalf("typed %d times into a blocked agent", n)
	}
}

func TestPromptAgentIdleStartsANewTurn(t *testing.T) {
	s, _, work := shareFixture(t)
	e := agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	if st, turn := agentState(e); st != proto.AgentIdle || turn != 0 {
		t.Fatalf("start: %s %d", st, turn)
	}

	res, perr := s.promptAgent(proto.AgentPromptParams{ID: "p1", Text: "  run the tests  "})
	if perr != nil {
		t.Fatal(perr)
	}
	if res != (proto.AgentPromptResult{ID: "p1", Agent: "claude", Turn: 1}) {
		t.Fatalf("result %+v", res)
	}
	// Trimmed and submitted once: cat prints the line when Enter arrives.
	a5WaitFor(t, "the submitted line", func() bool { return screenCount(e, "run the tests") == 1 })
	for _, l := range e.p.PlainLines() {
		if strings.Contains(l, "run the tests") && strings.TrimRight(l, " ") != "run the tests" {
			t.Fatalf("line %q", l)
		}
	}

	hook(s, e, "UserPromptSubmit")
	if st, turn := agentState(e); st != proto.AgentWorking || turn != 1 {
		t.Fatalf("working: %s %d", st, turn)
	}
	// A tool call and a question answered are the same turn.
	hook(s, e, "PreToolUse")
	hook(s, e, "PermissionRequest")
	hook(s, e, "PostToolUse")
	hook(s, e, "Stop")
	if st, turn := agentState(e); st != proto.AgentDone || turn != 1 {
		t.Fatalf("finished: %s %d", st, turn)
	}
}

// The race the turn exists for: a done agent turns idle as soon as it is
// typed into, which a wait on state alone takes for the answer.
func TestPromptAgentDoneIsNotTheAnswer(t *testing.T) {
	s, _, work := shareFixture(t)
	e := agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	hook(s, e, "UserPromptSubmit")
	hook(s, e, "Stop")
	if st, turn := agentState(e); st != proto.AgentDone || turn != 1 {
		t.Fatalf("before: %s %d", st, turn)
	}

	res, perr := s.promptAgent(proto.AgentPromptParams{ID: "p1", Text: "and the docs"})
	if perr != nil {
		t.Fatal(perr)
	}
	st, turn := agentState(e)
	if res.Turn != 2 || st != proto.AgentIdle || turn != 1 {
		t.Fatalf("after: want turn 2, have %s at turn %d (result %+v)", st, turn, res)
	}
	hook(s, e, "UserPromptSubmit")
	hook(s, e, "Stop")
	if st, turn := agentState(e); st != proto.AgentDone || turn != res.Turn {
		t.Fatalf("answered: %s %d", st, turn)
	}
}

// A working agent takes the message when its work ends, so that work
// answers it.
func TestPromptAgentWorkingKeepsItsTurn(t *testing.T) {
	s, _, work := shareFixture(t)
	e := agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	hook(s, e, "UserPromptSubmit")
	res, perr := s.promptAgent(proto.AgentPromptParams{ID: "p1", Text: "also the docs"})
	if perr != nil || res.Turn != 1 {
		t.Fatalf("working: %+v %v", res, perr)
	}
	a5WaitFor(t, "the submitted line", func() bool { return screenCount(e, "also the docs") == 1 })
}

// A question that comes up between the paste and its Enter keeps the Enter.
// awk (not a shell, so taken for the agent) asks as soon as the message's
// first byte reaches it — an "r" ends the first record — and then shows
// each line once an Enter finishes it.
func TestPromptAgentQuestionBeforeEnter(t *testing.T) {
	s, _, work := shareFixture(t)
	e := agentPane(t, s, "p1", "claude", work, `stty raw -echo; exec awk 'BEGIN { RS = "r" }
NR == 1 { printf "Do you want to proceed?\r\n"; fflush(); RS = "\r"; next }
{ printf "entered:%s\r\n", $0; fflush() }'`)

	_, perr := s.promptAgent(proto.AgentPromptParams{ID: "p1", Text: "run the tests"})
	if perr == nil || perr.Code != proto.ErrAgentBlocked ||
		!strings.HasSuffix(perr.Message, "is waiting for an answer; the message was typed but not sent") {
		t.Fatalf("question before Enter: %v", perr)
	}
	if st, _ := agentState(e); st != proto.AgentBlocked {
		t.Fatalf("state %q", st)
	}
	// Ours is the first Enter: the line holds the message, bar the byte
	// that set the question off, then the marker.
	e.p.SendText(" marker", false)
	e.p.SendKeys([]string{"enter"})
	a5WaitFor(t, "the line", func() bool { return screenCount(e, "entered:") == 1 })
	if screenCount(e, "entered:un the tests marker") != 1 {
		t.Fatalf("screen:\n%s", strings.Join(e.p.PlainLines(), "\n"))
	}
}

// Work the screen reports counts as a turn too, for agents without hooks.
func TestPromptAgentTurnFromTheScreen(t *testing.T) {
	s, _, work := shareFixture(t)
	// The first line puts Claude's working hint on screen, the second
	// clears it.
	e := agentPane(t, s, "p1", "claude", work, `stty -echo; exec awk 'NR == 1 { print "esc to interrupt"; fflush(); next }
{ printf "\033[2J\033[H"; fflush() }'`)
	res, perr := s.promptAgent(proto.AgentPromptParams{ID: "p1", Text: "go"})
	if perr != nil || res.Turn != 1 {
		t.Fatalf("prompt: %+v %v", res, perr)
	}
	a5WaitFor(t, "working from the screen", func() bool {
		s.observe(e)
		st, turn := agentState(e)
		return st == proto.AgentWorking && turn == 1
	})
	e.p.SendKeys([]string{"enter"})
	a5WaitFor(t, "the work ends", func() bool {
		s.observe(e)
		st, turn := agentState(e)
		return (st == proto.AgentDone || st == proto.AgentIdle) && turn == 1
	})
}
