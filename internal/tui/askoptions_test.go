package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/brain"
)

func optionTexts(opts []brain.Suggestion) []string {
	var out []string
	for _, o := range opts {
		out = append(out, o.Text)
	}
	return out
}

// askModel is a2Model online, with the agent selected and a request
// asked before.
func askModel(t *testing.T) (*Model, *askBar) {
	t.Helper()
	a2Isolate(t)
	m := a2Model()
	m.machines[0].c = a2Client() // online; nothing here calls it
	m.cfg.Brain.Provider = "openai"
	m.brain.history = []string{"show zsh"}
	m.rows = []row{{id: "pane", kind: kindPane, machine: localMachine, paneID: "p1"}}
	m.cursor = "pane"
	m.openAsk()
	return m, m.overlay.(*askBar)
}

func TestAskBarOptionsKeys(t *testing.T) {
	m, b := askModel(t)
	opts := b.shown()
	if len(opts) < 3 || b.pick != -1 {
		t.Fatalf("opened with %d options, pick %d", len(opts), b.pick)
	}
	b.update(m, a2Key("up"))
	if b.pick != -1 {
		t.Fatal("up from the request stays there")
	}
	for range len(opts) + 3 {
		b.update(m, a2Key("down"))
	}
	if b.pick != len(opts)-1 {
		t.Fatalf("down stops at the last: %d", b.pick)
	}
	b.update(m, a2Key("up"))
	if b.pick != len(opts)-2 {
		t.Fatal("up")
	}
	// Typing narrows the list and drops the highlight.
	a2Type(m, b, "tell")
	if b.pick != -1 || len(b.shown()) == 0 {
		t.Fatalf("typing: pick %d, %q", b.pick, optionTexts(b.shown()))
	}
	// tab finishes from the first; the template waits for the message.
	b.update(m, a2Key("tab"))
	if b.in.Value() != "tell claude to " || b.phase != askInput {
		t.Fatalf("tab: %q", b.in.Value())
	}
	// enter on a template fills it in and asks nothing yet.
	b.in.SetValue("")
	b.pick = -1
	all := b.shown()
	i := indexOf(all, "start an agent on api to …")
	if i < 0 {
		t.Fatalf("no template: %q", optionTexts(all))
	}
	b.pick = i
	if _, cmd := b.update(m, a2Key("enter")); b.phase != askInput || b.in.Value() != "start an agent on api to " || b.pick != -1 {
		t.Fatalf("template: %v %q %v", b.phase, b.in.Value(), cmd)
	}
	// enter on a whole request asks it: this one needs no model.
	b.in.SetValue("")
	b.pick = indexOf(b.shown(), "show zsh")
	if _, cmd := b.update(m, a2Key("enter")); cmd != nil || b.phase != askPlan || len(b.actions) != 1 || b.actions[0].Pane != "p2" {
		t.Fatalf("recent: phase %v %+v", b.phase, b.actions)
	}
}

func indexOf(opts []brain.Suggestion, text string) int {
	for i, o := range opts {
		if o.Text == text {
			return i
		}
	}
	return -1
}

func TestAskBarOptionsClick(t *testing.T) {
	m, b := askModel(t)
	bx := b.render(*m)
	y := -1
	for i, l := range bx.lines {
		if strings.Contains(ansi.Strip(l), "show zsh") {
			y = i
		}
	}
	if y < 0 {
		t.Fatalf("no option drawn:\n%s", a2Plain(bx.lines))
	}
	// A click on the heading above it does nothing.
	b.mouse(m, tea.MouseMsg{X: bx.x + 4, Y: bx.y + y - 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, bx)
	if b.phase != askInput || m.overlay == nil {
		t.Fatal("a click off the options")
	}
	b.mouse(m, tea.MouseMsg{X: bx.x + 4, Y: bx.y + y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, bx)
	if b.phase != askPlan || len(b.actions) != 1 || b.actions[0].Pane != "p2" {
		t.Fatalf("click: phase %v %+v", b.phase, b.actions)
	}
}

func TestAskBarSaysWhatEnterCosts(t *testing.T) {
	m, b := askModel(t)
	out := a2Plain(b.render(*m).lines)
	for _, want := range []string{"Context  pane p1", "Agents   1 agent · 1 waiting", "Model    openai", "Suggested", "Recent", "show zsh", "nothing runs until you confirm"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "enter asks this") {
		t.Fatal("nothing typed, nothing to say about it")
	}
	a2Type(m, b, "show claude")
	if out := a2Plain(b.render(*m).lines); !strings.Contains(out, "enter asks this · conch answers it at once") {
		t.Fatalf("fast:\n%s", out)
	}
	b.in.SetValue("fix the flaky tests")
	if out := a2Plain(b.render(*m).lines); !strings.Contains(out, "enter asks this · openai plans it") {
		t.Fatalf("model:\n%s", out)
	}
	m.cfg.Brain.NoFastPath = true
	b.in.SetValue("show claude")
	if out := a2Plain(b.render(*m).lines); !strings.Contains(out, "openai plans it") || !strings.Contains(out, "every request goes to the model") {
		t.Fatalf("no fast path:\n%s", out)
	}
}

func TestAskBarFitsAnyScreen(t *testing.T) {
	m, b := askModel(t)
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 14}, {40, 10}, {30, 8}, {20, 5}, {1, 1}} {
		mm := *m
		mm.width, mm.height = size[0], size[1]
		for _, typed := range []string{"", "s", strings.Repeat("long request ", 30)} {
			b.in.SetValue(typed)
			b.pick = 0
			bx := b.render(mm)
			a2CheckBox(t, bx, mm)
			if mm.width >= 44 && bx.width() > mm.width {
				t.Fatalf("%v: %d wide", size, bx.width())
			}
			if mm.height >= 11 && len(bx.lines) > mm.height {
				t.Fatalf("%v: %d lines\n%s", size, len(bx.lines), a2Plain(bx.lines))
			}
			// A heading is never the last thing of the list.
			plain := strings.Split(a2Plain(bx.lines), "\n")
			for i, l := range plain {
				if (strings.Contains(l, "Suggested") || strings.Contains(l, "Recent")) && i+1 < len(plain) &&
					strings.TrimSpace(strings.Trim(plain[i+1], "│")) == "" {
					t.Fatalf("%v: heading with nothing under it\n%s", size, strings.Join(plain, "\n"))
				}
			}
			// Every line a click maps to is an option.
			for line := range b.optLines {
				if line+1 >= len(bx.lines) || !strings.Contains(ansi.Strip(bx.lines[line+1]), b.shown()[b.optLines[line]].Text[:1]) {
					t.Fatalf("%v: line %d mapped to %q", size, line, b.shown()[b.optLines[line]].Text)
				}
			}
		}
	}
}
