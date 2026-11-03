package brain

import (
	"reflect"
	"strings"
	"testing"
)

func fastWorld() World {
	return World{
		DefaultAgent: "claude",
		Machines: []Machine{
			{ID: "local", Label: "this computer", Online: true, Agents: []string{"claude", "codex"},
				Projects: []Project{{ID: "j1", Name: "api", Git: true}},
				Panes: []Pane{
					{ID: "p1", Name: "✳ Fix login", Agent: "claude", State: "blocked", Branch: "fix-login", Summary: "asks to run rm"},
					{ID: "p2", Name: "codex", Agent: "codex", State: "working", Branch: "rate-limit"},
					{ID: "p3", Name: "zsh"},
					{ID: "p4", Name: "api", Agent: "claude", State: "idle"}, // shares the project's name
				}},
			{ID: "busybox", Label: "busybox", Online: true, Agents: []string{"claude"},
				Panes: []Pane{{ID: "p1", Name: "docs", Agent: "gemini", State: "blocked"}}},
			{ID: "away", Label: "away", Online: false,
				Panes: []Pane{{ID: "p9", Name: "sleeper", Agent: "claude", State: "blocked"}}},
		},
	}
}

func TestFastPlanActions(t *testing.T) {
	w := fastWorld()
	cases := []struct {
		req  string
		want []Action
	}{
		{"show codex", []Action{{Type: ActFocus, Machine: "local", Pane: "p2"}}},
		{"Focus the fix login pane", []Action{{Type: ActFocus, Machine: "local", Pane: "p1"}}},
		{"go to docs", []Action{{Type: ActFocus, Machine: "busybox", Pane: "p1"}}},
		{"switch to fix-login", []Action{{Type: ActFocus, Machine: "local", Pane: "p1"}}},
		{"show zsh", []Action{{Type: ActFocus, Machine: "local", Pane: "p3"}}},
		{"close the zsh terminal.", []Action{{Type: ActClose, Machine: "local", Pane: "p3"}}},
		{"kill codex", []Action{{Type: ActClose, Machine: "local", Pane: "p2"}}},
		{"tell codex to run the tests", []Action{{Type: ActSend, Machine: "local", Pane: "p2", Text: "Run the tests"}}},
		{"please ask   codex to go to the docs folder", []Action{{Type: ActSend, Machine: "local", Pane: "p2", Text: "Go to the docs folder"}}},
		{"tell the gemini agent to stop", []Action{{Type: ActSend, Machine: "busybox", Pane: "p1", Text: "Stop"}}},
		{"tell everyone to run the tests!", []Action{
			{Type: ActBroadcast, Machine: "local", Panes: []string{"p1", "p2", "p4"}, Text: "Run the tests"},
			{Type: ActBroadcast, Machine: "busybox", Panes: []string{"p1"}, Text: "Run the tests"},
		}},
		{"ask all agents to commit", []Action{
			{Type: ActBroadcast, Machine: "local", Panes: []string{"p1", "p2", "p4"}, Text: "Commit"},
			{Type: ActBroadcast, Machine: "busybox", Panes: []string{"p1"}, Text: "Commit"},
		}},
	}
	for _, c := range cases {
		plan, ok := FastPlan(w, c.req)
		if !ok {
			t.Errorf("%q: not matched", c.req)
			continue
		}
		if !plan.Fast {
			t.Errorf("%q: plan not marked fast", c.req)
		}
		if !reflect.DeepEqual(plan.Actions, c.want) {
			t.Errorf("%q:\n got %+v\nwant %+v", c.req, plan.Actions, c.want)
		}
		// Whatever the fast path proposes has to pass the checks a
		// model's plan does.
		for _, a := range plan.Actions {
			if err := w.Validate(&a); err != nil {
				t.Errorf("%q: %v", c.req, err)
			}
		}
	}
}

// Every request with more than one reading, or none, goes to the model.
func TestFastPlanLeavesTheRestToTheModel(t *testing.T) {
	w := fastWorld()
	for _, req := range []string{
		"",
		"   ",
		"?",
		"start 3 agents on api to fix the flaky tests",
		"show claude",                          // two claude panes
		"show api",                             // a pane and a project
		"show busybox",                         // a machine
		"show sleeper",                         // on an offline machine
		"show nobody",                          // no such pane
		"show p1",                              // p1 on two machines
		"tell codex to check its tests",        // about the agent, not to it
		"tell codex to ask them what they did", // likewise
		"tell everyone to commit their work",
		"tell codex",                   // nothing to say
		"tell codex to ",               // likewise
		"tell zsh to run the tests",    // a shell, not an agent
		"tell claude to run the tests", // two claudes
		"ask codex what it is doing",   // a question to relay, not "to"
		"hand the auth thread to codex",
		"show",
		"close",
		"tell everyone",
		"what is codex doing",
	} {
		if plan, ok := FastPlan(w, req); ok {
			t.Errorf("%q: matched without the model: %+v", req, plan)
		}
	}
}

func TestFastPlanBroadcastWithNoAgents(t *testing.T) {
	w := World{Machines: []Machine{{ID: "local", Label: "this computer", Online: true, Panes: []Pane{{ID: "p1", Name: "zsh"}}}}}
	if _, ok := FastPlan(w, "tell everyone to stop"); ok {
		t.Error("a broadcast to nobody should be the model's to explain")
	}
	if _, ok := FastPlan(World{}, "tell everyone to stop"); ok {
		t.Error("an empty world matched")
	}
}

// A send whose name splits two ways onto two agents is not guessed at.
func TestFastPlanSendAmbiguousSplit(t *testing.T) {
	w := World{Machines: []Machine{{ID: "local", Label: "this computer", Online: true, Panes: []Pane{
		{ID: "p1", Name: "codex", Agent: "codex"},
		{ID: "p2", Name: "codex to go", Agent: "claude"},
	}}}}
	if plan, ok := FastPlan(w, "tell codex to go to bed"); ok {
		t.Errorf("matched %+v", plan)
	}
}

func TestFastPlanQuestions(t *testing.T) {
	w := fastWorld()
	plan, ok := FastPlan(w, "What's waiting for me?")
	if !ok || !plan.Fast || len(plan.Actions) != 0 {
		t.Fatalf("got %+v %v", plan, ok)
	}
	for _, want := range []string{"2 agents are waiting", "✳ Fix login (claude on this computer): asks to run rm", "docs (gemini on busybox)"} {
		if !strings.Contains(plan.Reply, want) {
			t.Errorf("reply %q lacks %q", plan.Reply, want)
		}
	}
	if strings.Contains(plan.Reply, "sleeper") {
		t.Errorf("an offline machine's agent listed: %q", plan.Reply)
	}

	plan, ok = FastPlan(w, "what is working")
	if !ok || plan.Reply != "1 agent is working: codex on this computer." {
		t.Errorf("got %q %v", plan.Reply, ok)
	}

	plan, ok = FastPlan(World{}, "who is waiting")
	if !ok || plan.Reply != "No agent is waiting for you." || plan.Actions == nil {
		t.Errorf("got %+v %v", plan, ok)
	}
}
