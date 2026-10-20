package server

import (
	"context"
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

	"github.com/Amitgb14/conch/internal/gitx"
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
	// shipping is branches being pushed or given a pull request,
	// reship those whose agent finished again meanwhile, shipped again
	// at the next read of their pull requests, and unshipped what last
	// kept one from it, said once.
	shipping  map[string]bool
	reship    map[string]bool
	unshipped map[string]string
	resolving map[string]bool // conflicts being worked on now
	starting  map[string]bool // checks being started, by branch
	// capSaid is branches told their auto_fix allowance is spent, so it
	// is said once rather than at every failure after.
	capSaid map[string]bool
	// recheck is branches whose agent finished again while a check ran,
	// gateMemo the gates last read for each branch, said the last thing
	// said about each branch's conflict or check (noteOnce), and
	// resolvesBusy whether a pass over the conflicts is under way.
	recheck      map[string]bool
	gateMemo     map[string]gateResult
	said         map[string]string
	resolvesBusy bool
	// lastCheck is the terminal of each branch's last check, closed when
	// the next one starts.
	lastCheck map[string]string
	// prompt stands in for promptAgent in tests, where a message that
	// fails to reach a running agent cannot be arranged any other way.
	prompt func(proto.AgentPromptParams) (proto.AgentPromptResult, *proto.Error)
}

// autopilotFile is autopilot.json.
type autopilotFile struct {
	Policy proto.AutopilotPolicy `json:"policy"`
	// Seen is the projects whose pull requests have been read once, and
	// Checks what each of their branches' open pull request's CI was then.
	Seen   map[string]bool   `json:"seen,omitempty"`
	Checks map[string]string `json:"checks,omitempty"`
	// Fixes counts the project's own check's failures sent back, and
	// CIFixes CI's: two allowances, since a local check passing says
	// nothing about CI, and one count reset by it let CI's go on for ever.
	Fixes   map[string]int `json:"fixes,omitempty"`
	CIFixes map[string]int `json:"ci_fixes,omitempty"`
	// Waiting maps a green pull request waiting to merge to what last
	// held it back, so a reason is said once rather than at every look.
	Waiting map[string]string `json:"waiting,omitempty"`
	// Failed maps a branch to the commit its worktree was at when the
	// project's own check failed there.
	Failed map[string]string `json:"failed,omitempty"`
	// Passed is the same for a check that passed: a branch is shipped
	// only at a commit its check passed for.
	Passed map[string]string `json:"passed,omitempty"`
	// CheckPanes maps a terminal the server is running a check in to its
	// branch, so a check that ends after a reload is still read.
	CheckPanes map[string]string `json:"check_panes,omitempty"`
	// CheckHeads is the commit each of those started on: what its verdict
	// is about, whatever the branch is at by the time it ends.
	CheckHeads map[string]string `json:"check_heads,omitempty"`
	// PendingCI is branches whose CI turned red while their agent was
	// busy: sent once it is free, unless CI is green again by then.
	PendingCI map[string]bool `json:"pending_ci,omitempty"`
	// Asked maps a branch to the commit it was at when its agent was asked
	// to commit what it left uncommitted, so it is asked once, not at
	// every finish.
	Asked map[string]string `json:"asked,omitempty"`
	// Conflicted is the open pull requests that conflict with their base,
	// each with the commit the server last acted at ("" for not yet), and
	// Resolves how many times its agent has been asked to resolve one.
	Conflicted map[string]string `json:"conflicted,omitempty"`
	Resolves   map[string]int    `json:"resolves,omitempty"`
	// Rewrites is branches rebased onto their base, by the server or by
	// the agent it asked, that must replace what was pushed — checked
	// first (autopilot_resolve.go).
	Rewrites map[string]rewrite `json:"rewrites,omitempty"`
	// Drafted is branches whose pull request conch opened as a draft for
	// a gate (require_tests, max_diff), to mark ready once they are met;
	// TestAsked the commit each agent was asked for a test at.
	Drafted   map[string]bool   `json:"drafted,omitempty"`
	TestAsked map[string]string `json:"test_asked,omitempty"`
}

