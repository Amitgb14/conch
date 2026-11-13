package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestCloudGate: with Conch Cloud hidden its commands are refused with
// how to turn it on, the plumbing that reaches or runs inside a cloud
// machine is not, and nothing else is touched. Turned on, everything runs.
func TestCloudGate(t *testing.T) {
	a4Env(t)
	refused := [][]string{{"login"}, {"logout"}, {"account", "-json"}, {"cloud"}, {"cloud", "machine", "ls"}, {"cloud", "machines"}, {"cloud", "nope"},
		{"cloud", "machine"}, {"cloud", "machine", "new", "box"}, {"cloud", "machine", "sizes"}, {"cloud", "machine", "nope", "box"}}
	allowed := [][]string{{"cloud", "machine", "wake", "box"}, {"cloud", "machines", "sleep", "box"}, {"cloud", "machine", "rm", "box", "-y"},
		{"cloud", "machine", "forget-key", "box"}, {"cloud", "machine", "start", "box"}, {"cloud", "machine", "stop", "box"}, {"cloud", "machine", "delete", "box"},
		{"cloud", "bridge", "box"}, {"cloud", "exec", "box"}, {"cloud", "agent"}, {"cloud", "install"}, {"cloud", "git-credential", "get"},
		{"machine", "ls"}, {"status"}, {"project", "move", "x", "-to", "box"}, {"frobnicate"}, {""}}
	for _, a := range refused {
		err := cloudGate(a[0], a[1:])
		if err == nil || !strings.Contains(err.Error(), "Conch Cloud is not enabled") || !strings.Contains(err.Error(), "CONCH_CLOUD=1") ||
			!strings.Contains(err.Error(), "[cloud]") {
			t.Errorf("off: %v: %v", a, err)
		}
	}
	for _, a := range allowed {
		if err := cloudGate(a[0], a[1:]); err != nil {
			t.Errorf("off: %v refused: %v", a, err)
		}
	}
	t.Setenv("CONCH_CLOUD", "1")
	for _, a := range append(refused, allowed...) {
		if err := cloudGate(a[0], a[1:]); err != nil {
			t.Errorf("on: %v refused: %v", a, err)
		}
	}
	// The file turns it on as well, and the variable turns it off again.
	t.Setenv("CONCH_CLOUD", "")
	if err := os.WriteFile(filepath.Join(os.Getenv("CONCH_HOME"), "config.toml"), []byte("[cloud]\nenabled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cloudGate("login", nil); err != nil {
		t.Errorf("[cloud] enabled: %v", err)
	}
	t.Setenv("CONCH_CLOUD", "0")
	if err := cloudGate("login", nil); err == nil {
		t.Error("CONCH_CLOUD=0 over [cloud] enabled: login runs")
	}
}

// TestCloudHiddenFromMain: run as a person would, a hidden command says
// what it is and how to turn it on, and exits 1 — not 2, "unknown".
func TestCloudHiddenFromMain(t *testing.T) {
	a4Env(t)
	for _, args := range [][]string{{"login"}, {"logout"}, {"account"}, {"cloud", "machine", "ls"}} {
		code, out, errOut := a4RunMain(t, "", args...)
		if code != 1 || out != "" || !strings.Contains(errOut, "conch "+args[0]+": Conch Cloud is not enabled") || strings.Contains(errOut, "unknown command") {
			t.Errorf("%v: %d %q %q", args, code, out, errOut)
		}
	}
	// -h says the same rather than the command's grammar.
	code, out, _ := a4RunMain(t, "", "login", "-h")
	if code != 0 || !strings.Contains(out, "Conch Cloud is not enabled") || strings.Contains(out, "-no-browser") {
		t.Errorf("login -h: %d %q", code, out)
	}
	code, out, _ = a4RunMain(t, "", "help", "cloud")
	if code != 0 || !strings.Contains(out, "Conch Cloud is not enabled") {
		t.Errorf("help cloud: %d %q", code, out)
	}
	// Plumbing still runs: it fails on its own arguments, not the switch.
	code, _, errOut := a4RunMain(t, "", "cloud", "git-credential")
	if code != 1 || strings.Contains(errOut, "not enabled") || !strings.Contains(errOut, "usage: conch cloud git-credential") {
		t.Errorf("cloud git-credential: %d %q", code, errOut)
	}
}

func TestCloudHiddenFromUsage(t *testing.T) {
	a4Env(t)
	off := fullUsage()
	for _, gone := range []string{"conch login", "conch logout", "conch account", "conch cloud", "Conch Cloud", "conch-cloud-agent"} {
		if strings.Contains(off, gone) {
			t.Errorf("help all says %q with Cloud hidden", gone)
		}
	}
	// What is around it is all still there, line for line.
	for _, l := range strings.Split(usage, "\n") {
		if m := helpWords.FindStringSubmatch(l); m != nil && !slices.Contains(cloudCommands, m[1]) && !strings.Contains(off, l) {
			t.Errorf("help all lost %q", l)
		}
	}
	if !strings.HasSuffix(off, "\n") || !strings.Contains(off, "  conch relay [-listen ADDR]") || !strings.Contains(off, "  conch web pair [") {
		t.Errorf("help all around the cut:\n%s", off)
	}
	if got := strings.Count(usage, "\n") - strings.Count(off, "\n"); got < 30 {
		t.Errorf("took out %d lines; the Cloud entries are more than that", got)
	}
	t.Setenv("CONCH_CLOUD", "1")
	if fullUsage() != usage {
		t.Error("on: help all is not the whole usage")
	}
	// The helper on its own: an entry at the very end, and text with none.
	if got := withoutCommands("  conch a x\n  conch login y\n                                more\n", []string{"login"}); got != "  conch a x\n" {
		t.Errorf("last entry: %q", got)
	}
	if got := withoutCommands("", []string{"login"}); got != "" {
		t.Errorf("empty: %q", got)
	}
}

func TestCloudHiddenFromCompletion(t *testing.T) {
	a4Env(t)
	words := func(items []compItem) []string { return compWords(items) }
	top := words(compCandidates(nil, ""))
	for _, c := range cloudCommands {
		if slices.Contains(top, c) {
			t.Errorf("off: conch <tab> offers %s", c)
		}
	}
	if !slices.Contains(top, "machine") || !slices.Contains(top, "relay") {
		t.Errorf("off: lost the others: %v", top)
	}
	if got := compCandidates([]string{"cloud"}, ""); got != nil {
		t.Errorf("off: conch cloud <tab> offers %v", got)
	}
	if got := words(compCandidates(nil, "lo")); slices.Contains(got, "login") || slices.Contains(got, "logout") {
		t.Errorf("off: lo<tab> offers %v", got)
	}
	if near := nearestCommand("clod"); near == "cloud" {
		t.Error("off: a typo is pointed at conch cloud")
	}

	t.Setenv("CONCH_CLOUD", "1")
	top = words(compCandidates(nil, ""))
	for _, c := range cloudCommands {
		if !slices.Contains(top, c) {
			t.Errorf("on: conch <tab> does not offer %s", c)
		}
	}
	if got := words(compCandidates([]string{"cloud"}, "")); !slices.Contains(got, "machine") {
		t.Errorf("on: conch cloud <tab> offers %v", got)
	}
	if near := nearestCommand("clod"); near != "cloud" {
		t.Errorf("on: clod → %q", near)
	}
	if help := commandHelp("cloud"); !strings.Contains(help, "conch cloud machine ls") {
		t.Errorf("on: help cloud:\n%s", help)
	}
}
