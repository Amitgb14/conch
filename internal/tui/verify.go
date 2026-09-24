package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// A verify run answers "does this branch stand up?" with the project's own
// command — `go test ./...`, `npm test`, whatever it is — run in that
// branch's worktree. The queue then says what happened instead of only that
// an agent stopped typing.
//
// The run is a pane, as the compare view's is: its exit code is the result,
// its output stays readable, and nothing new has to survive a reload. There
// is no default command, and nothing runs until a project has one: a
// project's command can take minutes, touch a database or cost money, so
// conch never picks one on your behalf.

// verifyRun is one branch's run. The verdict is kept here rather than read
// back from the pane: a pane that exits on its own is closed moments later,
// so by the time anything asks, the exit code would be gone.
type verifyRun struct {
	pane string // the pane it runs in, while it runs
	err  string // why it could not start
	done bool
	exit int
	// sig is what the branch looked like when the check started: its last
	// commit, what it is ahead by, and what is uncommitted in its worktree.
	// A verdict is about that state, so when it moves the verdict is stale.
	sig string
}

func verifyKey(machine, projectID, branch string) string {
	return machine + "|" + projectID + "|" + branch
}

// verifyState is what a run came to, read from its pane.
type verifyState int

const (
	verifyNone    verifyState = iota // no command, or it has not run here
	verifyRunning                    // still going
	verifyPassed
	verifyFailed
	verifyBroken // the run could not start, or its pane is gone
	verifyStale  // it passed or failed, but the branch has changed since
)

// verifyOf reports what the last run on a branch came to.
func (m Model) verifyOf(machine, projectID, branch string) (verifyState, string) {
	run, ok := m.verifyRuns[verifyKey(machine, projectID, branch)]
	stale := ok && run.done && run.sig != "" && run.sig != m.branchSig(machine, projectID, branch)
	switch {
	case !ok:
		return verifyNone, ""
	case run.err != "":
		return verifyBroken, run.err
	case stale && run.exit == 0:
		return verifyStale, "check out of date"
	case stale:
		return verifyStale, fmt.Sprintf("check failed (exit %d), and out of date", run.exit)
	case run.done && run.exit == 0:
		return verifyPassed, "check passed"
	case run.done:
		return verifyFailed, fmt.Sprintf("check failed (exit %d)", run.exit)
	case run.pane == "":
		return verifyRunning, "checking…" // starting: its terminal isn't up yet
	}
	// A pane conch hasn't heard of yet is one whose pane.created is still
	// in flight, not a lost one; a terminal that is really gone is caught
	// when it closes.
	return verifyRunning, "checking…"
}

// verifyResettle is how long a branch has to stop changing before a check
// that went out of date is run again: an agent writes in bursts, and running
// the command on every keystroke would be worse than not running it at all.
const verifyResettle = 30 * time.Second

// sigSeen is a branch's signature and when it last changed.
type sigSeen struct {
	sig string
	at  time.Time
}

