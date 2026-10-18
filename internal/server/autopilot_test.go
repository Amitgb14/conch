package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/gitx"
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
	if !reflect.DeepEqual(b.policy(), proto.AutopilotPolicy{AutoFix: 2, AutoMerge: true}) || len(b.saved.Waiting) != 1 || b.saved.Checks[branchKey("p", "new")] != "fail" {
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
	if !reflect.DeepEqual(a.policy(), proto.AutopilotPolicy{}) {
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

// shipFixture is afterFixture with a bare origin, auto_pr on, and an agent
// that has finished on api/model with a commit of its own. The gh it puts
// on CONCH_GH logs what it was asked to create to the returned file.
func shipFixture(t *testing.T) (s *Server, p *project, e *entry, wt, origin, ghLog string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	s, p = afterFixture(t)
	quietPRs(t, p)
	dir := t.TempDir()
	origin = filepath.Join(dir, "origin.git")
	apGit(t, p.root, "init", "-q", "--bare", origin)
	apGit(t, p.root, "remote", "add", "origin", origin)
	ghLog = filepath.Join(dir, "gh.log")
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
case "$1 $2" in
"pr create") echo "$*" >> `+ghLog+`; echo https://github.com/o/r/pull/8 ;;
"pr ready") echo "$*" >> `+ghLog+` ;;
"pr list") echo '[]' ;;
"api graphql") echo '{}' ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	if _, perr := s.createTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"}); perr != nil {
		t.Fatal(perr)
	}
	e = branchPane(t, s, p, "api/model")
	w, _ := p.branchWorktree("api/model")
	wt = w.Path
	os.WriteFile(filepath.Join(wt, "model.go"), []byte("package api\n"), 0o644)
	apGit(t, wt, "add", ".")
	apGit(t, wt, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "Add the model")
	hook(s, e, "Stop")
	s.autopilot.set(proto.AutopilotPolicy{AutoPR: true})
	return s, p, e, wt, origin, ghLog
}

func apGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A finished branch with commits of its own is pushed and given a pull
// request, titled from its commits; once is enough.
func TestAutopilotShipOpensAPullRequest(t *testing.T) {
	s, p, _, wt, origin, ghLog := shipFixture(t)
	if why := s.ship(p.id, "api/model"); why != "" {
		t.Fatalf("held: %s", why)
	}
	if got := readCalls(ghLog); got != "pr create --base main --head api/model --fill\n" {
		t.Fatalf("gh: %q", got)
	}
	if apGit(t, origin, "rev-parse", "api/model") != apGit(t, wt, "rev-parse", "HEAD") {
		t.Fatal("not pushed")
	}
	// The base is never shipped, and neither is a branch with nothing of
	// its own.
	if why := s.ship(p.id, "main"); why != "" || strings.Count(readCalls(ghLog), "pr create") != 1 {
		t.Fatalf("the base: %q", why)
	}
	// Off: nothing at all.
	s.autopilot.set(proto.AutopilotPolicy{})
	s.autopilotShip(p.id, "api/model")
	if n := strings.Count(readCalls(ghLog), "pr create"); n != 1 {
		t.Fatalf("auto_pr off opened %d", n)
	}
}

// New commits on a branch whose pull request is open are pushed to it;
// one closed by somebody is not opened again.
func TestAutopilotShipPushesToTheOpenOne(t *testing.T) {
	s, p, _, wt, origin, ghLog := shipFixture(t)
	p.mu.Lock()
	p.prs = map[string]proto.PRInfo{"api/model": {Number: 8, State: "OPEN"}}
	p.mu.Unlock()
	if why := s.ship(p.id, "api/model"); why != "" {
		t.Fatalf("held: %s", why)
	}
	if apGit(t, origin, "rev-parse", "api/model") != apGit(t, wt, "rev-parse", "HEAD") || readCalls(ghLog) != "" {
		t.Fatalf("not pushed to #8, or another opened: %q", readCalls(ghLog))
	}
	if why := s.ship(p.id, "api/model"); why != "" { // up to date: nothing to do
		t.Fatalf("up to date: %s", why)
	}

	p.mu.Lock()
	p.prs = map[string]proto.PRInfo{"api/model": {Number: 8, State: "CLOSED"}}
	p.mu.Unlock()
	if why := s.ship(p.id, "api/model"); !strings.Contains(why, "#8 was closed") || readCalls(ghLog) != "" {
		t.Fatalf("closed: %q", why)
	}
	// Pull requests that can't be read hold it, saying why.
	p.mu.Lock()
	p.prs, p.prStatus = nil, "install gh (GitHub CLI) to see pull requests"
	p.mu.Unlock()
	if why := s.ship(p.id, "api/model"); !strings.Contains(why, "install gh") {
		t.Fatalf("no gh: %q", why)
	}
}

