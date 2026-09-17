package proto

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestA3IsRelease(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	for v, want := range map[string]bool{
		"0.2.0": true, "1.0.0-rc1": true, "0.1.0-dev": false, "dev": false, "": false, "0.3.0-devel": false,
	} {
		Version = v
		if got := IsRelease(); got != want {
			t.Errorf("IsRelease(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestA3Capabilities(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Capabilities {
		if seen[c] {
			t.Errorf("duplicate capability %q", c)
		}
		seen[c] = true
		if !strings.Contains(c, ".v") {
			t.Errorf("capability %q lacks a version suffix", c)
		}
	}
	// Clients depend on these; removing one breaks older clients' detection.
	for _, c := range []string{"pane.v1", "events.v1", "server.reload.v1", "session.delete.v1", "pane.default_shell.v1"} {
		if !seen[c] {
			t.Errorf("capability %q missing", c)
		}
	}
	if ProtocolVersion != 1 {
		t.Errorf("ProtocolVersion = %d", ProtocolVersion)
	}
}

func TestA3ErrorType(t *testing.T) {
	e := Errorf(ErrNotFound, "no pane %q (%d)", "p1", 3)
	if e.Code != ErrNotFound || e.Message != `no pane "p1" (3)` {
		t.Fatalf("Errorf = %+v", e)
	}
	if e.Error() != `not_found: no pane "p1" (3)` {
		t.Fatalf("Error() = %q", e.Error())
	}
	var err error = e
	var pe *Error
	if !errors.As(err, &pe) || pe != e {
		t.Fatal("errors.As")
	}
	if got := Errorf(ErrInternal, "100%% done").Message; got != "100% done" {
		t.Fatalf("format verbs: %q", got)
	}
	b, _ := json.Marshal(e)
	if string(b) != `{"code":"not_found","message":"no pane \"p1\" (3)"}` {
		t.Fatalf("json = %s", b)
	}
}

func TestA3MessageJSON(t *testing.T) {
	// Empty fields are omitted, keeping the wire format compact.
	b, err := json.Marshal(Message{ID: "1", Method: MethodPing})
	if err != nil || string(b) != `{"id":"1","method":"ping"}` {
		t.Fatalf("request = %s %v", b, err)
	}
	b, _ = json.Marshal(Message{Event: EventPaneFrame, Data: json.RawMessage(`{"id":"p"}`)})
	if string(b) != `{"event":"pane.frame","data":{"id":"p"}}` {
		t.Fatalf("event = %s", b)
	}
	b, _ = json.Marshal(Message{ID: "2", Error: Errorf(ErrUnknown, "nope")})
	if string(b) != `{"id":"2","error":{"code":"unknown_method","message":"nope"}}` {
		t.Fatalf("error response = %s", b)
	}

	in := Message{ID: "3", Method: MethodPaneCreate, Params: Marshal(PaneCreateParams{Agent: "claude", Cols: 80}),
		Result: json.RawMessage(`{"ok":true}`), Event: "x", Data: json.RawMessage(`[1]`), Error: &Error{Code: "c", Message: "m"}}
	b, _ = json.Marshal(in)
	var out Message
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip:\n in %+v\nout %+v", in, out)
	}
}

func a3RoundTrip[T any](t *testing.T, v T) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got T
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%T: %v", v, err)
	}
	if !reflect.DeepEqual(v, got) {
		t.Fatalf("%T round trip:\n in %+v\nout %+v\njson %s", v, v, got, b)
	}
}

