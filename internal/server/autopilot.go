package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// The autopilot is the part of finishing a branch that nobody needs to be
// watching for: a failed CI run or check goes back to the branch's agent
// ([verify] auto_fix), and a pull request that turns green merges on
// GitHub (auto_merge). It used to be the TUI's, so it stopped when the TUI
// closed and two TUIs could both act; the server is the one thing that is
// always there, so it is here, and the TUI only hands it the policy.
//
// Everything it remembers is in autopilot.json: the policy, the CI each
// pull request last had (what a turn is measured from, so a run that went
// red during a reload is still seen), how many failures went back, which
// green pull requests wait to merge, and where the project's own check
// failed. Only a turn the server watched counts: a pull request first seen
// red is not sent to an agent that may have finished with it days ago, and
// one first seen green is not merged.

// autopilotTick is how often green pull requests waiting to merge are
// looked at again: an agent finishing, a thread resolved or a check
// passing may be all one waited for, and none of those says so here.
const autopilotTick = 5 * time.Second

type autopilot struct {
	file string

	mu      sync.Mutex
	saved   autopilotFile
	merging map[string]bool // merges under way, by branch key
}

// autopilotFile is autopilot.json.
type autopilotFile struct {
	Policy proto.AutopilotPolicy `json:"policy"`
	// Seen is the projects whose pull requests have been read once, and
	// Checks what each of their branches' open pull request's CI was then.
	Seen   map[string]bool   `json:"seen,omitempty"`
	Checks map[string]string `json:"checks,omitempty"`
	Fixes  map[string]int    `json:"fixes,omitempty"`
	// Waiting maps a green pull request waiting to merge to what last
	// held it back, so a reason is said once rather than at every look.
	Waiting map[string]string `json:"waiting,omitempty"`
	// Failed maps a branch to the commit its worktree was at when the
	// project's own check failed there.
	Failed map[string]string `json:"failed,omitempty"`
}

func loadAutopilot(configDir string) *autopilot {
	a := &autopilot{file: filepath.Join(configDir, "autopilot.json"), merging: map[string]bool{}}
	raw, err := os.ReadFile(a.file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("autopilot: %v", err)
	}
	if err == nil {
		if err := json.Unmarshal(raw, &a.saved); err != nil {
			log.Printf("autopilot: %s: %v", a.file, err)
			a.saved = autopilotFile{}
		}
	}
	a.saved.Policy = cleanPolicy(a.saved.Policy)
	if a.saved.Seen == nil {
		a.saved.Seen = map[string]bool{}
	}
	if a.saved.Checks == nil {
		a.saved.Checks = map[string]string{}
	}
	if a.saved.Fixes == nil {
		a.saved.Fixes = map[string]int{}
	}
	if a.saved.Waiting == nil {
		a.saved.Waiting = map[string]string{}
	}
	if a.saved.Failed == nil {
		a.saved.Failed = map[string]string{}
	}
	return a
}

// autoFixMax is the most failures in a row an agent is sent: past it,
// an agent that cannot fix a test spends the night, and the plan, on it.
const autoFixMax = 10

func cleanPolicy(p proto.AutopilotPolicy) proto.AutopilotPolicy {
	p.AutoFix = max(0, min(p.AutoFix, autoFixMax))
	return p
}

func (a *autopilot) saveLocked() {
	raw, _ := json.MarshalIndent(a.saved, "", "  ")
	tmp := a.file + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		log.Printf("autopilot: save: %v", err)
		return
	}
	if err := os.Rename(tmp, a.file); err != nil {
		log.Printf("autopilot: save: %v", err)
	}
}

func (a *autopilot) policy() proto.AutopilotPolicy {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saved.Policy
}

// set takes a new policy. Turning auto_merge off forgets the pull
// requests waiting, so turning it on again later merges none that went
// green meanwhile, unwatched.
func (a *autopilot) set(p proto.AutopilotPolicy) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.saved.Policy = cleanPolicy(p)
	if !a.saved.Policy.AutoMerge {
		a.saved.Waiting = map[string]string{}
	}
	a.saveLocked()
}

// state is what autopilot.get answers.
func (a *autopilot) state(headOf func(key string) string) proto.AutopilotState {
	a.mu.Lock()
	defer a.mu.Unlock()
	byKey := map[string]*proto.AutopilotBranch{}
	at := func(key string) *proto.AutopilotBranch {
		if b := byKey[key]; b != nil {
			return b
		}
		projectID, branch, _ := strings.Cut(key, "\x00")
		b := &proto.AutopilotBranch{ProjectID: projectID, Branch: branch}
		byKey[key] = b
		return b
	}
	for key, n := range a.saved.Fixes {
		at(key).Fixes = n
	}
	for key, held := range a.saved.Waiting {
		b := at(key)
		b.Waiting, b.Held = true, held
	}
	for key, head := range a.saved.Failed {
		if head != "" && head == headOf(key) {
			at(key).CheckFailed = true
		}
	}
	out := proto.AutopilotState{Policy: a.saved.Policy}
	for _, b := range byKey {
		out.Branches = append(out.Branches, *b)
	}
	sort.Slice(out.Branches, func(i, j int) bool {
		x, y := out.Branches[i], out.Branches[j]
		return x.ProjectID < y.ProjectID || x.ProjectID == y.ProjectID && x.Branch < y.Branch
	})
	return out
}

