package brain

import (
	"strings"
	"testing"
)

// shareWorld has two agents and a terminal on one machine.
func shareWorld() World {
	w := world()
	w.Machines[0].Panes = []Pane{
		{ID: "p1", Name: "fix login", Agent: "claude", State: "blocked"},
		{ID: "p2", Name: "codex", Agent: "codex", State: "idle"},
		{ID: "p3", Name: "zsh"},
	}
	return w
}

func TestValidateShareAndBroadcast(t *testing.T) {
	w := shareWorld()
	bad := []struct {
		a    Action
		want string
	}{
		{Action{Type: ActShare, Machine: "local", Pane: "p9"}, "unknown pane"},
		{Action{Type: ActShare, Machine: "local", Pane: "p3"}, "a terminal, not an agent"},
		{Action{Type: ActShare, Machine: "local", Pane: "p1", Agent: "gemini"}, "not installed"},
		{Action{Type: ActShare, Machine: "local", Pane: "p1", ToPane: "p9"}, "unknown pane"},
		{Action{Type: ActShare, Machine: "local", Pane: "p1", ToPane: "p3"}, "a terminal, not an agent"},
		{Action{Type: ActShare, Machine: "local", Pane: "p1", ToPane: "p1"}, "to itself"},
		{Action{Type: ActShare, Machine: "gpu", Pane: "p1"}, "offline"},
		{Action{Type: ActBroadcast, Machine: "local", Panes: []string{"p1"}}, "nothing to send"},
		{Action{Type: ActBroadcast, Machine: "local", Text: "go"}, "no agents to send to"},
		{Action{Type: ActBroadcast, Machine: "local", Panes: []string{"p9"}, Text: "go"}, "unknown pane"},
		{Action{Type: ActBroadcast, Machine: "local", Panes: []string{"p3"}, Text: "go"}, "a terminal, not an agent"},
		{Action{Type: ActBroadcast, Machine: "local", Panes: []string{"p1", "p1"}, Text: "go"}, "listed twice"},
	}
	for _, c := range bad {
		if err := w.Validate(&c.a); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: got %v, want %q", c.a, err, c.want)
		}
	}

	// A share with no agent takes the default; one into a running agent
	// takes that pane's agent, whether or not it is installed by name.
	fresh := Action{Type: ActShare, Machine: "local", Pane: "p1"}
	if err := w.Validate(&fresh); err != nil || fresh.Agent != "claude" {
		t.Fatalf("default agent: %v %+v", err, fresh)
	}
	into := Action{Type: ActShare, Machine: "local", Pane: "p1", Agent: "gemini", ToPane: "p2"}
	if err := w.Validate(&into); err != nil || into.Agent != "codex" {
		t.Fatalf("into a running agent: %v %+v", err, into)
	}
	ok := Action{Type: ActBroadcast, Machine: "local", Panes: []string{"p1", "p2"}, Text: "run the tests"}
	if err := w.Validate(&ok); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
}

func TestDescribeShareAndBroadcast(t *testing.T) {
	w := shareWorld()
	for _, c := range []struct {
		a    Action
		want string
	}{
		{Action{Type: ActShare, Machine: "local", Pane: "p1", Agent: "codex"},
			"Hand the conversation of fix login on this computer to codex (a new pane)"},
		{Action{Type: ActShare, Machine: "local", Pane: "p1", Agent: "codex", ToPane: "p2"},
			"Hand the conversation of fix login on this computer to codex"},
		{Action{Type: ActBroadcast, Machine: "local", Panes: []string{"p1", "p2"}, Text: "run the tests"},
			"Send to 2 agents on this computer (fix login, codex): run the tests"},
		{Action{Type: ActBroadcast, Machine: "nowhere", Panes: []string{"p1"}, Text: "hi"},
			"Send to 1 agents on nowhere (p1): hi"},
	} {
		if got := w.Describe(c.a); got != c.want {
			t.Errorf("describe %s:\n got %q\nwant %q", c.a.Type, got, c.want)
		}
	}
}

func TestPlanSchemaOffersTheNewActions(t *testing.T) {
	props := planSchema["properties"].(map[string]any)["actions"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	types := props["type"].(map[string]any)["enum"].([]string)
	for _, want := range []string{ActShare, ActBroadcast} {
		found := false
		for _, t := range types {
			found = found || t == want
		}
		if !found {
			t.Fatalf("schema lacks %q: %v", want, types)
		}
	}
	for _, want := range []string{"to_pane", "panes"} {
		if _, ok := props[want]; !ok {
			t.Fatalf("schema lacks field %q", want)
		}
	}
	for _, want := range []string{"- share:", "- broadcast:"} {
		if !strings.Contains(planSystem, want) {
			t.Fatalf("the model is never told about %q", want)
		}
	}
}
