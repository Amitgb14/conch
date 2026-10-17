package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

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

// failureTail bounds what is sent: enough of the end of the output to show
// the error, never a megabyte of a log.
const (
	failureTailLines = 80
	failureTailBytes = 6000
)

// failureTail is the end of a check's output: blank lines at the bottom
// dropped, escape codes stripped, at most failureTailLines lines and
// failureTailBytes bytes, cut at a line.
func failureTail(lines []string) string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > failureTailLines {
		lines = lines[len(lines)-failureTailLines:]
	}
	var kept []string
	size := 0
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimRight(ansi.Strip(lines[i]), " ")
		if size+len(l)+1 > failureTailBytes {
			if len(kept) == 0 { // one enormous line: keep its end
				l = strings.ToValidUTF8(l[len(l)-failureTailBytes+1:], "")
				kept = append(kept, l)
			}
			break
		}
		size += len(l) + 1
		kept = append(kept, l)
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return strings.Join(kept, "\n")
}

// failurePrompt is what the agent is told: which command, how it ended,
// its output, and what is wanted — a fix proved by running it again.
func failurePrompt(what, command string, exit int, output string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s failed on this branch", what)
	if command != "" {
		fmt.Fprintf(&b, " (`%s`", command)
		if exit != 0 {
			fmt.Fprintf(&b, ", exit %d", exit)
		}
		b.WriteString(")")
	}
	b.WriteString(".")
	if output != "" {
		b.WriteString(" The end of its output:\n\n```\n" + output + "\n```\n\n")
	} else {
		b.WriteString(" It printed nothing.\n\n")
	}
	b.WriteString("Find the cause and fix it, then run it again to confirm it passes. If the failure is not in this branch's work, say so instead of changing unrelated code.")
	return b.String()
}

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
		return failurePrompt("The project's check", command, exit, failureTail(out.Lines)), nil
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
		what := "CI"
		if log.Workflow != "" {
			what = "CI (" + log.Workflow + ")"
		}
		text := failurePrompt(what, "", 0, failureTail(strings.Split(log.Log, "\n")))
		if log.URL != "" {
			text += "\n\nThe run: " + log.URL
		}
		return text, nil
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

// autoMergeCI merges a branch when its pull request's checks turn green,
// with [verify] auto_merge. It is the other half of autoFixCI: the same
// poll, the same transition, the opposite colour. Everything it needs is
// in the pull request conch already reads every minute.
//
// What it will not do, and why each one:
//
//   - A draft is not finished, whatever CI says.
//   - CHANGES_REQUESTED is a person asking for something, and green
//     checks are not an answer to it.
//   - A project's own check failing while CI passes is a disagreement,
//     not a go-ahead (verifyFailed).
//   - A branch whose agent is working or waiting may be mid-push.
//   - Only on the turn to green, so a poll that says "still passing"
//     merges nothing, and a branch merged by hand is not merged twice.
func (m *Model) autoMergeCI(machine string, was, now proto.ProjectInfo) tea.Cmd {
	if !m.cfg.Verify.AutoMerge {
		return nil
	}
	before := make(map[string]string, len(was.Branches))
	for _, b := range was.Branches {
		before[b.Name] = prChecks(b)
	}
	var cmds []tea.Cmd
	for _, b := range now.Branches {
		if prChecks(b) != "pass" || before[b.Name] == "pass" {
			continue
		}
		cmds = append(cmds, m.autoMergeBranch(machine, now, b))
	}
	return tea.Batch(cmds...)
}

// autoMergeBranch is autoMergeCI's decision for one branch that has just
// gone green. It says why it is holding back rather than doing it
// quietly: a merge that does not happen is as surprising as one that
// does.
func (m *Model) autoMergeBranch(machine string, proj proto.ProjectInfo, b proto.BranchInfo) tea.Cmd {
	hold := func(why string) tea.Cmd {
		m.setFlash(fmt.Sprintf("%s is green but not merged: %s", b.Name, why), false)
		return nil
	}
	switch {
	case b.PR.Draft:
		return hold("its pull request is a draft")
	case b.PR.Review == "CHANGES_REQUESTED":
		return hold(fmt.Sprintf("somebody asked for changes on #%d", b.PR.Number))
	case b.Name == proj.Base:
		return nil // the base does not merge into itself
	}
	if state, _ := m.verifyOf(machine, proj.ID, b.Name); state == verifyFailed {
		return hold("this project's own check failed, which CI passing does not answer")
	}
	// An unresolved thread on the pull request is somebody waiting for an
	// answer. A review that formally asked for changes is caught above;
	// this catches the commoner case of a reviewer who left notes and no
	// verdict. conch's own comments on the diff are not counted: those
	// are the person's working notes, and a stale one would hold a branch
	// for ever.
	if n := openThreads(m.machine(machine), proj.ID, b.Name); n > 0 {
		return hold(fmt.Sprintf("%s unresolved on #%d", count(n, "review thread"), b.PR.Number))
	}
	if agent := m.branchAgent(machine, proj.ID, b.Name); agent != nil &&
		(agent.Agent.State == proto.AgentWorking || agent.Agent.State == proto.AgentBlocked) {
		return hold(agent.DisplayName() + " is still at it")
	}
	t := harvestTarget{machine: machine, projectID: proj.ID, branch: b.Name}
	var res proto.CommitResult
	params := proto.BranchMergeParams{ProjectID: proj.ID, Branch: b.Name, Squash: true}
	m.setFlash(fmt.Sprintf("#%d is green: merging %s…", b.PR.Number, b.Name), false)
	return m.harvestCall(t, proto.MethodBranchMerge, params, &res, func() harvestDoneMsg {
		return harvestDoneMsg{text: fmt.Sprintf("merged %s into %s (%s), green on #%d", b.Name, res.Into, shortHash(res.Hash), b.PR.Number)}
	})
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
