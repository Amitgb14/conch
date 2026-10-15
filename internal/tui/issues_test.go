package tui

import (
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

func TestIssuesMenu(t *testing.T) {
	m, mach, peer := limitModel(t)
	c, p := a1FakeClient(t, proto.CapIssues)
	mach.c, peer = c, p
	peer.setResult(proto.MethodProjectIssues, proto.IssueList{Issues: []proto.IssueInfo{
		{Number: 12, Title: "Login fails", Labels: []string{"bug"}},
		{Number: 9, Title: strings.Repeat("very long title ", 10)},
	}})
	cmd := m.openIssues(mach.id, "r1")
	if cmd == nil {
		t.Fatalf("nothing read: %q", m.flash)
	}
	msg := cmd().(issuesMsg)
	m.receiveIssues(msg)
	mu, ok := m.overlay.(*menu)
	if !ok || len(mu.items) != 2 || mu.items[0].label != "#12 Login fails  [bug]" || mu.items[0].key != "1" {
		t.Fatalf("menu %#v", m.overlay)
	}
	for _, it := range mu.items {
		if w := len([]rune(it.label)); w > 70 {
			t.Fatalf("a label of %d", w)
		}
	}
	// Choosing one starts the default agent on it.
	m.cfg.Agents.Default = "codex"
	start := mu.items[0].run(m)
	if _, ok := start().(createdMsg); !ok {
		t.Fatal("no pane created")
	}
	var tp proto.TaskCreateParams
	sentParams(t, peer, proto.MethodTaskCreate, &tp)
	if tp.Issue != 12 || tp.Agent != "codex" || tp.ProjectID != "r1" || tp.Prompt != "" {
		t.Fatalf("task %+v", tp)
	}
	// Nothing open; an error from gh.
	m.overlay = nil
	m.receiveIssues(issuesMsg{machine: mach.id, projectID: "r1"})
	if m.overlay != nil || !strings.Contains(m.flash, "no open issues") {
		t.Fatalf("empty: %q", m.flash)
	}
	m.receiveIssues(issuesMsg{machine: mach.id, projectID: "r1", err: errString("gh issue list: no GitHub remote")})
	if !strings.Contains(m.flash, "no GitHub remote") {
		t.Fatalf("an error is shown: %q", m.flash)
	}
}

func TestIssuesRefusals(t *testing.T) {
	m, mach, _ := limitModel(t)
	if cmd := m.openIssues(mach.id, "r1"); cmd != nil || !strings.Contains(m.flash, "predates tasks from issues") {
		t.Fatalf("old server: %q", m.flash)
	}
	mach.projects[0].Git = false
	if cmd := m.openIssues(mach.id, "r1"); cmd != nil || !strings.Contains(m.flash, "git project") {
		t.Fatalf("plain folder: %q", m.flash)
	}
	if cmd := m.openIssues(mach.id, "nope"); cmd != nil {
		t.Fatal("an unknown project")
	}
}
