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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/brain"
	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/client"
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
		case proto.MethodProjectClone:
			var p proto.ProjectCloneParams
			json.Unmarshal(msg.Params, &p)
			if strings.Contains(p.URL, "private") {
				return nil, proto.Errorf(proto.ErrBadRequest, "git clone: Authentication failed")
			}
			return proto.ProjectInfo{ID: "cloned", Name: filepath.Base(p.Path), Path: p.Path, Git: true}, nil
		case proto.MethodProjectFiles:
			return proto.ProjectInfo{ID: "notes", Name: "notes", LocalFiles: nil}, nil
		case proto.MethodProjectSetup:
			var p proto.ProjectSetupParams
			json.Unmarshal(msg.Params, &p)
			return proto.ProjectInfo{ID: p.ProjectID, Name: p.ProjectID, Setup: p.Command}, nil
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

	// clone: into a folder named as git names it, in this folder.
	wd, _ := os.Getwd()
	var clp proto.ProjectCloneParams
	if out, err := run("clone", "https://github.com/Amitgb14/conch.git"); err != nil || out != "cloned  conch  "+filepath.Join(wd, "conch")+"\n" {
		t.Fatalf("clone: %q %v", out, err)
	}
	srv.params(t, proto.MethodProjectClone, &clp)
	if clp.URL != "https://github.com/Amitgb14/conch.git" || clp.Path != filepath.Join(wd, "conch") {
		t.Fatalf("clone params %+v", clp)
	}
	// A folder given is made absolute here, not on the server; ~ is the
	// server's to resolve.
	if _, err := run("clone", "git@github.com:a/b.git", "src/b2"); err != nil {
		t.Fatal(err)
	}
	if srv.params(t, proto.MethodProjectClone, &clp); clp.Path != filepath.Join(wd, "src", "b2") {
		t.Fatalf("clone into a relative dir: %+v", clp)
	}
	if _, err := run("clone", "git@github.com:a/b.git", "~/work/b"); err != nil {
		t.Fatal(err)
	}
	if srv.params(t, proto.MethodProjectClone, &clp); clp.Path != "~/work/b" {
		t.Fatalf("clone into ~: %+v", clp)
	}
	// On another machine: the default is its home's ~/NAME, and ~/DIR or
	// an absolute DIR are taken as they are, for its server to resolve; a
	// relative DIR would mean this computer's folder and is refused.
	func() {
		old := machineFlag
		machineFlag = "gpu"
		defer func() { machineFlag = old }()
		rc, err := client.Dial(config.SocketPath(), "test")
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		clone := func(args ...string) error {
			var err error
			a4Capture(t, "", func() { err = projectClone(rc, args) })
			return err
		}
		for _, c := range []struct {
			args []string
			want string
		}{
			{[]string{"https://github.com/Amitgb14/conch.git"}, "~/conch"},
			{[]string{"git@github.com:a/b.git", "~/work/b"}, "~/work/b"},
			{[]string{"git@github.com:a/b.git", "~"}, "~"},
			{[]string{"git@github.com:a/b.git", "/srv/b"}, "/srv/b"},
		} {
			if err := clone(c.args...); err != nil {
				t.Fatalf("clone %v on gpu: %v", c.args, err)
			}
			if srv.params(t, proto.MethodProjectClone, &clp); clp.Path != c.want {
				t.Fatalf("clone %v on gpu: %+v, want %s", c.args, clp, c.want)
			}
		}
		for _, dir := range []string{"src/b", "~bob/b", "./b"} {
			err := clone("git@github.com:a/b.git", dir)
			if err == nil || !strings.Contains(err.Error(), "under ~") || strings.Contains(err.Error(), "-cwd") {
				t.Fatalf("clone into %q on gpu: %v", dir, err)
			}
		}
	}()
	if _, err := run("clone", "https://example.com/private.git"); err == nil || !strings.Contains(err.Error(), "Authentication failed") {
		t.Fatalf("clone failure: %v", err)
	}
	if _, err := run("clone", "https://github.com/"); err == nil || !strings.Contains(err.Error(), "can't tell a folder name") {
		t.Fatalf("clone with no name: %v", err)
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

	out, err = run("setup", "api")
	if err != nil || out != "new worktrees of api run no setup\n" {
		t.Fatalf("setup show: %q %v", out, err)
	}
	if _, err := run("setup", "ghost"); err == nil || !strings.Contains(err.Error(), `no project "ghost"`) {
		t.Fatalf("setup unknown: %v", err)
	}
	out, err = run("setup", "api", "npm", "ci", "&&", "cp", ".env.example", ".env")
	if err != nil || out != "new worktrees of api run, in a terminal of their own:\n  npm ci && cp .env.example .env\n" {
		t.Fatalf("setup set: %q %v", out, err)
	}
	var sp proto.ProjectSetupParams
	srv.params(t, proto.MethodProjectSetup, &sp)
	if sp.ProjectID != "api" || sp.Command != "npm ci && cp .env.example .env" {
		t.Fatalf("setup params %+v", sp)
	}
	sp = proto.ProjectSetupParams{}
	if out, err := run("setup", "-none", "api"); err != nil || out != "new worktrees of api run no setup\n" {
		t.Fatalf("setup -none: %q %v", out, err)
	}
	srv.params(t, proto.MethodProjectSetup, &sp)
	if sp.Command != "" {
		t.Fatalf("setup -none sent %+v", sp)
	}
	// A flag after the ID is a flag, not the command: "-none" was saved as
	// the setup command, to be run in every new worktree.
	sp = proto.ProjectSetupParams{}
	if out, err := run("setup", "api", "-none"); err != nil || out != "new worktrees of api run no setup\n" {
		t.Fatalf("setup ID -none: %q %v", out, err)
	}
	if srv.params(t, proto.MethodProjectSetup, &sp); sp.Command != "" || sp.ProjectID != "api" {
		t.Fatalf("setup ID -none sent %+v", sp)
	}
	// Once the command has begun, what follows is the command's.
	if _, err := run("setup", "api", "make", "-j4", "-none"); err != nil {
		t.Fatal(err)
	}
	if srv.params(t, proto.MethodProjectSetup, &sp); sp.Command != "make -j4 -none" {
		t.Fatalf("setup with flags of its own sent %+v", sp)
	}
	// And -- passes a command that starts with a dash.
	if _, err := run("setup", "api", "--", "-none"); err != nil {
		t.Fatal(err)
	}
	if srv.params(t, proto.MethodProjectSetup, &sp); sp.Command != "-none" {
		t.Fatalf("setup -- -none sent %+v", sp)
	}

	// The same for files: a flag after the ID, or after a pattern, is a flag.
	for _, args := range [][]string{{"files", "notes", "-reset"}, {"files", "-reset", "notes"}} {
		fp = proto.ProjectFilesParams{}
		if _, err := run(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if srv.params(t, proto.MethodProjectFiles, &fp); !fp.Reset || len(fp.Patterns) != 0 || fp.ProjectID != "notes" {
			t.Fatalf("%v sent %+v", args, fp)
		}
	}
	for _, args := range [][]string{{"files", "notes", "-none"}, {"files", "-none", "notes"}} {
		fp = proto.ProjectFilesParams{}
		if _, err := run(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if srv.params(t, proto.MethodProjectFiles, &fp); fp.Reset || len(fp.Patterns) != 0 || fp.ProjectID != "notes" {
			t.Fatalf("%v sent %+v", args, fp)
		}
	}
	fp = proto.ProjectFilesParams{}
	if _, err := run("files", "notes", ".env", "--", "-weird", "-none"); err != nil {
		t.Fatal(err)
	}
	if srv.params(t, proto.MethodProjectFiles, &fp); fp.Reset || strings.Join(fp.Patterns, " ") != ".env -weird -none" {
		t.Fatalf("files -- sent %+v", fp)
	}

	if _, err := run("rm", "api"); err != nil {
		t.Fatal(err)
	}
	var ref proto.ProjectRef
	if srv.params(t, proto.MethodProjectRemove, &ref); ref.ID != "api" {
		t.Fatalf("rm %+v", ref)
	}

	for args, want := range map[string]string{
		"add":                  "usage: conch project add PATH",
		"add a b":              "usage: conch project add PATH",
		"create":               "usage: conch project create",
		"create -bad x":        "flag provided but not defined",
		"clone":                "usage: conch project clone URL [DIR]",
		"clone a b c":          "usage: conch project clone URL [DIR]",
		"clone -x":             "usage: conch project clone URL [DIR]",
		"files":                "usage: conch project files",
		"files -what":          "flag provided but not defined",
		"setup":                "usage: conch project setup",
		"setup -none a b":      "usage: conch project setup",
		"setup -what":          "flag provided but not defined",
		"setup a -what":        "flag provided but not defined",
		"setup -none":          "usage: conch project setup",
		"setup a -none b":      "usage: conch project setup",
		"files -none":          "usage: conch project files",
		"files a -what":        "flag provided but not defined",
		"files a b -none":      "usage: conch project files",
		"files a -reset -none": "usage: conch project files",
		"rm":                   "usage: conch project rm ID",
		"frobnicate":           `unknown project subcommand "frobnicate"`,
		"remove too many":      "usage: conch project rm ID",
	} {
		if _, err := run(strings.Fields(args)...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("project %s: %v, want %q", args, err, want)
		}
	}

	// A server from before setup existed is named, with what to do.
	srv.setHello(func(int) proto.HelloResult {
		h := currentHello(0)
		h.Capabilities = slices.DeleteFunc(slices.Clone(h.Capabilities), func(c string) bool { return c == proto.CapWorktreeSetup })
		return h
	})
	if _, err := run("setup", "api", "make"); err == nil || !strings.Contains(err.Error(), "predates worktree setup") {
		t.Fatalf("old server: %v", err)
	}
	srv.setHello(currentHello)

	projects.Set(nil)
	if out, err := run("list"); err != nil || out != "no projects\n" {
		t.Fatalf("empty ls: %q %v", out, err)
	}

	// Server errors surface.
	srv.setHandle(func(proto.Message, *proto.Conn) (any, *proto.Error) { return nil, proto.Errorf("boom", "broken") })
	for _, args := range [][]string{{"ls"}, {"create", "x"}, {"files", "x"}, {"files", "-reset", "x"}, {"setup", "x"}, {"setup", "x", "make"}, {"rm", "x"}} {
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
			return proto.ProjectInfo{ID: "proj1", Git: true}, nil
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
		return proto.ProjectInfo{ID: "proj1", Git: true}, nil
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

// A task in a folder that isn't a git repository starts its agents there
// with the prompt: no branch, no worktree, and nothing asked of git.
func TestA4TaskFromAnIssue(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	git := true
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodProjectAdd:
			return proto.ProjectInfo{ID: "proj1", Name: "api", Git: git}, nil
		case proto.MethodTaskCreate:
			return proto.PaneInfo{ID: "p5", Cwd: "/src/wt", Branch: "api/issue-12-login"}, nil
		}
		return nil, nil
	})
	t.Chdir(t.TempDir())
	var err error
	out, _ := a4Capture(t, "", func() { err = runTask([]string{"-issue", "12"}) })
	if err != nil || out != "p5  /src/wt  api/issue-12-login\n" {
		t.Fatalf("task -issue: %q %v", out, err)
	}
	var tp proto.TaskCreateParams
	srv.params(t, proto.MethodTaskCreate, &tp)
	if tp.Issue != 12 || tp.Prompt != "" || tp.Branch != "" {
		t.Fatalf("params %+v", tp)
	}
	// Words after it are added to the issue; several attempts number from it.
	a4Capture(t, "", func() { err = runTask([]string{"-issue", "12", "-n", "2", "keep", "it", "small"}) })
	srv.params(t, proto.MethodTaskCreate, &tp)
	if err != nil || tp.Prompt != "keep it small" || !strings.Contains(tp.Branch, "issue-12") {
		t.Fatalf("with words: %+v %v", tp, err)
	}
	if _, _ = a4Capture(t, "", func() { err = runTask([]string{"-issue", "-3"}) }); err == nil {
		t.Fatal("a negative issue was taken")
	}
	git = false
	if a4Capture(t, "", func() { err = runTask([]string{"-issue", "12"}) }); err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("no git: %v", err)
	}
	git = true
	srv.setHello(func(int) proto.HelloResult {
		h := currentHello(0)
		h.Capabilities = slices.DeleteFunc(slices.Clone(h.Capabilities), func(c string) bool { return c == proto.CapIssues })
		return h
	})
	if a4Capture(t, "", func() { err = runTask([]string{"-issue", "12"}) }); err == nil || !strings.Contains(err.Error(), "predates tasks from issues") {
		t.Fatalf("old server: %v", err)
	}
}

