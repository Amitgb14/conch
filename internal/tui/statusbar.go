package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/proto"
)

// statusItem is a clickable part of the status bar.
type statusItem struct {
	text string // rendered
	act  func(m *Model) tea.Cmd
}

// statusHit is where a clickable item landed on the bar.
type statusHit struct {
	x0, x1 int // [x0, x1)
	act    func(m *Model) tea.Cmd
}

// hint shows key and label; clicking it acts as pressing key.
func hint(key, label string) statusItem {
	return statusItem{
		text: styleAccent.Render(key) + " " + styleMuted.Render(label),
		act:  func(m *Model) tea.Cmd { return m.press(key) },
	}
}

func action(key, label string, act func(m *Model) tea.Cmd) statusItem {
	return statusItem{text: styleAccent.Render(key) + " " + styleMuted.Render(label), act: act}
}

// press runs a key through the normal key handling, as if typed.
func (m *Model) press(key string) tea.Cmd {
	var k tea.KeyMsg
	switch key {
	case "enter", "⏎":
		k.Type = tea.KeyEnter
	case "esc":
		k.Type = tea.KeyEsc
	case "tab":
		k.Type = tea.KeyTab
	case "space":
		k.Type = tea.KeySpace
	case "↑":
		k.Type = tea.KeyUp
	case "↓":
		k.Type = tea.KeyDown
	case "pgup":
		k.Type = tea.KeyPgUp
	case "pgdn":
		k.Type = tea.KeyPgDown
	default:
		k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, cmd := m.handleKey(k)
	*m = next.(Model)
	return cmd
}

func (m Model) statusHints() (chip string, items []statusItem) {
	r, _ := m.selectedRow()
	toTree := func(m *Model) tea.Cmd { m.focus, m.prefixArmed = focusSidebar, false; return nil }
	scrollMode := func(m *Model) tea.Cmd { m.enterScrollMode(); return nil }
	switch {
	case m.prefixArmed:
		chip = styleChip.Background(colorWarn).Render("PREFIX")
		items = []statusItem{hint("v", "split"), hint("-", "split down"), hint("x", "close split"), hint("←→↑↓", "focus"),
			hint("c", "new tab"), hint("n", "next tab"), hint("[", "scroll"), hint("z", "zoom"), action("esc", "tree", toTree)}
	case m.focus == focusMain && r.kind == kindPane && m.scrollMode:
		chip = styleChip.Background(colorWarn).Render("SCROLL")
		items = []statusItem{hint("↑↓←→", "move"), hint("v", "select"), hint("y", "copy"), hint("pgup", "page"),
			hint("pgdn", "page"), hint("g", "oldest"), hint("esc", "live")}
	case m.focus == focusMain && r.kind == kindPane && m.offset > 0:
		chip = styleChip.Background(colorWarn).Render("HISTORY")
		items = []statusItem{
			action("live", "back to now", func(m *Model) tea.Cmd { m.scrollPane(-m.offset); return nil }),
			action(m.cfg.Keys.Prefix+" [", "scroll keys", scrollMode),
			action("esc", "tree", toTree),
		}
	case m.focus == focusMain && r.kind == kindPane:
		chip = styleChip.Background(colorInput).Render("PANE")
		items = []statusItem{
			action(m.cfg.Keys.Prefix, "tree", toTree),
			action(m.cfg.Keys.Prefix+" v", "split", func(m *Model) tea.Cmd { return m.split(splitRight, viewRef{}) }),
			action(m.cfg.Keys.Prefix+" -", "split down", func(m *Model) tea.Cmd { return m.split(splitDown, viewRef{}) }),
			action(m.cfg.Keys.Prefix+" c", "tab", func(m *Model) tea.Cmd { return m.newTab(viewRef{}) }),
			action(m.cfg.Keys.Prefix+" [", "scroll", scrollMode),
			action(m.cfg.Keys.Prefix+" z", "zoom", func(m *Model) tea.Cmd { m.zoom = !m.zoom; return m.syncView() }),
		}
		if len(m.tab().root.leaves()) > 1 {
			items = append(items, action(m.cfg.Keys.Prefix+" x", "close split", func(m *Model) tea.Cmd { return m.closeLeaf() }))
		}
	case m.focus == focusMain && m.changes != nil && m.changes.diffFile != "":
		chip = styleChip.Background(colorInput).Render("DIFF")
		items = []statusItem{hint("↑", "up"), hint("↓", "down"), hint("pgup", "page"), hint("pgdn", "page"),
			hint("y", "copy"), hint("esc", "files")}
	case m.focus == focusMain && r.kind == kindSessions:
		chip = styleChip.Background(colorInput).Render("SESSIONS")
		items = []statusItem{hint("↑", "up"), hint("↓", "down"), hint("enter", "resume"), hint("a", "agent"),
			hint("I", "resume interrupted"), hint("x", "dismiss"), hint("R", "reload"), hint("esc", "tree")}
	case m.focus == focusMain:
		chip = styleChip.Background(colorInput).Render("CHANGES")
		items = []statusItem{hint("↑", "file"), hint("↓", "file"), hint("enter", "diff"), hint("o", "PR"),
			hint("R", "reload"), hint("esc", "tree")}
	case m.filtering:
		chip = styleChip.Background(colorAccent).Render("FILTER")
		items = []statusItem{hint("enter", "keep"), hint("esc", "clear")}
	default:
		chip = styleChip.Background(colorAccent).Render("TREE")
		switch r.kind {
		case kindPane:
			items = []statusItem{hint("enter", "open"), hint("v", "split"), hint("O", "new tab"), hint("r", "rename"),
				hint("x", "close"), hint("c", "agent"), hint("n", "shell"), hint("m", "menu")}
		case kindSessions:
			items = []statusItem{hint("enter", "open sessions"), hint("c", "agent"), hint("n", "shell"), hint("m", "menu")}
		case kindBranch:
			items = []statusItem{hint("enter", "changes"), hint("v", "split"), hint("o", "PR"), hint("c", "agent"), hint("n", "shell"),
				hint("x", "rm worktree"), hint("y", "copy"), hint("m", "menu")}
		case kindProject:
			items = []statusItem{hint("t", "task"), hint("c", "agent"), hint("n", "shell"), hint("space", "fold"),
				hint("x", "remove"), hint("m", "menu")}
		case kindMachine:
			items = []statusItem{hint("a", "project"), hint("c", "agent"), hint("n", "shell"), hint("M", "machine"),
				hint("R", "reconnect"), hint("m", "menu")}
		default:
			items = []statusItem{hint("a", "project"), hint("t", "task"), hint("c", "agent"), hint("n", "shell"),
				hint("/", "filter"), hint("m", "menu")}
		}
		items = append(items, hint("!", "waiting"), hint("?", "keys"))
	}
	return chip, items
}

// Right side levels: the bar gives up right-side detail, step by step, so
// the mode's key hints stay visible on narrow terminals.
const (
	rightFull      = iota
	rightNoVersion // and a shorter message
	rightNoExtras  // no plan limits or quiet-hours label
	rightIcons     // ✦ and ⚙ without words
	rightMinimal   // no message either
)

func (m Model) statusRightItems(level int) []statusItem {
	var items []statusItem
	if n := m.inboxCount(); n > 0 {
		items = append(items, statusItem{text: styleWarn.Render(fmt.Sprintf("⚑ %d waiting", n)),
			act: func(m *Model) tea.Cmd { return m.jumpToAttention() }})
	}
	if level < rightNoExtras {
		items = append(items, m.statusLimits(time.Now())...)
	}
	if label := m.silenceLabel(time.Now()); label != "" && level < rightNoExtras {
		items = append(items, statusItem{text: styleMuted.Render(label), act: func(m *Model) tea.Cmd {
			if time.Now().Before(m.snoozeUntil) {
				m.snoozeUntil = time.Time{}
				m.setFlash("notifications resumed", false)
			} else {
				s, cmd := newSettings(m)
				s.setTab(1)
				m.overlay = s
				return cmd
			}
			return nil
		}})
	}
	msgW := max(m.width/2, 10)
	if level >= rightNoVersion {
		msgW = max(m.width/4, 12)
	}
	switch {
	case level >= rightMinimal:
	case m.flash != "":
		style := styleOK
		if m.flashIsErr {
			style = styleErr
		}
		items = append(items, statusItem{text: style.Render(ansi.Truncate(m.flash, msgW, "…"))})
	case m.machines[0].warning != "":
		items = append(items, statusItem{text: styleErr.Render(ansi.Truncate(m.machines[0].warning, msgW, "…"))})
	}
	ask, label := "✦ Ask", " ⚙ Settings "
	if level >= rightIcons {
		ask, label = "✦", " ⚙ "
	}
	items = append(items, statusItem{text: styleAccent.Render(ask), act: func(m *Model) tea.Cmd { return m.openAsk() }})
	items = append(items, statusItem{text: styleSel.Render(label), act: func(m *Model) tea.Cmd {
		s, cmd := newSettings(m)
		m.overlay = s
		return cmd
	}})
	if level < rightNoVersion {
		style := styleMuted
		if m.serverBehind() {
			style = styleWarn // details say the server needs a restart
		}
		items = append(items, statusItem{text: style.Render(versionLabel()), act: func(m *Model) tea.Cmd {
			m.overlay = versionInfo{}
			return nil
		}})
	}
	return items
}

// versionLabel is this build's version, e.g. "v0.1.0-dev".
func versionLabel() string { return "v" + proto.Version }

// serverBehind reports whether the local server runs a different build
// than this client, so its new features need a restart.
func (m Model) serverBehind() bool {
	if len(m.machines) == 0 || m.machines[0].c == nil {
		return false
	}
	s := m.machines[0].server
	return s.Build != "" && buildinfo.Build() != "" && s.Build != buildinfo.Build()
}

// versionInfo is the box that opens from the version in the status bar.
type versionInfo struct{}

func (versionInfo) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		m.overlay = nil
		if k.String() == "r" && m.serverBehind() && m.canReload(localMachine) {
			return true, m.reloadServer(localMachine)
		}
		return true, nil
	}
	return false, nil
}

