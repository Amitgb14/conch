package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Amitgb14/conch/internal/adapter"
	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
	"github.com/Amitgb14/conch/internal/update"
)

// `conch doctor` answers "why isn't this working?" before anyone has to
// ask: each thing conch leans on — its config, its server, git, gh, the
// agents, the terminal — checked, and anything wrong said with the command
// that puts it right. It changes nothing and starts nothing: a doctor that
// started the server would hide the very problem somebody ran it for.

const doctorUsage = "usage: conch doctor [-json]"

var doctorVersion = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// Check outcomes.
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

// doctorChecks runs the checks side by side and lists them in a fixed
// order. Nothing but the agents depends on anything else, and one slow
// thing — a wedged server, a credential helper waiting on a keychain —
// should cost its own wait, not everybody's after it.
func doctorChecks() []doctorCheck {
	cfg, cfgCheck := doctorConfig()
	checks := []doctorCheck{
		{Name: "conch", Status: doctorOK, Detail: proto.Version + " · " + runtime.GOOS + "/" + runtime.GOARCH},
		cfgCheck,
		doctorSocket(),
		{}, {}, // server, agents
		{}, {}, // git, gh
		doctorTerminal(cfg),
		doctorMachines(),
	}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		server, c := doctorServer()
		checks[3], checks[4] = server, doctorAgents(c)
		if c != nil {
			c.Close()
		}
	}()
	go func() { defer wg.Done(); checks[5] = doctorGit() }()
	go func() { defer wg.Done(); checks[6] = doctorGH() }()
	wg.Wait()
	return checks
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
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return doctorCheck{Name: "server", Status: doctorWarn, Detail: "not running",
				Fix: "run conch, which starts it; agents keep running when conch closes"}, nil
		}
		// Something holds the socket and does not answer: a server stuck
		// mid-reload, or one that is not this person's. Starting conch would
		// clear the socket and every pane with it, so that is not the advice.
		return doctorCheck{Name: "server", Status: doctorFail,
			Detail: "the socket is there but the server does not answer: " + err.Error(),
			Fix:    "see " + config.ServerLogPath() + "; conch server stop ends it, and its panes"}, nil
	}
	s := c.Server
	detail := fmt.Sprintf("pid %d", s.PID)
	if !s.Started.IsZero() {
		detail += ", up " + doctorSince(time.Since(s.Started))
	}
	same, ok := update.SameBuild(s)
	missing := c.MissingCapabilities(proto.Capabilities)
	switch {
	case ok && !same && update.Compare(s.Version, proto.Version) > 0:
		// Reloading the server onto this binary would take it backwards.
		return doctorCheck{Name: "server", Status: doctorWarn,
			Detail: fmt.Sprintf("%s, on a newer conch (%s) than this one (%s)", detail, s.Version, proto.Version),
			Fix:    "run the newer conch, or conch update"}, c
	case ok && !same:
		what := fmt.Sprintf("another build (%s) than this conch (%s)", s.Version, proto.Version)
		if s.Version == proto.Version {
			what = "another build of " + s.Version + " than this conch"
		}
		return doctorCheck{Name: "server", Status: doctorWarn, Detail: detail + ", on " + what, Fix: doctorReloadFix(c)}, c
	case len(missing) > 0:
		return doctorCheck{Name: "server", Status: doctorWarn,
			Detail: fmt.Sprintf("%s, older than this conch: %s missing", detail, counted(len(missing), "feature")),
			Fix:    doctorReloadFix(c)}, c
	}
	return doctorCheck{Name: "server", Status: doctorOK, Detail: detail}, c
}

// doctorReloadFix moves the server onto this binary. A plain reload execs
// the server's own file again, which is no help when the new build is
// somewhere else (bin/conch beside ~/.local/bin/conch); a server too old to
// reload at all can only be restarted.
func doctorReloadFix(c *client.Client) string {
	if len(c.MissingCapabilities([]string{"server.reload.v1"})) > 0 {
		return "conch server stop, then conch  (this server cannot reload; its panes stop)"
	}
	fix := "conch server reload"
	if exe, err := os.Executable(); err == nil {
		fix += " -binary " + adapter.ShellQuote(exe)
	}
	return fix + "  (panes and agents keep running)"
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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := c.Call(ctx, proto.MethodAgentStatus, nil, &res); err != nil {
		var perr *proto.Error
		if errors.As(err, &perr) && perr.Code == proto.ErrUnknown {
			return doctorCheck{Name: "agents", Status: doctorWarn, Detail: "not checked: the server is too old to say (see server above)"}
		}
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
		fix := "conch agent install claude"
		if others := slices.DeleteFunc(slices.Clone(missing), func(n string) bool { return n == "claude" }); len(others) > 0 {
			fix += "  (or " + strings.Join(others, ", ") + ")"
		}
		return doctorCheck{Name: "agents", Status: doctorFail, Detail: "none installed; conch runs agents, so it needs one", Fix: fix}
	}
	detail := strings.Join(have, " · ")
	if len(missing) > 0 {
		detail += "  (not installed: " + strings.Join(missing, ", ") + ")"
	}
	return doctorCheck{Name: "agents", Status: doctorOK, Detail: detail}
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
	// git takes its identity from the environment before its config.
	name := cmp.Or(os.Getenv("GIT_AUTHOR_NAME"), os.Getenv("GIT_COMMITTER_NAME"))
	if name == "" {
		name, _ = doctorOutput(path, "config", "--get", "user.name")
	}
	email := cmp.Or(os.Getenv("GIT_AUTHOR_EMAIL"), os.Getenv("GIT_COMMITTER_EMAIL"), os.Getenv("EMAIL"))
	if email == "" {
		email, _ = doctorOutput(path, "config", "--get", "user.email")
	}
	// Both problems at once, so fixing one does not uncover the other.
	var problems, fixes []string
	if name == "" || email == "" {
		problems = append(problems, "no name or email to commit as")
		fixes = append(fixes, `git config --global user.name "Your Name" && git config --global user.email you@example.com`)
	}
	if version != "unknown version" && update.Compare(version, "2.38") < 0 {
		problems = append(problems, "telling which branches would conflict needs 2.38")
		fixes = append(fixes, "upgrade git")
	}
	if len(problems) > 0 {
		return doctorCheck{Name: "git", Status: doctorWarn,
			Detail: version + "; " + strings.Join(problems, "; "), Fix: strings.Join(fixes, "; ")}
	}
	return doctorCheck{Name: "git", Status: doctorOK, Detail: version + ", committing as " + name}
}

