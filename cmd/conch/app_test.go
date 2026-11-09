package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/Amitgb14/conch/internal/phone"
)

// appEnv isolates the home and config conch writes into, and takes the
// launch: a test must never open a terminal window on somebody's desktop.
// PATH holds nothing but the fakes, so no terminal of the developer's own
// is found and no real `open` or `infocmp` is run — and on macOS the
// applications folder is a temporary one too, or a developer with Ghostty
// installed would run a different suite from one without it.
func appEnv(t *testing.T) (bin string, launched *[]string) {
	t.Helper()
	a4Env(t)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "share"))
	apps := appSystemApps
	appSystemApps = t.TempDir()
	extra := appExtraDirs
	appExtraDirs = func() []string { return nil }
	t.Cleanup(func() { appSystemApps, appExtraDirs = apps, extra })
	// And no TERMINFO of the developer's: ghostty sets it for the shells
	// it starts, so `go test` run in one would answer the terminfo
	// question differently from the same test run anywhere else.
	t.Setenv("TERMINFO", "")
	var argv []string
	old := startApp
	startApp = func(a []string) error { argv = append([]string(nil), a...); return nil }
	t.Cleanup(func() { startApp = old })
	return dir, &argv
}

// appTerm is the terminal conch knows by that name.
func appTerm(t *testing.T, name string) appTerminal {
	t.Helper()
	for _, term := range appTerminals {
		if term.name == name {
			return term
		}
	}
	t.Fatalf("conch knows no %s", name)
	return appTerminal{}
}

// fakeTerminal puts a terminal of that name on the fake PATH.
func fakeTerminal(t *testing.T, dir, name string) string {
	t.Helper()
	return doctorScript(t, dir, name, `exit 0`)
}

func TestAppPicksATerminalAndWritesItsConfig(t *testing.T) {
	bin, launched := appEnv(t)
	// kitty alone: ghostty comes first in the order, so this also says
	// the order is a preference and not a requirement.
	kitty := fakeTerminal(t, bin, "kitty")

	if err := runApp(nil); err != nil {
		t.Fatal(err)
	}
	argv := *launched
	if len(argv) == 0 {
		t.Fatal("nothing was launched")
	}
	cfg := filepath.Join(os.Getenv("CONCH_HOME"), "app", "kitty.conf")
	self, _ := os.Executable()
	if strings.Join(argv, " ") != strings.Join([]string{kitty, "--config", cfg, self}, " ") {
		t.Fatalf("launched %q", argv)
	}
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Written by conch, says so, and says where to put your own.
	if !strings.HasPrefix(string(body), "# Written by conch") || !strings.Contains(string(body), "kitty.local.conf") {
		t.Fatalf("config:\n%s", body)
	}
	// Nothing is included that is not there, and without infocmp nothing
	// is claimed about TERM either way.
	if strings.Contains(string(body), "include ") || strings.Contains(string(body), "term ") {
		t.Fatalf("config says more than it knows:\n%s", body)
	}
	if mode, err := os.Stat(cfg); err != nil || mode.Mode().Perm() != 0o600 {
		t.Errorf("config mode %v %v", mode.Mode(), err)
	}
}

// The terminal's own terminfo decides whether TERM has to be held back,
// which is the one setting in the file that does any work.
func TestAppHoldsTERMBackWhenTheTerminfoIsMissing(t *testing.T) {
	bin, _ := appEnv(t)
	ghostty := appTerm(t, "ghostty")

	// An infocmp that knows every entry: nothing to hold back.
	doctorScript(t, bin, "infocmp", `exit 0`)
	cfg, err := appConfig(ghostty, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(os.Getenv("CONCH_HOME"), "app", "ghostty.conf"); cfg != want {
		t.Fatalf("config at %s, want %s", cfg, want)
	}
	body, _ := os.ReadFile(cfg)
	if strings.Contains(string(body), "term =") {
		t.Fatalf("TERM held back although the entry is there:\n%s", body)
	}
	// ghostty's own window has to be this one, not one an already
	// running ghostty makes from its own config — and on macOS the
	// instance `open -na` starts has to go when that window does, or
	// each launch leaves one behind with nothing in it.
	for _, want := range []string{"gtk-single-instance = false", "quit-after-last-window-closed = true"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("config has no %q:\n%s", want, body)
		}
	}

	// One that has never heard of xterm-ghostty: held back, and the file
	// says why, since somebody reading it will wonder.
	doctorScript(t, bin, "infocmp", `exit 1`)
	if _, err = appConfig(ghostty, ""); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(cfg)
	if !strings.Contains(string(body), "term = xterm-256color") {
		t.Fatalf("TERM not held back:\n%s", body)
	}
	if !strings.Contains(string(body), "no terminfo entry for xterm-ghostty") {
		t.Fatalf("no reason given:\n%s", body)
	}
}