func TestA4TaskWithoutGit(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	var creates a4Var[[]proto.PaneCreateParams]
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodProjectAdd:
			return proto.ProjectInfo{ID: "notes", Path: "/src/notes"}, nil
		case proto.MethodPaneCreate:
			var p proto.PaneCreateParams
			json.Unmarshal(msg.Params, &p)
			creates.Set(append(creates.Get(), p))
			if p.Agent == "gemini" {
				return nil, proto.Errorf(proto.ErrBadRequest, "gemini is not installed")
			}
			return proto.PaneInfo{ID: fmt.Sprintf("p%d", len(creates.Get())), Cwd: p.Cwd}, nil
		case proto.MethodTaskCreate:
			return nil, proto.Errorf(proto.ErrBadRequest, "task.create must not be called")
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{{ID: "p9", Name: "planner", State: proto.PaneRunning},
				{ID: "p8", Name: "old", State: proto.PaneExited}}}, nil
		}
		return nil, nil
	})
	work := t.TempDir()
	t.Chdir(work)

	var err error
	out, errOut := a4Capture(t, "", func() { err = runTask([]string{"-agent", "codex", "plan", "a", "cli"}) })
	got := creates.Get()
	if err != nil || out != "p1  "+work+"\n" || errOut != "" || len(got) != 1 {
		t.Fatalf("plain task: %q %q %v %+v", out, errOut, err, got)
	}
	if p := got[0]; p.Agent != "codex" || p.Prompt != "plan a cli" || p.Cwd != work || p.NoProject || p.Cols != 120 {
		t.Fatalf("params %+v", p)
	}

	// Several attempts share the folder, and say so; one failing leaves
	// the others running.
	creates.Set(nil)
	out, errOut = a4Capture(t, "", func() { err = runTask([]string{"-cwd", "/src/notes", "-agent", "claude,gemini", "-n", "3", "plan"}) })
	if err == nil || !strings.Contains(err.Error(), "1 of 3 attempts could not start") {
		t.Fatalf("attempts: %v", err)
	}
	if !strings.Contains(errOut, "/src/notes is not a git repository, so all 3 attempts work in the same folder") ||
		!strings.Contains(errOut, "conch: gemini: ") || !strings.Contains(errOut, "gemini is not installed") || strings.Count(out, "\n") != 2 {
		t.Fatalf("attempts output: %q %q", out, errOut)
	}
	var agents []string
	for _, p := range creates.Get() {
		agents = append(agents, p.Agent+"@"+p.Cwd)
	}
	if strings.Join(agents, " ") != "claude@/src/notes gemini@/src/notes claude@/src/notes" {
		t.Fatalf("agents: %v", agents)
	}

	// A branch needs a repository; nothing starts.
	creates.Set(nil)
	for _, args := range [][]string{{"-branch", "b", "p"}, {"-base", "main", "p"}} {
		if err := runTask(args); err == nil || !strings.Contains(err.Error(), "-branch and -base need a git repository, and "+work+" is not one") {
			t.Fatalf("%v: %v", args, err)
		}
	}
	// -name names the one pane; a name a running pane holds is refused
	// here, as pane.create won't, and one an exited pane held is free.
	creates.Set(nil)
	if _, _ = a4Capture(t, "", func() { err = runTask([]string{"-name", "reviewer", "p"}) }); err != nil {
		t.Fatalf("-name: %v", err)
	}
	if got := creates.Get(); len(got) != 1 || got[0].Name != "reviewer" {
		t.Fatalf("-name params %+v", got)
	}
	if _, _ = a4Capture(t, "", func() { err = runTask([]string{"-name", "old", "p"}) }); err != nil {
		t.Fatalf("a name only an exited pane held: %v", err)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-name", "planner", "p"}, `pane p9 is already named "planner"`},
		{[]string{"-name", "x", "-agent", "claude,codex", "p"}, "-name names one pane, and this starts 2"},
	} {
		creates.Set(nil)
		if err := runTask(c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v: %v", c.args, err)
		}
		if n := len(creates.Get()); n != 0 {
			t.Fatalf("%v still started %d", c.args, n)
		}
	}
	creates.Set(nil)
	// A single attempt that fails is the command's error.
	if err := runTask([]string{"-agent", "gemini", "p"}); err == nil || !strings.Contains(err.Error(), "gemini is not installed") {
		t.Fatalf("single failure: %v", err)
	}
	if n := len(creates.Get()); n != 1 {
		t.Fatalf("%d panes asked for, want only the failing one", n)
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
	_, errOut = a4Capture(t, "n\n", func() { err = runAsk([]string{"fix"}) })
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

// conch agent sync says what it would do, does it with -apply, and puts it
// back with -undo. The words matter: this is the only place the plan is
// read before anything is written.
func TestA4AgentSync(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		var p proto.AgentSyncParams
		json.Unmarshal(msg.Params, &p)
		res := proto.AgentSyncResult{Dir: p.Dir, From: p.From, To: []string{"codex", "gemini"},
			Notes: []string{"instructions from CLAUDE.md"},
			Changes: []proto.SyncChange{
				{Agent: "codex", Kind: proto.SyncInstructions, Name: "AGENTS.md", Path: "AGENTS.md", Action: proto.SyncCreate, Detail: "a copy of CLAUDE.md"},
				{Agent: "codex", Kind: proto.SyncSkill, Name: "review", Path: ".agents/skills/review", Action: proto.SyncLink},
				{Agent: "gemini", Kind: proto.SyncMCP, Name: "paid", Action: proto.SyncSkip, Detail: "conch does not copy secrets"},
			}}
		switch {
		case p.Undo:
			res.Undone, res.Undo = true, "20260926-101500"
			res.Changes = []proto.SyncChange{{Kind: proto.SyncInstructions, Name: "AGENTS.md", Path: "AGENTS.md", Action: proto.SyncRemove, Done: true}}
		case p.Apply:
			for i := range res.Changes {
				res.Changes[i].Done = res.Changes[i].Action != proto.SyncSkip
			}
			res.Applied, res.Undo = true, "20260926-101500"
		}
		return res, nil
	})
	dir := t.TempDir()
	t.Chdir(dir)

	var err error
	out, _ := a4Capture(t, "", func() { err = runAgent([]string{"sync"}) })
	if err != nil {
		t.Fatal(err)
	}
	var p proto.AgentSyncParams
	srv.params(t, proto.MethodAgentSync, &p)
	if p.Dir != dir || p.From != "claude" || p.Apply || p.Undo || len(p.To) != 0 {
		t.Fatalf("sync params %+v", p)
	}
	for _, want := range []string{dir + " · claude → codex, gemini", "instructions from CLAUDE.md",
		"AGENTS.md", "create", "a copy of CLAUDE.md", "does not copy secrets",
		"2 changes; run it again with -apply to make them"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "✓") {
		t.Fatalf("a plan claimed to have done something:\n%s", out)
	}

	// -apply, and only the named agents.
	out, _ = a4Capture(t, "", func() { err = runAgent([]string{"sync", "-from", "codex", "-to", "claude, gemini", "-apply"}) })
	if err != nil {
		t.Fatal(err)
	}
	srv.params(t, proto.MethodAgentSync, &p)
	if !p.Apply || p.From != "codex" || strings.Join(p.To, "|") != "claude|gemini" {
		t.Fatalf("apply params %+v", p)
	}
	if !strings.Contains(out, "✓ codex") || !strings.Contains(out, "undo with: conch agent sync -undo -stamp 20260926-101500") {
		t.Fatalf("apply output:\n%s", out)
	}

	// -undo
	out, _ = a4Capture(t, "", func() { err = runAgent([]string{"sync", "-undo", "-stamp", "20260926-101500"}) })
	if err != nil {
		t.Fatal(err)
	}
	srv.params(t, proto.MethodAgentSync, &p)
	if !p.Undo || p.Stamp != "20260926-101500" {
		t.Fatalf("undo params %+v", p)
	}
	if !strings.Contains(out, "put sync 20260926-101500 back") || !strings.Contains(out, "remove") {
		t.Fatalf("undo output:\n%s", out)
	}

	// Nothing to do reads as nothing to do.
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		return proto.AgentSyncResult{Dir: dir, From: "claude", To: []string{"codex"},
			Changes: []proto.SyncChange{{Agent: "codex", Kind: proto.SyncSkill, Name: "review", Action: proto.SyncSame, Detail: "already read from .agents/skills"}}}, nil
	})
	out, _ = a4Capture(t, "", func() { err = runAgent([]string{"sync"}) })
	if err != nil || !strings.Contains(out, "nothing to do: every agent already has it") {
		t.Fatalf("nothing to do: %q %v", out, err)
	}

	// Too many arguments, and a server too old to do it at all.
	if err := runAgent([]string{"sync", "a", "b"}); err == nil || !strings.Contains(err.Error(), "usage: conch agent sync") {
		t.Fatalf("two directories: %v", err)
	}
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		var caps []string
		for _, c := range h.Capabilities {
			if c != proto.CapAgentSync {
				caps = append(caps, c)
			}
		}
		h.Capabilities = caps
		return h
	})
	if err := runAgent([]string{"sync"}); err == nil || !strings.Contains(err.Error(), "too old to sync agent setup") {
		t.Fatalf("an old server: %v", err)
	}
}

