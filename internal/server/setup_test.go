package server_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func findItem(a proto.AgentSetup, group, name string) *proto.SetupItem {
	for _, g := range a.Groups {
		if g.Title != group {
			continue
		}
		for i := range g.Items {
			if g.Items[i].Name == name {
				return &g.Items[i]
			}
		}
	}
	return nil
}

func TestWorktreeLocalFilesAndSetup(t *testing.T) {
	home := t.TempDir()
	home, _ = filepath.EvalSymlinks(home)
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", home)

	c, dir := startServer(t)
	repo := filepath.Join(dir, "api")
	git(t, dir, "init", "-q", "-b", "main", repo)
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	write(t, filepath.Join(repo, ".gitignore"), ".env\nCLAUDE.local.md\n.claude/settings.local.json\n*.txt\n")
	write(t, filepath.Join(repo, "CLAUDE.md"), "# rules\n")
	write(t, filepath.Join(repo, ".claude/skills/review/SKILL.md"), "---\nname: review\ndescription: >-\n  Review a change\n  carefully.\n---\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")
	write(t, filepath.Join(repo, ".env"), "TOKEN=1\n")
	write(t, filepath.Join(repo, ".claude/settings.local.json"), "{}\n")
	write(t, filepath.Join(repo, "CLAUDE.local.md"), "mine\n")
	write(t, filepath.Join(repo, "notes.txt"), "not copied by default\n")
	write(t, filepath.Join(repo, ".mcp.json"), "{}\n") // matches a default pattern but isn't ignored
	write(t, filepath.Join(home, ".claude.json"), `{"projects":{"`+repo+`":{"hasTrustDialogAccepted":true,"mcpServers":{"db":{"command":"secret-cmd"}}}}}`)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var proj proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj); err != nil {
		t.Fatal(err)
	}
	if !proj.LocalFilesDefault || len(proj.LocalFiles) == 0 {
		t.Fatalf("default local files: %+v", proj)
	}
	waitProject(t, c, func(p proto.ProjectInfo) bool { return p.ID == proj.ID && p.Git && len(p.Worktrees) > 0 })

	var wt proto.WorktreeResult
	if err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "feat"}, &wt); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(wt.Copied, ","); got != ".claude/settings.local.json,.env,CLAUDE.local.md" {
		t.Fatalf("copied %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(wt.Path, ".env")); string(b) != "TOKEN=1\n" {
		t.Fatalf(".env in worktree: %q", b)
	}
	waitProject(t, c, func(p proto.ProjectInfo) bool { return p.ID == proj.ID && len(p.Worktrees) == 2 })
	write(t, filepath.Join(wt.Path, ".env"), "TOKEN=2\n")

	var res proto.AgentSetupResult
	if err := c.Call(ctx, proto.MethodAgentSetup, proto.AgentSetupParams{Dir: wt.Path, Agent: "claude"}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Main != repo || res.Worktree != wt.Path || res.ProjectID != proj.ID {
		t.Fatalf("result: %+v", res)
	}
	states := map[string]string{}
	for _, f := range res.LocalFiles {
		states[f.Path] = f.State
	}
	if states[".env"] != proto.FileDiffers || states["CLAUDE.local.md"] != proto.FileSame || states[".mcp.json"] != proto.FileNotIgnored {
		t.Fatalf("local files: %v", states)
	}
	a := res.Agents[0]
	if it := findItem(a, "Skills", "review"); it == nil || it.Missing || it.Scope != "project" || it.Detail != "Review a change carefully." {
		t.Fatalf("skill: %+v", it)
	}
	if it := findItem(a, "Instructions", "CLAUDE.local.md"); it == nil || it.Scope != "local" || it.Missing {
		t.Fatalf("CLAUDE.local.md: %+v", it)
	}
	db := findItem(a, "MCP servers", "db")
	if db == nil || !db.Missing || strings.Contains(db.Detail+db.Path, "secret-cmd") {
		t.Fatalf("local MCP server should be missing from the worktree: %+v", db)
	}
	if len(a.Notes) == 0 || !strings.Contains(a.Notes[0], "trust") {
		t.Fatalf("notes: %v", a.Notes)
	}

	// Custom patterns, then copying into the existing worktree.
	var custom proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectFiles, proto.ProjectFilesParams{ProjectID: proj.ID, Patterns: []string{"*.txt"}}, &custom); err != nil {
		t.Fatal(err)
	}
	if custom.LocalFilesDefault || strings.Join(custom.LocalFiles, ",") != "*.txt" {
		t.Fatalf("custom files: %+v", custom)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "projects.json")); !strings.Contains(string(b), `"*.txt"`) {
		t.Fatalf("custom patterns not saved: %s", b)
	}
	var cr proto.WorktreeFilesResult
	if err := c.Call(ctx, proto.MethodWorktreeFiles, proto.WorktreeFilesParams{ProjectID: proj.ID, Path: wt.Path}, &cr); err != nil {
		t.Fatal(err)
	}
	if strings.Join(cr.Copied, ",") != "notes.txt" {
		t.Fatalf("copy: %+v", cr)
	}
	var again proto.WorktreeFilesResult
	if err := c.Call(ctx, proto.MethodWorktreeFiles, proto.WorktreeFilesParams{ProjectID: proj.ID, Path: wt.Path}, &again); err != nil || len(again.Copied) != 0 || len(again.Skipped) != 1 {
		t.Fatalf("second copy keeps existing files: %+v %v", again, err)
	}
	if err := c.Call(ctx, proto.MethodWorktreeFiles, proto.WorktreeFilesParams{ProjectID: proj.ID, Path: repo}, nil); err == nil {
		t.Fatal("copying into the main checkout should fail")
	}
	if err := c.Call(ctx, proto.MethodProjectFiles, proto.ProjectFilesParams{ProjectID: proj.ID, Patterns: []string{"../x"}}, nil); err == nil {
		t.Fatal("patterns outside the project should be rejected")
	}
	var reset proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectFiles, proto.ProjectFilesParams{ProjectID: proj.ID, Reset: true}, &reset); err != nil || !reset.LocalFilesDefault {
		t.Fatalf("reset: %+v %v", reset, err)
	}
	// Copied files are ignored, so they don't stop the worktree's removal.
	if err := c.Call(ctx, proto.MethodWorktreeRemove, proto.WorktreeRemoveParams{ProjectID: proj.ID, Path: wt.Path}, nil); err != nil {
		t.Fatalf("remove worktree with local files: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "CLAUDE.local.md")); err != nil {
		t.Fatal("the main checkout's local files must stay")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "projects.json"))
	if strings.Contains(string(b), "local_files") {
		t.Fatalf("default patterns should not be saved: %s", b)
	}
}

