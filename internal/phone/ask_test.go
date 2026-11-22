package phone

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// A pending AskUserQuestion is the questions, options and descriptions
// the agent asked, in full; one already answered, or another tool, is not.
func TestPendingAskFromChat(t *testing.T) {
	input := `{"questions":[{"question":"Which usage can't you see?","header":"Usage","multiSelect":false,
	  "options":[{"label":"Project → Usage tab","description":"The third tab on a project page."},
	             {"label":"Claude plan limits","description":"How much of your plan is used."}]},
	  {"question":"Which machines?","header":"Machines","multiSelect":true,"options":[{"label":"This Mac"}]}]}`
	turns := []proto.ChatTurn{
		{Seq: 1, Role: "tool", Text: "AskUserQuestion", Input: `{"questions":[{"question":"old","options":[{"label":"a"}]}]}`, Done: true},
		{Seq: 2, Role: "assistant", Text: "Let me ask."},
		{Seq: 3, Role: "tool", Text: "AskUserQuestion", Input: input},
	}
	got := pendingAsk(turns)
	if len(got) != 2 {
		t.Fatalf("questions %+v", got)
	}
	q := got[0]
	if q.Question != "Which usage can't you see?" || q.Header != "Usage" || q.MultiSelect || len(q.Options) != 2 ||
		q.Options[1].Label != "Claude plan limits" || q.Options[1].Description != "How much of your plan is used." {
		t.Errorf("first %+v", q)
	}
	if !got[1].MultiSelect || got[1].Options[0].Description != "" {
		t.Errorf("second %+v", got[1])
	}

	turns[2].Done = true
	if got := pendingAsk(turns); len(got) != 0 {
		t.Errorf("answered: %+v", got)
	}
	if got := pendingAsk([]proto.ChatTurn{{Role: "tool", Text: "AskUserQuestion", Input: "{\"questions\": [\n… 9 lines not shown …\n]}"}}); len(got) != 0 {
		t.Errorf("clipped input: %+v", got)
	}
	// A later turn means the ask is no longer what the agent waits on.
	if got := pendingAsk([]proto.ChatTurn{{Role: "tool", Text: "AskUserQuestion", Input: input}, {Role: "tool", Text: "Bash", Input: "ls"}}); len(got) != 0 {
		t.Errorf("superseded: %+v", got)
	}
}

func TestAskEndpoint(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermView)
	var pane proto.PaneInfo
	f.call(proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/cat"}, Cwd: f.dir, Cols: 80, Rows: 24}, &pane)

	var got Ask
	if status := p.get("/api/ask?pane=local:"+pane.ID, &got); status != 200 || got.Pane != "local:"+pane.ID || got.Questions == nil || len(got.Questions) != 0 {
		t.Fatalf("a pane asking nothing: %d %+v", status, got)
	}
	if status, b := p.do("GET", "/api/ask", nil); status != 400 || !strings.Contains(string(b), CodeBadRequest) {
		t.Errorf("no pane: %d %s", status, b)
	}
	if status, _ := p.do("GET", "/api/ask?pane=local:p999", nil); status != 404 {
		t.Errorf("no such pane: %d", status)
	}
}
