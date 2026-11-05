package tui

import (
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/config"
)

// A TUI holds config.toml in memory and writes all of it back whenever a
// setting changes here, so a change made outside it — `conch config web`,
// Conch.app's Settings, an editor — was undone by the next toggle: a theme
// chosen in the browser went back to the old one at the TUI's next save.
// So the file is watched, and a change made elsewhere is taken in as a
// save from the settings page would be.

const configWatchEvery = time.Second

// configSaving counts this process's saves still being written: while one
// is, the file is between two of this TUI's own states and is not read.
var configSaving atomic.Int32

// configWatch is the file as this TUI last saw it.
type configWatch struct {
	mu    sync.Mutex
	stamp string
}

func newConfigWatch() *configWatch { return &configWatch{stamp: uiStamp(config.File())} }

// configWatchTick looks at config.toml in a second.
func configWatchTick(w *configWatch) tea.Cmd {
	return tea.Tick(configWatchEvery, func(time.Time) tea.Msg { return pollConfig(w) })
}

// configPollMsg is a look at the file: changed carries what it says now.
type configPollMsg struct {
	changed bool
	cfg     config.Config
}

func pollConfig(w *configWatch) configPollMsg {
	if configSaving.Load() > 0 {
		return configPollMsg{} // our own save is landing; look next time
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	stamp := uiStamp(config.File())
	if stamp == w.stamp {
		return configPollMsg{}
	}
	cfg, err := config.Load()
	if err != nil {
		return configPollMsg{} // half written or broken: keep what we have
	}
	w.stamp = stamp
	return configPollMsg{changed: true, cfg: cfg}
}

// takeConfigPoll applies a change made outside this TUI. Its own saves
// read back the same as what it holds, and change nothing.
func (m *Model) takeConfigPoll(msg configPollMsg) tea.Cmd {
	if !msg.changed || reflect.DeepEqual(msg.cfg, m.cfg) {
		return nil
	}
	m.applyConfig(msg.cfg)
	m.setFlash("settings changed in config.toml", false)
	return nil
}
