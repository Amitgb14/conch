package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// stubShellTheme makes the saved theme and the nudges the test's.
func stubShellTheme(t *testing.T, theme *string) *[]int {
	t.Helper()
	oldTheme, oldNudge := savedShellTheme, nudgeShell
	t.Cleanup(func() { savedShellTheme, nudgeShell = oldTheme, oldNudge })
	var mu sync.Mutex
	var nudged []int
	savedShellTheme = func() string { return *theme }
	nudgeShell = func(pid int) error { mu.Lock(); nudged = append(nudged, pid); mu.Unlock(); return nil }
	return &nudged
}

// touchConfig moves config.toml, as a save does.
func touchConfig(t *testing.T, n int) {
	t.Helper()
	os.WriteFile(config.File(), []byte(strings.Repeat("#", n)), 0o600)
}

// The saved theme is written where the shells look, and the zsh panes —
// only those, only running — are nudged when it changes; a config.toml
// that moved without the theme changing nudges nobody.
func TestFollowShellTheme(t *testing.T) {
	a5IsolateEnv(t)
	s, dir := a5Server(t)
	theme := ""
	nudged := stubShellTheme(t, &theme)

	// A program called zsh, and one that is not.
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "zsh"), []byte("#!/bin/sh\nsleep 30\n"), 0o755)
	z, perr := s.create(proto.PaneCreateParams{Command: []string{filepath.Join(bin, "zsh")}, Cwd: dir, NoProject: true})
	if perr != nil {
		t.Fatal(perr)
	}
	if _, perr := s.create(proto.PaneCreateParams{Command: []string{"/bin/sh", "-c", "sleep 30"}, Cwd: dir, NoProject: true}); perr != nil {
		t.Fatal(perr)
	}
	file := s.shellThemeFile()

	stamp := s.followShellTheme("")
	if _, err := os.Stat(file); err == nil || len(*nudged) != 0 || stamp != "none" {
		t.Fatalf("no config, no theme: wrote one, or nudged %v (%s)", *nudged, stamp)
	}
	if s.followShellTheme(stamp) != stamp {
		t.Fatal("nothing moved")
	}

	theme = "agnoster"
	touchConfig(t, 1)
	stamp = s.followShellTheme(stamp)
	if b, _ := os.ReadFile(file); string(b) != "agnoster" || !slices.Equal(*nudged, []int{z.PID}) {
		t.Fatalf("saved agnoster: file %q, nudged %v, want [%d]", b, *nudged, z.PID)
	}
	// Nothing moved: config.toml is not even read.
	theme = "bira"
	if s.followShellTheme(stamp) != stamp || len(*nudged) != 1 {
		t.Fatalf("read a file that did not move: %v", *nudged)
	}
	theme = "agnoster"
	touchConfig(t, 2)
	stamp = s.followShellTheme(stamp)
	if len(*nudged) != 1 {
		t.Fatalf("another setting saved, the theme the same: nudged %v", *nudged)
	}
	for _, bad := range []string{"a/b", "x y"} {
		theme = bad
		touchConfig(t, 3+len(bad))
		stamp = s.followShellTheme(stamp)
		if b, _ := os.ReadFile(file); string(b) != "" {
			t.Fatalf("theme %q written as %q", bad, b)
		}
		theme = "agnoster"
		touchConfig(t, 10)
		stamp = s.followShellTheme(stamp)
	}
	theme = ""
	touchConfig(t, 20)
	n := len(*nudged)
	stamp = s.followShellTheme(stamp)
	if b, err := os.ReadFile(file); err != nil || string(b) != "" || len(*nudged) != n+1 {
		t.Fatalf("back to .zshrc's: %q %v, nudged %v", b, err, *nudged)
	}

	// An exited zsh is not nudged.
	s.mu.Lock()
	e := s.panes[z.ID]
	s.mu.Unlock()
	e.p.Close()
	theme = "ys"
	touchConfig(t, 30)
	n = len(*nudged)
	s.followShellTheme(stamp)
	if len(*nudged) != n {
		t.Fatalf("an exited pane nudged: %v", *nudged)
	}

	// A theme file that cannot be written nudges nobody.
	s.configDir = filepath.Join(dir, "nowhere")
	os.WriteFile(s.configDir, nil, 0o600)
	theme = "candy"
	touchConfig(t, 40)
	s.followShellTheme("x")
	if len(*nudged) != n {
		t.Fatalf("nudged with nothing written: %v", *nudged)
	}
}

