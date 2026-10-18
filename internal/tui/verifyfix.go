package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// A failed check is a message for the agent, not only for you: f in the
// review queue hands the end of the check's output to the branch's agent
// and asks it to fix the cause, so "done" can come to mean the check
// passes rather than that an agent stopped typing. [verify] auto_fix does
// the same without asking, a set number of times per branch: a cap is the
// difference between an agent fixing a test and an agent spending the
// night, and the person's plan, on one it cannot fix.

// branchAgent is the agent working on a branch: the one not waiting on
// you if there are several, the first otherwise.
func (m Model) branchAgent(machine, projectID, branch string) *proto.PaneInfo {
	mach := m.machine(machine)
	if mach == nil {
		return nil
	}
	var found *proto.PaneInfo
	for i := range mach.panes {
		p := &mach.panes[i]
		if p.Agent == nil || p.State != proto.PaneRunning || p.ProjectID != projectID || p.Branch != branch || isReviewer(*p) {
			continue
		}
		if found == nil || (found.Agent.State == proto.AgentBlocked && p.Agent.State != proto.AgentBlocked) {
			found = p
		}
	}
	return found
}

// fixCheck sends a failed check's output to the branch's agent, or starts
// the default agent in the worktree with it when none is there.
func (m *Model) fixCheck(machine, projectID, branch string) tea.Cmd {
	key := verifyKey(machine, projectID, branch)
	run, ok := m.verifyRuns[key]
	if state, _ := m.verifyOf(machine, projectID, branch); !ok || state != verifyFailed {
		m.setFlash("only a failed check can be sent to an agent; v runs the check", true)
		return nil
	}
	if m.clientOf(machine) != nil && m.pane(machine, run.pane) == nil {
		m.setFlash("that check's terminal has been closed, so its output is gone — v runs it again", true)
		return nil
	}
	command, exit, checkPane := m.verifyCommand(projectID), run.exit, run.pane
	return m.sendToBranchAgent(machine, projectID, branch, "the check's failure", func(c *client.Client) (string, error) {
		var out proto.PaneReadResult
		if err := callCtx(c, proto.MethodPaneRead, proto.PaneRef{ID: checkPane}, &out); err != nil {
			return "", fmt.Errorf("reading the check's output: %w", err)
		}
		return proto.FailurePrompt("The project's check", command, exit, proto.FailureTail(out.Lines)), nil
	})
}

// fixCI sends what a branch's failed CI run printed to its agent, read
// through gh on the machine the branch is on.
func (m *Model) fixCI(machine, projectID, branch string) tea.Cmd {
	if !m.hasCapability(machine, proto.CapBranchCILog) {
		m.setFlash("the server there predates reading CI logs; reload it (conch server reload)", true)
		return nil
	}
	return m.sendToBranchAgent(machine, projectID, branch, "CI's failure", func(c *client.Client) (string, error) {
		var log proto.BranchCILog
		if err := callCtx(c, proto.MethodBranchCILog, proto.BranchCILogParams{ProjectID: projectID, Branch: branch}, &log); err != nil {
			return "", err
		}
		return proto.CIFailurePrompt(log), nil
	})
}

// sendToBranchAgent hands a message to the branch's agent — or starts the
// default agent in its worktree with it, when there is none — once text
// has fetched what to say. what names it in the status bar.
func (m *Model) sendToBranchAgent(machine, projectID, branch, what string, text func(*client.Client) (string, error)) tea.Cmd {
	c := m.clientOf(machine)
	if c == nil {
		m.setFlash(m.offlineText(machine), true)
		return nil
	}
	agent := m.branchAgent(machine, projectID, branch)
	switch {
	case agent != nil && agent.Agent.State == proto.AgentBlocked:
		m.setFlash(agent.DisplayName()+" is waiting for an answer of its own; answer it first", true)
		return nil
	case agent != nil && !m.hasCapability(machine, proto.CapAgentPrompt):
		m.setFlash("the server there predates prompting an agent; reload it", true)
		return nil
	}
	dir := ""
	if proj := m.project(machine, projectID); proj != nil {
		for _, wt := range proj.Worktrees {
			if wt.Branch == branch {
				dir = wt.Path
			}
		}
	}
	if agent == nil && dir == "" {
		m.setFlash("no agent on "+branch+" and no worktree to start one in", true)
		return nil
	}
	starting := m.defaultAgent()
	if agent != nil {
		m.setFlash("sending "+what+" to "+agent.DisplayName()+"…", false)
	} else {
		m.setFlash("starting "+agentLabel(starting)+" on "+branch+" with "+what+"…", false)
	}
	cols, rows := m.paneArea()
	return func() tea.Msg {
		prompt, err := text(c)
		if err != nil {
			return errMsg{err}
		}
		if agent != nil {
			var res proto.AgentPromptResult
			err := callCtx(c, proto.MethodAgentPrompt, proto.AgentPromptParams{ID: agent.ID, Text: prompt}, &res)
			return promptSentMsg{machine: machine, pane: agent.ID, res: res, err: err}
		}
		var info proto.PaneInfo
		params := proto.PaneCreateParams{Agent: starting, Prompt: prompt, Cwd: dir, Cols: cols, Rows: rows}
		if err := callCtx(c, proto.MethodPaneCreate, params, &info); err != nil {
			return errMsg{err}
		}
		return flashMsg("started " + agentLabel(starting) + " on " + branch + " with " + what)
	}
}

