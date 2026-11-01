package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Amitgb14/conch/internal/config"
)

// `conch app` opens conch in a terminal of its own, configured by conch,
// and puts it in the desktop's applications with its own icon — so conch
// is started like an app rather than typed into whatever terminal is to
// hand.
//
// What it is for is the terminal nobody has configured. conch draws
// itself and asks the terminal for very little, but that little has to
// work: a mouse it can read, a terminfo entry that exists, colours. A
// terminal somebody installed and never set up (or set up years ago, for
// something else) is where conch looks broken through no fault of its
// own, and the fix was always "go and fix your terminal".
//
// So conch writes a configuration of its own and starts the terminal with
// **only** that one: every terminal here is started in a way that leaves
// the user's own config unread (`--config-default-files=false` for
// ghostty, `--config` for kitty, `-c` for foot, `--config-file` for
// alacritty). That is the whole trick, and it is why the file conch writes
// is nearly empty: terminal defaults are fine, and what conch needs is for
// nothing broken to be read on top of them.
//
// It is not a window of conch's own. conch is a terminal program, and
// this starts a terminal — the one thing it must not grow is an interface
// of its own, which would then compete both with conch's and with the
// terminal it is standing in for.

const appUsage = `usage: conch app [-terminal NAME] [-n]
       conch app ls | install | remove`

// appTerminal is a terminal conch knows how to start on its own terms.
type appTerminal struct {
	name string // the binary, and the name to pass to -terminal
	// terminfo is the entry this terminal sets TERM to when it has one of
	// its own, so conch can tell whether that entry is installed here. A
	// terminal that runs as plain xterm leaves it empty.
	terminfo string
	// file is what conch's configuration for it is called. The extension
	// matters to some of them.
	file string
	// settings writes that configuration. fallback is whether TERM has to
	// be held back to xterm-256color because the terminal's own entry is
	// not installed; local is conch's own file for somebody's additions,
	// included when it exists.
	settings func(fallback bool, local string) string
	// args is how the terminal is started: the binary, conch's config and
	// the command to run in it, in that terminal's spelling.
	args func(bin, cfg, conch string) []string
	// bundle is set for a terminal that cannot be started from the
	// command line on macOS and has to be opened as an application
	// bundle instead.
	bundle bool
	// app is the application bundle's name on macOS, where a terminal is
	// an application and not a program on PATH: ghostty puts its own
	// directory on PATH only for the shells *it* starts, so a shell
	// started by anything else — a conch pane, launchd, another
	// terminal — cannot see it, and looking only on PATH said a terminal
	// sitting in /Applications was not installed. Empty for a terminal
	// with no bundle (foot is Wayland's).
	app string
}

