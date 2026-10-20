package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

func compWords(items []compItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.word)
	}
	return out
}

// TestCompletionKnowsEveryCommand: the list completion offers is the
// usage's list, both ways, so a new command cannot be left out of it and a
// renamed one cannot linger.
func TestCompletionKnowsEveryCommand(t *testing.T) {
	text := usage + shortUsage
	inUsage := map[string]bool{}
	// conch CMD, conch -m MACHINE CMD, and conch CMD|CMD.
	for _, m := range regexp.MustCompile(`(?m)^\s*conch (?:-m MACHINE )?([a-z][a-z|-]*)`).FindAllStringSubmatch(text, -1) {
		for _, cmd := range strings.Split(m[1], "|") {
			inUsage[cmd] = true
		}
	}
	offered := compWords(compCommands)
	for cmd := range inUsage {
		if !slices.Contains(offered, cmd) {
			t.Errorf("conch %s is in the usage but not completed", cmd)
		}
	}
	for _, cmd := range offered {
		if !inUsage[cmd] {
			t.Errorf("conch %s is completed but not in the usage", cmd)
		}
	}

	// Each subcommand is on its command's usage lines (with their
	// continuation lines), as a word.
	lines := strings.Split(text, "\n")
	for key, subs := range compSubcommands {
		var about strings.Builder
		for i, l := range lines {
			if !strings.Contains(l, "conch "+key) {
				continue
			}
			about.WriteString(l + "\n")
			for j := i + 1; j < len(lines) && strings.HasPrefix(lines[j], "                "); j++ {
				about.WriteString(lines[j] + "\n")
			}
		}
		for _, s := range subs {
			if !regexp.MustCompile(`(^|[\s|\[])` + regexp.QuoteMeta(s.word) + `($|[\s|\]])`).MatchString(about.String()) {
				t.Errorf("conch %s %s is completed but its usage does not name it:\n%s", key, s.word, about.String())
			}
		}
	}
}

