package server_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// harvestFixture starts a server with a project on main, a bare origin, and
// a task worktree for branch feat.
func harvestFixture(t *testing.T) (c *client.Client, proj proto.ProjectInfo, repo, wt, origin string) {
	t.Helper()
	// The server's own git processes inherit these, keeping the developer's
	// git config (signing, hooks) out.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	c, dir := startServer(t)
	repo = filepath.Join(dir, "api")
	origin = filepath.Join(dir, "origin.git")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.com")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")
	git(t, repo, "init", "-q", "--bare", origin)
	git(t, repo, "remote", "add", "origin", origin)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Call(ctx, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj); err != nil {
		t.Fatal(err)
	}
	proj = waitProject(t, c, func(p proto.ProjectInfo) bool { return p.Path == repo && p.Base == "main" })
	var res proto.WorktreeResult
	if err := c.Call(ctx, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "feat"}, &res); err != nil {
		t.Fatal(err)
	}
	return c, proj, repo, res.Path, origin
}

func call(t *testing.T, c *client.Client, method string, params, out any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.Call(ctx, method, params, out)
}

func wantErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got error %v, want %q", err, want)
	}
}

func TestHarvestCommitPushMerge(t *testing.T) {
	c, proj, repo, wt, origin := harvestFixture(t)
	id := proj.ID

	// Commit: every change, then only picked files.
	wantErr(t, call(t, c, proto.MethodBranchCommit, proto.BranchCommitParams{ProjectID: id, Branch: "feat", Message: "x"}, nil), "nothing to commit")
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("one\ntwo\n"), 0o644)
	os.WriteFile(filepath.Join(wt, "b.txt"), []byte("b\n"), 0o644)
	os.WriteFile(filepath.Join(wt, "later.txt"), []byte("not yet\n"), 0o644)
	wantErr(t, call(t, c, proto.MethodBranchCommit, proto.BranchCommitParams{ProjectID: id, Branch: "feat"}, nil), "needs a message")
	var cr proto.CommitResult
	if err := call(t, c, proto.MethodBranchCommit, proto.BranchCommitParams{ProjectID: id, Branch: "feat", Message: "Edit a, add b", Files: []string{"a.txt", "b.txt"}}, &cr); err != nil {
		t.Fatal(err)
	}
	if cr.Hash != gitOut(t, wt, "rev-parse", "HEAD") {
		t.Fatalf("commit hash %q", cr.Hash)
	}
	if st := gitOut(t, wt, "status", "--porcelain"); st != "?? later.txt" {
		t.Fatalf("after picking files: %q", st)
	}
	wantErr(t, call(t, c, proto.MethodBranchCommit, proto.BranchCommitParams{ProjectID: id, Branch: "nowhere", Message: "x"}, nil), "not checked out")
	wantErr(t, call(t, c, proto.MethodBranchCommit, proto.BranchCommitParams{ProjectID: "rmissing", Branch: "feat", Message: "x"}, nil), "no project")

	// A merge refuses uncommitted work on the branch; it would be left out.
	wantErr(t, call(t, c, proto.MethodBranchMerge, proto.BranchMergeParams{ProjectID: id, Branch: "feat", Squash: true}, nil), "1 uncommitted file")
	if err := call(t, c, proto.MethodBranchCommit, proto.BranchCommitParams{ProjectID: id, Branch: "feat", Message: "Add later"}, nil); err != nil {
		t.Fatal(err)
	}

	// Push sets the upstream.
	if err := call(t, c, proto.MethodBranchPush, proto.BranchRef{ProjectID: id, Branch: "feat"}, nil); err != nil {
		t.Fatal(err)
	}
	if gitOut(t, origin, "rev-parse", "feat") != gitOut(t, wt, "rev-parse", "HEAD") {
		t.Fatal("origin lacks the pushed commits")
	}
	wantErr(t, call(t, c, proto.MethodBranchPush, proto.BranchRef{ProjectID: id, Branch: "gone"}, nil), "no branch gone")

	// Merging the base into itself, or with no checkout of the base, is refused.
	wantErr(t, call(t, c, proto.MethodBranchMerge, proto.BranchMergeParams{ProjectID: id, Branch: "main"}, nil), "is the base branch")
	wantErr(t, call(t, c, proto.MethodBranchMerge, proto.BranchMergeParams{ProjectID: id, Branch: ""}, nil), "no branch")

	var mr proto.CommitResult
	if err := call(t, c, proto.MethodBranchMerge, proto.BranchMergeParams{ProjectID: id, Branch: "feat", Squash: true, Message: "Land feat"}, &mr); err != nil {
		t.Fatal(err)
	}
	if mr.Into != "main" || mr.Hash != gitOut(t, repo, "rev-parse", "main") || gitOut(t, repo, "log", "-1", "--format=%s") != "Land feat" {
		t.Fatalf("merge: %+v", mr)
	}
	if _, err := os.Stat(filepath.Join(repo, "later.txt")); err != nil {
		t.Fatal("merged file missing from the main checkout")
	}

	// The squash-merged branch can be discarded without force.
	var dr proto.BranchDiscardResult
	if err := call(t, c, proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: id, Branch: "feat"}, &dr); err != nil {
		t.Fatal(err)
	}
	if !dr.Done || dr.Worktree != wt || dr.Unmerged != 0 || len(dr.Uncommitted) != 0 {
		t.Fatalf("discard: %+v", dr)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree folder still there")
	}
	if out, _ := exec.Command("git", "-C", repo, "branch", "--list", "feat").Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatal("branch still there")
	}
}

