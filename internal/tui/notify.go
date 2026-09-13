package tui

import (
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/amitghadge/conch/internal/config"
	"github.com/amitghadge/conch/internal/proto"
)

// notify alerts the user as configured: a desktop notification, a sound
// and/or the terminal bell. It does nothing when notifications are off.
func notify(cfg config.NotifyCfg, title, body string) tea.Cmd {
	if !cfg.Enabled || (!cfg.Desktop && !cfg.Sound && !cfg.Bell) {
		return nil
	}
	return func() tea.Msg {
		if cfg.Bell {
			_, _ = os.Stdout.WriteString("\a")
		}
		if cfg.Sound {
			playSound()
		}
		if cfg.Desktop {
			var cmd *exec.Cmd
			switch runtime.GOOS {
			case "darwin":
				// Pass text as arguments so nothing needs AppleScript quoting.
				cmd = exec.Command("osascript",
					"-e", "on run argv",
					"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
					"-e", "end run", title, body)
			default:
				if _, err := exec.LookPath("notify-send"); err == nil {
					cmd = exec.Command("notify-send", title, body)
				}
			}
			if cmd != nil {
				_ = cmd.Run()
			}
		}
		return nil
	}
}

// playSound plays a short system sound without waiting for it.
func playSound() {
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"afplay", "/System/Library/Sounds/Glass.aiff"}}
	default:
		candidates = [][]string{
			{"paplay", "/usr/share/sounds/freedesktop/stereo/complete.oga"},
			{"canberra-gtk-play", "-i", "complete"},
			{"aplay", "-q", "/usr/share/sounds/alsa/Front_Center.wav"},
		}
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		if len(c) > 1 && strings.HasPrefix(c[len(c)-1], "/") {
			if _, err := os.Stat(c[len(c)-1]); err != nil {
				continue
			}
		}
		cmd := exec.Command(c[0], c[1:]...)
		if cmd.Start() == nil {
			go func() { _ = cmd.Wait() }()
			return
		}
	}
	_, _ = os.Stdout.WriteString("\a") // no player: fall back to the bell
}

// scopedPane is a pane together with its machine.
type scopedPane struct {
	machine string
	proto.PaneInfo
}

// attentionOrder lists panes that need the user: blocked ones first, then
// finished ones, each oldest first.
func attentionOrder(panes []scopedPane) []scopedPane {
	var waiting []scopedPane
	for _, p := range panes {
		if p.Agent.NeedsAttention() {
			waiting = append(waiting, p)
		}
	}
	sort.SliceStable(waiting, func(i, j int) bool {
		bi, bj := waiting[i].Agent.State == proto.AgentBlocked, waiting[j].Agent.State == proto.AgentBlocked
		if bi != bj {
			return bi
		}
		return waiting[i].Agent.Since.Before(waiting[j].Agent.Since)
	})
	return waiting
}

// nextAttention picks the pane to jump to from current (a scoped pane
// ID): the first waiting pane, or the one after current when current is
// itself waiting.
func nextAttention(panes []scopedPane, current string) (machine, id string) {
	order := attentionOrder(panes)
	if len(order) == 0 {
		return "", ""
	}
	next := order[0]
	for i, p := range order {
		if scoped(p.machine, p.ID) == current {
			next = order[(i+1)%len(order)]
			break
		}
	}
	return next.machine, next.ID
}
