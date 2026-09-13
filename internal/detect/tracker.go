package detect

import (
	"time"
)

// Status is what conch believes an agent pane is doing.
type Status struct {
	Agent     string    `json:"agent"`            // "" when no agent runs in the pane
	State     string    `json:"state,omitempty"`  // idle, working, blocked, done
	Source    string    `json:"source,omitempty"` // hook, screen
	Reason    string    `json:"reason,omitempty"` // rule or hook event that decided
	Message   string    `json:"message,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Since     time.Time `json:"since"`
}

// HookEvent is a lifecycle event reported by an agent integration.
type HookEvent struct {
	Agent            string `json:"agent"`
	Event            string `json:"event"`
	NotificationType string `json:"notification_type,omitempty"`
	Message          string `json:"message,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
}

// Observation is one sample of a pane.
type Observation struct {
	Now        time.Time
	Process    Process
	ProcessErr error
	Screen     []string // visible screen as plain text
	Title      string   // terminal title
	Watched    bool     // a client is looking at the pane
}

// Explanation shows how the current status was reached.
type Explanation struct {
	Status      Status   `json:"status"`
	Hint        string   `json:"hint,omitempty"`
	Process     Process  `json:"process"`
	ProcessErr  string   `json:"process_error,omitempty"`
	Manifest    string   `json:"manifest,omitempty"`
	ScreenRule  string   `json:"screen_rule,omitempty"`
	HookState   string   `json:"hook_state,omitempty"`
	HookReason  string   `json:"hook_reason,omitempty"`
	HookAge     string   `json:"hook_age,omitempty"`
	Seen        bool     `json:"seen"`
	ScreenLines []string `json:"screen_tail,omitempty"`
}

// Tracker keeps the agent status of one pane. It is not safe for concurrent
// use; the owner serialises calls.
type Tracker struct {
	manifests map[string]*Manifest
	hint      string // agent the pane was launched as, if any

	agent *Manifest
	hook  struct {
		state, reason, message string
		at                     time.Time
	}
	sessionID     string
	screenWorking time.Time // last time a working rule matched
	base          string    // state before the done/seen overlay
	seen          bool
	status        Status
	last          Explanation
}

// NewTracker creates a tracker. hint names the agent the pane was launched
// as, which identifies it when a wrapper process hides the agent binary.
func NewTracker(manifests map[string]*Manifest, hint string) *Tracker {
	return &Tracker{manifests: manifests, hint: hint, seen: true}
}

// hookState maps a lifecycle event to a state; ok is false for events that
// say nothing about the state.
func hookState(ev HookEvent) (state string, ok bool) {
	switch ev.Event {
	case "SessionStart":
		return StateIdle, true
	// Subagent and compaction events are deliberately absent: Claude Code
	// runs background subagents (e.g. naming the session) after Stop, and
	// real work around them already reports tool events.
	case "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionDenied":
		return StateWorking, true
	case "PermissionRequest":
		return StateBlocked, true
	case "Notification":
		switch ev.NotificationType {
		case "permission_prompt", "elicitation_dialog", "ToolPermission":
			return StateBlocked, true
		case "idle_prompt":
			return StateIdle, true
		}
		return "", false
	case "Stop", "StopFailure":
		return StateIdle, true

	// Gemini CLI hooks.
	case "BeforeAgent", "BeforeTool", "AfterTool", "BeforeModel", "PreCompress":
		return StateWorking, true
	case "AfterAgent":
		return StateIdle, true

	// OpenCode plugin events.
	case "session.busy", "session.retry", "permission.replied", "question.replied", "question.rejected":
		return StateWorking, true
	case "session.idle", "session.error":
		return StateIdle, true
	case "permission.asked", "question.asked":
		return StateBlocked, true
	case "SessionEnd":
		return "", true // the session is over; let the screen decide
	}
	return "", false
}

// Hook records a lifecycle event.
func (t *Tracker) Hook(ev HookEvent, now time.Time) {
	if ev.SessionID != "" {
		t.sessionID = ev.SessionID
	}
	state, ok := hookState(ev)
	if !ok {
		return
	}
	t.hook.state, t.hook.reason, t.hook.message, t.hook.at = state, "hook:"+ev.Event, ev.Message, now
	if ev.NotificationType != "" {
		t.hook.reason += ":" + ev.NotificationType
	}
}

// UserInput notes that someone typed into the pane, which also means they
// have seen it. A hook-reported blocked state is dropped: whatever was asked
// has likely been answered, and a refusal fires no hook to say so.
func (t *Tracker) UserInput() {
	t.seen = true
	if t.hook.state == StateBlocked {
		t.hook.state = ""
	}
}

// MarkSeen clears the done overlay. It reports whether the status changed.
func (t *Tracker) MarkSeen(now time.Time) bool {
	if t.seen {
		return false
	}
	t.seen = true
	if t.status.State == StateDone {
		t.status.State = StateIdle
		t.status.Since = now
		return true
	}
	return false
}

// Status returns the current status.
func (t *Tracker) Status() Status { return t.status }

// Explain returns details of the last evaluation.
func (t *Tracker) Explain() Explanation { return t.last }

