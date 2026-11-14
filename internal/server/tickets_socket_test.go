package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/conch/internal/config"
	"github.com/Amitgb14/conch/internal/proto"
)

// linearStub answers the operations the server's Linear client sends,
// for one ticket, ENG-7, and records the mutations.
type linearStub struct {
	mu    sync.Mutex
	calls []string
}

func (l *linearStub) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "lin-key" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	op, _, _ := strings.Cut(strings.Fields(req.Query)[1], "(")
	l.mu.Lock()
	l.calls = append(l.calls, op)
	l.mu.Unlock()
	reply := func(data any) { json.NewEncoder(w).Encode(map[string]any{"data": data}) }
	issue := map[string]any{"id": "u7", "identifier": "ENG-7", "title": "Export as CSV", "description": "Users ask for CSV.",
		"url": "https://linear.app/a/issue/ENG-7", "state": map[string]any{"name": "Todo"}}
	if id, _ := req.Variables["id"].(string); id != "" && id != "ENG-7" && id != "u7" {
		json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": "Entity not found", "extensions": map[string]any{"code": "ENTITY_NOT_FOUND"}}}})
		return
	}
	switch op {
	case "Tickets":
		if req.Variables["filter"].(map[string]any)["team"] == nil {
			http.Error(w, "no team filter", 400)
			return
		}
		reply(map[string]any{"issues": map[string]any{"nodes": []any{issue}}})
	case "Ticket":
		reply(map[string]any{"issue": issue})
	case "States":
		reply(map[string]any{"issue": map[string]any{"id": "u7", "team": map[string]any{"states": map[string]any{"nodes": []any{map[string]any{"id": "s1", "name": "In Review"}}}}}})
	case "SetState":
		reply(map[string]any{"issueUpdate": map[string]any{"success": true, "issue": map[string]any{"state": map[string]any{"name": "In Review"}}}})
	case "Comment":
		reply(map[string]any{"commentCreate": map[string]any{"success": true}})
	case "Link":
		reply(map[string]any{"attachmentLinkURL": map[string]any{"success": true}})
	}
}

func TestTaskFromATicket(t *testing.T) {
	stub := &linearStub{}
	srv := httptest.NewServer(http.HandlerFunc(stub.serve))
	defer srv.Close()
	c, _, worktree := taskFixture(t, "exec sleep 30")
	ctx := a5Ctx(t)
	var list proto.ProjectList
	if err := c.Call(ctx, proto.MethodProjectList, nil, &list); err != nil || len(list.Projects) != 1 {
		t.Fatalf("projects: %v %v", list, err)
	}
	proj := list.Projects[0]
	if len(c.MissingCapabilities([]string{proto.CapTickets})) > 0 {
		t.Fatal("tickets.v1 not announced")
	}

	// Nothing set up: said, and nothing made.
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("JIRA_API_TOKEN", "")
	err := c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID, Ticket: "ENG-7"}, nil)
	if err == nil || !strings.Contains(err.Error(), "no tracker is set up") {
		t.Fatalf("no tracker: %v", err)
	}

	cfg := config.Default()
	cfg.Trackers.Linear = config.LinearCfg{APIKeyEnv: "TEST_LINEAR", APIURL: srv.URL}
	cfg.Trackers.Projects = []config.TrackerProject{{Project: proj.Name, Tracker: "linear", Key: "ENG"}}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_LINEAR", "lin-key")

	var tickets proto.TicketList
	if err := c.Call(ctx, proto.MethodProjectTickets, proto.TicketsParams{ProjectID: proj.ID}, &tickets); err != nil {
		t.Fatal(err)
	}
	if tickets.Tracker != "linear" || len(tickets.Tickets) != 1 || tickets.Tickets[0].Key != "ENG-7" || tickets.Tickets[0].Status != "Todo" {
		t.Fatalf("tickets %+v", tickets)
	}

	var info proto.PaneInfo
	if err := c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID, Ticket: " eng-7 ", Cols: 60, Rows: 10}, &info); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info.Branch, "eng-7-export-as-csv") {
		t.Fatalf("branch %q", info.Branch)
	}
	if _, err := os.Stat(worktree(strings.ReplaceAll(info.Branch, "/", "-"))); err != nil {
		t.Fatalf("no worktree: %v", err)
	}

	// A ticket that isn't there says so, and makes nothing.
	err = c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID, Ticket: "ENG-404"}, nil)
	if err == nil || !strings.Contains(err.Error(), "ENG-404") || !strings.Contains(err.Error(), "no such ticket") {
		t.Fatalf("missing ticket: %v", err)
	}
	// Not shaped like a ticket; an issue and a ticket at once.
	if err := c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID, Ticket: "seven"}, nil); err == nil || !strings.Contains(err.Error(), "not a ticket") {
		t.Fatalf("bad ref: %v", err)
	}
	if err := c.Call(ctx, proto.MethodTaskCreate, proto.TaskCreateParams{ProjectID: proj.ID, Ticket: "ENG-7", Issue: 3}, nil); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("issue and ticket: %v", err)
	}

	var got proto.TicketInfo
	if err := c.Call(ctx, proto.MethodTicketGet, proto.TicketGetParams{ProjectID: proj.ID, Ticket: "ENG-7"}, &got); err != nil || got.Body != "Users ask for CSV." {
		t.Fatalf("ticket.get %+v %v", got, err)
	}
	stub.mu.Lock()
	stub.calls = nil
	stub.mu.Unlock()
	if err := c.Call(ctx, proto.MethodTicketUpdate, proto.TicketUpdateParams{ProjectID: proj.ID, Ticket: "ENG-7",
		Comment: "PR opened: https://github.com/a/b/pull/1", Status: "in review", LinkURL: "https://github.com/a/b/pull/1"}, &got); err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	calls := strings.Join(stub.calls, ",")
	stub.mu.Unlock()
	if calls != "Ticket,Comment,States,SetState,Ticket,Link,Ticket" {
		t.Fatalf("calls %s", calls)
	}
	if err := c.Call(ctx, proto.MethodTicketUpdate, proto.TicketUpdateParams{ProjectID: proj.ID, Ticket: "ENG-7"}, nil); err == nil || !strings.Contains(err.Error(), "nothing to change") {
		t.Fatalf("empty update: %v", err)
	}
	// A status the team has not got: the comment before it is said to be done.
	err = c.Call(ctx, proto.MethodTicketUpdate, proto.TicketUpdateParams{ProjectID: proj.ID, Ticket: "ENG-7", Comment: "x", Status: "Shipped"}, nil)
	if err == nil || !strings.Contains(err.Error(), "commented, but moving it failed") {
		t.Fatalf("half done: %v", err)
	}
	// A wrong key is the tracker's refusal, said.
	t.Setenv("TEST_LINEAR", "wrong")
	if err := c.Call(ctx, proto.MethodTicketGet, proto.TicketGetParams{Ticket: "ENG-7"}, nil); err == nil || !strings.Contains(err.Error(), "refused the key") {
		t.Fatalf("wrong key: %v", err)
	}
}
