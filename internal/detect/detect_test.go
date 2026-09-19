package detect

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
)

var (
	claudeProc = Process{PID: 10, Name: "claude", Args: []string{"claude"}}
	shellProc  = Process{PID: 11, Name: "zsh", Args: []string{"-zsh"}}
	t0         = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
)

func manifests(t *testing.T) map[string]*Manifest {
	t.Helper()
	m, errs := LoadManifests("")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return m
}

func screen(lines ...string) []string { return append(lines, "", "") }

func TestBuiltinClaudeRules(t *testing.T) {
	m := manifests(t)["claude"]
	for _, tc := range []struct {
		screen []string
		rule   string
	}{
		{screen("╭───╮", "│ Bash command │", "│ Do you want to proceed? │", "│ ❯ 1. Yes │", "│   2. No │"), "permission_prompt"},
		{screen(" Quick safety check: Is this a project you created or one you trust?", " ❯ 1. Yes, I trust this folder"), "trust_prompt"},
		{screen("✻ Thinking… (12s · ↑ 1.2k tokens · esc to interrupt)", "> "), "interruptible"},
		{screen("> ", "  ? for shortcuts"), ""},
		// Seen live: a narrower pane wraps the trust question mid-sentence.
		{screen(" Quick safety check: Is this a project you created or one", " you trust? (Like your own code)"), "trust_prompt"},
		{screen(" │ Do you want to", " │ proceed?"), "permission_prompt"},
	} {
		got := ""
		if r := m.MatchScreen(tc.screen); r != nil {
			got = r.Name
		}
		if got != tc.rule {
			t.Errorf("screen %q matched %q, want %q", tc.screen, got, tc.rule)
		}
	}
}

func TestOverrideManifest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "claude.toml"), []byte(`
agent = "claude"
process_names = ["claude", "claude-wrapper"]
[[rules]]
name = "custom"
state = "blocked"
pattern = 'HALT'
`), 0o600)
	os.WriteFile(filepath.Join(dir, "broken.toml"), []byte(`agent = "x"
[[rules]]
state = "sleeping"
pattern = "z"`), 0o600)
	m, errs := LoadManifests(dir)
	if len(errs) != 1 {
		t.Fatalf("want 1 error for broken manifest, got %v", errs)
	}
	if !m["claude"].MatchProcess(Process{Name: "claude-wrapper"}) {
		t.Fatal("override not applied")
	}
	if r := m["claude"].MatchScreen([]string{"HALT"}); r == nil || r.Name != "custom" {
		t.Fatalf("override rule not used: %v", r)
	}
}

func obs(now time.Time, p Process, watched bool, lines ...string) Observation {
	return Observation{Now: now, Process: p, Screen: lines, Watched: watched}
}

func TestTrackerLifecycle(t *testing.T) {
	tr := NewTracker(manifests(t), "")

	// A plain shell is not an agent.
	tr.Observe(obs(t0, shellProc, false, "$ "))
	if tr.Status().Agent != "" {
		t.Fatalf("shell detected as %q", tr.Status().Agent)
	}

	// Claude starts: idle by default.
	tr.Observe(obs(t0, claudeProc, false, "> "))
	if s := tr.Status(); s.Agent != "claude" || s.State != StateIdle || s.Reason != "default_idle" {
		t.Fatalf("got %+v", s)
	}

	// Prompt submitted: working from the hook.
	tr.Hook(HookEvent{Event: "UserPromptSubmit", SessionID: "sess-1"}, t0)
	tr.Observe(obs(t0.Add(time.Second), claudeProc, false, "esc to interrupt"))
	if s := tr.Status(); s.State != StateWorking || s.Source != "hook" || s.SessionID != "sess-1" {
		t.Fatalf("got %+v", s)
	}

	// A permission prompt on screen wins over the hook immediately.
	tr.Observe(obs(t0.Add(2*time.Second), claudeProc, false, "Do you want to proceed?", "❯ 1. Yes"))
	if s := tr.Status(); s.State != StateBlocked || s.Reason != "rule:permission_prompt" {
		t.Fatalf("got %+v", s)
	}

	// Answered; tool runs, then Stop while nobody watches: done.
	tr.Hook(HookEvent{Event: "PostToolUse"}, t0.Add(3*time.Second))
	tr.Observe(obs(t0.Add(3*time.Second), claudeProc, false, "esc to interrupt"))
	tr.Hook(HookEvent{Event: "Stop"}, t0.Add(4*time.Second))
	tr.Observe(obs(t0.Add(4*time.Second), claudeProc, false, "> "))
	if s := tr.Status(); s.State != StateDone {
		t.Fatalf("want done, got %+v", s)
	}

	// Looking at it clears done.
	if !tr.MarkSeen(t0.Add(5 * time.Second)) {
		t.Fatal("MarkSeen reported no change")
	}
	tr.Observe(obs(t0.Add(5*time.Second), claudeProc, true, "> "))
	if s := tr.Status(); s.State != StateIdle {
		t.Fatalf("want idle after seen, got %+v", s)
	}

	// Claude exits back to the shell: no agent, hook state forgotten.
	tr.Observe(obs(t0.Add(6*time.Second), shellProc, true, "$ "))
	if s := tr.Status(); s.Agent != "" {
		t.Fatalf("want no agent, got %+v", s)
	}
}

