package detect

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		{screen(" │ Do you want to", " │ proceed?", " │ ❯ 1. Yes", " │   2. No"), "permission_prompt"},
		// The cursor moved off the first choice: no ❯ on it.
		{screen("Do you want to make this edit to main.go?", "  1. Yes", "  2. Yes, allow all edits", "❯ 3. No"), "permission_prompt"},
		{screen(" Bash command", "", "   ls -la", "   List files", "", " Do you want to proceed?",
			" ❯ 1. Yes", "   2. Yes, and don't ask again for: ls", "   3. No", "", " Esc to cancel · Tab to amend"), "permission_prompt"},
		// Bordered and padded to the pane's width, as a wide pane draws it.
		{screen("│ Do you want to proceed?"+strings.Repeat(" ", 150)+"│", "│ ❯ 1. Yes"+strings.Repeat(" ", 150)+"│"), "permission_prompt"},
		// The question without its choices is not the dialog: typed, printed,
		// logged or quoted, it read as waiting once.
		{screen(`> printf "Do you want to proceed%s\n" "?"`, "Do you want to proceed?", "> "), ""},
		{screen("Do you want to proceed?", "❯ "), ""},
		{screen("⏺ The installer stopped at \"Do you want to proceed?\" and needs a yes.", "❯ "), ""},
		{screen("$ grep -r 'Do you want to run' docs/", "docs/faq.md: Do you want to run it twice?", "❯ "), ""},
		// "1. Yes" far below an old question is a different thing.
		{screen("Do you want to proceed?", strings.Repeat("log line that goes on and on\n", 12), "  1. Yes, that was the plan"), ""},
		// Seen live in v2.1.284 (e2e run R36): the spinner line no longer
		// says "esc to interrupt". Thinking, running a tool, running a hook.
		{screen("✻ Canoodling… (22s · ↓ 479 tokens · thinking)", "─────", "❯ ", "─────", "  ⏵⏵ auto mode on"), "spinner"},
		{screen("✢ Effecting… (32s · ↓ 1.5k tokens)", "❯ "), "spinner"},
		{screen("✻ Galloping… (running Stop hook · 20s · ↓ 1.2k tokens · thought for 6s)", "❯ "), "spinner"},
		{screen("✳ Pondering… (1m 12s · ↑ 3.4k tokens)", "❯ "), "spinner"}, // the same line past a minute
		// Finished, it says how long it took — not working.
		{screen("✻ Sautéed for 22s · done 10:20 PM", "❯ "), ""},
		{screen("✻ Crunched for 16s · done 10:28 PM", "❯ fix the issues it found"), ""},
		// Claude's own answers and tool output can say the same words.
		{screen("⏺ Loading… (5s · the build takes a while)", "❯ "), ""},
		{screen("  ⎿  Waiting… (3s elapsed)", "❯ "), ""},
		{screen("The log said Retrying… (4s · attempt 2) and then stopped.", "❯ "), ""},
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
helpers = ["my-lsp"]
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
	if got := m["claude"].Helpers; len(got) != 1 || got[0] != "my-lsp" {
		t.Fatalf("helpers %v", got)
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
		// The trust prompt on a first run is a real screen, so it is caught.
		{"devin", "", screen(" ✱ Do you trust the authors of this directory?", "   For security, devin should not be run in directories with untrusted content.", " ❭ 1 Yes, trust", " · 2 No, exit"), "trust_prompt"},
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
	// Codex's hooks share Claude's names, and add Interrupt: the person
	// stopped it, which ends the turn.
	for event, want := range map[string]string{
		"SessionStart": StateIdle, "UserPromptSubmit": StateWorking, "PreToolUse": StateWorking,
		"PostToolUse": StateWorking, "PermissionRequest": StateBlocked, "Stop": StateIdle, "Interrupt": StateIdle,
	} {
		if got, ok := hookState(HookEvent{Event: event}); !ok || got != want {
			t.Errorf("codex %s → %q (%v), want %q", event, got, ok, want)
		}
	}
	if got, _ := hookState(HookEvent{Event: "Notification", NotificationType: "ToolPermission"}); got != StateBlocked {
		t.Errorf("gemini ToolPermission → %q", got)
	}
}