func (versionInfo) mouse(m *Model, msg tea.MouseMsg, _ box) tea.Cmd {
	if msg.Action == tea.MouseActionPress {
		m.overlay = nil
	}
	return nil
}

func (v versionInfo) render(m Model) box {
	row := func(k, val string) string { return " " + styleMuted.Render(padRight(k, 10)) + val }
	lines := []string{
		row("Version", proto.Version),
		row("Build", buildinfo.Build()),
		row("Platform", buildinfo.Platform()),
	}
	if len(m.machines) > 0 {
		mach := m.machines[0]
		switch {
		case mach.c == nil:
			lines = append(lines, row("Server", styleErr.Render("not connected")))
		case m.serverBehind():
			lines = append(lines,
				row("Server", styleWarn.Render("outdated")+styleMuted.Render(fmt.Sprintf(" · %s build %s", mach.server.Version, mach.server.Build))),
				"")
			if m.canReload(localMachine) {
				lines = append(lines, " "+styleAccent.Render("r")+" reload the server onto this build "+styleMuted.Render("(panes keep running)"))
			} else {
				lines = append(lines, styleMuted.Render(" this server predates reloading: conch server stop, then reopen conch"),
					styleMuted.Render(" (stopping closes its panes; resume them from Sessions) — later updates reload"))
			}
		default:
			lines = append(lines, row("Server", styleOK.Render("up to date")+styleMuted.Render(fmt.Sprintf(" · pid %d · running %s", mach.server.PID, uptime(mach.server.Started)))))
		}
	}
	lines = append(lines, "", styleMuted.Render(" any key closes"))
	w := 0
	for _, l := range lines {
		w = max(w, ansi.StringWidth(l)+2)
	}
	b := box{lines: frameLines(" conch ", lines, w, colorAccent)}
	b.x = max(m.width-b.width()-1, 0)
	b.y = max(m.height-statusHeight-len(b.lines), 0)
	return b
}

