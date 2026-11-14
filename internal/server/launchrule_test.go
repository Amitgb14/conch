package server_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// launchShell is a login shell that says every agent is installed, and
// writes each command it is asked to run to log instead of running it.
func launchShell(t *testing.T) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "ran")
	sh := filepath.Join(dir, "fake-shell")
	script := `#!/bin/sh
case "$2" in *"command -v"*) echo /fake/agent; echo 1.0.0; exit 0;; esac
printf '%s\n' "$*" >> ` + log + `
exec sleep 30
`
	if err := os.WriteFile(sh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", sh)
	return log
}

func launchArg(t *testing.T, p proto.PaneCreateParams) string {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func ran(t *testing.T, log, want string) {
	t.Helper()
	for i := 0; i < 250; i++ {
		b, _ := os.ReadFile(log)
		if strings.Contains(string(b), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, _ := os.ReadFile(log)
	t.Fatalf("never ran %q; ran:\n%s", want, b)
}

// An agent calling from its pane may start an agent and choose its model,
// and nothing else about what runs: not a second agent told to ask nobody,
// not a command of its own, not an environment. The person is never held
// to it, and [agents] unrestricted_launch lifts it.
func TestAgentLaunchRule(t *testing.T) {
	c, a, _, _, _, _, _, _ := scopeFixture(t)
	log := launchShell(t)
	h := startHelper(t, c, a, "claude")

	refused := []proto.PaneCreateParams{
		{Agent: "claude", AgentArgs: "--dangerously-skip-permissions"},
		{Agent: "codex", AgentArgs: "--yolo"},
		{Agent: "claude", AgentArgs: "--model opus --dangerously-skip-permissions"},
		{Agent: "claude", AgentArgs: "--model opus; touch /tmp/conch-launch-rule"},
		{Agent: "claude", AgentArgs: "--model $(id)"},
		{Agent: "claude", AgentArgs: "--model -x"},
		{Agent: "claude", AgentArgs: "--model"},
		{Command: []string{"/bin/sh", "-c", "echo hi"}},
		{Agent: "claude", Command: []string{"claude", "--dangerously-skip-permissions"}},
		{Env: []string{"ZDOTDIR=/somewhere"}},
		{Agent: "claude", Env: []string{"CLAUDE_CONFIG_DIR=/somewhere"}},
	}
	for _, p := range refused {
		p.Cwd = a
		if code, _ := h.do("launch", launchArg(t, p)); code != proto.ErrOutOfScope {
			t.Errorf("%+v: %s, want refused", p, code)
		}
	}

	allowed := []struct {
		p    proto.PaneCreateParams
		runs string // in what the shell is asked to run
	}{
		{proto.PaneCreateParams{Agent: "claude"}, "exec claude"},
		{proto.PaneCreateParams{Agent: "claude", AgentArgs: "--model opus "}, "--model opus"},
		{proto.PaneCreateParams{Agent: "claude", AgentArgs: "--model='claude-opus-4-1[1m]'"}, "--model 'claude-opus-4-1[1m]'"},
		{proto.PaneCreateParams{Agent: "opencode", AgentArgs: `--model "openai/gpt-5"`}, "--model openai/gpt-5"},
		{proto.PaneCreateParams{}, "-l"},                                            // a plain terminal
		{proto.PaneCreateParams{Command: []string{os.Getenv("SHELL"), "-l"}}, "-l"}, // as an older conch new asked for one
	}
	for _, w := range allowed {
		w.p.Cwd = a
		if code, _ := h.do("launch", launchArg(t, w.p)); code != "ok" {
			t.Errorf("%+v: %s, want ok", w.p, code)
			continue
		}
		ran(t, log, w.runs)
	}

	// Nothing refused reached a shell.
	b, _ := os.ReadFile(log)
	for _, bad := range []string{"dangerously", "yolo", "touch", "$(id)", "echo hi", "ZDOTDIR"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("ran %q:\n%s", bad, b)
		}
	}

	// The person, from outside every pane, is not held to it.
	if err := c.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude",
		AgentArgs: "--dangerously-skip-permissions", Cwd: a}, nil); err != nil {
		t.Fatalf("the person's own launch: %v", err)
	}
	ran(t, log, "--dangerously-skip-permissions")

	// Somebody who wants agents to start agents however they like says so.
	cfg := filepath.Join(os.Getenv("CONCH_HOME"), "config.toml")
	if err := os.WriteFile(cfg, []byte("[agents]\nunrestricted_launch = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := proto.PaneCreateParams{Agent: "codex", AgentArgs: "--yolo", Cwd: a}
	if code, _ := h.do("launch", launchArg(t, p)); code != "ok" {
		t.Fatalf("with unrestricted_launch: %s", code)
	}
	ran(t, log, "--yolo")
	// And takes it back, without a restart.
	os.WriteFile(cfg, []byte("[agents]\nunrestricted_launch = false\n"), 0o600)
	if code, _ := h.do("launch", launchArg(t, p)); code != proto.ErrOutOfScope {
		t.Fatalf("after turning it off: %s", code)
	}
}

// The rule's other half: an agent may start a plain shell, but not type
// into one — what it types there runs, and `claude
// --dangerously-skip-permissions` typed into a shell it started is the
// launch refused above. It may type into agents: a shell that has an
// agent in front now, and not once that agent has gone back to the shell.
// The person is never held to it, and [agents] unrestricted_launch lifts
// it with the rest.
func TestAgentTypesOnlyIntoAgents(t *testing.T) {
	c, a, _, _, _, _, _, _ := scopeFixture(t)
	h := startHelper(t, c, a, "claude")
	screen := func(id string) string {
		var r proto.PaneReadResult
		c.Call(t.Context(), proto.MethodPaneRead, proto.PaneRef{ID: id}, &r)
		return strings.Join(r.Lines, "\n")
	}

	// A shell of its own: started, and not typed into.
	code, sh := h.do(proto.MethodPaneCreate, a)
	if code != "ok" {
		t.Fatalf("a plain shell: %s", code)
	}
	for _, m := range []string{proto.MethodPaneSendText, proto.MethodPaneSendKeys, proto.MethodPaneSendMouse} {
		if code, _ := h.do(m, sh); code != proto.ErrOutOfScope {
			t.Errorf("%s into its own shell: %s", m, code)
		}
	}

	// The person's shell in its project: not until an agent is in front.
	// The agent is this test binary under the name claude, waiting on its
	// input (TestLaunchRuleWaiter). A link to /bin/cat did this once, until
	// Linux runners shipped uutils coreutils, one binary that refuses to
	// run under any name but its own.
	bin := t.TempDir()
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), self, 0o755); err != nil {
		t.Fatal(err)
	}
	var mine proto.PaneInfo
	// A shell with job control, said rather than left to $SHELL: without
	// it (/bin/sh on Linux CI) a command typed at it stays in the shell's
	// process group, so the terminal's foreground is still the shell and
	// no agent is ever in front.
	if err := c.Call(t.Context(), proto.MethodPaneCreate, proto.PaneCreateParams{Cwd: a, Cols: 80, Rows: 10, Command: []string{"/bin/sh", "-i", "-m"}}, &mine); err != nil {
		t.Fatal(err)
	}
	if code, _ := h.do(proto.MethodPaneSendText, mine.ID); code != proto.ErrOutOfScope {
		t.Fatalf("into the person's shell: %s", code)
	}
	// The person may, and starts an agent there (cat, under the name).
	if err := c.Call(t.Context(), proto.MethodPaneSendText, proto.PaneSendTextParams{ID: mine.ID, Text: "CONCH_LAUNCH_WAITER=1 " + filepath.Join(bin, "claude") + " -test.run=^TestLaunchRuleWaiter$\r"}, nil); err != nil {
		t.Fatal(err)
	}
	agentIs := func(want bool) {
		t.Helper()
		for i := 0; ; i++ {
			var list proto.PaneList
			c.Call(t.Context(), proto.MethodPaneList, nil, &list)
			for _, p := range list.Panes {
				if p.ID == mine.ID && (p.Agent != nil) == want {
					return
				}
			}
			if i == 500 {
				var why json.RawMessage
				c.Call(t.Context(), proto.MethodAgentExplain, proto.PaneRef{ID: mine.ID}, &why)
				t.Fatalf("an agent in %s never became %v; conch saw: %s\nscreen:\n%s", mine.ID, want, why, screen(mine.ID))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	agentIs(true)
	if code, _ := h.do(proto.MethodPaneSendText, mine.ID); code != "ok" {
		t.Fatalf("into an agent in front of a shell: %s", code)
	}
	// The agent leaves; the shell is back, and so is the refusal.
	if err := c.Call(t.Context(), proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: mine.ID, Keys: []string{"ctrl+d"}}, nil); err != nil {
		t.Fatal(err)
	}
	agentIs(false)
	if code, _ := h.do(proto.MethodPaneSendText, mine.ID); code != proto.ErrOutOfScope {
		t.Fatalf("into the shell the agent left: %s", code)
	}

	// Nothing refused was typed.
	if strings.Contains(screen(sh), "from-helper") {
		t.Fatalf("typed into its own shell:\n%s", screen(sh))
	}

	// Somebody who wants agents to type into shells says so.
	cfg := filepath.Join(os.Getenv("CONCH_HOME"), "config.toml")
	if err := os.WriteFile(cfg, []byte("[agents]\nunrestricted_launch = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := h.do(proto.MethodPaneSendText, sh); code != "ok" {
		t.Fatalf("with unrestricted_launch: %s", code)
	}
	os.WriteFile(cfg, []byte("[agents]\nunrestricted_launch = false\n"), 0o600)
	if code, _ := h.do(proto.MethodPaneSendText, sh); code != proto.ErrOutOfScope {
		t.Fatalf("after turning it off: %s", code)
	}
}

// TestLaunchRuleWaiter is the agent TestAgentTypesOnlyIntoAgents starts in
// a shell: it reads its input until it ends (ctrl+d), as cat would.
func TestLaunchRuleWaiter(t *testing.T) {
	if os.Getenv("CONCH_LAUNCH_WAITER") != "1" {
		t.Skip("run by TestAgentTypesOnlyIntoAgents")
	}
	io.Copy(io.Discard, os.Stdin)
}