// prChecks is a branch's CI as the autopilot compares it: an open pull
// request's checks, and "" for a branch with none. A closed or merged one
// is nobody's move any more.
func prChecks(b proto.BranchInfo) string {
	if b.PR == nil || b.PR.State != "OPEN" {
		return ""
	}
	return b.PR.Checks
}

// observe is a project's pull requests as just read from GitHub. It
// returns the branches whose CI has just turned red, to go back to their
// agents. It is only called with a read that worked: one that failed
// says nothing about the checks, and taking it for "no pull requests"
// would make every red one look newly red once gh answers again.
func (a *autopilot) observe(info proto.ProjectInfo) (red []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	first := !a.saved.Seen[info.ID]
	a.saved.Seen[info.ID] = true
	prefix := info.ID + "\x00"
	now := map[string]string{}
	for _, b := range info.Branches {
		key := branchKey(info.ID, b.Name)
		checks := prChecks(b)
		now[key] = checks
		was := a.saved.Checks[key]
		switch {
		case checks == "pass":
			delete(a.saved.Fixes, key)
			if !first && was != "pass" && a.saved.Policy.AutoMerge && b.Name != info.Base {
				if _, waiting := a.saved.Waiting[key]; !waiting {
					a.saved.Waiting[key] = ""
				}
			}
		case checks == "fail" && !first && was != "fail":
			red = append(red, b.Name)
		}
	}
	for key := range a.saved.Checks {
		if strings.HasPrefix(key, prefix) {
			if _, ok := now[key]; !ok {
				delete(a.saved.Checks, key)
			}
		}
	}
	for key, checks := range now {
		if checks == "" {
			delete(a.saved.Checks, key)
		} else {
			a.saved.Checks[key] = checks
		}
	}
	// Red again, pending again, merged, closed or gone: nothing to wait
	// for. A merge under way stays until it answers.
	for key := range a.saved.Waiting {
		if strings.HasPrefix(key, prefix) && now[key] != "pass" && !a.merging[key] {
			delete(a.saved.Waiting, key)
		}
	}
	a.saveLocked()
	return red
}

// forget drops a project the server no longer has.
func (a *autopilot) forget(projectID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.saved.Seen, projectID)
	prefix := projectID + "\x00"
	for _, m := range []map[string]string{a.saved.Checks, a.saved.Waiting, a.saved.Failed} {
		for key := range m {
			if strings.HasPrefix(key, prefix) {
				delete(m, key)
			}
		}
	}
	for key := range a.saved.Fixes {
		if strings.HasPrefix(key, prefix) {
			delete(a.saved.Fixes, key)
		}
	}
	a.saveLocked()
}

// takeFix counts one more failure going back to a branch's agent, and
// says whether the allowance had room for it. At the cap it answers
// false once with capped set, so the person is told once.
func (a *autopilot) takeFix(key string) (ok, capped bool, limit int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	limit = a.saved.Policy.AutoFix
	if limit <= 0 {
		return false, false, 0
	}
	if a.saved.Fixes[key] >= limit {
		return false, true, limit
	}
	a.saved.Fixes[key]++
	a.saveLocked()
	return true, false, limit
}

// giveBack undoes a takeFix whose message never reached the agent.
func (a *autopilot) giveBack(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.saved.Fixes[key] > 0 {
		a.saved.Fixes[key]--
		if a.saved.Fixes[key] == 0 {
			delete(a.saved.Fixes, key)
		}
		a.saveLocked()
	}
}

// checked records how the project's own check ended at head.
func (a *autopilot) checked(key, head string, passed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if passed {
		delete(a.saved.Failed, key)
		delete(a.saved.Fixes, key)
	} else {
		a.saved.Failed[key] = head
	}
	a.saveLocked()
}

// ---- the server's side ----

// note tells every client what the autopilot did, and the log.
func (s *Server) autopilotNote(n proto.AutopilotNote) {
	log.Printf("autopilot: %s %s: %s", n.ProjectID, n.Branch, n.Text)
	s.broadcast(proto.EventAutopilotNote, n)
}

