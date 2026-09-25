package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// Finishing a branch's work from the TUI: commit, push, open a pull request,
// merge into the base, or discard the branch and its worktree. The server
// does the git work; these build the dialogs and report the outcome.

const (
	harvestCapability = "branch.harvest.v1"
	hunksCapability   = "branch.hunks.v1"
	// harvestTimeout covers pushes and gh, which reach the network; the
	// server gives up after two minutes.
	harvestTimeout = 150 * time.Second
)

// harvestTarget is the branch a harvest action works on.
type harvestTarget struct {
	machine, projectID, branch string
}

// harvestDoneMsg reports a finished harvest action.
type harvestDoneMsg struct {
	target    harvestTarget
	text      string
	committed bool // clears the files marked for the commit
}

// pushRejectedMsg is a push the remote is ahead of, with the server's own
// words for it.
type pushRejectedMsg struct {
	target harvestTarget
	why    string
}

// rebaseConflictMsg is a rebase that stopped on conflicts and was undone.
type rebaseConflictMsg struct {
	target harvestTarget
	why    string
}

// discardPlanMsg carries what discarding a branch would remove and lose.
type discardPlanMsg struct {
	target harvestTarget
	plan   proto.BranchDiscardResult
}

// harvestProject checks that a harvest action can run on t and returns its
// project, or flashes why not. base says whether the base branch itself is
// acceptable.
func (m *Model) harvestProject(t harvestTarget, base bool) *proto.ProjectInfo {
	proj := m.project(t.machine, t.projectID)
	switch {
	case proj == nil || !proj.Git || t.branch == "":
		m.setFlash("select a branch of a git project", true)
	case m.clientOf(t.machine) == nil:
		m.setFlash(m.offlineText(t.machine), true)
	case !m.hasCapability(t.machine, harvestCapability):
		m.setFlash("the server there predates committing and merging from conch; reload it", true)
	case !base && t.branch == proj.Base:
		m.setFlash(t.branch+" is the base branch", true)
	default:
		return proj
	}
	return nil
}

// harvestCall calls a branch method with room for the network, reporting
// done's text when it succeeds.
func (m Model) harvestCall(t harvestTarget, method string, params, out any, done func() harvestDoneMsg) tea.Cmd {
	c := m.clientOf(t.machine)
	if c == nil {
		return func() tea.Msg { return errMsg{errString(m.offlineText(t.machine))} }
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), harvestTimeout)
		defer cancel()
		if err := c.Call(ctx, method, params, out); err != nil {
			return errMsg{err}
		}
		msg := done()
		msg.target = t
		return msg
	}
}

func (m *Model) receiveHarvest(msg harvestDoneMsg) tea.Cmd {
	m.setFlash(msg.text, false)
	var cmds []tea.Cmd
	for _, t := range m.openTabs() {
		for _, l := range t.root.leaves() {
			cv := l.changes
			if cv == nil || cv.machine != msg.target.machine || cv.projectID != msg.target.projectID || cv.branch != msg.target.branch {
				continue
			}
			if msg.committed {
				cv.clearMarks()
			}
			cmds = append(cmds, cv.poll(m))
		}
	}
	return tea.Batch(cmds...)
}