// autoFix sends a check that just failed to its branch's agent, when
// [verify] auto_fix allows another try on that branch and the agent is
// free to take it: never one waiting on you, never one still working, and
// never by starting an agent nobody asked for. A pass resets the count.
func (m *Model) autoFix(machine string, info proto.PaneInfo) tea.Cmd {
	limit := m.cfg.Verify.AutoFix
	for key, run := range m.verifyRuns {
		if run.pane != info.ID || !strings.HasPrefix(key, machine+"|") || !run.done {
			continue
		}
		if run.server {
			return nil // the server read it, and acted
		}
		if m.autopilotOn(machine) {
			return m.reportCheck(machine, key, run)
		}
		if m.verifyFixes == nil {
			m.verifyFixes = map[string]int{}
		}
		if run.exit == 0 {
			delete(m.verifyFixes, key)
			return nil
		}
		parts := strings.SplitN(key, "|", 3)
		if limit <= 0 || len(parts) != 3 {
			return nil
		}
		projectID, branch := parts[1], parts[2]
		if m.verifyFixes[key] >= limit {
			m.setFlash(fmt.Sprintf("check failed on %s again; auto_fix tried %d times, so it's yours now", branch, limit), true)
			return nil
		}
		agent := m.branchAgent(machine, projectID, branch)
		if agent == nil || agent.Agent.State == proto.AgentBlocked || agent.Agent.State == proto.AgentWorking {
			return nil
		}
		m.verifyFixes[key]++
		return m.fixCheck(machine, projectID, branch)
	}
	return nil
}

// prChecks is a branch's CI as conch compares it: the checks of an open
// pull request, and "" for a branch with none. A closed or merged pull
// request is not somebody's move any more, so its checks are not either.
func prChecks(b proto.BranchInfo) string {
	if b.PR == nil || b.PR.State != "OPEN" {
		return ""
	}
	return b.PR.Checks
}

// autoFixCI sends a pull request's failed CI to its branch's agent when
// the checks have just turned red, on the same terms as a check of
// conch's own (autoFix): [verify] auto_fix allows another try on that
// branch, the agent is free to take it — never one waiting on you, never
// one still working — and no agent is started for it. A pass clears the
// count, so a branch that goes green and red again gets the whole
// allowance rather than none, and the two kinds of failure share one
// count because "a check failed, give it to the agent" is one idea.
//
// Only a change conch *watched* counts. Connecting to a machine with
// four red pull requests on it would otherwise prompt four agents about
// work they may have finished with days ago; those sit in the review
// queue, where f sends them when you say so.
func (m *Model) autoFixCI(machine string, was, now proto.ProjectInfo) tea.Cmd {
	if m.autopilotOn(machine) {
		return nil // the server sends it (autopilot.go)
	}
	before := make(map[string]string, len(was.Branches))
	for _, b := range was.Branches {
		before[b.Name] = prChecks(b)
	}
	var cmds []tea.Cmd
	for _, b := range now.Branches {
		key := machine + "|" + now.ID + "|" + b.Name
		switch checks := prChecks(b); {
		case checks == "pass":
			delete(m.verifyFixes, key)
		case checks != "fail" || before[b.Name] == "fail":
		default:
			cmds = append(cmds, m.autoFixCIBranch(machine, now.ID, b, key))
		}
	}
	return tea.Batch(cmds...)
}

