package detect

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestA6ManifestDir(t *testing.T) {
	if got := ManifestDir("/home/u/.config/conch"); got != "/home/u/.config/conch/agents" {
		t.Fatalf("ManifestDir: %s", got)
	}
}

func TestA6BuiltinManifests(t *testing.T) {
	ms := manifests(t)
	for _, name := range []string{"claude", "codex", "gemini", "opencode"} {
		m, ok := ms[name]
		if !ok {
			t.Fatalf("builtin %s missing", name)
		}
		if m.ScreenLines <= 0 || len(m.ProcessNames) == 0 || len(m.Rules) == 0 {
			t.Fatalf("%s manifest: %+v", name, m)
		}
		for _, r := range m.Rules {
			if r.re == nil {
				t.Fatalf("%s rule %s not compiled", name, r.Name)
			}
		}
	}
	if ms["claude"].HookWorkingStale.Duration != 8*time.Second {
		t.Fatalf("claude stale: %v", ms["claude"].HookWorkingStale)
	}
}

func TestA6ParseManifestErrors(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"syntax":      {`agent = `, ""},
		"no agent":    {`process_names = ["x"]`, "no agent name"},
		"bad state":   {"agent = \"x\"\n[[rules]]\nname = \"r\"\nstate = \"done\"\npattern = \"a\"", `not "done"`},
		"bad on":      {"agent = \"x\"\n[[rules]]\nname = \"r\"\nstate = \"idle\"\non = \"footer\"\npattern = \"a\"", `not "footer"`},
		"bad regex":   {"agent = \"x\"\n[[rules]]\nname = \"r\"\nstate = \"idle\"\npattern = \"(\"", `rule "r"`},
		"bad stale":   {"agent = \"x\"\nhook_working_stale = \"soon\"", ""},
		"wrong types": {`agent = 5`, ""},
	}
	for name, c := range cases {
		m, err := parseManifest([]byte(c.src))
		if err == nil || m != nil {
			t.Errorf("%s: accepted (%+v)", name, m)
			continue
		}
		if c.want != "" && !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	m, err := parseManifest([]byte("agent = \"x\"\nscreen_lines = -4\nhook_working_stale = \"2m\"\n[[rules]]\nname = \"t\"\nstate = \"working\"\non = \"title\"\npattern = \"busy\""))
	if err != nil || m.ScreenLines != 30 || m.HookWorkingStale.Duration != 2*time.Minute {
		t.Fatalf("defaults: %+v %v", m, err)
	}
	if r := m.Match([]string{"busy"}, ""); r != nil {
		t.Fatal("title rule matched the screen")
	}
	if r := m.Match(nil, "busy now"); r == nil || r.Name != "t" {
		t.Fatalf("title rule: %v", r)
	}
}

func TestA6LoadManifestsOverrideErrors(t *testing.T) {
	dir := t.TempDir()
	// A directory named like a manifest can't be read.
	os.Mkdir(filepath.Join(dir, "dir.toml"), 0o700)
	// A new agent not among the built-ins is added.
	os.WriteFile(filepath.Join(dir, "aider.toml"), []byte("agent = \"aider\"\nprocess_names = [\"aider\"]\n"), 0o600)
	// A broken override of a built-in keeps the built-in.
	os.WriteFile(filepath.Join(dir, "codex.toml"), []byte("agent = \"codex\"\n[[rules]]\nname=\"x\"\nstate=\"idle\"\npattern=\"[\"\n"), 0o600)
	// Non-toml files are ignored.
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("garbage"), 0o600)

	ms, errs := LoadManifests(dir)
	if len(errs) != 2 {
		t.Fatalf("errors: %v", errs)
	}
	joined := errors.Join(errs...).Error()
	if !strings.Contains(joined, "dir.toml") || !strings.Contains(joined, "codex.toml") {
		t.Fatalf("errors should name files: %s", joined)
	}
	if ms["aider"] == nil || ms["aider"].ScreenLines != 30 {
		t.Fatalf("aider: %+v", ms["aider"])
	}
	if ms["codex"] == nil || len(ms["codex"].Rules) < 2 {
		t.Fatalf("builtin codex should be kept: %+v", ms["codex"])
	}

	// A missing dir is fine.
	if _, errs := LoadManifests(filepath.Join(dir, "missing")); len(errs) != 0 {
		t.Fatalf("missing dir: %v", errs)
	}
	// A dir with glob metacharacters yields a pattern error.
	if _, errs := LoadManifests(filepath.Join(dir, "bad[")); len(errs) != 1 {
		t.Fatalf("bad pattern: %v", errs)
	}
}

