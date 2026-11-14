package phone

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// fakePRs is what the fake gh says `gh pr list` found: an open pull
// request for feature, approved, with one check passed and one running.
const fakePRs = `[{"number":42,"title":"Add the feature","state":"OPEN","isDraft":false,
"url":"https://github.com/o/api/pull/42","headRefName":"feature","headRefOid":"abc",
"isCrossRepository":false,"baseRefName":"main","reviewDecision":"APPROVED","mergeable":"MERGEABLE",
"updatedAt":"2026-11-01T00:00:00Z","statusCheckRollup":[
{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"},
{"__typename":"CheckRun","name":"lint","status":"IN_PROGRESS","conclusion":""}]}]`

// projectRepo makes a repository on main with a worktree on feature one
// commit ahead and an uncommitted change, gives the servers a gh that
// reports feature's pull request, and returns the two checkouts.
func projectRepo(t *testing.T, dir string) (repo, feature string) {
	t.Helper()
	gh := filepath.Join(dir, "fake-gh-prs")
	script := "#!/bin/sh\nif [ \"$1 $2\" = \"pr list\" ]; then cat <<'EOF'\n" + fakePRs + "\nEOF\nelse echo '[]'; fi\n"
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONCH_GH", gh)
	repo = filepath.Join(dir, "api")
	feature = filepath.Join(dir, "api-feature")
	os.MkdirAll(repo, 0o755)
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "config", "user.name", "t")
	gitIn(t, repo, "config", "user.email", "t@example.com")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\ntwo\n"), 0o644)
	gitIn(t, repo, "add", "a.txt")
	gitIn(t, repo, "commit", "-q", "-m", "init")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "feature", feature)
	gitIn(t, feature, "commit", "-q", "--allow-empty", "-m", "feature work")
	os.WriteFile(filepath.Join(feature, "a.txt"), []byte("one\n2\nthree\n"), 0o644)
	return repo, feature
}

func TestProjectDetail(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermView)
	repo, feature := projectRepo(t, f.dir)
	var proj proto.ProjectInfo
	f.call(proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj)
	// An agent at work in the feature worktree, and a terminal there that
	// is not one.
	var agent, term proto.PaneInfo
	f.call(proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude",
		Command: []string{"/bin/sh", "-c", "exec sleep 30"}, Cwd: feature, Cols: 80, Rows: 24}, &agent)
	f.call(proto.MethodPaneCreate, proto.PaneCreateParams{
		Command: []string{"/bin/sh", "-c", "exec sleep 30"}, Cwd: feature, Cols: 80, Rows: 24}, &term)

	path := "/api/project?project=" + proj.ID
	var got ProjectDetail
	waitFor(t, "the project's worktrees, pull request and agent", func() bool {
		got = ProjectDetail{}
		if p.get(path, &got) != 200 || len(got.Worktrees) != 2 || len(got.PRs) != 1 {
			return false
		}
		fw := got.Worktrees[1]
		return fw.Ahead == 1 && fw.Dirty && fw.PR == 42 && len(fw.Panes) == 1
	})
	if got.Project.ID != proj.ID || got.Project.Name != "api" || got.Project.Base != "main" {
		t.Errorf("project %+v", got.Project)
	}
	main, fw := got.Worktrees[0], got.Worktrees[1]
	if !main.Base || main.Branch != "main" || main.Dirty || main.Ahead != 0 || main.PR != 0 || len(main.Panes) != 0 {
		t.Errorf("the main checkout %+v", main)
	}
	want := Worktree{Branch: "feature", Path: fw.Path, Panes: []string{"local:" + agent.ID},
		Ahead: 1, Added: 2, Removed: 1, Dirty: true, PR: 42}
	if !reflect.DeepEqual(fw, want) || !strings.HasSuffix(fw.Path, "api-feature") {
		t.Errorf("the feature worktree\n got %+v\nwant %+v", fw, want)
	}
	wantPR := PullRequest{Number: 42, Title: "Add the feature", Branch: "feature", URL: "https://github.com/o/api/pull/42",
		State: PROpen, Review: "approved", Checks: []PRCheck{{Name: "test", State: "pass"}, {Name: "lint", State: "pending"}}}
	if !reflect.DeepEqual(got.PRs[0], wantPR) {
		t.Errorf("the pull request\n got %+v\nwant %+v", got.PRs[0], wantPR)
	}
	// A fake agent reports no usage, so there is none to show.
	if got.Usage != nil {
		t.Errorf("usage %+v from an agent that reported none", got.Usage)
	}

	// Optional fields are left out, never null.
	_, raw := p.do("GET", path, nil)
	if strings.Contains(string(raw), "null") || strings.Contains(string(raw), `"usage"`) {
		t.Errorf("null or empty usage in %s", raw)
	}

	// Asking wrongly, or about nothing.
	for q, code := range map[string]int{
		"/api/project":               400,
		"/api/project?project=rnope": 404,
		"/api/project?project=" + proj.ID + "&machine=Bad!": 400,
	} {
		if status, b := p.do("GET", q, nil); status != code {
			t.Errorf("%s: %d %s, want %d", q, status, b, code)
		}
	}
}

