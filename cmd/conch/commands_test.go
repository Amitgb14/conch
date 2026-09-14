package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

func TestA4ProjectCommands(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	projects := newA4Var([]proto.ProjectInfo{
		{ID: "api", Name: "api", Path: "/src/api", Base: "main",
			Branches:          []proto.BranchInfo{{Name: "main"}, {Name: "fix"}},
			Worktrees:         []proto.WorktreeInfo{{Branch: "main"}, {Branch: "fix", Status: &proto.GitStatus{Files: 2, Added: 5, Deleted: 1}}, {Branch: "clean", Status: &proto.GitStatus{}}},
			LocalFiles:        []string{".env", ".claude/settings.local.json"},
			LocalFilesDefault: true},
		{ID: "notes", Name: "notes", Path: "/src/notes"},
	})
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodProjectList:
			return proto.ProjectList{Projects: projects.Get()}, nil
		case proto.MethodProjectAdd:
			var p proto.ProjectAddParams
			json.Unmarshal(msg.Params, &p)
			if p.Path == "/bad" {
				return nil, proto.Errorf(proto.ErrBadRequest, "not a directory")
			}
			return proto.ProjectInfo{ID: "new", Name: "new", Path: p.Path}, nil
		case proto.MethodProjectCreate:
			var p proto.ProjectCreateParams
			json.Unmarshal(msg.Params, &p)
			return proto.ProjectInfo{ID: "made", Name: "made", Path: p.Path, Git: p.Git}, nil
		case proto.MethodProjectFiles:
			return proto.ProjectInfo{ID: "notes", Name: "notes", LocalFiles: nil}, nil
		case proto.MethodProjectRemove:
			return nil, nil
		}
		return nil, proto.Errorf(proto.ErrUnknown, "?")
	})
	run := func(args ...string) (string, error) {
		t.Helper()
		var err error
		out, _ := a4Capture(t, "", func() { err = runProject(args) })
		return out, err
	}

	out, err := run()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 || strings.Join(strings.Fields(lines[0]), " ") != "ID NAME BASE BRANCHES WORKTREES PATH" {
		t.Fatalf("ls:\n%s", out)
	}
	if f := strings.Fields(lines[1]); strings.Join(f, " ") != "api api main 2 main,fix(+5-1),clean /src/api" {
		t.Fatalf("api row %q", lines[1])
	}

	if out, err := run("add", "/src/new"); err != nil || out != "new  new  /src/new\n" {
		t.Fatalf("add: %q %v", out, err)
	}
	if _, err := run("add", "/bad"); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("add bad: %v", err)
	}
	if out, err := run("create", "/src/made"); err != nil || out != "made  made  /src/made\n" {
		t.Fatalf("create: %q %v", out, err)
	}
	var cp proto.ProjectCreateParams
	srv.params(t, proto.MethodProjectCreate, &cp)
	if !cp.Git {
		t.Fatal("create defaults to git")
	}
	cp = proto.ProjectCreateParams{}
	if _, err := run("new", "-no-git", "/src/plain"); err != nil {
		t.Fatal(err)
	}
	srv.params(t, proto.MethodProjectCreate, &cp)
	if cp.Git || cp.Path != "/src/plain" {
		t.Fatalf("create -no-git %+v", cp)
	}

	out, err = run("files", "api")
	if err != nil || out != "local files copied into new worktrees of api (default):\n  .env\n  .claude/settings.local.json\n" {
		t.Fatalf("files show: %q %v", out, err)
	}
	if _, err := run("files", "ghost"); err == nil || !strings.Contains(err.Error(), `no project "ghost"`) {
		t.Fatalf("files unknown: %v", err)
	}
	out, err = run("files", "-none", "notes")
	if err != nil || out != "local files copied into new worktrees of notes (custom):\n  (none)\n" {
		t.Fatalf("files -none: %q %v", out, err)
	}
	var fp proto.ProjectFilesParams
	if _, err := run("files", "-reset", "notes"); err != nil {
		t.Fatal(err)
	}
	srv.params(t, proto.MethodProjectFiles, &fp)
	if !fp.Reset || fp.ProjectID != "notes" {
		t.Fatalf("files -reset %+v", fp)
	}
	fp = proto.ProjectFilesParams{}
	if _, err := run("files", "notes", ".env", "*.local"); err != nil {
		t.Fatal(err)
	}
	srv.params(t, proto.MethodProjectFiles, &fp)
	if fp.Reset || strings.Join(fp.Patterns, " ") != ".env *.local" {
		t.Fatalf("files patterns %+v", fp)
	}

	if _, err := run("rm", "api"); err != nil {
		t.Fatal(err)
	}
	var ref proto.ProjectRef
	if srv.params(t, proto.MethodProjectRemove, &ref); ref.ID != "api" {
		t.Fatalf("rm %+v", ref)
	}

	for args, want := range map[string]string{
		"add":             "usage: conch project add PATH",
		"add a b":         "usage: conch project add PATH",
		"create":          "usage: conch project create",
		"create -bad x":   "flag provided but not defined",
		"files":           "usage: conch project files",
		"files -what":     "flag provided but not defined",
		"rm":              "usage: conch project rm ID",
		"frobnicate":      `unknown project subcommand "frobnicate"`,
		"remove too many": "usage: conch project rm ID",
	} {
		if _, err := run(strings.Fields(args)...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("project %s: %v, want %q", args, err, want)
		}
	}

	projects.Set(nil)
	if out, err := run("list"); err != nil || out != "no projects\n" {
		t.Fatalf("empty ls: %q %v", out, err)
	}

	// Server errors surface.
	srv.setHandle(func(proto.Message, *proto.Conn) (any, *proto.Error) { return nil, proto.Errorf("boom", "broken") })
	for _, args := range [][]string{{"ls"}, {"create", "x"}, {"files", "x"}, {"files", "-reset", "x"}, {"rm", "x"}} {
		if _, err := run(args...); err == nil || !strings.Contains(err.Error(), "broken") {
			t.Errorf("project %v with a failing server: %v", args, err)
		}
	}
}

