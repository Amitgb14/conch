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
	"github.com/Amitgb14/conch/internal/doctor"
	"github.com/Amitgb14/conch/internal/ghx"
	"github.com/Amitgb14/conch/internal/keymap"
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
	checks := doctorChecks(true)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(checks); err != nil {
			return err
		}
	} else {
		fmt.Print(doctor.Text(checks))
	}
	for _, c := range checks {
		if c.Status == doctor.Fail {
			return exitStatus(1) // already said, line by line
		}
	}
	return nil
}

// doctorChecks runs the checks side by side and lists them in a fixed
// order. Nothing but the agents depends on anything else, and one slow
// thing — a wedged server, a credential helper waiting on a keychain —
// should cost its own wait, not everybody's after it.
//
// ask is whether the terminal may be asked what it can do, which only the
// command may do: the TUI owns stdin while it runs, and a reply would
// arrive there as keystrokes (see doctorterm.go).
func doctorChecks(ask bool) []doctor.Check {
	cfg, cfgCheck := doctorConfig()
	checks := []doctor.Check{
		{Name: "conch", Status: doctor.OK, Detail: proto.Version + " · " + runtime.GOOS + "/" + runtime.GOARCH},
		cfgCheck,
		doctorKeys(cfg),
		doctorSocket(),
		{}, {}, // server, agents
		{}, {}, // git, gh
		{}, // terminal
		doctorMachines(),
	}
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		var said termAnswers
		if ask {
			said = askTerminal(termAskTime)
		}
		checks[8] = doctorTerminal(cfg, said)
	}()
	go func() {
		defer wg.Done()
		server, c := doctorServer()
		checks[4], checks[5] = server, doctorAgents(c)
		if c != nil {
			c.Close()
		}
	}()
	go func() { defer wg.Done(); checks[6] = doctorGit() }()
	go func() { defer wg.Done(); checks[7] = doctorGH() }()
	wg.Wait()
	return checks
}

// doctorKeys is what [keys.bind] came to: a table with a mistake in it is
// a warning and not a failure, since conch runs on the keys it could make
// sense of — but it is said here as well as on start-up, because a flash
// fades and a key that does nothing does not.
func doctorKeys(cfg config.Config) doctor.Check {
	binds, problems := keymap.Resolve(cfg.Keys.Bind)
	if len(problems) > 0 {
		return doctor.Check{Name: "keys", Status: doctor.Warn,
			Detail: config.File() + ": " + strings.Join(problems, "; "),
			Fix:    "edit [keys.bind] there; conch keys lists every name and what it is on"}
	}
	moved, gone := 0, 0
	for _, a := range keymap.Actions() {
		switch {
		case !binds.Bound(a):
			gone++
		case binds.Rebound(a):
			moved++
		}
	}
	detail := fmt.Sprintf("%s, then %d keys", cfg.Keys.Prefix, len(keymap.Actions()))
	if moved+gone > 0 {
		detail += fmt.Sprintf(" · %d rebound, %d unbound (conch keys)", moved, gone)
	}
	return doctor.Check{Name: "keys", Status: doctor.OK, Detail: detail}
}

