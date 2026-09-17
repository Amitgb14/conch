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
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Adapter launches one kind of agent.
type Adapter interface {
	// Name is the agent name used in manifests and the protocol.
	Name() string
	// Label is the name people know it by, e.g. "Claude Code".
	Label() string
	// Command returns the argv that starts the agent through shell, with
	// args (shell words typed by the user) appended.
	Command(shell, args string) []string
	// PromptArgs are the shell words that give the agent a first message
	// in its interactive session.
	PromptArgs(prompt string) string
	// ResumeArgs are the shell words that reopen a saved session; with id
	// "" the most recent one in the working directory.
	ResumeArgs(id string) string
	// Env is extra environment for the agent: conch's integration.
	Env() []string
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

// Registry holds the adapters available on this server, in display order.
type Registry []Adapter

// Get finds an adapter by name.
func (r Registry) Get(name string) (Adapter, bool) {
	for _, a := range r {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}

// New prepares every adapter. exe is the conch binary integrations call
// back into; dir is conch's config directory, where integration files are
// written.
func New(exe, dir string) (Registry, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	claude, err := newClaude(exe, dir)
	if err != nil {
		return nil, err
	}
	gemini, err := newGemini(exe, dir)
	if err != nil {
		return nil, err
	}
	opencode, err := newOpenCode(exe, dir)
	if err != nil {
		return nil, err
	}
	return Registry{claude, newCodex(), gemini, opencode, newDevin()}, nil
}

// cliAgent is an agent launched as a command in the user's login shell.
type cliAgent struct {
	name, label, binary string
	// dirs are where installers put the binary, searched before PATH: a
	// shell started before the install hasn't picked up PATH changes.
	dirs  []string
	flags string // conch's own flags, before the user's
	// promptFlag introduces a first message; "" passes it as an argument.
	promptFlag string
	// resume and resumeLast reopen a session by id (%s) or the latest.
	resume, resumeLast string
	env                []string
	install            string
}

func (a *cliAgent) Name() string  { return a.name }
func (a *cliAgent) Label() string { return a.label }
func (a *cliAgent) Env() []string { return a.env }

// ResumeArgs implements Adapter.
func (a *cliAgent) ResumeArgs(id string) string {
	if id == "" {
		return a.resumeLast
	}
	return fmt.Sprintf(a.resume, ShellQuote(id))
}

// PromptArgs implements Adapter.
func (a *cliAgent) PromptArgs(prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	if a.promptFlag != "" {
		return a.promptFlag + " " + ShellQuote(prompt)
	}
	return ShellQuote(prompt)
}
func (a *cliAgent) InstallScript() string { return a.install }

func (a *cliAgent) pathSetup() string {
	if len(a.dirs) == 0 {
		return ""
	}
	return `PATH="` + strings.Join(a.dirs, ":") + `:$PATH"; `
}

// Command implements Adapter. exec keeps the agent as the pane's foreground
// process.
func (a *cliAgent) Command(shell, args string) []string {
	line := a.pathSetup() + "exec " + a.binary
	if a.flags != "" {
		line += " " + a.flags
	}
	if args = strings.TrimSpace(args); args != "" {
		line += " " + args
	}
	return []string{shell, "-lc", line}
}

var versionRe = regexp.MustCompile(`\d+\.\d+(\.\d+)?[0-9A-Za-z.+-]*`)

// Detect implements Adapter.
func (a *cliAgent) Detect(ctx context.Context, shell string) Availability {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	script := a.pathSetup() + `p=$(command -v ` + a.binary + `) || exit 1; echo "$p"; ` + a.binary + ` --version 2>/dev/null | head -1`
	cmd := exec.CommandContext(ctx, shell, "-lc", script)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return Availability{}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	av := Availability{Installed: true, Path: strings.TrimSpace(lines[0])}
	if len(lines) > 1 {
		av.Version = versionRe.FindString(lines[len(lines)-1])
	}
	return av
}

// withDownloader wraps an install command that pipes a script from a URL
// into a shell, checking for curl or wget first.
func withDownloader(label, url, shell string, env string) string {
	run := strings.TrimSpace(env + " " + shell) // env may be empty
	return fmt.Sprintf(`set -e
echo "Installing %[1]s with the official installer (%[2]s)"
if command -v curl >/dev/null 2>&1; then
  curl -fsSL %[2]s | %[3]s
elif command -v wget >/dev/null 2>&1; then
  wget -qO- %[2]s | %[3]s
else
  echo "the installer needs curl or wget" >&2; exit 1
fi`, label, url, run)
}

// ---- Claude Code ----

// ClaudeHookEvents are the Claude Code hook events conch listens to.
var ClaudeHookEvents = []string{
	"SessionStart", "SessionEnd",
	"UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
	"PermissionRequest", "PermissionDenied", "Notification",
	"SubagentStart", "SubagentStop", "PreCompact",
	"Stop", "StopFailure",
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type hookGroup struct {
	Hooks []hookCommand `json:"hooks"`
}

// newClaude launches Claude Code with conch's hooks loaded via --settings,
// which leaves the user's own settings files untouched.
func newClaude(exe, dir string) (*cliAgent, error) {
	hooks := map[string][]hookGroup{}
	for _, ev := range ClaudeHookEvents {
		hooks[ev] = []hookGroup{{Hooks: []hookCommand{{Type: "command", Command: ShellQuote(exe) + " report claude-hook", Timeout: 5}}}}
	}
	path := filepath.Join(dir, "claude-settings.json")
	// The status line is how Claude shares plan limits and the context
	// window. conch's command reports them, then runs the user's own status
	// line (if any) so it looks as before.
	statusLine := map[string]any{"type": "command", "command": ShellQuote(exe) + " report claude-status"}
	if err := writeJSON(path, map[string]any{"hooks": hooks, "statusLine": statusLine}); err != nil {
		return nil, fmt.Errorf("write claude settings: %w", err)
	}
	return &cliAgent{
		name: "claude", label: "Claude Code", binary: "claude",
		resume: "--resume %s", resumeLast: "--continue",
		dirs:    []string{"$HOME/.local/bin"},
		flags:   "--settings " + ShellQuote(path),
		install: claudeInstall,
	}, nil
}

// claudeInstall runs Anthropic's native installer, which installs
// ~/.local/bin/claude for the current user (no root). Alpine needs a few
// system packages first, which only root can add, so say what to run.
var claudeInstall = `if [ -f /etc/alpine-release ] && ! apk info -e libstdc++ >/dev/null 2>&1; then
  echo "Alpine needs packages first; as root run: apk add bash curl libgcc libstdc++ ripgrep" >&2
  exit 1
fi
if ! command -v bash >/dev/null 2>&1; then echo "the installer needs bash" >&2; exit 1; fi
` + withDownloader("Claude Code", "https://claude.ai/install.sh", "bash", "") + `
echo
echo "Installed: $("$HOME/.local/bin/claude" --version)"
echo "Start it from conch and log in: open the link it shows on any computer and paste the code back."`

// ---- Codex ----

// newCodex launches OpenAI's Codex CLI. Its per-launch hooks need a trust
// bypass and its notify setting would replace the user's own, so conch
// injects nothing and reads the terminal title and screen instead.
func newCodex() *cliAgent {
	return &cliAgent{
		name: "codex", label: "Codex", binary: "codex",
		resume: "resume %s", resumeLast: "resume --last",
		dirs: []string{"$HOME/.local/bin"},
		install: withDownloader("Codex", "https://chatgpt.com/codex/install.sh", "sh", "CODEX_NON_INTERACTIVE=1") + `
echo
echo "Installed: $("$HOME/.local/bin/codex" --version)"
echo "Start it from conch and sign in with ChatGPT or an API key."`,
	}
}

// ---- Devin for Terminal ----

// newDevin launches Devin for Terminal, Cognition's `devin` CLI
// (docs.devin.ai/cli). A first message goes after `--`, so it isn't taken
// for a subcommand; sessions resume by id or name with -r, or the latest in
// the folder with -c. Its hooks follow Claude Code's shape, but conch
// doesn't pass its own yet: the per-launch --config flag isn't documented
// to merge with the user's config, and replacing that would drop their
// settings. Until that's checked against the real CLI, conch reads Devin's
// state from the process and the screen.
func newDevin() *cliAgent {
	return &cliAgent{
		name: "devin", label: "Devin", binary: "devin",
		promptFlag: "--",
		resume:     "-r %s", resumeLast: "-c",
		dirs: []string{"$HOME/.local/bin"},
		install: withDownloader("Devin", "https://cli.devin.ai/install.sh", "bash", "") + `
echo
echo "Installed: $("$HOME/.local/bin/devin" --version)"
echo "Sign in with devin auth login, or start it from conch and follow its prompt."`,
	}
}

// ---- Gemini CLI ----

// GeminiHookEvents are the Gemini CLI hook events conch listens to.
var GeminiHookEvents = []string{
	"SessionStart", "SessionEnd", "BeforeAgent", "AfterAgent",
	"BeforeTool", "AfterTool", "Notification", "PreCompress",
}

// newGemini launches Gemini CLI with conch's hooks in a system-defaults
// settings file: the lowest-precedence layer, whose hook lists are
// concatenated with the user's. Gemini only runs hooks in folders the user
// trusts; conch never bypasses that, and falls back to the title and
// screen elsewhere.
func newGemini(exe, dir string) (*cliAgent, error) {
	hooks := map[string][]hookGroup{}
	for _, ev := range GeminiHookEvents {
		hooks[ev] = []hookGroup{{Hooks: []hookCommand{{Type: "command", Command: ShellQuote(exe) + " report gemini-hook", Timeout: 5000}}}}
	}
	path := filepath.Join(dir, "gemini-defaults.json")
	if err := writeJSON(path, map[string]any{"hooks": hooks}); err != nil {
		return nil, fmt.Errorf("write gemini defaults: %w", err)
	}
	a := &cliAgent{
		name: "gemini", label: "Gemini CLI", binary: "gemini",
		resume: "--resume %s", resumeLast: "--resume latest",
		dirs: []string{"$HOME/.local/bin"},
		install: `set -e
echo "Installing Gemini CLI with npm (@google/gemini-cli) into ~/.local"
if ! command -v npm >/dev/null 2>&1; then echo "Gemini CLI needs Node.js 20 or newer (nodejs.org)" >&2; exit 1; fi
major=$(node -p 'process.versions.node.split(".")[0]')
if [ "$major" -lt 20 ]; then echo "Gemini CLI needs Node.js 20 or newer; this is $(node --version)" >&2; exit 1; fi
npm install -g --prefix "$HOME/.local" @google/gemini-cli
echo
echo "Installed: $("$HOME/.local/bin/gemini" --version)"
echo "Start it from conch and sign in."`,
	}
	if os.Getenv("GEMINI_CLI_SYSTEM_DEFAULTS_PATH") == "" { // don't displace an admin's file
		a.env = []string{"GEMINI_CLI_SYSTEM_DEFAULTS_PATH=" + path}
	}
	return a, nil
}

// ---- OpenCode ----

// openCodePlugin reports OpenCode's session events to conch. It runs inside
// OpenCode and must never disturb it.
const openCodePlugin = `// Written by conch: reports OpenCode session state to the conch pane it runs in.
import { spawn } from "node:child_process";

const conch = %s;
const forwarded = new Set(["session.idle", "session.error", "permission.asked", "permission.replied",
  "question.asked", "question.replied", "question.rejected"]);

function report(event) {
  if (!process.env.CONCH_PANE_ID) return;
  try {
    const child = spawn(conch, ["report", "opencode", event], { stdio: "ignore", detached: true });
    child.on("error", () => {});
    child.unref();
  } catch {}
}

export const ConchPlugin = async () => ({
  event: async ({ event }) => {
    if (event.type === "session.status") {
      const status = event.properties?.status?.type;
      if (status) report("session." + status);
    } else if (forwarded.has(event.type)) {
      report(event.type);
    }
  },
});
`

// newOpenCode launches OpenCode with conch's plugin added through
// OPENCODE_CONFIG_CONTENT, which is merged into the user's configuration
// (plugin lists are combined).
func newOpenCode(exe, dir string) (*cliAgent, error) {
	exeJSON, _ := json.Marshal(exe)
	path := filepath.Join(dir, "opencode-conch.js")
	if err := writeFileAtomic(path, []byte(fmt.Sprintf(openCodePlugin, exeJSON))); err != nil {
		return nil, fmt.Errorf("write opencode plugin: %w", err)
	}
	a := &cliAgent{
		name: "opencode", label: "OpenCode", binary: "opencode",
		resume: "--session %s", resumeLast: "--continue",
		promptFlag: "--prompt", // a bare argument is a project path
		dirs:       []string{"$HOME/.opencode/bin", "$HOME/bin", "$HOME/.local/bin"},
		install: withDownloader("OpenCode", "https://opencode.ai/install", "bash", "") + `
echo
echo "Installed OpenCode; start it from conch and connect a provider (/connect)."`,
	}
	if os.Getenv("OPENCODE_CONFIG_CONTENT") == "" { // don't displace the user's own
		cfg, _ := json.Marshal(map[string]any{"plugin": []string{"file://" + path}})
		a.env = []string{"OPENCODE_CONFIG_CONTENT=" + string(cfg)}
	}
	return a, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(b, '\n'))
}

// defaultTitles are titles agents show when they have no task yet.
var defaultTitles = map[string]bool{"claude code": true, "claude": true, "opencode": true, "gemini cli": true}

// statusPrefix matches the activity agents put at the start of their
// titles: Codex's "[ ! ] Action Required", Gemini's "✦  Working… (folder)".
var statusPrefix = regexp.MustCompile(`^\s*(\[ [!.] \] Action Required|✋\s*Action Required|[✦⏲]\s*Working…|◇\s*Ready)(\s*\([^)]*\))?`)

// CleanTitle turns a terminal title into a task label: activity prefixes,
// status symbols (spinners, ✳) and OpenCode's "OC | " are dropped, and
// titles that only name the agent become "".
func CleanTitle(title string) string {
	title = statusPrefix.ReplaceAllString(title, "")
	title = strings.TrimLeftFunc(title, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	title = strings.TrimSpace(strings.TrimPrefix(title, "OC |"))
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
