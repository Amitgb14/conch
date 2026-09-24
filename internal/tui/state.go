package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	st := uiState{Expanded: map[string]bool{}, ShowAll: map[string]bool{}, SidebarWidth: m.sidebarW,
		Tabs: m.savedTabs(), ActiveTab: m.activeTab, LimitAlerts: map[string]int{},
		QueueDismissed: map[string]string{}}
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
