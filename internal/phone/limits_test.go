package phone

import (
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestLimitsEndpoint: the plan limits agents reported reach the phone as
// one list of named windows per account, the three older fields spelled
// out as windows when the agent named none.
func TestLimitsEndpoint(t *testing.T) {
	f := newFixture(t)
	p := f.pair(PermView)

	var got Limits
	if status := p.get("/api/limits", &got); status != 200 || got.Machine != LocalMachine || got.Limits == nil || len(got.Limits) != 0 {
		t.Fatalf("nothing reported yet: %d %+v", status, got)
	}

	var pane proto.PaneInfo
	f.call(proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/cat"}, Cwd: f.dir, Cols: 80, Rows: 24}, &pane)
	resets := time.Now().Add(2 * time.Hour).Truncate(time.Second).UTC()
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: pane.ID, Agent: "claude", Event: "StatusLine",
		Limits: &proto.PlanLimits{FiveHour: &proto.LimitWindow{UsedPct: 42, ResetsAt: resets}, Week: &proto.LimitWindow{UsedPct: 18}}}, nil)
	f.call(proto.MethodAgentReport, proto.AgentReportParams{ID: pane.ID, Agent: "codex", Event: "StatusLine",
		Limits: &proto.PlanLimits{Windows: []proto.NamedWindow{{Key: "primary", UsedPct: 7}}}}, nil)

	deadline := time.Now().Add(5 * time.Second)
	for {
		got = Limits{}
		p.get("/api/limits", &got)
		if len(got.Limits) == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(got.Limits) != 2 {
		t.Fatalf("limits %+v", got.Limits)
	}
	c, x := got.Limits[0], got.Limits[1]
	if c.Agent != "claude" || x.Agent != "codex" {
		t.Fatalf("order %q %q", c.Agent, x.Agent)
	}
	if len(c.Windows) != 2 || c.Windows[0].Key != "five_hour" || c.Windows[0].UsedPct != 42 || !c.Windows[0].ResetsAt.Equal(resets) ||
		c.Windows[1].Key != "seven_day" || c.Windows[1].UsedPct != 18 || c.At.IsZero() {
		t.Errorf("claude %+v", c)
	}
	if len(x.Windows) != 1 || x.Windows[0].Key != "primary" || x.Windows[0].UsedPct != 7 {
		t.Errorf("codex %+v", x)
	}
	_, raw := p.do("GET", "/api/limits", nil)
	if strings.Contains(string(raw), "null") || strings.Contains(string(raw), "0001-01-01") {
		t.Errorf("null or a zero time in %s", raw)
	}
	var hello struct{ Features []string }
	p.get("/api/hello", &hello)
	if !strings.Contains(strings.Join(hello.Features, ","), FeatureLimits) {
		t.Errorf("hello features %v", hello.Features)
	}
	if status, b := p.do("GET", "/api/limits?machine=Bad!", nil); status != 400 {
		t.Errorf("a malformed machine: %d %s", status, b)
	}
}
