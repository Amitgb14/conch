package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// fakeDevin is Devin's v3 API, as far as conch uses it.
type fakeDevin struct {
	t  *testing.T
	mu sync.Mutex

	org      string // what /v3/self names
	sessions map[string]*devinSession
	statuses map[string][]string // queued statuses, one per look
	created  []map[string]any
	messages map[string][]string
	calls    []string
	queries  []string
	fail     map[string]int // a call's key → the status it answers with
	failBody string
	pages    []string // pre-baked answers for GET …/sessions, in order
	nextID   int
	comesUp  []string // statuses a new session walks through; nil is claimed, running
}

func newFakeDevin(t *testing.T) (*fakeDevin, *Devin) {
	t.Helper()
	f := &fakeDevin{t: t, org: "org-test", sessions: map[string]*devinSession{}, statuses: map[string][]string{},
		messages: map[string][]string{}, fail: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("DEVIN_API_KEY", "cog_secret")
	t.Setenv("DEVIN_API_URL", srv.URL)
	t.Setenv("DEVIN_ORG_ID", "")
	old := pollEvery
	pollEvery = time.Millisecond
	t.Cleanup(func() { pollEvery = old })
	return f, NewDevin(config.ProviderCfg{})
}

func (f *fakeDevin) with(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// add puts a session in place that walks through statuses as it is looked at.
func (f *fakeDevin) add(id string, archived bool, statuses ...string) {
	f.with(func() {
		f.sessions[id] = &devinSession{ID: id, Title: "box " + id, Status: statuses[0], Archived: archived,
			Tags: []string{Label}, CreatedAt: 1790000000}
		f.statuses[id] = statuses[1:]
	})
}

func (f *fakeDevin) called() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, "\n")
}

func (f *fakeDevin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer cog_secret" {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"title":"Unauthorized","status":401,"detail":"bad key"}`)
		return
	}
	path := r.URL.Path
	key := r.Method + " " + path
	f.calls = append(f.calls, key)
	f.queries = append(f.queries, r.URL.RawQuery)
	if status := f.fail[key]; status != 0 {
		delete(f.fail, key)
		w.WriteHeader(status)
		fmt.Fprint(w, f.failBody)
		return
	}
	if path == "/v3/self" {
		fmt.Fprintf(w, `{"principal_type":"service_user","service_user_id":"su","service_user_name":"conch","org_id":%q}`, f.org)
		return
	}
	prefix := "/v3/organizations/org-test/sessions"
	if !strings.HasPrefix(path, prefix) {
		f.t.Errorf("unexpected %s", key)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(path, prefix), "/")
	id, action, _ := strings.Cut(rest, "/")
	switch {
	case id == "" && r.Method == http.MethodGet:
		if len(f.pages) > 0 {
			page := f.pages[0]
			f.pages = f.pages[1:]
			fmt.Fprint(w, page)
			return
		}
		var items []devinSession
		for _, s := range f.sessions {
			items = append(items, *s)
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items, "has_next_page": false})
	case id == "" && r.Method == http.MethodPost:
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.created = append(f.created, body)
		f.nextID++
		id := fmt.Sprintf("devin-%032d", f.nextID)
		s := &devinSession{ID: id, Status: "new", CreatedAt: 1790000000}
		if title, _ := body["title"].(string); title != "" {
			s.Title = title
		}
		for _, tag := range body["tags"].([]any) {
			s.Tags = append(s.Tags, tag.(string))
		}
		f.sessions[id] = s
		f.statuses[id] = []string{"claimed", "running"}
		if f.comesUp != nil {
			f.statuses[id] = f.comesUp
		}
		json.NewEncoder(w).Encode(s)
	default:
		s := f.sessions[id]
		if s == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"title":"Not Found","status":404,"detail":"Session not found"}`)
			return
		}
		switch {
		case action == "" && r.Method == http.MethodGet:
			if q := f.statuses[id]; len(q) > 0 {
				s.Status, f.statuses[id] = q[0], q[1:]
			}
		case action == "" && r.Method == http.MethodDelete:
			s.Status = "exit"
		case action == "archive":
			s.Archived = true
			f.statuses[id] = []string{"running", "suspended"}
		case action == "unarchive":
			s.Archived = false
		case action == "messages":
			var body struct {
				Message string `json:"message"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			f.messages[id] = append(f.messages[id], body.Message)
			if s.Status == "suspended" {
				f.statuses[id] = []string{"resuming", "running"}
			}
		default:
			f.t.Errorf("unexpected %s", key)
		}
		json.NewEncoder(w).Encode(s)
	}
}

