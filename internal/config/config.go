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
}

// UICfg holds TUI preferences.
type UICfg struct {
	// Mouse lets conch handle clicks, the wheel and drags. Hold shift (option
	// in iTerm2) to select text with the terminal instead; false leaves the
	// mouse to the terminal entirely.
	Mouse bool `toml:"mouse"`
	// Accent colours selections, focused borders and dialogs: teal (default),
	// blue, green, orange, pink, red, gray, purple, or a "#rrggbb" value.
	Accent string `toml:"accent"`
}

// NotifyCfg controls how the TUI tells you an agent needs attention while
// you are looking at a different pane.
type NotifyCfg struct {
	Desktop bool `toml:"desktop"` // macOS notification / notify-send
	Bell    bool `toml:"bell"`    // terminal bell
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
		Notify: NotifyCfg{Desktop: true},
		UI:     UICfg{Mouse: true},
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
