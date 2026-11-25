package server_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/server"
)

// switchLimitHome is limitHome with a Codex beside it, installed and
// signed in: it says its version when asked, and otherwise writes its
// arguments to the same log and waits.
func switchLimitHome(t *testing.T) (home, log string) {
	t.Helper()
	home, log = limitHome(t)
	script := "#!/bin/sh\n[ \"$1\" = --version ] && { echo 'codex-cli 0.99.0'; exit 0; }\n[ \"$1\" = app-server ] && exit 1\n" +
		"printf 'codex|%s\\n' \"$*\" >> " + log + "\nexec cat\n"
	if err := os.WriteFile(filepath.Join(home, ".local", "bin", "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(home, ".codex"), 0o700)
	os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte(`{"tokens":{}}`), 0o600)
	return home, log
}

// startOutOfPlan starts Claude Code on its only account, whose plan has
// run out, with a conversation worth switching.
func startOutOfPlan(t *testing.T, c *client.Client, home, dir string) proto.PaneInfo {
	t.Helper()
	ctx := t.Context()
	var p proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", Cwd: dir, Cols: 80, Rows: 20}, &p); err != nil {
		t.Fatal(err)
	}
	_, conv := claudeSession(t, home, dir)
	report(t, c, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "Stop", TranscriptPath: conv})
	report(t, c, proto.AgentReportParams{ID: p.ID, Agent: "claude", Event: "StatusLine",
		Limits: &proto.PlanLimits{FiveHour: &proto.LimitWindow{UsedPct: 100, ResetsAt: time.Now().Add(2 * time.Hour)}}})
	return p
}

// offerEvent waits for an agent.switch.offered about pane.
func offerEvent(t *testing.T, c *client.Client, pane string, gone bool) proto.AgentSwitchOffer {
	t.Helper()
	var o proto.AgentSwitchOffer
	waitEvent(t, c, func(m proto.Message) bool {
		o = proto.AgentSwitchOffer{}
		return m.Event == proto.EventAgentSwitchOffered && json.Unmarshal(m.Data, &o) == nil && o.Pane == pane && o.Gone == gone
	})
	return o
}

// TestOutOfPlanEverywhereOffersCodex: no Claude account has room, so
// Codex is offered — and only offered: nothing starts until somebody
// takes it. Called off, the offer is gone and not made again while the
// agent has not worked.
func TestOutOfPlanEverywhereOffersCodex(t *testing.T) {
	t.Cleanup(server.SetMoveTiming(100*time.Millisecond, 300*time.Millisecond))
	home, log := switchLimitHome(t)
	c, dir := startServer(t)
	if !hasCap(c, proto.CapAgentSwitchLimit) {
		t.Fatal("agent.switch.limit.v1 not announced")
	}
	p := startOutOfPlan(t, c, home, dir)
	o := offerEvent(t, c, p.ID, false)
	if o.From != "claude" || o.To != "codex" || o.Auto || o.Crossed != proto.SwitchNative || o.ResetsAt.IsZero() {
		t.Fatalf("offer: %+v", o)
	}
	time.Sleep(800 * time.Millisecond) // past any delay, several looks
	if got := launches(t, log, 1); len(got) != 1 {
		t.Fatalf("something started unasked: %q", got)
	}
	if err := c.Call(t.Context(), proto.MethodAgentSwitchCancel, proto.AgentSwitchCancelParams{ID: p.ID}, nil); err != nil {
		t.Fatal(err)
	}
	offerEvent(t, c, p.ID, true)
	// Not offered again: a second cancel finds nothing.
	time.Sleep(500 * time.Millisecond)
	badRequest(t, c.Call(t.Context(), proto.MethodAgentSwitchCancel, proto.AgentSwitchCancelParams{ID: p.ID}, nil), "offered again")

	// Taking it is an ordinary switch.
	var res proto.AgentSwitchResult
	if err := c.Call(t.Context(), proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "codex", NoContinue: true}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Pane.SwitchedFrom == nil || res.Reason != "" {
		t.Fatalf("switched: %+v", res)
	}
}

