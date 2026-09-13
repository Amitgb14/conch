package brain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
)

// fakeClaude writes a claude stand-in that records its arguments and stdin
// and prints out.
func fakeClaude(t *testing.T, out string) (config.BrainCfg, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + log + ".args\ncat > " + log + ".stdin\ncat <<'EOF'\n" + out + "\nEOF\n"
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return config.BrainCfg{Provider: "claude", Command: bin}, log
}

func world() World {
	return World{DefaultAgent: "claude", Machines: []Machine{{
		ID: "local", Label: "this computer", Online: true, Agents: []string{"claude", "codex"},
		Projects: []Project{{ID: "r1", Name: "api", Path: "/src/api", Git: true, Base: "main"}, {ID: "r2", Name: "notes", Path: "/notes"}},
		Panes:    []Pane{{ID: "p1", Name: "fix login", Agent: "claude", State: "blocked"}},
	}, {ID: "gpu", Label: "gpu-box", Online: false}}}
}

func TestClaudePlan(t *testing.T) {
	cfg, log := fakeClaude(t, `{"type":"result","is_error":false,"result":"","total_cost_usd":0.01,
 "structured_output":{"reply":"Starting two agents.","actions":[
  {"type":"start_task","machine":"local","project":"r1","prompt":"Fix the flaky auth test","branch":"fix-auth"},
  {"type":"send","machine":"local","pane":"p1","text":"yes, continue"}]}}`)
	p, err := New(cfg)
	if err != nil || p.Check() != nil {
		t.Fatalf("provider: %v %v", err, p.Check())
	}
	w := world()
	plan, err := MakePlan(context.Background(), p, w, "fix the flaky tests in api and tell the login agent to continue")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reply != "Starting two agents." || len(plan.Actions) != 2 {
		t.Fatalf("plan: %+v", plan)
	}
	for i := range plan.Actions {
		if err := w.Validate(&plan.Actions[i]); err != nil {
			t.Fatalf("action %d: %v", i, err)
		}
	}
	if plan.Actions[0].Agent != "claude" {
		t.Fatalf("default agent not filled: %+v", plan.Actions[0])
	}
	if got := w.Describe(plan.Actions[0]); got != "Start claude in api on this computer (fix-auth): Fix the flaky auth test" {
		t.Fatalf("describe: %q", got)
	}

	args, _ := os.ReadFile(log + ".args")
	for _, want := range []string{"-p", "--json-schema", "--no-session-persistence", "--strict-mcp-config", "sonnet"} {
		if !strings.Contains(string(args), want+"\n") {
			t.Fatalf("claude args lack %s:\n%s", want, args)
		}
	}
	if stdin, _ := os.ReadFile(log + ".stdin"); !strings.Contains(string(stdin), `"fix login"`) || !strings.Contains(string(stdin), "tell the login agent") {
		t.Fatalf("prompt: %s", stdin)
	}
}

func TestClaudeErrorsAndFallbacks(t *testing.T) {
	cfg, _ := fakeClaude(t, `{"type":"result","is_error":true,"result":"Credit balance is too low"}`)
	p, _ := New(cfg)
	if _, err := MakePlan(context.Background(), p, world(), "x"); err == nil || !strings.Contains(err.Error(), "Credit balance") {
		t.Fatalf("error result: %v", err)
	}
	// Older CLIs return the JSON as text, possibly fenced.
	cfg, log := fakeClaude(t, `{"result":"`+"```json\\n{\\\"doing\\\":\\\"Editing auth.go\\\",\\\"needs\\\":\\\"Approve go test\\\"}\\n```"+`"}`)
	p, _ = New(cfg)
	s, err := Summarize(context.Background(), p, AgentView{Agent: "claude", State: "blocked", Screen: []string{"Run go test?", "", ""}})
	if err != nil || s.Doing != "Editing auth.go" || s.Needs != "Approve go test" {
		t.Fatalf("summary: %+v %v", s, err)
	}
	if args, _ := os.ReadFile(log + ".args"); !strings.Contains(string(args), "haiku\n") {
		t.Fatalf("summaries should use the small model:\n%s", args)
	}
	if _, err := New(config.BrainCfg{Provider: "nope"}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	missing, _ := New(config.BrainCfg{Provider: "claude", Command: filepath.Join(t.TempDir(), "none")})
	if missing.Check() == nil {
		t.Fatal("missing claude binary should fail Check")
	}
}

func TestValidate(t *testing.T) {
	w := world()
	cases := []struct {
		a    Action
		want string
	}{
		{Action{Type: ActStartTask, Machine: "gpu", Project: "r1", Prompt: "x"}, "offline"},
		{Action{Type: ActStartTask, Machine: "local", Project: "r9", Prompt: "x"}, "unknown project"},
		{Action{Type: ActStartTask, Machine: "local", Project: "r2", Prompt: "x"}, "not a git repository"},
		{Action{Type: ActStartTask, Machine: "local", Project: "r1"}, "needs a prompt"},
		{Action{Type: ActStartAgent, Machine: "local", Project: "r1", Agent: "gemini"}, "not installed"},
		{Action{Type: ActSend, Machine: "local", Pane: "p9", Text: "hi"}, "unknown pane"},
		{Action{Type: ActSend, Machine: "local", Pane: "p1"}, "nothing to send"},
		{Action{Type: "rm -rf", Machine: "local"}, "unknown action"},
	}
	for _, c := range cases {
		if err := w.Validate(&c.a); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: got %v, want %q", c.a, err, c.want)
		}
	}
}

func TestAPIProviders(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		switch r.URL.Path {
		case "/v1/messages":
			if r.Header.Get("x-api-key") != "k1" {
				http.Error(w, `{"error":"bad key"}`, 401)
				return
			}
			w.Write([]byte(`{"content":[{"type":"tool_use","name":"respond","input":{"doing":"Running tests","needs":""}}]}`))
		case "/v1/chat/completions":
			w.Write([]byte(`{"choices":[{"message":{"content":"{\"doing\":\"Idle\",\"needs\":\"Review the diff\"}"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_API_KEY", "")
	a, _ := New(config.BrainCfg{Provider: "anthropic", BaseURL: srv.URL})
	if a.Check() == nil {
		t.Fatal("anthropic without a key should fail Check")
	}
	t.Setenv("ANTHROPIC_API_KEY", "k1")
	s, err := Summarize(context.Background(), a, AgentView{Agent: "codex", Screen: []string{"running"}})
	if err != nil || s.Doing != "Running tests" {
		t.Fatalf("anthropic: %+v %v", s, err)
	}
	if got["model"] != "claude-haiku-4-5" || got["tool_choice"] == nil {
		t.Fatalf("anthropic request: %v", got)
	}

	o, _ := New(config.BrainCfg{Provider: "openai", BaseURL: srv.URL + "/v1", Model: "llama3"})
	if err := o.Check(); err != nil {
		t.Fatalf("local openai-compatible server needs no key: %v", err)
	}
	s, err = Summarize(context.Background(), o, AgentView{Agent: "claude"})
	if err != nil || s.Needs != "Review the diff" || got["model"] != "llama3" {
		t.Fatalf("openai: %+v %v %v", s, err, got)
	}
}