func TestA4Task(t *testing.T) {
	dir := a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodProjectAdd:
			return proto.ProjectInfo{ID: "proj1"}, nil
		case proto.MethodTaskCreate:
			return proto.PaneInfo{ID: "p5", Cwd: "/src/wt", Branch: "fix-tests"}, nil
		}
		return nil, nil
	})
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agents]\ndefault = \"codex\"\n"), 0o600)
	work := t.TempDir()
	t.Chdir(work)

	var err error
	out, _ := a4Capture(t, "", func() { err = runTask([]string{"-branch", "fix-tests", "-base", "dev", "fix", "the", "tests"}) })
	if err != nil || out != "p5  /src/wt  fix-tests\n" {
		t.Fatalf("task: %q %v", out, err)
	}
	var add proto.ProjectAddParams
	srv.params(t, proto.MethodProjectAdd, &add)
	var tp proto.TaskCreateParams
	srv.params(t, proto.MethodTaskCreate, &tp)
	if add.Path != work || tp.ProjectID != "proj1" || tp.Prompt != "fix the tests" || tp.Branch != "fix-tests" || tp.Base != "dev" || tp.Agent != "codex" || tp.Cols != 120 {
		t.Fatalf("task params %+v %+v", add, tp)
	}

	tp = proto.TaskCreateParams{}
	a4Capture(t, "", func() { err = runTask([]string{"-agent", "gemini", "-cwd", "/elsewhere", "go"}) })
	srv.params(t, proto.MethodProjectAdd, &add)
	srv.params(t, proto.MethodTaskCreate, &tp)
	if err != nil || tp.Agent != "gemini" || add.Path != "/elsewhere" {
		t.Fatalf("explicit agent: %+v %+v %v", add, tp, err)
	}

	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodTaskCreate {
			return nil, proto.Errorf(proto.ErrBadRequest, "dirty worktree")
		}
		return proto.ProjectInfo{ID: "proj1"}, nil
	})
	if err := runTask([]string{"x"}); err == nil || !strings.Contains(err.Error(), "dirty worktree") {
		t.Fatalf("task create error: %v", err)
	}
	srv.setHandle(func(proto.Message, *proto.Conn) (any, *proto.Error) {
		return nil, proto.Errorf(proto.ErrBadRequest, "not a git repo")
	})
	if err := runTask([]string{"x"}); err == nil || !strings.Contains(err.Error(), "not a git repo") {
		t.Fatalf("project add error: %v", err)
	}
}

