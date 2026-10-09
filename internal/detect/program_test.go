package detect

import (
	"testing"
	"time"
)

// prog is a report as a program would make it.
func prog(state, kind, msg string, at time.Time) *ProgramReport {
	return &ProgramReport{State: state, Kind: kind, App: "claude-code", Msg: msg, At: at}
}

// A program that reports its own state is not guessing, so it is taken
// over conch's screen rules — the ones that had to be rewritten each time
// an agent's spinner changed.
func TestProgramReportBeatsTheScreen(t *testing.T) {
	tr := NewTracker(manifests(t), "claude")
	// The screen says nothing; the program says it is working.
	tr.Observe(Observation{Now: t0, Process: claudeProc, Screen: screen("> ", "  ? for shortcuts"),
		Program: prog(programWorking, "", "", t0)})
	st := tr.Status()
	if st.State != StateWorking || st.Source != "program" {
		t.Fatalf("working: %+v", st)
	}
	if ex := tr.Explain(); ex.ProgramState != "working" || ex.ProgramApp != "claude-code" {
		t.Errorf("explain does not say where it came from: %+v", ex)
	}

	// Blocked comes first, even over a screen rule that says working:
	// the program knows it is waiting and the screen only has a spinner.
	tr.Observe(Observation{Now: t0.Add(time.Second), Process: claudeProc,
		Screen:  screen("✻ Thinking… (12s · esc to interrupt)", "> "),
		Program: prog(programBlocked, "permission", "Allow npm install?", t0.Add(time.Second))})
	st = tr.Status()
	if st.State != StateBlocked || st.Source != "program" || st.Reason != "program:permission" {
		t.Fatalf("blocked: %+v", st)
	}
	if st.Message != "Allow npm install?" {
		t.Errorf("the program's own words were dropped: %q", st.Message)
	}

	// A report conch cannot use is ignored, and the screen decides again.
	tr.Observe(Observation{Now: t0.Add(2 * time.Second), Process: claudeProc,
		Screen:  screen("✻ Thinking… (12s · esc to interrupt)", "> "),
		Program: &ProgramReport{State: "pondering"}})
	if st := tr.Status(); st.State != StateWorking || st.Source != "screen" {
		t.Fatalf("after an unusable report: %+v", st)
	}
}

// A hook is the same program speaking, with more to say — the message,
// whether the work failed, which session — so it still comes first where
// an agent has them. This is what keeps Claude Code exactly as it was.
func TestHooksStillBeatAPlainReport(t *testing.T) {
	tr := NewTracker(manifests(t), "claude")
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0)
	tr.Observe(Observation{Now: t0, Process: claudeProc, Screen: screen("> "),
		Program: prog(programIdle, "", "", t0)})
	if st := tr.Status(); st.State != StateWorking || st.Source != "hook" {
		t.Fatalf("a hook lost to a plain report: %+v", st)
	}
	// But a program saying it is blocked outranks a hook that says it is
	// working: one of them is waiting for a person.
	tr.Observe(Observation{Now: t0.Add(time.Second), Process: claudeProc, Screen: screen("> "),
		Program: prog(programBlocked, "question", "", t0.Add(time.Second))})
	if st := tr.Status(); st.State != StateBlocked || st.Source != "program" {
		t.Fatalf("a blocked program lost to a working hook: %+v", st)
	}
}

// A program that says it is working is evidence it is working, so a hook
// that said so is not retired for going quiet.
func TestAWorkingReportKeepsAHookAlive(t *testing.T) {
	tr := NewTracker(manifests(t), "claude")
	tr.Hook(HookEvent{Event: "UserPromptSubmit"}, t0)
	late := t0.Add(time.Hour) // far past claude's hook_working_stale
	tr.Observe(Observation{Now: late, Process: claudeProc, Screen: screen("> "),
		Program: prog(programWorking, "", "", late)})
	if st := tr.Status(); st.State != StateWorking {
		t.Fatalf("a working program was called idle: %+v", st)
	}
	// Without the report, the same stale hook gives way, as before.
	quiet := NewTracker(manifests(t), "claude")
	quiet.Hook(HookEvent{Event: "UserPromptSubmit"}, t0)
	quiet.Observe(Observation{Now: late, Process: claudeProc, Screen: screen("> ")})
	if st := quiet.Status(); st.State == StateWorking {
		t.Fatalf("a stale hook with nothing to back it still says working: %+v", st)
	}
}

