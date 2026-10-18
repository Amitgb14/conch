package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// apInfo is a project with one branch per name, each with an open pull
// request whose checks are as given ("" for no pull request at all).
func apInfo(id string, checks map[string]string) proto.ProjectInfo {
	info := proto.ProjectInfo{ID: id, Base: "main"}
	n := 0
	for name, c := range checks {
		n++
		b := proto.BranchInfo{Name: name}
		if c != "" {
			b.PR = &proto.PRInfo{Number: n, State: "OPEN", Checks: c}
		}
		info.Branches = append(info.Branches, b)
	}
	return info
}

// Only a turn the autopilot watched counts: the first look at a project
// sends nothing and merges nothing, whatever colour its pull requests are.
func TestAutopilotObserveTurns(t *testing.T) {
	dir := t.TempDir()
	a := loadAutopilot(dir)
	a.set(proto.AutopilotPolicy{AutoFix: 2, AutoMerge: true})

	if red := a.observe(apInfo("p", map[string]string{"red": "fail", "green": "pass", "main": "pending"})); len(red) != 0 {
		t.Fatalf("first sight sent %v", red)
	}
	if len(a.saved.Waiting) != 0 {
		t.Fatalf("first sight waits to merge %v", a.saved.Waiting)
	}
	// Still red is no turn; pending to red is, and so is a new pull
	// request that arrives red. To green waits to merge — but the base
	// never merges into itself.
	red := a.observe(apInfo("p", map[string]string{"red": "fail", "green": "pass", "main": "pass", "new": "fail", "slow": "pending"}))
	if strings.Join(red, ",") != "new" {
		t.Fatalf("red turns: %v", red)
	}
	a.observe(apInfo("p", map[string]string{"red": "pass", "green": "pass", "main": "pass", "new": "fail", "slow": "pass"}))
	if _, ok := a.saved.Waiting[branchKey("p", "main")]; ok {
		t.Fatal("the base waits to merge into itself")
	}
	for _, b := range []string{"red", "slow"} {
		if _, ok := a.saved.Waiting[branchKey("p", b)]; !ok {
			t.Fatalf("%s turned green and does not wait: %v", b, a.saved.Waiting)
		}
	}
	if _, ok := a.saved.Waiting[branchKey("p", "green")]; ok {
		t.Fatal("green all along waits to merge")
	}

	// Red again, or gone: nothing to wait for any more, and a branch gone
	// is forgotten.
	a.observe(apInfo("p", map[string]string{"red": "fail", "main": "pass", "new": "fail", "slow": "pass"}))
	if _, ok := a.saved.Waiting[branchKey("p", "red")]; ok {
		t.Fatal("red again and still waiting")
	}
	if _, ok := a.saved.Checks[branchKey("p", "green")]; ok {
		t.Fatal("a branch gone is remembered")
	}

	// It survives a reload: the same file, read by a new server.
	b := loadAutopilot(dir)
	if b.policy() != (proto.AutopilotPolicy{AutoFix: 2, AutoMerge: true}) || len(b.saved.Waiting) != 1 || b.saved.Checks[branchKey("p", "new")] != "fail" {
		t.Fatalf("after a reload: %+v", b.saved)
	}
	// A run that went red while the server was down is a turn the new one
	// still sees.
	if red := b.observe(apInfo("p", map[string]string{"main": "pass", "new": "fail", "slow": "fail"})); strings.Join(red, ",") != "slow" {
		t.Fatalf("red during a reload: %v", red)
	}
}

// With auto_merge off nothing waits, and turning it off forgets what
// did, so turning it on later merges nothing that went green unwatched.
func TestAutopilotMergeOff(t *testing.T) {
	a := loadAutopilot(t.TempDir())
	a.observe(apInfo("p", map[string]string{"x": "pending"}))
	a.observe(apInfo("p", map[string]string{"x": "pass"}))
	if len(a.saved.Waiting) != 0 {
		t.Fatalf("waiting with auto_merge off: %v", a.saved.Waiting)
	}
	a.set(proto.AutopilotPolicy{AutoMerge: true})
	a.observe(apInfo("p", map[string]string{"x": "pending"}))
	a.observe(apInfo("p", map[string]string{"x": "pass"}))
	if len(a.saved.Waiting) != 1 {
		t.Fatalf("not waiting: %v", a.saved.Waiting)
	}
	a.set(proto.AutopilotPolicy{})
	a.set(proto.AutopilotPolicy{AutoMerge: true})
	if len(a.saved.Waiting) != 0 {
		t.Fatalf("still waiting after auto_merge went off: %v", a.saved.Waiting)
	}
}