func TestTrackerFinishWhileWatchedIsIdle(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0)
	tr.Observe(obs(t0, claudeProc, true, "esc to interrupt"))
	tr.Hook(HookEvent{Event: "Stop"}, t0.Add(time.Second))
	tr.Observe(obs(t0.Add(time.Second), claudeProc, true, "> "))
	if s := tr.Status(); s.State != StateIdle {
		t.Fatalf("watched pane finished as %q, want idle", s.State)
	}
}

func TestTrackerStaleWorkingAfterInterrupt(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	tr.Hook(HookEvent{Event: "PreToolUse"}, t0)
	tr.Observe(obs(t0, claudeProc, true, "esc to interrupt"))
	// Esc pressed: no hook fires, the working indicator disappears.
	tr.Observe(obs(t0.Add(3*time.Second), claudeProc, true, "Interrupted by user", "> "))
	if s := tr.Status(); s.State != StateWorking {
		t.Fatalf("too early to call stale: %+v", s)
	}
	tr.Observe(obs(t0.Add(10*time.Second), claudeProc, true, "Interrupted by user", "> "))
	if s := tr.Status(); s.State != StateIdle || s.Reason != "hook_working_stale" {
		t.Fatalf("want stale idle, got %+v", s)
	}
}

func TestTrackerHookBlockedClearedByInput(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	tr.Hook(HookEvent{Event: "Notification", NotificationType: "permission_prompt", Message: "Claude needs your permission"}, t0)
	tr.Observe(obs(t0, claudeProc, true, "some prompt conch has no rule for"))
	if s := tr.Status(); s.State != StateBlocked || s.Message == "" {
		t.Fatalf("got %+v", s)
	}
	tr.UserInput()
	tr.Observe(obs(t0.Add(time.Second), claudeProc, true, "> "))
	if s := tr.Status(); s.State != StateIdle {
		t.Fatalf("want idle after input, got %+v", s)
	}
	// Notifications that don't describe a state are ignored.
	tr.Hook(HookEvent{Event: "Notification", NotificationType: "auth_success"}, t0.Add(2*time.Second))
	tr.Observe(obs(t0.Add(2*time.Second), claudeProc, true, "> "))
	if s := tr.Status(); s.State != StateIdle {
		t.Fatalf("auth_success changed state: %+v", s)
	}
}

// Seen in a real session: Claude Code names the session with a background
// subagent after Stop, and the user answered a trust prompt through the CLI
// while nobody watched the pane.
func TestTrackerRealSessionQuirks(t *testing.T) {
	tr := NewTracker(manifests(t), "claude")
	tr.Observe(obs(t0, claudeProc, false, "Is this a project you created or one you trust?"))
	if s := tr.Status(); s.State != StateBlocked {
		t.Fatalf("want blocked on trust prompt, got %+v", s)
	}
	tr.UserInput()
	tr.Observe(obs(t0.Add(time.Second), claudeProc, false, "❯ "))
	if s := tr.Status(); s.State != StateIdle {
		t.Fatalf("answered prompt should be idle, not %q", s.State)
	}

	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0.Add(2*time.Second))
	tr.Observe(obs(t0.Add(2*time.Second), claudeProc, false, "esc to interrupt"))
	tr.Hook(HookEvent{Event: "Stop"}, t0.Add(5*time.Second))
	tr.Hook(HookEvent{Event: "SubagentStop"}, t0.Add(6*time.Second))
	tr.Observe(obs(t0.Add(6*time.Second), claudeProc, false, "✻ Cooked for 3s", "❯ "))
	if s := tr.Status(); s.State != StateDone {
		t.Fatalf("SubagentStop after Stop: want done, got %+v", s)
	}
}

func TestTrackerHintIdentifiesWrapper(t *testing.T) {
	tr := NewTracker(manifests(t), "claude")
	tr.Observe(obs(t0, Process{Name: "node", Args: []string{"node", "cli.js"}}, false, "> "))
	if tr.Status().Agent != "claude" {
		t.Fatalf("hint ignored: %+v", tr.Status())
	}
	tr.Observe(obs(t0, shellProc, false, "$ "))
	if tr.Status().Agent != "" {
		t.Fatalf("shell with hint still an agent: %+v", tr.Status())
	}
}