// init makes every map a file left out, as one from an older build does.
func (f *autopilotFile) init() {
	orNew(&f.Seen)
	orNew(&f.Checks)
	orNew(&f.Fixes)
	orNew(&f.CIFixes)
	orNew(&f.Waiting)
	orNew(&f.Failed)
	orNew(&f.Passed)
	orNew(&f.CheckPanes)
	orNew(&f.CheckHeads)
	orNew(&f.PendingCI)
	orNew(&f.Asked)
	orNew(&f.Conflicted)
	orNew(&f.Resolves)
	orNew(&f.Rewrites)
	orNew(&f.Drafted)
	orNew(&f.TestAsked)
}

func orNew[K comparable, V any](m *map[K]V) {
	if *m == nil {
		*m = map[K]V{}
	}
}

func loadAutopilot(configDir string) *autopilot {
	a := &autopilot{file: filepath.Join(configDir, "autopilot.json"), merging: map[string]bool{},
		shipping: map[string]bool{}, unshipped: map[string]string{}, lastCheck: map[string]string{}, resolving: map[string]bool{},
		starting: map[string]bool{}, capSaid: map[string]bool{}, recheck: map[string]bool{}, reship: map[string]bool{},
		gateMemo: map[string]gateResult{}, said: map[string]string{}}
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
	a.saved.init()
	return a
}

// autoFixMax is the most failures in a row an agent is sent: past it,
// an agent that cannot fix a test spends the night, and the plan, on it.
const autoFixMax = 10

func cleanPolicy(p proto.AutopilotPolicy) proto.AutopilotPolicy {
	p.AutoFix = max(0, min(p.AutoFix, autoFixMax))
	p.AutoResolve = max(0, min(p.AutoResolve, autoFixMax))
	p.MaxDiff = max(0, p.MaxDiff)
	checks := map[string]string{}
	for id, cmd := range p.Checks {
		if cmd = strings.TrimSpace(cmd); id != "" && cmd != "" {
			checks[id] = cmd
		}
	}
	p.Checks = nil
	if len(checks) > 0 {
		p.Checks = checks
	}
	return p
}