// layoutStatus renders the status bar and records where its clickable
// items are. Hints that don't fit are dropped whole, so what's on screen
// stays clickable.
func (m Model) layoutStatus() (string, []statusHit) {
	chip, hints := m.statusHints()
	const sep = "  "
	width := func(items []statusItem) int {
		w := 0
		for i, it := range items {
			if i > 0 {
				w += len(sep)
			}
			w += ansi.StringWidth(it.text)
		}
		return w
	}
	// Keep at least the first few hints: shed right-side detail until they
	// fit (or there is nothing left to shed).
	want := hints[:min(len(hints), 4)]
	need := ansi.StringWidth(chip) + width(want) + len(sep)*len(want) + 1
	right := m.statusRightItems(rightFull)
	for level := rightFull + 1; need+width(right) > m.width && level <= rightMinimal; level++ {
		right = m.statusRightItems(level)
	}
	rightW := width(right)

	var b strings.Builder
	var hits []statusHit
	b.WriteString(chip)
	x := ansi.StringWidth(chip)
	room := m.width - rightW - 1
	for _, it := range hints {
		w := ansi.StringWidth(it.text)
		if x+len(sep)+w > room {
			break
		}
		b.WriteString(sep)
		x += len(sep)
		b.WriteString(it.text)
		if it.act != nil {
			hits = append(hits, statusHit{x0: x, x1: x + w, act: it.act})
		}
		x += w
	}
	if gap := m.width - x - rightW; gap > 0 {
		b.WriteString(strings.Repeat(" ", gap))
		x += gap
	}
	for i, it := range right {
		if i > 0 {
			b.WriteString(sep)
			x += len(sep)
		}
		w := ansi.StringWidth(it.text)
		b.WriteString(it.text)
		if it.act != nil {
			hits = append(hits, statusHit{x0: x, x1: x + w, act: it.act})
		}
		x += w
	}
	return fit(b.String(), m.width), hits
}

func (m Model) statusBar() string {
	line, _ := m.layoutStatus()
	return line
}

// clickStatus runs the status bar item under column x, if any.
func (m *Model) clickStatus(x int) tea.Cmd {
	_, hits := m.layoutStatus()
	for _, h := range hits {
		if x >= h.x0 && x < h.x1 {
			return h.act(m)
		}
	}
	return nil
}

func uptime(since time.Time) string {
	d := time.Since(since)
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours()/24), int(d.Hours())%24)
}
