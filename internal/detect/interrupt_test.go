package detect

import (
	"strings"
	"testing"
	"time"
)

// Stopped through conch: the screen going quiet is enough, at once, rather
// than after hook_working_stale — and it is not "done" for nobody, since
// whoever pressed Stop is there.
func TestTrackerInterruptedEndsWorkingAtOnce(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	tr.Hook(HookEvent{Event: "PreToolUse"}, t0)
	tr.Observe(obs(t0, claudeProc, false, "esc to interrupt"))
	tr.Interrupted(t0.Add(time.Second))
	// The spinner is still up: still working.
	tr.Observe(obs(t0.Add(1100*time.Millisecond), claudeProc, false, "esc to interrupt"))
	if s := tr.Status(); s.State != StateWorking {
		t.Fatalf("spinner still up: %+v", s)
	}
	tr.Observe(obs(t0.Add(1300*time.Millisecond), claudeProc, false, "Interrupted by user", "> "))
	if s := tr.Status(); s.State != StateIdle || s.Reason != "interrupted" {
		t.Fatalf("want idle at once, got %+v", s)
	}
	// A hook from the tool that was cut short is not a new turn.
	tr.Hook(HookEvent{Event: "PostToolUseFailure"}, t0.Add(1400*time.Millisecond))
	tr.Observe(obs(t0.Add(1500*time.Millisecond), claudeProc, false, "> "))
	if s := tr.Status(); s.State != StateIdle {
		t.Fatalf("tool's late hook: %+v", s)
	}
}

// The next message starts work again, and the interrupt is forgotten.
func TestTrackerInterruptForgottenByNextTurn(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0)
	tr.Interrupted(t0.Add(time.Second))
	tr.Observe(obs(t0.Add(2*time.Second), claudeProc, true, "> "))
	if s := tr.Status(); s.State != StateIdle {
		t.Fatalf("interrupted: %+v", s)
	}
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0.Add(3*time.Second))
	// Between two spinner frames the screen shows none; that is not a stop.
	tr.Observe(obs(t0.Add(3*time.Second), claudeProc, true, "> "))
	if s := tr.Status(); s.State != StateWorking {
		t.Fatalf("next turn: %+v", s)
	}
}

// An interrupt is believed for a while, not for ever: an agent that went
// back to work on its own is read as before.
func TestTrackerInterruptExpires(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	tr.Interrupted(t0)
	tr.Hook(HookEvent{Event: "PreToolUse"}, t0.Add(interruptHold+time.Second))
	tr.Observe(obs(t0.Add(interruptHold+time.Second), claudeProc, true, "> "))
	if s := tr.Status(); s.State != StateWorking {
		t.Fatalf("old interrupt still believed: %+v", s)
	}
}

// A program reporting itself at work (OSC 7501) outweighs a quiet screen.
func TestTrackerInterruptedButProgramWorking(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	tr.Hook(HookEvent{Event: "PreToolUse"}, t0)
	tr.Interrupted(t0)
	o := obs(t0.Add(100*time.Millisecond), claudeProc, true, "> ")
	o.Program = &ProgramReport{State: "working", At: t0.Add(50 * time.Millisecond)}
	tr.Observe(o)
	if s := tr.Status(); s.State != StateWorking {
		t.Fatalf("program says working: %+v", s)
	}
}

// Carried through a reload, as the rest of the tracker is.
func TestTrackerInterruptSurvivesExport(t *testing.T) {
	m := manifests(t)
	tr := NewTracker(m, "claude")
	tr.Hook(HookEvent{Event: "PreToolUse"}, t0)
	tr.Observe(obs(t0, claudeProc, true, "esc to interrupt"))
	tr.Interrupted(t0.Add(time.Second))
	back := RestoreTracker(m, tr.Export())
	back.Observe(obs(t0.Add(2*time.Second), claudeProc, true, "> "))
	if s := back.Status(); s.State != StateIdle || s.Reason != "interrupted" {
		t.Fatalf("after restore: %+v", s)
	}
	// An older state file has no interrupt: zero, nothing believed.
	old := RestoreTracker(m, TrackerState{Hint: "claude", HookState: StateWorking, HookAt: t0, Status: Status{Agent: "claude", State: StateWorking}})
	old.Observe(obs(t0.Add(2*time.Second), claudeProc, true, "> "))
	if s := old.Status(); s.State != StateWorking {
		t.Fatalf("old state: %+v", s)
	}
}

func TestManifestInterruptKeys(t *testing.T) {
	m := manifests(t)
	for agent, want := range map[string]string{"claude": "escape", "codex": "escape", "gemini": "escape", "opencode": "escape escape", "devin": "escape escape"} {
		got := m[agent].InterruptKeys()
		s := ""
		for i, k := range got {
			if i > 0 {
				s += " "
			}
			s += k
		}
		if s != want {
			t.Errorf("%s: %q, want %q", agent, s, want)
		}
	}
	var none *Manifest
	if k := none.InterruptKeys(); len(k) != 1 || k[0] != "escape" {
		t.Errorf("nil manifest: %v", k)
	}
	// The keys handed out are a copy: changing them changes no manifest.
	k := m["devin"].InterruptKeys()
	k[0] = "ctrl+c"
	if m["devin"].Interrupt[0] != "escape" {
		t.Error("InterruptKeys handed out the manifest's own slice")
	}
}

// TestShippedInterruptKeysMatchTheScreen: Stop sends a manifest's
// interrupt keys, so an agent whose own screen says which key interrupts
// it must be sent that key. Goose says "Ctrl+C to interrupt" and was sent
// Esc, which left it working.
func TestShippedInterruptKeysMatchTheScreen(t *testing.T) {
	m := manifests(t)
	if k := m["goose"].InterruptKeys(); len(k) != 1 || k[0] != "ctrl+c" {
		t.Errorf("goose: %v", k)
	}
	checked := 0
	for name, man := range m {
		for _, r := range man.Rules {
			// The pattern is a regular expression: read it as the words it
			// matches, near enough for these hints.
			hint := strings.ToLower(strings.ReplaceAll(r.Pattern, `\`, ""))
			want := ""
			switch {
			case strings.Contains(hint, "ctrl+c to interrupt"):
				want = "ctrl+c"
			case strings.Contains(hint, "esc to interrupt"):
				want = "escape"
			default:
				continue
			}
			checked++
			if k := man.InterruptKeys(); len(k) != 1 || k[0] != want {
				t.Errorf("%s: its screen says %q, and Stop sends %v", name, r.Pattern, k)
			}
		}
	}
	if checked < 2 {
		t.Fatalf("only %d rules name an interrupt key: the hints are not being read", checked)
	}
}