func TestForegroundProcess(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exec sleep 5")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait(); ptmx.Close() }()

	deadline := time.Now().Add(3 * time.Second)
	var p Process
	for time.Now().Before(deadline) {
		if p, err = ForegroundProcess(ptmx); err == nil && p.Name == "sleep" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if p.Name != "sleep" || p.PID != cmd.Process.Pid {
		t.Fatalf("got %+v (err %v), want sleep pid %d", p, err, cmd.Process.Pid)
	}
	if len(p.Args) < 2 || p.Args[1] != "5" {
		t.Fatalf("args %q", p.Args)
	}
}

func TestInterpreterScriptNames(t *testing.T) {
	for _, tc := range []struct {
		p    Process
		want string
	}{
		{Process{Name: "node", Args: []string{"node", "/opt/homebrew/bin/gemini"}}, "gemini"},
		{Process{Name: "node", Args: []string{"node", "--no-warnings", "/usr/lib/node_modules/@openai/codex/bin/codex.js"}}, "codex"},
		{Process{Name: "2.1.270", Args: []string{"claude"}}, "claude"},
	} {
		found := false
		for _, n := range tc.p.Names() {
			found = found || n == tc.want
		}
		if !found {
			t.Errorf("%+v names %v lack %q", tc.p, tc.p.Names(), tc.want)
		}
	}
}

func TestOtherAgentRules(t *testing.T) {
	ms := manifests(t)
	for _, tc := range []struct {
		agent, title string
		screen       []string
		rule         string
	}{
		{"codex", "[ ! ] Action Required agentprobe", screen("› "), "action_required_title"},
		{"codex", "agentprobe", screen("Would you like to run the following command?", "› 1. Yes, proceed"), "approval"},
		{"codex", "⠙ agentprobe", screen("• Working (3s • esc to interrupt)"), "interruptible"},
		{"codex", "", screen("  Do you trust the contents of this directory? Working with untrusted contents", "› 1. Yes, continue"), "trust_prompt"},
		// Seen live: the update screen before Codex starts.
		{"codex", "", screen("  ✨ Update available! 0.144.1 -> 0.145.0", "› 1. Update now", "  2. Skip", "  Press enter to continue"), "update_prompt"},
		{"codex", "agentprobe", screen("› Ask Codex to do anything"), ""},
		{"gemini", "✋  Action Required (api)", screen("> "), "action_required_title"},
		{"gemini", "", screen(" │ Allow execution of [Shell]? │", " │ ● 1. Allow once │"), "confirmation"},
		{"gemini", "✦  Working… (api)", screen("> "), "working_title"},
		{"gemini", "◇  Ready (api)", screen("> "), "ready_title"},
		// Seen live in an unauthenticated install.
		{"gemini", "", screen("│   No authentication method selected.   │"), "auth_needed"},
		{"opencode", "OC | Fix tests", screen("  △ Permission required", "  Allow once   Allow always   Reject"), "permission"},
		{"opencode", "OpenCode", screen("  esc interrupt"), "interruptible"},
		// Seen live in devin v3000.10.31: the verb changes, the interrupt
		// hint doesn't, and the placeholder changes with it.
		{"devin", "devin: hi", screen("⠸⠀ Thinking · 0s (esc twice to interrupt)", "❭ Guide Devin while it works"), "interruptible"},
		{"devin", "devin: hi", screen("⢰⠀ Typing · 3s (esc twice to interrupt)"), "interruptible"},
		{"devin", "devin: hi", screen("❭ Guide Devin while it works", "SWE-1.6 Slow"), "working_placeholder"},
		{"devin", "devin: hi", screen("❭ Ask Devin to build features, fix bugs, or work on your code", "SWE-1.6 Slow    Context: 17k / 200k tokens (8%)"), ""},
		// Devin asks in prose, with the idle chrome around it, so waiting
		// reads as idle rather than as a guess.
		{"devin", "devin: hi", screen(" This will permanently delete the file. Should I proceed?", "❭ Ask Devin to build features, fix bugs, or work on your code"), ""},
	} {
		got := ""
		if r := ms[tc.agent].Match(tc.screen, tc.title); r != nil {
			got = r.Name
		}
		if got != tc.rule {
			t.Errorf("%s title %q screen %q: matched %q, want %q", tc.agent, tc.title, tc.screen, got, tc.rule)
		}
	}
	// Gemini runs as node; its script name identifies it.
	if MatchAny(ms, Process{Name: "node", Args: []string{"node", "/opt/homebrew/bin/gemini"}}) != ms["gemini"] {
		t.Fatal("gemini not detected from its node process")
	}
}

func TestOpenCodeAndGeminiHookEvents(t *testing.T) {
	for event, want := range map[string]string{
		"session.busy": StateWorking, "session.idle": StateIdle, "permission.asked": StateBlocked,
		"question.asked": StateBlocked, "permission.replied": StateWorking,
		"BeforeAgent": StateWorking, "AfterAgent": StateIdle,
	} {
		if got, ok := hookState(HookEvent{Event: event}); !ok || got != want {
			t.Errorf("%s → %q (%v), want %q", event, got, ok, want)
		}
	}
	if got, _ := hookState(HookEvent{Event: "Notification", NotificationType: "ToolPermission"}); got != StateBlocked {
		t.Errorf("gemini ToolPermission → %q", got)
	}
}