// freshStart forgets the checks that were running: a server that starts
// afresh, not by a reload, has none of the panes they ran in, and numbers
// its own from p1 again — a saved pane ID would name somebody's shell.
func (a *autopilot) freshStart() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.saved.CheckPanes) == 0 && len(a.saved.CheckHeads) == 0 {
		return
	}
	a.saved.CheckPanes = map[string]string{}
	a.saved.CheckHeads = map[string]string{}
	a.saveLocked()
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
	// The heads are read from git, which can be slow: not while holding
	// the lock everything else in the autopilot waits on.
	a.mu.Lock()
	failed := make(map[string]string, len(a.saved.Failed))
	for key, head := range a.saved.Failed {
		failed[key] = head
	}
	a.mu.Unlock()
	failedNow := map[string]bool{}
	for key, head := range failed {
		failedNow[key] = head != "" && head == headOf(key)
	}
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
		at(key).Fixes += n
	}
	for key, n := range a.saved.CIFixes {
		at(key).Fixes += n
	}
	for key, held := range a.saved.Waiting {
		b := at(key)
		b.Waiting, b.Held = true, held
	}
	for key, now := range failedNow {
		if now && a.saved.Failed[key] == failed[key] {
			at(key).CheckFailed = true
		}
	}
	for pane, key := range a.saved.CheckPanes {
		at(key).Checking = pane
	}
	for key, why := range a.unshipped {
		if why != "" {
			at(key).Unshipped = why
		}
	}
	for key := range a.saved.Conflicted {
		at(key).Conflicting = true
	}
	for key, n := range a.saved.Resolves {
		at(key).Resolves = n
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
		checks := proto.PRChecks(b)
		now[key] = checks
		was := a.saved.Checks[key]
		switch {
		case checks == "pass":
			// Only the turn to green renews the allowance: CI still green
			// on a pushed commit says nothing about the project's check
			// failing again and again on the agent's newer one.
			if was != "pass" {
				delete(a.saved.CIFixes, key)
				delete(a.capSaid, ciCount(key))
			}
			delete(a.saved.PendingCI, key)
			if !first && was != "pass" && a.saved.Policy.AutoMerge && b.Name != info.Base {
				if _, waiting := a.saved.Waiting[key]; !waiting {
					a.saved.Waiting[key] = ""
				}
			}
		case checks == "fail" && !first && was != "fail":
			red = append(red, b.Name)
		}
		// A conflict is minded from the turn it was seen, like the rest.
		_, known := a.saved.Conflicted[key]
		switch conflicting := b.PR != nil && b.PR.State == "OPEN" && b.PR.Conflicting; {
		case conflicting && !known && !first:
			a.saved.Conflicted[key] = ""
		case conflicting && first:
			a.saved.Conflicted[key] = conflictUnwatched
		case !conflicting:
			delete(a.saved.Conflicted, key)
			delete(a.saved.Resolves, key)
			// The next conflict is news, even said in the same words.
			delete(a.said, key+"\x00resolve")
		}
	}
	for key := range a.saved.Checks {
		if strings.HasPrefix(key, prefix) {
			if _, ok := now[key]; !ok {
				delete(a.saved.Checks, key)
			}
		}
	}
	for key := range a.saved.Conflicted {
		if strings.HasPrefix(key, prefix) {
			if _, ok := now[key]; !ok {
				delete(a.saved.Conflicted, key)
				delete(a.saved.Resolves, key)
				delete(a.said, key+"\x00resolve")
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

// forget drops a project the server no longer has: everything kept about
// its branches, saved or in memory, so one added again starts afresh.
func (a *autopilot) forget(projectID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.saved.Seen, projectID)
	prefix := projectID + "\x00"
	ours := func(key string) bool { return strings.HasPrefix(key, prefix) }
	f := &a.saved
	dropKeys(ours, f.Checks, f.Waiting, f.Failed, f.Passed, f.Asked, f.Conflicted, f.TestAsked, a.unshipped, a.lastCheck, a.said)
	dropKeys(ours, f.Fixes, f.CIFixes, f.Resolves)
	dropKeys(ours, f.PendingCI, f.Drafted, a.capSaid, a.recheck, a.reship)
	dropKeys(ours, f.Rewrites)
	dropKeys(ours, a.gateMemo)
	for pane, key := range f.CheckPanes {
		if ours(key) {
			delete(f.CheckPanes, pane)
			delete(f.CheckHeads, pane)
		}
	}
	a.saveLocked()
}

// dropKeys deletes from each map the keys drop says to.
func dropKeys[V any](drop func(string) bool, maps ...map[string]V) {
	for _, m := range maps {
		for key := range m {
			if drop(key) {
				delete(m, key)
			}
		}
	}
}

// takeFix counts one more failure going back to a branch's agent, and
// says whether the allowance had room for it. At the cap it answers
// false, with capped set the first time only, so the person is told once.
func (a *autopilot) takeFix(key string, ci bool) (ok, capped bool, limit int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	limit = a.saved.Policy.AutoFix
	if limit <= 0 {
		return false, false, 0
	}
	counts, said := a.saved.Fixes, key
	if ci {
		counts, said = a.saved.CIFixes, ciCount(key)
	}
	if counts[key] >= limit {
		capped = !a.capSaid[said]
		a.capSaid[said] = true
		return false, capped, limit
	}
	counts[key]++
	a.saveLocked()
	return true, false, limit
}

// ciCount is the key capSaid knows CI's allowance by.
func ciCount(key string) string { return key + "\x00ci" }

// giveBack undoes a takeFix whose message never reached the agent.
func (a *autopilot) giveBack(key string, ci bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	counts := a.saved.Fixes
	if ci {
		counts = a.saved.CIFixes
	}
	if counts[key] > 0 {
		counts[key]--
		if counts[key] == 0 {
			delete(counts, key)
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
		delete(a.capSaid, key)
		a.saved.Passed[key] = head
	} else {
		a.saved.Failed[key] = head
		delete(a.saved.Passed, key)
	}
	a.saveLocked()
}

// ---- the server's side ----

// note tells every client what the autopilot did, and the log.
func (s *Server) autopilotNote(n proto.AutopilotNote) {
	log.Printf("autopilot: %s %s: %s", n.ProjectID, n.Branch, n.Text)
	s.broadcast(proto.EventAutopilotNote, n)
}

// autopilotPrompt is a message from the autopilot to an agent.
func (s *Server) autopilotPrompt(p proto.AgentPromptParams) (proto.AgentPromptResult, *proto.Error) {
	if f := s.autopilot.prompt; f != nil {
		return f(p)
	}
	return s.promptAgent(p)
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
			s.autopilotPendingCI()
			go s.autopilotResolves() // a fetch can take a while; merges must not wait for it
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
	s.autopilotReship(info.ID)
	s.autopilotMerges()
}

// freeAgent is the branch's agent when it can take a message now: never
// one waiting on the person, never one still working, and never one
// started for the purpose — nobody asked for an agent.
func (s *Server) freeAgent(projectID, branch string) *proto.PaneInfo {
	agent := s.branchAgent(projectID, branch)
	if agent == nil || busy(*agent) {
		return nil
	}
	return agent
}

// busyAgent is the branch's agent while it is working or waiting on the
// person, and nil when it is free or there is none.
func (s *Server) busyAgent(projectID, branch string) *proto.PaneInfo {
	agent := s.branchAgent(projectID, branch)
	if agent == nil || !busy(*agent) {
		return nil
	}
	return agent
}

func busy(agent proto.PaneInfo) bool {
	return agent.Agent.State == proto.AgentWorking || agent.Agent.State == proto.AgentBlocked
}

// autopilotPendingCI sends the red CI put off while an agent was busy,
// now that it is free — unless the pull request is not red any more.
func (s *Server) autopilotPendingCI() {
	a := s.autopilot
	a.mu.Lock()
	keys := make([]string, 0, len(a.saved.PendingCI))
	for key := range a.saved.PendingCI {
		keys = append(keys, key)
	}
	a.mu.Unlock()
	for _, key := range keys {
		projectID, branch, _ := strings.Cut(key, "\x00")
		p, perr := s.projects.get(projectID)
		red := false
		if perr == nil {
			p.mu.Lock()
			pr, ok := p.prs[branch]
			p.mu.Unlock()
			red = ok && pr.State == "OPEN" && pr.Checks == "fail"
		}
		if red && s.freeAgent(projectID, branch) == nil && s.busyAgent(projectID, branch) != nil {
			continue // still busy
		}
		a.mu.Lock()
		delete(a.saved.PendingCI, key)
		a.saveLocked()
		a.mu.Unlock()
		if red {
			s.autopilotFixCI(projectID, branch)
		}
	}
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
		if waiting := s.busyAgent(projectID, branch); waiting != nil && what == "CI" {
			// Red is only reported on the turn, so a failure put off now
			// is remembered, or it would never be sent.
			s.autopilot.mu.Lock()
			already := s.autopilot.saved.PendingCI[key]
			s.autopilot.saved.PendingCI[key] = true
			s.autopilot.saveLocked()
			s.autopilot.mu.Unlock()
			if !already {
				s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch,
					Text: fmt.Sprintf("CI failed on %s; it goes to %s once it is free", branch, waiting.DisplayName())})
			}
		}
		return
	}
	ci := what == "CI"
	ok, capped, limit := s.autopilot.takeFix(key, ci)
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
			_, perr := s.autopilotPrompt(proto.AgentPromptParams{ID: agent.ID, Text: prompt})
			if perr != nil {
				err = perr
			}
		}
		if err != nil {
			s.autopilot.giveBack(key, ci)
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
	if s.autopilot.ownCheck(ck.Pane) {
		return s.autopilotState(), nil // the server reads its own when it ends
	}
	s.autopilotCheckEnded(p, ck, nil, "")
	return s.autopilotState(), nil
}

