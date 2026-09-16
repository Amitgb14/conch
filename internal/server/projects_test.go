package server_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func startServer(t *testing.T) (*client.Client, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "conch")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	sock := filepath.Join(dir, "s.sock")
	srv := server.New(sock, dir)
	go srv.Run()
	t.Cleanup(func() { srv.Stop(); time.Sleep(100 * time.Millisecond); os.RemoveAll(dir) })
	var c *client.Client
	for i := 0; i < 100; i++ {
		if c, err = client.Dial(sock, "test"); err == nil {
			t.Cleanup(func() { c.Close() })
			return c, dir
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(err)
	return nil, ""
}

// waitProject waits for a project.updated event satisfying ok.
func waitProject(t *testing.T, c *client.Client, ok func(proto.ProjectInfo) bool) proto.ProjectInfo {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m, open := <-c.Events:
			if !open {
				t.Fatalf("connection closed: %v", c.Err())
			}
			var p proto.ProjectInfo
			if m.Event == proto.EventProjectUpdated && json.Unmarshal(m.Data, &p) == nil && ok(p) {
				return p
			}
		case <-timeout:
			t.Fatal("timed out waiting for project update")
		}
	}
}

func TestProjectsAndWorktrees(t *testing.T) {
	c, dir := startServer(t)
	repo := filepath.Join(dir, "api")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// A pane opened inside the repo adds it as a project.
	var info proto.PaneInfo
	err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", "sleep 30"}, Cwd: filepath.Join(repo),
	}, &info)
	if err != nil {
		t.Fatal(err)
	}
	proj := waitProject(t, c, func(p proto.ProjectInfo) bool { return p.Path == repo && len(p.Branches) > 0 })
	if proj.Base != "main" || proj.Branches[0].Name != "main" || len(proj.Worktrees) != 1 {
		t.Fatalf("unexpected project: %+v", proj)
	}

	// A machine-level pane stays out of the project even in its folder.
	var loose proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", "sleep 30"}, Cwd: repo, NoProject: true,
	}, &loose); err != nil {
		t.Fatal(err)
	}
	if loose.ProjectID != "" {
		t.Fatalf("no-project pane joined %q", loose.ProjectID)
	}

	// A worktree for a new branch lands next to the repo.
	var wt proto.WorktreeResult
	if err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "feat/x"}, &wt); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "api.worktrees", "feat-x"); wt.Path != want {
		t.Fatalf("worktree at %s, want %s", wt.Path, want)
	}
	os.WriteFile(filepath.Join(wt.Path, "a.txt"), []byte("one\ntwo\n"), 0o644)
	proj = waitProject(t, c, func(p proto.ProjectInfo) bool {
		for _, w := range p.Worktrees {
			if w.Branch == "feat/x" && w.Status != nil && w.Status.Added == 1 {
				return true
			}
		}
		return false
	})

	var ch proto.Changes
	if err := c.Call(ctx, proto.MethodProjectChanges, proto.ChangesParams{ProjectID: proj.ID, Branch: "feat/x"}, &ch); err != nil {
		t.Fatal(err)
	}
	if ch.Worktree != wt.Path || len(ch.Files) != 1 || ch.Files[0].Path != "a.txt" || ch.Files[0].Added != 1 {
		t.Fatalf("changes: %+v", ch)
	}
	var d proto.DiffResult
	if err := c.Call(ctx, proto.MethodProjectDiff, proto.DiffParams{ProjectID: proj.ID, Branch: "feat/x", File: "a.txt"}, &d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Diff, "+two") {
		t.Fatalf("diff: %s", d.Diff)
	}

	// A dirty worktree is not removed, and the main worktree never is.
	err = c.Call(ctx, proto.MethodWorktreeRemove, proto.WorktreeRemoveParams{ProjectID: proj.ID, Path: wt.Path}, nil)
	if err == nil {
		t.Fatal("dirty worktree was removed")
	}
	err = c.Call(ctx, proto.MethodWorktreeRemove, proto.WorktreeRemoveParams{ProjectID: proj.ID, Path: repo}, nil)
	if err == nil {
		t.Fatal("main worktree was removed")
	}

	// The pane in the repo reports its project and branch.
	var list proto.PaneList
	if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil {
		t.Fatal(err)
	}
	if p := list.Panes[0]; p.ProjectID != proj.ID || p.Branch != "main" {
		t.Fatalf("pane placement: project %q branch %q", p.ProjectID, p.Branch)
	}

	// Rename sticks; an empty name restores the default.
	var renamed proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneRename, proto.PaneRenameParams{ID: info.ID, Name: "server"}, &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "server" || !renamed.CustomName {
		t.Fatalf("rename: %+v", renamed)
	}

	// The catalog survives in projects.json.
	b, err := os.ReadFile(filepath.Join(dir, "projects.json"))
	if err != nil || !strings.Contains(string(b), repo) {
		t.Fatalf("catalog: %s %v", b, err)
	}
}

