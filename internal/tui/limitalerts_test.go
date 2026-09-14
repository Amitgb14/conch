package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func limitsAt(agent string, fiveHour, week float64, resets time.Time) proto.PlanLimits {
	l := proto.PlanLimits{Agent: agent, At: time.Now()}
	if fiveHour >= 0 {
		l.FiveHour = &proto.LimitWindow{UsedPct: fiveHour, ResetsAt: resets}
	}
	if week >= 0 {
		l.Week = &proto.LimitWindow{UsedPct: week, ResetsAt: resets.Add(72 * time.Hour)}
	}
	return l
}

func TestLimitCrossings(t *testing.T) {
	now := time.Now()
	resets := now.Add(2 * time.Hour)
	at := []int{80, 95}
	seen := map[string]int{}
	cross := func(l proto.PlanLimits) []limitAlert { return limitCrossings(seen, localMachine, l, at, now) }

	if a := cross(limitsAt("claude", 79.9, 10, resets)); len(a) != 0 {
		t.Fatalf("below every threshold: %+v", a)
	}
	a := cross(limitsAt("claude", 80, 10, resets))
	if len(a) != 1 || a[0].threshold != 80 || a[0].window != "5-hour" || a[0].agent != "claude" {
		t.Fatalf("exactly 80%%: %+v", a)
	}
	if a := cross(limitsAt("claude", 85, 10, resets)); len(a) != 0 {
		t.Fatalf("still between thresholds: %+v", a)
	}
	if a := cross(limitsAt("claude", 99, 10, resets)); len(a) != 1 || a[0].threshold != 95 {
		t.Fatalf("passing 95%%: %+v", a)
	}
	if a := cross(limitsAt("claude", 100, 10, resets)); len(a) != 0 {
		t.Fatalf("already alerted at 95%%: %+v", a)
	}

	// A jump straight past both thresholds alerts once, for the higher.
	if a := cross(limitsAt("claude", 50, 97, resets)); len(a) != 1 || a[0].threshold != 95 || a[0].window != "week" {
		t.Fatalf("jump: %+v", a)
	}

	// A new window (after a reset) starts over.
	next := resets.Add(5 * time.Hour)
	if a := cross(limitsAt("claude", 81, -1, next)); len(a) != 1 || a[0].threshold != 80 {
		t.Fatalf("new window: %+v", a)
	}

	// Dropping under every threshold re-arms the window.
	cross(limitsAt("claude", 10, -1, next))
	if a := cross(limitsAt("claude", 82, -1, next)); len(a) != 1 {
		t.Fatalf("climbing back: %+v", a)
	}

	// Agents and machines are separate accounts.
	if a := cross(limitsAt("codex", 90, -1, resets)); len(a) != 1 || a[0].agent != "codex" {
		t.Fatalf("codex: %+v", a)
	}
	if a := limitCrossings(seen, "box", limitsAt("claude", 99, -1, resets), at, now); len(a) != 1 || a[0].machine != "box" {
		t.Fatalf("another machine: %+v", a)
	}

	// A window whose reset time has passed is stale: no alert.
	if a := cross(limitsAt("claude", 99, -1, now.Add(-time.Minute))); len(a) != 0 {
		t.Fatalf("stale window: %+v", a)
	}
	// Spend limits have no reset time.
	spend := proto.PlanLimits{Agent: "claude", Spend: &proto.LimitWindow{UsedPct: 96}}
	if a := cross(spend); len(a) != 1 || a[0].window != "spend" || !a[0].resets.IsZero() {
		t.Fatalf("spend: %+v", a)
	}
	if a := cross(spend); len(a) != 0 {
		t.Fatal("spend alerted twice")
	}
	// Nothing reported at all.
	if a := cross(proto.PlanLimits{Agent: "claude"}); len(a) != 0 {
		t.Fatalf("empty: %+v", a)
	}
}

func TestLimitCrossingsCustomThresholds(t *testing.T) {
	seen := map[string]int{}
	now := time.Now()
	l := limitsAt("claude", 55, -1, now.Add(time.Hour))
	if a := limitCrossings(seen, localMachine, l, []int{50}, now); len(a) != 1 || a[0].threshold != 50 {
		t.Fatalf("50%%: %+v", a)
	}
	if a := limitCrossings(map[string]int{}, localMachine, l, nil, now); len(a) != 0 {
		t.Fatalf("no thresholds: %+v", a)
	}
}

func TestPruneLimitAlerts(t *testing.T) {
	now := time.Now()
	seen := map[string]int{
		limitKey(localMachine, "claude", "5-hour", now.Add(time.Hour)): 80,
		limitKey(localMachine, "claude", "week", now.Add(-time.Hour)):  95,
		limitKey(localMachine, "claude", "spend", time.Time{}):         80,
		"garbage":            80,
		"a|b|c|not-a-number": 80,
	}
	pruneLimitAlerts(seen, now)
	if len(seen) != 2 || seen[limitKey(localMachine, "claude", "5-hour", now.Add(time.Hour))] != 80 ||
		seen[limitKey(localMachine, "claude", "spend", time.Time{})] != 80 {
		t.Fatalf("after prune: %v", seen)
	}
}

