package tui

import (
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestFailedAgentGlyphAndNotification(t *testing.T) {
	m, _ := a1Fixture(t, false)
	failed := proto.PaneInfo{ID: "p9", Name: "opencode", State: proto.PaneRunning,
		Agent: &proto.AgentStatus{Name: "opencode", State: proto.AgentDone, Failed: true, Message: "the last request failed"}}
	if g, label, _ := m.paneGlyph(failed); g != "✗" || label != "failed" {
		t.Fatalf("done+failed: %q %q", g, label)
	}
	failed.Agent.State = proto.AgentIdle
	if g, label, _ := m.paneGlyph(failed); g != "✗" || label != "failed" {
		t.Fatalf("idle+failed: %q %q", g, label)
	}
	// Working again: no longer shown as failed even if a stale flag lingers.
	failed.Agent.State = proto.AgentWorking
	if _, label, _ := m.paneGlyph(failed); label != "working" {
		t.Fatalf("working: %q", label)
	}

	m.cfg.Notify.Desktop = false
	m.cfg.Notify.Bell = true // notify() returns a command we don't run
	failed.Agent.State = proto.AgentDone
	old := failed
	old.Agent = &proto.AgentStatus{Name: "opencode", State: proto.AgentWorking}
	if cmd := m.notifyAttention(m.machines[0], old, failed); cmd == nil {
		t.Fatal("no notification for a failure")
	}
	if got := attentionBody(failed); got != "opencode stopped: the last request failed" {
		t.Fatalf("body: %q", got)
	}
	for _, c := range []struct {
		a    proto.AgentStatus
		want string
	}{
		{proto.AgentStatus{State: proto.AgentDone}, "opencode finished"},
		{proto.AgentStatus{State: proto.AgentBlocked}, "opencode is waiting for you"},
		{proto.AgentStatus{State: proto.AgentBlocked, Message: "Allow Bash?"}, "Allow Bash?"},
	} {
		info := failed
		a := c.a
		info.Agent = &a
		if got := attentionBody(info); got != c.want {
			t.Errorf("%+v: %q, want %q", c.a, got, c.want)
		}
	}
}
