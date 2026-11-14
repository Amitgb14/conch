package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// What conch is doing about a ticket, from what the server knows: the most
// pressing of its agent, its pull request and a queued task.
func TestDoingFor(t *testing.T) {
	br := func(name string, pr *proto.PRInfo) proto.BranchInfo { return proto.BranchInfo{Name: name, PR: pr} }
	agent := func(id, branch, state string) proto.PaneInfo {
		return proto.PaneInfo{ID: id, ProjectID: "r1", Branch: branch, State: proto.PaneRunning, Agent: &proto.AgentStatus{Name: "claude", State: state}}
	}
	is12 := func(tp proto.TaskCreateParams) bool { return tp.Issue == 12 }
	queued := []proto.ScheduledItem{{ID: "s1", Waiting: "2 agents are busy", Task: &proto.TaskCreateParams{ProjectID: "r1", Issue: 12}}}
	open := &proto.PRInfo{Number: 30, State: "OPEN", Checks: "pending"}
	merged := &proto.PRInfo{Number: 31, State: "MERGED"}
	cases := map[string]struct {
		branches []proto.BranchInfo
		sched    []proto.ScheduledItem
		panes    []proto.PaneInfo
		want     proto.TicketDoing
	}{
		"nothing":            {nil, nil, nil, proto.TicketDoing{}},
		"queued":             {nil, queued, nil, proto.TicketDoing{State: proto.DoingQueued, Schedule: "s1", Note: "2 agents are busy"}},
		"queued elsewhere":   {nil, []proto.ScheduledItem{{ID: "s2", Task: &proto.TaskCreateParams{ProjectID: "r2", Issue: 12}}}, nil, proto.TicketDoing{}},
		"a branch, no agent": {[]proto.BranchInfo{br("conch/issue-12-fix-it", nil)}, nil, nil, proto.TicketDoing{Branch: "conch/issue-12-fix-it"}},
		"not issue 120":      {[]proto.BranchInfo{br("conch/issue-120-other", open)}, nil, nil, proto.TicketDoing{}},
		"working":            {[]proto.BranchInfo{br("conch/issue-12-fix-it", nil)}, nil, []proto.PaneInfo{agent("p3", "conch/issue-12-fix-it", proto.AgentWorking)}, proto.TicketDoing{State: proto.DoingWorking, Pane: "p3", Branch: "conch/issue-12-fix-it"}},
		"waiting beats a PR": {[]proto.BranchInfo{br("conch/issue-12-fix-it", open)}, nil, []proto.PaneInfo{agent("p3", "conch/issue-12-fix-it", proto.AgentBlocked)}, proto.TicketDoing{State: proto.DoingWaiting, Pane: "p3", Branch: "conch/issue-12-fix-it", PR: open}},
		"a PR beats queued":  {[]proto.BranchInfo{br("conch/issue-12-fix-it", open)}, queued, nil, proto.TicketDoing{State: proto.DoingPR, Schedule: "s1", Note: "2 agents are busy", Branch: "conch/issue-12-fix-it", PR: open}},
		"idle on an open PR": {[]proto.BranchInfo{br("conch/issue-12-fix-it", open)}, nil, []proto.PaneInfo{agent("p3", "conch/issue-12-fix-it", proto.AgentIdle)}, proto.TicketDoing{State: proto.DoingPR, Pane: "p3", Branch: "conch/issue-12-fix-it", PR: open}},
		"merged":             {[]proto.BranchInfo{br("conch/issue-12-fix-it", merged)}, nil, nil, proto.TicketDoing{State: proto.DoingMerged, Branch: "conch/issue-12-fix-it", PR: merged}},
		"an attempt":         {[]proto.BranchInfo{br("conch/issue-12-fix-it/codex", nil)}, nil, []proto.PaneInfo{agent("p4", "conch/issue-12-fix-it/codex", proto.AgentIdle)}, proto.TicketDoing{State: proto.DoingIdle, Pane: "p4", Branch: "conch/issue-12-fix-it/codex"}},
		"a reviewer is not its agent": {[]proto.BranchInfo{br("conch/issue-12-fix-it", nil)}, nil,
			[]proto.PaneInfo{func() proto.PaneInfo {
				p := agent("p5", "conch/issue-12-fix-it", proto.AgentWorking)
				p.Name = proto.ReviewerPrefix + "x"
				return p
			}()},
			proto.TicketDoing{Branch: "conch/issue-12-fix-it"}},
	}
	for name, c := range cases {
		got := doingFor(proto.ProjectInfo{Branches: c.branches}, "r1", is12, issueBranch(12), c.sched, c.panes)
		if got.State != c.want.State || got.Pane != c.want.Pane || got.Branch != c.want.Branch || got.Schedule != c.want.Schedule ||
			got.Note != c.want.Note || (got.PR == nil) != (c.want.PR == nil) || (got.PR != nil && got.PR.Number != c.want.PR.Number) {
			t.Errorf("%s: %+v, want %+v", name, got, c.want)
		}
	}
	if !keyBranch("ENG-142").MatchString("conch/eng-142-retry-uploads") || keyBranch("ENG-14").MatchString("conch/eng-142-retry") {
		t.Fatal("ticket branches")
	}
}

