package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestSelectionText(t *testing.T) {
	lines := []string{
		"\x1b[31mfirst\x1b[0m line   ",
		"second line",
		"third",
	}
	for _, tc := range []struct {
		name string
		sel  selection
		want string
	}{
		{"within a line", selection{ax: 0, ay: 0, bx: 4, by: 0}, "first"},
		{"backwards drag", selection{ax: 3, ay: 1, bx: 6, by: 0}, "line\nseco"},
		{"across lines trims trailing space", selection{ax: 6, ay: 0, bx: 2, by: 2}, "line\nsecond line\nthi"},
	} {
		if got := tc.sel.text(lines, 20); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSelectionHighlightKeepsWidth(t *testing.T) {
	lines := []string{"\x1b[32mhello\x1b[0m world", "next"}
	out := selection{ax: 2, ay: 0, bx: 7, by: 0}.highlight(lines, 20)
	if got := ansi.Strip(out[0]); got != "hello world" && got != "hello world         " {
		t.Fatalf("text changed: %q", got)
	}
	if out[1] != lines[1] {
		t.Fatal("unselected line changed")
	}
}

func TestWordAt(t *testing.T) {
	line := "  run go test ./internal/... now"
	from, to := wordAt(line, 16)
	if got := ansi.Cut(line, from, to+1); got != "./internal/..." {
		t.Fatalf("word %q", got)
	}
	if from, to := wordAt(line, 0); from != to {
		t.Fatal("space selected a word")
	}
}

func TestUIStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.json")
	if st := loadUIState(path); st.Expanded == nil || st.ShowAll == nil {
		t.Fatal("defaults must have maps")
	}
	want := uiState{Expanded: map[string]bool{"p:r1": false, "p:r1/branches": true}, ShowAll: map[string]bool{"r1": true}, SidebarWidth: 44}
	if err := saveUIState(path, want); err != nil {
		t.Fatal(err)
	}
	got := loadUIState(path)
	if got.SidebarWidth != 44 || got.Expanded["p:r1"] || !got.Expanded["p:r1/branches"] || !got.ShowAll["r1"] {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestApplyTheme(t *testing.T) {
	defer applyTheme("conch", "")
	applyTheme("nord", "")
	if colorAccent != themeByName("nord").accent {
		t.Fatalf("nord accent: %v", colorAccent)
	}
	applyTheme("nord", "orange")
	if colorAccent != accentColors["orange"] {
		t.Fatalf("accent override: %v", colorAccent)
	}
	applyTheme("nonsense", "#123456")
	if colorAccent != "#123456" || colorBorder != themes[0].border {
		t.Fatalf("unknown theme with hex accent: %v %v", colorAccent, colorBorder)
	}
}

func TestThemesAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, th := range themes {
		if th.name == "" || th.label == "" || th.name != strings.ToLower(th.name) || strings.ContainsAny(th.name, " \t") {
			t.Errorf("theme name/label: %q %q", th.name, th.label)
		}
		if seen[th.name] {
			t.Errorf("duplicate theme %q", th.name)
		}
		seen[th.name] = true
		for _, c := range []lipgloss.Color{th.accent, th.selFG, th.border, th.muted, th.ok, th.warn, th.err, th.work, th.selDim, th.merged} {
			if len(c) != 7 || c[0] != '#' || strings.Trim(strings.ToLower(string(c[1:])), "0123456789abcdef") != "" {
				t.Errorf("theme %q colour %q is not #rrggbb", th.name, c)
			}
		}
		if got := themeByName("  " + strings.ToUpper(th.name) + " "); got.name != th.name {
			t.Errorf("themeByName(%q) = %q", th.name, got.name)
		}
	}
	for _, name := range []string{"one-dark", "solarized", "rose-pine", "kanagawa", "everforest", "monokai", "github-dark", "ayu", "night-owl"} {
		if !seen[name] {
			t.Errorf("missing theme %q", name)
		}
	}
}

func TestKeyboardSelection(t *testing.T) {
	m := &Model{width: 100, height: 20, frames: map[string]*proto.Frame{}, subscribed: map[string]bool{}}
	m.machines = []*machine{{id: localMachine, label: "local", state: stateOnline, agents: map[string]bool{}, sizes: map[string][2]int{}}}
	m.frame = &proto.Frame{Lines: []string{"alpha beta", "gamma delta", "epsilon"}, History: 50}
	m.viewing = "p1"
	m.enterScrollMode()
	m.curY = 0
	press := func(keys ...string) tea.Cmd {
		var cmd tea.Cmd
		for _, k := range keys {
			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
			if k == "right" {
				msg = tea.KeyMsg{Type: tea.KeyRight}
			}
			cmd = m.scrollKey(msg)
		}
		return cmd
	}
	press("l", "l", "l", "l", "l", "l", "v", "j", "h", "h")
	cols, _ := m.paneArea()
	if got := m.sel.text(m.frame.Lines, cols); got != "beta\ngamma" {
		t.Fatalf("selected %q", got)
	}
	if cmd := press("y"); cmd == nil || m.scrollMode || m.sel != nil {
		t.Fatal("y should copy and leave scroll mode")
	}
}

type notes struct{ methods []string }

func (n *notes) Notify(method string, _ any) { n.methods = append(n.methods, method) }

func TestSelectOverMouseApp(t *testing.T) {
	m := &Model{width: 100, height: 30, viewMachine: localMachine, viewing: "p1",
		frame:    &proto.Frame{Mouse: true, Lines: []string{"copy this text", "second"}},
		machines: []*machine{{id: localMachine, panes: []proto.PaneInfo{{ID: "p1", Agent: &proto.AgentStatus{Name: "claude"}}}}}}
	if !m.selectsOverApp("p1") {
		t.Fatal("agent panes select over the app")
	}
	n := &notes{}
	press := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	m.selectOrClick(n, "p1", press, 0, 0)
	m.selectOrClick(n, "p1", tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft}, 3, 0)
	if cmd := m.selectOrClick(n, "p1", tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}, 3, 0); cmd == nil {
		t.Fatal("a drag copies")
	}
	if len(n.methods) != 0 {
		t.Fatalf("a drag must not reach the program: %v", n.methods)
	}
	if got := m.sel.text(m.frame.Lines, 100); got != "copy" {
		t.Fatalf("selected %q", got)
	}

	// A plain click goes through to the program, press and release.
	m.lastClickID = ""
	m.selectOrClick(n, "p1", press, 8, 1)
	m.selectOrClick(n, "p1", tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}, 8, 1)
	if len(n.methods) != 2 || m.sel != nil {
		t.Fatalf("click: forwarded %v, selection %+v", n.methods, m.sel)
	}
}

