package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/phone"
	"github.com/Amitgb14/conch/internal/proto"
)

// answerServer is a fake server with one pane whose agent and screen the
// test sets.
func answerServer(t *testing.T, state string, screen []string) *a4Server {
	t.Helper()
	a4Env(t)
	srv := startA4Server(t, config.SocketPath())
	since := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneList:
			return proto.PaneList{Panes: []proto.PaneInfo{
				{ID: "p1", Name: "zsh", State: proto.PaneRunning},
				{ID: "p3", Name: "codex", State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "codex", State: state, Since: since}},
			}}, nil
		case proto.MethodPaneRead:
			return proto.PaneReadResult{Lines: screen}, nil
		}
		return nil, nil
	})
	return srv
}

var permissionScreen = []string{
	"• Proposed change to internal/server/watch.go",
	"╭───────────────────────────────────────────╮",
	"│ Allow command?                            │",
	"│   go test -race ./internal/server         │",
	"│                                           │",
	"│ › 1. Yes                                  │",
	"│   2. Yes, and don't ask again for go test │",
	"│   3. No, tell codex what to do            │",
	"╰───────────────────────────────────────────╯",
}

func TestAgentQuestionReadsTheMenu(t *testing.T) {
	answerServer(t, proto.AgentBlocked, permissionScreen)
	var err error
	out, _ := a4Capture(t, "", func() { err = runAgent([]string{"question", "p3"}) })
	if err != nil {
		t.Fatal(err)
	}
	var q phone.Question
	if err := json.Unmarshal([]byte(out), &q); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if len(q.Choices) != 3 || q.Choices[0].Label != "Yes" || !q.Choices[0].Default || q.Choices[2].Choice != "3" {
		t.Fatalf("choices %+v", q.Choices)
	}
	if !strings.Contains(q.Text, "Allow command?") || !strings.HasPrefix(q.ID, "q_") {
		t.Fatalf("question %+v", q)
	}
}

// An agent that is not waiting has no question: null, and no error, since
// the app asks again and again.
func TestAgentQuestionWhenNotWaiting(t *testing.T) {
	answerServer(t, proto.AgentWorking, permissionScreen)
	var err error
	out, _ := a4Capture(t, "", func() { err = runAgent([]string{"question", "p3"}) })
	if err != nil || strings.TrimSpace(out) != "null" {
		t.Fatalf("%q, %v", out, err)
	}
}

func TestAgentAnswerPressesTheChoice(t *testing.T) {
	srv := answerServer(t, proto.AgentBlocked, permissionScreen)
	var qid string
	out, _ := a4Capture(t, "", func() { runAgent([]string{"question", "p3"}) }) //nolint:errcheck
	var q phone.Question
	json.Unmarshal([]byte(out), &q) //nolint:errcheck
	qid = q.ID

	// The cursor is on 1: choice 3 is two down and enter.
	if err := runAgent([]string{"answer", "-question", qid, "p3", "3"}); err != nil {
		t.Fatal(err)
	}
	var keys proto.PaneSendKeysParams
	if !srv.params(t, proto.MethodPaneSendKeys, &keys) || keys.ID != "p3" || strings.Join(keys.Keys, " ") != "down down enter" {
		t.Fatalf("keys %+v", keys)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"answer", "-question", "q_stale", "p3", "1"}, "has changed"},
		{[]string{"answer", "p3", "4"}, "not one of the choices"},
		{[]string{"answer", "p3", "01"}, "not one of the choices"},
		{[]string{"answer", "p3"}, agentAnswerUsage},
		{[]string{"question"}, agentQuestionUsage},
		{[]string{"answer", "p7", "1"}, "no pane"},
	} {
		if err := runAgent(tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestAgentAnswerWhenNotWaiting(t *testing.T) {
	srv := answerServer(t, proto.AgentDone, permissionScreen)
	if err := runAgent([]string{"answer", "p3", "1"}); err == nil || !strings.Contains(err.Error(), "not waiting") {
		t.Fatalf("%v", err)
	}
	var keys proto.PaneSendKeysParams
	if srv.params(t, proto.MethodPaneSendKeys, &keys) {
		t.Fatalf("sent %+v to an agent that was not waiting", keys)
	}
}
