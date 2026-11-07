package relocate

import (
	"context"
	"errors"
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

// git runs git in dir with a fixed identity, failing the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimRight(string(out), "\n") // git status starts lines with a space
}

// isolate keeps git and conch away from the developer's own.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	gitconfig := filepath.Join(home, ".gitconfig")
	os.WriteFile(gitconfig, []byte("[user]\n\tname = Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644)
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("CONCH_HOME", filepath.Join(home, ".config", "conch"))
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	return home
}

// start runs a conch server with its own state, and dials it.
func start(t *testing.T, name string) *client.Client {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "rl") // macOS caps socket paths near 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "s")
	srv := server.New(sock, filepath.Join(t.TempDir(), name))
	stopped := make(chan struct{})
	go func() { srv.Run(); close(stopped) }()
	// Stop only asks: the server shuts down — saving its state, finishing
	// a git call — after it returns, so wait, or the test's folders are
	// removed while it is still writing in them (seen on Linux).
	t.Cleanup(func() {
		srv.Stop()
		select {
		case <-stopped:
		case <-time.After(30 * time.Second):
			t.Error("the server did not stop")
		}
	})
	for i := 0; ; i++ {
		c, err := client.Dial(sock, "test")
		if err == nil {
			t.Cleanup(func() { c.Close() })
			return c
		}
		if i == 300 {
			t.Fatalf("server %s did not start: %v", name, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func call(t *testing.T, c *client.Client, method string, params, out any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.Call(ctx, method, params, out); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

// project waits until the server's view of project id says ok.
func project(t *testing.T, c *client.Client, id string, ok func(proto.ProjectInfo) bool) proto.ProjectInfo {
	t.Helper()
	var last proto.ProjectInfo
	for i := 0; i < 300; i++ {
		var list proto.ProjectList
		call(t, c, proto.MethodProjectList, nil, &list)
		for _, p := range list.Projects {
			if p.ID == id || p.Name == id {
				last = p
				if ok(p) {
					return p
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("project %s never became what was waited for: %+v", id, last)
	return last
}

func branch(p proto.ProjectInfo, name string) *proto.BranchInfo {
	for i := range p.Branches {
		if p.Branches[i].Name == name {
			return &p.Branches[i]
		}
	}
	return nil
}

// A whole project moves between two real conch servers: the repository is
// cloned there from its origin, the task worktree arrives with its commit
// and its uncommitted work, a branch that is only here goes too, and a
// branch already pushed is left to the clone.
func TestMoveProjectBetweenServers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	home := isolate(t)

	// An origin with main and a pushed branch, and a clone of it here.
	origin := filepath.Join(home, "origin.git")
	git(t, home, "init", "-q", "--bare", "-b", "main", origin)
	src := filepath.Join(home, "workspace", "daos")
	git(t, home, "clone", "-q", origin, src)
	os.WriteFile(filepath.Join(src, "README.md"), []byte("daos\n"), 0o644)
	git(t, src, "add", "README.md")
	git(t, src, "commit", "-q", "-m", "first")
	git(t, src, "push", "-q", "origin", "main")
	git(t, src, "checkout", "-q", "-b", "pushed")
	git(t, src, "commit", "-q", "--allow-empty", "-m", "on origin already")
	git(t, src, "push", "-q", "-u", "origin", "pushed")
	// A branch that is only here, with no worktree.
	git(t, src, "checkout", "-q", "-b", "wip/local", "main")
	git(t, src, "commit", "-q", "--allow-empty", "-m", "only here")
	git(t, src, "checkout", "-q", "main")

	from := start(t, "here")
	to := start(t, "there")

	var info proto.ProjectInfo
	call(t, from, proto.MethodProjectAdd, proto.ProjectAddParams{Path: src}, &info)
	// A task worktree with a commit and work not yet committed.
	var wt proto.WorktreeResult
	call(t, from, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: info.ID, Branch: "feat/fix"}, &wt)
	os.WriteFile(filepath.Join(wt.Path, "fix.go"), []byte("package fix\n"), 0o644)
	git(t, wt.Path, "add", "fix.go")
	git(t, wt.Path, "commit", "-q", "-m", "the fix")
	os.WriteFile(filepath.Join(wt.Path, "staged.txt"), []byte("staged\n"), 0o644)
	git(t, wt.Path, "add", "staged.txt")
	os.WriteFile(filepath.Join(wt.Path, "README.md"), []byte("daos, changed\n"), 0o644) // unstaged
	os.WriteFile(filepath.Join(wt.Path, "notes.txt"), []byte("untracked\n"), 0o644)

	// Wait for the server to have read the branches.
	project(t, from, info.ID, func(p proto.ProjectInfo) bool {
		return branch(p, "wip/local") != nil && branch(p, "pushed") != nil && len(ProjectWorktrees(&p)) == 1
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Both servers share this home, so the clone goes somewhere else.
	plan, err := NewPlan(ctx, from, to, info.ID, "here", "there", "~/there/daos")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.CloneTo != "~/there/daos" || plan.Target != "" || plan.Remote == "" {
		t.Fatalf("where: %+v", plan)
	}
	if len(plan.Worktrees) != 1 || plan.Worktrees[0].Branch != "feat/fix" {
		t.Fatalf("worktrees: %+v", plan.Worktrees)
	}
	if w := plan.Worktrees[0].Info; w.Staged != 1 || w.Unstaged != 1 || w.Other < 1 {
		t.Fatalf("what the worktree takes: %+v", w)
	}
	var names []string
	for _, b := range plan.Branches {
		names = append(names, b.Name)
	}
	if strings.Join(names, ",") != "wip/local" {
		t.Fatalf("branches with no worktree: %v (a pushed one is the clone's)", names)
	}
	if plan.Steps() != 3 { // clone, wip/local, feat/fix
		t.Fatalf("steps %d", plan.Steps())
	}

	var events []Event
	res, err := Run(ctx, plan, from, to, Options{Cols: 80, Rows: 24}, func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Failed) > 0 || strings.Join(res.Moved, ",") != "wip/local,feat/fix" {
		t.Fatalf("result: %+v", res)
	}
	var said []string
	for _, e := range events {
		said = append(said, e.Kind+":"+e.State)
	}
	if strings.Join(said, " ") != "clone:start clone:ok branch:start branch:ok worktree:start worktree:ok" {
		t.Fatalf("events: %v", said)
	}
	if last := events[len(events)-1]; last.Step != 3 || last.Steps != 3 {
		t.Fatalf("last event %+v", last)
	}

	// There: the clone, with both branches in worktrees, and the fix's
	// uncommitted work as it was.
	there := project(t, to, res.Target, func(p proto.ProjectInfo) bool { return len(ProjectWorktrees(&p)) == 2 })
	if there.Path != filepath.Join(home, "there", "daos") || res.TargetPath != there.Path {
		t.Fatalf("clone at %s (result %s)", there.Path, res.TargetPath)
	}
	var fix string
	for _, w := range ProjectWorktrees(&there) {
		if w.Branch == "feat/fix" {
			fix = w.Path
		}
	}
	if fix == "" {
		t.Fatalf("feat/fix not there: %+v", there.Worktrees)
	}
	if got := git(t, fix, "log", "-1", "--format=%s"); got != "the fix" {
		t.Fatalf("feat/fix's last commit there: %q", got)
	}
	status := git(t, fix, "status", "--porcelain")
	for _, want := range []string{"A  staged.txt", " M README.md", "?? notes.txt"} {
		if !strings.Contains(status, want) {
			t.Errorf("there, %q missing from git status:\n%s", want, status)
		}
	}
	if got := git(t, there.Path, "log", "-1", "--format=%s", "wip/local"); got != "only here" {
		t.Fatalf("wip/local there: %q", got)
	}

	// Here, everything is kept: nothing is lost if something went wrong.
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatal("the worktree here was removed")
	}

	// Moved again, the clone there is found by its origin, not made twice
	// (once the server there has read it: it does so in the background).
	project(t, to, res.Target, func(p proto.ProjectInfo) bool { return p.Remote != "" && len(ProjectWorktrees(&p)) == 2 })
	// Here, wip/local was given a worktree to travel in: wait for the
	// server to have read that too, or the plan sees it as a branch.
	project(t, from, info.ID, func(p proto.ProjectInfo) bool { return len(ProjectWorktrees(&p)) == 2 })
	// Both branches are checked out there already, and no agent is left to
	// follow them: nothing to do, said as such rather than failed twice.
	again, err := NewPlan(ctx, from, to, info.ID, "here", "there", "")
	if !errors.Is(err, ErrNothing) || !strings.Contains(err.Error(), "on there already") {
		var tl proto.ProjectList
		call(t, to, proto.MethodProjectList, nil, &tl)
		t.Fatalf("second plan: %v\nworktrees %+v\nbranches %+v\nalready %v\ntarget %s\nthere: %+v", err, again.Worktrees, again.Branches, again.Already, again.Target, tl.Projects)
	}
	if again.Target != res.Target || again.CloneTo != "" {
		t.Fatalf("second plan: target %q clone %q, want %q", again.Target, again.CloneTo, res.Target)
	}
	if got := strings.Join(again.Already, ","); got != "feat/fix,wip/local" {
		t.Fatalf("already there: %q", got)
	}
}

// What the project there has checked out already isn't moved again: a
// worktree with agents is joined (they continue in it), one without and a
// branch are said to be there.
func TestJoinWorktrees(t *testing.T) {
	agent := proto.PaneInfo{ID: "p1", Agent: &proto.AgentStatus{Name: "claude"}}
	p := Plan{
		Worktrees: []Worktree{
			{Branch: "feat/a", Path: "/here/a", Agents: []proto.PaneInfo{agent}},
			{Branch: "feat/b", Path: "/here/b"},
			{Branch: "feat/new", Path: "/here/new"},
		},
		Branches: []proto.BranchInfo{{Name: "wip/x"}, {Name: "wip/y"}},
	}
	p.joinWorktrees(&proto.ProjectInfo{Worktrees: []proto.WorktreeInfo{
		{Main: true, Branch: "main", Path: "/there"},
		{Branch: "feat/a", Path: "/there/a"},
		{Branch: "feat/b", Path: "/there/b"},
		{Branch: "wip/x", Path: "/there/x"},
		{Path: "/there/detached"},
	}})
	if len(p.Worktrees) != 2 || p.Worktrees[0].There != "/there/a" || p.Worktrees[1].Branch != "feat/new" || p.Worktrees[1].There != "" {
		t.Fatalf("worktrees: %+v", p.Worktrees)
	}
	if len(p.Branches) != 1 || p.Branches[0].Name != "wip/y" {
		t.Fatalf("branches: %+v", p.Branches)
	}
	if got := strings.Join(p.Already, ","); got != "feat/b,wip/x" {
		t.Fatalf("already: %q", got)
	}
	// A project there with no worktrees changes nothing.
	q := Plan{Worktrees: []Worktree{{Branch: "feat/a"}}, Branches: []proto.BranchInfo{{Name: "wip/x"}}}
	q.joinWorktrees(&proto.ProjectInfo{})
	if len(q.Worktrees) != 1 || len(q.Branches) != 1 || len(q.Already) != 0 {
		t.Fatalf("empty there: %+v", q)
	}
}

func TestPlanRefusals(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	home := isolate(t)
	from := start(t, "here")
	to := start(t, "there")
	ctx := context.Background()

	if _, err := NewPlan(ctx, from, to, "nope", "here", "there", ""); err == nil || !strings.Contains(err.Error(), `no project "nope" on here`) {
		t.Fatalf("unknown project: %v", err)
	}
	// A folder that isn't git doesn't move.
	plain := filepath.Join(home, "plain")
	os.MkdirAll(plain, 0o755)
	var info proto.ProjectInfo
	call(t, from, proto.MethodProjectAdd, proto.ProjectAddParams{Path: plain}, &info)
	if _, err := NewPlan(ctx, from, to, info.ID, "here", "there", ""); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("plain folder: %v", err)
	}
	// A repository with nothing of its own to move says so.
	repo := filepath.Join(home, "repo")
	git(t, home, "init", "-q", repo)
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "first")
	call(t, from, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &info)
	project(t, from, info.ID, func(p proto.ProjectInfo) bool { return len(p.Branches) > 0 })
	_, err := NewPlan(ctx, from, to, info.ID, "here", "there", "")
	if err == nil || !strings.Contains(err.Error(), "nothing would move") {
		t.Fatalf("nothing to move: %v", err)
	}
	// With a branch of its own but no origin, there is nothing to clone there.
	git(t, repo, "branch", "mine")
	project(t, from, info.ID, func(p proto.ProjectInfo) bool { return branch(p, "mine") != nil })
	if _, err := NewPlan(ctx, from, to, info.ID, "here", "there", ""); err == nil || !strings.Contains(err.Error(), "has no origin") {
		t.Fatalf("no origin: %v", err)
	}
}

func TestPlanHelpers(t *testing.T) {
	if CloneFolder("/h/workspace/daos", "/h") != "~/workspace/daos" || CloneFolder("/srv/daos", "/h") != "~/daos" {
		t.Fatal("clone folder")
	}
	if Tildify("/h", "/h") != "~" || Tildify("/h/x", "/h") != "~/x" || Tildify("/hx", "/h") != "/hx" || Tildify("/a", "") != "/a" {
		t.Fatal("tildify")
	}
	for _, c := range [][2]string{
		{"git@github.com:daos-stack/daos.git", "https://github.com/daos-stack/daos"},
		{"ssh://git@github.com/DAOS-stack/daos", "https://github.com/daos-stack/daos.git/"},
	} {
		if !SameRemote(c[0], c[1]) {
			t.Errorf("%s and %s are the same repository", c[0], c[1])
		}
	}
	if SameRemote("", "") || SameRemote("https://github.com/a/b", "https://github.com/a/c") {
		t.Fatal("different remotes matched")
	}
	if RemoteName("git@github.com:daos-stack/daos.git") != "daos-stack/daos" {
		t.Fatal(RemoteName("git@github.com:daos-stack/daos.git"))
	}

	proj := &proto.ProjectInfo{Base: "main",
		Worktrees: []proto.WorktreeInfo{{Path: "/r", Branch: "main", Main: true}, {Path: "/r.wt/a", Branch: "a"}, {Path: "/r.wt/d", Detached: true}},
		Branches: []proto.BranchInfo{
			{Name: "main"},                   // the base: the clone has it
			{Name: "a", Worktree: "/r.wt/a"}, // moves as a worktree
			{Name: "local"},                  // no upstream: only here
			{Name: "gone", Upstream: "origin/gone", Gone: true}, // its upstream went
			{Name: "ahead", Upstream: "origin/ahead", Ahead: 2}, // commits origin lacks
			{Name: "even", Upstream: "origin/even"},             // the clone has it
		}}
	var names []string
	for _, b := range BranchesToMove(proj) {
		names = append(names, b.Name)
	}
	if strings.Join(names, ",") != "local,gone,ahead" {
		t.Fatalf("branches to move: %v", names)
	}
	if w := ProjectWorktrees(proj); len(w) != 1 || w[0].Branch != "a" {
		t.Fatalf("worktrees: %+v", w)
	}
	if !InMainCheckout(proj, proto.PaneInfo{Cwd: "/r/sub"}) || InMainCheckout(proj, proto.PaneInfo{Cwd: "/r.wt/a"}) {
		t.Fatal("main checkout")
	}
	if (Plan{}).Empty() != true || (Plan{Branches: []proto.BranchInfo{{Name: "x"}}}).Empty() {
		t.Fatal("empty")
	}
}

// Counts say the noun, and verb, that fit: never agent(s).
func TestCount(t *testing.T) {
	for n, want := range map[int]string{0: "0 agents continue", 1: "1 agent continues", 2: "2 agents continue"} {
		if got := Count(n, "agent continues", "agents continue"); got != want {
			t.Errorf("Count(%d) = %q, want %q", n, got, want)
		}
	}
}
