// Package config resolves conch's on-disk locations and loads config.toml.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the user configuration read from config.toml.
type Config struct {
	Keys   Keys      `toml:"keys"`
	Pane   PaneCfg   `toml:"pane"`
	Notify NotifyCfg `toml:"notify"`
	UI     UICfg     `toml:"ui"`
	Shell  ShellCfg  `toml:"shell"`
	Agents AgentsCfg `toml:"agents"`
	Brain  BrainCfg  `toml:"brain"`
	Update UpdateCfg `toml:"update"`
	Remote RemoteCfg `toml:"remote"`
}

// RemoteCfg holds settings for panes on remote machines.
type RemoteCfg struct {
	// UploadDrops copies local files dropped (pasted as paths) into a remote
	// pane to that machine, and pastes their paths there instead.
	UploadDrops bool `toml:"upload_drops"`
	// UploadMaxMB refuses larger files; 0 or less means the default.
	UploadMaxMB int `toml:"upload_max_mb"`
}

// DefaultUploadMaxMB is the largest file dropped into a remote pane that is
// uploaded, unless config.toml says otherwise.
const DefaultUploadMaxMB = 25

// UploadLimit is the largest file to upload, in bytes.
func (r RemoteCfg) UploadLimit() int64 {
	if r.UploadMaxMB <= 0 {
		return DefaultUploadMaxMB << 20
	}
	return int64(r.UploadMaxMB) << 20
}

// UpdateCfg controls how conch looks for newer builds.
type UpdateCfg struct {
	// CheckReleases asks GitHub once a day whether a newer release exists
	// (release builds only; development builds update from source).
	CheckReleases bool `toml:"check_releases"`
	// Auto installs a newer release as soon as that check finds one —
	// staying in step with upstream without pressing u — and reloads the
	// local server onto it, keeping panes. Off by default. It needs
	// CheckReleases. `conch update rollback` goes back.
	Auto bool `toml:"auto"`
}

// BrainCfg configures conch's brain: the model behind the command bar and
// agent summaries. Requests use your own agent login or API key.
type BrainCfg struct {
	// Provider is claude (the Claude Code CLI and its login), anthropic
	// (the API, key in $ANTHROPIC_API_KEY) or openai (any OpenAI-compatible
	// endpoint: OpenAI, Ollama, LM Studio…).
	Provider string `toml:"provider"`
	// Model for the command bar; empty picks the provider's default.
	Model string `toml:"model"`
	// SummaryModel for agent summaries; empty picks a small, fast model.
	SummaryModel string `toml:"summary_model"`
	// Summaries lets conch summarise agents on its own when they finish or
	// need you. Off by default: each summary is a (small) model request.
	Summaries bool `toml:"summaries"`
	// BaseURL overrides the API endpoint (anthropic, openai).
	BaseURL string `toml:"base_url"`
	// APIKeyEnv names the environment variable holding the API key.
	APIKeyEnv string `toml:"api_key_env"`
	// Command is the claude binary; empty finds it on PATH.
	Command string `toml:"command"`
}

// AgentsCfg holds agent preferences.
type AgentsCfg struct {
	// Default is the agent c starts: claude, codex, gemini or opencode.
	Default string `toml:"default"`
}

// ShellCfg holds settings for terminal panes.
type ShellCfg struct {
	// OMZTheme is an Oh My Zsh theme for new zsh terminals, applied after
	// the user's own .zshrc; "" keeps the theme .zshrc chooses.
	OMZTheme string `toml:"omz_theme"`
}

// UICfg holds TUI preferences.
type UICfg struct {
	// Mouse lets conch handle clicks, the wheel and drags. Hold shift (option
	// in iTerm2) to select text with the terminal instead; false leaves the
	// mouse to the terminal entirely.
	Mouse bool `toml:"mouse"`
	// Theme is the colour scheme: conch, dracula, catppuccin, nord, gruvbox,
	// tokyo-night, one-dark, solarized, rose-pine, kanagawa, everforest,
	// monokai, github-dark, ayu or night-owl.
	Theme string `toml:"theme"`
	// Accent overrides the theme's accent (selections, focused borders,
	// dialogs): teal, blue, green, orange, pink, red, gray, purple, or a
	// "#rrggbb" value. Empty uses the theme's.
	Accent string `toml:"accent"`
	// Cost shows what agents have spent — a cost where the agent reports
	// one, else the tokens it used — on tree rows, in the Sessions list and
	// on the project and machine pages. On unless turned off.
	Cost bool `toml:"cost"`
}