func TestCompletionCandidates(t *testing.T) {
	a4Env(t)
	if _, err := remote.SaveMachine(remote.Machine{Label: "busybox", Target: "me@busybox"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		done []string
		cur  string
		want []string
	}{
		{nil, "st", []string{"status"}},
		{nil, "zzz", nil},
		{nil, "-", []string{"-m"}},
		{[]string{"-m"}, "", []string{"local", "busybox"}},
		{[]string{"-m", "busybox"}, "up", []string{"upload", "update"}},
		{[]string{"server"}, "", []string{"stop", "reload"}},
		{[]string{"agent"}, "s", []string{"status", "setup", "sync", "skill"}},
		{[]string{"agent", "library"}, "a", []string{"add", "apply"}},
		{[]string{"agent", "install"}, "co", []string{"codex"}},
		{[]string{"agent", "install", "codex"}, "", nil},
		{[]string{"new", "-agent"}, "ge", []string{"gemini"}},
		// A comma list goes on from its last name, and leaves out the ones
		// already in it.
		{[]string{"task", "-agent"}, "codex,c", []string{"codex,claude", "codex,cursor"}},
		{[]string{"task", "-sandbox"}, "b", []string{"boat"}},
		{[]string{"machine", "rm"}, "", []string{"busybox"}},
		{[]string{"machine", "rename", "busybox"}, "", nil},
		{[]string{"web", "pair", "-permission"}, "r", []string{"reply"}},
		{[]string{"web", "permission", "d1"}, "", []string{"view", "reply", "full"}},
		{[]string{"sandbox"}, "", []string{"-provider", "stats"}},
		{[]string{"sandbox", "-provider"}, "", []string{"daytona", "boat", "devin"}},
		{[]string{"sandbox", "-provider", "boat"}, "s", []string{"start", "stop", "ssh", "snapshot", "snapshots"}},
		{[]string{"sandbox", "-provider", "boat", "ls"}, "", nil},
		// A flag's value is not a subcommand: still choosing after `branch`.
		{[]string{"branch", "-cwd", "/x"}, "p", []string{"push", "pr"}},
		{[]string{"branch", "pr", "-title", "x"}, "", nil},
		{[]string{"branch", "push", "-rebase"}, "", nil},
		{[]string{"branch"}, "-", nil}, // flags: the command's -h says them
		{[]string{"completion"}, "", []string{"bash", "zsh", "fish"}},
		{[]string{"help"}, "", []string{"all"}},
		{[]string{"update"}, "r", []string{"rollback"}},
		{[]string{"read"}, "", nil}, // no server: nothing, and no wait
	} {
		got := compWords(compCandidates(c.done, c.cur))
		if !slices.Equal(got, c.want) {
			t.Errorf("conch %s %q: %q, want %q", strings.Join(c.done, " "), c.cur, got, c.want)
		}
	}
}

// TestCompletionPanesAndProjects: what the local server has running, by id
// and by name, described so a list of p-numbers can be told apart.
func TestCompletionPanesAndProjects(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{
				{ID: "p1", Name: "reviewer", State: proto.PaneRunning, Cwd: "/w/api",
					Agent: &proto.AgentStatus{Name: "claude", State: "waiting"}},
				{ID: "p2", State: proto.PaneRunning, Cwd: "/w/web"},
				{ID: "p3", State: proto.PaneExited, Cwd: "/w/old"}, // ended: not offered
			}}, nil
		case proto.MethodProjectList:
			return proto.ProjectList{Projects: []proto.ProjectInfo{{ID: "api", Name: "API"}}}, nil
		}
		return nil, nil
	})
	got := compCandidates([]string{"read"}, "")
	want := []compItem{{"reviewer", "p1 · claude · api · waiting"}, {"p1", "reviewer · claude · api · waiting"}, {"p2", "web"}}
	if !slices.Equal(got, want) {
		t.Errorf("panes:\n%q\nwant\n%q", got, want)
	}
	for _, done := range [][]string{{"agent", "explain"}, {"agent", "prompt", "-wait"}, {"show", "p1"}, {"wait", "-state", "done"}} {
		if got := compWords(compCandidates(done, "p2")); !slices.Equal(got, []string{"p2"}) {
			t.Errorf("conch %s: %q", strings.Join(done, " "), got)
		}
	}
	if got := compCandidates([]string{"send", "p1"}, ""); got != nil {
		t.Errorf("send's text is not a pane: %q", got)
	}
	// Another machine's panes are an ssh away: not asked for on a tab.
	if got := compCandidates([]string{"-m", "busybox", "read"}, ""); got != nil {
		t.Errorf("a remote machine's panes: %q", got)
	}
	if got := compCandidates([]string{"-m", "local", "read"}, "p2"); len(got) != 1 {
		t.Errorf("-m local is this computer: %q", got)
	}
	if got := compCandidates([]string{"project", "rm"}, ""); !slices.Equal(got, []compItem{{"api", "API"}}) {
		t.Errorf("projects: %q", got)
	}
}

