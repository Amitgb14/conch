package main

import (
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestReportSubagents: Claude's subagent hooks reach the server naming the
// subagent and its task, and the other hooks a subagent fires — which name
// it too — do not pass that on.
func TestReportSubagents(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, "")
	t.Setenv("CONCH_SOCKET", srv.sock)
	t.Setenv("CONCH_PANE_ID", "p7")

	last := func() proto.AgentReportParams {
		t.Helper()
		var p proto.AgentReportParams
		if !srv.params(t, proto.MethodAgentReport, &p) {
			t.Fatal("nothing reported")
		}
		return p
	}

	a4Capture(t, `{"hook_event_name":"SubagentStart","session_id":"s1","agent_id":"a1","agent_type":"Explore","task_description":"Study the API","transcript_path":"/t.jsonl"}`,
		func() { runReport([]string{"claude-hook"}) })
	if p := last(); p.Event != "SubagentStart" || p.AgentID != "a1" || p.AgentType != "Explore" ||
		p.TaskDescription != "Study the API" || p.SessionID != "s1" || p.TranscriptPath != "/t.jsonl" {
		t.Fatalf("start: %+v", p)
	}

	// An older Claude says no task.
	a4Capture(t, `{"hook_event_name":"SubagentStart","agent_id":"a2","agent_type":"general-purpose"}`,
		func() { runReport([]string{"claude-hook"}) })
	if p := last(); p.AgentID != "a2" || p.TaskDescription != "" {
		t.Fatalf("start without a task: %+v", p)
	}

	a4Capture(t, `{"hook_event_name":"SubagentStop","agent_id":"a1","agent_type":"Explore","last_assistant_message":"done"}`,
		func() { runReport([]string{"claude-hook"}) })
	if p := last(); p.Event != "SubagentStop" || p.AgentID != "a1" || p.Message != "" {
		t.Fatalf("stop: %+v", p)
	}

	a4Capture(t, `{"hook_event_name":"PreToolUse","agent_id":"a1","agent_type":"Explore"}`,
		func() { runReport([]string{"claude-hook"}) })
	if p := last(); p.Event != "PreToolUse" || p.AgentID != "" || p.AgentType != "" {
		t.Fatalf("a subagent's tool use: %+v", p)
	}
}

// TestReportArtifactTool: the Artifact tool's PostToolUse names the tool,
// so the server reads what was published; other tools, and the Artifact
// tool's other hooks, do not.
func TestReportArtifactTool(t *testing.T) {
	a4Env(t)
	srv := startA4Server(t, "")
	t.Setenv("CONCH_SOCKET", srv.sock)
	t.Setenv("CONCH_PANE_ID", "p7")

	last := func() proto.AgentReportParams {
		t.Helper()
		var p proto.AgentReportParams
		if !srv.params(t, proto.MethodAgentReport, &p) {
			t.Fatal("nothing reported")
		}
		return p
	}
	a4Capture(t, `{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Artifact","tool_input":{"title":"Mock"}}`,
		func() { runReport([]string{"claude-hook"}) })
	if p := last(); p.Event != "PostToolUse" || p.Tool != "Artifact" || p.SessionID != "s1" {
		t.Fatalf("artifact: %+v", p)
	}
	a4Capture(t, `{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Bash"}`,
		func() { runReport([]string{"claude-hook"}) })
	if p := last(); p.Tool != "" {
		t.Fatalf("bash named its tool: %+v", p)
	}
	a4Capture(t, `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Artifact"}`,
		func() { runReport([]string{"claude-hook"}) })
	if p := last(); p.Event != "PreToolUse" || p.Tool != "" {
		t.Fatalf("before the publish: %+v", p)
	}
	// Gemini's hooks never name it, whatever the tool is called.
	a4Capture(t, `{"hook_event_name":"PostToolUse","session_id":"s1","tool_name":"Artifact"}`,
		func() { runReport([]string{"gemini-hook"}) })
	if p := last(); p.Agent != "gemini" || p.Tool != "" {
		t.Fatalf("gemini: %+v", p)
	}
}
