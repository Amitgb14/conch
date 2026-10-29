package tui

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/doctor"
)

var (
	healthGit    = doctor.Check{Name: "git", Status: doctor.Warn, Detail: "2.49.0; no name or email to commit as", Fix: `git config --global user.email you@example.com`}
	healthAgents = doctor.Check{Name: "agents", Status: doctor.Fail, Detail: "none installed; conch runs agents, so it needs one", Fix: "conch agent install claude"}
	healthServer = doctor.Check{Name: "server", Status: doctor.Warn, Detail: "pid 7, on another build", Fix: "conch server reload"}
	healthFine   = doctor.Check{Name: "terminal", Status: doctor.OK, Detail: "xterm-256color"}
)

// healthModel is a2Model handed checks that report what is given, and
// count how often they ran.
func healthModel(t *testing.T, checks ...doctor.Check) (*Model, *atomic.Int32) {
	t.Helper()
	a2Isolate(t)
	m := a2Model()
	m.machines[0].c = a2Client()
	m.rebuild()
	runs := &atomic.Int32{}
	*m = m.Doctor(func() []doctor.Check { runs.Add(1); return checks })
	return m, runs
}

// runHealth runs a command the way Bubble Tea would, and feeds what it
// says back into the model.
func runHealth(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run the checks")
	}
	msg, ok := cmd().(healthMsg)
	if !ok {
		t.Fatalf("the command did not run the checks")
	}
	next, _ := m.Update(msg)
	*m = next.(Model)
}

func menuText(mu *menu) string {
	var lines []string
	for _, it := range mu.items {
		lines = append(lines, ansi.Strip(it.label))
	}
	return strings.Join(lines, "\n")
}

// TestHealthWithoutDoctor: a model nobody handed the checks — every other
// test — runs nothing, and its page and bar say nothing about health.
func TestHealthWithoutDoctor(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	if cmd := m.healthAfter(time.Millisecond); cmd != nil {
		t.Error("checks scheduled with none to run")
	}
	if cmd := m.checkHealth(); cmd != nil || m.healthRunning {
		t.Error("checks run with none to run")
	}
	if _, ok := m.healthStatusItem(); ok || m.healthLines(80) != nil {
		t.Error("health shown with nothing checked")
	}
	// The menu still opens, and says where the checks are.
	m.openHealth()
	mu := m.overlay.(*menu)
	if !strings.Contains(menuText(mu), "conch doctor") {
		t.Errorf("menu:\n%s", menuText(mu))
	}
	for _, it := range mu.items {
		if strings.Contains(it.label, "Check again") {
			t.Error("Check again offered with nothing to run")
		}
	}
}

// TestHealthRunsAfterStart: Init schedules one run, the tick starts it,
// and a run already going is not started twice.
func TestHealthRunsAfterStart(t *testing.T) {
	m, runs := healthModel(t, healthFine)
	if m.healthAfter(healthDelay) == nil {
		t.Fatal("nothing scheduled after start")
	}
	next, cmd := m.Update(healthStartMsg{})
	*m = next.(Model)
	if !m.healthRunning {
		t.Fatal("not marked running")
	}
	if again := m.checkHealth(); again != nil {
		t.Error("a second run started while one was going")
	}
	runHealth(t, m, cmd)
	if runs.Load() != 1 || m.healthRunning || len(m.health) != 1 {
		t.Errorf("runs %d, running %v, health %v", runs.Load(), m.healthRunning, m.health)
	}
	// All good: nothing on the bar or the page.
	if _, ok := m.healthStatusItem(); ok || m.healthLines(120) != nil {
		t.Error("a clean bill of health is shown as a problem")
	}
}

// TestHealthBarIsForFailures: a failure is on the status bar and opens
// the menu; a warning alone is not, and nor is the server, which the page
// already speaks for.
func TestHealthBarIsForFailures(t *testing.T) {
	m, _ := healthModel(t)
	m.width, m.height = 200, 40
	m.health = []doctor.Check{healthGit, healthServer, healthFine}
	if _, ok := m.healthStatusItem(); ok {
		t.Error("warnings alone are on the bar")
	}
	if strings.Contains(ansi.Strip(m.statusBar()), "to fix") {
		t.Errorf("bar: %s", ansi.Strip(m.statusBar()))
	}

	m.health = append(m.health, healthAgents)
	it, ok := m.healthStatusItem()
	if !ok || ansi.Strip(it.text) != "✗ 1 thing to fix" {
		t.Fatalf("item %q, %v", ansi.Strip(it.text), ok)
	}
	if bar := ansi.Strip(m.statusBar()); !strings.Contains(bar, "✗ 1 thing to fix") {
		t.Errorf("not on the bar: %s", bar)
	}
	it.act(m)
	if mu, ok := m.overlay.(*menu); !ok || !mu.health {
		t.Fatalf("clicking it opened %#v", m.overlay)
	}
	// Every width keeps the bar to the width.
	for _, w := range []int{20, 40, 80, 120, 200} {
		m.width = w
		if got := ansi.StringWidth(m.statusBar()); got > w {
			t.Errorf("width %d: the bar is %d wide", w, got)
		}
	}
}