// done and error are conch's "idle, and nobody has looked since" — said
// by the program rather than worked out from a transition conch has to
// have caught. error also marks the work failed.
func TestProgramDoneAndError(t *testing.T) {
	for _, c := range []struct {
		state  string
		failed bool
	}{{programDone, false}, {programError, true}} {
		tr := NewTracker(manifests(t), "claude")
		// Straight to done without conch ever seeing it work.
		tr.Observe(Observation{Now: t0, Process: claudeProc, Screen: screen("> "),
			Program: prog(c.state, "", "all finished", t0)})
		st := tr.Status()
		if st.State != StateDone {
			t.Errorf("%s: state %q, wanted done", c.state, st.State)
		}
		if st.Failed != c.failed {
			t.Errorf("%s: failed=%v, wanted %v", c.state, st.Failed, c.failed)
		}
		if st.Message != "all finished" {
			t.Errorf("%s: message %q", c.state, st.Message)
		}
		// Somebody looking at it clears done, as it does for every agent.
		if !tr.MarkSeen(t0.Add(time.Second)) {
			t.Errorf("%s: looking at it changed nothing", c.state)
		}
		if st := tr.Status(); st.State != StateIdle {
			t.Errorf("%s: looking at it left %q", c.state, st.State)
		}
		// And it does not come back on the next look at the same report.
		tr.Observe(Observation{Now: t0.Add(2 * time.Second), Process: claudeProc, Screen: screen("> "),
			Program: prog(c.state, "", "all finished", t0)})
		if st := tr.Status(); st.State != StateIdle {
			t.Errorf("%s: done came back after it was seen: %q", c.state, st.State)
		}
	}
}

// A program in a pane conch does not take for an agent reports into the
// void: the tier is what decides whether there is an agent, not the
// protocol. (An `app` naming the agent could decide it one day; it does
// not today, and the docs say so rather than half-doing it.)
func TestAReportDoesNotMakeAShellAnAgent(t *testing.T) {
	tr := NewTracker(manifests(t), "")
	tr.Observe(Observation{Now: t0, Process: shellProc, Screen: screen("$ "),
		Program: prog(programWorking, "", "", t0)})
	if st := tr.Status(); st.Agent != "" || st.State != "" {
		t.Fatalf("a shell became an agent: %+v", st)
	}
}

// A program speaks when something changes, so the same record is there at
// every look afterwards. "Done" is raised once for it, not at every tick
// — and not again after a reload, which would nag about work somebody
// cleared before it.
func TestAFinishedReportIsRaisedOnce(t *testing.T) {
	tr := NewTracker(manifests(t), "claude")
	report := prog(programDone, "", "finished", t0)
	tr.Observe(Observation{Now: t0, Process: claudeProc, Screen: screen("> "), Program: report})
	if st := tr.Status(); st.State != StateDone {
		t.Fatalf("the first look: %+v", st)
	}
	tr.MarkSeen(t0.Add(time.Second))
	for i := range 5 {
		tr.Observe(Observation{Now: t0.Add(time.Duration(i+2) * time.Second), Process: claudeProc,
			Screen: screen("> "), Program: report})
		if st := tr.Status(); st.State != StateIdle {
			t.Fatalf("look %d after it was seen: %+v", i, st)
		}
	}
	// Through a reload, with the same record the pane carried over.
	back := RestoreTracker(manifests(t), tr.Export())
	back.Observe(Observation{Now: t0.Add(time.Minute), Process: claudeProc, Screen: screen("> "), Program: report})
	if st := back.Status(); st.State != StateIdle {
		t.Fatalf("after a reload: %+v", st)
	}
	// A new report of its own does raise it again.
	back.Observe(Observation{Now: t0.Add(2 * time.Minute), Process: claudeProc, Screen: screen("> "),
		Program: prog(programDone, "", "finished again", t0.Add(2*time.Minute))})
	if st := back.Status(); st.State != StateDone {
		t.Fatalf("a new report did not raise done: %+v", st)
	}
}
