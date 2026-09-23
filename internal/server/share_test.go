package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/detect"
	"github.com/Amitgb14/conch/internal/pane"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/sessions"
)

// shareFixture is an isolated server with a git checkout holding one Claude
// session and one Codex session.
func shareFixture(t *testing.T) (s *Server, home, work string) {
	t.Helper()
	home = a5IsolateEnv(t)
	s, dir := a5Server(t)
	work = filepath.Join(dir, "work")
	a5GitRepo(t, work)
	var err error
	if s.adapters, err = adapter.New("/usr/local/bin/conch", s.configDir); err != nil { // Run sets these up
		t.Fatal(err)
	}

	claude := filepath.Join(home, ".claude", "projects", claudeProjectName(work), "c1.jsonl")
	os.MkdirAll(filepath.Dir(claude), 0o755)
	os.WriteFile(claude, []byte(strings.Join([]string{
		`{"type":"user","cwd":"` + work + `","gitBranch":"main","timestamp":"2026-09-14T10:00:00Z","message":{"role":"user","content":"Why does deploy fail on arm64?"}}`,
		`{"type":"assistant","cwd":"` + work + `","message":{"content":[{"type":"text","text":"The cross compiler is missing."}]}}`,
		`{"type":"ai-title","aiTitle":"Deploy on arm64"}`,
	}, "\n")+"\n"), 0o644)

	rollout := filepath.Join(home, ".codex", "sessions", "2026", "09", "14", "rollout-x1.jsonl")
	os.MkdirAll(filepath.Dir(rollout), 0o755)
	os.WriteFile(rollout, []byte(strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"x1","cwd":"` + work + `"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"tidy the README"}}`,
	}, "\n")+"\n"), 0o644)
	return s, home, work
}

func claudeProjectName(dir string) string {
	b := []byte(dir)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

func TestSearchSessions(t *testing.T) {
	s, _, work := shareFixture(t)

	for _, q := range []string{"", "   "} {
		if _, perr := s.searchSessions(proto.SessionSearchParams{Dir: work, Query: q}); perr == nil || perr.Code != proto.ErrBadRequest {
			t.Errorf("query %q: %v", q, perr)
		}
	}
	if _, perr := s.searchSessions(proto.SessionSearchParams{Query: "x"}); perr == nil || perr.Code != proto.ErrBadRequest {
		t.Errorf("no project or dir: %v", perr)
	}
	if _, perr := s.searchSessions(proto.SessionSearchParams{ProjectID: "r404", Query: "x"}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Errorf("unknown project: %v", perr)
	}

	res, perr := s.searchSessions(proto.SessionSearchParams{Dir: work, Query: "compiler"})
	if perr != nil || len(res.Sessions) != 1 || res.Sessions[0].ID != "c1" || !strings.Contains(res.Sessions[0].Snippet, "cross compiler") ||
		res.Sessions[0].Branch != "main" {
		t.Fatalf("content match: %+v %v", res, perr)
	}
	res, _ = s.searchSessions(proto.SessionSearchParams{Dir: work, Query: "arm64 deploy"})
	if len(res.Sessions) != 1 || res.Sessions[0].Snippet != "" {
		t.Fatalf("title match: %+v", res)
	}
	res, _ = s.searchSessions(proto.SessionSearchParams{Dir: work, Query: "codex"})
	if len(res.Sessions) != 1 || res.Sessions[0].Agent != "codex" {
		t.Fatalf("agent match: %+v", res)
	}
	res, _ = s.searchSessions(proto.SessionSearchParams{Dir: work, Query: "zebra"})
	if res.Sessions == nil || len(res.Sessions) != 0 {
		t.Fatalf("no match must be an empty list: %+v", res)
	}

	// An interrupted run with nothing saved never matches.
	s.runs.mu.Lock()
	s.runs.interrupted = append(s.runs.interrupted, agentRun{Agent: "gemini", Dir: work, Started: time.Now(), Seen: time.Now()})
	s.runs.mu.Unlock()
	res, _ = s.searchSessions(proto.SessionSearchParams{Dir: work, Query: "interrupted"})
	if len(res.Sessions) != 0 {
		t.Fatalf("interrupted run matched: %+v", res)
	}
	res, _ = s.searchSessions(proto.SessionSearchParams{Dir: work, Query: "e", Limit: 1})
	if len(res.Sessions) != 1 {
		t.Fatalf("limit: %+v", res)
	}
}

// agentPane registers a running pane that the tracker takes for agent,
// running script.
func agentPane(t *testing.T, s *Server, id, agent, dir, script string) *entry {
	t.Helper()
	p, err := pane.Start(pane.Options{ID: id, Command: []string{"/bin/sh", "-c", script}, Cwd: dir, Cols: 200, Rows: 20})
	if err != nil {
		t.Fatal(err)
	}
	e := newEntry(p, dir, detect.NewTracker(s.manifests, agent), nil)
	s.mu.Lock()
	s.panes[id] = e
	s.order = append(s.order, id)
	s.mu.Unlock()
	if agent != "" {
		a5WaitFor(t, agent+" detected", func() bool {
			s.observe(e)
			return e.info().Agent != nil
		})
	}
	return e
}

func TestShareSessionErrors(t *testing.T) {
	s, _, work := shareFixture(t)
	code := func(p proto.SessionShareParams, want string) {
		t.Helper()
		if _, perr := s.shareSession(p); perr == nil || perr.Code != want {
			t.Errorf("%+v: %v, want %s", p, perr, want)
		}
	}
	code(proto.SessionShareParams{Agent: "claude", Dir: work, To: "codex"}, proto.ErrBadRequest)                         // no ID
	code(proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: work}, proto.ErrBadRequest)                            // no target
	code(proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: work, To: "codex", PaneID: "p1"}, proto.ErrBadRequest) // both
	code(proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: work + "-gone", To: "codex"}, proto.ErrBadRequest)     // removed worktree
	code(proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: work, To: "aider"}, proto.ErrBadRequest)               // unknown agent
	code(proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: work, PaneID: "p404"}, proto.ErrNotFound)              // unknown pane
	code(proto.SessionShareParams{Agent: "claude", ID: "nope", Dir: work, To: "codex"}, proto.ErrNotFound)               // no such session
	code(proto.SessionShareParams{Agent: "gemini", ID: "c1", Dir: work, To: "codex"}, proto.ErrNotFound)                 // wrong agent

	shell := agentPane(t, s, "p1", "", work, "sleep 30")
	code(proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: work, PaneID: "p1"}, proto.ErrBadRequest) // not an agent
	shell.p.Close()
	a5WaitFor(t, "exit", func() bool { return shell.info().State == proto.PaneExited })
	code(proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: work, PaneID: "p1"}, proto.ErrBadRequest) // exited

	// A session with no real prompt isn't listed, so it can't be shared.
	x3 := filepath.Join(os.Getenv("HOME"), ".codex", "sessions", "2026", "09", "14", "rollout-x3.jsonl")
	os.WriteFile(x3, []byte(`{"type":"session_meta","payload":{"id":"x3","cwd":"`+work+`"}}`+"\n"+
		`{"type":"event_msg","payload":{"type":"user_message","message":"<environment_context>injected</environment_context>"}}`+"\n"), 0o644)
	code(proto.SessionShareParams{Agent: "codex", ID: "x3", Dir: work, To: "claude"}, proto.ErrNotFound) // untitled: not listed

	// A listed session whose messages were all tool calls has nothing to share.
	tools := filepath.Join(os.Getenv("HOME"), ".claude", "projects", claudeProjectName(work), "c2.jsonl")
	os.WriteFile(tools, []byte(`{"type":"user","cwd":"`+work+`","message":{"content":[{"type":"tool_result","content":"out"}]}}`+"\n"+
		`{"type":"ai-title","aiTitle":"Only tools"}`+"\n"), 0o644)
	if _, perr := s.shareSession(proto.SessionShareParams{Agent: "claude", ID: "c2", Dir: work, To: "codex"}); perr == nil ||
		perr.Code != proto.ErrBadRequest || !strings.Contains(perr.Message, "no messages") {
		t.Errorf("no messages: %v", perr)
	}
	if _, err := os.Stat(filepath.Join(work, ".conch")); err == nil {
		t.Fatal("a failed share wrote a handoff")
	}
}

func TestShareSessionToNewAgent(t *testing.T) {
	s, home, work := shareFixture(t)
	// The agent starts through $SHELL -lc '…exec codex …': record that.
	record := filepath.Join(home, "argv")
	sh := filepath.Join(home, "fake-shell")
	os.WriteFile(sh, []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\" > "+record+"\nsleep 30\n"), 0o755)
	t.Setenv("SHELL", sh)

	res, perr := s.shareSession(proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: work, To: "codex", Cols: 100, Rows: 30})
	if perr != nil {
		t.Fatal(perr)
	}
	want := filepath.Join(work, ".conch", "handoff", "claude-c1.md")
	if res.Path != want || res.Pane.ID == "" {
		t.Fatalf("result: %+v", res)
	}
	doc, err := os.ReadFile(want)
	if err != nil || !strings.Contains(string(doc), "# Handoff: Deploy on arm64") || !strings.Contains(string(doc), "The cross compiler is missing.") {
		t.Fatalf("handoff file: %v\n%s", err, doc)
	}
	a5WaitFor(t, "the agent's command line", func() bool {
		b, _ := os.ReadFile(record)
		return strings.Contains(string(b), "codex") && strings.Contains(string(b), ".conch/handoff/claude-c1.md")
	})
	b, _ := os.ReadFile(record)
	if !strings.HasPrefix(string(b), work+"\n") || !strings.Contains(string(b), "earlier Claude Code session") {
		t.Fatalf("started as:\n%s", b)
	}

	// .conch is kept out of git, once.
	exclude := filepath.Join(work, ".git", "info", "exclude")
	if _, perr := s.shareSession(proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: work, To: "codex"}); perr != nil {
		t.Fatal(perr)
	}
	ex, _ := os.ReadFile(exclude)
	if strings.Count(string(ex), ".conch/") != 1 {
		t.Fatalf("exclude:\n%s", ex)
	}
	out, _ := exec.Command("git", "-C", work, "status", "--porcelain", "--untracked-files=all").Output()
	if strings.Contains(string(out), ".conch") {
		t.Fatalf("git sees the handoff: %s", out)
	}
}

func TestShareSessionIntoRunningAgent(t *testing.T) {
	s, _, work := shareFixture(t)
	e := agentPane(t, s, "p7", "claude", work, "stty -echo; exec cat")

	res, perr := s.shareSession(proto.SessionShareParams{Agent: "codex", ID: "x1", Dir: work, PaneID: "p7"})
	if perr != nil {
		t.Fatal(perr)
	}
	if res.Pane.ID != "p7" || res.Path != filepath.Join(work, ".conch", "handoff", "codex-x1.md") {
		t.Fatalf("result: %+v", res)
	}
	// The pane runs in the checkout: the prompt names the file relative to
	// it, typed in and submitted.
	a5WaitFor(t, "the prompt in the pane", func() bool {
		return strings.Contains(strings.Join(e.p.PlainLines(), ""), "saved in .conch/handoff/codex-x1.md")
	})
	if !strings.Contains(strings.Join(e.p.PlainLines(), ""), "earlier Codex session") {
		t.Fatalf("screen: %q", e.p.PlainLines())
	}

	// A pane anywhere else (a subfolder, another worktree) gets the
	// absolute path.
	elsewhere := filepath.Join(work, "sub")
	os.MkdirAll(elsewhere, 0o755)
	e2 := agentPane(t, s, "p8", "claude", elsewhere, "stty -echo; exec cat")
	if _, perr := s.shareSession(proto.SessionShareParams{Agent: "codex", ID: "x1", Dir: work, PaneID: "p8"}); perr != nil {
		t.Fatal(perr)
	}
	a5WaitFor(t, "the absolute path", func() bool {
		return strings.Contains(strings.Join(e2.p.PlainLines(), ""), work+"/.conch/handoff/codex-x1.md")
	})
}

func TestExcludeFromGit(t *testing.T) {
	a5IsolateEnv(t)
	plain := t.TempDir()
	if err := excludeFromGit(plain, ".conch/"); err != nil {
		t.Fatalf("not a checkout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plain, ".git")); err == nil {
		t.Fatal("created .git in a plain folder")
	}

	repo := filepath.Join(t.TempDir(), "r")
	a5GitRepo(t, repo)
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	os.MkdirAll(filepath.Dir(exclude), 0o755)
	os.WriteFile(exclude, []byte("*.log"), 0o644) // no trailing newline
	for i := 0; i < 2; i++ {
		if err := excludeFromGit(repo, ".conch/"); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(exclude); string(b) != "*.log\n.conch/\n" {
		t.Fatalf("exclude: %q", b)
	}

	// A linked worktree shares the main checkout's exclude file.
	wt := filepath.Join(t.TempDir(), "wt")
	cmd := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat", wt)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	os.Remove(exclude)
	if err := excludeFromGit(wt, ".conch/"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exclude); string(b) != ".conch/\n" {
		t.Fatalf("worktree exclude: %q", b)
	}
}

func TestSafeNameAndPrompt(t *testing.T) {
	for in, want := range map[string]string{"ses_01ABC": "ses_01ABC", "a/b\\c d": "a_b_c_d", "": "session", "..": "session", ".": "session", "v1.2-x": "v1.2-x"} {
		if got := safeName(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	if p := handoffPrompt("gemini", "", "x.md"); !strings.Contains(p, "earlier Gemini CLI session.") || !strings.Contains(p, "saved in x.md") {
		t.Errorf("prompt: %q", p)
	}
	if p := handoffPrompt("codex", "busybox", "x.md"); !strings.Contains(p, "earlier Codex session on busybox.") {
		t.Errorf("prompt from another machine: %q", p)
	}
}

func TestShareSessionHandoffUnwritable(t *testing.T) {
	s, _, work := shareFixture(t)
	// .conch exists as a file: the handoff folder can't be made.
	os.WriteFile(filepath.Join(work, ".conch"), []byte("x"), 0o644)
	if _, perr := s.shareSession(proto.SessionShareParams{Agent: "claude", ID: "c1", Dir: work, To: "codex"}); perr == nil ||
		perr.Code != proto.ErrInternal || !strings.Contains(perr.Message, "write the conversation") {
		t.Fatalf("unwritable: %v", perr)
	}
	s.mu.Lock()
	n := len(s.panes)
	s.mu.Unlock()
	if n != 0 {
		t.Fatal("started an agent without its handoff")
	}

	// The handoff file's place is taken by a folder: the rename fails and
	// no temporary file is left behind.
	os.Remove(filepath.Join(work, ".conch"))
	target := filepath.Join(work, handoffDir, "claude-c1.md")
	os.MkdirAll(filepath.Join(target, "x"), 0o755)
	if _, err := writeHandoff(work, sessionFor("claude", "c1"), "doc"); err == nil {
		t.Fatal("rename over a folder: no error")
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind")
	}
}

func sessionFor(agent, id string) sessions.Session { return sessions.Session{Agent: agent, ID: id} }

// A directory named through a symlink finds the sessions recorded under its
// real path, for every session method.
func TestSessionMethodsResolveSymlinks(t *testing.T) {
	s, _, work := shareFixture(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(work, link); err != nil {
		t.Fatal(err)
	}
	call := func(method string, params any) proto.Message {
		t.Helper()
		b, _ := json.Marshal(params)
		res, perr := s.dispatch(nil, proto.Message{ID: "1", Method: method, Params: b})
		out := proto.Message{Result: proto.Marshal(res)}
		if perr != nil {
			out.Error = perr
		}
		return out
	}
	var list proto.SessionList
	msg := call(proto.MethodSessionList, proto.SessionListParams{Dir: link})
	if msg.Error != nil || json.Unmarshal(msg.Result, &list) != nil || len(list.Sessions) != 2 {
		t.Fatalf("list through a symlink: %+v %s", msg.Error, msg.Result)
	}
	msg = call(proto.MethodSessionSearch, proto.SessionSearchParams{Dir: link, Query: "compiler"})
	if msg.Error != nil || json.Unmarshal(msg.Result, &list) != nil || len(list.Sessions) != 1 {
		t.Fatalf("search through a symlink: %+v %s", msg.Error, msg.Result)
	}
	if realDir("") != "" || realDir("/no/such/dir") != "/no/such/dir" {
		t.Fatal("realDir fallbacks")
	}
}

func TestExportSession(t *testing.T) {
	s, _, work := shareFixture(t)
	for _, c := range []struct {
		ref  proto.SessionRef
		code string
	}{
		{proto.SessionRef{Agent: "claude", Dir: work}, proto.ErrBadRequest},                // no ID
		{proto.SessionRef{Agent: "claude", ID: "nope", Dir: work}, proto.ErrNotFound},      // no such session
		{proto.SessionRef{Agent: "gemini", ID: "c1", Dir: work}, proto.ErrNotFound},        // wrong agent
		{proto.SessionRef{Agent: "claude", ID: "c1", Dir: work + "-x"}, proto.ErrNotFound}, // another folder
	} {
		if _, perr := s.exportSession(c.ref); perr == nil || perr.Code != c.code {
			t.Errorf("%+v: %v, want %s", c.ref, perr, c.code)
		}
	}

	res, perr := s.exportSession(proto.SessionRef{Agent: "claude", ID: "c1", Dir: work})
	if perr != nil || res.Name != "claude-c1.md" || !strings.Contains(res.Doc, "# Handoff: Deploy on arm64") ||
		!strings.Contains(res.Doc, "The cross compiler is missing.") {
		t.Fatalf("export: %+v %v", res, perr)
	}
	// Exporting writes nothing: the checkout stays as it was.
	if _, err := os.Stat(filepath.Join(work, ".conch")); err == nil {
		t.Fatal("export wrote a handoff")
	}

	// Through dispatch, as a client on another machine calls it.
	b, _ := json.Marshal(proto.SessionRef{Agent: "codex", ID: "x1", Dir: work})
	out, perr := s.dispatch(nil, proto.Message{ID: "1", Method: proto.MethodSessionExport, Params: b})
	if exp, ok := out.(proto.SessionExport); perr != nil || !ok || exp.Name != "codex-x1.md" || !strings.Contains(exp.Doc, "tidy the README") {
		t.Fatalf("dispatch: %#v %v", out, perr)
	}
	if !slowMethods[proto.MethodSessionExport] {
		t.Fatal("session.export reads transcripts: it must not hold up the connection")
	}
}

// A conversation exported on another machine is written as sent and
// handed to an agent that is told where it came from.
func TestShareSessionFromAnotherMachine(t *testing.T) {
	s, home, _ := shareFixture(t)
	// The receiving checkout has no sessions of its own.
	there := filepath.Join(home, "there")
	a5GitRepo(t, there)
	record := filepath.Join(home, "argv")
	sh := filepath.Join(home, "fake-shell")
	os.WriteFile(sh, []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\" > "+record+"\nsleep 30\n"), 0o755)
	t.Setenv("SHELL", sh)

	doc := "# Handoff: from afar\n\nhello\n"
	res, perr := s.shareSession(proto.SessionShareParams{Agent: "claude", ID: "c9", Dir: there, To: "codex",
		Doc: doc, Name: "claude-c9.md", From: "laptop"})
	if perr != nil {
		t.Fatal(perr)
	}
	want := filepath.Join(there, ".conch", "handoff", "claude-c9.md")
	if b, err := os.ReadFile(want); res.Path != want || err != nil || string(b) != doc {
		t.Fatalf("result %+v, file %q %v", res, b, err)
	}
	a5WaitFor(t, "the agent's command line", func() bool {
		b, _ := os.ReadFile(record)
		return strings.Contains(string(b), ".conch/handoff/claude-c9.md")
	})
	if b, _ := os.ReadFile(record); !strings.Contains(string(b), "earlier Claude Code session on laptop") {
		t.Fatalf("started as:\n%s", b)
	}
	if out, _ := exec.Command("git", "-C", there, "status", "--porcelain", "--untracked-files=all").Output(); strings.Contains(string(out), ".conch") {
		t.Fatalf("git sees the handoff: %s", out)
	}

	// Into a running agent there.
	e := agentPane(t, s, "p7", "claude", there, "stty -echo; exec cat")
	if _, perr := s.shareSession(proto.SessionShareParams{Agent: "codex", ID: "x9", Dir: there, PaneID: "p7", Doc: doc, From: "laptop"}); perr != nil {
		t.Fatal(perr)
	}
	a5WaitFor(t, "the prompt in the pane", func() bool {
		return strings.Contains(strings.Join(e.p.PlainLines(), ""), "saved in .conch/handoff/codex-x9.md")
	})
}

func TestShareSessionDocEdges(t *testing.T) {
	s, _, work := shareFixture(t)
	share := func(p proto.SessionShareParams) (proto.SessionShareResult, *proto.Error) {
		p.Agent, p.Dir, p.PaneID = "claude", work, "p404" // the pane check fails after the doc checks pass
		return s.shareSession(p)
	}
	// The receiving end is still checked before anything is written.
	if _, perr := share(proto.SessionShareParams{ID: "c9", Doc: "x"}); perr == nil || perr.Code != proto.ErrNotFound {
		t.Fatalf("unknown pane: %v", perr)
	}
	if _, err := os.Stat(filepath.Join(work, ".conch")); err == nil {
		t.Fatal("a failed share wrote a handoff")
	}
	// No ID is still refused, even with a document.
	if _, perr := s.shareSession(proto.SessionShareParams{Agent: "claude", Dir: work, To: "codex", Doc: "x"}); perr == nil || perr.Code != proto.ErrBadRequest {
		t.Fatalf("no ID: %v", perr)
	}

	agentPane(t, s, "p1", "claude", work, "stty -echo; exec cat")
	send := func(doc, name string) (proto.SessionShareResult, *proto.Error) {
		return s.shareSession(proto.SessionShareParams{Agent: "claude", ID: "c9", Dir: work, PaneID: "p1", Doc: doc, Name: name})
	}
	// Too long: refused before writing.
	if _, perr := send(strings.Repeat("x", maxSharedDoc+1), "big.md"); perr == nil || perr.Code != proto.ErrBadRequest || !strings.Contains(perr.Message, "too long") {
		t.Fatalf("too long: %v", perr)
	}
	// Exactly the limit is fine.
	if res, perr := send(strings.Repeat("x", maxSharedDoc), "big.md"); perr != nil || filepath.Base(res.Path) != "big.md" {
		t.Fatalf("at the limit: %+v %v", res, perr)
	}
	// A name can't leave the handoff folder.
	for name, want := range map[string]string{"../../evil.md": ".._.._evil.md", "/etc/x": "_etc_x", "..": "session", "": "claude-c9.md"} {
		res, perr := send("doc", name)
		if perr != nil || res.Path != filepath.Join(work, handoffDir, want) {
			t.Errorf("name %q: %+v %v", name, res, perr)
		}
	}
	// The session ID in a default name is made safe too.
	res, perr := s.shareSession(proto.SessionShareParams{Agent: "claude", ID: "a/b", Dir: work, PaneID: "p1", Doc: "doc"})
	if perr != nil || res.Path != filepath.Join(work, handoffDir, "claude-a_b.md") {
		t.Fatalf("unsafe ID: %+v %v", res, perr)
	}
	// An unwritable folder is an internal error.
	os.RemoveAll(filepath.Join(work, ".conch"))
	os.WriteFile(filepath.Join(work, ".conch"), []byte("x"), 0o644)
	if _, perr := send("doc", "x.md"); perr == nil || perr.Code != proto.ErrInternal {
		t.Fatalf("unwritable: %v", perr)
	}
}
