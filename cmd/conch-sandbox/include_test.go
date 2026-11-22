package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runTerminal runs conch-sandbox as if from a terminal, answering with in.
func runTerminal(t *testing.T, in string, args ...string) (int, string) {
	t.Helper()
	var out, errb strings.Builder
	a := &app{stdin: strings.NewReader(in), stdout: &out, stderr: &errb, terminal: true, wait: 30 * time.Second}
	return a.run(context.Background(), args), errb.String()
}

// With nobody to ask and no --yes, create refuses before anything is made,
// and says exactly what to run.
func TestIncludeRefusedWithNoTerminal(t *testing.T) {
	home := bare(t)
	f := fakeProvider(t, "fakebox", tokenKind)
	code, out, errs := run(t, "", "create", "--provider", "fakebox", "--name", "my box")
	if code != 1 || out != "" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	for _, want := range []string{"Run conch-sandbox create --provider fakebox --name 'my box' --yes", includeLine()} {
		if !strings.Contains(errs, want) {
			t.Fatalf("lacks %q:\n%s", want, errs)
		}
	}
	if strings.Contains(f.called(), "POST /boxes") {
		t.Fatal("a sandbox was made")
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh")); err == nil {
		t.Fatal("~/.ssh was made")
	}
	if code, _, errs := run(t, "", "ssh-alias", "--provider", "fakebox", "box1"); code != 1 || !strings.Contains(errs, "ssh-alias --provider fakebox box1 --yes") {
		t.Fatalf("ssh-alias: %d %q", code, errs)
	}
}

// --yes adds it, making ~/.ssh where there was none.
func TestIncludeYes(t *testing.T) {
	home := bare(t)
	fakeProvider(t, "fakebox", tokenKind)
	code, _, errs := run(t, "", "create", "--provider", "fakebox", "--yes")
	if code != 0 || !strings.Contains(errs, "to the top of") {
		t.Fatalf("%d %q", code, errs)
	}
	cfg := filepath.Join(home, ".ssh", "config")
	if got := readFile(t, cfg); got != includeLine()+"\n" {
		t.Fatalf("%q", got)
	}
	for path, mode := range map[string]os.FileMode{cfg: 0o600, filepath.Dir(cfg): 0o700} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != mode {
			t.Fatalf("%s: %v %v", path, fi, err)
		}
	}
	// There now: not added again.
	run(t, "", "create", "--provider", "fakebox", "--yes")
	if got := readFile(t, cfg); got != includeLine()+"\n" {
		t.Fatalf("twice: %q", got)
	}
}

func TestIncludeAskedAtATerminal(t *testing.T) {
	for _, c := range []struct {
		answer string
		added  bool
	}{{"y\n", true}, {"YES\n", true}, {"\n", true}, {"", true}, {"n\n", false}, {"no\n", false}, {"maybe\n", false}} {
		home := bare(t)
		f := fakeProvider(t, "fakebox", tokenKind)
		cfg := filepath.Join(home, ".ssh", "config")
		os.MkdirAll(filepath.Dir(cfg), 0o700)
		mine := "Host *\n  ServerAliveInterval 30\n"
		os.WriteFile(cfg, []byte(mine), 0o644)
		code, errs := runTerminal(t, c.answer, "create", "--provider", "fakebox")
		if !strings.Contains(errs, "[Y/n]") {
			t.Fatalf("%q: not asked: %q", c.answer, errs)
		}
		got := readFile(t, cfg)
		if c.added {
			if code != 0 || got != includeLine()+"\n\n"+mine {
				t.Fatalf("%q: %d %q\n%s", c.answer, code, got, errs)
			}
			if fi, _ := os.Stat(cfg); fi.Mode().Perm() != 0o644 {
				t.Fatalf("mode changed: %v", fi.Mode())
			}
			continue
		}
		if code != 1 || got != mine || strings.Contains(f.called(), "POST /boxes") || !strings.Contains(errs, "not added") {
			t.Fatalf("%q: %d %q\n%s", c.answer, code, got, errs)
		}
	}
}