// Your own additions are included, and only when the file is there.
func TestAppIncludesYourOwnFile(t *testing.T) {
	bin, _ := appEnv(t)
	fakeTerminal(t, bin, "foot")
	dir := filepath.Join(os.Getenv("CONCH_HOME"), "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(dir, "foot.local.ini")
	if err := os.WriteFile(local, []byte("[main]\nfont=Fira Code:size=13\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runApp(nil); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "foot.ini"))
	if !strings.Contains(string(body), "include="+local) {
		t.Fatalf("config:\n%s", body)
	}
	// The generated file is rewritten, so what somebody keeps has to be
	// the other one: this is the file that says so.
	if !strings.Contains(string(body), "Edits here are lost") {
		t.Fatalf("config:\n%s", body)
	}
}

// The terminal conch opens is not the pane conch was asked from. Nearly
// everybody runs this from inside conch, and a conch that inherits
// CONCH_PANE_ID beside a socket refuses to draw a TUI — so the window
// somebody just asked for would have held an error instead of conch.
func TestAppDoesNotPassOnItsPane(t *testing.T) {
	env := appEnviron([]string{
		"PATH=/usr/bin",
		"CONCH_PANE_ID=p7",
		"CONCH_SOCKET=/tmp/conch.sock",
		"CONCH_HOME=/home/me/.config/conch",
		"TERM=xterm-ghostty",
	})
	for _, kv := range env {
		if strings.HasPrefix(kv, "CONCH_PANE_ID=") {
			t.Fatalf("the pane was passed on: %q", env)
		}
	}
	// Everything else is kept, the socket above all: the new window wants
	// the same server, with the same panes in it.
	for _, want := range []string{"PATH=/usr/bin", "CONCH_SOCKET=/tmp/conch.sock", "CONCH_HOME=/home/me/.config/conch", "TERM=xterm-ghostty"} {
		if !slices.Contains(env, want) {
			t.Errorf("%q was dropped: %q", want, env)
		}
	}
	// A variable that merely starts the same way is not the pane.
	if env = appEnviron([]string{"CONCH_PANE_IDS=x", "CONCH_PANE=1"}); len(env) != 2 {
		t.Errorf("dropped more than the pane: %q", env)
	}
	if env = appEnviron(nil); len(env) != 0 {
		t.Errorf("an empty environment became %q", env)
	}
}

func TestAppNamesATerminalOrSaysWhatItKnows(t *testing.T) {
	bin, launched := appEnv(t)

	// Nothing installed: it says what it would take, and launches nothing.
	err := runApp(nil)
	if err == nil || !strings.Contains(err.Error(), "ghostty, kitty, foot, alacritty") {
		t.Fatalf("err %v", err)
	}
	if len(*launched) != 0 {
		t.Fatalf("launched %q", *launched)
	}

	// A terminal conch does not know.
	if err = runApp([]string{"-terminal", "xterm"}); err == nil || !strings.Contains(err.Error(), "it knows") {
		t.Fatalf("err %v", err)
	}
	// One it knows that is not here: said, rather than quietly using
	// another one.
	fakeTerminal(t, bin, "kitty")
	if err = runApp([]string{"-terminal", "alacritty"}); err == nil || !strings.Contains(err.Error(), "not installed here") {
		t.Fatalf("err %v", err)
	}
	if len(*launched) != 0 {
		t.Fatalf("launched %q", *launched)
	}
	// And the one asked for, when it is here.
	if err = runApp([]string{"-terminal", "kitty"}); err != nil {
		t.Fatal(err)
	}
	if argv := *launched; len(argv) == 0 || filepath.Base(argv[0]) != "kitty" {
		t.Fatalf("launched %q", argv)
	}
	// A word that is not a subcommand, and a stray argument.
	if err = runApp([]string{"open"}); err == nil || !strings.Contains(err.Error(), `no "open"`) {
		t.Fatalf("err %v", err)
	}
	if err = runApp([]string{"-terminal", "kitty", "extra"}); err == nil {
		t.Fatal("a stray argument was taken")
	}
}

// A desktop entry is started with the session's PATH, not the shell's, so
// a terminal in ~/.local/bin, snap or flatpak can be found when `conch
// app` is typed and missed when the icon is clicked — and the entry says
// Terminal=false, so a click that finds nothing shows nothing at all.
// conch looks in those places itself.
func TestAppLooksPastPath(t *testing.T) {
	_, _ = appEnv(t) // PATH holds nothing, and neither does anywhere else
	ghostty := appTerm(t, "ghostty")
	if got := appWhere(ghostty); got != "" {
		t.Fatalf("found %q with nothing installed", got)
	}

	// A terminal in a directory PATH does not have.
	dir := t.TempDir()
	appExtraDirs = func() []string { return []string{dir} }
	bin := filepath.Join(dir, "ghostty")
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := appWhere(ghostty); got != bin {
		t.Fatalf("appWhere = %q, want %q", got, bin)
	}
	// It has to be a program, not a file of that name.
	if err := os.Chmod(bin, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := appWhere(ghostty); got != "" {
		t.Fatalf("a file nobody can run answered %q", got)
	}
	// A directory that is not there is passed over, not an error.
	appExtraDirs = func() []string { return []string{filepath.Join(dir, "nope"), dir} }
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := appWhere(ghostty); got != bin {
		t.Fatalf("appWhere = %q, want %q", got, bin)
	}
	// And the real list names the places that bite: a session PATH and a
	// shell PATH rarely agree about these three.
	appExtraDirs = defaultExtraDirs
	home, _ := os.UserHomeDir()
	want := []string{"/usr/bin", filepath.Join(home, ".local", "bin")}
	if runtime.GOOS == "darwin" {
		want = append(want, "/opt/homebrew/bin")
	} else {
		want = append(want, "/snap/bin", "/var/lib/flatpak/exports/bin")
	}
	for _, dir := range want {
		if !slices.Contains(defaultExtraDirs(), dir) {
			t.Errorf("%s is not looked in: %q", dir, defaultExtraDirs())
		}
	}
}

// A terminal that brings its own terminfo and points TERMINFO at it for
// what it starts needs nothing held back: the window resolves the entry
// even where conch, asked without that variable, cannot. macOS Ghostty
// keeps one in its bundle and sets TERMINFO for the program it runs, so
// reading the environment conch happened to be in cost the window
// xterm-ghostty and wrote a comment saying the entry was not installed.
func TestAppAsksTheTerminalsOwnTerminfo(t *testing.T) {
	bin, _ := appEnv(t)
	ghostty := appTerm(t, "ghostty")
	// An infocmp like the real one: the entry is there only when
	// TERMINFO points at the terminal's own directory.
	doctorScript(t, bin, "infocmp", `[ -n "$TERMINFO" ] && exit 0 || exit 1`)

	// A bundle that carries the entry: TERM is left alone.
	bundle := filepath.Join(t.TempDir(), "Ghostty.app")
	exe := filepath.Join(bundle, "Contents", "MacOS", "ghostty")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bundle, "Contents", "Resources", "terminfo"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := appConfig(ghostty, exe)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(cfg)
	if runtime.GOOS == "darwin" {
		if strings.Contains(string(body), "term =") {
			t.Fatalf("TERM held back although the terminal carries the entry:\n%s", body)
		}
		if strings.Contains(string(body), "no terminfo entry") {
			t.Fatalf("the config says something untrue about this computer:\n%s", body)
		}
		// The bundle is found through EvalSymlinks, and /var is a
		// symlink on macOS.
		real, err := filepath.EvalSymlinks(bundle)
		if err != nil {
			t.Fatal(err)
		}
		if got := appTerminfoDir(ghostty, exe); got != filepath.Join(real, "Contents", "Resources", "terminfo") {
			t.Errorf("appTerminfoDir = %q, want under %q", got, real)
		}
	}

	// A bundle with no terminfo of its own: nothing anywhere resolves it,
	// so TERM is held back and the config says why.
	plain := filepath.Join(t.TempDir(), "Ghostty.app")
	bare := filepath.Join(plain, "Contents", "MacOS", "ghostty")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bare, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err = appConfig(ghostty, bare); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(cfg)
	if !strings.Contains(string(body), "term = xterm-256color") {
		t.Fatalf("TERM not held back for a bundle that carries nothing:\n%s", body)
	}
	if got := appTerminfoDir(ghostty, bare); got != "" {
		t.Errorf("appTerminfoDir = %q for a bundle with no terminfo", got)
	}
	// A terminal with no bundle at all carries nothing to ask.
	if got := appTerminfoDir(appTerm(t, "foot"), "/usr/bin/foot"); got != "" {
		t.Errorf("foot carries %q", got)
	}
}

// On macOS a terminal is an application, not a program on PATH. ghostty
// puts its own directory on PATH only for the shells it starts itself, so
// a shell started by anything else — a conch pane, launchd, another
// terminal — cannot see it, and looking only on PATH told somebody with
// Ghostty in /Applications that no terminal was installed. Found by the
// first person to run it.
func TestAppFindsAnApplicationNotOnPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("applications are macOS's")
	}
	bin, _ := appEnv(t) // a PATH with nothing on it
	ghostty := appTerm(t, "ghostty")
	if got := appWhere(ghostty); got != "" {
		t.Fatalf("found %q with nothing installed", got)
	}

	// One installed for everybody.
	apps := t.TempDir()
	appSystem := filepath.Join(apps, "Ghostty.app", "Contents", "MacOS")
	if err := os.MkdirAll(appSystem, 0o755); err != nil {
		t.Fatal(err)
	}
	system := filepath.Join(appSystem, "ghostty")
	if err := os.WriteFile(system, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	appSystemApps = apps // appEnv restores it
	if got := appWhere(ghostty); got != system {
		t.Fatalf("appWhere = %q, want %q", got, system)
	}

	// One of somebody's own comes first: installing it for yourself when
	// there is another means you meant yours.
	mine := filepath.Join(os.Getenv("HOME"), "Applications", "Ghostty.app", "Contents", "MacOS", "ghostty")
	if err := os.MkdirAll(filepath.Dir(mine), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := appWhere(ghostty); got != mine {
		t.Fatalf("appWhere = %q, want %q", got, mine)
	}

	// A bundle with nothing runnable in it is not a terminal.
	if err := os.Chmod(mine, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(system, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := appWhere(ghostty); got != "" {
		t.Fatalf("a bundle with no executable answered %q", got)
	}

	// PATH still wins when it has one: that is the Linux way in, and a
	// test binary put there on purpose.
	onPath := doctorScript(t, bin, "ghostty", `exit 0`)
	if got := appWhere(ghostty); got != onPath {
		t.Fatalf("appWhere = %q, want the one on PATH %q", got, onPath)
	}

	// foot has no bundle, so nothing is looked for.
	if got := appWhere(appTerm(t, "foot")); got != "" {
		t.Fatalf("foot answered %q", got)
	}
}

// On macOS a terminal that cannot be started from the command line is
// opened as its bundle instead, with the same arguments handed over.
func TestAppOpensABundle(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "Applications", "Ghostty.app")
	bin := filepath.Join(bundle, "Contents", "MacOS", "ghostty")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	argv, err := appOpenBundle(bin, []string{bin, "--config-file=/c", "-e", "/bin/conch"})
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(bundle) // /var is a symlink on macOS
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"open", "-na", real, "--args", "--config-file=/c", "-e", "/bin/conch"}
	if strings.Join(argv, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q\nwant %q", argv, want)
	}

	// A symlink on PATH is the usual way it is found, and leads to the
	// same bundle.
	link := filepath.Join(t.TempDir(), "ghostty")
	if err := os.Symlink(bin, link); err != nil {
		t.Fatal(err)
	}
	if argv, err = appOpenBundle(link, []string{link}); err != nil {
		t.Fatal(err)
	}
	if len(argv) < 3 || argv[2] != real {
		t.Fatalf("through a symlink: %q", argv)
	}

	// A binary in no bundle at all cannot be opened this way, and is
	// said rather than opened wrongly.
	plain := filepath.Join(t.TempDir(), "ghostty")
	if err := os.WriteFile(plain, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err = appOpenBundle(plain, []string{plain}); err == nil {
		t.Fatal("a bare binary was taken for a bundle")
	}
}

func TestAppLists(t *testing.T) {
	bin, _ := appEnv(t)
	fakeTerminal(t, bin, "foot")
	out, _, err := a4Out(t, func() error { return appList(nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "✓ foot") || !strings.Contains(out, "ghostty    not installed") {
		t.Fatalf("ls says:\n%s", out)
	}
}

// install writes files of the user's own, says which, and remove takes
// away exactly those.
func TestAppInstallAndRemove(t *testing.T) {
	bin, _ := appEnv(t)

	// With no terminal there is nothing to launch, so the entry is
	// refused rather than written as a button that does nothing.
	if err := appInstall(nil); err == nil || !strings.Contains(err.Error(), "none of") {
		t.Fatalf("err %v", err)
	}
	fakeTerminal(t, bin, "kitty")

	out, _, err := a4Out(t, func() error { return appInstall(nil) })
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		bundle := appMacBundle()
		for _, p := range []string{"Contents/Info.plist", "Contents/MacOS/conch"} {
			if _, err := os.Stat(filepath.Join(bundle, p)); err != nil {
				t.Fatalf("%s: %v", p, err)
			}
		}
		script, _ := os.ReadFile(filepath.Join(bundle, "Contents", "MacOS", "conch"))
		self, _ := os.Executable()
		if !strings.Contains(string(script), appShellQuote(self)+" app") {
			t.Fatalf("the bundle's program:\n%s", script)
		}
		if mode, err := os.Stat(filepath.Join(bundle, "Contents", "MacOS", "conch")); err != nil || mode.Mode().Perm()&0o100 == 0 {
			t.Errorf("the bundle's program is not executable: %v %v", mode, err)
		}
	} else {
		entry, err := os.ReadFile(appDesktopFile())
		if err != nil {
			t.Fatal(err)
		}
		self, _ := os.Executable()
		// Categories carries exactly one main category: with two, the
		// entry can be listed twice, which desktop-file-validate says.
		for _, want := range []string{"[Desktop Entry]", "Exec=" + self + " app", "Icon=conch", "Terminal=false", "Categories=Development;\n"} {
			if !strings.Contains(string(entry), want) {
				t.Errorf("the entry has no %q:\n%s", want, entry)
			}
		}
		icon, err := os.ReadFile(appIconFile())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(icon), "<svg") {
			t.Errorf("the icon is not an svg: %.40s", icon)
		}
		if !strings.Contains(out, appDesktopFile()) || !strings.Contains(out, appIconFile()) {
			t.Errorf("install did not say what it wrote:\n%s", out)
		}
	}
	// It names the terminal it will open, which is the thing somebody
	// clicking it is trusting.
	if !strings.Contains(out, "kitty") {
		t.Errorf("install says:\n%s", out)
	}

	// remove takes them away and says so; a second remove is not an error.
	if out, _, err = a4Out(t, func() error { return appRemove(nil) }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "removed") {
		t.Errorf("remove says:\n%s", out)
	}
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat(appMacBundle()); !os.IsNotExist(err) {
			t.Errorf("the bundle is still there: %v", err)
		}
	} else if _, err := os.Stat(appDesktopFile()); !os.IsNotExist(err) {
		t.Errorf("the entry is still there: %v", err)
	}
	if err = appRemove(nil); err != nil {
		t.Errorf("removing twice: %v", err)
	}
	if err = appRemove([]string{"extra"}); err == nil {
		t.Error("a stray argument was taken")
	}
}

// A desktop entry's Exec is a quoted word, not a line of shell, so a conch
// installed under a path with a space in it still starts — and the
// backslashes are doubled once more, since the value is a string first
// and GLib refuses an escape it does not know (\" or \$).
func TestAppQuotesExec(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/local/bin/conch":  "/usr/local/bin/conch",
		"/opt/my apps/conch":    `"/opt/my apps/conch"`,
		`/opt/wei"rd/conch`:     `"/opt/wei\\"rd/conch"`,
		`/opt/back\slash/conch`: `"/opt/back\\\\slash/conch"`,
		"/opt/$HOME/conch":      `"/opt/\\$HOME/conch"`,
		"/opt/`x`/conch":        "\"/opt/\\\\`x\\\\`/conch\"",
		"/opt/100%/conch":       "/opt/100%%/conch",
		"/opt/50% off/conch":    `"/opt/50%% off/conch"`,
		"/opt/new\nline/conch":  `"/opt/new\nline/conch"`,
		"/opt/tab\there/conch":  `"/opt/tab\there/conch"`,
		"/opt/it's/conch":       `"/opt/it's/conch"`,
		"":                      "",
	} {
		if got := appQuoteExec(in); got != want {
			t.Errorf("appQuoteExec(%q) = %q, want %q", in, got, want)
		}
	}
	if got := appShellQuote("/opt/it's/conch"); got != `'/opt/it'\''s/conch'` {
		t.Errorf("appShellQuote: %s", got)
	}
}

// Whatever the path, the Exec line written for it reads back — by the
// specification's three steps — as that path and the word app, on one line.
func TestAppExecRoundTrips(t *testing.T) {
	for _, path := range []string{
		"/usr/bin/conch", "/opt/my apps/conch", `/a"b/conch`, `/a\b/conch`,
		"/a$b/conch", "/a`b`/conch", "/a%b/conch", "/a%%u/conch", "/a\nb/conch",
		"/a\tb\rc/conch", "/a'b/conch", "/a;b|c&d<e>f~g*h?i#j(k)/conch",
		`/all " ' \ $ ` + "` % \n/conch", "/trailing\\",
	} {
		line := "Exec=" + appQuoteExec(path) + " app"
		if strings.ContainsAny(line, "\n\r") {
			t.Errorf("%q: the line breaks: %q", path, line)
			continue
		}
		argv, err := readDesktopExec(strings.TrimPrefix(line, "Exec="))
		if err != nil {
			t.Errorf("%q: %v (%q)", path, err, line)
			continue
		}
		if len(argv) != 2 || argv[0] != path || argv[1] != "app" {
			t.Errorf("%q reads back as %q (%q)", path, argv, line)
		}
	}
}

// readDesktopExec reads an Exec value as the Desktop Entry specification
// says a launcher does: the string escapes, then the quoting, then the
// field codes (only %% here — conch writes no other). It is strict where
// GLib is, so an escape the writer got wrong is an error.
func readDesktopExec(v string) ([]string, error) {
	var s strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' {
			s.WriteByte(v[i])
			continue
		}
		if i++; i == len(v) {
			return nil, errors.New("a backslash ends the value")
		}
		switch v[i] {
		case 's':
			s.WriteByte(' ')
		case 'n':
			s.WriteByte('\n')
		case 't':
			s.WriteByte('\t')
		case 'r':
			s.WriteByte('\r')
		case '\\':
			s.WriteByte('\\')
		default:
			return nil, fmt.Errorf("invalid string escape \\%c", v[i])
		}
	}
	var argv []string
	var word strings.Builder
	inWord, quoted := false, false
	str := s.String()
	for i := 0; i < len(str); i++ {
		c := str[i]
		switch {
		case quoted && c == '\\':
			if i++; i == len(str) || !strings.ContainsRune("\"`$\\", rune(str[i])) {
				return nil, errors.New("invalid escape in a quoted argument")
			}
			word.WriteByte(str[i])
		case quoted && c == '"':
			quoted = false
		case quoted:
			word.WriteByte(c)
		case c == '"':
			quoted, inWord = true, true
		case c == ' ':
			if inWord {
				argv = append(argv, word.String())
				word.Reset()
				inWord = false
			}
		case strings.ContainsRune(appExecReserved, rune(c)):
			return nil, fmt.Errorf("reserved %q outside quotes", c)
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	if quoted {
		return nil, errors.New("an unclosed quote")
	}
	if inWord {
		argv = append(argv, word.String())
	}
	for i, a := range argv {
		var out strings.Builder
		for j := 0; j < len(a); j++ {
			if a[j] != '%' {
				out.WriteByte(a[j])
				continue
			}
			if j++; j == len(a) || a[j] != '%' {
				return nil, errors.New("a field code conch did not write")
			}
			out.WriteByte('%')
		}
		argv[i] = out.String()
	}
	return argv, nil
}

// appNativeBundle puts what Conch.app's bundle.sh writes at path: the
// native app, which on a Mac's case-insensitive volume is the very path
// `conch app install` uses when it lives in ~/Applications.
func appNativeBundle(t *testing.T, path, plist string) map[string]string {
	t.Helper()
	files := map[string]string{
		"Contents/MacOS/Conch":            "native program",
		"Contents/Resources/AppIcon.icns": "native icon",
	}
	if plist != "" {
		files["Contents/Info.plist"] = plist
	}
	for name, body := range files {
		p := filepath.Join(path, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// appUnchanged fails unless every file is still there as it was, and
// nothing was added beside them.
func appUnchanged(t *testing.T, root string, files map[string]string) {
	t.Helper()
	seen := 0
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		want, ok := files[filepath.ToSlash(rel)]
		if !ok {
			t.Errorf("%s was added to a bundle that is not conch's", rel)
			return nil
		}
		if got, _ := os.ReadFile(p); string(got) != want {
			t.Errorf("%s was changed to %q", rel, got)
		}
		seen++
		return nil
	})
	if err != nil {
		t.Fatalf("the bundle is gone: %v", err)
	}
	if seen != len(files) {
		t.Errorf("%d of %d files are left", seen, len(files))
	}
}

const nativePlist = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>Conch (Beta)</string>
	<key>CFBundleIdentifier</key>
	<string>dev.conch.mac</string>
	<key>CFBundleExecutable</key><string>Conch</string>
</dict>
</plist>
`

// The launcher is never written over, or removed in place of, a bundle
// conch did not write — Conch.app above all, which on the default volume
// is ~/Applications/conch.app as far as the file system is concerned.
func TestAppMacLeavesOtherBundles(t *testing.T) {
	for name, plist := range map[string]string{
		"the native app":               nativePlist,
		"no Info.plist":                "",
		"a garbled Info.plist":         "<plist><dict><key>CFBundleIdentifier</key><str",
		"a binary Info.plist":          "bplist00\xd1\x01\x02_\x10\x12CFBundleIdentifier",
		"an identifier only near ours": strings.Replace(nativePlist, "dev.conch.mac", "dev.conch.app.other", 1),
	} {
		t.Run(name, func(t *testing.T) {
			appEnv(t)
			bundle := appMacBundle()
			files := appNativeBundle(t, bundle, plist)
			out, _, err := a4Out(t, func() error { return appInstallMac("/usr/bin/conch", appTerm(t, "kitty")) })
			if err == nil || !strings.Contains(err.Error(), "leaves it alone") || !strings.Contains(err.Error(), bundle) {
				t.Errorf("install: %v\n%s", err, out)
			}
			if name == "the native app" && (err == nil || !strings.Contains(err.Error(), "dev.conch.mac")) {
				t.Errorf("install does not say what is there: %v", err)
			}
			appUnchanged(t, bundle, files)
			if out, _, err = a4Out(t, appRemoveMac); err == nil || strings.Contains(out, "removed") {
				t.Errorf("remove: %v\n%s", err, out)
			}
			appUnchanged(t, bundle, files)
		})
	}
}

// Nor through a link, or over a file: install never makes either.
func TestAppMacLeavesLinksAndFiles(t *testing.T) {
	appEnv(t)
	bundle := appMacBundle()
	elsewhere := filepath.Join(t.TempDir(), "Conch.app")
	files := appNativeBundle(t, elsewhere, strings.Replace(nativePlist, "dev.conch.mac", appMacID, 1))
	if err := os.MkdirAll(filepath.Dir(bundle), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, bundle); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a4Out(t, func() error { return appInstallMac("/usr/bin/conch", appTerm(t, "kitty")) }); err == nil {
		t.Error("install wrote through a link")
	}
	if _, _, err := a4Out(t, appRemoveMac); err == nil {
		t.Error("remove took a link")
	}
	appUnchanged(t, elsewhere, files)
	if _, err := os.Lstat(bundle); err != nil {
		t.Errorf("the link is gone: %v", err)
	}

	if err := os.Remove(bundle); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, []byte("a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a4Out(t, func() error { return appInstallMac("/usr/bin/conch", appTerm(t, "kitty")) }); err == nil {
		t.Error("install wrote over a file")
	}
	if b, _ := os.ReadFile(bundle); string(b) != "a file" {
		t.Errorf("the file is now %q", b)
	}
}

// conch's own bundle is still installed, written over by the next install
// (a new conch path), and removed.
func TestAppMacOwnBundle(t *testing.T) {
	appEnv(t)
	bundle := appMacBundle()
	term := appTerm(t, "kitty")
	if _, _, err := a4Out(t, func() error { return appInstallMac("/old/conch", term) }); err != nil {
		t.Fatal(err)
	}
	plist, _ := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
	if appPlistID(plist) != appMacID {
		t.Fatalf("install wrote an identifier it does not recognise:\n%s", plist)
	}
	if _, _, err := a4Out(t, func() error { return appInstallMac("/new place/conch", term) }); err != nil {
		t.Fatalf("updating conch's own bundle: %v", err)
	}
	script, _ := os.ReadFile(filepath.Join(bundle, "Contents", "MacOS", "conch"))
	if !strings.Contains(string(script), "'/new place/conch' app") {
		t.Errorf("the update was not written:\n%s", script)
	}
	out, _, err := a4Out(t, appRemoveMac)
	if err != nil || !strings.Contains(out, "removed "+bundle) {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if _, err := os.Lstat(bundle); !os.IsNotExist(err) {
		t.Errorf("the bundle is still there: %v", err)
	}
	if out, _, err = a4Out(t, appRemoveMac); err != nil || !strings.Contains(out, "nothing to remove") {
		t.Errorf("a second remove: %v\n%s", err, out)
	}
}

// A case-insensitive volume is the case this is for: Conch.app put in
// ~/Applications by its own name is the path install would use.
func TestAppMacCaseInsensitiveVolume(t *testing.T) {
	appEnv(t)
	bundle := appMacBundle()
	native := filepath.Join(filepath.Dir(bundle), "Conch.app")
	files := appNativeBundle(t, native, nativePlist)
	if _, err := os.Stat(bundle); err != nil {
		t.Skip("this volume tells conch.app from Conch.app")
	}
	if _, _, err := a4Out(t, func() error { return appInstallMac("/usr/bin/conch", appTerm(t, "kitty")) }); err == nil {
		t.Error("install wrote into Conch.app")
	}
	if _, _, err := a4Out(t, appRemoveMac); err == nil {
		t.Error("remove took Conch.app")
	}
	appUnchanged(t, native, files)
}

func TestAppPlistID(t *testing.T) {
	for in, want := range map[string]string{
		"": "",
		"<key>CFBundleIdentifier</key><string>a.b</string>":                                        "a.b",
		"<key>CFBundleIdentifier</key>\n\t <string> a.b </string>":                                 "a.b",
		"<key>CFBundleIdentifier</key><integer>1</integer>":                                        "",
		"<key>CFBundleIdentifier</key><string>a.b":                                                 "",
		"<key>CFBundleIdentifier</key>":                                                            "",
		"<key>CFBundleName</key><string>a.b</string>":                                              "",
		"<key>CFBundleName</key><string>x</string><key>CFBundleIdentifier</key><string>y</string>": "y",
	} {
		if got := appPlistID([]byte(in)); got != want {
			t.Errorf("appPlistID(%q) = %q, want %q", in, got, want)
		}
	}
}

// The Linux entry and icon follow the same rule: conch's own are written
// over and removed, anybody else's are left and named.
func TestAppXDGLeavesOtherFiles(t *testing.T) {
	appEnv(t)
	term := appTerm(t, "kitty")
	theirs := "[Desktop Entry]\nType=Application\nName=conch\nExec=/opt/other/conch\n"
	if err := appWrite(appDesktopFile(), []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a4Out(t, func() error { return appInstallXDG("/usr/bin/conch", term) }); err == nil || !strings.Contains(err.Error(), appDesktopFile()) {
		t.Errorf("install over somebody's entry: %v", err)
	}
	if _, err := os.Stat(appIconFile()); !os.IsNotExist(err) {
		t.Errorf("a refused install wrote the icon: %v", err)
	}
	out, _, err := a4Out(t, appRemoveXDG)
	if err == nil || strings.Contains(out, "removed") {
		t.Errorf("remove: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(appDesktopFile()); string(b) != theirs {
		t.Errorf("their entry is now:\n%s", b)
	}

	// Somebody's icon by the same name, with no entry of conch's beside it.
	if err := os.Remove(appDesktopFile()); err != nil {
		t.Fatal(err)
	}
	if err := appWrite(appIconFile(), []byte("<svg>theirs</svg>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a4Out(t, func() error { return appInstallXDG("/usr/bin/conch", term) }); err == nil || !strings.Contains(err.Error(), appIconFile()) {
		t.Errorf("install over somebody's icon: %v", err)
	}
	if _, err := os.Stat(appDesktopFile()); !os.IsNotExist(err) {
		t.Errorf("a refused install wrote the entry: %v", err)
	}
	if b, _ := os.ReadFile(appIconFile()); string(b) != "<svg>theirs</svg>" {
		t.Errorf("their icon is now %q", b)
	}
}

// conch's own entry is replaced and removed — and so is one an older
// conch wrote before the mark, with the icon beside it, whatever picture
// that conch had.
func TestAppXDGOwnFiles(t *testing.T) {
	appEnv(t)
	term := appTerm(t, "kitty")
	older := strings.Join([]string{
		"[Desktop Entry]", "Type=Application", "Name=conch",
		"Comment=Terminal orchestrator for AI coding agents",
		`Exec="/old place/conch" app`, "Icon=conch", "",
	}, "\n")
	if err := appWrite(appDesktopFile(), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appWrite(appIconFile(), []byte("<svg>an older conch</svg>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a4Out(t, func() error { return appInstallXDG("/usr/bin/conch", term) }); err != nil {
		t.Fatalf("updating an older conch's entry: %v", err)
	}
	entry, _ := os.ReadFile(appDesktopFile())
	if !strings.Contains(string(entry), "Exec=/usr/bin/conch app\n") || !strings.Contains(string(entry), appDesktopMark+"\n") {
		t.Errorf("the entry is:\n%s", entry)
	}
	if icon, _ := os.ReadFile(appIconFile()); string(icon) != string(phone.Icon()) {
		t.Error("the icon was not brought up to date")
	}
	// Again over its own.
	if _, _, err := a4Out(t, func() error { return appInstallXDG("/usr/bin/conch", term) }); err != nil {
		t.Fatalf("installing twice: %v", err)
	}
	// The icon alone, its entry already gone, is still conch's by its picture.
	if err := os.Remove(appDesktopFile()); err != nil {
		t.Fatal(err)
	}
	out, _, err := a4Out(t, appRemoveXDG)
	if err != nil || !strings.Contains(out, "removed "+appIconFile()) {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if _, err := os.Stat(appIconFile()); !os.IsNotExist(err) {
		t.Errorf("the icon is still there: %v", err)
	}
	if out, _, err = a4Out(t, appRemoveXDG); err != nil || !strings.Contains(out, "nothing to remove") {
		t.Errorf("a second remove: %v\n%s", err, out)
	}
}

// The icon comes out of the binary, so an install has one wherever conch
// runs from.
func TestAppIconIsEmbedded(t *testing.T) {
	if svg := string(phone.Icon()); !strings.Contains(svg, "<svg") {
		t.Errorf("icon: %.60s", svg)
	}
	if png := phone.IconPNG(); len(png) < 1000 || string(png[1:4]) != "PNG" {
		t.Errorf("icon png: %d bytes", len(png))
	}
}

// appConfigBody writes conch's configuration for a terminal and reads it
// back.
func appConfigBody(t *testing.T, name string) string {
	t.Helper()
	cfg, err := appConfig(appTerm(t, name), "")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// appSetConfig writes config.toml into the test's CONCH_HOME.
func appSetConfig(t *testing.T, toml string) {
	t.Helper()
	if err := os.MkdirAll(os.Getenv("CONCH_HOME"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("CONCH_HOME"), "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The window is conch's: it says so to the TUI in it, has ⌘ shortcuts
// that are conch's prefix and a key, and is drawn in the theme.
func TestAppWindowIsConchs(t *testing.T) {
	appEnv(t)
	body := appConfigBody(t, "ghostty")
	for _, want := range []string{
		"env = CONCH_APP_WINDOW=1",
		"macos-option-as-alt = left",
		"window-padding-x = 12",
		"window-padding-y = 0,8",
		"window-padding-color = extend",
		"adjust-cell-height = 12%",
		"font-size = 14",
		"window-width = 160",
		"window-save-state = never",
		"macos-titlebar-style = native",
		"background = #15191E",
		"cursor-color = #0F7B8A",
		`keybind = super+t=text:\x02c`,
		`keybind = super+shift+d=text:\x02\x22`,
		`keybind = super+k=text:\x02\x2f`,
		`keybind = super+alt+arrow_up=text:\x02\x1b\x5bA`,
		// ⌘1 is bound by ghostty both as a character and a key: both go.
		`keybind = super+1=text:\x021`,
		`keybind = super+digit_1=text:\x021`,
		"keybind = super+,=unbind",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("ghostty config has no %q:\n%s", want, body)
		}
	}

	body = appConfigBody(t, "kitty")
	for _, want := range []string{
		"env CONCH_APP_WINDOW=1",
		"macos_option_as_alt left",
		"window_padding_width 8 12",
		"initial_window_width 160c",
		"remember_window_size no",
		"background #15191E",
		`map cmd+t send_text all \x02c`,
		`map cmd+shift+enter send_text all \x02z`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("kitty config has no %q:\n%s", want, body)
		}
	}

	for _, name := range []string{"foot", "alacritty"} {
		if body := appConfigBody(t, name); !strings.Contains(body, "CONCH_APP_WINDOW") || !strings.Contains(strings.ToUpper(body), "15191E") {
			t.Errorf("%s config:\n%s", name, body)
		}
	}
}

// The theme and prefix are the ones in config.toml.
func TestAppWindowFollowsTheSettings(t *testing.T) {
	appEnv(t)
	appSetConfig(t, "[ui]\ntheme = \"nord\"\naccent = \"#123456\"\n[keys]\nprefix = \"ctrl+a\"\n")
	body := appConfigBody(t, "ghostty")
	for _, want := range []string{"background = #2E3440", "cursor-color = #123456", `keybind = super+t=text:\x01c`} {
		if !strings.Contains(body, want) {
			t.Errorf("ghostty config has no %q:\n%s", want, body)
		}
	}

	// A prefix no character stands for gets no shortcuts, and says why.
	appSetConfig(t, "[keys]\nprefix = \"f12\"\n")
	body = appConfigBody(t, "ghostty")
	if strings.Contains(body, "keybind") || !strings.Contains(body, "No ⌘ shortcuts: the prefix key f12") {
		t.Errorf("f12 prefix:\n%s", body)
	}

	// A config.toml that will not load still gets a window, with the
	// defaults the TUI would draw.
	appSetConfig(t, "[ui\ntheme = ")
	if body = appConfigBody(t, "ghostty"); !strings.Contains(body, "background = #15191E") || !strings.Contains(body, `\x02c`) {
		t.Errorf("broken config.toml:\n%s", body)
	}
}

// Somebody's own file comes after conch's settings where the terminal
// lets it, so it can change them.
func TestAppYourFileComesLast(t *testing.T) {
	appEnv(t)
	dir := filepath.Join(os.Getenv("CONCH_HOME"), "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, file := range map[string]string{"ghostty": "ghostty.local.conf", "kitty": "kitty.local.conf"} {
		if err := os.WriteFile(filepath.Join(dir, file), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		body := strings.TrimSpace(appConfigBody(t, name))
		last := body[strings.LastIndex(body, "\n")+1:]
		if !strings.Contains(last, file) {
			t.Errorf("%s: last line %q", name, last)
		}
	}
}

// alacritty's file is TOML, where a table named twice is an error: the
// environment conch sets and the TERM it holds back share one [env].
func TestAppAlacrittyConfigIsTOML(t *testing.T) {
	bin, _ := appEnv(t)
	dir := filepath.Join(os.Getenv("CONCH_HOME"), "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alacritty.local.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	doctorScript(t, bin, "infocmp", `exit 1`) // TERM held back too
	body := appConfigBody(t, "alacritty")
	var v map[string]any
	if _, err := toml.Decode(body, &v); err != nil {
		t.Fatalf("%v:\n%s", err, body)
	}
	env, _ := v["env"].(map[string]any)
	if env["CONCH_APP_WINDOW"] != "1" || env["TERM"] != "xterm-256color" {
		t.Fatalf("env %v", env)
	}
}

func TestAppPrefixBytes(t *testing.T) {
	for in, want := range map[string]string{
		"ctrl+b": "\x02", "ctrl+a": "\x01", "CTRL+Z": "\x1a", " ctrl+b ": "\x02",
		"ctrl+space": "\x00", "ctrl+\\": "\x1c", "ctrl+]": "\x1d", "ctrl+_": "\x1f",
		"f12": "", "alt+b": "", "": "", "ctrl+": "", "ctrl+bb": "",
	} {
		if got := appPrefixBytes(in); got != want {
			t.Errorf("appPrefixBytes(%q) = %q, want %q", in, got, want)
		}
	}
	if got := appEscape("\x02\"a 1\x1b[A"); got != `\x02\x22a\x201\x1b\x5bA` {
		t.Errorf("appEscape: %s", got)
	}
}
