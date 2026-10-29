package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/doctor"
)

// conch doctor, without having to know it exists. Somebody new opens
// conch, not a list of commands, and the reasons it does not do what they
// expected — no agent installed, git that cannot commit as anybody, a tmux
// prefix that is conch's too — were only ever said by a command they had
// not heard of. So the same checks run once in the background a moment
// after the TUI starts, a failure is on the status bar, and everything
// found is on this computer's page with its fix.
//
// The checks are cmd/conch's (they reach the server and the tools the way
// `conch doctor` does) and are handed in by Doctor; a model without them —
// every test — checks nothing.

// healthDelay lets the start-up settle first: connecting, listing panes,
// the agents' own status. Nothing here is urgent enough to compete.
const healthDelay = 2 * time.Second

// healthPageMax is how many problems the machine page lists before it
// points at the menu for the rest.
const healthPageMax = 4

// healthMsg is a finished run of the checks.
type healthMsg struct{ checks []doctor.Check }

// healthStartMsg starts a run.
type healthStartMsg struct{}

// Doctor gives the TUI the checks `conch doctor` runs.
func (m Model) Doctor(run func() []doctor.Check) Model {
	m.doctor = run
	return m
}

// healthAfter starts the checks after d.
func (m Model) healthAfter(d time.Duration) tea.Cmd {
	if m.doctor == nil {
		return nil
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return healthStartMsg{} })
}

// checkHealth runs the checks in the background; one run at a time.
func (m *Model) checkHealth() tea.Cmd {
	if m.doctor == nil || m.healthRunning {
		return nil
	}
	m.healthRunning = true
	run := m.doctor
	return func() tea.Msg { return healthMsg{run()} }
}

// gotHealth keeps what a run found, and refreshes the menu if it is open.
func (m *Model) gotHealth(msg healthMsg) {
	m.healthRunning = false
	m.health = msg.checks
	if mu, ok := m.overlay.(*menu); ok && mu.health {
		fresh := newHealthMenu(*m)
		fresh.sel = min(mu.sel, max(len(fresh.items)-1, 0))
		m.overlay = fresh
	}
}

// healthProblems are the checks that found something. The server's own
// state is left to the page, which already says it, and the status of a
// server this TUI is talking to is not news.
func (m Model) healthProblems() []doctor.Check {
	var out []doctor.Check
	for _, c := range m.health {
		if c.Status != doctor.OK && c.Name != "server" {
			out = append(out, c)
		}
	}
	return out
}

// healthStatusItem is the status bar's word on a failure: only failures,
// since a warning (gh not installed, say) can be somebody's settled choice
// and a bar that nags for ever is a bar people stop reading.
func (m Model) healthStatusItem() (statusItem, bool) {
	_, fails := doctor.Count(m.healthProblems())
	if fails == 0 {
		return statusItem{}, false
	}
	return statusItem{text: styleErr.Render("✗ " + count(fails, "thing") + " to fix"),
		act: func(m *Model) tea.Cmd { return m.openHealth() }}, true
}

// healthLines are the problems on this computer's page, each with its fix,
// cut to the page's width.
func (m Model) healthLines(cols int) []string {
	problems := m.healthProblems()
	if len(problems) == 0 {
		return nil
	}
	room := max(cols-4, 10)
	lines := []string{""}
	for i, c := range problems {
		if i == healthPageMax {
			lines = append(lines, styleMuted.Render(fmt.Sprintf("%d more · ⚙ Settings → Check conch…", len(problems)-i)))
			break
		}
		style := styleWarn
		if c.Status == doctor.Fail {
			style = styleErr
		}
		lines = append(lines, style.Render(ansi.Truncate(c.Mark()+" "+c.Name+": "+c.Detail, room, "…")))
		if c.Fix != "" {
			lines = append(lines, styleMuted.Render(ansi.Truncate("  → "+c.Fix, room, "…")))
		}
	}
	return lines
}

// openHealth shows every check and what can be done about them, and asks
// again: the menu is opened to see how things stand now.
func (m *Model) openHealth() tea.Cmd {
	cmd := m.checkHealth()
	m.overlay = newHealthMenu(*m)
	return cmd
}

func newHealthMenu(m Model) *menu {
	var items []menuItem
	info := func(s string) { items = append(items, menuItem{label: s}) }
	switch {
	case m.doctor == nil:
		info(styleMuted.Render("  conch doctor, in a shell, checks this computer"))
	case len(m.health) == 0:
		info(styleWork.Render("  checking…"))
	default:
		for _, c := range m.health {
			style := styleOK
			switch c.Status {
			case doctor.Warn:
				style = styleWarn
			case doctor.Fail:
				style = styleErr
			}
			info(style.Render("  "+c.Mark()+" "+padRight(c.Name, 9)) + " " + c.Detail)
			if c.Fix != "" {
				info(styleMuted.Render("    " + strings.Repeat(" ", 9) + " → " + c.Fix))
			}
		}
		summary := doctor.Summary(m.health)
		if m.healthRunning {
			summary = "checking again…"
		}
		info("")
		info(styleBold.Render("  " + summary))
	}
	first := len(items)
	for _, c := range m.health {
		if c.Status == doctor.OK || c.Fix == "" {
			continue
		}
		fix := c.Fix
		items = append(items, menuItem{label: "Copy the " + c.Name + " fix: " + fix, run: func(m *Model) tea.Cmd {
			putClipboard(fix)
			m.setFlash("copied — paste it in a terminal (n opens one)", false)
			return nil
		}})
	}
	if m.doctor != nil {
		items = append(items, menuItem{key: "r", label: "Check again", run: func(m *Model) tea.Cmd { return m.openHealth() }})
	}
	if len(m.health) > 0 {
		text := doctor.Text(m.health)
		items = append(items, menuItem{label: "Copy all of it", run: func(m *Model) tea.Cmd {
			putClipboard(text)
			m.setFlash("checks copied", false)
			return nil
		}})
	}
	return &menu{title: " Check conch ", items: items, sel: min(first, len(items)-1), x: 4, y: 2, health: true}
}
