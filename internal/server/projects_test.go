package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

	// Committed, a worktree's files are only what is left uncommitted —
	// nothing, once an agent has committed — so a reader comparing
	// attempts asks for what the commits change against the base too.
	git(t, wt.Path, "add", "a.txt")
	git(t, wt.Path, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "two lines")
	os.WriteFile(filepath.Join(wt.Path, "b.txt"), []byte("left out\n"), 0o644)
	ch = proto.Changes{}
	if err := c.Call(ctx, proto.MethodProjectChanges, proto.ChangesParams{ProjectID: proj.ID, Branch: "feat/x", Committed: true}, &ch); err != nil {
		t.Fatal(err)
	}
	if len(ch.CommittedFiles) != 1 || ch.CommittedFiles[0].Path != "a.txt" || ch.CommittedFiles[0].Added != 1 ||
		len(ch.Files) != 1 || ch.Files[0].Path != "b.txt" {
		t.Fatalf("committed: %+v", ch)
	}
	ch = proto.Changes{}
	if err := c.Call(ctx, proto.MethodProjectChanges, proto.ChangesParams{ProjectID: proj.ID, Branch: "feat/x"}, &ch); err != nil || ch.CommittedFiles != nil {
		t.Fatalf("not asked for: %+v %v", ch.CommittedFiles, err)
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
  "statusCheckRollup":[{"__typename":"CheckRun","name":"test (ubuntu)","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://github.com/o/r/runs/1"},
                       {"__typename":"CheckRun","name":"test (macos)","status":"IN_PROGRESS","conclusion":""}]}]
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
	// Each check by name reaches the clients, its state and link with it.
	if len(pr.Runs) != 2 || pr.Runs[0] != (proto.PRCheck{Name: "test (ubuntu)", State: "fail", URL: "https://github.com/o/r/runs/1"}) ||
		pr.Runs[1].Name != "test (macos)" || pr.Runs[1].State != "pending" || pr.Total != 2 {
		t.Fatalf("runs: %+v (total %d)", pr.Runs, pr.Total)
	}
}

// mergeRig is a project cloned from a bare "origin", a second clone that
// plays GitHub (it merges and pushes), and a fake gh answering from a file
// the test rewrites.
type mergeRig struct {
	t                 *testing.T
	c                 *client.Client
	ctx               context.Context
	dir, repo, other  string
	origin, state, id string
	logs              *lockedLog
}

type lockedLog struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLog) has(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Contains(l.buf.String(), s)
}

func newMergeRig(t *testing.T) *mergeRig {
	t.Helper()
	scratch := t.TempDir()
	r := &mergeRig{t: t, state: filepath.Join(scratch, "prs.json"), logs: &lockedLog{}}
	old := log.Writer()
	log.SetOutput(io.MultiWriter(old, r.logs))
	t.Cleanup(func() { log.SetOutput(old) })
	r.prs() // gh's first answer: none
	gh := filepath.Join(scratch, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\ncat "+r.state+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_GH", gh)

	r.c, r.dir = startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	r.ctx = ctx
	r.origin = filepath.Join(r.dir, "origin.git")
	git(t, r.dir, "init", "-q", "--bare", "-b", "main", r.origin)
	r.other = filepath.Join(r.dir, "other")
	git(t, r.dir, "clone", "-q", r.origin, r.other)
	r.repo = filepath.Join(r.dir, "web")
	for _, d := range []string{r.other} {
		git(t, d, "config", "user.name", "t")
		git(t, d, "config", "user.email", "t@example.com")
	}
	git(t, r.other, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, r.other, "push", "-q", "origin", "main")
	git(t, r.dir, "clone", "-q", r.origin, r.repo)
	git(t, r.repo, "config", "user.name", "t")
	git(t, r.repo, "config", "user.email", "t@example.com")
	return r
}

