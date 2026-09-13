package tui

import (
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
	if strings.Index(plain, "⚙ Settings") > strings.Index(plain, "conch ") {
		t.Fatalf("version should come after Settings: %q", plain)
	}
	m.width = 80
	if line, _ := m.layoutStatus(); strings.Contains(ansi.Strip(line), "conch 0") {
		t.Fatal("narrow terminals drop the version")
	}
}