// Work left uncommitted goes to the agent to commit, once for each
// commit it is at; nothing is pushed meanwhile.
func TestAutopilotShipAsksToCommit(t *testing.T) {
	s, p, e, wt, _, ghLog := shipFixture(t)
	os.WriteFile(filepath.Join(wt, "loose.go"), []byte("package api\n"), 0o644)
	if why := s.ship(p.id, "api/model"); !strings.Contains(why, "asked") {
		t.Fatalf("first: %q", why)
	}
	a5WaitFor(t, "the request on the agent's screen", func() bool { return strings.Contains(screenText(e), "uncommitted changes") })
	hook(s, e, "Stop")
	if why := s.ship(p.id, "api/model"); !strings.Contains(why, "commit them and it goes out") {
		t.Fatalf("second: %q", why)
	}
	if n := strings.Count(screenText(e), "You finished with uncommitted"); n != 1 {
		t.Fatalf("asked %d times", n)
	}
	if readCalls(ghLog) != "" {
		t.Fatal("opened a pull request over uncommitted work")
	}
	// With no agent there, it says so.
	s.close(e.info().ID)
	a5WaitFor(t, "the agent gone", func() bool { return s.branchAgent(p.id, "api/model") == nil })
	if why := s.ship(p.id, "api/model"); !strings.Contains(why, "no agent") {
		t.Fatalf("no agent: %q", why)
	}
}

// A project with a check ships only a commit the check passed for.
func TestAutopilotShipWaitsForTheCheck(t *testing.T) {
	s, p, _, wt, _, ghLog := shipFixture(t)
	s.autopilot.set(proto.AutopilotPolicy{AutoPR: true, Checks: map[string]string{p.id: "true"}})
	if why := s.ship(p.id, "api/model"); why != "" || readCalls(ghLog) != "" {
		t.Fatalf("unchecked: %q %q", why, readCalls(ghLog))
	}
	key := branchKey(p.id, "api/model")
	s.autopilot.checked(key, "an older commit", true)
	if s.ship(p.id, "api/model"); readCalls(ghLog) != "" {
		t.Fatal("shipped on a pass of another commit")
	}
	s.autopilot.checked(key, apGit(t, wt, "rev-parse", "HEAD"), true)
	if why := s.ship(p.id, "api/model"); why != "" || !strings.Contains(readCalls(ghLog), "pr create") {
		t.Fatalf("checked: %q %q", why, readCalls(ghLog))
	}
}