// autoFixCIBranch is autoFixCI's decision for one branch whose checks
// have just failed.
func (m *Model) autoFixCIBranch(machine, projectID string, b proto.BranchInfo, key string) tea.Cmd {
	limit := m.cfg.Verify.AutoFix
	if limit <= 0 {
		return nil
	}
	if m.verifyFixes[key] >= limit {
		m.setFlash(fmt.Sprintf("CI failed on %s again; auto_fix tried %d times, so it's yours now", b.Name, limit), true)
		return nil
	}
	agent := m.branchAgent(machine, projectID, b.Name)
	if agent == nil || agent.Agent.State == proto.AgentBlocked || agent.Agent.State == proto.AgentWorking {
		return nil
	}
	if m.verifyFixes == nil {
		m.verifyFixes = map[string]int{}
	}
	m.verifyFixes[key]++
	return m.fixCI(machine, projectID, b.Name)
}

// autoMergeCI merges a branch's pull request on GitHub when its checks
// turn green, with [verify] auto_merge. It is the other half of
// autoFixCI: the same poll, the same transition, the opposite colour.
// Everything it needs is in the pull request conch already reads every
// minute.
//
// The merge is GitHub's, not a local one: merging into a checkout left
// the pull request open, and once somebody merged it on GitHub the base
// there and the base here had diverged.
//
// What it will not do, and why each one:
//
//   - A draft is not finished, whatever CI says.
//   - CHANGES_REQUESTED is a person asking for something, and green
//     checks are not an answer to it.
//   - A pull request that conflicts with its base cannot be merged as it
//     stands; somebody has to rebase it.
//   - A project's own check failing while CI passes is a disagreement,
//     not a go-ahead (verifyFailed).
//   - A branch whose agent is working or waiting may be mid-push.
//   - Only a turn to green conch watched, so connecting to a machine
//     with old green pull requests merges none of them.
//
// A branch held back for one of those still wants merging: it waits in
// m.autoMerges, and goes as soon as nothing holds it — its agent
// finishing, a thread resolved, the conflict rebased away — rather than
// never, which is what holding back used to mean.
func (m *Model) autoMergeCI(machine string, was, now proto.ProjectInfo) tea.Cmd {
	if !m.cfg.Verify.AutoMerge || m.autopilotOn(machine) {
		return nil
	}
	before := make(map[string]string, len(was.Branches))
	for _, b := range was.Branches {
		before[b.Name] = prChecks(b)
	}
	if m.autoMerges == nil {
		m.autoMerges = map[string]*autoMergeWait{}
	}
	green := map[string]bool{}
	for _, b := range now.Branches {
		key := machine + "|" + now.ID + "|" + b.Name
		if prChecks(b) != "pass" {
			continue
		}
		green[key] = true
		if _, waiting := m.autoMerges[key]; !waiting && before[b.Name] != "pass" {
			m.autoMerges[key] = &autoMergeWait{}
		}
	}
	// Red again, pending again, merged, closed or gone: nothing to wait
	// for. A merge in flight stays until it answers.
	prefix := machine + "|" + now.ID + "|"
	for key, w := range m.autoMerges {
		if strings.HasPrefix(key, prefix) && !green[key] && !w.merging {
			delete(m.autoMerges, key)
		}
	}
	return m.autoMergeProject(machine, now)
}

// autoMergeWait is a green pull request waiting to merge: what last held
// it back, so the reason is said once rather than at every poll, and
// whether its merge is under way.
type autoMergeWait struct {
	held    string
	merging bool
}

// autoMergeDoneMsg is a pull request merge that answered.
type autoMergeDoneMsg struct {
	key    string
	target harvestTarget
	number int
	method string
	err    error
}

// autoMergeRetry looks again at every pull request waiting to merge on
// machine, after something that can lift a hold: an agent's state, a
// check's result, a review thread.
func (m *Model) autoMergeRetry(machine string) tea.Cmd {
	if len(m.autoMerges) == 0 || !m.cfg.Verify.AutoMerge || m.autopilotOn(machine) {
		return nil
	}
	mach := m.machine(machine)
	if mach == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, proj := range mach.projects {
		cmds = append(cmds, m.autoMergeProject(machine, proj))
	}
	return tea.Batch(cmds...)
}

