package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/config"
)

// uiState is what the TUI remembers between runs.
type uiState struct {
	Expanded     map[string]bool `json:"expanded"`
	ShowAll      map[string]bool `json:"show_all"`
	SidebarWidth int             `json:"sidebar_width,omitempty"`
	Tabs         []savedTab      `json:"tabs,omitempty"`
	ActiveTab    int             `json:"active_tab,omitempty"`
	LimitAlerts  map[string]int  `json:"limit_alerts,omitempty"` // see limitalerts.go
	// QueueDismissed is what x put aside in the review queue, by the row's
	// key and the state it was in: a row comes back when that changes.
	QueueDismissed map[string]string `json:"queue_dismissed,omitempty"`
	SavedSSH       []string          `json:"saved_ssh,omitempty"` // ssh hosts kept in the tree (ssh.go)
	// SSHHosts are saved hosts' names and options, by host (sshhosts.go).
	// The hosts themselves stay in SavedSSH, which older builds read; a
	// host's folder is a member of that folder.
	SSHHosts map[string]sshHostInfo `json:"ssh_hosts,omitempty"`
	// SSHGroups is the order of ssh groups from a build before they became
	// folders: read once to make the folders, never written.
	SSHGroups []string `json:"ssh_groups,omitempty"`
	// SandboxRan is what each sandbox was running when it stopped, by
	// machine ID, to offer back when it starts again (sandboxran.go).
	SandboxRan map[string][]ranPane `json:"sandbox_ran,omitempty"`
	// Folders are the groups of your own in the tree (folders.go), by the
	// section they sit in. Absent for anybody who never made one.
	Folders map[string][]savedFolder `json:"folders,omitempty"`
	// NoProjectOffer is the repositories you answered No to adding as a
	// project when conch was started in them (launch.go).
	NoProjectOffer []string `json:"no_project_offer,omitempty"`
	// Spaces are the workspaces after the first (spaces.go); Tabs above
	// stay the first one's, which is all an older build knows of.
	Spaces      []savedSpace `json:"spaces,omitempty"`
	FirstSpace  *savedSpace  `json:"first_space,omitempty"` // what workspace 1 holds; its tabs are Tabs
	ActiveSpace int          `json:"active_space,omitempty"`
}

// savedFolder is one folder of the tree and what is in it. A pane is held
// by name and by id: ids die with the server, and the name is what was
// typed, so a folder finds its panes again after a restart.
type savedFolder struct {
	Name    string        `json:"name"`
	Members []savedMember `json:"members,omitempty"`
}

type savedMember struct {
	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`
	// Host is a saved ssh host in a folder of the SSH section: the host,
	// not a pane, so its sessions follow it in (sshhosts.go).
	Host string `json:"host,omitempty"`
}

func uiStatePath() string { return filepath.Join(config.Dir(), "ui.json") }

// loadUIState reads the saved state; a missing or broken file gives defaults.
func loadUIState(path string) uiState {
	st := uiState{Expanded: map[string]bool{}, ShowAll: map[string]bool{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	if json.Unmarshal(b, &st) != nil {
		return uiState{Expanded: map[string]bool{}, ShowAll: map[string]bool{}}
	}
	if st.Expanded == nil {
		st.Expanded = map[string]bool{}
	}
	if st.ShowAll == nil {
		st.ShowAll = map[string]bool{}
	}
	if st.LimitAlerts == nil {
		st.LimitAlerts = map[string]int{}
	}
	if st.QueueDismissed == nil {
		st.QueueDismissed = map[string]string{}
	}
	pruneLimitAlerts(st.LimitAlerts, time.Now())
	return st
}

func saveUIState(path string, st uiState) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// saveState writes fold state and sidebar width in the background. The
// maps are copied because the model keeps changing them.
func (m Model) saveState() tea.Cmd {
	tabs, active := m.firstSpaceTabs()
	st := uiState{Expanded: map[string]bool{}, ShowAll: map[string]bool{}, SidebarWidth: m.sidebarW,
		Tabs: saveTabs(tabs), ActiveTab: active, Spaces: m.savedSpaces(), FirstSpace: m.savedFirstSpace(), ActiveSpace: m.activeSpace, LimitAlerts: map[string]int{},
		QueueDismissed: map[string]string{}, SavedSSH: slices.Clone(m.savedSSH), NoProjectOffer: slices.Clone(m.noProjectOffer)}
	if len(m.sshInfo) > 0 {
		st.SSHHosts = make(map[string]sshHostInfo, len(m.sshInfo))
		for k, v := range m.sshInfo {
			v.Args = slices.Clone(v.Args)
			st.SSHHosts[k] = v
		}
	}
	if len(m.folders) > 0 {
		st.Folders = make(map[string][]savedFolder, len(m.folders))
		for k, v := range m.folders {
			st.Folders[k] = slices.Clone(v)
		}
	}
	if len(m.sandboxRan) > 0 {
		st.SandboxRan = make(map[string][]ranPane, len(m.sandboxRan))
		for k, v := range m.sandboxRan {
			st.SandboxRan[k] = slices.Clone(v)
		}
	}
	for k, v := range m.limitSeen {
		st.LimitAlerts[k] = v
	}
	for k, v := range m.queueSeen {
		st.QueueDismissed[k] = v
	}
	for k, v := range m.expanded {
		st.Expanded[k] = v
	}
	for k, v := range m.showAll {
		st.ShowAll[k] = v
	}
	path := m.statePath
	return func() tea.Msg {
		if path == "" {
			return nil
		}
		_ = saveUIState(path, st) // losing fold state is not worth interrupting the user
		return nil
	}
}
