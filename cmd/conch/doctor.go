package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/update"
)

// `conch doctor` answers "why isn't this working?" before anyone has to
// ask: each thing conch leans on — its config, its server, git, gh, the
// agents, the terminal — checked, and anything wrong said with the command
// that puts it right. It changes nothing and starts nothing: a doctor that
// started the server would hide the very problem somebody ran it for.

const doctorUsage = "usage: conch doctor [-json]"

var doctorVersion = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// Check outcomes, worst last so the worst of a list is its maximum.
const (
	doctorOK   = "ok"
	doctorWarn = "warn" // works, but something is missing or out of date
	doctorFail = "fail" // conch cannot do what it is for until this is fixed
)

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the checks as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(doctorUsage)
	}
	if machineFlag != "" && machineFlag != "local" {
		// git, gh and the terminal here say nothing about that machine.
		return fmt.Errorf("conch doctor checks this computer, not %s: git, gh and the terminal here say nothing about it", machineFlag)
	}
	checks := doctorChecks()
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(checks); err != nil {
			return err
		}
	} else {
		fmt.Print(doctorText(checks))
	}
	for _, c := range checks {
		if c.Status == doctorFail {
			return exitStatus(1) // already said, line by line
		}
	}
	return nil
}

func doctorChecks() []doctorCheck {
	cfg, cfgCheck := doctorConfig()
	server, c := doctorServer()
	if c != nil {
		defer c.Close()
	}
	return []doctorCheck{
		{Name: "conch", Status: doctorOK, Detail: proto.Version + " · " + runtime.GOOS + "/" + runtime.GOARCH},
		cfgCheck,
		doctorSocket(),
		server,
		doctorAgents(c),
		doctorGit(),
		doctorGH(),
		doctorTerminal(cfg),
		doctorMachines(),
	}
}

// doctorText lays the checks out one to a line, the fix indented beneath,
// and ends with a count so a long list still has an answer at the bottom.
func doctorText(checks []doctorCheck) string {
	var b strings.Builder
	warns, fails := 0, 0
	for _, c := range checks {
		mark := "✓"
		switch c.Status {
		case doctorWarn:
			mark = "!"
			warns++
		case doctorFail:
			mark = "✗"
			fails++
		}
		fmt.Fprintf(&b, "%s %-9s %s\n", mark, c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Fprintf(&b, "  %-9s → %s\n", "", c.Fix)
		}
	}
	switch {
	case fails == 0 && warns == 0:
		b.WriteString("\nAll good.\n")
	case fails == 0:
		fmt.Fprintf(&b, "\nconch works; %s worth a look.\n", counted(warns, "thing"))
	default:
		fmt.Fprintf(&b, "\n%s to fix", counted(fails, "thing"))
		if warns > 0 {
			fmt.Fprintf(&b, ", %d more worth a look", warns)
		}
		b.WriteString(".\n")
	}
	return b.String()
}

// doctorConfig loads config.toml. A file that does not parse is a failure,
// not a shrug: conch runs on defaults then, and every setting somebody made
// is silently not in force.
func doctorConfig() (config.Config, doctorCheck) {
	path := config.File()
	cfg, err := config.Load()
	if err != nil {
		return config.Default(), doctorCheck{Name: "config", Status: doctorFail,
			Detail: fmt.Sprintf("%s does not parse, so none of it is in force: %v", path, err),
			Fix:    "fix that line, or move the file aside to start from defaults"}
	}
	if _, err := os.Stat(path); err != nil {
		return cfg, doctorCheck{Name: "config", Status: doctorOK, Detail: "defaults (no config.toml yet; settings is , in conch)"}
	}
	if problems := cfg.ActionProblems(); len(problems) > 0 {
		return cfg, doctorCheck{Name: "config", Status: doctorWarn,
			Detail: path + ": " + strings.Join(problems, "; "), Fix: "edit the [[actions]] it names"}
	}
	return cfg, doctorCheck{Name: "config", Status: doctorOK, Detail: path}
}

// socketLimit is the longest unix socket path the platform takes, without
// its terminating NUL: sun_path is 104 bytes on macOS and 108 on Linux.
func socketLimit() int {
	if runtime.GOOS == "darwin" {
		return 103
	}
	return 107
}