// TestProjectDetailHonoursMachines: a project's page is that machine's,
// out of reach for a device not given it, and not found on one that is
// not there.
func TestProjectDetailHonoursMachines(t *testing.T) {
	f := newFixture(t)
	other := addMachine(t, f, "busybox", true)
	p := f.pair(PermView)
	var tp proto.ProjectInfo
	callOn(t, other, proto.MethodProjectAdd, proto.ProjectAddParams{Path: t.TempDir()}, &tp)

	status, b := p.do("GET", "/api/project?machine=busybox&project="+tp.ID, nil)
	if status != 403 || !strings.Contains(string(b), CodeForbidden) {
		t.Fatalf("an ungranted machine: %d %s", status, b)
	}
	f.grant(p, "busybox", "nowhere")
	waitMachine(t, f, p, "busybox", MachineOnline)
	var got ProjectDetail
	if status := p.get("/api/project?machine=busybox&project="+tp.ID, &got); status != 200 {
		t.Fatalf("a granted machine: %d", status)
	}
	// A plain folder: no worktrees, no pull requests, and both still lists.
	if got.Project.ID != tp.ID || got.Worktrees == nil || len(got.Worktrees) != 0 || got.PRs == nil || len(got.PRs) != 0 {
		t.Fatalf("busybox's project %+v", got)
	}
	// This computer does not have busybox's project.
	if status := p.get("/api/project?project="+tp.ID, nil); status != 404 {
		t.Fatalf("busybox's project asked of this computer: %d", status)
	}
	if status, b := p.do("GET", "/api/project?machine=nowhere&project="+tp.ID, nil); status != 404 {
		t.Fatalf("a machine that is not there: %d %s", status, b)
	}
}

// TestProjectDetailFromAnOlderConch: a machine whose conch has no pull
// requests still answers with its worktrees, and the pull requests are
// empty rather than an error.
func TestProjectDetailFromAnOlderConch(t *testing.T) {
	f := newFixture(t)
	other := addMachine(t, f, "busybox", true, runningAnOlderConch(capProjectPR))
	p := f.pair(PermView)
	f.grant(p, "busybox")
	waitMachine(t, f, p, "busybox", MachineOnline)
	repo, _ := projectRepo(t, f.dir)
	var tp proto.ProjectInfo
	callOn(t, other, proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &tp)
	// The server there has the pull request; the gateway must not take
	// it from a conch that did not announce them.
	waitFor(t, "busybox to read the pull request", func() bool {
		var list proto.ProjectList
		callOn(t, other, proto.MethodProjectList, nil, &list)
		for _, b := range list.Projects[0].Branches {
			if b.PR != nil {
				return true
			}
		}
		return false
	})
	var got ProjectDetail
	waitFor(t, "the worktrees", func() bool {
		got = ProjectDetail{}
		return p.get("/api/project?machine=busybox&project="+tp.ID, &got) == 200 && len(got.Worktrees) == 2
	})
	if got.PRs == nil || len(got.PRs) != 0 {
		t.Errorf("pull requests from a conch without them: %+v", got.PRs)
	}
	for _, w := range got.Worktrees {
		if w.PR != 0 {
			t.Errorf("worktree %s names pull request %d", w.Branch, w.PR)
		}
	}
	if fw := got.Worktrees[1]; fw.Branch != "feature" || fw.Ahead != 1 || !fw.Dirty {
		t.Errorf("the feature worktree %+v", fw)
	}
}