// A file that is not JSON, or from a newer build with odd numbers in it,
// loads as something usable rather than stopping the server.
func TestAutopilotLoadBadFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "autopilot.json"), []byte(`{"policy": {"auto_fix": 3`), 0o600)
	a := loadAutopilot(dir)
	if a.policy() != (proto.AutopilotPolicy{}) {
		t.Fatalf("truncated file: %+v", a.policy())
	}
	a.observe(apInfo("p", map[string]string{"x": "fail"})) // no nil map
	os.WriteFile(filepath.Join(dir, "autopilot.json"), []byte(`{"policy": {"auto_fix": 99, "auto_merge": true}, "fixes": {"p\u0000x": 1}, "later": 1}`), 0o600)
	if a := loadAutopilot(dir); a.policy().AutoFix != autoFixMax || !a.policy().AutoMerge || a.saved.Fixes["p\x00x"] != 1 {
		t.Fatalf("newer file: %+v", a.saved)
	}
	a.set(proto.AutopilotPolicy{AutoFix: -4})
	if a.policy().AutoFix != 0 {
		t.Fatalf("negative auto_fix: %+v", a.policy())
	}
}

// The allowance runs out, says so once, is given back for a message that
// never arrived, and a pass of the project's check renews it.
func TestAutopilotFixAllowance(t *testing.T) {
	a := loadAutopilot(t.TempDir())
	if ok, capped, _ := a.takeFix("k"); ok || capped {
		t.Fatal("auto_fix 0 sent one")
	}
	a.set(proto.AutopilotPolicy{AutoFix: 2})
	for i := range 2 {
		if ok, _, _ := a.takeFix("k"); !ok {
			t.Fatalf("try %d refused", i+1)
		}
	}
	if ok, capped, limit := a.takeFix("k"); ok || !capped || limit != 2 {
		t.Fatalf("past the cap: %v %v %d", ok, capped, limit)
	}
	a.giveBack("k")
	if ok, _, _ := a.takeFix("k"); !ok {
		t.Fatal("given back and still refused")
	}
	a.checked("k", "abc", false)
	if a.saved.Failed["k"] != "abc" || a.saved.Fixes["k"] != 2 {
		t.Fatalf("a failure: %+v", a.saved)
	}
	a.checked("k", "abd", true)
	if _, ok := a.saved.Failed["k"]; ok || a.saved.Fixes["k"] != 0 {
		t.Fatalf("a pass: %+v", a.saved)
	}
	a.giveBack("nothing") // nothing to give back is no harm
}

// fakeMergeGH is a gh that merges any pull request but #9, and logs
// every call.
func fakeMergeGH(t *testing.T) (calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
echo "$*" >> `+calls+`
case "$1 $2" in
"repo view") echo '{"mergeCommitAllowed":true}' ;;
"pr merge") if [ "$3" = 9 ]; then echo 'X not mergeable' >&2; exit 1; fi ;;
"pr list") echo '[]' ;;
"run list") echo '[{"databaseId": 4, "workflowName": "ci", "url": "https://x/runs/4", "headSha": "abc", "conclusion": "failure"}]' ;;
"run view") echo 'TestX failed: AUTOPILOT-CI-MARK' ;;
"api graphql") echo '{}' ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	return calls
}

func apFixes(s *Server, key string) int {
	s.autopilot.mu.Lock()
	defer s.autopilot.mu.Unlock()
	return s.autopilot.saved.Fixes[key]
}

func readCalls(path string) string { b, _ := os.ReadFile(path); return string(b) }

// apPR puts an open pull request on branch, as GitHub was last read, and
// hands the autopilot the read.
func apPR(s *Server, p *project, branch string, pr proto.PRInfo) {
	pr.State = "OPEN"
	p.mu.Lock()
	if p.prs == nil {
		p.prs = map[string]proto.PRInfo{}
	}
	p.prs[branch] = pr
	info := p.info
	info.Branches = append([]proto.BranchInfo(nil), info.Branches...)
	found := false
	for i := range info.Branches {
		if info.Branches[i].Name == branch {
			found = true
		}
	}
	if !found {
		info.Branches = append(info.Branches, proto.BranchInfo{Name: branch})
	}
	p.applyPRs(&info)
	p.info = info
	p.mu.Unlock()
	s.autopilotPRs(info)
}

