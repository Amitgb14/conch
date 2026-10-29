package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/config"
)

// TestHelpForEveryCommand: every command has help of its own, made of what
// the full usage says about it, and points at a section of the CLI page
// that is there.
func TestHelpForEveryCommand(t *testing.T) {
	page, err := os.ReadFile("../../web/src/app/docs/cli/page.mdx")
	if err != nil {
		t.Fatal(err)
	}
	headings := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^###? (.+)$`).FindAllStringSubmatch(string(page), -1) {
		headings[strings.ToLower(strings.ReplaceAll(strings.TrimSpace(m[1]), " ", "-"))] = true
	}
	for _, c := range compCommands {
		help := commandHelp(c.word)
		if c.word == "help" {
			if help != shortUsage {
				t.Errorf("conch help -h is not the short help")
			}
			continue
		}
		if !strings.HasPrefix(help, "conch "+c.word+" — "+c.desc+"\n") {
			t.Errorf("conch %s: help starts %q", c.word, strings.SplitN(help, "\n", 2)[0])
		}
		about := usageOf(c.word)
		if about == "" {
			t.Errorf("conch %s: the usage says nothing about it", c.word)
		}
		for _, l := range strings.Split(strings.TrimSuffix(about, "\n"), "\n") {
			if !strings.HasPrefix(l, "  conch ") && !strings.HasPrefix(l, "                ") {
				t.Errorf("conch %s: a line of another command's: %q", c.word, l)
			}
		}
		anchor := regexp.MustCompile(`docs/cli#([a-z-]+)`).FindStringSubmatch(help)
		if anchor == nil || !headings[anchor[1]] {
			t.Errorf("conch %s: points at %v, which the CLI page has no section for", c.word, anchor)
		}
	}
	// Each example is a command that exists.
	for cmd, examples := range helpExamples {
		if helpCommand(cmd) != cmd {
			t.Errorf("examples for %q, which is no command", cmd)
		}
		for _, e := range examples {
			f := strings.Fields(e)
			if len(f) > 3 && f[1] == "-m" {
				f = append([]string{"conch"}, f[3:]...)
			}
			if len(f) < 2 || f[0] != "conch" || helpCommand(f[1]) == "" {
				t.Errorf("%s: example %q runs no command", cmd, e)
			}
			if n := utf8.RuneCountInString(e) + 2; n > 100 {
				t.Errorf("%s: example %q is %d columns", cmd, e, n)
			}
		}
	}
}

func TestUsageOf(t *testing.T) {
	// conch -m MACHINE upload, and conch upload|download.
	up := usageOf("upload")
	if !strings.Contains(up, "conch -m MACHINE upload") || !strings.Contains(up, "conch upload|download -ssh") {
		t.Errorf("upload:\n%s", up)
	}
	if strings.Contains(up, "MACHINE download") {
		t.Errorf("upload's help has download's own line:\n%s", up)
	}
	// A continuation that is not a description (project's "| setup").
	if p := usageOf("project"); !strings.Contains(p, "| setup ID") {
		t.Errorf("project:\n%s", p)
	}
	// A word that is in other lines but is not the command: "agent" is in
	// "conch new -agent claude".
	if a := usageOf("agent"); strings.Contains(a, "conch new") {
		t.Errorf("agent's help has new's lines:\n%s", a)
	}
	if usageOf("nosuch") != "" {
		t.Error("a command that is not there has usage")
	}
}

func TestNearestCommand(t *testing.T) {
	for word, want := range map[string]string{
		"brnach": "branch", "stauts": "status", "tsak": "task", "projetc": "project",
		"Branch": "branch", "machines": "machine", "worktre": "worktree", "doc": "doctor",
		"sandboxs": "sandbox", "updat": "update", "comp": "completion",
		// Nothing close: no suggestion is better than a wrong one.
		"zzz": "", "frobnicate": "", "go": "", "x": "", "": "", "deploy": "",
	} {
		if got := nearestCommand(word); got != want {
			t.Errorf("%q: %q, want %q", word, got, want)
		}
	}
}

func TestEditDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"", "", 0}, {"a", "", 1}, {"", "abc", 3}, {"same", "same", 0},
		{"brnach", "branch", 1}, // a swap
		{"stat", "status", 2}, {"kitten", "sitting", 3}, {"ü", "u", 1},
	} {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("%q %q: %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestHelpFromTheCommandLine: -h, --help and help CMD answer before the
// command runs — `conch status -h` used to start a server — and a typo is
// one line with the likely command, not the whole list.
func TestHelpFromTheCommandLine(t *testing.T) {
	a4Env(t)
	for _, args := range [][]string{
		{"status", "-h"}, {"ls", "--help"}, {"help", "status"}, {"-m", "local", "status", "-help"},
	} {
		code, out, errOut := a4RunMain(t, "", args...)
		if code != 0 || errOut != "" || !strings.HasPrefix(out, "conch status — ") {
			t.Errorf("%v: %d %q %q", args, code, out, errOut)
		}
	}
	if _, err := os.Stat(config.SocketPath()); err == nil {
		t.Fatal("asking for help started a server")
	}
	for _, args := range [][]string{{"server", "-h"}, {"update", "--help"}, {"task", "-h"}, {"help", "help"}} {
		if code, out, errOut := a4RunMain(t, "", args...); code != 0 || errOut != "" || out == "" {
			t.Errorf("%v: %d %q %q", args, code, out, errOut)
		}
	}

	// A subcommand's -h is its own: its flags, exit 0, and no "flag: help
	// requested" said as if it were an error.
	code, _, errOut := a4RunMain(t, "", "branch", "pr", "-h")
	if code != 0 || !strings.Contains(errOut, "-draft") || strings.Contains(errOut, "help requested") {
		t.Errorf("branch pr -h: %d %q", code, errOut)
	}

	// Typos.
	code, out, errOut := a4RunMain(t, "", "brnach", "push")
	if code != 2 || out != "" || !strings.Contains(errOut, `unknown command "brnach" — did you mean conch branch?`) ||
		strings.Contains(errOut, "Keep an eye on them") {
		t.Errorf("brnach: %d %q %q", code, out, errOut)
	}
	code, _, errOut = a4RunMain(t, "", "help", "brnach")
	if code != 2 || !strings.Contains(errOut, "did you mean conch branch?") {
		t.Errorf("help brnach: %d %q", code, errOut)
	}
	code, _, errOut = a4RunMain(t, "", "help", "nosuch")
	if code != 2 || !strings.Contains(errOut, `unknown command "nosuch"`) || !strings.Contains(errOut, "conch help all") {
		t.Errorf("help nosuch: %d %q", code, errOut)
	}

	// Only right after the command: a pane named -h further in is the
	// command's to make sense of, and a pane called help is a pane.
	code, out, errOut = a4RunMain(t, "", "send", "help")
	if code != 1 || strings.HasPrefix(out, "conch send — ") || !strings.Contains(errOut, "usage: conch send") {
		t.Errorf("send help was taken as asking for help: %d %q %q", code, out, errOut)
	}
}
