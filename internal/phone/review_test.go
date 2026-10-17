package phone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/conch/internal/proto"
)

// reviewAgent is an agent on branch feat of a git project, as a task makes
// one, and the project's ID.
func reviewAgent(t *testing.T, f *fixture) (pane, projectID string) {
	t.Helper()
	repo := filepath.Join(f.dir, "api")
	os.MkdirAll(repo, 0o755)
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "config", "user.name", "t")
	gitIn(t, repo, "config", "user.email", "t@example.com")
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	var proj proto.ProjectInfo
	f.call(proto.MethodProjectAdd, proto.ProjectAddParams{Path: repo}, &proj)
	var wt proto.WorktreeInfo
	f.call(proto.MethodWorktreeAdd, proto.WorktreeAddParams{ProjectID: proj.ID, Branch: "feat"}, &wt)
	var info proto.PaneInfo
	f.call(proto.MethodPaneCreate, proto.PaneCreateParams{Agent: "claude", Command: []string{"/bin/sh", "-c", "stty -echo; exec cat"},
		Cwd: wt.Path, Cols: 100, Rows: 24}, &info)
	waitFor(t, "the agent on feat", func() bool {
		var list proto.PaneList
		f.call(proto.MethodPaneList, nil, &list)
		for _, p := range list.Panes {
			if p.ID == info.ID && p.Agent != nil && p.Branch == "feat" && p.ProjectID == proj.ID {
				return true
			}
		}
		return false
	})
	return info.ID, proj.ID
}

// The comments on an agent's branch come with it, and a phone that may
// reply hands them over.
func TestReviewOnThePhone(t *testing.T) {
	f := newFixture(t)
	view, p := f.pair(PermView), f.pair(PermReply)
	pane, proj := reviewAgent(t, f)
	other := f.pane("claude", "stty -echo; exec cat") // on no branch

	f.call(proto.MethodReviewSet, proto.ReviewComment{ProjectID: proj, Branch: "feat", File: "a.go", Line: 3, Note: "off by one"}, nil)
	f.call(proto.MethodReviewSet, proto.ReviewComment{ProjectID: proj, Branch: "feat", File: "b.go",
		Hunk: "@@ -1 +1 @@\n-x\n+y", Note: "why y?"}, nil)
	f.call(proto.MethodReviewSet, proto.ReviewComment{ProjectID: proj, Branch: "elsewhere", File: "c.go", Note: "not this"}, nil)

	byPane := map[string]Agent{}
	for _, a := range view.agents() {
		byPane[a.Pane] = a
	}
	// The app's list of panes carries the same.
	var panes PaneList
	view.get("/api/panes", &panes)
	for _, pn := range panes.Panes {
		if pn.Pane == phoneID(pane) && len(pn.Review) != 2 {
			t.Fatalf("pane %+v", pn)
		}
	}
	got := byPane[phoneID(pane)].Review
	if len(got) != 2 || got[0].Where != "a.go:3" || got[0].Note != "off by one" || got[1].Where != "b.go @@ -1 +1 @@" || got[0].ID == "" {
		t.Fatalf("review %+v", got)
	}
	if r := byPane[phoneID(other)].Review; r != nil {
		t.Fatalf("an agent on no branch has %+v", r)
	}

	if status, e := view.post("/api/review/send", ReviewSendRequest{Pane: pane}, nil); status != 403 || e.Code != CodeForbidden {
		t.Fatalf("view sending: %d %+v", status, e)
	}
	for _, c := range []struct {
		req    ReviewSendRequest
		status int
	}{
		{ReviewSendRequest{}, 400},
		{ReviewSendRequest{Pane: "p999"}, 404},
		{ReviewSendRequest{Pane: other}, 400},                       // not on a branch
		{ReviewSendRequest{Pane: f.pane("", "exec sleep 30")}, 404}, // no agent there
	} {
		if status, e := p.post("/api/review/send", c.req, nil); status != c.status {
			t.Errorf("%+v: %d %+v, want %d", c.req, status, e, c.status)
		}
	}

	var res ReviewSendResponse
	if status, e := p.post("/api/review/send", ReviewSendRequest{Pane: pane}, &res); status != 200 {
		t.Fatalf("send: %d %+v", status, e)
	}
	if res.Pane != phoneID(pane) || res.Agent != "claude" || res.Sent != 2 {
		t.Fatalf("sent %+v", res)
	}
	waitFor(t, "the comments typed", func() bool { return strings.Contains(f.screen(pane), "why y?") })
	for _, a := range view.agents() {
		if a.Pane == phoneID(pane) && a.Review != nil {
			t.Fatalf("still offered after sending: %+v", a.Review)
		}
	}
	// Nothing left to send says so.
	if status, e := p.post("/api/review/send", ReviewSendRequest{Pane: pane}, nil); status != 400 || !strings.Contains(e.Message, "no review comments") {
		t.Fatalf("again: %d %+v", status, e)
	}
}