// The bug e2e run R36 found: with no hook for 8s — a long think, a long
// command — the stale-hook check fell back to the screen, which in Claude
// v2.1.284 never says "esc to interrupt", so a working agent read as done
// and `conch agent prompt -wait` returned before it had answered.
func TestClaudeSpinnerKeepsWorking(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	t0 := time.Now()
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0)
	for i, line := range []string{"✢ Effecting… (32s · ↓ 1.5k tokens)", "✻ Galloping… (running Stop hook · 20s · ↓ 1.2k tokens · thought for 6s)"} {
		tr.Observe(obs(t0.Add(time.Duration(10+i*10)*time.Second), claudeProc, false, line, "❯ "))
		if s := tr.Status(); s.State != StateWorking {
			t.Fatalf("%q after %ds without a hook: %+v", line, 10+i*10, s)
		}
	}
	// And once the spinner goes, the stale hook is let go as before.
	tr.Observe(obs(t0.Add(45*time.Second), claudeProc, false, "✻ Crunched for 16s · done 10:28 PM", "❯ "))
	if s := tr.Status(); s.State != StateDone || s.Reason != "hook_working_stale" {
		t.Fatalf("after the spinner: %+v", s)
	}
}

// The agents conch only runs: what their manifests read off a screen, and
// which process each is found by. The strings are the ones their sources
// show (each manifest says where); none of these has been watched live.
func TestLaterAgentRules(t *testing.T) {
	ms := manifests(t)
	for _, tc := range []struct {
		agent  string
		screen []string
		rule   string
	}{
		{"crush", screen("  Permission Required", "  Allow   Allow for Session   Deny"), "permission"},
		{"crush", screen("> "), ""},
		{"continue", screen("Would you like to continue?", "> Continue", "  Continue + don't ask again"), "permission"},
		{"continue", screen("⠋ Thinking… (3s • esc to interrupt)"), "interruptible"},
		{"auggie", screen("Working... (esc to interrupt)"), "interruptible"},
		{"auggie", screen("› "), ""},
	} {
		got := ""
		if r := ms[tc.agent].Match(tc.screen, ""); r != nil {
			got = r.Name
		}
		if got != tc.rule {
			t.Errorf("%s screen %q: matched %q, want %q", tc.agent, tc.screen, got, tc.rule)
		}
	}
	for _, tc := range []struct {
		p    Process
		want string
	}{
		{Process{Name: "kiro-cli", Args: []string{"kiro-cli", "chat"}}, "kiro"},
		{Process{Name: "droid", Args: []string{"droid"}}, "droid"},
		{Process{Name: "kimi-code", Args: []string{"kimi"}}, "kimi"},
		{Process{Name: "crush", Args: []string{"crush"}}, "crush"},
		{Process{Name: "agy", Args: []string{"agy"}}, "antigravity"},
		// The node ones are found by their script's name.
		{Process{Name: "node", Args: []string{"node", "/usr/local/bin/auggie"}}, "auggie"},
		{Process{Name: "node", Args: []string{"node", "/usr/local/bin/cn"}}, "continue"},
		// Vibe's launcher execs the Rust interface.
		{Process{Name: "vibe-rs", Args: []string{"vibe-rs"}}, "vibe"},
	} {
		if m := MatchAny(ms, tc.p); m != ms[tc.want] {
			t.Errorf("%+v is not found as %s", tc.p, tc.want)
		}
	}
}