// runAutopilot looks at waiting merges until the server stops.
func (s *Server) runAutopilot(quit <-chan struct{}) {
	t := time.NewTicker(autopilotTick)
	defer t.Stop()
	for {
		select {
		case <-quit:
			return
		case <-t.C:
			s.autopilotMerges()
		}
	}
}

// autopilotPRs is a project's pull requests as just read: CI that turned
// red goes back to its agents, and green ones may merge.
func (s *Server) autopilotPRs(info proto.ProjectInfo) {
	if s.autopilot == nil {
		return
	}
	for _, branch := range s.autopilot.observe(info) {
		s.autopilotFixCI(info.ID, branch)
	}
	s.autopilotMerges()
}

// freeAgent is the branch's agent when it can take a message now: never
// one waiting on the person, never one still working, and never one
// started for the purpose — nobody asked for an agent.
func (s *Server) freeAgent(projectID, branch string) *proto.PaneInfo {
	agent := s.branchAgent(projectID, branch)
	if agent == nil || agent.Agent.State == proto.AgentBlocked || agent.Agent.State == proto.AgentWorking {
		return nil
	}
	return agent
}

// autopilotFixCI sends a branch's failed CI to its agent, read through gh
// on this machine.
func (s *Server) autopilotFixCI(projectID, branch string) {
	s.autopilotFix(projectID, branch, "CI", func() (string, error) {
		lg, perr := s.projects.ciLog(proto.BranchCILogParams{ProjectID: projectID, Branch: branch})
		if perr != nil {
			return "", perr
		}
		return proto.CIFailurePrompt(lg), nil
	})
}

// autopilotFix is one failure going back to a branch's agent, if
// auto_fix has room and the agent is free; text is what it is told,
// fetched only once it is going.
func (s *Server) autopilotFix(projectID, branch, what string, text func() (string, error)) {
	key := branchKey(projectID, branch)
	if s.autopilot.policy().AutoFix <= 0 {
		return
	}
	agent := s.freeAgent(projectID, branch)
	if agent == nil {
		return
	}
	ok, capped, limit := s.autopilot.takeFix(key)
	if capped {
		s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Error: true,
			Text: fmt.Sprintf("%s failed on %s again; auto_fix tried %d times, so it's yours now", what, branch, limit)})
	}
	if !ok {
		return
	}
	go func() {
		prompt, err := text()
		if err == nil {
			_, perr := s.promptAgent(proto.AgentPromptParams{ID: agent.ID, Text: prompt})
			if perr != nil {
				err = perr
			}
		}
		if err != nil {
			s.autopilot.giveBack(key)
			s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Error: true,
				Text: fmt.Sprintf("%s failed on %s, and could not go to %s: %v", what, branch, agent.DisplayName(), err)})
			return
		}
		s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch,
			Text: fmt.Sprintf("%s failed on %s: sent it to %s to fix", what, branch, agent.DisplayName())})
	}()
}

// autopilotCheck is how the project's own check on a branch ended, told
// by the client that ran it: a failure holds the branch's pull request
// back from merging until the branch moves, and goes back to its agent
// like CI's does, counted in the same allowance.
func (s *Server) autopilotCheck(ck proto.AutopilotCheck) (proto.AutopilotState, *proto.Error) {
	p, perr := s.projects.get(ck.ProjectID)
	if perr != nil {
		return proto.AutopilotState{}, perr
	}
	if ck.Branch == "" {
		return proto.AutopilotState{}, proto.Errorf(proto.ErrBadRequest, "no branch given")
	}
	wt, _ := p.branchWorktree(ck.Branch)
	s.autopilot.checked(branchKey(ck.ProjectID, ck.Branch), wt.Head, ck.Exit == 0)
	if ck.Exit != 0 {
		s.autopilotFix(ck.ProjectID, ck.Branch, "The project's check", func() (string, error) {
			var lines []string
			if ck.Pane != "" {
				e, perr := s.get(ck.Pane)
				if perr != nil {
					return "", fmt.Errorf("the check's terminal is gone, and its output with it")
				}
				lines = e.p.PlainLines()
			}
			return proto.FailurePrompt("The project's check", ck.Command, ck.Exit, proto.FailureTail(lines)), nil
		})
	}
	s.autopilotMerges() // a pass may be what a green pull request waited for
	return s.autopilotState(), nil
}

// autopilotState is autopilot.get's answer.
func (s *Server) autopilotState() proto.AutopilotState {
	return s.autopilot.state(func(key string) string {
		projectID, branch, _ := strings.Cut(key, "\x00")
		p, perr := s.projects.get(projectID)
		if perr != nil {
			return ""
		}
		wt, _ := p.branchWorktree(branch)
		return wt.Head
	})
}