func TestTicketOverview(t *testing.T) {
	s, p := afterFixture(t)
	quietPRs(t, p)
	t.Setenv("LINEAR_API_KEY", "")
	lin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 4096)
		n, _ := r.Body.Read(body)
		if strings.Contains(string(body[:n]), "isMe") {
			w.Write([]byte(`{"data":{"issues":{"nodes":[{"id":"2","identifier":"ENG-9","title":"mine"}]}}}`))
			return
		}
		w.Write([]byte(`{"data":{"issues":{"nodes":[{"id":"1","identifier":"ENG-8","title":"theirs","state":{"name":"Todo"}},{"id":"2","identifier":"ENG-9","title":"mine","state":{"name":"In Progress"}}]}}}`))
	}))
	t.Cleanup(lin.Close)
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
case "$1 $2" in
"api user") echo amit ;;
"issue list") echo '[{"number":12,"title":"Fix it","url":"https://x/12","labels":[{"name":"ready"}],"assignees":[{"login":"amit"}]},{"number":13,"title":"Other","url":"https://x/13","labels":[],"assignees":[{"login":"ana"}]}]' ;;
"pr list") echo '[]' ;;
"api graphql") echo '{}' ;;
*) exit 7 ;;
esac
`), 0o755)
	t.Setenv("CONCH_GH", gh)
	if _, perr := s.scheduleAdd(proto.ScheduledItem{At: time.Now().Add(time.Hour), Queue: true, Task: &proto.TaskCreateParams{ProjectID: p.id, Issue: 12}}); perr != nil {
		t.Fatal(perr)
	}
	writeConfig(t, s, `[trackers.linear]
api_key = "k"
api_url = "`+lin.URL+`"
[[trackers.projects]]
project = "api"
tracker = "linear"
key = "ENG"
`)
	ov, perr := s.ticketOverview(proto.TicketOverviewParams{})
	if perr != nil {
		t.Fatal(perr)
	}
	if len(ov.Projects) != 1 {
		t.Fatalf("%+v", ov)
	}
	g := ov.Projects[0]
	if g.ProjectID != p.id || g.Name != "api" || strings.Join(g.Sources, ",") != "github,linear · ENG" || len(g.Errors) != 0 || len(g.Tickets) != 4 {
		t.Fatalf("group %+v", g)
	}
	i12, i13, e8, e9 := g.Tickets[0], g.Tickets[1], g.Tickets[2], g.Tickets[3]
	if i12.Source != "github" || i12.Number != 12 || !i12.Mine || i12.Doing.State != proto.DoingQueued || i12.Labels[0] != "ready" {
		t.Fatalf("#12 %+v", i12)
	}
	if i13.Mine || i13.Doing.State != "" {
		t.Fatalf("#13 %+v", i13)
	}
	if e8.Source != "linear" || e8.Key != "ENG-8" || e8.Mine || e8.Status != "Todo" || !e9.Mine {
		t.Fatalf("linear %+v %+v", e8, e9)
	}

	// No project entries: the tracker is a group of its own; a project with
	// no GitHub remote lists nothing and says nothing is wrong.
	os.WriteFile(gh, []byte("#!/bin/sh\ncase \"$1 $2\" in\n\"api user\") echo amit ;;\n\"issue list\") echo 'none of the git remotes configured for this repository point to a known GitHub host' >&2; exit 1 ;;\n\"pr list\") echo '[]' ;;\n*) exit 7 ;;\nesac\n"), 0o755)
	writeConfig(t, s, "[trackers.linear]\napi_key = \"k\"\napi_url = \""+lin.URL+"\"\nteam = \"ENG\"\n")
	ov, _ = s.ticketOverview(proto.TicketOverviewParams{})
	if len(ov.Projects) != 2 || len(ov.Projects[0].Tickets) != 0 || len(ov.Projects[0].Errors) != 0 ||
		ov.Projects[1].ProjectID != "" || ov.Projects[1].Name != "Linear" || len(ov.Projects[1].Tickets) != 2 || ov.Projects[1].Tickets[0].Doing.State != "" {
		t.Fatalf("no entries %+v", ov)
	}
	// Asked for one project that is not a git project: nothing, no error.
	if ov, perr := s.ticketOverview(proto.TicketOverviewParams{ProjectIDs: []string{"nope"}}); perr != nil || len(ov.Projects) != 0 {
		t.Fatalf("%+v %v", ov, perr)
	}
	// A tracker failing is said in its group; the issues still list.
	writeConfig(t, s, "[trackers.jira]\nurl = \"https://acme.atlassian.net\"\n[[trackers.projects]]\nproject = \"api\"\ntracker = \"jira\"\n")
	ov, _ = s.ticketOverview(proto.TicketOverviewParams{})
	if len(ov.Projects) != 1 || len(ov.Projects[0].Errors) != 1 || !strings.Contains(ov.Projects[0].Errors[0], "email") {
		t.Fatalf("a failing tracker %+v", ov.Projects)
	}
}
