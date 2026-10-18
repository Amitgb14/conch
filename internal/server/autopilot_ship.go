package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/gitx"
	"github.com/Amitgb14/conch/internal/proto"
)

// Shipping is the start of the loop the rest of the autopilot closes: an
// agent finishes on a branch, the project's check runs there, and once it
// passes the branch is pushed and given a pull request — or its new
// commits pushed to the one already open, which is how an agent's fix for
// red CI gets back to CI. From there CI's verdict goes back to the agent
// or merges it (autopilot.go).
//
// The check is run here, not by a TUI, for the same reason the rest is:
// nobody need be watching. It runs in a terminal like the one the TUI
// starts — same name, same command — so a TUI open at the time shows it
// in the review queue as its own.

// checkPrefix names a check's terminal; the TUI's are named the same.
const checkPrefix = "check · "

// ownCheck says whether pane is a check the server started.
func (a *autopilot) ownCheck(pane string) bool {
	if pane == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.saved.CheckPanes[pane]
	return ok
}

// autopilotFinished is an agent that has just finished a turn on a
// branch: its check runs, where the project has one, or the branch ships.
func (s *Server) autopilotFinished(info proto.PaneInfo) {
	if s.autopilot == nil || info.ProjectID == "" || info.Branch == "" || strings.HasPrefix(info.Name, "review · ") {
		return
	}
	if cmd := s.autopilot.policy().Checks[info.ProjectID]; cmd != "" {
		go s.autopilotStartCheck(info.ProjectID, info.Branch, cmd)
		return
	}
	go s.autopilotShip(info.ProjectID, info.Branch)
}

// autopilotStartCheck runs the project's check in the branch's worktree,
// unless one is running there already. The last one's terminal is
// closed: with nobody watching they would pile up, one a finish.
func (s *Server) autopilotStartCheck(projectID, branch, cmd string) {
	p, perr := s.projects.get(projectID)
	if perr != nil {
		return
	}
	wt, ok := p.branchWorktree(branch)
	if !ok || wt.Path == "" {
		return
	}
	key := branchKey(projectID, branch)
	a := s.autopilot
	a.mu.Lock()
	var old []string
	for pane, k := range a.saved.CheckPanes {
		if k != key {
			continue
		}
		if e, perr := s.get(pane); perr == nil && s.alive(e) && e.info().State == proto.PaneRunning {
			a.mu.Unlock()
			return // one is already going
		}
		old = append(old, pane)
		delete(a.saved.CheckPanes, pane)
	}
	if prev := a.lastCheck[key]; prev != "" {
		old = append(old, prev)
	}
	a.mu.Unlock()
	for _, pane := range old {
		s.close(pane)
	}
	info, perr := s.create(proto.PaneCreateParams{Name: checkPrefix + branch, Command: []string{"/bin/sh", "-lc", cmd},
		Cwd: wt.Path, Cols: 120, Rows: 40})
	if perr != nil {
		s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Error: true,
			Text: fmt.Sprintf("could not run the check on %s: %s", branch, perr.Message)})
		return
	}
	a.mu.Lock()
	a.saved.CheckPanes[info.ID] = key
	a.lastCheck[key] = info.ID
	a.saveLocked()
	a.mu.Unlock()
	// A check quicker than the line above has ended already, unseen.
	if e, perr := s.get(info.ID); perr == nil {
		select {
		case <-e.p.Done():
			s.autopilotExited(e.info())
		default:
		}
	}
}

// autopilotExited is a pane that ended: if it was a check the server
// started, its result is read — the output first, before a client that
// sees the pane end can close it.
func (s *Server) autopilotExited(info proto.PaneInfo) {
	if s.autopilot == nil {
		return
	}
	a := s.autopilot
	a.mu.Lock()
	key, ok := a.saved.CheckPanes[info.ID]
	if ok {
		delete(a.saved.CheckPanes, info.ID)
		a.saveLocked()
	}
	cmd := a.saved.Policy.Checks[strings.SplitN(key, "\x00", 2)[0]]
	a.mu.Unlock()
	if !ok {
		return
	}
	var output []string
	if e, perr := s.get(info.ID); perr == nil {
		output = e.p.PlainLines()
	}
	projectID, branch, _ := strings.Cut(key, "\x00")
	p, perr := s.projects.get(projectID)
	if perr != nil {
		return
	}
	if info.ExitCode == 0 {
		s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Text: "check passed on " + branch})
	} else {
		s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Error: true,
			Text: fmt.Sprintf("check failed on %s (exit %d) — its terminal has the output", branch, info.ExitCode)})
	}
	s.autopilotCheckEnded(p, proto.AutopilotCheck{ProjectID: projectID, Branch: branch, Pane: info.ID, Command: cmd, Exit: info.ExitCode}, output)
}