func TestA4AgentStatusAndInstall(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	exitCode := newA4Var(0)
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodAgentStatus:
			return proto.AgentStatusResult{Agents: []proto.AgentAvailability{
				{Name: "claude", Installed: true, Version: "2.1.0", Path: "/bin/claude"},
				{Name: "codex"},
			}}, nil
		case proto.MethodAgentInstall:
			go func() {
				time.Sleep(50 * time.Millisecond)
				// An unrelated pane exits first, then the installer.
				conn.Write(proto.Message{Event: proto.EventPaneExited, Data: proto.Marshal(proto.PaneInfo{ID: "other", ExitCode: 9})})
				conn.Write(proto.Message{Event: proto.EventPaneUpdated, Data: proto.Marshal(proto.PaneInfo{ID: "inst"})})
				conn.Write(proto.Message{Event: proto.EventPaneExited, Data: proto.Marshal(proto.PaneInfo{ID: "inst", ExitCode: exitCode.Get()})})
			}()
			return proto.PaneInfo{ID: "inst"}, nil
		case proto.MethodPaneRead:
			return proto.PaneReadResult{Lines: []string{"installing…", "done", "   ", ""}}, nil
		}
		return nil, nil
	})

	var err error
	out, _ := a4Capture(t, "", func() { err = runAgent([]string{"status"}) })
	if err != nil || out != "claude  installed  2.1.0  /bin/claude\ncodex  not installed  (conch agent install codex)\n" {
		t.Fatalf("status: %q %v", out, err)
	}

	out, errOut := a4Capture(t, "", func() { err = runAgent([]string{"install", "claude"}) })
	if err != nil || out != "installing…\ndone\n" || !strings.Contains(errOut, "installing claude in pane inst") {
		t.Fatalf("install: %q %q %v", out, errOut, err)
	}
	var ip proto.AgentInstallParams
	if srv.params(t, proto.MethodAgentInstall, &ip); ip.Agent != "claude" || ip.Cols != 120 {
		t.Fatalf("install params %+v", ip)
	}
	var closed proto.PaneRef
	if !srv.params(t, proto.MethodPaneClose, &closed) || closed.ID != "inst" {
		t.Fatalf("installer pane not closed: %+v", closed)
	}

	exitCode.Set(4)
	a4Capture(t, "", func() { err = runAgent([]string{"install", "codex"}) })
	if err == nil || err.Error() != "installing codex failed (exit 4)" {
		t.Fatalf("failed install: %v", err)
	}

	// The connection drops before the installer ends.
	srv.setHandle(func(msg proto.Message, conn *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodAgentInstall {
			go func() { time.Sleep(50 * time.Millisecond); srv.stop() }()
			return proto.PaneInfo{ID: "inst"}, nil
		}
		return nil, nil
	})
	a4Capture(t, "", func() { err = runAgent([]string{"install", "claude"}) })
	if err == nil {
		t.Fatal("dropped connection reported success")
	}
}

func TestA4AgentErrors(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(proto.Message, *proto.Conn) (any, *proto.Error) {
		return nil, proto.Errorf(proto.ErrBadRequest, `conch can't install "vim"`)
	})
	for _, args := range [][]string{{"status"}, {"install", "vim"}, {"setup"}} {
		var err error
		a4Capture(t, "", func() { err = runAgent(args) })
		if err == nil || !strings.Contains(err.Error(), "vim") {
			t.Errorf("agent %v: %v", args, err)
		}
	}
}

