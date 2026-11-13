package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestSafeView(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"plain", "hello\nworld", "hello\nworld"},
		{"colour", "\x1b[38;2;1;2;3mx\x1b[0m", "\x1b[38;2;1;2;3mx\x1b[0m"},
		{"underline colour", "\x1b[58:2::1:2:3mx", "\x1b[58:2::1:2:3mx"},
		{"link", "\x1b]8;;https://e.x/a\x1b\\go\x1b]8;;\x1b\\", "\x1b]8;;https://e.x/a\x1b\\go\x1b]8;;\x1b\\"},
		{"link, BEL", "\x1b]8;;u\x07go\x1b]8;;\x07", "\x1b]8;;u\x07go\x1b]8;;\x07"},
		{"clipboard", "a\x1b]52;c;aGk=\x07b", "ab"},
		{"clipboard, ST", "a\x1b]52;c;aGk=\x1b\\b", "ab"},
		{"title", "a\x1b]2;owned\x07b", "ab"},
		{"OSC ended by another escape", "a\x1b]52;c;x\x1b[31mb", "a\x1b[31mb"},
		{"unended OSC", "a\x1b]52;c;x", "a"},
		{"clear screen", "a\x1b[2Jb", "ab"},
		{"cursor move", "a\x1b[10;10Hb", "ab"},
		{"private mode", "a\x1b[?1049hb", "ab"},
		{"DCS", "a\x1bPq#0;2;0;0;0\x1b\\b", "ab"},
		{"APC (kitty graphics)", "a\x1b_Gf=100;AAAA\x1b\\b", "ab"},
		{"reset", "a\x1bcb", "ab"},
		{"ESC last", "a\x1b", "a"},
		{"not a CSI", "a\x1b[\x01b", "ab"},
		{"C0", "a\x07\x08\x0d\x00b", "ab"},
		{"tab", "a\tb", "a b"},
		{"DEL", "a\x7fb", "ab"},
		{"C1 CSI", "a\u009b2Jb", "a2Jb"},
		{"C1 OSC", "a\u009d52;c;x\u009cb", "a52;c;xb"},
		{"raw 0x9b", "a\x9b2Jb", "a�2Jb"},
		{"wide and box drawing", "│ 日本 ✳", "│ 日本 ✳"},
		{"link with a control in it", "\x1b]8;;u\x05\x07go", "go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := safeView(tc.in); got != tc.want {
				t.Fatalf("safeView(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// noTerminalEscapes fails when s holds anything but conch's own colours
// and links.
func noTerminalEscapes(t *testing.T, what, s string) {
	t.Helper()
	for _, bad := range []string{"\x1b]52", "\x1b]2;", "\x1b]0;", "\x07", "\x1b[2J", "\x7f"} {
		if strings.Contains(s, bad) {
			t.Errorf("%s: drawn with %q", what, bad)
		}
	}
	for _, r := range s {
		if r >= 0x80 && r <= 0x9f {
			t.Errorf("%s: drawn with C1 %U", what, r)
			break
		}
	}
	if strings.ContainsRune(ansi.Strip(s), '\x1b') {
		t.Errorf("%s: an escape that is not conch's styling: %q", what, ansi.Strip(s))
	}
}

// A pane's name and title, and an agent's message, are whatever a program
// or an agent set — an older server sends them as they were set. Drawn,
// they reach the person's own terminal: nothing in them may act on it, and
// a line break in one may not push the screen past its height.
func TestScreenDrawsNoEscapesFromPanes(t *testing.T) {
	m := a2Model()
	mach := m.machines[0]
	agent := proto.PaneInfo{ID: "p1", State: proto.PaneRunning, ProjectID: "r1", Cwd: "/src/api",
		Name: "ev\x1b]52;c;cHduZWQ=\x07il\nname", CustomName: true,
		Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentBlocked, Message: "ok?\x1b]2;x\x07\n\n\nyes"}}
	shell := proto.PaneInfo{ID: "p3", State: proto.PaneRunning, Title: "t\u009d2;owned\u009c\x1b[2J\nitle"}
	m.handleEvent(mach, proto.Message{Event: proto.EventPaneUpdated, Data: proto.Marshal(agent)})
	m.handleEvent(mach, proto.Message{Event: proto.EventPaneCreated, Data: proto.Marshal(shell)})
	if got := mach.panes[0].Name; got != "ev·]52;c;cHduZWQ=·il·name" {
		t.Errorf("stored as %q", got)
	}
	m.expanded[projectNodeID(localMachine, "r1")] = true
	m.rebuild()
	for _, size := range [][2]int{{160, 40}, {40, 12}, {20, 5}} {
		m.width, m.height, m.sidebarW = size[0], size[1], min(40, size[0]/2)
		out := m.View()
		noTerminalEscapes(t, "the screen", out)
		if lines := strings.Count(out, "\n") + 1; lines > m.height {
			t.Errorf("%dx%d: %d lines", m.width, m.height, lines)
		}
		if size[0] == 160 && !strings.Contains(ansi.Strip(out), "ev·]52") {
			t.Errorf("the pane is not on screen:\n%s", ansi.Strip(out))
		}
	}
}

// What an agent asks to be shown with — its reason, a tab's name — is its
// own words.
func TestViewShowTextIsCleaned(t *testing.T) {
	m := splitModel()
	m.showPanes(localMachine, proto.ViewShowParams{Panes: []string{"p1", "p2"},
		Why: "the agent\x1b]52;c;aGk=\x07", Name: "tab\x1b]2;x\x07\n" + strings.Repeat("n", 100)})
	if name := m.tab().name; strings.ContainsAny(name, "\x1b\x07\n") || ansi.StringWidth(name) > maxShownTabName {
		t.Fatalf("tab named %q", name)
	}
	noTerminalEscapes(t, "the flash", m.flash)
}

// A paste into the palette is drawn as it is typed: its escapes go.
func TestPalettePasteDropsEscapes(t *testing.T) {
	m := splitModel()
	p := &cmdPalette{}
	p.update(m, tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune("find\x1b]52;c;aGk=\x07 me")})
	p.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("\x1b[2J!")})
	if strings.ContainsAny(p.query, "\x1b\x07") {
		t.Fatalf("query %q", p.query)
	}
}