func TestA6HookStateTable(t *testing.T) {
	cases := []struct {
		ev    HookEvent
		state string
		ok    bool
	}{
		{HookEvent{Event: "SessionStart"}, StateIdle, true},
		{HookEvent{Event: "UserPromptSubmit"}, StateWorking, true},
		{HookEvent{Event: "PreToolUse"}, StateWorking, true},
		{HookEvent{Event: "PostToolUse"}, StateWorking, true},
		{HookEvent{Event: "PostToolUseFailure"}, StateWorking, true},
		{HookEvent{Event: "PermissionDenied"}, StateWorking, true},
		{HookEvent{Event: "PermissionRequest"}, StateBlocked, true},
		{HookEvent{Event: "Notification", NotificationType: "permission_prompt"}, StateBlocked, true},
		{HookEvent{Event: "Notification", NotificationType: "elicitation_dialog"}, StateBlocked, true},
		{HookEvent{Event: "Notification", NotificationType: "idle_prompt"}, StateIdle, true},
		{HookEvent{Event: "Notification"}, "", false},
		{HookEvent{Event: "Stop"}, StateIdle, true},
		{HookEvent{Event: "StopFailure"}, StateIdle, true},
		{HookEvent{Event: "BeforeTool"}, StateWorking, true},
		{HookEvent{Event: "AfterTool"}, StateWorking, true},
		{HookEvent{Event: "BeforeModel"}, StateWorking, true},
		{HookEvent{Event: "PreCompress"}, StateWorking, true},
		{HookEvent{Event: "session.retry"}, StateWorking, true},
		{HookEvent{Event: "question.replied"}, StateWorking, true},
		{HookEvent{Event: "question.rejected"}, StateWorking, true},
		{HookEvent{Event: "session.error"}, StateIdle, true},
		{HookEvent{Event: "SessionEnd"}, "", true},
		{HookEvent{Event: "SubagentStart"}, "", false},
		{HookEvent{Event: "SubagentStop"}, "", false},
		{HookEvent{Event: "PreCompact"}, "", false},
		{HookEvent{Event: ""}, "", false},
	}
	for _, c := range cases {
		state, ok := hookState(c.ev)
		if state != c.state || ok != c.ok {
			t.Errorf("%+v → %q %v, want %q %v", c.ev, state, ok, c.state, c.ok)
		}
	}
}

func TestA6HookIgnoredEventKeepsSessionID(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	tr.Hook(HookEvent{Event: "SubagentStop", SessionID: "s-9"}, t0)
	tr.Observe(obs(t0, claudeProc, true, "> "))
	if s := tr.Status(); s.SessionID != "s-9" || s.Reason != "default_idle" {
		t.Fatalf("status: %+v", s)
	}
	// An empty session id doesn't erase the known one.
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0.Add(time.Second))
	tr.Observe(obs(t0.Add(time.Second), claudeProc, true, "esc to interrupt"))
	if s := tr.Status(); s.SessionID != "s-9" || s.Reason != "hook:UserPromptSubmit" {
		t.Fatalf("status: %+v", s)
	}
	// Notification reasons include the type.
	tr.Hook(HookEvent{Event: "Notification", NotificationType: "idle_prompt", Message: "waiting"}, t0.Add(2*time.Second))
	tr.Observe(obs(t0.Add(2*time.Second), claudeProc, true, "> "))
	if s := tr.Status(); s.Reason != "hook:Notification:idle_prompt" || s.Message != "waiting" || s.State != StateIdle {
		t.Fatalf("notification: %+v", s)
	}
}

func TestA6SessionEndLetsScreenDecide(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0)
	tr.Observe(obs(t0, claudeProc, true, "> "))
	if tr.Status().Source != "hook" {
		t.Fatalf("status: %+v", tr.Status())
	}
	tr.Hook(HookEvent{Event: "SessionEnd"}, t0.Add(time.Second))
	tr.Observe(obs(t0.Add(time.Second), claudeProc, true, "esc to interrupt"))
	if s := tr.Status(); s.Source != "screen" || s.State != StateWorking || s.Reason != "rule:interruptible" {
		t.Fatalf("after SessionEnd: %+v", s)
	}
}