// The whole round with nobody watching: the agent finishes, the server
// runs the check in a terminal of its own, it fails and goes back to the
// agent with its output; the next finish passes and the branch ships.
func TestAutopilotFinishRunsTheCheckThenShips(t *testing.T) {
	s, p, e, wt, _, ghLog := shipFixture(t)
	flag := filepath.Join(t.TempDir(), "pass")
	s.autopilot.set(proto.AutopilotPolicy{AutoPR: true, AutoFix: 1,
		Checks: map[string]string{p.id: "if [ -f " + flag + " ]; then echo CHECK-OK; else echo CHECK-BROKE-MARK; exit 3; fi"}})
	_ = wt

	hook(s, e, "UserPromptSubmit")
	hook(s, e, "Stop")
	a5WaitFor(t, "the failure on the agent's screen", func() bool { return strings.Contains(screenText(e), "CHECK-BROKE-MARK") })
	if !strings.Contains(screenText(e), "exit 3") || readCalls(ghLog) != "" {
		t.Fatalf("screen:\n%s\ngh: %q", screenText(e), readCalls(ghLog))
	}
	var checks int
	for _, info := range s.list() {
		if strings.HasPrefix(info.Name, checkPrefix) {
			checks++
		}
	}
	if checks != 1 {
		t.Fatalf("%d check terminals", checks)
	}

	// The message is in once it is submitted, which leaves the agent idle.
	a5WaitFor(t, "the failure submitted", func() bool { st, _ := agentState(e); return st == proto.AgentIdle })
	os.WriteFile(flag, nil, 0o644)
	hook(s, e, "UserPromptSubmit")
	hook(s, e, "Stop")
	a5WaitFor(t, "the pull request", func() bool { return strings.Contains(readCalls(ghLog), "pr create") })
	// The first check's terminal went when the second started.
	checks = 0
	for _, info := range s.list() {
		if strings.HasPrefix(info.Name, checkPrefix) {
			checks++
		}
	}
	if checks != 1 {
		t.Fatalf("%d check terminals after the second", checks)
	}
	// A reviewer finishing is not the branch's agent finishing.
	s.autopilotFinished(proto.PaneInfo{Name: "review · api/model", ProjectID: p.id, Branch: "api/model"})
	if st := s.autopilotState(); len(st.Branches) > 0 && st.Branches[0].Checking != "" {
		t.Fatal("a reviewer started a check")
	}
	// A client reporting the server's own check is not counted twice.
	if !s.autopilot.ownCheck("") && s.autopilot.ownCheck("p999") {
		t.Fatal("a pane it never started is its own")
	}
}

// resolveFixture is shipFixture with the branch pushed, its pull request
// #8 open, and main moved on the remote by somebody else: in file, to
// content, which conflicts when file is model.go.
func resolveFixture(t *testing.T, file, content string) (s *Server, p *project, e *entry, wt, origin string) {
	t.Helper()
	s, p, e, wt, origin, _ = shipFixture(t)
	apGit(t, p.root, "push", "-q", "origin", "main")
	apGit(t, wt, "push", "-q", "-u", "origin", "api/model")
	other := filepath.Join(t.TempDir(), "other")
	apGit(t, filepath.Dir(other), "clone", "-q", "-b", "main", origin, other)
	os.WriteFile(filepath.Join(other, file), []byte(content), 0o644)
	apGit(t, other, "add", ".")
	apGit(t, other, "-c", "user.name=o", "-c", "user.email=o@example.com", "commit", "-q", "-m", "main changes "+file)
	apGit(t, other, "push", "-q", "origin", "main")
	s.autopilot.set(proto.AutopilotPolicy{AutoResolve: 1})
	apPR(s, p, "api/model", proto.PRInfo{Number: 8, Checks: "pass"})
	apPR(s, p, "api/model", proto.PRInfo{Number: 8, Checks: "pass", Conflicting: true})
	return s, p, e, wt, origin
}

func apConflicted(s *Server, key string) (string, bool) {
	s.autopilot.mu.Lock()
	defer s.autopilot.mu.Unlock()
	at, ok := s.autopilot.saved.Conflicted[key]
	return at, ok
}

// Most conflicts are a base that moved: the server rebases the branch
// itself, and pushes it over the old one.
func TestAutopilotResolveRebasesCleanly(t *testing.T) {
	s, p, e, wt, origin := resolveFixture(t, "other.txt", "theirs\n")
	key := branchKey(p.id, "api/model")
	if at, ok := apConflicted(s, key); !ok || at != "" {
		t.Fatalf("not minded: %q %v", at, ok)
	}
	s.autopilotResolve(key)
	if !gitx.IsAncestor(context.Background(), wt, "origin/main", "HEAD") {
		t.Fatal("not rebased")
	}
	a5WaitFor(t, "the rebased branch pushed", func() bool {
		return apGit(t, origin, "rev-parse", "api/model") == apGit(t, wt, "rev-parse", "HEAD")
	})
	if _, err := os.Stat(filepath.Join(wt, "other.txt")); err != nil {
		t.Fatal("the base's change is missing")
	}
	if strings.Contains(screenText(e), "conflicts with") {
		t.Fatal("asked the agent about a conflict it did not have")
	}
	// Again at the same commit: nothing.
	s.autopilotResolve(key)
}

