package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/proto"
)

func seen(installed ...string) []proto.AgentAvailability {
	var out []proto.AgentAvailability
	for _, name := range []string{"claude", "codex", "gemini", "aider"} {
		av := proto.AgentAvailability{Name: name}
		for _, i := range installed {
			av.Installed = av.Installed || i == name
		}
		out = append(out, av)
	}
	return out
}

// TestDefaultAgentForATask: a task that names no agent gets Claude Code,
// or — where this machine was last seen without it — an agent it has.
func TestDefaultAgentForATask(t *testing.T) {
	a5IsolateEnv(t)
	s, _ := a5Server(t)
	var err error
	if s.adapters, err = adapter.New("/usr/local/bin/conch", s.configDir); err != nil {
		t.Fatal(err)
	}
	if ad, ok := s.adapters.Get("aider"); !ok || ad.Tier() != proto.TierRunsHere {
		t.Fatal("aider is not an agent conch only runs; the cases below need one")
	}
	if got := s.defaultAgent(); got != "claude" {
		t.Fatalf("nothing seen yet: %q", got)
	}
	for _, tc := range []struct {
		name      string
		installed []string
		want      string
	}{
		{"claude there", []string{"claude", "codex"}, "claude"},
		{"only codex", []string{"codex"}, "codex"},
		{"supported before runs-here", []string{"aider", "gemini"}, "gemini"},
		{"only one conch runs", []string{"aider"}, "aider"},
		{"none", nil, "claude"},
	} {
		s.agentsSeen.note(seen(tc.installed...))
		if got := s.defaultAgent(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	// An old answer is not trusted: somebody may have installed it since.
	s.agentsSeen.note(seen("codex"))
	s.agentsSeen.mu.Lock()
	s.agentsSeen.at = time.Now().Add(-2 * agentsSeenFor)
	s.agentsSeen.mu.Unlock()
	if got := s.defaultAgent(); got != "claude" {
		t.Fatalf("an hour-old answer: %q", got)
	}
}

// TestATaskNamingNoAgentStartsOneInstalled: through agent.status and
// task.create, as `conch task "…"` from a shell does — the server asked
// what is installed through a login shell that finds only Codex.
func TestATaskNamingNoAgentStartsOneInstalled(t *testing.T) {
	s, p := afterFixture(t)
	// The login shell: finds codex and nothing else, and starts nothing.
	sh := filepath.Join(t.TempDir(), "codex-only-shell")
	script := "#!/bin/sh\ncase \"$2\" in\n*'command -v codex'*) echo /opt/fake/codex; echo 'codex-cli 0.46.0';;\n*'command -v '*) exit 1;;\n*) exec sleep 30;;\nesac\n"
	if err := os.WriteFile(sh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", sh)

	res, perr := s.dispatch(nil, proto.Message{ID: "1", Method: proto.MethodAgentStatus})
	if perr != nil {
		t.Fatal(perr)
	}
	for _, a := range res.(proto.AgentStatusResult).Agents {
		if a.Installed != (a.Name == "codex") {
			t.Fatalf("the shell found %+v", a)
		}
	}
	info, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"})
	if perr != nil {
		t.Fatal(perr)
	}
	if !strings.Contains(strings.Join(info.Command, " "), "codex") || strings.Contains(strings.Join(info.Command, " "), "claude") {
		t.Fatalf("the task started %q", info.Command)
	}
	// Named, it is what was named, whatever is installed.
	info, perr = s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the view", Branch: "api/view", Agent: "claude"})
	if perr != nil {
		t.Fatal(perr)
	}
	if !strings.Contains(strings.Join(info.Command, " "), "claude") {
		t.Fatalf("named claude, started %q", info.Command)
	}
}