func TestHarvestMergeConflictAndNoCheckout(t *testing.T) {
	c, proj, repo, wt, _ := harvestFixture(t)
	id := proj.ID
	git(t, wt, "config", "user.name", "t")
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("feat\n"), 0o644)
	git(t, wt, "commit", "-q", "-am", "feat")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("main\n"), 0o644)
	git(t, repo, "commit", "-q", "-am", "main")
	before := gitOut(t, repo, "rev-parse", "HEAD")

	wantErr(t, call(t, c, proto.MethodBranchMerge, proto.BranchMergeParams{ProjectID: id, Branch: "feat"}, nil), "conflicts in a.txt")
	if gitOut(t, repo, "rev-parse", "HEAD") != before || gitOut(t, repo, "status", "--porcelain") != "" {
		t.Fatal("a conflicting merge changed the main checkout")
	}

	// With the base checked out nowhere there is nowhere to merge.
	git(t, repo, "checkout", "-q", "-b", "elsewhere")
	wantErr(t, call(t, c, proto.MethodBranchMerge, proto.BranchMergeParams{ProjectID: id, Branch: "feat"}, nil), "main isn't checked out")
}

func TestHarvestDiscardRefusals(t *testing.T) {
	c, proj, repo, wt, _ := harvestFixture(t)
	id := proj.ID

	wantErr(t, call(t, c, proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: id, Branch: "main"}, nil), "is the base branch")

	// Work that would be lost: an uncommitted file and a commit.
	os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x\n"), 0o644)
	git(t, wt, "add", "new.txt")
	git(t, wt, "commit", "-q", "-m", "unmerged")
	os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("wip\n"), 0o644)

	var dry proto.BranchDiscardResult
	if err := call(t, c, proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: id, Branch: "feat", DryRun: true}, &dry); err != nil {
		t.Fatal(err)
	}
	if dry.Done || dry.Worktree != wt || dry.Unmerged != 1 || strings.Join(dry.Uncommitted, ",") != "wip.txt" {
		t.Fatalf("dry run: %+v", dry)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal("a dry run removed the worktree")
	}
	wantErr(t, call(t, c, proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: id, Branch: "feat"}, nil),
		"would lose 1 uncommitted file and 1 commit not merged or pushed")

	// A pane working in the worktree blocks it, forced or not.
	var pane proto.PaneInfo
	if err := call(t, c, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", "sleep 30"}, Cwd: wt}, &pane); err != nil {
		t.Fatal(err)
	}
	wantErr(t, call(t, c, proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: id, Branch: "feat", Force: true}, nil), "panes are still running")
	if err := call(t, c, proto.MethodPaneClose, proto.PaneRef{ID: pane.ID}, nil); err != nil {
		t.Fatal(err)
	}

	var dr proto.BranchDiscardResult
	for i := 0; ; i++ { // the pane's entry goes once its process is reaped
		dr = proto.BranchDiscardResult{}
		err := call(t, c, proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: id, Branch: "feat", Force: true}, &dr)
		if err == nil {
			break
		}
		if i > 50 || !strings.Contains(err.Error(), "panes are still running") {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !dr.Done {
		t.Fatalf("forced discard: %+v", dr)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("forced discard left the worktree")
	}

	// A branch checked out in the main checkout stays.
	git(t, repo, "checkout", "-q", "-b", "here")
	wantErr(t, call(t, c, proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: id, Branch: "here", Force: true}, nil), "main checkout")

	// A branch that is not checked out is just deleted.
	git(t, repo, "branch", "loose", "main")
	var loose proto.BranchDiscardResult
	if err := call(t, c, proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: id, Branch: "loose"}, &loose); err != nil || !loose.Done || loose.Worktree != "" {
		t.Fatalf("loose branch: %+v %v", loose, err)
	}
	wantErr(t, call(t, c, proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: id, Branch: "loose"}, nil), "not found")
}

