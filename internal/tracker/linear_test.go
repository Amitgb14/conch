package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeLinear answers the GraphQL requests this package makes, by which
// operation they name, from a small store of issues; it records each
// request so a test can say what was sent.
type fakeLinear struct {
	mu       sync.Mutex
	key      string
	issues   map[string]map[string]any // by identifier
	states   []map[string]any
	reqs     []map[string]any
	comments []map[string]any
	links    []map[string]any
	moved    map[string]string // issue id → state id
}

func newFakeLinear(t *testing.T) (*fakeLinear, *httptest.Server) {
	f := &fakeLinear{
		key: "lin_api_test",
		issues: map[string]map[string]any{
			"ENG-1": {
				"id": "uuid-1", "identifier": "ENG-1", "title": "Fix the login", "description": "It fails on Safari.",
				"url": "https://linear.app/acme/issue/ENG-1/fix-the-login", "state": map[string]any{"name": "Todo"},
				"comments":    map[string]any{"nodes": []any{map[string]any{"body": "Seen on 17.2", "user": map[string]any{"name": "Ana"}}, map[string]any{"body": "from a bot", "user": nil}}},
				"attachments": map[string]any{"nodes": []any{map[string]any{"title": "Sentry", "url": "https://sentry.example/1"}}},
			},
		},
		states: []map[string]any{{"id": "s-todo", "name": "Todo"}, {"id": "s-review", "name": "In Review"}, {"id": "s-done", "name": "Done"}},
		moved:  map[string]string{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeLinear) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != f.key {
		http.Error(w, `{"errors":[{"message":"Authentication required"}]}`, http.StatusUnauthorized)
		return
	}
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, map[string]any{"query": req.Query, "variables": req.Variables})
	reply := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
	missing := func() {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "errors": []any{map[string]any{"message": "Entity not found: Issue", "extensions": map[string]any{"code": "ENTITY_NOT_FOUND"}}}})
	}
	byID := func(id string) map[string]any {
		if i, ok := f.issues[id]; ok {
			return i
		}
		for _, i := range f.issues {
			if i["id"] == id {
				return i
			}
		}
		return nil
	}
	op, _, _ := strings.Cut(strings.Fields(req.Query)[1], "(")
	switch op {
	case "Tickets":
		var nodes []any
		for _, i := range f.issues {
			nodes = append(nodes, i)
		}
		reply(map[string]any{"issues": map[string]any{"nodes": nodes}})
	case "Ticket":
		i := byID(req.Variables["id"].(string))
		if i == nil {
			missing()
			return
		}
		reply(map[string]any{"issue": i})
	case "States":
		i := byID(req.Variables["id"].(string))
		if i == nil {
			missing()
			return
		}
		reply(map[string]any{"issue": map[string]any{"id": i["id"], "team": map[string]any{"states": map[string]any{"nodes": f.states}}}})
	case "SetState":
		f.moved[req.Variables["id"].(string)] = req.Variables["state"].(string)
		name := ""
		for _, s := range f.states {
			if s["id"] == req.Variables["state"] {
				name = s["name"].(string)
			}
		}
		reply(map[string]any{"issueUpdate": map[string]any{"success": true, "issue": map[string]any{"state": map[string]any{"name": name}}}})
	case "Comment":
		f.comments = append(f.comments, req.Variables)
		reply(map[string]any{"commentCreate": map[string]any{"success": true}})
	case "Link":
		f.links = append(f.links, req.Variables)
		reply(map[string]any{"attachmentLinkURL": map[string]any{"success": true}})
	default:
		http.Error(w, "unknown operation "+op, http.StatusBadRequest)
	}
}

func TestLinearGet(t *testing.T) {
	f, srv := newFakeLinear(t)
	lin := NewLinear(srv.URL, f.key)
	got, err := lin.Get(context.Background(), "ENG-1")
	if err != nil {
		t.Fatal(err)
	}
	want := Ticket{Tracker: "linear", Key: "ENG-1", Title: "Fix the login", Body: "It fails on Safari.",
		URL: "https://linear.app/acme/issue/ENG-1/fix-the-login", Status: "Todo",
		Comments: []Comment{{Author: "Ana", Body: "Seen on 17.2"}, {Body: "from a bot"}},
		Links:    []Link{{Title: "Sentry", URL: "https://sentry.example/1"}}}
	if g, _ := json.Marshal(got); string(g) != mustJSON(want) {
		t.Errorf("got %s\nwant %s", g, mustJSON(want))
	}
	if _, err := lin.Get(context.Background(), "ENG-404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing ticket: %v", err)
	}
}