func TestA6ObserveChangedAndSince(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	if tr.Observe(obs(t0, shellProc, false, "$ ")) {
		t.Fatal("shell → shell reported a change")
	}
	if !tr.Observe(obs(t0.Add(time.Second), claudeProc, true, "> ")) {
		t.Fatal("agent appearing is a change")
	}
	since := tr.Status().Since
	if !since.Equal(t0.Add(time.Second)) {
		t.Fatalf("since: %v", since)
	}
	if tr.Observe(obs(t0.Add(2*time.Second), claudeProc, true, "> ")) {
		t.Fatal("same status reported a change")
	}
	if !tr.Status().Since.Equal(since) {
		t.Fatal("since moved without a change")
	}
	// A new message alone is a change but keeps Since.
	tr.Hook(HookEvent{Event: "Stop", Message: "m1"}, t0.Add(3*time.Second))
	if !tr.Observe(obs(t0.Add(3*time.Second), claudeProc, true, "> ")) {
		t.Fatal("message change not reported")
	}
	if !tr.Status().Since.Equal(since) {
		t.Fatalf("since moved on a message change: %v", tr.Status().Since)
	}
	// Session id alone is a change.
	tr.Hook(HookEvent{Event: "Stop", Message: "m1", SessionID: "new"}, t0.Add(4*time.Second))
	if !tr.Observe(obs(t0.Add(4*time.Second), claudeProc, true, "> ")) {
		t.Fatal("session change not reported")
	}
}

func TestA6MarkSeenCases(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	if tr.MarkSeen(t0) {
		t.Fatal("new tracker is seen")
	}
	// Unseen but not done (the agent went away): no change, but seen again.
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0)
	tr.Observe(obs(t0, claudeProc, false, "esc to interrupt"))
	tr.Hook(HookEvent{Event: "Stop"}, t0.Add(time.Second))
	tr.Observe(obs(t0.Add(time.Second), claudeProc, false, "> "))
	if tr.Status().State != StateDone {
		t.Fatalf("want done: %+v", tr.Status())
	}
	tr.Observe(obs(t0.Add(2*time.Second), shellProc, false, "$ "))
	if tr.Status().State != "" {
		t.Fatalf("shell: %+v", tr.Status())
	}
	if tr.MarkSeen(t0.Add(3 * time.Second)) {
		t.Fatal("MarkSeen on a non-done status reported a change")
	}
	if tr.MarkSeen(t0.Add(4 * time.Second)) {
		t.Fatal("second MarkSeen reported a change")
	}
}

func TestA6DoneStaysUntilSeenAndBlockedIsNotDone(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0)
	tr.Observe(obs(t0, claudeProc, false, "esc to interrupt"))
	tr.Hook(HookEvent{Event: "Stop"}, t0.Add(time.Second))
	tr.Observe(obs(t0.Add(time.Second), claudeProc, false, "> "))
	// Watching later doesn't clear done by itself; only MarkSeen/UserInput do.
	tr.Observe(obs(t0.Add(2*time.Second), claudeProc, true, "> "))
	if tr.Status().State != StateDone {
		t.Fatalf("done cleared by observation: %+v", tr.Status())
	}
	tr.UserInput()
	tr.Observe(obs(t0.Add(3*time.Second), claudeProc, true, "> "))
	if tr.Status().State != StateIdle {
		t.Fatalf("after input: %+v", tr.Status())
	}

	// blocked → idle unwatched is not done.
	tr2 := NewTracker(manifests(t), "")
	tr2.Hook(HookEvent{Event: "PermissionRequest"}, t0)
	tr2.Observe(obs(t0, claudeProc, false, "> "))
	tr2.Hook(HookEvent{Event: "Stop"}, t0.Add(time.Second))
	tr2.Observe(obs(t0.Add(time.Second), claudeProc, false, "> "))
	if tr2.Status().State != StateIdle {
		t.Fatalf("blocked → idle: %+v", tr2.Status())
	}
}

func TestA6StaleWorkingRefreshedByScreen(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	tr.Hook(HookEvent{Event: "PreToolUse"}, t0)
	// The screen keeps showing work long after the hook: still working.
	for i := 1; i <= 20; i += 3 {
		tr.Observe(obs(t0.Add(time.Duration(i)*time.Second), claudeProc, true, "esc to interrupt"))
		if s := tr.Status(); s.State != StateWorking || s.Source != "hook" {
			t.Fatalf("t+%ds: %+v", i, s)
		}
	}
	// The indicator goes away: stale 8s after the last working screen.
	tr.Observe(obs(t0.Add(25*time.Second), claudeProc, true, "> "))
	if tr.Status().State != StateWorking {
		t.Fatalf("stale too early: %+v", tr.Status())
	}
	tr.Observe(obs(t0.Add(28*time.Second), claudeProc, true, "> "))
	if s := tr.Status(); s.State != StateIdle || s.Reason != "hook_working_stale" || s.Message != "" {
		t.Fatalf("want stale: %+v", s)
	}
}

