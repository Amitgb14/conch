package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/proto"
)

func a7Limits(m *Model, now time.Time) {
	mach := m.machines[0]
	mach.setLimits(proto.PlanLimits{Agent: "claude", At: now.Add(-3 * time.Minute),
		FiveHour: &proto.LimitWindow{UsedPct: 42, ResetsAt: now.Add(90 * time.Minute)},
		Week:     &proto.LimitWindow{UsedPct: 18, ResetsAt: now.Add(72 * time.Hour)}})
	mach.setLimits(proto.PlanLimits{Agent: "codex", At: now.Add(-time.Minute),
		FiveHour: &proto.LimitWindow{UsedPct: 96, ResetsAt: now.Add(20 * time.Minute)}})
}

func TestLimitsWindowShowsEverything(t *testing.T) {
	applyTheme("conch", "")
	m, _ := a1Fixture(t, false)
	now := time.Now()
	a7Limits(m, now)

	v := &limitsView{mid: localMachine, fill: 1}
	text := a2Plain(v.render(*m).lines)

	// Both agents, both windows, the percentages and when they reset.
	for _, want := range []string{"Claude Code", "Codex", "5-hour", "week", "42%", "18%", "96%", "resets"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the window does not say %q:\n%s", want, text)
		}
	}
	// The reset time is given twice: how long, and when.
	if !strings.Contains(text, "in ") || !strings.Contains(text, now.Add(90*time.Minute).Local().Format("Mon 15:04")) {
		t.Fatalf("reset times:\n%s", text)
	}
	// When the agent last said so, which is all the caveat it needs: the
	// numbers are the agent's report, at the time it made it.
	if !strings.Contains(text, "as the agent last said") {
		t.Fatalf("no as-of line:\n%s", text)
	}
	// esc closes it — through the key path a keystroke really takes, not
	// by calling update and trusting what it returns: handleKey throws the
	// closed flag away, so an overlay that only returns true stays put.
	m.overlay = v
	next, _ := m.Update(a2Key("esc"))
	if mm := next.(Model); mm.overlay != nil {
		t.Fatalf("esc left %T on screen", mm.overlay)
	}
	// Any other key closes it too: there is nothing to type here.
	m.overlay = v
	next, _ = m.Update(a2Key("j"))
	if mm := next.(Model); mm.overlay != nil {
		t.Fatalf("a key left %T on screen", mm.overlay)
	}
	// And a click outside.
	m.overlay = v
	v.mouse(m, tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, box{x: 10, y: 10, lines: []string{"xx"}})
	if m.overlay != nil {
		t.Fatal("a click outside left it open")
	}
}

func TestLimitsWindowEdges(t *testing.T) {
	applyTheme("conch", "")
	m, _ := a1Fixture(t, false)
	now := time.Now()

	// Nothing reported at all.
	v := &limitsView{mid: localMachine}
	if got := a2Plain(v.render(*m).lines); !strings.Contains(got, "no agent here has reported") {
		t.Fatalf("with no limits:\n%s", got)
	}
	// A window that is used up, and one whose reset has passed: both are
	// said in words rather than left as a number to puzzle over.
	m.machines[0].setLimits(proto.PlanLimits{Agent: "claude", At: now,
		FiveHour: &proto.LimitWindow{UsedPct: 100, ResetsAt: now.Add(time.Hour)},
		Week:     &proto.LimitWindow{UsedPct: 61, ResetsAt: now.Add(-time.Minute)}})
	got := a2Plain(v.render(*m).lines)
	if !strings.Contains(got, "used up") || !strings.Contains(got, "turned over since") {
		t.Fatalf("edge windows:\n%s", got)
	}
	// A machine that has gone while the window was open does not panic.
	v.mid = "nope"
	if got := a2Plain(v.render(*m).lines); !strings.Contains(got, "local") {
		t.Fatalf("a machine that is not there:\n%s", got)
	}
	// Every line fits the box, at any width, and it sits at the bottom
	// right — under the chip it belongs to, like the resources window.
	for _, w := range []int{200, 100, 60, 40} {
		m.width, m.height = w, 30
		b := v.render(*m)
		a2CheckBox(t, b, *m)
		if b.x+b.width() != m.width || b.y+len(b.lines) != m.height-statusHeight {
			t.Fatalf("%d columns: the window sits at %d,%d (%dx%d) in a %dx%d screen",
				w, b.x, b.y, b.width(), len(b.lines), m.width, m.height)
		}
	}
	// A screen too short for it still starts on screen rather than above it.
	m.width, m.height = 100, 8
	if b := v.render(*m); b.y < 0 {
		t.Fatalf("a short screen put it at y=%d", b.y)
	}
}