func TestLinearListFilters(t *testing.T) {
	f, srv := newFakeLinear(t)
	lin := NewLinear(srv.URL, f.key)
	list, err := lin.List(context.Background(), ListOptions{Scope: "eng", Assigned: true, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Key != "ENG-1" || list[0].Status != "Todo" {
		t.Errorf("list = %+v", list)
	}
	vars := f.reqs[0]["variables"].(map[string]any)
	filter, _ := json.Marshal(vars["filter"])
	for _, want := range []string{`"team":{"key":{"eq":"ENG"}}`, `"assignee":{"isMe":{"eq":true}}`, `"nin":["completed","canceled"]`} {
		if !strings.Contains(string(filter), want) {
			t.Errorf("filter %s lacks %s", filter, want)
		}
	}
	if vars["first"].(float64) != 100 {
		t.Errorf("first = %v, want the cap of 100", vars["first"])
	}
	// No scope, not assigned: neither filter, and the default count.
	f.reqs = nil
	if _, err := lin.List(context.Background(), ListOptions{}); err != nil {
		t.Fatal(err)
	}
	vars = f.reqs[0]["variables"].(map[string]any)
	filter, _ = json.Marshal(vars["filter"])
	if strings.Contains(string(filter), "team") || strings.Contains(string(filter), "assignee") || vars["first"].(float64) != 30 {
		t.Errorf("unscoped list sent %s, first %v", filter, vars["first"])
	}
}

func TestLinearCommentStatusLink(t *testing.T) {
	f, srv := newFakeLinear(t)
	lin := NewLinear(srv.URL, f.key)
	ctx := context.Background()
	if err := lin.Comment(ctx, "ENG-1", "PR opened: https://github.com/a/b/pull/7"); err != nil {
		t.Fatal(err)
	}
	if len(f.comments) != 1 || f.comments[0]["id"] != "uuid-1" || f.comments[0]["body"] != "PR opened: https://github.com/a/b/pull/7" {
		t.Errorf("comments = %+v", f.comments)
	}
	if err := lin.Comment(ctx, "ENG-1", "  "); err == nil {
		t.Error("an empty comment was sent")
	}
	got, err := lin.SetStatus(ctx, "ENG-1", "in review")
	if err != nil || got != "In Review" || f.moved["uuid-1"] != "s-review" {
		t.Errorf("SetStatus = %q, %v; moved %v", got, err, f.moved)
	}
	if _, err := lin.SetStatus(ctx, "ENG-1", "Shipped"); err == nil || !strings.Contains(err.Error(), "Todo, In Review, Done") {
		t.Errorf("unknown status: %v", err)
	}
	if err := lin.Link(ctx, "ENG-1", "https://github.com/a/b/pull/7", "Pull request #7"); err != nil {
		t.Fatal(err)
	}
	if len(f.links) != 1 || f.links[0]["id"] != "uuid-1" || f.links[0]["title"] != "Pull request #7" {
		t.Errorf("links = %+v", f.links)
	}
	if err := lin.Link(ctx, "ENG-1", "", ""); err == nil {
		t.Error("an empty link was sent")
	}
	for _, err := range []error{
		lin.Comment(ctx, "ENG-404", "x"),
		lin.Link(ctx, "ENG-404", "https://x", ""),
		func() error { _, err := lin.SetStatus(ctx, "ENG-404", "Done"); return err }(),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("missing ticket: %v", err)
		}
	}
}

func TestLinearErrors(t *testing.T) {
	f, srv := newFakeLinear(t)
	if _, err := NewLinear(srv.URL, "wrong").Get(context.Background(), "ENG-1"); err == nil || !strings.Contains(err.Error(), "refused the key") {
		t.Errorf("wrong key: %v", err)
	}
	_ = f
	// A GraphQL error that is not a missing ticket is said as it is.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errors":[{"message":"Rate limit exceeded"}]}`))
	}))
	defer bad.Close()
	if _, err := NewLinear(bad.URL, "k").List(context.Background(), ListOptions{}); err == nil || !strings.Contains(err.Error(), "Rate limit exceeded") {
		t.Errorf("graphql error: %v", err)
	}
	// Junk instead of JSON, a server error, a closed server, a cancelled context.
	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer junk.Close()
	if _, err := NewLinear(junk.URL, "k").Get(context.Background(), "ENG-1"); err == nil {
		t.Error("junk reply was taken")
	}
	boom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", 502) }))
	defer boom.Close()
	if _, err := NewLinear(boom.URL, "k").Get(context.Background(), "ENG-1"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("server error: %v", err)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	if _, err := NewLinear(closed.URL, "k").Get(context.Background(), "ENG-1"); err == nil {
		t.Error("a closed server answered")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewLinear(srv.URL, f.key).Get(ctx, "ENG-1"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	if NewLinear("", "k").(*linear).url != LinearAPI {
		t.Error("the default endpoint is not Linear's")
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
