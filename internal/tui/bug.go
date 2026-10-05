package tui

import (
	"os"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/report"
)

// Reporting a problem from the tree, in one key. What conch knows about
// itself is worth more than what somebody can remember to write down — the
// build it is running, the build of the server it is talking to (not always
// the same one), what is missing between them, how big the terminal is —
// and none of it is the sort of thing anybody types into an issue by hand.
//
// The report carries facts and never contents (internal/report), which is
// what makes it safe to put behind one key: nothing has to be read through
// before it is handed over. And nothing leaves this computer here either —
// the browser opens with the words in it, and the person presses submit.

// bugFacts is this conch as a report describes it.
func (m Model) bugFacts() report.Facts {
	f := report.Facts{
		Version:  proto.Version,
		Build:    buildinfo.Build(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Terminal: report.Terminal{
			Program: os.Getenv("TERM_PROGRAM"),
			Term:    os.Getenv("TERM"),
			Cols:    m.width,
			Rows:    m.height,
			Icons:   m.cfg.UI.Icons,
			Tmux:    os.Getenv("TMUX") != "",
			SSH:     os.Getenv("SSH_CONNECTION") != "",
		},
		Agents: map[string]int{},
		// The last thing conch said went wrong, which is usually what the
		// person is reporting — its own words, naming no file.
		LastError: lastFlashError(m),
	}
	if f.Terminal.Icons == "" {
		f.Terminal.Icons = string(iconsText)
	}
	for _, mach := range m.machines {
		f.Machines++
		if mach.state == stateOnline {
			f.MachinesOnline++
		}
		for _, p := range mach.panes {
			if p.State != proto.PaneRunning {
				continue
			}
			f.Server.Panes++
			if p.Agent != nil && p.Agent.Name != "" {
				f.Agents[p.Agent.Name]++
			}
		}
	}
	// The server of the machine the cursor is on: the one a problem here
	// is about. This computer's when the cursor says nothing.
	if mach := m.machine(m.reportMachine()); mach != nil && mach.c != nil {
		f.Server.Version = mach.server.Version
		f.Server.Build = mach.server.Build
		f.Server.Platform = mach.server.Platform
		f.Server.PID = mach.server.PID
		f.Server.Started = mach.server.Started
		f.Server.LoadedAt = mach.server.LoadedAt
		f.Server.Missing = mach.c.MissingCapabilities(proto.Capabilities)
	}
	return f
}

// openBugReport shows the report and what can be done with it. It is a menu
// rather than a dialog because there is nothing to fill in: the report is
// already written, and the only question is where it goes.
func (m *Model) openBugReport() tea.Cmd {
	facts := m.bugFacts()
	text := facts.Text()
	items := []menuItem{{label: styleMuted.Render("  Nothing is sent until you submit it in the browser.")}}
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		items = append(items, menuItem{label: styleMuted.Render("  " + line)})
	}
	items = append(items,
		menuItem{label: "Open a GitHub issue with this in it", run: func(m *Model) tea.Cmd {
			link := facts.IssueURL("")
			if err := openInBrowser(link); err != nil {
				m.setFlash("could not open a browser: "+err.Error(), true)
				return nil
			}
			m.setFlash("opening an issue — nothing is sent until you submit it", false)
			return nil
		}},
		menuItem{label: "Copy it", run: func(m *Model) tea.Cmd {
			putClipboard(text)
			m.setFlash("report copied", false)
			return nil
		}},
		menuItem{label: "Copy the server log's path (" + m.tildify(localMachine, config.ServerLogPath()) + ")",
			run: func(m *Model) tea.Cmd {
				// The log is not in the report and never will be: it holds
				// project paths, branch names and pane titles. Handing over
				// the path is the person's to do, having read it.
				putClipboard(config.ServerLogPath())
				m.setFlash("log path copied — it holds paths and branch names, so read it before you attach it", false)
				return nil
			}})
	m.overlay = &menu{title: " Report a problem ", items: items, sel: len(items) - 3, x: 4, y: 2}
	return nil
}

// lastFlashError is the last thing conch told the person went wrong, which
// is usually what they are reporting; a flash that was not an error says
// nothing about a problem.
func lastFlashError(m Model) string {
	if m.flashIsErr {
		return m.flash
	}
	return ""
}

// reportMachine is the machine a report is about: the one the cursor is on,
// or this computer.
func (m Model) reportMachine() string {
	if r, ok := m.selectedRow(); ok && r.machine != "" {
		return r.machine
	}
	return localMachine
}