// autopilotShip pushes a finished branch and opens its pull request,
// with auto_pr, or says once what keeps it back.
func (s *Server) autopilotShip(projectID, branch string) {
	a := s.autopilot
	if a == nil {
		return
	}
	key := branchKey(projectID, branch)
	a.mu.Lock()
	_, rewritten := a.saved.Rewrites[key]
	if !a.saved.Policy.AutoPR && !rewritten {
		a.mu.Unlock()
		return
	}
	if a.shipping[key] {
		a.mu.Unlock()
		return
	}
	a.shipping[key] = true
	a.mu.Unlock()
	why := s.ship(projectID, branch)
	a.mu.Lock()
	delete(a.shipping, key)
	was := a.unshipped[key]
	if why == "" {
		delete(a.unshipped, key)
	} else {
		a.unshipped[key] = why
	}
	a.mu.Unlock()
	if why != "" && why != was {
		s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Text: branch + " is finished but not pushed: " + why})
	}
}

// ship is autopilotShip's work: "" when the branch went out, or had
// nothing to send, and otherwise what is in the way.
func (s *Server) ship(projectID, branch string) string {
	p, perr := s.projects.get(projectID)
	if perr != nil {
		return ""
	}
	// What the agent just did, not what the last tick saw.
	s.projects.refresh(p, "")
	info := p.snapshot()
	var b *proto.BranchInfo
	for i := range info.Branches {
		if info.Branches[i].Name == branch {
			b = &info.Branches[i]
		}
	}
	var wt *proto.WorktreeInfo
	for i := range info.Worktrees {
		if info.Worktrees[i].Branch == branch {
			wt = &info.Worktrees[i]
		}
	}
	if b == nil || wt == nil || branch == info.Base || info.Base == "" {
		return ""
	}
	if agent := s.branchAgent(projectID, branch); agent != nil &&
		(agent.Agent.State == proto.AgentWorking || agent.Agent.State == proto.AgentBlocked) {
		return "" // not finished after all; it ships when it is
	}
	key := branchKey(projectID, branch)
	a := s.autopilot
	pol := a.policy()
	if s.midway(wt.Path) {
		return "a rebase or merge is under way there; once it is finished it goes out"
	}
	if st := wt.Status; st != nil && st.Staged+st.Unstaged+st.Untracked > 0 {
		return s.askToCommit(projectID, branch, wt.Head)
	}
	if pol.Checks[projectID] != "" {
		a.mu.Lock()
		passed := a.saved.Passed[key]
		a.mu.Unlock()
		if passed == "" || passed != wt.Head {
			return "" // its check runs when its agent finishes, and this is called again once it passes
		}
	}
	a.mu.Lock()
	rw, rewritten := a.saved.Rewrites[key]
	a.mu.Unlock()
	if rewritten {
		if b.PR == nil || b.PR.State != "OPEN" {
			a.mu.Lock()
			delete(a.saved.Rewrites, key) // nothing open to replace it under
			a.saveLocked()
			a.mu.Unlock()
			return ""
		}
		return s.shipRewrite(p, branch, wt.Path, rw, b.PR.Number)
	}
	if !pol.AutoPR {
		return ""
	}
	if b.BaseAhead == 0 {
		return "" // nothing of its own to send
	}
	if info.PRStatus != "" {
		return "pull requests can't be read here (" + info.PRStatus + ")"
	}
	switch pr := b.PR; {
	case pr != nil && pr.State == "OPEN":
		if b.Upstream == "" || b.Ahead > 0 {
			if _, perr := s.projects.pushBranch(proto.BranchPushParams{ProjectID: projectID, Branch: branch}); perr != nil {
				return "pushing it failed: " + perr.Message
			}
			s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch,
				Text: fmt.Sprintf("pushed %s to #%d", branch, pr.Number)})
		}
		return s.markReady(p, projectID, branch, wt.Path, info.Base, wt.Head, *pr)
	case pr != nil && pr.State == "CLOSED":
		return fmt.Sprintf("its pull request #%d was closed, so conch opens no other; open one yourself if it should be", pr.Number)
	default:
		unmet := s.gates(projectID, branch, wt.Path, info.Base, wt.Head)
		res, perr := s.projects.openPR(proto.BranchPRParams{ProjectID: projectID, Branch: branch, Draft: len(unmet) > 0})
		if perr != nil {
			return "opening its pull request failed: " + perr.Message
		}
		text := "opened " + res.URL + " for " + branch
		if len(unmet) > 0 {
			a.mu.Lock()
			a.saved.Drafted[key] = true
			a.saveLocked()
			a.mu.Unlock()
			text += " as a draft, since " + strings.Join(unmet, " and ") + "; it is marked ready once that changes"
		}
		s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Text: text})
	}
	return ""
}