// conch agent sync -user asks for the setup in your home, and says so.
func TestA4AgentSyncUser(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		var p proto.AgentSyncParams
		json.Unmarshal(msg.Params, &p)
		return proto.AgentSyncResult{Dir: "/Users/x", From: p.From, To: []string{"codex"},
			Changes: []proto.SyncChange{{Agent: "codex", Kind: proto.SyncInstructions, Name: "AGENTS.md",
				Path: "~/.codex/AGENTS.md", Action: proto.SyncCreate, Detail: "a copy of ~/.claude/CLAUDE.md"}}}, nil
	})
	t.Chdir(t.TempDir())

	var err error
	out, _ := a4Capture(t, "", func() { err = runAgent([]string{"sync", "-user"}) })
	if err != nil {
		t.Fatal(err)
	}
	var p proto.AgentSyncParams
	srv.params(t, proto.MethodAgentSync, &p)
	if !p.User || p.From != "claude" || p.Apply {
		t.Fatalf("params %+v", p)
	}
	for _, want := range []string{"~/.codex/AGENTS.md", "a copy of ~/.claude/CLAUDE.md", "run it again with -apply"} {
		if !strings.Contains(out, want) {
			t.Fatalf("out lacks %q:\n%s", want, out)
		}
	}
	// -user -undo puts your own back, not a checkout's.
	a4Capture(t, "", func() { err = runAgent([]string{"sync", "-user", "-undo"}) })
	srv.params(t, proto.MethodAgentSync, &p)
	if !p.User || !p.Undo {
		t.Fatalf("undo params %+v", p)
	}
	// A server that knows sync but not the user half says which it lacks.
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		var caps []string
		for _, c := range h.Capabilities {
			if c != proto.CapAgentSyncUser {
				caps = append(caps, c)
			}
		}
		h.Capabilities = caps
		return h
	})
	if err := runAgent([]string{"sync", "-user"}); err == nil || !strings.Contains(err.Error(), proto.CapAgentSyncUser) {
		t.Fatalf("an older server: %v", err)
	}
	// …while a checkout sync still works there.
	if _, _ = a4Capture(t, "", func() { err = runAgent([]string{"sync"}) }); err != nil {
		t.Fatalf("a checkout sync on that server: %v", err)
	}
}