func TestHarvestPullRequest(t *testing.T) {
	log := filepath.Join(t.TempDir(), "gh.log")
	gh := filepath.Join(t.TempDir(), "gh")
	// pr list reports what $STATE says for feat; pr create logs and answers.
	os.WriteFile(gh, []byte(`#!/bin/sh
if [ "$1 $2" = "pr list" ]; then
  state=$(cat "`+log+`.state" 2>/dev/null)
  head=$(cat "`+log+`.head" 2>/dev/null)
  if [ -n "$state" ]; then
    printf '[{"number":5,"title":"t","state":"%s","url":"https://github.com/o/r/pull/5","headRefName":"feat","headRefOid":"%s","updatedAt":"2026-09-12T10:00:00Z"}]' "$state" "$head"
  else
    echo '[]'
  fi
  exit 0
fi
printf '%s\n' "$@" >> "`+log+`"
echo https://github.com/o/r/pull/5
`), 0o755)
	t.Setenv("CONCH_GH", gh)

	c, proj, _, wt, origin := harvestFixture(t)
	id := proj.ID
	os.WriteFile(filepath.Join(wt, "b.txt"), []byte("b\n"), 0o644)
	if err := call(t, c, proto.MethodBranchCommit, proto.BranchCommitParams{ProjectID: id, Branch: "feat", Message: "Add b"}, nil); err != nil {
		t.Fatal(err)
	}

	wantErr(t, call(t, c, proto.MethodBranchPR, proto.BranchPRParams{ProjectID: id, Branch: "main"}, nil), "is the base branch")
	var pr proto.BranchPRResult
	if err := call(t, c, proto.MethodBranchPR, proto.BranchPRParams{ProjectID: id, Branch: "feat", Title: "Add b", Body: "why", Draft: true}, &pr); err != nil {
		t.Fatal(err)
	}
	if pr.URL != "https://github.com/o/r/pull/5" {
		t.Fatalf("url %q", pr.URL)
	}
	// It pushed before asking gh.
	head := gitOut(t, wt, "rev-parse", "HEAD")
	if gitOut(t, origin, "rev-parse", "feat") != head {
		t.Fatal("the branch was not pushed")
	}
	b, _ := os.ReadFile(log)
	if got := strings.ReplaceAll(string(b), "\n", " "); got != "pr create --base main --head feat --title Add b --body why --draft " {
		t.Fatalf("gh args: %q", got)
	}

	// Once gh lists it as open, a second one is refused.
	os.WriteFile(log+".state", []byte("OPEN"), 0o644)
	waitPR := func(state string) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			// A push asks for pull requests to be fetched again.
			_ = call(t, c, proto.MethodBranchPush, proto.BranchRef{ProjectID: id, Branch: "feat"}, nil)
			var list proto.ProjectList
			if err := call(t, c, proto.MethodProjectList, nil, &list); err == nil {
				for _, p := range list.Projects {
					for _, br := range p.Branches {
						if p.ID == id && br.Name == "feat" && br.PR != nil && br.PR.State == state {
							return
						}
					}
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("pull request never became %s", state)
	}
	waitPR("OPEN")
	wantErr(t, call(t, c, proto.MethodBranchPR, proto.BranchPRParams{ProjectID: id, Branch: "feat"}, nil), "#5 is already open")

	// A pull request merged on GitHub with the branch's commit makes the
	// branch safe to discard, even though main here never got it.
	os.WriteFile(log+".head", []byte(head), 0o644)
	os.WriteFile(log+".state", []byte("MERGED"), 0o644)
	waitPR("MERGED")
	git(t, wt, "config", "branch.feat.merge", "refs/heads/elsewhere") // its upstream no longer counts
	var dry proto.BranchDiscardResult
	if err := call(t, c, proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: id, Branch: "feat", DryRun: true}, &dry); err != nil || dry.Unmerged != 0 {
		t.Fatalf("merged pull request: %+v %v", dry, err)
	}
	// A commit made after the merge is at risk again.
	os.WriteFile(filepath.Join(wt, "c.txt"), []byte("c\n"), 0o644)
	git(t, wt, "add", "c.txt")
	git(t, wt, "commit", "-q", "-m", "after the merge")
	if err := call(t, c, proto.MethodBranchDiscard, proto.BranchDiscardParams{ProjectID: id, Branch: "feat", DryRun: true}, &dry); err != nil || dry.Unmerged == 0 {
		t.Fatalf("commit after the merge: %+v %v", dry, err)
	}
}

