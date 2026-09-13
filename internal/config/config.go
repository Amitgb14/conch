// Package config resolves conch's on-disk locations and loads config.toml.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the user configuration read from config.toml.
type Config struct {
	Keys   Keys      `toml:"keys"`
	Pane   PaneCfg   `toml:"pane"`
	Notify NotifyCfg `toml:"notify"`
	UI     UICfg     `toml:"ui"`
	Shell  ShellCfg  `toml:"shell"`
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
	// Theme is the colour scheme: conch, dracula, catppuccin, nord, gruvbox
	// or tokyo-night.
	Theme string `toml:"theme"`
	// Accent overrides the theme's accent (selections, focused borders,
	// dialogs): teal, blue, green, orange, pink, red, gray, purple, or a
	// "#rrggbb" value. Empty uses the theme's.
	Accent string `toml:"accent"`
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
		Notify: NotifyCfg{Enabled: true, Desktop: true, Waiting: true, Done: true},
		UI:     UICfg{Mouse: true, Theme: "conch"},
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
