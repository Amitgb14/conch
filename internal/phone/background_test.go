package phone

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// An agent's card says what it left running (agent.background.v1), with
// each job as the server named it.
func TestAgentCarriesBackgroundJobs(t *testing.T) {
	since := time.Date(2026, 11, 13, 9, 0, 0, 0, time.FixedZone("x", 3600))
	p := proto.PaneInfo{ID: "p1", Name: "w", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "claude",
		State: proto.AgentDone, Background: 2, Jobs: []proto.BackgroundJob{
			{PID: 41, Command: "npm run dev", Since: since, Ports: []int{5173}},
			{PID: 42, Command: "pytest -f", Since: since},
		}}}
	a := buildAgent("local", p, nil, nil)
	if len(a.Background) != 2 || a.Background[0].PID != 41 || a.Background[0].Command != "npm run dev" ||
		a.Background[0].Ports[0] != 5173 || !a.Background[0].Since.Equal(since) || a.Background[0].Since.Location() != time.UTC {
		t.Fatalf("background %+v", a.Background)
	}
	b, _ := json.Marshal(a)
	if !strings.Contains(string(b), `"background":[{"pid":41,"command":"npm run dev","since":"2026-11-13T08:00:00Z","ports":[5173]}`) {
		t.Fatalf("json %s", b)
	}

	// None: nothing on the wire, as for every optional field.
	p.Agent.Background, p.Agent.Jobs = 0, nil
	if b, _ := json.Marshal(buildAgent("local", p, nil, nil)); strings.Contains(string(b), "background") {
		t.Fatalf("no jobs: %s", b)
	}
	// A count from a server that sent no list is still said, as jobs
	// with nothing known about them.
	p.Agent.Background = 1
	if a := buildAgent("local", p, nil, nil); len(a.Background) != 1 || a.Background[0].PID != 0 {
		t.Fatalf("count alone: %+v", a.Background)
	}
}

// A push says how many things the agent left running, and nothing of
// what they are: it passes through Apple's or Google's servers, and a
// command line can say as much as a question.
func TestPushSaysHowManyStillRun(t *testing.T) {
	p := proto.PaneInfo{ID: "p1", Name: "w", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "claude",
		State: proto.AgentDone, Background: 2, Jobs: []proto.BackgroundJob{{PID: 4, Command: "SECRET=x npm run dev"}}}}
	msg := pushOf(StateDone, p, "busybox:p1", "api")
	if msg != (PushMessage{Type: "done", Pane: "busybox:p1", Name: "w", Project: "api", URL: "/agent/busybox:p1", Running: 2}) {
		t.Fatalf("message %+v", msg)
	}
	if b, _ := json.Marshal(msg); strings.Contains(string(b), "npm") || strings.Contains(string(b), "SECRET") {
		t.Fatalf("a command in a push: %s", b)
	}
	p.Agent.Background = 0
	if b, _ := json.Marshal(pushOf(StateDone, p, "p1", "")); strings.Contains(string(b), "running") {
		t.Fatalf("nothing running, nothing said: %s", b)
	}
}