// pr is one pull request as gh lists it; base "" leaves baseRefName out.
func pr(number int, branch, base, state string) string {
	b := ""
	if base != "" {
		b = fmt.Sprintf(`,"baseRefName":%q`, base)
	}
	return fmt.Sprintf(`{"number":%d,"title":"t","state":%q,"url":"https://github.com/o/r/pull/%d","headRefName":%q%s,"updatedAt":"2026-11-05T10:00:00Z","statusCheckRollup":[]}`, number, state, number, branch, b)
}

// prs is gh's next answer, put in place whole: written beside and renamed,
// so a gh run meanwhile reads the old answer or the new, never half.
func (r *mergeRig) prs(list ...string) { r.raw("[" + strings.Join(list, ",") + "]") }

func (r *mergeRig) raw(body string) {
	r.t.Helper()
	tmp := r.state + ".new"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Rename(tmp, r.state); err != nil {
		r.t.Fatal(err)
	}
}

func (r *mergeRig) add() {
	r.t.Helper()
	var added proto.ProjectInfo
	if err := r.c.Call(r.ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: r.repo}, &added); err != nil {
		r.t.Fatal(err)
	}
	r.id = added.ID
}

// look asks for the pull requests now and waits until ok says they came.
func (r *mergeRig) look(ok func(proto.ProjectInfo) bool) {
	r.t.Helper()
	if r.id != "" {
		if err := r.c.Call(r.ctx, proto.MethodProjectRefresh, proto.ProjectRef{ID: r.id}, nil); err != nil {
			r.t.Fatal(err)
		}
	}
	waitProject(r.t, r.c, ok)
}

func prState(branch, state string) func(proto.ProjectInfo) bool {
	return func(p proto.ProjectInfo) bool {
		for _, b := range p.Branches {
			if b.Name == branch && b.PR != nil && b.PR.State == state {
				return true
			}
		}
		return false
	}
}

// merge is GitHub merging branch (pushed to origin) into main; it returns
// the merge commit.
func (r *mergeRig) merge(branch string) string {
	r.t.Helper()
	git(r.t, r.other, "fetch", "-q", "origin", branch)
	git(r.t, r.other, "merge", "-q", "--no-ff", "-m", "Merge "+branch, "FETCH_HEAD")
	git(r.t, r.other, "push", "-q", "origin", "main")
	return revParse(r.t, r.other, "HEAD")
}

func (r *mergeRig) originMain() string { return revParse(r.t, r.repo, "refs/remotes/origin/main") }