func TestUsageBarIsCellsNotGlyphs(t *testing.T) {
	applyTheme("conch", "")
	for _, pct := range []float64{0, 1, 50, 99.6, 100, 140, -5} {
		b := usageBar(pct, 20)
		if w := ansi.StringWidth(b); w != 20 {
			t.Errorf("%.0f%%: the bar is %d cells, want 20", pct, w)
		}
		for _, r := range ansi.Strip(b) {
			if r != ' ' {
				t.Errorf("%.0f%%: the bar draws %q, a glyph a font may widen", pct, string(r))
			}
		}
	}
}

func TestClickingTheChipOpensTheWindow(t *testing.T) {
	applyTheme("conch", "")
	m, _ := a1Fixture(t, false)
	a7Limits(m, time.Now())
	items := m.statusLimits(time.Now())
	if len(items) != 1 || items[0].act == nil {
		t.Fatalf("no clickable chip: %+v", items)
	}
	items[0].act(m)
	if _, ok := m.overlay.(*limitsView); !ok {
		t.Fatalf("the chip opened %T", m.overlay)
	}
	// Clicking it again closes it, the way the resources icon does.
	items = m.statusLimits(time.Now())
	items[0].act(m)
	if m.overlay != nil {
		t.Fatalf("a second click left %T open", m.overlay)
	}
}

// TestEveryWindowTheAgentNames: an account with a per-model allowance
// reports more windows than the three conch grew up with — a weekly window
// for one model beside the weekly window for all of them — and they are
// shown under the agent's own names.
func TestEveryWindowTheAgentNames(t *testing.T) {
	applyTheme("conch", "")
	m, _ := a1Fixture(t, false)
	now := time.Now()
	m.machines[0].setLimits(proto.PlanLimits{Agent: "claude", At: now,
		FiveHour: &proto.LimitWindow{UsedPct: 9, ResetsAt: now.Add(4 * time.Hour)},
		Week:     &proto.LimitWindow{UsedPct: 38, ResetsAt: now.Add(48 * time.Hour)},
		Windows: []proto.NamedWindow{
			{Key: "five_hour", UsedPct: 9, ResetsAt: now.Add(4 * time.Hour)},
			{Key: "seven_day", UsedPct: 38, ResetsAt: now.Add(48 * time.Hour)},
			{Key: "seven_day_fable", UsedPct: 0, ResetsAt: now.Add(48 * time.Hour)},
			{Key: "some_new_thing", UsedPct: 12},
		}})
	v := &limitsView{mid: localMachine, fill: 1}
	text := a2Plain(v.render(*m).lines)
	for _, want := range []string{"5-hour", "week", "week (Fable)", "some new thing", "9%", "38%", "12%"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the window does not say %q:\n%s", want, text)
		}
	}
	// The two-line disclaimer is gone: it was the same words every time.
	if strings.Contains(text, "not conch's") {
		t.Fatalf("the disclaimer is still there:\n%s", text)
	}
	// An agent that named nothing falls back to the three conch knows.
	m.machines[0].setLimits(proto.PlanLimits{Agent: "codex", At: now,
		FiveHour: &proto.LimitWindow{UsedPct: 50, ResetsAt: now.Add(time.Hour)}})
	if text := a2Plain(v.render(*m).lines); !strings.Contains(text, "5-hour") {
		t.Fatalf("an agent with no named windows:\n%s", text)
	}
}