// markReady takes a pull request conch opened as a draft out of draft
// once the gates that held it are met. One somebody made a draft is
// theirs, and left alone.
func (s *Server) markReady(p *project, projectID, branch, dir, base, head string, pr proto.PRInfo) string {
	key := branchKey(projectID, branch)
	a := s.autopilot
	a.mu.Lock()
	drafted := a.saved.Drafted[key]
	a.mu.Unlock()
	if !drafted {
		return ""
	}
	if !pr.Draft {
		a.mu.Lock()
		delete(a.saved.Drafted, key) // somebody marked it ready
		a.saveLocked()
		a.mu.Unlock()
		return ""
	}
	if unmet := s.gates(projectID, branch, dir, base, head); len(unmet) > 0 {
		return fmt.Sprintf("#%d stays a draft, since %s", pr.Number, strings.Join(unmet, " and "))
	}
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	if err := ghx.Ready(ctx, p.root, pr.Number); err != nil {
		return fmt.Sprintf("#%d could not be marked ready: %v", pr.Number, err)
	}
	a.mu.Lock()
	delete(a.saved.Drafted, key)
	a.saveLocked()
	a.mu.Unlock()
	s.projects.requestPRs(p)
	s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch,
		Text: fmt.Sprintf("marked #%d ready: what held it as a draft is met", pr.Number)})
	return ""
}

// gates says which of require_tests and max_diff a branch does not meet,
// and asks its agent for a test, once a commit, when it lacks one.
func (s *Server) gates(projectID, branch, dir, base, head string) []string {
	pol := s.autopilot.policy()
	if !pol.RequireTests && pol.MaxDiff <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	var unmet []string
	if pol.RequireTests {
		files, err := gitx.Changed(ctx, dir, base)
		if err == nil && needsTest(files) {
			unmet = append(unmet, "it changes code and no test")
			s.askForTest(projectID, branch, head)
		}
	}
	if pol.MaxDiff > 0 {
		if n, err := gitx.DiffSize(ctx, dir, base); err == nil && n > pol.MaxDiff {
			unmet = append(unmet, fmt.Sprintf("it changes %d lines, over max_diff's %d — split it if it can be", n, pol.MaxDiff))
		}
	}
	return unmet
}

// testPrompt is what an agent whose branch changes code and no test is
// told.
const testPrompt = "This branch changes code but no test. Add a test that fails without your change and passes with it, " +
	"run it, and commit it. If the change truly cannot be tested, say why in a commit message instead. " +
	"Do not push: conch pushes it, and marks the pull request ready once it has a test."

// askForTest asks the branch's agent for a test, once for each commit.
func (s *Server) askForTest(projectID, branch, head string) {
	key := branchKey(projectID, branch)
	a := s.autopilot
	agent := s.freeAgent(projectID, branch)
	if agent == nil {
		return
	}
	a.mu.Lock()
	asked := a.saved.TestAsked[key] == head
	if !asked {
		a.saved.TestAsked[key] = head
		a.saveLocked()
	}
	a.mu.Unlock()
	if asked {
		return
	}
	if _, perr := s.promptAgent(proto.AgentPromptParams{ID: agent.ID, Text: testPrompt}); perr != nil {
		s.autopilotNote(proto.AutopilotNote{ProjectID: projectID, Branch: branch, Error: true,
			Text: "asking " + agent.DisplayName() + " for a test on " + branch + " failed: " + perr.Message})
	}
}