// A real conflict goes to the agent, told what the base did; its
// resolution, once it finishes, is pushed over the old branch.
func TestAutopilotResolveAsksTheAgent(t *testing.T) {
	s, p, e, wt, origin := resolveFixture(t, "model.go", "package api // theirs\n")
	key := branchKey(p.id, "api/model")
	pushed := apGit(t, origin, "rev-parse", "api/model")
	s.autopilotResolve(key)
	a5WaitFor(t, "the conflict on the agent's screen", func() bool {
		text := screenText(e)
		return strings.Contains(text, "conflicts with origin/main") && strings.Contains(text, "main changes model.go")
	})
	if gitx.InProgress(context.Background(), wt) != "" {
		t.Fatal("the server's rebase was left half done")
	}
	if apGit(t, origin, "rev-parse", "api/model") != pushed {
		t.Fatal("pushed before the agent resolved it")
	}
	a5WaitFor(t, "the request submitted", func() bool { st, _ := agentState(e); return st == proto.AgentIdle })

	// The agent resolves it, keeping both sides, and finishes.
	cmd := exec.Command("git", "-C", wt, "rebase", "origin/main")
	cmd.Run() // stops on the conflict
	os.WriteFile(filepath.Join(wt, "model.go"), []byte("package api // theirs, and ours\n"), 0o644)
	apGit(t, wt, "add", "model.go")
	cont := exec.Command("git", "-C", wt, "-c", "core.editor=true", "rebase", "--continue")
	if out, err := cont.CombinedOutput(); err != nil {
		t.Fatalf("continue: %v\n%s", err, out)
	}
	hook(s, e, "UserPromptSubmit")
	hook(s, e, "Stop")
	a5WaitFor(t, "the resolution pushed", func() bool {
		return apGit(t, origin, "rev-parse", "api/model") == apGit(t, wt, "rev-parse", "HEAD")
	})

	// Conflicting again at the new commit: the allowance is spent.
	apPR(s, p, "api/model", proto.PRInfo{Number: 8, Checks: "pass"})
	apPR(s, p, "api/model", proto.PRInfo{Number: 8, Checks: "pass", Conflicting: true})
	s.autopilot.mu.Lock()
	s.autopilot.saved.Resolves[key] = 1
	s.autopilot.mu.Unlock()
	other := filepath.Join(t.TempDir(), "other")
	apGit(t, filepath.Dir(other), "clone", "-q", "-b", "main", origin, other)
	os.WriteFile(filepath.Join(other, "model.go"), []byte("package api // theirs again\n"), 0o644)
	apGit(t, other, "-c", "user.name=o", "-c", "user.email=o@example.com", "commit", "-qam", "main again")
	apGit(t, other, "push", "-q", "origin", "main")
	hook(s, e, "Stop")
	before := strings.Count(screenText(e), "conflicts with origin/main")
	s.autopilotResolve(key)
	if n := strings.Count(screenText(e), "conflicts with origin/main"); n != before {
		t.Fatal("asked past the allowance")
	}
}

