package tui

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// featBranch is the fixture's feat branch, to set its stage.
func featBranch(t *testing.T, m *Model) *proto.BranchInfo {
	t.Helper()
	proj := m.project(localMachine, "r1")
	for i := range proj.Branches {
		if proj.Branches[i].Name == "feat" {
			return &proj.Branches[i]
		}
	}
	t.Fatal("no feat branch")
	return nil
}

// committedChanges is a changes view showing a branch's committed work,
// with nothing uncommitted: what an agent that commits its own work
// leaves behind.
func committedChanges() *changesView {
	d := a2Changes("a.go", "b.go")
	d.Worktree = "" // the files are the branch's commits, not uncommitted work
	return &changesView{machine: localMachine, projectID: "r1", branch: "feat", data: &d}
}

// TestNextStep: the step that finishing a committed branch comes to, at
// each stage.
func TestNextStep(t *testing.T) {
	m, _ := harvestModel(t, harvestCapability)
	b := featBranch(t, m)
	open := b.PR
	for _, tc := range []struct {
		name  string
		stage func()
		want  string
	}{
		{"never pushed", func() { b.PR, b.Upstream, b.Ahead, b.BaseAhead = nil, "", 0, 2 }, "P push · p pull request · M merge"},
		{"commits not pushed", func() { b.Upstream, b.Ahead = "origin/feat", 1 }, "P push · p pull request · M merge"},
		{"pushed", func() { b.Ahead = 0 }, "p pull request · M merge"},
		{"a pull request open", func() { b.PR = open }, "o its pull request · M merge"},
		{"a closed pull request is no pull request", func() { b.PR = &proto.PRInfo{State: "CLOSED"} }, "p pull request · M merge"},
		{"merged", func() { b.PR, b.BaseAhead = nil, 0 }, "main has it all · D removes the branch and worktree"},
	} {
		tc.stage()
		if got := m.nextStep(localMachine, "r1", "feat"); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	// The base branch, a branch conch does not know, a project it does not.
	for _, args := range [][3]string{{localMachine, "r1", "main"}, {localMachine, "r1", "nosuch"}, {localMachine, "nosuch", "feat"}, {"box", "r1", "feat"}} {
		if got := m.nextStep(args[0], args[1], args[2]); got != "" {
			t.Errorf("%v: %q", args, got)
		}
	}
}

// TestChangesHintFollowsTheStage: with nothing uncommitted, the changes
// view says what comes next rather than "c commit", which would only be
// told there is nothing to commit.
func TestChangesHintFollowsTheStage(t *testing.T) {
	m, _ := harvestModel(t, harvestCapability)
	m.focus = focusMain
	b := featBranch(t, m)
	b.PR, b.Upstream = nil, ""

	cv := committedChanges()
	if got := cv.listHint(*m); got != "P push · p pull request · M merge" {
		t.Errorf("committed: %q", got)
	}
	// Uncommitted work: committing it is the step.
	d := a2Changes("a.go")
	cv.data = &d
	if got := cv.listHint(*m); !strings.Contains(got, "c commit") {
		t.Errorf("uncommitted: %q", got)
	}
	// A clean worktree reads as the branch's committed work too.
	d.Files = nil
	if got := cv.listHint(*m); got != "P push · p pull request · M merge" {
		t.Errorf("clean worktree: %q", got)
	}
	// The base branch has no next step of this kind.
	base := committedChanges()
	base.branch = "main"
	if got := base.listHint(*m); got != "enter diff · esc back" {
		t.Errorf("base: %q", got)
	}
	// The tree still has focus: how to get in, as before.
	m.focus = focusSidebar
	if got := cv.listHint(*m); got != "enter to work in these changes" {
		t.Errorf("unfocused: %q", got)
	}
}

// TestFinishingATaskByKeys walks a task's branch from committed work to
// merged and removed, by the keys the changes view says, each step's
// message saying the next.
func TestFinishingATaskByKeys(t *testing.T) {
	m, peer := harvestModel(t, harvestCapability)
	m.focus = focusMain
	b := featBranch(t, m)
	b.PR, b.Upstream, b.Ahead, b.BaseAhead = nil, "", 0, 2
	cv := committedChanges()

	// P pushes, and says a pull request is next.
	if !strings.HasPrefix(cv.listHint(*m), "P push") {
		t.Fatalf("hint: %q", cv.listHint(*m))
	}
	_, cmd := cv.key(m, a2Key("P"))
	if done := a2Run(cmd)[0].(harvestDoneMsg); done.text != "pushed feat · p opens a pull request" {
		t.Fatalf("push: %q", done.text)
	}
	b.Upstream = "origin/feat" // as the next refresh says

	// p asks for the pull request, and says merging is next.
	if got := cv.listHint(*m); got != "p pull request · M merge" {
		t.Fatalf("after the push: %q", got)
	}
	cv.key(m, a2Key("p"))
	peer.setResult(proto.MethodBranchPR, proto.BranchPRResult{URL: "https://example.invalid/pr/9"})
	if done := submitDialog(t, m)[0].(harvestDoneMsg); !strings.HasPrefix(done.text, "opened https://example.invalid/pr/9") {
		t.Fatalf("pull request: %q", done.text)
	}
	b.PR = &proto.PRInfo{Number: 9, State: "OPEN", URL: "https://example.invalid/pr/9"}

	// With the pull request open, o reaches it and M merges.
	if got := cv.listHint(*m); got != "o its pull request · M merge" {
		t.Fatalf("pull request open: %q", got)
	}
	cv.key(m, a2Key("M"))
	peer.setResult(proto.MethodBranchMerge, proto.CommitResult{Hash: "abcdef0123", Into: "main"})
	if done := submitDialog(t, m)[0].(harvestDoneMsg); !strings.HasSuffix(done.text, "· D removes the branch and its worktree") {
		t.Fatalf("merge: %q", done.text)
	}
	b.PR, b.BaseAhead = nil, 0

	// Merged: D, which asks the server what removing it would lose.
	if got := cv.listHint(*m); !strings.Contains(got, "D removes the branch") {
		t.Fatalf("merged: %q", got)
	}
	peer.setResult(proto.MethodBranchDiscard, proto.BranchDiscardResult{})
	if _, cmd := cv.key(m, a2Key("D")); cmd == nil {
		t.Fatal("D did nothing")
	} else {
		a2Run(cmd)
	}
	if p := lastParams[proto.BranchDiscardParams](t, peer, proto.MethodBranchDiscard); !p.DryRun || p.Branch != "feat" {
		t.Fatalf("discard asked %+v", p)
	}
}

// TestAfterACommit: a commit says how to push it — to a new pull request,
// to the one open, or simply pushed, for the base branch.
func TestAfterACommit(t *testing.T) {
	m, peer := harvestModel(t, harvestCapability)
	b := featBranch(t, m)
	peer.setResult(proto.MethodBranchCommit, proto.CommitResult{Hash: "0123456789abcdef"})
	for _, tc := range []struct {
		target harvestTarget
		pr     *proto.PRInfo
		want   string
	}{
		{feat, nil, "committed 0123456 on feat · P pushes it, p opens a pull request"},
		{feat, &proto.PRInfo{State: "OPEN"}, "committed 0123456 on feat · P pushes it to its pull request"},
		{feat, &proto.PRInfo{State: "MERGED"}, "committed 0123456 on feat · P pushes it, p opens a pull request"},
		{harvestTarget{machine: localMachine, projectID: "r1", branch: "main"}, nil, "committed 0123456 on main · P pushes it"},
	} {
		b.PR = tc.pr
		m.openCommit(tc.target, commitSelection{})
		a2Type(m, m.overlay.(*dialog), "a change")
		if done := submitDialog(t, m)[0].(harvestDoneMsg); done.text != tc.want {
			t.Errorf("%s, pr %v: %q, want %q", tc.target.branch, tc.pr, done.text, tc.want)
		}
	}
}

// TestAfterASquashMerge: a squash merge — the default — leaves the
// branch's commit off the base, so git still counts it ahead and the
// hint went back to "M merge". The first end-to-end run found it.
func TestAfterASquashMerge(t *testing.T) {
	m, _ := harvestModel(t, harvestCapability)
	m.focus = focusMain
	b := featBranch(t, m)
	b.PR, b.Upstream, b.Ahead, b.BaseAhead = nil, "origin/feat", 0, 1
	cv := committedChanges()

	m.receiveHarvest(harvestDoneMsg{target: feat, text: "merged", merged: true})
	if got := cv.listHint(*m); got != "merged into main · D removes the branch and worktree" {
		t.Fatalf("after the merge: %q", got)
	}
	// The branch grows again: the merge no longer covers it.
	b.BaseAhead = 2
	if got := cv.listHint(*m); got != "p pull request · M merge" {
		t.Fatalf("new commits: %q", got)
	}
	// Discarded and made again under the same name: a new branch.
	m.receiveHarvest(harvestDoneMsg{target: feat, text: "discarded", discarded: true})
	b.BaseAhead = 1
	if got := cv.listHint(*m); got != "p pull request · M merge" {
		t.Fatalf("a new branch of the same name: %q", got)
	}
}

// TestChangesBarFollowsTheStage: with nothing uncommitted the status bar
// leaves out marking and committing, so P, p and M are not pushed off it.
func TestChangesBarFollowsTheStage(t *testing.T) {
	m, _ := harvestModel(t, harvestCapability)
	m.focus = focusMain
	m.tab().focused().view = viewRef{Row: branchNodeID(localMachine, "r1", "feat"), Kind: kindBranch, Machine: localMachine, ProjectID: "r1", Branch: "feat"}
	hints := func() string {
		_, items := m.statusHints()
		var words []string
		for _, it := range items {
			words = append(words, a2Plain([]string{it.text}))
		}
		return strings.Join(words, " | ")
	}
	cv := committedChanges()
	m.tab().focused().changes = cv
	if h := hints(); strings.Contains(h, "commit") || strings.Contains(h, "mark") || !strings.Contains(h, "push") {
		t.Errorf("committed: %s", h)
	}
	d := a2Changes("a.go")
	cv.data = &d
	if h := hints(); !strings.Contains(h, "commit") || !strings.Contains(h, "mark") {
		t.Errorf("uncommitted: %s", h)
	}
	cv.data = nil // not loaded yet: the keys stay said
	if h := hints(); !strings.Contains(h, "commit") {
		t.Errorf("loading: %s", h)
	}
}