// doctorConfig loads config.toml. A file that does not parse is a failure,
// not a shrug: conch runs on defaults then, and every setting somebody made
// is silently not in force.
func doctorConfig() (config.Config, doctor.Check) {
	path := config.File()
	cfg, err := config.Load()
	if err != nil {
		return config.Default(), doctor.Check{Name: "config", Status: doctor.Fail,
			Detail: fmt.Sprintf("%s does not parse, so none of it is in force: %v", path, err),
			Fix:    "fix that line, or move the file aside to start from defaults"}
	}
	if _, err := os.Stat(path); err != nil {
		return cfg, doctor.Check{Name: "config", Status: doctor.OK, Detail: "defaults (no config.toml yet; settings is , in conch)"}
	}
	if problems := cfg.ActionProblems(); len(problems) > 0 {
		return cfg, doctor.Check{Name: "config", Status: doctor.Warn,
			Detail: path + ": " + strings.Join(problems, "; "), Fix: "edit the [[actions]] it names"}
	}
	return cfg, doctor.Check{Name: "config", Status: doctor.OK, Detail: path}
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
func doctorSocket() doctor.Check {
	sock := config.SocketPath()
	if n := len(sock); n > socketLimit() {
		return doctor.Check{Name: "socket", Status: doctor.Fail,
			Detail: fmt.Sprintf("%s is %d bytes; this system allows %d", sock, n, socketLimit()),
			Fix:    "set CONCH_HOME (or CONCH_SOCKET) to a shorter path"}
	}
	return doctor.Check{Name: "socket", Status: doctor.OK, Detail: sock}
}

// doctorServer reaches the server without starting one, and says whether it
// is this conch: a server left on an older build is the usual reason a new
// feature "does nothing". The client is kept for the agents check.
func doctorServer() (doctor.Check, *client.Client) {
	c, err := client.Dial(config.SocketPath(), "conch-doctor")
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return doctor.Check{Name: "server", Status: doctor.Warn, Detail: "not running",
				Fix: "run conch, which starts it; agents keep running when conch closes"}, nil
		}
		// Something holds the socket and does not answer: a server stuck
		// mid-reload, or one that is not this person's. Starting conch would
		// clear the socket and every pane with it, so that is not the advice.
		return doctor.Check{Name: "server", Status: doctor.Fail,
			Detail: "the socket is there but the server does not answer: " + err.Error(),
			Fix:    "see " + config.ServerLogPath() + "; conch server stop ends it, and its panes"}, nil
	}
	s := c.Server
	detail := fmt.Sprintf("pid %d", s.PID)
	if !s.Started.IsZero() {
		detail += ", up " + doctorSince(time.Since(s.Started))
	}
	same, ok := update.SameBuild(s)
	missing := c.OlderBuild()
	switch {
	case ok && !same && update.Compare(s.Version, proto.Version) > 0:
		// Reloading the server onto this binary would take it backwards.
		return doctor.Check{Name: "server", Status: doctor.Warn,
			Detail: fmt.Sprintf("%s, on a newer conch (%s) than this one (%s)", detail, s.Version, proto.Version),
			Fix:    "run the newer conch, or conch update"}, c
	case ok && !same:
		what := fmt.Sprintf("another build (%s) than this conch (%s)", s.Version, proto.Version)
		if s.Version == proto.Version {
			what = "another build of " + s.Version + " than this conch"
		}
		return doctor.Check{Name: "server", Status: doctor.Warn, Detail: detail + ", on " + what, Fix: doctorReloadFix(c)}, c
	case len(missing) > 0:
		return doctor.Check{Name: "server", Status: doctor.Warn,
			Detail: fmt.Sprintf("%s, older than this conch: %s missing", detail, counted(len(missing), "feature")),
			Fix:    doctorReloadFix(c)}, c
	}
	return doctor.Check{Name: "server", Status: doctor.OK, Detail: detail}, c
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
func doctorAgents(c *client.Client) doctor.Check {
	if c == nil {
		return doctor.Check{Name: "agents", Status: doctor.Warn, Detail: "not checked: the server finds them, and it is not running",
			Fix: "run conch, then conch doctor again"}
	}
	var res proto.AgentStatusResult
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := c.Call(ctx, proto.MethodAgentStatus, nil, &res); err != nil {
		var perr *proto.Error
		if errors.As(err, &perr) && perr.Code == proto.ErrUnknown {
			return doctor.Check{Name: "agents", Status: doctor.Warn, Detail: "not checked: the server is too old to say (see server above)"}
		}
		return doctor.Check{Name: "agents", Status: doctor.Warn, Detail: "the server could not say: " + err.Error()}
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
		return doctor.Check{Name: "agents", Status: doctor.Fail, Detail: "none installed; conch runs agents, so it needs one", Fix: fix}
	}
	detail := strings.Join(have, " · ")
	if len(missing) > 0 {
		detail += "  (not installed: " + strings.Join(missing, ", ") + ")"
	}
	return doctor.Check{Name: "agents", Status: doctor.OK, Detail: detail}
}