// openCommit asks for a message and commits what sel holds: every change,
// the marked files, or the marked hunks.
func (m *Model) openCommit(t harvestTarget, sel commitSelection) tea.Cmd {
	proj := m.harvestProject(t, true)
	if proj == nil {
		return nil
	}
	where := ""
	for _, wt := range proj.Worktrees {
		if wt.Branch == t.branch {
			where = " in " + m.tildify(t.machine, wt.Path)
		}
	}
	if where == "" {
		m.setFlash(t.branch+" is not checked out, so it has nothing to commit", true)
		return nil
	}
	if sel.patch != "" && !m.hasCapability(t.machine, hunksCapability) {
		m.setFlash("the server there predates committing single hunks; mark whole files instead", true)
		return nil
	}
	text := "Commits every change" + where + ", untracked files included."
	switch {
	case sel.patch != "" && len(sel.files) > 0:
		text = fmt.Sprintf("Commits the %s marked in %s, and %s%s: %s. Anything already staged is committed too.",
			counted(sel.hunks, "hunk"), counted(sel.inned, "file"), counted(len(sel.files), "whole file"), where, listSome(sel.files, 3))
	case sel.patch != "":
		text = fmt.Sprintf("Commits the %s marked in %s%s. The rest of the changes, and the files themselves, stay as they are.",
			counted(sel.hunks, "hunk"), counted(sel.inned, "file"), where)
	case len(sel.files) > 0:
		text = fmt.Sprintf("Commits the %s marked%s: %s. Other changes stay as they are.",
			counted(len(sel.files), "file"), where, listSome(sel.files, 4))
	}
	d := newDialog(*m, " Commit · "+t.branch+" ", []string{text}, []string{"Message"}, nil)
	d.fields[0].in.Placeholder = "what the change does"
	d.submit = func(m *Model, v []string) tea.Cmd {
		msg := strings.TrimSpace(v[0])
		if msg == "" {
			return func() tea.Msg { return errMsg{errString("a commit needs a message")} }
		}
		var res proto.CommitResult
		params := proto.BranchCommitParams{ProjectID: t.projectID, Branch: t.branch, Message: msg,
			Files: sel.files, Patch: sel.patch}
		m.setFlash("committing…", false)
		return m.harvestCall(t, proto.MethodBranchCommit, params, &res, func() harvestDoneMsg {
			return harvestDoneMsg{text: "committed " + shortHash(res.Hash) + " on " + t.branch, committed: true}
		})
	}
	m.overlay = d
	return d.focusCmd()
}

// pushBranch pushes the branch, setting its upstream the first time. A
// remote that has moved on refuses the push; rebase then offers to take its
// commits, which is what a person would do by hand.
func (m *Model) pushBranch(t harvestTarget) tea.Cmd { return m.push(t, false) }

func (m *Model) push(t harvestTarget, rebase bool) tea.Cmd {
	if m.harvestProject(t, true) == nil {
		return nil
	}
	if rebase && !m.hasCapability(t.machine, proto.CapBranchRebase) {
		m.setFlash("the server there predates rebasing onto the remote; pull --rebase in the worktree, then push", true)
		return nil
	}
	if rebase {
		m.setFlash("taking what the remote has on "+t.branch+", then pushing…", false)
	} else {
		m.setFlash("pushing "+t.branch+"…", false)
	}
	c := m.clientOf(t.machine)
	if c == nil {
		return func() tea.Msg { return errMsg{errString(m.offlineText(t.machine))} }
	}
	params := proto.BranchPushParams{ProjectID: t.projectID, Branch: t.branch, Rebase: rebase}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), harvestTimeout)
		defer cancel()
		var res proto.BranchPushResult
		if err := c.Call(ctx, proto.MethodBranchPush, params, &res); err != nil {
			var perr *proto.Error
			if errors.As(err, &perr) {
				switch perr.Code {
				case proto.ErrPushRejected:
					return pushRejectedMsg{target: t, why: perr.Message}
				case proto.ErrRebaseConflict:
					return rebaseConflictMsg{target: t, why: perr.Message}
				}
			}
			return errMsg{err}
		}
		text := "pushed " + t.branch
		if res.Took > 0 {
			text = fmt.Sprintf("pushed %s after taking %s from the remote", t.branch, counted(res.Took, "commit"))
		}
		return harvestDoneMsg{target: t, text: text}
	}
}

// receiveRebaseConflict says what stopped, and what is left to do: sorting
// a conflict out is work in the worktree, and the commands for it are the
// same ones whatever brought it about.
func (m *Model) receiveRebaseConflict(msg rebaseConflictMsg) tea.Cmd {
	t := msg.target
	m.setFlash(msg.why, true)
	text := []string{msg.why, ""}
	if path := m.worktreeOf(t); path != "" {
		where := m.tildify(t.machine, path)
		if t.machine != localMachine {
			where += " on " + m.machineLabel(t.machine)
		}
		text = append(text, "Sort it out in "+where+" (n opens a terminal there):")
	} else {
		text = append(text, "Sort it out in the branch's worktree:")
	}
	return m.notice(" "+ansi.Truncate(t.branch, 30, "…")+" ", append(text,
		"  git pull --rebase",
		"  fix the files it names, then git rebase --continue",
		"  P here, or git push, once it is done"))
}

// notice opens a notice and asks for nothing.
func (m *Model) notice(title string, text []string) tea.Cmd {
	m.overlay = newNotice(title, text)
	return nil
}

