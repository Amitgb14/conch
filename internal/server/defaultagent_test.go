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

// seen is a whole agent.status answer — every agent conch knows, the ones
// named installed — as a client asking would leave behind. A partial one
// is what a failed launch leaves, and makes the server look for the rest.
func seen(s *Server, installed ...string) []proto.AgentAvailability {
	var out []proto.AgentAvailability
	for _, ad := range s.adapters {
		av := proto.AgentAvailability{Name: ad.Name()}
		for _, i := range installed {
			av.Installed = av.Installed || i == ad.Name()
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
		s.agentsSeen.note(seen(s, tc.installed...))
		if got := s.defaultAgent(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	// An old answer is not trusted: somebody may have installed it since.
	s.agentsSeen.note(seen(s, "codex"))
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
	// Named, an agent this machine was found without is refused, with
	// how to install it, rather than started to die at once.
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the view", Branch: "api/view", Agent: "claude"}); perr == nil ||
		!strings.Contains(perr.Message, "Claude Code is not installed here") {
		t.Fatalf("named claude: %v", perr)
	}
}

// agentsShell is a login shell that finds the agents named and no
// other, and runs everything else with run (a shell snippet).
func agentsShell(t *testing.T, run string, found ...string) {
	t.Helper()
	var cases strings.Builder
	for _, name := range found {
		cases.WriteString("*'command -v " + name + "'*) echo /opt/fake/" + name + "; echo '" + name + " 1.0.0';;\n")
	}
	script := "#!/bin/sh\ncase \"$2\" in\n" + cases.String() + "*'command -v '*) exit 1;;\n*) " + run + ";;\nesac\n"
	sh := filepath.Join(t.TempDir(), "login-shell")
	if err := os.WriteFile(sh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", sh)
}

// TestAMissingAgentIsRefused: a task for an agent this machine was found
// without is refused with how to install it — before a branch or worktree
// is made — where it used to start a pane that died at once with 127.
func TestAMissingAgentIsRefused(t *testing.T) {
	s, p := afterFixture(t)
	agentsShell(t, "exec sleep 30", "codex")
	s.agentsSeen.note(seen(s, "codex"))

	_, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model", Agent: "claude"})
	if perr == nil || perr.Message != "Claude Code is not installed here: conch agent install claude, or c in conch" {
		t.Fatalf("refusal: %v", perr)
	}
	for _, b := range p.snapshot().Branches {
		if b.Name == "api/model" {
			t.Fatal("a refused task left its branch behind")
		}
	}
	// conch new -agent too.
	if _, perr := s.create(proto.PaneCreateParams{Agent: "claude", Cwd: p.root}); perr == nil || !strings.Contains(perr.Message, "not installed here") {
		t.Fatalf("new -agent claude: %v", perr)
	}
	// An agent with no installer says so rather than offering one.
	if ad, ok := s.adapters.Get("aider"); ok && strings.TrimSpace(ad.InstallScript()) == "" {
		s.agentsSeen.noteOne("aider", false)
		if perr := s.notInstalled(ad); perr == nil || !strings.Contains(perr.Message, "install it yourself") {
			t.Fatalf("no installer: %v", perr)
		}
	}
	// Nothing known: tried, as before.
	s.agentsSeen.mu.Lock()
	s.agentsSeen.installed = nil
	s.agentsSeen.mu.Unlock()
	if perr := s.notInstalled(mustAdapter(t, s, "opencode")); perr != nil {
		t.Fatalf("unknown is not missing: %v", perr)
	}
}

// TestAnAgentInstalledSinceIsNotRefused: the last look said missing, but
// it has been installed since, from a shell — looked at again, it starts.
func TestAnAgentInstalledSinceIsNotRefused(t *testing.T) {
	s, p := afterFixture(t)
	s.agentsSeen.note(seen(s, "codex")) // claude missing, as last found
	agentsShell(t, "exec sleep 30", "codex", "claude")
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model", Agent: "claude"}); perr != nil {
		t.Fatalf("installed since: %v", perr)
	}
	if s.agentsSeen.missing("claude") {
		t.Fatal("the new look was not kept")
	}
}

// TestALaunchThatFailsIsLearnt: an agent's pane that ends at once with
// "command not found" is noted as a failed launch; other ends are not. It
// is a hint, not a look, so it refuses nothing by itself.
func TestALaunchThatFailsIsLearnt(t *testing.T) {
	s, _ := afterFixture(t)
	now := time.Now()
	for _, tc := range []struct {
		name     string
		launched string
		info     proto.PaneInfo
		failed   bool
	}{
		{"127 at once", "codex", proto.PaneInfo{ExitCode: 127, Created: now}, true},
		{"127 after a while", "gemini", proto.PaneInfo{ExitCode: 127, Created: now.Add(-time.Minute)}, false},
		{"another failure", "opencode", proto.PaneInfo{ExitCode: 1, Created: now}, false},
		{"a terminal, not an agent", "", proto.PaneInfo{ExitCode: 127, Created: now}, false},
	} {
		s.launchFailed(&entry{launched: tc.launched}, tc.info)
		s.agentsSeen.mu.Lock()
		got := s.agentsSeen.failed[tc.launched]
		s.agentsSeen.mu.Unlock()
		if tc.launched != "" && got != tc.failed {
			t.Errorf("%s: failed %v, want %v", tc.name, got, tc.failed)
		}
		if s.agentsSeen.missing(tc.launched) {
			t.Errorf("%s: a failed launch alone made it missing", tc.name)
		}
	}
}

// TestAPaneThatCannotStartItsAgent: end to end — a task whose agent's
// command is not found ends with 127. Naming it again is not refused on
// that alone (two quick launches of the same agent raced the first one's
// exit, which is how CI found it); a task naming no agent then looks, and
// that look is what refuses it by name.
func TestAPaneThatCannotStartItsAgent(t *testing.T) {
	s, p := afterFixture(t)
	agentsShell(t, "exit 127") // finds nothing, and every agent's command is "not found"
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model", Agent: "codex"}); perr != nil {
		t.Fatal(perr) // nothing known yet: tried
	}
	a5WaitFor(t, "the launch failure noted", func() bool {
		s.agentsSeen.mu.Lock()
		defer s.agentsSeen.mu.Unlock()
		return s.agentsSeen.failed["codex"]
	})
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the view", Branch: "api/view", Agent: "codex"}); perr != nil {
		t.Fatalf("refused on a failed launch alone: %v", perr)
	}
	// claude fails too, then a task naming none looks: nothing anywhere.
	s.launchFailed(&entry{launched: "claude"}, proto.PaneInfo{ExitCode: 127, Created: time.Now()})
	_, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the client", Branch: "api/client"})
	if perr == nil || !strings.Contains(perr.Message, "Claude Code is not installed here: conch agent install claude") {
		t.Fatalf("after the look: %v", perr)
	}
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the docs", Branch: "api/docs", Agent: "codex"}); perr == nil ||
		!strings.Contains(perr.Message, "Codex is not installed here") {
		t.Fatalf("named after the look: %v", perr)
	}
}

func mustAdapter(t *testing.T, s *Server, name string) adapter.Adapter {
	t.Helper()
	ad, ok := s.adapters.Get(name)
	if !ok {
		t.Fatalf("no adapter %q", name)
	}
	return ad
}

// TestAFailedLaunchLooksForTheOthers: all anybody learnt is that Claude
// Code is missing, from a launch that failed — so a task naming no agent
// looks once for the rest and starts the one that is there, rather than
// being refused while Codex sits on the PATH.
func TestAFailedLaunchLooksForTheOthers(t *testing.T) {
	s, p := afterFixture(t)
	agentsShell(t, "exec sleep 30", "codex")
	s.launchFailed(&entry{launched: "claude"}, proto.PaneInfo{ExitCode: 127, Created: time.Now()})
	info, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"})
	if perr != nil {
		t.Fatal(perr)
	}
	if !strings.Contains(strings.Join(info.Command, " "), "codex") {
		t.Fatalf("started %q", info.Command)
	}
	// And with nothing there at all, the look is not repeated for every
	// task: a whole answer that found none is the answer.
	agentsShell(t, "exec sleep 30")
	s.agentsSeen.note(seen(s))
	if got := s.defaultAgent(); got != "claude" {
		t.Fatalf("none installed: %q", got)
	}
}

// TestANewServerHasNotReloaded: a server that has never reloaded was
// loaded when it started — to the nanosecond, or clients that compare
// the two say it reloaded.
func TestANewServerHasNotReloaded(t *testing.T) {
	a5IsolateEnv(t)
	s, _ := a5Server(t)
	if !s.loaded.Equal(s.started) {
		t.Fatalf("started %v, loaded %v", s.started, s.loaded)
	}
}