func TestChangesPollKeepsSelection(t *testing.T) {
	cv := &changesView{projectID: "r1", branch: "feat"}
	files := func(paths ...string) proto.Changes {
		var c proto.Changes
		for _, p := range paths {
			c.Files = append(c.Files, proto.FileChange{Path: p, Code: "?"})
		}
		return c
	}
	cv.receive(changesMsg{projectID: "r1", branch: "feat", data: files("b.txt", "c.txt")})
	cv.sel = 1 // c.txt
	if cv.receive(changesMsg{projectID: "r1", branch: "feat", data: files("b.txt", "c.txt"), poll: true}) {
		t.Fatal("an unchanged poll reports no change")
	}
	if !cv.receive(changesMsg{projectID: "r1", branch: "feat", data: files("a.txt", "b.txt", "c.txt"), poll: true}) {
		t.Fatal("a new file is a change")
	}
	if cv.data.Files[cv.sel].Path != "c.txt" {
		t.Fatalf("selection moved to %s", cv.data.Files[cv.sel].Path)
	}
	cv.receive(changesMsg{projectID: "r1", branch: "feat", err: errString("offline"), poll: true})
	if cv.err != "" || cv.data == nil {
		t.Fatal("a failed background poll keeps what is shown")
	}
}

// A style that only sets a background keeps the terminal's foreground,
// which in a light terminal is dark — and the unfocused selection bar (a
// dark selDim) then hid the row's name. Reported with a screenshot.
func TestUnfocusedSelectionStaysReadable(t *testing.T) {
	for _, th := range themes {
		applyTheme(th.name, "")
		fg, bg := styleSelDim.GetForeground(), styleSelDim.GetBackground()
		if fg == nil || fmt.Sprint(fg) == "" {
			t.Fatalf("%s: the unfocused selection sets no text colour", th.name)
		}
		lf, lb := luminance(lipgloss.Color(fmt.Sprint(fg))), luminance(lipgloss.Color(fmt.Sprint(bg)))
		if diff := lf - lb; diff < 0.3 && diff > -0.3 {
			t.Errorf("%s: text %v on %v is too close (%.2f)", th.name, fg, bg, diff)
		}
	}
	applyTheme("conch", "")
	if styleSel.GetForeground() == nil || styleSel.GetBackground() == nil {
		t.Fatal("the focused selection sets both colours")
	}
	// textOn picks light text on dark and dark text on light.
	if textOn("#000000") != "#F0F6FC" || textOn("#FFFFFF") != "#11151A" || textOn("#2D333B") != "#F0F6FC" {
		t.Fatalf("textOn: %q %q", textOn("#000000"), textOn("#FFFFFF"))
	}
	// Unparseable colours keep light text rather than guessing.
	for _, c := range []lipgloss.Color{"", "red", "#12345", "#zzzzzz"} {
		if luminance(c) != 0 || textOn(c) != "#F0F6FC" {
			t.Errorf("unparseable %q", c)
		}
	}
	if l := luminance("#808080"); l < 0.4 || l > 0.6 {
		t.Errorf("mid grey luminance %.2f", l)
	}
}
