package tui

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
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
	// checked records machines whose first connection since the update
	// was looked at: only that one may update them. Later reconnects —
	// after another client upgraded the machine, say — are left alone, or
	// two builds would keep replacing each other there.
	checked map[string]bool
	// skip holds machines unticked in the version box: this update, and
	// the TUI it restarts into, leave them on their build.
	skip map[string]bool
}

const (
	updateCheckEvery = 5 * time.Second
	releaseCheckGap  = 24 * time.Hour
	updateRemotesEnv = "CONCH_UPDATE_REMOTES"
	updateSkipEnv    = "CONCH_UPDATE_SKIP" // comma-separated machine IDs
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
		u.remotes = true
		for _, id := range strings.Split(os.Getenv(updateSkipEnv), ",") {
			if id != "" {
				if u.skip == nil {
					u.skip = map[string]bool{}
				}
				u.skip[id] = true
			}
		}
	}
	os.Unsetenv(updateRemotesEnv)
	os.Unsetenv(updateSkipEnv)
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
	machine       string // a remote machine's ID; "" for this computer
}

// pendingUpdates lists what is out of date.
func (m Model) pendingUpdates() []updateItem {
	u := m.upd
	if u == nil {
		return nil
	}
	var items []updateItem
	if u.release != nil {
		items = append(items, updateItem{"Release", fmt.Sprintf("%s available (running %s)", u.release.Version, versionLabel()), ""})
	}
	if u.diskBuild != "" {
		items = append(items, updateItem{"This TUI", "new build on disk " + u.diskBuild + " · restarts onto it", ""})
	}
	if len(m.machines) > 0 && m.serverBehindDisk() {
		items = append(items, updateItem{"Server", "runs build " + m.machines[0].server.Build + " · reloads, panes keep running", ""})
	}
	for _, mach := range m.machines[min(1, len(m.machines)):] {
		if mach.c == nil {
			continue
		}
		if m.remoteBehind(mach) {
			items = append(items, updateItem{mach.label, "runs build " + firstNonEmpty(mach.server.BuildID, mach.server.Build) + " · installs and reloads", mach.id})
		}
	}
	return items
}

// remoteBehind reports whether a machine runs another build than the one
// an update brings: the build on disk when conch was rebuilt, else this
// TUI's. Comparing with the TUI alone missed every remote after a rebuild —
// they matched the old TUI — so there was nothing to tick before the
// restarted TUI updated them all.
func (m Model) remoteBehind(mach *machine) bool {
	if m.upd != nil && m.upd.diskBuild != "" && !proto.IsRelease() {
		return mach.server.BuildID != "" && mach.server.BuildID != m.upd.diskBuild
	}
	same, ok := update.SameBuild(mach.server)
	return ok && !same
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

// startUpdate applies everything pending except the machines in skip: a
// release download, the local server's reload, then a restart of this TUI
// when its binary changed (the new TUI updates remote machines); without a
// TUI restart, remotes now.
func (m *Model) startUpdate(skip map[string]bool) tea.Cmd {
	u := m.upd
	if u.running {
		return nil
	}
	pending := m.pendingUpdates()
	if len(pending) == 0 {
		m.setFlash("conch is up to date", false)
		return nil
	}
	if !slices.ContainsFunc(pending, func(it updateItem) bool { return it.machine == "" || !skip[it.machine] }) {
		m.setFlash("nothing ticked to update", true)
		return nil
	}
	u.running, u.skip = true, nil
	for id, off := range skip {
		if off && id != "" {
			if u.skip == nil {
				u.skip = map[string]bool{}
			}
			u.skip[id] = true
		}
	}
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

// updateRemotes brings the connected machines behind this build up to
// date, except those left unticked.
func (m *Model) updateRemotes() tea.Cmd {
	var cmds []tea.Cmd
	for _, mach := range m.machines[1:] {
		if mach.c == nil || m.upd.skip[mach.id] {
			continue
		}
		if same, ok := update.SameBuild(mach.server); ok && !same {
			cmds = append(cmds, m.updateMachine(mach.id))
		}
	}
	if len(cmds) == 0 {
		m.upd.running = false
		m.setFlash(m.updatedText(), false)
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
		err := update.Machine(ctx, c, remote.SSH(target, false), func(string) {})
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
			m.setFlash(m.updatedText(), false)
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

// updatedText is the status after an update, naming machines left out.
func (m *Model) updatedText() string {
	var left []string
	for _, mach := range m.machines {
		if m.upd.skip[mach.id] {
			left = append(left, mach.label)
		}
	}
	if len(left) == 0 {
		return "conch is up to date · panes kept"
	}
	return "conch updated · panes kept · not updated: " + strings.Join(left, ", ")
}

// autoUpdateMachine updates a machine that connected behind this build,
// when this TUI was started by an update and the machine wasn't unticked.
func (m *Model) autoUpdateMachine(mid string) tea.Cmd {
	if m.upd == nil || !m.upd.remotes || mid == localMachine || m.upd.skip[mid] {
		return nil
	}
	mach := m.machine(mid)
	if mach == nil || mach.c == nil || m.upd.checked[mid] {
		return nil
	}
	if m.upd.checked == nil {
		m.upd.checked = map[string]bool{}
	}
	m.upd.checked[mid] = true
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

// RestartEnv is the environment for the TUI restarted from model: it
// updates remote machines, except those unticked for this update.
func RestartEnv(model tea.Model) []string {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, updateRemotesEnv+"=") && !strings.HasPrefix(kv, updateSkipEnv+"=") {
			out = append(out, kv)
		}
	}
	out = append(out, updateRemotesEnv+"=1")
	if mm, ok := model.(Model); ok && mm.upd != nil && len(mm.upd.skip) > 0 {
		ids := slices.Sorted(maps.Keys(mm.upd.skip))
		out = append(out, updateSkipEnv+"="+strings.Join(ids, ","))
	}
	return out
}