// agent.setup.sync over the protocol: the plan writes nothing, applying
// writes each agent's own files, and undoing puts the checkout back.
func TestAgentSyncOverTheProtocol(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", home)
	c, dir := startServer(t)
	repo := filepath.Join(dir, "api")
	git(t, dir, "init", "-q", "-b", "main", repo)
	write(t, filepath.Join(repo, "CLAUDE.md"), "# rules\n\nRun the tests.\n")
	write(t, filepath.Join(repo, ".claude/skills/review/SKILL.md"), "---\nname: review\n---\n")
	write(t, filepath.Join(repo, ".mcp.json"), `{"mcpServers":{"gh":{"command":"npx","args":["gh"]}}}`)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	call := func(p proto.AgentSyncParams) proto.AgentSyncResult {
		t.Helper()
		var res proto.AgentSyncResult
		if err := c.Call(ctx, proto.MethodAgentSync, p, &res); err != nil {
			t.Fatal(err)
		}
		return res
	}

	// A plan: changes, and nothing on disk.
	plan := call(proto.AgentSyncParams{Dir: repo, From: "claude"})
	if len(plan.Changes) == 0 || plan.Undo != "" || len(plan.To) != 3 {
		t.Fatalf("plan %+v", plan)
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("the plan wrote AGENTS.md")
	}

	// Applying, for one agent only.
	done := call(proto.AgentSyncParams{Dir: repo, From: "claude", To: []string{"codex"}, Apply: true})
	if !done.Applied || done.Undo == "" || len(done.Undos) != 1 {
		t.Fatalf("applied %+v", done)
	}
	wrote := 0
	for _, ch := range done.Changes {
		if ch.Error != "" {
			t.Fatalf("change failed: %+v", ch)
		}
		if ch.Done {
			wrote++
		}
	}
	if wrote != 3 { // AGENTS.md, the skill, the server
		t.Fatalf("wrote %d of %+v", wrote, done.Changes)
	}
	if got, err := os.ReadFile(filepath.Join(repo, "AGENTS.md")); err != nil || !strings.Contains(string(got), "Run the tests.") {
		t.Fatalf("AGENTS.md %q %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(repo, ".codex", "config.toml")); err != nil || !strings.Contains(string(got), "[mcp_servers.gh]") {
		t.Fatalf("config.toml %q %v", got, err)
	}

	// Undoing the last one.
	back := call(proto.AgentSyncParams{Dir: repo, Undo: true})
	if !back.Undone || back.Undo != done.Undo || len(back.Undos) != 0 {
		t.Fatalf("undone %+v", back)
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("AGENTS.md is still there")
	}

	// What it refuses: an agent it doesn't know, a folder that isn't one,
	// and a checkout with nothing to copy.
	for _, p := range []proto.AgentSyncParams{
		{Dir: repo, From: "devin"},
		{Dir: filepath.Join(repo, "nope"), From: "claude"},
		{Dir: dir, From: "claude"},
		{Dir: repo, Undo: true},
	} {
		if err := c.Call(ctx, proto.MethodAgentSync, p, nil); err == nil {
			t.Fatalf("%+v was accepted", p)
		}
	}
}

// The record a sync leaves behind is kept out of git, as the handoff
// documents beside it are.
func TestAgentSyncKeepsItsRecordOutOfGit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(k, "")
	}
	c, dir := startServer(t)
	repo := filepath.Join(dir, "api")
	git(t, dir, "init", "-q", "-b", "main", repo)
	write(t, filepath.Join(repo, "CLAUDE.md"), "# rules\n")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var res proto.AgentSyncResult
	if err := c.Call(ctx, proto.MethodAgentSync, proto.AgentSyncParams{Dir: repo, From: "claude", To: []string{"codex"}, Apply: true}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Undo == "" {
		t.Fatalf("nothing was written: %+v", res)
	}
	exclude, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(exclude), ".conch/") {
		t.Fatalf("exclude %q %v", exclude, err)
	}
	// The record itself is the user's own business, not the machine's.
	st, err := os.Stat(filepath.Join(repo, ".conch", "agent-sync"))
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("the record folder is %v (%v)", st.Mode().Perm(), err)
	}
}