func TestDevinNeedsAKey(t *testing.T) {
	f, _ := newFakeDevin(t)
	t.Setenv("DEVIN_API_KEY", "")
	d := NewDevin(config.ProviderCfg{})
	err := d.Check()
	if !errors.Is(err, ErrNotConfigured) || !strings.Contains(err.Error(), "$DEVIN_API_KEY") {
		t.Fatalf("check: %v", err)
	}
	if _, err := d.List(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("list: %v", err)
	}
	if f.called() != "" {
		t.Fatalf("asked Devin with no key:\n%s", f.called())
	}
	// A key of its own variable, or kept in the settings, is used.
	t.Setenv("MY_DEVIN", "cog_secret")
	if err := NewDevin(config.ProviderCfg{APIKeyEnv: "MY_DEVIN"}).Check(); err != nil {
		t.Fatalf("own variable: %v", err)
	}
	d = NewDevin(config.ProviderCfg{APIKey: " cog_secret "})
	if _, err := d.List(context.Background()); err != nil {
		t.Fatalf("kept key: %v", err)
	}
	if d.Name() != "devin" || ProviderLabel("devin") != "Devin Cloud" || !Known("devin") {
		t.Fatalf("names: %s %s", d.Name(), ProviderLabel("devin"))
	}
}

func TestDevinCreate(t *testing.T) {
	f, d := newFakeDevin(t)
	ctx := context.Background()
	s, err := d.Create(ctx, Spec{Name: "fix-login", Labels: map[string]string{"team": "web", "conch": "dup"},
		Env: map[string]string{"ZED": "z", "ALPHA": "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.State != StateStarted || !strings.HasPrefix(s.ID, "devin-") || s.Name != "fix-login" {
		t.Fatalf("created %+v", s)
	}
	body := f.created[0]
	if body["prompt"] != devinPrompt || body["title"] != "fix-login" {
		t.Fatalf("body %v", body)
	}
	if tags := fmt.Sprint(body["tags"]); tags != "[conch team=web]" {
		t.Fatalf("tags %s", tags)
	}
	if _, ok := body["platform"]; ok {
		t.Fatalf("no platform was asked for: %v", body)
	}
	if secrets := fmt.Sprint(body["session_secrets"]); secrets != "[map[key:ALPHA sensitive:true value:a] map[key:ZED sensitive:true value:z]]" {
		t.Fatalf("secrets %s", secrets)
	}
	// The organization is asked for once, then remembered.
	d.Create(ctx, Spec{})
	if n := strings.Count(f.called(), "GET /v3/self"); n != 1 {
		t.Fatalf("asked for the organization %d times:\n%s", n, f.called())
	}
	if body := f.created[1]; body["title"] != "conch sandbox" || fmt.Sprint(body["tags"]) != "[conch]" || body["session_secrets"] != nil {
		t.Fatalf("plain body %v", body)
	}

	// A platform comes from the settings, and the spec overrides it.
	d = NewDevin(config.ProviderCfg{Snapshot: " windows "})
	d.Create(ctx, Spec{})
	d.Create(ctx, Spec{Snapshot: "my-pool"})
	if f.created[2]["platform"] != "windows" || f.created[3]["platform"] != "my-pool" {
		t.Fatalf("platforms %v %v", f.created[2]["platform"], f.created[3]["platform"])
	}

	// Numbers are refused before anything is made.
	before := f.called()
	if _, err := d.Create(ctx, Spec{CPU: 4}); err == nil || !strings.Contains(err.Error(), "Devin chooses") {
		t.Fatalf("numbers: %v", err)
	}
	if f.called() != before {
		t.Fatal("asked Devin for a size it chooses itself")
	}
}

func TestDevinCreateGoesWrong(t *testing.T) {
	f, d := newFakeDevin(t)
	ctx := context.Background()
	f.with(func() {
		f.fail["POST /v3/organizations/org-test/sessions"] = http.StatusUnprocessableEntity
		f.failBody = `{"title":"Unprocessable Content","status":422,"detail":"Unknown platform 'beos'. Available: linux, windows"}`
	})
	if _, err := d.Create(ctx, Spec{Snapshot: "beos"}); err == nil || !strings.Contains(err.Error(), "Unknown platform 'beos'") ||
		!strings.Contains(err.Error(), "(422)") {
		t.Fatalf("refused: %v", err)
	}
	// A session that errors while it comes up says so, with its ID, so the
	// caller can take it down.
	f.with(func() { f.comesUp = []string{"claimed", "error"} })
	s, err := d.Create(ctx, Spec{})
	var failed *FailedError
	if !errors.As(err, &failed) || s.ID == "" || failed.State != StateError {
		t.Fatalf("error state: %+v %v", s, err)
	}
	f.with(func() { f.comesUp = nil })
	// A context that ends while it waits says what it was waiting on.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.wait(cctx, Sandbox{ID: "x", State: StateStarting}, StateStarted); !errors.Is(err, context.Canceled) ||
		!strings.Contains(err.Error(), "still starting") {
		t.Fatalf("cancelled: %v", err)
	}
	// No organization at all is said plainly.
	f.with(func() { f.org = "" })
	if _, err := NewDevin(config.ProviderCfg{}).List(ctx); err == nil || !strings.Contains(err.Error(), "$DEVIN_ORG_ID") {
		t.Fatalf("no org: %v", err)
	}
}

// DEVIN_ORG_ID names the organization, so the key is never asked whose it
// is; /v3/self prefers the organization a key's sessions run in.
func TestDevinOrganization(t *testing.T) {
	f, _ := newFakeDevin(t)
	t.Setenv("DEVIN_ORG_ID", "org-test")
	d := NewDevin(config.ProviderCfg{})
	if _, err := d.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.called(), "/v3/self") {
		t.Fatalf("asked anyway:\n%s", f.called())
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"principal_type":"pat_user","org_id":"org-primary","devin_sessions_org_id":"org-sessions"}`)
	}))
	defer srv.Close()
	t.Setenv("DEVIN_ORG_ID", "")
	d = NewDevin(config.ProviderCfg{APIURL: srv.URL})
	if org, err := d.orgID(context.Background()); err != nil || org != "org-sessions" {
		t.Fatalf("org %q %v", org, err)
	}
}

func TestDevinListKeepsToConchs(t *testing.T) {
	f, d := newFakeDevin(t)
	ctx := context.Background()
	f.add("devin-a", false, "running")
	f.add("devin-b", true, "suspended")
	f.add("devin-gone", false, "exit")
	f.with(func() {
		f.sessions["devin-theirs"] = &devinSession{ID: "devin-theirs", Status: "running", Tags: []string{"other"}}
	})
	list, err := d.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]State{}
	for _, s := range list {
		got[s.ID] = s.State
	}
	if len(got) != 2 || got["devin-a"] != StateStarted || got["devin-b"] != StateStopped {
		t.Fatalf("listed %v", got)
	}
	if q := f.queries[len(f.queries)-1]; !strings.Contains(q, "tags=conch") {
		t.Fatalf("query %q", q)
	}

	// Pages are followed by their cursor, and a cursor that repeats ends it.
	f.with(func() {
		f.pages = []string{
			`{"items":[{"session_id":"devin-1","status":"running","tags":["conch"]}],"has_next_page":true,"end_cursor":"c1"}`,
			`{"items":[{"session_id":"devin-2","status":"new","tags":["conch"]}],"has_next_page":true,"end_cursor":"c1"}`,
		}
	})
	list, err = d.List(ctx)
	if err != nil || len(list) != 2 || list[1].State != StateStarting {
		t.Fatalf("pages %+v %v", list, err)
	}
	if q := f.queries[len(f.queries)-1]; !strings.Contains(q, "after=c1") {
		t.Fatalf("second page query %q", q)
	}
	// A cursor that never ends is cut off.
	var pages []string
	for i := 0; i <= maxPages; i++ {
		pages = append(pages, fmt.Sprintf(`{"items":[],"has_next_page":true,"end_cursor":"c%d"}`, i))
	}
	f.with(func() { f.pages = pages })
	if _, err := d.List(ctx); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("endless: %v", err)
	}
	f.with(func() { f.pages = nil })
	// A reply that isn't JSON is an error, not an empty list.
	f.with(func() { f.pages = []string{"<html>"} })
	if _, err := d.List(ctx); err == nil {
		t.Fatal("garbage was read as a list")
	}
}

func TestDevinStates(t *testing.T) {
	for status, want := range map[string]State{
		"new": StateStarting, "claimed": StateStarting, "resuming": StateStarting, "running": StateStarted,
		"suspended": StateStopped, "exit": StateDestroyed, "error": StateError, "later": "later",
	} {
		if got := (devinSession{ID: "x", Status: status}).sandbox().State; got != want {
			t.Errorf("%s: %s, want %s", status, got, want)
		}
	}
	s := devinSession{ID: "devin-x", Status: "suspended", StatusDetail: "out_of_credits", Tags: []string{"conch"}}.sandbox()
	if s.Reason != "out_of_credits" || s.Name != "devin-x" || !s.Created.IsZero() || len(s.Labels) != 1 {
		t.Fatalf("%+v", s)
	}
	// A running session's detail is what Devin is doing, not a reason.
	if r := (devinSession{Status: "running", StatusDetail: "working"}).sandbox().Reason; r != "" {
		t.Fatalf("reason %q", r)
	}
}

func TestDevinStartAndStop(t *testing.T) {
	f, d := newFakeDevin(t)
	ctx := context.Background()

	// Running already: nothing is sent, so Devin is given nothing to do.
	f.add("devin-up", false, "running")
	if s, err := d.Start(ctx, "devin-up"); err != nil || s.State != StateStarted {
		t.Fatalf("running: %+v %v", s, err)
	}
	if len(f.messages["devin-up"]) != 0 {
		t.Fatal("woke a session that was awake")
	}

	// Asleep from inactivity: a message wakes it.
	f.add("devin-zz", false, "suspended")
	f.with(func() { f.sessions["devin-zz"].StatusDetail = "inactivity" })
	if s, err := d.Start(ctx, "devin-zz"); err != nil || s.State != StateStarted {
		t.Fatalf("asleep: %+v %v", s, err)
	}
	if got := f.messages["devin-zz"]; len(got) != 1 || got[0] != devinWake {
		t.Fatalf("messages %q", got)
	}

	// Stop archives it and waits for it to sleep; a second stop asks nothing.
	if err := d.Stop(ctx, "devin-zz"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.called(), "POST /v3/organizations/org-test/sessions/devin-zz/archive") {
		t.Fatalf("not archived:\n%s", f.called())
	}
	before := f.called()
	if err := d.Stop(ctx, "devin-zz"); err != nil || strings.Contains(strings.TrimPrefix(f.called(), before), "archive") {
		t.Fatalf("second stop: %v", err)
	}
	// Archived: started again, it is unarchived and woken.
	if s, err := d.Start(ctx, "devin-zz"); err != nil || s.State != StateStarted {
		t.Fatalf("archived: %+v %v", s, err)
	}
	after := strings.TrimPrefix(f.called(), before)
	if !strings.Contains(after, "devin-zz/unarchive") || !strings.Contains(after, "devin-zz/messages") {
		t.Fatalf("archived start:\n%s", after)
	}

	// Ended: nothing starts it, and stopping it is nothing to do.
	f.add("devin-end", false, "exit")
	if _, err := d.Start(ctx, "devin-end"); err == nil || !strings.Contains(err.Error(), "has ended") {
		t.Fatalf("ended start: %v", err)
	}
	if err := d.Stop(ctx, "devin-end"); err != nil {
		t.Fatalf("ended stop: %v", err)
	}
	// Asleep for want of credit: waiting won't mend it, so it says why.
	f.add("devin-broke", false, "suspended")
	f.with(func() {
		f.sessions["devin-broke"].StatusDetail = "out_of_credits"
		f.statuses["devin-broke"] = []string{"suspended"}
	})
	var failed *FailedError
	if _, err := d.Start(ctx, "devin-broke"); !errors.As(err, &failed) || failed.Reason != "out_of_credits" {
		t.Fatalf("out of credits: %v", err)
	}
	// One that isn't there is ErrNotFound, for start and stop alike.
	if _, err := d.Start(ctx, "devin-none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing start: %v", err)
	}
	if err := d.Stop(ctx, "devin-none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing stop: %v", err)
	}
	if _, err := d.Get(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no id: %v", err)
	}
	// A refused wake is passed on.
	f.add("devin-no", false, "suspended")
	f.with(func() {
		f.fail["POST /v3/organizations/org-test/sessions/devin-no/messages"] = http.StatusForbidden
		f.failBody = `{"title":"Forbidden","status":403,"detail":"not allowed"}`
	})
	if _, err := d.Start(ctx, "devin-no"); err == nil || !strings.Contains(err.Error(), "wake session") ||
		!strings.Contains(err.Error(), "permission to manage sessions") {
		t.Fatalf("refused wake: %v", err)
	}
}

func TestDevinDelete(t *testing.T) {
	f, d := newFakeDevin(t)
	ctx := context.Background()
	f.add("devin-a", false, "running")
	if err := d.Delete(ctx, "devin-a"); err != nil {
		t.Fatal(err)
	}
	if s, _ := d.Get(ctx, "devin-a"); !s.State.Going() {
		t.Fatalf("after delete: %+v", s)
	}
	if err := d.Delete(ctx, "devin-none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := d.Delete(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no id: %v", err)
	}
	// Ending one that has already ended may be refused; it is gone anyway.
	f.with(func() {
		f.fail["DELETE /v3/organizations/org-test/sessions/devin-a"] = http.StatusConflict
		f.failBody = `{"title":"Conflict","status":409,"detail":"already terminated"}`
	})
	if err := d.Delete(ctx, "devin-a"); err != nil {
		t.Fatalf("already ended: %v", err)
	}
	// One that is still running and refused says so.
	f.add("devin-b", false, "running")
	f.with(func() {
		f.fail["DELETE /v3/organizations/org-test/sessions/devin-b"] = http.StatusInternalServerError
		f.failBody = "upstream broke"
	})
	if err := d.Delete(ctx, "devin-b"); err == nil || !strings.Contains(err.Error(), "upstream broke") {
		t.Fatalf("refused: %v", err)
	}
}

func TestDevinErrors(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		want   []string
	}{
		{401, `{"title":"Unauthorized","status":401,"detail":"bad key"}`, []string{"devin: bad key (401)", "$DEVIN_API_KEY"}},
		{403, `{"title":"Forbidden","status":403}`, []string{"devin: Forbidden (403)", "permission"}},
		{429, `{"detail":"slow down"}`, []string{"slow down (429)", "too often"}},
		{422, `{"title":"Unprocessable","detail":[{"loc":["body","prompt"],"msg":"required"}]}`, []string{`"msg":"required"`}},
		{500, ``, []string{"devin: Internal Server Error (500)"}},
		{502, `plain text`, []string{"devin: plain text (502)"}},
	} {
		err := (&DevinError{Status: c.status, Message: devinMessage([]byte(c.body)), keyEnv: "DEVIN_API_KEY"}).Error()
		for _, w := range c.want {
			if !strings.Contains(err, w) {
				t.Errorf("%d %s: %q lacks %q", c.status, c.body, err, w)
			}
		}
	}
	if !errors.Is(&DevinError{Status: 404}, ErrNotFound) || errors.Is(&DevinError{Status: 400}, ErrNotFound) {
		t.Fatal("only a 404 is not found")
	}

	// An overlong reply is refused rather than read without end.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"org_id":"`+strings.Repeat("x", maxBody)+`"}`)
	}))
	defer srv.Close()
	t.Setenv("DEVIN_API_KEY", "cog_secret")
	t.Setenv("DEVIN_ORG_ID", "")
	d := NewDevin(config.ProviderCfg{APIURL: srv.URL})
	if _, err := d.List(context.Background()); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("overlong: %v", err)
	}
	// Nobody answering is an error too.
	d = NewDevin(config.ProviderCfg{APIURL: "http://127.0.0.1:1"})
	if _, err := d.List(context.Background()); err == nil {
		t.Fatal("no server, no error")
	}
}

