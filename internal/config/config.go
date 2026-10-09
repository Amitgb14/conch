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
	Keys    Keys       `toml:"keys"`
	Pane    PaneCfg    `toml:"pane"`
	Notify  NotifyCfg  `toml:"notify"`
	UI      UICfg      `toml:"ui"`
	Shell   ShellCfg   `toml:"shell"`
	Agents  AgentsCfg  `toml:"agents"`
	Brain   BrainCfg   `toml:"brain"`
	Update  UpdateCfg  `toml:"update"`
	Remote  RemoteCfg  `toml:"remote"`
	Verify  VerifyCfg  `toml:"verify"`
	Sandbox SandboxCfg `toml:"sandbox"`
	Web     WebCfg     `toml:"web"`
	// Actions are commands of your own on conch's menus. Last, because
	// TOML's array of tables has to come after the plain tables when the
	// settings screen writes this file back out.
	Actions []Action `toml:"actions,omitempty"`
}

// WebCfg is the phone gateway, conch web. URL is the address phones open —
// the https name `tailscale serve` gives — and Port where it listens;
// either flag given to conch web wins over them.
type WebCfg struct {
	URL  string `toml:"url,omitempty"`
	Port int    `toml:"port,omitempty"`
}

// DefaultWebPort is where conch web listens unless told otherwise.
const DefaultWebPort = 8722

// PortOrDefault is the port set, or the default.
func (w WebCfg) PortOrDefault() int {
	if w.Port < 1 || w.Port > 65535 {
		return DefaultWebPort
	}
	return w.Port
}

// RestoresRunning reports whether conch should remember what a sandbox
// was running and offer it back. Unset is true.
func (p ProviderCfg) RestoresRunning() bool { return p.Restore == nil || *p.Restore }

// IdleMinutes is how long a sandbox may sit idle before conch stops it:
// what was set, or IdleStopDefault when nothing was. 0 never stops one.
func (p ProviderCfg) IdleMinutes() int {
	if p.IdleStop == nil {
		return IdleStopDefault
	}
	return max(*p.IdleStop, 0)
}

// Priced reports whether a cost can be worked out at all.
func (p ProviderCfg) Priced() bool {
	return p.PriceCPUHour > 0 || p.PriceGiBHour > 0 || p.PriceDiskGiBHour > 0
}

// CostPerHour is what a sandbox of this size costs an hour while it runs,
// or 0 when no prices are set.
func (p ProviderCfg) CostPerHour(cpu, memGiB, diskGiB int) float64 {
	return float64(cpu)*p.PriceCPUHour + float64(memGiB)*p.PriceGiBHour + float64(diskGiB)*p.PriceDiskGiBHour
}

// StoppedCostPerHour is what it costs an hour while it is stopped: the
// disk it keeps. Providers charge for that until the sandbox is deleted,
// which is the cost people forget.
func (p ProviderCfg) StoppedCostPerHour(diskGiB int) float64 {
	return float64(diskGiB) * p.PriceDiskGiBHour
}

// SandboxCfg holds the sandbox providers conch can use, keyed by the
// provider's own name: `[sandbox.daytona]`, `[sandbox.e2b]` and so on.
// Every provider takes the same settings, so one conch grows needs no
// change here — and a table for a provider this build doesn't know is
// kept as it is rather than thrown away when the settings are saved.
type SandboxCfg map[string]ProviderCfg

// Of is the configuration for a provider, or the zero value when it has
// none: every field's zero means "the provider's own default".
func (c SandboxCfg) Of(name string) ProviderCfg { return c[name] }

// Set stores a provider's configuration, making the map if it is nil.
func (c *SandboxCfg) Set(name string, p ProviderCfg) {
	if *c == nil {
		*c = SandboxCfg{}
	}
	(*c)[name] = p
}

// IdleStopDefault is how long a sandbox may sit idle before conch stops
// it when nothing says otherwise: long enough to think, short enough that
// a forgotten sandbox costs an evening rather than a month.
const IdleStopDefault = 30