func TestA6AgentChangeForgetsHooks(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	codex := Process{Name: "codex", Args: []string{"codex"}}
	tr.Hook(HookEvent{Event: "PermissionRequest", SessionID: "s1"}, t0)
	tr.Observe(obs(t0, claudeProc, true, "> "))
	if tr.Status().State != StateBlocked {
		t.Fatalf("status: %+v", tr.Status())
	}
	tr.Observe(obs(t0.Add(time.Second), codex, true, "› "))
	if s := tr.Status(); s.Agent != "codex" || s.State != StateIdle || s.SessionID != "" {
		t.Fatalf("switch: %+v", s)
	}
}

func TestA6ProcessErrorUsesHint(t *testing.T) {
	tr := NewTracker(manifests(t), "gemini")
	o := obs(t0, Process{}, true, "> ")
	o.ProcessErr = errors.New("no such process")
	o.Title = "✦  Working… (api)"
	tr.Observe(o)
	s := tr.Status()
	if s.Agent != "gemini" || s.State != StateWorking || s.Reason != "rule:working_title" {
		t.Fatalf("status: %+v", s)
	}
	ex := tr.Explain()
	if ex.ProcessErr != "no such process" || ex.Hint != "gemini" || ex.Manifest != "gemini" || ex.ScreenRule != "working_title" || !ex.Seen {
		t.Fatalf("explain: %+v", ex)
	}
	// Without a hint an error means no agent.
	tr2 := NewTracker(manifests(t), "")
	tr2.Observe(o)
	if tr2.Status().Agent != "" || tr2.Explain().Manifest != "" {
		t.Fatalf("no hint: %+v", tr2.Explain())
	}
	// An unknown hint is ignored.
	tr3 := NewTracker(manifests(t), "aider")
	tr3.Observe(obs(t0, Process{Name: "python3", Args: []string{"python3", "-m", "aider"}}, true, "> "))
	if tr3.Status().Agent != "" {
		t.Fatalf("unknown hint: %+v", tr3.Status())
	}
}

func TestA6ExplainDetails(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	if !reflect.DeepEqual(tr.Explain(), Explanation{}) {
		t.Fatalf("explain before observe: %+v", tr.Explain())
	}
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "row")
	}
	lines = append(lines, "last", "", "")
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0)
	tr.Observe(obs(t0.Add(1500*time.Millisecond), claudeProc, true, lines...))
	ex := tr.Explain()
	if ex.HookState != StateWorking || ex.HookReason != "hook:UserPromptSubmit" || ex.HookAge != "2s" {
		t.Fatalf("hook details: %+v", ex)
	}
	if len(ex.ScreenLines) != 30 || ex.ScreenLines[29] != "last" {
		t.Fatalf("screen tail: %d %q", len(ex.ScreenLines), ex.ScreenLines[len(ex.ScreenLines)-1])
	}
	if ex.Process.Name != "claude" || ex.Status != tr.Status() {
		t.Fatalf("explain: %+v", ex)
	}
	// Explanations serialise.
	if _, err := json.Marshal(ex); err != nil {
		t.Fatal(err)
	}
}

func TestA6Tail(t *testing.T) {
	if got := tail(nil, 5); len(got) != 0 {
		t.Fatalf("nil: %q", got)
	}
	if got := tail([]string{"", ""}, 5); len(got) != 0 {
		t.Fatalf("blank: %q", got)
	}
	if got := tail([]string{"a", "b", "c", ""}, 2); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("tail: %q", got)
	}
	// Whitespace rows are not blank for tail (unlike bottom).
	if got := tail([]string{"a", " "}, 1); !reflect.DeepEqual(got, []string{" "}) {
		t.Fatalf("space row: %q", got)
	}
}