// doctorSocket catches a CONCH_HOME deep enough that the server cannot
// listen at all — which otherwise shows up as a server that never starts.
func doctorSocket() doctorCheck {
	sock := config.SocketPath()
	if n := len(sock); n > socketLimit() {
		return doctorCheck{Name: "socket", Status: doctorFail,
			Detail: fmt.Sprintf("%s is %d bytes; this system allows %d", sock, n, socketLimit()),
			Fix:    "set CONCH_HOME (or CONCH_SOCKET) to a shorter path"}
	}
	return doctorCheck{Name: "socket", Status: doctorOK, Detail: sock}
}

// doctorServer reaches the server without starting one, and says whether it
// is this conch: a server left on an older build is the usual reason a new
// feature "does nothing". The client is kept for the agents check.
func doctorServer() (doctorCheck, *client.Client) {
	c, err := client.Dial(config.SocketPath(), "conch-doctor")
	if err != nil {
		return doctorCheck{Name: "server", Status: doctorWarn, Detail: "not running",
			Fix: "run conch, which starts it; agents keep running when conch closes"}, nil
	}
	s := c.Server
	detail := fmt.Sprintf("pid %d", s.PID)
	if !s.Started.IsZero() {
		detail += ", up " + doctorSince(time.Since(s.Started))
	}
	if same, ok := update.SameBuild(s); ok && !same {
		return doctorCheck{Name: "server", Status: doctorWarn,
			Detail: fmt.Sprintf("%s, on another build (%s) than this conch (%s)", detail, s.Version, proto.Version),
			Fix:    "conch server reload  (panes and agents keep running)"}, c
	}
	if missing := c.MissingCapabilities(proto.Capabilities); len(missing) > 0 {
		return doctorCheck{Name: "server", Status: doctorWarn,
			Detail: fmt.Sprintf("%s, older than this conch: %s missing", detail, counted(len(missing), "feature")),
			Fix:    "conch server reload  (panes and agents keep running)"}, c
	}
	return doctorCheck{Name: "server", Status: doctorOK, Detail: detail}, c
}

