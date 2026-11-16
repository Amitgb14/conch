package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/conch/internal/tracker"
)

// fakeShortcut answers as Shortcut's REST API v3 documents
// (developer.shortcut.com/api/rest/v3: List Workflows, Search Stories —
// {data, next, total} — Get Story with its comments, Create Story
// Comment, Update Story), with the token in Shortcut-Token.
func fakeShortcut(t *testing.T) (*File, *[]string) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Shortcut-Token") != testKey {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"message": "Unauthorized"}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery+" "+string(body))
		mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v3/workflows":
			io.WriteString(w, `[{"id": 500000001, "name": "Engineering", "states": [
			  {"id": 500000005, "name": "Ready for Development", "type": "unstarted"},
			  {"id": 500000006, "name": "In Development", "type": "started"},
			  {"id": 500000007, "name": "In Review", "type": "started"},
			  {"id": 500000008, "name": "Done", "type": "done"}]}]`)
		case "GET /api/v3/search/stories":
			io.WriteString(w, `{"data": [
			  {"id": 123, "name": "Retry uploads", "description": "They fail on flaky wifi.", "app_url": "https://app.shortcut.com/acme/story/123", "workflow_state_id": 500000006},
			  {"id": 124, "name": "Dark mode", "description": "", "app_url": "https://app.shortcut.com/acme/story/124", "workflow_state_id": 500000005}],
			  "next": null, "total": 2}`)
		case "GET /api/v3/stories/123":
			io.WriteString(w, `{"id": 123, "name": "Retry uploads", "description": "They fail on flaky wifi.", "app_url": "https://app.shortcut.com/acme/story/123",
			  "workflow_state_id": 500000007, "comments": [
			  {"id": 1, "text": "Seen on 3G too.", "author_id": "5f1e", "deleted": false},
			  {"id": 2, "text": "", "author_id": "5f1e", "deleted": true}]}`)
		case "POST /api/v3/stories/123/comments":
			io.WriteString(w, `{"id": 3, "text": "x"}`)
		case "PUT /api/v3/stories/123":
			io.WriteString(w, `{"id": 123, "workflow_state_id": 500000007}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message": "Resource not found."}`)
		}
	}))
	t.Cleanup(srv.Close)
	f := shipped(t, "shortcut")
	f.API = srv.URL + "/api/v3"
	return f, &seen
}

func TestShortcutTickets(t *testing.T) {
	f, seen := fakeShortcut(t)
	c, err := f.Connect(testKey, "", "")
	if err != nil {
		t.Fatal(err)
	}
	tr := c.Tracker(Target{})
	ctx := context.Background()
	if tr.Name() != "shortcut" {
		t.Fatalf("name %q", tr.Name())
	}
	list, err := tr.List(ctx, tracker.ListOptions{})
	if err != nil || len(list) != 2 {
		t.Fatalf("list %+v %v", list, err)
	}
	if got := list[0]; got.Tracker != "shortcut" || got.Key != "123" || got.Title != "Retry uploads" ||
		got.Status != "In Development" || got.URL != "https://app.shortcut.com/acme/story/123" || got.Body != "They fail on flaky wifi." {
		t.Fatalf("first %+v", got)
	}
	if list[1].Status != "Ready for Development" {
		t.Fatalf("a state's name from the workflows: %+v", list[1])
	}
	if q := strings.Join(*seen, "\n"); !strings.Contains(q, "query=%21is%3Adone+%21is%3Aarchived") {
		t.Fatalf("open stories not asked for: %s", q)
	}
	// One is asked for: one is given.
	if one, err := tr.List(ctx, tracker.ListOptions{Limit: 1}); err != nil || len(one) != 1 {
		t.Fatalf("limit 1: %+v %v", one, err)
	}
	if _, err := tr.List(ctx, tracker.ListOptions{Assigned: true}); err == nil || !strings.Contains(err.Error(), "assigned to you") {
		t.Fatalf("no mine: %v", err)
	}

	got, err := tr.Get(ctx, "123")
	if err != nil || got.Key != "123" || got.Status != "In Review" || len(got.Comments) != 1 || got.Comments[0].Body != "Seen on 3G too." {
		t.Fatalf("get %+v %v", got, err)
	}
	if _, err := tr.Get(ctx, "999"); !errors.Is(err, tracker.ErrNotFound) {
		t.Fatalf("no such story: %v", err)
	}
	if _, err := tr.Get(ctx, " "); err == nil {
		t.Fatal("no key")
	}

	*seen = nil
	if err := tr.Comment(ctx, "123", "On it: conch/123-retry-uploads"); err != nil {
		t.Fatal(err)
	}
	if err := tr.Link(ctx, "123", "https://github.com/acme/app/pull/7", ""); err != nil {
		t.Fatal(err)
	}
	var bodies []map[string]any
	for _, s := range *seen {
		if strings.HasPrefix(s, "POST ") {
			var m map[string]any
			json.Unmarshal([]byte(s[strings.Index(s, "{"):]), &m)
			bodies = append(bodies, m)
		}
	}
	if len(bodies) != 2 || bodies[0]["text"] != "On it: conch/123-retry-uploads" ||
		bodies[1]["text"] != "[https://github.com/acme/app/pull/7](https://github.com/acme/app/pull/7)" {
		t.Fatalf("comment and link: %v", bodies)
	}

	// A status as somebody types it: matched to the workflow's whatever
	// its case, and sent as the state's id, a number.
	*seen = nil
	st, err := tr.SetStatus(ctx, "123", "in review")
	if err != nil || st != "In Review" {
		t.Fatalf("move: %q %v", st, err)
	}
	if put := (*seen)[1]; !strings.Contains(put, `"workflow_state_id":500000007`) {
		t.Fatalf("move sent %s", put)
	}
	if _, err := tr.SetStatus(ctx, "123", "Shipped to the moon"); err == nil || !errors.Is(err, ErrNothing) {
		t.Fatalf("a state Shortcut has not got: %v", err)
	}

	// The wrong key is said, and is not repeated back.
	bad, _ := f.Connect("wrong-key", "", "")
	if _, err := bad.Tracker(Target{}).List(ctx, tracker.ListOptions{}); err == nil || !strings.Contains(err.Error(), "$SHORTCUT_API_TOKEN") {
		t.Fatalf("refused key: %v", err)
	}
}