// TestSwitchAtLimitSwitchesUnasked: with switch_at_limit = "codex", the
// offer says it will switch, and does once its seconds are up — saying
// it was the limit — and the conversation can be switched back.
func TestSwitchAtLimitSwitchesUnasked(t *testing.T) {
	t.Cleanup(server.SetMoveTiming(100*time.Millisecond, 300*time.Millisecond))
	home, log := switchLimitHome(t)
	c, dir := startServer(t)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agents]\nswitch_at_limit = \"codex\"\n"), 0o600)
	p := startOutOfPlan(t, c, home, dir)
	o := offerEvent(t, c, p.ID, false)
	if !o.Auto || o.To != "codex" || o.At.IsZero() || o.Setting != "codex" {
		t.Fatalf("offer: %+v", o)
	}
	var res proto.AgentSwitchResult
	waitEvent(t, c, func(m proto.Message) bool {
		return m.Event == proto.EventAgentSwitched && json.Unmarshal(m.Data, &res) == nil && res.Pane.ID == p.ID
	})
	if res.Error != "" || res.Reason != "limit" || res.To != "codex" || res.Crossed != proto.SwitchNative {
		t.Fatalf("switched: %+v", res)
	}
	if second := launches(t, log, 2)[1]; !strings.HasPrefix(second, "codex|") || !strings.Contains(second, " resume "+res.Session) {
		t.Fatalf("Codex started as %q", second)
	}
	if sf := panesNow(t, c)[p.ID].SwitchedFrom; sf == nil || sf.Agent != "claude" {
		t.Fatalf("switched from: %+v", sf)
	}

	// Switch back, as the banner offers.
	report(t, c, proto.AgentReportParams{ID: p.ID, Agent: "codex", Event: "Stop", SessionID: res.Session})
	var back proto.AgentSwitchResult
	if err := c.Call(t.Context(), proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "claude", Now: true, NoContinue: true}, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Pane.SwitchedFrom.Back {
		t.Fatalf("back: %+v", back.Pane.SwitchedFrom)
	}
	if third := launches(t, log, 3)[2]; strings.HasPrefix(third, "codex|") {
		t.Fatalf("started as %q", third)
	}
}

// TestSwitchAtLimitCalledOff: the switch the offer counts down to is
// called off like any waiting switch, and nothing starts.
func TestSwitchAtLimitCalledOff(t *testing.T) {
	t.Cleanup(server.SetMoveTiming(100*time.Millisecond, 800*time.Millisecond))
	home, log := switchLimitHome(t)
	c, dir := startServer(t)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agents]\nswitch_at_limit = \"codex\"\n"), 0o600)
	p := startOutOfPlan(t, c, home, dir)
	offerEvent(t, c, p.ID, false)
	if err := c.Call(t.Context(), proto.MethodAgentSwitchCancel, proto.AgentSwitchCancelParams{ID: p.ID}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if got := launches(t, log, 1); len(got) != 1 {
		t.Fatalf("switched after it was called off: %q", got)
	}
	if panesNow(t, c)[p.ID].SwitchedFrom != nil {
		t.Fatal("switched after it was called off")
	}
}

// TestAnAccountWithRoomMovesRatherThanSwitches: account moves come first;
// with another Claude account that has room, no other agent is offered.
func TestAnAccountWithRoomMovesRatherThanSwitches(t *testing.T) {
	t.Cleanup(server.SetMoveTiming(100*time.Millisecond, 300*time.Millisecond))
	home, _ := switchLimitHome(t)
	c, dir := startServer(t)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agents]\nswitch_at_limit = \"codex\"\n"), 0o600)
	p := startAtLimit(t, c, home, dir)
	waitEvent(t, c, func(m proto.Message) bool {
		if m.Event == proto.EventAgentSwitchOffered || m.Event == proto.EventAgentSwitched {
			t.Fatalf("a switch with an account that had room: %s %s", m.Event, m.Data)
		}
		var moved proto.AgentMoved
		return m.Event == proto.EventAgentMoved && json.Unmarshal(m.Data, &moved) == nil && moved.Replaces == p.ID
	})
}

// TestContinueNowTakesTheOffer: switched by hand during the countdown
// (Continue Now), the server's own switch is not made a second time.
func TestContinueNowTakesTheOffer(t *testing.T) {
	t.Cleanup(server.SetMoveTiming(100*time.Millisecond, 800*time.Millisecond))
	home, log := switchLimitHome(t)
	c, dir := startServer(t)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[agents]\nswitch_at_limit = \"codex\"\n"), 0o600)
	p := startOutOfPlan(t, c, home, dir)
	offerEvent(t, c, p.ID, false)
	var res proto.AgentSwitchResult
	if err := c.Call(t.Context(), proto.MethodAgentSwitch, proto.AgentSwitchParams{ID: p.ID, To: "codex", NoContinue: true}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Pending || res.Pane.SwitchedFrom == nil {
		t.Fatalf("switched: %+v", res)
	}
	time.Sleep(1500 * time.Millisecond) // past the countdown
	if got := launches(t, log, 2); len(got) != 2 {
		t.Fatalf("launched %q", got)
	}
	for {
		select {
		case m := <-c.Events:
			var r proto.AgentSwitchResult
			if m.Event == proto.EventAgentSwitched && json.Unmarshal(m.Data, &r) == nil && r.Error != "" {
				t.Fatalf("a second switch was tried: %+v", r)
			}
			continue
		default:
		}
		break
	}
}