func TestHarvestPlainFolder(t *testing.T) {
	c, dir := startServer(t)
	plain := filepath.Join(dir, "notes")
	os.MkdirAll(plain, 0o755)
	var proj proto.ProjectInfo
	if err := call(t, c, proto.MethodProjectAdd, proto.ProjectAddParams{Path: plain}, &proj); err != nil {
		t.Fatal(err)
	}
	for method, params := range map[string]any{
		proto.MethodBranchCommit:  proto.BranchCommitParams{ProjectID: proj.ID, Branch: "b", Message: "m"},
		proto.MethodBranchPush:    proto.BranchRef{ProjectID: proj.ID, Branch: "b"},
		proto.MethodBranchPR:      proto.BranchPRParams{ProjectID: proj.ID, Branch: "b"},
		proto.MethodBranchMerge:   proto.BranchMergeParams{ProjectID: proj.ID, Branch: "b"},
		proto.MethodBranchDiscard: proto.BranchDiscardParams{ProjectID: proj.ID, Branch: "b"},
	} {
		wantErr(t, call(t, c, method, params, nil), "not a git repository")
	}
}

func TestHarvestCommitHunks(t *testing.T) {
	c, proj, _, wt, _ := harvestFixture(t)
	id := proj.ID
	lines := ""
	for i := 1; i <= 30; i++ {
		lines += fmt.Sprintf("line %d\n", i)
	}
	os.WriteFile(filepath.Join(wt, "f.txt"), []byte(lines), 0o644)
	git(t, wt, "add", "f.txt")
	git(t, wt, "commit", "-q", "-m", "add f")
	edited := strings.Replace(lines, "line 1\n", "FIRST\n", 1)
	edited = strings.Replace(edited, "line 30\n", "LAST\n", 1)
	os.WriteFile(filepath.Join(wt, "f.txt"), []byte(edited), 0o644)
	os.WriteFile(filepath.Join(wt, "new.txt"), []byte("new\n"), 0o644)

	var d proto.DiffResult
	if err := call(t, c, proto.MethodProjectDiff, proto.DiffParams{ProjectID: id, Branch: "feat", File: "f.txt"}, &d); err != nil {
		t.Fatal(err)
	}
	header, hs, _ := strings.Cut(d.Diff, "@@")
	first, _, _ := strings.Cut(hs, "\n@@")
	patch := header + "@@" + first + "\n"

	// The first hunk and one whole file; the other hunk stays uncommitted.
	var res proto.CommitResult
	params := proto.BranchCommitParams{ProjectID: id, Branch: "feat", Message: "First line", Patch: patch, Files: []string{"new.txt"}}
	if err := call(t, c, proto.MethodBranchCommit, params, &res); err != nil {
		t.Fatal(err)
	}
	show := gitOut(t, wt, "show", "HEAD")
	if !strings.Contains(show, "+FIRST") || strings.Contains(show, "+LAST") || !strings.Contains(show, "new.txt") {
		t.Fatalf("committed:\n%s", show)
	}
	if st := gitOut(t, wt, "status", "--porcelain"); st != "M f.txt" {
		t.Fatalf("status %q", st)
	}

	// The same patch again: refused, and nothing is committed.
	head := gitOut(t, wt, "rev-parse", "HEAD")
	wantErr(t, call(t, c, proto.MethodBranchCommit, params, nil), "changed since the diff was read")
	if gitOut(t, wt, "rev-parse", "HEAD") != head {
		t.Fatal("a stale patch committed")
	}
	big := proto.BranchCommitParams{ProjectID: id, Branch: "feat", Message: "m", Patch: strings.Repeat("x", 5<<20)}
	wantErr(t, call(t, c, proto.MethodBranchCommit, big, nil), "patch is too large")
}