// needsTest says whether a set of changed files is code with no test
// beside it. Documentation, configuration and data need none.
func needsTest(files []string) bool {
	code := false
	for _, f := range files {
		if isTestFile(f) {
			return false
		}
		if sourceExts[strings.ToLower(filepath.Ext(f))] {
			code = true
		}
	}
	return code
}

// sourceExts are the files that are code.
var sourceExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".cjs": true,
	".rs": true, ".java": true, ".kt": true, ".rb": true, ".c": true, ".cc": true, ".cpp": true, ".h": true, ".hpp": true,
	".swift": true, ".cs": true, ".php": true, ".scala": true, ".m": true, ".mm": true, ".ex": true, ".exs": true,
	".dart": true, ".lua": true, ".sh": true, ".vue": true, ".svelte": true,
}

// isTestFile says whether path is a test by the conventions of the
// common languages: a test directory, or a test's own name.
func isTestFile(path string) bool {
	for _, dir := range strings.Split(filepath.ToSlash(filepath.Dir(path)), "/") {
		switch dir {
		case "test", "tests", "__tests__", "spec", "specs", "testdata", "e2e":
			return true
		}
	}
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	switch {
	case strings.HasSuffix(name, "_test"), strings.HasSuffix(name, "_spec"),
		strings.HasPrefix(name, "test_") && ext == ".py",
		strings.Contains(base, ".test."), strings.Contains(base, ".spec."),
		strings.HasSuffix(name, "Test") || strings.HasSuffix(name, "Tests"):
		return true
	}
	return false
}

// commitPrompt is what an agent that left work uncommitted is told.
const commitPrompt = "You finished with uncommitted changes on this branch. Commit the ones that belong to this work, " +
	"with a message that says what changed and why; remove or ignore anything that does not belong. " +
	"Do not push: conch pushes the branch and opens its pull request once it is committed and its check passes."

// askToCommit asks the branch's agent, once for each commit it is at, to
// commit what it left — the pull request is of commits, and picking which
// files belong is the agent's call, not conch's.
func (s *Server) askToCommit(projectID, branch, head string) string {
	key := branchKey(projectID, branch)
	a := s.autopilot
	agent := s.freeAgent(projectID, branch)
	a.mu.Lock()
	asked := a.saved.Asked[key] == head
	if agent != nil && !asked {
		a.saved.Asked[key] = head
		a.saveLocked()
	}
	a.mu.Unlock()
	switch {
	case agent == nil:
		return "it has uncommitted changes and no agent to commit them"
	case asked:
		return "it has uncommitted changes; commit them and it goes out"
	}
	if _, perr := s.promptAgent(proto.AgentPromptParams{ID: agent.ID, Text: commitPrompt}); perr != nil {
		return "it has uncommitted changes, and asking " + agent.DisplayName() + " to commit them failed: " + perr.Message
	}
	return "it has uncommitted changes; asked " + agent.DisplayName() + " to commit them"
}

// midway says whether a rebase, merge or the like is under way in dir —
// whose conflicted files are not work left uncommitted.
func (s *Server) midway(dir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	return gitx.InProgress(ctx, dir) != ""
}

// shipNote is added to a task's first message while the server ships
// branches itself (auto_pr), so the agent works the way that needs from
// the start rather than learn it from a refusal: tested, committed with
// messages that say why, and never pushed by hand — a push of its own
// would race conch's, and a force push could drop what the base or
// somebody else added.
func (s *Server) shipNote(projectID string) string {
	if s.autopilot == nil {
		return ""
	}
	pol := s.autopilot.policy()
	if !pol.AutoPR {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n(How this branch is shipped: when you finish, conch ")
	if pol.Checks[projectID] != "" {
		fmt.Fprintf(&b, "runs `%s` here and, once it passes, ", pol.Checks[projectID])
	}
	b.WriteString("pushes the branch and opens its pull request. So: write a test that fails without the change first, then the change; ")
	b.WriteString("commit your work in small commits whose messages say what changed and why; leave nothing that belongs to it uncommitted; ")
	b.WriteString("and do not push, force-push, rebase onto the base or merge — conch does those, and tells you if CI fails or the base conflicts.)")
	return b.String()
}