func TestA4AgentSetup(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	worktree := newA4Var(false)
	copied := newA4Var(false)
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodAgentSetup:
			var p proto.AgentSetupParams
			json.Unmarshal(msg.Params, &p)
			res := proto.AgentSetupResult{Dir: p.Dir, Agents: []proto.AgentSetup{{Agent: "claude", Label: "Claude Code",
				Notes:  []string{"CLAUDE.md is large"},
				Groups: []proto.SetupGroup{{Title: "Instructions", Items: []proto.SetupItem{{Name: "CLAUDE.md", Scope: "project", Detail: "2 KB"}, {Name: ".mcp.json", Scope: "local", Missing: !copied.Get()}}}}}}}
			if worktree.Get() {
				res.Main, res.Worktree, res.ProjectID = "/src/main", p.Dir, "proj"
				res.LocalFiles = []proto.LocalFile{{Path: ".env", State: proto.FileMissing}}
			}
			return res, nil
		case proto.MethodWorktreeFiles:
			copied.Set(true)
			return proto.WorktreeFilesResult{Copied: []string{".env", ".mcp.json"}}, nil
		}
		return nil, nil
	})
	dir := t.TempDir()
	t.Chdir(dir)

	var err error
	out, _ := a4Capture(t, "", func() { err = runAgent([]string{"setup", "-agent", "claude"}) })
	if err != nil {
		t.Fatal(err)
	}
	var p proto.AgentSetupParams
	srv.params(t, proto.MethodAgentSetup, &p)
	if p.Dir != dir || p.Agent != "claude" {
		t.Fatalf("setup params %+v", p)
	}
	want := dir + "\n\n== Claude Code ==\n  ! CLAUDE.md is large\n  Instructions\n     CLAUDE.md                    project   2 KB\n   ✗ .mcp.json                    local     (only in the main checkout)\n"
	if out != want {
		t.Fatalf("setup output\n got %q\nwant %q", out, want)
	}

	// -copy needs a linked worktree.
	a4Capture(t, "", func() { err = runAgent([]string{"setup", "-copy", "sub"}) })
	if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "sub")+" is not a linked worktree") {
		t.Fatalf("copy outside worktree: %v", err)
	}

	worktree.Set(true)
	out, _ = a4Capture(t, "", func() { err = runAgent([]string{"setup", "-copy", "-json", "wt"}) })
	if err != nil || !strings.HasPrefix(out, "copied .env\ncopied .mcp.json\n{") {
		t.Fatalf("copy: %q %v", out, err)
	}
	var res proto.AgentSetupResult
	if json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &res) != nil || res.Main != "/src/main" || res.Agents[0].Groups[0].Items[1].Missing {
		t.Fatalf("json after copy: %q", out)
	}
	var wf proto.WorktreeFilesParams
	if srv.params(t, proto.MethodWorktreeFiles, &wf); wf.ProjectID != "proj" || wf.Path != filepath.Join(dir, "wt") {
		t.Fatalf("worktree files params %+v", wf)
	}

	// With -m the directory is the remote one: left as given.
	machineFlag = "local"
	a4Capture(t, "", func() { err = runAgent([]string{"setup", "rel/dir"}) })
	machineFlag = ""
	p = proto.AgentSetupParams{}
	srv.params(t, proto.MethodAgentSetup, &p)
	if err != nil || p.Dir != "rel/dir" {
		t.Fatalf("machine dir %+v %v", p, err)
	}

	// Copy fails.
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodWorktreeFiles {
			return nil, proto.Errorf("io", "disk full")
		}
		return proto.AgentSetupResult{Main: "/m", Worktree: "/w"}, nil
	})
	a4Capture(t, "", func() { err = runAgent([]string{"setup", "-copy"}) })
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("copy failure: %v", err)
	}
}