func TestA3PayloadRoundTrips(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 30, 0, 0, time.UTC)
	status := &AgentStatus{Name: "claude", State: AgentBlocked, Source: "hook", Reason: "permission", Message: "ok?",
		SessionID: "s1", Since: now, Tokens: &Tokens{Input: 1, CacheWrite: 2, CacheRead: 3, Output: 4, Context: 5, Model: "opus", CostUSD: 1.25, ContextSize: 200000}}
	limits := PlanLimits{Agent: "claude", FiveHour: &LimitWindow{UsedPct: 42.5, ResetsAt: now}, Week: &LimitWindow{UsedPct: 101}, Spend: &LimitWindow{UsedPct: 3}, At: now}

	a3RoundTrip(t, HelloParams{Client: "tui", Version: "0.1.0", Protocol: 1, Capabilities: []string{"a"}})
	a3RoundTrip(t, HelloResult{Version: "1", Protocol: 1, Capabilities: []string{"x"}, PID: 9, Started: now, Build: "b", BuildID: "id", Platform: "linux/amd64", Hostname: "h", Home: "/home/u"})
	a3RoundTrip(t, ServerReloadParams{Binary: "/bin/conch"})
	a3RoundTrip(t, ServerReloadResult{Binary: "/bin/conch"})
	a3RoundTrip(t, PaneList{Panes: []PaneInfo{{ID: "p1", Name: "zsh", Command: []string{"zsh", "-l"}, Cwd: "/w", Cols: 80, Rows: 24, PID: 7,
		State: PaneRunning, ExitCode: 0, Created: now, Agent: status, Title: "Fix bug", CustomName: true, ProjectID: "pr", Branch: "main"}}})
	a3RoundTrip(t, PaneRenameParams{ID: "p", Name: "n"})
	a3RoundTrip(t, PaneSendMouseParams{ID: "p", X: 1, Y: 2, Button: "left", Action: MousePress, Shift: true, Alt: true, Ctrl: true})
	a3RoundTrip(t, ProjectList{Projects: []ProjectInfo{{ID: "p", Name: "n", Path: "/p", Git: true, Base: "main",
		Worktrees: []WorktreeInfo{{Path: "/p", Branch: "main", Head: "abc", Detached: true, Main: true, Status: &GitStatus{Staged: 1, Unstaged: 2, Untracked: 3, Conflicts: 4, Files: 5, Added: 6, Deleted: 7}}},
		Branches: []BranchInfo{{Name: "b", Upstream: "origin/b", Gone: true, Ahead: 1, Behind: 2, BaseAhead: 3, BaseBehind: 4, Committed: now, Subject: "s", Worktree: "/w",
			PR: &PRInfo{Number: 5, Title: "t", State: "OPEN", Draft: true, URL: "u", Review: "APPROVED", Checks: "pass", Passed: 3, Total: 4, Head: "abc"}}},
		Error: "e", Refreshed: now, PRStatus: "no GitHub remote", LocalFiles: []string{".env"}, LocalFilesDefault: true}}})
	a3RoundTrip(t, ProjectRef{ID: "p"})
	a3RoundTrip(t, ProjectAddParams{Path: "/p"})
	a3RoundTrip(t, ProjectCreateParams{Path: "/p", Git: true})
	a3RoundTrip(t, FSListParams{Path: "~", Hidden: true})
	a3RoundTrip(t, FSList{Path: "/a", Parent: "/", Home: "/h", Entries: []FSEntry{{Name: "x", Git: true, Project: true}}, Truncated: true})
	a3RoundTrip(t, FSMkdirParams{Path: "/a"})
	a3RoundTrip(t, ChangesParams{ProjectID: "p", Branch: "b"})
	a3RoundTrip(t, Changes{ProjectID: "p", Branch: "b", Base: "main", Worktree: "/w",
		Files:   []FileChange{{Path: "a", OrigPath: "b", Code: "R", Staged: true, Unstaged: true, Added: 1, Deleted: 2, Binary: true}},
		Commits: []CommitInfo{{Hash: "h", Subject: "s", Author: "a", Time: now}}})
	a3RoundTrip(t, DiffParams{ProjectID: "p", Branch: "b", File: "f"})
	a3RoundTrip(t, DiffResult{Diff: "+x\n-y\n"})
	a3RoundTrip(t, WorktreeAddParams{ProjectID: "p", Branch: "b", Base: "main"})
	a3RoundTrip(t, ProjectFilesParams{ProjectID: "p", Patterns: []string{"*.env"}, Reset: true})
	a3RoundTrip(t, WorktreeFilesParams{ProjectID: "p", Path: "/w", Overwrite: true})
	a3RoundTrip(t, WorktreeFilesResult{Copied: []string{"a"}, Skipped: []string{".env (exists)"}})
	a3RoundTrip(t, WorktreeRemoveParams{ProjectID: "p", Path: "/w"})
	a3RoundTrip(t, BranchRef{ProjectID: "p", Branch: "b"})
	a3RoundTrip(t, WorktreeStale{Worktrees: []StaleWorktree{{Path: "/w", Branch: "b", Base: true, Missing: true, Locked: true, Panes: true,
		Uncommitted: 1, Unmerged: 2, Merged: true, Gone: true, PR: &PRInfo{Number: 1, State: "MERGED"}, Committed: now,
		Reasons: []string{"merged into main"}, Suggested: true}}})
	a3RoundTrip(t, WorktreeCleanupParams{ProjectID: "p", Remove: []CleanupWorktree{{Path: "/w", Force: true}}})
	a3RoundTrip(t, WorktreeCleanupResult{Removed: []string{"/w"}, Failed: []CleanupFailure{{Path: "/x", Error: "locked"}}})
	a3RoundTrip(t, BranchCommitParams{ProjectID: "p", Branch: "b", Message: "m", Files: []string{"a", "b"}, Patch: "@@ -1 +1 @@\n-a\n+b\n"})
	a3RoundTrip(t, CommitResult{Hash: "h", Into: "main"})
	a3RoundTrip(t, BranchPRParams{ProjectID: "p", Branch: "b", Title: "t", Body: "d", Draft: true})
	a3RoundTrip(t, BranchPRResult{URL: "https://example.invalid/pr/1"})
	a3RoundTrip(t, BranchMergeParams{ProjectID: "p", Branch: "b", Squash: true, Message: "m"})
	a3RoundTrip(t, BranchDiscardParams{ProjectID: "p", Branch: "b", DryRun: true, Force: true})
	a3RoundTrip(t, BranchDiscardResult{Worktree: "/w", Uncommitted: []string{"a"}, Unmerged: 2, Done: true})
	a3RoundTrip(t, WorktreeResult{Path: "/w", Copied: []string{"a"}})
	a3RoundTrip(t, TaskCreateParams{ProjectID: "p", Prompt: "do", Branch: "b", Base: "m", Agent: "codex", Cols: 1, Rows: 2})
	a3RoundTrip(t, AgentLimitsResult{Limits: []PlanLimits{limits}})
	a3RoundTrip(t, AgentStatusResult{Agents: []AgentAvailability{{Name: "claude", Label: "Claude Code", Installed: true, Path: "/bin/claude", Version: "2"}}})
	a3RoundTrip(t, SessionListParams{ProjectID: "p", Dir: "/d", Limit: 5})
	a3RoundTrip(t, SessionList{Sessions: []SessionInfo{{Agent: "claude", ID: "s", Dir: "/d", Branch: "b", Title: "t", Started: now, Updated: now, PaneID: "p", Interrupted: true}}})
	a3RoundTrip(t, SessionRef{Agent: "claude", ID: "s", Dir: "/d", Cols: 1, Rows: 2})
	a3RoundTrip(t, AgentSetupParams{Dir: "/d", Agent: "claude"})
	a3RoundTrip(t, AgentSetupResult{Dir: "/d", ProjectID: "p", Main: "/m", Worktree: "/w", LocalFiles: []LocalFile{{Path: ".env", State: FileMissing}},
		Agents: []AgentSetup{{Agent: "claude", Label: "Claude", Notes: []string{"n"}, Groups: []SetupGroup{{Title: "Skills", Items: []SetupItem{{Name: "x", Scope: "user", Path: "/p", Detail: "d", Missing: true}}}}}}})
	a3RoundTrip(t, AgentInstallParams{Agent: "codex", Cols: 1, Rows: 2})
	a3RoundTrip(t, AgentReportParams{ID: "p", Agent: "claude", Event: "Stop", NotificationType: "idle", Message: "m", SessionID: "s", TranscriptPath: "/t", Limits: &limits, ContextUsed: 5, ContextSize: 10})
	a3RoundTrip(t, PaneCreateParams{Name: "n", Agent: "claude", AgentArgs: "--model opus", Prompt: "p", Command: []string{"zsh"}, Cwd: "/c", Env: []string{"A=B"}, Cols: 1, Rows: 2, ShellTheme: "agnoster", NoProject: true})
	a3RoundTrip(t, ShellThemes{Shell: "zsh", OMZ: true, Current: "robbyrussell", Themes: []string{"a", "b"}})
	a3RoundTrip(t, PaneRef{ID: "p"})
	a3RoundTrip(t, PaneResizeParams{ID: "p", Cols: 100, Rows: 40})
	a3RoundTrip(t, PaneSendTextParams{ID: "p", Text: "héllo\n", Paste: true})
	a3RoundTrip(t, PaneSendKeysParams{ID: "p", Keys: []string{"enter", "ctrl+c"}})
	a3RoundTrip(t, PaneReadResult{Lines: []string{"a", ""}})
	a3RoundTrip(t, Frame{ID: "p", Cols: 2, Rows: 1, Lines: []string{"\x1b[1mab"}, Title: "t", Offset: 3, History: 10, Mouse: true, AltScreen: true})
	a3RoundTrip(t, PaneScrollParams{ID: "p", Offset: 4})
}