// The follower runs until the server stops.
func TestRunShellThemeStops(t *testing.T) {
	a5IsolateEnv(t)
	s, _ := a5Server(t)
	theme := "agnoster"
	stubShellTheme(t, &theme)
	old := shellThemeEvery
	shellThemeEvery = 5 * time.Millisecond
	t.Cleanup(func() { shellThemeEvery = old })
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() { s.runShellTheme(quit); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, _ := os.ReadFile(s.shellThemeFile()); string(b) == "agnoster" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never wrote the theme")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(quit)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop")
	}
}

// A real zsh started through the wrapper takes the theme it was given,
// then the one saved while it waits at its prompt — redrawn in place, with
// nothing typed — then .zshrc's own when the setting is cleared. A zsh
// not started this way ignores the nudge.
func TestOpenShellFollowsTheSavedTheme(t *testing.T) {
	home := a5IsolateEnv(t)
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("no zsh")
	}
	s, dir := a5Server(t)
	theme := ""
	stubShellTheme(t, &theme)
	nudgeShell = func(pid int) error { return realNudge(pid) }

	omz := filepath.Join(home, "omz")
	os.MkdirAll(filepath.Join(omz, "themes"), 0o755)
	for name, p := range map[string]string{"own": "OWN", "first": "FIRST", "second": "SECOND"} {
		os.WriteFile(filepath.Join(omz, "themes", name+".zsh-theme"), []byte("PROMPT='"+p+"%# '\n"), 0o644)
	}
	os.WriteFile(filepath.Join(home, ".zshrc"), []byte("export ZSH="+omz+"\nZSH_THEME=own\nsource $ZSH/themes/$ZSH_THEME.zsh-theme\n"), 0o644)
	t.Setenv("SHELL", zsh)

	info, perr := s.create(proto.PaneCreateParams{ShellTheme: "first", Cwd: dir, Cols: 40, Rows: 6, NoProject: true})
	if perr != nil {
		t.Fatal(perr)
	}
	s.mu.Lock()
	p := s.panes[info.ID].p
	s.mu.Unlock()
	screen := func(want string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			lines := p.PlainLines()
			if last := lastNonBlank(lines); strings.HasPrefix(last, want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("prompt never became %q: %q", want, lines)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	screen("FIRST%")

	stamp := s.followShellTheme("")
	theme = "second"
	touchConfig(t, 1)
	stamp = s.followShellTheme(stamp)
	screen("SECOND%")

	theme = ""
	touchConfig(t, 2)
	s.followShellTheme(stamp)
	screen("OWN%")

	// A zsh of somebody else's, with the same .zshrc, takes no notice.
	plain, perr := s.create(proto.PaneCreateParams{Command: []string{zsh, "-i"}, Cwd: dir, Cols: 40, Rows: 6, NoProject: true})
	if perr != nil {
		t.Fatal(perr)
	}
	s.mu.Lock()
	pp := s.panes[plain.ID].p
	s.mu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.HasSuffix(lastNonBlank(pp.PlainLines()), "%") {
		if time.Now().After(deadline) {
			t.Fatalf("plain zsh: %q", pp.PlainLines())
		}
		time.Sleep(20 * time.Millisecond)
	}
	before := lastNonBlank(pp.PlainLines())
	realNudge(plain.PID)
	time.Sleep(300 * time.Millisecond)
	if pp.Info().State != proto.PaneRunning || lastNonBlank(pp.PlainLines()) != before {
		t.Fatalf("a zsh without the wrapper: %s %q", pp.Info().State, pp.PlainLines())
	}
}

var realNudge = nudgeShell

func lastNonBlank(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return strings.TrimSpace(lines[i])
		}
	}
	return ""
}
