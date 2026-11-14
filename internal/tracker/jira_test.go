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

type fakeJira struct {
	mu          sync.Mutex
	email, tok  string
	queries     []string
	comments    []map[string]any
	links       []map[string]any
	transitions []string
	noLinks     bool
}

func newFakeJira(t *testing.T) (*fakeJira, *httptest.Server) {
	f := &fakeJira{email: "me@acme.test", tok: "tok"}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeJira) serve(w http.ResponseWriter, r *http.Request) {
	if u, p, ok := r.BasicAuth(); !ok || u != f.email || p != f.tok {
		http.Error(w, "", http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	js := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	body := func() map[string]any {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		return m
	}
	issue := map[string]any{"key": "PROJ-42", "fields": map[string]any{
		"summary": "Export as CSV", "description": "Users want *CSV*.", "status": map[string]any{"name": "To Do"},
		"comment": map[string]any{"comments": []any{map[string]any{"body": "Also XLSX?", "author": map[string]any{"displayName": "Bo"}}}},
	}}
	switch p := r.URL.Path; {
	case r.Method == "GET" && p == "/rest/api/3/search/jql":
		f.queries = append(f.queries, r.URL.RawQuery)
		js(map[string]any{"issues": []any{map[string]any{"key": "PROJ-42", "fields": map[string]any{"summary": "Export as CSV", "status": map[string]any{"name": "To Do"}}}}})
	case r.Method == "GET" && p == "/rest/api/2/issue/PROJ-42":
		if r.URL.Query().Get("fields") == "" {
			http.Error(w, "no fields", 400)
			return
		}
		js(issue)
	case r.Method == "GET" && p == "/rest/api/2/issue/PROJ-42/remotelink":
		if f.noLinks {
			http.Error(w, "", 500)
			return
		}
		js([]any{map[string]any{"object": map[string]any{"url": "https://github.com/a/b/pull/3", "title": "PR #3"}}})
	case r.Method == "POST" && p == "/rest/api/2/issue/PROJ-42/comment":
		f.comments = append(f.comments, body())
		w.WriteHeader(201)
		js(map[string]any{"id": "1"})
	case r.Method == "POST" && p == "/rest/api/2/issue/PROJ-42/remotelink":
		f.links = append(f.links, body())
		w.WriteHeader(201)
		js(map[string]any{"id": 1})
	case r.Method == "GET" && p == "/rest/api/2/issue/PROJ-42/transitions":
		js(map[string]any{"transitions": []any{
			map[string]any{"id": "11", "name": "Start", "to": map[string]any{"name": "In Progress"}},
			map[string]any{"id": "21", "name": "Ready for review", "to": map[string]any{"name": "In Review"}},
			map[string]any{"id": "31", "name": "Close", "to": map[string]any{"name": "Done"}},
		}})
	case r.Method == "POST" && p == "/rest/api/2/issue/PROJ-42/transitions":
		b := body()
		f.transitions = append(f.transitions, b["transition"].(map[string]any)["id"].(string))
		w.WriteHeader(204)
	case r.Method == "GET" && p == "/rest/api/2/issue/NEW-1/transitions":
		js(map[string]any{"transitions": []any{}})
	default:
		http.Error(w, `{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`, 404)
	}
}

func TestJiraGet(t *testing.T) {
	f, srv := newFakeJira(t)
	j := NewJira(srv.URL+"/", f.email, f.tok)
	got, err := j.Get(context.Background(), "PROJ-42")
	if err != nil {
		t.Fatal(err)
	}
	want := Ticket{Tracker: "jira", Key: "PROJ-42", Title: "Export as CSV", Body: "Users want *CSV*.",
		URL: srv.URL + "/browse/PROJ-42", Status: "To Do",
		Comments: []Comment{{Author: "Bo", Body: "Also XLSX?"}},
		Links:    []Link{{Title: "PR #3", URL: "https://github.com/a/b/pull/3"}}}
	if g := mustJSON(got); g != mustJSON(want) {
		t.Errorf("got %s\nwant %s", g, mustJSON(want))
	}
	// Links that can't be read leave the ticket without them, not unread.
	f.noLinks = true
	got, err = j.Get(context.Background(), "PROJ-42")
	if err != nil || len(got.Links) != 0 || got.Title != "Export as CSV" {
		t.Errorf("without links: %+v, %v", got, err)
	}
	if _, err := j.Get(context.Background(), "PROJ-404"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if _, err := NewJira(srv.URL, f.email, "wrong").Get(context.Background(), "PROJ-42"); err == nil || !strings.Contains(err.Error(), "refused the key") {
		t.Errorf("wrong token: %v", err)
	}
}

func TestJiraList(t *testing.T) {
	f, srv := newFakeJira(t)
	j := NewJira(srv.URL, f.email, f.tok)
	list, err := j.List(context.Background(), ListOptions{Scope: `pr"oj`, Assigned: true, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Key != "PROJ-42" || list[0].Status != "To Do" || list[0].URL != srv.URL+"/browse/PROJ-42" {
		t.Errorf("list = %+v", list)
	}
	q := f.queries[0]
	for _, want := range []string{"statusCategory+%21%3D+Done", "project+%3D+%22PR%5C%22OJ%22", "assignee+%3D+currentUser%28%29", "maxResults=5", "ORDER+BY+updated+DESC"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %s lacks %s", q, want)
		}
	}
	if _, err := j.List(context.Background(), ListOptions{}); err != nil {
		t.Fatal(err)
	}
	if q := f.queries[1]; strings.Contains(q, "project") || strings.Contains(q, "assignee") || !strings.Contains(q, "maxResults=30") {
		t.Errorf("unscoped query %s", q)
	}
}

func TestJiraCommentStatusLink(t *testing.T) {
	f, srv := newFakeJira(t)
	j := NewJira(srv.URL, f.email, f.tok)
	ctx := context.Background()
	if err := j.Comment(ctx, "PROJ-42", "PR opened"); err != nil {
		t.Fatal(err)
	}
	if len(f.comments) != 1 || f.comments[0]["body"] != "PR opened" {
		t.Errorf("comments %+v", f.comments)
	}
	if err := j.Comment(ctx, "PROJ-42", ""); err == nil {
		t.Error("empty comment sent")
	}
	// By the status it leads to, then by the transition's own name.
	if got, err := j.SetStatus(ctx, "PROJ-42", "in review"); err != nil || got != "In Review" {
		t.Errorf("SetStatus = %q, %v", got, err)
	}
	if got, err := j.SetStatus(ctx, "PROJ-42", "close"); err != nil || got != "Done" {
		t.Errorf("SetStatus by transition = %q, %v", got, err)
	}
	if strings.Join(f.transitions, ",") != "21,31" {
		t.Errorf("transitions %v", f.transitions)
	}
	if _, err := j.SetStatus(ctx, "PROJ-42", "Shipped"); err == nil || !strings.Contains(err.Error(), "In Progress, In Review, Done") {
		t.Errorf("unknown status: %v", err)
	}
	if _, err := j.SetStatus(ctx, "NEW-1", "Done"); err == nil || !strings.Contains(err.Error(), "can't be moved") {
		t.Errorf("no transitions: %v", err)
	}
	if _, err := j.SetStatus(ctx, "PROJ-404", "Done"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if err := j.Link(ctx, "PROJ-42", "https://github.com/a/b/pull/9", ""); err != nil {
		t.Fatal(err)
	}
	obj := f.links[0]["object"].(map[string]any)
	if f.links[0]["globalId"] != "https://github.com/a/b/pull/9" || obj["title"] != "https://github.com/a/b/pull/9" {
		t.Errorf("link %+v", f.links[0])
	}
	if err := j.Link(ctx, "PROJ-42", " ", "x"); err == nil {
		t.Error("empty link sent")
	}
}