// The way in is the devin CLI: from $CONCH_DEVIN, PATH, or where its
// installer puts it; and none says how to get it.
func TestDevinSSHCommand(t *testing.T) {
	f, d := newFakeDevin(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CONCH_DEVIN", "")

	if _, err := d.SSHCommand("devin-a"); err == nil || !strings.Contains(err.Error(), "devin auth login") {
		t.Fatalf("no CLI: %v", err)
	}
	// A directory, or a file that won't run, where the installer puts it is not the CLI.
	local := filepath.Join(home, ".local", "bin")
	os.MkdirAll(filepath.Join(local, "devin"), 0o755)
	if _, err := d.SSHCommand("devin-a"); err == nil {
		t.Fatal("took a directory for the CLI")
	}
	os.Remove(filepath.Join(local, "devin"))
	os.WriteFile(filepath.Join(local, "devin"), []byte("#!/bin/sh\n"), 0o644)
	if _, err := d.SSHCommand("devin-a"); err == nil {
		t.Fatal("took a file that won't run for the CLI")
	}
	os.Chmod(filepath.Join(local, "devin"), 0o755)
	if argv, err := d.SSHCommand("devin-a"); err != nil || strings.Join(argv, " ") != filepath.Join(local, "devin")+" ssh devin-a" {
		t.Fatalf("installer's: %q %v", argv, err)
	}
	onPath := t.TempDir()
	os.WriteFile(filepath.Join(onPath, "devin"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", onPath)
	if argv, _ := d.SSHCommand("devin-a"); argv[0] != filepath.Join(onPath, "devin") {
		t.Fatalf("PATH's: %q", argv)
	}
	t.Setenv("CONCH_DEVIN", "/opt/devin")
	if argv, _ := d.SSHCommand("devin-a"); argv[0] != "/opt/devin" {
		t.Fatalf("$CONCH_DEVIN's: %q", argv)
	}
	if _, err := d.SSHCommand(""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no id: %v", err)
	}

	// SSHAccess names the gateway, for anyone using plain ssh.
	f.add("devin-a", false, "running")
	a, err := d.SSHAccess(context.Background(), "devin-a")
	if err != nil || a.Target() != "devin-a@ssh.devin.ai" || !a.PlainUser {
		t.Fatalf("access %+v %v", a, err)
	}
	f.add("abc", false, "running")
	if a, _ := d.SSHAccess(context.Background(), "abc"); a.User != "devin-abc" {
		t.Fatalf("bare id: %+v", a)
	}
	if _, err := d.SSHAccess(context.Background(), "devin-none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	// The dialog's words come from the provider.
	var _ CommandAccess = d
	var _ Placed = d
	var _ Noted = d
	if !strings.Contains(d.PlaceHint(), "outpost") || !strings.Contains(d.Note(), "devin auth login") {
		t.Fatalf("hint %q note %q", d.PlaceHint(), d.Note())
	}
}

func TestDevinTags(t *testing.T) {
	if got := fmt.Sprint(devinTags(nil)); got != "[conch]" {
		t.Fatalf("none: %s", got)
	}
	if got := fmt.Sprint(devinTags(map[string]string{"b": "", "a": "1", Label: "x"})); got != "[conch a=1 b]" {
		t.Fatalf("some: %s", got)
	}
}