func TestA4PrintSetupWorktree(t *testing.T) {
	var b bytes.Buffer
	printSetup(&b, proto.AgentSetupResult{Dir: "/w", Main: "/m",
		LocalFiles: []proto.LocalFile{{Path: ".env", State: "missing"}, {Path: "x", State: "same"}}})
	want := "/w\nworktree of /m\n\nLocal files (from the main checkout)\n  missing  .env\n  same     x\n"
	if b.String() != want {
		t.Fatalf("got %q\nwant %q", b.String(), want)
	}
	b.Reset()
	printSetup(&b, proto.AgentSetupResult{Dir: "/w", Main: "/m"})
	if b.String() != "/w\nworktree of /m\n" {
		t.Fatalf("no local files: %q", b.String())
	}
}

func TestA4Ask(t *testing.T) {
	dir := a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodAgentStatus:
			return proto.AgentStatusResult{Agents: []proto.AgentAvailability{{Name: "claude", Installed: true}}}, nil
		case proto.MethodProjectList:
			return proto.ProjectList{Projects: []proto.ProjectInfo{{ID: "api", Name: "api", Path: "/src/api", Git: true}}}, nil
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{{ID: "p1", Name: "shell", State: proto.PaneRunning}}}, nil
		case proto.MethodTaskCreate:
			return proto.PaneInfo{ID: "p2", Cwd: "/src/api-wt"}, nil
		case proto.MethodPaneSendText:
			return nil, proto.Errorf(proto.ErrNotFound, "pane went away")
		}
		return nil, nil
	})
	plan := `{"reply":"On it.","actions":[` +
		`{"type":"start_task","machine":"local","project":"api","prompt":"fix tests"},` +
		`{"type":"send","machine":"local","pane":"p1","text":"ls"},` +
		`{"type":"focus","machine":"local","pane":"p1"},` +
		`{"type":"close","machine":"local","pane":"nope"}]}`
	prompts := newA4Var([]string(nil))
	planVar := newA4Var(plan)
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if n := len(body.Messages); n > 0 {
			prompts.Set(append(prompts.Get(), body.Messages[n-1].Content))
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": planVar.Get()}}}})
	}))
	defer llm.Close()
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(fmt.Sprintf("[brain]\nprovider = \"openai\"\nmodel = \"test\"\nbase_url = %q\n", llm.URL+"/v1")), 0o600)
	t.Setenv("OPENAI_API_KEY", "")
	work := t.TempDir()
	t.Chdir(work)

	// -n shows the plan only.
	var err error
	out, errOut := a4Capture(t, "", func() { err = runAsk([]string{"-n", "fix", "the", "tests"}) })
	if err != nil {
		t.Fatal(err)
	}
	want := "On it.\n" +
		"  1. Start claude in api on this computer (new branch): fix tests\n" +
		"  2. Send to shell on this computer: ls\n" +
		"  3. Show shell on this computer\n" +
		"  ✗ Close this computer — skipped: unknown pane \"nope\" on this computer\n"
	if out != want || !strings.Contains(errOut, "thinking (openai)") {
		t.Fatalf("plan\n got %q\nwant %q\nstderr %q", out, want, errOut)
	}
	if ps := prompts.Get(); len(ps) != 1 || !strings.Contains(ps[0], "User request: fix the tests") || !strings.Contains(ps[0], "working directory "+work) {
		t.Fatalf("prompt %q", ps)
	}
	if strings.Contains(strings.Join(srv.methods(), ","), proto.MethodTaskCreate) {
		t.Fatal("-n ran actions")
	}

	// Declined at the prompt.
	out, errOut = a4Capture(t, "n\n", func() { err = runAsk([]string{"fix"}) })
	if err != nil || !strings.Contains(errOut, "Run 3 action(s)? [y/N]") || strings.Contains(strings.Join(srv.methods(), ","), proto.MethodTaskCreate) {
		t.Fatalf("declined: %v %q", err, errOut)
	}

	// -y runs them and reports each outcome.
	out, _ = a4Capture(t, "", func() { err = runAsk([]string{"-y", "fix"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"  1. started p2 in /src/api-wt\n", "  2. failed: not_found: pane went away\n", "  3. done\n"} {
		if !strings.Contains(out, line) {
			t.Fatalf("run output lacks %q:\n%s", line, out)
		}
	}
	var tp proto.TaskCreateParams
	if srv.params(t, proto.MethodTaskCreate, &tp); tp.ProjectID != "api" || tp.Agent != "claude" || tp.Prompt != "fix tests" {
		t.Fatalf("task params %+v", tp)
	}

	// A plan with nothing valid runs nothing and asks nothing.
	planVar.Set(`{"reply":"","actions":[{"type":"explode","machine":"local"}]}`)
	out, errOut = a4Capture(t, "", func() { err = runAsk([]string{"x"}) })
	if err != nil || strings.Contains(errOut, "Run ") || !strings.Contains(out, "skipped: unknown action") {
		t.Fatalf("nothing valid: %q %q %v", out, errOut, err)
	}

	// The model answers garbage.
	planVar.Set("I cannot help with that")
	if _, _ = a4Capture(t, "", func() { err = runAsk([]string{"x"}) }); err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("garbage plan: %v", err)
	}

	// A server call fails.
	srv.setHandle(func(proto.Message, *proto.Conn) (any, *proto.Error) { return nil, proto.Errorf("x", "no agents today") })
	if _, _ = a4Capture(t, "", func() { err = runAsk([]string{"x"}) }); err == nil || !strings.Contains(err.Error(), "no agents today") {
		t.Fatalf("server failure: %v", err)
	}
}

func TestA4AskConfigErrors(t *testing.T) {
	dir := a4Env(t)
	cfg := filepath.Join(dir, "config.toml")

	os.WriteFile(cfg, []byte("this is = = not toml"), 0o600)
	if err := runAsk([]string{"hi"}); err == nil {
		t.Fatal("broken config accepted")
	}
	os.WriteFile(cfg, []byte("[brain]\nprovider = \"hal9000\"\n"), 0o600)
	if err := runAsk([]string{"hi"}); err == nil || !strings.Contains(err.Error(), `unknown brain provider "hal9000"`) {
		t.Fatalf("unknown provider: %v", err)
	}
	os.WriteFile(cfg, []byte("[brain]\nprovider = \"openai\"\n"), 0o600)
	if err := runAsk([]string{"hi"}); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("unconfigured provider: %v", err)
	}
	// Configured, but no server can be started.
	os.WriteFile(cfg, []byte("[brain]\nprovider = \"openai\"\nmodel = \"m\"\nbase_url = \"http://127.0.0.1:1/v1\"\n"), 0o600)
	var err error
	a4Capture(t, "", func() { err = runAsk([]string{"hi"}) })
	if err == nil || !strings.Contains(err.Error(), "server exited during startup") {
		t.Fatalf("no server: %v", err)
	}
}