// Observe evaluates a sample and reports whether the status changed.
func (t *Tracker) Observe(o Observation) bool {
	ex := Explanation{Hint: t.hint, Process: o.Process}
	if o.ProcessErr != nil {
		ex.ProcessErr = o.ProcessErr.Error()
	}

	m := MatchAny(t.manifests, o.Process)
	if m == nil && t.hint != "" && (o.ProcessErr != nil || !IsShell(o.Process)) {
		m = t.manifests[t.hint]
	}
	// Forget hook state when a detected agent goes away or changes. Going
	// from nothing to an agent keeps it: hooks can arrive before the first
	// sample sees the process.
	if t.agent != nil && m != t.agent {
		t.hook.state, t.sessionID, t.screenWorking = "", "", time.Time{}
	}
	t.agent = m

	next := Status{}
	base := ""
	if m != nil {
		ex.Manifest = m.Agent
		rule := m.Match(o.Screen, o.Title)
		if rule != nil {
			ex.ScreenRule = rule.Name
			if rule.State == StateWorking {
				t.screenWorking = o.Now
			}
		}
		next = Status{Agent: m.Agent, SessionID: t.sessionID}
		switch {
		case rule != nil && rule.State == StateBlocked:
			next.State, next.Source, next.Reason = StateBlocked, "screen", "rule:"+rule.Name
		case t.hook.state != "":
			next.State, next.Source, next.Reason, next.Message = t.hook.state, "hook", t.hook.reason, t.hook.message
			stale := m.HookWorkingStale.Duration
			lastEvidence := t.hook.at
			if t.screenWorking.After(lastEvidence) {
				lastEvidence = t.screenWorking
			}
			if next.State == StateWorking && stale > 0 && (rule == nil || rule.State != StateWorking) &&
				o.Now.Sub(lastEvidence) > stale {
				next.State, next.Source, next.Reason, next.Message = StateIdle, "screen", "hook_working_stale", ""
			}
		case rule != nil:
			next.State, next.Source, next.Reason = rule.State, "screen", "rule:"+rule.Name
		default:
			next.State, next.Source, next.Reason = StateIdle, "screen", "default_idle"
		}
		ex.HookState, ex.HookReason = t.hook.state, t.hook.reason
		if !t.hook.at.IsZero() {
			ex.HookAge = o.Now.Sub(t.hook.at).Round(time.Second).String()
		}
		ex.ScreenLines = tail(o.Screen, m.ScreenLines)
		base = next.State
	}

	// Done: the agent finished working while nobody was watching. Leaving
	// blocked for idle is not "done": someone answered or dismissed it.
	switch {
	case base == StateIdle && t.base == StateWorking && !o.Watched:
		t.seen = false
	case base != StateIdle:
		t.seen = true
	}
	t.base = base
	if base == StateIdle && !t.seen {
		next.State = StateDone
	}

	changed := next.Agent != t.status.Agent || next.State != t.status.State ||
		next.Message != t.status.Message || next.SessionID != t.status.SessionID
	if next.State != t.status.State || next.Agent != t.status.Agent {
		next.Since = o.Now
	} else {
		next.Since = t.status.Since
	}
	// Source and reason alone don't warrant an event, but keep them fresh.
	t.status = next
	ex.Status, ex.Seen = next, t.seen
	t.last = ex
	return changed
}

func tail(lines []string, n int) []string {
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	return lines[max(end-n, 0):end]
}

// TrackerState is what a tracker carries across a server reload: what hooks
// last said, so an agent doesn't briefly look idle until its next event.
type TrackerState struct {
	Hint          string    `json:"hint,omitempty"`
	HookState     string    `json:"hook_state,omitempty"`
	HookReason    string    `json:"hook_reason,omitempty"`
	HookMessage   string    `json:"hook_message,omitempty"`
	HookAt        time.Time `json:"hook_at,omitempty"`
	SessionID     string    `json:"session_id,omitempty"`
	ScreenWorking time.Time `json:"screen_working,omitempty"`
	Base          string    `json:"base,omitempty"`
	Seen          bool      `json:"seen"`
	Status        Status    `json:"status"`
}

// Export returns the tracker's state.
func (t *Tracker) Export() TrackerState {
	return TrackerState{Hint: t.hint, HookState: t.hook.state, HookReason: t.hook.reason, HookMessage: t.hook.message,
		HookAt: t.hook.at, SessionID: t.sessionID, ScreenWorking: t.screenWorking, Base: t.base, Seen: t.seen, Status: t.status}
}

// RestoreTracker rebuilds a tracker from exported state.
func RestoreTracker(manifests map[string]*Manifest, st TrackerState) *Tracker {
	t := NewTracker(manifests, st.Hint)
	t.hook.state, t.hook.reason, t.hook.message, t.hook.at = st.HookState, st.HookReason, st.HookMessage, st.HookAt
	t.sessionID, t.screenWorking, t.base, t.seen, t.status = st.SessionID, st.ScreenWorking, st.Base, st.Seen, st.Status
	if st.Status.Agent != "" {
		t.agent = manifests[st.Status.Agent]
	}
	return t
}