// appTerminals are the terminals conch will start, in the order it picks
// one: ghostty and kitty first because they do everything conch asks of a
// terminal (an addressable mouse, OSC 52 clipboard, OSC 22 pointer
// shapes, synchronized updates), then foot and alacritty.
//
// Only the way each one is *started* is conch's business here. Deliberately
// absent: a terminal with no way to be given a config file of its own, and
// one that cannot be told to leave the user's own config unread — starting
// it would carry the very configuration this is here to step around.
var appTerminals = []appTerminal{{
	name:     "ghostty",
	app:      "Ghostty.app",
	terminfo: "xterm-ghostty",
	file:     "ghostty.conf",
	bundle:   true, // "launching the terminal emulator from the CLI is not supported" on macOS
	settings: func(fallback bool, local string) string {
		var b strings.Builder
		// A running ghostty would otherwise make the window itself, from
		// the config it was started with rather than this one.
		b.WriteString("gtk-single-instance = false\n")
		// macOS needs `open -na`, which starts a *separate* instance of
		// ghostty — and a macOS application does not stop when its last
		// window closes, so that instance would sit in the Dock with
		// nothing in it after the conch window was closed, one more for
		// every launch. This is an instance conch made for one window,
		// so it goes when the window does.
		b.WriteString("quit-after-last-window-closed = true\n")
		if fallback {
			b.WriteString("term = xterm-256color\n")
		}
		if local != "" {
			fmt.Fprintf(&b, "config-file = %s\n", local)
		}
		return b.String()
	},
	args: func(bin, cfg, conch string) []string {
		return []string{bin, "--config-default-files=false", "--config-file=" + cfg, "-e", conch}
	},
}, {
	name:     "kitty",
	app:      "kitty.app",
	terminfo: "xterm-kitty",
	file:     "kitty.conf",
	settings: func(fallback bool, local string) string {
		var b strings.Builder
		if fallback {
			b.WriteString("term xterm-256color\n")
		}
		if local != "" {
			fmt.Fprintf(&b, "include %s\n", local)
		}
		return b.String()
	},
	args: func(bin, cfg, conch string) []string {
		return []string{bin, "--config", cfg, conch}
	},
}, {
	name:     "foot",
	terminfo: "foot",
	file:     "foot.ini",
	settings: func(fallback bool, local string) string {
		var b strings.Builder
		if local != "" {
			fmt.Fprintf(&b, "include=%s\n", local)
		}
		if fallback {
			b.WriteString("[main]\nterm=xterm-256color\n")
		}
		return b.String()
	},
	args: func(bin, cfg, conch string) []string {
		return []string{bin, "-c", cfg, conch}
	},
}, {
	name:     "alacritty",
	app:      "Alacritty.app",
	terminfo: "alacritty",
	file:     "alacritty.toml",
	settings: func(fallback bool, local string) string {
		var b strings.Builder
		if local != "" {
			fmt.Fprintf(&b, "[general]\nimport = [%q]\n", local)
		}
		if fallback {
			b.WriteString("[env]\nTERM = \"xterm-256color\"\n")
		}
		return b.String()
	},
	args: func(bin, cfg, conch string) []string {
		return []string{bin, "--config-file", cfg, "-e", conch}
	},
}}

// startApp is the launch, behind a variable so a test can take it: this
// one opens a window on somebody's desktop.
var startApp = func(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	// Nothing of the shell's: a terminal of its own, which outlives the
	// command that started it.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.Env = appEnviron(os.Environ())
	return cmd.Start()
}

// appEnviron is the environment the terminal is started with: this one,
// without the pane conch is running in.
//
// Most people will run this from inside conch, since that is where they
// are. CONCH_PANE_ID says "this process is in that pane", a pane passes it
// to everything it starts, and a conch that sees it beside a socket
// refuses to draw a TUI (nestedPane) — rightly, for a program *in* the
// pane. The window conch opens here is not in it: it is another terminal,
// on its own pty, and the only thing the inherited id would do is put an
// error on the screen of the window somebody just asked for.
//
// CONCH_SOCKET stays. That is which server to talk to, and the new window
// wanting the same one — the same panes, the same agents — is the point.
func appEnviron(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "CONCH_PANE_ID=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func runApp(args []string) error {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch verb := args[0]; verb {
		case "ls":
			return appList(args[1:])
		case "install":
			return appInstall(args[1:])
		case "remove":
			return appRemove(args[1:])
		default:
			return fmt.Errorf("conch app has no %q; %s", verb, appUsage)
		}
	}
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	which := fs.String("terminal", "", "the terminal to open conch in")
	dry := fs.Bool("n", false, "write the configuration and print the command, without starting it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(appUsage)
	}
	term, bin, err := appFind(*which)
	if err != nil {
		return err
	}
	conch, err := os.Executable()
	if err != nil {
		return err
	}
	cfg, err := appConfig(term)
	if err != nil {
		return err
	}
	argv := term.args(bin, cfg, conch)
	if term.bundle && runtime.GOOS == "darwin" {
		argv, err = appOpenBundle(bin, argv)
		if err != nil {
			return err
		}
	}
	// The configuration is written either way: what -n is for is reading
	// it, and a command printed without the file beside it says nothing.
	if *dry {
		fmt.Println(strings.Join(argv, " "))
		fmt.Println(cfg)
		return nil
	}
	if err := startApp(argv); err != nil {
		return fmt.Errorf("%s would not start: %w", term.name, err)
	}
	fmt.Printf("conch is opening in %s, with %s\n", term.name, cfg)
	return nil
}