// A resolution that deletes what the branch never deleted, or a push to
// the branch by somebody else meanwhile, keeps the old branch.
func TestAutopilotRewriteGuards(t *testing.T) {
	s, p, _, wt, origin := resolveFixture(t, "other.txt", "theirs\n")
	ctx := context.Background()
	base := gitx.FreshBase(ctx, wt, "api/model", "main")
	lease := gitx.PushedHead(ctx, wt, "api/model")
	if _, err := gitx.RebaseOnto(ctx, wt, "api/model", base); err != nil {
		t.Fatal(err)
	}
	pushed := apGit(t, origin, "rev-parse", "api/model")

	// Not on top of the base it was rebased for.
	if why := s.shipRewrite(p, "api/model", wt, rewrite{Base: "origin/nothing", Lease: lease}, 8); why == "" {
		t.Fatal("pushed without its base")
	}
	// The resolution dropped a file the base has.
	apGit(t, wt, "rm", "-q", "other.txt")
	apGit(t, wt, "commit", "-qm", "drop theirs")
	if why := s.shipRewrite(p, "api/model", wt, rewrite{Base: base, Lease: lease}, 8); !strings.Contains(why, "deleted other.txt") {
		t.Fatalf("deleted: %q", why)
	}
	apGit(t, wt, "reset", "-q", "--hard", "HEAD~1")
	// Somebody pushed to the branch meanwhile.
	if why := s.shipRewrite(p, "api/model", wt, rewrite{Base: base, Lease: "0000000000000000000000000000000000000001"}, 8); !strings.Contains(why, "nothing was overwritten") {
		t.Fatalf("stale lease: %q", why)
	}
	if apGit(t, origin, "rev-parse", "api/model") != pushed {
		t.Fatal("a refused rewrite was pushed")
	}
	if why := s.shipRewrite(p, "api/model", wt, rewrite{Base: base, Lease: lease}, 8); why != "" {
		t.Fatalf("a good one: %q", why)
	}
	if apGit(t, origin, "rev-parse", "api/model") != apGit(t, wt, "rev-parse", "HEAD") {
		t.Fatal("not pushed")
	}
}

// A pull request already conflicting when the server first sees it is
// left to the person, and auto_resolve 0 leaves every one.
func TestAutopilotResolveOnlyWhatItWatched(t *testing.T) {
	a := loadAutopilot(t.TempDir())
	a.set(proto.AutopilotPolicy{AutoResolve: 2})
	info := apInfo("p", map[string]string{"x": "pass"})
	info.Branches[0].PR.Conflicting = true
	a.observe(info)
	if at := a.saved.Conflicted[branchKey("p", "x")]; at != conflictUnwatched {
		t.Fatalf("first sight: %q", at)
	}
	a.observe(info)
	if at := a.saved.Conflicted[branchKey("p", "x")]; at != conflictUnwatched {
		t.Fatalf("still conflicting: %q", at)
	}
	info.Branches[0].PR.Conflicting = false
	a.observe(info)
	if _, ok := a.saved.Conflicted[branchKey("p", "x")]; ok {
		t.Fatal("resolved and still minded")
	}
	info.Branches[0].PR.Conflicting = true
	a.observe(info)
	if at, ok := a.saved.Conflicted[branchKey("p", "x")]; !ok || at != "" {
		t.Fatalf("a turn to conflicting: %q %v", at, ok)
	}
	if !strings.Contains(resolvePrompt("origin/main", []string{"a.go"}, "abc main did it", "go test ./..."), "run `go test ./...`") {
		t.Fatal("the prompt does not ask for the check")
	}
}

func TestIsTestFile(t *testing.T) {
	for path, want := range map[string]bool{
		"internal/x/a_test.go": true, "a.go": false, "test_a.py": true, "a_test.py": true, "a.py": false,
		"src/a.test.ts": true, "src/a.spec.tsx": true, "src/a.ts": false, "spec/a_spec.rb": true,
		"src/test/java/FooTest.java": true, "Foo.java": false, "tests/helpers.js": true, "__tests__/x.js": true,
		"internal/x/testdata/in.json": true, "README.md": false, "test_a.go": false,
	} {
		if got := isTestFile(path); got != want {
			t.Errorf("%s: %v", path, got)
		}
	}
	for _, c := range []struct {
		files []string
		want  bool
	}{
		{nil, false},
		{[]string{"README.md", "docs/x.mdx", "go.mod"}, false},
		{[]string{"a.go"}, true},
		{[]string{"a.go", "a_test.go"}, false},
		{[]string{"web/App.TSX"}, true},
	} {
		if got := needsTest(c.files); got != c.want {
			t.Errorf("%v: %v", c.files, got)
		}
	}
}