// The page from what a server said: states, reviews, usage and the
// checkouts that are not on a branch.
func TestBuildProjectDetail(t *testing.T) {
	proj := proto.ProjectInfo{ID: "r1", Name: "api", Path: "/src/api", Base: "main", Git: true,
		Worktrees: []proto.WorktreeInfo{
			{Path: "/src/api", Branch: "main", Main: true, Status: &proto.GitStatus{}},
			{Path: "/src/api-x", Detached: true, Head: "abcdef1"},
			{Path: "/src/api-d", Branch: "draft", Status: &proto.GitStatus{Untracked: 1, Files: 1}},
		},
		Branches: []proto.BranchInfo{
			{Name: "draft", BaseAhead: 3, BaseBehind: 2, PR: &proto.PRInfo{Number: 7, State: "OPEN", Draft: true, Review: "CHANGES_REQUESTED"}},
			{Name: "old", PR: &proto.PRInfo{Number: 5, State: "MERGED", Checks: "pass", Total: 2, Passed: 2}},
			{Name: "gone", PR: &proto.PRInfo{Number: 4, State: "CLOSED"}},
			{Name: "main"},
		}}
	agent := func(id, project, branch string, tok *proto.Tokens) proto.PaneInfo {
		return proto.PaneInfo{ID: id, ProjectID: project, Branch: branch, State: proto.PaneRunning,
			Agent: &proto.AgentStatus{Name: "claude", Tokens: tok}}
	}
	panes := []proto.PaneInfo{
		agent("p1", "r1", "draft", &proto.Tokens{Input: 10, CacheRead: 100, Output: 5, CostUSD: 1.5}),
		agent("p2", "r1", "draft", &proto.Tokens{Input: 1, Output: 1}),
		agent("p3", "r1", "", nil),                                             // no branch, no usage
		agent("p4", "r2", "draft", &proto.Tokens{Output: 99, CostUSD: 9}),      // another project's
		{ID: "p5", ProjectID: "r1", Branch: "draft", State: proto.PaneRunning}, // a terminal
	}
	got := buildProjectDetail("busybox", proj, panes, true)
	if d := got.Worktrees[1]; d.Branch != "" || d.Path != "/src/api-x" || d.Panes != nil || d.Dirty {
		t.Errorf("the detached checkout %+v", d)
	}
	if d := got.Worktrees[2]; !reflect.DeepEqual(d.Panes, []string{"busybox:p1", "busybox:p2"}) ||
		d.Ahead != 3 || d.Behind != 2 || !d.Dirty || d.PR != 7 {
		t.Errorf("the draft worktree %+v", d)
	}
	var states, reviews []string
	for _, pr := range got.PRs {
		states = append(states, pr.State)
		reviews = append(reviews, pr.Review)
		if pr.Checks == nil {
			t.Errorf("pull request %d's checks are null", pr.Number)
		}
	}
	if strings.Join(states, ",") != "draft,merged,closed" || reviews[0] != "changes requested" || reviews[1] != "" {
		t.Errorf("states %v reviews %q", states, reviews)
	}
	wantUsage := &ProjectUsage{Agents: 2, CostUSD: 1.5, CostAgents: 1, InputTokens: 111, OutputTokens: 6}
	if !reflect.DeepEqual(got.Usage, wantUsage) {
		t.Errorf("usage %+v, want %+v", got.Usage, wantUsage)
	}

	// Without pull requests: none listed, none named on a worktree.
	got = buildProjectDetail("local", proj, nil, false)
	if len(got.PRs) != 0 || got.PRs == nil || got.Worktrees[2].PR != 0 || got.Usage != nil {
		t.Errorf("without pull requests %+v", got)
	}
	// Nothing at all is still lists, not nulls.
	b, _ := json.Marshal(buildProjectDetail("local", proto.ProjectInfo{ID: "r9"}, nil, true))
	if want := `{"project":{"id":"r9","name":"","path":""},"worktrees":[],"prs":[]}`; string(b) != want {
		t.Errorf("an empty project is %s, want %s", b, want)
	}
}
