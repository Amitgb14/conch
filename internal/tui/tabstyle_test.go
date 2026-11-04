package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// Tabs are cells with a rule between them, and the one on screen is
// underlined rather than filled with the accent; each tab keeps the
// columns it had, so clicks and drags land where they did.
func TestTabBarUnderlinesTheActiveTab(t *testing.T) {
	// In colour, or every style renders as the same plain text.
	old := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(old); applyTheme("conch", "") })
	lipgloss.SetColorProfile(termenv.TrueColor)
	applyTheme("conch", "")
	m, _ := a1Fixture(t, false)
	for _, id := range []string{"p1", "p2"} {
		m.cursor = paneNodeID(localMachine, id)
		m.newTab(viewOf(m.rows[indexOfRow(m.rows, m.cursor)]))
	}
	m.cursor = projectNodeID(localMachine, "r1") // the bar lists the project's tabs
	if n := len(m.visibleTabs()); n < 2 {
		t.Fatalf("%d tabs listed of %d", n, len(m.tabs))
	}
	w := m.mainRect().w
	bar, hits := m.tabBar(w)
	plain := ansi.Strip(bar)
	if strings.Count(plain, "│") < 2 {
		t.Fatalf("no rules between tabs: %q", plain)
	}
	if !strings.Contains(bar, tabUnderline+styleTabOn.Render(" 2 zsh ")) || tabUnderline == "" {
		t.Fatalf("the active tab is not drawn as one: %q", bar)
	}
	if strings.Contains(bar, styleSel.Render(" 2 zsh ")) {
		t.Fatal("the active tab is still the accent bar")
	}
	// Each hit is exactly where its text is, the rule taking the column
	// a space used to.
	for _, h := range hits {
		if h.tab < 0 {
			continue
		}
		if got := ansi.Cut(plain, h.x0, h.x1); !strings.HasPrefix(got, " ") || !strings.HasSuffix(got, " ") {
			t.Errorf("tab %d at [%d,%d) reads %q", h.tab, h.x0, h.x1, got)
		}
	}
	if ansi.StringWidth(bar) > w {
		t.Fatalf("bar %d wide in %d", ansi.StringWidth(bar), w)
	}
	for _, narrow := range []int{0, 1, 3, 8} {
		if b, _ := m.tabBar(narrow); ansi.StringWidth(b) > narrow {
			t.Errorf("width %d: bar %d wide", narrow, ansi.StringWidth(b))
		}
	}
}

func TestUnderlineColor(t *testing.T) {
	old := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
	lipgloss.SetColorProfile(termenv.TrueColor)
	// Colons, so a terminal without underline colours drops it whole.
	if got := underlineColor("#0F7B8A"); got != "\x1b[58:2::15:123:138m" {
		t.Fatalf("%q", got)
	}
	for _, bad := range []string{"", "teal", "#12", "#GGGGGG", "12"} {
		if got := underlineColor(lipgloss.Color(bad)); got != "" {
			t.Errorf("%q: %q", bad, got)
		}
	}
	lipgloss.SetColorProfile(termenv.Ascii)
	if got := underlineColor("#0F7B8A"); got != "" {
		t.Fatalf("no colours, yet %q", got)
	}
}

// The bar says whose tabs it lists, at its far end, where it fits.
func TestTabBarNamesItsGroup(t *testing.T) {
	m, _ := a1Fixture(t, false)
	a1Open(t, m, paneNodeID(localMachine, "p1"))
	m.cursor = projectNodeID(localMachine, "r1")
	w := m.mainRect().w
	bar, _ := m.tabBar(w)
	plain := strings.TrimRight(ansi.Strip(bar), " ")
	if !strings.HasSuffix(plain, "api") {
		t.Fatalf("bar %q", plain)
	}
	if ansi.StringWidth(bar) > w {
		t.Fatalf("bar %d wide in %d", ansi.StringWidth(bar), w)
	}
	// No room, no name: never cut, never over a tab.
	if bar, _ := m.tabBar(12); strings.Contains(ansi.Strip(bar), "api") {
		t.Fatalf("narrow bar %q", ansi.Strip(bar))
	}
}

// Tabs past the end of the bar leave nothing behind them: not a rule each.
func TestTabBarDrawsNoRulesForTabsThatDoNotFit(t *testing.T) {
	m, _ := a1Fixture(t, false)
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		m.cursor = paneNodeID(localMachine, id)
		m.newTab(viewOf(m.rows[indexOfRow(m.rows, m.cursor)]))
	}
	m.cursor = ""
	bar, hits := m.tabBar(30)
	plain := ansi.Strip(bar)
	if strings.Contains(plain, "││") {
		t.Fatalf("rules for tabs that did not fit: %q", plain)
	}
	tabs := 0
	for _, h := range hits {
		if h.tab >= 0 {
			tabs++
		}
	}
	if rules := strings.Count(plain, "│"); rules != tabs {
		t.Fatalf("%d rules for %d tabs drawn: %q", rules, tabs, plain)
	}
}