// conch agent library edits the library through the server and plans,
// applies and undoes it.
func TestA4AgentLibrary(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	var mu sync.Mutex
	lib := proto.Library{Servers: []proto.LibraryServer{}, Skills: []proto.LibrarySkill{}}
	agents := []string{"claude", "codex", "gemini", "opencode", "devin"}
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		mu.Lock()
		defer mu.Unlock()
		switch msg.Method {
		case proto.MethodAgentLibrary:
			var p proto.AgentLibraryParams
			json.Unmarshal(msg.Params, &p)
			res := proto.AgentLibraryResult{Agents: agents}
			if p.Set != nil {
				lib = *p.Set
			}
			if p.Import == "claude" {
				lib.Servers = append(lib.Servers, proto.LibraryServer{Name: "imported", Command: "x", Agents: []string{"claude"}})
				res.Imported, res.Skipped = []string{"imported"}, []string{"paid: its environment holds a value"}
			}
			res.Library = lib
			for _, s := range lib.Servers {
				for _, a := range s.Agents {
					res.Cells = append(res.Cells, proto.LibraryCell{Kind: proto.SyncMCP, Name: s.Name, Agent: a, State: "pending"})
				}
			}
			return res, nil
		case proto.MethodLibraryApply:
			var p proto.LibraryApplyParams
			json.Unmarshal(msg.Params, &p)
			res := proto.AgentSyncResult{Dir: "~", From: "library", To: agents, Applied: p.Apply, Undone: p.Undo,
				Changes: []proto.SyncChange{{Agent: "codex", Kind: proto.SyncMCP, Name: "gh", Path: "~/.codex/config.toml",
					Action: proto.SyncCreate, Detail: "stdio · npx", Done: p.Apply}}}
			if p.Apply {
				res.Undo = "20260928-010203"
			}
			return res, nil
		}
		return nil, nil
	})
	run := func(args ...string) (string, error) {
		t.Helper()
		var err error
		out, _ := a4Capture(t, "", func() { err = runAgent(append([]string{"library"}, args...)) })
		return out, err
	}

	out, err := run()
	if err != nil || !strings.Contains(out, "the library is empty") {
		t.Fatalf("empty: %v\n%s", err, out)
	}
	out, err = run("add", "-to", "codex,devin", "-env", "GITHUB_TOKEN=${GITHUB_TOKEN}", "gh", "--", "npx", "-y", "server-github")
	if err != nil {
		t.Fatal(err)
	}
	if got := lib.Servers[0]; got.Command != "npx" || strings.Join(got.Args, " ") != "-y server-github" ||
		got.Env["GITHUB_TOKEN"] != "${GITHUB_TOKEN}" || strings.Join(got.Agents, ",") != "codex,devin" {
		t.Fatalf("added %+v", got)
	}
	if !strings.Contains(out, "gh") || !strings.Contains(out, "+") || !strings.Contains(out, "library plan") {
		t.Fatalf("add output:\n%s", out)
	}
	// A URL is an http server; -sse says which kind; the same name replaces.
	if _, err := run("add", "-sse", "-header", "Authorization=Bearer ${T}", "gh", "https://x.example/mcp"); err != nil {
		t.Fatal(err)
	}
	if got := lib.Servers; len(got) != 1 || got[0].URL != "https://x.example/mcp" || got[0].Transport != "sse" ||
		got[0].Headers["Authorization"] != "Bearer ${T}" || len(got[0].Agents) != 5 {
		t.Fatalf("replaced %+v", got)
	}
	// -plain marks a token written out as the person's choice; without it
	// nothing is marked.
	if _, err := run("add", "-plain", "-header", "Authorization=Bearer tok-1", "gh", "https://x.example/mcp"); err != nil {
		t.Fatal(err)
	}
	if got := lib.Servers[0]; !got.Plain || got.Headers["Authorization"] != "Bearer tok-1" {
		t.Fatalf("plain %+v", got)
	}
	if _, err := run("add", "-header", "Authorization=Bearer ${T}", "gh", "https://x.example/mcp"); err != nil {
		t.Fatal(err)
	}
	if lib.Servers[0].Plain {
		t.Fatal("marked plain without -plain")
	}
	// conch's own tools in one action: nothing to type, and twice is once.
	if _, err := run("mcp"); err != nil {
		t.Fatalf("library mcp: %v", err)
	}
	if _, err := run("mcp", "-to", "codex"); err != nil {
		t.Fatalf("library mcp -to: %v", err)
	}
	mu.Lock()
	var conch []proto.LibraryServer
	for _, sv := range lib.Servers {
		if sv.Name == "conch" {
			conch = append(conch, sv)
		}
	}
	mu.Unlock()
	if len(conch) != 1 {
		t.Fatalf("%d entries called conch, want 1: %+v", len(conch), conch)
	}
	if conch[0].Command != "conch" || strings.Join(conch[0].Args, " ") != "mcp" {
		t.Errorf("conch entry runs %q %v", conch[0].Command, conch[0].Args)
	}
	if strings.Join(conch[0].Agents, ",") != "codex" {
		t.Errorf("-to codex gave it to %v", conch[0].Agents)
	}
	if _, err := run("mcp", "extra"); err == nil {
		t.Error("library mcp takes no arguments, but accepted one")
	}
	if _, err := run("rm", "conch"); err != nil {
		t.Fatalf("rm conch: %v", err)
	}

	if _, err := run("skill", "-to", "claude", "tide", "~/skills/tide"); err != nil {
		t.Fatal(err)
	}
	if got := lib.Skills; len(got) != 1 || got[0].Path != "~/skills/tide" {
		t.Fatalf("skill %+v", got)
	}
	if _, err := run("off", "gh", "claude,gemini"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lib.Servers[0].Agents, ","); got != "codex,opencode,devin" {
		t.Fatalf("off: %s", got)
	}
	if _, err := run("on", "tide", "devin"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lib.Skills[0].Agents, ","); got != "claude,devin" {
		t.Fatalf("on: %s", got)
	}
	if _, err := run("rm", "tide"); err != nil || len(lib.Skills) != 0 {
		t.Fatalf("rm: %v %+v", err, lib.Skills)
	}
	if _, err := run("rm", "nothing"); err == nil || !strings.Contains(err.Error(), "no server or skill called nothing") {
		t.Fatalf("rm unknown: %v", err)
	}
	out, err = run("import", "claude")
	if err != nil || !strings.Contains(out, "imported imported") || !strings.Contains(out, "left out paid") {
		t.Fatalf("import: %v\n%s", err, out)
	}

	out, err = run("plan")
	var ap proto.LibraryApplyParams
	srv.params(t, proto.MethodLibraryApply, &ap)
	if err != nil || ap.Apply || !strings.Contains(out, "conch agent library apply makes them") {
		t.Fatalf("plan: %v %+v\n%s", err, ap, out)
	}
	out, err = run("apply")
	srv.params(t, proto.MethodLibraryApply, &ap)
	if err != nil || !ap.Apply || !strings.Contains(out, "conch agent library undo -stamp 20260928-010203") {
		t.Fatalf("apply: %v %+v\n%s", err, ap, out)
	}
	if _, err = run("undo", "-stamp", "20260928-010203"); err != nil {
		t.Fatal(err)
	}
	srv.params(t, proto.MethodLibraryApply, &ap)
	if !ap.Undo || ap.Stamp != "20260928-010203" {
		t.Fatalf("undo params %+v", ap)
	}

	for _, bad := range [][]string{
		{"add", "gh"}, {"add", "-env", "nope", "gh", "x"}, {"skill", "tide"}, {"on", "gh"}, {"rm"},
		{"import"}, {"plan", "-stamp", "x"}, {"list", "extra"}, {"what"},
	} {
		if _, err := run(bad...); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%v: %v", bad, err)
		}
	}

	// An older server says what it lacks.
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		var caps []string
		for _, c := range h.Capabilities {
			if c != proto.CapAgentLibrary {
				caps = append(caps, c)
			}
		}
		h.Capabilities = caps
		return h
	})
	if _, err := run(); err == nil || !strings.Contains(err.Error(), proto.CapAgentLibrary) {
		t.Fatalf("an older server: %v", err)
	}
}

