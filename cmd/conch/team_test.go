package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// teamServer is a fake server with three agents in two projects; pane.team
// is recorded and answered with the pane in that team.
func teamServer(t *testing.T) *a4Var[[]proto.PaneTeamParams] {
	t.Helper()
	a4Env(t)
	os.WriteFile(filepath.Join(config.Dir(), "config.toml"), []byte(`
[[teams]]
name = "Backend"
projects = ["api"]

[[teams]]
name = "UI"
branches = ["ui/*"]
`), 0o644)
	srv := startA4Server(t, config.SocketPath())
	seen := newA4Var([]proto.PaneTeamParams(nil))
	panes := []proto.PaneInfo{
		{ID: "p1", Name: "claude", State: proto.PaneRunning, ProjectID: "r1", Branch: "retry", Agent: &proto.AgentStatus{Name: "claude"}},
		{ID: "p2", Name: "codex", State: proto.PaneRunning, ProjectID: "r2", Branch: "ui/nav", Agent: &proto.AgentStatus{Name: "codex"}},
		{ID: "p3", Name: "gemini", State: proto.PaneRunning, ProjectID: "r2", Branch: "docs", Team: "Report", Agent: &proto.AgentStatus{Name: "gemini"}},
		{ID: "p4", Name: "out", State: proto.PaneRunning, ProjectID: "r1", Branch: "x", Team: config.NoTeam, Agent: &proto.AgentStatus{Name: "claude"}},
	}
	srv.setHandle(func(msg proto.Message, _ *proto.Conn) (any, *proto.Error) {
		switch msg.Method {
		case proto.MethodPaneList:
			return proto.PaneList{Panes: panes}, nil
		case proto.MethodProjectList:
			return proto.ProjectList{Projects: []proto.ProjectInfo{{ID: "r1", Name: "api"}, {ID: "r2", Name: "web"}}}, nil
		case proto.MethodPaneTeam:
			var p proto.PaneTeamParams
			json.Unmarshal(msg.Params, &p)
			seen.Set(append(seen.Get(), p))
			for _, q := range panes {
				if q.ID == p.ID {
					q.Team = p.Team
					return q, nil
				}
			}
			return nil, proto.Errorf(proto.ErrNotFound, "no pane %s", p.ID)
		}
		return nil, nil
	})
	return seen
}

func TestAgentTeam(t *testing.T) {
	seen := teamServer(t)
	run := func(args ...string) (string, error) {
		var err error
		out, _ := a4Capture(t, "", func() { err = runAgent(append([]string{"team"}, args...)) })
		return out, err
	}
	for ref, want := range map[string]string{
		"p1":     "p1 is in Backend (by its rule in config.toml)",
		"codex":  "p2 is in UI (by its rule in config.toml)",
		"p3":     "p3 is in Report (by hand)",
		"p4":     "p4 is kept out of every team",
		"gemini": "p3 is in Report (by hand)",
	} {
		if out, err := run(ref); err != nil || !strings.Contains(out, want) {
			t.Errorf("%s: %q %v, want %q", ref, out, err, want)
		}
	}
	if out, err := run("p1", "UX"); err != nil || !strings.Contains(out, "p1 is in UX (by hand)") {
		t.Fatalf("set: %q %v", out, err)
	}
	run("p1", "-")
	run("p1", "-clear")
	got := seen.Get()
	if len(got) != 3 || got[0] != (proto.PaneTeamParams{ID: "p1", Team: "UX"}) || got[1].Team != "-" || got[2].Team != "" {
		t.Fatalf("sent %+v", got)
	}
	for _, bad := range [][]string{{}, {"p1", "a", "b"}, {"p1", " "}} {
		if _, err := run(bad...); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := run("nope"); err == nil {
		t.Fatal("an unknown pane should fail")
	}
}

func TestStatusTeam(t *testing.T) {
	teamServer(t)
	var err error
	out, _ := a4Capture(t, "", func() { err = runStatus([]string{"-wide", "-team", "backend"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "p1") || strings.Contains(out, "p2") || strings.Contains(out, "p4") {
		t.Fatalf("backend: %s", out)
	}
	out, _ = a4Capture(t, "", func() { err = runStatus([]string{"-team", "Nobody"}) })
	if err != nil || !strings.Contains(out, "no agents in Nobody") {
		t.Fatalf("empty: %q %v", out, err)
	}
}

// -team is for agents that start now: a task kept for later has no pane
// to put in a team.
func TestTaskTeamOnlyNow(t *testing.T) {
	a4Env(t)
	for _, extra := range [][]string{{"-at", "+30m"}, {"-every", "1h"}, {"-queue"}, {"-after", "main"}, {"-sandbox", "daytona"}} {
		args := append(append([]string{"-team", "UI"}, extra...), "do it")
		if err := runTask(args); err == nil || !strings.Contains(err.Error(), "start them later or elsewhere") {
			t.Errorf("%v: %v", extra, err)
		}
	}
}