func TestWindowName(t *testing.T) {
	for key, want := range map[string]string{
		"five_hour":       "5-hour",
		"seven_day":       "week",
		"spend_limit":     "spend",
		"seven_day_fable": "week (Fable)",
		"seven_day_opus":  "week (Opus)",
		"whatever_next":   "whatever next",
		"":                "",
	} {
		if got := windowName(key); got != want {
			t.Errorf("windowName(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestBarsFillOnceAndStop: the bars grow when the window opens and then
// stop — nothing keeps ticking behind a window nobody is looking at.
func TestBarsFillOnceAndStop(t *testing.T) {
	applyTheme("conch", "")
	m, _ := a1Fixture(t, false)
	a7Limits(m, time.Now())
	cmd := m.openLimits(localMachine)
	v, ok := m.overlay.(*limitsView)
	if !ok || cmd == nil {
		t.Fatalf("opening gave %T, cmd %v", m.overlay, cmd != nil)
	}
	if v.fill != 0 {
		t.Fatalf("it started full: %v", v.fill)
	}
	// One tick through the path a real tick takes, for the same reason esc
	// needed testing that way: what update returns is not what happens.
	next, _ := m.Update(limitsFillMsg{v: v})
	*m = next.(Model)
	if v.fill == 0 {
		t.Fatal("a tick through Update did not grow the bars")
	}
	// Each tick grows it and asks for the next, until it is there.
	steps := 0
	for {
		closed, next := v.update(m, limitsFillMsg{v: v})
		if closed {
			t.Fatal("a tick closed the window")
		}
		steps++
		if next == nil {
			break
		}
		if steps > limitsFillSteps+2 {
			t.Fatal("the bars never stopped growing")
		}
	}
	if v.fill != 1 {
		t.Fatalf("it stopped at %v", v.fill)
	}
	// A tick for a window that has since closed is ignored rather than
	// growing the one that replaced it.
	other := &limitsView{mid: localMachine}
	if _, next := other.update(m, limitsFillMsg{v: v}); next != nil || other.fill != 0 {
		t.Fatalf("a stale tick reached another window: fill %v", other.fill)
	}
}

// TestSeenByConchSaysWhatItCounts: the number beside an agent's own usage
// panel is read against it, and the two count different things — conch has
// the whole conversation, the agent's panel the run it is on. Say so, and
// break the tokens out the way the agent's panel does, or neither number
// can be reconciled with the other.
func TestSeenByConchSaysWhatItCounts(t *testing.T) {
	applyTheme("conch", "")
	m, _ := a1Fixture(t, false)
	now := time.Now()
	a7Limits(m, now)
	mach := m.machines[0]
	mach.panes[0].Agent.Tokens = &proto.Tokens{Input: 1600, CacheRead: 25_700_000, CacheWrite: 24_100_000,
		Output: 16_600, CostUSD: 13.5}
	v := &limitsView{mid: localMachine, fill: 1}
	text := a2Plain(v.render(*m).lines)

	if strings.Contains(text, "this session") {
		t.Fatalf("it still calls a whole conversation a session:\n%s", text)
	}
	if !strings.Contains(text, "whole conversations, as each agent counts them") {
		t.Fatalf("it does not say what it counts:\n%s", text)
	}
	// The parts, not one number that folds cache reads into "input".
	for _, want := range []string{"prompt", "cache read", "cache write", "out"} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q in the tokens line:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "25.7M") || !strings.Contains(text, "24.1M") {
		t.Fatalf("the cache numbers are not shown as the agent shows them:\n%s", text)
	}
}