// A pull request that turns green merges on GitHub, by its number and at
// the commit its checks passed for, with nobody's TUI involved.
func TestAutopilotMergesOnTheTurnToGreen(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	calls := fakeMergeGH(t)
	s.autopilot.set(proto.AutopilotPolicy{AutoMerge: true})

	apPR(s, p, "feat", proto.PRInfo{Number: 5, Checks: "pending", Head: "abc"})
	apPR(s, p, "feat", proto.PRInfo{Number: 5, Checks: "pending", Head: "abc"}) // seen once now
	if strings.Contains(readCalls(calls), "pr merge") {
		t.Fatal("merged while pending")
	}
	apPR(s, p, "feat", proto.PRInfo{Number: 5, Checks: "pass", Head: "abc"})
	a5WaitFor(t, "the merge", func() bool { return strings.Contains(readCalls(calls), "pr merge 5 --merge --match-head-commit abc") })
	a5WaitFor(t, "nothing left waiting", func() bool { return len(s.autopilotState().Branches) == 0 })
	if n := strings.Count(readCalls(calls), "pr merge"); n != 1 {
		t.Fatalf("merged %d times", n)
	}
}

// What holds a green pull request back is said, and once it is gone the
// pull request merges without turning green again.
func TestAutopilotHoldsThenMerges(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	calls := fakeMergeGH(t)
	s.autopilot.set(proto.AutopilotPolicy{AutoMerge: true})
	apPR(s, p, "feat", proto.PRInfo{Number: 5, Checks: "pending", Draft: true})
	apPR(s, p, "feat", proto.PRInfo{Number: 5, Checks: "pass", Draft: true})
	st := s.autopilotState()
	if len(st.Branches) != 1 || !st.Branches[0].Waiting || st.Branches[0].Held != "its pull request is a draft" {
		t.Fatalf("held: %+v", st)
	}
	// Out of draft, as the next read of GitHub says; the tick merges it.
	p.mu.Lock()
	pr := p.prs["feat"]
	pr.Draft = false
	p.prs["feat"] = pr
	p.mu.Unlock()
	s.autopilotMerges()
	a5WaitFor(t, "the merge after the hold", func() bool { return strings.Contains(readCalls(calls), "pr merge 5") })
}

// GitHub refusing is said once and not tried again until it is green
// again.
func TestAutopilotMergeRefused(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	calls := fakeMergeGH(t)
	s.autopilot.set(proto.AutopilotPolicy{AutoMerge: true})
	apPR(s, p, "clash", proto.PRInfo{Number: 9, Checks: "pending"})
	apPR(s, p, "clash", proto.PRInfo{Number: 9, Checks: "pass"})
	a5WaitFor(t, "the merge refused", func() bool {
		return strings.Contains(readCalls(calls), "pr merge 9") && len(s.autopilotState().Branches) == 0
	})
	s.autopilotMerges()
	apPR(s, p, "clash", proto.PRInfo{Number: 9, Checks: "pass"})
	time.Sleep(50 * time.Millisecond)
	if n := strings.Count(readCalls(calls), "pr merge"); n != 1 {
		t.Fatalf("tried %d times", n)
	}
}

func TestAutopilotMergeHold(t *testing.T) {
	s, p := afterFixture(t)
	info := p.snapshot()
	for _, c := range []struct {
		pr          proto.PRInfo
		checkFailed bool
		want        string
	}{
		{proto.PRInfo{Number: 1}, false, ""},
		{proto.PRInfo{Number: 1, Draft: true}, false, "draft"},
		{proto.PRInfo{Number: 2, Review: "CHANGES_REQUESTED"}, false, "asked for changes on #2"},
		{proto.PRInfo{Number: 3, Conflicting: true}, false, "#3 conflicts with main"},
		{proto.PRInfo{Number: 4}, true, "own check failed"},
	} {
		got := s.mergeHold(info, "feat", c.pr, c.checkFailed)
		if c.want == "" && got != "" || !strings.Contains(got, c.want) {
			t.Errorf("%+v: %q, want %q", c.pr, got, c.want)
		}
	}
	noBase := info
	noBase.Base = ""
	if got := s.mergeHold(noBase, "feat", proto.PRInfo{Number: 3, Conflicting: true}, false); !strings.Contains(got, "its base") {
		t.Errorf("no base: %q", got)
	}

	// An agent still at it on the branch may be mid-push.
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	e := branchPane(t, s, p, "api/model")
	hook(s, e, "UserPromptSubmit")
	if got := s.mergeHold(info, "api/model", proto.PRInfo{Number: 5}, false); !strings.Contains(got, "still at it") {
		t.Fatalf("working agent: %q", got)
	}
	hook(s, e, "Stop")
	if got := s.mergeHold(info, "api/model", proto.PRInfo{Number: 5}, false); got != "" {
		t.Fatalf("done agent: %q", got)
	}
}

