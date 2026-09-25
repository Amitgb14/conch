package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// monitorFixture is a1Fixture online through a server that can monitor.
func monitorFixture(t *testing.T, caps ...string) (*Model, *a1Peer) {
	t.Helper()
	m, _ := a1Fixture(t, false)
	if caps == nil {
		caps = []string{proto.CapPaneMonitor}
	}
	c, peer := a1FakeClient(t, caps...)
	m.machines[0].c, m.machines[0].server = c, c.Server
	return m, peer
}

func TestToggleMonitor(t *testing.T) {
	m, peer := monitorFixture(t)
	run := func(quiet bool) {
		t.Helper()
		cmd := m.toggleMonitor(localMachine, "p2", quiet)
		if cmd == nil {
			t.Fatal("no call")
		}
		cmd()
	}
	run(true)
	peer.waitMethod(t, proto.MethodPaneMonitor, `"id":"p2","silence":30}`)
	if m.flashIsErr || !strings.Contains(m.flash, "quiet for 30s") {
		t.Fatalf("flash %q", m.flash)
	}
	// What the server then reports is what the next toggle starts from.
	m.machines[0].panes[1].Monitor = &proto.PaneMonitor{Silence: 30}
	run(false)
	peer.waitMethod(t, proto.MethodPaneMonitor, `"id":"p2","activity":true,"silence":30}`)
	m.machines[0].panes[1].Monitor = &proto.PaneMonitor{Activity: true, Silence: 30}
	run(true)
	peer.waitMethod(t, proto.MethodPaneMonitor, `"id":"p2","activity":true}`)
	if !strings.Contains(m.flash, "no alert when it goes quiet") {
		t.Fatalf("flash %q", m.flash)
	}
	m.machines[0].panes[1].Monitor = &proto.PaneMonitor{Activity: true}
	run(false)
	peer.waitMethod(t, proto.MethodPaneMonitor, `{"id":"p2"}`)

	// The configured silence, and a nonsense one falls back to the default.
	m.machines[0].panes[1].Monitor = nil
	m.cfg.Notify.Silence = 5
	run(true)
	peer.waitMethod(t, proto.MethodPaneMonitor, `"silence":5}`)
	m.cfg.Notify.Silence = -3
	run(true)
	if peer.count(t, m.machines[0].c, proto.MethodPaneMonitor, `{"id":"p2","silence":30}`) != 2 {
		t.Fatal("a negative setting should use the default")
	}

	if m.toggleMonitor(localMachine, "p404", true) != nil {
		t.Fatal("an unknown pane")
	}
	if m.toggleMonitor("elsewhere", "p2", true) != nil {
		t.Fatal("an unknown machine")
	}
}

func TestToggleMonitorOldServer(t *testing.T) {
	m, _ := monitorFixture(t, "pane.v1")
	if m.toggleMonitor(localMachine, "p2", true) != nil {
		t.Fatal("called a server without pane.monitor")
	}
	if !m.flashIsErr || !strings.Contains(m.flash, "too old") {
		t.Fatalf("flash %q", m.flash)
	}
	m.machines[0].c = nil // offline
	if m.toggleMonitor(localMachine, "p2", true) != nil {
		t.Fatal("offline machine")
	}
}

func TestMonitorPrefixKeys(t *testing.T) {
	m, peer := monitorFixture(t)
	a1Open(t, m, paneNodeID(localMachine, "p2"))
	m.focus = focusMain
	if cmd := a1Prefixed(t, m, runes("M")); cmd == nil {
		t.Fatal("ctrl+b M did nothing")
	} else {
		cmd()
	}
	peer.waitMethod(t, proto.MethodPaneMonitor, `"silence":30`)
	if cmd := a1Prefixed(t, m, runes("A")); cmd == nil {
		t.Fatal("ctrl+b A did nothing")
	} else {
		cmd()
	}
	peer.waitMethod(t, proto.MethodPaneMonitor, `"activity":true`)
}