// recheckSettled runs a project's check again on branches whose verdict went
// out of date and have since been quiet for verifyResettle — a commit, or an
// agent that stopped writing. Branches with an agent still working are left
// alone: it is not finished with them.
func (m *Model) recheckSettled(now time.Time) tea.Cmd {
	if m.branchSigs == nil {
		m.branchSigs = map[string]sigSeen{}
	}
	var cmds []tea.Cmd
	for _, mach := range m.machines {
		if mach.state != stateOnline {
			continue
		}
		for _, proj := range mach.projects {
			if m.verifyCommand(proj.ID) == "" {
				continue
			}
			for _, b := range proj.Branches {
				key := verifyKey(mach.id, proj.ID, b.Name)
				sig := m.branchSig(mach.id, proj.ID, b.Name)
				was, seen := m.branchSigs[key]
				if !seen || was.sig != sig {
					m.branchSigs[key] = sigSeen{sig: sig, at: now}
					continue // it just moved; let it settle
				}
				if state, _ := m.verifyOf(mach.id, proj.ID, b.Name); state != verifyStale {
					continue
				}
				if now.Sub(was.at) < verifyResettle || m.agentWorkingOn(mach.id, proj.ID, b.Name) {
					continue
				}
				if cmd := m.startVerify(mach.id, proj.ID, b.Name); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
	}
	return tea.Batch(cmds...)
}

// agentWorkingOn reports whether an agent is still writing on a branch.
func (m Model) agentWorkingOn(machine, projectID, branch string) bool {
	mach := m.machine(machine)
	if mach == nil {
		return false
	}
	for _, p := range mach.panes {
		if p.Agent != nil && p.ProjectID == projectID && p.Branch == branch &&
			p.Agent.State == proto.AgentWorking {
			return true
		}
	}
	return false
}

// branchSig is what a check's verdict is about: the branch's last commit,
// how far ahead it is, and what is uncommitted in its worktree. An agent
// committing, or anyone editing a file, moves it.
func (m Model) branchSig(machine, projectID, branch string) string {
	proj := m.project(machine, projectID)
	if proj == nil {
		return ""
	}
	sig := ""
	for _, b := range proj.Branches {
		if b.Name == branch {
			sig = fmt.Sprintf("%d|%d|%d", b.Committed.Unix(), b.Ahead, b.BaseAhead)
			break
		}
	}
	for _, w := range proj.Worktrees {
		if w.Branch != branch {
			continue
		}
		if st := w.Status; st != nil { // a worktree whose status has not been read yet
			sig += fmt.Sprintf("|%d|%d|%d|%d", st.Files, st.Added, st.Deleted, st.Untracked)
		}
		break
	}
	return sig
}

// verifyPane is the terminal a branch's last check ran in, while it is
// still around to read.
func (m Model) verifyPane(machine, projectID, branch string) string {
	run, ok := m.verifyRuns[verifyKey(machine, projectID, branch)]
	if !ok || run.pane == "" || m.pane(machine, run.pane) == nil {
		return ""
	}
	return run.pane
}

// verifyCommand is the command configured for a project, if any.
func (m Model) verifyCommand(projectID string) string {
	return strings.TrimSpace(m.cfg.Verify.Commands[projectID])
}

// startVerify runs a project's command in a branch's worktree. A previous
// run's terminal is left alone so its output can still be read.
func (m *Model) startVerify(machine, projectID, branch string) tea.Cmd {
	cmd := m.verifyCommand(projectID)
	if cmd == "" {
		return nil
	}
	proj := m.project(machine, projectID)
	if proj == nil {
		return nil
	}
	dir := ""
	for _, wt := range proj.Worktrees {
		if wt.Branch == branch {
			dir = wt.Path
		}
	}
	c := m.clientOf(machine)
	if dir == "" || c == nil {
		return nil // nothing checked out to run in, or the machine is away
	}
	if m.verifyRuns == nil {
		m.verifyRuns = map[string]verifyRun{}
	}
	key := verifyKey(machine, projectID, branch)
	if run, ok := m.verifyRuns[key]; ok && run.pane != "" {
		if p := m.pane(machine, run.pane); p != nil && p.State != proto.PaneExited {
			return nil // one is already going
		}
	}
	m.verifyRuns[key] = verifyRun{sig: m.branchSig(machine, projectID, branch)}
	params := proto.PaneCreateParams{Name: "check · " + branch, Command: []string{"/bin/sh", "-lc", cmd},
		Cwd: dir, Cols: 120, Rows: 40}
	return func() tea.Msg {
		var info proto.PaneInfo
		if err := callCtx(c, proto.MethodPaneCreate, params, &info); err != nil {
			return verifyStartedMsg{machine: machine, projectID: projectID, branch: branch, err: err.Error()}
		}
		return verifyStartedMsg{machine: machine, projectID: projectID, branch: branch, info: info}
	}
}

// verifyStartedMsg carries the terminal a run started in.
type verifyStartedMsg struct {
	machine, projectID, branch string
	info                       proto.PaneInfo
	err                        string
}

func (m *Model) receiveVerifyStarted(msg verifyStartedMsg) {
	if m.verifyRuns == nil {
		m.verifyRuns = map[string]verifyRun{}
	}
	key := verifyKey(msg.machine, msg.projectID, msg.branch)
	sig := m.verifyRuns[key].sig
	if msg.err != "" {
		m.verifyRuns[key] = verifyRun{err: msg.err, sig: sig}
		return
	}
	m.verifyRuns[key] = verifyRun{pane: msg.info.ID, sig: sig}
}

// agentDone reports whether a pane's agent has finished and nobody has
// looked since.
func agentDone(p proto.PaneInfo) bool {
	return p.Agent != nil && p.Agent.State == proto.AgentDone
}

// verifyExited records what a check came to, as its pane exits, and says
// so. It reports whether the pane was a check, since a check's terminal is
// kept rather than closed: it is the only place its output lives.
func (m *Model) verifyExited(machine string, info proto.PaneInfo) bool {
	for key, run := range m.verifyRuns {
		if run.pane != info.ID || !strings.HasPrefix(key, machine+"|") {
			continue
		}
		run.done, run.exit = true, info.ExitCode
		m.verifyRuns[key] = run
		branch := key[strings.LastIndex(key, "|")+1:]
		if info.ExitCode == 0 {
			m.setFlash("check passed on "+branch, false)
		} else {
			m.setFlash(fmt.Sprintf("check failed on %s (exit %d) — its terminal has the output", branch, info.ExitCode), true)
		}
		return true
	}
	return false
}

// verifyClosed marks a check whose terminal was closed before it finished:
// conch keeps a check's terminal, so closing one is a person doing it.
func (m *Model) verifyClosed(machine, paneID string) {
	for key, run := range m.verifyRuns {
		if run.pane == paneID && !run.done && strings.HasPrefix(key, machine+"|") {
			run.err = "its terminal was closed before it finished"
			m.verifyRuns[key] = run
			return
		}
	}
}

// verifyOnDone runs the check when an agent finishes on a branch. Only when
// the project has a command: without one this does nothing at all.
func (m *Model) verifyOnDone(machine string, p proto.PaneInfo) tea.Cmd {
	if p.Agent == nil || p.Agent.State != proto.AgentDone || p.Branch == "" || p.ProjectID == "" {
		return nil
	}
	return m.startVerify(machine, p.ProjectID, p.Branch)
}

// askVerifyCommand asks for the project's command, and runs it when given.
func (m *Model) askVerifyCommand(machine, projectID, branch string) tea.Cmd {
	name := m.projectName(machine, projectID)
	d := newDialog(*m, " Check command for "+name+" ",
		[]string{"Run in a branch's worktree when its agent finishes, so the review queue can say whether the work stands up. " +
			"It runs in its own terminal, which stays open for the output. Nothing runs until you name a command."},
		[]string{"Command"}, []string{m.verifyCommand(projectID)})
	d.fields[0].in.Placeholder = "go test ./... · npm test · make check"
	d.submit = func(m *Model, values []string) tea.Cmd {
		cmd := strings.TrimSpace(values[0])
		m.overlay = nil
		if m.cfg.Verify.Commands == nil {
			m.cfg.Verify.Commands = map[string]string{}
		}
		if cmd == "" {
			delete(m.cfg.Verify.Commands, projectID)
			m.setFlash("no check for "+name+"; its branches show as unchecked", false)
			return saveConfig(m.cfg)
		}
		m.cfg.Verify.Commands[projectID] = cmd
		return tea.Batch(saveConfig(m.cfg), m.startVerify(machine, projectID, branch))
	}
	m.overlay = d
	return d.focusCmd()
}
