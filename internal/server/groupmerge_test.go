package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// mergeFixture is a server with git projects api, web and specs, a group
// clinic holding them and a task on feat across all three — recorded as
// made, since merging needs only its pull requests, and the branch feat in
// each, which its pull requests are at — and a gh that merges any pull
// request but #9. Reading GitHub is held off for good, so the
// pull requests are what the test says they are.
func mergeFixture(t *testing.T) (s *Server, ps map[string]*project, calls string) {
	t.Helper()
	s, api := afterFixture(t)
	ps = map[string]*project{"api": api}
	dir := filepath.Dir(api.root)
	for _, name := range []string{"web", "specs"} {
		repo := filepath.Join(dir, name)
		a5GitRepo(t, repo)
		p, err := s.projects.add(repo, false)
		if err != nil {
			t.Fatal(err)
		}
		a5WaitFor(t, name+" loaded", func() bool { s.projects.refresh(p, ""); return p.snapshot().Base == "main" })
		ps[name] = p
	}
	for _, p := range ps {
		holdPRs(t, p)
		apBranch(t, p, "feat")
	}
	calls = fakeMergeGH(t)
	s.groups.mu.Lock()
	s.groups.groups = append(s.groups.groups, savedGroup{Name: "clinic", Projects: []string{api.root, ps["web"].root, ps["specs"].root},
		Tasks: []savedTask{{Branch: "feat", Folder: filepath.Join(s.configDir, "tasks", "clinic", "feat"),
			Projects: []string{api.root, ps["web"].root, ps["specs"].root}, Created: time.Now()}}})
	s.groups.mu.Unlock()
	return s, ps, calls
}

// holdPRs keeps the server from reading GitHub again: a read after a
// merge would replace the pull requests the test set with the fake's none.
func holdPRs(t *testing.T, p *project) {
	quietPRs(t, p)
	p.mu.Lock()
	p.prFetching = true
	p.mu.Unlock()
}

// setPR sets branch's pull request; an open one with no head given is at
// the branch's own commit, as an agent's here is.
func setPR(p *project, branch string, pr proto.PRInfo) {
	if pr.State == "" {
		pr.State = "OPEN"
	}
	if pr.Head == "" && pr.State == "OPEN" {
		pr.Head = branchHead(p, branch)
	}
	p.mu.Lock()
	if p.prs == nil {
		p.prs = map[string]proto.PRInfo{}
	}
	p.prs[branch] = pr
	p.mu.Unlock()
}

func taskMerge(t *testing.T, s *Server) proto.GroupTaskMerge {
	t.Helper()
	for _, g := range s.groups.list().Groups {
		for _, tk := range g.Tasks {
			if g.Name == "clinic" && tk.Branch == "feat" && tk.Merge != nil {
				return *tk.Merge
			}
		}
	}
	return proto.GroupTaskMerge{}
}

func merges(calls string) []string {
	var out []string
	for _, l := range strings.Split(readCalls(calls), "\n") {
		if f := strings.Fields(l); len(f) > 2 && f[0] == "pr" && f[1] == "merge" {
			out = append(out, f[2])
		}
	}
	return out
}

