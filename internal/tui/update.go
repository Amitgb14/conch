package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/update"
)

// updateState tracks newer builds: a rebuilt or downloaded binary on disk,
// a newer release, and machines behind this build.
type updateState struct {
	exe       string
	exeMod    time.Time
	diskBuild string // build of the executable on disk, when it changed

	release        *update.Release
	releaseChecked time.Time

	running bool
	step    string // what the update is doing
	// remotes: this TUI was restarted by an update and should bring
	// machines that connect behind this build up to date.
	remotes bool
}

const (
	updateCheckEvery = 5 * time.Second
	releaseCheckGap  = 24 * time.Hour
	updateRemotesEnv = "CONCH_UPDATE_REMOTES"
)

type (
	updateTickMsg    struct{}
	releaseCheckMsg  struct{ rel *update.Release }
	updateStepMsg    struct{ step string }
	updateDoneMsg    struct{ err error }
	restartTUIMsg    struct{}
	machineUpdateMsg struct {
		machine string
		err     error
	}
)

func newUpdateState() *updateState {
	u := &updateState{}
	u.exe, _ = update.Executable()
	if st, err := os.Stat(u.exe); err == nil {
		u.exeMod = st.ModTime()
		// Rebuilt between this process starting and now: already newer.
		if h := buildinfo.HashFile(u.exe); h != "" && h != buildinfo.Build() {
			u.diskBuild = h
		}
	}
	if os.Getenv(updateRemotesEnv) != "" {
		os.Unsetenv(updateRemotesEnv)
		u.remotes = true
	}
	return u
}

func updateTick() tea.Cmd {
	return tea.Tick(updateCheckEvery, func(time.Time) tea.Msg { return updateTickMsg{} })
}

// checkUpdates notices a changed executable (cheap: a stat, then a hash
// only when it changed) and, once a day, a newer release.
func (m *Model) checkUpdates() tea.Cmd {
	u := m.upd
	cmds := []tea.Cmd{updateTick()}
	if st, err := os.Stat(u.exe); err == nil && !st.ModTime().Equal(u.exeMod) {
		u.exeMod = st.ModTime()
		if h := buildinfo.HashFile(u.exe); h != "" && h != buildinfo.Build() {
			u.diskBuild = h
		} else {
			u.diskBuild = ""
		}
	}
	if m.cfg.Update.CheckReleases && time.Since(u.releaseChecked) > releaseCheckGap {
		u.releaseChecked = time.Now()
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			rel, _ := update.NewerRelease(ctx)
			return releaseCheckMsg{rel: rel}
		})
	}
	return tea.Batch(cmds...)
}

// updateItem is one thing an update would change.
type updateItem struct {
	label, detail string
}

// pendingUpdates lists what is out of date.
func (m Model) pendingUpdates() []updateItem {
	u := m.upd
	if u == nil {
		return nil
	}
	var items []updateItem
	if u.release != nil {
		items = append(items, updateItem{"Release", fmt.Sprintf("%s available (running %s)", u.release.Version, versionLabel())})
	}
	if u.diskBuild != "" {
		items = append(items, updateItem{"This TUI", "new build on disk " + u.diskBuild + " · restarts onto it"})
	}
	if len(m.machines) > 0 && m.serverBehindDisk() {
		items = append(items, updateItem{"Server", "runs build " + m.machines[0].server.Build + " · reloads, panes keep running"})
	}
	for _, mach := range m.machines[min(1, len(m.machines)):] {
		if mach.c == nil {
			continue
		}
		if same, ok := update.SameBuild(mach.server); ok && !same {
			items = append(items, updateItem{mach.label, "runs build " + firstNonEmpty(mach.server.BuildID, mach.server.Build) + " · installs and reloads"})
		}
	}
	return items
}

// targetBuild is the build the local server should run: the one on disk
// when it was rebuilt, else this TUI's.
func (m Model) targetBuild() string {
	if m.upd != nil && m.upd.diskBuild != "" {
		return m.upd.diskBuild
	}
	return buildinfo.Build()
}

func (m Model) serverBehindDisk() bool {
	s := m.machines[0]
	return s.c != nil && s.server.Build != "" && m.targetBuild() != "" && s.server.Build != m.targetBuild()
}

