package config

import (
	"strings"
	"time"
)

// Hooks are actions run on events rather than chosen from a menu: the
// project's setup when a worktree is made, a message when an agent
// finishes, a cache warmed when a pull request merges. The server runs
// them (internal/server/hooks.go), since it is the one that sees the
// events and is there with no TUI open. An action is a hook only once its
// `on` names an event, so a config written before hooks keeps doing
// exactly what it did.
//
//	[[actions]]
//	name    = "Post to the channel"
//	run     = "curl -d \"$CONCH_BRANCH is done\" https://example.com/hook"
//	on      = ["agent.done"]
//	timeout = 30

// The events a hook can be run on. They are a promise to somebody's
// config file: renaming one leaves their hook silently never running.
const (
	EventWorktreeCreated = "worktree.created"
	EventAgentDone       = "agent.done"
	EventAgentWaiting    = "agent.waiting"
	EventPROpened        = "pr.opened"
	EventPRMerged        = "pr.merged"
	EventTaskFailed      = "task.failed"
)

// HookEvents is every event `on` takes.
var HookEvents = []string{EventWorktreeCreated, EventAgentDone, EventAgentWaiting, EventPROpened, EventPRMerged, EventTaskFailed}

// DefaultHookTimeout is how long a hook runs before it is stopped when its
// action does not say: long enough for an install, short enough that a
// hook stuck on a prompt nobody will answer does not stay for the day.
const DefaultHookTimeout = 5 * time.Minute

// IsHookEvent reports whether s names an event, whatever its case.
func IsHookEvent(s string) bool {
	for _, ev := range HookEvents {
		if strings.EqualFold(strings.TrimSpace(s), ev) {
			return true
		}
	}
	return false
}

// isEventWord is a word in `on` written as an event is — with a dot, which
// no place has — so a misspelt event is said as one rather than as a place.
func isEventWord(s string) bool { return strings.Contains(s, ".") }

// Events is the known events the action runs on, as written in HookEvents.
func (a Action) Events() []string {
	var out []string
	for _, p := range a.On {
		for _, ev := range HookEvents {
			if strings.EqualFold(strings.TrimSpace(p), ev) {
				out = append(out, ev)
			}
		}
	}
	return out
}

// RunsOn reports whether the action is a hook for event.
func (a Action) RunsOn(event string) bool {
	for _, ev := range a.Events() {
		if ev == event {
			return true
		}
	}
	return false
}

// HookTimeout is how long one run of the action as a hook may take.
func (a Action) HookTimeout() time.Duration {
	if a.Timeout <= 0 {
		return DefaultHookTimeout
	}
	return time.Duration(a.Timeout) * time.Second
}

// ActionsFor is the actions run on event, in the order written; ones
// missing a name or a command are left out, as from the menus.
func (c Config) ActionsFor(event string) []Action {
	var out []Action
	for _, a := range c.Actions {
		if a.Named() && a.RunsOn(event) {
			out = append(out, a)
		}
	}
	return out
}