func TestPullRequestsOnBranches(t *testing.T) {
	gh := filepath.Join(t.TempDir(), "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
cat <<'JSON'
[{"number":7,"title":"Ship it","state":"OPEN","isDraft":false,"url":"https://github.com/o/r/pull/7",
  "headRefName":"main","reviewDecision":"APPROVED","updatedAt":"2026-09-12T10:00:00Z",
  "statusCheckRollup":[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"FAILURE"}]}]
JSON
`), 0o755)
	t.Setenv("CONCH_GH", gh)

	c, dir := startServer(t)
	repo := filepath.Join(dir, "web")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, nil); err != nil {
		t.Fatal(err)
	}
	proj := waitProject(t, c, func(p proto.ProjectInfo) bool {
		return len(p.Branches) > 0 && p.Branches[0].PR != nil
	})
	pr := proj.Branches[0].PR
	if pr.Number != 7 || pr.Checks != "fail" || pr.Review != "APPROVED" || proj.PRStatus != "" {
		t.Fatalf("pr: %+v status %q", pr, proj.PRStatus)
	}
}

func TestBrowseAndCreateProjects(t *testing.T) {
	c, dir := startServer(t)
	os.MkdirAll(filepath.Join(dir, "src", "api", ".git"), 0o755)
	os.MkdirAll(filepath.Join(dir, "src", "Notes"), 0o755)
	os.MkdirAll(filepath.Join(dir, "src", ".hidden"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "file.txt"), []byte("x"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var list proto.FSList
	if err := c.Call(ctx, proto.MethodFSList, proto.FSListParams{Path: filepath.Join(dir, "src")}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 2 || list.Entries[0].Name != "api" || !list.Entries[0].Git || list.Entries[1].Name != "Notes" || list.Parent != dir {
		t.Fatalf("listing: %+v", list)
	}
	if err := c.Call(ctx, proto.MethodFSList, proto.FSListParams{Path: filepath.Join(dir, "src"), Hidden: true}, &list); err != nil || len(list.Entries) != 3 {
		t.Fatalf("hidden listing: %+v %v", list.Entries, err)
	}

	if err := c.Call(ctx, proto.MethodFSMkdir, proto.FSMkdirParams{Path: filepath.Join(dir, "src", "new")}, &list); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, proto.MethodFSMkdir, proto.FSMkdirParams{Path: filepath.Join(dir, "src", "new")}, nil); err == nil {
		t.Fatal("mkdir over an existing folder succeeded")
	}

	var proj proto.ProjectInfo
	target := filepath.Join(dir, "src", "fresh")
	if err := c.Call(ctx, proto.MethodProjectCreate, proto.ProjectCreateParams{Path: target, Git: true}, &proj); err != nil {
		t.Fatal(err)
	}
	if proj.Path != target || !proj.Git {
		t.Fatalf("created: %+v", proj)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		t.Fatal("no git repository created")
	}
	if err := c.Call(ctx, proto.MethodProjectCreate, proto.ProjectCreateParams{Path: target}, nil); err == nil {
		t.Fatal("created a project over an existing folder")
	}
	// The listing now marks it as a project.
	c.Call(ctx, proto.MethodFSList, proto.FSListParams{Path: filepath.Join(dir, "src")}, &list)
	for _, e := range list.Entries {
		if e.Name == "fresh" && !e.Project {
			t.Fatalf("fresh not marked as project: %+v", list.Entries)
		}
	}
}

func TestOhMyZshThemes(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}
	home, _ := os.MkdirTemp("", "h")
	defer os.RemoveAll(home)
	omz := filepath.Join(home, ".oh-my-zsh")
	os.MkdirAll(filepath.Join(omz, "themes"), 0o755)
	os.MkdirAll(filepath.Join(omz, "custom", "themes"), 0o755)
	os.WriteFile(filepath.Join(omz, "oh-my-zsh.sh"), []byte("# stand-in: no omz function\n"), 0o644)
	os.WriteFile(filepath.Join(omz, "themes", "robbyrussell.zsh-theme"), []byte("PROMPT='ROBBY> '\n"), 0o644)
	os.WriteFile(filepath.Join(omz, "themes", "fancy.zsh-theme"), []byte("PROMPT='FANCY> '\n"), 0o644)
	os.WriteFile(filepath.Join(omz, "custom", "themes", "mine.zsh-theme"), []byte("PROMPT='MINE> '\n"), 0o644)
	os.WriteFile(filepath.Join(home, ".zshrc"), []byte(`export ZSH="$HOME/.oh-my-zsh"
ZSH_THEME="robbyrussell"
ZSH_CUSTOM="$ZSH/custom"
source $ZSH/oh-my-zsh.sh
source $ZSH/themes/$ZSH_THEME.zsh-theme
export RC_LOADED=yes
`), 0o644)
	os.WriteFile(filepath.Join(home, ".zlogin"), []byte("export LOGIN_LOADED=yes\n"), 0o644)
	t.Setenv("HOME", home)
	t.Setenv("SHELL", zsh)
	t.Setenv("ZDOTDIR", "")

	c, _ := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var th proto.ShellThemes
	if err := c.Call(ctx, proto.MethodShellThemes, nil, &th); err != nil {
		t.Fatal(err)
	}
	if !th.OMZ || th.Current != "robbyrussell" || strings.Join(th.Themes, ",") != "fancy,mine,robbyrussell" {
		t.Fatalf("themes: %+v", th)
	}

	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Cwd: home, ShellTheme: "fancy", Cols: 200}, &info); err != nil {
		t.Fatal(err)
	}
	c.Call(ctx, proto.MethodPaneSendText, proto.PaneSendTextParams{ID: info.ID, Text: `echo "rc=$RC_LOADED login=$LOGIN_LOADED zdotdir=$ZDOTDIR"`}, nil)
	c.Call(ctx, proto.MethodPaneSendKeys, proto.PaneSendKeysParams{ID: info.ID, Keys: []string{"enter"}}, nil)
	want := "rc=yes login=yes zdotdir=" + home
	deadline := time.Now().Add(10 * time.Second)
	var screen string
	for time.Now().Before(deadline) {
		var r proto.PaneReadResult
		c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &r)
		screen = strings.Join(r.Lines, "") // the pane wraps long lines
		if strings.Contains(screen, want) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(screen, want) || !strings.Contains(screen, "FANCY> ") || strings.Contains(screen, "ROBBY> ") {
		t.Fatalf("want the user's files loaded, ZDOTDIR restored and the fancy prompt:\n%s", screen)
	}
}

// A checkout in a pane's folder changes the branch clients show for it.
// The branch comes from the project, which refreshes on its own, so a pane
// update has to be compared with what was last sent — not with a second
// sample taken in the same breath, which already has the new branch.
func TestPaneBranchFollowsCheckout(t *testing.T) {
	c, dir := startServer(t)
	repo := filepath.Join(dir, "api")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")
	git(t, repo, "branch", "feature")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", "sleep 60"}, Cwd: repo,
	}, &info); err != nil {
		t.Fatal(err)
	}
	waitProject(t, c, func(p proto.ProjectInfo) bool { return p.Path == repo })

	branchOf := func(want string) {
		t.Helper()
		waitEvent(t, c, func(m proto.Message) bool {
			var p proto.PaneInfo
			return m.Event == proto.EventPaneUpdated && json.Unmarshal(m.Data, &p) == nil && p.ID == info.ID && p.Branch == want
		})
		var panes proto.PaneList
		if err := c.Call(ctx, proto.MethodPaneList, nil, &panes); err != nil {
			t.Fatal(err)
		}
		for _, p := range panes.Panes {
			if p.ID == info.ID && p.Branch != want {
				t.Fatalf("pane.list says %q, want %q", p.Branch, want)
			}
		}
	}
	// Nothing else about the pane changes, so only the branch can announce it.
	git(t, repo, "checkout", "-q", "feature")
	branchOf("feature")
	git(t, repo, "checkout", "-q", "main")
	branchOf("main")
}

// pane.redraw clears the screen conch keeps and asks the program to draw
// it again, without sending it a keystroke.
func TestPaneRedraw(t *testing.T) {
	c, _ := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", `printf 'agents orchestrator leftover\rwhat next?'; sleep 30`}, Cols: 60, Rows: 10,
	}, &info); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		t.Helper()
		var res proto.PaneReadResult
		if err := c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: info.ID}, &res); err != nil {
			t.Fatal(err)
		}
		return strings.Join(res.Lines, "\n")
	}
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(read(), "leftover"); {
		if time.Now().After(deadline) {
			t.Fatalf("stale text never appeared:\n%s", read())
		}
		time.Sleep(20 * time.Millisecond)
	}
	var after proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneRedraw, proto.PaneRef{ID: info.ID}, &after); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(), "leftover") {
		t.Fatalf("stale text survived:\n%s", read())
	}
	if after.ID != info.ID || after.State != proto.PaneRunning {
		t.Fatalf("pane after redraw: %+v", after)
	}
	if err := c.Call(ctx, proto.MethodPaneRedraw, proto.PaneRef{ID: "nope"}, nil); err == nil {
		t.Fatal("redrawing an unknown pane")
	}
}