// NotifyCfg controls how the TUI tells you an agent needs attention while
// you are looking at a different pane.
type NotifyCfg struct {
	Enabled bool `toml:"enabled"` // master switch
	Desktop bool `toml:"desktop"` // macOS notification / notify-send
	Sound   bool `toml:"sound"`   // play a system sound
	Bell    bool `toml:"bell"`    // terminal bell
	Waiting bool `toml:"waiting"` // when an agent needs an answer
	Done    bool `toml:"done"`    // when an agent finishes
	// QuietStart and QuietEnd ("22:00", "08:00") silence alerts every day
	// between them; waiting agents still show in the sidebar. Empty: never.
	QuietStart string `toml:"quiet_start"`
	QuietEnd   string `toml:"quiet_end"`
	// Limits alerts when an agent's plan limit window (Claude's 5-hour or
	// weekly, Codex's) passes a percentage in LimitAt.
	Limits  bool  `toml:"limits"`
	LimitAt []int `toml:"limit_at"`
}

// DefaultLimitAt is when plan limit alerts fire, in percent used.
var DefaultLimitAt = []int{80, 95}

// Thresholds is LimitAt cleaned up: percentages in 1..100, ascending, once
// each; the defaults when none are valid.
func (n NotifyCfg) Thresholds() []int {
	seen := map[int]bool{}
	var out []int
	for _, p := range n.LimitAt {
		if p >= 1 && p <= 100 && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return append([]int(nil), DefaultLimitAt...)
	}
	sort.Ints(out)
	return out
}

// Quiet reports whether t falls in the configured quiet hours. The range
// may wrap past midnight.
func (n NotifyCfg) Quiet(t time.Time) bool {
	start, ok1 := clockMinutes(n.QuietStart)
	end, ok2 := clockMinutes(n.QuietEnd)
	if !ok1 || !ok2 || start == end {
		return false
	}
	now := t.Hour()*60 + t.Minute()
	if start < end {
		return now >= start && now < end
	}
	return now >= start || now < end
}

func clockMinutes(s string) (int, bool) {
	var h, m int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// Keys holds key bindings.
type Keys struct {
	// Prefix is the key that, while typing into a pane, hands control back
	// to conch (Bubble Tea key notation, e.g. "ctrl+b").
	Prefix string `toml:"prefix"`
}

// PaneCfg holds defaults for new panes.
type PaneCfg struct {
	// DefaultCommand is the command a new pane runs; empty means $SHELL.
	DefaultCommand string `toml:"default_command"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Keys:   Keys{Prefix: "ctrl+b"},
		Notify: NotifyCfg{Enabled: true, Desktop: true, Waiting: true, Done: true, Limits: true, LimitAt: append([]int(nil), DefaultLimitAt...)},
		UI:     UICfg{Mouse: true, Theme: "conch", Cost: true},
		Agents: AgentsCfg{Default: "claude"},
		Brain:  BrainCfg{Provider: "claude"},
		Update: UpdateCfg{CheckReleases: true},
		Remote: RemoteCfg{UploadDrops: true, UploadMaxMB: DefaultUploadMaxMB},
	}
}

// Dir returns the conch config/state directory.
// Order: $CONCH_HOME, $XDG_CONFIG_HOME/conch, ~/.config/conch.
func Dir() string {
	if d := os.Getenv("CONCH_HOME"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "conch")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "conch")
	}
	return filepath.Join(home, ".config", "conch")
}

// SocketPath returns the server socket path ($CONCH_SOCKET overrides).
func SocketPath() string {
	if p := os.Getenv("CONCH_SOCKET"); p != "" {
		return p
	}
	return filepath.Join(Dir(), "conch.sock")
}

// ServerLogPath returns the path the detached server logs to.
func ServerLogPath() string {
	return filepath.Join(Dir(), "server.log")
}

// Load reads config.toml from Dir(), falling back to defaults when absent.
func Load() (Config, error) {
	cfg := Default()
	_, err := toml.DecodeFile(filepath.Join(Dir(), "config.toml"), &cfg)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Default(), err
	}
	if cfg.Keys.Prefix == "" {
		cfg.Keys.Prefix = Default().Keys.Prefix
	}
	return cfg, nil
}

// Save writes cfg to config.toml, as the settings screen does. Comments in
// a hand-edited file are not preserved.
func Save(cfg Config) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# conch settings. The settings screen (⚙ or ,) rewrites this file.\n\n")
	if err := toml.NewEncoder(&b).Encode(cfg); err != nil {
		return err
	}
	path := filepath.Join(Dir(), "config.toml")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DefaultShell returns the user's login shell.
func DefaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// MergeEnv returns base with each KEY=VALUE in overrides set, replacing any
// existing entry for that key. Appending instead leaves duplicates, and which
// duplicate a program sees depends on its runtime.
func MergeEnv(base []string, overrides ...string) []string {
	keys := make(map[string]bool, len(overrides))
	for _, kv := range overrides {
		k, _, _ := strings.Cut(kv, "=")
		keys[k] = true
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		if k, _, _ := strings.Cut(kv, "="); !keys[k] {
			out = append(out, kv)
		}
	}
	return append(out, overrides...)
}