// autoMergeProject merges every pull request of proj that is waiting and
// no longer held back.
func (m *Model) autoMergeProject(machine string, proj proto.ProjectInfo) tea.Cmd {
	var cmds []tea.Cmd
	for _, b := range proj.Branches {
		key := machine + "|" + proj.ID + "|" + b.Name
		w, waiting := m.autoMerges[key]
		if !waiting || w.merging || prChecks(b) != "pass" {
			continue
		}
		if why := m.autoMergeHold(machine, proj, b); why != "" {
			if why != w.held {
				w.held = why
				m.setFlash(fmt.Sprintf("%s is green but not merged: %s", b.Name, why), false)
			}
			continue
		}
		if b.Name == proj.Base {
			delete(m.autoMerges, key) // the base does not merge into itself
			continue
		}
		w.merging = true
		cmds = append(cmds, m.autoMergePR(key, machine, proj.ID, b))
	}
	return tea.Batch(cmds...)
}

// autoMergeHold says what keeps a green pull request from merging, or ""
// when nothing does. It says why rather than holding back quietly: a
// merge that does not happen is as surprising as one that does.
func (m *Model) autoMergeHold(machine string, proj proto.ProjectInfo, b proto.BranchInfo) string {
	switch {
	case b.PR.Draft:
		return "its pull request is a draft"
	case b.PR.Review == "CHANGES_REQUESTED":
		return fmt.Sprintf("somebody asked for changes on #%d", b.PR.Number)
	case b.PR.Conflicting:
		base := proj.Base
		if base == "" {
			base = "its base"
		}
		return fmt.Sprintf("#%d conflicts with %s; rebase it and it merges once it is green again", b.PR.Number, base)
	case !m.hasCapability(machine, proto.CapPRMerge):
		return "conch there predates merging pull requests; update it (u in the version box) and it merges"
	}
	if state, _ := m.verifyOf(machine, proj.ID, b.Name); state == verifyFailed {
		return "this project's own check failed, which CI passing does not answer"
	}
	// An unresolved thread on the pull request is somebody waiting for an
	// answer. A review that formally asked for changes is caught above;
	// this catches the commoner case of a reviewer who left notes and no
	// verdict. conch's own comments on the diff are not counted: those
	// are the person's working notes, and a stale one would hold a branch
	// for ever.
	if n := openThreads(m.machine(machine), proj.ID, b.Name); n > 0 {
		return fmt.Sprintf("%s unresolved on #%d", count(n, "review thread"), b.PR.Number)
	}
	if agent := m.branchAgent(machine, proj.ID, b.Name); agent != nil &&
		(agent.Agent.State == proto.AgentWorking || agent.Agent.State == proto.AgentBlocked) {
		return agent.DisplayName() + " is still at it"
	}
	return ""
}

// autoMergePR merges b's pull request on GitHub, at the commit whose
// checks passed.
func (m *Model) autoMergePR(key, machine, projectID string, b proto.BranchInfo) tea.Cmd {
	t := harvestTarget{machine: machine, projectID: projectID, branch: b.Name}
	number := b.PR.Number
	params := proto.BranchPRMergeParams{ProjectID: projectID, Branch: b.Name, Number: number, Head: b.PR.Head}
	m.setFlash(fmt.Sprintf("#%d is green: merging it on GitHub…", number), false)
	c := m.clientOf(machine)
	return func() tea.Msg {
		msg := autoMergeDoneMsg{key: key, target: t, number: number}
		if c == nil {
			msg.err = errString(m.offlineText(machine))
			return msg
		}
		ctx, cancel := context.WithTimeout(context.Background(), harvestTimeout)
		defer cancel()
		var res proto.BranchPRMergeResult
		msg.err = c.Call(ctx, proto.MethodBranchPRMerge, params, &res)
		msg.method = res.Method
		return msg
	}
}

// autoMergeDone reports a merge. One GitHub refused is not tried again
// until the pull request turns green again — after a push, which is what
// it takes to change GitHub's mind — so a refusal is said once, not every
// minute.
func (m *Model) autoMergeDone(msg autoMergeDoneMsg) tea.Cmd {
	delete(m.autoMerges, msg.key)
	if msg.err != nil {
		m.setFlash(fmt.Sprintf("#%d is green but GitHub would not merge it: %v", msg.number, msg.err), true)
		return nil
	}
	return m.receiveHarvest(harvestDoneMsg{target: msg.target,
		text: fmt.Sprintf("merged #%d on GitHub (%s), green", msg.number, msg.method)})
}

// openThreads is how many of a branch's pull request comments are still
// unresolved. conch reads GitHub's unresolved threads only (review.go),
// so every comment it holds from there is one.
func openThreads(mach *machine, projectID, branch string) int {
	if mach == nil {
		return 0
	}
	n := 0
	for _, c := range mach.reviewsOn(projectID, branch) {
		if c.Source == proto.ReviewGitHub {
			n++
		}
	}
	return n
}
