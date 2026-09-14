package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// a6Provider is an in-memory Provider.
type a6Provider struct {
	res  Response
	err  error
	reqs []Request
}

func (p *a6Provider) Name() string { return "fake" }
func (p *a6Provider) Check() error { return nil }
func (p *a6Provider) Complete(_ context.Context, r Request) (Response, error) {
	p.reqs = append(p.reqs, r)
	return p.res, p.err
}

func TestA6SummarizePrompt(t *testing.T) {
	var screen []string
	for i := 0; i < 100; i++ {
		screen = append(screen, fmt.Sprintf("line-%03d", i))
	}
	screen = append(screen, "", "   ", "")
	long := strings.Repeat("é", 120)
	p := &a6Provider{res: Response{JSON: json.RawMessage(`{"doing":"  multi\nline  ","needs":"` + long + `"}`)}}
	s, err := Summarize(context.Background(), p, AgentView{Agent: "codex", State: "working", Title: "T", Branch: "feat", Cwd: "/w", Screen: screen})
	if err != nil {
		t.Fatal(err)
	}
	if s.Doing != "multi line" {
		t.Fatalf("doing: %q", s.Doing)
	}
	if r := []rune(s.Needs); len(r) != 90 || r[89] != '…' {
		t.Fatalf("needs not clipped to 90 runes: %d", len(r))
	}
	req := p.reqs[0]
	if !req.Small || req.Schema == nil || req.System == "" {
		t.Fatalf("request: %+v", req)
	}
	for _, want := range []string{"Agent: codex\n", "State: working\n", "Terminal title: T\n", "Branch: feat\n", "Directory: /w\n", "line-020", "line-099"} {
		if !strings.Contains(req.Prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, req.Prompt)
		}
	}
	if strings.Contains(req.Prompt, "line-019") {
		t.Fatal("only the last 80 lines should be sent")
	}
	if !strings.HasSuffix(req.Prompt, "line-099\n") {
		t.Fatalf("trailing blank lines should be trimmed: %q", req.Prompt[len(req.Prompt)-30:])
	}

	// No title/branch lines when unset.
	p2 := &a6Provider{res: Response{JSON: json.RawMessage(`{"doing":"d","needs":""}`)}}
	Summarize(context.Background(), p2, AgentView{Agent: "a", Screen: []string{"", ""}})
	if strings.Contains(p2.reqs[0].Prompt, "Terminal title") || strings.Contains(p2.reqs[0].Prompt, "Branch:") {
		t.Fatalf("unexpected lines: %s", p2.reqs[0].Prompt)
	}
}

func TestA6SummarizeAndPlanErrors(t *testing.T) {
	boom := errors.New("boom")
	if _, err := Summarize(context.Background(), &a6Provider{err: boom}, AgentView{}); !errors.Is(err, boom) {
		t.Fatalf("summary provider error: %v", err)
	}
	if _, err := Summarize(context.Background(), &a6Provider{res: Response{JSON: json.RawMessage(`[1]`)}}, AgentView{}); err == nil || !strings.Contains(err.Error(), "unexpected summary") {
		t.Fatalf("bad summary: %v", err)
	}
	if _, err := MakePlan(context.Background(), &a6Provider{err: boom}, World{}, "x"); !errors.Is(err, boom) {
		t.Fatalf("plan provider error: %v", err)
	}
	if _, err := MakePlan(context.Background(), &a6Provider{res: Response{JSON: json.RawMessage(`{"actions":"nope"}`)}}, World{}, "x"); err == nil || !strings.Contains(err.Error(), "unexpected plan") {
		t.Fatalf("bad plan: %v", err)
	}
	p := &a6Provider{res: Response{JSON: json.RawMessage(`{"reply":"hi","actions":[]}`)}}
	plan, err := MakePlan(context.Background(), p, world(), "what is waiting?")
	if err != nil || plan.Reply != "hi" {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	if p.reqs[0].Small || !strings.Contains(p.reqs[0].Prompt, "User request: what is waiting?") || !strings.Contains(p.reqs[0].Prompt, `"default_agent": "claude"`) {
		t.Fatalf("plan request: %+v", p.reqs[0])
	}
}

func TestA6Clip(t *testing.T) {
	if clip("  abc  ", 5) != "abc" {
		t.Fatal("clip trim")
	}
	if clip("abcdef", 5) != "abcd…" {
		t.Fatalf("clip: %q", clip("abcdef", 5))
	}
	if clip("abcde", 5) != "abcde" {
		t.Fatal("clip exact length")
	}
}