func TestMonitorMenuItems(t *testing.T) {
	m, _ := monitorFixture(t)
	items := m.monitorItems(localMachine, "p2")
	if len(items) != 2 || strings.HasPrefix(items[0].label, "✓") || strings.HasPrefix(items[1].label, "✓") {
		t.Fatalf("items %+v", items)
	}
	if !strings.Contains(items[0].label, "ctrl+b M") || !strings.Contains(items[1].label, "ctrl+b A") {
		t.Fatalf("labels should name their keys: %q %q", items[0].label, items[1].label)
	}
	m.machines[0].panes[1].Monitor = &proto.PaneMonitor{Activity: true, Silence: 30}
	items = m.monitorItems(localMachine, "p2")
	if !strings.HasPrefix(items[0].label, "✓") || !strings.HasPrefix(items[1].label, "✓") {
		t.Fatalf("ticks: %q %q", items[0].label, items[1].label)
	}
	m.machines[0].panes[1].State = proto.PaneExited
	if items := m.monitorItems(localMachine, "p2"); items != nil {
		t.Fatal("an exited pane has nothing to watch")
	}
	if items := m.monitorItems(localMachine, "p404"); items != nil {
		t.Fatal("an unknown pane")
	}
}

func TestNotifyMonitor(t *testing.T) {
	m, _ := monitorFixture(t)
	mach := m.machines[0]
	base := mach.panes[1]
	raised := base
	raised.Alert, raised.Monitor = proto.AlertSilence, &proto.PaneMonitor{Silence: 30}
	if m.notifyMonitor(mach, base, raised) == nil {
		t.Fatal("a new alert should notify")
	}
	if m.notifyMonitor(mach, raised, raised) != nil {
		t.Fatal("the same alert twice")
	}
	if m.notifyMonitor(mach, raised, base) != nil {
		t.Fatal("an alert cleared is not news")
	}
	m.snoozeUntil = time.Now().Add(time.Hour)
	if m.notifyMonitor(mach, base, raised) != nil {
		t.Fatal("snoozed")
	}
	m.snoozeUntil = time.Time{}
	m.viewMachine, m.viewing = localMachine, base.ID
	if m.notifyMonitor(mach, base, raised) != nil {
		t.Fatal("the pane on screen")
	}
	m.viewing = ""
	m.cfg.Notify.Enabled = false
	if m.notifyMonitor(mach, base, raised) != nil {
		t.Fatal("notifications off")
	}
}

func TestAlertBody(t *testing.T) {
	p := proto.PaneInfo{Name: "make", Alert: proto.AlertSilence, Monitor: &proto.PaneMonitor{Silence: 45}}
	if got := alertBody(p); got != "make has been quiet for 45s" {
		t.Fatal(got)
	}
	p.Monitor = nil
	if got := alertBody(p); got != "make has gone quiet" {
		t.Fatal(got)
	}
	p.Alert = proto.AlertActivity
	if got := alertBody(p); got != "make printed something" {
		t.Fatal(got)
	}
}

func TestMonitorGlyphs(t *testing.T) {
	m, _ := monitorFixture(t)
	p := proto.PaneInfo{State: proto.PaneRunning}
	if g, _, _ := m.paneGlyph(p); g != "›" {
		t.Fatalf("plain terminal %q", g)
	}
	p.Alert = proto.AlertActivity
	if g, label, _ := m.paneGlyph(p); g != "#" || label != "output" {
		t.Fatalf("activity %q %q", g, label)
	}
	p.Alert = proto.AlertSilence
	if g, label, _ := m.paneGlyph(p); g != "~" || label != "quiet" {
		t.Fatalf("silence %q %q", g, label)
	}
	// An agent's own state wins; an exited pane shows its exit.
	p.Agent = &proto.AgentStatus{Name: "claude", State: proto.AgentBlocked}
	if g, _, _ := m.paneGlyph(p); g != "!" {
		t.Fatalf("agent %q", g)
	}
	p.Agent, p.State, p.ExitCode = nil, proto.PaneExited, 2
	if g, _, _ := m.paneGlyph(p); g != "✗" {
		t.Fatalf("exited %q", g)
	}
}

func TestPaneMenuHasMonitoring(t *testing.T) {
	m, _ := monitorFixture(t)
	id := paneNodeID(localMachine, "p2")
	mn := newRowMenu(*m, m.rows[indexOfRow(m.rows, id)], 0, 0)
	var labels []string
	for _, it := range mn.items {
		labels = append(labels, it.label)
	}
	all := strings.Join(labels, "|")
	if !strings.Contains(all, "Alert when it goes quiet") || !strings.Contains(all, "Alert on output") ||
		!strings.HasSuffix(all, "Close") {
		t.Fatalf("menu: %s", all)
	}
}