// The four agents supported after the first five. Copilot, Qwen and Goose
// report their own state over OSC 7501; these are the screens for a build
// that does not, and Cline's, which never does.
func TestLaterSupportedAgentRules(t *testing.T) {
	ms := manifests(t)
	for _, tc := range []struct {
		agent, title string
		screen       []string
		rule         string
	}{
		{"copilot", "", screen("Do you want to run this command?", "❯ 1. Yes"), "approval"},
		{"copilot", "", screen("Do you want to modify src/main.go?"), "approval"},
		{"copilot", "", screen("∙ Thinking… (Esc to stop)"), "stoppable"},
		{"copilot", "Fix tests - api - GitHub Copilot", screen("> "), ""},
		{"cline", "Cline", screen("Approve tool call?", "read_files", "[y] approve  [n] deny"), "approval"},
		{"cline", "", screen(`Approve tool "run_commands" with input ls? [y/N]`), "approval"},
		{"cline", "> fix it", screen("⠋ Thinking... (esc to cancel)"), "cancellable"},
		{"cline", "Cline", screen("> "), ""},
		{"qwen", "✳︎ Qwen - api", screen("> "), "waiting_title"},
		{"qwen", "", screen(" │ Allow execution of: 'rm -rf build'? │", " │ ● 1. Yes, allow once │"), "confirmation"},
		{"qwen", "◐︎ Qwen - api", screen("> "), "working_title"},
		{"qwen", "", screen("⠋ Reticulating splines (3s · esc to cancel)"), "cancellable"},
		{"qwen", "Qwen - api", screen("> "), ""},
		{"goose", "🪿 api", screen("◇  Goose would like to call the above tool, do you allow?", "│  ● Allow"), "approval"},
		{"goose", "🪿 api", screen("◒  Consulting the flock...  (Ctrl+C to interrupt)"), "interruptible"},
		{"goose", "🪿 api", screen("( O)> "), ""},
	} {
		got := ""
		if r := ms[tc.agent].Match(tc.screen, tc.title); r != nil {
			got = r.Name
		}
		if got != tc.rule {
			t.Errorf("%s title %q screen %q: matched %q, want %q", tc.agent, tc.title, tc.screen, got, tc.rule)
		}
	}
	for _, tc := range []struct {
		p    Process
		want string
	}{
		{Process{Name: "copilot", Args: []string{"copilot", "-i", "hi"}}, "copilot"},
		{Process{Name: "node", Args: []string{"node", "/usr/local/lib/node_modules/cline/bin/cline"}}, "cline"},
		{Process{Name: ".cline", Args: []string{"/usr/local/lib/node_modules/cline/bin/.cline"}}, "cline"},
		{Process{Name: "node", Args: []string{"node", "/home/me/.local/bin/qwen"}}, "qwen"},
		{Process{Name: "goose", Args: []string{"goose", "session"}}, "goose"},
	} {
		if m := MatchAny(ms, tc.p); m != ms[tc.want] {
			t.Errorf("%+v is not found as %s", tc.p, tc.want)
		}
	}
}

// Codex reports through conch's hooks now, so a long command between
// PreToolUse and PostToolUse fires none: its "esc to interrupt" keeps the
// hook's working from going stale, and once that goes, and Stop never
// came, the screen is believed again rather than working for ever.
func TestCodexHookWorkingFollowsTheScreen(t *testing.T) {
	codex := Process{Name: "codex"}
	tr := NewTracker(manifests(t), "")
	t0 := time.Now()
	tr.Hook(HookEvent{Event: "PreToolUse"}, t0)
	tr.Observe(obs(t0.Add(30*time.Second), codex, false, "• Working (30s • esc to interrupt)", "› "))
	if s := tr.Status(); s.State != StateWorking || s.Source != "hook" {
		t.Fatalf("a long command: %+v", s)
	}
	tr.Observe(obs(t0.Add(45*time.Second), codex, false, "› "))
	if s := tr.Status(); s.State == StateWorking || s.Reason != "hook_working_stale" {
		t.Fatalf("no Stop and no work on screen: %+v", s)
	}
	// The person stopping it ends the turn at once.
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0.Add(50*time.Second))
	tr.Hook(HookEvent{Event: "Interrupt"}, t0.Add(51*time.Second))
	tr.Observe(obs(t0.Add(52*time.Second), codex, false, "› "))
	if s := tr.Status(); s.State == StateWorking || s.Reason != "hook:Interrupt" {
		t.Fatalf("after Interrupt: %+v", s)
	}
}