// worktreeOf is where a branch is checked out on its machine, or "".
func (m Model) worktreeOf(t harvestTarget) string {
	proj := m.project(t.machine, t.projectID)
	if proj == nil {
		return ""
	}
	for _, wt := range proj.Worktrees {
		if wt.Branch == t.branch {
			return wt.Path
		}
	}
	return ""
}

// receivePushRejected offers what mends a rejected push: taking the
// remote's commits and putting this branch's own on top. Nothing is
// rebased until the answer is yes.
func (m *Model) receivePushRejected(msg pushRejectedMsg) tea.Cmd {
	t := msg.target
	if !m.hasCapability(t.machine, proto.CapBranchRebase) {
		m.setFlash(msg.why+"; pull --rebase in its worktree, then push", true)
		return nil
	}
	m.overlay = newConfirm(
		msg.why+". Take them, put "+t.branch+"'s own commits on top and push? Nothing is changed if the rebase conflicts.",
		func(m *Model) tea.Cmd { return m.push(t, true) })
	return nil
}

// openPullRequest asks for a title and opens a pull request into the base.
func (m *Model) openPullRequest(t harvestTarget) tea.Cmd {
	proj := m.harvestProject(t, false)
	if proj == nil {
		return nil
	}
	if pr := m.branchPR(t.machine, t.projectID, t.branch); pr != nil && pr.State == "OPEN" {
		m.setFlash(fmt.Sprintf("pull request #%d is already open; o opens it", pr.Number), true)
		return nil
	}
	d := newDialog(*m, " Pull request · "+t.branch+" → "+proj.Base+" ",
		[]string{"Pushes " + t.branch + ", then opens a pull request into " + proj.Base + " with gh. Leave the title empty to take the title and description from the commits."},
		[]string{"Title", "Description"}, nil)
	d.fields[0].in.Placeholder = "from the commits"
	d.addCheck("Draft", "open it as a draft", false)
	d.submit = func(m *Model, v []string) tea.Cmd {
		var res proto.BranchPRResult
		params := proto.BranchPRParams{ProjectID: t.projectID, Branch: t.branch,
			Title: strings.TrimSpace(v[0]), Body: strings.TrimSpace(v[1]), Draft: v[2] == "on"}
		m.setFlash("pushing "+t.branch+" and opening a pull request…", false)
		return m.harvestCall(t, proto.MethodBranchPR, params, &res, func() harvestDoneMsg {
			return harvestDoneMsg{text: "opened " + res.URL}
		})
	}
	m.overlay = d
	return d.focusCmd()
}

// openMerge asks how to merge the branch into the base.
func (m *Model) openMerge(t harvestTarget) tea.Cmd {
	proj := m.harvestProject(t, false)
	if proj == nil {
		return nil
	}
	into := ""
	for _, wt := range proj.Worktrees {
		if wt.Branch == proj.Base {
			into = m.tildify(t.machine, wt.Path)
		}
	}
	if into == "" {
		m.setFlash(proj.Base+" isn't checked out anywhere; check it out to merge into it", true)
		return nil
	}
	what := t.branch
	for _, b := range proj.Branches {
		if b.Name != t.branch {
			continue
		}
		if b.BaseAhead == 0 {
			m.setFlash(t.branch+" has no commits that "+proj.Base+" lacks", true)
			return nil
		}
		what = "the " + counted(b.BaseAhead, "commit") + " of " + t.branch
	}
	d := newDialog(*m, " Merge · "+t.branch+" → "+proj.Base+" ",
		[]string{"Merges " + what + " into " + proj.Base + " in " + into + ". If it conflicts, nothing is changed. " + t.branch + " needs its changes committed first."},
		[]string{"Message"}, nil)
	d.fields[0].in.Placeholder = "git's default"
	d.addCheck("Squash", "one new commit on "+proj.Base+" instead of a merge commit", true)
	d.submit = func(m *Model, v []string) tea.Cmd {
		var res proto.CommitResult
		params := proto.BranchMergeParams{ProjectID: t.projectID, Branch: t.branch,
			Message: strings.TrimSpace(v[0]), Squash: v[1] == "on"}
		m.setFlash("merging "+t.branch+"…", false)
		return m.harvestCall(t, proto.MethodBranchMerge, params, &res, func() harvestDoneMsg {
			return harvestDoneMsg{text: "merged " + t.branch + " into " + res.Into + " (" + shortHash(res.Hash) + ")"}
		})
	}
	m.overlay = d
	return d.focusCmd()
}

