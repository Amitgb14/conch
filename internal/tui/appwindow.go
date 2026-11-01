package tui

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Amitgb14/conch/internal/pane"
)

// conch's own window is a terminal `conch app` opened and configured: a
// title bar nobody else is writing to, and ⌘ shortcuts that send conch's
// prefix and a key (cmd/conch/app.go). The TUI in it is the same TUI; what
// it does differently is small and all here. It names what is waiting in
// the window's title — the title bar and the Dock's window list are what
// is on screen while conch is not — and the palette shows each command's
// ⌘ shortcut beside its key.
//
// It is told by CONCH_APP_WINDOW, which the terminal's configuration sets
// (not CONCH_APP, which is install.sh's). Anywhere else conch leaves the
// title alone: a title belongs to whoever owns the terminal, and a tmux or
// a shell prompt that sets its own would be fought.

// AppWindowEnv is the variable `conch app` sets in the window it opens.
const AppWindowEnv = "CONCH_APP_WINDOW"

// inAppWindow and sendWindowTitle are the reaching-out, so the suite can
// replace them (main_test.go): a title written for real would retitle the
// window `go test` runs in.
var (
	inAppWindow = func() bool {
		return os.Getenv(AppWindowEnv) == "1" && os.Getenv("TMUX") == "" && stdoutIsTerminal()
	}
	sendWindowTitle = func(title string) { _, _ = os.Stdout.WriteString("\x1b]2;" + title + "\x1b\\") }
)

// windowTitle is what conch's window is called now: conch, the workspace
// when there is more than one, and the one line the host status says —
// the agent waiting for you, or how many are working.
func (m Model) windowTitle() string {
	parts := []string{"conch"}
	if m.cur() != nil {
		parts[0] += " — " + m.spaceLabel(m.activeSpace)
	}
	rec := m.hostStatus()
	mark := map[string]string{
		pane.StatusBlocked: "⚑ ",
		pane.StatusError:   "✗ ",
		pane.StatusWorking: "",
		pane.StatusDone:    "✓ ",
	}
	if prefix, ok := mark[rec.State]; ok && rec.Msg != "" {
		parts = append(parts, prefix+rec.Msg)
	}
	// A title is one line: a name or a branch with a newline in it would
	// end the escape early in some terminals and print the rest.
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.Join(parts, " · "))
}

// titleWindow writes the window's title when it has changed, in conch's
// own window only.
func (m *Model) titleWindow() {
	if !m.appWindow {
		return
	}
	t := m.windowTitle()
	if t == m.titleSaid {
		return
	}
	m.titleSaid = t
	sendWindowTitle(t)
}

// AppShortcut is a ⌘ shortcut in conch's own window: the terminal sends
// conch's prefix and then Send, so it does exactly what that key does in
// any terminal and the TUI needs nothing of its own to take it. These are
// the shortcuts every macOS application has (⌘T a tab, ⌘W close, ⌘1–9 a
// tab), put on what conch calls the same thing, and they replace the
// terminal's own — which would make a tab or a split of the *terminal*,
// outside conch, where nothing of conch's can see it. cmd/conch writes
// them into the terminal's configuration; the palette names them.
type AppShortcut struct {
	Mods string // "", "shift" or "alt", beside ⌘
	Key  string // a letter, digit, punctuation, or enter/up/down/left/right
	Send string // what follows the prefix
}

var AppShortcuts = func() []AppShortcut {
	keys := []AppShortcut{
		{"", "t", "c"},             // new tab
		{"", "w", "x"},             // close split (asks)
		{"shift", "w", "&"},        // close tab (asks)
		{"", "d", "%"},             // split right
		{"shift", "d", "\""},       // split down
		{"shift", "]", "n"},        // next tab
		{"shift", "[", "p"},        // previous tab
		{"", "]", "o"},             // next split
		{"", "[", ";"},             // last split
		{"shift", "enter", "z"},    // zoom
		{"", "k", "/"},             // the palette
		{"", ".", "!"},             // next agent waiting
		{"", "/", "?"},             // help
		{"", "n", "N"},             // new workspace
		{"alt", "up", "\x1b[A"},    // focus the split above…
		{"alt", "down", "\x1b[B"},  // …below
		{"alt", "right", "\x1b[C"}, // …right
		{"alt", "left", "\x1b[D"},  // …left
	}
	for d := '1'; d <= '9'; d++ {
		keys = append(keys, AppShortcut{"", string(d), string(d)}) // go to tab
	}
	return keys
}()