// fakeGitLabIssues answers as GitLab's REST API documents
// (docs.gitlab.com/api/issues and /api/notes: list project issues with
// state, scope and per_page, single issue, issue notes, new note, edit
// issue with state_event), for a project in nested groups.
func fakeGitLabIssues(t *testing.T) (*File, *[]string) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != testKey {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"message": "401 Unauthorized"}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery+" "+string(body))
		mu.Unlock()
		const p = "/api/v4/projects/grp%2Fsub%2Fwidgets"
		switch r.Method + " " + r.URL.EscapedPath() {
		case "GET " + p + "/issues":
			if r.URL.Query().Get("scope") == "assigned_to_me" {
				io.WriteString(w, `[{"id": 9012, "iid": 12, "title": "Crash on start", "state": "opened", "web_url": "https://gitlab.com/grp/sub/widgets/-/issues/12"}]`)
				return
			}
			io.WriteString(w, `[{"id": 9012, "iid": 12, "title": "Crash on start", "description": "Since 2.1.", "state": "opened", "web_url": "https://gitlab.com/grp/sub/widgets/-/issues/12", "labels": ["bug"]},
			  {"id": 9013, "iid": 13, "title": "Docs typo", "description": null, "state": "opened", "web_url": "https://gitlab.com/grp/sub/widgets/-/issues/13"}]`)
		case "GET " + p + "/issues/12":
			io.WriteString(w, `{"id": 9012, "iid": 12, "title": "Crash on start", "description": "Since 2.1.", "state": "opened", "web_url": "https://gitlab.com/grp/sub/widgets/-/issues/12"}`)
		case "GET " + p + "/issues/12/notes":
			io.WriteString(w, `[{"id": 1, "body": "added ~bug label", "system": true, "author": {"username": "ada"}},
			  {"id": 2, "body": "Repro: open twice.", "system": false, "author": {"username": "ada"}}]`)
		case "GET " + p + "/issues/13/notes":
			io.WriteString(w, `[]`)
		case "GET " + p + "/issues/13":
			io.WriteString(w, `{"id": 9013, "iid": 13, "title": "Docs typo", "description": null, "state": "closed", "web_url": "https://gitlab.com/grp/sub/widgets/-/issues/13"}`)
		case "POST " + p + "/issues/12/notes":
			io.WriteString(w, `{"id": 3, "body": "x"}`)
		case "PUT " + p + "/issues/13":
			io.WriteString(w, `{"id": 9013, "iid": 13, "state": "closed"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message": "404 Not found"}`)
		}
	}))
	t.Cleanup(srv.Close)
	f := shipped(t, "gitlab-issues")
	f.API = srv.URL + "/api/v4"
	return f, &seen
}

