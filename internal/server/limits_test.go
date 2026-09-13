package server_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestStatusLineLimits(t *testing.T) {
	c, dir := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodPaneCreate, proto.PaneCreateParams{Command: []string{"/bin/cat"}, Cwd: dir}, &info); err != nil {
		t.Fatal(err)
	}
	resets := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	report := proto.AgentReportParams{ID: info.ID, Agent: "claude", Event: "StatusLine", ContextUsed: 45000, ContextSize: 200000,
		Limits: &proto.PlanLimits{FiveHour: &proto.LimitWindow{UsedPct: 42, ResetsAt: resets}, Week: &proto.LimitWindow{UsedPct: 18}}}
	if err := c.Call(ctx, proto.MethodAgentReport, report, nil); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, c, func(m proto.Message) bool {
		var l proto.PlanLimits
		return m.Event == proto.EventAgentLimits && json.Unmarshal(m.Data, &l) == nil && l.Agent == "claude" && l.FiveHour.UsedPct == 42
	})
	var res proto.AgentLimitsResult
	if err := c.Call(ctx, proto.MethodAgentLimits, nil, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Limits) != 1 || !res.Limits[0].FiveHour.ResetsAt.Equal(resets) || res.Limits[0].Week.UsedPct != 18 {
		t.Fatalf("limits: %+v", res.Limits)
	}
	// A status line report is not an agent state event.
	var list proto.PaneList
	c.Call(ctx, proto.MethodPaneList, nil, &list)
	if list.Panes[0].Agent != nil {
		t.Fatalf("status line changed agent detection: %+v", list.Panes[0].Agent)
	}
}
