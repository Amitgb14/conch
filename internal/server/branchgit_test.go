package server_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestBranchGitRunsInTheWorktree(t *testing.T) {
	c, proj, repo, wt, _ := harvestFixture(t)
	id := proj.ID
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("changed\n"), 0o644)

	// No commands: only where the branch is and what is unfinished there.
	var res proto.BranchGitResult
	if err := call(t, c, proto.MethodBranchGit, proto.BranchGitParams{ProjectID: id, Branch: "feat"}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Worktree != wt || res.Output != "" || res.InProgress != "" || res.ExitCode != 0 {
		t.Fatalf("state only: %+v", res)
	}

	// Commands run one after another in the branch's worktree, not the main one.
	res = proto.BranchGitResult{}
	params := proto.BranchGitParams{ProjectID: id, Branch: "feat", Commands: [][]string{
		{"add", "--all"}, {"commit", "-q", "-m", "panel commit"}, {"log", "-1", "--format=%s"}}}
	if err := call(t, c, proto.MethodBranchGit, params, &res); err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Output, "$ git commit -q -m 'panel commit'\n") ||
		!strings.HasSuffix(res.Output, "$ git log -1 --format=%s\npanel commit\n") {
		t.Fatalf("chained: %+v", res)
	}
	if gitOut(t, wt, "log", "-1", "--format=%s") != "panel commit" || gitOut(t, repo, "log", "-1", "--format=%s") != "init" {
		t.Fatal("the commit landed in the wrong checkout")
	}

	// The first failure stops the chain, and is a result rather than an error.
	res = proto.BranchGitResult{}
	params.Commands = [][]string{{"checkout", "no-such"}, {"commit", "--allow-empty", "-m", "never"}}
	if err := call(t, c, proto.MethodBranchGit, params, &res); err != nil {
		t.Fatal(err)
	}
	if res.ExitCode == 0 || !strings.Contains(res.Output, "no-such") || strings.Contains(res.Output, "never") {
		t.Fatalf("failure: %+v", res)
	}
	if gitOut(t, wt, "log", "-1", "--format=%s") != "panel commit" {
		t.Fatal("ran past the failure")
	}
}

func TestBranchGitReportsAConflict(t *testing.T) {
	c, proj, repo, wt, _ := harvestFixture(t)
	git(t, wt, "config", "user.name", "t")
	git(t, wt, "config", "user.email", "t@example.com")
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("feat\n"), 0o644)
	git(t, wt, "commit", "-q", "-am", "feat")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("main\n"), 0o644)
	git(t, repo, "commit", "-q", "-am", "main")

	var res proto.BranchGitResult
	params := proto.BranchGitParams{ProjectID: proj.ID, Branch: "feat", Commands: [][]string{{"rebase", "main"}}}
	if err := call(t, c, proto.MethodBranchGit, params, &res); err != nil {
		t.Fatal(err)
	}
	if res.ExitCode == 0 || res.InProgress != "rebase" || !strings.Contains(res.Output, "a.txt") {
		t.Fatalf("conflict: %+v", res)
	}
	res = proto.BranchGitResult{}
	params.Commands = [][]string{{"rebase", "--abort"}}
	if err := call(t, c, proto.MethodBranchGit, params, &res); err != nil || res.ExitCode != 0 || res.InProgress != "" {
		t.Fatalf("abort: %+v %v", res, err)
	}
}

func TestBranchGitRefusals(t *testing.T) {
	c, proj, _, _, _ := harvestFixture(t)
	id := proj.ID
	for _, step := range []struct {
		params proto.BranchGitParams
		want   string
	}{
		{proto.BranchGitParams{ProjectID: id, Branch: "feat", Commands: [][]string{{"-C", "/", "status"}}}, "options before the command"},
		{proto.BranchGitParams{ProjectID: id, Branch: "feat", Commands: [][]string{{"status"}, {}}}, "no git command"},
		{proto.BranchGitParams{ProjectID: id, Branch: "feat", Commands: make([][]string, 9)}, "at most 8"},
		{proto.BranchGitParams{ProjectID: id, Commands: [][]string{{"status"}}}, "no branch"},
		{proto.BranchGitParams{ProjectID: id, Branch: "nowhere", Commands: [][]string{{"status"}}}, "not checked out"},
		{proto.BranchGitParams{ProjectID: "rmissing", Branch: "feat", Commands: [][]string{{"status"}}}, "no project"},
	} {
		wantErr(t, call(t, c, proto.MethodBranchGit, step.params, nil), step.want)
	}

	c2, dir := startServer(t)
	plain := filepath.Join(dir, "notes")
	os.MkdirAll(plain, 0o755)
	var p proto.ProjectInfo
	if err := call(t, c2, proto.MethodProjectAdd, proto.ProjectAddParams{Path: plain}, &p); err != nil {
		t.Fatal(err)
	}
	wantErr(t, call(t, c2, proto.MethodBranchGit, proto.BranchGitParams{ProjectID: p.ID, Branch: "b"}, nil), "not a git repository")
}