// Nothing merges while any one pull request is not green, and why is said;
// once all are, they merge one by one in the order given, and the task
// says it is merged.
func TestGroupMergeWaitsForEveryRepoThenMergesInOrder(t *testing.T) {
	s, ps, calls := mergeFixture(t)
	setPR(ps["api"], "feat", proto.PRInfo{Number: 1, Checks: "pass"})
	a1 := branchHead(ps["api"], "feat")
	setPR(ps["web"], "feat", proto.PRInfo{Number: 2, Checks: "pending"})
	setPR(ps["specs"], "feat", proto.PRInfo{Number: 3, Checks: "pass"})
	info, perr := s.groups.armMerge(proto.GroupTaskMergeParams{Group: "clinic", Branch: "feat",
		Order: []string{ps["specs"].id, ps["api"].id, ps["web"].id}})
	if perr != nil {
		t.Fatal(perr)
	}
	if info.Merge == nil || info.Merge.State != proto.MergeWaiting || strings.Join(info.Merge.Order, ",") != ps["specs"].id+","+ps["api"].id+","+ps["web"].id {
		t.Fatalf("armed: %+v", info.Merge)
	}
	s.groups.merges()
	a5WaitFor(t, "why it waits", func() bool { return taskMerge(t, s).Why == "web #2 is waiting on its checks" })
	if m := merges(calls); len(m) != 0 {
		t.Fatalf("merged %v while web was pending", m)
	}

	setPR(ps["web"], "feat", proto.PRInfo{Number: 2, Checks: "fail"})
	s.groups.merges()
	a5WaitFor(t, "a failure said", func() bool { return taskMerge(t, s).Why == "web #2 has a failing check" })

	setPR(ps["web"], "feat", proto.PRInfo{Number: 2, Checks: "pass"})
	s.groups.merges()
	a5WaitFor(t, "every one merged", func() bool { return taskMerge(t, s).State == proto.MergeMerged })
	if m := strings.Join(merges(calls), ","); m != "3,1,2" {
		t.Fatalf("merged in the order %s, not specs, api, web", m)
	}
	if !strings.Contains(readCalls(calls), "pr merge 1 --merge --match-head-commit "+a1) {
		t.Fatalf("merged without the head its checks passed for: %s", readCalls(calls))
	}
	if m := taskMerge(t, s); m.Why != "" || len(m.Merged) != 3 {
		t.Fatalf("done: %+v", m)
	}
	// Merged is merged: more ticks do nothing.
	s.groups.merges()
	time.Sleep(50 * time.Millisecond)
	if n := len(merges(calls)); n != 3 {
		t.Fatalf("merged %d times", n)
	}
}

// What the autopilot would hold a pull request for holds the whole task;
// a repo merged by hand counts as merged; GitHub refusing one stops it
// there with nothing after it merged, and arming again carries on.
func TestGroupMergeHoldsRefusesAndCarriesOn(t *testing.T) {
	s, ps, calls := mergeFixture(t)
	setPR(ps["api"], "feat", proto.PRInfo{Number: 1, Checks: "pass", Draft: true})
	setPR(ps["web"], "feat", proto.PRInfo{Number: 9, Checks: "pass"}) // the fake refuses #9
	setPR(ps["specs"], "feat", proto.PRInfo{Number: 3, State: "MERGED"})
	if _, perr := s.groups.armMerge(proto.GroupTaskMergeParams{Group: "clinic", Branch: "feat"}); perr != nil {
		t.Fatal(perr)
	}
	a5WaitFor(t, "the draft holds it", func() bool { return taskMerge(t, s).Why == "api: its pull request is a draft" })
	if m := taskMerge(t, s); len(m.Merged) != 1 || m.Merged[0] != ps["specs"].id {
		t.Fatalf("merged by hand not counted: %+v", m)
	}
	setPR(ps["api"], "feat", proto.PRInfo{Number: 1, Checks: "pass"})
	s.groups.merges()
	a5WaitFor(t, "stopped at web", func() bool { return taskMerge(t, s).State == proto.MergeStopped })
	if m := taskMerge(t, s); !strings.Contains(m.Why, "GitHub would not merge web #9") || len(m.Merged) != 2 {
		t.Fatalf("stopped: %+v", m)
	}
	if m := strings.Join(merges(calls), ","); m != "1,9" {
		t.Fatalf("tried %s", m)
	}
	// Stopped is stopped until armed again; armed again it keeps what merged.
	s.groups.merges()
	time.Sleep(50 * time.Millisecond)
	if n := len(merges(calls)); n != 2 {
		t.Fatalf("tried again while stopped: %d", n)
	}
	setPR(ps["web"], "feat", proto.PRInfo{Number: 4, Checks: "pass"})
	if _, perr := s.groups.armMerge(proto.GroupTaskMergeParams{Group: "clinic", Branch: "feat"}); perr != nil {
		t.Fatal(perr)
	}
	a5WaitFor(t, "merged after arming again", func() bool { return taskMerge(t, s).State == proto.MergeMerged })
	if m := strings.Join(merges(calls), ","); m != "1,9,4" {
		t.Fatalf("merged %s", m)
	}
	if _, perr := s.groups.armMerge(proto.GroupTaskMergeParams{Group: "clinic", Branch: "feat"}); perr == nil {
		t.Fatal("armed a task that is all merged")
	}
}