func TestA3WireNames(t *testing.T) {
	// Field names are the wire contract with servers of other builds.
	b, _ := json.Marshal(PaneCreateParams{AgentArgs: "x", ShellTheme: "y", NoProject: true})
	if string(b) != `{"agent_args":"x","shell_theme":"y","no_project":true}` {
		t.Fatalf("PaneCreateParams = %s", b)
	}
	b, _ = json.Marshal(HelloResult{BuildID: "i"})
	if !strings.Contains(string(b), `"build_id":"i"`) || strings.Contains(string(b), `"build"`) || strings.Contains(string(b), `"hostname"`) {
		t.Fatalf("HelloResult = %s", b)
	}
	b, _ = json.Marshal(PaneInfo{})
	for _, omit := range []string{"agent", "title", "custom_name", "project_id", "branch"} {
		if strings.Contains(string(b), `"`+omit+`"`) {
			t.Errorf("PaneInfo zero value includes %q: %s", omit, b)
		}
	}
}

func TestA3DisplayName(t *testing.T) {
	agent := &AgentStatus{Name: "claude"}
	for _, c := range []struct {
		p    PaneInfo
		want string
	}{
		{PaneInfo{Name: "zsh"}, "zsh"},
		{PaneInfo{Name: "zsh", Title: "vim foo"}, "zsh"},                                         // titles only count for agents
		{PaneInfo{Name: "claude", Agent: agent}, "claude"},                                       // agent with no title
		{PaneInfo{Name: "claude", Agent: agent, Title: "Fix the tests"}, "Fix the tests"},        // agent task title
		{PaneInfo{Name: "mine", Agent: agent, Title: "Fix the tests", CustomName: true}, "mine"}, // user's name wins
	} {
		if got := c.p.DisplayName(); got != c.want {
			t.Errorf("%+v: DisplayName = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestA3GitStatusClean(t *testing.T) {
	var nilStatus *GitStatus
	if !nilStatus.Clean() {
		t.Error("nil status is clean")
	}
	if !(&GitStatus{}).Clean() {
		t.Error("zero status is clean")
	}
	if !(&GitStatus{Added: 3}).Clean() {
		t.Error("Clean counts files, not lines")
	}
	if (&GitStatus{Files: 1, Untracked: 1}).Clean() {
		t.Error("one file is not clean")
	}
}

func TestA3NeedsAttention(t *testing.T) {
	var none *AgentStatus
	if none.NeedsAttention() {
		t.Error("nil agent")
	}
	for state, want := range map[string]bool{AgentIdle: false, AgentWorking: false, AgentBlocked: true, AgentDone: true, "": false} {
		if got := (&AgentStatus{State: state}).NeedsAttention(); got != want {
			t.Errorf("%q: %v", state, got)
		}
	}
}

func TestA3Marshal(t *testing.T) {
	if Marshal(nil) != nil {
		t.Fatal("nil must marshal to no params")
	}
	if got := string(Marshal(PaneRef{ID: "p"})); got != `{"id":"p"}` {
		t.Fatalf("got %s", got)
	}
	var typedNil *PaneRef
	if got := string(Marshal(typedNil)); got != "null" {
		t.Fatalf("typed nil = %s", got)
	}
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(r.(string), "proto: marshal chan int") {
			t.Fatalf("recover = %v", r)
		}
	}()
	Marshal(make(chan int))
}

type a3RW struct {
	io.Reader
	io.Writer
	closed bool
}

func (rw *a3RW) Close() error { rw.closed = true; return nil }

func TestA3ConnReadWrite(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("\n\n{\"id\":\"1\",\"method\":\"ping\"}\n\n{\"event\":\"pane.closed\",\"data\":{\"id\":\"p\"}}\n{\"id\":\"2\"}")
	rw := &a3RW{Reader: in, Writer: &out}
	c := NewConn(rw)

	m, err := c.Read()
	if err != nil || m.ID != "1" || m.Method != MethodPing {
		t.Fatalf("first: %+v %v", m, err)
	}
	m, err = c.Read()
	if err != nil || m.Event != EventPaneClosed || string(m.Data) != `{"id":"p"}` {
		t.Fatalf("second (blank lines skipped): %+v %v", m, err)
	}
	m, err = c.Read()
	if err != nil || m.ID != "2" {
		t.Fatalf("last line without newline: %+v %v", m, err)
	}
	if _, err := c.Read(); err != io.EOF {
		t.Fatalf("end: %v", err)
	}

	if err := c.Write(Message{ID: "9", Result: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(Message{Event: "e"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "{\"id\":\"9\",\"result\":{}}\n{\"event\":\"e\"}\n" {
		t.Fatalf("written %q", out.String())
	}
	if err := c.Close(); err != nil || !rw.closed {
		t.Fatalf("close: %v %v", err, rw.closed)
	}
}

func TestA3ConnReadErrors(t *testing.T) {
	c := NewConn(&a3RW{Reader: strings.NewReader("not json\n{\"id\":\"ok\"}\n"), Writer: io.Discard})
	if _, err := c.Read(); err == nil || !strings.Contains(err.Error(), "decode message") {
		t.Fatalf("garbage: %v", err)
	}
	// The stream resynchronises on the next line.
	if m, err := c.Read(); err != nil || m.ID != "ok" {
		t.Fatalf("after garbage: %+v %v", m, err)
	}

	boom := errors.New("boom")
	c = NewConn(&a3RW{Reader: io.MultiReader(strings.NewReader("{\"id\":\"a\"}\n"), a3ErrReader{boom}), Writer: io.Discard})
	if m, err := c.Read(); err != nil || m.ID != "a" {
		t.Fatalf("before error: %+v %v", m, err)
	}
	if _, err := c.Read(); !errors.Is(err, boom) {
		t.Fatalf("underlying error: %v", err)
	}

	if err := NewConn(&a3RW{Reader: strings.NewReader(""), Writer: a3ErrWriter{boom}}).Write(Message{ID: "x"}); !errors.Is(err, boom) {
		t.Fatalf("write error: %v", err)
	}
}

func TestA3ConnLargeMessage(t *testing.T) {
	// Frames can be much larger than bufio's default 64KB token size.
	big := strings.Repeat("x", 2<<20)
	var buf bytes.Buffer
	w := NewConn(&a3RW{Reader: strings.NewReader(""), Writer: &buf})
	if err := w.Write(Message{Event: EventPaneFrame, Data: Marshal(Frame{Lines: []string{big}})}); err != nil {
		t.Fatal(err)
	}
	r := NewConn(&a3RW{Reader: &buf, Writer: io.Discard})
	m, err := r.Read()
	if err != nil {
		t.Fatal(err)
	}
	var f Frame
	if err := json.Unmarshal(m.Data, &f); err != nil || len(f.Lines) != 1 || len(f.Lines[0]) != len(big) {
		t.Fatalf("large frame: %v", err)
	}
}

type a3ErrReader struct{ err error }

func (r a3ErrReader) Read([]byte) (int, error) { return 0, r.err }

type a3ErrWriter struct{ err error }

func (w a3ErrWriter) Write([]byte) (int, error) { return 0, w.err }

func TestA3ConnConcurrentWrites(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	w, r := NewConn(a), NewConn(b)

	const writers, each = 8, 50
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := w.Write(Message{ID: strings.Repeat("i", 100), Method: "m", Params: Marshal(map[string]int{"g": g, "i": i})}); err != nil {
					t.Error(err)
					return
				}
			}
		}(g)
	}
	go func() { wg.Wait(); a.Close() }()

	last := map[int]int{}
	n := 0
	for {
		m, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("interleaved write corrupted the stream: %v", err)
		}
		var p map[string]int
		if err := json.Unmarshal(m.Params, &p); err != nil {
			t.Fatal(err)
		}
		if prev, ok := last[p["g"]]; ok && p["i"] != prev+1 {
			t.Fatalf("writer %d out of order: %d after %d", p["g"], p["i"], prev)
		}
		last[p["g"]] = p["i"]
		n++
	}
	if n != writers*each {
		t.Fatalf("read %d messages, want %d", n, writers*each)
	}
}
