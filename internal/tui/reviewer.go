package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

// A second opinion, by default. With [verify] reviewer naming an agent,
// conch starts that agent on a branch whose agent has finished — once its
// check passes, where the project has one — to read the diff and report,
// not edit. Its report shows in the review queue, and R hands it to the
// agent that wrote the code. Another vendor's model reading the work with
// a fresh context catches what the writer's own context hides from it,
// which is the pattern people describe running by hand.
//
// It costs plan usage, so it runs only when named, and once per state of
// the branch: a reviewer finishing, or the writer finishing again with
// nothing changed, starts nothing.

// reviewPrefix names a reviewer's pane, which is how conch knows it again
// after a restart: it is not the branch's writer, so failures and reviews
// are never sent to it, and its finishing starts no check and no review.
const reviewPrefix = "review · "

func isReviewer(p proto.PaneInfo) bool { return strings.HasPrefix(p.Name, reviewPrefix) }

// reviewerPrompt is what the reviewer is asked.
func reviewerPrompt(branch, base string) string {
	if base == "" {
		base = "the base branch"
	}
	return fmt.Sprintf("You are reviewing another agent's work on the branch %s, against %s. Do not edit, create or delete any files, and do not commit.\n\n"+
		"Read the changes: `git diff %s...HEAD` for what is committed, and `git status` and `git diff` for what is not.\n\n"+
		"Report what is wrong, most serious first: bugs, edge cases not handled, missing tests, and anything that contradicts what the change is for. "+
		"Give each finding a file and line and say why it matters. Leave out style preferences. If the work is sound, say so in one line.\n\n"+
		"Make your final message the whole report: it is handed to the agent that wrote the code.", branch, base, base)
}

// branchReviewer is the reviewer on a branch, if one is running.
func (m Model) branchReviewer(machine, projectID, branch string) *proto.PaneInfo {
	mach := m.machine(machine)
	if mach == nil {
		return nil
	}
	for i := range mach.panes {
		p := &mach.panes[i]
		if isReviewer(*p) && p.State == proto.PaneRunning && p.ProjectID == projectID && p.Branch == branch {
			return p
		}
	}
	return nil
}

// startReview starts the configured reviewer on a branch, when there is
// one to start and the branch has changed since it was last reviewed.
func (m *Model) startReview(machine, projectID, branch string) tea.Cmd {
	reviewer := strings.TrimSpace(m.cfg.Verify.Reviewer)
	if reviewer == "" || branch == "" {
		return nil
	}
	proj := m.project(machine, projectID)
	if proj == nil || branch == proj.Base {
		return nil
	}
	dir := ""
	for _, wt := range proj.Worktrees {
		if wt.Branch == branch {
			dir = wt.Path
		}
	}
	mach := m.machine(machine)
	c := m.clientOf(machine)
	if dir == "" || c == nil || mach == nil {
		return nil
	}
	key := verifyKey(machine, projectID, branch)
	sig := m.branchSig(machine, projectID, branch)
	if m.reviewSigs[key] == sig || m.branchReviewer(machine, projectID, branch) != nil {
		return nil // reviewed as it stands, or being reviewed now
	}
	if mach.missingAgent(reviewer) {
		m.setFlash(agentLabel(reviewer)+" is not installed on "+mach.label+", so "+branch+" was not reviewed ([verify] reviewer)", true)
		return nil
	}
	if m.reviewSigs == nil {
		m.reviewSigs = map[string]string{}
	}
	m.reviewSigs[key] = sig
	params := proto.PaneCreateParams{Agent: reviewer, Name: reviewPrefix + branch, Prompt: reviewerPrompt(branch, proj.Base),
		Cwd: dir, Cols: 120, Rows: 40}
	m.setFlash("asking "+agentLabel(reviewer)+" to review "+branch+"…", false)
	return func() tea.Msg {
		var info proto.PaneInfo
		if err := callCtx(c, proto.MethodPaneCreate, params, &info); err != nil {
			return errMsg{fmt.Errorf("starting the reviewer: %w", err)}
		}
		return nil
	}
}

// sendReviewToWriter hands the reviewer's report on a branch to the agent
// that wrote the code there.
func (m *Model) sendReviewToWriter(machine, projectID, branch string) tea.Cmd {
	rv := m.branchReviewer(machine, projectID, branch)
	switch {
	case rv == nil:
		m.setFlash("no reviewer on "+branch+" ([verify] reviewer names one)", true)
		return nil
	case rv.Agent == nil || rv.Agent.State == proto.AgentWorking:
		m.setFlash("the reviewer on "+branch+" is still reading", true)
		return nil
	case !m.hasCapability(machine, proto.CapAgentReply):
		m.setFlash("the server there predates reading an agent's answer; reload it (conch server reload)", true)
		return nil
	}
	reviewerPane, reviewerName := rv.ID, agentLabel(rv.Agent.Name)
	return m.sendToBranchAgent(machine, projectID, branch, reviewerName+"'s review", func(c *client.Client) (string, error) {
		var reply proto.AgentReply
		if err := callCtx(c, proto.MethodAgentReply, proto.PaneRef{ID: reviewerPane}, &reply); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s reviewed your changes on %s:\n\n%s\n\nAddress each finding that holds up. If you disagree with one, say why rather than changing the code.",
			reviewerName, branch, reply.Text), nil
	})
}
