package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// The Oh My Zsh theme Settings saved, followed by the zsh panes already
// open: the server notices config.toml change, writes the theme where the
// shells look (zsh/theme, beside zshWrapper's files) and nudges each with
// SIGURG, which zshWrapper's TRAPURG answers by taking the theme and
// redrawing its prompt. The server, not whoever saved: the TUI, the app's
// settings page and an editor all write config.toml, and only the server
// knows the panes.

// shellThemeEvery is how often config.toml is looked at: a stat, and a read
// only when it moved.
var shellThemeEvery = time.Second

// savedShellTheme is [shell] omz_theme as config.toml has it now; tests put
// their own here.
var savedShellTheme = func() string {
	c, err := config.Load()
	if err != nil {
		return ""
	}
	return c.Shell.OMZTheme
}

// nudgeShell is how a shell is told; tests count instead.
var nudgeShell = func(pid int) error { return syscall.Kill(pid, syscall.SIGURG) }

func validShellTheme(t string) bool { return !strings.ContainsAny(t, "/\\ \t\n") }

func (s *Server) shellThemeFile() string { return filepath.Join(s.configDir, "zsh", "theme") }

// runShellTheme follows config.toml until quit.
func (s *Server) runShellTheme(quit <-chan struct{}) {
	t := time.NewTicker(shellThemeEvery)
	defer t.Stop()
	stamp := ""
	for {
		stamp = s.followShellTheme(stamp)
		select {
		case <-quit:
			return
		case <-t.C:
		}
	}
}

// followShellTheme writes the saved theme for the shells when config.toml
// has moved since stamp, and nudges them when it changed; it returns the
// file's new stamp.
func (s *Server) followShellTheme(stamp string) string {
	now := "none"
	if st, err := os.Stat(config.File()); err == nil {
		now = fmt.Sprint(st.ModTime().UnixNano(), st.Size())
	}
	if now == stamp {
		return stamp
	}
	theme := savedShellTheme()
	if !validShellTheme(theme) {
		theme = ""
	}
	file := s.shellThemeFile()
	if old, err := os.ReadFile(file); err == nil && string(old) == theme {
		return now
	} else if err != nil && theme == "" {
		// Never written, and nothing to say: shells started with none.
		return now
	}
	if os.MkdirAll(filepath.Dir(file), 0o700) != nil {
		return now
	}
	tmp := file + ".new"
	if os.WriteFile(tmp, []byte(theme), 0o600) != nil || os.Rename(tmp, file) != nil {
		return now
	}
	s.nudgeShells()
	return now
}

// nudgeShells tells every running zsh pane the theme moved. A zsh this
// server did not start through the wrapper ignores the signal, as does
// any other program.
func (s *Server) nudgeShells() {
	s.mu.Lock()
	var pids []int
	for _, e := range s.panes {
		info := e.p.Info()
		if info.State != proto.PaneRunning || info.PID <= 0 || len(info.Command) == 0 || filepath.Base(info.Command[0]) != "zsh" {
			continue
		}
		pids = append(pids, info.PID)
	}
	s.mu.Unlock()
	for _, pid := range pids {
		_ = nudgeShell(pid)
	}
}