// CI that turns red goes to the branch's agent — a free one only — and
// within the allowance.
func TestAutopilotSendsRedCIToTheAgent(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	fakeMergeGH(t)
	s.autopilot.set(proto.AutopilotPolicy{AutoFix: 1})
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	e := branchPane(t, s, p, "api/model")
	key := branchKey(p.id, "api/model")

	// Working: nothing is typed, and nothing is counted.
	hook(s, e, "UserPromptSubmit")
	apPR(s, p, "api/model", proto.PRInfo{Number: 5, Checks: "pending", Head: "abc"})
	apPR(s, p, "api/model", proto.PRInfo{Number: 5, Checks: "fail", Head: "abc"})
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(screenText(e), "AUTOPILOT-CI-MARK") || apFixes(s, key) != 0 {
		t.Fatal("sent to a working agent")
	}

	hook(s, e, "Stop")
	apPR(s, p, "api/model", proto.PRInfo{Number: 5, Checks: "pending", Head: "abc"})
	apPR(s, p, "api/model", proto.PRInfo{Number: 5, Checks: "fail", Head: "abc"})
	a5WaitFor(t, "the CI log on the agent's screen", func() bool { return strings.Contains(screenText(e), "AUTOPILOT-CI-MARK") })

	// The allowance is spent: red again is the person's.
	hook(s, e, "Stop")
	apPR(s, p, "api/model", proto.PRInfo{Number: 5, Checks: "pending", Head: "abc"})
	apPR(s, p, "api/model", proto.PRInfo{Number: 5, Checks: "fail", Head: "abc"})
	time.Sleep(50 * time.Millisecond)
	if n := strings.Count(screenText(e), "CI (ci) failed"); n != 1 {
		t.Fatalf("sent %d times past the cap", n)
	}
	// Green renews it.
	apPR(s, p, "api/model", proto.PRInfo{Number: 5, Checks: "pass", Head: "abc"})
	if n := apFixes(s, key); n != 0 {
		t.Fatalf("green kept the count: %d", n)
	}
}

// The project's own check: a failure holds the merge at that commit, goes
// to the agent, and a pass lifts the hold.
func TestAutopilotCheck(t *testing.T) {
	s, p := afterFixture(t)
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	e := branchPane(t, s, p, "api/model")
	hook(s, e, "Stop")
	s.autopilot.set(proto.AutopilotPolicy{AutoFix: 2})
	if _, perr := s.autopilotCheck(proto.AutopilotCheck{ProjectID: "nope", Branch: "x", Exit: 1}); perr == nil {
		t.Fatal("no such project")
	}
	if _, perr := s.autopilotCheck(proto.AutopilotCheck{ProjectID: p.id, Exit: 1}); perr == nil {
		t.Fatal("no branch")
	}

	st, perr := s.autopilotCheck(proto.AutopilotCheck{ProjectID: p.id, Branch: "api/model", Command: "make check", Exit: 2})
	if perr != nil || len(st.Branches) != 1 || !st.Branches[0].CheckFailed {
		t.Fatalf("failed: %+v %v", st, perr)
	}
	a5WaitFor(t, "the check's failure on the agent's screen", func() bool { return strings.Contains(screenText(e), "make check") })

	// A terminal that is gone has nothing to send, and the try is given back.
	hook(s, e, "Stop")
	key := branchKey(p.id, "api/model")
	s.autopilotCheck(proto.AutopilotCheck{ProjectID: p.id, Branch: "api/model", Pane: "p999", Exit: 2})
	a5WaitFor(t, "the try given back", func() bool {
		return apFixes(s, key) == 1
	})

	st, _ = s.autopilotCheck(proto.AutopilotCheck{ProjectID: p.id, Branch: "api/model", Exit: 0})
	if len(st.Branches) != 0 {
		t.Fatalf("a pass left %+v", st)
	}
}

// A project removed takes what the autopilot knew of it along.
func TestAutopilotForgetsARemovedProject(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	s.autopilot.set(proto.AutopilotPolicy{AutoMerge: true})
	apPR(s, p, "feat", proto.PRInfo{Number: 5, Checks: "pending", Draft: true})
	apPR(s, p, "feat", proto.PRInfo{Number: 5, Checks: "pass", Draft: true})
	if len(s.autopilotState().Branches) != 1 {
		t.Fatal("nothing waiting")
	}
	if perr := s.projects.remove(p.id); perr != nil {
		t.Fatal(perr)
	}
	if st := s.autopilotState(); len(st.Branches) != 0 || s.autopilot.saved.Seen[p.id] {
		t.Fatalf("after removing: %+v", s.autopilot.saved)
	}
}
