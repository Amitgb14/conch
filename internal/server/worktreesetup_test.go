package server_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// setupRepo is a repository registered with the server, with HOME and SHELL
// pointed somewhere harmless: setup runs the person's login shell, which
// must not read the developer's own profile.
func setupRepo(t *testing.T) (c *client.Client, dir string, proj proto.ProjectInfo) {
	t.Helper()
	c, dir = startServer(t)
	t.Setenv("HOME", dir)
	t.Setenv("SHELL", "/bin/sh")
	repo := filepath.Join(dir, "api")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj); err != nil {
		t.Fatal(err)
	}
	waitProject(t, c, func(p proto.ProjectInfo) bool { return p.ID == proj.ID && len(p.Branches) > 0 })
	return c, dir, proj
}

func waitFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(b), "end\n") {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never finished", path)
	return ""
}

func paneState(t *testing.T, c *client.Client, id string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var list proto.PaneList
	if err := c.Call(ctx, proto.MethodPaneList, nil, &list); err != nil {
		t.Fatal(err)
	}
	for _, p := range list.Panes {
		if p.ID == id {
			return p.State
		}
	}
	return "gone"
}

func TestWorktreeSetupRunsWithItsPorts(t *testing.T) {
	c, _, proj := setupRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// What the command sees, written where the test can read it. The
	// command has quotes and a pipe in it, as real ones do.
	cmd := `printf '%s|%s|%s|%s\n' "$CONCH_PORT" "$CONCH_PORT_COUNT" "$CONCH_BRANCH" "$CONCH_ROOT" > seen && echo "end" >> seen`
	var info proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectSetup, proto.ProjectSetupParams{ProjectID: proj.ID, Command: "  " + cmd + "\n"}, &info); err != nil {
		t.Fatal(err)
	}
	if info.Setup != cmd {
		t.Fatalf("setup stored as %q, want it trimmed to %q", info.Setup, cmd)
	}

	var wt proto.WorktreeResult
	if err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "feat/a"}, &wt); err != nil {
		t.Fatal(err)
	}
	if wt.Setup == "" {
		t.Fatal("no setup pane reported")
	}
	got := waitFile(t, filepath.Join(wt.Path, "seen"))
	fields := strings.Split(strings.SplitN(got, "\n", 2)[0], "|")
	if len(fields) != 4 || fields[0] == "" || fields[1] != "10" || fields[2] != "feat/a" || fields[3] != proj.Path {
		t.Fatalf("setup saw %q", got)
	}
	port := fields[0]

	// A setup that worked goes away on its own.
	deadline := time.Now().Add(10 * time.Second)
	for paneState(t, c, wt.Setup) == proto.PaneRunning && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if st := paneState(t, c, wt.Setup); st == proto.PaneRunning {
		t.Fatalf("setup pane still %s after it finished", st)
	}

	// The checkout's row says which ports it holds, and every pane started
	// in it is given the same ones.
	waitProject(t, c, func(p proto.ProjectInfo) bool {
		for _, w := range p.Worktrees {
			if w.Path == wt.Path && w.Port != 0 {
				return true
			}
		}
		return false
	})
	var pane proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", `echo "$CONCH_PORT" > again; echo end >> again; sleep 30`}, Cwd: wt.Path,
	}, &pane); err != nil {
		t.Fatal(err)
	}
	if again := waitFile(t, filepath.Join(wt.Path, "again")); !strings.HasPrefix(again, port+"\n") {
		t.Fatalf("a second pane got port %q, want %s", again, port)
	}

	// Another worktree gets a block of its own.
	var other proto.WorktreeResult
	if err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "feat/b"}, &other); err != nil {
		t.Fatal(err)
	}
	if seen := waitFile(t, filepath.Join(other.Path, "seen")); strings.HasPrefix(seen, port+"|") {
		t.Fatalf("two worktrees share port %s", port)
	}
}

func TestWorktreeSetupFailureStaysOpen(t *testing.T) {
	c, _, proj := setupRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Call(ctx, proto.MethodProjectSetup, proto.ProjectSetupParams{ProjectID: proj.ID, Command: "echo end > tried; echo end >> tried; exit 3"}, nil); err != nil {
		t.Fatal(err)
	}
	var wt proto.WorktreeResult
	if err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "broken"}, &wt); err != nil {
		t.Fatal(err)
	}
	waitFile(t, filepath.Join(wt.Path, "tried"))
	// Failed, it leaves a shell behind instead of vanishing with the error.
	time.Sleep(300 * time.Millisecond)
	if st := paneState(t, c, wt.Setup); st != proto.PaneRunning {
		t.Fatalf("failed setup's pane is %s, want it kept running", st)
	}
	var shot proto.PaneReadResult
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := c.Call(ctx, proto.MethodPaneRead, proto.PaneRef{ID: wt.Setup}, &shot); err == nil && strings.Contains(strings.Join(shot.Lines, "\n"), "setup failed (exit 3)") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("screen never said why:\n%s", strings.Join(shot.Lines, "\n"))
}

func TestWorktreeWithoutSetupStartsNothing(t *testing.T) {
	c, _, proj := setupRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var wt proto.WorktreeResult
	if err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "plain"}, &wt); err != nil {
		t.Fatal(err)
	}
	if wt.Setup != "" {
		t.Fatalf("a project with no setup started %s", wt.Setup)
	}
	// Clearing it again: an empty command is "none", not an error.
	var info proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectSetup, proto.ProjectSetupParams{ProjectID: proj.ID, Command: "make"}, &info); err != nil {
		t.Fatal(err)
	}
	var cleared proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectSetup, proto.ProjectSetupParams{ProjectID: proj.ID}, &cleared); err != nil || cleared.Setup != "" {
		t.Fatalf("clearing: %v, %q", err, cleared.Setup)
	}
}

func TestProjectSetupRefusals(t *testing.T) {
	c, dir, _ := setupRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Call(ctx, proto.MethodProjectSetup, proto.ProjectSetupParams{ProjectID: "nope", Command: "make"}, nil); err == nil {
		t.Fatal("an unknown project took a setup")
	}
	plain := filepath.Join(dir, "notes")
	os.MkdirAll(plain, 0o755)
	var p proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: plain}, &p); err != nil {
		t.Fatal(err)
	}
	err := c.Call(ctx, proto.MethodProjectSetup, proto.ProjectSetupParams{ProjectID: p.ID, Command: "make"}, nil)
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("a plain folder took a setup: %v", err)
	}
}