// Merge All holds, as the autopilot does, a pull request whose head is
// not on the branch here — pushed from somewhere else — and says so; once
// the branch has that commit, it merges.
func TestGroupMergeOnlyItsOwnCommits(t *testing.T) {
	s, ps, calls := mergeFixture(t)
	stranger := strings.Repeat("ab", 20) // a commit id these repositories have never had
	setPR(ps["api"], "feat", proto.PRInfo{Number: 1, Checks: "pass", Head: stranger})
	setPR(ps["web"], "feat", proto.PRInfo{Number: 2, Checks: "pass"})
	setPR(ps["specs"], "feat", proto.PRInfo{Number: 3, Checks: "pass", Head: "--all"})
	if _, perr := s.groups.armMerge(proto.GroupTaskMergeParams{Group: "clinic", Branch: "feat",
		Order: []string{ps["api"].id, ps["web"].id, ps["specs"].id}}); perr != nil {
		t.Fatal(perr)
	}
	a5WaitFor(t, "the stranger's commit held", func() bool {
		return taskMerge(t, s).Why == "api: #1 is at abababa, which is not on feat here: conch merges only commits it has"
	})
	// web is green and its own, but nothing merges while any one holds.
	if m := merges(calls); len(m) != 0 {
		t.Fatalf("merged %v", m)
	}
	setPR(ps["api"], "feat", proto.PRInfo{Number: 1, Checks: "pass", Head: ""})
	s.groups.merges()
	a5WaitFor(t, "an unreadable head held", func() bool {
		return taskMerge(t, s).Why == "specs: the commit #3 would merge could not be read"
	})
	if m := merges(calls); len(m) != 0 {
		t.Fatalf("merged %v", m)
	}
	setPR(ps["specs"], "feat", proto.PRInfo{Number: 3, Checks: "pass"})
	s.groups.merges()
	a5WaitFor(t, "every one merged", func() bool { return taskMerge(t, s).State == proto.MergeMerged })
	if m := strings.Join(merges(calls), ","); m != "1,2,3" {
		t.Fatalf("merged %s", m)
	}
}