// A branch that changes code and no test opens as a draft, which
// auto_merge leaves alone; its agent is asked for a test, once; the push
// that brings one marks it ready. A draft somebody else made is theirs.
func TestAutopilotRequireTests(t *testing.T) {
	s, p, e, wt, _, ghLog := shipFixture(t)
	s.autopilot.set(proto.AutopilotPolicy{AutoPR: true, RequireTests: true})
	if why := s.ship(p.id, "api/model"); why != "" {
		t.Fatalf("held: %s", why)
	}
	if got := readCalls(ghLog); got != "pr create --base main --head api/model --fill --draft\n" {
		t.Fatalf("gh: %q", got)
	}
	a5WaitFor(t, "the request for a test", func() bool { return strings.Contains(screenText(e), "changes code but no test") })
	a5WaitFor(t, "the request submitted", func() bool { st, _ := agentState(e); return st == proto.AgentIdle })

	// Pushed again with still no test: it stays a draft, and is not asked twice.
	p.mu.Lock()
	p.prs = map[string]proto.PRInfo{"api/model": {Number: 8, State: "OPEN", Draft: true}}
	p.mu.Unlock()
	if why := s.ship(p.id, "api/model"); !strings.Contains(why, "stays a draft") {
		t.Fatalf("no test yet: %q", why)
	}
	if n := strings.Count(screenText(e), "changes code but no test"); n != 1 {
		t.Fatalf("asked %d times", n)
	}

	os.WriteFile(filepath.Join(wt, "model_test.go"), []byte("package api\n"), 0o644)
	apGit(t, wt, "add", ".")
	apGit(t, wt, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "Test the model")
	if why := s.ship(p.id, "api/model"); why != "" {
		t.Fatalf("with a test: %q", why)
	}
	if !strings.Contains(readCalls(ghLog), "pr ready 8") {
		t.Fatalf("not marked ready: %q", readCalls(ghLog))
	}

	// Somebody's own draft is left a draft.
	s.autopilot.mu.Lock()
	delete(s.autopilot.saved.Drafted, branchKey(p.id, "api/model"))
	s.autopilot.mu.Unlock()
	os.Remove(ghLog)
	if why := s.ship(p.id, "api/model"); why != "" || strings.Contains(readCalls(ghLog), "pr ready") {
		t.Fatalf("someone's draft: %q %q", why, readCalls(ghLog))
	}
}

// Past max_diff a pull request opens as a draft too, saying how big it is.
func TestAutopilotMaxDiff(t *testing.T) {
	s, p, _, wt, _, ghLog := shipFixture(t)
	os.WriteFile(filepath.Join(wt, "model.go"), []byte("package api\n\ntype Model struct{}\n"), 0o644)
	apGit(t, wt, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qam", "A model") // 3 lines in all
	s.autopilot.set(proto.AutopilotPolicy{AutoPR: true, MaxDiff: 2})
	s.autopilotShip(p.id, "api/model")
	if !strings.Contains(readCalls(ghLog), "--draft") {
		t.Fatalf("not a draft: %q", readCalls(ghLog))
	}
	if got := s.gates(p.id, "api/model", p.root, "main", ""); len(got) != 0 {
		t.Fatalf("the base itself: %v", got)
	}
	s.autopilot.set(proto.AutopilotPolicy{AutoPR: true, MaxDiff: -3})
	if s.autopilot.policy().MaxDiff != 0 {
		t.Fatal("a negative limit")
	}
}

// A task started while conch ships branches is told how, from the start;
// otherwise its prompt is the person's alone.
func TestAutopilotShipNote(t *testing.T) {
	s, p := afterFixture(t)
	if note := s.shipNote(p.id); note != "" {
		t.Fatalf("auto_pr off: %q", note)
	}
	s.autopilot.set(proto.AutopilotPolicy{AutoPR: true, Checks: map[string]string{p.id: "make check"}})
	note := s.shipNote(p.id)
	for _, want := range []string{"runs `make check`", "do not push", "fails without the change"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note lacks %q: %s", want, note)
		}
	}
	if strings.Contains(s.shipNote("another"), "make check") {
		t.Fatal("another project's check")
	}
	info, _, perr := s.startTask(proto.TaskCreateParams{ProjectID: p.id, Prompt: "add the model", Branch: "api/model"})
	if perr != nil {
		t.Fatal(perr)
	}
	if cmd := strings.Join(info.Command, " "); !strings.Contains(cmd, "How this branch is shipped") || !strings.Contains(cmd, "add the model") {
		t.Fatalf("first message: %s", cmd)
	}
}