// appFind is the terminal to use: the one named, or the first one
// installed. Naming one that is not there is an error rather than a
// silent second choice — somebody who asked for kitty wants to know it is
// missing.
func appFind(name string) (appTerminal, string, error) {
	if name != "" {
		for _, t := range appTerminals {
			if t.name == name {
				bin := appWhere(t)
				if bin == "" {
					return appTerminal{}, "", fmt.Errorf("%s is not installed here", t.name)
				}
				return t, bin, nil
			}
		}
		return appTerminal{}, "", fmt.Errorf("conch cannot start %s; it knows %s", name, appNames())
	}
	for _, t := range appTerminals {
		if bin := appWhere(t); bin != "" {
			return t, bin, nil
		}
	}
	return appTerminal{}, "", fmt.Errorf("none of %s is installed; conch needs one of them to open a terminal of its own (or run conch in the terminal you have)", appNames())
}

// appSystemApps is where applications are installed for everybody, a
// variable so a test can look somewhere of its own.
var appSystemApps = "/Applications"

// appWhere is the program to run for a terminal, or "" when it is not
// here: on PATH, or — on macOS — the application bundle, in the user's
// own Applications before the one everybody shares, since somebody who
// has installed it for themselves means that one.
func appWhere(t appTerminal) string {
	if bin, err := exec.LookPath(t.name); err == nil {
		return bin
	}
	if runtime.GOOS != "darwin" || t.app == "" {
		return ""
	}
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	dirs = append(dirs, appSystemApps)
	for _, dir := range dirs {
		bin := filepath.Join(dir, t.app, "Contents", "MacOS", t.name)
		if st, err := os.Stat(bin); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
			return bin
		}
	}
	return ""
}

func appNames() string {
	names := make([]string, len(appTerminals))
	for i, t := range appTerminals {
		names[i] = t.name
	}
	return strings.Join(names, ", ")
}

// appConfig writes conch's configuration for a terminal and answers where
// it is. It is written every time: it is conch's file, in conch's folder,
// and a stale one would quietly hold a setting conch no longer means. What
// somebody wants to keep goes in the <name>.local file beside it, which is
// included when it exists.
func appConfig(t appTerminal) (string, error) {
	dir := filepath.Join(config.Dir(), "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	local := filepath.Join(dir, strings.TrimSuffix(t.file, filepath.Ext(t.file))+".local"+filepath.Ext(t.file))
	if _, err := os.Stat(local); err != nil {
		local = ""
	}
	fallback := false
	if t.terminfo != "" {
		if has, known := terminfoHas(t.terminfo); known && !has {
			fallback = true
		}
	}
	head := fmt.Sprintf("# Written by conch (conch app). Edits here are lost.\n# Yours go in %s, which is included when it exists.\n",
		filepath.Join(dir, strings.TrimSuffix(t.file, filepath.Ext(t.file))+".local"+filepath.Ext(t.file)))
	if fallback {
		head += fmt.Sprintf("# TERM is held at xterm-256color: this computer has no terminfo entry for %s.\n", t.terminfo)
	}
	path := filepath.Join(dir, t.file)
	return path, os.WriteFile(path, []byte(head+"\n"+t.settings(fallback, local)), 0o600)
}

// appOpenBundle turns a command line into the one macOS takes for a
// terminal that cannot be started from the command line: the application
// bundle, opened with the arguments handed to it. The bundle is found from
// the binary — /Applications/Ghostty.app/Contents/MacOS/ghostty — with
// symlinks resolved first, since the name on PATH is usually one.
func appOpenBundle(bin string, argv []string) ([]string, error) {
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		real = bin
	}
	dir := real
	for dir != "/" && dir != "." {
		if strings.HasSuffix(dir, ".app") {
			return append([]string{"open", "-na", dir, "--args"}, argv[1:]...), nil
		}
		dir = filepath.Dir(dir)
	}
	return nil, fmt.Errorf("%s is not in an application bundle, and macOS cannot start it from the command line", bin)
}

// appList says which terminals are here, so "none of them is installed"
// can be answered without reading this file.
func appList(args []string) error {
	if len(args) > 0 {
		return errors.New(appUsage)
	}
	for _, t := range appTerminals {
		switch bin := appWhere(t); bin {
		case "":
			fmt.Printf("  %-10s not installed\n", t.name)
		default:
			fmt.Printf("✓ %-10s %s\n", t.name, bin)
		}
	}
	return nil
}