// autopilotCheckEnded records how a check on a branch ended — one the
// server ran, with the output it printed and the commit it started on, or
// one a client reports, whose terminal is read — and acts on it: a failure goes to
// the agent, a pass ships the branch and may be what a green pull
// request waited for.
func (s *Server) autopilotCheckEnded(p *project, ck proto.AutopilotCheck, output []string, head string) {
	if head == "" {
		head = branchHead(p, ck.Branch)
	}
	s.autopilot.checked(branchKey(ck.ProjectID, ck.Branch), head, ck.Exit == 0)
	if ck.Exit == 0 {
		go s.autopilotShip(ck.ProjectID, ck.Branch)
	} else {
		s.autopilotFix(ck.ProjectID, ck.Branch, "The project's check", func() (string, error) {
			lines := output
			if lines == nil && ck.Pane != "" {
				e, perr := s.get(ck.Pane)
				if perr != nil {
					return "", fmt.Errorf("the check's terminal is gone, and its output with it")
				}
				lines = e.p.PlainLines()
			}
			return proto.FailurePrompt("The project's check", ck.Command, ck.Exit, proto.FailureTail(lines)), nil
		})
	}
	s.autopilotMerges()
}

// autopilotState is autopilot.get's answer.
func (s *Server) autopilotState() proto.AutopilotState {
	return s.autopilot.state(func(key string) string {
		projectID, branch, _ := strings.Cut(key, "\x00")
		p, perr := s.projects.get(projectID)
		if perr != nil {
			return ""
		}
		return branchHead(p, branch)
	})
}