func TestLimitAlertText(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.Local)
	a := limitAlert{agent: "claude", window: "5-hour", pct: 82.7, resets: now.Add(3 * time.Hour)}
	if got := a.text("", now); got != "Claude 5-hour limit 82% used · resets 13:00" {
		t.Errorf("local: %q", got)
	}
	a.resets = time.Time{}
	a.agent, a.window = "codex", "week"
	if got := a.text("devbox", now); got != "Codex week limit 82% used on devbox" {
		t.Errorf("remote without reset: %q", got)
	}
}

func TestAlertLimitsInTUI(t *testing.T) {
	a2Isolate(t)
	resets := time.Now().Add(2 * time.Hour)
	limits := []proto.PlanLimits{limitsAt("claude", 83, 20, resets)}

	m := a2Model()
	mach := m.machines[0]
	next, cmd := m.update(limitsMsg{machine: mach.id, gen: mach.gen, limits: limits})
	*m = next.(Model)
	if !strings.Contains(m.flash, "Claude 5-hour limit 83% used") || m.flashIsErr {
		t.Fatalf("flash %q (err %v)", m.flash, m.flashIsErr)
	}
	if cmd == nil || m.limitSeen[limitKey(mach.id, "claude", "5-hour", resets)] != 80 {
		t.Fatalf("no notification or record: %v", m.limitSeen)
	}
	if mach.limits["claude"].FiveHour.UsedPct != 83 {
		t.Fatal("limits not stored")
	}

	// The same report again (a reconnect) doesn't alert.
	m.flash = ""
	next, cmd = m.update(limitsMsg{machine: mach.id, gen: mach.gen, limits: limits})
	*m = next.(Model)
	if m.flash != "" || cmd != nil {
		t.Fatalf("repeated: %q", m.flash)
	}

	// An agent.limits event past 95% is shown as an error.
	ev := proto.Message{Event: proto.EventAgentLimits, Data: mustJSON(t, limitsAt("claude", 96, 20, resets))}
	if cmd := m.handleEvent(mach, ev); cmd == nil || !m.flashIsErr || !strings.Contains(m.flash, "96%") {
		t.Fatalf("event: %q err %v", m.flash, m.flashIsErr)
	}

	// Silenced: still flashed and recorded, no desktop notification.
	m2 := a2Model()
	m2.snoozeUntil = time.Now().Add(time.Hour)
	if cmd := m2.alertLimits(m2.machines[0], limitsAt("codex", 90, -1, resets)); cmd == nil || !strings.Contains(m2.flash, "Codex") {
		t.Fatalf("snoozed: %q", m2.flash)
	}

	// Turned off: nothing, and nothing recorded so turning it on alerts.
	m3 := a2Model()
	m3.cfg.Notify.Limits = false
	if cmd := m3.alertLimits(m3.machines[0], limitsAt("claude", 99, -1, resets)); cmd != nil || m3.flash != "" || len(m3.limitSeen) != 0 {
		t.Fatalf("disabled: %q %v", m3.flash, m3.limitSeen)
	}
	m3.cfg.Notify.Limits = true
	if cmd := m3.alertLimits(m3.machines[0], limitsAt("claude", 99, -1, resets)); cmd == nil {
		t.Fatal("enabled later: no alert")
	}

	// A remote machine is named.
	m4 := a2Model()
	box := newMachine("box", "devbox", "dev@box")
	if m4.alertLimits(box, limitsAt("claude", 81, -1, resets)); !strings.HasSuffix(m4.flash, "on devbox") {
		t.Fatalf("remote: %q", m4.flash)
	}
}

func TestLimitAlertsPersist(t *testing.T) {
	a2Isolate(t)
	path := filepath.Join(t.TempDir(), "ui.json")
	now := time.Now()
	live := limitKey(localMachine, "claude", "5-hour", now.Add(time.Hour))
	gone := limitKey(localMachine, "claude", "week", now.Add(-time.Hour))

	m := a2Model()
	m.statePath = path
	m.limitSeen = map[string]int{live: 95, gone: 80}
	a2Run(m.saveState())

	st := loadUIState(path)
	if len(st.LimitAlerts) != 1 || st.LimitAlerts[live] != 95 {
		t.Fatalf("loaded: %v", st.LimitAlerts)
	}

	// A ui.json from before alerts existed loads with none.
	os.WriteFile(path, []byte(`{"expanded":{},"show_all":{}}`), 0o600)
	if st := loadUIState(path); st.LimitAlerts == nil || len(st.LimitAlerts) != 0 {
		t.Fatalf("old file: %v", st.LimitAlerts)
	}
}

func TestLimitAlertSetting(t *testing.T) {
	a2Isolate(t)
	m := a2Model()
	s := &settings{}
	var item settingItem
	for _, it := range s.notifyItems(m) {
		if it.label == "A plan limit is nearly used" {
			item = it
		}
	}
	if item.run == nil || item.detail != "Claude/Codex at 80%, 95%" || !*item.on {
		t.Fatalf("setting: %+v", item)
	}
	item.run(m)
	if m.cfg.Notify.Limits {
		t.Fatal("toggle did not turn it off")
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
