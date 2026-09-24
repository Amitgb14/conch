package server

import (
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestMonitorActivity(t *testing.T) {
	t0 := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	var m paneMonitor
	m.step(t0, true, false)
	if m.alert != "" {
		t.Fatalf("unmonitored pane alerted: %q", m.alert)
	}
	m.set(proto.PaneMonitor{Activity: true})
	m.step(t0, false, false)
	if m.alert != "" {
		t.Fatal("alerted without output")
	}
	m.step(t0, true, true)
	if m.alert != "" {
		t.Fatal("alerted while someone was looking")
	}
	m.step(t0, true, false)
	if m.alert != proto.AlertActivity {
		t.Fatalf("alert = %q", m.alert)
	}
	m.set(proto.PaneMonitor{Activity: true})
	if m.alert != "" {
		t.Fatal("setting the monitor should drop what was raised")
	}
}

func TestMonitorSilence(t *testing.T) {
	t0 := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	var m paneMonitor
	m.set(proto.PaneMonitor{Silence: 10})

	// A quiet pane has nothing to report until it prints.
	m.step(t0.Add(time.Hour), false, false)
	if m.alert != "" {
		t.Fatalf("silence before any output: %q", m.alert)
	}
	m.step(t0, true, false)
	m.step(t0.Add(9*time.Second), false, false)
	if m.alert != "" {
		t.Fatal("alerted before the silence was long enough")
	}
	m.step(t0.Add(10*time.Second), false, false) // exactly the limit
	if m.alert != proto.AlertSilence || m.armed {
		t.Fatalf("alert %q armed %v", m.alert, m.armed)
	}
	// Once per quiet spell: it does not re-raise after being looked at.
	m.alert = ""
	m.step(t0.Add(time.Hour), false, false)
	if m.alert != "" {
		t.Fatal("silence reported twice")
	}
	// Watched: going quiet is seen, not reported, and disarms.
	m.step(t0.Add(2*time.Hour), true, true)
	m.step(t0.Add(3*time.Hour), false, true)
	if m.alert != "" || m.armed {
		t.Fatalf("watched pane: alert %q armed %v", m.alert, m.armed)
	}
}

func TestMonitorSilenceOutranksActivity(t *testing.T) {
	t0 := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	var m paneMonitor
	m.set(proto.PaneMonitor{Activity: true, Silence: 5})
	m.step(t0, true, false)
	if m.alert != proto.AlertActivity {
		t.Fatalf("alert = %q", m.alert)
	}
	m.step(t0.Add(5*time.Second), false, false)
	if m.alert != proto.AlertSilence {
		t.Fatalf("quiet after output should say so: %q", m.alert)
	}
	// Output after that keeps the silence: it is what happened first.
	m.step(t0.Add(6*time.Second), true, false)
	if m.alert != proto.AlertSilence {
		t.Fatalf("alert = %q", m.alert)
	}
}

func TestMonitorInfo(t *testing.T) {
	var m paneMonitor
	if pm, alert := m.info(); pm != nil || alert != "" {
		t.Fatalf("zero monitor: %+v %q", pm, alert)
	}
	m.set(proto.PaneMonitor{Silence: 3})
	pm, _ := m.info()
	if pm == nil || pm.Silence != 3 {
		t.Fatalf("info: %+v", pm)
	}
	pm.Silence = 99 // a copy: clients can't change the entry's
	if m.Silence != 3 {
		t.Fatal("info returned the entry's own monitor")
	}
	// Turning monitoring off keeps nothing raised.
	m.alert = proto.AlertSilence
	m.set(proto.PaneMonitor{})
	if pm, alert := m.info(); pm != nil || alert != "" {
		t.Fatalf("off: %+v %q", pm, alert)
	}
}
