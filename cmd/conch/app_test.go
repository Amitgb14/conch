package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

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
	t.Cleanup(func() { appSystemApps = apps })
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
	cfg, err := appConfig(ghostty)
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
	if _, err = appConfig(ghostty); err != nil {
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
// installed under a path with a space in it still starts.
func TestAppQuotesExec(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/local/bin/conch":  "/usr/local/bin/conch",
		"/opt/my apps/conch":    `"/opt/my apps/conch"`,
		`/opt/wei"rd/conch`:     `"/opt/wei\"rd/conch"`,
		`/opt/back\slash/conch`: `"/opt/back\\slash/conch"`,
		"/opt/$HOME/conch":      `"/opt/\$HOME/conch"`,
	} {
		if got := appQuoteExec(in); got != want {
			t.Errorf("appQuoteExec(%q) = %q, want %q", in, got, want)
		}
	}
	if got := appShellQuote("/opt/it's/conch"); got != `'/opt/it'\''s/conch'` {
		t.Errorf("appShellQuote: %s", got)
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
