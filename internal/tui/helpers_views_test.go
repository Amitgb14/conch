package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// a2Isolate keeps a test away from the user's real configuration and any
// conch the test run may be inside.
func a2Isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CONCH_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("CONCH_SOCKET", "")
	t.Setenv("CONCH_PANE_ID", "")
	t.Setenv("TMUX", "")
}

// a2Key builds a key message the way bubbletea reports k.
func a2Key(k string) tea.KeyMsg {
	special := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd,
		"backspace": tea.KeyBackspace, "delete": tea.KeyDelete, " ": tea.KeySpace,
	}
	if t, ok := special[k]; ok {
		if t == tea.KeySpace {
			return tea.KeyMsg{Type: t, Runes: []rune(" ")}
		}
		return tea.KeyMsg{Type: t}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// a2Type types s one rune at a time into an overlay.
func a2Type(m *Model, o overlay, s string) {
	for _, r := range s {
		o.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// a2Client is a client that was never connected: only its handshake
// result (capabilities, builds) is usable. Commands that would call it must
// not be run.
func a2Client(caps ...string) *client.Client {
	return &client.Client{Server: proto.HelloResult{Capabilities: caps}}
}

// a2Model is a model with this computer online (no connection), a git
// project with a worktree branch, and two panes.
func a2Model() *Model {
	m := &Model{cfg: config.Default(), width: 120, height: 40, brain: newBrainState(),
		expanded: map[string]bool{}, showAll: map[string]bool{}}
	mach := newMachine(localMachine, "local", "")
	mach.state = stateOnline
	mach.projects = []proto.ProjectInfo{{ID: "r1", Name: "api", Path: "/src/api", Git: true, Base: "main",
		Worktrees: []proto.WorktreeInfo{{Path: "/src/api", Branch: "main", Main: true}, {Path: "/src/api-feat", Branch: "feat"}},
		Branches: []proto.BranchInfo{{Name: "main"}, {Name: "feat", BaseAhead: 2, BaseBehind: 1,
			PR: &proto.PRInfo{Number: 7, Title: "Add feature", State: "OPEN", URL: "https://example.invalid/pr/7"}}}}}
	mach.panes = []proto.PaneInfo{
		{ID: "p1", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Cwd: "/src/api",
			Agent: &proto.AgentStatus{Name: "claude", State: proto.AgentBlocked, Since: time.Now()}},
		{ID: "p2", Name: "zsh", State: proto.PaneRunning},
	}
	m.machines = []*machine{mach}
	return m
}

// a2Run runs a command that is known not to touch a connection, flattening
// batches, and returns the messages it produced.
func a2Run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, a2Run(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// a2ErrText is the text of the first errMsg among msgs.
func a2ErrText(msgs []tea.Msg) string {
	for _, msg := range msgs {
		if e, ok := msg.(errMsg); ok {
			return e.err.Error()
		}
	}
	return ""
}

func a2Plain(lines []string) string { return ansi.Strip(strings.Join(lines, "\n")) }

// a2CheckBox fails when a rendered box is ragged or leaves the screen.
func a2CheckBox(t *testing.T, b box, m Model) {
	t.Helper()
	if len(b.lines) == 0 {
		t.Fatal("empty box")
	}
	w := b.width()
	for i, l := range b.lines {
		if lw := ansi.StringWidth(l); lw != w {
			t.Fatalf("line %d is %d wide, box is %d:\n%s", i, lw, w, a2Plain(b.lines))
		}
	}
	if b.x < 0 || b.y < 0 || (m.width >= w && b.x+w > m.width) {
		t.Fatalf("box at %d,%d (%d wide) leaves a %dx%d screen", b.x, b.y, w, m.width, m.height)
	}
}
