package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestBugReportIsFactsOnly: one key hands this over, so it must carry
// nothing that has to be read through first — no path, no project, no
// branch, no pane title, nothing off a screen.
func TestBugReportIsFactsOnly(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.width, m.height = 247, 48
	m.cfg.UI.Icons = iconsNerd
	mach := m.machines[0]
	mach.c = a2Client(proto.CapAgentPrompt)
	mach.server = proto.HelloResult{Version: "0.1.7-dev", Build: "abc123", Platform: "darwin/arm64", PID: 4242}
	m.rebuild()

	text := m.bugFacts().Text()
	// What a2Model's fixture holds, offered to the report and refused by
	// it: the project's name and path, a branch, a pane's title.
	for _, secret := range []string{"/src/api", "api", "feat", "main", "Add feature"} {
		for _, line := range strings.Split(text, "\n") {
			// "api" is a substring of plenty; the point is that no line of
			// the report is *about* the project, branch or title.
			if strings.Contains(line, "/src/") || strings.Contains(line, "Add feature") {
				t.Errorf("the report carries %q:\n%s", secret, text)
			}
		}
	}
	for _, want := range []string{"conch ", "server: 0.1.7-dev (build abc123, darwin/arm64), pid 4242",
		"panes:", "terminal: ", "247×48", "icons=nerd"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report does not say %q:\n%s", want, text)
		}
	}
}

// TestBugReportSaysWhatIsMissing: the commonest thing a report has to show
// is a stale server, and the TUI is the half that knows.
func TestBugReportSaysWhatIsMissing(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	mach := m.machines[0]
	mach.c = a2Client("agent.setup.v1") // an old server: nearly everything missing
	mach.server = proto.HelloResult{Version: "0.1.0", PID: 7}
	m.rebuild()
	text := m.bugFacts().Text()
	if !strings.Contains(text, "missing: ") {
		t.Fatalf("it says nothing about what the server lacks:\n%s", text)
	}
	// And the last error conch showed, which is usually what is being
	// reported — but only when it *was* an error.
	m.setFlash("out_of_scope: the claude agent in p1 may not reload the server", true)
	if !strings.Contains(m.bugFacts().Text(), "last error: out_of_scope") {
		t.Error("the last error is not in the report")
	}
	m.setFlash("report copied", false)
	if strings.Contains(m.bugFacts().Text(), "last error:") {
		t.Error("a flash that was not an error is reported as one")
	}
}

// TestBugReportMenu: what the key opens — the report on screen, and the
// three things that can be done with it. Nothing leaves this computer
// except through the browser, where the person sees it first.
func TestBugReportMenu(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.machines[0].c = a2Client()
	m.rebuild()
	m.openBugReport()
	mn, ok := m.overlay.(*menu)
	if !ok {
		t.Fatalf("overlay %#v", m.overlay)
	}
	var labels []string
	for _, it := range mn.items {
		labels = append(labels, ansi.Strip(it.label))
	}
	joined := strings.Join(labels, "\n")
	for _, want := range []string{"Nothing is sent until you submit it", "conch ",
		"Open a GitHub issue with this in it", "Copy it", "Copy the server log's path"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the menu has no %q:\n%s", want, joined)
		}
	}
	// The cursor starts on the thing most people want, not on a line of
	// the report, which does nothing.
	if mn.sel < 0 || mn.sel >= len(mn.items) || !strings.Contains(ansi.Strip(mn.items[mn.sel].label), "Open a GitHub issue") {
		t.Fatalf("the cursor starts on %d: %q", mn.sel, ansi.Strip(mn.items[mn.sel].label))
	}

	// Choosing it opens a link, and the link is a GitHub issue with the
	// facts in it. (openInBrowser is replaced in TestMain, so nothing
	// opens on whoever runs the tests.)
	opened := ""
	old := openInBrowser
	openInBrowser = func(url string) error { opened = url; return nil }
	t.Cleanup(func() { openInBrowser = old })
	mn.items[mn.sel].run(m)
	if !strings.HasPrefix(opened, "https://github.com/Amitgb14/conch/issues/new?") {
		t.Fatalf("it opened %q", opened)
	}
	if !strings.Contains(opened, "body=") || !strings.Contains(m.flash, "nothing is sent") {
		t.Errorf("link %q, flash %q", opened, m.flash)
	}

	// Copying puts the report on the clipboard and says so. putClipboard
	// is replaced in TestMain for the same reason.
	copied := ""
	oldCopy := putClipboard
	putClipboard = func(s string) { copied = s }
	t.Cleanup(func() { putClipboard = oldCopy })
	for _, it := range mn.items {
		if ansi.Strip(it.label) == "Copy it" {
			it.run(m)
		}
	}
	if !strings.Contains(copied, "conch ") || strings.Contains(copied, "/src/") {
		t.Fatalf("copied %q", copied)
	}
}

// TestBugButtonEarnsItsPlace: the 🐞 on the status bar opens the report in
// one click — but the bar keeps only its first four hints when it runs out
// of room and lets the rest go quietly, so a button that merely fitted
// would be taking "? keys" off bars that had it. It is there only when
// every hint still fits beside it.
func TestBugButtonEarnsItsPlace(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	m.machines[0].c = a2Client()

	narrow := 0
	for _, w := range []int{80, 100, 120, 140, 160, 200, 240} {
		m.width = w
		m.rebuild()
		line, hits := m.layoutStatus()
		plain := ansi.Strip(line)
		if got := ansi.StringWidth(line); got != w {
			t.Fatalf("width %d: the bar is %d wide", w, got)
		}
		bug := strings.Contains(plain, "🐞")
		keys := strings.Contains(plain, "keys")
		switch {
		case bug && !keys:
			t.Errorf("width %d: the button pushed the key hints off:\n%s", w, plain)
		case !bug:
			narrow++
			continue
		}
		// Where it is shown, it is clickable, and the click opens the
		// report rather than anything else.
		col := ansi.StringWidth(plain[:strings.Index(plain, "🐞")])
		var act func(m *Model) tea.Cmd
		for _, h := range hits {
			if col >= h.x0 && col < h.x1 {
				act = h.act
			}
		}
		if act == nil {
			t.Fatalf("width %d: the button is drawn but not clickable", w)
		}
		if w == 240 {
			act(m)
			mn, ok := m.overlay.(*menu)
			if !ok || mn.title != " Report a problem " {
				t.Fatalf("clicking it opened %#v", m.overlay)
			}
			m.overlay = nil
		}
	}
	if narrow == 0 {
		t.Error("the button is on every bar, so it is never the thing that gives way")
	}
}
