package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// harvestModel is a1Fixture online through a peer advertising caps: project
// r1 with main checked out at /src/api and feat (2 commits ahead, open PR
// #7, 2 uncommitted files) at /src/api-feat.
func harvestModel(t *testing.T, caps ...string) (*Model, *a1Peer) {
	t.Helper()
	m, _ := a1Fixture(t, false)
	c, peer := a1FakeClient(t, caps...)
	m.machines[0].c, m.machines[0].server = c, c.Server
	m.rebuild()
	return m, peer
}

var feat = harvestTarget{machine: localMachine, projectID: "r1", branch: "feat"}

// submitDialog presses enter in the open dialog and returns what its
// command produced.
func submitDialog(t *testing.T, m *Model) []tea.Msg {
	t.Helper()
	d, ok := m.overlay.(*dialog)
	if !ok {
		t.Fatalf("no dialog open: %T", m.overlay)
	}
	_, cmd := d.update(m, a2Key("enter"))
	return a2Run(cmd)
}

func lastParams[T any](t *testing.T, peer *a1Peer, method string) T {
	t.Helper()
	msg := peer.waitMethod(t, method, "")
	var p T
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHarvestNeedsAGitBranchAndANewServer(t *testing.T) {
	old, _ := harvestModel(t, "project.v1")
	if old.openCommit(feat, nil) != nil || old.overlay != nil || !strings.Contains(old.flash, "predates") {
		t.Fatalf("old server: overlay %T flash %q", old.overlay, old.flash)
	}
	if items := harvestMenuItems(*old, row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"}); items != nil {
		t.Fatalf("an old server's branch menu offers %d harvest items", len(items))
	}

	offline, _ := a1Fixture(t, false)
	if offline.pushBranch(feat) != nil || !strings.Contains(offline.flash, "local is") {
		t.Fatalf("offline: %q", offline.flash)
	}

	m, _ := harvestModel(t, harvestCapability)
	for name, target := range map[string]harvestTarget{
		"unknown project": {machine: localMachine, projectID: "nope", branch: "feat"},
		"no branch":       {machine: localMachine, projectID: "r1"},
	} {
		m.flash = ""
		if m.pushBranch(target) != nil || !strings.Contains(m.flash, "select a branch") {
			t.Fatalf("%s: %q", name, m.flash)
		}
	}
	main := harvestTarget{machine: localMachine, projectID: "r1", branch: "main"}
	for name, open := range map[string]func() tea.Cmd{
		"pull request": func() tea.Cmd { return m.openPullRequest(main) },
		"merge":        func() tea.Cmd { return m.openMerge(main) },
		"discard":      func() tea.Cmd { return m.discardBranch(main) },
	} {
		m.flash = ""
		if cmd := open(); cmd != nil || m.overlay != nil || m.flash != "main is the base branch" {
			t.Fatalf("%s of the base: overlay %T flash %q", name, m.overlay, m.flash)
		}
	}
	// Committing and pushing the base are fine.
	if m.openCommit(main, nil); m.overlay == nil {
		t.Fatalf("commit on main: %q", m.flash)
	}
}

func TestHarvestCommit(t *testing.T) {
	m, peer := harvestModel(t, harvestCapability)
	m.openCommit(harvestTarget{machine: localMachine, projectID: "r1", branch: "loose"}, nil)
	if m.overlay != nil || !strings.Contains(m.flash, "not checked out") {
		t.Fatalf("branch without a checkout: %q", m.flash)
	}

	m.openCommit(feat, nil)
	d := m.overlay.(*dialog)
	if text := strings.Join(d.text, " "); !strings.Contains(text, "every change in /src/api-feat") {
		t.Fatalf("commit-all text %q", text)
	}
	if msgs := submitDialog(t, m); len(msgs) != 1 || msgs[0].(errMsg).err.Error() != "a commit needs a message" {
		t.Fatalf("empty message: %v", msgs)
	}

	m.openCommit(feat, []string{"a.go", "old.go", "new.go", "b.go", "c.go"})
	d = m.overlay.(*dialog)
	if text := strings.Join(d.text, " "); !strings.Contains(text, "the 5 files marked in /src/api-feat: a.go, old.go, new.go, b.go and 1 more") {
		t.Fatalf("marked text %q", text)
	}
	a2Type(m, d, "  Fix the thing  ")
	peer.setResult(proto.MethodBranchCommit, proto.CommitResult{Hash: "0123456789abcdef"})
	msgs := submitDialog(t, m)
	done, ok := msgs[0].(harvestDoneMsg)
	if !ok || done.text != "committed 0123456 on feat" || !done.committed || done.target != feat {
		t.Fatalf("commit result: %#v", msgs)
	}
	p := lastParams[proto.BranchCommitParams](t, peer, proto.MethodBranchCommit)
	if p.Message != "Fix the thing" || p.Branch != "feat" || p.ProjectID != "r1" || len(p.Files) != 5 {
		t.Fatalf("params %+v", p)
	}

	// A server error reaches the status bar.
	peer.setError(proto.MethodBranchCommit, "nothing to commit")
	m.openCommit(feat, nil)
	a2Type(m, m.overlay.(*dialog), "x")
	if msgs := submitDialog(t, m); !strings.Contains(a2ErrText(msgs), "nothing to commit") {
		t.Fatalf("server error: %v", msgs)
	}
}

func TestHarvestPushAndPullRequest(t *testing.T) {
	m, peer := harvestModel(t, harvestCapability)
	cmd := m.pushBranch(feat)
	if m.flash != "pushing feat…" {
		t.Fatalf("flash %q", m.flash)
	}
	if msgs := a2Run(cmd); msgs[0].(harvestDoneMsg).text != "pushed feat" {
		t.Fatalf("push: %v", msgs)
	}
	if p := lastParams[proto.BranchRef](t, peer, proto.MethodBranchPush); p.Branch != "feat" {
		t.Fatalf("push params %+v", p)
	}
	peer.setError(proto.MethodBranchPush, "rejected")
	if !strings.Contains(a2ErrText(a2Run(m.pushBranch(feat))), "rejected") {
		t.Fatal("push error not shown")
	}

	// feat's pull request is open: o opens it instead.
	if m.openPullRequest(feat); m.overlay != nil || !strings.Contains(m.flash, "#7 is already open") {
		t.Fatalf("open PR: %q", m.flash)
	}
	m.machines[0].projects[0].Branches[1].PR.State = "CLOSED"
	m.openPullRequest(feat)
	d := m.overlay.(*dialog)
	if len(d.fields) != 3 || d.fields[2].check == nil || *d.fields[2].check {
		t.Fatalf("fields: %+v", d.fields)
	}
	a2Type(m, d, "Add login")
	d.update(m, a2Key("tab"))
	a2Type(m, d, "Because")
	d.update(m, a2Key("tab"))
	d.update(m, a2Key(" ")) // draft
	peer.setResult(proto.MethodBranchPR, proto.BranchPRResult{URL: "https://example.invalid/pr/8"})
	msgs := submitDialog(t, m)
	if msgs[0].(harvestDoneMsg).text != "opened https://example.invalid/pr/8" {
		t.Fatalf("pr: %v", msgs)
	}
	p := lastParams[proto.BranchPRParams](t, peer, proto.MethodBranchPR)
	if p.Title != "Add login" || p.Body != "Because" || !p.Draft || p.Branch != "feat" {
		t.Fatalf("pr params %+v", p)
	}
}

func TestHarvestMerge(t *testing.T) {
	m, peer := harvestModel(t, harvestCapability)
	m.openMerge(feat)
	d := m.overlay.(*dialog)
	if text := strings.Join(d.text, " "); !strings.Contains(text, "Merges the 2 commits of feat into main in /src/api") {
		t.Fatalf("merge text %q", text)
	}
	if !*d.fields[1].check {
		t.Fatal("squash is not the default")
	}
	peer.setResult(proto.MethodBranchMerge, proto.CommitResult{Hash: "abcdef0123", Into: "main"})
	if msgs := submitDialog(t, m); msgs[0].(harvestDoneMsg).text != "merged feat into main (abcdef0)" {
		t.Fatalf("merge: %v", msgs)
	}
	if p := lastParams[proto.BranchMergeParams](t, peer, proto.MethodBranchMerge); !p.Squash || p.Message != "" || p.Branch != "feat" {
		t.Fatalf("merge params %+v", p)
	}

	// Nothing to merge, or nowhere to merge it.
	proj := &m.machines[0].projects[0]
	proj.Branches[1].BaseAhead = 0
	if m.openMerge(feat); m.overlay != nil || !strings.Contains(m.flash, "no commits that main lacks") {
		t.Fatalf("nothing ahead: %q", m.flash)
	}
	proj.Worktrees[0].Branch = "elsewhere"
	if m.openMerge(feat); m.overlay != nil || !strings.Contains(m.flash, "isn't checked out") {
		t.Fatalf("base not checked out: %q", m.flash)
	}
}

func TestHarvestDiscard(t *testing.T) {
	m, peer := harvestModel(t, harvestCapability)
	peer.setResult(proto.MethodBranchDiscard, proto.BranchDiscardResult{Worktree: "/src/api-feat",
		Uncommitted: []string{"a.go", "b.go", "c.go", "d.go"}, Unmerged: 2})
	msgs := a2Run(m.discardBranch(feat))
	plan, ok := msgs[0].(discardPlanMsg)
	if !ok || plan.plan.Unmerged != 2 {
		t.Fatalf("plan: %v", msgs)
	}
	if p := lastParams[proto.BranchDiscardParams](t, peer, proto.MethodBranchDiscard); !p.DryRun || p.Force {
		t.Fatalf("dry run params %+v", p)
	}

	next, _ := m.update(plan)
	*m = next.(Model)
	d, ok := m.overlay.(*dialog)
	if !ok || !d.yesOnly {
		t.Fatalf("discard confirm: %T", m.overlay)
	}
	text := strings.Join(d.text, " ")
	for _, want := range []string{"Removes the worktree /src/api-feat and deletes branch feat.",
		"4 uncommitted files (a.go, b.go, c.go and 1 more) and 2 commits not merged or pushed, for good."} {
		if !strings.Contains(text, want) {
			t.Fatalf("confirm text %q lacks %q", text, want)
		}
	}
	// Enter doesn't discard; only y does.
	if closed, cmd := d.update(m, a2Key("enter")); closed || cmd != nil || m.overlay == nil {
		t.Fatal("enter confirmed a discard")
	}
	if out := a2Plain(d.render(*m).lines); !strings.Contains(out, "y yes · n or esc no") {
		t.Fatalf("hint:\n%s", out)
	}
	peer.setResult(proto.MethodBranchDiscard, proto.BranchDiscardResult{Done: true})
	_, cmd := d.update(m, a2Key("y"))
	if msgs := a2Run(cmd); msgs[0].(harvestDoneMsg).text != "discarded feat" {
		t.Fatalf("discard: %v", msgs)
	}
	forced := peer.waitFor(t, "forced discard", func(msg proto.Message) bool {
		return msg.Method == proto.MethodBranchDiscard && strings.Contains(string(msg.Params), `"force":true`)
	})
	if strings.Contains(string(forced.Params), "dry_run") {
		t.Fatalf("forced params %s", forced.Params)
	}

	// Nothing to lose: no force, so work made since the plan still stops it.
	m2, peer2 := harvestModel(t, harvestCapability)
	m2.confirmDiscard(discardPlanMsg{target: feat, plan: proto.BranchDiscardResult{}})
	d = m2.overlay.(*dialog)
	if text := strings.Join(d.text, " "); !strings.Contains(text, "Deletes branch feat.") || !strings.Contains(text, "Nothing is lost") {
		t.Fatalf("safe text %q", text)
	}
	_, cmd = d.update(m2, a2Key("y"))
	a2Run(cmd)
	if p := lastParams[proto.BranchDiscardParams](t, peer2, proto.MethodBranchDiscard); p.Force || p.DryRun {
		t.Fatalf("safe discard params %+v", p)
	}
	// n and esc keep the branch.
	for _, k := range []string{"n", "esc"} {
		m2.confirmDiscard(discardPlanMsg{target: feat})
		if closed, cmd := m2.overlay.(*dialog).update(m2, a2Key(k)); !closed || cmd != nil || m2.overlay != nil {
			t.Fatalf("%s did not cancel", k)
		}
	}

	peer2.setError(proto.MethodBranchDiscard, "panes are still running")
	if !strings.Contains(a2ErrText(a2Run(m2.discardBranch(feat))), "panes are still running") {
		t.Fatal("dry-run error not shown")
	}
}

func TestHarvestMenu(t *testing.T) {
	m, _ := harvestModel(t, harvestCapability)
	labels := func(branch string) string {
		return a2MenuLabels(newRowMenu(*m, row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: branch}, 0, 0))
	}
	got := labels("feat")
	for _, want := range []string{"C Commit all changes…", "P Push", "M Merge into main…", "D Discard branch and worktree…"} {
		if !strings.Contains(got, want) {
			t.Fatalf("feat menu lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Open a pull request") {
		t.Fatalf("feat already has an open pull request:\n%s", got)
	}
	m.machines[0].projects[0].Branches[1].PR = nil
	if got := labels("feat"); !strings.Contains(got, "p Open a pull request…") {
		t.Fatalf("no PR yet:\n%s", got)
	}
	// main is clean and the base: push only.
	got = labels("main")
	if !strings.Contains(got, "P Push") || strings.Contains(got, "Commit") || strings.Contains(got, "Merge") || strings.Contains(got, "Discard") {
		t.Fatalf("main menu:\n%s", got)
	}
	// Menu keys are unique, so each runs what it says.
	mu := newRowMenu(*m, row{kind: kindBranch, machine: localMachine, projectID: "r1", branch: "feat"}, 0, 0)
	seen := map[string]string{}
	for _, it := range mu.items {
		if it.key == "" {
			continue
		}
		if prev, dup := seen[it.key]; dup {
			t.Fatalf("key %s used by %q and %q", it.key, prev, it.label)
		}
		seen[it.key] = it.label
	}
}

func TestChangesViewHarvestKeys(t *testing.T) {
	m, peer := harvestModel(t, harvestCapability)
	cv := &changesView{machine: localMachine, projectID: "r1", branch: "feat"}
	// Before data arrives space marks nothing.
	cv.key(m, a2Key(" "))
	if len(cv.marked) != 0 {
		t.Fatal("marked without data")
	}
	d := proto.Changes{ProjectID: "r1", Branch: "feat", Base: "main", Worktree: "/src/api-feat", Files: []proto.FileChange{
		{Path: "a.go", Code: "M", Added: 1}, {Path: "new.go", OrigPath: "old.go", Code: "R"}, {Path: "z.go", Code: "?"}}}
	cv.receive(changesMsg{projectID: "r1", branch: "feat", data: d})

	cv.key(m, a2Key(" ")) // a.go, moves to new.go
	cv.key(m, a2Key(" ")) // new.go, moves to z.go
	cv.key(m, a2Key(" ")) // z.go, stays on the last file
	cv.key(m, a2Key(" ")) // z.go again: unmarked
	if cv.sel != 2 || !cv.marked["a.go"] || !cv.marked["new.go"] || cv.marked["z.go"] {
		t.Fatalf("marks %v sel %d", cv.marked, cv.sel)
	}
	if got := strings.Join(cv.markedPaths(), ","); got != "a.go,old.go,new.go" {
		t.Fatalf("marked paths %q", got)
	}
	out := a2Plain(cv.render(*m, 80, 20))
	if !strings.Contains(out, "2 marked") || strings.Count(out, "✓") != 2 {
		t.Fatalf("render:\n%s", out)
	}
	// Marked rows fit narrow views (the title and meta lines are clipped by
	// the split around them).
	for _, w := range []int{20, 39, 80} {
		for _, l := range cv.render(*m, w, 10) {
			if strings.Contains(l, "✓") && ansi.StringWidth(l) > w {
				t.Fatalf("width %d: line %q is %d wide", w, ansi.Strip(l), ansi.StringWidth(l))
			}
		}
	}

	// c commits the marked files.
	cv.key(m, a2Key("c"))
	dlg, ok := m.overlay.(*dialog)
	if !ok || !strings.Contains(strings.Join(dlg.text, " "), "the 3 files marked") {
		t.Fatalf("commit dialog: %T %v", m.overlay, dlg)
	}
	a2Type(m, dlg, "msg")
	msgs := submitDialog(t, m)
	if p := lastParams[proto.BranchCommitParams](t, peer, proto.MethodBranchCommit); strings.Join(p.Files, ",") != "a.go,old.go,new.go" {
		t.Fatalf("committed files %v", p.Files)
	}

	// The commit's success clears the marks of the view showing the branch.
	cv2, _ := m.changesFor(localMachine, "r1", "feat")
	cv2.data, cv2.marked = &d, map[string]bool{"a.go": true}
	m.tab().focused().changes = cv2
	m.receiveHarvest(msgs[0].(harvestDoneMsg))
	if len(cv2.marked) != 0 || m.flash != msgs[0].(harvestDoneMsg).text {
		t.Fatalf("after commit: marks %v flash %q", cv2.marked, m.flash)
	}

	// A refresh drops marks of files that are gone.
	cv.marked = map[string]bool{"a.go": true, "gone.go": true}
	cv.receive(changesMsg{projectID: "r1", branch: "feat", poll: true, data: proto.Changes{Worktree: "/src/api-feat", Files: d.Files[:1]}})
	if len(cv.marked) != 1 || !cv.marked["a.go"] {
		t.Fatalf("pruned marks %v", cv.marked)
	}

	// A branch that isn't checked out has nothing to mark.
	loose := &changesView{machine: localMachine, projectID: "r1", branch: "loose"}
	loose.receive(changesMsg{projectID: "r1", branch: "loose", data: proto.Changes{Files: d.Files}})
	loose.key(m, a2Key(" "))
	if len(loose.marked) != 0 {
		t.Fatal("marked a file of a branch that isn't checked out")
	}

	// The other keys open their flows.
	m.overlay = nil
	if cv.key(m, a2Key("M")); m.overlay == nil {
		t.Fatalf("M: %q", m.flash)
	}
	m.overlay = nil
	m.machines[0].projects[0].Branches[1].PR = nil
	if cv.key(m, a2Key("p")); m.overlay == nil {
		t.Fatalf("p: %q", m.flash)
	}
	m.overlay = nil
	if _, cmd := cv.key(m, a2Key("P")); cmd == nil {
		t.Fatal("P")
	}
	if _, cmd := cv.key(m, a2Key("D")); cmd == nil {
		t.Fatal("D")
	}
}
