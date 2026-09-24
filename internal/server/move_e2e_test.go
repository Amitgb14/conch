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

// Two servers stand in for a laptop and a remote machine: the project is
// cloned on the remote, then a worktree with commits and uncommitted work
// moves across through a client, as the TUI does it.
func TestMoveWorktreeBetweenServers(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	laptop, proj, repo, wt, origin := harvestFixture(t)
	git(t, repo, "push", "-q", "origin", "main")
	os.WriteFile(filepath.Join(wt, "feat.txt"), []byte("feat\n"), 0o644)
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-q", "-m", "feat")
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("one\nwip\n"), 0o644)
	os.WriteFile(filepath.Join(wt, ".env"), []byte("KEY=1\n"), 0o644)
	os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte(".env\n"), 0o644)

	remote, rdir := startServer(t)
	for _, c := range []*client.Client{laptop, remote} {
		if missing := c.MissingCapabilities([]string{proto.CapWorktreeMove}); len(missing) > 0 {
			t.Fatalf("missing %v", missing)
		}
	}
	var there proto.ProjectInfo
	if err := call(t, remote, proto.MethodProjectClone, proto.ProjectCloneParams{URL: origin, Path: filepath.Join(rdir, "code", "api")}, &there); err != nil {
		t.Fatal(err)
	}

	var info proto.WorktreeMoveInfo
	if err := call(t, laptop, proto.MethodWorktreeDescribe, proto.WorktreeRef{ProjectID: proj.ID, Path: wt}, &info); err != nil {
		t.Fatal(err)
	}
	if info.Branch != "feat" || info.Remote != origin || info.Unstaged != 1 || strings.Join(info.Local, ",") != ".env" {
		t.Fatalf("describe: %+v", info)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := client.NewMove(laptop, remote, proj.ID, wt, there.ID, info.History).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Path, filepath.Join(rdir, "code")) || res.Branch != "feat" {
		t.Fatalf("result: %+v", res)
	}
	if got, want := gitOut(t, res.Path, "rev-parse", "HEAD"), gitOut(t, wt, "rev-parse", "HEAD"); got != want {
		t.Fatalf("head %s, want %s", got, want)
	}
	for name, want := range map[string]string{"feat.txt": "feat\n", "a.txt": "one\nwip\n", ".env": "KEY=1\n"} {
		if b, err := os.ReadFile(filepath.Join(res.Path, name)); err != nil || string(b) != want {
			t.Errorf("%s: %q %v", name, b, err)
		}
	}
	// The remote's project sees the new worktree, and knows where it came from.
	waitProject(t, remote, func(p proto.ProjectInfo) bool {
		for _, w := range p.Worktrees {
			if w.Path == res.Path && w.Branch == "feat" {
				return p.Remote == origin
			}
		}
		return false
	})
	// The laptop's worktree is left as it was.
	if b, _ := os.ReadFile(filepath.Join(wt, "a.txt")); string(b) != "one\nwip\n" {
		t.Fatal("the laptop's worktree changed")
	}
}
