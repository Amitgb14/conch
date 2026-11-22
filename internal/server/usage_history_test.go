package server_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// usage.history reads the agents' saved conversations under the server's
// HOME and answers each agent's periods.
func TestUsageHistory(t *testing.T) {
	home := a5Env(t)
	c, _ := startServer(t)
	a5Collect(c)
	if !hasCap(c, proto.CapUsageHistory) {
		t.Fatal("the server does not offer usage.history.v1")
	}
	ctx := a5Ctx(t)
	var empty proto.UsageHistory
	if err := c.Call(ctx, proto.MethodUsageHistory, nil, &empty); err != nil || len(empty.Agents) != 0 || empty.At.IsZero() {
		t.Fatalf("nothing saved: %+v %v", empty, err)
	}
	dir := filepath.Join(home, ".claude", "projects", "-src-api")
	os.MkdirAll(dir, 0o755)
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	line := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":12,"output_tokens":34}}}`+"\n", at)
	os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(line), 0o644)

	var got proto.UsageHistory
	if err := c.Call(ctx, proto.MethodUsageHistory, nil, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Agents) != 1 || got.Agents[0].Agent != "claude" || len(got.Agents[0].Periods) != 3 {
		t.Fatalf("%+v", got)
	}
	day := got.Agents[0].Periods[0]
	if day.Hours != 24 || day.Input != 12 || day.Output != 34 || len(day.Models) != 1 || day.Models[0].Model != "claude-opus-5-5" {
		t.Fatalf("24h %+v", day)
	}

	// By account, as Conch.app asks: the agent's own as "", with buckets;
	// an added account with nothing saved is left out.
	if !hasCap(c, proto.CapUsageAccounts) {
		t.Fatal("the server does not offer usage.history.accounts.v1")
	}
	if err := c.Call(ctx, proto.MethodAccountAdd, proto.AccountAddParams{Agent: "claude", Name: "work"}, nil); err != nil {
		t.Fatal(err)
	}
	var by proto.UsageHistory
	if err := c.Call(ctx, proto.MethodUsageHistory, proto.UsageHistoryParams{ByAccount: true}, &by); err != nil {
		t.Fatal(err)
	}
	if len(by.Agents) != 1 || by.Agents[0].Account != "" || len(by.Agents[0].Hours) != 24 || len(by.Agents[0].Days) != 30 {
		t.Fatalf("by account: %+v", by.Agents)
	}
	var out int
	for _, b := range by.Agents[0].Hours {
		out += b.Output
	}
	if out != 34 {
		t.Errorf("hours add up to %d, want 34", out)
	}
	// Asked as before, nothing new comes back.
	var plain proto.UsageHistory
	if err := c.Call(ctx, proto.MethodUsageHistory, proto.UsageHistoryParams{}, &plain); err != nil || plain.Agents[0].Days != nil {
		t.Errorf("without by_account: %+v %v", plain.Agents, err)
	}
	// A malformed request is said, not taken as the old one.
	var raw any
	if err := c.Call(ctx, proto.MethodUsageHistory, []int{1}, &raw); err == nil {
		t.Error("a list as params was accepted")
	}
}