// a4Release serves a release of conch for this platform.
func a4Release(t *testing.T, version string, binary []byte) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "conch_" + version + "/conch", Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg})
	tw.Write(binary)
	tw.Close()
	gz.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	asset := fmt.Sprintf("conch_%s_%s.tar.gz", version, strings.ReplaceAll(buildinfo.Platform(), "/", "_"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			w.Header().Set("Location", "https://github.com/Amitgb14/conch/releases/tag/v"+version)
			w.WriteHeader(http.StatusFound)
		case "/v" + version + "/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
		case "/v" + version + "/" + asset:
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CONCH_RELEASE_URL", srv.URL)
	return srv
}

func TestA4UpdateInProcess(t *testing.T) {
	a4Env(t)
	// Already the latest.
	a4Release(t, proto.Version, []byte("same"))
	var err error
	out, _ := a4Capture(t, "", func() { err = runUpdate(nil) })
	if err != nil || out != "conch "+proto.Version+" is the latest release\n" {
		t.Fatalf("latest: %q %v", out, err)
	}

	// No release published.
	none := httptest.NewServer(http.NotFoundHandler())
	defer none.Close()
	t.Setenv("CONCH_RELEASE_URL", none.URL)
	a4Capture(t, "", func() { err = runUpdate(nil) })
	if err == nil || !strings.Contains(err.Error(), "no published release") {
		t.Fatalf("no release: %v", err)
	}

	// A named version that doesn't exist fails before touching the binary.
	exe, _ := os.Executable()
	before, _ := os.Stat(exe)
	_, errOut := a4Capture(t, "", func() { err = runUpdate([]string{"v9.9.9"}) })
	if err == nil || !strings.Contains(err.Error(), "download conch 9.9.9") || !strings.Contains(errOut, "downloading conch 9.9.9 for "+buildinfo.Platform()) {
		t.Fatalf("missing version: %v %q", err, errOut)
	}
	if after, _ := os.Stat(exe); !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("test binary modified")
	}
}

