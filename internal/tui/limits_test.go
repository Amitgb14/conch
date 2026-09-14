package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

func TestA2StatusLimits(t *testing.T) {
	m := a2Model()
	now := time.Now()
	m.rows = []row{{id: "pane:p1", kind: kindPane, machine: localMachine, paneID: "p1"}, {id: "pane:p2", kind: kindPane, machine: localMachine, paneID: "p2"}}
	m.cursor = "pane:p1"
	if m.statusLimits(now) != nil {
		t.Fatal("no limits reported")
	}
	mach := m.machines[0]
	mach.setLimits(proto.PlanLimits{Agent: "codex", Week: &proto.LimitWindow{UsedPct: 95, ResetsAt: now.Add(48 * time.Hour)}})
	mach.setLimits(proto.PlanLimits{Agent: "claude", FiveHour: &proto.LimitWindow{UsedPct: 10, ResetsAt: now.Add(-time.Minute)}})

	// The viewed agent's limits come first, but expired windows hide them.
	items := m.statusLimits(now)
	if len(items) != 1 || ansi.Strip(items[0].text) != "Codex 7d 95%" {
		t.Fatalf("status limits: %+v", items)
	}
	mach.setLimits(proto.PlanLimits{Agent: "claude", FiveHour: &proto.LimitWindow{UsedPct: 72.4}, Spend: &proto.LimitWindow{UsedPct: 3}})
	items = m.statusLimits(now)
	if got := ansi.Strip(items[0].text); got != "Claude 5h 72% · spend 3%" {
		t.Fatalf("claude pane: %q", got)
	}
	// Clicking spells the windows out.
	items[0].act(m)
	if m.flash != "Claude Code: 5-hour 72% used · spend 3% used" {
		t.Fatalf("detail flash %q", m.flash)
	}
	// A shell pane falls back to claude, then codex.
	m.cursor = "pane:p2"
	if got := ansi.Strip(m.statusLimits(now)[0].text); !strings.HasPrefix(got, "Claude") {
		t.Fatalf("fallback: %q", got)
	}
	delete(mach.limits, "claude")
	if got := ansi.Strip(m.statusLimits(now)[0].text); !strings.HasPrefix(got, "Codex") {
		t.Fatalf("codex fallback: %q", got)
	}
	mach.limits = map[string]proto.PlanLimits{"gemini": {Agent: "gemini", Week: &proto.LimitWindow{UsedPct: 1}}}
	if m.statusLimits(now) != nil {
		t.Fatal("only the viewed agent, claude and codex are shown")
	}
	if limitsChip(proto.PlanLimits{Agent: "claude"}, now) != "" {
		t.Fatal("a chip without windows is empty")
	}
}

func TestA2LimitsHelpers(t *testing.T) {
	for p, want := range map[float64]string{95: styleErr.Render("x"), 90: styleErr.Render("x"), 75: styleWarn.Render("x"), 10: styleMuted.Render("x")} {
		if pctStyle(p)("x") != want {
			t.Errorf("pctStyle(%v)", p)
		}
	}
	for p, want := range map[float64]string{95: styleErr.Render("x"), 70: styleWarn.Render("x"), 10: styleOK.Render("x")} {
		if pctStyleBar(p)("x") != want {
			t.Errorf("pctStyleBar(%v)", p)
		}
	}
	for agent, want := range map[string]string{"claude": "Claude", "codex": "Codex", "gemini": "Gemini CLI", "mystery": "mystery"} {
		if got := shortAgent(agent); got != want {
			t.Errorf("shortAgent(%s) = %q", agent, got)
		}
	}
	for pct, want := range map[float64]string{0: "░░░░░░░░░░", 50: "█████░░░░░", 100: "██████████", 150: "██████████", -5: "░░░░░░░░░░"} {
		if got := ansi.Strip(bar(pct, 10)); got != want {
			t.Errorf("bar(%v) = %q", pct, got)
		}
	}

	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{now.Add(30 * time.Minute), "in 31m"},
		{now.Add(3 * time.Hour), "13:00"},
		{now.Add(20 * time.Hour), "Tue 06:00"},
		{now.Add(10 * 24 * time.Hour), "Sep 24 10:00"},
	} {
		if got := resetText(c.at, now); got != c.want {
			t.Errorf("resetText(+%v) = %q, want %q", c.at.Sub(now), got, c.want)
		}
	}
	if liveWindow(nil, now) != nil || liveWindow(&proto.LimitWindow{}, now) == nil {
		t.Fatal("liveWindow")
	}
}

func TestA2LimitsLines(t *testing.T) {
	m := a2Model()
	mach := m.machines[0]
	if m.limitsLines(mach, 80) != nil {
		t.Fatal("no limits, no block")
	}
	now := time.Now()
	mach.setLimits(proto.PlanLimits{Agent: "claude", At: now.Add(-5 * time.Minute),
		FiveHour: &proto.LimitWindow{UsedPct: 42, ResetsAt: now.Add(90 * time.Minute)},
		Week:     &proto.LimitWindow{UsedPct: 91},
	})
	mach.setLimits(proto.PlanLimits{Agent: "codex", Week: &proto.LimitWindow{UsedPct: 50, ResetsAt: now.Add(-time.Hour)}})
	out := a2Plain(m.limitsLines(mach, 80))
	for _, want := range []string{"Claude Code plan usage  as of 5m", "5-hour  ████████░░░░░░░░░░░░  42%  resets ", "week    ██████████████████░░  91%"} {
		if !strings.Contains(out, want) {
			t.Fatalf("limits lines lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Codex") {
		t.Fatalf("an agent with only expired windows is hidden:\n%s", out)
	}
}

func TestA2Notify(t *testing.T) {
	// The commands would ring, play or post a notification, so they are only built.
	if notify(config.NotifyCfg{Desktop: true, Sound: true, Bell: true}, "t", "b") != nil {
		t.Fatal("notifications off")
	}
	if notify(config.NotifyCfg{Enabled: true}, "t", "b") != nil {
		t.Fatal("no way to notify")
	}
	for _, cfg := range []config.NotifyCfg{{Enabled: true, Bell: true}, {Enabled: true, Sound: true}, {Enabled: true, Desktop: true}} {
		if notify(cfg, "t", "b") == nil {
			t.Fatalf("%+v should notify", cfg)
		}
	}
	m := Model{}
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.Local)
	m.snoozeUntil = now.Add(30 * time.Minute)
	if got := m.silenceLabel(now); got != "🔕 snoozed until 12:30" {
		t.Fatalf("snooze label %q", got)
	}
	// Quiet hours are only shown while notifications are on.
	m.snoozeUntil = time.Time{}
	m.cfg.Notify.QuietStart, m.cfg.Notify.QuietEnd = "09:00", "18:00"
	if !m.silenced(now) || m.silenceLabel(now) != "" {
		t.Fatalf("quiet with notifications off: %q", m.silenceLabel(now))
	}
	if _, id := nextAttention(nil, ""); id != "" {
		t.Fatal("nothing waiting")
	}
}
