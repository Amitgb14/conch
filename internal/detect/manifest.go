// Package detect works out which agent runs in a pane and what it is doing,
// from the terminal's foreground process, the visible screen, and lifecycle
// hooks the agent reports.
package detect

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Agent states.
const (
	StateIdle    = "idle"
	StateWorking = "working"
	StateBlocked = "blocked"
	StateDone    = "done" // idle, and nobody has looked since it finished
	StateUnknown = "unknown"
)

// Manifest describes how to recognise one agent and read its screen.
type Manifest struct {
	Agent        string   `toml:"agent"`
	ProcessNames []string `toml:"process_names"`
	// ScreenLines is how many rows from the bottom of the screen rules see.
	ScreenLines int `toml:"screen_lines"`
	// HookWorkingStale demotes a hook-reported "working" state to idle once
	// no working rule has matched the screen for this long. Hooks miss some
	// transitions (an Esc interrupt fires no hook); 0 disables.
	HookWorkingStale duration `toml:"hook_working_stale"`
	Rules            []Rule   `toml:"rules"`
}

// Rule maps a pattern to a state. Rules are tried in order.
type Rule struct {
	Name    string `toml:"name"`
	State   string `toml:"state"`
	Pattern string `toml:"pattern"`
	// On is what the pattern is matched against: "screen" (default) or
	// "title", the terminal title agents use to show their activity.
	On string `toml:"on"`
	re *regexp.Regexp
}

type duration struct{ time.Duration }

func (d *duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	d.Duration = v
	return err
}

//go:embed manifests/*.toml
var builtin embed.FS

// ManifestDir is where user overrides live: <dir>/<agent>.toml replaces the
// built-in manifest of the same agent.
func ManifestDir(configDir string) string {
	return filepath.Join(configDir, "agents")
}

// LoadManifests returns the built-in manifests with overrides from dir
// applied. A broken override is reported and the built-in is kept.
func LoadManifests(dir string) (map[string]*Manifest, []error) {
	out := map[string]*Manifest{}
	var errs []error
	entries, _ := fs.ReadDir(builtin, "manifests")
	for _, e := range entries {
		b, _ := fs.ReadFile(builtin, "manifests/"+e.Name())
		m, err := parseManifest(b)
		if err != nil {
			panic(fmt.Sprintf("builtin manifest %s: %v", e.Name(), err))
		}
		out[m.Agent] = m
	}
	if dir == "" {
		return out, nil
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.toml"))
	if err != nil {
		return out, []error{err}
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err == nil {
			var m *Manifest
			if m, err = parseManifest(b); err == nil {
				out[m.Agent] = m
				continue
			}
		}
		errs = append(errs, fmt.Errorf("%s: %w", f, err))
	}
	return out, errs
}

func parseManifest(b []byte) (*Manifest, error) {
	var m Manifest
	if _, err := toml.Decode(string(b), &m); err != nil {
		return nil, err
	}
	if m.Agent == "" {
		return nil, errors.New("manifest has no agent name")
	}
	if m.ScreenLines <= 0 {
		m.ScreenLines = 30
	}
	for i := range m.Rules {
		r := &m.Rules[i]
		switch r.State {
		case StateIdle, StateWorking, StateBlocked:
		default:
			return nil, fmt.Errorf("rule %q: state must be idle, working or blocked, not %q", r.Name, r.State)
		}
		switch r.On {
		case "", "screen", "title":
		default:
			return nil, fmt.Errorf("rule %q: on must be screen or title, not %q", r.Name, r.On)
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", r.Name, err)
		}
		r.re = re
	}
	return &m, nil
}

// MatchProcess reports whether a foreground process is this agent.
func (m *Manifest) MatchProcess(p Process) bool {
	for _, n := range p.Names() {
		for _, want := range m.ProcessNames {
			if n == want {
				return true
			}
		}
	}
	return false
}

// MatchScreen returns the first rule matching the bottom of the screen.
// Each rule is tried against the rows as they are and flattened to one
// line (whitespace collapsed, box borders removed), because agents re-wrap
// their text when the pane is resized.
func (m *Manifest) MatchScreen(lines []string) *Rule { return m.Match(lines, "") }

// Match returns the first rule matching the screen or, for title rules, the
// terminal title.
func (m *Manifest) Match(lines []string, title string) *Rule {
	text := bottom(lines, m.ScreenLines)
	// Box-drawing borders sit between wrapped words in bordered dialogs.
	flat := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r >= 0x2500 && r <= 0x257F {
			return ' '
		}
		return r
	}, text)), " ")
	for i := range m.Rules {
		r := &m.Rules[i]
		if r.On == "title" {
			if title != "" && r.re.MatchString(title) {
				return r
			}
			continue
		}
		if r.re.MatchString(text) || r.re.MatchString(flat) {
			return r
		}
	}
	return nil
}

// bottom joins the last n rows, ignoring trailing blank rows.
func bottom(lines []string, n int) string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return strings.Join(lines[max(end-n, 0):end], "\n")
}

// MatchAny finds the manifest whose agent owns the process.
func MatchAny(manifests map[string]*Manifest, p Process) *Manifest {
	names := make([]string, 0, len(manifests))
	for n := range manifests {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if manifests[n].MatchProcess(p) {
			return manifests[n]
		}
	}
	return nil
}

var shells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
	"ksh": true, "tcsh": true, "csh": true, "nu": true, "login": true,
	"-sh": true, "-bash": true, "-zsh": true, "-fish": true,
}

// IsShell reports whether the process is an interactive shell, i.e. no
// program is running in the foreground.
func IsShell(p Process) bool {
	for _, n := range p.Names() {
		if shells[n] {
			return true
		}
	}
	return false
}