// doctorGit needs git itself, a version new enough for the overlap check,
// and an identity: conch commits on an agent's branch, and git refuses to
// commit for somebody it cannot name.
func doctorGit() doctor.Check {
	path, err := exec.LookPath("git")
	if err != nil {
		fix := "install git with your package manager"
		if runtime.GOOS == "darwin" {
			fix = "xcode-select --install"
		}
		return doctor.Check{Name: "git", Status: doctor.Fail,
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
		return doctor.Check{Name: "git", Status: doctor.Warn,
			Detail: version + "; " + strings.Join(problems, "; "), Fix: strings.Join(fixes, "; ")}
	}
	return doctor.Check{Name: "git", Status: doctor.OK, Detail: version + ", committing as " + name}
}

// doctorGH is optional — conch runs without it — but pull requests, CI
// results, issues and review threads all come through it. `gh auth token`
// is asked first rather than `auth status`, which reaches GitHub and would
// call an offline laptop signed out; the token itself is thrown away. A gh
// from before `auth token` (Ubuntu 22.04 packages 2.4) fails it whoever is
// signed in, so a failure is asked again the old way.
func doctorGH() doctor.Check {
	path, err := exec.LookPath(ghx.Binary())
	if err != nil {
		return doctor.Check{Name: "gh", Status: doctor.Warn,
			Detail: "not found; pull requests, CI results and issues come through it",
			Fix:    "install the GitHub CLI: https://cli.github.com"}
	}
	out, _ := doctorOutput(path, "--version")
	version := doctorVersion.FindString(out)
	if _, err := doctorOutput(path, "auth", "token"); err != nil && !doctorRuns(path, "auth", "status") {
		return doctor.Check{Name: "gh", Status: doctor.Warn, Detail: strings.TrimSpace(version + " not signed in"),
			Fix: "gh auth login"}
	}
	return doctor.Check{Name: "gh", Status: doctor.OK, Detail: strings.TrimSpace(version + " signed in")}
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
// TUI will draw. What bites: no terminal at all; a terminal whose terminfo
// is not installed, so everything curses draws is a guess; a terminal run
// as plain xterm although it has an entry of its own; a mouse conch cannot
// read; and a tmux prefix that is conch's too, so the first press goes to
// whichever is outside.
//
// said is what the terminal answered when asked (termAnswers{} when nobody
// asked — the TUI never does). A finding is only made from what it said,
// never from its silence: a terminal that does not answer mode queries is
// not a terminal without the modes.
func doctorTerminal(cfg config.Config, said termAnswers) doctor.Check {
	term := os.Getenv("TERM")
	if term == "" || term == "dumb" {
		return doctor.Check{Name: "terminal", Status: doctor.Fail,
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
	if said.name != "" {
		detail += ", says it is " + said.name
	}
	if os.Getenv("CONCH_PANE_ID") != "" {
		// Then the terminal answering is conch's own emulator, not the
		// window somebody is looking at: worth saying, or a finding here
		// reads as being about their terminal.
		detail += ", in a conch pane"
	}
	if said.has(modeSyncUpdate) {
		detail += ", synchronized updates"
	}

	// Findings, worst first; the first one's fix is the one offered, and
	// every one of them is said in the detail, so nothing is hidden by
	// there being only one Fix.
	var notes, fixes []string
	note := func(what, fix string) {
		notes, fixes = append(notes, what), append(fixes, fix)
	}
	if missing, fix := terminfoMissing(term); missing != "" {
		note(missing, fix)
	}
	if want := terminfoName(said.name); want != "" && !strings.Contains(term, want) && os.Getenv("TMUX") == "" {
		word, _, _ := strings.Cut(said.name, " ")
		note(fmt.Sprintf("TERM does not name %s, so its own keys and colours are off", word),
			fmt.Sprintf("set TERM to its own entry (%s's is usually %s) in the terminal's settings, and install that terminfo where you ssh to", word, "xterm-"+want))
	}
	if said.lacks(modeSGRMouse) {
		note("no SGR mouse reports, so clicks and drags do not reach conch",
			"turn the mouse on in the terminal's settings, or use a terminal that reports it (conch is clicked as much as typed)")
	}
	if said.lacks(modeBracketedPaste) {
		note("no bracketed paste, so a pasted prompt is typed line by line into an agent",
			"turn bracketed paste on in the terminal's settings")
	}
	if os.Getenv("TMUX") != "" {
		detail += ", inside tmux"
		prefixes := tmuxPrefixes()
		whose := "whose prefix"
		if prefixes == nil {
			prefixes, whose = []string{"ctrl+b"}, "whose usual prefix" // tmux would not say
		}
		mine := strings.ToLower(cfg.Keys.Prefix)
		if slices.Contains(prefixes, mine) {
			free := "ctrl+a"
			for _, k := range []string{"ctrl+a", "ctrl+g", "ctrl+x"} {
				if !slices.Contains(prefixes, k) {
					free = k
					break
				}
			}
			note(fmt.Sprintf("%s %s is conch's too", whose, mine),
				fmt.Sprintf(`set prefix = %q under [keys] in %s`, free, config.File()))
		}
	}
	if len(notes) == 0 {
		return doctor.Check{Name: "terminal", Status: doctor.OK, Detail: detail}
	}
	return doctor.Check{Name: "terminal", Status: doctor.Warn,
		Detail: detail + ", " + strings.Join(notes, ", "), Fix: fixes[0]}
}

// terminfoMissing says why TERM is no good here, and how to fix it, or ""
// when it is fine or cannot be told. A terminal that sets TERM to an entry
// this computer does not have is the quiet failure worth catching: conch
// itself draws anyway, since it writes the escapes it needs, but ncurses
// programs — vim, less, an agent's own full-screen interface — read
// terminfo and fall back or refuse. Installing a terminal and not its
// terminfo is how that happens, and ssh carries TERM to machines that have
// never heard of it.
func terminfoMissing(term string) (why, fix string) {
	if has, known := terminfoHas(term); !known || has {
		return "", ""
	}
	return fmt.Sprintf("no terminfo entry for %s here, so curses programs (vim, less, an agent's own screen) fall back", term),
		"install the terminal's terminfo (its own docs say how; `tic` takes the file), or set TERM=xterm-256color for now"
}

// terminfoHas reports whether this computer describes a terminal, and
// whether anything here could say: without infocmp nothing can, and a
// guess either way would be worse than the question going unanswered —
// "no entry" is advice to go and install one.
func terminfoHas(term string) (has, known bool) {
	return terminfoHasIn("", term)
}

// terminfoHasIn is the same question asked of one terminfo directory: a
// terminal may carry its own and point TERMINFO at it for what it starts
// (macOS Ghostty keeps one in its bundle), so "this computer has no entry"
// and "nothing in the window will find one" are different questions, and
// only the second is a reason to hold TERM back.
func terminfoHasIn(dir, term string) (has, known bool) {
	path, err := exec.LookPath("infocmp")
	if err != nil {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-1", term)
	cmd.WaitDelay = time.Second
	if dir != "" {
		cmd.Env = append(os.Environ(), "TERMINFO="+dir)
	}
	return cmd.Run() == nil, true
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
func doctorMachines() doctor.Check {
	ms, err := remote.Machines()
	if err != nil {
		// The TUI would show none of them, with nothing said.
		return doctor.Check{Name: "machines", Status: doctor.Fail,
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
		return doctor.Check{Name: "machines", Status: doctor.OK, Detail: "this computer only (conch machine add HOST adds one)"}
	}
	if _, err := exec.LookPath(remote.SSHBinary()); err != nil {
		return doctor.Check{Name: "machines", Status: doctor.Fail,
			Detail: fmt.Sprintf("%s saved, but no ssh to reach them", counted(n, "machine")),
			Fix:    "install OpenSSH's client"}
	}
	return doctor.Check{Name: "machines", Status: doctor.OK,
		Detail: counted(n, "machine") + " saved (not dialled; conch -m NAME status reaches one)"}
}
