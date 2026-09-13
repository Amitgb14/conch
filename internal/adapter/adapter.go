// Package adapter knows how to launch each supported agent with conch's
// integration wired in.
package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Adapter launches one kind of agent.
type Adapter interface {
	// Name is the agent name used in manifests and the protocol.
	Name() string
	// Command returns the argv that starts the agent through shell, with
	// args (shell words typed by the user) appended.
	Command(shell, args string) []string
	// Detect reports whether the agent is installed, as the user's login
	// shell would find it.
	Detect(ctx context.Context, shell string) Availability
	// InstallScript is a shell script that installs the agent for the
	// current user, run visibly in a pane.
	InstallScript() string
}

// Availability is whether an agent is installed on this machine.
type Availability struct {
	Installed bool
	Path      string
	Version   string
}

// Registry holds the adapters available on this server.
type Registry map[string]Adapter

// New prepares every adapter. exe is the conch binary hooks call back into;
// dir is conch's config directory, where integration files are written.
func New(exe, dir string) (Registry, error) {
	claude, err := newClaude(exe, dir)
	if err != nil {
		return nil, err
	}
	return Registry{claude.Name(): claude}, nil
}

// Claude launches Claude Code with conch's hooks loaded via --settings,
// which leaves the user's own settings files untouched.
type Claude struct {
	settingsPath string
}

// ClaudeHookEvents are the Claude Code hook events conch listens to.
var ClaudeHookEvents = []string{
	"SessionStart", "SessionEnd",
	"UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
	"PermissionRequest", "PermissionDenied", "Notification",
	"SubagentStart", "SubagentStop", "PreCompact",
	"Stop", "StopFailure",
}

func newClaude(exe, dir string) (*Claude, error) {
	type hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type group struct {
		Hooks []hook `json:"hooks"`
	}
	hooks := map[string][]group{}
	cmd := ShellQuote(exe) + " report claude-hook"
	for _, ev := range ClaudeHookEvents {
		hooks[ev] = []group{{Hooks: []hook{{Type: "command", Command: cmd, Timeout: 5}}}}
	}
	b, err := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "claude-settings.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(path, append(b, '\n')); err != nil {
		return nil, fmt.Errorf("write claude settings: %w", err)
	}
	return &Claude{settingsPath: path}, nil
}

// Name implements Adapter.
func (c *Claude) Name() string { return "claude" }

// Command implements Adapter. The login shell gives claude the user's
// usual PATH; exec keeps claude as the pane's foreground process.
func (c *Claude) Command(shell, args string) []string {
	// The native installer puts claude in ~/.local/bin and adds that to
	// shell startup files, which a shell started before the install hasn't
	// read.
	line := `PATH="$HOME/.local/bin:$PATH" exec claude --settings ` + ShellQuote(c.settingsPath)
	if args = strings.TrimSpace(args); args != "" {
		line += " " + args
	}
	return []string{shell, "-lc", line}
}

// Detect implements Adapter.
func (c *Claude) Detect(ctx context.Context, shell string) Availability {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-lc", `PATH="$HOME/.local/bin:$PATH"; p=$(command -v claude) || exit 1; echo "$p"; claude --version`)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return Availability{}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	av := Availability{Installed: true, Path: strings.TrimSpace(lines[0])}
	if len(lines) > 1 {
		av.Version = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(lines[len(lines)-1]), "(Claude Code)"))
	}
	return av
}

// claudeInstall runs Anthropic's native installer, which installs
// ~/.local/bin/claude for the current user (no root). Alpine needs a few
// system packages first, which only root can add, so say what to run.
const claudeInstall = `set -e
echo "Installing Claude Code with the official installer (https://claude.ai/install.sh)"
if [ -f /etc/alpine-release ] && ! apk info -e libstdc++ >/dev/null 2>&1; then
  echo "Alpine needs packages first; as root run: apk add bash curl libgcc libstdc++ ripgrep" >&2
  exit 1
fi
if ! command -v bash >/dev/null 2>&1; then echo "the installer needs bash" >&2; exit 1; fi
if command -v curl >/dev/null 2>&1; then
  curl -fsSL https://claude.ai/install.sh | bash
elif command -v wget >/dev/null 2>&1; then
  wget -qO- https://claude.ai/install.sh | bash
else
  echo "the installer needs curl or wget" >&2; exit 1
fi
echo
echo "Installed: $("$HOME/.local/bin/claude" --version)"
echo "Start Claude from conch (c) and log in: open the link it shows on any computer and paste the code back."`

// InstallScript implements Adapter.
func (c *Claude) InstallScript() string { return claudeInstall }

// defaultTitles are titles agents show when they have no task yet.
var defaultTitles = map[string]bool{"claude code": true, "claude": true}

// CleanTitle turns a terminal title into a task label: leading status
// symbols (spinners, ✳) are dropped, and agents' default titles become "".
func CleanTitle(title string) string {
	title = strings.TrimLeftFunc(title, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	title = strings.TrimSpace(title)
	if defaultTitles[strings.ToLower(title)] {
		return ""
	}
	return title
}

// sessionEnv are variables an agent sets for its own session. A pane
// inheriting them (because the conch server was started from inside such a
// session) would make a new agent think it is a child of that session.
var sessionEnv = []string{
	"CLAUDECODE", "CLAUDE_PID", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_EXECPATH",
	"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ATTENDED",
	"CLAUDE_CODE_BRIDGE_SESSION_ID", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN",
	"CLAUDE_CODE_SSE_PORT",
	"CONCH_PANE_ID", // a server started from inside a pane
}

// SanitizeEnv drops agent session variables from env.
func SanitizeEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, s := range sessionEnv {
			if name == s {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// ShellQuote quotes s for POSIX shells.
func ShellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:@%+=,", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