// TestA4UpdateReplacesBinary runs `conch update` from a copy of the test
// binary, so the file it replaces is the copy.
func TestA4UpdateReplacesBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("copies the test binary")
	}
	a4Env(t)
	exe, _ := os.Executable()
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	copyPath := filepath.Join(dir, "conch")
	if err := os.WriteFile(copyPath, data, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "conch-link")
	os.Symlink(copyPath, link)
	a4Release(t, "7.7.7", []byte("#!/bin/sh\necho new conch\n"))

	// Via a symlink: the real file is replaced, the link kept.
	code, out, errOut := a4RunBinary(t, link, "", "update")
	if code != 0 {
		t.Fatalf("update exit %d: %q %q", code, out, errOut)
	}
	real, _ := filepath.EvalSymlinks(copyPath)
	if !strings.Contains(out, "updated "+real+": "+proto.Version+" → 7.7.7") {
		t.Fatalf("output %q", out)
	}
	if strings.Contains(out, "server reloaded") {
		t.Fatalf("no server was running: %q", out)
	}
	if b, _ := os.ReadFile(copyPath); string(b) != "#!/bin/sh\necho new conch\n" {
		t.Fatalf("binary not replaced: %d bytes", len(b))
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced")
	}
}

func TestA4UpdateReloadsServer(t *testing.T) {
	if testing.Short() {
		t.Skip("copies the test binary")
	}
	a4Env(t)
	exe, _ := os.Executable()
	data, _ := os.ReadFile(exe)
	dir := t.TempDir()
	copyPath := filepath.Join(dir, "conch")
	os.WriteFile(copyPath, data, 0o755)
	a4Release(t, "7.7.8", []byte("new"))

	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodServerReload {
			return nil, proto.Errorf(proto.ErrBadRequest, "cannot exec")
		}
		return nil, nil
	})
	code, out, errOut := a4RunBinary(t, copyPath, "", "update", "7.7.8")
	if code != 0 || !strings.Contains(out, "the server keeps the old build: bad_request: cannot exec") {
		t.Fatalf("refused reload: %d %q %q", code, out, errOut)
	}

	os.WriteFile(copyPath, data, 0o755)
	var reloaded atomic.Bool
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodServerReload {
			reloaded.Store(true)
		}
		return nil, nil
	})
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		if reloaded.Load() {
			h.Started = h.Started.Add(time.Hour)
		}
		return h
	})
	code, out, errOut = a4RunBinary(t, copyPath, "", "update", "v7.7.8")
	if code != 0 || !strings.Contains(out, "server reloaded onto it; panes keep running") || !strings.Contains(out, "conch machine upgrade ID") {
		t.Fatalf("reload: %d %q %q", code, out, errOut)
	}
	var p proto.ServerReloadParams
	if srv.params(t, proto.MethodServerReload, &p); p.Binary != copyPath {
		real, _ := filepath.EvalSymlinks(copyPath)
		if p.Binary != real {
			t.Fatalf("reload binary %q", p.Binary)
		}
	}
}
