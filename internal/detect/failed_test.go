package detect

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFailedRequest(t *testing.T) {
	tr := NewTracker(manifests(t), "claude")
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0)
	tr.Observe(obs(t0, claudeProc, false, "esc to interrupt"))
	tr.Hook(HookEvent{Event: "StopFailure"}, t0.Add(time.Second))
	changed := tr.Observe(obs(t0.Add(time.Second), claudeProc, false, "> "))
	s := tr.Status()
	if !changed || s.State != StateDone || !s.Failed || s.Message != FailedMessage {
		t.Fatalf("after StopFailure: %+v", s)
	}
	// Seen: idle, still marked failed until the next request.
	tr.UserInput()
	tr.Observe(obs(t0.Add(2*time.Second), claudeProc, true, "> "))
	if s := tr.Status(); s.State != StateIdle || !s.Failed {
		t.Fatalf("seen failure: %+v", s)
	}
	// Survives a reload.
	b, _ := json.Marshal(tr.Export())
	var st TrackerState
	json.Unmarshal(b, &st)
	back := RestoreTracker(manifests(t), st)
	back.Observe(obs(t0.Add(3*time.Second), claudeProc, true, "> "))
	if s := back.Status(); !s.Failed || s.Message != FailedMessage {
		t.Fatalf("restored: %+v", s)
	}
	// The next request clears it.
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0.Add(4*time.Second))
	tr.Observe(obs(t0.Add(4*time.Second), claudeProc, true, "esc to interrupt"))
	if s := tr.Status(); s.Failed || s.Message != "" || s.State != StateWorking {
		t.Fatalf("next request: %+v", s)
	}
	tr.Hook(HookEvent{Event: "Stop"}, t0.Add(5*time.Second))
	tr.Observe(obs(t0.Add(5*time.Second), claudeProc, true, "> "))
	if s := tr.Status(); s.Failed {
		t.Fatalf("a normal stop is not a failure: %+v", s)
	}
}

func TestFailedKeepsAgentMessageAndClearsOnAgentChange(t *testing.T) {
	tr := NewTracker(manifests(t), "claude")
	tr.Hook(HookEvent{Event: "session.error", Message: "provider auth expired"}, t0)
	tr.Observe(obs(t0, claudeProc, true, "> "))
	if s := tr.Status(); !s.Failed || s.Message != "provider auth expired" {
		t.Fatalf("own message: %+v", s)
	}
	// Stale working after a failure resets it too.
	tr.Hook(HookEvent{Event: "PreToolUse"}, t0.Add(time.Second))
	tr.Observe(obs(t0.Add(time.Minute), claudeProc, true, "> "))
	if s := tr.Status(); s.Failed {
		t.Fatalf("after a later hook: %+v", s)
	}
	// The agent going away forgets a failure.
	tr2 := NewTracker(manifests(t), "")
	tr2.Hook(HookEvent{Event: "StopFailure"}, t0)
	tr2.Observe(obs(t0, claudeProc, true, "> "))
	tr2.Observe(obs(t0.Add(time.Second), Process{PID: 11, Name: "zsh", Args: []string{"-zsh"}}, true, "$ "))
	tr2.Observe(obs(t0.Add(2*time.Second), claudeProc, true, "> "))
	if s := tr2.Status(); s.Failed {
		t.Fatalf("after the agent changed: %+v", s)
	}
}
