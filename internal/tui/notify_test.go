package tui

import (
	"github.com/Amitgb14/conch/internal/config"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestNextAttention(t *testing.T) {
	at := func(id, state string, sec int) scopedPane {
		p := scopedPane{machine: localMachine, PaneInfo: proto.PaneInfo{ID: id}}
		if state != "" {
			p.Agent = &proto.AgentStatus{Name: "claude", State: state, Since: time.Unix(int64(sec), 0)}
		}
		return p
	}
	panes := []scopedPane{
		at("p1", proto.AgentDone, 1),
		at("p2", "", 0),
		at("p3", proto.AgentBlocked, 5),
		at("p4", proto.AgentWorking, 0),
		at("p5", proto.AgentBlocked, 2),
	}
	for _, tc := range []struct{ current, want string }{
		{"p2", "p5"}, // oldest blocked first
		{"p5", "p3"},
		{"p3", "p1"}, // then done
		{"p1", "p5"}, // wraps
	} {
		if _, got := nextAttention(panes, tc.current); got != tc.want {
			t.Errorf("from %s: got %s, want %s", tc.current, got, tc.want)
		}
	}
	if _, got := nextAttention([]scopedPane{at("p1", proto.AgentIdle, 0)}, ""); got != "" {
		t.Errorf("idle pane offered: %s", got)
	}
}

func TestSilenced(t *testing.T) {
	m := Model{}
	m.cfg.Notify.Enabled = true
	now := time.Date(2026, 9, 13, 23, 30, 0, 0, time.Local)
	if m.silenced(now) || m.silenceLabel(now) != "" {
		t.Fatal("silenced without quiet hours or snooze")
	}
	m.cfg.Notify.QuietStart, m.cfg.Notify.QuietEnd = "22:00", "08:00"
	if !m.silenced(now) || m.silenceLabel(now) != "🔕 quiet until 08:00" {
		t.Fatalf("quiet hours: %q", m.silenceLabel(now))
	}
	m.cfg.Notify.QuietStart = ""
	m.snoozeUntil = now.Add(time.Hour)
	if !m.silenced(now) || m.silenced(now.Add(2*time.Hour)) {
		t.Fatal("snooze")
	}
}

func TestTokenSummary(t *testing.T) {
	if got := tokenSummary(&proto.Tokens{Context: 12000, Output: 812, CostUSD: 0.0259}); got != "ctx 12k · out 812 · $0.03" {
		t.Fatalf("%q", got)
	}
	if got := tokenSummary(&proto.Tokens{Output: 5, CostUSD: 0.001}); got != "out 5 · <$0.01" {
		t.Fatalf("%q", got)
	}
}

func TestLimitsDisplay(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.Local)
	l := proto.PlanLimits{Agent: "claude",
		FiveHour: &proto.LimitWindow{UsedPct: 41.7, ResetsAt: now.Add(2 * time.Hour)},
		Week:     &proto.LimitWindow{UsedPct: 18, ResetsAt: now.Add(3 * 24 * time.Hour)},
		Spend:    &proto.LimitWindow{UsedPct: 50, ResetsAt: now.Add(-time.Minute)}, // expired: hidden
	}
	if got := ansi.Strip(limitsChip(l, now)); got != "Claude 5h 42% · 7d 18%" {
		t.Fatalf("chip %q", got)
	}
	if got := limitsDetail(l, now); got != "Claude Code: 5-hour 42% used, resets 14:00 · week 18% used, resets Wed 12:00" {
		t.Fatalf("detail %q", got)
	}
	if got := tokenSummary(&proto.Tokens{Context: 45210, ContextSize: 200000, Output: 12000}); got != "ctx 45k/200k · out 12k" {
		t.Fatalf("tokens %q", got)
	}
}

func TestVersionInStatusBar(t *testing.T) {
	m := Model{width: 160, machines: []*machine{{id: localMachine, label: "local", state: stateOnline}}, brain: newBrainState()}
	line, _ := m.layoutStatus()
	plain := ansi.Strip(line)
	if !strings.HasSuffix(strings.TrimRight(plain, " "), versionLabel()) || !strings.Contains(plain, "⚙ Settings") {
		t.Fatalf("version should end the status bar: %q", plain)
	}
	if strings.Index(plain, "⚙ Settings") > strings.Index(plain, versionLabel()) {
		t.Fatalf("version should come after Settings: %q", plain)
	}
	m.width = 80
	if line, _ := m.layoutStatus(); strings.Contains(ansi.Strip(line), versionLabel()) {
		t.Fatal("narrow terminals drop the version")
	}
	box := ansi.Strip(strings.Join(versionInfo{}.render(Model{width: 160, height: 40, machines: m.machines}).lines, "\n"))
	for _, want := range []string{"Version", "Build", "Platform", "Server"} {
		if !strings.Contains(box, want) {
			t.Fatalf("details lack %s:\n%s", want, box)
		}
	}
}

func TestNarrowStatusKeepsHints(t *testing.T) {
	m := Model{width: 90, machines: []*machine{{id: localMachine, label: "local", state: stateOnline}}, brain: newBrainState()}
	m.setFlash("a long message that would otherwise take half of the bar away", false)
	line, _ := m.layoutStatus()
	plain := ansi.Strip(line)
	if !strings.Contains(plain, "a project") || !strings.Contains(plain, "c agent") || !strings.Contains(plain, "⚙") {
		t.Fatalf("hints or settings missing at 90 columns: %q", plain)
	}
	if strings.Contains(plain, versionLabel()) {
		t.Fatalf("the version should give way first: %q", plain)
	}
	if w := ansi.StringWidth(line); w != 90 {
		t.Fatalf("bar width %d", w)
	}
}

func TestFlashExpires(t *testing.T) {
	m := Model{width: 120, machines: []*machine{{id: localMachine, label: "local"}}, brain: newBrainState()}
	m.setFlash("hello", false)
	next, cmd := m.Update(flashExpiredMsg{})
	if cmd == nil || next.(Model).flash != "hello" {
		t.Fatal("an unexpired message stays and a clear is scheduled")
	}
	nm := next.(Model)
	nm.flashUntil = time.Now().Add(-time.Second)
	next, _ = nm.Update(flashExpiredMsg{})
	if next.(Model).flash != "" {
		t.Fatal("an expired message is cleared")
	}
}

func TestNarrowPaneHints(t *testing.T) {
	m := Model{width: 100, focus: focusMain, cfg: config.Default(), brain: newBrainState(),
		machines: []*machine{{id: localMachine, label: "local", state: stateOnline,
			panes: []proto.PaneInfo{{ID: "p1", Name: "claude", State: proto.PaneRunning}}}}}
	m.rows = []row{{id: "pane:p1", kind: kindPane, machine: localMachine, paneID: "p1"}}
	m.cursor = "pane:p1"
	m.setFlash("worktree created with local files: .env, .claude/settings.local.json, CLAUDE.local.md", false)
	plain := ansi.Strip(m.statusBar())
	for _, want := range []string{"PANE", "ctrl+b tree", "ctrl+b v split", "ctrl+b - split down", "ctrl+b c tab"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("%q missing at 100 columns: %q", want, plain)
		}
	}
}