// doctorGH is optional — conch runs without it — but pull requests, CI
// results, issues and review threads all come through it. `gh auth token`
// is asked first rather than `auth status`, which reaches GitHub and would
// call an offline laptop signed out; the token itself is thrown away. A gh
// from before `auth token` (Ubuntu 22.04 packages 2.4) fails it whoever is
// signed in, so a failure is asked again the old way.
func doctorGH() doctorCheck {
	path, err := exec.LookPath(ghx.Binary())
	if err != nil {
		return doctorCheck{Name: "gh", Status: doctorWarn,
			Detail: "not found; pull requests, CI results and issues come through it",
			Fix:    "install the GitHub CLI: https://cli.github.com"}
	}
	out, _ := doctorOutput(path, "--version")
	version := doctorVersion.FindString(out)
	if _, err := doctorOutput(path, "auth", "token"); err != nil && !doctorRuns(path, "auth", "status") {
		return doctorCheck{Name: "gh", Status: doctorWarn, Detail: strings.TrimSpace(version + " not signed in"),
			Fix: "gh auth login"}
	}
	return doctorCheck{Name: "gh", Status: doctorOK, Detail: strings.TrimSpace(version + " signed in")}
}

// doctorRuns reports whether a program succeeds, saying nothing.
func doctorRuns(path string, args ...string) bool {
	_, err := doctorOutput(path, args...)
	return err == nil
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
// TUI will draw. Two things bite: no terminal at all, and a tmux prefix that
// is conch's too, so the first press goes to whichever is outside.
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
	if os.Getenv("TMUX") == "" {
		return doctorCheck{Name: "terminal", Status: doctorOK, Detail: detail}
	}
	detail += ", inside tmux"
	prefixes := tmuxPrefixes()
	whose := "whose prefix"
	if prefixes == nil {
		prefixes, whose = []string{"ctrl+b"}, "whose usual prefix" // tmux would not say
	}
	mine := strings.ToLower(cfg.Keys.Prefix)
	if !slices.Contains(prefixes, mine) {
		return doctorCheck{Name: "terminal", Status: doctorOK, Detail: detail}
	}
	free := "ctrl+a"
	for _, k := range []string{"ctrl+a", "ctrl+g", "ctrl+x"} {
		if !slices.Contains(prefixes, k) {
			free = k
			break
		}
	}
	return doctorCheck{Name: "terminal", Status: doctorWarn,
		Detail: fmt.Sprintf("%s, %s %s is conch's too", detail, whose, mine),
		Fix:    fmt.Sprintf(`set prefix = %q under [keys] in %s`, free, config.File())}
}

// tmuxPrefixes asks the tmux conch runs inside for its prefix keys, in
// conch's spelling (C-a is ctrl+a, M-a alt+a); nil when tmux cannot say.
func tmuxPrefixes() []string {
	path, err := exec.LookPath("tmux")
	if err != nil {
		return nil
	}
	var keys []string
	for _, opt := range []string{"prefix", "prefix2"} {
		out, err := doctorOutput(path, "show-options", "-gv", opt)
		if err != nil || out == "" || out == "None" {
			continue
		}
		key := strings.NewReplacer("C-", "ctrl+", "M-", "alt+", "S-", "shift+").Replace(out)
		keys = append(keys, strings.ToLower(key))
	}
	return keys
}

// doctorMachines counts the saved machines, never naming them, and checks
// for the ssh they are reached with. It does not dial them: one machine
// that is asleep would make the doctor hang, and conch machine ls exists.
func doctorMachines() doctorCheck {
	ms, err := remote.Machines()
	if err != nil {
		// The TUI would show none of them, with nothing said.
		return doctorCheck{Name: "machines", Status: doctorFail,
			Detail: remote.CatalogPath() + " cannot be read: " + err.Error(),
			Fix:    "fix or move that file aside; conch machine add puts machines back"}
	}
	n := 0
	for _, m := range ms {
		if m.Enabled {
			n++
		}
	}
	if n == 0 {
		return doctorCheck{Name: "machines", Status: doctorOK, Detail: "this computer only (conch machine add HOST adds one)"}
	}
	if _, err := exec.LookPath(remote.SSHBinary()); err != nil {
		return doctorCheck{Name: "machines", Status: doctorFail,
			Detail: fmt.Sprintf("%s saved, but no ssh to reach them", counted(n, "machine")),
			Fix:    "install OpenSSH's client"}
	}
	return doctorCheck{Name: "machines", Status: doctorOK,
		Detail: counted(n, "machine") + " saved (not dialled; conch -m NAME status reaches one)"}
}