// An Include already there, in any of the ways ssh reads one, is enough.
func TestIncludeAlreadyThere(t *testing.T) {
	home := bare(t)
	rel, _ := filepath.Rel(filepath.Join(home, ".ssh"), aliasFile())
	tilde := "~/" + strings.TrimPrefix(aliasFile(), home+"/")
	for _, line := range []string{
		"Include " + aliasFile(),
		"include " + tilde,
		"Include\t" + tilde,
		"Include=" + tilde,
		"Include = " + tilde,
		`Include "` + aliasFile() + `"`,
		"Include ~/other " + tilde,
		"  Include " + filepath.Join(filepath.Dir(tilde), "*"),
		"Include " + rel,
	} {
		if !includes("Host *\n"+line+"\n", home) {
			t.Errorf("%q not seen", line)
		}
	}
	// ssh reads an Include where it stands: one under a Host or Match
	// that is not every host is read for those hosts alone, and so for
	// none of the aliases.
	line := "  Include " + aliasFile() + "\n"
	for _, text := range []string{
		line,
		"# mine\nUser me\n" + line + "Host x\n",
		"Host x\n  User y\nHost *\n" + line,
		"Host x\nMatch all\n" + line,
		"Match All\n" + line,
		"Host=*\n" + line,
		"Host \"*\"\n" + line,
	} {
		if !includes(text, home) {
			t.Errorf("%q not seen", text)
		}
	}
	for _, text := range []string{
		"Host work-*\n" + line,
		"Host * !bastion\n" + line,
		"Host x *\n" + line,
		"Match host x\n" + line,
		"Match all exec true\n" + line,
		"Host *\nMatch user me\n" + line,
	} {
		if includes(text, home) {
			t.Errorf("%q seen", text)
		}
	}
	for _, line := range []string{"# Include " + aliasFile(), "Include " + aliasFile() + ".old", "IncludeX " + aliasFile(), "Include", ""} {
		if includes(line+"\n", home) {
			t.Errorf("%q seen", line)
		}
	}
	// Seen: nothing asked, nothing written.
	fakeProvider(t, "fakebox", tokenKind)
	cfg := filepath.Join(home, ".ssh", "config")
	os.MkdirAll(filepath.Dir(cfg), 0o700)
	text := "Host *\n  User me\n\ninclude " + tilde + "\n"
	os.WriteFile(cfg, []byte(text), 0o600)
	if code, errs := runTerminal(t, "", "create", "--provider", "fakebox"); code != 0 || strings.Contains(errs, "[Y/n]") {
		t.Fatalf("%d %q", code, errs)
	}
	if got := readFile(t, cfg); got != text {
		t.Fatalf("rewritten: %q", got)
	}
}

// A ~/.ssh/config kept in a dotfiles repository is a link, written through.
func TestIncludeThroughALink(t *testing.T) {
	home := bare(t)
	fakeProvider(t, "fakebox", tokenKind)
	dots := filepath.Join(home, "dotfiles", "ssh_config")
	os.MkdirAll(filepath.Dir(dots), 0o700)
	os.WriteFile(dots, []byte("Host a\n"), 0o600)
	cfg := filepath.Join(home, ".ssh", "config")
	os.MkdirAll(filepath.Dir(cfg), 0o700)
	if err := os.Symlink(dots, cfg); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := run(t, "", "create", "--provider", "fakebox", "--yes"); code != 0 {
		t.Fatalf("%q", errs)
	}
	if fi, err := os.Lstat(cfg); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced")
	}
	if got := readFile(t, dots); got != includeLine()+"\n\nHost a\n" {
		t.Fatalf("%q", got)
	}
}

func TestIncludeUnreadable(t *testing.T) {
	home := bare(t)
	fakeProvider(t, "fakebox", tokenKind)
	os.MkdirAll(filepath.Join(home, ".ssh", "config"), 0o700) // a folder, not a file
	if code, _, errs := run(t, "", "create", "--provider", "fakebox", "--yes"); code != 1 || !strings.Contains(errs, "~/.ssh/config") {
		t.Fatalf("%d %q", code, errs)
	}
}

func TestAgainQuotes(t *testing.T) {
	a := &app{line: []string{"create", "--name", "it's mine", "--provider", "p", ""}}
	if got := a.again(); got != `conch-sandbox create --name 'it'\''s mine' --provider p ''` {
		t.Fatalf("%s", got)
	}
}

func TestSplitArgs(t *testing.T) {
	for in, want := range map[string]string{
		`a b`:          "a|b",
		`"a b" c`:      "a b|c",
		"a\tb":         "a|b",
		`"unclosed`:    "unclosed",
		`  `:           "",
		`x "y z" "w"`:  "x|y z|w",
		`"" a`:         "|a",
		`a  "b"   "c"`: "a|b|c",
	} {
		if got := strings.Join(splitArgs(in), "|"); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

// ssh expands % and ${} in an Include's path, so a CONCH_HOME with them
// would be included as some other file: refused, ~/.ssh/config untouched.
func TestIncludeRefusesAPathSSHWouldExpand(t *testing.T) {
	home := bare(t)
	t.Setenv("CONCH_HOME", filepath.Join(home, "50%off"))
	fakeProvider(t, "fakebox", tokenKind)
	code, _, errs := run(t, "", "create", "--provider", "fakebox", "--yes")
	if code != 1 || !strings.Contains(errs, "CONCH_HOME") {
		t.Fatalf("%d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh", "config")); !os.IsNotExist(err) {
		t.Fatalf("~/.ssh/config: %v", err)
	}
}