func TestProjectResolve(t *testing.T) {
	c, proj, repo, wt, _ := harvestFixture(t)

	// A worktree made a moment ago is already known: resolve reads git, not
	// the last refresh.
	var fresh proto.WorktreeResult
	if err := call(t, c, proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "brand-new"}, &fresh); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{repo: "main", wt: "feat", fresh.Path: "brand-new",
		filepath.Join(wt, "deep", "..", "."): "feat"} {
		var place proto.ProjectPlace
		if err := call(t, c, proto.MethodProjectResolve, proto.ProjectAddParams{Path: path}, &place); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if place.ProjectID != proj.ID || place.Branch != want || place.Base != "main" || !place.Git || place.Root != repo {
			t.Fatalf("%s: %+v, want branch %s", path, place, want)
		}
	}

	// A detached checkout has no branch; a plain folder is its own project.
	det := filepath.Join(filepath.Dir(repo), "api.worktrees", "det")
	git(t, repo, "worktree", "add", "-q", "--detach", det, "main")
	var place proto.ProjectPlace
	if err := call(t, c, proto.MethodProjectResolve, proto.ProjectAddParams{Path: det}, &place); err != nil {
		t.Fatal(err)
	}
	if place.Branch != "" || place.Worktree != det {
		t.Fatalf("detached: %+v", place)
	}
	notes := filepath.Join(filepath.Dir(repo), "notes")
	os.MkdirAll(notes, 0o755)
	var plain proto.ProjectPlace
	if err := call(t, c, proto.MethodProjectResolve, proto.ProjectAddParams{Path: notes}, &plain); err != nil {
		t.Fatal(err)
	}
	if plain.Git || plain.Branch != "" || plain.Root != notes {
		t.Fatalf("plain folder: %+v", plain)
	}
	wantErr(t, call(t, c, proto.MethodProjectResolve, proto.ProjectAddParams{Path: filepath.Join(repo, "nope")}, nil), "no such file")
}