// ProviderCfg configures one sandbox provider. The API key is never
// stored: it is read from the environment variable APIKeyEnv names.
type ProviderCfg struct {
	// APIKey is the key itself, when you would rather keep it here than in
	// your environment. It is stored as it is: config.toml is written
	// 0600, but anything running as you can read it, and it travels with
	// a backup or a synced dotfile. Empty means the variable APIKeyEnv
	// names is used instead.
	APIKey string `toml:"api_key,omitempty"`
	// APIKeyEnv names the variable holding the key; "" is the provider's
	// own, e.g. DAYTONA_API_KEY. A key set above wins over it.
	APIKeyEnv string `toml:"api_key_env,omitempty"`
	// APIURL overrides the API endpoint; "" is the provider's own, or what
	// its environment variable says.
	APIURL string `toml:"api_url,omitempty"`
	// Target is the region, e.g. "us" or "eu"; "" is the account default.
	Target string `toml:"target,omitempty"`
	// Snapshot (image) new sandboxes start from; "" is the provider's own.
	Snapshot string `toml:"snapshot,omitempty"`
	// AutoStop is the provider's own idle timer, in minutes; 0 (the
	// default) turns it off. Providers count only what reaches them from
	// outside — an ssh connection, an API call — so an agent working
	// quietly inside looks idle to them and would be stopped. IdleStop
	// below is conch's own, which knows better.
	AutoStop int `toml:"auto_stop,omitempty"`
	// Restore says whether conch writes down what a sandbox was running
	// when it stops, and offers it back when it starts again. Unset is
	// true: the agents' conversations are on the sandbox's own disk, so
	// the offer costs nothing until it is taken. Set it false to have
	// conch keep no note of what ran.
	Restore *bool `toml:"restore_running,omitempty"`
	// IdleStop is how many minutes a sandbox may go without an agent
	// working or a pane printing before conch stops it, keeping its files
	// and its memory. Unset is IdleStopDefault; 0 never stops one.
	IdleStop *int `toml:"idle_stop,omitempty"`
	// PriceCPUHour, PriceGiBHour and PriceDiskGiBHour are what an hour of
	// a sandbox costs, for conch to show what one has run up. conch ships
	// no price list — providers change theirs, and a stale one misleads —
	// so nothing is shown until these are set, from the provider's own
	// pricing page.
	PriceCPUHour     float64 `toml:"price_cpu_hour,omitempty"`
	PriceGiBHour     float64 `toml:"price_gib_hour,omitempty"`
	PriceDiskGiBHour float64 `toml:"price_disk_gib_hour,omitempty"`
	// Env names variables of this environment passed into new sandboxes,
	// e.g. CLAUDE_CODE_OAUTH_TOKEN. The provider keeps their values with
	// the sandbox.
	Env []string `toml:"env,omitempty"`
}

// VerifyCfg holds the command conch runs in a branch's worktree when its
// agent finishes, so the review queue can say whether the work stands up.
// Commands are per project and there is no default: nothing runs until you
// name one, since a project's own command can be slow or have side effects.
type VerifyCfg struct {
	// Commands maps a project ID to its command, e.g. "go test ./...".
	Commands map[string]string `toml:"commands,omitempty"`
}

// RemoteCfg holds settings for panes on remote machines.
type RemoteCfg struct {
	// UploadDrops copies local files dropped (pasted as paths) into a remote
	// pane to that machine, and pastes their paths there instead.
	UploadDrops bool `toml:"upload_drops"`
	// UploadMaxMB refuses larger files; 0 or less means the default.
	UploadMaxMB int `toml:"upload_max_mb"`
	// NoCompression stops conch asking ssh to deflate its connections to
	// other machines (Compression yes in the config it generates). It is on
	// otherwise: those connections carry panes, and a frame is a screenful
	// of styled text, which deflates to a tenth or less — the tens of
	// microseconds that costs buy milliseconds back on any network. Worth
	// turning off only where the link is as fast as the processor — a
	// machine on the same host, say — or where the CPU is the scarce thing.
	// Nothing local is ever compressed: there is no connection to settle
	// it on, only a unix socket.
	NoCompression bool `toml:"no_compression,omitempty"`
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
	// Hover lights the row, tab or button under the pointer. It asks the
	// terminal to report every pointer move rather than only drags, which
	// is a message per cell crossed — cheap on this computer, less so on a
	// slow ssh link — so it is off until asked for.
	Hover bool `toml:"hover,omitempty"`
	// Diff is how a file's changes are read: "side" for two columns,
	// "unified" for one, and "auto" (the default, and what an empty
	// value means) for two where the terminal is wide enough and one
	// where it is not. `s` in the changes view overrides it for the file
	// being read, without writing anything down.
	Diff string `toml:"diff,omitempty"`
	// Icons is how the file explorer marks each kind of file: "text" (the
	// default, a coloured two-letter tag that works in any font), "nerd"
	// (Nerd Font glyphs) or "off". Empty means text.
	Icons string `toml:"icons,omitempty"`
	// TreeGroups is how a project's panes are grouped in the tree:
	// "sections" (the default: Agents and Terminals) or "tabs", a section
	// per tab of the bar holding the panes open in it. Panes open in no tab
	// keep their sections either way, so nothing is hidden by being closed.
	TreeGroups string `toml:"tree_groups,omitempty"`
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
	// Silence is how many seconds without output count as quiet, for a
	// pane watched for it (ctrl+b M).
	Silence int `toml:"silence"`
}

// DefaultSilence is how long a watched pane must be quiet, in seconds.
const DefaultSilence = 30

// SilenceAfter is Silence, or the default when it is unset or makes no
// sense.
func (n NotifyCfg) SilenceAfter() int {
	if n.Silence < 1 {
		return DefaultSilence
	}
	return n.Silence
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
		Notify: NotifyCfg{Enabled: true, Desktop: true, Waiting: true, Done: true, Limits: true, LimitAt: append([]int(nil), DefaultLimitAt...), Silence: DefaultSilence},
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

// File is where config.toml lives, for telling somebody which file to
// edit. Load and Save already know; this is for saying so on screen.
func File() string { return filepath.Join(Dir(), "config.toml") }

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
	_, err := toml.DecodeFile(File(), &cfg)
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
	path := File()
	keepUnreadable(path)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// keepUnreadable moves aside a config.toml conch could not parse, so that
// writing the settings over it doesn't lose whatever was being edited. The
// typo is then in config.toml.invalid to be put right or thrown away.
func keepUnreadable(path string) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return // nothing there, or not a file conch could have written
	}
	var probe Config
	if _, err := toml.DecodeFile(path, &probe); err == nil {
		return
	}
	_ = os.Rename(path, path+".invalid")
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