// TestHealthOnThePage: this computer's page lists each problem with its
// fix, cut to the page, and points at the menu past the first few.
func TestHealthOnThePage(t *testing.T) {
	m, _ := healthModel(t)
	m.health = []doctor.Check{healthFine, healthServer, healthGit, healthAgents}
	page := ansi.Strip(strings.Join(m.machinePage(m.machines[0], 120, 50), "\n"))
	for _, want := range []string{"! git: 2.49.0; no name", "→ git config --global user.email", "✗ agents: none installed", "→ conch agent install claude"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not say %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "on another build") || strings.Contains(page, "xterm-256color") {
		t.Errorf("the page lists the server or a check that passed:\n%s", page)
	}

	// Narrow: every line within the width.
	for _, cols := range []int{12, 30, 40} {
		for _, l := range m.healthLines(cols) {
			if w := ansi.StringWidth(l); w > max(cols-4, 10) {
				t.Errorf("cols %d: %d wide: %q", cols, w, ansi.Strip(l))
			}
		}
	}

	// More than the page holds.
	many := []doctor.Check{}
	for range healthPageMax + 2 {
		many = append(many, healthGit)
	}
	m.health = many
	text := ansi.Strip(strings.Join(m.healthLines(120), "\n"))
	if strings.Count(text, "! git") != healthPageMax || !strings.Contains(text, "2 more · ⚙ Settings → Check conch…") {
		t.Errorf("past the first %d:\n%s", healthPageMax, text)
	}
}

// TestHealthMenu: every check, the fixes to copy, and checking again,
// which redraws the menu when it finishes.
func TestHealthMenu(t *testing.T) {
	m, runs := healthModel(t, healthFine)
	m.health = []doctor.Check{healthFine, healthGit, healthAgents}
	cmd := m.openHealth()
	mu := m.overlay.(*menu)
	text := menuText(mu)
	for _, want := range []string{"✓ terminal", "! git", "✗ agents", "→ conch agent install claude", "checking again…",
		"Copy the git fix: git config", "Copy the agents fix: conch agent install claude", "Check again", "Copy all of it"} {
		if !strings.Contains(text, want) {
			t.Errorf("the menu has no %q:\n%s", want, text)
		}
	}
	// The cursor starts on the first fix, not on a line that only says.
	if got := ansi.Strip(mu.items[mu.sel].label); !strings.HasPrefix(got, "Copy the git fix") {
		t.Errorf("the cursor starts on %q", got)
	}
	mu.items[mu.sel].run(m)
	if lastClipboard != healthGit.Fix || !strings.Contains(m.flash, "copied") {
		t.Errorf("copied %q, flash %q", lastClipboard, m.flash)
	}

	// Opening it checked again, and the result redraws the menu in place.
	m.openHealth()
	runHealth(t, m, cmd)
	if runs.Load() != 1 {
		t.Errorf("%d runs", runs.Load())
	}
	mu = m.overlay.(*menu)
	if text := menuText(mu); !strings.Contains(text, "All good.") || strings.Contains(text, "Copy the") {
		t.Errorf("after the fixes:\n%s", text)
	}

	// r checks again from the menu.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	*m = next.(Model)
	runHealth(t, m, cmd)
	if runs.Load() != 2 {
		t.Errorf("r: %d runs", runs.Load())
	}

	// A result arriving with another overlay up leaves it alone.
	m.overlay = &menu{title: "other", items: []menuItem{{label: "x"}}}
	m.gotHealth(healthMsg{[]doctor.Check{healthAgents}})
	if m.overlay.(*menu).title != "other" {
		t.Error("a finished run replaced another menu")
	}

	// The menu fits a tiny terminal.
	m.width, m.height = 20, 5
	m.openHealth()
	b := m.overlay.(*menu).render(*m)
	for _, l := range b.lines {
		if w := ansi.StringWidth(l); w > 20 {
			t.Errorf("%d wide at 20: %q", w, ansi.Strip(l))
		}
	}
}

// TestHealthMenuBeforeFirstRun: opened before anything has come back, it
// says it is checking rather than listing nothing.
func TestHealthMenuBeforeFirstRun(t *testing.T) {
	m, _ := healthModel(t, healthAgents)
	cmd := m.openHealth()
	if text := menuText(m.overlay.(*menu)); !strings.Contains(text, "checking…") {
		t.Errorf("menu:\n%s", text)
	}
	runHealth(t, m, cmd)
	if text := menuText(m.overlay.(*menu)); !strings.Contains(text, "✗ agents") {
		t.Errorf("after the run:\n%s", text)
	}
}

// TestMenuLineThatOnlySays: enter or a click on a line with nothing to do
// — a line of the report, of the checks — leaves the menu up. It used to
// call a nil function and take the TUI down with it.
func TestMenuLineThatOnlySays(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	mu := &menu{title: "t", items: []menuItem{{label: "says"}, {label: "does", run: func(*Model) tea.Cmd { return nil }}}}
	m.overlay = mu
	if cmd := mu.run(m, 0); cmd != nil || m.overlay != mu {
		t.Errorf("enter on a line that only says: overlay %#v", m.overlay)
	}
	mu.run(m, 1)
	if m.overlay != nil {
		t.Error("a line that does something left the menu up")
	}
}

// TestHealthFromSettings: , → Check conch… opens the menu.
func TestHealthFromSettings(t *testing.T) {
	m, _ := healthModel(t)
	s, _ := newSettings(m)
	for _, it := range s.items(m) {
		if it.label == "Check conch…" {
			it.run(m)
			if mu, ok := m.overlay.(*menu); !ok || !mu.health {
				t.Fatalf("it opened %#v", m.overlay)
			}
			return
		}
	}
	t.Fatal("no Check conch… in Settings")
}