// appShortcutFor is how the palette writes the ⌘ shortcut for a prefix
// key, "" when there is none.
func appShortcutFor(send string) string {
	for _, k := range AppShortcuts {
		if k.Send != send {
			continue
		}
		s := "⌘"
		switch k.Mods {
		case "shift":
			s = "⇧⌘"
		case "alt":
			s = "⌥⌘"
		}
		key := strings.ToUpper(k.Key)
		switch k.Key {
		case "enter":
			key = "⏎"
		case "up":
			key = "↑"
		case "down":
			key = "↓"
		case "left":
			key = "←"
		case "right":
			key = "→"
		}
		return s + key
	}
	return ""
}

// appHeaderMinHeight is the shortest window that gets a header: below it
// every row is wanted for the tree and the panes.
const appHeaderMinHeight = 12

// appHeaderRows is the band and a row of space under it, so the search
// box and the tab bar are not pressed together: the band is the title
// bar's, and the tabs are the content's.
const appHeaderRows = 2

// The header is the strip along the top of conch's own window: a search
// box in the middle that opens the palette, and on the right what is
// waiting for you. A terminal title bar can hold neither, and the macOS
// shortcut for the box (⌘K) is printed in it, which is where somebody
// who has never pressed it learns it exists.

// appHeaderBox is the search box's place on the header row: [x0, x1).
func (m Model) appHeaderBox() (x0, x1 int) {
	w := clamp(m.width/3, min(24, m.width), 64)
	x0 = max((m.width-w)/2, 0)
	return x0, x0 + w
}

// appHeaderCounts is the waiting/done label and where it starts.
func (m Model) appHeaderCounts() (string, int) {
	label := inboxLabel(m.inboxCount())
	if label == "" {
		return "", m.width
	}
	label += " "
	return label, m.width - ansi.StringWidth(label)
}

// appHeaderSpaces is conch's mark and the workspaces, at the left of the
// header — where the sidebar's top row keeps them in any other terminal —
// with their buttons, counted from the header's first column.
func (m Model) appHeaderSpaces() (string, []spaceHit) {
	x0, _ := m.appHeaderBox()
	room := x0 - 2
	if room < 1 {
		return "", nil
	}
	s, hits := m.spaceHeader(room)
	if ansi.StringWidth(s) > room {
		return "", nil
	}
	for i := range hits {
		hits[i].x0++
		hits[i].x1++
	}
	return " " + s, hits
}

func (m Model) appHeader() string {
	x0, x1 := m.appHeaderBox()
	w := x1 - x0
	text := " ⌕ Search agents, branches, commands"
	key := "⌘K "
	if ansi.StringWidth(text)+ansi.StringWidth(key) > w {
		text = " ⌕ Search"
	}
	if ansi.StringWidth(text)+ansi.StringWidth(key) > w {
		text, key = " ⌕", ""
	}
	inner := text + strings.Repeat(" ", max(w-ansi.StringWidth(text)-ansi.StringWidth(key), 0)) + key
	field := lipgloss.NewStyle().Background(colorWell).Foreground(colorMuted).Render(ansi.Truncate(inner, w, ""))
	left, _ := m.appHeaderSpaces()
	line := left + strings.Repeat(" ", max(x0-ansi.StringWidth(left), 0)) + field
	if counts, cx := m.appHeaderCounts(); counts != "" && cx > x1+1 {
		line += strings.Repeat(" ", cx-x1) + counts
	}
	line = fit(line, m.width)
	return onBand(line+strings.Repeat(" ", max(m.width-ansi.StringWidth(line), 0)), colorChrome)
}

// onBand lays a line over a background colour: the whole width, the gaps
// between its parts included, and under every part that sets no
// background of its own. Each styled part ends in a reset, which would
// take the band away with it, so the band is set again after each.
func onBand(line string, bg lipgloss.Color) string {
	if lipgloss.ColorProfile() == termenv.Ascii {
		return line
	}
	seq := lipgloss.ColorProfile().Color(string(bg)).Sequence(true)
	if seq == "" {
		return line
	}
	set := "\x1b[" + seq + "m"
	return set + strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+set) + "\x1b[0m"
}

// headerMouse is a click on the header: the box opens the palette and
// the counts go to the next agent waiting. Anything else on the row is
// nothing, as the space around a title bar's controls is.
func (m *Model) headerMouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || m.overlay != nil {
		if msg.Action == tea.MouseActionPress && m.overlay != nil {
			m.overlay = nil // a press outside an open menu closes it, here too
		}
		return nil
	}
	if x0, x1 := m.appHeaderBox(); msg.X >= x0 && msg.X < x1 {
		return m.openPalette()
	}
	if _, hits := m.appHeaderSpaces(); len(hits) > 0 {
		if sp, ok := spaceHitAt(hits, msg.X); ok {
			return m.actOnSpace(sp, true)
		}
	}
	if counts, cx := m.appHeaderCounts(); counts != "" && msg.X >= cx {
		return m.jumpToAttention()
	}
	return nil
}
