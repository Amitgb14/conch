package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// gitInitProject asks, then makes a project that is a plain folder a git
// repository (project.git_init): git init and an empty first commit, the
// files already there left uncommitted. Its project.updated then turns the
// row into a repository's, where t makes a branch and a worktree.
func (m *Model) gitInitProject(mid, pid string) tea.Cmd {
	proj := m.project(mid, pid)
	switch {
	case proj == nil:
		return nil
	case proj.Git:
		m.setFlash(proj.Name+" is already a git repository", true)
		return nil
	case !m.hasCapability(mid, proto.CapProjectGitInit):
		m.setFlash("the server there predates making a project a git repository; reload it (conch server reload)", true)
		return nil
	}
	name := proj.Name
	m.overlay = newConfirm("Make "+name+" a git repository? git init and an empty first commit; its files stay uncommitted.", func(m *Model) tea.Cmd {
		var info proto.ProjectInfo
		return m.callOn(mid, proto.MethodProjectGitInit, proto.ProjectRef{ID: pid}, &info, func() tea.Msg {
			return flashMsg(name + " is a git repository now: t makes a task on a branch of its own")
		})
	})
	return nil
}