func TestA6ExportRestoreRoundTrip(t *testing.T) {
	ms := manifests(t)
	tr := NewTracker(ms, "claude")
	tr.Hook(HookEvent{Event: "PreToolUse", SessionID: "sess", Message: "running"}, t0)
	tr.Observe(obs(t0, claudeProc, false, "esc to interrupt"))

	st := tr.Export()
	if st.Hint != "claude" || st.HookState != StateWorking || st.HookReason != "hook:PreToolUse" || st.HookMessage != "running" ||
		!st.HookAt.Equal(t0) || st.SessionID != "sess" || !st.ScreenWorking.Equal(t0) || st.Base != StateWorking || !st.Seen ||
		st.Status.Agent != "claude" || st.Status.State != StateWorking {
		t.Fatalf("export: %+v", st)
	}

	// Through JSON, as a server reload does.
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var back TrackerState
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	rt := RestoreTracker(ms, back)
	if !reflect.DeepEqual(normalize(rt.Export()), normalize(st)) {
		t.Fatalf("round trip:\n%+v\n%+v", rt.Export(), st)
	}
	if rt.Status() != tr.Status() && !rt.Status().Since.Equal(tr.Status().Since) {
		t.Fatalf("status: %+v vs %+v", rt.Status(), tr.Status())
	}
	if rt.agent != ms["claude"] {
		t.Fatal("agent not restored")
	}

	// The restored tracker continues: the same agent keeps its hook state
	// (not forgotten as an agent change), and finishing unwatched is done.
	if rt.Observe(obs(t0.Add(time.Second), claudeProc, false, "esc to interrupt")) {
		t.Fatalf("no change expected: %+v", rt.Status())
	}
	if s := rt.Status(); s.SessionID != "sess" || s.Source != "hook" || !s.Since.Equal(t0) {
		t.Fatalf("restored status: %+v", s)
	}
	rt.Hook(HookEvent{Event: "Stop"}, t0.Add(2*time.Second))
	rt.Observe(obs(t0.Add(2*time.Second), claudeProc, false, "> "))
	if rt.Status().State != StateDone {
		t.Fatalf("done after restore: %+v", rt.Status())
	}
	if !rt.MarkSeen(t0.Add(3 * time.Second)) {
		t.Fatal("MarkSeen after restore")
	}
}

func normalize(s TrackerState) TrackerState {
	s.HookAt = s.HookAt.UTC()
	s.ScreenWorking = s.ScreenWorking.UTC()
	s.Status.Since = s.Status.Since.UTC()
	return s
}

func TestA6RestoreUnknownOrEmpty(t *testing.T) {
	ms := manifests(t)
	// Empty state: a fresh tracker that is not seen (Seen false in state).
	rt := RestoreTracker(ms, TrackerState{})
	if rt.agent != nil || rt.hint != "" || rt.seen {
		t.Fatalf("empty restore: %+v", rt.Export())
	}
	// Unseen but not done: MarkSeen marks it seen without a change.
	if rt.MarkSeen(t0) || !rt.seen {
		t.Fatal("MarkSeen on an unseen idle status")
	}
	// An agent whose manifest no longer exists restores no manifest; the
	// next sample sees it as nothing → agent and keeps the hook state.
	rt = RestoreTracker(ms, TrackerState{HookState: StateBlocked, HookReason: "hook:PermissionRequest", HookAt: t0, Seen: true,
		Status: Status{Agent: "retired", State: StateBlocked}})
	if rt.agent != nil {
		t.Fatal("unknown agent restored a manifest")
	}
	rt.Observe(obs(t0.Add(time.Second), claudeProc, true, "> "))
	if s := rt.Status(); s.Agent != "claude" || s.State != StateBlocked {
		t.Fatalf("after restore: %+v", s)
	}
}

func TestA6ForegroundProcessErrors(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := ForegroundProcess(f); err == nil {
		t.Fatal("regular file is not a terminal")
	}
}

func TestA6ProcessNamesEdges(t *testing.T) {
	cases := []struct {
		p    Process
		want []string
	}{
		{Process{Name: "zsh"}, []string{"zsh"}},
		{Process{Name: "claude", Args: []string{"/usr/bin/claude"}}, []string{"claude"}},
		{Process{Name: "node", Args: []string{"node"}}, []string{"node"}},
		{Process{Name: "node", Args: []string{"node", "--a", "-b"}}, []string{"node"}},
		{Process{Name: "MainThread", Args: []string{"/usr/bin/python3", "/opt/aider.py", "x"}}, []string{"MainThread", "python3", "aider"}},
		{Process{Name: "bun", Args: []string{"bun", "run", "x.ts"}}, []string{"bun", "run"}},
	}
	for _, c := range cases {
		if got := c.p.Names(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v.Names() = %q, want %q", c.p, got, c.want)
		}
	}
	if !IsShell(Process{Name: "login"}) || IsShell(Process{Name: "claude"}) || !IsShell(Process{Name: "x", Args: []string{"-bash"}}) {
		t.Fatal("IsShell")
	}
	if MatchAny(manifests(t), Process{Name: "vim"}) != nil {
		t.Fatal("vim matched an agent")
	}
}

func TestA6ProcessInfoMissingPID(t *testing.T) {
	// A pid far beyond any real one.
	p, err := processInfo(0x7ffffff0)
	if err == nil && p.Name != "" {
		t.Fatalf("nonexistent pid: %+v", p)
	}
	self, err := processInfo(os.Getpid())
	if err != nil || self.PID != os.Getpid() || self.Name == "" || len(self.Args) == 0 {
		t.Fatalf("self: %+v %v", self, err)
	}
}