func (r *mergeRig) waitOriginMain(want string) {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for r.originMain() != want {
		if time.Now().After(deadline) {
			r.t.Fatalf("origin/main is %s, want the merge %s", r.originMain(), want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A pull request seen open and then merged fetches its base, so the merge
// GitHub made reaches this checkout's origin/main and the commit graph can
// show it; one already merged at the first look fetches nothing. The
// merged branch tracks a fork, and the base still comes from origin: the
// remote is the base's, not the head's.
func TestMergedPullRequestFetchesItsBase(t *testing.T) {
	r := newMergeRig(t)
	fork := filepath.Join(r.dir, "fork.git")
	git(t, r.dir, "init", "-q", "--bare", "-b", "main", fork)
	git(t, r.repo, "remote", "add", "fork", fork)
	git(t, r.repo, "checkout", "-q", "-b", "feat")
	git(t, r.repo, "commit", "-q", "--allow-empty", "-m", "feature")
	git(t, r.repo, "push", "-q", "-u", "fork", "feat")
	git(t, r.repo, "push", "-q", "origin", "feat") // where GitHub merges it from, here
	git(t, r.repo, "branch", "-q", "old")
	before := r.originMain()

	// The remote moves on before the server looks: "old" was merged
	// already, which must not be fetched for.
	git(t, r.other, "commit", "-q", "--allow-empty", "-m", "merge old")
	git(t, r.other, "push", "-q", "origin", "main")
	r.prs(pr(6, "old", "main", "MERGED"), pr(7, "feat", "main", "OPEN"))
	r.add()
	r.look(prState("feat", "OPEN"))
	if got := r.originMain(); got != before {
		t.Fatalf("a pull request merged before the first look fetched the base: %s, was %s", got, before)
	}

	merged := r.merge("feat")
	r.prs(pr(6, "old", "main", "MERGED"), pr(7, "feat", "main", "MERGED"))
	r.look(prState("feat", "MERGED"))
	r.waitOriginMain(merged)
	if out, _ := exec.Command("git", "-C", r.repo, "rev-parse", "--verify", "-q", "refs/remotes/fork/main").Output(); len(out) > 0 {
		t.Fatalf("fetched the head's remote: fork/main is %s", out)
	}
}

// A fetch that fails is owed until one succeeds: the next look tries it
// again, though the pull request was already merged at the look before.
func TestMergedBaseFetchRetried(t *testing.T) {
	r := newMergeRig(t)
	git(t, r.repo, "checkout", "-q", "-b", "feat")
	git(t, r.repo, "commit", "-q", "--allow-empty", "-m", "feature")
	git(t, r.repo, "push", "-q", "-u", "origin", "feat")
	r.prs(pr(7, "feat", "main", "OPEN"))
	r.add()
	r.look(prState("feat", "OPEN"))

	merged := r.merge("feat")
	away := r.origin + ".away" // the network gone
	if err := os.Rename(r.origin, away); err != nil {
		t.Fatal(err)
	}
	r.prs(pr(7, "feat", "main", "MERGED"))
	r.look(prState("feat", "MERGED"))
	deadline := time.Now().Add(10 * time.Second)
	for !r.logs.has("fetching it failed") {
		if time.Now().After(deadline) {
			t.Fatal("the failed fetch was never tried")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := os.Rename(away, r.origin); err != nil {
		t.Fatal(err)
	}
	// MERGED at this look and the last: only the debt brings the fetch.
	if err := r.c.Call(r.ctx, proto.MethodProjectRefresh, proto.ProjectRef{ID: r.id}, nil); err != nil {
		t.Fatal(err)
	}
	r.waitOriginMain(merged)
}

// gh failing between the two looks forgets nothing: the pull request it
// last saw open is still owed its fetch when the next look says merged.
// gh not naming the base, the project's base is the one fetched.
func TestMergeSeenAcrossAGHError(t *testing.T) {
	r := newMergeRig(t)
	git(t, r.repo, "checkout", "-q", "-b", "feat")
	git(t, r.repo, "commit", "-q", "--allow-empty", "-m", "feature")
	git(t, r.repo, "push", "-q", "-u", "origin", "feat")
	r.prs(pr(7, "feat", "", "OPEN"))
	r.add()
	r.look(prState("feat", "OPEN"))

	r.raw("not json")
	r.look(func(p proto.ProjectInfo) bool { return p.PRStatus != "" })

	merged := r.merge("feat")
	r.prs(pr(7, "feat", "", "MERGED"))
	r.look(prState("feat", "MERGED"))
	r.waitOriginMain(merged)
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

// TestWorktreeChangedReachesAClient is the whole live-diff path through a
// real server: a file written in a worktree, the watcher, the broadcast and
// a client's event channel. Everything else about the watcher is tested on
// its own; this proves it is actually wired up.
func TestWorktreeChangedReachesAClient(t *testing.T) {
	c, dir := startServer(t)
	if len(c.MissingCapabilities([]string{proto.CapWorktreeWatch})) > 0 {
		t.Skip("this machine gives no file watches")
	}
	repo := filepath.Join(dir, "api")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "init")

	var added proto.ProjectInfo
	if err := c.Call(context.Background(), proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &added); err != nil {
		t.Fatal(err)
	}
	// The watcher starts with the project's first git refresh.
	waitProject(t, c, func(p proto.ProjectInfo) bool { return p.ID == added.ID && len(p.Worktrees) > 0 })

	// Rewrite the line in place: the file's +/− counts do not move, so only
	// the watcher can notice this.
	deadline := time.After(20 * time.Second)
	retry := time.NewTicker(300 * time.Millisecond)
	defer retry.Stop()
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package a // one\n")
	for {
		select {
		case m, open := <-c.Events:
			if !open {
				t.Fatalf("connection closed: %v", c.Err())
			}
			if m.Event != proto.EventWorktreeChanged {
				continue
			}
			var wc proto.WorktreeChanged
			if err := json.Unmarshal(m.Data, &wc); err != nil {
				t.Fatalf("event payload: %v", err)
			}
			if wc.ProjectID != added.ID {
				t.Fatalf("event for %q, want %q", wc.ProjectID, added.ID)
			}
			if !slices.Contains(wc.Paths, "a.go") {
				t.Fatalf("paths %v", wc.Paths)
			}
			return
		case <-retry.C:
			// The watch goes on with the first refresh; a write that beat
			// it by a hair would otherwise wait for the deadline.
			write("package a // " + time.Now().Format("15:04:05.000") + "\n")
		case <-deadline:
			t.Fatal("no worktree.changed event for an edited file")
		}
	}
}

func TestBrowseACheckout(t *testing.T) {
	c, dir := startServer(t)
	repo := filepath.Join(dir, "repo")
	os.MkdirAll(filepath.Join(repo, "src"), 0o755)
	os.WriteFile(filepath.Join(repo, "src", "main.go"), []byte("package main\n"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if missing := c.MissingCapabilities([]string{proto.CapFSFiles, proto.CapFSRead}); len(missing) > 0 {
		t.Fatalf("missing %v", missing)
	}
	var proj proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj); err != nil {
		t.Fatal(err)
	}
	var list proto.FSList
	if err := c.Call(ctx, proto.MethodFSList, proto.FSListParams{Root: proj.Path, Path: "src", Files: true}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 1 || list.Entries[0].Name != "main.go" || list.Entries[0].Dir {
		t.Fatalf("listing: %+v", list)
	}
	var read proto.FSReadResult
	if err := c.Call(ctx, proto.MethodFSRead, proto.FSReadParams{Root: proj.Path, Path: "src/main.go"}, &read); err != nil || read.Data != "package main\n" {
		t.Fatalf("read: %+v %v", read, err)
	}
	if err := c.Call(ctx, proto.MethodFSRead, proto.FSReadParams{Root: dir, Path: "repo/src/main.go"}, &read); err == nil {
		t.Fatal("read outside any project")
	}
}

// A folder added as a project and only then made a repository — `git init`
// in its shell, as somebody does with a new project — becomes a git
// project without a server restart, and a task can start there although
// nothing has been committed yet.
func TestPlainFolderBecomesGitProject(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	c, dir := startServer(t)
	folder := filepath.Join(dir, "cachemind")
	os.MkdirAll(folder, 0o755)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var proj proto.ProjectInfo
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: folder}, &proj); err != nil {
		t.Fatal(err)
	}
	if proj.Git {
		t.Fatalf("plain folder added as git: %+v", proj)
	}
	var wt proto.WorktreeResult
	if err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "feat"}, &wt); err == nil {
		t.Fatal("worktree made in a plain folder")
	}

	git(t, folder, "init", "-q", "-b", "master")
	git(t, folder, "config", "user.name", "t")
	git(t, folder, "config", "user.email", "t@example.com")
	proj = waitProject(t, c, func(p proto.ProjectInfo) bool { return p.ID == proj.ID && p.Git })
	if proj.Base != "master" {
		t.Fatalf("base %q, want master", proj.Base)
	}
	if err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "feat"}, &wt); err != nil {
		t.Fatalf("worktree in a repository with no commits: %v", err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatal(err)
	}
}