// startUpdate applies everything pending: a release download, the local
// server's reload, then a restart of this TUI when its binary changed (the
// new TUI updates remote machines); without a TUI restart, remotes now.
func (m *Model) startUpdate() tea.Cmd {
	u := m.upd
	if u.running {
		return nil
	}
	if len(m.pendingUpdates()) == 0 {
		m.setFlash("conch is up to date", false)
		return nil
	}
	u.running = true
	rel, exe := u.release, u.exe
	var local = m.machines[0]
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if rel != nil {
			if err := update.InstallRelease(ctx, rel.Version, exe); err != nil {
				return updateDoneMsg{err: fmt.Errorf("download %s: %w", rel.Version, err)}
			}
		}
		disk := buildinfo.HashFile(exe)
		if local.c != nil && local.server.Build != disk {
			if err := update.Reload(ctx, local.c, exe); err != nil {
				return updateDoneMsg{err: fmt.Errorf("reload the server: %w", err)}
			}
			time.Sleep(500 * time.Millisecond)
		}
		if disk != "" && disk != buildinfo.Build() {
			return restartTUIMsg{}
		}
		return updateStepMsg{step: "remotes"}
	}
}

// updateRemotes brings every connected machine behind this build up to date.
func (m *Model) updateRemotes() tea.Cmd {
	var cmds []tea.Cmd
	for _, mach := range m.machines[1:] {
		if mach.c == nil {
			continue
		}
		if same, ok := update.SameBuild(mach.server); ok && !same {
			cmds = append(cmds, m.updateMachine(mach.id))
		}
	}
	if len(cmds) == 0 {
		m.upd.running = false
		m.setFlash("conch is up to date · panes kept", false)
		return nil
	}
	return tea.Sequence(append(cmds, func() tea.Msg { return updateDoneMsg{} })...)
}

func (m *Model) updateMachine(mid string) tea.Cmd {
	mach := m.machine(mid)
	if mach == nil || mach.c == nil {
		return nil
	}
	c, target, label := mach.c, mach.target, mach.label
	m.setFlash("updating "+label+"…", false)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := update.Machine(ctx, c, target, func(string) {})
		time.Sleep(500 * time.Millisecond)
		return machineUpdateMsg{machine: mid, err: err}
	}
}

// handleUpdate processes update messages; handled is false for others.
func (m *Model) handleUpdate(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case updateTickMsg:
		return m.checkUpdates(), true
	case releaseCheckMsg:
		m.upd.release = msg.rel
		return nil, true
	case updateStepMsg:
		return m.updateRemotes(), true
	case updateDoneMsg:
		m.upd.running = false
		if msg.err != nil {
			m.setFlash("update: "+msg.err.Error(), true)
		} else {
			m.upd.release = nil
			m.setFlash("conch is up to date · panes kept", false)
		}
		return nil, true
	case machineUpdateMsg:
		mach := m.machine(msg.machine)
		if mach == nil {
			return nil, true
		}
		if msg.err != nil {
			m.setFlash("update "+mach.label+": "+msg.err.Error(), true)
			return nil, true
		}
		mach.close()
		mach.warning = ""
		return tea.Batch(mach.connect(false), m.rebuild()), true
	case restartTUIMsg:
		m.restart = true
		return tea.Quit, true
	}
	return nil, false
}

// autoUpdateMachine updates a machine that connected behind this build,
// when this TUI was started by an update.
func (m *Model) autoUpdateMachine(mid string) tea.Cmd {
	if m.upd == nil || !m.upd.remotes || mid == localMachine {
		return nil
	}
	mach := m.machine(mid)
	if mach == nil || mach.c == nil {
		return nil
	}
	if same, ok := update.SameBuild(mach.server); ok && !same {
		return m.updateMachine(mid)
	}
	return nil
}

// RestartRequested reports whether the TUI quit to restart onto a new
// build.
func RestartRequested(model tea.Model) bool {
	mm, ok := model.(Model)
	return ok && mm.restart
}

// RestartEnv is the environment for the restarted TUI.
func RestartEnv() []string {
	env := os.Environ()
	out := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, updateRemotesEnv+"=") {
			out = append(out, kv)
		}
	}
	return append(out, updateRemotesEnv+"=1")
}