// A request conch can plan alone never reaches the model; -model and
// no_fast_path send it there anyway.
func TestA4AskFastPath(t *testing.T) {
	dir := a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{
				{ID: "p1", Name: "shell", State: proto.PaneRunning},
				{ID: "p2", Name: "codex", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "codex", State: "blocked"}},
			}}, nil
		}
		return nil, nil
	})
	asked := newA4Var(0)
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Set(asked.Get() + 1)
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"reply":"from the model","actions":[]}`}}}})
	}))
	defer llm.Close()
	cfg := filepath.Join(dir, "config.toml")
	base := fmt.Sprintf("[brain]\nprovider = \"openai\"\nmodel = \"test\"\nbase_url = %q\n", llm.URL+"/v1")
	os.WriteFile(cfg, []byte(base), 0o600)
	t.Setenv("OPENAI_API_KEY", "")

	var err error
	out, errOut := a4Capture(t, "", func() { err = runAsk([]string{"-n", "tell", "codex", "to", "run", "the", "tests"}) })
	if err != nil || out != "  1. Send to codex on this computer: Run the tests\n" || !strings.Contains(errOut, "planned without the model") || asked.Get() != 0 {
		t.Fatalf("fast: %v %q %q asked %d", err, out, errOut, asked.Get())
	}
	out, _ = a4Capture(t, "", func() { err = runAsk([]string{"what's", "waiting?"}) })
	if err != nil || out != "1 agent is waiting for you: codex on this computer.\n" || asked.Get() != 0 {
		t.Fatalf("question: %v %q asked %d", err, out, asked.Get())
	}
	// Run without asking: the send goes to the server.
	if _, _ = a4Capture(t, "", func() { err = runAsk([]string{"-y", "show", "codex"}) }); err != nil || asked.Get() != 0 {
		t.Fatalf("run: %v", err)
	}

	out, errOut = a4Capture(t, "", func() { err = runAsk([]string{"-n", "-model", "show", "codex"}) })
	if err != nil || asked.Get() != 1 || out != "from the model\n" || !strings.Contains(errOut, "thinking (openai)") {
		t.Fatalf("-model: %v %q %q asked %d", err, out, errOut, asked.Get())
	}
	os.WriteFile(cfg, []byte(base+"no_fast_path = true\n"), 0o600)
	if _, _ = a4Capture(t, "", func() { err = runAsk([]string{"-n", "show", "codex"}) }); err != nil || asked.Get() != 2 {
		t.Fatalf("no_fast_path: %v asked %d", err, asked.Get())
	}
}

// -suggest is the Mac app's list: what to ask, from the server's panes,
// with no model set up and none asked; -pane says which agent "this" is.
func TestA4AskSuggest(t *testing.T) {
	dir := a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{
				{ID: "p1", Name: "shell", State: proto.PaneRunning},
				{ID: "p2", Name: "codex", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "codex", State: "blocked"}},
				{ID: "p3", Name: "claude", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "claude", State: "working"}},
			}}, nil
		}
		return nil, nil
	})
	// A provider that could not plan anything: suggesting does not need one.
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("[brain]\nprovider = \"openai\"\n"), 0o600)
	t.Setenv("OPENAI_API_KEY", "")

	var err error
	out, _ := a4Capture(t, "", func() { err = runAsk([]string{"-suggest", "-pane", "p3", "show shell", "fix the tests"}) })
	if err != nil {
		t.Fatal(err)
	}
	var got askSuggestions
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if got.Provider != "openai" || !got.Fast || got.Context != "pane p3 on machine local" ||
		got.Counts != (brain.Counts{Agents: 2, Waiting: 1, Working: 1}) || got.Summary != "2 agents · 1 waiting · 1 working" {
		t.Fatalf("head: %+v", got)
	}
	var texts []string
	for _, s := range got.Suggestions {
		texts = append(texts, s.Text)
	}
	want := "what is waiting for me?|show codex|tell claude to …|tell everyone to …|what is working?|fix the tests|show shell"
	if strings.Join(texts, "|") != want {
		t.Fatalf("suggestions\n got %q\nwant %q", strings.Join(texts, "|"), want)
	}
	if s := got.Suggestions[len(got.Suggestions)-1]; !s.Recent || s.Note != "instant" {
		t.Fatalf("recent: %+v", s)
	}
	if strings.Contains(strings.Join(srv.methods(), ","), proto.MethodPaneSendText) {
		t.Fatal("suggesting ran something")
	}

	// Earlier requests come after --, as the app passes them, so one
	// that starts with a dash is a request and not a flag.
	out, _ = a4Capture(t, "", func() { err = runAsk([]string{"-suggest", "--", "-y everything"}) })
	if err != nil || !strings.Contains(out, `"text": "-y everything"`) {
		t.Fatalf("after --: %v\n%s", err, out)
	}

	// No agents, nothing asked before: still a list, never null.
	srv.setHandle(func(proto.Message, *proto.Conn) (any, *proto.Error) { return nil, nil })
	out, _ = a4Capture(t, "", func() { err = runAsk([]string{"-suggest"}) })
	if err != nil || !strings.Contains(out, `"summary": "no agents running"`) || !strings.Contains(out, `"suggestions": [`) {
		t.Fatalf("empty: %v\n%s", err, out)
	}
	os.WriteFile(cfg, []byte("[brain]\nprovider = \"openai\"\nno_fast_path = true\n"), 0o600)
	out, _ = a4Capture(t, "", func() { err = runAsk([]string{"-suggest"}) })
	if err != nil || !strings.Contains(out, `"fast": false`) || strings.Contains(out, "instant") {
		t.Fatalf("no fast path: %v\n%s", err, out)
	}
	// Without -suggest a request is still required.
	if err := runAsk([]string{"-pane", "p3"}); err == nil || !strings.Contains(err.Error(), "-pane ID") {
		t.Fatalf("no request: %v", err)
	}
	// A server that fails is said.
	srv.setHandle(func(proto.Message, *proto.Conn) (any, *proto.Error) { return nil, proto.Errorf("x", "no panes today") })
	if _, _ = a4Capture(t, "", func() { err = runAsk([]string{"-suggest"}) }); err == nil || !strings.Contains(err.Error(), "no panes today") {
		t.Fatalf("server failure: %v", err)
	}
}

// A library skill from GitHub is fetched by the machine's server and saved
// with where it came from; one of several must be named; an older server,
// or a folder without a name, is refused.
// -plain against a server from before it is refused, saying what to do,
// rather than sent and refused as a token written out.
func TestA4LibraryPlainOlderServer(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		h.Capabilities = slices.DeleteFunc(slices.Clone(h.Capabilities), func(c string) bool { return c == proto.CapLibraryPlain })
		return h
	})
	saved := false
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodAgentLibrary {
			var p proto.AgentLibraryParams
			json.Unmarshal(msg.Params, &p)
			saved = saved || p.Set != nil
			return proto.AgentLibraryResult{Agents: []string{"claude"}}, nil
		}
		return nil, nil
	})
	var err error
	a4Capture(t, "", func() {
		err = runAgent([]string{"library", "add", "-plain", "-header", "Authorization=Bearer tok-1", "gh", "https://x.example/mcp"})
	})
	if err == nil || !strings.Contains(err.Error(), "reload") || saved {
		t.Fatalf("older server: %v, saved %v", err, saved)
	}
}

func TestA4LibrarySkillFromGitHub(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	var mu sync.Mutex
	lib := proto.Library{Servers: []proto.LibraryServer{}, Skills: []proto.LibrarySkill{}}
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		mu.Lock()
		defer mu.Unlock()
		switch msg.Method {
		case proto.MethodLibraryFetch:
			var p proto.LibraryFetchParams
			json.Unmarshal(msg.Params, &p)
			if p.Source == "acme/many" {
				return proto.LibraryFetchResult{Source: "acme/many", Found: []string{"acme/many/skills/pdf", "acme/many/skills/xlsx"}}, nil
			}
			name := p.Name
			if name == "" {
				name = "pdf"
			}
			return proto.LibraryFetchResult{Name: name, Path: "/home/x/.config/conch/library/skills/" + name, Source: p.Source}, nil
		case proto.MethodAgentLibrary:
			var p proto.AgentLibraryParams
			json.Unmarshal(msg.Params, &p)
			if p.Set != nil {
				lib = *p.Set
			}
			return proto.AgentLibraryResult{Agents: []string{"claude", "codex"}, Library: lib}, nil
		}
		return nil, nil
	})
	run := func(args ...string) (string, error) {
		t.Helper()
		var err error
		out, _ := a4Capture(t, "", func() { err = agentLibrary(args) })
		return out, err
	}

	out, err := run("skill", "-to", "claude", "acme/many/skills/pdf@v1")
	if err != nil || !strings.Contains(out, "fetched acme/many/skills/pdf@v1") {
		t.Fatalf("fetch: %q %v", out, err)
	}
	var fp proto.LibraryFetchParams
	srv.params(t, proto.MethodLibraryFetch, &fp)
	if fp.Source != "acme/many/skills/pdf@v1" || fp.Name != "" {
		t.Fatalf("fetch params %+v", fp)
	}
	mu.Lock()
	got := lib.Skills
	mu.Unlock()
	if len(got) != 1 || got[0].Name != "pdf" || got[0].Source != "acme/many/skills/pdf@v1" ||
		!strings.HasSuffix(got[0].Path, "/skills/pdf") || len(got[0].Agents) != 1 || got[0].Agents[0] != "claude" {
		t.Fatalf("saved %+v", got)
	}
	// Under a name of its own, from a link.
	if _, err := run("skill", "forms", "https://github.com/acme/many/tree/main/skills/pdf"); err != nil {
		t.Fatal(err)
	}
	srv.params(t, proto.MethodLibraryFetch, &fp)
	if fp.Name != "forms" {
		t.Fatalf("named fetch %+v", fp)
	}
	// Several: name one, listing them.
	if _, err := run("skill", "acme/many"); err == nil || !strings.Contains(err.Error(), "acme/many/skills/xlsx") {
		t.Fatalf("several: %v", err)
	}
	// A folder still needs a name, and isn't fetched.
	if _, err := run("skill", "~/skills/review"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("a folder with no name: %v", err)
	}
	n := len(srv.methods())
	if _, err := run("skill", "review", "~/skills/review"); err != nil {
		t.Fatal(err)
	}
	for _, m := range srv.methods()[n:] {
		if m == proto.MethodLibraryFetch {
			t.Fatal("a folder was fetched")
		}
	}

	// A server that can't fetch says so.
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		var caps []string
		for _, c := range h.Capabilities {
			if c != proto.CapLibraryFetch {
				caps = append(caps, c)
			}
		}
		h.Capabilities = caps
		return h
	})
	if _, err := run("skill", "acme/one"); err == nil || !strings.Contains(err.Error(), "can't fetch skills from GitHub") {
		t.Fatalf("old server: %v", err)
	}
}

// conch agent skill -skill picks some of conch's skills, and isn't sent to
// a server that would install them all.
func TestA4AgentSkillPick(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		if msg.Method == proto.MethodAgentSkill {
			return proto.AgentSkillResult{Changes: []proto.SkillChange{}}, nil
		}
		return nil, nil
	})
	var err error
	a4Capture(t, "", func() { err = agentSkill([]string{"-skill", "conch-review,conch-ship", "-apply"}) })
	if err != nil {
		t.Fatal(err)
	}
	var p proto.AgentSkillParams
	srv.params(t, proto.MethodAgentSkill, &p)
	if strings.Join(p.Skills, ",") != "conch-review,conch-ship" || !p.Apply {
		t.Fatalf("params %+v", p)
	}
	srv.setHello(func(n int) proto.HelloResult {
		h := currentHello(n)
		var caps []string
		for _, c := range h.Capabilities {
			if c != proto.CapAgentSkillPick {
				caps = append(caps, c)
			}
		}
		h.Capabilities = caps
		return h
	})
	a4Capture(t, "", func() { err = agentSkill([]string{"-skill", "conch-review"}) })
	if err == nil || !strings.Contains(err.Error(), "only all together") {
		t.Fatalf("old server: %v", err)
	}
	// All of them still works there.
	a4Capture(t, "", func() { err = agentSkill(nil) })
	if err != nil {
		t.Fatalf("all on an old server: %v", err)
	}
}