func TestGitLabIssues(t *testing.T) {
	f, seen := fakeGitLabIssues(t)
	c, err := f.Connect(testKey, "", "")
	if err != nil {
		t.Fatal(err)
	}
	tr := c.Tracker(Target{Remote: Remote{Host: "gitlab.com", Path: "grp/sub/widgets"}})
	ctx := context.Background()
	list, err := tr.List(ctx, tracker.ListOptions{Limit: 20})
	if err != nil || len(list) != 2 || list[0].Key != "12" || list[0].Status != "opened" || list[1].Body != "" {
		t.Fatalf("list %+v %v", list, err)
	}
	if q := (*seen)[0]; !strings.Contains(q, "state=opened") || !strings.Contains(q, "per_page=20") {
		t.Fatalf("asked %s", q)
	}
	mine, err := tr.List(ctx, tracker.ListOptions{Assigned: true})
	if err != nil || len(mine) != 1 || mine[0].Key != "12" {
		t.Fatalf("mine %+v %v", mine, err)
	}
	got, err := tr.Get(ctx, "12")
	if err != nil || got.Title != "Crash on start" || got.Body != "Since 2.1." || len(got.Comments) != 1 ||
		got.Comments[0] != (tracker.Comment{Author: "ada", Body: "Repro: open twice."}) {
		t.Fatalf("get %+v %v", got, err)
	}
	// An issue nobody has commented on still reads.
	if got, err := tr.Get(ctx, "13"); err != nil || got.Key != "13" || len(got.Comments) != 0 {
		t.Fatalf("no notes: %+v %v", got, err)
	}
	if _, err := tr.Get(ctx, "404"); !errors.Is(err, tracker.ErrNotFound) {
		t.Fatalf("no such issue: %v", err)
	}
	if err := tr.Comment(ctx, "12", "Fixed on conch/12-crash"); err != nil {
		t.Fatal(err)
	}
	if st, err := tr.SetStatus(ctx, "13", "close"); err != nil || st != "closed" {
		t.Fatalf("move %q %v", st, err)
	}
	all := strings.Join(*seen, "\n")
	if !strings.Contains(all, `{"body":"Fixed on conch/12-crash"}`) || !strings.Contains(all, `{"state_event":"close"}`) {
		t.Fatalf("sent %s", all)
	}
	// A project with no remote has no {project}: said, not sent.
	if _, err := c.Tracker(Target{}).List(ctx, tracker.ListOptions{}); err == nil {
		t.Fatal("no project")
	}
}

// Every shipped tickets file reads into a tracker; Linear and Jira stay
// conch's own code.
func TestTicketsFilesAreTrackers(t *testing.T) {
	for _, name := range []string{"shortcut", "gitlab-issues"} {
		f := shipped(t, name)
		if f.Kind != KindTickets {
			t.Fatalf("%s is %s", name, f.Kind)
		}
		ops := strings.Join(f.Operations(), " ")
		for _, want := range []string{"list", "get", "comment", "link", "move"} {
			if !strings.Contains(ops, want) {
				t.Errorf("%s has no %s: %s", name, want, ops)
			}
		}
	}
}

const goodTickets = `format = 3
kind = "tickets"
name = "acme-tickets"
api = "https://api.acme.dev"
key_env = "ACME_TOKEN"

[list]
call = "GET /tickets?open=1&n={limit}&team={scope}"
items = "tickets"
key = "id"
title = "title"

[get]
key = "id"
title = "title"
status = "state"
status_names = "{states}"
comments = "comments"
comment_body = "text"

[[get.steps]]
as = "states"
call = "GET /states"

[[get.steps]]
call = "GET /tickets/{ticket}"

[move]
call = "PUT /tickets/{ticket}"
body = { state = "{status}" }
`

const goodNotify = `format = 3
kind = "notify"
name = "acme-chat"
api = "https://hooks.acme.dev"
key_env = "ACME_HOOK"

[auth]
type = "url"

[send]
call = "POST"
body = { text = "{title}: {body} {url}" }
headers = { X-Acme-Tag = "conch" }
`