// autopilotMerges merges every green pull request that waits and that
// nothing holds back any more, saying once what holds the others.
func (s *Server) autopilotMerges() {
	a := s.autopilot
	a.mu.Lock()
	if !a.saved.Policy.AutoMerge || len(a.saved.Waiting) == 0 {
		a.mu.Unlock()
		return
	}
	keys := make([]string, 0, len(a.saved.Waiting))
	for key := range a.saved.Waiting {
		if !a.merging[key] {
			keys = append(keys, key)
		}
	}
	a.mu.Unlock()
	sort.Strings(keys)
	for _, key := range keys {
		s.autopilotMerge(key)
	}
}

// autopilotMerge is autopilotMerges' decision for one pull request.
func (s *Server) autopilotMerge(key string) {
	a := s.autopilot
	projectID, branch, _ := strings.Cut(key, "\x00")
	p, perr := s.projects.get(projectID)
	if perr != nil {
		a.forget(projectID)
		return
	}
	p.mu.Lock()
	info := p.info
	pr, havePR := p.prs[branch]
	p.mu.Unlock()
	var wt proto.WorktreeInfo
	for _, w := range info.Worktrees {
		if w.Branch == branch {
			wt = w
		}
	}
	a.mu.Lock()
	held, waiting := a.saved.Waiting[key]
	failedAt := a.saved.Failed[key]
	a.mu.Unlock()
	if !waiting {
		return
	}
	if !havePR || pr.State != "OPEN" || pr.Checks != "pass" || branch == info.Base {
		a.mu.Lock()
		delete(a.saved.Waiting, key)
		a.saveLocked()
		a.mu.Unlock()
		return
	}
	why := s.mergeHold(info, branch, pr, failedAt != "" && failedAt == wt.Head)
	if why != "" {
		if why != held {
			a.mu.Lock()
			if _, still := a.saved.Waiting[key]; still {
				a.saved.Waiting[key] = why
				a.saveLocked()
			}
			a.mu.Unlock()
			s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch,
				Text: fmt.Sprintf("%s is green but not merged: %s", branch, why)})
		}
		return
	}
	a.mu.Lock()
	if a.merging[key] {
		a.mu.Unlock()
		return
	}
	a.merging[key] = true
	a.mu.Unlock()
	go func() {
		res, perr := s.projects.mergePR(proto.BranchPRMergeParams{ProjectID: projectID, Branch: branch, Number: pr.Number, Head: pr.Head})
		// One GitHub refused is not tried again until it turns green
		// again — after a push, which is what it takes to change
		// GitHub's mind — so a refusal is said once, not every tick.
		a.mu.Lock()
		delete(a.merging, key)
		delete(a.saved.Waiting, key)
		a.saveLocked()
		a.mu.Unlock()
		if perr != nil {
			s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Error: true,
				Text: fmt.Sprintf("#%d is green but GitHub would not merge it: %s", pr.Number, perr.Message)})
			return
		}
		s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Merged: true,
			Text: fmt.Sprintf("merged #%d on GitHub (%s), green", pr.Number, res.Method)})
	}()
}

// mergeHold says what keeps a green pull request from merging, or ""
// when nothing does. It says why rather than holding back quietly: a
// merge that does not happen is as surprising as one that does.
func (s *Server) mergeHold(info proto.ProjectInfo, branch string, pr proto.PRInfo, checkFailed bool) string {
	switch {
	case pr.Draft:
		return "its pull request is a draft"
	case pr.Review == "CHANGES_REQUESTED":
		return fmt.Sprintf("somebody asked for changes on #%d", pr.Number)
	case pr.Conflicting:
		base := info.Base
		if base == "" {
			base = "its base"
		}
		return fmt.Sprintf("#%d conflicts with %s; rebase it and it merges once it is green again", pr.Number, base)
	case checkFailed:
		return "this project's own check failed, which CI passing does not answer"
	}
	// An unresolved thread is somebody waiting for an answer; a review
	// that formally asked for changes is caught above. conch's own
	// comments are the person's working notes and are not counted, or a
	// stale one would hold a branch for ever.
	if s.reviews != nil {
		n := 0
		for _, c := range s.reviews.list(proto.ReviewRef{ProjectID: info.ID, Branch: branch}).Comments {
			if c.Source == proto.ReviewGitHub {
				n++
			}
		}
		if n == 1 {
			return fmt.Sprintf("1 review thread unresolved on #%d", pr.Number)
		} else if n > 1 {
			return fmt.Sprintf("%d review threads unresolved on #%d", n, pr.Number)
		}
	}
	if agent := s.branchAgent(info.ID, branch); agent != nil &&
		(agent.Agent.State == proto.AgentWorking || agent.Agent.State == proto.AgentBlocked) {
		return agent.DisplayName() + " is still at it"
	}
	return ""
}