// doctorSince is a duration as somebody would say it.
func doctorSince(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// doctorAgents asks the server, since agents run in its environment, not
// this shell's. With no server it says so rather than guessing: finding an
// agent means running it, and that is the server's to do.
func doctorAgents(c *client.Client) doctorCheck {
	if c == nil {
		return doctorCheck{Name: "agents", Status: doctorWarn, Detail: "not checked: the server finds them, and it is not running",
			Fix: "run conch, then conch doctor again"}
	}
	var res proto.AgentStatusResult
	if err := callFor(c, proto.MethodAgentStatus, nil, &res, 45*time.Second); err != nil {
		return doctorCheck{Name: "agents", Status: doctorWarn, Detail: "the server could not say: " + err.Error()}
	}
	var have, missing []string
	for _, a := range res.Agents {
		switch {
		case a.Installed && a.Version != "":
			have = append(have, a.Name+" "+a.Version)
		case a.Installed:
			have = append(have, a.Name)
		case a.Tier != proto.TierRunsHere: // a manifest of somebody's own is not a gap
			missing = append(missing, a.Name)
		}
	}
	if len(have) == 0 {
		return doctorCheck{Name: "agents", Status: doctorFail, Detail: "none installed; conch runs agents, so it needs one",
			Fix: "conch agent install claude  (or " + strings.Join(without(missing, "claude"), ", ") + ")"}
	}
	detail := strings.Join(have, " · ")
	if len(missing) > 0 {
		detail += "  (not installed: " + strings.Join(missing, ", ") + ")"
	}
	return doctorCheck{Name: "agents", Status: doctorOK, Detail: detail}
}

func without(list []string, drop string) []string {
	var out []string
	for _, s := range list {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

// doctorGit needs git itself, a version new enough for the overlap check,
// and an identity: conch commits on an agent's branch, and git refuses to
// commit for somebody it cannot name.
func doctorGit() doctorCheck {
	path, err := exec.LookPath("git")
	if err != nil {
		fix := "install git with your package manager"
		if runtime.GOOS == "darwin" {
			fix = "xcode-select --install"
		}
		return doctorCheck{Name: "git", Status: doctorFail,
			Detail: "not found; projects, branches and worktrees are all git's", Fix: fix}
	}
	out, _ := doctorOutput(path, "--version")
	version := doctorVersion.FindString(out)
	if version == "" {
		version = "unknown version"
	}
	name, _ := doctorOutput(path, "config", "--get", "user.name")
	email, _ := doctorOutput(path, "config", "--get", "user.email")
	if name == "" || email == "" {
		return doctorCheck{Name: "git", Status: doctorWarn,
			Detail: version + ", with no name or email to commit as",
			Fix:    `git config --global user.name "Your Name" && git config --global user.email you@example.com`}
	}
	if version != "unknown version" && update.Compare(version, "2.38") < 0 {
		return doctorCheck{Name: "git", Status: doctorWarn,
			Detail: version + "; telling which branches would conflict needs 2.38", Fix: "upgrade git"}
	}
	return doctorCheck{Name: "git", Status: doctorOK, Detail: version + ", committing as " + name}
}

// doctorGH is optional — conch runs without it — but pull requests, CI
// results, issues and review threads all come through it. `gh auth token`
// is asked rather than `auth status`, which reaches GitHub and would call
// an offline laptop signed out; the token itself is thrown away.
func doctorGH() doctorCheck {
	bin := os.Getenv("CONCH_GH")
	if bin == "" {
		bin = "gh"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return doctorCheck{Name: "gh", Status: doctorWarn,
			Detail: "not found; pull requests, CI results and issues come through it",
			Fix:    "install the GitHub CLI: https://cli.github.com"}
	}
	out, _ := doctorOutput(path, "--version")
	version := doctorVersion.FindString(out)
	if _, err := doctorOutput(path, "auth", "token"); err != nil {
		return doctorCheck{Name: "gh", Status: doctorWarn, Detail: strings.TrimSpace(version + " not signed in"),
			Fix: "gh auth login"}
	}
	return doctorCheck{Name: "gh", Status: doctorOK, Detail: strings.TrimSpace(version + " signed in")}
}

// doctorOutput runs a program briefly and returns its first line.
func doctorOutput(path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line), err
}

// doctorTerminal is about the terminal this is run in, which is where the
// TUI will draw. Two things bite: no terminal at all, and tmux, whose usual
// prefix is conch's too, so the first press goes to whichever is outside.
func doctorTerminal(cfg config.Config) doctorCheck {
	term := os.Getenv("TERM")
	if term == "" || term == "dumb" {
		return doctorCheck{Name: "terminal", Status: doctorFail,
			Detail: fmt.Sprintf("TERM is %q, which cannot draw conch", term),
			Fix:    "run conch in a terminal emulator, or set TERM=xterm-256color"}
	}
	detail := term
	if p := os.Getenv("TERM_PROGRAM"); p != "" {
		detail = p + ", " + term
	}
	switch strings.ToLower(os.Getenv("COLORTERM")) {
	case "truecolor", "24bit":
		detail += ", 24-bit colour"
	default:
		detail += ", 256 colours"
	}
	if os.Getenv("TMUX") != "" {
		detail += ", inside tmux"
		if strings.EqualFold(cfg.Keys.Prefix, "ctrl+b") {
			return doctorCheck{Name: "terminal", Status: doctorWarn,
				Detail: detail + ", whose usual prefix ctrl+b is conch's too",
				Fix:    `set prefix = "ctrl+a" under [keys] in ` + config.File()}
		}
	}
	return doctorCheck{Name: "terminal", Status: doctorOK, Detail: detail}
}

// doctorMachines counts the saved machines, never naming them, and checks
// for the ssh they are reached with. It does not dial them: one machine
// that is asleep would make the doctor hang, and conch machine ls exists.
func doctorMachines() doctorCheck {
	n := savedMachineCount()
	if n == 0 {
		return doctorCheck{Name: "machines", Status: doctorOK, Detail: "this computer only (conch machine add HOST adds one)"}
	}
	bin := os.Getenv("CONCH_SSH")
	if bin == "" {
		bin = "ssh"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return doctorCheck{Name: "machines", Status: doctorFail,
			Detail: fmt.Sprintf("%s saved, but no ssh to reach them", counted(n, "machine")),
			Fix:    "install OpenSSH's client"}
	}
	return doctorCheck{Name: "machines", Status: doctorOK,
		Detail: counted(n, "machine") + " saved (not dialled; conch -m NAME status reaches one)"}
}