func TestTabAlertFlags(t *testing.T) {
	m, _ := monitorFixture(t)
	a := m.newLeaf(viewRef{Kind: kindPane, Machine: localMachine, PaneID: "p2"})
	b := m.newLeaf(viewRef{Kind: kindPane, Machine: localMachine, PaneID: "p3"})
	tb := &tab{root: &layoutNode{leaf: a}, focus: a.id}
	tb.root.split(a.id, splitRight, b)
	plain := m.tabLabel(tb)
	if strings.HasSuffix(plain, "#") || strings.HasSuffix(plain, "~") {
		t.Fatalf("no alerts: %q", plain)
	}
	m.machines[0].panes[2].Alert = proto.AlertActivity // p3, the second split
	if got := m.tabLabel(tb); got != plain+" #" {
		t.Fatalf("activity: %q", got)
	}
	m.machines[0].panes[1].Alert = proto.AlertSilence
	if got := m.tabLabel(tb); got != plain+" ~" {
		t.Fatalf("silence wins: %q", got)
	}
	// A tab the user named is flagged too.
	tb.name = "build"
	if got := m.tabLabel(tb); got != "build ~" {
		t.Fatalf("named tab: %q", got)
	}
	// A tab of something other than panes, or a pane since closed.
	m.machines[0].panes = m.machines[0].panes[:1]
	tb.name = ""
	if got := m.tabAlert(tb); got != "" {
		t.Fatalf("closed panes: %q", got)
	}
}

// The menu's two entries run what they say, and an alert names the pane,
// the agent and the machine it happened on.
func TestA2MonitorMenuAndAlertText(t *testing.T) {
	m := a2Model()
	c, peer := a1FakeClient(t, "pane.monitor.v1")
	m.machines[0].c = c
	items := m.monitorItems(localMachine, "p2")
	if len(items) != 2 {
		t.Fatalf("want a quiet and an output entry, got %d", len(items))
	}
	// Each one asks the server; the first for quiet, the second for output.
	for i, item := range items {
		if cmd := item.run(m); cmd != nil {
			a2Run(cmd)
		}
		peer.waitMethod(t, proto.MethodPaneMonitor, "")
		_ = i
	}

	// What the alert says, for each kind and each place it can come from.
	quiet := proto.PaneInfo{ID: "p2", Name: "build", Alert: proto.AlertSilence,
		Monitor: &proto.PaneMonitor{Silence: 30}}
	if got := alertBody(quiet); !strings.Contains(got, "quiet for 30s") {
		t.Errorf("a timed silence: %q", got)
	}
	quiet.Monitor = nil
	if got := alertBody(quiet); !strings.Contains(got, "has gone quiet") {
		t.Errorf("silence without a setting: %q", got)
	}
	if got := alertBody(proto.PaneInfo{ID: "p2", Name: "build", Alert: proto.AlertActivity}); !strings.Contains(got, "printed something") {
		t.Errorf("activity: %q", got)
	}
}

// An alert is told once, about a pane you are not looking at, and never
// while quiet hours are on.
func TestA2MonitorNotifies(t *testing.T) {
	m := a2Model()
	m.cfg.Notify.Enabled, m.cfg.Notify.Bell = true, true
	mach := m.machines[0]
	old := proto.PaneInfo{ID: "p2", Name: "build"}
	fresh := proto.PaneInfo{ID: "p2", Name: "build", Alert: proto.AlertActivity}

	if cmd := m.notifyMonitor(mach, old, fresh); cmd == nil {
		t.Fatal("a new alert should be told")
	}
	// The same alert again says nothing.
	if cmd := m.notifyMonitor(mach, fresh, fresh); cmd != nil {
		t.Fatal("an alert already told should not be repeated")
	}
	// Nor does one about the pane on screen.
	m.viewing, m.viewMachine = "p2", localMachine
	if cmd := m.notifyMonitor(mach, old, fresh); cmd != nil {
		t.Fatal("no alert about the pane you are looking at")
	}
	m.viewing = ""
	// Quiet hours silence it.
	m.cfg.Notify.QuietStart, m.cfg.Notify.QuietEnd = "00:00", "23:59"
	if cmd := m.notifyMonitor(mach, old, fresh); cmd != nil {
		t.Fatal("quiet hours should silence an alert")
	}
	m.cfg.Notify.QuietStart, m.cfg.Notify.QuietEnd = "", ""
	// An agent's name and a remote machine's label reach the title.
	withAgent := fresh
	withAgent.Agent = &proto.AgentStatus{Name: "codex"}
	mach.id, mach.label = "busybox", "busybox"
	if cmd := m.notifyMonitor(mach, old, withAgent); cmd == nil {
		t.Fatal("an agent's alert on another machine should be told")
	}
}
