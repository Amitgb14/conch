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
	"github.com/Amitgb14/conch/internal/tui"
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
// alacritty). That is the whole trick: terminal defaults are fine, and
// what conch needs is for nothing broken to be read on top of them. What
// the file does add makes that terminal look and act like conch rather
// than like a stock terminal: the theme's colours, ⌘ shortcuts that are
// conch's keys, and a variable that tells the TUI where it is (appOpts).
//
// It is not a window of conch's own. conch is a terminal program, and
// this starts a terminal — the one thing it must not grow is an interface
// of its own, which would then compete both with conch's and with the
// terminal it is standing in for.

const appUsage = `usage: conch app [-terminal NAME] [-n]
       conch app ls | install | remove
       conch app update check | install VERSION | rollback`

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
	// settings writes that configuration from what conch decided (appOpts).
	settings func(o appOpts) string
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

// appOpts is what goes into a terminal's configuration.
type appOpts struct {
	// fallback is whether TERM has to be held back to xterm-256color
	// because the terminal's own entry is not installed.
	fallback bool
	// local is conch's own file for somebody's additions, included when it
	// exists — last wherever the terminal lets it be, so it can change
	// what conch wrote.
	local string
	// bg, fg, cursor and selection are the conch theme's (tui.TerminalColors).
	bg, fg, cursor, selection string
	// prefix is the bytes conch's prefix key sends ("\x02" for ctrl+b),
	// or "" when it is a key no byte stands for — then there are no ⌘
	// shortcuts, since each one is the prefix and a key.
	prefix string
}

// appEnvVar is set in the window conch opens, so the TUI in it knows it is
// conch's own window (internal/tui/appwindow.go). Set through the
// terminal's configuration and not the environment, because on macOS the
// terminal is opened by `open`, which passes no environment on.
const appEnvVar = tui.AppWindowEnv

// appPrefixBytes is what the terminal has to send for conch's prefix key:
// the control character for ctrl and a letter (or one of the few symbols
// that have one), and "" for a key no character stands for.
func appPrefixBytes(prefix string) string {
	k, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(prefix)), "ctrl+")
	if !ok {
		return ""
	}
	switch {
	case len(k) == 1 && k[0] >= 'a' && k[0] <= 'z':
		return string(rune(k[0] - 'a' + 1))
	case k == "space" || k == "@":
		return "\x00"
	case k == "\\":
		return "\x1c"
	case k == "]":
		return "\x1d"
	case k == "^":
		return "\x1e"
	case k == "_":
		return "\x1f"
	}
	return ""
}

// appEscape writes bytes the way ghostty's `text:` and kitty's
// `send_text` both read them: letters and digits as they are, everything
// else as \xNN, so no quote, space or control character is left for
// either config parser to take a view on.
func appEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "\\x%02x", c)
		}
	}
	return b.String()
}

// appGhosttyKeys are a shortcut's key as ghostty names it in a keybind,
// with any other spelling of it to bind too: ghostty binds ⌘1 both as the
// character and as the physical key (digit_1), and a binding of only one
// leaves the other doing what ghostty does by default.
func appGhosttyKeys(k tui.AppShortcut) []string {
	name := k.Key
	switch name {
	case "up", "down", "left", "right":
		name = "arrow_" + name
	}
	mods := "super+"
	if k.Mods != "" {
		mods += k.Mods + "+"
	}
	out := []string{mods + name}
	if len(k.Key) == 1 && k.Key[0] >= '0' && k.Key[0] <= '9' {
		out = append(out, mods+"digit_"+k.Key)
	}
	return out
}

func appGhosttyColors(b *strings.Builder, o appOpts) {
	for _, kv := range [][2]string{{"background", o.bg}, {"foreground", o.fg}, {"cursor-color", o.cursor}, {"selection-background", o.selection}, {"selection-foreground", o.fg}} {
		if kv[1] != "" {
			fmt.Fprintf(b, "%s = %s\n", kv[0], kv[1])
		}
	}
}