func TestParseTicketsAndNotify(t *testing.T) {
	for name, src := range map[string]string{"tickets": goodTickets, "notify": goodNotify} {
		if _, problems, err := Parse([]byte(src)); err != nil || len(problems) > 0 {
			t.Fatalf("%s: %v %v", name, err, problems)
		}
	}
	for name, tc := range map[string]struct {
		src, from, to, want string
	}{
		"no list":            {goodTickets, "[list]\ncall = \"GET /tickets?open=1&n={limit}&team={scope}\"", "[list]", "[list]: required for kind tickets"},
		"list no items":      {goodTickets, `items = "tickets"`, ``, "[list] items"},
		"no title":           {goodTickets, "key = \"id\"\ntitle = \"title\"\n\n[get]", "key = \"id\"\n\n[get]", "[list] key and title"},
		"comments in list":   {goodTickets, `title = "title"` + "\n\n[get]", `title = "title"` + "\ncomments = \"c\"\ncomment_body = \"t\"\n\n[get]", "read by get"},
		"no comment body":    {goodTickets, `comment_body = "text"`, ``, "comment_body"},
		"status names path":  {goodTickets, `status_names = "{states}"`, `status_names = "states"`, "status_names"},
		"status names later": {goodTickets, `status_names = "{states}"`, `status_names = "{later}"`, "{later} is nothing known"},
		"ticket in list":     {goodTickets, `team={scope}`, `team={ticket}`, "{ticket} is nothing known"},
		"status not in get":  {goodTickets, `GET /tickets/{ticket}`, `GET /tickets/{status}`, "{status} is nothing known"},
		"no project":         {goodTickets, `team={scope}`, `team={project}`, "{project} is nothing known"},
		"states":             {goodTickets, "[list]", "[states]\nopen = \"pass\"\n[list]", "[states]: kind tickets has none"},
		"ci op":              {goodTickets, "[list]", "[rerun]\ncall = \"POST /x\"\n[list]", "[rerun]: not an operation of kind tickets"},
		"url auth tickets":   {goodTickets, "[list]", "[auth]\ntype = \"url\"\n[list]", "[auth] url is for kind notify"},
		"no send":            {goodNotify, "[send]\ncall = \"POST\"", "[send]", "[send]: required for kind notify"},
		"branch in notify":   {goodNotify, `{title}: {body}`, `{branch}: {body}`, "{branch} is nothing known"},
		"key in header":      {goodNotify, `X-Acme-Tag = "conch"`, `X-Acme-Tag = "{key}"`, "{key} in"},
		"auth header":        {goodNotify, `X-Acme-Tag = "conch"`, `Authorization = "x"`, `headers "Authorization"`},
		"header name":        {goodNotify, `X-Acme-Tag = "conch"`, `"X Acme" = "x"`, `headers "X Acme"`},
		"url call path":      {goodNotify, `call = "POST"`, `call = "POST https://evil.dev/x"`, "a path under api"},
		"url call word":      {goodNotify, `call = "POST"`, `call = "POST evil"`, "the method alone"},
		"no method":          {goodNotify, `call = "POST"`, `call = "SEND"`, "the method one of"},
		"body a list":        {goodNotify, `body = { text = "{title}: {body} {url}" }`, `body = ["x"]`, "a table, sent as JSON, or a string"},
	} {
		src := strings.Replace(tc.src, tc.from, tc.to, 1)
		if src == tc.src {
			t.Fatalf("%s: the replacement changed nothing", name)
		}
		_, problems, err := Parse([]byte(src))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
			t.Errorf("%s: problems %q, want %q", name, problems, tc.want)
		}
	}
	// A webhook's call may add to the address.
	for _, call := range []string{"POST /hooks", "POST ?wait=true"} {
		src := strings.Replace(goodNotify, `call = "POST"`, `call = "`+call+`"`, 1)
		if _, problems, _ := Parse([]byte(src)); len(problems) > 0 {
			t.Errorf("%s: %v", call, problems)
		}
	}
}

// What a tickets or notify file does in conch is said from its contract.
func TestDoesTicketsAndNotify(t *testing.T) {
	tk := Does(KindTickets, []string{"list", "get"})
	if len(tk) == 0 {
		t.Fatal("tickets: nothing")
	}
	levels := map[string]string{}
	for _, f := range tk {
		levels[f.What] = f.Level
	}
	var saw bool
	for what, lv := range levels {
		if strings.Contains(what, "Move") && lv != "no" {
			t.Errorf("move without [move]: %s", lv)
		}
		saw = saw || strings.Contains(what, "Tickets view") || strings.Contains(what, "tickets")
	}
	if !saw {
		t.Errorf("no ticket line: %v", levels)
	}
	if n := Does(KindNotify, []string{"send"}); len(n) == 0 || n[0].Level != "yes" {
		t.Fatalf("notify: %+v", n)
	}
	if Does(KindHost, nil) != nil {
		t.Fatal("host")
	}
}