// Starting an agent writes the files it is launched with, even when the
// folder they live in has gone since the server started: somebody clearing
// ~/.config in a sandbox left Claude failing with "Settings file not
// found: …/conch/claude-settings.json".
func TestAgentFilesComeBackBeforeALaunch(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	c, dir := startServer(t)
	settings := filepath.Join(dir, "claude-settings.json")
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("the server did not write it at start-up: %v", err)
	}
	// Everything conch keeps there, gone.
	for _, name := range []string{"claude-settings.json", "gemini-defaults.json", "opencode-conch.js"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var info proto.PaneInfo
	// claude is not installed here, so the pane's shell will fail — what
	// matters is that conch wrote the file before launching anything.
	err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Agent: "claude", Cwd: dir, NoProject: true, Cols: 80, Rows: 24,
	}, &info)
	if err != nil {
		t.Fatalf("pane.create: %v", err)
	}
	t.Cleanup(func() { _ = c.Call(context.Background(), proto.MethodPaneClose, proto.PaneRef{ID: info.ID}, nil) })
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("the settings file did not come back: %v", err)
	}
	if !strings.Contains(string(b), "report claude-hook") || !strings.Contains(string(b), "statusLine") {
		t.Fatalf("it came back without conch's own settings:\n%s", b)
	}
	// And the pane really was launched with that file.
	var list proto.PaneList
	if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range list.Panes {
		if p.ID == info.ID && strings.Contains(strings.Join(p.Command, " "), settings) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the pane's command does not name the settings file: %+v", list.Panes)
	}
}

// A conversation handed to an agent keeps the name of the work it
// continues: a tree row saying "codex" beside one saying "fix the flaky
// login test" hides that they are the same thing.
func TestSharedPaneKeepsTheName(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	c, dir := startServer(t)
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	share := func(name string) proto.PaneInfo {
		t.Helper()
		var res proto.SessionShareResult
		err := c.Call(ctx, proto.MethodSessionShare, proto.SessionShareParams{
			Agent: "claude", ID: "c1", Dir: work, To: "claude", Doc: "# what happened\n\nsomething",
			Name: "claude-c1.md", From: "laptop", PaneName: name, Cols: 80, Rows: 24,
		}, &res)
		if err != nil {
			t.Fatalf("share: %v", err)
		}
		t.Cleanup(func() { _ = c.Call(context.Background(), proto.MethodPaneClose, proto.PaneRef{ID: res.Pane.ID}, nil) })
		return res.Pane
	}

	if got := share("fix the flaky login test").Name; got != "fix the flaky login test" {
		t.Fatalf("the pane is called %q", got)
	}
	// Nothing to go on: conch's own default, the agent's name.
	if got := share("").Name; got != "claude" {
		t.Fatalf("with no name: %q", got)
	}
	// A name from somewhere else is tidied rather than trusted: one line,
	// and short enough for a row.
	long := strings.Repeat("ab ", 40)
	got := share("two\nlines").Name
	if got != "two lines" {
		t.Fatalf("a name with a newline: %q", got)
	}
	if got := share(long).Name; len([]rune(got)) > 60 || strings.HasSuffix(got, " ") {
		t.Fatalf("a long name came out %d runes: %q", len([]rune(got)), got)
	}
}