// appTerminals are the terminals conch will start, in the order it picks
// one: ghostty and kitty first because they do everything conch asks of a
// terminal (an addressable mouse, OSC 52 clipboard, OSC 22 pointer
// shapes, synchronized updates), then foot and alacritty. The ⌘
// shortcuts are ghostty's and kitty's: those are the two on macOS, where
// ⌘ is the key every application uses, and foot has no ⌘ to give.
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
	settings: func(o appOpts) string {
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
		if o.fallback {
			b.WriteString("term = xterm-256color\n")
		}
		fmt.Fprintf(&b, "env = %s=1\n", appEnvVar)
		// The left option key is alt, for conch's alt keys and alt+click;
		// the right one still types the characters a keyboard puts there.
		b.WriteString("macos-option-as-alt = left\n")
		// Room around the grid and between its lines, so the window reads
		// as an application's rather than as a terminal filled to its edges.
		// None above the first row, which is conch's header band, and the
		// padding beside it takes the band's colour (extend), so the band
		// meets the title bar and reads as part of it.
		b.WriteString("window-padding-x = 12\nwindow-padding-y = 0,8\nwindow-padding-color = extend\nadjust-cell-height = 12%\n")
		// An application's size: a type size to read at, and a window of
		// about 160 by 46 that opens that way every time, rather than one
		// restored to whatever filled the screen last, where conch spreads
		// over 300 columns and every line is mostly space.
		b.WriteString("font-size = 14\nwindow-width = 160\nwindow-height = 46\nwindow-save-state = never\n")
		// A title bar of its own, a shade off the window, as an
		// application has. The transparent one ghostty draws by default
		// is the window's background, and on macOS no setting colours it.
		b.WriteString("macos-titlebar-style = native\nwindow-theme = dark\n")
		appGhosttyColors(&b, o)
		if o.prefix != "" {
			for _, k := range tui.AppShortcuts {
				for _, name := range appGhosttyKeys(k) {
					fmt.Fprintf(&b, "keybind = %s=text:%s\n", name, appEscape(o.prefix+k.Send))
				}
			}
			// ghostty's settings are not the ones this window was started
			// with, so opening them would edit a file it never read.
			b.WriteString("keybind = super+,=unbind\n")
		}
		if o.local != "" {
			fmt.Fprintf(&b, "config-file = %s\n", o.local)
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
	settings: func(o appOpts) string {
		var b strings.Builder
		if o.fallback {
			b.WriteString("term xterm-256color\n")
		}
		fmt.Fprintf(&b, "env %s=1\n", appEnvVar)
		b.WriteString("macos_option_as_alt left\n")
		b.WriteString("window_padding_width 8 12\nmodify_font cell_height 112%\n")
		b.WriteString("font_size 14\nremember_window_size no\ninitial_window_width 160c\ninitial_window_height 46c\n")
		for _, kv := range [][2]string{{"background", o.bg}, {"foreground", o.fg}, {"cursor", o.cursor}, {"selection_background", o.selection}, {"selection_foreground", o.fg}} {
			if kv[1] != "" {
				fmt.Fprintf(&b, "%s %s\n", kv[0], kv[1])
			}
		}
		if o.prefix != "" {
			for _, k := range tui.AppShortcuts {
				mods := "cmd+"
				if k.Mods != "" {
					mods += k.Mods + "+"
				}
				fmt.Fprintf(&b, "map %s%s send_text all %s\n", mods, k.Key, appEscape(o.prefix+k.Send))
			}
		}
		if o.local != "" {
			fmt.Fprintf(&b, "include %s\n", o.local)
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
	settings: func(o appOpts) string {
		var b strings.Builder
		// foot reads an include where it stands and has nowhere after the
		// sections to put one, so somebody's own file comes first here and
		// conch's colours are the ones that hold.
		if o.local != "" {
			fmt.Fprintf(&b, "include=%s\n", o.local)
		}
		if o.fallback {
			b.WriteString("[main]\nterm=xterm-256color\n")
		}
		fmt.Fprintf(&b, "[environment]\n%s=1\n", appEnvVar)
		if o.bg != "" {
			fmt.Fprintf(&b, "[colors]\nbackground=%s\nforeground=%s\n", strings.TrimPrefix(o.bg, "#"), strings.TrimPrefix(o.fg, "#"))
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
	settings: func(o appOpts) string {
		var b strings.Builder
		// An import is read first and this file over it, whatever the
		// order: alacritty has no way for somebody's own file to come
		// last, so conch's colours are the ones that hold here too.
		if o.local != "" {
			fmt.Fprintf(&b, "[general]\nimport = [%q]\n", o.local)
		}
		b.WriteString("[env]\n")
		fmt.Fprintf(&b, "%s = \"1\"\n", appEnvVar)
		if o.fallback {
			b.WriteString("TERM = \"xterm-256color\"\n")
		}
		b.WriteString("[window]\noption_as_alt = \"OnlyLeft\"\npadding = { x = 12, y = 8 }\ndimensions = { columns = 160, lines = 46 }\n")
		b.WriteString("[font]\nsize = 14\n")
		if o.bg != "" {
			fmt.Fprintf(&b, "[colors.primary]\nbackground = %q\nforeground = %q\n", o.bg, o.fg)
			fmt.Fprintf(&b, "[colors.cursor]\ncursor = %q\ntext = %q\n", o.cursor, o.bg)
			fmt.Fprintf(&b, "[colors.selection]\nbackground = %q\ntext = %q\n", o.selection, o.fg)
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
		case "update":
			return appUpdate(args[1:])
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
	cfg, err := appConfig(term, bin)
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

// appExtraDirs is where else a terminal may be, past PATH. A variable for
// the same reason: a test must look at its own directories and not at
// whatever the developer has installed.
var appExtraDirs = defaultExtraDirs

// defaultExtraDirs are the places a terminal lives that the PATH in hand
// may not have. A desktop entry is started with the *session's* PATH,
// which is not the shell's: ~/.local/bin, snap and flatpak are each
// commonly in one and not the other, so a terminal found when `conch app`
// is typed could be missed when the icon is clicked — and an icon that
// silently does nothing (the entry says Terminal=false, so there is not
// even a window to show an error) is the failure `conch app install`
// refuses to create at install time. Looking here closes it at click
// time.
func defaultExtraDirs() []string {
	dirs := []string{"/usr/local/bin", "/usr/bin", "/bin"}
	if runtime.GOOS == "darwin" {
		dirs = append(dirs, "/opt/homebrew/bin")
	} else {
		dirs = append(dirs, "/snap/bin", "/var/lib/flatpak/exports/bin")
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".local", "share", "flatpak", "exports", "bin"))
	}
	return dirs
}

// appRunnable reports whether a path is a program that can be run.
func appRunnable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// appWhere is the program to run for a terminal, or "" when it is not
// here: on PATH, or — on macOS — the application bundle, in the user's
// own Applications before the one everybody shares, since somebody who
// has installed it for themselves means that one.
func appWhere(t appTerminal) string {
	if bin, err := exec.LookPath(t.name); err == nil {
		return bin
	}
	for _, dir := range appExtraDirs() {
		if bin := filepath.Join(dir, t.name); appRunnable(bin) {
			return bin
		}
	}
	if runtime.GOOS != "darwin" || t.app == "" {
		return ""
	}
	var apps []string
	if home, err := os.UserHomeDir(); err == nil {
		apps = append(apps, filepath.Join(home, "Applications"))
	}
	apps = append(apps, appSystemApps)
	for _, dir := range apps {
		if bin := filepath.Join(dir, t.app, "Contents", "MacOS", t.name); appRunnable(bin) {
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
func appConfig(t appTerminal, bin string) (string, error) {
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
		has, known := terminfoHas(t.terminfo)
		// A terminal that brings its own terminfo and points TERMINFO at
		// it — macOS Ghostty does, in its bundle — needs nothing held
		// back: what it starts resolves the entry even though this
		// computer, asked without that variable, cannot. Reading the
		// environment conch happens to be in said the opposite, and the
		// window lost xterm-ghostty for xterm-256color with a comment
		// claiming the entry was not installed.
		if known && !has {
			if dir := appTerminfoDir(t, bin); dir != "" {
				if inOwn, asked := terminfoHasIn(dir, t.terminfo); asked && inOwn {
					has = true
				}
			}
		}
		fallback = known && !has
	}
	head := fmt.Sprintf("# Written by conch (conch app). Edits here are lost.\n# Yours go in %s, which is included when it exists.\n",
		filepath.Join(dir, strings.TrimSuffix(t.file, filepath.Ext(t.file))+".local"+filepath.Ext(t.file)))
	if fallback {
		head += fmt.Sprintf("# TERM is held at xterm-256color: this computer has no terminfo entry for %s.\n", t.terminfo)
	}
	path := filepath.Join(dir, t.file)
	o := appOpts{fallback: fallback, local: local}
	// The theme and keys are config.toml's. A file that will not load
	// still gets a window: the defaults, as the TUI itself would draw.
	cfg, err := config.Load()
	if err != nil {
		cfg = config.Default()
	}
	o.bg, o.fg, o.cursor, o.selection = tui.TerminalColors(cfg.UI.Theme, cfg.UI.Accent)
	o.prefix = appPrefixBytes(cfg.Keys.Prefix)
	if o.prefix == "" {
		head += fmt.Sprintf("# No ⌘ shortcuts: the prefix key %s sends no character a terminal can be told to send.\n", cfg.Keys.Prefix)
	}
	return path, os.WriteFile(path, []byte(head+"\n"+t.settings(o)), 0o600)
}

// appOpenBundle turns a command line into the one macOS takes for a
// terminal that cannot be started from the command line: the application
// bundle, opened with the arguments handed to it. The bundle is found from
// the binary — /Applications/Ghostty.app/Contents/MacOS/ghostty — with
// symlinks resolved first, since the name on PATH is usually one.
func appOpenBundle(bin string, argv []string) ([]string, error) {
	bundle := appBundleOf(bin)
	if bundle == "" {
		return nil, fmt.Errorf("%s is not in an application bundle, and macOS cannot start it from the command line", bin)
	}
	return append([]string{"open", "-na", bundle, "--args"}, argv[1:]...), nil
}

// appBundleOf is the application bundle a program is in, or "": symlinks
// resolved first, since the name on PATH is usually one.
func appBundleOf(bin string) string {
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		real = bin
	}
	for dir := real; dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		if strings.HasSuffix(dir, ".app") {
			return dir
		}
	}
	return ""
}

// appTerminfoDir is the terminfo a terminal carries with it, or "". Only
// macOS has it: a bundle keeps its own under Contents/Resources, and
// points TERMINFO there for what it starts.
func appTerminfoDir(t appTerminal, bin string) string {
	if runtime.GOOS != "darwin" || t.app == "" {
		return ""
	}
	bundle := appBundleOf(bin)
	if bundle == "" {
		return ""
	}
	dir := filepath.Join(bundle, "Contents", "Resources", "terminfo")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return ""
	}
	return dir
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
