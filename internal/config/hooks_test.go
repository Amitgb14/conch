package config

import (
	"strings"
	"testing"
	"time"
)

func TestHookEventsRead(t *testing.T) {
	cfg := writeConfig(t, `
[[actions]]
name = "Setup"
run = "make setup"
on = ["worktree.created"]

[[actions]]
name = "Tell the channel"
run = "notify"
on = ["agent.done", "Agent.Waiting", "branch"]
timeout = 30

[[actions]]
name = "Run tests"
run = "go test ./..."
on = ["branch"]

[[actions]]
name = "Everywhere"
run = "true"
`)
	got := cfg.ActionsFor(EventWorktreeCreated)
	if len(got) != 1 || got[0].Name != "Setup" {
		t.Errorf("worktree.created runs %+v", got)
	}
	// Event names are matched as places are, whatever the case.
	if got := cfg.ActionsFor(EventAgentWaiting); len(got) != 1 || got[0].Name != "Tell the channel" {
		t.Errorf("agent.waiting runs %+v", got)
	}
	// An action without an event in on is never a hook: existing configs
	// keep working as they did.
	for _, ev := range HookEvents {
		for _, a := range cfg.ActionsFor(ev) {
			if a.Name == "Run tests" || a.Name == "Everywhere" {
				t.Errorf("%s runs %s, which names no event", ev, a.Name)
			}
		}
	}
	if cfg.Actions[1].HookTimeout() != 30*time.Second {
		t.Errorf("timeout = %v", cfg.Actions[1].HookTimeout())
	}
	if cfg.Actions[0].HookTimeout() != DefaultHookTimeout {
		t.Errorf("default timeout = %v", cfg.Actions[0].HookTimeout())
	}
}

func TestHookOnlyActionsStayOffMenus(t *testing.T) {
	cfg := writeConfig(t, `
[[actions]]
name = "Setup"
run = "make setup"
on = ["worktree.created"]

[[actions]]
name = "Both"
run = "x"
on = ["pr.merged", "branch"]

[[actions]]
name = "Menus"
run = "y"
`)
	for _, place := range ActionPlaces {
		for _, a := range cfg.ActionsOn(place) {
			if a.Name == "Setup" {
				t.Errorf("a hook naming no place is offered on %s", place)
			}
		}
	}
	names := func(place string) string {
		var out []string
		for _, a := range cfg.ActionsOn(place) {
			out = append(out, a.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names(ActionOnBranch); got != "Both,Menus" {
		t.Errorf("branch menu: %s", got)
	}
	if got := names(ActionOnPane); got != "Menus" {
		t.Errorf("pane menu: %s", got)
	}
}

func TestHookProblems(t *testing.T) {
	cfg := writeConfig(t, `
[[actions]]
name = "Typo"
run = "x"
on = ["agent.finished", "branch"]

[[actions]]
name = "Negative"
run = "x"
on = ["pr.opened"]
timeout = -1
`)
	got := strings.Join(cfg.ActionProblems(), "\n")
	if !strings.Contains(got, "Typo runs on agent.finished") || !strings.Contains(got, "agent.done") {
		t.Errorf("an unknown event is not said, or not with the right ones: %q", got)
	}
	if !strings.Contains(got, "Negative") || !strings.Contains(got, "timeout") {
		t.Errorf("a negative timeout is not said: %q", got)
	}
	// The unknown event is said and not silently a place either.
	if strings.Contains(got, "offered on agent.finished") {
		t.Errorf("an event taken for a place: %q", got)
	}
}

func TestIsHookEvent(t *testing.T) {
	for _, ev := range HookEvents {
		if !IsHookEvent(ev) || !IsHookEvent(strings.ToUpper(ev)) {
			t.Errorf("%s not an event", ev)
		}
	}
	for _, s := range []string{"", "branch", "agent", "agent.", ".done", "agent.finished"} {
		if IsHookEvent(s) {
			t.Errorf("%q taken for an event", s)
		}
	}
}