// discardBranch asks the server what discarding would lose, then confirms.
func (m *Model) discardBranch(t harvestTarget) tea.Cmd {
	if m.harvestProject(t, false) == nil {
		return nil
	}
	var plan proto.BranchDiscardResult
	params := proto.BranchDiscardParams{ProjectID: t.projectID, Branch: t.branch, DryRun: true}
	c := m.clientOf(t.machine)
	return func() tea.Msg {
		if err := callCtx(c, proto.MethodBranchDiscard, params, &plan); err != nil {
			return errMsg{err}
		}
		return discardPlanMsg{target: t, plan: plan}
	}
}

// confirmDiscard shows what a discard removes and loses. Only y confirms,
// and force is asked for only when the plan showed a loss, so work made
// after the plan was read still makes the server refuse.
func (m *Model) confirmDiscard(msg discardPlanMsg) {
	t, plan := msg.target, msg.plan
	what := "Deletes branch " + t.branch + "."
	if plan.Worktree != "" {
		what = "Removes the worktree " + m.tildify(t.machine, plan.Worktree) + " and deletes branch " + t.branch + "."
	}
	force := len(plan.Uncommitted) > 0 || plan.Unmerged > 0
	loss := "Nothing is lost: its commits are merged or pushed."
	if force {
		var parts []string
		if n := len(plan.Uncommitted); n > 0 {
			parts = append(parts, counted(n, "uncommitted file")+" ("+listSome(plan.Uncommitted, 3)+")")
		}
		if plan.Unmerged > 0 {
			parts = append(parts, counted(plan.Unmerged, "commit")+" not merged or pushed")
		}
		loss = "This loses " + strings.Join(parts, " and ") + ", for good."
	}
	d := newConfirm(what, func(m *Model) tea.Cmd {
		var res proto.BranchDiscardResult
		params := proto.BranchDiscardParams{ProjectID: t.projectID, Branch: t.branch, Force: force}
		m.setFlash("discarding "+t.branch+"…", false)
		return m.harvestCall(t, proto.MethodBranchDiscard, params, &res, func() harvestDoneMsg {
			return harvestDoneMsg{text: "discarded " + t.branch}
		})
	})
	d.title = " Discard " + t.branch + " "
	d.text = append(d.text, loss)
	d.yesOnly = true
	m.overlay = d
}

// harvestMenuItems are the branch menu's entries for finishing its work.
func harvestMenuItems(m Model, r row) []menuItem {
	proj := m.project(r.machine, r.projectID)
	if proj == nil || !proj.Git || !m.hasCapability(r.machine, harvestCapability) {
		return nil
	}
	t := harvestTarget{machine: r.machine, projectID: r.projectID, branch: r.branch}
	var items []menuItem
	for _, wt := range proj.Worktrees {
		if wt.Branch == r.branch && !wt.Status.Clean() {
			items = append(items, menuItem{"C", "Commit all changes…", func(m *Model) tea.Cmd { return m.openCommit(t, commitSelection{}) }})
		}
	}
	items = append(items, menuItem{"P", "Push", func(m *Model) tea.Cmd { return m.pushBranch(t) }})
	if r.branch == proj.Base {
		return items
	}
	if pr := m.branchPR(r.machine, r.projectID, r.branch); pr == nil || pr.State != "OPEN" {
		items = append(items, menuItem{"p", "Open a pull request…", func(m *Model) tea.Cmd { return m.openPullRequest(t) }})
	}
	return append(items,
		menuItem{"M", "Merge into " + proj.Base + "…", func(m *Model) tea.Cmd { return m.openMerge(t) }},
		menuItem{"D", "Discard branch and worktree…", func(m *Model) tea.Cmd { return m.discardBranch(t) }},
	)
}

// counted is n what, pluralised: "1 file", "3 files".
func counted(n int, what string) string {
	return fmt.Sprintf("%d %s%s", n, what, plural(n))
}

// listSome joins up to n items, noting how many more there are.
func listSome(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(" and %d more", len(items)-n)
}

func shortHash(h string) string {
	return h[:min(len(h), 7)]
}

// showError says what went wrong. A reason the status bar has no room for —
// the files a merge or a rebase stopped on — opens a notice as well, so it
// can be read rather than guessed at from its first few words.
func (m *Model) showError(err error) {
	text := errText(err)
	m.setFlash(text, true)
	if len([]rune(text)) > m.flashRoom() {
		m.overlay = newNotice(" Failed ", []string{text}) // the dialog wraps it to its own width
	}
}