// Stop disarms; an order must be the task's repos, every one of them; a
// task that is not there is said; and the state survives a restart.
func TestGroupMergeArming(t *testing.T) {
	s, ps, calls := mergeFixture(t)
	setPR(ps["api"], "feat", proto.PRInfo{Number: 1, Checks: "pending"})
	for _, c := range []struct {
		mp   proto.GroupTaskMergeParams
		want string
	}{
		{proto.GroupTaskMergeParams{Group: "nope", Branch: "feat"}, "no group called nope"},
		{proto.GroupTaskMergeParams{Group: "clinic", Branch: "other"}, "has no task on other"},
		{proto.GroupTaskMergeParams{Group: "clinic", Branch: "feat", Order: []string{ps["api"].id}}, "the order leaves out"},
		{proto.GroupTaskMergeParams{Group: "clinic", Branch: "feat", Order: []string{"missing"}}, ""},
	} {
		if _, perr := s.groups.armMerge(c.mp); perr == nil || !strings.Contains(perr.Message, c.want) {
			t.Errorf("%+v: %v", c.mp, perr)
		}
	}
	// A project outside the task.
	other := filepath.Join(filepath.Dir(ps["api"].root), "other")
	a5GitRepo(t, other)
	op, err := s.projects.add(other, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, perr := s.groups.armMerge(proto.GroupTaskMergeParams{Group: "clinic", Branch: "feat",
		Order: []string{ps["api"].id, ps["web"].id, ps["specs"].id, op.id}}); perr == nil || !strings.Contains(perr.Message, "not one of the task's repos") {
		t.Fatalf("an outsider: %v", perr)
	}

	if _, perr := s.groups.armMerge(proto.GroupTaskMergeParams{Group: "clinic", Branch: "feat", RemoveTask: true}); perr != nil {
		t.Fatal(perr)
	}
	a5WaitFor(t, "waiting", func() bool { return taskMerge(t, s).Why == "api #1 is waiting on its checks" })
	setPR(ps["api"], "feat", proto.PRInfo{Number: 1, Checks: "pass"})
	s.groups.merges()
	a5WaitFor(t, "a repo with no pull request", func() bool { return taskMerge(t, s).Why == "web has no open pull request on feat" })
	if _, perr := s.groups.armMerge(proto.GroupTaskMergeParams{Group: "clinic", Branch: "feat", Stop: true}); perr != nil {
		t.Fatal(perr)
	}
	if m := taskMerge(t, s); m.State != proto.MergeStopped || m.Why != "stopped by hand" || !m.RemoveTask {
		t.Fatalf("stopped: %+v", m)
	}
	if n := len(merges(calls)); n != 0 {
		t.Fatalf("merged %d", n)
	}

	// Kept in groups.json, as an older file without it still loads.
	var f groupsFile
	b, _ := os.ReadFile(s.groups.file)
	if err := json.Unmarshal(b, &f); err != nil || len(f.Groups) != 1 || f.Groups[0].Tasks[0].Merge == nil ||
		f.Groups[0].Tasks[0].Merge.State != proto.MergeStopped || len(f.Groups[0].Tasks[0].Merge.Order) != 3 {
		t.Fatalf("saved: %s", b)
	}
	again := loadGroups(s, s.configDir)
	if m := again.groups[0].Tasks[0].Merge; m == nil || m.State != proto.MergeStopped {
		t.Fatalf("loaded: %+v", m)
	}
	old := `{"groups":[{"name":"g","projects":["/x"],"tasks":[{"branch":"b","folder":"/f","projects":["/x"],"created":"2026-11-10T00:00:00Z"}]}]}`
	if err := os.WriteFile(s.groups.file, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if g := loadGroups(s, s.configDir); len(g.groups) != 1 || g.groups[0].Tasks[0].Merge != nil {
		t.Fatalf("an older file: %+v", g.groups)
	}
}

// Asked to, a task all merged takes itself away: each repo's worktree and
// branch, and the task's folder.
func TestGroupMergeThenRemovesTheTask(t *testing.T) {
	s, ps, _ := mergeFixture(t)
	for i, name := range []string{"api", "web", "specs"} {
		p := ps[name]
		wt := filepath.Join(filepath.Dir(p.root), name+"-feat")
		mergeGit(t, p.root, "worktree", "add", "-q", wt, "feat")
		setPR(p, "feat", proto.PRInfo{Number: i + 1, Checks: "pass"})
	}
	folder := filepath.Join(s.configDir, "tasks", "clinic", "feat")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, perr := s.groups.armMerge(proto.GroupTaskMergeParams{Group: "clinic", Branch: "feat", RemoveTask: true}); perr != nil {
		t.Fatal(perr)
	}
	a5WaitFor(t, "the task gone", func() bool {
		gs := s.groups.list().Groups
		return len(gs) == 1 && len(gs[0].Tasks) == 0
	})
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("the folder stayed: %v", err)
	}
	for _, name := range []string{"api", "web", "specs"} {
		if out := mergeGit(t, ps[name].root, "branch", "--list", "feat"); strings.TrimSpace(out) != "" {
			t.Errorf("%s kept feat", name)
		}
	}
}

func mergeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// A repo whose base is not known yet is named in the brief without one,
// rather than as "from “".
func TestTaskBriefWithoutABase(t *testing.T) {
	known, unknown := &project{info: proto.ProjectInfo{Name: "spring", Base: "main"}}, &project{info: proto.ProjectInfo{Name: "web"}}
	b := taskBrief(proto.GroupTaskParams{Group: "clinic", Branch: "feat", Prompt: "Do it"}, "", []*project{known, unknown},
		[]string{"spring", "web"}, []string{"/s", "/w"})
	if !strings.Contains(b, "branch `feat` from `main`; its worktree is /s") || !strings.Contains(b, "- `web/` — web, branch `feat`; its worktree is /w") || strings.Contains(b, "``") {
		t.Fatalf("%s", b)
	}
}