// A comment written while the phone watches arrives on the agent's card,
// and its going takes it off.
func TestReviewOnTheSocket(t *testing.T) {
	f := newFixture(t)
	pane, proj := reviewAgent(t, f)
	s := f.pair(PermFull).socket()
	s.send(ClientMessage{Type: MsgAgentsWatch, ID: "w1"})
	s.next("the list", func(m ServerMessage) bool { return m.Type == MsgAgents })
	// The app keeps panes: a socket watching them hears too.
	ps := f.pair(PermView).socket()
	ps.send(ClientMessage{Type: MsgPanesWatch, ID: "w2"})
	ps.next("the panes", func(m ServerMessage) bool { return m.Type == MsgPanes })

	var list proto.ReviewList
	f.call(proto.MethodReviewSet, proto.ReviewComment{ProjectID: proj, Branch: "feat", File: "a.go", Note: "rename it"}, &list)
	s.next("the comment", func(m ServerMessage) bool {
		return m.Type == MsgAgent && m.Agent.Pane == phoneID(pane) && len(m.Agent.Review) == 1 && m.Agent.Review[0].Note == "rename it"
	})
	ps.next("the comment on the pane", func(m ServerMessage) bool {
		return m.Type == MsgPaneChanged && m.Info.Pane == phoneID(pane) && len(m.Info.Review) == 1
	})
	f.call(proto.MethodReviewSet, proto.ReviewComment{ID: list.Comments[0].ID, ProjectID: proj, Branch: "feat"}, nil)
	s.next("it taken away", func(m ServerMessage) bool {
		return m.Type == MsgAgent && m.Agent.Pane == phoneID(pane) && m.Agent.Review == nil
	})
	ps.next("it taken off the pane", func(m ServerMessage) bool {
		return m.Type == MsgPaneChanged && m.Info.Pane == phoneID(pane) && m.Info.Review == nil
	})
}

// A pull request's comments reach the phone with who wrote them on GitHub,
// the rest of the thread and where to answer them.
func TestReviewNotesFromGitHub(t *testing.T) {
	p := proto.PaneInfo{ProjectID: "api", Branch: "feat", Agent: &proto.AgentStatus{Name: "claude"}}
	got := reviewNotes([]proto.ReviewComment{
		{ID: "c1", ProjectID: "api", Branch: "feat", File: "a.go", Hunk: "@@ -1 +1 @@\n+x", Note: "mine"},
		{ID: "gh1", ProjectID: "api", Branch: "feat", File: "model.go", Line: 12, Hunk: "@@ -8 +8 @@", Note: "leaks", By: "alice",
			Source: proto.ReviewGitHub, URL: "https://github.com/o/r/pull/5#discussion_r1", Outdated: true,
			Replies: []proto.ReviewReply{{By: "bob", Note: "agreed"}}},
		{ID: "ghr7", ProjectID: "api", Branch: "feat", Note: "Needs tests.", By: "alice", Source: proto.ReviewGitHub},
		{ID: "gh9", ProjectID: "api", Branch: "other", File: "x.go", Note: "elsewhere", Source: proto.ReviewGitHub},
	}, p)
	if len(got) != 3 {
		t.Fatalf("notes %+v", got)
	}
	if n := got[0]; n.Where != "a.go @@ -1 +1 @@" || n.GitHub || n.URL != "" || n.Replies != nil {
		t.Fatalf("ours %+v", n)
	}
	if n := got[1]; n.Where != "model.go:12" || !n.GitHub || n.By != "alice" || !n.Outdated || !strings.Contains(n.URL, "discussion_r1") ||
		len(n.Replies) != 1 || n.Replies[0].By != "bob" || n.Replies[0].Note != "agreed" {
		t.Fatalf("thread %+v", n)
	}
	if n := got[2]; n.Where != "the pull request" || !n.GitHub {
		t.Fatalf("review %+v", n)
	}
}