// TestCompletionGivesUpQuickly: a server that takes the connection and never
// answers costs a tab its wait and no more.
func TestCompletionGivesUpQuickly(t *testing.T) {
	a4Env(t)
	ln, err := net.Listen("unix", config.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // held open, never answered
		}
	}()
	old := compWait
	compWait = 100 * time.Millisecond
	t.Cleanup(func() { compWait = old })
	start := time.Now()
	if got := compCandidates([]string{"read"}, ""); got != nil {
		t.Errorf("a silent server: %q", got)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestCompletionFormat(t *testing.T) {
	c := compItem{"a:b", "two\nlines  here"}
	for shell, want := range map[string]string{
		"bash": "a:b",
		"zsh":  `a\:b:two lines here`,
		"fish": "a:b\ttwo lines here",
	} {
		if got := compFormat(shell, c); got != want {
			t.Errorf("%s: %q, want %q", shell, got, want)
		}
	}
	if got := compFormat("zsh", compItem{"zsh", ""}); got != "zsh" {
		t.Errorf("no description in zsh: %q", got)
	}
	if got := compFormat("fish", compItem{"fish", ""}); got != "fish" {
		t.Errorf("no description in fish: %q", got)
	}

	// Too few words: nothing, and no error into somebody's command line.
	out, errOut := a4Capture(t, "", func() { runComplete([]string{"bash"}) })
	if out != "" || errOut != "" {
		t.Errorf("with no words: %q %q", out, errOut)
	}
}

func TestCompletionCommand(t *testing.T) {
	a4Env(t)
	for shell, want := range map[string]string{"bash": "complete -o default -F _conch conch", "zsh": "#compdef conch", "fish": "complete -c conch"} {
		out, _ := a4Capture(t, "", func() {
			if err := runCompletion([]string{shell}); err != nil {
				t.Errorf("%s: %v", shell, err)
			}
		})
		if !strings.Contains(out, want) || !strings.Contains(out, "conch __complete "+shell) {
			t.Errorf("%s script:\n%s", shell, out)
		}
	}
	if err := runCompletion([]string{"tcsh"}); err == nil || !strings.Contains(err.Error(), `not "tcsh"`) {
		t.Errorf("an unknown shell: %v", err)
	}
	// No shell: how to turn it on, the one in use first.
	t.Setenv("SHELL", "/usr/local/bin/fish")
	err := runCompletion(nil)
	if err == nil || !strings.Contains(err.Error(), "To turn it on:\n  fish:") || !strings.Contains(err.Error(), "Homebrew") {
		t.Errorf("no shell: %v", err)
	}
}

// completionPath puts a `conch` on PATH that is this test binary running
// main, so the scripts can be run in the shells they are for.
func completionPath(t *testing.T) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nA4_HELPER_MODE=main exec '" + exe + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "conch"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestCompletionInBash runs the bash script as bash would on a tab, with
// macOS's bash 3.2 in mind: no associative arrays, no mapfile.
func TestCompletionInBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	a4Env(t)
	completionPath(t)
	for _, c := range []struct{ line, cword, want string }{
		{"conch agent in", "2", "install"},
		{"conch '' ", "1", ""}, // the whole list; checked below
		{"conch -m local sta", "3", "status"},
		{"conch completion z", "2", "zsh"},
	} {
		script := completionBash + `COMP_WORDS=(` + c.line + `); COMP_CWORD=` + c.cword +
			`; _conch; printf '%s\n' "${COMPREPLY[@]}"`
		out, err := exec.Command(bash, "--norc", "--noprofile", "-c", script).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.line, err, out)
		}
		got := strings.TrimSpace(string(out))
		if c.want == "" {
			if !strings.Contains(got, "task\nnew\nstatus") {
				t.Errorf("%s: %q", c.line, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.line, got, c.want)
		}
	}
}

// TestCompletionInZsh loads the script after compinit, as a .zshrc does,
// and asks zsh what it would complete.
func TestCompletionInZsh(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("no zsh")
	}
	a4Env(t)
	completionPath(t)
	t.Setenv("ZDOTDIR", t.TempDir()) // no .zshrc of the developer's
	script := `autoload -U compinit && compinit -u -D
eval "$(cat <<'CONCH'
` + completionZsh + `CONCH
)"
print -r -- "registered:${_comps[conch]}"
_describe() { print -r -l -- "${(@P)${@[-1]}}" }
_files() { print -r -- files }
words=(conch agent in); CURRENT=3; _conch
words=(conch project add ''); CURRENT=4; _conch`
	out, err := exec.Command(zsh, "-f", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("zsh: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{"registered:_conch", "install:install an agent", "files"} {
		if !strings.Contains(got, want) {
			t.Errorf("zsh does not say %q:\n%s", want, got)
		}
	}
}

func TestCompletionFishParses(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("no fish")
	}
	path := filepath.Join(t.TempDir(), "conch.fish")
	os.WriteFile(path, []byte(completionFish), 0o644)
	if out, err := exec.Command(fish, "--no-execute", path).CombinedOutput(); err != nil {
		t.Errorf("fish: %v\n%s", err, out)
	}
}
