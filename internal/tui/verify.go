package tui

import (
	"fmt"
	"strings"

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
)

// verifyOf reports what the last run on a branch came to.
func (m Model) verifyOf(machine, projectID, branch string) (verifyState, string) {
	run, ok := m.verifyRuns[verifyKey(machine, projectID, branch)]
	switch {
	case !ok:
		return verifyNone, ""
	case run.err != "":
		return verifyBroken, run.err
	case run.done && run.exit == 0:
		return verifyPassed, "checked"
	case run.done:
		return verifyFailed, fmt.Sprintf("check failed (exit %d)", run.exit)
	case run.pane == "":
		return verifyRunning, "checking…" // starting: its terminal isn't up yet
	case m.pane(machine, run.pane) == nil:
		return verifyBroken, "its terminal went away"
	}
	return verifyRunning, "checking…"
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
	m.verifyRuns[key] = verifyRun{}
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
	if msg.err != "" {
		m.verifyRuns[key] = verifyRun{err: msg.err}
		return
	}
	m.verifyRuns[key] = verifyRun{pane: msg.info.ID}
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