// branchHead is the commit branch is at, read from git: the last refresh
// can be a commit behind an agent that has just committed, and a check's
// verdict is about the commit it ran on.
func branchHead(p *project, branch string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	head, err := gitx.BranchHead(ctx, p.root, branch)
	if err != nil {
		return ""
	}
	return head
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
	// The verdict that matters is the one on the commit being merged;
	// without its head from GitHub, the branch's is the best there is.
	at := pr.Head
	if at == "" {
		at = branchHead(p, branch)
	}
	why := s.mergeHold(info, branch, pr, failedAt != "" && failedAt == at)
	if why == "" {
		why = s.mergeGates(p, info, branch, pr, at)
	}
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

// mergeGates holds a green pull request for its project's check until it
// has passed on the commit being merged — running it there when that
// commit is checked out, and otherwise saying where it has to be — and
// for require_tests and max_diff, which hold every pull request, not only
// the ones conch opened as drafts.
func (s *Server) mergeGates(p *project, info proto.ProjectInfo, branch string, pr proto.PRInfo, at string) string {
	key := branchKey(info.ID, branch)
	a := s.autopilot
	pol := a.policy()
	if cmd := pol.Checks[info.ID]; cmd != "" {
		a.mu.Lock()
		passed := at != "" && a.saved.Passed[key] == at
		a.mu.Unlock()
		wt, checkedOut := p.branchWorktree(branch)
		switch {
		case s.checkBusy(key):
			return "this project's check is still running"
		case passed:
		case at == "":
			return fmt.Sprintf("the commit #%d would merge could not be read, so this project's check cannot vouch for it", pr.Number)
		case checkedOut && wt.Path != "" && branchHead(p, branch) == at:
			// A failure there is mergeHold's to say; this is a commit
			// with no verdict yet.
			go s.autopilotStartCheck(info.ID, branch, cmd)
			return "this project's check has not run on it yet; running it"
		default:
			// Merging would put a commit nobody checked on the base, and
			// running the check every tick where it cannot run does
			// nothing but say so every tick.
			return fmt.Sprintf("this project's check has not passed on %s, the commit #%d is at; it runs once %s is checked out there", shortCommit(at), pr.Number, branch)
		}
	}
	if unmet := s.gatesAt(p, key, info.Base, at); len(unmet) > 0 {
		return strings.Join(unmet, " and ")
	}
	return ""
}

// shortCommit is a commit as a person reads one.
func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
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
		if s.autopilot.policy().AutoResolve > 0 {
			return fmt.Sprintf("#%d conflicts with %s; conch is bringing it up to date (auto_resolve), and it merges once it is green again", pr.Number, base)
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
	if agent := s.busyAgent(info.ID, branch); agent != nil {
		return agent.DisplayName() + " is still at it"
	}
	return ""
}
