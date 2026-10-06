package tui

import (
	"os"
	"path/filepath"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/proto"
)

// Where conch was started says where you mean to work. Run in a project —
// its checkout or one of its worktrees — the tree opens on that project, so
// c starts an agent there; run in a git repository that isn't one yet, conch
// offers to add it, once. Without this an agent started from a repository
// went under CLI, in the home folder, which nobody launching conch in a
// repository expected.

// launchProjectMsg is the project added from the launch folder's offer.
type launchProjectMsg struct{ info proto.ProjectInfo }

// LaunchedIn records the folder conch was started in, for the first list of
// the local machine's projects to place the tree by. The TUI's own tests
// never call it: they run inside conch's repository. A TUI restarted onto
// a new build keeps where you were instead.
func (m Model) LaunchedIn(dir string) Model {
	if os.Getenv(updateRemotesEnv) != "" {
		return m
	}
	m.launchDir = resolvePath(dir)
	return m
}

// resolvePath is dir with its symlinks resolved, so /tmp and /private/tmp
// on macOS are one folder; dir as it is when that fails.
func resolvePath(dir string) string {
	if dir == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return filepath.Clean(dir)
}

// launchProject is the project dir belongs to: the one whose checkout or
// worktree holds it most closely, so a worktree inside another project's
// folder counts as its own.
func launchProject(dir string, projects []proto.ProjectInfo) (proto.ProjectInfo, bool) {
	var best proto.ProjectInfo
	bestLen := -1
	for _, p := range projects {
		roots := []string{p.Path}
		for _, w := range p.Worktrees {
			roots = append(roots, w.Path)
		}
		for _, r := range roots {
			if r == "" {
				continue // within would take it for the root of everything
			}
			if r = resolvePath(r); within(dir, r) && len(r) > bestLen {
				best, bestLen = p, len(r)
			}
		}
	}
	return best, bestLen >= 0
}

// repoRoot is the top of the git checkout holding dir — the nearest folder
// with a .git, a directory or, in a worktree, a file — or "" outside one.
func repoRoot(dir string) string {
	for d := dir; d != ""; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
	return ""
}

// placeLaunch runs once, on the local machine's first list of projects:
// it selects the project conch was started in, or offers to add the
// repository it was started in.
func (m *Model) placeLaunch(projects []proto.ProjectInfo) {
	dir := m.launchDir
	m.launchDir = ""
	if dir == "" {
		return
	}
	if p, ok := launchProject(dir, projects); ok {
		m.selectProject(localMachine, p.ID)
		return
	}
	root := repoRoot(dir)
	home, _ := os.UserHomeDir()
	// A home folder kept in git (dotfiles) is not a project anybody meant.
	if root == "" || root == resolvePath(home) || m.overlay != nil || slices.Contains(m.noProjectOffer, root) {
		return
	}
	m.overlay = newProjectOffer(*m, root)
}

// newProjectOffer asks to add the repository conch was started in. No is
// remembered for that folder; esc only puts the question off to next time.
func newProjectOffer(m Model, root string) *dialog {
	d := newConfirm("Add "+m.tildify(localMachine, root)+" as a project? Agents you start there work in it. (No: don't ask again for this folder.)",
		func(m *Model) tea.Cmd {
			var info proto.ProjectInfo
			return m.callOn(localMachine, proto.MethodProjectAdd, proto.ProjectAddParams{Path: root}, &info,
				func() tea.Msg { return launchProjectMsg{info} })
		})
	d.title = " Add project "
	d.decline = func(m *Model) tea.Cmd {
		if !slices.Contains(m.noProjectOffer, root) {
			m.noProjectOffer = append(m.noProjectOffer, root)
		}
		return m.saveState()
	}
	return d
}

// receiveLaunchProject selects the project just added from the offer; the
// server's event for it may not have arrived yet.
func (m *Model) receiveLaunchProject(info proto.ProjectInfo) tea.Cmd {
	if mach := m.machine(localMachine); mach != nil && !slices.ContainsFunc(mach.projects, func(p proto.ProjectInfo) bool { return p.ID == info.ID }) {
		mach.projects = append(mach.projects, info)
	}
	m.selectProject(localMachine, info.ID)
	m.setFlash("added project "+info.Name, false)
	return tea.Batch(m.rebuild(), m.saveState())
}

// selectProject opens the tree down to a project and puts the cursor on it;
// the next rebuild shows it.
func (m *Model) selectProject(mid, pid string) {
	m.expanded[machineID(mid)] = true
	m.expanded[workspaceID(mid)] = true
	m.cursor = projectNodeID(mid, pid)
	m.focus = focusSidebar
}
