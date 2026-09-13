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
